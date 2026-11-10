package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jawaracloud/skopos/internal/auth"
)

func TestProfileDisabledByDefault(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/debug/pprof/")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestProfileEnabled(t *testing.T) {
	s := newTestServer(t, fakePinger{}, func(cfg *Config) { cfg.Profile = true })
	rec := doRequest(t, s.Handler(), http.MethodGet, "/debug/pprof/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestProfileRequiresAuth(t *testing.T) {
	guard, err := auth.New("viewer", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, fakePinger{}, func(cfg *Config) {
		cfg.Profile = true
		cfg.Auth = guard
	})
	h := s.Handler()
	if rec := doRequest(t, h, http.MethodGet, "/debug/pprof/"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d, want 401", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("viewer:secret")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with auth = %d, want 200", rec.Code)
	}
}
