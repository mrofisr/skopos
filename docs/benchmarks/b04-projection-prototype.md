# B-04 prototype: metric projections at 100k

- Date: 2026-11-16
- ClickHouse: 26.8.36.3
- Source table: `otel.otel_metrics_sum`, 36M rows, 100k series
- Test table: a 1M-row copy covering 10 minutes of the 100k dataset
  (00:30–00:40Z, 100,004 series)
- Question: can a projection reduce the 6.36M-row instant rate query?

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

## What this means

A projection is **not a drop-in fix** for the 100k instant path:

1. The reliable `ReplacingMergeTree` rejects it unless the table changes its
   projection merge mode; `rebuild` adds merge work that has not been measured.
2. The projection that could prune the metric name is only used for a raw
   `MetricName =` filter, not the normalized-name function the gateway emits.
3. The time-first projections were not selected for the time-range query.

The B-04 work should test a **separate time-bucketed table** (or a new sort
key) with an explicit bucket predicate, then measure storage, insert/merge
cost, and dedup/`FINAL` correctness. If a projection is revisited, pair it
with a raw-name equality fast path and measure
`deduplicate_merge_projection_mode=rebuild` merges. Do not add a projection to
the default schema from this evidence.
