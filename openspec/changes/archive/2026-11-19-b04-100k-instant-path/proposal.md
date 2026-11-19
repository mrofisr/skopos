# Proposal

## Why

The beta latency gate (ADR 0006) is open at 100k series: B-03 measures 9,436 ms
overall p95, ~10.6 s instant p95, and 12.57 req/s on the lab reference host,
against 954 ms and 69.50 req/s at 10k. Two ranked causes dominate the 30-client
mix: the instant rate query reads 6.36M rows because the metrics sort key's
time granularity is one hour, and admission weights every instant query as 1,
so 30 expensive queries all enter and saturate ClickHouse's two cores. ADR 0009
defers the fix choice (rollup vs cost-aware admission) to measurement, and the
B-04 prototype shows a five-minute time bucket cuts rows 10.6× but is not
sufficient alone.

## What Changes

- Re-key the five metrics tables to a five-minute time-bucket prefix, following
  the existing logs-table pattern, with an idempotent online migration that
  preserves replay-dedup identity and also runs for the fast profile.
- Make the rate and sample emitters bucket-aware so instant reads prune to the
  touched five-minute buckets; prove deterministic row-read bounds with the
  per-request query recorder.
- Add a cardinality-aware cost model for instant queries to the existing
  weighted admission limiter (config-driven, safe defaults). The rejection
  contract is unchanged: deliberate 429/503 with `Retry-After` in the head's
  error shape, 400 for over-capacity queries, probes exempt.
- Measure both B-04 candidates on the lab 30-client mix: bucket + admission vs
  bucket + a lab-only rollup prototype (no merged rollup code). Record the
  decision and its trade-offs in a new ADR. If the rollup wins, its
  implementation is a follow-up change, not part of this one.
- Update the evidence: B-04 benchmark report, a reviewed bench-baseline update,
  the `beta-gates.md` latency row, and regenerated limits if defaults change.
- Prerequisite: extend `repoctl prcheck` to accept `phase:beta` issues (it
  rejects them today) and open the tracking issue for this work.
- No API breaks. The schema migration runs on upgrade while ingest continues.

## Capabilities

### New Capabilities

- `metrics-storage`: metrics table sort-key shape, the time-bucket pruning
  contract, the online re-key migration, and replay-dedup identity preservation
  across the reliable and fast profiles.
- `query-admission`: the weighted admission cost model, including
  cardinality-aware instant cost, the rejection and error contract, and probe
  exemptions.
- `instant-rate-pushdown`: instant rate pushdown read bounds, bucket pruning,
  reference-engine parity, and parameterized-SQL safety.

### Modified Capabilities

None. `openspec/specs/` is empty; this change introduces the first specs.

## Impact

- **Schema and migration**: `deploy/helm/skopos/files/schema.sql`,
  `deploy/helm/skopos/files/migrate.sh`, schema renderers and their tests.
- **Query path**: `internal/sqlgen` (rate and sample emitters), `internal/prom`
  (storage/rate routing), golden SQL and fuzz targets.
- **Admission**: `internal/app/gateway/cost.go`, `internal/config`, chart
  values and render tests; `internal/admission` invariants stay unchanged.
- **Tooling**: `tools/repoctl/internal/prcheck` (accept `phase:beta`).
- **Evidence and docs**: new decision ADR, `docs/benchmarks/` B-04 report,
  `docs/benchmarks.md`, `docs/beta-gates.md`, `test/quality/bench/` baselines,
  regenerated `docs/support-matrix.md` limits if defaults change.
- **Gates that must stay green**: `make compat-all` (0 divergences),
  `make bench-gate`, `repoctl bench-gate`, `make check`, coverage ratchet.
- **Out of scope**: metric catalog/skip index (B-03 cause 2), label-path scans
  (cause 3), the long-range row budget (cause 4, already an explicit 422), the
  HA-profile baseline, absolute 100k budgets (pilot-defined per ADR 0009),
  rollup implementation, and the frozen surfaces (ADR 0007).
- **Human checkpoints**: tracking-issue creation, lab migration execution,
  baseline-update approval, and ADR acceptance.
