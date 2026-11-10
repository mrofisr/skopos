package metrics

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTP holds the RED metrics for the gateway and query heads.
type HTTP struct {
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	inFlight    *prometheus.GaugeVec
	unsupported *prometheus.CounterVec
	admission   *prometheus.CounterVec
}

// NewHTTP registers the HTTP metrics on registry.
func NewHTTP(registry *Registry) *HTTP {
	httpMetrics := &HTTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_http_requests_total",
			Help: "HTTP requests by head, route pattern, and status code.",
		}, []string{"head", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "skopos_http_request_duration_seconds",
			Help:    "HTTP request duration by head and route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"head", "route"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "skopos_http_in_flight",
			Help: "HTTP requests currently being served by head.",
		}, []string{"head"}),
		unsupported: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_unsupported_queries_total",
			Help: "Queries rejected as unsupported by head and feature.",
		}, []string{"head", "feature"}),
		admission: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_admission_rejected_total",
			Help: "Requests rejected by the admission limiter, by head.",
		}, []string{"head"}),
	}
	registry.MustRegister(httpMetrics.requests, httpMetrics.duration, httpMetrics.inFlight,
		httpMetrics.unsupported, httpMetrics.admission)
	return httpMetrics
}

type unsupportedRecorderKey struct{}

// RecordUnsupported increments the unsupported-query counter for the request's
// head. It is a no-op outside the HTTP middleware.
func RecordUnsupported(ctx context.Context, feature string) {
	if feature == "" {
		return
	}
	if record, ok := ctx.Value(unsupportedRecorderKey{}).(func(string)); ok {
		record(feature)
	}
}

// RecordAdmissionRejected increments the admission rejection counter.
func (m *HTTP) RecordAdmissionRejected(head string) {
	m.admission.WithLabelValues(head).Inc()
}

// Wrap instruments next. The route label is the ServeMux pattern, never the
// concrete path, so high-cardinality values cannot create series.
func (m *HTTP) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := Head(r.URL.Path)
		m.inFlight.WithLabelValues(head).Inc()
		defer m.inFlight.WithLabelValues(head).Dec()

		started := time.Now()
		recorder := &statusWriter{ResponseWriter: w}
		ctx := context.WithValue(r.Context(), unsupportedRecorderKey{}, func(feature string) {
			m.unsupported.WithLabelValues(head, feature).Inc()
		})
		r = r.WithContext(ctx)

		defer func() {
			status := recorder.status
			if status == 0 {
				// A panic unwound before writing a response; the recover
				// middleware will send a 500.
				status = http.StatusInternalServerError
			}
			route := routePattern(r)
			m.requests.WithLabelValues(head, route, strconv.Itoa(status)).Inc()
			m.duration.WithLabelValues(head, route).Observe(time.Since(started).Seconds())
		}()
		next.ServeHTTP(recorder, r)
	})
}

// Head classifies a request path into the closed set of heads.
func Head(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/context"):
		return "context"
	case strings.HasPrefix(path, "/loki/"):
		return "loki"
	case strings.HasPrefix(path, "/api/v1/"):
		return "prom"
	case strings.HasPrefix(path, "/api/"):
		return "tempo"
	default:
		return "gateway"
	}
}

func routePattern(r *http.Request) string {
	route := r.Pattern
	if route == "" {
		return "/"
	}
	if index := strings.IndexByte(route, ' '); index >= 0 {
		route = route[index+1:]
	}
	return route
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the original writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush preserves streaming for handlers that assert http.Flusher.
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		flusher.Flush()
	}
}

// Hijack preserves WebSocket-style upgrades.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("metrics: response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

// Push preserves HTTP/2 server push.
func (w *statusWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}
