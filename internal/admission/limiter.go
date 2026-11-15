package admission

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrRejected means the limiter had no free slot and no usable wait queue.
	ErrRejected = errors.New("admission: request rejected")
	// ErrTooHeavy means the request's weight exceeds the limiter's total
	// capacity, so waiting cannot help. It is a bad-query error, not a
	// retryable capacity rejection.
	ErrTooHeavy = errors.New("admission: request weight exceeds capacity")
)

// Limiter bounds weighted in-flight work and the number waiting for budget.
// A zero queue or a zero wait turns excess requests away immediately.
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
	weight  int
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

// Acquire takes one unit of budget, waiting up to the configured wait. The
// returned release is idempotent and must be called once the work is done;
// defer it so a panic cannot leak budget.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	return l.AcquireWeight(ctx, 1)
}

// AcquireWeight takes weight units of budget. A request whose weight exceeds
// the total capacity is rejected immediately; it could never fit.
func (l *Limiter) AcquireWeight(ctx context.Context, weight int) (func(), error) {
	if weight < 1 {
		weight = 1
	}
	if weight > l.capacity {
		return nil, ErrTooHeavy
	}
	l.mu.Lock()
	if l.inflight+weight <= l.capacity {
		l.inflight += weight
		l.mu.Unlock()
		return l.releaser(weight), nil
	}
	if l.queueMax <= 0 || l.wait <= 0 || l.waiters.Len() >= l.queueMax {
		l.mu.Unlock()
		return nil, ErrRejected
	}
	pending := &waiter{ch: make(chan struct{}, 1), weight: weight}
	pending.element = l.waiters.PushBack(pending)
	l.mu.Unlock()

	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	select {
	case <-pending.ch:
		return l.releaser(weight), nil
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

func (l *Limiter) releaser(weight int) func() {
	var once sync.Once
	return func() {
		once.Do(func() { l.release(weight) })
	}
}

func (l *Limiter) release(weight int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight -= weight
	// Prefer one-unit waiters so cheap instant queries cannot be starved by a
	// backlog of range queries. Then admit any heavier waiter that now fits.
	for {
		pending := l.nextWaiterLocked()
		if pending == nil {
			return
		}
		l.waiters.Remove(pending.element)
		pending.element = nil
		pending.granted = true
		l.inflight += pending.weight
		pending.ch <- struct{}{}
	}
}

func (l *Limiter) nextWaiterLocked() *waiter {
	for element := l.waiters.Front(); element != nil; element = element.Next() {
		pending := element.Value.(*waiter)
		if pending.weight == 1 && l.inflight+1 <= l.capacity {
			return pending
		}
	}
	for element := l.waiters.Front(); element != nil; element = element.Next() {
		pending := element.Value.(*waiter)
		if l.inflight+pending.weight <= l.capacity {
			return pending
		}
	}
	return nil
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
		l.release(pending.weight)
	}
	return reason
}
