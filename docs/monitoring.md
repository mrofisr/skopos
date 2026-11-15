# Monitoring and alerts

Skopos ships three opt-in monitoring assets. All three are disabled by default
because they need CRDs or a Grafana sidecar that the chart does not install.

| Asset | Value | Requires |
| --- | --- | --- |
| ServiceMonitor | `metrics.serviceMonitor.enabled` | Prometheus Operator CRDs |
| PrometheusRule | `metrics.prometheusRule.enabled` | Prometheus Operator CRDs |
| Grafana dashboards | `metrics.dashboards.enabled` | Grafana dashboard sidecar, or manual import |

The metric names and labels are listed in [metrics.md](metrics.md). The chart
does not scrape the Collector or ClickHouse directly; the alerts below use the
gateway's own ClickHouse accounting metrics, so they work with the shipped
ServiceMonitor.

## Enable monitoring

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set gateway.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set-json 'metrics.serviceMonitor.selector={"app.kubernetes.io/name":"skopos"}' \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.dashboards.enabled=true
```

The ServiceMonitor selector must match the gateway Service. In distributed mode
it should also match the query Service so `SkoposQueryDown` has a series to
test. If you scrape `/metrics` with your own Prometheus configuration instead,
the ServiceMonitor is not required; the alerts only need the `skopos_*` series
and the `namespace` label.

`metrics.prometheusRule.additionalLabels` and
`metrics.dashboards.labels` let you match your Prometheus `ruleSelector` and
Grafana sidecar selector. `metrics.dashboards.folder` sets the
`grafana_folder` annotation; `metrics.dashboards.annotations` adds any other
sidecar annotation. The dashboards are selectable through a `DS_PROMETHEUS`
template variable, whose default UID is `skopos-prometheus`.

The generated ConfigMap is named `<fullname>-dashboards`; with the default
release name `skopos` the name is `skopos-dashboards`. To import it without the
sidecar:

```sh
kubectl -n observability get configmap skopos-dashboards \
  -o jsonpath='{.data.skopos-overview\.json}' > skopos-overview.json
kubectl -n observability get configmap skopos-dashboards \
  -o jsonpath='{.data.obi-red\.json}' > obi-red.json
