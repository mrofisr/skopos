# Installation and operations

> Alpha: the chart installs the data plane and the alpha Prometheus, Tempo,
> and Loki query APIs. Unsupported functions return explicit errors; see the
> per-language support lists. Do not expose this installation to the internet.

For a non-Kubernetes host, see [Running Skopos as a systemd
service](systemd.md). The systemd path uses the same `skopos` binary and
ClickHouse schema, but manages the unit and schema directly.

## Prerequisites

- Kubernetes 1.27+.
- Helm 3.19+ or Helm 4.
- A default StorageClass, or set `clickhouse.persistence.storageClass`.
- Linux nodes that OBI supports:
  - kernel 5.8+ with BTF, or
  - RHEL-family kernel 4.18+ with the eBPF backports.
- `hostPID` and the OBI host mounts (`/sys/fs/cgroup`, `/sys/kernel/tracing`)
  must be allowed by your pod security policy. OBI is a node-level agent.
- The default chart grants OBI `BPF`, `SYS_PTRACE`, `NET_RAW`,
  `CHECKPOINT_RESTORE`, `DAC_READ_SEARCH`, `PERFMON`, and `SYS_RESOURCE`. It does
  not grant `NET_ADMIN` or `SYS_ADMIN`. See `values.yaml` and OBI's security
  documentation before enabling trace-context propagation or network
  observability.
- Optional tiered storage and backups need S3-compatible endpoints, existing
  buckets, and credentials. See [storage.md](storage.md).
- The optional durability checkpoint needs an S3-compatible endpoint, an
  existing bucket, and credentials, plus a PVC for its local WAL. See
  [checkpoint.md](checkpoint.md).

## Install the data plane

The gateway is disabled by default. Install the data plane from a local
checkout:

```sh
helm install skopos ./deploy/helm/skopos \
  --namespace observability \
  --create-namespace
```

Published releases use the OCI chart and the attached digest-pinned values:

```sh
helm install skopos oci://ghcr.io/mrofisr/charts/skopos \
  --version 0.0.1-alpha \
  -f values.release.yaml \
  --namespace observability \
  --create-namespace
```

Download `values.release.yaml` from the matching GitHub release. See
[release.md](release.md) and [chart-repository.md](chart-repository.md).

The init job waits for ClickHouse, creates the pinned schema, and creates the
read-only gateway user. If the job fails, inspect its logs before retrying:

```sh
kubectl -n observability logs job/skopos-clickhouse-init
kubectl -n observability get pods
```

## Build and enable the gateway

```sh
docker build -t skopos-gateway:dev .
# k3d:
k3d image import -c <cluster> skopos-gateway:dev
# kind:
# kind load docker-image skopos-gateway:dev

helm upgrade skopos ./deploy/helm/skopos --namespace observability \
  --set gateway.enabled=true \
  --set gateway.image.repository=skopos-gateway \
  --set gateway.image.tag=dev \
  --set gateway.image.digest= \
  --set gateway.image.pullPolicy=IfNotPresent
```

For a real cluster, push the image to a registry and set the repository, tag,
and digest. Release values must set `gateway.image.digest`.

Check health:

```sh
kubectl -n observability port-forward svc/skopos-gateway 8080:8080
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

`/readyz` returns 503 when ClickHouse is unreachable. `/healthz` only reports
that the gateway process is alive.

## Distributed read path (optional)

`distributed.enabled=true` splits the read path:

- `skopos-frontend` proxies Prometheus/Loki/Tempo requests to
  `SKOPOS_QUERY_URLS`; it has no ClickHouse credentials.
- `skopos-query` runs the three query heads against ClickHouse.
- The public Service keeps the name `skopos-gateway` and selects frontend pods.
- `/readyz` on the frontend returns 503 unless at least one query backend is
  ready.
- NetworkPolicy allows Grafana/ingress to the frontend, frontend to query, and
  query to ClickHouse.

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set gateway.enabled=true \
  --set distributed.enabled=true \
  --set frontend.replicaCount=2 \
  --set query.replicaCount=2
```

The default (`distributed.enabled=false`, `gateway.mode=all`) remains the
single-binary deployment. `SKOPOS_MODE=worker` is an explicit alias for
`SKOPOS_MODE=query`; both register the three query heads. ClickHouse remains
the backend and owns merges/compaction; this is not a ClickHouse HA topology.

