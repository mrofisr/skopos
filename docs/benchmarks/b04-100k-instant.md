# B-04: 100k instant path

Status: **in progress** — upgrade drill and dataset regeneration complete;
candidate measurements pending.

## Reference dataset

Regenerated on `k0s-lab` from seed 42 with the B-03 shape:

```sh
go run ./test/scale/cmd/scale-gen \
  --profile 100k --seed 42 --range 6h --step 60s --batch 50000 \
  --url http://127.0.0.1:18124 --database otel --user default --password "$PW"
```

Generator summary: `series=100000 services=50 samples=360
metric_samples=36000000 histogram_rows=18000 logs=100000 spans=100000
hash=b900affe5ce4b9ec`. After `OPTIMIZE ... FINAL`, `otel_metrics_sum` held
**36,000,418 rows** (36M generated plus live lab telemetry). The hash matches
the B-03 reference (`b900affe5ce4b9ec`); a dry run with the same flags confirms
it before writing.

## 2.5 Online upgrade drill

The lab started with the old hour key on all five metrics tables:

```text
ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(mapSort(Attributes)), TimeUnix, _skopos_row_hash
```

Deploying the bucketed chart (gateway image `dev-b04bucket`) ran the post-upgrade
init job, which applied `schema.sql` and then `migrate.sh`. Evidence:

| Check | Result |
| --- | --- |
| Sorting key after upgrade | `toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix, _skopos_row_hash` on all five metrics tables |
| Rows before → after | `otel_metrics_sum` 36,000,418 → 36,000,568 (live telemetry continued) |
| Backfill | `INSERT INTO ... SELECT` wrote 36,000,451 rows / 7.40 GiB in **183,743 ms** (≈184 s); B-04 prototype measured 195 s |
| Ingest continuity | Collectors stayed running through the rename/copy; row count grew rather than dropping |
| Re-run no-op | Re-running the init job logged only `migrate: schema is ready`; no copy statement in `system.query_log` |
| Replay dedup | Go integration tests `TestReplayDedupParityAcrossMetricsTables` cover all five re-keyed tables |

## 5.2 Candidate A — bucketed key + cost-aware admission

Configuration after tuning: `gateway.config.rowsPerSlot=100000`,
`instantFallbackWeight=30`, `workWeighting=true`, `maxInflight=256`,
`queueDepth=64`, `queueWaitSeconds=2`. The first trial with `maxInflight=32`
and `rowsPerSlot=10000` made each 100k-series rate cost 200 slots, above the
budget, so every instant query got the typed 400 over-capacity answer; the
knobs were tuned until a single expensive rate fits while bursts get 503.

Three 30-client runs (60 s each, 1 s warmup, `prom_range_6h` skipped):

| Run | Overall p95 | Throughput | Typed rejections |
| --- | ---: | ---: | ---: |
| 1 | 5,353 ms | 19.79 req/s | 329 |
| 2 | 9,173 ms | 18.07 req/s | 271 |
| 3 | 9,284 ms | 17.48 req/s | 300 |
| **Median** | **9,173 ms** | **18.07 req/s** | 300 |

Baseline B-03 at the same profile: 9,436 ms overall p95, 12.57 req/s, 0
rejections. The absolute latency gain at 100k is therefore modest and noisy;
the clear gains are throughput (+44%) and store work. `system.query_log` for
the concurrent window shows the rate pushdown reading **~934k rows/query**
(max 1,016,304) versus B-03's **6.36M rows/query** — a 6.8× reduction. The
metadata refresh still scans the full metrics table (max 36M rows); that is
pre-existing B-03 cause 2, not a new per-request scan from the cardinality
estimate (verified by the recorder test).

Resource samples on the lab node: ClickHouse ~1.4–1.8 GiB RSS, ~1.8–2.3 CPU;
gateway ~30 MiB RSS with short ~0.4 CPU bursts.

The checked-in `test/quality/bench/100k-concurrent.json` is the **median
envelope of the three runs above**: per-query p50/p95/p99 are medians, error
counts are maxima, and throughput is the median. A single run cannot serve as
the baseline because admission shedding and ClickHouse contention move the
tail by more than the gate's 15%/20% tolerances between runs. All rejections
are typed 503s with `Retry-After`, counted in `skopos_admission_rejected_total`;
there are no transport errors or unexpected statuses.

## 5.3 Candidate B — lab-only rollup

A hand-created `AggregatingMergeTree` rollup plus materialized view on
`otel_metrics_sum` (one row per five-minute bucket and series, storing
`min/max` time and `argMin/argMax` value states) was backfilled from the same
dataset in 37.8 s, producing 7.2M rows. Queries were hand-routed directly to
ClickHouse; the 30-client direct run measured **2,531 ms p95, 0 errors**.

Parity checks disqualify it under the decision rule:

| Check | Result |
| --- | --- |
| Monotonic series | rollup 1666.66666666663 vs exact 1666.66666666667 — matches within floating-point noise |
| Counter reset (`reset_probe_b04`: 10, 20, 5, 15) | exact rate **0.13889**, rollup **0.02778** — 5× divergence; the rollup has no reset state |
| Late data / replay dedup | rollup has no row-hash identity and must re-merge affected buckets; it cannot claim replay-dedup parity |

The rollup is faster but changes semantics. Per design D4 it cannot win, so
candidate A is the decision; no rollup product code is added by this change.

## Host and software

`k0s-lab`: one `oracle-cloud` node (4 CPU, 24 GiB), k0s v1.36.6, ClickHouse
26.8.36.3, gateway image `dev-b04bucket`. The gateway was deployed with one
replica, resource requests `100m`/`256Mi` and limits `3 CPU`/`1536Mi`;
ClickHouse `2 CPU`/`4Gi` (matching B-03's measured peaks). The earlier 2-replica
layout could not schedule with 3-CPU requests on a 4-CPU node.
