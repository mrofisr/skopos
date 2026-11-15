# Support matrix (v0.1 alpha)

Alpha means no compatibility guarantee. The table below is the public list of
what is implemented. Anything not listed must return an explicit error, not an
empty or incorrect result.

## Signals

| Signal | Ingest | Storage | Query API | Status |
| --- | --- | --- | --- | --- |
| Metrics | OBI or OTLP clients -> Collector -> ClickHouse | `otel_metrics_gauge`, `_sum`, `_histogram`, `_exponential_histogram`, `_summary`; optional S3 cold tier | Prometheus-compatible (M3) | Alpha |
| Traces | OBI or OTLP clients -> Collector -> ClickHouse | `otel_traces`, `otel_traces_trace_id_ts`; optional S3 cold tier | Tempo-compatible (M4) | Alpha |
| Logs | OTLP clients -> Collector -> ClickHouse; filelog receiver not bundled | `otel_logs`; optional S3 cold tier | Loki-compatible (M5) | Alpha |

OBI does not emit logs. The default chart accepts logs through the Collector's
OTLP receiver. A manual k3d smoke test sent traces, metrics, and logs through
the Collector and read them back from ClickHouse; this is not yet an automated
chart test. A filelog receiver is not bundled; enabling one requires verifying
the receiver component and its maturity first.

## Query endpoints

