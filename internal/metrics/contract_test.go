package metrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/semctx"
)

// TestMetricsContract is the EC4 evidence gate. It asserts the metric names
// and label keys that the alpha observability contract promises.
func TestMetricsContract(t *testing.T) {
	registry := NewRegistry("contract")
	httpMetrics := NewHTTP(registry)
	clickhouse := NewClickHouse(registry)

	api := semctx.NewAPI(catalogFixture())
	api.Observer = fakeObserver{observed: []semctx.ObservedMetric{{Name: "up"}}}
	api.SignalObserver = fakeSignalObserver{
		logs:   []semctx.ObservedLog{{Service: "checkout"}},
		traces: []semctx.ObservedTrace{{Service: "checkout"}},
	}
	registry.MustRegister(NewContextCollector(api))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		RecordUnsupported(r.Context(), "function")
		w.WriteHeader(http.StatusOK)
	})
	handler := httpMetrics.Wrap(mux)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))

	httpMetrics.RecordAdmissionRejected("prom")
	clickhouse.QueryDone("prom", "ok", time.Millisecond, 1, 1)

	required := map[string][]string{
		"skopos_build_info":                        {"version"},
		"skopos_http_requests_total":               {"head", "route", "code"},
		"skopos_http_request_duration_seconds":     {"head", "route"},
		"skopos_http_in_flight":                    {"head"},
		"skopos_unsupported_queries_total":         {"head", "feature"},
		"skopos_admission_rejected_total":          {"head"},
		"skopos_clickhouse_queries_total":          {"head", "result"},
		"skopos_clickhouse_query_duration_seconds": {"head"},
		"skopos_clickhouse_read_rows_total":        {"head"},
		"skopos_clickhouse_read_bytes_total":       {"head"},
		"skopos_context_documented":                {"signal"},
		"skopos_context_observed":                  {"signal"},
		"skopos_context_coverage_ratio":            {"signal"},
		"skopos_context_low_coverage":              {"signal"},
	}
	for name, wantLabels := range required {
		family := gatherFamily(t, registry, name)
		if family == nil {
			t.Errorf("missing metric family %q", name)
			continue
		}
		if len(family.GetMetric()) == 0 {
			t.Errorf("metric family %q has no series", name)
			continue
		}
		got := labelsOf(family.GetMetric()[0])
		for _, label := range wantLabels {
			if _, ok := got[label]; !ok {
				t.Errorf("metric %q is missing label %q (labels=%v)", name, label, got)
			}
		}
	}

	failureRegistry := NewRegistry("contract-failure")
	failureAPI := semctx.NewAPI(catalogFixture())
	failureAPI.Observer = fakeObserver{err: errors.New("observer unavailable")}
	failureAPI.SignalObserver = fakeSignalObserver{
		logsErr:   errors.New("log observer unavailable"),
		tracesErr: errors.New("trace observer unavailable"),
	}
	failureRegistry.MustRegister(NewContextCollector(failureAPI))
	available := gatherFamily(t, failureRegistry, "skopos_context_observed_available")
	if available == nil || len(available.GetMetric()) == 0 {
		t.Fatal("missing skopos_context_observed_available on observer failure")
	}
	if got := labelsOf(available.GetMetric()[0]); got["signal"] == "" {
		t.Fatalf("skopos_context_observed_available has no signal label: %v", got)
	}
}
