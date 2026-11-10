package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingLogFetcher struct {
	done chan struct{}
	once sync.Once
}

func (f *blockingLogFetcher) wait(ctx context.Context) ([]LogEntry, error) {
	f.once.Do(func() { close(f.done) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingLogFetcher) FetchRange(ctx context.Context, _, _ time.Time) ([]LogEntry, error) {
	return f.wait(ctx)
}

func (f *blockingLogFetcher) FetchQueries(ctx context.Context, _ []Query, _, _ time.Time) ([]LogEntry, error) {
	return f.wait(ctx)
}

func lokiTimeoutAPI(t *testing.T, fetcher LogFetcher, timeout time.Duration) http.Handler {
	t.Helper()
	api := NewAPI(fetcher, "test-version")
	api.Timeout = timeout
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

func TestLokiQueryTimeoutShape(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	h := lokiTimeoutAPI(t, fetcher, 50*time.Millisecond)
	started := time.Now()
	rec := do(t, h, `/loki/api/v1/query_range?query=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("query returned after %s, want a prompt timeout", elapsed)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("content type = %q, want text/plain", got)
	}
	if rec.Body.String() != lokiTimeoutMessage {
		t.Fatalf("body = %q, want the Loki timeout message", rec.Body.String())
	}
	select {
	case <-fetcher.done:
	case <-time.After(time.Second):
		t.Fatal("fetcher never saw the deadline")
	}
}

func TestLokiMetricQueryTimeoutShape(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	h := lokiTimeoutAPI(t, fetcher, 50*time.Millisecond)
	query := `rate(%7Bservice_name%3D%22checkout%22%7D%5B5m%5D)`
	rec := do(t, h, `/loki/api/v1/query_range?query=`+query+`&start=1793491200&end=1793491500&step=60`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != lokiTimeoutMessage {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestLokiLabelsTimeoutShape(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	h := lokiTimeoutAPI(t, fetcher, 50*time.Millisecond)
	rec := do(t, h, `/loki/api/v1/labels?start=1793491200&end=1793491500`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestLokiSeriesTimeoutShape(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	h := lokiTimeoutAPI(t, fetcher, 50*time.Millisecond)
	rec := do(t, h, `/loki/api/v1/series?match%5B%5D=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestLokiClientDisconnectCancelsTheQuery(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	server := httptest.NewServer(lokiTimeoutAPI(t, fetcher, time.Minute))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		server.URL+`/loki/api/v1/query_range?query=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`, nil)
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

type lokiHeaderCounter struct {
	header http.Header
	code   int
	writes int
}

func (h *lokiHeaderCounter) Header() http.Header { return h.header }
func (h *lokiHeaderCounter) WriteHeader(code int) {
	h.writes++
	h.code = code
}
func (h *lokiHeaderCounter) Write(body []byte) (int, error) { return len(body), nil }

func TestLokiTimeoutWritesHeadersOnce(t *testing.T) {
	fetcher := &blockingLogFetcher{done: make(chan struct{})}
	api := NewAPI(fetcher, "test")
	api.Timeout = 20 * time.Millisecond
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := &lokiHeaderCounter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet,
		`/loki/api/v1/query_range?query=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`, nil)
	mux.ServeHTTP(rec, request)
	if rec.writes != 1 {
		t.Fatalf("WriteHeader calls = %d, want 1", rec.writes)
	}
}
