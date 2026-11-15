// Package httpapi implements the gateway's HTTP surface. In M2 it serves only
// health, readiness, and version endpoints; the Prometheus, Loki, and Tempo
// heads are added in later milestones.
package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime/debug"
	"sync"
	"time"

	"github.com/mrofisr/skopos/internal/auth"
)

// Pinger checks backend readiness. The ClickHouse client implements it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Clock returns the current time. Tests inject a fake clock.
type Clock func() time.Time

const defaultReadinessCacheTTL = time.Second

// Config configures the HTTP server.
type Config struct {
	Logger           *slog.Logger
	Pinger           Pinger
	Version          string
	ReadinessTimeout time.Duration
	// ReadinessCacheTTL bounds how long a /readyz probe result is reused.
	// Zero uses one second.
	ReadinessCacheTTL time.Duration
	// Clock defaults to time.Now.
	Clock           Clock
	MaxRequestBytes int64
	// Auth optionally protects every route except /healthz and /readyz.
	Auth *auth.Basic
	// Register adds the query-head routes to the server mux. It is called
	// before the JSON 404 catch-all.
	Register func(*http.ServeMux)
	// Metrics serves the Prometheus registry. Nil disables the /metrics route.
	Metrics http.Handler
	// Profile enables the auth-protected /debug/pprof routes.
	Profile bool
	// ProfileAllowUnauth permits Profile without Auth. Prefer auth; this is
	// for local and benchmark stacks.
	ProfileAllowUnauth bool
}

// Server owns the gateway handlers.
type Server struct {
	logger            *slog.Logger
	pinger            Pinger
	version           string
	readinessTimeout  time.Duration
	readinessCacheTTL time.Duration
	clock             Clock
	readinessMu       sync.Mutex
	readinessChecked  time.Time
	readinessErr      error
	maxRequestBytes   int64
	auth              *auth.Basic
	register          func(*http.ServeMux)
	metrics           http.Handler
	profile           bool
}

// New validates cfg and returns a server.
func New(cfg Config) (*Server, error) {
	if cfg.Pinger == nil {
		return nil, errors.New("httpapi: pinger is required")
	}
	if cfg.ReadinessTimeout < 0 {
		return nil, errors.New("httpapi: readiness timeout must not be negative")
	}
	if cfg.ReadinessCacheTTL < 0 {
		return nil, errors.New("httpapi: readiness cache TTL must not be negative")
	}
	if cfg.MaxRequestBytes < 0 {
		return nil, errors.New("httpapi: max request bytes must not be negative")
	}
	if cfg.Profile && cfg.Auth == nil && !cfg.ProfileAllowUnauth {
		return nil, errors.New("httpapi: profile requires auth or an explicit ProfileAllowUnauth opt-in")
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
	if cfg.ReadinessCacheTTL == 0 {
		cfg.ReadinessCacheTTL = defaultReadinessCacheTTL
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.MaxRequestBytes == 0 {
		cfg.MaxRequestBytes = 1 << 20
	}
	return &Server{
		logger:            cfg.Logger,
		pinger:            cfg.Pinger,
		version:           cfg.Version,
		readinessTimeout:  cfg.ReadinessTimeout,
		readinessCacheTTL: cfg.ReadinessCacheTTL,
		clock:             cfg.Clock,
		maxRequestBytes:   cfg.MaxRequestBytes,
		auth:              cfg.Auth,
		register:          cfg.Register,
		metrics:           cfg.Metrics,
		profile:           cfg.Profile,
	}, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.HandlerWith(nil)
}

// HandlerWith inserts wrap between the routes and the auth guard, so
// authentication is evaluated before wrap. A nil wrap is the normal handler.
func (s *Server) HandlerWith(wrap func(http.Handler) http.Handler) http.Handler {
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
	if s.profile {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
		for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
			mux.Handle("GET /debug/pprof/"+name, pprof.Handler(name))
		}
	}
	if s.register != nil {
		s.register(mux)
	}
	mux.HandleFunc("/", s.notFound)

	known := map[string]struct{}{
		"/healthz": {},
		"/readyz":  {},
		"/version": {},
	}
	if s.metrics != nil {
		known["/metrics"] = struct{}{}
	}
	if s.profile {
		known["/debug/pprof/"] = struct{}{}
		for _, name := range []string{"allocs", "block", "cmdline", "goroutine", "heap", "mutex", "profile", "symbol", "threadcreate", "trace"} {
			known["/debug/pprof/"+name] = struct{}{}
		}
	}
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if _, ok := known[r.URL.Path]; ok {
				s.methodNotAllowed(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})

	var h http.Handler = router
	if wrap != nil {
		h = wrap(h)
	}
	if s.auth != nil {
		h = s.auth.Wrap(h, "/healthz", "/readyz")
	}
	h = s.limitBody(h)
	h = s.securityHeaders(h)
	h = recoverMiddleware(s.logger, h)
	h = s.logging(h)
	return h
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.readiness(r.Context()); err != nil {
		s.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// readiness reuses one probe result for ReadinessCacheTTL, so a Kubernetes
// probe and a frontend fan-out cannot multiply ClickHouse pings. The probe is
// detached from the caller's cancellation: a disconnected probe still fills
// the cache, and the probe is bounded by ReadinessTimeout.
func (s *Server) readiness(ctx context.Context) error {
	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()
	if !s.readinessChecked.IsZero() {
		if age := s.clock().Sub(s.readinessChecked); age >= 0 && age < s.readinessCacheTTL {
			return s.readinessErr
		}
	}
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.readinessTimeout)
	defer cancel()
	err := s.pinger.Ping(probeCtx)
	s.readinessChecked = s.clock()
	s.readinessErr = err
	return err
}

func (s *Server) versionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.version})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (s *Server) methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
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
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
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
			"request_id", requestID,
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

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(value)
}
