---
name: support-matrix
description: >-
  Update the public support matrix and the per-language support pages when a
  query feature, endpoint, deployment mode, or MCP capability changes. Use when
  behavior is added, removed, or explicitly declared unsupported.
license: Apache-2.0
---

# Support matrix

Keep the public claim and the tested behavior in step. The support matrix is a
contract: anything not listed must return an explicit error, not an empty or
incorrect result.

## When to use

- A query function, operator, parser stage, endpoint, or signal is added or
  removed.
- A deployment mode, chart capability, or MCP tool changes.
- A behavior becomes explicitly unsupported, or an unsupported behavior becomes
  supported.
- A reference version changes and the supported subset needs re-stating.

Do not use this skill for a pure refactor that does not change observable
behavior. Say so in the pull request instead.

## Inputs

- The user-visible change and its compatibility impact.
- The test or corpus case that proves it.
- The current row in the support matrix and the relevant detail page.

## Files

| File | What it claims |
| --- | --- |
| `docs/support-matrix.md` | Public inventory of signals, endpoints, modes, and language subsets |
| `docs/promql-support.md` | PromQL functions, operators, and known gaps |
| `docs/traceql-support.md` | TraceQL subset, Tempo endpoints, and wire formats |
| `docs/logql-support.md` | LogQL subset, parser stages, and pushdown behavior |
| `docs/ai-context.md` | Context API and MCP tools |
| `README.md` | Status table and the short user-facing claim |
| `docs/beta-gates.md` | Evidence and remaining beta work, when a gate moved |

## Workflow

1. Classify the change:
   - **new supported behavior**: add the claim and a test;
   - **changed supported behavior**: update the claim and the expected result;
   - **explicitly unsupported**: add the error behavior and the reason;
   - **deployment or MCP capability**: update the mode and tool tables.
2. Find every place the old claim appears. Search the docs and README for the
   function, endpoint, stage, or mode name.
3. Update the matrix row and the detail page. Keep the text narrow: name the
   reference version, the subset, and the known gaps. Do not imply full
   compliance.
4. Add or update the proof: a corpus case, a unit test, an integration test, or a
   chart render test. If there is no proof, do not make the claim.
5. Run the focused test, then the relevant gate:
   - language surface: `make compat-all`;
   - metrics or `/metrics`: `make metrics-contract`;
   - chart or deployment mode: `make helm-lint` and `make helm-template`;
   - quality or alpha evidence: `make quality-report`.
6. Run `make check` before opening the pull request.
7. Link the implementation pull request to the proposal or issue that motivated
   the claim.

## Verification

```sh
# The claim must have a test. Run the narrowest one first.
go test ./internal/<head>/... -run '<TestName>' -count=1

# Then the gate that covers the claim.
make compat-all
make metrics-contract
make helm-lint
make helm-template
make check
```

A pass means the claim, the test, and the support page agree. If the test proves
only a subset, narrow the page rather than broadening the test.

## Guardrails

- Never list a feature without a test that fails when the feature regresses.
- Never claim full PromQL, LogQL, or TraceQL compliance. State the reference
  version and the supported subset.
- Keep the alpha status and the known limitations visible.
- Do not silently remove a limitation. If the limitation is fixed, say so and
  point at the proof.
- Update the README status table when the user-visible status changes.

## References

- `docs/support-matrix.md`
- `docs/promql-support.md`, `docs/traceql-support.md`, `docs/logql-support.md`
- `test/corpus/README.md`
- `CONTRIBUTING.md` working rule 4: do not guess; record unknowns.
