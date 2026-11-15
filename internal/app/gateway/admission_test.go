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

	"github.com/mrofisr/skopos/internal/auth"
	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/httpapi"
	"github.com/mrofisr/skopos/internal/metrics"
)

type pingerStub struct{}

func (pingerStub) Ping(context.Context) error { return nil }

func TestAdmissionRejectsBeyondCapAndQueue(t *testing.T) {
	registry := metrics.NewRegistry("test")
	httpMetrics := metrics.NewHTTP(registry)
	middleware, err := newAdmissionMiddleware(config.Config{
		MaxInflight: 4,
		QueueDepth:  4,
		QueueWait:   200 * time.Millisecond,
	}, httpMetrics)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(httpMetrics.Wrap(middleware.Wrap(mux)))
	t.Cleanup(server.Close)

	const clients = 20
	var wg sync.WaitGroup
	statuses := make(chan int, clients)
	errors := make(chan error, clients)
	for index := 0; index < clients; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := server.Client().Get(server.URL + "/api/v1/query")
			if err != nil {
				errors <- err
				return
			}
			defer func() { _ = response.Body.Close() }()
			statuses <- response.StatusCode
			if response.StatusCode != http.StatusOK {
				if got := response.Header.Get("Retry-After"); got != admissionRetryAfterSeconds {
					t.Errorf("Retry-After = %q, want %q", got, admissionRetryAfterSeconds)
				}
			}
		}()
	}

	for _, path := range []string{"/healthz", "/readyz"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, response.StatusCode)
		}
	}

	wg.Wait()
	close(statuses)
	close(errors)
	for err := range errors {
		t.Errorf("request error: %v", err)
	}
	admitted, rejected := 0, 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			admitted++
		case http.StatusServiceUnavailable, http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if admitted < 4 || admitted > 8 {
		t.Fatalf("admitted = %d, want between the cap (4) and cap+queue (8)", admitted)
	}
	if admitted+rejected != clients {
		t.Fatalf("admitted+rejected = %d, want %d", admitted+rejected, clients)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for _, family := range families {
		if family.GetName() != "skopos_admission_rejected_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			recorded += int(metric.GetCounter().GetValue())
		}
	}
	if recorded != rejected {
		t.Fatalf("recorded rejections = %d, want %d", recorded, rejected)
	}
}

func TestAdmissionDisabled(t *testing.T) {
	httpMetrics := metrics.NewHTTP(metrics.NewRegistry("test"))
	middleware, err := newAdmissionMiddleware(config.Config{}, httpMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if middleware != nil {
		t.Fatal("admission middleware was built with MaxInflight 0")
	}
}

func TestAdmissionRejectionShapes(t *testing.T) {
	httpMetrics := metrics.NewHTTP(metrics.NewRegistry("test"))
	middleware, err := newAdmissionMiddleware(config.Config{MaxInflight: 1}, httpMetrics)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		head      string
		status    int
		errorType string
	}{
		{head: "prom", status: http.StatusServiceUnavailable, errorType: "unavailable"},
		{head: "loki", status: http.StatusTooManyRequests, errorType: "too_many_requests"},
		{head: "tempo", status: http.StatusTooManyRequests},
	}
	for _, test := range tests {
		rec := httptest.NewRecorder()
		middleware.writeRejection(rec, test.head)
		if rec.Code != test.status {
			t.Errorf("%s status = %d, want %d", test.head, rec.Code, test.status)
		}
		if got := rec.Header().Get("Retry-After"); got != admissionRetryAfterSeconds {
			t.Errorf("%s Retry-After = %q", test.head, got)
		}
		if test.errorType != "" && !strings.Contains(rec.Body.String(), `"`+test.errorType+`"`) {
			t.Errorf("%s body = %q, want errorType %q", test.head, rec.Body.String(), test.errorType)
		}
	}
}

func TestAdmissionExemptPaths(t *testing.T) {
	for path, want := range map[string]bool{
		"/healthz":                 true,
		"/readyz":                  true,
		"/metrics":                 true,
		"/api/v1/query":            false,
		"/loki/api/v1/query_range": false,
	} {
		if got := admissionExempt(path); got != want {
			t.Errorf("admissionExempt(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestAdmissionInvalidConfig(t *testing.T) {
	httpMetrics := metrics.NewHTTP(metrics.NewRegistry("test"))
	if _, err := newAdmissionMiddleware(config.Config{MaxInflight: -1}, httpMetrics); err == nil {
		t.Fatal("newAdmissionMiddleware accepted a negative in-flight cap")
	}
}

func TestAdmissionUsesPOSTRangeCost(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{
		MaxInflight: 1, QueueDepth: 0, PointsPerSlot: 1, MaxRequestBytes: 1 << 20,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	form := url.Values{
		"query": {"sum(rate(x[5m]))"},
		"start": {"0"},
		"end":   {"900"},
		"step":  {"90"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query_range", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if called {
		t.Fatal("POST range was admitted with weight 1; the form cost was ignored")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "increase the step") {
		t.Fatalf("body = %q, want the step hint", rec.Body.String())
	}
}

func TestAdmissionRejectsOverCapacityAsBadData(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{MaxInflight: 1, PointsPerSlot: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?start=0&end=900&step=90", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if called {
		t.Fatal("over-capacity range reached the handler")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty on a bad-data rejection", got)
	}
	if !strings.Contains(rec.Body.String(), "increase the step") {
		t.Fatalf("body = %q, want the step hint", rec.Body.String())
	}
}

func TestAuthRunsBeforeAdmission(t *testing.T) {
	middleware, err := newAdmissionMiddleware(config.Config{MaxInflight: 1, QueueDepth: 0, PointsPerSlot: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	release, err := middleware.limiters["prom"].AcquireWeight(context.Background(), 1)
	if err != nil {
		t.Fatalf("saturate limiter: %v", err)
	}
	t.Cleanup(release)

	guard, err := auth.New("user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{
		Pinger:  pingerStub{},
		Auth:    guard,
		Version: "test",
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := queryHandler(api, middleware)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; admission ran before auth", rec.Code)
	}
}
