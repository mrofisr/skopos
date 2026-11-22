# Go code audit (core Skopos function vs tooling)

- Audit date: 2026-11-22
- Repository state: branch `beta/b04-100k-instant-path`, change
  `go-code-core-audit`
- Status: **inventory for maintainer review**. This document proposes removals;
  it does not perform them.

## 1. Scope and method

All figures come from the commands below, run from the repository root. They are
reproducible; re-run them and compare with the totals in §3.

```sh
find . -name go.mod -not -path './.git/*' | sort
go list ./... | sort
go list -deps ./cmd/skopos | grep '^github.com/mrofisr/skopos/' | sort
go list -tags=integration ./... | sort
find . -name '*.go' -not -path './.git/*' \
  -not -path './internal/tools/*' -not -path './test/compat/prometheus/*' \
  -not -path './test/e2e/obi/app/*' -not -path './internal/archtest/testdata/*' \
  | wc -l                       # files in the root module
find . -name '*.go' ... -exec cat {} + | wc -l    # lines in the root module
grep -rn 'repoctl' Makefile .github/workflows scripts docs .agents
```

Line counts include tests and comments; they measure the size of the surface,
not implementation complexity. Build-tagged packages are called out separately
because plain `go list ./...` omits them.

## 2. What counts as "core"

The maintainer asked to audit before defining "core". The tables use this
working definition:

> **Core Skopos function** is the set of packages in the import closure of
> `./cmd/skopos`, computed by `go list -deps ./cmd/skopos`. Everything else is
> tooling or verification.

Three alternative definitions and their consequences:

| Definition | Consequence |
| --- | --- |
| Shipped binary only | Keeps 23 packages; deletes the layout gate, all compatibility corpora, and the 7 evidence gates. CI loses every required check that is implemented in Go. |
| Binary + black-box tests | Keeps the 23 runtime packages plus compatibility/corpus/integration harnesses; still deletes `repoctl`, which owns 13 of the 18 required checks. |
| Binary + evidence gates | Keeps tests and the gates that enforce alpha/beta exit criteria; nearly all of `repoctl` stays because `evidence`, `exit-check`, `coverage`, `mutation`, `prcheck`, `ruleset`, `workflow-lint`, and `workflows-doc` are all gate implementations. |

The working definition is the narrowest one; §8 asks the maintainer to choose
before any deletion is proposed as a follow-up.

## 3. Totals

| Area | Files | Lines | Notes |
| --- | ---: | ---: | --- |
| Core runtime (23 packages: `cmd/skopos` + 22 `internal/`) | 168 | 41,759 | In the import closure |
| `internal/archtest` | 2 | 532 | Non-runtime `internal/` package |
| Root-module test harnesses | 34 | 7,556 | 10 directories, incl. build-tagged checks |
| `tools/repoctl` (main + 21 internal packages) | 83 | 19,537 | 18 registered commands |
| `internal/archtest/testdata` fixtures | 7 | 74 | Fixture code, not compiled into any product |
| Separate module: `test/compat/prometheus` | 7 | 1,558 | promqltest adapter + compliance report |
| Separate module: `test/e2e/obi/app` | 1 | 49 | Fake OBI app |
| Separate module: `internal/tools` | 0 | 0 | `go.mod` only; pins third-party dev binaries |
| **Total** | **302** | **71,065** | Reconciles with `find . -name '*.go' | wc -l` |

## 4. Role inventory

### 4.1 Core runtime — keep (23 packages, 168 files, 41,759 lines)

| Package | Files | Lines |
| --- | ---: | ---: |
| `cmd/skopos` | 1 | 19 |
| `internal/admission` | 3 | 592 |
| `internal/app` | 2 | 229 |
| `internal/app/checkpoint` | 4 | 948 |
| `internal/app/gateway` | 19 | 2,064 |
| `internal/app/mcp` | 5 | 1,411 |
| `internal/appenv` | 2 | 141 |
| `internal/auth` | 2 | 173 |
| `internal/chclient` | 7 | 2,083 |
| `internal/checkpoint` | 15 | 3,255 |
| `internal/config` | 3 | 1,074 |
| `internal/frontend` | 4 | 1,532 |
| `internal/httpapi` | 6 | 1,329 |
| `internal/loki` | 18 | 5,203 |
| `internal/metrics` | 9 | 1,194 |
| `internal/plan` | 3 | 717 |
| `internal/prom` | 27 | 8,984 |
| `internal/semctx` | 5 | 2,564 |
| `internal/sqlgen` | 8 | 1,524 |
| `internal/tempo` | 21 | 6,097 |
| `internal/ttlcache` | 3 | 619 |
| `internal/version` | 1 | 7 |

