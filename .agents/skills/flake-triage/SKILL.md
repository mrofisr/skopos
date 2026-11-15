---
name: flake-triage
description: >-
  Diagnose and fix an intermittent Skopos test failure without hiding it. Use
  when a test flakes, CI fails intermittently, a timeout is suspected, or a
  quarantine is proposed.
license: Apache-2.0
---

# Flake triage

Evidence before edits. Never change a test whose failure you have not read, and
never skip or quarantine a test to make a run green.

## When to use

- A unit, integration, e2e, benchmark, or chart test fails intermittently.
- A CI job times out or fails only on one runner.
- A test is proposed for quarantine, an allow-list, or a longer timeout.

## Inputs

- The failing test name and package.
- The CI log, JUnit output, or local failure output.
- The commit and environment where it failed.
- Whether it reproduces locally, and at what rate.

## Reproduce first

1. Read the failure. Capture the exact error, stack, and the first failing
   assertion. Do not start from a guess.
2. Find the test:
   `git grep -n "func <TestName>(" -- '*_test.go'`.
3. Stress the narrowest target:

```sh
# Unit test, 50 runs with the race detector
go test ./<package>/... -run '^<TestName>$' -race -count=50

# Integration test; requires CLICKHOUSE_URL and the integration build tag
CLICKHOUSE_URL=http://127.0.0.1:8123 \
  go test -tags=integration ./internal/chclient -run '^<TestName>$' -count=10

# Fault or storage harness
make test-faults
make storage-test
```

4. If it does not reproduce, say so and analyze the CI output instead. Do not
   "fix" a test you cannot observe failing unless the failure output identifies a
   concrete cause.

## Common causes

- Wall-clock assumptions: fixed `time.Sleep`, a timestamp near a bucket edge, or
  a timeout tuned for a fast machine. Replace sleeps with polling and a deadline.
- Shared state: a fixed port, a fixed database or table, a fixed temp path, or a
  package-level fixture reused across parallel tests.
- Ordering: assuming map or slice order, or that a previous subtest ran.
- Leaked goroutines or HTTP bodies. Run with `-race` and close bodies.
- ClickHouse timing: a mutation, TTL, or `FINAL` query has not converged yet.
  Poll with a deadline and assert the eventual result.
- Docker startup: the harness assumes a container is ready before its health
  check passes.
- Parallel tests: a test mutates a shared environment variable or working
  directory.

## Fix and verify

1. Make the smallest fix. Prefer deterministic polling, explicit cleanup, or a
   per-test namespace/database over a larger timeout.
2. Re-run the stress loop from "Reproduce first". It must pass repeatedly:
   unit `-count=50`, integration `-count=10`.
3. Run the package tests, then `make test` and `make test-race`. If the change
   touches the chart, also run `make helm-lint` and `make helm-template`.
4. If the test is genuinely flaky and cannot be fixed in this change, do not
   skip it silently. Open or link an issue, add a documented quarantine with a
   named owner and expiry, and get human approval. `CONTRIBUTING.md` forbids
   unreviewed skips.

## Deliver

The pull request body must include:

- the failure output or a link to it;
- the stress command and the number of clean runs;
- the root cause in one or two sentences;
- the fix and why it removes the cause rather than the symptom;
- any follow-up issue for a product bug that the flake exposed.

## Guardrails

- No `t.Skip`, allow-list, or timeout increase without a documented reason and
  human approval.
- No `-count=1` "proof" that a flake is fixed.
- No retry loop inside the test to hide a race.
- If the cause is a product bug, say so and fix the product bug instead of
  loosening the test.

## References

- `CONTRIBUTING.md` working rules 1-3
- `test/compat/README.md`
- `test/integration/faults/README.md`
- `Makefile` targets `test`, `test-race`, `test-integration`, `test-faults`
