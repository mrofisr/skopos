# Benchmark parity fixtures

`baseline-sequential.json`, `baseline-concurrent.json`, `baseline-errors.json`,
and `baseline-dropped.json` were recorded from the retired Python harness before
the Go port became the runner (RS-20). They are the acceptance evidence for the
port: `bench_test.go` runs the Go harness against a stub that serves the same
bytes and compares every deterministic field.

Latency, elapsed time, throughput, and resource samples are excluded from the
comparison because they depend on the host. Request counts, error counts, error
classes, response sizes, query order, and the report shape are compared.

The Go test is the reproducible check. The repository has no Python toolchain
and no Python source.
