# Instant-query decode and grouping (#25)

- Date: 2026-11-15
- Issue: [#25](https://github.com/mrofisr/skopos/issues/25)
- Dataset: `test/scale` 10k profile, 6 h range, 60 s step,
  hash `202641d15fe61544`
- Gateway: `test/bench` image from this branch, admission 16/32/2 s
- ClickHouse: 26.8.36.3, `test/bench` Compose service
- Host for these numbers: **aarch64, 4 vCPU** (not the Intel reference host
  used by [B-02](b02-10k.md); absolute latency is not directly comparable)

## What changed

The instant path used to fetch one JSON object per sample and decode every
cell with its own `json.Decoder`. It now:

1. decodes a JSON response in one `UseNumber` pass;
2. requests `FORMAT JSONCompact`, so rows are arrays and column names are
   not repeated;
3. groups short-window (≤15 min) gauge/sum samples by series in ClickHouse,
   returning one row with `Times` and `Values` arrays per series;
4. computes label keys once per series/sort instead of inside every sort
   comparator, and appends a grouped series' samples under one key.

Longer windows keep the per-sample query so the result-row limit still bounds
memory. The Prom fetchers use `QueryCompact`; the other heads keep `Query`.

## Microbenchmarks

`internal/chclient`, 50,000 samples (10k series × 5 points), same host:

| Decode path | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| Per-sample JSON, per-cell decoder | 670 ms | 231 MB | 3.10 M |
| Per-sample JSON, one-pass decoder | 263 ms | 85 MB | 1.30 M |
| Per-sample JSONCompact | 211 ms | 65 MB | 1.20 M |
| Grouped JSONCompact | **95 ms** | **28 MB** | **0.61 M** |

The grouped decode is 7.0× faster and uses 88% less memory than the original
per-cell decode. CI uses the decode-only benchmarks; the numbers above come
from `go test -bench`.

## Real instant query

Same ClickHouse, same 10k-series/5-minute window, `sum(rate(...))` and
`sum by (service_name) (rate(...))`:

| Path | Query time |
| --- | ---: |
| Per-sample on this branch (grouping disabled) | ~800 ms |
| Grouped + key-cached sorts | **~300 ms** |

The 2.7× reduction includes the grouped fetch, one key per series, and the
sort-key cache. The result values matched the per-sample path.

## Mixed 30-client workload

`test/bench` with 30 clients for 60 s, the same eight queries as B-02 (the 6 h
range is skipped), on this aarch64 4-vCPU host:

| Scope | Requests | Errors | p50 | p95 | Throughput |
| --- | ---: | ---: | ---: | ---: | ---: |
| overall | 1,436 | 35 (2.44%) | 604 ms | 4.24 s | 23.47 req/s |
| `prom_instant_count` | 165 | 6 | 3.47 s | 4.77 s | — |
| `prom_instant_by_service` | 169 | 11 | 3.67 s | 4.95 s | — |
| `prom_histogram_p95` | 161 | 6 | 912 ms | 2.06 s | — |
| `prom_series` | 162 | 6 | 1.27 s | 2.10 s | — |
| `prom_label_values` | 153 | 6 | 521 ms | 1.95 s | — |

All errors are admission `503`s. This host is slower and differently sized
from the Intel reference host, so these absolute numbers do not close the
acceptance gate. The instant p95 moved from the issue's 7.4 s reference
measurement to 4.8 s here, but the reference-host k6 run is still required.

## Lab deployment run

`dev-b52a32a` was deployed to the k0s lab (`oracle-cloud`, aarch64, 4 vCPU)
with the exact 10k dataset, 32 in-flight/64 queued admission, gateway CPU limit
raised from 500m to 3 cores, and ClickHouse at 2 cores. The tables were
optimized after generation. The mixed workload was the same eight queries for
60 s with the 6 h range skipped.

| Scope | Requests | Errors | p50 | p95 | Throughput |
| --- | ---: | ---: | ---: | ---: | ---: |
| overall | 1,234 | 0 | 706 ms | 4.96 s | 19.77 req/s |
| `prom_instant_count` | 146 | 0 | 4.37 s | 5.46 s | — |
| `prom_instant_by_service` | 144 | 0 | 4.27 s | 5.45 s | — |
| `prom_histogram_p95` | 129 | 0 | 603 ms | 890 ms | — |
| `prom_series` | 131 | 0 | 957 ms | 1.62 s | — |
| `prom_label_values` | 132 | 0 | 311 ms | 636 ms | — |

The lab also runs OBI, the Collector, and the reliable `ReplacingMergeTree`
schema, so this is not the fast-profile Compose reference. A single instant
query measured 0.6 s through the gateway versus 0.15 s direct to ClickHouse;
30 concurrent queries on 4 vCPU therefore sit at roughly 4–5 s. Raising the
gateway from 500m to 3 cores moved overall throughput from 5.9 to 19.8 req/s
and instant p95 from 17.3 s to 5.45 s, but did not close the gate.

## Acceptance status

- [x] Red/edge tests: the instant result matches the per-sample path; grouped
  and long-window queries are covered; the JSONCompact shapes were checked
  against ClickHouse 26.8.36.3.
- [ ] **Reference gate:** instant p95 < 2 s and throughput ≥ 48 req/s at
  10k/30 clients. The lab run above measured 5.45 s and 19.77 req/s, so the
  gate is still open. The fast-profile Compose/reference run also remains.
- [ ] If the reference run still misses 2 s, the next lever is SQL `rate`/`sum`
  pushdown (needs subquery/window support in `sqlgen`) or a deliberate
  one-second instant-result cache (a staleness tradeoff).

## Commands

```sh
scripts/e2e/obi/render-schema.sh
cd test/bench
SKOPOS_BENCH_PROFILE=true \
  docker compose -p skopos-bench -f docker-compose.yml up -d --build

go run ./test/scale/cmd/scale-gen \
  --profile 10k --range 6h --step 60s \
  --url http://127.0.0.1:18124 --database otel

go run . --base-url http://127.0.0.1:28082 \
  --concurrency 30 --duration 60 --skip prom_range_6h \
  --json-out bench-instant.json
```
