# k6 load test

A manual k6 load test for the Skopos query gateway. It exercises the
Prometheus, Tempo, and Loki-compatible HTTP paths against live telemetry in
ClickHouse.

This is a throughput/latency tool, not a CI correctness gate. The reproducible
benchmark and concurrency gate remain in [`test/bench`](../../bench).

## Prerequisites

- k6 on the machine where you run the test (workstation or laptop)
- `kubectl` if you need a port-forward
- Skopos installed and the gateway ready

k6 does **not** need to run on the Skopos server.

## Reach the gateway

The gateway is a ClusterIP Service. If you run k6 from your workstation, start
a port-forward on the server:

```sh
kubectl -n observability port-forward svc/skopos-gateway 18081:8080
```

Then tunnel it from your workstation:

```sh
ssh -L 18081:127.0.0.1:18081 ubuntu@10.0.0.81
```

Keep both sessions open. If you already expose the gateway through an ingress
or internal URL, use that as `BASE_URL` instead.

## Smoke test

```sh
SMOKE=true BASE_URL=http://127.0.0.1:18081 \
  k6 run test/load/k6/skopos-query.js
```

The smoke profile runs one VU for three iterations and fails if the gateway is
not ready.

## Stress run

Start small, then increase:

```sh
BASE_URL=http://127.0.0.1:18081 \
VUS=10 \
RAMP_UP=30s \
HOLD=2m \
k6 run test/load/k6/skopos-query.js
```

Set `SLEEP=0` to remove per-iteration pacing. The ramping-VU scenario uses:

```text
1 VU -> VUS over RAMP_UP
VUS for HOLD
VUS -> 0 over RAMP_DOWN
```

## Design point

The performance design point is 50 VUs with `SLEEP=0.5`, a 30s ramp, and a 3m
hold. The 25-VU run is the no-regression guard: every per-query threshold must
pass there before a change lands. 100 VUs is a capacity probe, not the design
point.

## Per-query thresholds

The script gates every query separately instead of relying on one aggregate
p95. The budgets are a ratchet: start at the measured 50-VU baseline and lower
a budget only in the PR that improves that query.

| Query | p95 budget |
| --- | ---: |
| `prom_vector` | 1500 ms |
| `prom_rate` | 1500 ms |
| `prom_by_service` | 1500 ms |
| `prom_p95` | 1500 ms |
| `prom_range` | 1900 ms |
| `prom_series` | 4450 ms |
| `prom_label_values` | 2100 ms |
| `tempo_search` | 1500 ms |
| `loki_labels` | 1500 ms |

The three budgets above the aggregate target are regression ceilings seeded
from the 50-VU baseline. The rest stay at the aggregate target, so a query that
drifts above 1500 ms stays visible. Lower a ceiling only with a before/after
run that shows the improvement. The aggregate `P95_MS` remains the user-facing
gate.

`ZERO_5XX=true` (the default) fails the run on any 5xx response. Set
`ZERO_5XX=false` for an above-design-point capacity probe where deliberate
503/504 shedding is part of the result being measured. The smoke profile keeps
only the aggregate gates, so a cold first request cannot fail a readiness
check; the per-query budgets apply to stress runs.

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `BASE_URL` | `http://127.0.0.1:8080` | Gateway base URL |
| `AUTH_TOKEN` | empty | Optional bearer token |
| `SMOKE` | `false` | `true` = 1 VU, 3 iterations |
| `ITERATIONS` | `3` | Smoke iterations |
| `VUS` | `5` | Peak virtual users |
| `RAMP_UP` | `30s` | Ramp duration |
| `HOLD` | `1m` | Hold duration |
| `RAMP_DOWN` | `15s` | Ramp-down duration |
| `SLEEP` | `0.5` | Seconds between iterations |
| `P95_MS` | `1500` | p95 latency threshold in milliseconds |
| `ZERO_5XX` | `true` | Fail the run on any 5xx response |

## Query mix

Each iteration picks the next query based on `__VU + __ITER`:

| Name | Path | Query |
| --- | --- | --- |
| `prom_vector` | `/api/v1/query` | `vector(42)` baseline |
| `prom_rate` | `/api/v1/query` | `sum(rate(http_server_request_duration_count[5m]))` |
| `prom_by_service` | `/api/v1/query` | rate grouped by `service_name` |
| `prom_p95` | `/api/v1/query` | histogram p95 over request duration |
| `prom_range` | `/api/v1/query_range` | 5-minute range, 30s step |
| `prom_series` | `/api/v1/series` | series match for whoami |
| `prom_label_values` | `/api/v1/label/__name__/values` | metric name values |
| `tempo_search` | `/api/search` | TraceQL `{ service.name = "whoami" }` |
| `loki_labels` | `/loki/api/v1/labels` | Loki label list (currently empty) |

The metric names omit the `_seconds` suffix; the gateway exposes
`http_server_request_duration_*` and `http_client_request_duration_*`.

## What to watch

- `http_req_duration{name:...}` in the k6 summary: per-query latency.
- `skopos_5xx`: 5xx responses by status. Must be zero at the 50-VU design point.
- `http_req_failed`: transport errors and non-2xx responses.
- `checks`: HTTP 200 and valid gateway response shape.
- The **Skopos Overview** dashboard: `skopos_http_requests_total`,
  `skopos_http_in_flight`, `skopos_admission_rejected_total`, ClickHouse latency
  and read throughput.
- A `429` or `503` means admission control shed the request; inspect the
  `Retry-After` header and the dashboard counters. That is a valid stress
  result, not necessarily a bug.

Start with the smoke profile, then increase `VUS` and lower `SLEEP`. The load is
real ClickHouse work; watch node CPU, memory, and ClickHouse latency. Do not run
a high-load profile against a shared cluster without coordination.
