// Package frontend implements SKOPOS_MODE=frontend: a small public reverse
// proxy that health-checks and forwards query traffic to one or more query
// workers. It never talks to ClickHouse directly.
package frontend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrofisr/skopos/internal/auth"
)

const (
	defaultProxyTimeout = 30 * time.Second
	// proxyTimeoutSlack leaves the worker time to send its own 503 timeout
	// response before the proxy gives up.
	proxyTimeoutSlack = 2 * time.Second
)

// Config configures the frontend proxy.
type Config struct {
	Logger           *slog.Logger
	Backends         []string
	Version          string
	ReadinessTimeout time.Duration
	// QueryTimeout is the worker's query budget. ProxyTimeout must exceed it
	// so the worker's own timeout response reaches the client.
	QueryTimeout    time.Duration
	ProxyTimeout    time.Duration
	MaxRequestBytes int64
	Auth            *auth.Basic
	Transport       http.RoundTripper
	// Metrics serves the local Prometheus registry. It is never proxied.
	Metrics http.Handler
}

type backend struct {
	baseURL *url.URL
	ready   atomic.Bool
}

// Server is the frontend HTTP handler.
type Server struct {
	logger           *slog.Logger
	backends         []*backend
	version          string
	readinessTimeout time.Duration
	proxyTimeout     time.Duration
	maxRequestBytes  int64
	auth             *auth.Basic
	client           *http.Client
	next             atomic.Uint64
	metrics          http.Handler
}

