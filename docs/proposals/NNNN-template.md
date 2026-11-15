# Proposal NNNN: Short, descriptive title

- **Status:** Draft
- **Author(s):** @handle
- **Date:** YYYY-MM-DD
- **Related issue:** #NNN
- **Related ADR:** `docs/adr/NNNN-*.md` or "none"
- **Supersedes:** `docs/proposals/NNNN-*.md` or "none"

## Summary

One paragraph. What changes for the user, and why now? A reader should be able
to decide whether to keep reading from this paragraph alone.

## Motivation

Describe the current state, the problem, and who is affected. Link to real
issues or user reports. If the problem is not demonstrated, the proposal is not
ready.

## Goals

- What this proposal must achieve.
- How we will know it succeeded.

## Non-goals

- What is explicitly out of scope.
- Which adjacent problem is left for a later proposal.

## User stories

- As an operator, I want <goal> so that <benefit>.
- As an AI SRE agent, I want <goal> so that <benefit>.
- As a contributor, I want <goal> so that <benefit>.

## Proposal

Describe the user-visible behavior first. Include the API, chart values, CLI
flags, or MCP tools that change. A reviewer should understand what ships without
reading the design section.

### API and compatibility

- New or changed endpoints, request fields, response fields, or error codes.
- New or changed Helm values, defaults, or deployment modes.
- What remains compatible, and what requires a migration.

### Design

Explain how it works at the system level: components, data flow, storage, and
failure modes. Include a diagram when it removes ambiguity. This is not a code
walkthrough.

### Security and privacy

- New trust boundaries, credentials, or network paths.
- What data becomes readable by which principal.
- The effect on tenant isolation, auth, TLS, and the support matrix.

### Observability

- Metrics, logs, traces, or alerts that prove the feature works.
- The cardinality impact of any new label.
- The user-visible status or error when the feature is unavailable.

## Test plan

- **Unit:** the packages and the exact seam.
- **Corpus or compatibility:** which case proves the behavior, and against which
  reference version.
- **Integration:** the real ClickHouse, chart, or container path.
- **E2E or deployment:** what only a cluster can prove.
- **Failure and boundary:** invalid input, empty result, limit, and timeout.

Commands should be exact:

```sh
go test ./internal/<package>/... -run '<TestName>' -count=1
make compat-all
make check
```

## Alternatives considered

For each alternative: what it is, how it would work, and why it was rejected.
Show that the obvious alternatives were evaluated.

## Graduation criteria

What must be true before this moves from alpha to beta, or from beta to stable?
If the change is low-risk and always-on, say so and explain why a gate is not
needed. If it is gated, name the gate and the default.

## Open questions

Number the questions that need a decision before implementation. Remove this
section when every question is resolved.

## Implementation history

- YYYY-MM-DD: proposal created.
- YYYY-MM-DD: accepted; implementation started in #NNN.
- YYYY-MM-DD: implemented; support matrix updated.
