package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewRejectsNegativeReadinessTimeout(t *testing.T) {
	_, err := New(Config{Pinger: fakePinger{}, ReadinessTimeout: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "readiness") {
		t.Fatalf("error = %v, want a readiness error", err)
	}
}

func TestNewDefaults(t *testing.T) {
	s, err := New(Config{Pinger: fakePinger{}})
	if err != nil {
		t.Fatal(err)
	}
	if s.readinessTimeout != 2*time.Second {
		t.Fatalf("readiness timeout = %s, want 2s", s.readinessTimeout)
	}
	if s.maxRequestBytes != 1<<20 {
		t.Fatalf("max request bytes = %d, want 1MiB", s.maxRequestBytes)
	}
}

func TestLimitBodyHandlesNilBody(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	called := false
	handler := s.limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Body != nil {
			_, _ = io.ReadAll(r.Body)
		}
	}))
	request := &http.Request{Method: http.MethodPost, Body: nil, Header: http.Header{}}
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !called {
		t.Fatal("next handler was not called with a nil body")
	}
}

func TestStatusRecorderWriteDefaultsTo200(t *testing.T) {
	recorder := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, err := recorder.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if recorder.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.status)
	}
	if recorder.bytes != 2 {
		t.Fatalf("bytes = %d, want 2", recorder.bytes)
	}
}

func TestRequestIDGeneratedAndPreserved(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	first := doRequest(t, s.Handler(), http.MethodGet, "/healthz")
	generated := first.Header().Get("X-Request-ID")
	if len(generated) != 16 {
		t.Fatalf("generated request ID = %q, want 16 hex chars", generated)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "caller-supplied")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "caller-supplied" {
		t.Fatalf("request ID = %q, want the caller value", got)
	}
}

func TestLoggingRecordsStatusAndDuration(t *testing.T) {
	var logs bytes.Buffer
	s := newTestServer(t, fakePinger{}, func(cfg *Config) {
		cfg.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
		cfg.Register = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /fail", func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(2 * time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)
			})
			mux.HandleFunc("GET /empty", func(w http.ResponseWriter, r *http.Request) {})
		}
	})
	if rec := doRequest(t, s.Handler(), http.MethodGet, "/fail"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := doRequest(t, s.Handler(), http.MethodGet, "/empty"); rec.Code != http.StatusOK {
		t.Fatalf("empty status = %d", rec.Code)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2:\n%s", len(lines), logs.String())
	}
	var failed, empty map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &failed); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &empty); err != nil {
		t.Fatal(err)
	}
	if failed["level"] != "ERROR" || failed["status"] != float64(500) {
		t.Fatalf("failed log = %#v", failed)
	}
	if empty["level"] != "INFO" || empty["status"] != float64(200) {
		t.Fatalf("empty log = %#v", empty)
	}
	duration, ok := failed["duration_ms"].(float64)
	if !ok || duration <= 0 || duration >= 1000 {
		t.Fatalf("duration_ms = %v, want a millisecond value", failed["duration_ms"])
	}
}