### Availability guardrails

The chart adds these by default:

- **PodDisruptionBudget** for gateway, frontend, query, Collector, and
  checkpoint (`maxUnavailable: 1`). Set `minAvailable` to override it.
- **Soft hostname topology spread** for any component with more than one
  replica. Set explicit `topologySpreadConstraints` to override it.
- **Rolling updates** for stateless components (`maxUnavailable: 0`,
  `maxSurge: 1`). The Collector StatefulSet updates one replica at a time and
  each replica owns its queue PVC.
- **Collector StatefulSet**: each Collector replica gets its own
  `ReadWriteOnce` queue PVC. `collector.sendingQueue.persistence.enabled=false`
  uses a per-pod emptyDir instead. Scaling from the old Deployment to this
  StatefulSet requires deleting the old Deployment after draining its queue;
  the chart fails the upgrade with that instruction instead of running both
  workloads. `make test-faults` proves the queue survives a backend outage and
  a Collector restart. The old `skopos-collector-queue` PVC is not reused; delete it
  after the new StatefulSet is healthy.
- ClickHouse is still a single replica. These guardrails do not make it HA.

#### One-time Collector StatefulSet migration

If you are upgrading from a release where the Collector was a Deployment:

1. Wait until the Collector queue is drained (`otelcol_exporter_queue_size`
   returns to zero).
2. Delete the old Deployment:
   ```sh
   kubectl -n observability delete deployment skopos-collector
   ```
3. Run the Helm upgrade. The StatefulSet creates a queue PVC per replica.
4. Delete the old `skopos-collector-queue` PVC after the new StatefulSet is
   healthy; it is not reused.

The chart fails the upgrade before step 2 instead of running both workloads.

## OTLP authentication

OTLP ingress auth is **off by default**. Enable a static bearer token for the
Collector's gRPC and HTTP receivers:

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set collector.otlpAuth.enabled=true \
  --set collector.otlpAuth.token='change-me'
```

For production, create the Secret yourself and pass its name. It must contain
the token key (`token` by default) and a full header key (`otlp-headers` by
default):

```sh
kubectl -n observability create secret generic skopos-otlp-auth \
  --from-literal=token='change-me' \
  --from-literal=otlp-headers='Authorization=Bearer change-me'

helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set collector.otlpAuth.enabled=true \
  --set collector.otlpAuth.existingSecret=skopos-otlp-auth
```

The chart sets `OTLP_AUTH_TOKEN` on the Collector and
`OTEL_EXPORTER_OTLP_HEADERS` on OBI, so OBI keeps sending after auth is enabled.
External senders must add the same header:

```sh
curl -X POST http://skopos-collector:4318/v1/logs \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer change-me' \
  --data-binary @logs.json
```

For OTLP/gRPC, send `authorization: Bearer change-me` as request metadata.
Without the token the Collector returns **401**.

The token is an environment variable, so rotate it by updating the Secret and
restarting the Collector and OBI pods. The token protects only the Collector
ingress; it does not protect the checkpoint endpoint or ClickHouse. Bearer
tokens over plaintext are visible to anything that can sniff the network — use
TLS at the ingress or mTLS for untrusted networks.

### TLS and mTLS

TLS is also off by default. The Secret must contain:

| Key | Purpose |
| --- | --- |
| `tls.crt` | Collector server certificate |
| `tls.key` | Collector server private key |
| `ca.crt` | CA used to verify the server (and clients for mTLS) |
| `client.crt` | OBI client certificate, required for mTLS |
| `client.key` | OBI client private key, required for mTLS |

The server certificate's SANs must include the Collector service DNS name,
for example `skopos-collector` and
`skopos-collector.observability.svc`.

```sh
kubectl -n observability create secret generic skopos-otlp-tls \
  --from-file=tls.crt=server.crt \
  --from-file=tls.key=server.key \
  --from-file=ca.crt=ca.crt \
  --from-file=client.crt=client.crt \
  --from-file=client.key=client.key

helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set collector.tls.enabled=true \
  --set collector.tls.existingSecret=skopos-otlp-tls \
  --set collector.tls.mtls=true
```

With TLS enabled, OBI switches to `https://skopos-collector:4317`, trusts
`ca.crt`, and presents the client certificate when `mtls=true`. External
senders must do the same:

