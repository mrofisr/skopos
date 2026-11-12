# ADR 0004: Repository layout and language policy

- Status: Accepted for the alpha line; amended 2026-11-12
- Date: 2026-11-10
- Supersedes: the "Existing Python under `test/` is frozen" paragraph in
  `CONTRIBUTING.md` (commit 93da71b)
- Related: [ADR 0001](0001-observability-stack.md), [CONTRIBUTING.md](../../CONTRIBUTING.md),
  [AGENTS.md](../../AGENTS.md), [structure.md](../structure.md)

## Context

The alpha-exit work added one repository-check binary per ticket. By
2026-11-10 there were nine `cmd/skopos-*` tooling binaries beside three shipped
ones: code scanning, coverage, issues, OSV exceptions, PR guard, Grafana
health, quickstart, release verification, and ruleset consistency. Each had its
own flag parsing, output convention, and GitHub access. Three implementations
reached GitHub differently: a `gh` wrapper, inline `gh api` calls, and raw
`net/http`.

The alpha plan's rule 3 says work that serves no exit criterion waits. This
policy is the exception: without one layout and one tool contract, AX-52,
AX-53, AX-54, AX-60, and AX-61 each add another binary or another shell script
with decision logic. The repository-check tooling is itself part of EC6 (strict
gates are live), so keeping it maintainable serves the exit.

## Decision

### 1. One shipped binary

`cmd/` holds exactly one directory, `cmd/skopos`. The binary chooses its role
from `SKOPOS_MODE`:

| Mode | Role |
| --- | --- |
| `all` (default) | Public API and the three query heads |
| `gateway` | Alias for `all`, kept for existing manifests |
| `query` | Query worker only |
| `worker` | Legacy alias for `query` |
| `frontend` | Health/version proxy in front of query workers |
| `mcp` | MCP server over stdio, or HTTP with `SKOPOS_MCP_HTTP_ADDR` |
| `checkpoint` | Checkpoint receiver; `skopos replay` replays from S3 |

The mode implementations live under `internal/app/<mode>`. `cmd/skopos/main.go`
contains only signal wiring and the call to `app.Main`, and stays under 50
lines.

`SKOPOS_MODE=checkpoint skopos replay` preserves the checkpoint CLI. `serve`
remains the default checkpoint subcommand.

The developer tool lives outside `cmd/`:

```text
tools/repoctl/            main package, registry only
tools/repoctl/internal/   one package per check, plus ghapi
```

No CI, release, or repository tooling gets a `cmd/` directory, a second binary,
or a Makefile-only script.

### 2. Tool code is private to the tool

Rule engines and the GitHub client live under `tools/repoctl/internal/`, one
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
lines.

### 4. Go for logic

Go is the implementation language for product code and repository checks.
Workflow YAML sets triggers, permissions, and calls Go. Shell is only thin
orchestration of external tools (docker, k3d, cosign, helm), under 80 lines,
with no `jq`/`awk`/`sed` pipeline that decides pass or fail. Decisions belong
in Go with Go tests.

The repository has no Python source. The earlier Python harnesses were retired:
`bench.py` was replaced by the Go runner in `test/bench`, and the Cerberus and
Loki differentials were replaced by committed corpus expectations and Go tests.
The recorded results are kept in
[`cerberus-baseline.md`](../cerberus-baseline.md) and
[`logql-upstream-baseline.md`](../logql-upstream-baseline.md). The architecture
test fails on any new `*.py` file.

Every shell script lives under `scripts/`, grouped by purpose:

```text
scripts/bench/ compat/ deploy/ e2e/ integration/ quickstart/ release/ scorecard/
```

The one exception is `deploy/helm/skopos/files/migrate.sh`, which the Helm
chart packages and must stay beside the chart. `TestShellScriptsAreCentralized`
enforces both rules.

### 5. Third-party developer tools

Linters and scanners are pinned in the separate module `internal/tools` and run
through make targets (RS-39). The module is exempt from the root module's
dependency graph because nothing in it is shipped. Inline `go install
tool@version` lines in workflows are not allowed once the module lands.
Adopting the module remains a dependency-policy decision; this ADR records the
layout if it is adopted.

## Consequences

- Adding a repository check is one package plus one registry line. It does not
  add a binary, a workflow, or a flag convention.
- Adding a process role is one package under `internal/app` plus one mode
  constant. It does not add a binary or an image.
- GitHub access has one implementation to test for pagination, rate limits, and
  token redaction.
- Moving a check is a pure move: tests move with the code and assertions do not
  change.
- The coverage floor is keyed by import path. A move records the old and new
  path in `test/quality/coverage-renames.json` so the ratchet does not mistake a
  rename for a removed package.
- A differential gate that depended on a retired harness is documented as
  recorded evidence, not as a live check. Porting it back to Go is a normal
  ticket.

## Open decisions

The following are not settled by this ADR and need a separate maintainer
decision:

- whether to adopt `golangci-lint` (RS-30) and the pinned-tools module (RS-39)
- whether to port the Cerberus three-way comparison to Go
- whether to move `issues/alpha-exit.json` under `.github/alpha-exit/`
- whether the chart's `migrate.sh` becomes a Go binary
- whether to adopt changelog entries as files after the exit
