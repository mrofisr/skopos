---
name: corpus-case
description: >-
  Add or change a PromQL, LogQL, or TraceQL corpus case and prove it against the
  reference implementation. Use when adding compatibility coverage, fixing a
  divergence, or refreshing a corpus after a reference version bump.
license: Apache-2.0
---

# Corpus case

Add a deterministic compatibility case whose expected result comes from the
reference implementation, not from the Skopos gateway.

## When to use

- A query feature is claimed in `docs/promql-support.md`,
  `docs/traceql-support.md`, or `docs/logql-support.md` but has no case.
- A compatibility divergence needs a regression case.
- A reference version bump requires a reviewed corpus refresh. Do this in a
  separate pull request from a behavior change.

Do not use this skill to make a failing case pass by editing the expectation to
match Skopos. That is the failure mode the corpora exist to prevent.

## Inputs

- The language: PromQL, LogQL, or TraceQL.
- The query and the data it runs against.
- The reference implementation and version.
- Whether Skopos intends to support the behavior, or must return an explicit
  error.

## Choose the corpus

| Language | Corpus | Focused test |
| --- | --- | --- |
| PromQL | `test/compat/prometheus/harness_test.go` | `cd test/compat/prometheus && go test -count=1 ./...` |
| LogQL | `test/corpus/logql/corpus.json` | `go test ./internal/loki -run '^TestLogQLCorpus$' -count=1` |
| TraceQL | `test/corpus/traceql/corpus.json` and `corpus.go` | `go test ./internal/tempo -run '^TestCorpus' -count=1` |

The recorded reference versions are in `test/corpus/README.md`:
Tempo 2.7.2, Loki 3.5.7, and the official Prometheus `promql` engine. If you
bump a reference version, record the new image or module version in the same
pull request.

## Workflow

1. Read the relevant support page and the corpus README. Confirm the behavior is
   claimed, or is intended to be an explicit unsupported error.
2. Add the smallest input data that exercises the behavior. Prefer extending an
   existing trace or log fixture over adding a new fixture.
3. Run the query against the reference implementation and capture its exact
   result. For LogQL, add a case under `cases` with `name`, `kind`, `query`,
   `start`, `end`, and `expected_logs`. For TraceQL, add to `queries`,
   `tagNameTests`, `tagValueTests`, or `traceTests`. For PromQL, add to the
   table in `harness_test.go`.
4. For behavior Skopos does not support, assert the explicit error. TraceQL has
   `expectError`, `expectErrorContains`, and `note`; for LogQL and PromQL, follow
   the rejection assertion already used in the harness or add one in the same
   style. Do not delete an unsupported case to make the suite green.
5. Run the focused test. If it fails, inspect whether the implementation or the
   expectation is wrong. Do not update the expectation from the implementation
   output.
6. Run `make compat-all` before opening the pull request. It starts reference
   containers and takes several minutes. Record the exact command and result in
   the pull request.
7. If the claimed surface changed, update the support pages with the
   [support-matrix](../support-matrix/SKILL.md) skill.

## Verification

```sh
# PromQL
(cd test/compat/prometheus && go test -count=1 ./...)

# LogQL
go test ./internal/loki -run '^TestLogQLCorpus$' -count=1

# TraceQL
go test ./internal/tempo -run '^TestCorpus' -count=1

# Full compatibility gate
make compat-all

# If product code changed
make check
```

A pass means the focused test is green and `make compat-all` is green. If a
reference container cannot start, report the failure and do not claim the gate
passed.

## Guardrails

- Expected values come from the reference, not from Skopos.
- Include exact values, not "a result exists". Cover boundaries, empty results,
  invalid input, and counter resets where relevant.
- Do not add a `skip` or an allow-list entry without a documented reason and
  human approval. `CONTRIBUTING.md` forbids silent skips.
- Do not change a corpus and a product behavior in the same pull request unless
  the support claim cannot be separated. Explain the coupling in the body.
- Review the corpus diff before committing. A large unexplained refresh is a red
  flag.

## References

- `test/corpus/README.md`
- `test/compat/prometheus/README.md`
- `docs/promql-support.md`, `docs/traceql-support.md`, `docs/logql-support.md`
- `CONTRIBUTING.md` working rules 1-4