// New validates cfg and constructs a frontend server.
func New(cfg Config) (*Server, error) {
	if len(cfg.Backends) == 0 {
		return nil, errors.New("frontend: at least one query backend is required")
	}
	if cfg.ReadinessTimeout < 0 {
		return nil, errors.New("frontend: readiness timeout must not be negative")
	}
	if cfg.QueryTimeout < 0 {
		return nil, errors.New("frontend: query timeout must not be negative")
	}
	if cfg.ProxyTimeout < 0 {
		return nil, errors.New("frontend: proxy timeout must not be negative")
	}
	if cfg.QueryTimeout > 0 && cfg.ProxyTimeout > 0 && cfg.ProxyTimeout <= cfg.QueryTimeout {
		return nil, fmt.Errorf(
			"frontend: proxy timeout %s must exceed query timeout %s",
			cfg.ProxyTimeout, cfg.QueryTimeout)
	}
	if cfg.MaxRequestBytes < 0 {
		return nil, errors.New("frontend: max request bytes must not be negative")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.ReadinessTimeout == 0 {
		cfg.ReadinessTimeout = 2 * time.Second
	}
	if cfg.ProxyTimeout == 0 {
		if cfg.QueryTimeout > 0 {
			cfg.ProxyTimeout = cfg.QueryTimeout + proxyTimeoutSlack
		} else {
			cfg.ProxyTimeout = defaultProxyTimeout
		}
	}
	if cfg.MaxRequestBytes == 0 {
		cfg.MaxRequestBytes = 1 << 20
	}

	backends := make([]*backend, 0, len(cfg.Backends))
	for _, raw := range cfg.Backends {
		u, err := parseBaseURL(raw)
		if err != nil {
			return nil, err
		}
		b := &backend{baseURL: u}
		// Assume a backend is ready until a probe says otherwise. This avoids
		// rejecting traffic before Kubernetes has called /readyz.
		b.ready.Store(true)
		backends = append(backends, b)
	}

	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Server{
		logger:           cfg.Logger,
		backends:         backends,
		version:          cfg.Version,
		readinessTimeout: cfg.ReadinessTimeout,
		proxyTimeout:     cfg.ProxyTimeout,
		maxRequestBytes:  cfg.MaxRequestBytes,
		auth:             cfg.Auth,
		metrics:          cfg.Metrics,
		client: &http.Client{
			Transport: transport,
			// A proxy must not follow redirects; the client sees them.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("frontend: invalid query URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("frontend: query URL %q must use http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("frontend: query URL %q has no host", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

// Handler returns the frontend HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /version", s.versionHandler)
	if s.metrics != nil {
		mux.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			s.metrics.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/", s.proxy)

	known := map[string]struct{}{
		"/healthz": {},
		"/readyz":  {},
		"/version": {},
	}
	if s.metrics != nil {
		known["/metrics"] = struct{}{}
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if _, ok := known[r.URL.Path]; ok {
				writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})

	var h http.Handler = router
	if s.auth != nil {
		h = s.auth.Wrap(h, "/healthz", "/readyz")
	}
	h = s.limitBody(h)
	h = s.securityHeaders(h)
	h = recoverMiddleware(s.logger, h)
	h = s.logging(h)
	return h
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.readinessTimeout)
	defer cancel()

	ready := make([]bool, len(s.backends))
	var wg sync.WaitGroup
	for i, b := range s.backends {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready[i] = s.probe(ctx, b)
		}()
	}
	wg.Wait()

	count := 0
	for _, ok := range ready {
		if ok {
			count++
		}
	}
	body := map[string]any{
		"status":         "unavailable",
		"ready_backends": count,
		"backends":       len(s.backends),
	}
	if count == 0 {
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}
	body["status"] = "ready"
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) probe(ctx context.Context, b *backend) bool {
	target := *b.baseURL
	target.Path = joinPath(b.baseURL.Path, "/readyz")
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		b.ready.Store(false)
		s.logger.Debug("query backend readiness request failed", "backend", b.baseURL.Redacted(), "error", err)
		return false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		b.ready.Store(false)
		s.logger.Debug("query backend readiness probe failed", "backend", b.baseURL.Redacted(), "error", err)
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	ok := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	b.ready.Store(ok)
	if !ok {
		s.logger.Debug("query backend not ready", "backend", b.baseURL.Redacted(), "status", resp.StatusCode)
	}
	return ok
}

func (s *Server) versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request) {
	b := s.pick()
	if b == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no ready query backends"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.proxyTimeout)
	defer cancel()

	resp, bodyErr, err := s.forward(ctx, r, b)
	if err != nil && backendFailure(r, err, bodyErr) {
		b.ready.Store(false)
		// A GET has no side effects, so one retry on another ready backend
		// turns a lost connection into a successful response. Do not replay
		// a request with a body.
		if canRetry(r) {
			if next := s.pickExcept(b); next != nil {
				b = next
				resp, bodyErr, err = s.forward(ctx, r, b)
				if err != nil && backendFailure(r, err, bodyErr) {
					b.ready.Store(false)
				}
			}
		}
	}
	if err != nil {
		s.logger.Warn("query backend request failed", "backend", b.baseURL.Redacted(), "error", err)
		msg := "query backend request failed"
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "query backend timed out"
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":   msg,
			"backend": b.baseURL.Redacted(),
		})
		return
	}
	defer resp.Body.Close()

	b.ready.Store(true)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.Warn("copy query backend response failed", "backend", b.baseURL.Redacted(), "error", err)
	}
}

// forward builds and sends one backend request. bodyErr reports a failure
// while reading the client's request body, which says nothing about backend
// health.
func (s *Server) forward(ctx context.Context, r *http.Request, b *backend) (*http.Response, error, error) {
	target := *b.baseURL
	target.Path = joinPath(b.baseURL.Path, r.URL.Path)
	if r.URL.RawPath != "" || b.baseURL.RawPath != "" {
		target.RawPath = joinPath(b.baseURL.EscapedPath(), r.URL.EscapedPath())
	}
	target.RawQuery = joinQuery(b.baseURL.RawQuery, r.URL.RawQuery)
	target.Fragment = ""

	var body io.ReadCloser
	var tracked *trackedBody
	if r.Body != nil && r.Body != http.NoBody {
		tracked = &trackedBody{ReadCloser: r.Body}
		body = tracked
	}
	out, err := http.NewRequestWithContext(ctx, r.Method, target.String(), body)
	if err != nil {
		return nil, nil, err
	}
	out.ContentLength = r.ContentLength
	out.Host = r.Host
	out.Header = cloneHeader(r.Header)
	addForwardedHeaders(out.Header, r)

	resp, err := s.client.Do(out)
	if tracked != nil {
		return resp, tracked.readErr(), err
	}
	return resp, nil, err
}