| Endpoint group | Status | Support list |
| --- | --- | --- |
| Prometheus `/api/v1/*` | Implemented alpha | [promql-support.md](promql-support.md) |
| Tempo `/api/*` and `/api/v2/*` | Implemented alpha | [traceql-support.md](traceql-support.md) |
| Loki `/loki/api/v1/*` | Implemented alpha | [logql-support.md](logql-support.md) |
| Health `/healthz`, `/readyz`, `/version` | Implemented | — |
| Context `/api/v1/context/*` | Implemented alpha (optional catalog; metrics, services, logs, traces, coverage/alerts, discovery, observed metadata, suggestions) | [ai-context.md](ai-context.md) |
| Context metrics `/metrics` | Implemented alpha (Prometheus text gauges; auth applies) | [ai-context.md](ai-context.md) |
| MCP server | Implemented alpha (`SKOPOS_MODE=mcp`, stdio and HTTP sessions/SSE) | [ai-context.md](ai-context.md#mcp-server) |

The gateway exposes all three APIs on one public port. The Grafana datasource
example points all three datasource types at that service.

## Deployment modes

| Mode | Status | Notes |
| --- | --- | --- |
| Single process (`gateway.mode=all`) | Default | One Deployment serves all three APIs and connects to ClickHouse. |
| systemd (`deploy/systemd/`) | Alpha | Same binary and schema under systemd; supports single-process or frontend/query split units. No Kubernetes policy/HPA. |
| Distributed read path (`distributed.enabled=true`) | Alpha | Public frontend proxies to internal stateless query workers. The frontend has no ClickHouse credentials. |
| Tiered S3 retention (`clickhouse.storage.enabled=true`) | Alpha | Hot local parts move to an S3-compatible cold volume; cold parts stay queryable. |
| Scheduled backups (`clickhouse.backup.enabled=true`) | Alpha | `BACKUP DATABASE ... TO S3(...)` CronJob to a separate prefix. Optional restore drill restores the latest backup into a temporary database; optional lifecycle CronJob applies backup retention. Backups are restore-only, not queryable through the gateway. |

ClickHouse remains the backend and owns merges/compaction. There is no Skopos
ingester or Thanos-style compactor. See [storage.md](storage.md) and
[ADR 0002](adr/0002-distributed-read-path.md).

## Language support

- **PromQL:** selectors, matchers, `rate`/`irate`/`increase` with boundary
  extrapolation and counter resets, range aggregations, `histogram_quantile`,
  math, set operators (`and`/`or`/`unless`), `group_left`/`group_right`,
  `topk`/`bottomk`/`count_values`, `absent`/`absent_over_time`,
  `label_replace`/`label_join`, scalar/vector and vector/vector binary
  operators, `offset`, and summary metrics. Exponential histograms return an
  explicit `bad_data` error. The PromQL differential oracle exercises 212
  instant/range cases against the official Prometheus engine; it is not the
  full Prometheus compliance tester.
- **TraceQL:** one spanset with `&&`/`||`, resource/span/event/link/
  instrumentation scopes, trace/span intrinsics, tag search, tags and tag
  values, trace by ID in OTLP JSON or protobuf (v2 uses the Tempo
  `TraceByIDResponse` envelope), and search `spanSets`. The 55-case corpus is
  compared with Tempo 2.7.2 for the supported subset.
- **LogQL:** stream selectors, line filters, `| json`, `| logfmt`, label
  filters, and `count_over_time`/`rate` with `sum`/`sum by`. Selector predicates
  and body line filters are pushed into ClickHouse: `|=`/`!=` use
  `position(Body, ?)`; `|~`/`!~` use `match(Body, ?)` and can use the
  case-sensitive `idx_body` text index. Go still evaluates every returned row.
  The 11-case corpus is compared with Loki 3.5.7 for the supported subset.

## ClickHouse version

Tested with ClickHouse **26.8.36.3**. The schema uses the exporter's full-text
indexes for logs, which require ClickHouse 26.2 or newer. Older versions are not
supported in the v0.1 alpha. The minimum supported version will be re-evaluated
with the beta test matrix.

## Detailed language inventory

<!-- BEGIN GENERATED SUPPORT MATRIX -->
Generated by `go run ./test/supportmatrix`; do not edit by hand.
Every row is backed by an executable inventory test.

## PromQL functions

| Function | Status | Test |
| --- | --- | --- |
| abs | supported | TestSupportedFunctionsEvaluate |
| absent | supported | TestSupportedFunctionsEvaluate |
| absent_over_time | supported | TestSupportedFunctionsEvaluate |
| acos | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| acosh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| asin | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| asinh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| atan | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| atanh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| avg_over_time | supported | TestSupportedFunctionsEvaluate |
| ceil | supported | TestSupportedFunctionsEvaluate |
| changes | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| clamp | supported | TestSupportedFunctionsEvaluate |
| clamp_max | supported | TestSupportedFunctionsEvaluate |
| clamp_min | supported | TestSupportedFunctionsEvaluate |
| cos | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| cosh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| count_over_time | supported | TestSupportedFunctionsEvaluate |
| day_of_month | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| day_of_week | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| day_of_year | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| days_in_month | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| deg | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| delta | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| deriv | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| double_exponential_smoothing | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| end | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| exp | supported | TestSupportedFunctionsEvaluate |
| first_over_time | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| floor | supported | TestSupportedFunctionsEvaluate |
| histogram_avg | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| histogram_count | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| histogram_fraction | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| histogram_quantile | supported | TestSupportedFunctionsEvaluate |
| histogram_quantiles | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| histogram_stddev | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| histogram_stdvar | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| histogram_sum | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| hour | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| idelta | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| increase | supported | TestSupportedFunctionsEvaluate |
| info | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| integral | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| irate | supported | TestSupportedFunctionsEvaluate |
| label_join | supported | TestSupportedFunctionsEvaluate |
| label_replace | supported | TestSupportedFunctionsEvaluate |
| last_over_time | supported | TestSupportedFunctionsEvaluate |
| ln | supported | TestSupportedFunctionsEvaluate |
| log10 | supported | TestSupportedFunctionsEvaluate |
| log2 | supported | TestSupportedFunctionsEvaluate |
| mad_over_time | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| max_of | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| max_over_time | supported | TestSupportedFunctionsEvaluate |
| min_of | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| min_over_time | supported | TestSupportedFunctionsEvaluate |
| minute | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| month | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| pi | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| predict_linear | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| present_over_time | supported | TestSupportedFunctionsEvaluate |
| quantile_over_time | supported | TestSupportedFunctionsEvaluate |
| rad | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| range | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| rate | supported | TestSupportedFunctionsEvaluate |
| resets | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| round | supported | TestSupportedFunctionsEvaluate |
| scalar | supported | TestSupportedFunctionsEvaluate |
| sgn | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| sin | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| sinh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| sort | supported | TestSupportedFunctionsEvaluate |
| sort_by_label | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| sort_by_label_desc | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| sort_desc | supported | TestSupportedFunctionsEvaluate |
| sqrt | supported | TestSupportedFunctionsEvaluate |
| start | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| start_timestamp | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| stddev_over_time | supported | TestSupportedFunctionsEvaluate |
| stdvar_over_time | supported | TestSupportedFunctionsEvaluate |
| step | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| sum_over_time | supported | TestSupportedFunctionsEvaluate |
| tan | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| tanh | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |
| time | supported | TestSupportedFunctionsEvaluate |
| timestamp | supported | TestSupportedFunctionsEvaluate |
| ts_of_first_over_time | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| ts_of_last_over_time | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| ts_of_max_over_time | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| ts_of_min_over_time | parser-disabled | TestParserDisabledFunctionsReturnPrometheusError |
| vector | supported | TestSupportedFunctionsEvaluate |
| year | explicit error | TestExplicitErrorFunctionsReturnPrometheusError |

## LogQL constructs

| Construct | Status | Test |
| --- | --- | --- |
| avg aggregation | error | TestEveryLogQLConstructClassified |
| avg_over_time | error | TestEveryLogQLConstructClassified |
| bool modifier | error | TestEveryLogQLConstructClassified |
| bytes_over_time | error | TestEveryLogQLConstructClassified |
| bytes_rate | error | TestEveryLogQLConstructClassified |
| constant vector | supported | TestEveryLogQLConstructClassified |
| constant vector arithmetic | supported | TestEveryLogQLConstructClassified |
| constant vector scalar arithmetic | error | TestEveryLogQLConstructClassified |
| count aggregation | error | TestEveryLogQLConstructClassified |
| count_over_time | supported | TestEveryLogQLConstructClassified |
| decolorize stage | error | TestEveryLogQLConstructClassified |
| drop stage | error | TestEveryLogQLConstructClassified |
| ip parser | error | TestEveryLogQLConstructClassified |
| json field selection | supported | TestEveryLogQLConstructClassified |
| json parser | supported | TestEveryLogQLConstructClassified |
| keep stage | error | TestEveryLogQLConstructClassified |
| label equals | supported | TestEveryLogQLConstructClassified |
| label greater | supported | TestEveryLogQLConstructClassified |
| label greater or equal | supported | TestEveryLogQLConstructClassified |
| label less | supported | TestEveryLogQLConstructClassified |
| label less or equal | supported | TestEveryLogQLConstructClassified |
| label not equals | supported | TestEveryLogQLConstructClassified |
| label not regex | supported | TestEveryLogQLConstructClassified |
| label regex | supported | TestEveryLogQLConstructClassified |
| label_filter stage | error | TestEveryLogQLConstructClassified |
| label_format stage | error | TestEveryLogQLConstructClassified |
| line filter contains | supported | TestEveryLogQLConstructClassified |
| line filter not contains | supported | TestEveryLogQLConstructClassified |
| line filter not regex | supported | TestEveryLogQLConstructClassified |
| line filter regex | supported | TestEveryLogQLConstructClassified |
| line_format stage | error | TestEveryLogQLConstructClassified |
| logfmt flags | error | TestEveryLogQLConstructClassified |
| logfmt parser | supported | TestEveryLogQLConstructClassified |
| max aggregation | error | TestEveryLogQLConstructClassified |
| max_over_time | error | TestEveryLogQLConstructClassified |
| min aggregation | error | TestEveryLogQLConstructClassified |
| min_over_time | error | TestEveryLogQLConstructClassified |
| nested constant vector arithmetic | error | TestEveryLogQLConstructClassified |
| offset modifier | error | TestEveryLogQLConstructClassified |
| pack parser | error | TestEveryLogQLConstructClassified |
| pattern parser | error | TestEveryLogQLConstructClassified |
| quantile_over_time | error | TestEveryLogQLConstructClassified |
| query sharding | error | TestEveryLogQLConstructClassified |
| rate | supported | TestEveryLogQLConstructClassified |
| regexp parser | error | TestEveryLogQLConstructClassified |
| stdvar_over_time | error | TestEveryLogQLConstructClassified |
| sum aggregation | supported | TestEveryLogQLConstructClassified |
| sum by aggregation | supported | TestEveryLogQLConstructClassified |
| sum_over_time | error | TestEveryLogQLConstructClassified |
| topk aggregation | error | TestEveryLogQLConstructClassified |
| unpack parser | error | TestEveryLogQLConstructClassified |
| unwrap stage | error | TestEveryLogQLConstructClassified |
| vector matching | error | TestEveryLogQLConstructClassified |

## TraceQL forms

| Form | Status | Test |
| --- | --- | --- |
| count() -> TraceQL pipeline "\| count()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| rate() -> TraceQL metrics query "\| rate()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| supported subset: supported subset: one spanset { ... } with &&, \|\| and comparison operators =, !=, =~, !~, >, <, >=, <= | supported | TestRejectedFormsMatchDocs |
| { (span:name = "x") } -> TraceQL grouping, functions, and aggregations are not supported (found "(") | explicit error | TestRejectedFormsMatchDocs |
| { span:count() > 1 } -> TraceQL grouping, functions, and aggregations are not supported (found "(") | explicit error | TestRejectedFormsMatchDocs |
| { span:foo } -> TraceQL condition "span:foo" has no operator | explicit error | TestRejectedFormsMatchDocs |
| { span:name = "x" -> TraceQL spanset is not closed: missing '}' | explicit error | TestRejectedFormsMatchDocs |
| { span:name = "x" } foo -> TraceQL unexpected text after the spanset: "foo" | explicit error | TestRejectedFormsMatchDocs |
| { span:name = GET /cart } -> TraceQL value "GET /cart" contains spaces; quote string values | explicit error | TestRejectedFormsMatchDocs |
| { {a} } -> TraceQL nested spansets are not supported | explicit error | TestRejectedFormsMatchDocs |
| { } !< { span:name = "x" } -> TraceQL structural operator "!<" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } !<< { span:name = "x" } -> TraceQL structural operator "!<<" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } !> { span:name = "x" } -> TraceQL structural operator "!>" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } !>> { span:name = "x" } -> TraceQL structural operator "!>>" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } < { span:name = "x" } -> TraceQL structural operator "<" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } << { span:name = "x" } -> TraceQL structural operator "<<" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } > { span:name = "x" } -> TraceQL structural operator ">" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } >> { span:name = "x" } -> TraceQL structural operator ">>" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } \| compare() -> TraceQL metrics query "\| compare()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } \| count_over_time() -> TraceQL metrics query "\| count_over_time()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } \| histogram_over_time(duration) -> TraceQL metrics query "\| histogram_over_time()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } \| quantile_over_time(duration, .99) -> TraceQL metrics query "\| quantile_over_time()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } \| rate() -> TraceQL metrics query "\| rate()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| { } ~ { span:name = "x" } -> TraceQL structural operator "~" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} & {b} -> TraceQL multiple spansets are not supported (found "&" between spansets) | explicit error | TestRejectedFormsMatchDocs |
| {a} && {b} -> TraceQL multiple spansets are not supported (found "&&" between spansets) | explicit error | TestRejectedFormsMatchDocs |
| {a} {b} -> TraceQL multiple spansets are not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \| by(span:name) -> TraceQL pipeline "\| by()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \| coalesce() -> TraceQL pipeline "\| coalesce()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \| count() -> TraceQL pipeline "\| count()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \| count() > 1 -> TraceQL pipeline "\| count()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \| select(span:name) -> TraceQL pipeline "\| select()" is not supported | explicit error | TestRejectedFormsMatchDocs |
| {a} \|\| {b} -> TraceQL multiple spansets are not supported (found "\|\|" between spansets) | explicit error | TestRejectedFormsMatchDocs |

