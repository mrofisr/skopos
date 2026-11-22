# Design

## Context

See proposal.md — Why for motivation. The observed shape that drives the audit:

- `cmd/skopos` is the only shipped binary and imports 23 module packages under
  `internal/`. Those are the runtime closure.
- `internal/archtest` is the only package under `internal/` that is not in that
  closure; it enforces repository layout rules.
- `tools/repoctl` registers 18 commands (`bench-gate`, `changes`, `ci-gate`,
  `codescan`, `coverage`, `evidence`, `exit-check`, `grafana-check`, `issues`,
  `mutation`, `osv`, `prcheck`, `quickstart`, `release-verify`, `ruleset`,
  `sbom`, `workflow-lint`, `workflows-doc`) across 83 Go files (~19,500 lines).
- Go code also lives in four modules: the root module, `internal/tools`
  (pinned dev tools), `test/compat/prometheus` (promqltest harness), and
  `test/e2e/obi/app` (fake OBI app).
- `repoctl` is referenced by Makefile targets, `.github/workflows/ci.yml`, and
  scripts under `scripts/`. `internal/archtest` contains rules that refer to
  `tools/repoctl` by path, so deleting the tool without updating those rules
  and the layout baseline would break `go test ./...`.
- The three existing main specs (`instant-rate-pushdown`, `metrics-storage`,
  `query-admission`) describe product behavior only; none covers repository
  tooling, which is why this change skips specs.

## Goals / Non-Goals

**Goals:**

- Produce a reproducible, reviewable inventory of every Go package and its
  consumers.
- Define "core Skopos function" explicitly and apply the same definition to
  every package.
- Make the cost of removing `repoctl` visible: which gates, evidence files,
  generated docs, and scripts depend on each command.
- Give maintainers a tiered removal sequence with prerequisites, not a
  one-shot deletion.

**Non-Goals:**

- No Go source, Makefile, workflow, script, or generated-document edits.
- No deletion of `tools/repoctl`, tests, or modules in this change.
- No new generator binary or new CI check; the audit is a checked-in document.
- No ADR deciding the final shape of repository tooling; that follows the
  review of this inventory.

## Decisions

### D1: Classify by import direction and consumer, not by directory name

A package is **core runtime** when it is in the import closure of
`./cmd/skopos`, computed with `go list -deps ./cmd/skopos`. Directory names are
not evidence: `internal/archtest` looks like product code by location but is
not imported by the binary, while some `test/` packages are required gates.
Everything outside the runtime closure is assigned a role from its consumer:
Makefile target, workflow job, script, generated document, or test invocation.

- **Alternative considered:** classify by folder (`internal/` = core,
  `test/`/`tools/` = disposable) — rejected because it mislabels the layout
  gate and the compatibility harnesses that enforce documented beta gates.

### D2: Checked document, no generator

The inventory is a single Markdown file produced once from `go list`, `find`,
and grep of consumers, with the exact commands and the audit date recorded in
the document. A generator would add another piece of repository tooling to a
change that is investigating how much tooling can be removed.

- **Alternative considered:** commit a `repoctl inventory` command — rejected:
  it expands the tool whose removal is under review.

### D3: Five explicit roles, with unresolved cases called out

| Role | Definition |
| --- | --- |
| Core runtime | In the `cmd/skopos` import closure |
| Product-support | Product-adjacent code outside the closure (currently `internal/archtest`) |
| Required verification | Tests or harnesses named by a documented gate (`make check`, `compat-all`, `test-integration`, EC1–EC7 evidence) |
| Repository tooling | `repoctl` commands, issue/quality/evidence automation, doc generators |
| External/dev tooling | Separate modules (`internal/tools`, promqltest, OBI fake app) and pinned third-party binaries |

Where a package fits more than one role, the audit records both and marks the
classification **unsettled** rather than picking one silently. The user's
answer to the core-definition question was "audit first", so the document
presents the possible definitions and their consequences instead of choosing.

### D4: Consumer mapping before removal tiers

Each `repoctl` command is mapped to its concrete call sites (Makefile target,
workflow step, script, and generated artifact) before any tier is proposed. A
command with no discovered consumer is marked "unused — verify", not deleted.
Removal tiers are ordered by prerequisite: documentation/stale items first,
then commands with a drop-in replacement, then commands whose gate must be
replaced, and finally product-adjacent verification.

## Risks / Trade-offs

- [The inventory can go stale as code changes] → record the audit date, the
  exact collection commands, and package-level counts so a reviewer can
  regenerate and diff it.
- [A consumer can be missed (dynamic invocation, docs only, human runbook)] →
  search Makefile, workflows, scripts, docs, and Go imports; mark every
  uncertain mapping explicitly and ask the maintainer to confirm.
- [Classifying a gate as removable could silently weaken a beta/alpha exit
  criterion] → tier ordering requires an explicit replacement before removal
  and forbids dropping a documented gate in this change.
- [Counting only `*.go` under the root module misses separate modules] → the
  audit enumerates all four `go.mod` roots.
- [The word "core" means different things to different maintainers] → the
  document states the runtime-closure definition used for the tables and
  records the alternative definitions with their consequences.

## Migration Plan

1. Run the collection commands, write `docs/go-code-audit.md`, and have the
   maintainer review the role assignments and unsettled cases.
2. Open follow-up issues per tier, with the gate replacements named explicitly.
3. Implement removals in separate changes; update `internal/archtest` rules,
   `test/quality/layout-violations.txt`, Makefile, workflows, and scripts in
   the same PR that removes a referenced command.
4. Rollback for this change is reverting the audit document; no product or CI
   state changes.

## Open Questions

- Must the EC1–EC7 evidence mechanism survive in another form, or do the beta
  gates accept direct CI results?
- Are the compatibility harnesses (`test/compat`, `test/corpus`,
  `test/integration`) required verification for the beta line, or optional
  lab tooling?
- Should `internal/archtest` remain as a Go test or be folded into a build-time
  layout check?
