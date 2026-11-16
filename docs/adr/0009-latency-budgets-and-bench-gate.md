# ADR 0009: Latency budgets and the relative bench gate (B-04)

- Status: Accepted for the beta planning line; budgets are provisional until
  the pilot
- Date: 2026-11-16
- Related: [ADR 0006](0006-beta-gate.md),
  [B-02 10k](../benchmarks/b02-10k.md),
  [B-03 100k](../benchmarks/b03-100k.md),
  [B-04 prototypes](../benchmarks/b04-projection-prototype.md),
  [instant-decode](../benchmarks/instant-decode.md),
  [benchmarks.md](../benchmarks.md)

## Context

ADR 0006 requires B-04: p50/p95 budgets per query kind, taken from B-02,
B-03, and the pilot, plus a relative nightly regression gate. The evidence
now exists for 10k and 100k; the pilot does not.

The measured positions are far apart:

| Scope | 10k gate | 100k B-03 |
| --- | ---: | ---: |
| Throughput | **69.50 req/s** | **12.57 req/s** |
| Overall p95 | **954 ms** | **9,436 ms** |
| Instant p95 | **716 ms** | **10,614 ms** |
| Errors | 0 | 0 |

The 10k gate is the post-fix lab run (`dev-ddd295e`) from
[instant-decode](../benchmarks/instant-decode.md). The 100k run
(`dev-1a60b00`) is [B-03](../benchmarks/b03-100k.md). At 100k, ClickHouse
saturates its two cores while the gateway uses under 10% of its three-core
limit. The instant rate query reads 6.36M rows because the metrics sort key's
time prefix is one hour, and the 100k per-series window/aggregation still
dominates after a five-minute time key cuts rows 10.6×
([prototype](../benchmarks/b04-projection-prototype.md)).

The 100k numbers are a baseline, not a latency promise. A budget set at 10 s
would codify a bad result; a budget set without the pilot would invent user
expectations. The gate also needs a stable environment: the repository has no
self-hosted runner, and a GitHub-hosted runner cannot reproduce the lab
baseline.

## Decision

### 1. Two kinds of budget

- **Absolute budgets** are user-facing p50/p95 targets per query kind. The
  pilot confirms them; until then, the 10k budgets below are provisional and
  the 100k budgets are marked open.
- **Relative budgets** are the nightly ratchet: a run fails when a query kind
  regresses against a checked-in baseline from the same profile, host class,
  and dataset. The relative gate catches regressions; it does not claim that
  the current 100k latency is acceptable.

### 2. Provisional 10k budgets

These come from the 10k gate with headroom: the p50 budgets are about twice
the measured p50, and the p95 budgets are the #25 gate for instant and about
twice the measured p95 for the rest.

| Query kind | 10k p50 budget | 10k p95 budget | 10k measured p50 / p95 | State |
| --- | ---: | ---: | ---: | --- |
| Prom instant (count) | 800 ms | 2,000 ms | 359 / 716 ms | met |
| Prom instant (by service) | 800 ms | 2,000 ms | 284 / 714 ms | met |
| Prom histogram | 600 ms | 1,000 ms | 266 / 483 ms | met |
| Prom series | 1,700 ms | 2,000 ms | 830 / 1,270 ms | met |
| Prom label values | 600 ms | 1,000 ms | 269 / 446 ms | met |
| Overall | 800 ms | 1,500 ms | 364 / 954 ms | met |
| Throughput | — | at least 48 req/s | 69.50 req/s | met |
| Errors | — | 0 | 0 | met |

The 10k gate report lists only the Prom paths. Tempo search, Tempo trace by
ID, Tempo tags, and Loki range are **pilot-defined**; the relative gate still
covers them.

### 3. 100k position

The 100k absolute budgets are **open until the pilot**. The B-03 baseline is
the ratchet:

| Query kind | 100k B-03 p50 / p95 | Absolute budget |
| --- | ---: | --- |
| Prom instant (count) | 8,273 / 10,614 ms | pilot-defined |
| Prom instant (by service) | 8,188 / 9,455 ms | pilot-defined |
| Prom histogram | 297 / 509 ms | pilot-defined |
| Prom series | 1,332 / 1,894 ms | pilot-defined |
| Prom label values | 187 / 1,447 ms | pilot-defined |
| Overall | 510 / 9,436 ms | pilot-defined |
| Throughput | 12.57 req/s | pilot-defined |

The 100k absolute budgets are open, and the current position is far from the
10k budgets. Closing the gap belongs to the B-04 optimization work (a
per-service rollup or a cost-aware admission limit) and the pilot; this ADR
does not choose between them. The relative gate protects the baseline in the
meantime.

### 4. The relative nightly gate

- A baseline is one `test/bench` JSON file plus metadata: dataset hash, image
  tag, host class, bench flags, per-query p50/p95/p99, throughput, and error
  count. Baselines live under `test/quality/bench/`.
- A gate run repeats the bench three times and takes the median per-query
  p95. Sequential runs use 30 iterations after 3 warmups; concurrent runs use
  30 clients for 60 s after 1 warmup; the 6 h range is skipped.
- The run fails when any query's p95 is more than **20%** above the baseline,
  p50 is more than **15%** above, throughput is more than **15%** below, or a
  query that had no errors now has one. The run must use the same dataset hash
  and host class as the baseline.
- Baselines move only through a reviewed pull request that includes a
  benchmark report and a measured improvement. A regression that must be
  accepted needs a documented `bench-exception` reason and human approval;
  the workflow never updates a baseline automatically.
- The gate compares relative values; it does not enforce the absolute budgets.
  The pilot and the beta acceptance check enforce those.

### 5. Runner prerequisite

The 100k gate needs a stable host: the lab's 4 vCPU / 24 GiB k0s node is the
reference. A GitHub-hosted runner is not a substitute because its CPU and
memory vary and the 36M-row dataset generation dominates the run.

Until a self-hosted `skopos-bench` runner is registered, the gate runs as a
documented manual command on the lab; a GitHub workflow needs that runner
label and cannot use the lab without it. The gate tool and the baseline
format can be implemented before the runner exists; enabling the nightly
trigger is a separate, human-approved change.

## Consequences

- The 10k latency row is met. The 100k row is open and the beta gate stays
  open until the pilot and the B-04 optimization work close it.
- Regressions become visible against a measured baseline instead of a
  version number. A baseline update is a deliberate, reported change.
- A dedicated lab runner is a prerequisite for the nightly 100k gate. The
  lab remains the reference environment.
- The 100k optimization decision (rollup, admission, or both) is not made
  here; the B-03 report and the prototype record the evidence.
- No code, schema, or chart change is made by this ADR.

## Alternatives

- **Set absolute 100k budgets now.** Rejected: there is no pilot data, and a
  budget at the current 10 s p95 would make the regression gate meaningless.
- **Run the gate on every pull request.** Rejected: the dataset and the
  30-client run are too slow and too noisy for PR latency; a nightly or
  dispatched run is the right cadence.
- **Use a GitHub-hosted runner for 100k.** Rejected: the host class varies,
  the baselines would not transfer, and the dataset generation dominates.
- **Wait for the pilot before adding any gate.** Rejected: ADR 0006 requires
  a relative nightly gate, and the 100k path needs regression protection
  while the optimization work lands.
