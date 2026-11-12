# Compatibility harnesses

Black-box and differential checks for M3–M5. Every harness is Go; the
repository has no Python source or Python toolchain.

- `prometheus/` — a separate Go module that runs the official Prometheus
  `promql` engine against the Skopos engine over 212 instant/range cases.
- `tempo/` — a Go differential test that compares the TraceQL corpus with
  `grafana/tempo:2.7.2`; run `scripts/compat/tempo.sh`.
- `loki/` — the recorded LogQL corpus and the pinned upstream corpus. The
  Skopos side is `internal/loki.TestLogQLCorpus`; the upstream copy is checked
  by `scripts/compat/loki-upstream.sh verify`.
- `cerberus/` — the deterministic three-signal seed generator
  (`cerberus/seed`) used to populate ClickHouse and the Prometheus OpenMetrics
  fixture. Its Go tests cover the corpus and chart-schema rendering.
- `scripts/e2e/obi/` — a privileged Docker-level end-to-end test of real OBI
  eBPF instrumentation through Collector and ClickHouse to the gateway.

The harnesses run reference implementations as binaries or containers. They do
not link, vendor, or copy code from cerberus, oteldb, Loki, or Tempo. Tempo and
Loki are AGPL-3.0; they are only executed as reference containers.

## What was retired

The earlier Python harnesses (`harness.py`, `seed_clickhouse.py`, `upstream.py`,
and `bench.py`) were removed. Their recorded results are kept as fixtures and
documentation:

- `docs/logql-upstream-baseline.md` — the 83-case LogQL result recorded against
  Loki 3.5.7.
- `test/bench/testdata/baseline-*.json` — the benchmark parity fixtures.

Two gates narrowed when the harnesses were removed:

- The Cerberus three-way comparison is no longer run end to end. The corpus,
  the Prometheus fixture, and the seed generator remain, and the Skopos side is
  covered by unit and corpus tests.
- The Loki differential no longer starts a reference container. The corpus
  expectations are committed, `internal/loki.TestLogQLCorpus` checks Skopos
  against them, and the upstream generator is still pinned and verified by
  `scripts/compat/loki-upstream.sh`.

The full Prometheus compliance tester and the complete Loki benchmark corpus
remain beta gates. The checks here are the repository's current subset.
