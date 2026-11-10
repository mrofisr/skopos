# Skopos repository structure and code-quality instructions

Accepted for the alpha line · 10 Nov 2026 · snapshot based on `development` at 0192849; refreshed for the AX-52, AX-53, and AX-54 tooling added after that snapshot

## 1. Purpose and status

These instructions make the repository read like a professional Go project and stop the tooling sprawl from growing while the alpha-exit tickets land. Sections 3 to 5 are normative. Section 4 is mirrored in `CONTRIBUTING.md`; the layout and language policy is recorded as [ADR 0004](docs/adr/0004-repository-layout-and-language-policy.md), and the directory map is [docs/structure.md](docs/structure.md).

Two things to know:

- **This amends the earlier policy.** `CONTRIBUTING.md` (commit 93da71b) said the existing Python under `test/` was frozen for the alpha and that migrating it was a post-alpha decision. ADR 0004 supersedes that paragraph with a parity-first migration order that protects the exit evidence.
- **This adds scope.** None of it is in an exit criterion. The alpha plan's rule still applies: work that serves no EC waits in `backlog:post-alpha`. Section 8 separates what is cheap and prevents rework now from what should wait.

**Revision (10 Nov 2026).** After comparing the layout with `opentelemetry-collector-contrib` (section 3), developer tooling lives in `cmd/repoctl` instead of `tools/repoctl`, its private packages sit in `cmd/repoctl/internal/`, and pinned third-party dev tools move to a separate `internal/tools` module. Sections 3, 4, 7, 8 and 9 below already reflect that. AX-52, AX-53, and AX-54 have since added four more tooling binaries (`skopos-grafana-check`, `skopos-quickstart`, `skopos-release-verify`, `skopos-ruleset-check`), so the migration covers nine tools, not five. The findings below are kept as the 9 Nov snapshot; the rules and ticket sequence apply to all nine.

## 2. What I found

| Area | Finding |
| --- | --- |
| `cmd/` | 12 directories. Three are shipped binaries (`gateway`, `skopos-checkpoint`, `skopos-mcp`). Nine are repository tooling (`skopos-code-scanning`, `skopos-coverage`, `skopos-grafana-check`, `skopos-issues`, `skopos-osv-check`, `skopos-prcheck`, `skopos-quickstart`, `skopos-release-verify`, `skopos-ruleset-check`), all added by AX tickets. AX-60 and AX-61 would add more without this policy. |
| Tooling mains | Three of the original five (`skopos-coverage` 173 lines, `skopos-issues` 145, `skopos-prcheck` 242) have no tests. The four added by AX-52 to AX-54 (`skopos-grafana-check` 69, `skopos-quickstart` 113, `skopos-release-verify` 92, `skopos-ruleset-check` 85) do have tests. prcheck's `main` runs `gh api` through `exec` and parses the event JSON inline. The rule engines have Go tests in their `internal/` packages (prcheck 330 test lines, issueops 539, codescanning 161, osvexceptions 100, quality 135). |
| GitHub access | Three separate implementations: a `gh` wrapper in `internal/issueops/gh.go`, inline `gh api` in `skopos-prcheck`, and raw `net/http` in `internal/codescanning`. No shared client, pagination or rate-limit handling. |
| `internal/` | Product packages (`auth`, `chclient`, `config`, `frontend`, `httpapi`, `loki`, `plan`, `prom`, `sqlgen`, `tempo`, `checkpoint`, `semctx`) sit beside repository-tooling packages (`codescanning`, `grafanacheck`, `issueops`, `osvexceptions`, `prcheck`, `quality`, `quickstart`, `releaseverify`, `ruleset`) with nothing to tell them apart. |
| Python | Five files, about 2,200 lines: `test/bench/bench.py` (641), `test/compat/cerberus/harness.py` (713), `test/compat/loki/harness.py` (254), `seed_clickhouse.py` (68), `upstream.py` (534). No Python setup or pinned version appears in `.github`, the Makefile or the Dockerfile; the harnesses run from `run.sh` on whatever python3 the runner has. Go harnesses already exist beside them (`test/compat/prometheus`, `test/compat/tempo`, `test/compat/cerberus/seed`, `test/compat/loki/upstream`), so Go is the established direction. |
| Shell | 27 scripts. Many are thin docker or k3d orchestration, which your policy allows. Scripts with real logic: `deploy/helm/skopos/files/migrate.sh` (137 lines), `scripts/release.sh` (108), `scripts/scorecard.sh` (133), `scripts/sbom-fallback.sh` (105), `test/e2e/obi/verify.sh` (103). |
| Linting | CI runs `go vet` only. There is no golangci-lint config and no import-boundary check. |
| Large files | `internal/prom/engine.go` 1,565 lines, `internal/tempo/filter.go` 912, `internal/tempo/api.go` 861, `internal/loki/query.go` 722, `internal/chclient/client.go` 621 (plus the frozen `semctx/api.go` 1,068 and `skopos-mcp/server.go` 705). Open AX tickets (AX-12, AX-13, AX-31 to AX-38) are about to edit most of these. |
| Already good | Stdlib-first dependencies, no `init()` functions, no `util` packages, and Go tests for the rule engines. |