```sh
curl -X POST https://skopos-collector:4318/v1/logs \
  --cacert ca.crt \
  --cert client.crt --key client.key \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer change-me' \
  --data-binary @logs.json
```

Without a client certificate, the TLS handshake fails. With a client
certificate but no token, the Collector returns **401**. The server reloads its
certificate every 10 minutes; restart OBI after rotating the client
certificate.

TLS-only (without `mtls`) still encrypts traffic and verifies the server, but
does not authenticate clients. mTLS is the recommended setting for untrusted
networks.

## Admission limits

Admission is enabled by default in the chart: **16 in-flight requests per query
head, 32 queued, 2-second wait**. Set `gateway.config.maxInflight` to change
the cap, or to `0` to disable admission. `gateway.config.queueDepth` and
`gateway.config.queueWaitSeconds` add a bounded FIFO wait queue and must be set
together. The chart rejects a queue without an in-flight cap.

Range requests consume budget in proportion to their evaluated points:
`ceil(points / gateway.config.pointsPerSlot)` units, with a minimum of one.
Instant queries cost one unit. The default `pointsPerSlot: 100` means a 6-hour
range at a 60-second step costs four units, so a few ranges cannot starve
instant queries. Set `pointsPerSlot: 0` to disable cost weighting.

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set gateway.enabled=true \
  --set gateway.config.maxInflight=16 \
  --set gateway.config.queueDepth=32 \
  --set gateway.config.queueWaitSeconds=2 \
  --set gateway.config.pointsPerSlot=100
```

The same settings are the `SKOPOS_MAX_INFLIGHT`, `SKOPOS_QUEUE_DEPTH`, and
`SKOPOS_QUEUE_WAIT` environment variables for a binary install. The chart also
sets `GOMEMLIMIT` to 90% of `gateway.resources.limits.memory`, or to
`gateway.goMemLimit` when set; omit both to leave `GOMEMLIMIT` unset. At
startup the gateway warns when `maxInflight x maxResponseBytes` exceeds the
Go memory limit. The gateway and query workers default to **128Mi request /
1.5Gi limit**; the bench gate enforces a 1.5Gi peak.

`gateway.config.metadataTTL` (default `30s`, environment
`SKOPOS_METADATA_TTL`) caches metric-to-table discovery, per-table emptiness,
and metric metadata. A table that gains data is picked up after the TTL. A rejected
request gets the head's error shape, `Retry-After: 1`, and increments
`skopos_admission_rejected_total{head}`. `/healthz`, `/readyz`, and `/metrics`
are exempt, so probes and scrapes keep working under load.

Instant queries without an explicit `time` are evaluated at the start of the
current second and cached for `SKOPOS_INSTANT_CACHE_TTL` (default `1s`; set
`0s` to disable). The cache is per gateway replica and holds at most 64
results. Supply `time` for an exact instant; explicit timestamps are cached by
their exact value for the same TTL.

## Prometheus Operator, alerts, and dashboards

`metrics.serviceMonitor.enabled=true` renders a `ServiceMonitor` for `/metrics`
when the Prometheus Operator CRDs are installed. The selector must match the
gateway Service; an empty selector fails the render. The default is disabled.

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set gateway.enabled=true \
  --set metrics.serviceMonitor.enabled=true \
  --set-json 'metrics.serviceMonitor.selector={"app.kubernetes.io/name":"skopos"}'
```

`metrics.prometheusRule.enabled=true` adds up to seven default alert rules for
gateway availability, error rate, p99 latency, admission shedding, ClickHouse
errors, distributed query availability, and semantic-context coverage. Set
`metrics.prometheusRule.additionalLabels` to match your Prometheus
`ruleSelector`, and use `metrics.prometheusRule.additionalRules` for extra
rules. The runbook link in each alert points at
[monitoring.md](monitoring.md).

`metrics.dashboards.enabled=true` renders a ConfigMap with the `Skopos Overview`
and `OBI RED` dashboards. It carries the `grafana_dashboard: "1"` label for the
Grafana dashboard sidecar by default; change `metrics.dashboards.labels` to
match your sidecar selector. `metrics.dashboards.folder` sets the
`grafana_folder` annotation.

```sh
helm upgrade --install skopos ./deploy/helm/skopos \
  --namespace observability \
  --set gateway.enabled=true \
  --set metrics.prometheusRule.enabled=true \
  --set metrics.dashboards.enabled=true
```

