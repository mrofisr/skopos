# Design

## Context

See proposal.md — Why for motivation. The current state that shapes the
approach:

- All five metrics tables share the sort key
  `(ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(mapSort(Attributes)), TimeUnix[, _skopos_row_hash])`.
  The logs table already uses a five-minute bucket prefix — the pattern exists
  in the same schema.
- `deploy/helm/skopos/files/migrate.sh` performs rename-aside → recreate →
  backfill for engine changes, keeps the new table live before the copy, and
  recovers interrupted runs. It exits early for the fast profile today.
- Weighted admission already exists: per-head limiters with `AcquireWeight`,
  `ErrTooHeavy`, and cheap-waiter priority. The cost model weights range
  queries by result points; **instant queries always weigh 1**.
- A fixed-shape rate pushdown exists (FINAL, per-series first/last/reset
  aggregation, extrapolated rate in SQL). The B-04 prototype measured the
  bucketed key: rows 10.6× lower, bytes 8.6× lower, storage +9%, 30-concurrent
  p95 only ~24% better — the per-series window/aggregation still dominates.
- The lab (`k0s-lab`) is reachable from the development host. `test/scale`
  generates the 100k profile (seed 42, hash `b900affe5ce4b9ec`); `test/bench`
  runs the 30-client mix; `repoctl bench-gate` implements the ADR 0009 relative
  gate with baselines under `test/quality/bench/`.
- Constraints: ADR 0008 keeps reliable `FINAL` dedup as the default; the
  prototype rules out projections as a drop-in; ADR 0009 keeps absolute 100k
  budgets pilot-defined; every PR follows the red-first micro-ticket rules.

## Goals / Non-Goals

**Goals:**

- Prune instant reads to the touched five-minute buckets, with deterministic
  row-read bounds checkable in PR CI.
- Make admission bind on expensive instant work without changing the rejection
  contract or the limiter mechanism.
- Produce the measured rollup-vs-admission decision that ADR 0009 requires.
- Keep every parity gate at 0 divergences; keep the migration online,
  idempotent, and safe for both profiles.

**Non-Goals:**

- No rollup product code. The rollup is measured as a lab-only prototype; if it
  wins, implementation is a follow-up change (agreed with the maintainer).
- No projections in the default schema.
- No change to the weighted-limiter mechanism itself.
- No absolute 100k latency promise and no nightly-trigger enablement (the
  self-hosted runner is a separate human gate).
- No metric catalog, label-path, or long-range row-budget work (B-03 causes
  2–4).

## Decisions

### D1: In-place re-key through the existing migration pattern

Change the metrics `ORDER BY` in `schema.sql` to the prototype's measured key:
`(toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix[, _skopos_row_hash])`.
Extend `migrate.sh` to detect the hour key via `system.tables.sorting_key` and
run rename-aside → recreate → backfill for it, in **both** profiles (the
early-exit for the fast profile must not skip a key migration).

- **Why not a separate bucketed table maintained by a materialized view:** it
  doubles the write path and storage, adds MV + ReplacingMergeTree dedup risk,
  and leaves a permanent two-table routing fallback. The prototype's separate
  table was a measurement vehicle, not a target design.
- **Why not `ALTER ... MODIFY ORDER BY`:** the MergeTree family cannot rewrite
  existing parts' ordering in place; a copy is required regardless. The
  rename-aside pattern keeps the new table live before the copy and is already
  proven in this chart.
- **Dedup identity:** `TimeUnix` and the row hash stay in the key, and the
  bucket is a function of `TimeUnix`, so duplicate identity is unchanged.
  Replay-parity tests prove it.
- **Column order:** time bucket first gives the measured 10.6× pruning with an
  explicit bucket predicate; `MetricName` second keeps name equality cheap.
  The +9% storage cost is accepted and recorded in the benchmark report.

### D2: Explicit bucket predicate in the emitters

The rate and sample emitters add a parameterized
`toStartOfFiveMinutes(TimeUnix)` range predicate derived from the query window.
The prototype showed the planner does not infer bucket pruning from a raw
`TimeUnix` range alone, so the predicate is emitted explicitly. Golden SQL,
recorder row bounds, and the extended fuzz property prove the behavior.

- **Alternative considered:** rely on the primary index with the raw time
  range — rejected by the prototype evidence (hour-granularity pruning
  persists without the bucket predicate).

### D3: Instant cost = estimated rows read, mapped to slots

Extend the cost model so instant rate queries weigh
`ceil(estimated_rows / rowsPerSlot)`, where `estimated_rows` comes from a
cached per-metric cardinality estimate times the window's sample count.

