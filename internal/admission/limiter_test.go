package admission

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

func acquire(t *testing.T, limiter *Limiter) func() {
	t.Helper()
	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return release
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met within one second")
}

func TestLimiterRejectsBeyondCapAndQueue(t *testing.T) {
	limiter, err := New(2, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first := acquire(t, limiter)
	second := acquire(t, limiter)
	if got := limiter.InFlight(); got != 2 {
		t.Fatalf("InFlight = %d, want 2", got)
	}

	queued := make(chan func(), 1)
	go func() {
		release, err := limiter.Acquire(context.Background())
		if err != nil {
			queued <- nil
			return
		}
		queued <- release
	}()
	waitFor(t, func() bool { return limiter.Queued() == 1 })

	if _, err := limiter.Acquire(context.Background()); !errors.Is(err, ErrRejected) {
		t.Fatalf("Acquire beyond the queue = %v, want ErrRejected", err)
	}

	first()
	select {
	case release := <-queued:
		if release == nil {
			t.Fatal("queued Acquire failed")
		}
		release()
	case <-time.After(time.Second):
		t.Fatal("queued Acquire was not granted")
	}
	second()
}

func TestLimiterRejectsWithQueueZero(t *testing.T) {
	limiter, err := New(1, 0, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)
	defer release()
	if _, err := limiter.Acquire(context.Background()); !errors.Is(err, ErrRejected) {
		t.Fatalf("Acquire = %v, want ErrRejected", err)
	}
}

func TestLimiterRejectsWithZeroWait(t *testing.T) {
	limiter, err := New(1, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)
	defer release()
	started := time.Now()
	if _, err := limiter.Acquire(context.Background()); !errors.Is(err, ErrRejected) {
		t.Fatalf("Acquire = %v, want ErrRejected", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("Acquire waited %s, want an immediate rejection", elapsed)
	}
}

func TestLimiterWaitTimeout(t *testing.T) {
	limiter, err := New(1, 1, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)
	defer release()
	started := time.Now()
	if _, err := limiter.Acquire(context.Background()); !errors.Is(err, ErrRejected) {
		t.Fatalf("Acquire = %v, want ErrRejected", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("Acquire returned after %s, want at least the wait", elapsed)
	}
	if got := limiter.Queued(); got != 0 {
		t.Fatalf("Queued = %d, want 0", got)
	}
}

func TestLimiterContextCancel(t *testing.T) {
	limiter, err := New(1, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	if got := limiter.Queued(); got != 0 {
		t.Fatalf("Queued = %d, want 0", got)
	}
}

func TestLimiterReleasesOnPanic(t *testing.T) {
	limiter, err := New(1, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected a panic")
			}
		}()
		release := acquire(t, limiter)
		defer release()
		panic("boom")
	}()
	if got := limiter.InFlight(); got != 0 {
		t.Fatalf("InFlight = %d after panic, want 0", got)
	}
	release := acquire(t, limiter)
	release()
}

func TestLimiterReleaseIsIdempotent(t *testing.T) {
	limiter, err := New(1, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)
	release()
	release()
	if got := limiter.InFlight(); got != 0 {
		t.Fatalf("InFlight = %d, want 0", got)
	}
	again := acquire(t, limiter)
	if got := limiter.InFlight(); got != 1 {
		t.Fatalf("InFlight = %d, want 1", got)
	}
	again()
}

func TestLimiterNoGoroutineLeak(t *testing.T) {
	limiter, err := New(2, 4, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		release, err := limiter.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	waitFor(t, func() bool { return runtime.NumGoroutine() <= before })
}

func TestLimiterFIFONoStarvation(t *testing.T) {
	limiter, err := New(1, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release := acquire(t, limiter)

	order := make(chan int, 3)
	var wg sync.WaitGroup
	for id := 1; id <= 3; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			held, err := limiter.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire(%d): %v", id, err)
				return
			}
			order <- id
			held()
		}(id)
		waitFor(t, func() bool { return limiter.Queued() == id })
	}

	release()
	wg.Wait()
	close(order)
	got := make([]int, 0, 3)
	for id := range order {
		got = append(got, id)
	}
	want := []int{1, 2, 3}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("grant order = %v, want %v", got, want)
		}
	}
}

func TestNewRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		queue    int
		wait     time.Duration
	}{
		{name: "zero capacity", capacity: 0, queue: 1, wait: time.Second},
		{name: "negative capacity", capacity: -1, queue: 1, wait: time.Second},
		{name: "negative queue", capacity: 1, queue: -1, wait: time.Second},
		{name: "negative wait", capacity: 1, queue: 1, wait: -time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.capacity, test.queue, test.wait); err == nil {
				t.Fatal("New accepted invalid values")
			}
		})
	}
}

func TestLimiterLargeCapacity(t *testing.T) {
	limiter, err := New(1000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	releases := make([]func(), 0, 1000)
	for i := 0; i < 1000; i++ {
		release, err := limiter.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if got := limiter.InFlight(); got != 1000 {
		t.Fatalf("InFlight = %d, want 1000", got)
	}
	for _, release := range releases {
		release()
	}
}