## 3. Target layout

```
cmd/                              binaries, shipped or developer; developer tools carry no skopos- prefix
  gateway/                        shipped: main.go only, wiring and no logic
  skopos-checkpoint/              shipped (frozen)
  skopos-mcp/                     shipped (frozen)
  repoctl/                        the one developer tool: subcommands only
    internal/                     tool-private packages, importable only from cmd/repoctl
      ghapi/                      the one GitHub client
      quality/ issueops/ prcheck/ codescanning/ osvexceptions/    one package per check
      grafanacheck/ quickstart/ releaseverify/ ruleset/           checks added by AX-52 to AX-54
internal/
  app/                            composition roots (gateway.New, Run) so wiring is testable
  auth/ chclient/ config/ frontend/ httpapi/ loki/ plan/ prom/ sqlgen/ tempo/    product libraries
  checkpoint/ semctx/             (frozen)
  tools/                          separate Go module: pinned dev tools (golangci-lint, govulncheck, actionlint)
  archtest/                       tests that enforce this layout
test/
  compat/ e2e/ integration/ bench/ grafana/    Go harnesses, build-tagged, black-box
scripts/                          thin shell orchestration of external tools only
.github/                          workflows, CODEOWNERS, dependabot
deploy/ docs/ vendor/
AGENTS.md CONTRIBUTING.md         agent and contributor rules (section 4)
```

This layout follows `opentelemetry-collector-contrib` (checked at commit b83eb00, 9 Nov 2026), with one change from my first draft, which had put the tool in `tools/repoctl`. Contrib keeps developer tools in `cmd/` beside its shipped binaries, named by function (`golden`, `codecovgen`) rather than by project, and gives a tool its own private `internal/` packages (`cmd/golden/internal`). Doing the same keeps `cmd/` the one place to look for binaries, and Go's own `internal` rule, not a lint, stops product code from importing tool code. Contrib has no Python tooling (its one `.py` file is a Spark workload fixture in a receiver's `testdata`) and pins third-party dev tools in a separate module, `internal/tools`. One binary instead of nine means one flag, environment and exit-code convention, one build and one place for GitHub access; adding a check means adding a package and one registry line. What I took from contrib and what I did not:

| Contrib practice | Skopos decision | Why |
| --- | --- | --- |
| Developer tools in `cmd/`, named by function, with tool-private `internal/` packages | Adopted: `cmd/repoctl` with `cmd/repoctl/internal/` | The compiler enforces the boundary |
| Pinned dev tools in a separate `internal/tools` module (Go `tool` directive) | Adopted (RS-39, needs your approval) | Replaces the inline `go install tool@version` pins in `ci.yml` and keeps the product `go.mod` clean |
| Root `AGENTS.md` | Adopted (RS-01) | One obvious place for agent rules |
| `doc.go` and `README.md` per component | Adopted for packages (RS-40) | Documentation sits next to the code |
| Files split by responsibility, with their tests beside them and helpers in a local `internal/` | Adopted for the file splits (RS-32 to RS-35) | Same shape as a contrib component |
| CI scope computed from changed files, a `ci:full` label, always-running aggregator jobs | Adopted in section 9 | Same pattern, written in Go |
| Changelog entries as files in `.chloggen/` | Optional after exit (RS-41) | Many micro-PRs would not conflict on one `CHANGELOG.md`; Skopos has no changelog yet |
| CI glue as shell in `.github/workflows/scripts/` (25 of contrib's 45 shell scripts) | Not adopted (decision 9) | Skopos policy puts decisions in Go |
| About 350 Go modules (one per component) and public `pkg/` libraries | Not adopted | Skopos is one module with no outside importers; contrib splits for independent versioning |
| `metadata.yaml` plus code generation per component (about 390 files) | Not adopted | Heavy for three heads; generated docs are already covered by STG-7 |
| Top-level directories by component kind (`receiver/`, `processor/`, `exporter/`) | Not adopted | Skopos has three query heads, not hundreds of plug-ins, so they stay in `internal/` |

## 4. Rules for agents and contributors (paste-ready)

1. `cmd/` holds binaries only: the shipped ones (`gateway`, `skopos-checkpoint`, `skopos-mcp`) and one developer tool, `cmd/repoctl`. Developer tools are named by function and never carry a `skopos-` prefix. Never add another `cmd/` directory for CI, release or repository tooling.
2. A new repository check is a package under `cmd/repoctl/internal/<name>` with a `Run(ctx, env, args) error` entry point, plus one line registering it in `cmd/repoctl`. No new binary and no new Makefile-only script.
3. GitHub is reached only through `cmd/repoctl/internal/ghapi`. Check logic never execs `gh` and never calls `net/http` directly.
4. `main` packages only wire dependencies and call `Run`. They contain no parsing, validation or decision logic and stay under 50 lines (`cmd/gateway` is grandfathered until RS-31).
5. Go for logic. No new Python. Shell only for thin orchestration of external tools (docker, k3d, cosign, helm): under 80 lines, with no jq, awk or sed pipeline that decides pass or fail. Decisions belong in Go with Go tests.
6. Test harnesses are Go under `test/`, build-tagged, and talk to references black-box.
7. Package names describe a responsibility. No `util`, `common`, `helpers`, `misc` or `types` packages.
8. Define small interfaces (three methods or fewer) in the package that uses them. Accept interfaces, return concrete types. Do not export an interface just to mock it.
9. No package-level mutable state and no `init()`. Inject the clock, environment, filesystem, HTTP client and command runner.
10. Anything that does I/O takes `context.Context` first. Wrap errors with `%w`. No panics for control flow. `os.Exit` only in `main`.
11. Product code never imports `cmd/repoctl/internal/...`; the compiler enforces it. `cmd/repoctl` may import a product package only if the allowlist in RS-02 names it (for example `internal/config`, to generate the limits table).
12. Third-party developer tools (linters, scanners) are pinned in the separate module `internal/tools` and run through make targets. Do not add inline `go install tool@version` lines to workflows.
13. Every package has a `doc.go` with a package comment. The query heads (`prom`, `loki`, `tempo`) also have a `README.md` that states what is supported and links to the generated support matrix.
14. A refactor PR is a pure move or rename: no existing test assertion changes (only import paths), golden outputs byte-identical, label `type:refactor`. Behavior changes go in a separate PR.
15. Every rule in section 4 of the alpha plan (red-first, edge cases, ratchets, mutation) still applies to every ticket here.

## 5. SOLID in this codebase

| Principle | What it means here | Enforced by |
| --- | --- | --- |
| Single responsibility | One package per check or per signal head, one reason to change, files split by responsibility when they pass about 600 lines | RS-02, RS-32 to RS-35 |
| Open/closed | A new check is a new package plus one registry line; existing commands are not edited | registry test in RS-04 |
| Liskov | Every subcommand honors the same `Run` contract: the same exit codes, the same output channels, no panics | contract tests in RS-04 |
| Interface segregation | Each check declares the one or two methods it needs from GitHub (for example `OpenAlerts`), not the whole client | review and RS-30 |
| Dependency inversion | Checks depend on interfaces; `repoctl` wires the real `ghapi` client, clock and runner; tests inject fakes | rule 11 in RS-02 and RS-30 |

## 6. Instructions for tickets already in flight

| Ticket | Instruction |
| --- | --- |
| AX-52 ruleset check | Build as `repoctl ruleset`. No new `cmd/` directory. |
| AX-53 release verify | The branch `agent/ax-53-release-verify` already exists. Put the decision logic in Go as `repoctl release-verify`; shell may only invoke cosign and syft through an injected runner. Do not add another `scripts/*.sh` with logic. |
| AX-54 quickstart doc-test | README block extraction and version checks in Go. k3d orchestration may stay shell. |
| AX-60 exit check | `repoctl exit-check`, reading machine-readable evidence. |
| AX-25 bench gate | Written in Go. Port `bench.py` first (RS-20) because AX-25 rewrites most of the harness; the alternative is a Go gate that reads `bench.py`'s JSON. |
| AX-40 to AX-44 Grafana harness | Go from the start, under `test/grafana/`. |
| Already merged (AX-01, 04, 05, 06, 20, 45, 46, 48, 50, 51) | No rework except the moves in RS-05 to RS-09. |

## 7. Micro-tickets

Same template as the alpha plan: Blocked by, Red tests, Edge and negative, Done when. All seven test gates apply. Pure moves use the label `type:refactor`.

### Phase R0: stop the sprawl

**RS-01 ADR 0004: layout and language policy.** Blocked by: none.

- Docs only (exempt from red-first with `red-first-exempt`).
- Done when: `CONTRIBUTING.md` links ADR 0004, a root `AGENTS.md` carries every rule in section 4 verbatim, the frozen-Python paragraph is replaced by the migration order in RS-20 to RS-23, and `docs/structure.md` exists.

**RS-02 Architecture test with a shrinking violation list.** Blocked by: RS-01.

- Red tests: `TestCmdContainsOnlyAllowedBinaries` (allowlist: `gateway`, `skopos-checkpoint`, `skopos-mcp`, `repoctl`; fails today for the five `skopos-*` tooling directories), `TestNoRepoToolingInInternal` (fails today for `internal/prcheck`, `quality`, `issueops`, `codescanning` and `osvexceptions`), `TestRepoctlImportsOnlyAllowedProductPackages`, `TestNoUtilPackages`, `TestNoPythonFiles` (fails today). Known violations are listed in `test/quality/layout-violations.txt`, which may only shrink, the same ratchet idea as coverage.
- Mechanism: stdlib only, reading `go list -json ./...`. A product package importing `cmd/repoctl/internal` needs no test, because the compiler rejects it.
- Edge and negative: a fixture tree in `testdata/` with one violation per rule proves each rule fires; a new violation that is not listed fails; a listed violation that no longer exists fails, which forces the file to shrink.

**RS-03 `cmd/repoctl/internal/ghapi` shared GitHub client.** Blocked by: RS-01.

- Red tests (httptest): pagination across three pages; a 403 with `Retry-After` waits on a fake clock and then succeeds; 404 becomes a typed error; the token never appears in errors or logs; a cancelled context stops retries.
- Edge and negative: no token gives a clear error; a malformed `Link` header fails loudly; retries are bounded. In mutation scope.

**RS-04 `cmd/repoctl` skeleton and command contract.** Blocked by: RS-01.

- A `Command` interface (`Name`, `Run(ctx, env, args)`), an `Env` carrying stdout, stderr, getenv, clock, runner and the GitHub client, a registry, and exit-code mapping.
- Red tests: an unknown subcommand exits 2 with usage; `--help` exits 0 and lists every registered command; a command that returns an error exits 1 and prints it once; `main` is under 50 lines (checked by RS-02).
- Edge and negative: nil env fields never panic; duplicate command names fail at registration.

**RS-05 Move coverage to `repoctl coverage` (report, gate, write).** Blocked by: RS-02, RS-04. Refactor.

**RS-06 Move issues to `repoctl issues` (manifest, sync, lint).** Blocked by: RS-03, RS-04. Refactor. Port `issueops/gh.go` onto `ghapi` and move the root `issues/` manifest to `.github/alpha-exit/` (decision 7).

**RS-07 Move prcheck to `repoctl prcheck`.** Blocked by: RS-03, RS-04. Refactor. Replace the inline `gh api` exec with `ghapi` and update `pr-guard.yml`.

**RS-08 Move code scanning to `repoctl codescan`.** Blocked by: RS-03, RS-04. Refactor. Switch it onto `ghapi`.

**RS-09 Move the OSV check to `repoctl osv`.** Blocked by: RS-04. Refactor.

Shared Done-when for RS-05 to RS-09:

- Before deleting the old directory, commit golden files of the old binary's stdout, stderr and exit code on the existing fixtures. This is the red-first step: the new subcommand fails them while it is empty.
- The moved packages' existing tests pass with import-path changes only. The previously untested mains (coverage, issues, prcheck) gain CLI-level tests through `Run` with a fake env.
- The Makefile, workflows and docs call `go run ./cmd/repoctl <command>`. The old `cmd/skopos-*` directory is deleted in the same PR, its package moves to `cmd/repoctl/internal/`, and the RS-02 violation list shrinks by one. Required status check names do not change.

**RS-10 Remove the one-line wrapper scripts.** Blocked by: RS-08, RS-09.

- Delete `scripts/check-code-scanning.sh` and `scripts/check-osv-exceptions.sh`; call `repoctl` from Make and the workflows.

### Phase R1: Python to Go, parity first

Rule for every port: a Go harness is accepted only when it reproduces the Python harness's recorded results on pinned inputs. The Python file is deleted in a later PR. Any difference is investigated and documented, never silently adjusted (rule 4 of CONTRIBUTING).

**RS-20 Port `test/bench/bench.py` (641 lines) to Go.** Blocked by: RS-01. Blocks: AX-25.

- Red tests: a parity test runs the Go harness and a recorded Python result against a deterministic stub server and compares normalized JSON (same keys, counts and error classes; latency values excluded); error classification matches for dropped connections, 5xx and deliberate 429 or 503.
- Edge and negative: a stub that resets connections, a stub that returns a bare 500, an empty workload.

**RS-21 Port the cerberus three-way harness (713 lines).** Blocked by: RS-01.

- Lives beside the existing Go `seed/`. Parity: per-case verdicts (match, diverge, unsupported) equal a golden file recorded from the Python harness on the pinned reference images, with zero differences allowed.

**RS-22 Port the Loki harness, `seed_clickhouse.py` and `upstream.py` (about 850 lines).** Blocked by: RS-01.

- Parity: the regenerated upstream baseline report equals the committed one (83 of 83 cases) byte for byte.

**RS-23 Delete the Python and enforce it.** Blocked by: RS-20, RS-21, RS-22 and two consecutive green runs of each affected workflow.

- Remove every `*.py`, update the docs, and make `TestNoPythonFiles` pass with an empty violation list.

**RS-24 Shell audit (spike, one day).** Blocked by: RS-01.

- Classify the 27 scripts as orchestration (keep) or logic (port), write `docs/shell-inventory.md`, and create one micro-ticket per script that has logic, starting with `release.sh`, `scorecard.sh`, `sbom-fallback.sh`, `verify-release.sh`, `test/e2e/obi/verify.sh` and `deploy/helm/skopos/files/migrate.sh`.
- Done when: another person can reproduce the classification from the document.

### Phase R2: code structure and linting

**RS-30 golangci-lint with a ratchet.** Blocked by: RS-01. Needs your approval (new CI tool, dependency policy).

- Pin the exact version in `internal/tools` (RS-39). Enable errcheck, govet, staticcheck, unused, ineffassign, errorlint, bodyclose, noctx, contextcheck, gosec, gocritic, revive, misspell, forbidigo (no `fmt.Print*` outside `main`, no `http.DefaultClient`), depguard (rule 11) and a complexity linter with a threshold taken from the measured baseline. Use contrib's `.golangci.yml` as a reference for the list, not as a copy. Use new-from-rev so only new code is judged, then burn the backlog down.
- Red tests: `TestLintConfig_CatchesSeededViolations` runs the linter over a seeded testdata module and expects one finding per enabled linter; a clean module yields none.
- Edge and negative: existing violations do not fail the build; a new one does.

**RS-31 Gateway composition root.** Blocked by: AX-23. Refactor.

- Move server construction, timeouts and handler assembly out of `cmd/gateway/main.go` (244 lines plus the context files) into `internal/app/gateway` with `New(cfg)` and `Run(ctx)`; `main` becomes wiring only. The frozen context glue moves under a logged `frozen-exception`.
- Red tests: `TestApp_RunStopsOnContextCancel`, `TestApp_NewRejectsInvalidConfig`, and a golden list of registered routes that must not change.

**RS-32 Split `internal/prom/engine.go` (1,565 lines) by responsibility.** Blocked by: AX-31, AX-32. Refactor.

**RS-33 Split `internal/tempo/filter.go` and `api.go`.** Blocked by: AX-35 to AX-38. Refactor.

**RS-34 Split `internal/loki/query.go` and `api.go`.** Blocked by: RS-30. Refactor.

**RS-35 Split `internal/chclient/client.go`.** Blocked by: AX-12, AX-13. Refactor.

Shared Done-when for RS-32 to RS-35: pure moves; `git diff --color-moved` shows only moved blocks; tests move with the code they test (a new `engine_select.go` pairs with `engine_select_test.go`) and no assertion changes; golden and oracle outputs byte-identical; each new file states one responsibility in its header comment; helpers used by several new files go into a package-local `internal/` subpackage, the way contrib components do; no new file over about 600 lines.

**RS-36 Refactor guard.** Blocked by: AX-04.

- For PRs labelled `type:refactor`, fail if an existing `_test.go` line changes other than imports, package clauses or file moves.
- Red tests: fixtures where an import-only change passes, an added test passes, and a modified or removed assertion fails.

**RS-37 Package-level variable audit (spike).** Blocked by: RS-30.

- Classify the 24 package-level variables as constant-like (keep) or mutable state (inject) and create one micro-ticket per mutable one.

**RS-38 `docs/structure.md` kept honest.** Blocked by: RS-02.

- A generated directory table; CI fails on a diff (STG-7).

**RS-39 `internal/tools` pinned dev-tools module.** Blocked by: RS-01. Needs your approval (dependency policy).

- A separate Go module with a `tool` directive for the third-party tools CI runs (govulncheck, actionlint, go-licenses, and golangci-lint once RS-30 lands), run through make targets. It replaces the inline `go install tool@version` pins in `ci.yml`, and Dependabot gets a `gomod` entry for `/internal/tools`. ADR 0004 states that this module is exempt from vendoring because nothing in it is shipped (contrib has no `vendor/`).
- Red tests: a script test fails when any workflow contains a `go install` with an `@version` (fixtures: pinned in the module passes, inline version fails); `make tools-verify` fails when `internal/tools/go.sum` is out of date.
- Edge and negative: the root `go.mod`, `go.sum` and `vendor/` do not change, and `go build ./...` at the root ignores the module.

**RS-40 `doc.go` and README per package.** Blocked by: RS-02.

- Every package under `cmd/` and `internal/` gets a `doc.go` with a package comment. `prom`, `loki` and `tempo` also get a `README.md` that states what is supported, links to the generated support matrix (AX-49) and says how to run the package's tests.
- Red tests: `TestEveryPackageHasDocGo`, with a shrink-only exception list in `test/quality/layout-violations.txt`; `TestHeadsHaveReadme`.
- Edge and negative: `testdata/` and generated directories are exempt; a `doc.go` with an empty comment fails.

**RS-41 Changelog entries as files (optional, after exit).** Blocked by: RS-01.

- One small file per PR in `.changelog/` (contrib's `.chloggen/` pattern; YAML if the dependency in section 9.5, decision 3 is approved, otherwise JSON), assembled into `CHANGELOG.md` at release time by `repoctl changelog`. Many micro-PRs then never conflict on one file, and it feeds the release notes in AX-55.
- Red tests: a valid entry passes; a `type:micro` PR with no entry fails unless it carries the label `no-changelog`; a malformed entry fails; the assembled output is deterministic.
- Edge and negative: docs-only and CI-only PRs are exempt.

## 8. Sequencing, scope and decisions

**Do now** (cheap, and it prevents rework; the closest EC fit is EC6, because the strict-gate tooling itself has to stay maintainable): RS-01 to RS-04, then RS-05 to RS-09 in any order, then RS-10, plus RS-20 because it blocks AX-25. That is about 12 tickets. Without RS-01 to RS-04 first, the open AX-52, AX-53, AX-54 and AX-60 will each add another `cmd/skopos-*` or script.

**Wait until after exit** (`backlog:post-alpha`): RS-21 to RS-24, because those harnesses hold the EC2 evidence and moving them mid-alpha puts it at risk; and RS-30 to RS-38, because lint adoption and file splits would conflict with AX-12, AX-13, AX-23 and AX-31 to AX-38 while those are open. Move one forward only if you judge it serves an EC, which by rule 3 means a PR to the alpha plan.

**The three tickets added after the contrib comparison.** RS-39 (pinned dev tools) can join the do-now group once you approve it, because it only changes how CI installs tools. RS-40 (`doc.go` and READMEs) and RS-41 (changelog entries) wait until after exit.

**Decisions to confirm**

1. Name and location: `cmd/repoctl` with `cmd/repoctl/internal/*`, following contrib. This changes my first draft (`tools/repoctl`); the alternatives are `hack/` or `tools/`.
2. Supersede the Python freeze with ADR 0004. It is your policy; I am only proposing the amendment.
3. Port `bench.py` before AX-25 (alternative: the Go gate reads `bench.py`'s JSON and the port waits).
4. Port the cerberus and Loki harnesses after exit (recommended) or now.
5. Adopt golangci-lint, pinned through the `internal/tools` module (a new CI tool that needs your approval under the dependency policy).
6. Keep the product binary names. `gateway` has no prefix while the others do, but renaming is churn with no user value.
7. Move `issues/alpha-exit.json` out of the repo root into `.github/alpha-exit/`.
8. Decide later whether the chart's `migrate.sh` becomes a Go binary. It is product deployment code, so it is not part of this plan.
9. Keep the top-level `scripts/` (recommended: few scripts remain after RS-10 and RS-24) or adopt contrib's split, `.github/workflows/scripts/` for CI-only scripts and a separate directory for developer scripts.
10. Adopt the separate pinned-tools module (RS-39). It needs your approval and the vendoring statement in ADR 0004.
11. Adopt changelog entries as files after exit (RS-41).

## 9. Workflow triggers and conditions

You asked for workflows that run only on the right conditions: CI on pull requests into `development` when relevant files change, and so on. This section is based on the seven workflows on `development` at 44e20ae. I could not read the live branch rulesets (the unauthenticated API is rate-limited), so every statement about which checks are required is unverified until WF-01 is done.

### 9.1 What I found

| Workflow | Trigger today | Verdict |
| --- | --- | --- |
| `ci.yml` | `push` on every branch and `pull_request` on every base, with no filters, no concurrency and no manual dispatch | Too broad. A branch with an open PR runs the 8-job matrix twice (push and PR). A docs-only PR runs the ClickHouse, Loki, Tempo, cerberus (25-minute limit) and RustFS jobs. Superseded runs are not cancelled. |
| `pr-guard.yml` | `pull_request`, any base, with concurrency | Fine. `internal/prcheck/prcheck.go` has no base or head branch logic, so a `development` to `main` promotion PR is checked like any other PR (I did not trace the `cmd` main). |
| `dependency-review.yml` | `pull_request`, any base, no concurrency | Add `branches` and concurrency. It is cheap, so run it on every PR rather than filter it. |
| `release.yml` | tags `v*`; preflight requires the tag commit to be on `main` | Right as is. |
| `scorecard.yml` | push to `main`, weekly, branch-protection event, manual | Right as is. |
| `issues.yml`, `promql-compliance.yml` | weekly and manual | Right as is. |

Gaps and traps:

- **The warning in `ci.yml` is correct.** Its header says not to add path filters, because a workflow skipped by `paths` leaves its required checks Pending forever and blocks the PR. A job skipped by an `if` condition reports Success and does not block. So the design below skips work inside the workflow and never filters the workflow out.
- **Hidden couplings break naive path filters.** The `go` job runs `make issues-manifest-check`, which reads `docs/alpha-exit-plan.md` and `issues/`. The coverage ratchet reads `test/quality/coverage-floor.json`. There are three Go modules (root, `test/compat/prometheus`, `test/e2e/obi/app`). The `policy` job bundles workflow linting with a license check over `go.mod` and `vendor/`. Loki verification compares against committed baseline docs.
- **Nothing builds the Dockerfile before a release.** A broken Dockerfile is found at tag time.
- **Promotion PRs need the full matrix.** Tags must be on `main` and `main-protection` requires green checks, so a PR from `development` into `main` must still get every check.

### 9.2 Design

Rule: every PR into a protected branch always gets a result for every required check. Work is skipped inside the workflow, never by filtering the workflow out.

1. **Triggers.** `pull_request` with `branches: [development, main]`, plus `workflow_dispatch` and a nightly `schedule` (it runs on the default branch, `development`). No bare `push`. The nightly full run replaces post-merge push runs as the safety net; decision 1 covers the alternative contrib uses.
2. **A `changes` job.** It runs first, always, and is not a required check. `repoctl changes` lists the PR's files through the GitHub API (the same approach `pr-guard` already uses) and writes one output per group. Contrib does this with a scope script and `dorny/paths-filter`; going through `ghapi` avoids another third-party action.
3. **Every existing job keeps its name** and gains `needs: changes` plus `if: ${{ !cancelled() && needs.changes.outputs.<group> != 'false' }}`. Unchanged names mean unchanged required checks. `!= 'false'` makes a failed detection run everything instead of silently skipping. `!cancelled()` is needed because without a status function GitHub adds an implicit `success()`, which would skip the job whenever `changes` fails.
4. **Run everything** when the base is `main`, the event is not a PR, the PR carries the label `ci:full` (a maintainer override, as in contrib), `ci.yml`, `.github/ci-paths.json`, `.golangci.yml` or `internal/tools/**` changed, the PR has no files or more than 3,000 (the API limit), or any changed path matches no known group. The last rule is the fail-safe: a new top-level directory can never skip checks.
5. **A `ci-gate` job** with `if: always()` that fails when `changes` did not succeed, any job failed or was cancelled, or a job was skipped although its group was true. It becomes the single required check, which ends the rule that renaming a job needs a ruleset edit. Contrib's aggregator jobs (`lint`, `checks`, `unittest`, `integration-tests`) always run and accept `success` or `skipped`; this one is stricter, accepting `skipped` only when the group was false.
6. **Concurrency** with the group `ci-${{ github.event.pull_request.number || github.run_id }}` and `cancel-in-progress: true`, contrib's pattern. PR runs cancel each other, while every push, tag or scheduled run has a unique `run_id` and is never cancelled.

Initial path groups (`go` means `**/*.go`, `**/go.mod`, `**/go.sum`, `vendor/**`, `Makefile`, `test/quality/**`, `docs/alpha-exit-plan.md`, `issues/**`):

| Job (name unchanged) | Runs when changed paths include |
| --- | --- |
| Go build, vet, test | `go` |
| Coverage ratchet | `go` |
| ClickHouse integration | `go`, `test/integration/**`, `deploy/helm/skopos/files/**` |
| Tempo and Loki differentials | `go`, `test/compat/**`, `test/corpus/**`, the baseline docs those tests read |
| cerberus three-way differential | `go`, `test/compat/cerberus/**`, `test/corpus/**` |
| S3 tiered storage and backup | `deploy/**`, `test/integration/s3/**` |
| Helm lint and render | `deploy/**` |
| Workflow policy | `.github/**`, `go` (the license check reads `go.mod` and `vendor/`) |
| Docker image build (new) | `Dockerfile`, `.dockerignore`, `cmd/**`, `go` |
| PR traceability and freeze guard, dependency-review, `changes`, `ci-gate` | always |

A docs-only PR therefore runs only the always-on jobs.

### 9.3 Safety nets

The fail-safe default, the coupling test (WF-04), the gate (WF-06), the trigger lint (WF-08) and the nightly full run on `development` together mean a wrong path rule can delay a failure by at most a day, not hide it.

### 9.4 Micro-tickets

Same template as before. All seven test gates apply; pure CI YAML changes use the `red-first-exempt` label and must link evidence from a throwaway PR.

**WF-01 Read the live rules and record the CI contract (spike, half a day).** Blocked by: none.

- With a maintainer token, read the `main-protection` ruleset (id 26493931) and any ruleset on `development`. Record the required check names, the up-to-date setting and which bases each workflow must report on in `docs/ci-contract.md`.
- Done when: another person can reproduce the list from one `gh api` command shown in the document. No workflow changes.

**WF-02 CI triggers and concurrency (YAML only).** Blocked by: WF-01.

- Make `on:` a `pull_request` with `branches: [development, main]`, plus `workflow_dispatch` and a nightly `schedule`. Remove the bare `push`. Add `concurrency` with the group `ci-${{ github.event.pull_request.number || github.run_id }}` and `cancel-in-progress: true` (contrib's pattern), so PR runs cancel each other and nothing else is ever cancelled. Rewrite the header comment to state the new rule.
- Evidence: a throwaway PR shows one CI run per push; a push to a branch with no PR runs nothing; a superseded run is cancelled; a scheduled run is not.
- Edge and negative: required check names are unchanged; a promotion PR into `main` still runs the full matrix.

**WF-03 `repoctl changes` and `.github/ci-paths.json`.** Blocked by: RS-03, RS-04, WF-01.

- Reads the PR file list through `ghapi`, matches it against JSON path groups, writes `key=true|false` lines to `$GITHUB_OUTPUT` and a table to the step summary.
- Red tests: a table of at least 25 fixtures: a `.go` file sets `go`; a docs-only change sets nothing; `Dockerfile` sets docker; `deploy/helm/**` sets deploy; `.github/workflows/ci.yml` sets all; `.golangci.yml` sets all; the label `ci:full` sets all; an unknown top-level path sets all; base `main` sets all; a non-PR event sets all; zero files sets all; more than 3,000 files sets all; `test/compat/prometheus/go.mod` sets `go`.
- Edge and negative: renames count both paths; deletions count; an API error exits non-zero.

**WF-04 Path-coupling test.** Blocked by: WF-03.

- Red tests: `TestEveryReferencedPathHasATrigger` scans Go tests, shell scripts and the Makefile for string literals pointing into `docs/`, `issues/`, `deploy/` and `test/`, and asserts each is matched by a group that triggers a job. A fixture test that reads `docs/x.md`, which is in no group, fails.
- Edge and negative: globs; generated files; paths built from variables are flagged for review.

**WF-05 Gate the jobs by changed paths.** Blocked by: WF-02, WF-03, WF-04, WF-08.

- Add the `changes` job and give every existing job `needs: changes` plus the fail-safe `if`. Job names unchanged.
- Red tests: the WF-08 rule 'every job except `changes` and `ci-gate` has `needs: changes` and an `if` containing `!cancelled()` and `!= 'false'`' fails on today's file and passes after.
- Evidence: a docs-only PR runs only the always-on jobs; a Go-only PR skips Helm and storage; a Dockerfile-only PR runs the image build; a deliberately failing `changes` job makes everything run.

**WF-06 `repoctl ci-gate` and the `ci-gate` job.** Blocked by: WF-05.

- The job receives the `needs` results as JSON and applies the rule in section 9.2, step 5.
- Red tests: all success passes; one failure fails; `changes` failed fails; skipped with its group false passes; skipped with its group true fails; cancelled fails.

**WF-07 Move the required checks to `ci-gate` (human, ruleset change).** Blocked by: WF-06, and AX-52 if it has landed.

- Add `ci-gate` as required next to the old checks, verify on three PRs (docs-only, Go-only, Helm-only), then drop the per-job requirements. Update `docs/security/ruleset.json` in the same change.
- Done when: a docs-only PR merges with only `ci-gate` and the PR guard required, and renaming a job no longer needs a ruleset edit.

**WF-08 Workflow trigger policy lint.** Blocked by: RS-04 and decision 3 below.

- Rules for every workflow: no bare `push` (needs `branches` or `tags`); `pull_request` names `branches`; a workflow that runs on `pull_request` declares `concurrency`; no `paths` or `paths-ignore` under `on:` for workflows whose jobs may be required; `schedule` workflows also have `workflow_dispatch`; every job has `timeout-minutes`; top-level `permissions` is declared.
- Red tests: one violating fixture per rule fails with the rule's name; today's `ci.yml` fails the first three rules and passes the rest; `release.yml` (tags only) passes. Runs inside the `policy` job.
- Edge and negative: malformed YAML fails.

**WF-09 dependency-review and pr-guard.** Blocked by: WF-01, WF-08.

- dependency-review gets `branches: [development, main]` and `concurrency` and stays unconditional.
- Decide how a `development` to `main` promotion PR is treated by the guard. If it is exempt, implement that in Go inside `prcheck`.
- Red tests: base `main` with head `development` passes without a traceability reference; any other PR into `main` is still checked; base `development` is unchanged.

**WF-10 Docker image build job.** Blocked by: WF-05.

- `docker build` without push when `Dockerfile`, `.dockerignore`, `cmd/**` or Go files change, then list the exported filesystem and assert it contains `/gateway` and `/skopos-checkpoint`.
- Evidence: a throwaway PR with a broken `COPY` fails the job.

**WF-11 `docs/workflows.md`, generated.** Blocked by: WF-08.

- A table of every workflow with its triggers, jobs, which jobs are required and why, generated from the files; CI fails on a diff (STG-7).
- Red tests: a golden test for the generator; changing a trigger without regenerating fails.

### 9.5 Sequencing and decisions

Start with WF-01, then WF-02: it needs no Go code and removes the duplicate runs and the stale ones immediately. WF-03 to WF-07 need `repoctl` and the shared GitHub client (RS-03, RS-04), so they follow those. WF-08 can run in parallel with WF-03. WF-09 to WF-11 can come any time after their blockers. The closest exit criterion is EC6 (strict gates stay trustworthy and fast); if you judge it does not serve one, rule 3 of the alpha plan says it waits, except WF-01 and WF-02, which cost almost nothing.

Decisions to confirm:

1. **Push runs.** Recommended: no `push` trigger and a nightly full run instead, which is what you asked for. Contrib also runs on pushes to `main` and on `merge_group`. The alternative is the same for `development` and `main`, which doubles CI cost per merge but catches two PRs that conflict semantically.
2. **Promotion PRs.** Recommended: full matrix, with the PR guard exempting `development` into `main` (WF-09).
3. **A YAML parser for WF-08.** No YAML library is vendored or in `go.mod`. Either add one (for example `gopkg.in/yaml.v3`) with a `docs/dependencies.md` entry and your approval, or limit the lint to grep rules like the existing 'blocked workflow patterns' step. I recommend the parser, since the repo's own policy puts parsing in Go.
4. **A single required `ci-gate`** (recommended) or keep the per-job names required.
5. **Docker build job.** Recommended: add it, since the gap is real.
