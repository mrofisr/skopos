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