All three assets need resources that the chart does not install: the Prometheus
Operator CRDs for the ServiceMonitor and PrometheusRule, and a Grafana dashboard
sidecar for the ConfigMap. See [monitoring.md](monitoring.md) for the alert
runbooks, dashboard contents, and enablement details.

## Basic authentication

Authentication is optional and disabled by default. When enabled, all query
routes and `/version` require HTTP Basic auth; `/healthz` and `/readyz` are
exempt so Kubernetes probes continue to work.

Preferred: create a Secret with `username` and `password`, then reference it:

```sh
kubectl -n observability create secret generic skopos-auth \
  --from-literal=username=viewer \
  --from-literal=password='change-me'

helm upgrade skopos ./deploy/helm/skopos --namespace observability \
  --set auth.enabled=true \
  --set auth.existingSecret=skopos-auth
```

For a local test only, inline values are also supported:

```sh
helm upgrade skopos ./deploy/helm/skopos --namespace observability \
  --set auth.enabled=true \
  --set auth.username=viewer \
  --set auth.password='change-me'
```

For a non-Helm deployment, set `SKOPOS_AUTH_ENABLED=true`,
`SKOPOS_AUTH_USERNAME`, and `SKOPOS_AUTH_PASSWORD` directly.

When upgrading a release created before the 200k result cap,
`helm upgrade --reuse-values` keeps the old `clickhouse.limits.maxResultRows`
(for example `100000`). The gateway rejects a scan cap above the result cap:
set `clickhouse.limits.maxResultRows=200000`, or drop `--reuse-values` so the
new chart default applies. The new termination-grace, pprof opt-in, and
profile defaults are applied by the templates, so an old values file does not
need to add them.

