# Bench baselines

These files back `repoctl bench-gate`, the relative regression gate in
[ADR 0009](../../../docs/adr/0009-latency-budgets-and-bench-gate.md). A
baseline is one `test/bench` JSON report plus the metadata the gate checks:

| Field | Meaning |
| --- | --- |
| `profile` | `10k` or `100k` |
| `dataset_hash` | the `test/scale` dataset digest |
| `image` | the gateway image tag that produced the run |
| `host` | the host class; `oracle-cloud` is the 4 vCPU / 24 GiB k0s lab |
| `captured_at` | the run date |
| `bench` | the unmodified `--json-out` report |

The `100k` baselines are the B-03 run from
[b03-100k.md](../../../docs/benchmarks/b03-100k.md). The `10k` concurrent
baseline is the post-fix gate run (`dev-ddd295e`, 69.50 req/s, 954 ms overall
p95) from [instant-decode](../../../docs/benchmarks/instant-decode.md). The
`10k` sequential baseline waits for a 10k dataset run; the B-02 sequential
report predates the #25 instant cache and is not comparable.

Run the gate with three current reports (the median per-query p95 is used):

```sh
go run ./tools/repoctl bench-gate \
  --baseline test/quality/bench/100k-concurrent.json \
  --current /tmp/run-1.json \
  --current /tmp/run-2.json \
  --current /tmp/run-3.json \
  --profile 100k \
  --dataset-hash b900affe5ce4b9ec \
  --image dev-1a60b00 \
  --host oracle-cloud
```

The run fails when a query p95 is more than 20% above the baseline, p50 is
more than 15% above, throughput is more than 15% below, or an error appears.
The 100k gate is a manual lab command until a self-hosted `skopos-bench`
runner exists; see ADR 0009. Baselines move only through a reviewed pull
request with a benchmark report and a measured improvement.
