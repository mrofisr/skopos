# ADR 0005: OBI-wrapper speed and durability

- Status: Accepted for the alpha line
- Date: 2026-11-14
- Related: [benchmarks.md](../benchmarks.md), [checkpoint.md](../checkpoint.md),
  [storage.md](../storage.md), [ADR 0003](0003-replay-deduplication.md)

## Context

The storage benchmark compared the Skopos ClickHouse schema with ClickStack's
default schema on the same ClickHouse 26.8.36.3 instance and the same
2.66M-row synthetic telemetry set.

| Signal | Skopos | ClickStack | Difference |
| --- | ---: | ---: | ---: |
| Metrics | 45.42 MiB | 5.15 MiB | +8.8x |
| Logs | 52.40 MiB | 39.01 MiB | +34% |
| Traces | 4.90 MiB | 6.06 MiB | -24% |
| Total | 103.66 MiB | 50.25 MiB | +106% |

The gap was not ClickHouse itself. Most of it was the 16-byte
`_skopos_row_hash` used for replay deduplication: 37.67 MiB, or 43% of the
reliable Skopos storage. The measured fast profile (`dedup.enabled=false`)
stores **48.93 MiB**, slightly smaller than ClickStack's 50.25 MiB.

Skopos also lacked two reliability properties an OBI wrapper needs:

- The Collector's queue was in memory unless the checkpoint was enabled, so a
  Collector pod restart could lose accepted batches.
- The ClickHouse exporter stopped retrying after five minutes, silently
  reopening a loss window during a longer outage.

## Decisions

### 1. Use ZSTD(3) for signal columns

All non-hash columns in `deploy/helm/skopos/files/schema.sql` use
`CODEC(ZSTD(3))` instead of `ZSTD(1)`. Measured on the benchmark dataset:

- total storage: 103.66 MiB to 91.9 MiB (-11.3%)
- metrics sum: -17.9%, gauge: -15.9%, histogram: -42.8%
- traces: -20.1%
- load time: 29.6 s to 28.3 s (no regression)

Logs barely moved because their storage is dominated by text indexes.

### 2. Drop the redundant case-insensitive body index

The default schema shipped both `idx_body Body` and
`idx_lower_body lower(Body)`. LogQL is case-sensitive and only uses `idx_body`.
The second text index cost about 5 MB per 500k log rows and is removed from the
default schema. With both changes the benchmark total is **87.12 MiB**, down
15.9% from the 103.66 MiB baseline, and load time is 27.4 s. Operators who run
case-insensitive `lower(Body)` SQL outside the gateway can add it explicitly.

### 3. Make the Collector queue durable and bounded

The chart now always enables the `file_storage` extension and puts both the
ClickHouse and checkpoint exporters on a disk-backed queue:

- PVC-backed by default (`collector.sendingQueue.persistence.enabled=true`,
  5 GiB), so queued batches survive Collector restarts and pod rescheduling.
- `retry_on_failure.max_elapsed_time: 0s` for ClickHouse: the bounded disk
  queue supplies backpressure instead of a fixed retry window silently
  dropping data.
- A `memory_limiter` processor keeps the Collector below its 1 GiB limit
  (700 MiB limit, 100 MiB spike) and applies backpressure before the OOM
  killer.

The emptyDir fallback remains for clusters that cannot provision another PVC.

### 4. Increase OBI batch and queue sizes

OBI's OTLP exporter moves from 512 to 2048 records per batch and from 2048 to
8192 queued records. This reduces gRPC/send overhead and absorbs short
Collector stalls without changing OBI's capture behavior.

### 5. Add an opt-in fast profile

`clickhouse.dedup.enabled=false` renders the fast profile:

- `otel_logs` and all metric tables use `MergeTree` and omit
  `_skopos_row_hash`; no row hash is stored or computed.
- Traces keep `ReplacingMergeTree` and the `(TraceId, SpanId)` identity; that
  dedup is cheap and does not need a hash column.
- The default remains `true` (the reliable profile). Fast mode is
  at-least-once: the durable Collector queue protects transport, but a
  checkpoint replay can duplicate rows in ClickHouse.
- The ClickHouse client remembers `ILLEGAL_FINAL` per table for one minute and
  strips `FINAL` before the request, so fast-mode log and metric reads do not
  pay a failed round trip on every query. The cache also covers a gateway that
  starts before the dedup migration finishes.

The migration script skips engine conversion when dedup is disabled. Switching
fast to full is automatic (`MergeTree` to `ReplacingMergeTree`); switching full
to fast keeps the existing hash tables until they are rebuilt, which is
documented as an alpha limitation.

### 6. Keep the default reliable

The benchmark trade-off is explicit:

| Profile | Storage | Insert | Replay dedup |
| --- | ---: | ---: | --- |
| Reliable (`dedup.enabled=true`) | 87.12 MiB | 27.5 s | logs, metrics, traces |
| Fast (`dedup.enabled=false`) | 48.93 MiB | 15.1 s | traces only |
| ClickStack default | 50.25 MiB | 14.8 s | none |

The default stays reliable. The fast profile exists for installations that
prefer ClickStack-like storage and ingestion and accept at-least-once replay.
On the direct-SQL query benchmark, fast matched or beat ClickStack on every
query; reliable paid 2-2.8x on log queries and about 15% on metric ranges for
`FINAL` dedup, while trace timings were unchanged.

## Consequences

- New installs get smaller and faster storage without losing replay dedup.
- The Collector no longer drops accepted batches during a ClickHouse or
  checkpoint outage until the bounded queue and OBI queue are both full.
- Existing installs keep their old codecs and indexes because
  `CREATE TABLE IF NOT EXISTS` does not alter them. The alpha migration script
  handles engine changes only; changing codecs/indexes requires a table
  rebuild, which is acceptable before the first compliant release.
- The default Collector needs one more PVC. Clusters without a default
  StorageClass can set `collector.sendingQueue.persistence.enabled=false` and
  keep the emptyDir fallback.
- Storage is still larger than ClickStack in reliable mode because of the
  replay-dedup hash: 87.12 MiB versus 50.25 MiB. The fast profile drops that
  hash and lands at 48.93 MiB (-2.6%), with trace identity dedup retained.
- Existing full-mode installs that switch to fast mode keep their old hash
  tables until a rebuild; the migration script does not convert
  `ReplacingMergeTree` back to `MergeTree`. Fast-to-full upgrades are
  automatic.
