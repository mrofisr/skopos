package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingEngine struct {
	done chan struct{}
	once sync.Once
}

func (e *blockingEngine) wait(ctx context.Context) (Value, error) {
	e.once.Do(func() { close(e.done) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (e *blockingEngine) Query(ctx context.Context, _ string, _ time.Time) (Value, error) {
	return e.wait(ctx)
}

func (e *blockingEngine) QueryRange(ctx context.Context, _ string, _, _ time.Time, _ time.Duration) (Matrix, error) {
	e.once.Do(func() { close(e.done) })
	<-ctx.Done()
	return nil, ctx.Err()
}

type blockingFetcher struct {
	done chan struct{}
	once sync.Once
}

func (f *blockingFetcher) wait(ctx context.Context) error {
	f.once.Do(func() { close(f.done) })
	<-ctx.Done()
	return ctx.Err()
}

func (f *blockingFetcher) Fetch(ctx context.Context, _ Selector, _, _ time.Time) ([]Series, error) {
	return nil, f.wait(ctx)
}

func (f *blockingFetcher) Metadata(ctx context.Context, _ string) (map[string]Metadata, error) {
	return nil, f.wait(ctx)
}

func timeoutAPI(t *testing.T, engine QueryEngine, fetcher SeriesFetcher, timeout time.Duration) http.Handler {
	t.Helper()
	api := NewAPI(engine, fetcher, "test-version")
	api.Timeout = timeout
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

func TestQueryTimeoutShape(t *testing.T) {
	engine := &blockingEngine{done: make(chan struct{})}
	h := timeoutAPI(t, engine, fakeSeriesFetcher{}, 50*time.Millisecond)
	started := time.Now()
	rec := do(t, h, "/api/v1/query?query=up")
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("query returned after %s, want a prompt timeout", elapsed)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content type = %q, want JSON", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "error" || body["errorType"] != "timeout" {
		t.Fatalf("body = %#v, want status=error errorType=timeout", body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "query timed out in 50ms") {
		t.Fatalf("error = %q, want the Prometheus timeout message", message)
	}
	select {
	case <-engine.done:
	case <-time.After(time.Second):
		t.Fatal("engine never saw the deadline")
	}
}

func TestQueryRangeTimeoutShape(t *testing.T) {
	engine := &blockingEngine{done: make(chan struct{})}
	h := timeoutAPI(t, engine, fakeSeriesFetcher{}, 50*time.Millisecond)
	rec := do(t, h, "/api/v1/query_range?query=up&start=0&end=60&step=60")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorType"] != "timeout" {
		t.Fatalf("body = %#v", body)
	}
}

func TestSeriesTimeoutShape(t *testing.T) {
	fetcher := &blockingFetcher{done: make(chan struct{})}
	h := timeoutAPI(t, fakeQueryEngine{}, fetcher, 50*time.Millisecond)
	rec := do(t, h, `/api/v1/series?match[]=up&start=0&end=60`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorType"] != "timeout" {
		t.Fatalf("body = %#v", body)
	}
}

func TestClientDisconnectCancelsTheQuery(t *testing.T) {
	engine := &blockingEngine{done: make(chan struct{})}
	server := httptest.NewServer(timeoutAPI(t, engine, fakeSeriesFetcher{}, time.Minute))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/query?query=up", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-engine.done
		cancel()
	}()
	response, err := server.Client().Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
	select {
	case <-engine.done:
	case <-time.After(time.Second):
		t.Fatal("engine never saw the cancellation")
	}
}

type headerCounter struct {
	header http.Header
	code   int
	writes int
}

func (h *headerCounter) Header() http.Header { return h.header }
func (h *headerCounter) WriteHeader(code int) {
	h.writes++
	h.code = code
}
func (h *headerCounter) Write(body []byte) (int, error) { return len(body), nil }

func TestTimeoutWritesHeadersOnce(t *testing.T) {
	engine := &blockingEngine{done: make(chan struct{})}
	api := NewAPI(engine, fakeSeriesFetcher{}, "test")
	api.Timeout = 20 * time.Millisecond
	mux := http.NewServeMux()
	api.Register(mux)
	rec := &headerCounter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil)
	mux.ServeHTTP(rec, request)
	if rec.writes != 1 {
		t.Fatalf("WriteHeader calls = %d, want 1", rec.writes)
	}
}