`internal/prom`, `internal/tempo`, and `internal/loki` together account for
20,284 lines — about half of the runtime closure. These are the query heads,
not tooling.

### 4.2 Product-support — unsettled (1 package, 2 files, 532 lines)

| Package | Files | Lines | Consumer |
| --- | ---: | ---: | --- |
| `internal/archtest` | 2 | 532 | `go test ./...`, `scripts/quality/report.sh:18`, EC6 |

It is not imported by the binary. It enforces the layout rules from AGENTS.md §3
and §4: one `cmd/` binary, no repository tooling under `internal/`, no forbidden
package names, no Python, shells under `scripts/`, and small `main` packages.
Deleting it removes the enforcement of the layout ADR; keeping it as "product
code" mislabels its role. Classified **unsettled** pending §8.

### 4.3 Required verification and lab harnesses (41 files, 9,114 lines)

This group spans the root module (34 files / 7,556 lines, including three
build-tagged packages) and the separate promqltest module (7 files / 1,558
lines).

| Package | Files | Lines | Build tag | Concrete consumer |
| --- | ---: | ---: | --- | --- |
| `test/bench` | 7 | 1,887 | — | `scripts/bench/run.sh`, `make bench`, `make bench-gate`, EC3 |
| `test/bench/gate-report` | 2 | 188 | — | Manual generator for the `docs/benchmarks.md` gate block |
| `test/compat/cerberus/seed` | 2 | 795 | — | `scripts/compat/all.sh`, `ci.yml:257`, EC2 |
| `test/compat/tempo` | 1 | 856 | `TEMPO_COMPAT=1` env | `scripts/compat/tempo.sh`, EC2 |
| `test/corpus/traceql` | 2 | 654 | — | `go test ./...`, EC2 (`ci.yml` via `make compat-all`) |
| `test/e2e/obi` | 1 | 61 | — | `make e2e-obi`, `scripts/e2e/obi/run.sh` |
| `test/e2e/obi/seedgen` | 2 | 510 | — | `scripts/e2e/obi/grafana.sh:132` |
| `test/helm` | 1 | 79 | — | `go test ./...`; Helm value/env mapping checks |
| `test/integration/faults` | 1 | 184 | — | `make test-faults`, `ci.yml:289` |
| `test/integration/migrate` | 1 | 422 | `integration` | `make test-integration` |
| `test/scale` | 8 | 1,001 | — | `test/scale/cmd/scale-gen` for lab dataset generation |
| `test/scale/cmd/scale-gen` | 1 | 19 | — | Lab dataset generation (seed 42, hash `b900affe5ce4b9ec`) |
| `test/supportmatrix` | 2 | 475 | — | `ci.yml:777` (`--check`); generates `docs/support-matrix.md` |
| `test/compat/prometheus` (separate module) | 3 | 821 | — | `scripts/compat/all.sh`, `make compat-all`, EC2 |
| `test/compat/prometheus/compliance` (same module) | 2 | 245 | — | `scripts/compat/prometheus-compliance.sh` |
| `test/compat/prometheus/compliance/cmd/compliance-report` (same module) | 2 | 492 | — | Classifies compliance results; exits 1 on unexpected failures |
| `test/compat/loki/upstream` | 1 | 47 | `skopos_upstream` | `scripts/compat/loki-upstream.sh` (copied into Loki tree) |
| `test/buildtargets` | 1 | 135 | `buildcheck` | `ci.yml:153` cross-compile verification |
| `test/e2e/kwok` | 1 | 243 | `e2e` | `ci.yml:682` install/upgrade against KWOK |

The build-tagged packages are invisible to plain `go list ./...` but are
explicitly called by CI or compatibility scripts. They must not be classified
as dead code on the basis of a plain package listing.

### 4.4 Repository tooling — `tools/repoctl` (83 files, 19,537 lines)

| Package | Files | Lines | Command |
| --- | ---: | ---: | --- |
| `tools/repoctl` (main) | 2 | 67 | binary + command registry |
| `internal/benchgate` | 3 | 693 | `bench-gate` |
| `internal/changes` | 5 | 1,372 | `changes` |
| `internal/cigate` | 2 | 414 | `ci-gate` |
| `internal/codescanning` | 4 | 557 | `codescan` |
| `internal/command` | 2 | 408 | shared `Run` contract, registry, exit codes |
| `internal/evidence` | 3 | 355 | `evidence` |
| `internal/exitcheck` | 2 | 474 | `exit-check` |
| `internal/ghapi` | 2 | 648 | single GitHub client (internal dependency) |
| `internal/grafanacheck` | 13 | 3,918 | `grafana-check` |
| `internal/issueops` | 12 | 2,685 | `issues` |
| `internal/mutation` | 2 | 466 | `mutation` |
| `internal/osvexceptions` | 4 | 440 | `osv` |
| `internal/prcheck` | 5 | 1,496 | `prcheck` |
| `internal/quality` | 4 | 863 | `coverage` |
| `internal/quickstart` | 4 | 824 | `quickstart` |
| `internal/releaseverify` | 4 | 853 | `release-verify` |
| `internal/ruleset` | 4 | 1,478 | `ruleset` |
| `internal/sbom` | 2 | 500 | `sbom` |
| `internal/workflowlint` | 2 | 620 | `workflow-lint` |
| `internal/workflowsdoc` | 2 | 406 | `workflows-doc` |

