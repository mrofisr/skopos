# Loki corpus and upstream baseline

The Loki comparison is corpus-based. There is no reference container to start
and no Python harness.

The two checks are:

```sh
# Compare the Skopos Loki head with the committed expectations.
go test ./internal/loki -run TestLogQLCorpus -count=1

# Regenerate the upstream corpus from the pinned Loki source and diff it.
scripts/compat/loki-upstream.sh verify
```

## Corpus

`test/corpus/logql/corpus.json` holds query, time range, and expected result.
`internal/loki.TestLogQLCorpus` runs every case through the in-process Loki
head and compares normalized results. Loki's presentation-only
`detected_level` label and `stats` object are not part of the comparison;
`__error__` and `__error_details__` parser diagnostics are not synthesized.

The expectations were recorded from `grafana/loki:3.5.7` before the Python
harness was removed. They are a fixed contract: a Skopos change that alters a
result fails the Go test, and a corpus change must be reviewed as a contract
change. `docs/logql-upstream-baseline.md` records the full 83-case run from the
same release.

## Upstream benchmark corpus

`test/compat/loki/upstream/corpus.json` is generated from Loki's own benchmark
package. `pkg/logql/bench` builds the cases with a fixed seed, so the script
extracts the pinned source archive, runs that generator, and compares the
result.

```sh
# Regenerate the corpus from the pinned source and diff it against the commit.
scripts/compat/loki-upstream.sh verify

# Rewrite the committed corpus after changing the pinned Loki version.
scripts/compat/loki-upstream.sh refresh
```

The pinned tag, commit, archive SHA-256, and generator are recorded in
`scripts/compat/loki-upstream.sh` and in the committed corpus.

`test/compat/loki/upstream/seed.json` is the synthetic log fixture used by
`internal/loki/fixture_test.go` to exercise the same selectors. The upstream
generator defines queries and time ranges, not log data.

## Normalization

Range-query evaluation points follow Loki: `step` is truncated to whole
milliseconds, then points are aligned to Unix-epoch multiples of that step,
with the first point rounded down and the last rounded up. This matters for the
upstream benchmark's fractional steps.

## Limitations

- The corpus pins one Loki release (`3.5.7`). Update the expectations and the
  pinned archive together when adopting a new reference version.
- The corpus covers the supported subset only. Unsupported LogQL must return an
  explicit HTTP 400, which is covered by unit tests.
- The corpus uses fixed timestamps. Query fixtures remain valid because the
  engine evaluates the stored timestamps, not wall-clock data.
