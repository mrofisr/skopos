# Why the 6 h range query fails under load

AX-39 spike, 10 November 2026. This is a cut-line ticket: it records the cause
and the reproduction, and merges no query behaviour change.

## Verdict

The 6 h range query does not fail because ClickHouse scans too much or because
the Go evaluator walks 361 steps. It fails because **every request decodes the
whole ClickHouse response through a generic map, and every client repeats that
work**:

- `chclient.Query` unmarshals `data` as `[]map[string]json.RawMessage`, then
  runs a fresh `json.Decoder` over every field. The decoder is **66 % of all
  CPU samples**; GC allocation from it is another **22 %**.
- Each of the 30 clients fetches the same range independently. The live heap
  during the run is **306 MiB**, above the chart's **256 MiB** limit.

At 30 clients the gateway averages 2.2 cores and peaks at 3.6; the range
query's p50 goes from **545 ms** alone to **6.35 s**. Every query degrades about
10x, which is the shape of a saturated decoder, not a slow backend.

## Reproduce

```sh
make profile-range
```

The target starts the bench stack with `SKOPOS_PROFILE=1`, runs the sequential
harness, then drives eight concurrent 6 h range queries for 90 s while it
captures a 30 s CPU profile, a heap profile, and `/metrics`. Artifacts land in
the ignored `test/bench/out/` directory:

```text
test/bench/out/range-query-cpu.pprof
test/bench/out/range-query-heap.pprof
test/bench/out/range-query-metrics.prom
```

Inspect them with:

```sh
go tool pprof -http=:8081 test/bench/out/range-query-cpu.pprof
go tool pprof -top -nodecount=20 test/bench/out/range-query-cpu.pprof
go tool pprof -top -inuse_space test/bench/out/range-query-heap.pprof
```

The query is the workload's own `prom_range_6h`:

```text
sum(rate(http_requests_total[5m]))   start=now-6h   end=now   step=60s
```

361 points; the seeded dataset holds 24 h of data, so the range covers a
quarter of it.

## What the harness measures

`./scripts/bench/run.sh` on the seeded stack, one query at a time (30 iterations):

| Query | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: |
| `prom_instant_count` | 39.8 | 43.4 | 47.0 |
| `prom_range_6h` | 544.8 | 563.6 | 579.0 |
| `tempo_search` | 47.8 | 55.0 | 72.4 |

The same queries with 30 clients for 60 s:

| Query | Requests | Errors | p50 ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| `prom_instant_count` | 142 | 0 | 408 | 813 | 935 |
| `prom_range_6h` | 151 | 0 | 6349 | 7791 | 8056 |
| overall | 1402 | 0 | 604 | 6378 | 7447 |

| Container | CPU avg | CPU peak | Memory avg | Memory peak |
| --- | ---: | ---: | ---: | ---: |
| gateway | 2.22 cores | 3.60 cores | 1.7 GiB | 2.3 GiB |
| ClickHouse | 1.35 cores | 2.68 cores | 813 MiB | 1004 MiB |

The 6 h query is 13.7x the instant query alone and 15.5x under load. ClickHouse
stays at 2.7 cores and flat memory, so the backend is not the limit.

## Profile evidence

Eight concurrent 6 h queries, 30 s CPU profile, 88.06 s of samples:

| Function | Flat | Cumulative |
| --- | ---: | ---: |
| `chclient.(*Client).Query` | 1.3 % | **66.2 %** |
| `chclient.decodeValue` | 0.03 % | **46.6 %** |
| `encoding/json.(*Decoder).Decode` | 0.15 % | **32.5 %** |
| `runtime.mallocgc` | 1.2 % | **21.9 %** |
| `runtime.gcBgMarkWorker` | 0 % | **10.0 %** |
| `prom.(*ClickHouseFetcher).addGaugeRow` | 0.04 % | **17.1 %** |
| `prom.NormalizeLabelName` | 0.9 % | 4.6 % |
| `prom.(*evalCtx).evalMatrixSelector` | 0.03 % | 84.1 % |
| — of which `Fetch` | 0 % | 83.3 % |

`evalMatrixSelector` looks dominant only because the one fetch happens inside
it. Subtracting the fetch, per-step evaluation is under 5 % of the samples: the
same 361 steps evaluated against cached series cost about 1 s of the 88 s.

Heap in use at the same moment (`-inuse_space`, 305.8 MiB total):

| Site | In use |
| --- | ---: |
| `reflect.mapassign_faststr0` | 118.0 MiB |
| `chclient.(*Client).Query` | 41.5 MiB |
| `encoding/json/jsontext.(*Value).UnmarshalJSON` | 38.0 MiB |
| `encoding/json/jsontext.NewDecoder` | 31.0 MiB |
| `reflect.makemap` | 18.5 MiB |

The `map[string]json.RawMessage` row shape and the per-field decoder account
for almost all of it. The chart's default gateway limit is 256 MiB; the live
heap already exceeds it without counting stacks, goroutine buffers, or the
response bytes held during encoding. With `GOMEMLIMIT` at 90 % of the limit the
runtime spends its time in GC instead of decoding; without it the container is
OOM-killed.

## Ranked causes

### 1. Generic JSON decode of every row (highest)

`Client.Query` reads the whole response, unmarshals `data` into
`[]map[string]json.RawMessage`, then decodes each field with a new
`json.Decoder` and `UseNumber`. A row with eight columns allocates one map, one
`RawMessage` per column, one `any` per value, and a decoder per field. At
14,400 rows per range query, repeated by every client, that is the 66 % CPU and
the 306 MiB heap.

