package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/jawaracloud/skopos/internal/metrics"
	"github.com/jawaracloud/skopos/internal/prom"
	"github.com/jawaracloud/skopos/internal/tempo"
)

func TestUnsupportedFeatureCounted(t *testing.T) {
	registry := metrics.NewRegistry("test")
	httpMetrics := metrics.NewHTTP(registry)

	promMux := http.NewServeMux()
	prom.NewAPI(prom.NewEngine(nil), nil, "test").Register(promMux)
	tempoMux := http.NewServeMux()
	tempo.NewAPI(nil, "test").Register(tempoMux)

	for name, handler := range map[string]http.Handler{
		"prom":  httpMetrics.Wrap(promMux),
		"tempo": httpMetrics.Wrap(tempoMux),
	} {
		path := `/api/v1/query?query=rate%28up%5B5m%3A1m%5D%29`
		if name == "tempo" {
			path = `/api/search?q=%7B%7D%20%3E%3E%20%7B%7D`
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %s)", name, rec.Code, rec.Body.String())
		}
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var family *dto.MetricFamily
	for _, candidate := range families {
		if candidate.GetName() == "skopos_unsupported_queries_total" {
			family = candidate
		}
	}
	if family == nil || len(family.GetMetric()) != 2 {
		t.Fatalf("unsupported series = %v, want two", family)
	}
	got := make(map[string]bool)
	for _, metric := range family.GetMetric() {
		labels := make(map[string]string)
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		got[labels["head"]+"/"+labels["feature"]] = true
	}
	for _, want := range []string{"prom/subquery", "tempo/structural_operator"} {
		if !got[want] {
			t.Errorf("missing unsupported series %q; got %v", want, got)
		}
	}
}
