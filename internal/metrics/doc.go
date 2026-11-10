// Package metrics owns the process-wide Prometheus registry and the collector
// that exports the semantic-context gauges. The gateway serves this registry at
// /metrics; the semctx package no longer registers its own route.
package metrics