## Limits

| Limit | Default | Source |
| --- | --- | --- |
| Maximum query range | 168h0m0s | `internal/config` |
| Maximum scan rows | 200000 | `internal/config` |
| Maximum rows to read | 10000000 | `internal/config` |
| Maximum result rows | 100000 | `internal/config` |
| Maximum response bytes | 67108864 (64 MiB) | `internal/config` |
| Maximum request bytes | 1048576 (1 MiB) | `internal/config` |
| Query timeout | 30s | `internal/config` |
| Maximum execution time | 30s | `internal/config` |
| Maximum memory usage | 1073741824 (1 GiB) | `internal/config` |
| In-flight cap | unset | admission limiter; wired by AX-23 |
<!-- END GENERATED SUPPORT MATRIX -->

## Known limitations

- The chart's init job uses `CREATE TABLE IF NOT EXISTS`; it does not migrate
  existing tables. Changing table columns or TTL requires a manual migration.
- Basic auth is optional and uses one shared credential; there is no per-user
  authorization or tenant isolation. See [SECURITY.md](../SECURITY.md).
- OBI's kernel and capability requirements are external constraints, not
  something the chart can validate.
- Query evaluation is in the gateway, not pushed down to ClickHouse. The row
  scan limit and 7-day range bound protect the single-node alpha deployment.
  LogQL only pushes filters; metric aggregation still happens in the gateway.