```

Import the two JSON files in Grafana and pick your Prometheus datasource.

The alert rules are scoped with the `namespace` label that Prometheus Operator
adds to ServiceMonitor targets. If you disable that label or add rules through
`additionalRules`, keep the namespace scope yourself. The default rules use the
gateway Service name `<fullname>-gateway` and, in distributed mode,
`<fullname>-query`.

## Alert rules

| Alert | Severity | Fires when |
| --- | --- | --- |
| `SkoposGatewayDown` | critical | No gateway build-info series for 10 minutes. |
| `SkoposQueryDown` | critical | Distributed mode only: no query build-info series for 10 minutes. |
| `SkoposHighErrorRate` | warning | More than 5% of HTTP requests are 5xx for 10 minutes. |
| `SkoposHighLatency` | warning | HTTP p99 is above 5 seconds for 15 minutes. |
| `SkoposAdmissionShedding` | warning | More than 0.1 requests/second are rejected for 15 minutes. |
| `SkoposClickHouseErrorRate` | critical | More than 5% of ClickHouse queries fail for 10 minutes. |
| `SkoposLowContextCoverage` | warning | A signal's documented/observed ratio is below 0.8 for 1 hour. |

Every alert has a `runbook_url` annotation pointing at the matching section
below. Set `metrics.prometheusRule.runbookURL` to your own runbook location, or
set it to `""` to omit the annotation.

## Runbooks

### SkoposGatewayDown

Prometheus has not scraped the gateway for 10 minutes.

1. `kubectl -n observability get pods,svc,endpoints -l app.kubernetes.io/name=skopos`
   and check that the gateway pod is Ready.
2. Check `/healthz` and `/readyz` from inside the cluster.
3. Check the ServiceMonitor selector; an empty selector is rejected, and a
   selector that does not match the gateway Service produces no target.
4. Check that the Prometheus NetworkPolicy, if any, allows scrapes on `/metrics`.
5. If the gateway is running but metrics are absent, check the scrape target in
   Prometheus and the ServiceMonitor endpoint port (`http`).

### SkoposQueryDown

Distributed mode is enabled and Prometheus has not scraped the query service for
10 minutes. Frontend requests will fail without query workers.

1. `kubectl -n observability get pods,svc,endpoints` and confirm the query pods
   are Ready.
2. Probe a query pod's `/readyz`; it checks ClickHouse connectivity.
3. Check frontend logs for connection errors to the query Service.
4. Confirm the ServiceMonitor selector also matches the query Service.

### SkoposHighErrorRate

More than 5% of HTTP requests have returned 5xx for 10 minutes.

1. Split by head and route:
   `sum by (head, route) (rate(skopos_http_requests_total{code=~"5.."}[5m]))`.
2. Check `sum by (head) (rate(skopos_clickhouse_queries_total{result="error"}[5m]))`.
   If ClickHouse errors dominate, follow the ClickHouse runbook below.
3. Check `skopos_unsupported_queries_total`; a dashboard using an unsupported
   PromQL, TraceQL, or LogQL feature returns an explicit error, not a 5xx.
4. Check gateway logs for panics, request-size rejections, and timeouts.
5. If the 5xx are deliberate admission `503`s, use the admission runbook.

### SkoposHighLatency

The HTTP p99 has been above 5 seconds for 15 minutes. Long range queries can be
legitimately expensive; alert fatigue here usually means the query timeout or
admission budget is too generous for the workload.

1. Compare HTTP latency with
   `histogram_quantile(0.95, sum by (le, head) (rate(skopos_clickhouse_query_duration_seconds_bucket[5m])))`.
2. Check in-flight requests and admission rejections by head.
3. Check ClickHouse memory, disk, and `max_execution_time` errors.
4. Reduce the query range, add a lower `clickhouse.limits.maxExecutionTimeSeconds`,
   or add query replicas. Do not raise `maxInflight` before finding the slow
   query.

### SkoposAdmissionShedding

The admission limiter is rejecting requests. This is deliberate backpressure,
not a bug: the gateway is protecting memory and ClickHouse.

1. Check whether a load test or a single client is responsible:
   `sum by (head, route) (rate(skopos_http_requests_total[5m]))`.
2. Check slow in-flight requests and ClickHouse latency.
3. Add gateway replicas for more total slots, or lower `pointsPerSlot` only if
   the workload is genuinely made of small queries.
4. Keep `maxInflight`, `queueDepth`, and `queueWaitSeconds` aligned. See
   [install.md](install.md#admission-limits).

### SkoposClickHouseErrorRate

More than 5% of ClickHouse queries failed for 10 minutes.

1. Check the ClickHouse pod and PVC: disk pressure, OOM kills, and restarts.
2. Check ClickHouse logs for `MEMORY_LIMIT_EXCEEDED`, `TIMEOUT_EXCEEDED`, and
   `TOO_MANY_ROWS` errors.
3. Compare the error rate with `skopos_clickhouse_queries_total{result="error"}`
   by head to find the failing signal.
4. Check that the gateway's ClickHouse user still has `SELECT` on the configured
   database and that the schema init job completed.
5. If ClickHouse is healthy but one query shape fails, reduce its range or
   remove it from the dashboard.

### FINAL fallback

`skopos_clickhouse_final_fallback_total` counts reads that dropped `FINAL`:

- `action="retried"` means ClickHouse returned `ILLEGAL_FINAL` and the query
  succeeded without read-time deduplication. The signal table is still plain
  `MergeTree`; run the schema init job or migrate it to `ReplacingMergeTree`.
- `action="skipped"` is the one-minute per-table cache that avoids repeating
  the rejected round trip. It follows a `retried` event for the same table.

The fallback keeps reads available during a migration, but it loses merge-time
deduplication. An increase that does not clear after the init job is a schema
problem, not a gateway problem.

### SkoposLowContextCoverage

A signal's observed metric count is more than 20% larger than the documented
catalog for an hour. This does not fail queries; AI SRE answers are marked
`known:false` and `observed_only`.

1. Inspect `skopos_context_coverage_ratio` and `skopos_context_observed` by
   signal.
2. Add the missing metric names to the semantic context catalog. See
   [ai-context.md](ai-context.md) and the example
   `deploy/examples/context/catalog.json`.
3. If the gap is expected, leave it. Coverage is a documentation signal, not a
   data-loss signal.

## Backup and restore jobs

The optional backup, lifecycle, and restore-drill CronJobs are Kubernetes Jobs,
not `/metrics` targets. Monitor their Job status with kube-state-metrics or your
cluster alerting; the chart does not add a PrometheusRule for them. A failed
restore drill keeps its temporary database for inspection. See
[storage.md](storage.md#restore-drills).

## Dashboards

`Skopos Overview` shows:

- request rate, 5xx rate, in-flight requests, and admission rejections;
- HTTP p95/p99 and ClickHouse query latency;
- ClickHouse query errors and read throughput;
- semantic-context coverage and unsupported-query counts.

`OBI RED` is the acceptance dashboard for the OBI path. It shows server and
client request rate, error rate, and p95 duration from the OpenTelemetry
`http_server_*` and `http_client_*` histograms. The Grafana end-to-end harness
runs every panel target against seed data through the Grafana query API; the
same file is shipped by the chart at
`deploy/helm/skopos/dashboards/obi-red.json`.
