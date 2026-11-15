package ttlcache

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Clock returns the current time. Tests inject a fake clock.
type Clock func() time.Time

// Loader loads one value. A loader error is returned to every waiter and is
// never cached.
type Loader[K comparable, V any] func(context.Context, K) (V, error)

// Options configures a Cache.
type Options[K comparable, V any] struct {
	// TTL is how long a loaded value stays fresh. Zero disables caching.
	TTL time.Duration
	// MaxEntries bounds the live entries. Once reached, the oldest entry is
	// evicted before a new one is inserted.
	MaxEntries int
	// Clock defaults to time.Now.
	Clock Clock
	// Load is required.
	Load Loader[K, V]
	// LoadTimeout bounds a shared load and detaches it from the first caller's
	// cancellation. Zero uses 30s.
	LoadTimeout time.Duration
}

const defaultLoadTimeout = 30 * time.Second

// ErrLoaderPanicked is returned to waiters when the first caller's loader
// panics. The panic is re-raised to that caller after the call is cleaned up.
var ErrLoaderPanicked = errors.New("ttlcache: loader panicked")

// Cache is a bounded TTL cache with single-flight loading. It is safe for
// concurrent use.
type Cache[K comparable, V any] struct {
	mu          sync.Mutex
	ttl         time.Duration
	maxEntries  int
	loadTimeout time.Duration
	clock       Clock
	load        Loader[K, V]

	entries map[K]*list.Element
	order   *list.List
	calls   map[K]*call[V]
}

type item[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time
}

type call[V any] struct {
	done  chan struct{}
	value V
	err   error
}

// New validates the options and returns a cache.
func New[K comparable, V any](opts Options[K, V]) (*Cache[K, V], error) {
	if opts.Load == nil {
		return nil, errors.New("ttlcache: a loader is required")
	}
	if opts.TTL < 0 {
		return nil, fmt.Errorf("ttlcache: TTL must not be negative, got %s", opts.TTL)
	}
	if opts.MaxEntries <= 0 {
		return nil, fmt.Errorf("ttlcache: MaxEntries must be positive, got %d", opts.MaxEntries)
	}
	if opts.LoadTimeout < 0 {
		return nil, fmt.Errorf("ttlcache: LoadTimeout must not be negative, got %s", opts.LoadTimeout)
	}
	loadTimeout := opts.LoadTimeout
	if loadTimeout == 0 {
		loadTimeout = defaultLoadTimeout
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Cache[K, V]{
		ttl:         opts.TTL,
		maxEntries:  opts.MaxEntries,
		loadTimeout: loadTimeout,
		clock:       clock,
		load:        opts.Load,
		entries:     make(map[K]*list.Element),
		order:       list.New(),
		calls:       make(map[K]*call[V]),
	}, nil
}

// Get returns the cached value or loads it. Concurrent misses for one key
// share one loader call.
func (c *Cache[K, V]) Get(ctx context.Context, key K) (V, error) {
	var zero V
	if c.ttl <= 0 {
		return c.load(ctx, key)
	}
	now := c.clock()

	c.mu.Lock()
	if element, ok := c.entries[key]; ok {
		cached := element.Value.(*item[K, V])
		if now.Before(cached.expires) {
			value := cached.value
			c.mu.Unlock()
			return value, nil
		}
		c.removeElement(element)
	}
	if pending, ok := c.calls[key]; ok {
		c.mu.Unlock()
		select {
		case <-pending.done:
			return pending.value, pending.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	pending := &call[V]{done: make(chan struct{})}
	c.calls[key] = pending
	c.mu.Unlock()

	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.loadTimeout)
	defer cancel()
	var (
		value    V
		err      error
		panicked any
	)
	func() {
		defer func() { panicked = recover() }()
		value, err = c.load(loadCtx, key)
	}()

	c.mu.Lock()
	delete(c.calls, key)
	if panicked != nil {
		err = fmt.Errorf("%w: %v", ErrLoaderPanicked, panicked)
	} else if err == nil {
		c.insert(key, value, now.Add(c.ttl))
	}
	pending.value = value
	pending.err = err
	close(pending.done)
	c.mu.Unlock()
	if panicked != nil {
		panic(panicked)
	}
	// The load is detached so a canceled leader does not poison waiters, but
	// the leader still observes its own cancellation.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, ctxErr
	}
	return value, err
}

func (c *Cache[K, V]) insert(key K, value V, expires time.Time) {
	if len(c.entries) >= c.maxEntries {
		if oldest := c.order.Front(); oldest != nil {
			c.removeElement(oldest)
		}
	}
	element := c.order.PushBack(&item[K, V]{key: key, value: value, expires: expires})
	c.entries[key] = element
}

func (c *Cache[K, V]) removeElement(element *list.Element) {
	cached := element.Value.(*item[K, V])
	delete(c.entries, cached.key)
	c.order.Remove(element)
}

// Len returns the number of live entries.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Invalidate removes one key. An in-flight load may still populate it.
func (c *Cache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.removeElement(element)
	}
}
