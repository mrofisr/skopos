package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mrofisr/skopos/internal/admission"
	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/metrics"
)

const admissionRetryAfterSeconds = "1"

type admissionMiddleware struct {
	limiters        map[string]*admission.Limiter
	metrics         *metrics.HTTP
	cost            CostModel
	maxRequestBytes int64
}

// newAdmissionMiddleware builds one limiter per query head. A zero in-flight
// cap disables admission entirely.
func newAdmissionMiddleware(cfg config.Config, httpMetrics *metrics.HTTP, cardinality ...func(context.Context, string) int) (*admissionMiddleware, error) {
	if cfg.MaxInflight == 0 {
		return nil, nil
	}
	model := CostModel{
		Enabled:        !cfg.DisableWorkWeighting,
		PointsPerSlot:  cfg.PointsPerSlot,
		RowsPerSlot:    cfg.RowsPerSlot,
		FallbackWeight: cfg.InstantFallbackWeight,
	}
	if len(cardinality) > 0 {
		model.Cardinality = cardinality[0]
	}
	middleware := &admissionMiddleware{
		limiters:        make(map[string]*admission.Limiter, 3),
		metrics:         httpMetrics,
		cost:            model,
		maxRequestBytes: cfg.MaxRequestBytes,
	}
	for _, head := range []string{"prom", "loki", "tempo"} {
		limiter, err := admission.New(cfg.MaxInflight, cfg.QueueDepth, cfg.QueueWait)
		if err != nil {
			return nil, err
		}
		middleware.limiters[head] = limiter
	}
	return middleware, nil
}

// Wrap applies admission to query heads. Probes, metrics, context, and the
// gateway routes pass through untouched.
func (a *admissionMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if admissionExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		head := metrics.Head(r.URL.Path)
		limiter, ok := a.limiters[head]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.Body != nil && a.maxRequestBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, a.maxRequestBytes)
		}
		values := r.URL.Query()
		if err := r.ParseForm(); err == nil {
			values = r.Form
		}
		release, err := limiter.AcquireWeight(r.Context(), a.cost.requestCost(r.Context(), r.URL.Path, values))
		if err != nil {
			switch {
			case errors.Is(err, admission.ErrTooHeavy):
				a.writeTooHeavy(w, head)
			case errors.Is(err, admission.ErrRejected):
				a.writeRejection(w, head)
			}
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

func admissionExempt(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return true
	default:
		return false
	}
}

// writeRejection uses each head's error shape and a Retry-After hint.
func (a *admissionMiddleware) writeRejection(w http.ResponseWriter, head string) {
	if a.metrics != nil {
		a.metrics.RecordAdmissionRejected(head)
	}
	w.Header().Set("Retry-After", admissionRetryAfterSeconds)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch head {
	case "prom":
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "error",
			"errorType": "unavailable",
			"error":     "too many concurrent requests",
		})
	case "loki":
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "error",
			"errorType": "too_many_requests",
			"error":     "too many concurrent requests",
		})
	default:
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "too many concurrent requests"})
	}
}

// writeTooHeavy returns a client error, not a retryable capacity rejection:
// waiting cannot make an over-capacity request fit.
func (a *admissionMiddleware) writeTooHeavy(w http.ResponseWriter, head string) {
	if a.metrics != nil {
		a.metrics.RecordAdmissionRejected(head)
	}
	const message = "query is too large for the admission budget; increase the step or narrow the range"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	switch head {
	case "prom", "loki":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "error",
			"errorType": "bad_data",
			"error":     message,
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
}
