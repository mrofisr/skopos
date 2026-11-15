package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/auth"
)

type fakePinger struct {
	err   error
	delay time.Duration
}

func (f fakePinger) Ping(ctx context.Context) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func newTestServer(t *testing.T, p Pinger, opts ...func(*Config)) *Server {
	t.Helper()
	cfg := Config{
		Logger:           slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		Pinger:           p,
		Version:          "test-version",
		ReadinessTimeout: 100 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func doRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decodeBody(t, rec)["status"]; got != "ok" {
		t.Fatalf("status body = %v", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}
}

func TestReadyzOK(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if got := decodeBody(t, rec)["status"]; got != "ready" {
		t.Fatalf("status body = %v", got)
	}
}

func TestReadyzUnavailable(t *testing.T) {
	s := newTestServer(t, fakePinger{err: errors.New("clickhouse down: password=secret")})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	body := decodeBody(t, rec)
	if body["status"] != "unavailable" {
		t.Fatalf("status body = %v", body)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("readiness response leaked an internal error: %s", rec.Body)
	}
}

func TestReadyzTimeout(t *testing.T) {
	s := newTestServer(t, fakePinger{delay: time.Second})
	start := time.Now()
	rec := doRequest(t, s.Handler(), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("readiness took too long: %s", elapsed)
	}
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock() *manualClock {
	return &manualClock{now: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type countingPinger struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (p *countingPinger) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.err
}

func (p *countingPinger) setError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *countingPinger) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestReadyzCachesProbeForTTL(t *testing.T) {
	clock := newManualClock()
	pinger := &countingPinger{}
	s := newTestServer(t, pinger, func(c *Config) {
		c.Clock = clock.Now
		c.ReadinessCacheTTL = time.Second
	})
	h := s.Handler()

	if rec := doRequest(t, h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("first status = %d", rec.Code)
	}
	if rec := doRequest(t, h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("cached status = %d", rec.Code)
	}
	if got := pinger.callCount(); got != 1 {
		t.Fatalf("probe calls = %d after two requests, want 1", got)
	}

	clock.Advance(500 * time.Millisecond)
	doRequest(t, h, http.MethodGet, "/readyz")
	if got := pinger.callCount(); got != 1 {
		t.Fatalf("probe calls = %d inside the TTL, want 1", got)
	}

	pinger.setError(errors.New("clickhouse down"))
	clock.Advance(600 * time.Millisecond)
	if rec := doRequest(t, h, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expired status = %d, want 503", rec.Code)
	}
	doRequest(t, h, http.MethodGet, "/readyz")
	if got := pinger.callCount(); got != 2 {
		t.Fatalf("probe calls = %d, want 2; the failure was not cached", got)
	}

	pinger.setError(nil)
	clock.Advance(time.Second)
	if rec := doRequest(t, h, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("recovered status = %d, want 200", rec.Code)
	}
	if got := pinger.callCount(); got != 3 {
		t.Fatalf("probe calls = %d, want 3", got)
	}
}

type blockingPinger struct {
	started  chan struct{}
	release  chan struct{}
	observed chan error
}

func (p *blockingPinger) Ping(ctx context.Context) error {
	close(p.started)
	<-p.release
	p.observed <- ctx.Err()
	return nil
}

func TestReadyzProbeOutlivesCaller(t *testing.T) {
	pinger := &blockingPinger{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		observed: make(chan error, 1),
	}
	s := newTestServer(t, pinger, func(c *Config) {
		c.Clock = newManualClock().Now
		c.ReadinessCacheTTL = time.Second
	})
	h := s.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()
	<-pinger.started
	cancel()
	close(pinger.release)
	<-done

	select {
	case err := <-pinger.observed:
		if err != nil {
			t.Fatalf("probe context was canceled with the caller: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not finish")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestVersion(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	rec := doRequest(t, s.Handler(), http.MethodGet, "/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decodeBody(t, rec)["version"]; got != "test-version" {
		t.Fatalf("version = %v", got)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	s := newTestServer(t, fakePinger{})
	h := s.Handler()

	rec := doRequest(t, h, http.MethodGet, "/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d", rec.Code)
	}
	if got := decodeBody(t, rec)["error"]; got != "not found" {
		t.Fatalf("not found body = %v", got)
	}

	rec = doRequest(t, h, http.MethodPost, "/healthz")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", rec.Code)
	}
	if got := decodeBody(t, rec)["error"]; got != "method not allowed" {
		t.Fatalf("method body = %v", got)
	}

	rec = doRequest(t, h, http.MethodHead, "/healthz")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("head status = %d", rec.Code)
	}
}

func TestBasicAuth(t *testing.T) {
	guard, err := auth.New("viewer", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, fakePinger{}, func(cfg *Config) { cfg.Auth = guard })
	h := s.Handler()

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := doRequest(t, h, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s bypass status = %d", path, rec.Code)
		}
	}

	rec := doRequest(t, h, http.MethodGet, "/version")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("version without auth status = %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate")
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/query")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("query without auth status = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("viewer:secret")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("version with auth status = %d", rec.Code)
	}
}

func TestRecovery(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := recoverMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if !strings.Contains(buf.String(), "panic") {
		t.Fatalf("log = %q", buf.String())
	}
}

func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	s := newTestServer(t, fakePinger{}, func(cfg *Config) { cfg.Logger = logger })
	doRequest(t, s.Handler(), http.MethodGet, "/healthz")
	log := buf.String()
	for _, want := range []string{`"method":"GET"`, `"path":"/healthz"`, `"status":200`} {
		if !strings.Contains(log, want) {
			t.Fatalf("log %q does not contain %q", log, want)
		}
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no pinger", Config{}},
		{"negative readiness timeout", Config{Pinger: fakePinger{}, ReadinessTimeout: -time.Second}},
		{"negative readiness cache", Config{Pinger: fakePinger{}, ReadinessCacheTTL: -time.Second}},
		{"negative max body", Config{Pinger: fakePinger{}, MaxRequestBytes: -1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestHandlerWithWrapsUnderAuth(t *testing.T) {
	guard, err := auth.New("user", "pass")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	s := newTestServer(t, fakePinger{}, func(c *Config) {
		c.Auth = guard
		c.Register = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /wrapped", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		}
	})
	h := s.HandlerWith(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	})
	rec := doRequest(t, h, http.MethodGet, "/wrapped")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
	if called {
		t.Fatal("wrap ran before auth")
	}
	req := httptest.NewRequest(http.MethodGet, "/wrapped", nil)
	req.SetBasicAuth("user", "pass")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("wrap did not run for an authenticated request")
	}
}
