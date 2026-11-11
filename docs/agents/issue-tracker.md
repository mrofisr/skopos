# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues in `jawaracloud/skopos`.
Use the `gh` CLI for all operations. The alpha-exit backlog has one extra layer:
the plan is the source of truth and `repoctl issues` keeps GitHub in step with
it.

## Ticket bodies

Every AX ticket body is generated from its plan entry and uses the
[mattpocock to-tickets](https://github.com/mattpocock/skills) issue template:

```markdown
<!-- skopos-issue: AX-62 -->

## Parent

#37

## What to build

- What the ticket makes work, from the user's perspective.

## Acceptance criteria

- [ ] Red tests: `TestName`.
- [ ] Edge and negative: the boundary and failure cases.
- [ ] Done when: the observable result.

## Blocked by

- AX-01
```

- The hidden `<!-- skopos-issue: AX-N -->` marker is how `sync` finds the issue
  again. Keep it.
- `## Parent` is omitted when the ticket has no parent. The parent is named in
  the child body only. The EC umbrella issues (#25 to #37) carry no marker, so
  the sync never rewrites their bodies and never links them as native
  sub-issues; their labels are touched only by the one-time legacy relabel list.
- `## Blocked by` lists the AX ids, or says `None (can start immediately)`.
- Labels carry the phase, EC, type, and owner. The plan heading marker
  `(owner:human)` sets `owner:human`, so the manifest declares the label and the
  sync keeps it.

Do not hand-edit the body of an AX ticket. Change its plan entry and run
`make issues-sync APPLY=1`. Hand-written tickets (#38 to #47) follow the same
template and are edited directly.

## Blocking

GitHub's native issue dependencies are the canonical, UI-visible record. The
sync also writes the `## Blocked by` section, which is the offline mirror that
`make issues-lint` reads. A ticket is unblocked when every blocker is closed.

- List: `gh api repos/{owner}/{repo}/issues/<n>/dependencies/blocked_by`
- Add: `gh api --method POST repos/{owner}/{repo}/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`
  where `<blocker-db-id>` is the blocker's database id
  (`gh api repos/{owner}/{repo}/issues/<n> --jq .id`), not its number.
- Remove: `gh api --method DELETE repos/{owner}/{repo}/issues/<child>/dependencies/blocked_by/<blocker-db-id>`

The sync adds and removes native edges only for open AX tickets. An edge to a
human-created blocker, or to a ticket that is not managed by the marker, is left
alone.

## Sync and lint

- `make issues-manifest` regenerates `issues/alpha-exit.json` from
  `docs/alpha-exit-plan.md`; CI fails when it is stale.
- `make issues-sync` is a dry run; `make issues-sync APPLY=1` applies it.
- `make issues-lint` validates the manifest; `make issues-lint-live` checks the
  live repo and runs inside `alpha-exit-check`.

The sync rewrites the bodies and titles of **open** tickets only. Closed tickets
keep the body they had when they closed. Checkbox ticks made on GitHub are not a
difference, so a tick survives a sync whose plan text is unchanged; a plan edit
rewrites the body and resets the ticks.

## Pull requests

A PR must contain `Closes #N` or `Refs #N` for an open issue labelled
`phase:alpha-exit`, unless its commit type is fix, deps, docs, security or ci.
The guard is implemented in `repoctl prcheck`.

## Triage

See [triage-labels.md](triage-labels.md) for how the triage roles map onto this
repo's labels.
