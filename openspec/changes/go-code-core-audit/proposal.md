# Proposal

## Why

The repository ships one binary but carries a second Go program, `tools/repoctl`,
with 18 repository-check commands, 83 Go files, and about 19,500 lines of
tooling,
plus separate test harness modules and a pinned dev-tools module. Removing that
code would delete CI gates, evidence records, and quality ratchets, so the
removal target and its consequences must be established before any deletion.

## What Changes

- Add `docs/go-code-audit.md`: a complete inventory of Go code in the
  repository, grouped by role rather than by directory.
- Classify every Go package as one of: **core runtime** (imported by
  `cmd/skopos`), **product-support** (product code that is not on the runtime
  import path), **required verification** (tests and harnesses that enforce a
  documented gate), **repository tooling** (checks, generators, evidence), or
  **external/dev tooling** (separate modules and pinned tools).
- Record for each package: file count, approximate lines, import direction, and
  the concrete Makefile target, CI workflow, script, or document that consumes
  it.
- Map all 18 `repoctl` commands to their consumers and state exactly which
  gates, generated files, and evidence artifacts would break if the command
  were removed.
- List removal candidates in tiers with risk, prerequisite replacements, and a
  recommended order. This change only produces the inventory and the
  recommendation; it does **not** delete code or alter CI.
- Record the agreed definition of "core Skopos function" used for the
  classification, including the cases where the answer is not yet settled.

## Capabilities

### New Capabilities
None. This is a documentation and audit change with no externally observable
system behavior; the change declares `skip_specs: true`.

### Modified Capabilities
None.

## Impact

- New file: `docs/go-code-audit.md`; the audit is the review artifact.
- No Go source, Makefile, workflow, script, or generated document changes.
- No API, schema, CLI, or deployment behavior changes.
- Follow-up changes that actually remove code are proposed, not implemented,
  and require their own issue and PR.
