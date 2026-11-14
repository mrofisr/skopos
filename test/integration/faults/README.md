# Fault-injection test

This harness proves the Collector's disk queue survives two failures:

1. The backend goes down while the Collector is accepting OTLP.
2. The Collector restarts while the backend is still down.

It starts a pinned Collector with a named queue volume and a controllable
OTLP/HTTP sink on the host, sends OTLP logs, stops the sink, restarts the
Collector, then starts the sink and waits for every queued log to arrive.

```sh
make test-faults
# or
SKOPOS_FAULTS_TEST=1 go test ./test/integration/faults -count=1 -v
```

Keep the stack after a failure for inspection:

```sh
SKOPOS_FAULTS_KEEP=1 make test-faults
```

The test needs Linux Docker with Compose v2 (it uses host networking so the
Collector can reach the test sink without bridge firewall rules). It is skipped
by a plain `go test ./...` unless `SKOPOS_FAULTS_TEST=1` is set.
