package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/internal/config"
	"github.com/jawaracloud/skopos/internal/metrics"
)

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
