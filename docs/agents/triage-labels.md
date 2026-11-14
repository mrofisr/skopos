# Triage labels

The engineering skills speak in terms of five canonical triage roles. This table
maps each role to the label, or close reason, this repo uses. It creates no new
labels.

| Role in mattpocock/skills | This repo | Meaning |
| --- | --- | --- |
| `needs-triage` | not used | Work enters as a plan entry or a maintainer decision, so it is already triaged when it is filed. |
| `needs-info` | not used | Ask in a comment instead of labelling; the plan is the place for scope. |
| `ready-for-agent` | `ready-for-agent` | Unblocked, fully specified, and completable by an agent without a human action. |
| `ready-for-human` | `owner:human` | Needs a maintainer or an external action. The plan heading marker `(owner:human)` sets it. |
| `wontfix` | close as not planned | Post a comment with the reason, then `gh issue close --reason not planned`. |

## Rules

- `ready-for-agent` goes only on an open ticket that is unblocked, in scope for
  the current phase, and not labelled `owner:human` or `backlog:post-alpha`.
- `owner:human` marks a ticket whose next step is a human or external action. It
  never carries `ready-for-agent`.
- Tickets in `backlog:post-alpha` wait for AX-61 and carry no triage label until
  they are promoted.
- The triage roles are labels on the issue; closing an issue is not a triage
  state for work that is still wanted.
