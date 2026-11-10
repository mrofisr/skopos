# Metrics

`GET /metrics` is served by the gateway's shared Prometheus registry. In
distributed mode the frontend serves its own registry and never proxies the
scrape to a query worker. Basic auth applies when it is enabled.

## HTTP metrics

| Metric | Labels | Meaning |
| --- | --- | --- |
| `skopos_http_requests_total` | `head`, `route`, `code` | Requests by query head, `ServeMux` pattern, and status code. |
| `skopos_http_request_duration_seconds` | `head`, `route` | Request duration histogram. |
| `skopos_http_in_flight` | `head` | Requests currently being served. |
| `skopos_unsupported_queries_total` | `head`, `feature` | Queries rejected as unsupported. |

`head` is one of `prom`, `loki`, `tempo`, `context`, or `gateway`. The route is
always the registered pattern, never the concrete path: `/api/v1/label/foo/values`
is recorded as `/api/v1/label/{name}/values`, and every unrouted path is `/`.
That keeps unknown paths from growing the series count.

The unsupported features come from a closed set:

- Prometheus: `subquery`, `function`, `aggregation`, `unsupported`.
- Tempo: `structural_operator`, `multiple_spansets`, `nested_spansets`,
  `pipeline`, `metrics_query`, `grouping`.

A recovered panic is recorded as status `500`.

## ClickHouse metrics

| Metric | Labels | Meaning |
| --- | --- | --- |
| `skopos_clickhouse_queries_total` | `head`, `result` | Queries by head and `ok`/`error`. |
| `skopos_clickhouse_query_duration_seconds` | `head` | Query duration histogram. |
| `skopos_clickhouse_read_rows_total` | `head` | Rows read, from `X-ClickHouse-Summary`. |
| `skopos_clickhouse_read_bytes_total` | `head` | Bytes read, from the same header. |

A missing or malformed summary header counts zero rows and is logged at debug;
it never fails the query.

## Context gauges

The registry also merges the semantic-context gauges:

- `skopos_context_documented{signal}`
- `skopos_context_observed{signal}`
- `skopos_context_coverage_ratio{signal}`
- `skopos_context_low_coverage{signal}`
- `skopos_context_observed_available{signal}` (0 when an observer fails)

## Build info

`skopos_build_info{version}` is always present and set to 1.

ClickHouse accounting metrics and the optional ServiceMonitor are follow-up
tickets (AX-12 to AX-14).
