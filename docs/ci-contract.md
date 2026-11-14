# CI contract

Recorded from the live repository on 10 Nov 2026 (WF-01). This is the source of
truth for which checks a pull request must report, so workflow changes can be
reviewed against it instead of guessing from the YAML.

## How to reproduce

```sh
gh api repos/mrofisr/skopos/rulesets/26493931
gh api 'repos/mrofisr/skopos/rulesets?targets=branch'
```

The second command lists the active branch rulesets. Only `main-protection`
(id `26493931`) exists; there is no ruleset on `development`.

## Required checks on `main`

`main-protection` is active for `refs/heads/main` only. Its required status
checks, in ruleset order, all use the GitHub Actions integration id `15368`:

| Required check | Workflow | Job key |
| --- | --- | --- |
| `Go build, vet, test` | `ci.yml` | `go` |
| `ClickHouse integration` | `ci.yml` | `integration` |
| `Tempo and Loki differentials` | `ci.yml` | `compat` |
| `cerberus three-way differential` | `ci.yml` | `cerberus` |
| `S3 tiered storage and backup (RustFS)` | `ci.yml` | `storage` |
| `Helm lint and render` | `ci.yml` | `helm` |
| `Workflow policy` | `ci.yml` | `policy` |
| `dependency-review` | `dependency-review.yml` | `dependency-review` |

`strict_required_status_checks_policy` is `true`, so a pull request must be
up to date with `main` before merging. The pull request rule requires zero
approving reviews, resolves review threads, and allows squash and rebase
merges only.

## Drift against `docs/security/ruleset.json`

The checked-in desired state and the live ruleset do not match. `make
ruleset-check` validates the file against workflow display names; it cannot see
the live ruleset.

| Check | Live `main` | Checked-in file |
| --- | --- | --- |
| `Coverage ratchet` | not required | required |
| `CI gate` | not required yet | required |
| dependency review | `dependency-review` (job key) | `Dependency review` (display name) |

WF-07 resolves this: add `CI gate` as the single required check, then drop the
per-job requirements, in both the live ruleset and the checked-in file.
`allow_extra_jobs` is set so the matrix jobs can stay in `ci.yml` without each
one being a required check. Until the live flip, treat the live column above as
the merge gate.

## Trigger contract

| Workflow | Trigger now | Must report on |
| --- | --- | --- |
| `ci.yml` | `pull_request` into `development` and `main`, `workflow_dispatch`, nightly schedule | every pull request into either protected branch; the full matrix also runs nightly |
| `dependency-review.yml` | `pull_request` into `development` and `main` | every pull request, including promotions |
| `pr-guard.yml` | `pull_request` into `development` and `main` | every pull request; the development-to-main promotion skips only traceability |
| `release.yml` | tags `v*` | tag runs only; preflight requires the commit on `main` |
| `scorecard.yml` | push to `main`, schedule, branch-protection event, dispatch | not a pull request check |
| `issues.yml` | schedule, dispatch | not a pull request check |
| `promql-compliance.yml` | schedule, dispatch | not a pull request check |

## Consequences for the path-filter design

- A workflow must not be skipped by `paths`, because that leaves its required
  checks Pending forever. Skip work inside the workflow with job-level `if`
  conditions instead (WF-05).
- Every job except `changes` and `ci-gate` must keep its display name until the
  live ruleset is switched to `CI gate` (WF-07).
- Promotion pull requests from `development` into `main` must run the full
  matrix: all required checks are attached to `main`.
- `docs/security/ruleset.json` and the live ruleset both need the same change
  when the required-check model moves. A `repoctl ruleset` pass is necessary
  but not sufficient evidence that the live gate matches the file.
- `repoctl changes` implements the path groups, `repoctl ci-gate` aggregates
  the job results, and `repoctl workflows-doc --check` keeps
  `docs/workflows.md` generated from the workflow files.
