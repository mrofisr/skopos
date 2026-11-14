# Skopos repository structure and code-quality instructions

Accepted for the alpha line · 12 Nov 2026 · reflects the single-binary
consolidation, the Python removal, and the centralized script layout.

## 1. Purpose and status

These instructions keep the repository readable and stop tooling sprawl while
the alpha-exit tickets land. Sections 3 and 4 are normative. The layout and
language policy is recorded as [ADR 0004](docs/adr/0004-repository-layout-and-language-policy.md);
the directory map is [docs/structure.md](docs/structure.md).

Current shape:

- `cmd/` contains one binary, `cmd/skopos`. `SKOPOS_MODE` selects `all`
  (default), `gateway` (alias), `query`, `worker` (legacy alias), `frontend`,
  `mcp`, or `checkpoint`.
- Repository tooling is one binary, `tools/repoctl`.
- The repository has no Python. The Go harnesses in `test/` and the committed
  corpus expectations are the checks.
- Shell scripts live under `scripts/`, grouped by purpose. The only exception
  is the Helm-packaged `deploy/helm/skopos/files/migrate.sh`.

## 2. What changed

| Area | Before | Now |
| --- | --- | --- |
| `cmd/` | Three shipped binaries and nine `cmd/skopos-*` tools | One `cmd/skopos` |
| Mode selection | One binary per role | `SKOPOS_MODE` |
| Repository checks | Nine binaries with separate flag and output conventions | `tools/repoctl` with one `Run` contract |
| GitHub access | `gh` wrapper, inline `gh api`, raw `net/http` | `tools/repoctl/internal/ghapi` |
| Python | Five harnesses, about 2,200 lines | None; Go tests and committed fixtures |
| Scripts | 27 scripts spread across `test/`, `deploy/`, and `scripts/` | `scripts/<purpose>/`, one Helm asset |
| `main` size | Some mains over 200 lines | Under 50 lines, checked |

The retired Python harnesses left recorded evidence, kept as documentation:

- `docs/cerberus-baseline.md` — the 160-check three-way result.
- `docs/logql-upstream-baseline.md` — the 83-case LogQL result.
- `test/bench/testdata/baseline-*.json` — the benchmark parity fixtures.

Those are historical evidence, not live gates. The live checks are the Go
tests and the corpus verifiers.

## 3. Layout

```text
cmd/
  skopos/                         the single shipped binary: wiring only
tools/
  repoctl/                        the one developer tool
    internal/                     tool-private packages
      command/                    Command contract, Env, registry, exit codes
      ghapi/                      the one GitHub client
      changes/                    changed-path to CI job mapping
      cigate/                     aggregate CI result gate
      codescanning/               code-scanning alert gate
      quality/                    coverage report and ratchet
      issueops/                   alpha-exit manifest, lint, and sync
      prcheck/                    PR traceability and freeze guard
      osvexceptions/              dated OSV exception ledger
      grafanacheck/               Grafana datasource health
      quickstart/                 README quickstart doc-test
      releaseverify/              release signature and digest verification
      ruleset/                    ruleset-to-workflow consistency
      sbom/                       CycloneDX fallback inventory for releases
      workflowlint/               workflow trigger and timeout policy
      workflowsdoc/               generated docs/workflows.md
internal/
  app/                            mode dispatch and composition roots
  appenv/                         injected process environment
  version/                        link-time build version
  auth/ chclient/ config/ frontend/ httpapi/ loki/ metrics/ plan/ prom/ sqlgen/ tempo/
  checkpoint/ semctx/             frozen product packages
  archtest/                       tests that enforce this layout
  tools/                          separate module: pinned dev tools (RS-39)
test/
  bench/ compat/ e2e/ integration/ grafana/    Go harnesses, build-tagged, black-box
test/quality/                     coverage floor, layout violations, quality data
scripts/                          thin orchestration of external tools only
  bench/ compat/ deploy/ e2e/ integration/ quality/ quickstart/ release/ scorecard/
.github/                          workflows, CODEOWNERS, dependabot
deploy/ docs/
AGENTS.md CONTRIBUTING.md
```

This follows `opentelemetry-collector-contrib` where it fits Skopos: developer
tools named by function, tool-private `internal/` packages, a separate pinned
tools module, and files split by responsibility with tests beside them. It
diverges where one module is simpler than contrib's per-component modules, and
where a single shipped binary replaces three.

## 4. Rules for agents and contributors

1. `cmd/` holds one binary, `cmd/skopos`. Never add a `cmd/` directory for a
   role, CI, release, or repository tooling.
2. A new process role is a package under `internal/app/<name>` plus one mode
   constant in `internal/app` and `internal/config`. A new repository check is
   a package under `tools/repoctl/internal/<name>` with a
   `Run(ctx, env, args) error` entry point, plus one line registering it.
3. GitHub is reached only through `tools/repoctl/internal/ghapi`. Check logic
   never execs `gh` and never calls `net/http` directly.
4. `main` packages only wire dependencies and call `Run`. They contain no
   parsing, validation, or decision logic and stay under 50 lines.
5. Go for logic. No Python. Shell only for thin orchestration of external tools
   (docker, k3d, cosign, helm): under 80 lines, with no jq, awk, or sed
   pipeline that decides pass or fail. Decisions belong in Go with Go tests.
6. Every shell script lives under `scripts/`; the only exception is
   `deploy/helm/skopos/files/migrate.sh`, which the chart packages.
