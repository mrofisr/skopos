# Feature proposals

A proposal explains a change before it is implemented. It is deliberately
lighter than a Kubernetes KEP: one to three pages, enough for a reviewer to
agree on the problem, the user-visible behavior, the non-goals, and the proof.

## When a proposal is required

Write a proposal when a change affects any of:

- the public query APIs, their semantics, or the support matrix;
- the Helm values, deployment modes, or resource/security posture;
- the stored schema, retention, or storage compatibility;
- the MCP tools, the context API, or the data an agent can read;
- the release, signing, or supply-chain process.

A proposal is not required for a bug fix, an internal refactor, a dependency
bump, a docs-only change, or a change fully covered by an existing ADR. When in
doubt, open an issue and ask.

## Proposal or ADR?

| Artifact | Answers | When it lands |
| --- | --- | --- |
| Proposal | Should we do this, and what will it look like? | Before implementation |
| ADR | Which decision did we make, and why? | When the decision is accepted |
| Support matrix | What is actually implemented and tested? | With the implementation |

A proposal may result in an ADR. The implementation pull request updates the
support matrix and links both.

## Lifecycle

| Status | Meaning |
| --- | --- |
| `Draft` | Under discussion; not agreed |
| `Review` | Ready for maintainer review; scope is stable |
| `Accepted` | Agreed, implementation may start |
| `Implemented` | The behavior is in the tree and tested |
| `Rejected` | Not doing it; the reason stays in the document |
| `Superseded` | Replaced by a newer proposal; link the replacement |

Change the status in the same pull request as the event. Do not delete a
rejected or superseded proposal; it is part of the project history.

## Naming

Use `NNNN-short-descriptive-title.md`, where `NNNN` is the next four-digit
number. Check the directory before choosing:

```sh
ls docs/proposals
```

A proposal that spans releases keeps its number. A follow-up that changes the
design is a new proposal that links the old one.

## Review

- A proposal is a documentation pull request. The title starts with `docs:`.
- Keep the scope to one problem. Split a proposal that has two independent
  decisions.
- Security-sensitive proposals need the human review gates from
  `CONTRIBUTING.md`.
- Link the implementation issue or pull request when work starts.
- If the implementation diverges, update the proposal or record the divergence
  in an ADR. Do not leave an accepted proposal describing code that does not
  exist.

## Template

Copy [NNNN-template.md](NNNN-template.md) and fill it in. Delete the guidance
comments before the proposal moves to `Review`.
