package ttlcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func TestConcurrentMissLoadsOnce(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	cache, err := New(Options[string, string]{
		TTL:        time.Minute,
		MaxEntries: 10,
		Clock:      newFakeClock().Now,
		Load: func(context.Context, string) (string, error) {
			calls.Add(1)
			<-release
			return "value", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.Get(context.Background(), "key")
			if err != nil {
				errs <- err
				return
			}
			if value != "value" {
				errs <- fmt.Errorf("value = %q", value)
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
}

func TestTTLExpiry(t *testing.T) {
	clock := newFakeClock()
	var calls atomic.Int32
	cache, err := New(Options[string, int]{
		TTL:        time.Minute,
		MaxEntries: 10,
		Clock:      clock.Now,
		Load: func(context.Context, string) (int, error) {
			return int(calls.Add(1)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if value, err := cache.Get(ctx, "key"); err != nil || value != 1 {
		t.Fatalf("first Get = %d, %v", value, err)
	}
	if value, err := cache.Get(ctx, "key"); err != nil || value != 1 {
		t.Fatalf("cached Get = %d, %v", value, err)
	}
	clock.Advance(time.Minute)
	if value, err := cache.Get(ctx, "key"); err != nil || value != 2 {
		t.Fatalf("expired Get = %d, %v", value, err)
	}
}

func TestEvictionKeepsTheCacheBounded(t *testing.T) {
	clock := newFakeClock()
	var calls atomic.Int32
	cache, err := New(Options[string, int]{
		TTL:        time.Hour,
		MaxEntries: 2,
		Clock:      clock.Now,
		Load: func(_ context.Context, key string) (int, error) {
			return int(calls.Add(1)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := cache.Get(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	if _, err := cache.Get(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("loader calls = %d, want 4 (a was evicted)", got)
	}
}

func TestLoaderErrorIsNotCached(t *testing.T) {
	var calls atomic.Int32
	cache, err := New(Options[string, string]{
		TTL:        time.Minute,
		MaxEntries: 10,
		Load: func(context.Context, string) (string, error) {
			if calls.Add(1) == 1 {
				return "", errors.New("temporary failure")
			}
			return "ok", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := cache.Get(ctx, "key"); err == nil {
		t.Fatal("first Get succeeded, want an error")
	}
	if value, err := cache.Get(ctx, "key"); err != nil || value != "ok" {
		t.Fatalf("second Get = %q, %v", value, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
}

func TestTTLZeroDisablesCaching(t *testing.T) {
	var calls atomic.Int32
	cache, err := New(Options[string, int]{
		TTL:        0,
		MaxEntries: 10,
		Load: func(context.Context, string) (int, error) {
			return int(calls.Add(1)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := cache.Get(ctx, "key"); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("loader calls = %d, want 3", got)
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
}

func TestInvalidate(t *testing.T) {
	var calls atomic.Int32
	cache, err := New(Options[string, int]{
		TTL:        time.Minute,
		MaxEntries: 10,
		Load: func(context.Context, string) (int, error) {
			return int(calls.Add(1)), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := cache.Get(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	cache.Invalidate("key")
	if _, err := cache.Get(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
}

func TestInvalidOptions(t *testing.T) {
	if _, err := New(Options[string, int]{TTL: time.Minute, MaxEntries: 1}); err == nil {
		t.Fatal("New accepted a nil loader")
	}
	load := func(context.Context, string) (int, error) { return 0, nil }
	if _, err := New(Options[string, int]{TTL: time.Minute, MaxEntries: 0, Load: load}); err == nil {
		t.Fatal("New accepted MaxEntries 0")
	}
	if _, err := New(Options[string, int]{TTL: time.Minute, MaxEntries: 1, LoadTimeout: -time.Second, Load: load}); err == nil {
		t.Fatal("New accepted a negative LoadTimeout")
	}
	cache, err := New(Options[string, int]{TTL: time.Minute, MaxEntries: 1, Load: load})
	if err != nil {
		t.Fatalf("New with the default LoadTimeout: %v", err)
	}
	if cache.loadTimeout != defaultLoadTimeout {
		t.Fatalf("loadTimeout = %s, want %s", cache.loadTimeout, defaultLoadTimeout)
	}
}

func TestLoaderUsesDetachedContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	observed := make(chan error, 1)
	cache, err := New(Options[string, string]{
		TTL:        time.Minute,
		MaxEntries: 1,
		Load: func(ctx context.Context, _ string) (string, error) {
			close(started)
			<-release
			observed <- ctx.Err()
			return "value", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		value string
		err   error
	}
	results := make(chan result, 1)
	go func() {
		value, err := cache.Get(ctx, "key")
		results <- result{value: value, err: err}
	}()
	<-started
	cancel()
	close(release)

	select {
	case err := <-observed:
		if err != nil {
			t.Fatalf("loader context was canceled with the caller: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loader did not return")
	}
	select {
	case got := <-results:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("leader Get error = %v, want context.Canceled", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leader Get did not return")
	}
	if value, err := cache.Get(context.Background(), "key"); err != nil || value != "value" {
		t.Fatalf("cached Get = %q, %v; the detached load was not cached", value, err)
	}
}

// doneObserver closes reached when a Get evaluates the waiter select, so the
// test can panic the loader only after a waiter is registered.
type doneObserver struct {
	context.Context
	reached chan struct{}
	once    sync.Once
}

func (c *doneObserver) Done() <-chan struct{} {
	c.once.Do(func() { close(c.reached) })
	return c.Context.Done()
}

func TestPanickingLoaderDoesNotBlock(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	cache, err := New(Options[string, string]{
		TTL:        time.Minute,
		MaxEntries: 1,
		Load: func(context.Context, string) (string, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
				panic("boom")
			}
			return "ok", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	leaderPanic := make(chan any, 1)
	go func() {
		defer func() { leaderPanic <- recover() }()
		_, _ = cache.Get(context.Background(), "key")
	}()
	<-started

	waiterCtx := &doneObserver{Context: context.Background(), reached: make(chan struct{})}
	waiterErr := make(chan error, 1)
	go func() {
		_, err := cache.Get(waiterCtx, "key")
		waiterErr <- err
	}()
	<-waiterCtx.reached
	close(release)

	select {
	case err := <-waiterErr:
		if !errors.Is(err, ErrLoaderPanicked) {
			t.Fatalf("waiter error = %v, want ErrLoaderPanicked", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter blocked on a panicking loader")
	}
	select {
	case got := <-leaderPanic:
		if got != "boom" {
			t.Fatalf("recovered panic = %v, want boom", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leader did not re-panic")
	}
	if value, err := cache.Get(context.Background(), "key"); err != nil || value != "ok" {
		t.Fatalf("Get after panic = %q, %v", value, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
}

func TestLoaderTimeout(t *testing.T) {
	var calls atomic.Int32
	cache, err := New(Options[string, string]{
		TTL:         time.Minute,
		MaxEntries:  1,
		LoadTimeout: 20 * time.Millisecond,
		Load: func(ctx context.Context, _ string) (string, error) {
			calls.Add(1)
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := cache.Get(context.Background(), "key"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	if _, err := cache.Get(context.Background(), "key"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second error = %v, want context.DeadlineExceeded", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2; a timed-out load was cached", got)
	}
}

func TestCanceledWaiter(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	cache, err := New(Options[string, int]{
		TTL:        time.Minute,
		MaxEntries: 1,
		Load: func(context.Context, string) (int, error) {
			close(started)
			<-release
			return 1, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = cache.Get(context.Background(), "key")
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Get(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	close(release)
}
