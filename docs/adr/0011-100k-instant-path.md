# ADR 0011: 100k instant path (B-04)

- Status: Accepted
- Date: 2026-11-18
- Related: [ADR 0009](0009-latency-budgets-and-bench-gate.md),
  [B-03 100k](../benchmarks/b03-100k.md),
  [B-04 prototype](../benchmarks/b04-projection-prototype.md),
  [B-04 measurements](../benchmarks/b04-100k-instant.md),
  [change proposal](../../openspec/changes/b04-100k-instant-path/proposal.md)

## Context

At 100k series the B-03 reference run measured 9,436 ms overall p95,
~10.6 s instant p95, 12.57 req/s, and 6.36M rows read by each instant rate
query. Two ranked causes dominate: the hour-granularity metrics sort key, and
admission that weights every instant query as one slot so 30 expensive
queries enter together. ADR 0009 requires a measured decision between bucket
pruning with cost-aware admission and a rollup, with 100k absolute budgets
left pilot-defined.

## Candidates

Both ran on the regenerated seed-42 dataset (`b900affe5ce4b9ec`) with the
five-minute key already applied and the same 30-client mix, three runs,
median per-query p95.

- **A — bucketed key + tuned cost-aware admission.** `rowsPerSlot=100000`,
  fallback weight 30, weighting enabled, `maxInflight=256`, queue 64/2 s.
  Median overall p95 **9,173 ms**, throughput **18.07 req/s**, ~300 typed
  503 rejections per 60 s run. Rate pushdown reads ~934k rows/query vs
  6.36M in B-03 (6.8× fewer).
- **B — bucketed key + lab-only rollup materialized view.** Direct hand-routed
  queries against a per-bucket `AggregatingMergeTree` of min/max time and
  argMin/argMax value states: p95 **2,531 ms**, 0 errors, 7.2M rollup rows
  (37.8 s backfill).

## Decision

**Choose candidate A.** Candidate B is faster but fails the parity
precondition in design D4: on a counter-reset fixture (10, 20, 5, 15) the
exact rate is 0.13889 while the rollup returns 0.02778, a 5× divergence. The
rollup also has no replay-dedup identity and needs explicit late-bucket
re-merge. Adding reset/late-data state to the rollup is a separate semantic
surface; A keeps the existing exact rate engine and the unchanged rejection
contract.

A's absolute latency gain is modest and noisy (9,173 ms vs 9,436 ms), so this
ADR does not claim a closed 100k latency budget. It records a materially
lower store read (6.8×), higher throughput (+44%), and deliberate,
typed shedding of work that would otherwise saturate ClickHouse.

## Consequences

- The five-minute key, its online migration, bucket predicates, and the
  cost model ship together; the keys and predicates are correctness-neutral
  across rolling upgrades.
- The reviewed 100k baseline is updated from candidate A; the relative gate
  then protects the new position.
- A rollup remains a documented experiment. If semantic parity is ever
  achieved, implementation belongs in a follow-up change.
- The metadata refresh still performs a full grouped scan; B-03 cause 2 is
  explicitly out of scope here.
