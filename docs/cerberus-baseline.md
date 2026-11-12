# Cerberus three-way baseline

Recorded by the Python three-way harness on 2026-11-09, before the harness was
retired in the Python removal. This page preserves the evidence; the harness is
no longer in the tree.

## Pinned references

| Component | Pin |
| --- | --- |
| cerberus | `ghcr.io/tsouza/cerberus:v1.23.1@sha256:e5ced00b1ee85c371295147ed0bce9caa4a5c5554443ee8aabc329d5351dc4a5` |
| ClickHouse | `clickhouse/clickhouse-server:26.8.36.3@sha256:d426d2f612e1e189c90f9a3f8dcd85a03c6851273833817a296bb327e63aa7a7` |
| Prometheus | `prom/prometheus:v3.15.1@sha256:fa3302279710be2f21a8301ec17a8b4f2301fd829bd113839b3b6b02b6f191a2` |
| Loki (recorded) | `grafana/loki:3.5.7` |
| Tempo (recorded) | `grafana/tempo:2.7.2` |

## Result

```text
summary: 160 cases; 103 pass; 57 known cerberus gaps; 0 cerberus regressions; 0 fixed baseline entries; 0 Skopos divergences
ok: Skopos matches the reference on every corpus case
```

The gate was zero for Skopos against the reference, and zero against cerberus on
every case where cerberus implements the feature. An earlier run over a smaller
corpus reported 124 checks and 39 cerberus-only gaps; the numbers below are the
final 2026-11-09 run.

## Corpus

| Signal | Cases | Source |
| --- | ---: | --- |
| PromQL | 44 (36 instant, 8 range) | `test/compat/cerberus/metrics.json` |
| LogQL | 19 | `test/corpus/logql/corpus.json` |
| TraceQL search | 83 | `test/corpus/traceql/corpus.json` |
| TraceQL tag scopes | 5 scopes in 1 check | same corpus |
| TraceQL tag values | 10 | same corpus |
| TraceQL trace-by-id | 3 | same corpus |
| **Total** | **160 checks** | |

The PromQL fixture was rendered from `metrics.json` into two identical datasets:
rows in the OTel ClickHouse tables and an OpenMetrics file imported into a real
Prometheus TSDB. The corpus used the fixed base time `2026-11-01T00:00:00Z`.

Log entries modelled Loki stream labels as OTel resource attributes
(`service.name`, `severity_text`) and also filled the exporter's `ServiceName`
and `SeverityText` columns.

Trace spans came from `test/corpus/traceql` (3 traces, 7 spans) and were inserted
with the corpus package's own `ClickHouseRows` renderer.

## Known cerberus gaps (57 checks)

Every gap was traced to cerberus behavior, not to Skopos or the corpus.

| Root cause | Checks |
| --- | ---: |
| PromQL summary metrics are not read | 4 |
| PromQL range rate drops the `+Inf` histogram bucket | 1 |
| LogQL range step alignment differs from Loki | 1 |
| LogQL constant vectors return no data | 1 |
| TraceQL search summary uses the matched span, not the full trace | 20 |
| TraceQL `event:timeSinceStart` is unsupported | 1 |
| TraceQL unsupported-construct error parity (HTTP 200 instead of 400) | 23 |
| TraceQL tag-value discovery is incomplete | 5 |
| Trace-by-id omits events and links | 1 |

Presentation-only differences that were normalized away, not counted as gaps:
per-span `name` and `serviceStats` in search summaries, extra intrinsic tag
aliases, tag-scope ordering, and base64/enum encodings in trace-by-id.

## What remains

`test/compat/cerberus/seed` still renders the chart schema and seeds the
three-signal corpus, and its Go tests cover the corpus and schema rendering. The
comparison itself was not ported; it is a post-alpha candidate.