Configure Grafana's Prometheus, Loki, and Tempo datasources with `basicAuth:
true`, `basicAuthUser`, and the password in `secureJsonData`. Basic auth sends
reusable credentials with every request: use TLS at the ingress and do not
expose the gateway over plain HTTP.

The gateway compares the presented username and password in constant time and
keeps the configured credential only in memory. It deliberately does not hash
the configured password with bcrypt or argon2: Basic auth must compare the
plaintext the client sends, the plaintext already lives in the Secret or
environment, and a slow hash on every unauthenticated request would be a
CPU-denial-of-service surface. Protect the Secret with RBAC, use a long random
password, and keep TLS in front of the gateway.

## Configuration

The important values are:

| Value | Default | Meaning |
| --- | --- | --- |
| `clickhouse.database` | `otel` | Database name. Validated as an SQL identifier. |
| `clickhouse.tables.*` | `otel_*` | Table names used by the DDL, Collector, and gateway. Validated as SQL identifiers. The gateway parses them now; the M3–M5 query heads consume them. |
| `clickhouse.dedup.enabled` | `true` | Full replay dedup for logs and metrics (`ReplacingMergeTree` + 16-byte row hash). Set `false` for the fast profile: `MergeTree`, no row hash, at-least-once replay. Trace identity dedup stays on. |
| `clickhouse.ttl.tracesDays` | `7` | Trace and trace-lookup TTL in days. |
| `clickhouse.ttl.logsDays` | `7` | Log TTL in days. |
| `clickhouse.ttl.metricsDays` | `30` | Metrics TTL in days. |
| `clickhouse.persistence.size` | `50Gi` | PVC request. |
| `clickhouse.persistence.storageClass` | `""` | Empty uses the cluster default. |
| `clickhouse.limits.*` | see `values.yaml` | Gateway user limits in ClickHouse, also used by the gateway client. `maxExecutionTimeSeconds` is also the query timeout in the gateway and query pods, so the HTTP write timeout is execution time plus 5 s; a query timeout below the execution time is rejected at startup. |
| `clickhouse.ttl.*` | see `values.yaml` | Table TTLs. The chart keeps ClickHouse and the init client in UTC. Ignored for signal tables when tiered storage is enabled. |
| `clickhouse.storage.enabled` | `false` | Optional tiered storage: hot local PVC plus S3-compatible cold volume. Requires a bucket and credentials. See [storage.md](storage.md). |
| `clickhouse.storage.hotDays`, `totalDays` | `3`, `30` | Move parts to cold after `hotDays`; delete after `totalDays`. These replace `clickhouse.ttl.*` when storage is enabled. |
| `clickhouse.storage.cold.*` | empty | S3 endpoint, bucket, prefix, and credentials (or `existingSecret`) for the live cold tier. |
| `clickhouse.storage.cacheSize` | `10Gi` | Local read cache for cold parts, on the ClickHouse PVC. |
| `clickhouse.backup.enabled` | `false` | Optional CronJob that runs `BACKUP DATABASE` to a separate S3 destination. See [storage.md](storage.md). |
| `clickhouse.backup.schedule` | `0 2 * * *` | Cron schedule for the full backup. |
| `clickhouse.backup.lifecycle.enabled` | `false` | Optional CronJob that applies a backup-prefix lifecycle rule. It replaces the bucket's existing lifecycle configuration. |
| `clickhouse.backup.lifecycle.retentionDays` | `30` | Expire backup objects after this many days. |
| `clickhouse.backup.restoreDrill.enabled` | `false` | Optional CronJob that restores the latest backup into a temporary database and verifies every table. |
| `clickhouse.backup.*` | empty | Backup S3 endpoint, bucket, prefix, and credentials. The destination must not overlap the live cold tier. |
| `checkpoint.enabled` | `false` | Fan out every OTLP signal to the recent-window durability checkpoint. Requires a bucket and credentials; see [checkpoint.md](checkpoint.md). |
| `collector.sendingQueue.storageSize` | `5Gi` | `emptyDir` size when the queue PVC is disabled. The default is a PVC. |
| `collector.sendingQueue.persistence.enabled` | `true` | Give each Collector replica its own PVC for the disk-backed queue. Queued batches survive restarts and pod rescheduling. |
| `collector.sendingQueue.persistence.size` | `5Gi` | Queue PVC size. Resize it with the StorageClass if ClickHouse outages last longer than the buffer. |
| `collector.memoryLimiter.limitMiB`, `spikeLimitMiB` | `700`, `100` | Keep the Collector below its memory limit and apply backpressure before the OOM killer. Lower these with a smaller Collector memory limit. |
| `collector.otlpAuth.enabled` | `false` | Require a bearer token on OTLP/gRPC and OTLP/HTTP ingress. |
| `collector.otlpAuth.existingSecret` | `""` | Secret with `token` and `otlp-headers` keys (names configurable). Preferred over inline tokens. |
| `collector.otlpAuth.token` | `""` | Inline token, rendered into a Secret. The chart also creates the `otlp-headers` value for OBI. |
| `collector.tls.enabled` | `false` | Serve OTLP over TLS. Requires `collector.tls.existingSecret` with `tls.crt`, `tls.key`, and `ca.crt`. |
| `collector.tls.mtls` | `false` | Require a client certificate signed by `ca.crt`; OBI uses `client.crt`/`client.key`. |
| `collector.tls.minVersion` | `1.2` | Minimum TLS version (`1.2` or `1.3`). |
| `obi.batch.maxExportBatchSize`, `maxQueueSize` | `2048`, `8192` | OBI's OTLP batch and in-memory queue. Larger values reduce overhead and absorb short Collector stalls. |
| `checkpoint.persistence.size` | `2Gi` | Per-replica WAL PVC. Keep it above `checkpoint.maxLocalBytes` plus filesystem reserve. |
| `checkpoint.sealBytes`, `sealInterval` | `8388608`, `15s` | Seal the active segment on size or age. The unshipped window is bounded by `max(sealBytes, arrival_rate * sealInterval)`. |
| `checkpoint.maxLocalBytes` | `1073741824` | Local budget; the receiver returns `503` with `Retry-After` above it. |
| `checkpoint.s3.*` | empty | S3 endpoint, bucket, prefix, region, and credentials (or `existingSecret`) for checkpoint objects. The chart does not create the bucket. |
| `checkpoint.terminationGracePeriodSeconds` | `40` | Must cover the 10 s HTTP drain plus `flushTimeout`. |
| `context.enabled` | `false` | Mount a semantic context catalog and serve `/api/v1/context/*`. |
| `context.observed` | `true` | Read observed metric type/unit/description from ClickHouse for unknown metrics; labeled `known:false` and `observed_only`. |
| `context.existingConfigMap` | `""` | Existing ConfigMap with a `catalog.json` key. |
| `context.catalog` | `""` | Inline catalog JSON rendered into a ConfigMap. |
| `auth.enabled` | `false` | Enable HTTP Basic auth on query and version routes. `/healthz` and `/readyz` stay open. |
| `auth.existingSecret` | `""` | Secret with `username` and `password` keys. Preferred over inline values. |
| `auth.username`, `auth.password` | `""` | Inline credentials rendered into a Secret. Use only when `existingSecret` is empty. |
| `gateway.mode` | `all` | `all`, `query`/`worker`, or `frontend` for a single Deployment. |
| `gateway.terminationGracePeriodSeconds` | `45` | Kubernetes drain budget. Keep it above `clickhouse.limits.maxExecutionTimeSeconds + 10`. |
| `frontend.terminationGracePeriodSeconds` | `45` | Same for the distributed frontend; keep it above `frontend.config.queryTimeout + 10`. |
| `distributed.enabled` | `false` | Split into a public frontend and an internal query tier. |
| `frontend.replicaCount` | `1` | Public proxy replicas when distributed. |
| `frontend.config.queryTimeout` | `30s` | Per-request proxy timeout. |
| `frontend.config.readinessTimeout` | `1s` | Frontend probe timeout for query `/readyz`. |
| `query.replicaCount` | `1` | Internal query worker replicas when distributed. |
| `*.podDisruptionBudget.enabled` | `true` | PDB for gateway/frontend/query/collector/checkpoint. `maxUnavailable` defaults to 1; set `minAvailable` to override. |
| `*.topologySpreadConstraints` | `[]` | Explicit spread constraints. When empty and replicas > 1, a soft `kubernetes.io/hostname` spread is rendered. |
| `collector.replicas` | `1` | Collector StatefulSet replicas. Each replica gets its own queue PVC when persistence is enabled. |
| `collector.sendingQueue.persistence.retentionPolicy` | `Retain` / `Retain` | Keep queue PVCs when a replica is deleted or scaled down; clean them up explicitly after the outage window. |
| `metrics.serviceMonitor.enabled` | `false` | Render a ServiceMonitor for `/metrics`. Requires Prometheus Operator CRDs and a non-empty `selector`. |
| `metrics.prometheusRule.enabled` | `false` | Render the default Skopos alert rules. Requires Prometheus Operator CRDs. See [monitoring.md](monitoring.md). |
| `metrics.prometheusRule.additionalLabels` | `{}` | Labels used by the Prometheus `ruleSelector`. |
| `metrics.prometheusRule.runbookURL` | GitHub link | Base URL for alert `runbook_url` annotations; empty omits them. |
| `metrics.prometheusRule.additionalRules` | `[]` | Extra rule objects appended to the `skopos.rules` group. |
| `metrics.dashboards.enabled` | `false` | Render a ConfigMap with the Skopos Overview and OBI RED dashboards. |
| `metrics.dashboards.labels` | `grafana_dashboard: "1"` | Labels used by the Grafana dashboard sidecar. |
| `metrics.dashboards.annotations` | `{}` | Additional sidecar annotations; a `grafana_folder` key overrides `folder`. |
| `metrics.dashboards.folder` | `""` | Optional `grafana_folder` annotation for the sidecar. |
| `gateway.image.digest` | `""` | Release images must set this. |
| `networkPolicy.enabled` | `true` | Component ingress NetworkPolicies. |
| `networkPolicy.gateway.allowFrom` | `[{namespaceSelector: {}}]` | Namespace/label selectors allowed to reach the gateway. Empty means no ingress. Tighten this to your Grafana namespace. |
| `networkPolicy.collector.allowFrom` | `[{namespaceSelector: {}}]` | Namespace/label selectors allowed to send OTLP to the Collector. Tighten this to the namespaces that emit telemetry. |

The chart generates ClickHouse admin and gateway passwords into a Secret. It
reuses the existing Secret on upgrade through `lookup`; do not delete it while
the database still has users created from it. To supply your own credentials,
set `clickhouse.auth.existingSecret` and provide the keys `admin-password` and
`gateway-password`.

## Grafana datasources

The example provisioning file is
`deploy/examples/grafana/provisioning/datasources/skopos.yaml`. It points all
three datasources at the gateway service. Replace the namespace and service
name if you changed them, then mount the file into Grafana's provisioning
directory.

The datasources use the alpha query heads. Supported queries are listed in
[promql-support.md](promql-support.md), [traceql-support.md](traceql-support.md),
and [logql-support.md](logql-support.md). Unsupported functions return an
explicit error, so a failing panel is usually a query outside the alpha list,
not a connection problem.

The chart can also ship the Skopos Overview and OBI RED dashboards through a
Grafana sidecar; see [monitoring.md](monitoring.md).

## Ingress example

`deploy/examples/kubernetes/ingress.example.yaml` is a starting point for an
ingress that points at the gateway Service. It is not part of the chart and it
is intentionally not secure by itself: terminate TLS at the ingress and enable
`auth.enabled=true` (or an authenticating proxy), add rate limiting, and
tighten `networkPolicy.gateway.allowFrom` to the ingress controller namespace.

## TTL, PVC, and disk-full behavior

- TTL is enforced by ClickHouse at the table level. The init job writes the TTL
  from the chart values. Changing TTL on an existing install requires a schema
  migration; the alpha chart's `CREATE TABLE IF NOT EXISTS` does not modify
  existing tables. The replay-dedup engine migration is separate and runs
  automatically; see [storage.md](storage.md#replay-deduplication).
- The PVC is a single `ReadWriteOnce` volume. ClickHouse single-node does not
  replicate. A lost PVC loses telemetry.
- When the disk fills, ClickHouse rejects inserts with errors such as
  `NOT_ENOUGH_SPACE` or `TOO_MANY_PARTS`. The Collector retries against its
  disk-backed queue and applies backpressure until the queue fills; after that
  OBI drops from its own bounded in-memory queue. The gateway remains readable
  but queries may fail. Increase the PVC (if the StorageClass supports
  expansion), lower TTL, or add storage; do not delete the ClickHouse data
  directory while the pod is running.
- The chart does not set a Prometheus alert for disk usage. Use your cluster's
  PVC alerts.
- Tiered storage is optional and off by default. When enabled, cold parts live
  in S3 and remain queryable, while TTL still deletes them at `totalDays`.
  Backups are separate: they are for disaster recovery and must be restored
  before the gateway can query them. The optional restore drill restores the
  latest backup into a temporary database and verifies every table. See
  [storage.md](storage.md) for values, existing-install `ALTER` steps, restore
  commands, and lifecycle ownership.
- The durability checkpoint is optional and off by default. When enabled, it
  uses its own per-replica WAL PVC and S3 prefix, independent of the ClickHouse
  hot and cold volumes. PVC loss loses the unshipped tail; uploaded objects
  remain in S3. See [checkpoint.md](checkpoint.md).

## Security notes

- No built-in auth. The gateway is a ClusterIP service. Do not expose it with an
  Ingress without adding authentication and TLS at the edge.
- The default gateway NetworkPolicy allows pods from any namespace to reach port
  8080. Tighten `networkPolicy.gateway.allowFrom` to your Grafana namespace.
  Setting it to an empty list denies all ingress.
- The Collector accepts OTLP from any namespace by default so applications and
  log forwarders can send telemetry. Tighten
  `networkPolicy.collector.allowFrom` to the emitting namespaces. ClickHouse is
  reachable only from the Collector, gateway, and init job. The v0.1 policies
  restrict ingress; egress restrictions are part of the M6 hardening work.
- OBI mounts host paths and needs `hostPID`. Treat it as a privileged agent.

## Upgrade and uninstall

```sh
helm upgrade skopos ./deploy/helm/skopos -n observability
helm uninstall skopos -n observability
```

On upgrade the ClickHouse init Job applies the current schema and, when needed,
migrates the signal tables from `MergeTree` to the replay-dedup
`ReplacingMergeTree` layout. It renames each old table aside, recreates it, and
copies the rows back while the new table is already live. Ingest retries during
the short rename window. Raise `clickhouse.initJob.activeDeadlineSeconds` for
large tables; a stale `<table>__skopos_pre_dedup` table means the copy was
interrupted and the next Job resumes it. See [storage.md](storage.md#replay-deduplication).

The ClickHouse PVC is not deleted by `helm uninstall`. Delete it explicitly only
after confirming you no longer need the data:

```sh
kubectl -n observability delete pvc data-skopos-clickhouse-0
```
