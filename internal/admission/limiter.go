package admission

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRejected means the limiter had no free slot and no usable wait queue.
var ErrRejected = errors.New("admission: request rejected")

// Limiter bounds the number of in-flight requests and the number waiting for a
// slot. A zero queue or a zero wait turns excess requests away immediately.
type Limiter struct {
	mu       sync.Mutex
	capacity int
	queueMax int
	wait     time.Duration
	inflight int
	waiters  *list.List
}

type waiter struct {
	ch      chan struct{}
	granted bool
	element *list.Element
}

// New returns a limiter with the given in-flight capacity, queue depth, and
// maximum queue wait.
func New(capacity, queue int, wait time.Duration) (*Limiter, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("admission: capacity must be at least 1, got %d", capacity)
	}
	if queue < 0 {
		return nil, fmt.Errorf("admission: queue must not be negative, got %d", queue)
	}
	if wait < 0 {
		return nil, fmt.Errorf("admission: wait must not be negative, got %s", wait)
	}
	return &Limiter{
		capacity: capacity,
		queueMax: queue,
		wait:     wait,
		waiters:  list.New(),
	}, nil
}

// Acquire takes a slot, waiting up to the configured wait. The returned
// release is idempotent and must be called once the work is done; defer it so
// a panic cannot leak a slot.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	l.mu.Lock()
	if l.inflight < l.capacity {
		l.inflight++
		l.mu.Unlock()
		return l.releaser(), nil
	}
	if l.queueMax <= 0 || l.wait <= 0 || l.waiters.Len() >= l.queueMax {
		l.mu.Unlock()
		return nil, ErrRejected
	}
	pending := &waiter{ch: make(chan struct{}, 1)}
	pending.element = l.waiters.PushBack(pending)
	l.mu.Unlock()

	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	select {
	case <-pending.ch:
		return l.releaser(), nil
	case <-timer.C:
		return nil, l.abandon(pending, ErrRejected)
	case <-ctx.Done():
		return nil, l.abandon(pending, ctx.Err())
	}
}

// InFlight returns the number of held slots.
func (l *Limiter) InFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight
}

// Queued returns the number of waiters.
func (l *Limiter) Queued() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waiters.Len()
}

func (l *Limiter) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(l.releaseSlot)
	}
}

func (l *Limiter) releaseSlot() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if front := l.waiters.Front(); front != nil {
		pending := front.Value.(*waiter)
		l.waiters.Remove(front)
		pending.element = nil
		pending.granted = true
		pending.ch <- struct{}{}
		return
	}
	l.inflight--
}

// abandon removes a waiter that timed out or was cancelled. If a release
// granted it a slot in the meantime, that slot is handed to the next waiter.
func (l *Limiter) abandon(pending *waiter, reason error) error {
	l.mu.Lock()
	wasGranted := pending.granted
	if pending.element != nil {
		l.waiters.Remove(pending.element)
		pending.element = nil
	}
	l.mu.Unlock()
	if wasGranted {
		l.releaseSlot()
	}
	return reason
}
