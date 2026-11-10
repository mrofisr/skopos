package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jawaracloud/skopos/internal/admission"
	"github.com/jawaracloud/skopos/internal/config"
	"github.com/jawaracloud/skopos/internal/metrics"
)

const admissionRetryAfterSeconds = "1"

type admissionMiddleware struct {
	limiters map[string]*admission.Limiter
	metrics  *metrics.HTTP
}

// newAdmissionMiddleware builds one limiter per query head. A zero in-flight
// cap disables admission entirely.
func newAdmissionMiddleware(cfg config.Config, httpMetrics *metrics.HTTP) (*admissionMiddleware, error) {
	if cfg.MaxInflight == 0 {
		return nil, nil
	}
	middleware := &admissionMiddleware{
		limiters: make(map[string]*admission.Limiter, 3),
		metrics:  httpMetrics,
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
		release, err := limiter.Acquire(r.Context())
		if err != nil {
			if errors.Is(err, admission.ErrRejected) {
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