// trackedBody records a client-side read error so it can be told apart from a
// backend transport failure.
type trackedBody struct {
	io.ReadCloser
	mu  sync.Mutex
	err error
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.mu.Lock()
		if b.err == nil {
			b.err = err
		}
		b.mu.Unlock()
	}
	return n, err
}

func (b *trackedBody) readErr() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// backendFailure reports whether err indicates the backend itself failed.
// Timeouts and client body errors say nothing about backend health.
func backendFailure(r *http.Request, err, bodyErr error) bool {
	if err == nil || bodyErr != nil {
		return false
	}
	if r.Context().Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	return true
}

func canRetry(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return r.ContentLength <= 0
}

// pick returns the next ready backend in round-robin order, or nil when none
// are ready.
func (s *Server) pick() *backend {
	return s.pickExcept(nil)
}

func (s *Server) pickExcept(exclude *backend) *backend {
	n := len(s.backends)
	start := int((s.next.Add(1) - 1) % uint64(n))
	for i := 0; i < n; i++ {
		b := s.backends[(start+i)%n]
		if b != exclude && b.ready.Load() {
			return b
		}
	}
	return nil
}

func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the original writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush preserves streaming for handlers that assert http.Flusher.
func (r *statusRecorder) Flush() {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack preserves WebSocket-style upgrades.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Push preserves HTTP/2 server push.
func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := r.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

// ReadFrom keeps the sendfile path when the underlying writer supports it.
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		r.bytes += int(n)
		return n, err
	}
	return io.Copy(writerOnly{r}, src)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
			"remote", r.RemoteAddr,
			"response_bytes", rec.bytes,
		}
		if rec.status >= http.StatusInternalServerError {
			s.logger.Error("request", attrs...)
		} else {
			s.logger.Info("request", attrs...)
		}
	})
}

type recoveryWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recoveryWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveryWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the original writer.
func (w *recoveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush preserves streaming for handlers that assert http.Flusher.
func (w *recoveryWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack preserves WebSocket-style upgrades. A successful hijack marks the
// response as started so a later panic does not try to write JSON.
func (w *recoveryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		w.wroteHeader = true
	}
	return conn, rw, err
}

// Push preserves HTTP/2 server push.
func (w *recoveryWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

// ReadFrom keeps the sendfile path when the underlying writer supports it.
func (w *recoveryWriter) ReadFrom(src io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(src)
	}
	return io.Copy(writerOnly{w}, src)
}

// writerOnly hides ReadFrom from io.Copy so the fallback path stays here.
type writerOnly struct{ io.Writer }

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recoveryWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic recovered", "panic", recovered, "stack", string(debug.Stack()))
				if !rw.wroteHeader {
					writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func cloneHeader(h http.Header) http.Header {
	out := h.Clone()
	if out == nil {
		out = make(http.Header)
	}
	for _, connection := range h.Values("Connection") {
		for _, name := range strings.Split(connection, ",") {
			out.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		out.Del(name)
	}
	return out
}

func copyHeader(dst, src http.Header) {
	for name, values := range cloneHeader(src) {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func addForwardedHeaders(h http.Header, r *http.Request) {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if prior := h.Values("X-Forwarded-For"); len(prior) > 0 {
			h.Set("X-Forwarded-For", strings.Join(prior, ", ")+", "+host)
		} else {
			h.Set("X-Forwarded-For", host)
		}
	}
	if h.Get("X-Forwarded-Host") == "" && r.Host != "" {
		h.Set("X-Forwarded-Host", r.Host)
	}
	if h.Get("X-Forwarded-Proto") == "" {
		if r.TLS != nil {
			h.Set("X-Forwarded-Proto", "https")
		} else {
			h.Set("X-Forwarded-Proto", "http")
		}
	}
}

func joinPath(base, request string) string {
	switch {
	case base == "":
		if request == "" {
			return "/"
		}
		return request
	case request == "":
		return base
	default:
		return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(request, "/")
	}
}

func joinQuery(base, request string) string {
	switch {
	case base == "":
		return request
	case request == "":
		return base
	default:
		return base + "&" + request
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(value)
}
