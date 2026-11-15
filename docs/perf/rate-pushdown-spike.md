# Spike: push `sum(rate(...))` into ClickHouse (#25)

- Date: 2026-11-15
- Issue: [#25](https://github.com/mrofisr/skopos/issues/25)
- ClickHouse: 26.8.36.3
- Dataset: 10k `test/scale` series, 5-minute instant window
- Question: can ClickHouse compute the exact Prometheus `sum(rate(...))`
  without returning raw samples?

## Findings

ClickHouse 26.8 has no `rate` aggregate (`UNKNOWN_FUNCTION`). The exact
Prometheus result can still be computed with window aggregates:

- `argMin(Value, TimeUnix)` / `argMax(Value, TimeUnix)` for the first and last
  values;
- `min(TimeUnix)` / `max(TimeUnix)` / `count()` for the sample interval;
- `sumIf(prev, Value < prev)` with
  `lagInFrame(Value) OVER (PARTITION BY series ORDER BY TimeUnix)` for the
  counter-reset correction.

The Prometheus `extrapolatedRate` factor is then plain arithmetic:
`increase = last - first + resetSum`, `sampled = lastT - firstT`,
`avg = sampled / (count - 1)`, the start/end extrapolation with the 1.1×
threshold and the zero-point clamp, then `increase * factor / rangeSeconds`.

## Measurements

Same lab ClickHouse, 10k series, 5-minute window:

| Query | Time | Result size |
| --- | ---: | ---: |
| Current grouped samples | 0.13 s | 2.59 MB |
| Per-series aggregates (window) | 0.14 s | 1.85 MB |
| Per-series aggregates (no reset sum) | 0.11 s | 1.82 MB |
| **Full `sum(rate)` pushdown** | **0.13 s** | **1 row** |
| **Full `sum by (service_name)`** | **0.10 s** | **10 rows** |

The gateway’s current single instant query is **0.6 s**. The pushdown result
matched the gateway exactly within float noise:

```text
gateway sum:                 166.66666666666666
ClickHouse pushdown sum:     166.66666666666634
relative difference:         2e-15  (differential tolerance is 1e-9)
service-0, both:             16.666666666666668 / 16.666666666666654
```

The SQL used `TimeUnix > start AND TimeUnix <= end` so the left-open Prometheus
range boundary is preserved, and `FINAL` on the signal table.

## Proposed implementation

1. **`sqlgen.RateQuery`** with a fixed nested template:
   inner `FINAL` table + parameterized `WHERE`/time range, the window
   aggregate, the per-series rate arithmetic, and an outer `sum` (optionally
   `GROUP BY ServiceName`). Identifiers come from the schema, values from
   `plan.Param`; no user SQL.
2. **`prom.RateFetcher`** optional interface implemented by
   `ClickHouseFetcher`. It discovers the metric tables as `Fetch` does, runs the
   rate query per table, and merges the scalar/service results.
3. **`Engine.Query` fast path** for exactly:
   - `sum(rate(<vector selector>[<range>]))`
   - `sum by (service_name) (rate(<vector selector>[<range>]))`
   with an instant evaluation, short range, and no unsupported modifiers.
   Everything else falls back to the current sample path.
4. **Tests:** emitter golden SQL; fixture parity against the current engine
   (including a counter reset and an empty result); integration against
   ClickHouse; the Prometheus differential corpus; the 10k/30-client bench.

## Guards and open edges

- `service_name` must use the same empty-value fallback as `baseLabels`
  (`Attributes['service.name']`), or the fast path should fall back.
- `sum by` labels other than `service_name` need attribute extraction; fall
  back for now.
- Gauge tables and `irate`/`increase` are out of scope for the first fast path.
- `offset`/`@` can be supported by shifting the window; validate before use.
- Long windows must not use the pushdown (the result-row cap no longer bounds
  the inner series); keep it to the instant/short-window path.
- Float arithmetic order differs slightly; the differential harness uses a
  1e-9 relative tolerance, so this is acceptable.

## Expected effect

At ~0.13 s of SQL plus a one-row response, a single instant query should fall
from ~0.6 s to roughly 0.2 s through the gateway. Thirty concurrent queries on
the 4-vCPU lab then sit near 1.5 s instead of 4–5 s, which is the first path
likely to close the #25 <2 s gate. The next step is the `sqlgen` + `RateFetcher`
implementation and a lab rerun.
