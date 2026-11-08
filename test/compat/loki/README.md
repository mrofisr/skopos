# Loki differential harness

This harness replays the deterministic LogQL corpus in
`test/corpus/logql/corpus.json` against a real `grafana/loki:3.5.7` container
and compares the normalized results with the corpus expectations. The Go test
`internal/loki.TestLogQLCorpus` compares the Skopos Loki head with the same
expectations, so a corpus change is checked against both implementations.

The harness needs Docker with Compose v2 and Python 3.11+ (standard library
only). It does not link or vendor Loki code.

## Commands

```sh
# Start Loki, push the corpus, verify every expected result, then stop Loki.
test/compat/loki/run.sh verify

# Keep Loki running for manual queries on http://127.0.0.1:13100.
KEEP_LOKI=1 test/compat/loki/run.sh verify

# Refresh corpus expectations from the reference Loki (maintainers only).
test/compat/loki/run.sh record

# Start or stop the reference container without running the checks.
test/compat/loki/run.sh up
test/compat/loki/run.sh down
```

`record` rewrites `expected_logs` and `expected` in the corpus from Loki and
removes the reference-only `stats` and `detected_level` fields. Review the diff
before committing it.

## Optional gateway differential

The Go corpus test covers the gateway logic in-process. To compare a real
gateway with Loki over HTTP, seed a dedicated ClickHouse database with the
same corpus entries and point `GATEWAY_URL` at the gateway:

```sh
# Use a disposable database/table, not a production one.
CLICKHOUSE_DATABASE=skopos_loki_compat \
  python3 test/compat/loki/seed_clickhouse.py --truncate

GATEWAY_URL=http://127.0.0.1:8080 test/compat/loki/run.sh verify
```

The gateway must be configured with
`SKOPOS_CLICKHOUSE_DATABASE=skopos_loki_compat` and expose the normal Loki
routes. The harness drops `detected_level` and `stats`, sorts streams, series,
and samples, and compares the normalized payloads.

## Normalization

Loki and Skopos differ in presentation-only details that the harness removes:

- `detected_level` is synthesized by Loki from the body and is not stored in
  `otel_logs`; Skopos does not synthesize it.
- `__error__` and `__error_details__` are Loki parser diagnostics; Skopos keeps
  unparseable lines without adding diagnostic labels.
- `stats` is omitted from the comparison.
- Streams, series, and samples are sorted before comparison.

Range-query evaluation points follow Loki: `step` is truncated to whole
milliseconds, then points are aligned to Unix-epoch multiples of that step,
with the first point rounded down and the last rounded up. This matters for the
upstream benchmark's fractional steps.

## Upstream benchmark corpus

`test/corpus/logql/corpus.json` is a hand-curated contract. Loki does not ship
its benchmark queries as a static file either: `pkg/logql/bench` generates them
with a fixed seed. `upstream.sh` extracts the pinned source archive, runs that
generator, and writes `test/compat/loki/upstream/corpus.json`.

```sh
# Regenerate the corpus from the pinned source and diff it against the commit.
test/compat/loki/upstream.sh verify

# Rewrite the committed corpus after changing the pinned Loki version.
test/compat/loki/upstream.sh refresh

# Start Loki plus a seeded ClickHouse gateway, run every case, write the report.
test/compat/loki/upstream-run.sh --report docs/logql-upstream-baseline.md
```

The pinned tag, commit, archive SHA-256, and generator are recorded in
`upstream.sh` and in the committed corpus. The generator defines queries and
time ranges, not log data, so `upstream.py` seeds one synthetic stream per
selector with bodies that exercise the corpus filters. Every case is shifted by
one constant so the fixture ends five minutes before the run; query text,
relative windows, steps, and directions are unchanged.

The latest result is
[`docs/logql-upstream-baseline.md`](../../../docs/logql-upstream-baseline.md).

## Limitations

- The harness pins one Loki release (`3.5.7`) by digest. Update the image and
  run `record` together when adopting a new reference version.
- The corpus covers the supported subset only. Unsupported LogQL must return an
  explicit HTTP 400, which is covered by unit tests rather than this harness.
- The reference container uses filesystem storage and a one-year retention
  period; it is not a production configuration.
- The corpus uses fixed timestamps. The harness config keeps those chunks in
  the ingester (`max_chunk_age`/`chunk_idle_period: 8760h`) so the disposable
  container can query them. Refresh the corpus (`record`) or shift its
  timestamps if running against a date more than a year after the corpus date.
