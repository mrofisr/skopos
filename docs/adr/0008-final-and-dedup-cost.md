# ADR 0008: Keep reliable replay dedup and defer adaptive FINAL (B-06)

- Status: Accepted for the beta planning line
- Date: 2026-11-16
- Related: [ADR 0003](0003-replay-deduplication.md),
  [ADR 0005](0005-obi-wrapper-speed-and-durability.md),
  [ADR 0007](0007-frozen-surfaces-maintenance.md),
  [benchmarks.md](../benchmarks.md),
  [B-02](https://github.com/mrofisr/skopos/issues/18)

## Context

ADR 0003 makes `ReplacingMergeTree` plus read-time `FINAL` the default reliable
profile; ADR 0005 adds the opt-in fast profile (`clickhouse.dedup.enabled=false`
and `FINAL` on traces only). B-06 was parked because the adaptive-FINAL change
was too large for alpha and needed B-02 cost data and the B-08 checkpoint
decision.

The B-02 10k report ranked the top causes:

1. `/series` raw-sample fetch (#19).
2. The 6 h range result limit (#20).
3. The label-values sample scan with `FINAL` (#21).
4. Shared saturation and admission.
5. JSON decode and allocation (#25).

#19, #20, #21, and #25 are fixed: `/series` is label-only, the scope/result
caps are explicit, metadata reads skip `FINAL` and use a cache, and the instant
path uses grouped decoding plus a rate pushdown and a one-second result cache.
The 10k lab gate now measures 69.50 req/s and 716 ms instant p95 with zero
503s.

The remaining `FINAL` cost from the 2.66M-row storage benchmark is real but
not the current 10k bottleneck:

| Evidence | Reliable | Fast |
| --- | ---: | ---: |
| Storage | 87.12 MiB | 48.93 MiB |
| Insert time | 27.5 s | 15.1 s |
| Direct log queries | baseline | 2–2.8x faster |
| Metric 6 h range | baseline | ~15% faster |
| Trace queries | same | same (trace identity dedup is kept in both) |

The B-02 `FINAL` finding was the metadata/label-values path, which #21 fixed;
the remaining log difference is at single-digit-millisecond direct SQL latency.
The parked adaptive-FINAL branch (`6bc2092` on `agent/27-bounded-final`) has
not been exercised at 100k series. B-08 keeps the checkpoint maintenance-only,
so replay dedup remains a product property.

## Decision

1. **Keep the reliable profile as the default.** `clickhouse.dedup.enabled=true`
   remains the default; sample reads keep `FINAL`; metadata reads keep the
   skip/cache behavior from #21.
2. **Do not merge adaptive FINAL now.** The parked branch stays parked. The
   B-02 top-three `FINAL` cost was metadata/label values and is fixed; the
   remaining log cost does not justify a large read-path change before the
   100k data exists.
3. **Keep the fast profile opt-in.** It remains at-least-once and is not the
   default. Operators who prefer ClickStack-like storage and insertion accept
   replay duplicates explicitly.
4. **Revisit after B-03/B-04 or the pilot.** If the 100k profile puts `FINAL`
   among the top three costs, or a log latency budget is missed, amend this
   ADR and start from the parked branch. Any adaptive behavior needs its own
   measurement and parity tests.

## Consequences

- Replay deduplication and the 87 MiB reliable storage profile remain the
  default; the fast profile's 49 MiB option remains documented.
- The checkpoint's maintenance-only status supports keeping replay dedup; the
  two decisions are consistent.
- No read-path code changes are made by this ADR.
- A future adaptive-FINAL change is gated on 100k evidence, not on a hunch or
  on the alpha-era parked branch alone.
- The OBI wrapper continues to favor correctness after replay; operators who
  need the lower storage/insert cost can opt into the fast profile and accept
  at-least-once.

## Alternatives

- **Default to the fast profile.** Rejected: it makes replay duplicates
  visible and gives up the reliable property the checkpoint exists to support.
  The storage saving is large, but it is an explicit operator choice.
- **Merge adaptive FINAL now.** Rejected: the 10k top-three `FINAL` cost was
  the metadata path already fixed by #21; the parked branch is untested at
  100k, and the change is large relative to the current evidence.
- **Move dedup to the Collector or checkpoint.** Rejected by ADR 0003: OTLP
  re-batching and the absence of a universal record id make transport-level
  dedup unreliable.
