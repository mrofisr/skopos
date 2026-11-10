# ADR 0004: Repository layout and language policy

- Status: Accepted for the alpha line
- Date: 2026-11-10
- Supersedes: the "Existing Python under `test/` is frozen" paragraph in
  `CONTRIBUTING.md` (commit 93da71b)
- Related: [ADR 0001](0001-observability-stack.md), [CONTRIBUTING.md](../../CONTRIBUTING.md),
  [AGENTS.md](../../AGENTS.md), [structure.md](../structure.md)

## Context

The alpha-exit work added one repository-check binary per ticket. By
2026-11-10 there were nine `cmd/skopos-*` tooling binaries beside the three
shipped ones: code scanning, coverage, issues, OSV exceptions, PR guard,
Grafana health, quickstart, release verification, and ruleset consistency. Each
had its own flag parsing, output convention, and GitHub access. Three
implementations reached GitHub differently: a `gh` wrapper, inline `gh api`
calls, and raw `net/http`.

The alpha plan's rule 3 says work that serves no exit criterion waits. This
policy is the exception: without one layout and one tool contract, AX-52,
AX-53, AX-54, AX-60, and AX-61 each add another binary or another shell script
with decision logic. The repository-check tooling is itself part of EC6 (strict
gates are live), so keeping it maintainable serves the exit.

## Decision

### 1. Binaries live in `cmd/`

`cmd/` holds the shipped binaries (`gateway`, `skopos-checkpoint`,
`skopos-mcp`) and exactly one developer tool, `cmd/repoctl`. A developer tool
is named by function and never carries a `skopos-` prefix. No CI, release, or
repository tooling gets its own `cmd/` directory or Makefile-only script.

### 2. Tool code is private to the tool

Rule engines and the GitHub client live under `cmd/repoctl/internal/`, one
package per check. Go's `internal` rule prevents product code from importing
them. Product packages stay under `internal/`; a product package that needs a
repository check is a design error, not an import exception.

The one allowed exception is a product package named in the architecture test's
allowlist. The allowlist starts empty; adding an entry requires a reason in the
PR and a test update.

### 3. One command contract

Every subcommand implements:

```go
type Command interface {
    Name() string
    Summary() string
    Run(ctx context.Context, env Env, args []string) error
}
```

`Env` carries stdout, stderr, environment lookup, the clock, the command
runner, and a GitHub client factory. The contract is:

- unknown subcommand: exit 2 with usage on stderr
- `--help` or `help`: exit 0 and list every registered command
- a command error: exit 1 and print the error once on stderr
- duplicate command names: fail at registration
- a nil `Env` field never panics; defaults are explicit

`main` packages only wire dependencies and call `Run`. They stay under 50
lines. `cmd/gateway` is grandfathered until its composition root moves to
`internal/app` (RS-31); the frozen `cmd/skopos-checkpoint` and `cmd/skopos-mcp`
are exempt while frozen.

### 4. Go for logic

Go is the implementation language for product code and repository checks.
Workflow YAML sets triggers, permissions, and calls Go. Shell is only thin
orchestration of external tools (docker, k3d, cosign, helm), under 80 lines,
with no `jq`/`awk`/`sed` pipeline that decides pass or fail. Decisions belong
in Go with Go tests.

No new Python. The existing harnesses under `test/` are frozen until their
parity-first ports land.

### 5. Python migration order

Each port is accepted only when it reproduces the recorded Python result on
pinned inputs. A difference is investigated and documented, never silently
adjusted. The order protects the EC2 evidence:

1. `test/bench/bench.py` (RS-20), because AX-25 rewrites most of that harness.
2. `test/compat/cerberus/harness.py` (RS-21), after the exit.
3. `test/compat/loki/harness.py`, `seed_clickhouse.py`, and `upstream.py`
   (RS-22), after the exit.
4. Delete every `*.py` and make the architecture test pass with an empty
   violation list (RS-23).

Until then, the architecture test records each existing Python file in
`test/quality/layout-violations.txt`. The list may only shrink.

### 6. Third-party developer tools

Linters and scanners are pinned in the separate module `internal/tools` and run
through make targets (RS-39). The module is exempt from the root `vendor/`
tree because nothing in it is shipped. Inline `go install tool@version` lines in
workflows are not allowed once the module lands. Adopting the module remains a
dependency-policy decision; this ADR records the layout if it is adopted.

## Consequences

- Adding a repository check is one package plus one registry line. It does not
  add a binary, a workflow, or a flag convention.
- GitHub access has one implementation to test for pagination, rate limits, and
  token redaction.
- Moving a check is a pure move: tests move with the code and assertions do not
  change.
- The coverage floor is keyed by import path. A move records the old and new
  path in `test/quality/coverage-renames.json` so the ratchet does not mistake a
  rename for a removed package.
- The frozen Python harnesses remain until their parity ports land; the
  architecture test prevents new Python from being added.

## Open decisions

The following are not settled by this ADR and need a separate maintainer
decision:

- whether to adopt `golangci-lint` (RS-30) and the pinned-tools module (RS-39)
- whether to port the cerberus and Loki harnesses before or after the exit
- whether to keep the product binary names unchanged
- whether to move `issues/alpha-exit.json` under `.github/alpha-exit/`
- whether the chart's `migrate.sh` becomes a Go binary
- whether to adopt changelog entries as files after the exit
