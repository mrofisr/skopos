# Tasks

## 1. Inventory collection

- [x] 1.1 Enumerate all Go modules, packages, files, and lines with reproducible commands (`find . -name go.mod`, `go list ./...`, `find -name '*.go' | wc -l`, `find -exec cat | wc -l`); verify completion by recording the commands, date, and totals in `docs/go-code-audit.md`
- [x] 1.2 Compute the runtime import closure with `go list -deps ./cmd/skopos` and list every module package outside it; verify the core set contains 23 packages and that `internal/archtest` is the only non-runtime `internal/` package
- [x] 1.3 Inventory the four `go.mod` roots (root, `internal/tools`, `test/compat/prometheus`, `test/e2e/obi/app`) and their purpose; verify each root appears in the audit with its own file/line count
- [x] 1.4 Search Makefile, `.github/workflows/`, `scripts/`, `.agents/`, and docs for `repoctl` and test-harness invocations; verify each consumer is recorded as a concrete target, job, script path, or document

## 2. Classification

- [x] 2.1 Define "core Skopos function" as the `cmd/skopos` runtime import closure and record the alternative definitions with their consequences; verify the definition is stated verbatim in the audit and marked as the one used for the tables
- [x] 2.2 Assign every Go package one of the five roles (core runtime, product-support, required verification, repository tooling, external/dev tooling); verify the package count in the role tables equals the total from 1.1
- [x] 2.3 Mark packages that fit two roles or whose gate status is unclear as **unsettled** with the reason and the decision needed; verify no unsettled case is silently classified, including `internal/archtest`, `test/compat/*`, `test/corpus/*`, and `test/integration/*`
- [x] 2.4 Map all 18 `repoctl` commands (from `tools/repoctl/commands.go`) to their consumers, generated files, and evidence artifacts; verify every registered command has a row and commands with no discovered consumer are labelled "unused — verify"

## 3. Audit document

- [x] 3.1 Write `docs/go-code-audit.md` with the inventory tables, role definitions, and per-package counts; verify the tables are sorted, totals reconcile, and the collection commands are included
- [x] 3.2 Document for each `repoctl` command what breaks on removal (gate, evidence file, generated doc, script, or archtest rule); verify the document names the exact Makefile targets and workflow jobs rather than generic "CI"
- [x] 3.3 Propose removal candidates in tiers with prerequisites and a recommended order, explicitly excluding deletions from this change; verify every tier names its prerequisite replacement and that no task here edits Go/CI code
- [x] 3.4 Add a "Review decisions" section listing the design open questions and the unsettled classifications for maintainer sign-off; verify each item has a concrete question and the options recorded in design.md

## 4. Verification and hand-off

- [x] 4.1 Re-run the collection commands from 1.1 and diff against the recorded totals; verify no discrepancy or explain it in the audit
- [x] 4.2 Run `openspec validate go-code-core-audit --strict`; verify zero errors
- [x] 4.3 Inspect `git diff --stat` and `git status`; verify the only new/changed tracked files are the four OpenSpec artifacts and `docs/go-code-audit.md` (no Go, Makefile, workflow, or script edits)
- [x] 4.4 Obtain maintainer review of the role assignments and unsettled cases; verify sign-off is recorded in the PR or issue before opening follow-up removal changes — LGTM recorded as a comment on PR #66 by the maintainer (2026-11-22); the five review questions stay open in the audit and no follow-up removal issues have been opened

## Workflow follow-up

- Open a `backlog:post-alpha` issue for each proposed tier after sign-off; implement each removal in its own PR with gate replacements, archtest updates, and generated-doc regeneration.
