// Package loki implements the Loki-compatible log query head (M5).
//
// The in-house LogQL parser supports stream selectors, line filters, the json
// and logfmt parser stages, label filters, and the count_over_time and rate
// metric queries with sum / sum by aggregation. It also supports the constant
// vector expressions Grafana's Loki datasource health check uses, such as
// vector(1)+vector(1). Unsupported syntax is rejected at parse time. No code
// is copied from Loki (AGPL-3.0); see docs/logql-support.md for the published
// subset.
package loki
