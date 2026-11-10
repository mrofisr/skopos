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
	if _, err := New(Options[string, int]{TTL: time.Minute, MaxEntries: 0, Load: func(context.Context, string) (int, error) { return 0, nil }}); err == nil {
		t.Fatal("New accepted MaxEntries 0")
	}
}

func TestCanceledWaiter(t *testing.T) {
	release := make(chan struct{})
	cache, err := New(Options[string, int]{
		TTL:        time.Minute,
		MaxEntries: 1,
		Load: func(context.Context, string) (int, error) {
			<-release
			return 1, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	go func() {
		close(started)
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