- The estimate **piggybacks on the existing cached metadata refresh** (one
  grouped aggregation in the same probe), so admission adds no per-request
  scan. The TTL cache already bounds refresh frequency.
- When the estimate is unavailable (cold cache, new metric), the query gets a
  configured conservative fallback weight — never zero, never unbounded.
- Config knobs (`rowsPerSlot`, fallback weight, enable/disable) follow the
  existing `SKOPOS_POINTS_PER_SLOT` pattern: env + chart values + validation.
- **Alternative considered:** a static class weight for all rate queries.
  Simpler, but it cannot distinguish a 10-series rate from a 100k-series rate;
  kept as the fallback behavior, not the model.
- **Alternative considered:** a per-request cardinality probe — rejected: it
  adds a scan per request and worsens B-03 cause 2.
- The measurement stage tunes the knobs against the 30-client mix; the specs
  fix the contract, not the numbers.

### D4: Measurement protocol and decision rule

- Regenerate the 100k profile on the lab (seed 42, hash `b900affe5ce4b9ec`),
  `OPTIMIZE ... FINAL`, deploy candidate images.
- Candidate A: bucketed key + cost-aware admission (tuned). Candidate B:
  bucketed key + a **lab-only** rollup materialized view with hand-routed
  queries; no product code merges for B.
- Both run the same 30-client mix, three runs, median per-query p95 (ADR 0009
  method). Record p50/p95/p99, throughput, errors, rows/bytes read from
  `system.query_log`, resource peaks, storage delta, and write cost.
- Decision rule: prefer the candidate that protects the mix with the smallest
  semantic surface. The rollup can win only if it also demonstrates counter
  reset, late-data, and dedup parity on the corpus. If neither closes the gap
  sufficiently, record that honestly — absolute 100k budgets stay
  pilot-defined.
- Deliverables: `docs/adr/0011-100k-instant-path.md` (decision) and
  `docs/benchmarks/b04-100k-instant.md` (evidence). If the rollup wins, open
  the follow-up change instead of implementing it here.

### D5: prcheck accepts `phase:beta`

Extend the traceability guard to accept `phase:beta` alongside
`phase:alpha-exit` and `backlog:post-alpha`. The frozen-path guard stays
unchanged (ADR 0007 keeps the exception process). Red-first table fixtures:
beta issue passes, alpha issue still passes, unlabelled issue still fails.
This unblocks traceability for every beta PR, not just this change.

## Risks / Trade-offs

- [Backfill copies 36M rows on upgrade (~195 s measured at 100k; longer on
  larger datasets)] → the new table is live before the copy; ingest retries
  during the rename window; run the upgrade drill on the lab before merge and
  document the expected window.
- [+9% storage and worse compression from the time-first order] → recorded in
  the benchmark report; accepted against the 10.6× read reduction; revisit
  only with pilot data.
- [Bucket predicates change golden SQL across the emitters] → regenerate
  goldens in the same PR; parity suites and fuzz gate the change.
- [Cost model mis-estimates and sheds too much or too little] → conservative
  fallback, config knobs, measured tuning on the 30-client mix; rejections
  stay deliberate and counted; the relative gate catches regressions.
- [Rollup prototype shows tempting numbers but hides reset/late-data bugs] →
  corpus parity is a precondition for the rollup to win; no rollup product
  code in this change.
- [Migration interrupted mid-copy] → leftover detection and repeat copy
  (existing pattern); repeated copies collapse on the dedup key.
- [Rolling upgrade mixes old and new emitters] → both directions stay correct:
  old SQL on the new key is valid (less pruning); bucket predicates on the old
  key are valid filters (no pruning). Only performance differs, never results.

## Migration Plan

1. Merge schema + migration + emitters together; no feature flag is needed
   because correctness is invariant across the key change (see risks).
2. The init Job applies `schema.sql` then `migrate.sh` on install/upgrade
   (existing flow); the key migration runs for both profiles.
3. Lab upgrade drill on the 100k dataset before merge: row counts, replay
   parity, ingest continuity, re-run no-op (human checkpoint).
4. Rollback: the previous chart version leaves the bucketed key in place;
   old emitters remain correct on it. Correctness-safe; the key migrates
   forward again on the next upgrade.
5. After the decision ADR: update the bench baseline through a reviewed PR and
   re-run `repoctl bench-gate` against it.

## Open Questions

- Exact default values for `rowsPerSlot` and the fallback weight: answered by
  the measurement stage; the specs fix the contract, not the numbers.
- Whether the metadata refresh can carry per-metric cardinality within its
  row budget: verified during implementation; if not, the static fallback
  weight applies until a bounded estimate exists. The approach and task
  breakdown do not change either way.
