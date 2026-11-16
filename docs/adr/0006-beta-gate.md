# ADR 0006: Beta gate

- Status: Accepted for the beta planning line
- Date: 2026-11-15
- Updated: 2026-11-16 (B-03 scale evidence recorded)
- Related: [alpha-exit-plan.md](../alpha-exit-plan.md),
  [benchmarks.md](../benchmarks.md),
  [ADR 0005](0005-obi-wrapper-speed-and-durability.md),
  [B-01 (#14)](https://github.com/mrofisr/skopos/issues/14)

## Context

Alpha exit is complete (7/7) and `v0.0.1-alpha` is released. The query path now
holds p95 648 ms and 58.8 req/s at 50 VUs on the lab reference scale (about 560
series, 2.66M rows, one 4 vCPU node) after the `service_name` metadata pushdown
and the per-query k6 ratchet. That is a strong alpha result; it is not beta
evidence.

The alpha plan says beta proves: it stays fast at 10k and then 100k series,
latency SLOs set from observed data, HA ClickHouse with failover and restore
drills, authorization and tenant isolation, and a pilot on real cardinality.
The current deployment is one node, one gateway replica, auth disabled, and one
workload. Losing the node is an outage.

The alternative to a gate is to wait for a "perfect" configuration or a single
algorithm. Neither is a beta property. Beta is a promise about a known
envelope, known failure behavior, and a known cost, not about perfection.

## Decision

1. Remain on the alpha line until the gate below passes. Alpha releases may
   ship fixes; the deployment is not relabelled beta.
2. Beta evidence is measured, not guessed:
   - B-01: a deterministic scale harness with 10k and 100k series profiles.
   - B-02: a 10k profile run with a ranked bottleneck report.
   - B-03: the 100k profile.
   - B-04: p50/p95 budgets per query kind from B-02, B-03, and the pilot, with
     a relative nightly regression gate.
   - HA: at least two gateway replicas, ClickHouse replication or a proven
     restore path, a node-failure drill, and a restore drill.
   - Authorization and tenant isolation as specified by the beta scope.
   - A pilot at real cardinality with an error budget.
   - The OBI RED dashboard has a non-empty series per panel; the current
     whoami gap blocks this.
   - A published cost envelope: CPU and memory per 1k series and per 100
     req/s at 10k and 100k series.
3. Prefer adaptive control to perfect constants. The fixed per-head
   `maxInflight` and `queueDepth` are replaced by a limiter derived from
   measured latency and estimated cost once B-02 ranks the bottleneck.
4. The first post-alpha work is B-01 and B-02. The data chooses the next
   structural change (series catalog or materialized view, adaptive admission,
   rollups, caching); no optimization is merged without a measured bottleneck.

## Gate

| Gate | Evidence | Current state |
| --- | --- | --- |
| Scale | B-01, B-02, B-03 | **B-02 10k and B-03 100k measured**; see [benchmarks](../benchmarks.md) |
| Latency budgets | B-04 and a nightly relative gate | B-03 baseline recorded; p50/p95 budgets and nightly gate open |
| HA | two or more gateway replicas, node-failure drill, ClickHouse restore drill | single node and single replica |
| Auth and tenancy | auth enabled, isolation tests, tenant model | `SKOPOS_AUTH_ENABLED=false` |
| Pilot | real cardinality, error budget | not started |
| OBI | non-empty series per dashboard panel | whoami series stops at 00:36 UTC |
| Cost | CPU and memory per 1k series and per 100 req/s | resource peaks recorded at 10k and 100k; normalized envelope open |

## Consequences

- The next release is an alpha release with the metadata pushdown and the
  dependency bumps; the beta label waits for the gate.
- B-01 to B-03 are measured. B-04 becomes the critical path; the 100k profile
  puts the instant rate path, the metadata refresh, and the sample row budget
  on the B-04 agenda. The merged query-performance work is the first entry in
  that record, not the end of it.
- The "perfect configuration" question becomes an adaptive-control question
  with measured bounds.
- A failing gate is useful: it names the missing evidence instead of hiding it
  behind a version number.
