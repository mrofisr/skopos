package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/metrics"
)

func burstConfig() config.Config {
	return config.Config{
		MaxInflight:           300,
		PointsPerSlot:         100,
		RowsPerSlot:           10_000,
		InstantFallbackWeight: 30,
	}
}

func burstCardinality(_ context.Context, metric string) int {
	if metric == "http_requests_total" {
		return 100_000 // 5m rate -> weight 200
	}
	return 10 // cheap instant -> weight 1
}

type statusResult struct {
	code int
	body string
}

func getAsync(client *http.Client, target string) <-chan statusResult {
	out := make(chan statusResult, 1)
	go func() {
		response, err := client.Get(target)
		if err != nil {
			out <- statusResult{code: -1, body: err.Error()}
			return
		}
		defer response.Body.Close()
		out <- statusResult{code: response.StatusCode}
	}()
	return out
}

// A saturated store must shed expensive instant work while cheap instants and
// probes still get through, and the budget must be available again afterwards.
func TestBurstShedsExpensiveInstantWork(t *testing.T) {
	middleware, err := newAdmissionMiddleware(burstConfig(), metrics.NewHTTP(metrics.NewRegistry("test")), burstCardinality)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("query"), "http_requests_total") {
			w.WriteHeader(http.StatusOK)
			return
		}
		once.Do(func() { close(entered) })
		<-release
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	server := httptest.NewServer(middleware.Wrap(mux))
	t.Cleanup(server.Close)

	expensive := "/api/v1/query?query=" + url.QueryEscape("sum(rate(http_requests_total[5m]))")
	first := getAsync(server.Client(), server.URL+expensive)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first expensive query never entered the handler")
	}

	// Only 100 of 300 slots remain, so a second weight-200 query is shed.
	second := getAsync(server.Client(), server.URL+expensive)
	select {
	case got := <-second:
		if got.code != http.StatusServiceUnavailable {
			t.Fatalf("second expensive status = %d, want 503", got.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second expensive query was not rejected")
	}

	// A weight-1 instant and a probe still fit.
	cheap := getAsync(server.Client(), server.URL+"/api/v1/query?query=up")
	select {
	case got := <-cheap:
		if got.code != http.StatusOK {
			t.Fatalf("cheap instant status = %d, want 200", got.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cheap instant was not admitted")
	}
	probe := getAsync(server.Client(), server.URL+"/healthz")
	if got := <-probe; got.code != http.StatusOK {
		t.Fatalf("probe status = %d, want 200", got.code)
	}

	close(release)
	if got := <-first; got.code != http.StatusOK {
		t.Fatalf("first expensive status = %d, want 200", got.code)
	}
	// Wait for the deferred release before checking capacity.
	deadline := time.Now().Add(5 * time.Second)
	for middleware.limiters["prom"].InFlight() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	after := getAsync(server.Client(), server.URL+expensive)
	select {
	case got := <-after:
		if got.code != http.StatusOK {
			t.Fatalf("expensive query after release = %d, want 200", got.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expensive query was not admitted after release")
	}
}

func TestHandlerPanicReleasesAdmissionBudget(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{MaxInflight: 1, RowsPerSlot: 10_000}, nil, burstCardinality)
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil)
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()
	if got := middleware.limiters["prom"].InFlight(); got != 0 {
		t.Fatalf("in-flight after panic = %d, want 0", got)
	}
	handler = middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status after panic = %d, want 200", rec.Code)
	}
}

func TestClientDisconnectReleasesAdmissionBudget(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{MaxInflight: 1, RowsPerSlot: 10_000}, nil, burstCardinality)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	handler := middleware.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(done)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil).WithContext(ctx)
	go handler.ServeHTTP(httptest.NewRecorder(), request)
	deadline := time.Now().Add(5 * time.Second)
	for middleware.limiters["prom"].InFlight() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not observe the disconnect")
	}
	for middleware.limiters["prom"].InFlight() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := middleware.limiters["prom"].InFlight(); got != 0 {
		t.Fatalf("in-flight after disconnect = %d, want 0", got)
	}
}

func TestQueueWaitExpiryDoesNotLeakBudget(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{
		MaxInflight: 1, QueueDepth: 1, QueueWait: 20 * time.Millisecond, RowsPerSlot: 10_000,
	}, metrics.NewHTTP(metrics.NewRegistry("test")), burstCardinality)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	first := getAsync(server.Client(), server.URL+"/api/v1/query?query=up")
	<-entered
	second := getAsync(server.Client(), server.URL+"/api/v1/query?query=up")
	select {
	case got := <-second:
		if got.code != http.StatusServiceUnavailable {
			t.Fatalf("queued status = %d, want 503", got.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued request did not time out")
	}
	if got := middleware.limiters["prom"].Queued(); got != 0 {
		t.Fatalf("queued after expiry = %d, want 0", got)
	}
	close(release)
	<-first
	if got := middleware.limiters["prom"].InFlight(); got != 0 {
		t.Fatalf("in-flight after completion = %d, want 0", got)
	}
}
