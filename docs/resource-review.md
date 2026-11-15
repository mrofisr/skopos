# Resource review

This page is a method for reviewing CPU, memory, replicas, disruption budgets,
and storage. It is not a sizing guarantee. The source of truth is
[`deploy/helm/skopos/values.yaml`](../deploy/helm/skopos/values.yaml); this page
explains what to measure and which failure mode each knob controls.

## Current chart defaults

The defaults are a starting point for a small evaluation cluster. They are not
recommended production values. Measure before changing them.

| Workload | Requests | Limits | Replicas | Disruption budget |
| --- | --- | --- | --- | --- |
| OBI (DaemonSet) | 100m / 256Mi | 1 CPU / 1Gi | one per node | none; a node agent cannot be drained like a Deployment |
| Collector | 100m / 256Mi | 1 CPU / 1Gi | 1 | `maxUnavailable: 1` |
| ClickHouse | 500m / 1Gi | 2 CPU / 4Gi | 1 | none; single-node alpha |
| Gateway (`gateway.mode=all`) | 50m / 128Mi | 500m / 1536Mi | 1 | `maxUnavailable: 1` |
| Frontend (`distributed.enabled=true`) | 50m / 64Mi | 500m / 256Mi | 1 | `maxUnavailable: 1` |
| Query worker (`distributed.enabled=true`) | 50m / 128Mi | 500m / 1536Mi | 1 | `maxUnavailable: 1` |
| Checkpoint | 50m / 64Mi | 500m / 256Mi | 1 | `maxUnavailable: 1` |

Storage defaults: ClickHouse PVC 50Gi, Collector queue PVC 5Gi, checkpoint WAL
PVC 2Gi. TTL defaults are 7 days for traces and logs, 30 days for metrics.

## Questions to answer

### CPU

- Is the workload CPU-bound or latency-bound? Measure p95 and p99 query latency,
  not only average CPU.
- Does the CPU limit throttle the process? Compare `container_cpu_cfs_throttled`
  with request latency under load.
- Is the request large enough to schedule on a node with the other components?
  The gateway, query worker, Collector, and ClickHouse may share a node in a
  small cluster.

### Memory

- What is the observed peak resident memory under the expected cardinality and
  query concurrency? Set the limit above the peak, not at the average.
- The gateway derives `GOMEMLIMIT` from 90% of `resources.limits.memory` unless
  `gateway.goMemLimit` is set. Keep the limit above the live heap plus the
  response and query buffers.
- The Collector has a `memory_limiter` at 700MiB with a 100MiB spike limit under
  a 1Gi limit. If you raise the limit, raise the limiter and the queue size
  together.
- ClickHouse `maxMemoryUsageBytes` is 1GiB under a 4Gi limit. A larger limit
  without a larger ClickHouse setting does not make a query faster; it only
  gives the process headroom.
- OOM kills are a failed review. A limit below the observed peak is not a
  capacity plan.

### Concurrency and admission

- The gateway default is 16 in-flight requests and 32 queued, with a 2s queue
  wait. A burst above that returns `429` or `503` with `Retry-After`; it does not
  grow memory without bound.
- ClickHouse execution is capped at 30s. `terminationGracePeriodSeconds` is 45s
  so an in-flight query and the HTTP drain can finish. Do not lower the grace
  period below the execution limit plus 10s.
- A range query consumes `ceil(points / pointsPerSlot)` units with the default
  `pointsPerSlot: 100`. A larger range is heavier; test it before raising the
  in-flight limit.

### Replicas and disruption

- A PDB with one replica and `maxUnavailable: 1` permits one voluntary
  disruption. It does not make a singleton highly available. Scale out before
  relying on the PDB during a node drain.
- The frontend and query tiers are stateless. The Collector has a disk-backed
  queue per replica; do not scale it down while a queue PVC still holds data you
  need to inspect.
- ClickHouse is a single StatefulSet with one PVC. It is not highly available in
  v0.1; a node drain or PVC loss is an outage. Use the backup and restore drill.

### Storage

- ClickHouse PVC size must cover the retention window plus merges, not only the
  current data size. The default 50Gi is an evaluation value.
- The checkpoint PVC must be larger than `maxLocalBytes` plus a filesystem
  reserve. The default is 2Gi against a 1Gi `maxLocalBytes`.
- Tiered storage moves cold parts to S3; it does not remove the need for a local
  hot volume. Size the cache and the hot window together.
- Backups go to a separate prefix and are not queryable. Size the bucket for the
  backup retention, not the live TTL.

### Autoscaling

The chart does not ship a HorizontalPodAutoscaler or a
cluster-proportional-autoscaler. If you add one:

- scale the frontend or query tier, not ClickHouse;
- use a real signal: request rate, p95 latency, or queue depth, not CPU alone;
- keep the PDB and readiness probe meaningful;
- test scale-up and scale-down under load, including a drain.

`prometheus-adapter` is a Kubernetes-native way to expose a custom metric for an
HPA. `cluster-proportional-autoscaler` is the reference for scaling with node or
core count. Both need a measured baseline before they are useful.

## How to measure

```sh
make bench          # compression and gateway latency baseline
make bench-gate     # concurrent safety gate
make profile-range  # pprof and metrics for the 6-hour range query
make e2e-grafana    # Grafana datasources, journeys, and the OBI RED dashboard
make metrics-contract
```

Record the environment, the data volume, the query mix, and the result. Change
one knob at a time. A resource change without a before/after measurement is a
guess.

## Change procedure

1. Record the baseline with the commands above.
2. Change one value in `deploy/helm/skopos/values.yaml`.
3. Re-run the same measurement.
4. Put the command, environment, and result in the pull request.
5. Run `make helm-lint` and `make helm-template`.
6. Update this page only if the method or the defaults change.

## Guardrails

- Do not copy a limit from another cluster or another chart without measuring.
- Do not set `requests` equal to `limits` unless you have measured the workload
  and want Guaranteed QoS.
- Do not lower a limit to make a cost target without checking the OOM and
  throttling behavior.
- Do not add an HPA without a load test and a PDB.
- Do not claim a sizing recommendation in a document without the measurement
  that supports it.

## References

- `deploy/helm/skopos/values.yaml`
- `deploy/helm/skopos/values.schema.json`
- `docs/benchmarks.md`
- `docs/perf/range-query-findings.md`
- `docs/storage.md`
- `docs/install.md`
