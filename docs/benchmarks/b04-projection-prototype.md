# B-04 prototypes: metric projections and a time-bucketed table at 100k

- Date: 2026-11-16
- ClickHouse: 26.8.36.3
- Source table: `otel.otel_metrics_sum`, 36M rows, 100k series
- Test table: a 1M-row copy covering 10 minutes of the 100k dataset
  (00:30–00:40Z, 100,004 series)
- Question: can a projection or a finer time key reduce the 6.36M-row
  instant rate query and its concurrency cost?

This prototype follows the first B-04 candidate in the
[B-03 report](b03-100k.md). It is a lab measurement, not a merged schema
change.

## Results

### 1. A ReplacingMergeTree rejects projections by default

```text
Code: 344. DB::Exception: ADD PROJECTION is not supported in
ReplacingMergeTree with deduplicate_merge_projection_mode = throw.
Please set setting 'deduplicate_merge_projection_mode' to 'drop' or
'rebuild'.
```

The test table needed `deduplicate_merge_projection_mode = 'rebuild'` before
`ADD PROJECTION` succeeded. That setting rebuilds the projection on merges;
the merge cost was not measured. The reliable profile cannot add a projection
as a plain index.

### 2. The metric-first projection only works for a raw name equality

A projection ordered by `(MetricName, TimeUnix, ServiceName, Attributes,
Value)` was materialized at **3.01 MiB** for 1M rows (the base table is 17.17
MiB).

It was used for a raw equality:

```sql
SELECT count() FROM otel.metrics_proj_test
WHERE MetricName = 'http_requests_total'
```

`EXPLAIN` shows `AggregatingProjection`. It was **not** used for the real
instant filter:

```sql
... WHERE TimeUnix > ... AND TimeUnix <= ...
  AND replaceRegexpAll(MetricName, '[^A-Za-z0-9_]', '_') = 'http_requests_total'
```

The normalized-name function is not sargable against the projection prefix,
so the planner falls back to the base table.

### 3. A time-first projection is not selected

Two more projections were materialized:

- `(toStartOfFiveMinutes(TimeUnix), MetricName, ...)`: 2.48 MiB.
- `(TimeUnix, MetricName, ...)`: materialized and active.

Neither was selected for a five-minute range query, even with an explicit
`toStartOfFiveMinutes(TimeUnix)` predicate and `force_optimize_projection=1`:

```text
Code: 584. DB::Exception: No projection is used when
optimize_use_projections = 1 and force_optimize_projection = 1.
(PROJECTION_NOT_USED)
```

The base table's `idx_time_minmax` index handled the time range instead. On
the 1M-row copy, the five-minute query read 1,000,080 rows; the copy only held
ten minutes, so the plan could not show the full-table hour-to-five-minute
reduction.

## Time-bucketed table

A separate copy of the table used the same columns and engine with a
five-minute time prefix:

```sql
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName,
          cityHash64(mapSort(Attributes)), TimeUnix, _skopos_row_hash)
```

The full 36M rows were inserted in 195 s. The copy is **637.86 MiB**
compressed (585.55 MiB for the base table, so **+9%**); the time-first order
compresses worse because it mixes series across each five-minute bucket.

The full by-service rate query at `2026-11-16 00:40:00`, with an explicit
`toStartOfFiveMinutes(TimeUnix)` predicate:

| Table | Rows | Read bytes | Query time | Memory |
| --- | ---: | ---: | ---: | ---: |
| Base `(ServiceName, MetricName, hour, ...)` | 6,360,155 | 412.20 MiB | 1,156 ms | 144 MiB |
| Time-bucketed `(five-minute, MetricName, ...)` | **598,016** | **47.94 MiB** | **893 ms** | 132 MiB |

The results match: 2,000 series and `33.333…` per service for all 50 services.
The bucket cuts rows **10.6×** and bytes **8.6×**, but query time only
**1.29×**.

Thirty concurrent copies of the same query, three rounds, direct to
ClickHouse (no gateway cache):

| Table | p50 | p95 |
| --- | ---: | ---: |
| Base | 38.5–39.2 s | 41.4–42.1 s |
| Time-bucketed | **29.5–30.5 s** | **31.5–32.2 s** |

The concurrency p95 improves by about **24%**, not by the 8.6× byte
reduction. The 100k-series window and rate aggregation dominate once the scan
is cheap. The time bucket is necessary for I/O and memory pressure, but it is
not sufficient for the 30-client latency budget.

## What this means

A projection is **not a drop-in fix** for the 100k instant path:

1. The reliable `ReplacingMergeTree` rejects it unless the table changes its
   projection merge mode; `rebuild` adds merge work that has not been measured.
2. The projection that could prune the metric name is only used for a raw
   `MetricName =` filter, not the normalized-name function the gateway emits.
3. The time-first projections were not selected for the time-range query.

A **separate time-bucketed table** is a real but partial improvement: it cuts
rows 10.6× and bytes 8.6×, yet the 100k-series rate computation keeps the
query at ~0.9 s and the 30-concurrent p95 at ~32 s. B-04 should pair the time
key with one of:

- a **pre-aggregated rollup** that reduces the 100k series to the 50 services
  (the rate/increase state must preserve counter resets, late data, and
  dedup), or
- a **cost-aware admission limit** that caps concurrent expensive rate
  queries instead of the flat 32-request limit.

Measure both against the 30-client mix. If a projection is revisited, pair it
with a raw-name equality fast path and measure
`deduplicate_merge_projection_mode=rebuild` merges. Do not add a projection to
the default schema from this evidence.
