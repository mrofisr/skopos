# Cerberus corpus fixture

This directory keeps the deterministic three-signal fixture that the retired
three-way harness used. The harness itself and its Python implementation are
gone; see [`docs/cerberus-baseline.md`](../../../docs/cerberus-baseline.md) for
the recorded 2026-11-09 result against Prometheus, Loki, Tempo, and cerberus
v1.23.1.

## What remains

| File | Purpose |
| --- | --- |
| `seed/main.go` | Renders the chart schema, seeds logs, traces, and metrics, and writes the Prometheus OpenMetrics fixture. |
| `seed/main_test.go` | Go tests for chart-schema rendering and corpus loading. |
| `metrics.json` | Deterministic metric fixture (gauges, counter, histogram, summary). |

The seed reuses `test/corpus/logql/corpus.json` and `test/corpus/traceql`.

## Run the seed

The seed writes to a ClickHouse instance and, when asked, an OpenMetrics file:

```sh
go run ./test/compat/cerberus/seed \
  --clickhouse-url http://127.0.0.1:18131 \
  --prometheus-openmetrics /tmp/cerberus.om
```

The default schema is `deploy/helm/skopos/files/schema.sql`, rendered with the
chart's local-storage defaults. Pass `--schema` to use another file.

The seed is a manual fixture generator, not a gate. The CI job named
`cerberus three-way differential` runs the Go tests:

```sh
go test ./test/compat/cerberus/... -count=1
```

## Corpus

| Signal | Cases | Source |
| --- | ---: | --- |
| PromQL | 44 (36 instant, 8 range) | `metrics.json` |
| LogQL | 19 | `test/corpus/logql/corpus.json` |
| TraceQL | 3 traces / 7 spans | `test/corpus/traceql/corpus.json` |

PromQL covers selectors and regex matchers, `rate`/`increase`, classic
`histogram_quantile`, histogram `_count`/`_sum`, summary quantiles/count/sum,
aggregations, binary and set operators, vector matching with `group_left`,
`label_replace`, `absent`, `*_over_time`, `sort_desc`, `round`, `clamp`, and
the same shapes over ranges.

The metrics fixture is rendered into two identical datasets: rows in the OTel
ClickHouse tables (`otel_metrics_gauge`, `otel_metrics_sum`,
`otel_metrics_histogram`, `otel_metrics_summary`) and an OpenMetrics file for a
Prometheus TSDB. The corpus uses the fixed base time `2026-11-01T00:00:00Z`. The
OTel histogram `BucketCounts` column is per-bucket; the OpenMetrics writer
accumulates it into Prometheus `_bucket` series.

Log entries model Loki stream labels as OTel resource attributes
(`service.name`, `severity_text`) and also fill the exporter's `ServiceName` and
`SeverityText` columns.

Trace spans come from `test/corpus/traceql` and are inserted with the corpus
package's own `ClickHouseRows` renderer.
