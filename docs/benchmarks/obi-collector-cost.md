# OBI and Collector cost measurement

- Date: 2026-11-15
- Lab: k0s `oracle-cloud`, arm64, 4 vCPU
- Chart: `skopos-0.0.1-alpha` at `dev-ddd295e`
- OBI: `otel/ebpf-instrument:v1.0.0`
- Collector: `otel/opentelemetry-collector-contrib:0.164.0`
- ClickHouse: 26.8.36.3

## Question

The review left one open question: what do OBI and the Collector cost, and is
the default deployment actually producing telemetry?

## Setup

A temporary `obi-cost` namespace ran `nginx:1.27-alpine` and a 60-second load
of 20 concurrent curl requests through the ClusterIP Service. OBI’s default
chart security context was used first, then `SYS_ADMIN` was added to the
DaemonSet for a second run. The namespace and the manual capability were
removed afterwards; the lab is back on the chart defaults.

## Results

| State | OBI CPU / memory | Collector CPU / memory | Telemetry |
| --- | ---: | ---: | --- |
| Idle, default chart | 16m / 399 MiB | 3m / 52 MiB | `target.info` only |
| 60 s nginx load, default chart | 37–48m / 410 MiB | 23m / 111 MiB | `target.info` only |
| 60 s nginx load, `+SYS_ADMIN` | 44–49m / 531–535 MiB | 19–25m / 121–128 MiB | **94,980 nginx traces**; `target.info` 191; 176 istio metric series |

ClickHouse CPU rose from ~73m idle to 111–170m during the load. The default
chart logged `join target netns: operation not permitted` for the nginx
process; it discovered the process but could not instrument it. With
`SYS_ADMIN`, the netns join succeeded and the nginx traces appeared.

The nginx RED metrics did not appear as `http*` metric families; the trace
stream is the usable signal from this workload. The Collector was cheap
relative to OBI.

## Cost per trace

For ~95k nginx traces in 60 s:

- OBI + Collector: (49 + 25) millicores × 60 s = 4.44 CPU-seconds, or about
  **47 µs of CPU per trace**.
- ClickHouse ingestion: ~149m × 60 s = 8.94 CPU-seconds, or about **94 µs per
  trace**.
- Agent memory: **~535 MiB OBI + ~128 MiB Collector = ~663 MiB** for the
  workload.

The OBI memory request is **256 Mi**, below the measured 535 MiB; the 1 Gi
limit holds. The CPU request (100m) covers the measured 49m. The Collector
request/limit (100m/256Mi, 1/1Gi) covers its 25m/128MiB.

## Findings

1. **The default OBI security context is not portable.** On this k0s/
   containerd lab, the unprivileged capability set cannot join the target
   netns. OBI burns 16–48m CPU and ~400 MiB while producing only
   `target.info`. Upstream notes that unprivileged setups depend on the
   runtime and host policy.
2. **`SYS_ADMIN` makes it work.** With it, OBI instruments nginx and the
   Collector exports 95k traces in a minute. The cost is real but measurable:
   ~47 µs/trace at the agents plus ~94 µs/trace in ClickHouse, and ~660 MiB
   agent memory.
3. **Istio is instrumented too.** The lab’s `istio-system` namespace is not in
   the chart exclusion list; OBI produced istio metric series and traces
   alongside the app. That is unrequested cost and noise for a deployment that
   only wants application RED data.
4. **The Collector is not the problem.** Its 100m/256Mi request is adequate at
   25m/128Mi under this load. The OBI memory request is the one that is below
   observed usage.

## Recommendations

1. Decide the security posture: grant `SYS_ADMIN` by default (as tested) or run
   OBI privileged (the upstream Helm chart default), and update the
   `contextPropagation` comment, which currently implies `SYS_ADMIN` is only
   for context propagation. This is a security-sensitive chart change.
2. Raise `obi.resources.requests.memory` from **256Mi to 512Mi**; keep the
   1Gi limit. The measured peak was 535 MiB. No CPU request change is needed
   from this run.
3. Add an exclusion for `istio-system` (or document an `extraRules` example)
   unless mesh instrumentation is explicitly wanted.
4. Consider `OTEL_EBPF_ENFORCE_SYS_CAPS=1` so a missing capability fails fast
   instead of running with `target.info` only.
5. Keep the Collector resources unchanged from this measurement.

These are recommendations from one 60-second lab run, not a claim about every
workload; re-measure after the security-context change.
