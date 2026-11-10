package httpapi

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jawaracloud/skopos/internal/auth"
)

func metricsStub(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, body)
	})
}

func TestMetrics_RouteRegistered(t *testing.T) {
	const body = "skopos_build_info{version=\"test-version\"} 1\n"
	s := newTestServer(t, fakePinger{}, func(cfg *Config) {
		cfg.Metrics = metricsStub(body)
	})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rec.Code)
	}
	if rec.Body.String() != body {
		t.Fatalf("metrics body = %q, want %q", rec.Body.String(), body)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("metrics content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("metrics cache control = %q, want no-store", got)
	}
}

func TestMetrics_RequiresAuthWhenEnabled(t *testing.T) {
	guard, err := auth.New("viewer", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, fakePinger{}, func(cfg *Config) {
		cfg.Auth = guard
		cfg.Metrics = metricsStub("skopos_build_info{version=\"test-version\"} 1\n")
	})
	h := s.Handler()

	rec := doRequest(t, h, http.MethodGet, "/metrics")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("metrics without auth status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("viewer:secret")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics with auth status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "skopos_build_info") {
		t.Fatalf("metrics body = %q", rec.Body.String())
	}
}

func TestMetrics_RejectsNonGet(t *testing.T) {
	s := newTestServer(t, fakePinger{}, func(cfg *Config) {
		cfg.Metrics = metricsStub("skopos_build_info{version=\"test-version\"} 1\n")
	})
	rec := doRequest(t, s.Handler(), http.MethodPost, "/metrics")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post metrics status = %d, want 405", rec.Code)
	}
}
