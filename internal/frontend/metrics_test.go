package frontend

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/auth"
)

func metricsStub(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, body)
	})
}

func newMetricsServer(t *testing.T, backends []string, guard *auth.Basic) *Server {
	t.Helper()
	s, err := New(Config{
		Backends:         backends,
		Version:          "test-version",
		ReadinessTimeout: time.Second,
		ProxyTimeout:     time.Second,
		Auth:             guard,
		Metrics:          metricsStub("skopos_build_info{version=\"test-version\"} 1\n"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestFrontendMetrics_NotProxiedToWorkers(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	s := newMetricsServer(t, []string{backend.URL}, nil)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "skopos_build_info") {
		t.Fatalf("metrics body = %q", rec.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("backend calls = %d, want 0; /metrics was proxied", calls.Load())
	}
}

func TestFrontendMetrics_RequiresAuthWhenEnabled(t *testing.T) {
	guard, err := auth.New("viewer", "secret")
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("backend received %s; /metrics must be local", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	s := newMetricsServer(t, []string{backend.URL}, guard)
	h := s.Handler()

	rec := doRequest(t, h, http.MethodGet, "/metrics", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("metrics without auth status = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("viewer:secret")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics with auth status = %d, want 200", rec.Code)
	}
}

func TestFrontendMetrics_RejectsNonGet(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("backend received %s %s; /metrics must be local", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	s := newMetricsServer(t, []string{backend.URL}, nil)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/metrics", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post metrics status = %d, want 405", rec.Code)
	}
}