The two largest commands are `grafana-check` (3,918 lines) and `issueops`
(2,685 lines); together they are a third of the tool. The smallest is
`evidence` (355 lines) but it is a dependency of every EC gate.

### 4.5 External/dev tooling

| Location | Files | Lines | Purpose |
| --- | ---: | ---: | --- |
| `internal/tools/go.mod` | 0 | 0 | Separate module pinning gremlins, golangci-lint, cosign, syft, etc. (RS-39) |
| `test/e2e/obi/app/go.mod` | 1 | 49 | Fake OBI app for the end-to-end harness |
| `docs/security/ruleset.json` | — | — | Checked-in ruleset snapshot consumed by `repoctl ruleset` |
| `test/quality/{coverage-floor.json,coverage-renames.json,layout-violations.txt,mutation-allowlist.json}` | — | — | Ratchets consumed by `coverage`, `mutation`, `archtest` |
| `test/quality/bench/*.json` | — | — | Baselines consumed by `repoctl bench-gate` |
| `test/quality/evidence/ec-*.json` | — | — | EC1–EC7 evidence written by `repoctl evidence` |

Third-party binaries are pinned in `internal/tools` and invoked through make
targets; AGENTS.md §4.13 forbids inline `go install tool@version` in workflows.
Any tool removal must preserve that pinning rule.

## 5. `repoctl` command → consumer → breakage map

| Command | Concrete consumer | What breaks if removed |
| --- | --- | --- |
| `bench-gate` | `test/quality/bench/README.md:26`, ADR 0009, B-04 report. **No Makefile or CI call site found.** | Documented relative regression gate procedure; 4 checked-in baselines lose their verifier. Marked **unused — verify**; confirm no human runbook depends on it. |
| `changes` | `.github/workflows/ci.yml` (`changes` job) | Path→group mapping that decides which jobs run; every other job consumes its outputs, so removing it breaks all CI gating. |
| `ci-gate` | `.github/workflows/ci.yml:826` | Aggregate `CI gate` required check. The live `main-protection` ruleset requires per-job names until WF-07 flips, so this cannot be dropped without a ruleset change. |
| `codescan` | `.github/workflows/release.yml:102`, `.agents/skills/{release-verify,security-review}/SKILL.md`, `docs/release.md` | Release preflight fails closed on high code-scanning alerts. Replacement would be inline `gh api` + `jq`, which AGENTS.md §4.5 forbids for pass/fail decisions. |
| `coverage` | `Makefile:188–198` (`coverage`, `coverage-report`, `coverage-write`), `scripts/quality/report.sh:12` | Per-package coverage floor and rename ratchet in `test/quality/coverage-{floor,renames}.json`. No shell replacement without reimplementing JSON comparison in Go. |
| `evidence` | `Makefile:111,116,119,124,130,144,167` (EC1–EC7) | All `test/quality/evidence/ec-*.json` files and `docs/alpha-exit-report.md`; `make alpha-exit-check` has no input to aggregate. |
| `exit-check` | `Makefile:160–168`, `docs/quality.md`, `docs/alpha-exit-plan.md` | The 7/7 PASS alpha exit gate and scope checks (no `phase:beta` issue on frozen paths, no stale manifest). |
| `grafana-check` | `scripts/e2e/obi/grafana.sh:179`, `test/e2e/obi/README.md` | EC1 evidence: Grafana datasource health, Loki/Tempo journeys, and the OBI RED dashboard check. |
| `issues` | `Makefile:164,165,200–213`, `docs/agents/issue-tracker.md` | Alpha-exit manifest staleness check, live issue validation, label sync, and the PR traceability issue data. |
| `mutation` | `Makefile:154–174`, `.github/workflows/mutation.yml` (nightly) | Mutation efficacy floor of 70% and `test/quality/mutation-allowlist.json`; report generation for in-scope packages. |
| `osv` | `.github/workflows/release.yml:105`, `docs/release.md`, skills | Dated OSV exception ledger validation; release preflight would stop checking unexpired exceptions. |
| `prcheck` | `.github/workflows/pr-guard.yml:48`, `scripts/quality/report.sh:25`, `docs/agents/issue-tracker.md` | PR traceability (open issue reference) and the alpha-exit freeze guard; also accepts `phase:beta` for this beta line. |
| `quickstart` | `scripts/quickstart/quickstart-test.sh:58`, `scripts/release/evidence.sh:18`, `Makefile:135` | README quickstart doc-test and EC5 release evidence. |
| `release-verify` | `scripts/release/verify-release.sh:48`, `Makefile:182`, `docs/release.md`, skills | Signature, attestation, SBOM, digest, and chart render verification for published releases. |
| `ruleset` | `Makefile:100,166,216,218`, `scripts/quality/report.sh:22`, `ci.yml` via `make ruleset-check` | Checked-in `docs/security/ruleset.json` vs workflow job consistency; part of `make check`. |
| `sbom` | `scripts/release/release.sh:92` | CycloneDX fallback inventory when syft cannot produce an SBOM during release. |
| `workflow-lint` | `.github/workflows/ci.yml:773`, `scripts/quality/report.sh:21` | Trigger/concurrency/timeout/permissions policy (AGENTS.md §7). |
| `workflows-doc` | `.github/workflows/ci.yml:775`, `docs/workflows.md`, `docs/style-guide.md` | Generated workflow documentation and `--check`; CI fails on a stale doc. |

