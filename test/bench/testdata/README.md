# Benchmark parity fixtures

`python-sequential.json`, `python-concurrent.json`, `python-errors.json`, and
`python-dropped.json` were recorded from the Python harness (`bench.py`) before
the Go port became the runner (RS-20). They are the acceptance evidence for the
port: `bench_test.go` runs the Go harness against a stub that serves the same
bytes and compares every deterministic field.

Latency, elapsed time, throughput, and resource samples are excluded from the
comparison because they depend on the host. Request counts, error counts, error
classes, response sizes, query order, and the report shape are compared.

## How the fixtures were recorded

1. Start a stub server that serves the bodies in `stubBodies` in
   `bench_test.go`, plus two failure modes: a 500 on `/api/v1/series` and a 429
   on `/loki/api/v1/query_range`; and a dropped connection on
   `/api/v1/series`.
2. Run the Python harness with a fixed clock-independent query set:

```sh
# Healthy, sequential.
python3 test/bench/bench.py --base-url http://127.0.0.1:PORT \
  --iterations 2 --warmup 1 --json-out testdata/python-sequential.json

# Healthy, concurrent with a fixed number of passes.
python3 test/bench/bench.py --base-url http://127.0.0.1:PORT \
  --concurrency 2 --iterations 2 --warmup 1 \
  --json-out testdata/python-concurrent.json

# Deliberate 500 and 429 responses.
python3 test/bench/bench.py --base-url http://127.0.0.1:PORT \
  --concurrency 2 --iterations 1 --warmup 0 \
  --json-out testdata/python-errors.json

# A dropped connection on /api/v1/series.
python3 test/bench/bench.py --base-url http://127.0.0.1:PORT \
  --concurrency 2 --iterations 1 --warmup 0 \
  --json-out testdata/python-dropped.json
```

The Go test is the reproducible check. It does not invoke Python, so CI does not
need a Python toolchain. `bench.py` stays in the tree until RS-23 deletes the
frozen harnesses.