- S3 cold reads are slower than hot reads and depend on the local ClickHouse
  S3 cache. A backup is not a queryable retention tier; restore it first.
- Tiered storage and backups need external S3-compatible buckets and
  credentials. The chart does not create buckets.
- The distributed frontend scales the read path only. ClickHouse is still a
  single node in the default chart; there is no ClickHouse replication/HA.
- The language support lists are intentionally narrow. The current checks
  cover a subset: PromQL differential oracle (212 cases), TraceQL corpus
  (83 queries plus tag and trace-by-id cases), LogQL corpus (19 cases), and the
  deterministic three-signal fixture recorded against Prometheus, Loki, Tempo,
  and cerberus v1.23.1 (0 Skopos divergences; see
  [cerberus-baseline.md](cerberus-baseline.md)). The end-to-end cerberus
  comparison was retired with the Python harnesses, so CI now runs the Go seed
  and corpus tests.
- The full Prometheus compliance tester and the complete Loki benchmark corpus
  remain beta gates; see [beta-gates.md](beta-gates.md).
- The OBI e2e and release signing/publishing are manual because they need
  privileged eBPF or project credentials; normal CI covers build, unit,
  integration, fuzz, Helm, PromQL oracle, Tempo and LogQL corpora, the cerberus
  seed tests, and the S3/RustFS test. OpenSSF Scorecard runs weekly.
