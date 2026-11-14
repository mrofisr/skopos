package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mrofisr/skopos/internal/semctx"
)

const lowCoverageThreshold = 0.8

// Registry is the process registry. It embeds the Prometheus registry so
// callers can register collectors and gather families, and adds the HTTP
// handler the gateway serves.
type Registry struct {
	*prometheus.Registry
}

// NewRegistry returns the process registry with the build-info gauge.
func NewRegistry(version string) *Registry {
	registry := &Registry{Registry: prometheus.NewRegistry()}
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "skopos_build_info",
		Help: "Build information for the Skopos gateway.",
	}, []string{"version"})
	build.WithLabelValues(version).Set(1)
	registry.MustRegister(build)
	return registry
}

// Handler returns the Prometheus exposition handler.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.Registry, promhttp.HandlerOpts{})
}

// contextCollector exports the gauges formerly written by semctx's
// /metrics handler.
type contextCollector struct {
	api *semctx.API

	documented *prometheus.Desc
	observed   *prometheus.Desc
	coverage   *prometheus.Desc
	low        *prometheus.Desc
	available  *prometheus.Desc
}

// NewContextCollector returns a collector for the semantic-context catalog and
// its observers.
func NewContextCollector(api *semctx.API) prometheus.Collector {
	return &contextCollector{
		api:        api,
		documented: prometheus.NewDesc("skopos_context_documented", "Number of documented catalog entries.", []string{"signal"}, nil),
		observed:   prometheus.NewDesc("skopos_context_observed", "Number of observed catalog entries.", []string{"signal"}, nil),
		coverage:   prometheus.NewDesc("skopos_context_coverage_ratio", "Documented metrics divided by observed metrics, capped at 1.", []string{"signal"}, nil),
		low:        prometheus.NewDesc("skopos_context_low_coverage", "1 when observed entries exist and coverage is below 0.8.", []string{"signal"}, nil),
		available:  prometheus.NewDesc("skopos_context_observed_available", "1 when observed entries are available, 0 when the observer failed.", []string{"signal"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *contextCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.documented
	ch <- c.observed
	ch <- c.coverage
	ch <- c.low
	ch <- c.available
}

// Collect implements prometheus.Collector.
func (c *contextCollector) Collect(ch chan<- prometheus.Metric) {
	documented := map[string]int{
		"metrics":  len(c.api.Catalog.Metrics),
		"services": len(c.api.Catalog.Services),
		"logs":     len(c.api.Catalog.Logs),
		"traces":   len(c.api.Catalog.Traces),
	}
	for _, signal := range []string{"metrics", "services", "logs", "traces"} {
		ch <- prometheus.MustNewConstMetric(c.documented, prometheus.GaugeValue, float64(documented[signal]), signal)
	}

	if c.api.Observer != nil {
		observed, err := c.api.Observer.ListObserved(context.Background())
		if err != nil {
			ch <- prometheus.MustNewConstMetric(c.available, prometheus.GaugeValue, 0, "metrics")
		} else {
			count := len(observed)
			ch <- prometheus.MustNewConstMetric(c.observed, prometheus.GaugeValue, float64(count), "metrics")
			ratio := 0.0
			if count > 0 {
				ratio = float64(documented["metrics"]) / float64(count)
				if ratio > 1 {
					ratio = 1
				}
			}
			ch <- prometheus.MustNewConstMetric(c.coverage, prometheus.GaugeValue, ratio, "metrics")
			ch <- prometheus.MustNewConstMetric(c.low, prometheus.GaugeValue, lowCoverage(ratio, count), "metrics")
		}
	}

	if c.api.SignalObserver == nil {
		return
	}
	logs, logsErr := c.api.SignalObserver.ListObservedLogs(context.Background())
	if logsErr != nil {
		ch <- prometheus.MustNewConstMetric(c.available, prometheus.GaugeValue, 0, "logs")
	} else {
		count := len(logs)
		ch <- prometheus.MustNewConstMetric(c.observed, prometheus.GaugeValue, float64(count), "logs")
		ratio := 0.0
		if count > 0 {
			ratio = float64(documented["logs"]) / float64(count)
		}
		ch <- prometheus.MustNewConstMetric(c.low, prometheus.GaugeValue, lowCoverage(ratio, count), "logs")
	}
	traces, tracesErr := c.api.SignalObserver.ListObservedTraces(context.Background())
	if tracesErr != nil {
		ch <- prometheus.MustNewConstMetric(c.available, prometheus.GaugeValue, 0, "traces")
	} else {
		count := len(traces)
		ch <- prometheus.MustNewConstMetric(c.observed, prometheus.GaugeValue, float64(count), "traces")
		ratio := 0.0
		if count > 0 {
			ratio = float64(documented["traces"]) / float64(count)
		}
		ch <- prometheus.MustNewConstMetric(c.low, prometheus.GaugeValue, lowCoverage(ratio, count), "traces")
	}
}

func lowCoverage(ratio float64, observed int) float64 {
	if observed > 0 && ratio < lowCoverageThreshold {
		return 1
	}
	return 0
}