ClickHouse can return `JSONCompact` (rows as arrays) or `RowBinary`; either
removes the map-per-row and the per-field decoder. The `Result` accessors
(`String`, `Float64`, `Time`) are the only consumers, so the change is behind
the client.

Follow-up: **AX-62**, below.

### 2. Identical ranges are fetched once per client (high)

Each `QueryRange` creates a private cache, fetches each selector for the whole
`[start-lookback, end]` window, and retains every decoded row until the
response is written. Eight identical ranges in the profile did eight identical
fetches and decodes; 30 clients do 30. Nothing coalesces in-flight work, and
nothing bounds the decoded rows before the 200,000-sample cap is checked
(after decode).

Follow-up: **AX-63**, below.

### 3. Admission counts requests, not points (medium)

Admission (`SKOPOS_MAX_INFLIGHT`, `SKOPOS_QUEUE_DEPTH`, `SKOPOS_QUEUE_WAIT`)
gives a 361-point range query the same slot as an instant query even though it
decodes 72x more samples (720 per series instead of 10 for a five-minute
instant query). The chart default disables admission
(`maxInflight: 0`), so nothing bounds the number of expensive ranges; when it
is enabled, a few ranges occupy every slot and instant queries queue behind
them. The only per-request guard is `SKOPOS_QUERY_TIMEOUT` (30 s), which turns
saturation into 503s instead of shedding cheap work first.

Follow-up: **AX-64**, below.

### 4. Range and scan limits surface as HTTP 500 (low, but user-visible)

The sample cap (`prom: selector returned N samples`), the ClickHouse row cap
(`max_rows_to_read`), and the Tempo row cap all fall through
`writeEngineError`'s default branch and return `500 execution`. Prometheus
returns `422` for query limits. Clients treat 500s as server failures and
retry, adding load exactly when the gateway is saturated. The same class of bug
made every full Tempo page a 500 until #130: a search that matched exactly
`limit` traces reported "reached the row limit".

Follow-up: **AX-65**, below.

## Non-causes

- **ClickHouse scan volume.** The 6 h range reads about 14,400 gauge rows and
  ClickHouse spends 1.35 cores across the whole mixed workload.
- **Per-step Go evaluation.** Under 5 % of the samples once the fetch is
  excluded; the per-step label clones are real but not what saturates the
  gateway today.
- **Response size.** The 6 h result is 12 KB (361 points).
- **The Tempo tag cache.** `tempo_tags` is 36 ms p50 under load, the cheapest
  query in the mix.

## Follow-up micro-tickets

These are proposals from the spike. They are not in the alpha plan yet; the
next planning pass decides which ones serve an exit criterion.

### AX-62 Typed ClickHouse row decoding

- Blocked by: AX-39.
- Decode hot query responses without a map per row: request `JSONCompact`
  (or `RowBinary`) and index columns by position, or generate a typed row
  decoder per query. Keep the `Result` accessors unchanged.
- Red tests: a 100,000-row fixture decodes with a fixed allocation budget per
  row (measured with `testing.AllocsPerRun`); `String`, `Float64`, `Time`, and
  missing-column behavior are byte-identical to today; a malformed row fails
  with a typed error.
- Edge and negative: nulls, `UInt64` above 2^53, `DateTime64`, arrays, and
  duplicate column names.
- Done when: the bench `prom_range_6h` CPU profile no longer shows
  `reflect.mapassign` or a per-field `json.Decoder`, and the 30-client p50
  drops materially.

### AX-63 Coalesce identical range fetches

- Blocked by: AX-39.
- Share one in-flight selector fetch between identical concurrent requests
  (single-flight) and enforce the sample budget from the ClickHouse summary
  before decoding the body.
- Red tests: two concurrent identical ranges issue one ClickHouse query; a
  range over the sample budget returns 422 without decoding; different
  selectors, offsets, or windows do not coalesce.
- Edge and negative: a cancelled leader does not cancel a follower; the shared
  result is immutable; the cache key is the emitted SQL plus parameters.
- Done when: the 30-client mixed workload keeps the gateway under its chart
  memory limit with `maxInflight` disabled.

### AX-64 Cost-aware admission

- Blocked by: AX-39.
- Weight admitted work by estimated points (`(end-start)/step`) instead of one
  slot per request, so cheap instant queries are not blocked behind ranges.
- Red tests: a 6 h range consumes more budget than an instant query; a range
  over the budget is rejected with 429 and `Retry-After`; instant queries still
  pass while ranges queue.
- Edge and negative: unknown step uses a conservative default; a zero
  `maxInflight` disables the budget as it disables admission today.
- Done when: under 30 clients the instant-query p95 stays within its
  single-client envelope.

### AX-65 Typed limit errors

- Blocked by: AX-39.
- Return `422 bad_data` for sample, point, and row limits in the Prometheus,
  Loki, and Tempo heads instead of `500 execution`.
- Red tests: one fixture per limit asserts 422 and a stable error type/message;
  a ClickHouse transport error still returns 500.
- Edge and negative: an upstream 5xx is not reclassified; the Prometheus error
  envelope stays valid.
- Done when: no query-limit failure is recorded as a 5xx in the metrics.

## What this ticket merged

- `SKOPOS_PROFILE` (default off) exposes the auth-protected `/debug/pprof/*`
  routes; with the flag off the routes are absent.
- Helm passes the flag to the gateway, frontend, and query deployments.
- `test/bench/docker-compose.yml` forwards `SKOPOS_BENCH_PROFILE` to the
  gateway, and `make profile-range` captures the profiles and metrics above.
