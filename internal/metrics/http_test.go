package metrics

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func gatherFamily(t *testing.T, registry *Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

func labelsOf(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

func TestRouteLabelUsesPattern(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/label/{name}/values", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := httpMetrics.Wrap(mux)
	for _, name := range []string{"alpha", "beta"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/label/"+name+"/values", nil))
	}
	family := gatherFamily(t, registry, "skopos_http_requests_total")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("request series = %v, want one pattern series", family)
	}
	labels := labelsOf(family.GetMetric()[0])
	if labels["head"] != "prom" || labels["route"] != "/api/v1/label/{name}/values" || labels["code"] != "200" {
		t.Fatalf("labels = %v", labels)
	}
	if family.GetMetric()[0].GetCounter().GetValue() != 2 {
		t.Fatalf("counter = %v, want 2", family.GetMetric()[0].GetCounter().GetValue())
	}
}

func TestUnknownPathsDoNotGrowSeries(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := httpMetrics.Wrap(mux)
	for index := 0; index < 1000; index++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/random/"+strconv.Itoa(index), nil))
	}
	family := gatherFamily(t, registry, "skopos_http_requests_total")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("request series = %d, want exactly one catch-all series", len(family.GetMetric()))
	}
	labels := labelsOf(family.GetMetric()[0])
	if labels["head"] != "gateway" || labels["route"] != "/" || labels["code"] != "404" {
		t.Fatalf("labels = %v", labels)
	}
	if family.GetMetric()[0].GetCounter().GetValue() != 1000 {
		t.Fatalf("counter = %v, want 1000", family.GetMetric()[0].GetCounter().GetValue())
	}
}

func TestRecoveredPanicCountsAs500(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	recoverer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
	handler := recoverer(httpMetrics.Wrap(panicking))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	family := gatherFamily(t, registry, "skopos_http_requests_total")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("request series = %v", family)
	}
	if code := labelsOf(family.GetMetric()[0])["code"]; code != "500" {
		t.Fatalf("code = %q, want 500", code)
	}
}

func TestInFlightGauge(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	handler := httpMetrics.Wrap(mux)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))
	}()
	<-started
	family := gatherFamily(t, registry, "skopos_http_in_flight")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("in-flight series = %v", family)
	}
	if value := family.GetMetric()[0].GetGauge().GetValue(); value != 1 {
		t.Fatalf("in-flight = %v, want 1", value)
	}
	close(release)
	wg.Wait()
	family = gatherFamily(t, registry, "skopos_http_in_flight")
	if value := family.GetMetric()[0].GetGauge().GetValue(); value != 0 {
		t.Fatalf("in-flight after release = %v, want 0", value)
	}
}

func TestUnsupportedRecorder(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		RecordUnsupported(r.Context(), "subquery")
		w.WriteHeader(http.StatusBadRequest)
	})
	rec := httptest.NewRecorder()
	httpMetrics.Wrap(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))
	family := gatherFamily(t, registry, "skopos_unsupported_queries_total")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("unsupported series = %v", family)
	}
	labels := labelsOf(family.GetMetric()[0])
	if labels["head"] != "prom" || labels["feature"] != "subquery" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestHead(t *testing.T) {
	tests := map[string]string{
		"/api/v1/query":            "prom",
		"/api/v1/context/metrics":  "context",
		"/loki/api/v1/query_range": "loki",
		"/api/search":              "tempo",
		"/api/v2/traces/abc":       "tempo",
		"/healthz":                 "gateway",
		"/metrics":                 "gateway",
		"/api/v1/label/x/values":   "prom",
	}
	for path, want := range tests {
		if got := Head(path); got != want {
			t.Errorf("Head(%q) = %q, want %q", path, got, want)
		}
	}
	if !strings.Contains(Head("/api/v1/context"), "context") {
		t.Fatal("context prefix was not classified first")
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
	rw   *bufio.ReadWriter
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, h.rw, nil
}

type pushRecorder struct {
	*httptest.ResponseRecorder
	target string
}

func (p *pushRecorder) Push(target string, _ *http.PushOptions) error {
	p.target = target
	return nil
}

func TestStatusWriter(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: recorder}
	if _, err := writer.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if writer.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", writer.status)
	}
	writer.WriteHeader(http.StatusCreated)
	if writer.status != http.StatusOK {
		t.Fatalf("status = %d, want the first status to win", writer.status)
	}
	if writer.Unwrap() != recorder {
		t.Fatal("Unwrap did not return the original writer")
	}

	second := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	second.WriteHeader(http.StatusAccepted)
	if second.status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", second.status)
	}
}

func TestStatusWriterFlush(t *testing.T) {
	flusher := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := &statusWriter{ResponseWriter: flusher}
	writer.Flush()
	if !flusher.flushed {
		t.Fatal("Flush did not reach the underlying writer")
	}
	if writer.status != http.StatusOK {
		t.Fatalf("status = %d, want 200 after Flush", writer.status)
	}

	plain := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	plain.Flush() // must not panic when the writer is not a Flusher
}

func TestStatusWriterHijack(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	recorder := &hijackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		conn:             server,
		rw:               bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)),
	}
	writer := &statusWriter{ResponseWriter: recorder}
	if _, _, err := writer.Hijack(); err != nil {
		t.Fatalf("Hijack: %v", err)
	}
	plain := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := plain.Hijack(); err == nil {
		t.Fatal("Hijack succeeded on a non-hijackable writer")
	}
}

func TestStatusWriterPush(t *testing.T) {
	pusher := &pushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := &statusWriter{ResponseWriter: pusher}
	if err := writer.Push("/asset", nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if pusher.target != "/asset" {
		t.Fatalf("push target = %q", pusher.target)
	}
	plain := &statusWriter{ResponseWriter: httptest.NewRecorder()}
	if err := plain.Push("/asset", nil); err != http.ErrNotSupported {
		t.Fatalf("Push error = %v, want ErrNotSupported", err)
	}
}

func TestRoutePatternAndUnsupportedNoOps(t *testing.T) {
	if got := routePattern(&http.Request{}); got != "/" {
		t.Fatalf("routePattern(empty) = %q", got)
	}
	if got := routePattern(&http.Request{Pattern: "GET /api/v1/query"}); got != "/api/v1/query" {
		t.Fatalf("routePattern = %q", got)
	}
	RecordUnsupported(context.Background(), "")
	RecordUnsupported(context.Background(), "subquery")
}

func TestRecordAdmissionRejected(t *testing.T) {
	registry := NewRegistry("test")
	httpMetrics := NewHTTP(registry)
	httpMetrics.RecordAdmissionRejected("prom")
	family := gatherFamily(t, registry, "skopos_admission_rejected_total")
	if family == nil || len(family.GetMetric()) != 1 {
		t.Fatalf("admission series = %v", family)
	}
	if value := family.GetMetric()[0].GetCounter().GetValue(); value != 1 {
		t.Fatalf("admission counter = %v, want 1", value)
	}
}
