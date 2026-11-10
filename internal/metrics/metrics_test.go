package metrics

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil/promlint"

	"github.com/jawaracloud/skopos/internal/semctx"
)

var update = flag.Bool("update", false, "update golden files")

func catalogFixture() *semctx.Catalog {
	return &semctx.Catalog{
		Schema:  semctx.SchemaVersion,
		Version: "ctx/v1",
		Metrics: map[string]semctx.Metric{
			"http_requests_total":   {Type: "counter", Unit: "requests"},
			"http_request_duration": {Type: "histogram", Unit: "seconds"},
			"up":                    {Type: "gauge"},
		},
		Services: map[string]semctx.Service{
			"checkout": {Description: "Checkout service"},
		},
		Logs: map[string]semctx.LogContext{
			"checkout_errors":  {Service: "checkout", Event: "error"},
			"checkout_timeout": {Service: "checkout", Event: "timeout"},
		},
		Traces: map[string]semctx.TraceContext{
			"checkout":      {Service: "checkout", Operation: "POST /checkout"},
			"checkout_pay":  {Service: "checkout", Operation: "POST /pay"},
			"checkout_cart": {Service: "checkout", Operation: "GET /cart"},
		},
	}
}

type fakeObserver struct {
	observed []semctx.ObservedMetric
	err      error
}

func (f fakeObserver) Observe(context.Context, string) (semctx.ObservedMetric, bool, error) {
	return semctx.ObservedMetric{}, false, f.err
}

func (f fakeObserver) ListObserved(context.Context) ([]semctx.ObservedMetric, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.observed, nil
}

type fakeSignalObserver struct {
	logs      []semctx.ObservedLog
	traces    []semctx.ObservedTrace
	logsErr   error
	tracesErr error
}

func (f fakeSignalObserver) ListObservedLogs(context.Context) ([]semctx.ObservedLog, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	return f.logs, nil
}

func (f fakeSignalObserver) ListObservedTraces(context.Context) ([]semctx.ObservedTrace, error) {
	if f.tracesErr != nil {
		return nil, f.tracesErr
	}
	return f.traces, nil
}

// safeObserver is concurrency-safe so the scrape test can run under -race.
type safeObserver struct {
	mu    sync.Mutex
	calls int
}

func (s *safeObserver) Observe(context.Context, string) (semctx.ObservedMetric, bool, error) {
	return semctx.ObservedMetric{}, false, nil
}

func (s *safeObserver) ListObserved(context.Context) ([]semctx.ObservedMetric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return []semctx.ObservedMetric{
		{Name: "http_requests_total"},
		{Name: "up"},
		{Name: "missing"},
	}, nil
}

type safeSignalObserver struct{}

func (safeSignalObserver) ListObservedLogs(context.Context) ([]semctx.ObservedLog, error) {
	return []semctx.ObservedLog{{Service: "checkout"}}, nil
}

func (safeSignalObserver) ListObservedTraces(context.Context) ([]semctx.ObservedTrace, error) {
	return []semctx.ObservedTrace{{Service: "checkout"}}, nil
}

func contextRegistry(t *testing.T, api *semctx.API) *prometheus.Registry {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewContextCollector(api))
	return registry
}

func render(t *testing.T, registry prometheus.Gatherer) string {
	t.Helper()
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rec.Code)
	}
	return rec.Body.String()
}

func TestMetrics_ExposesBuildInfo(t *testing.T) {
	registry := NewRegistry("test-version")
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "skopos_build_info" {
			continue
		}
		if len(family.GetMetric()) != 1 {
			t.Fatalf("build info samples = %d, want 1", len(family.GetMetric()))
		}
		metric := family.GetMetric()[0]
		if got := metric.GetGauge().GetValue(); got != 1 {
			t.Fatalf("build info value = %v, want 1", got)
		}
		for _, label := range metric.GetLabel() {
			if label.GetName() == "version" && label.GetValue() == "test-version" {
				return
			}
		}
		t.Fatalf("build info labels = %v, want version=test-version", metric.GetLabel())
	}
	t.Fatal("skopos_build_info is missing")
}

func TestMetrics_ContextGaugesUnchanged(t *testing.T) {
	api := semctx.NewAPI(catalogFixture()).
		WithObserver(fakeObserver{observed: []semctx.ObservedMetric{
			{Name: "http_requests_total"},
			{Name: "http_request_duration"},
			{Name: "up"},
			{Name: "observed_only"},
		}}).
		WithSignalObserver(fakeSignalObserver{
			logs: []semctx.ObservedLog{
				{Service: "checkout", Severity: "error"},
				{Service: "checkout", Severity: "warn"},
				{Service: "checkout", Severity: "info"},
			},
			traces: []semctx.ObservedTrace{
				{Service: "checkout", Operation: "POST /checkout"},
				{Service: "checkout", Operation: "POST /pay"},
			},
		})
	got := render(t, contextRegistry(t, api))
	path := filepath.Join("testdata", "context.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("metrics output changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMetrics_ContextObserverFailures(t *testing.T) {
	api := semctx.NewAPI(catalogFixture()).
		WithObserver(fakeObserver{err: errors.New("metadata store down")}).
		WithSignalObserver(fakeSignalObserver{
			logsErr:   errors.New("logs down"),
			tracesErr: errors.New("traces down"),
		})
	body := render(t, contextRegistry(t, api))
	for _, want := range []string{
		`skopos_context_documented{signal="metrics"} 3`,
		`skopos_context_observed_available{signal="metrics"} 0`,
		`skopos_context_observed_available{signal="logs"} 0`,
		`skopos_context_observed_available{signal="traces"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{
		`skopos_context_observed{signal="metrics"}`,
		`skopos_context_observed{signal="logs"}`,
		`skopos_context_observed{signal="traces"}`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("metrics body contains %q after observer failure:\n%s", unwanted, body)
		}
	}
}

func TestMetrics_ContextWithoutObservers(t *testing.T) {
	body := render(t, contextRegistry(t, semctx.NewAPI(catalogFixture())))
	if !strings.Contains(body, `skopos_context_documented{signal="services"} 1`) {
		t.Fatalf("metrics body missing documented services:\n%s", body)
	}
	if strings.Contains(body, "skopos_context_observed") {
		t.Fatalf("metrics body has observed families without observers:\n%s", body)
	}
}

func TestMetrics_PassesPromlint(t *testing.T) {
	registry := NewRegistry("test-version")
	registry.MustRegister(NewContextCollector(semctx.NewAPI(catalogFixture())))
	problems, err := promlint.New(strings.NewReader(render(t, registry))).Lint()
	if err != nil {
		t.Fatalf("promlint: %v", err)
	}
	for _, problem := range problems {
		t.Errorf("promlint: %s: %s", problem.Metric, problem.Text)
	}
}

func TestMetrics_ConcurrentScrapes(t *testing.T) {
	registry := NewRegistry("test-version")
	observer := &safeObserver{}
	registry.MustRegister(NewContextCollector(
		semctx.NewAPI(catalogFixture()).
			WithObserver(observer).
			WithSignalObserver(safeSignalObserver{}),
	))
	handler := registry.Handler()

	const (
		workers    = 16
		iterations = 25
	)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
				if rec.Code != http.StatusOK {
					errs <- fmt.Errorf("status = %d", rec.Code)
					return
				}
				if !strings.Contains(rec.Body.String(), `skopos_context_documented{signal="metrics"} 3`) {
					errs <- fmt.Errorf("body missing documented metrics: %s", rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.calls != workers*iterations {
		t.Fatalf("observer calls = %d, want %d", observer.calls, workers*iterations)
	}
}
