# Quality gates

Skopos uses generated, checked-in evidence for the alpha-exit quality gates.
This page covers the coverage ratchet (STG-5 / AX-01). Mutation testing
(STG-6 / AX-02) is advisory for alpha and documented with its harness.

## Coverage ratchet

Package coverage must stay at or above the floor recorded in
[`test/quality/coverage-floor.json`](../test/quality/coverage-floor.json).
Floors are measured baselines rounded down to one decimal place. They only
move up: lowering a floor fails the check even when the tests still pass.

The 80% target is a beta goal. Alpha only requires that coverage does not
regress from the baseline.

### Commands

| Command | What it does |
| --- | --- |
| `make coverage` | Runs the tests, writes `coverage.out`, and fails when a package is below its floor, is missing from the profile, or has a lowered floor |
| `make coverage-report` | Prints per-package coverage next to the floor |
| `make coverage-write` | Regenerates `test/quality/coverage-floor.json` from measured coverage |

`make coverage` compares the checked-in floor with the floor at
`origin/development`. If that ref or file is unavailable, it prints a warning
and skips only the lowering check, so a fresh checkout without the remote
branch still runs the rest of the gate.

The gate covers product packages (`internal/...` and `cmd/...`). Test-only
packages such as `test/compat/...` are not part of the floor.

### Raising a floor

Run `make coverage-write` after adding tests, inspect the diff, and commit the
floor with the tests in the same PR. Do not hand-edit a floor downward; fix the
regression or add the missing tests instead.

A new package has no floor until the file is regenerated. The gate fails when
a measured package is missing from the floor file, and the failure names the
package, so the fix is always to commit its measured floor.

### CI

The `Coverage ratchet` job runs `make coverage` on every pull request and on
`development`. It uses a full checkout so the previous floor can be read from
`origin/development`.

## Red-first evidence

Every behavior PR starts with a tests-only commit that fails for the stated
reason, and the PR links the failing run. The check is defined in
`docs/alpha-exit-plan.md` under STG-1. Bootstrap and tooling PRs may carry the
`red-first-exempt` label.