## 6. Import direction

- Product code never imports `tools/repoctl/internal/...`; `internal/archtest`
  enforces this with the `repoctl-import` rule and an empty layout baseline.
- `repoctl` may import product packages only if named in the archtest allowlist;
  the rule exists because `internal/config` is currently imported by tooling.
- The shared `command` and `ghapi` packages are dependencies of other `repoctl`
  commands, not standalone tools. Removing a single command does not make them
  removable; removing all commands does.
- `test/*` packages import product packages but are never imported by them, so
  the dependency direction is one-way from harness to product.

## 7. Removal tiers (proposals only)

| Tier | Candidates | Prerequisite before removal | Risk |
| --- | --- | --- | --- |
| 0 — stale/unused | `repoctl bench-gate` | Confirm with maintainer that no human runbook invokes it; update ADR 0009 and `test/quality/bench/README.md` to point at `repoctl bench-gate` replacement or drop the gate explicitly | Low (no CI call site) |
| 1 — drop-in replacement | `sbom`, `quickstart`, `grafana-check` | Replace with documented shell/curl or existing CI steps; preserve EC1/EC5 evidence and the pinned-tool rule | Medium |
| 2 — gate replacement required | `codescan`, `osv`, `ruleset`, `workflow-lint`, `workflows-doc`, `changes`, `ci-gate`, `coverage`, `mutation`, `release-verify`, `evidence`, `exit-check`, `prcheck`, `issues` | For each: a Go test or equivalent gate in the product/test tree, plus changes to workflows, Makefile, docs, ratchets, and archtest rules in the same PR | High |
| 3 — product-adjacent verification | `internal/archtest`, `test/compat/*`, `test/corpus/*`, `test/integration/*`, `test/supportmatrix` | Not recommended under any of the three "core" definitions; would require dropping documented EC2 evidence and layout enforcement | Very high |

Recommended order: Tier 0 after sign-off, then Tier 1 one command per PR, then
Tier 2 only with an explicit decision that the replaced gate is no longer
required. Tier 3 is listed for completeness, not proposed for removal.

## 8. Review decisions (maintainer sign-off required)

1. **Definition of core.** Choose one of the three definitions in §2. The
   tables use the narrow one (runtime import closure).
2. **EC1–EC7 evidence.** Must `repoctl evidence` and `exit-check` survive in
   another form, or do the beta gates accept direct CI results? Affects
   `evidence`, `exit-check`, and `docs/alpha-exit-report.md`.
3. **Compatibility harnesses.** Are `test/compat/*`, `test/corpus/*`, and
   `test/integration/*` required verification for the beta line (EC2) or
   optional lab tooling? Affects Tier 3 and the 212-case oracle claim in
   `docs/beta-gates.md`.
4. **`internal/archtest`.** Keep it as a Go test, or fold the layout rules into
   a build-time check? Affects §4.2 and the `repoctl-import` rule.
5. **Unused `bench-gate`.** Confirm whether the documented `repoctl bench-gate`
   command has a human consumer before treating it as Tier 0.

No item in this section was resolved by this change; that was the purpose of the
audit. Maintainer sign-off on the inventory was recorded as "LGTM" on PR #66
(2026-11-22). The five questions remain open for the follow-up removal changes;
no code has been deleted under this change.