7. Test harnesses are Go under `test/`, build-tagged, and talk to references
   black-box. Committed corpus expectations are contracts, not regenerated
   inputs for the same test.
8. Package names describe a responsibility. No `util`, `common`, `helpers`,
   `misc`, or `types` packages.
9. Define small interfaces (three methods or fewer) in the package that uses
   them. Accept interfaces, return concrete types. Do not export an interface
   just to mock it.
10. No package-level mutable state and no `init()`. Inject the clock,
    environment, filesystem, HTTP client, and command runner.
11. Anything that does I/O takes `context.Context` first. Wrap errors with
    `%w`. No panics for control flow. `os.Exit` only in `main`.
12. Product code never imports `tools/repoctl/internal/...`; the compiler
    enforces it. `tools/repoctl` may import a product package only if the
    allowlist in `internal/archtest` names it.
13. Third-party developer tools (linters, scanners) are pinned in the separate
    module `internal/tools` and run through make targets. Do not add inline
    `go install tool@version` lines to workflows.
14. Every package has a `doc.go` with a package comment. The query heads
    (`prom`, `loki`, `tempo`) also have a `README.md` that states what is
    supported and links to the generated support matrix.
15. A refactor PR is a pure move or rename: no existing test assertion changes
    (only import paths), golden outputs byte-identical, label `type:refactor`.
    Behavior changes go in a separate PR.
16. Every rule in section 4 of the alpha plan still applies: red-first, edge
    cases, ratchets, mutation testing.

## 5. SOLID in this codebase

| Principle | What it means here | Enforced by |
| --- | --- | --- |
| Single responsibility | One package per check or per signal head, one reason to change, files split by responsibility when they pass about 600 lines | `internal/archtest`, review |
| Open/closed | A new check is a new package plus one registry line; existing commands are not edited | registry test in `tools/repoctl` |
| Liskov | Every subcommand honors the same `Run` contract: the same exit codes, the same output channels, no panics | contract tests |
| Interface segregation | Each check declares the one or two methods it needs from GitHub, not the whole client | review |
| Dependency inversion | Checks depend on interfaces; `repoctl` wires the real `ghapi` client, clock, and runner; tests inject fakes | `tools/repoctl/internal/command` |

## 6. Testing and quality gates

- Unit tests run with `go test ./...`; the race detector runs in CI.
- `internal/archtest` enforces the layout: one `cmd/` binary, no repository
  tooling in `internal/`, no forbidden package names, no Python, scripts under
  `scripts/`, and every `main` under 50 lines.
- `test/quality/layout-violations.txt` is a ratchet. It is currently empty. A
  new violation fails, and a listed violation that no longer exists also fails.
- Coverage floors live in `test/quality/coverage-floor.json`; a move records
  the old and new import path in `test/quality/coverage-renames.json`.
- The workflow policy check (`repoctl workflow-lint`) enforces triggers,
  concurrency, timeouts, and permissions.
- `docs/workflows.md` is generated by `repoctl workflows-doc`; CI fails on a
  diff.

## 7. CI triggers

Every pull request into `development` or `main` always reports a result for
every required check. Work is skipped inside the workflow, never by filtering
the workflow out.

- `pull_request` with `branches: [development, main]`, plus
  `workflow_dispatch` and a nightly `schedule`. No bare `push`.
- A `changes` job maps changed paths to groups; every other job has
  `needs: changes` and the fail-safe
  `if: ${{ !cancelled() && needs.changes.outputs.<group> != 'false' }}`.
- Run everything when the base is `main`, the event is not a PR, the PR has the
  `ci:full` label, a force path changed, the file list is empty or over 3,000,
  or a changed path matches no group.
- A `ci-gate` job with `if: always()` aggregates the result: it fails when
  path detection failed, a job failed, or a job was skipped although its group
  was true. The checked-in ruleset already requires `CI gate`; the live
  `main-protection` ruleset still requires the per-job names until WF-07 flips
  it. See [docs/ci-contract.md](docs/ci-contract.md) for the current live
  required checks and the recorded drift.
- Concurrency group `ci-${{ github.event.pull_request.number || github.run_id }}`
  with `cancel-in-progress: true`.

`docs/workflows.md` and `docs/ci-contract.md` record the current jobs and
required checks. `docs/security/ruleset.json` is the ruleset snapshot.

## 8. Open work

The alpha plan in [docs/alpha-exit-plan.md](docs/alpha-exit-plan.md) owns the
exit criteria. The layout work that remains:

- RS-30 golangci-lint with a ratchet (needs approval).
- RS-32 to RS-35 split the large query-engine files.
- RS-36 refactor guard.
- RS-37 package-level variable audit.
- RS-38 generate `docs/structure.md`.
- RS-39 pinned dev-tools module.
- RS-40 `doc.go` and README per package.
- RS-41 changelog entries as files (after exit).
- Port the Cerberus three-way comparison back to Go if it must run again.

Use `backlog:post-alpha` for work that serves no exit criterion.

## Agent skills

This repo uses the [mattpocock/skills](https://github.com/mattpocock/skills)
conventions for agent-facing documentation:

- [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md): where tickets
  live, the to-tickets body template, native blocked-by edges, and the sync and
  lint rules.
- [docs/agents/triage-labels.md](docs/agents/triage-labels.md): how the five
  canonical triage roles map onto this repo's labels.
