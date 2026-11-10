package tempo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingTraceFetcher struct {
	done chan struct{}
	once sync.Once
}

func (f *blockingTraceFetcher) wait(ctx context.Context) ([]Span, error) {
	f.once.Do(func() { close(f.done) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingTraceFetcher) GetTrace(ctx context.Context, _ string, _, _ *time.Time) ([]Span, error) {
	return f.wait(ctx)
}

func (f *blockingTraceFetcher) FetchRange(ctx context.Context, _, _ time.Time) ([]Span, error) {
	return f.wait(ctx)
}

func tempoTimeoutAPI(t *testing.T, fetcher TraceFetcher, timeout time.Duration) http.Handler {
	t.Helper()
	api := NewAPI(fetcher, "test-version")
	api.Timeout = timeout
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

func doTempo(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request)
	return rec
}

func TestTempoSearchTimeoutShape(t *testing.T) {
	fetcher := &blockingTraceFetcher{done: make(chan struct{})}
	h := tempoTimeoutAPI(t, fetcher, 50*time.Millisecond)
	started := time.Now()
	rec := doTempo(t, h, `/api/search?q=%7B%7D`)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("search returned after %s, want a prompt timeout", elapsed)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("content type = %q, want text/plain", got)
	}
	if rec.Body.String() != "context deadline exceeded" {
		t.Fatalf("body = %q, want the Tempo deadline message", rec.Body.String())
	}
	select {
	case <-fetcher.done:
	case <-time.After(time.Second):
		t.Fatal("fetcher never saw the deadline")
	}
}

func TestTempoTraceTimeoutShape(t *testing.T) {
	fetcher := &blockingTraceFetcher{done: make(chan struct{})}
	h := tempoTimeoutAPI(t, fetcher, 50*time.Millisecond)
	rec := doTempo(t, h, `/api/traces/0123456789abcdef0123456789abcdef`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "context deadline exceeded" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestTempoTagsTimeoutShape(t *testing.T) {
	fetcher := &blockingTraceFetcher{done: make(chan struct{})}
	h := tempoTimeoutAPI(t, fetcher, 50*time.Millisecond)
	rec := doTempo(t, h, `/api/v2/search/tags`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestTempoClientDisconnectCancelsTheQuery(t *testing.T) {
	fetcher := &blockingTraceFetcher{done: make(chan struct{})}
	server := httptest.NewServer(tempoTimeoutAPI(t, fetcher, time.Minute))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+`/api/search?q=%7B%7D`, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-fetcher.done
		cancel()
	}()
	response, err := server.Client().Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
	select {
	case <-fetcher.done:
	case <-time.After(time.Second):
		t.Fatal("fetcher never saw the cancellation")
	}
}

type tempoHeaderCounter struct {
	header http.Header
	code   int
	writes int
}

func (h *tempoHeaderCounter) Header() http.Header { return h.header }
func (h *tempoHeaderCounter) WriteHeader(code int) {
	h.writes++
	h.code = code
}
func (h *tempoHeaderCounter) Write(body []byte) (int, error) { return len(body), nil }

func TestTempoTimeoutWritesHeadersOnce(t *testing.T) {
	fetcher := &blockingTraceFetcher{done: make(chan struct{})}
	api := NewAPI(fetcher, "test")
	api.Timeout = 20 * time.Millisecond
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := &tempoHeaderCounter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet, `/api/search?q=%7B%7D`, nil)
	mux.ServeHTTP(rec, request)
	if rec.writes != 1 {
		t.Fatalf("WriteHeader calls = %d, want 1", rec.writes)
	}
}
