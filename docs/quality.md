# Quality gates

Skopos uses generated evidence artifacts for the alpha-exit quality gates.
This page covers the coverage ratchet (STG-5 / AX-01). Mutation testing
(STG-6 / AX-02) is advisory for alpha and documented with its harness.

## Coverage ratchet

Package coverage must stay at or above the floor recorded in
[`test/quality/coverage-floor.json`](../test/quality/coverage-floor.json).
Floors are measured baselines rounded down to one decimal place. They only
move up: lowering a floor fails the check even when the tests still pass.

The 80% target is a beta goal. Alpha only requires that coverage does not
regress from the baseline.

### Commands

| Command | What it does |
| --- | --- |
| `make coverage` | Runs the tests, writes `coverage.out`, and fails when a package is below its floor, is missing from the profile, or has a lowered floor |
| `make coverage-report` | Prints per-package coverage next to the floor |
| `make coverage-write` | Regenerates `test/quality/coverage-floor.json` from measured coverage |

`make coverage` compares the checked-in floor with the floor at
`origin/development`. If that ref or file is unavailable, it prints a warning
and skips only the lowering check, so a fresh checkout without the remote
branch still runs the rest of the gate.

The gate covers product packages (`internal/...` and `cmd/...`). Test-only
packages such as `test/compat/...` are not part of the floor.

### Raising a floor

Run `make coverage-write` after adding tests, inspect the diff, and commit the
floor with the tests in the same PR. Do not hand-edit a floor downward; fix the
regression or add the missing tests instead.

A new package has no floor until the file is regenerated. The gate fails when
a measured package is missing from the floor file, and the failure names the
package, so the fix is always to commit its measured floor.

### CI

The `Coverage ratchet` job runs `make coverage` on every pull request and on
`development`. It uses a full checkout so the previous floor can be read from
`origin/development`.

## Mutation testing

Mutation testing is advisory for alpha (STG-6 / AX-02); it does not block a
pull request. The tool is pinned in the separate `internal/tools` module so the
root module's dependency set stays clean.

Two candidates were evaluated on `internal/auth`:

| Tool | Version | Result |
| --- | --- | --- |
| gremlins | `v0.6.0` | Builds with Go 1.27; 2 killed, 3 timed out, 0 lived; efficacy 100%, about 1.2 s |
| avito-tech/go-mutesting | `v0.0.0-20261002081621-9ce5f731e321` | Does not build on Go 1.27/linux-arm64: `zimmski/osutil v1.6.1` uses the removed `syscall.Dup2` |

Gremlins was chosen for the working build, the machine-readable report, and
the built-in efficacy and mutant-coverage thresholds.

```sh
make mutation                 # defaults to PKG=./internal/auth
make mutation PKG=./internal/sqlgen
make mutation-gate PKG=./internal/httpapi
```

The targets build the pinned binary into `bin/`, run `gremlins unleash` with a
JSON report, and print the summary through `repoctl mutation report`. The
report is written to `mutation.json`, which is ignored.

The gate uses a 70% efficacy floor. Survivors may be accepted in
`test/quality/mutation-allowlist.json` only with a stated reason; an entry
without a reason fails. Packages with zero killed and lived mutants fail
explicitly instead of passing.

Baselines measured on 10 November 2026 (`--timeout-coefficient 10`; the
default coefficient times out `internal/config` and makes it look empty):

| Package | Efficacy | Mutant coverage |
| --- | ---: | ---: |
| `internal/auth` | 100.0% | 100.0% |
| `internal/config` | 98.9% | 100.0% |
| `internal/httpapi` | 100.0% | 100.0% |
| `internal/plan` | 100.0% | 33.3% |
| `internal/sqlgen` | 91.7% | 59.0% |

The nightly `Mutation` workflow runs the gate per package on that matrix. It is
scheduled and manual only, is not a required check, and is advisory for alpha.

## Kubernetes installation (KWOK)

The `Kubernetes install (KWOK)` job installs and upgrades the local chart in a
[KWOK](https://kwok.sigs.k8s.io/) cluster. It creates a fake node, binds the
ClickHouse and Collector queue claims to static volumes, and then uses Helm to
install the data plane and upgrade to the gateway deployment. The harness lives
in [`test/e2e/kwok`](../test/e2e/kwok/README.md).

KWOK fakes the pod lifecycle. The job proves that the API server accepts the
chart, workloads schedule, PVCs bind, Helm hooks complete, and Services get
endpoints. It does not start containers, run probes, execute ClickHouse SQL, or
exercise OBI eBPF; those paths remain covered by the ClickHouse integration,
compatibility, and OBI harnesses.

## Alpha exit evidence

`make alpha-exit-check` aggregates the seven criteria into one report:

```sh
make alpha-exit-check
```

Each criterion is a generated `test/quality/evidence/ec-N.json` file:

```json
{
  "ec": 1,
  "status": "pass",
  "commit": "<sha>",
  "timestamp": "2026-11-10T12:00:00Z",
  "command": "make grafana-journeys"
}
```

The evidence directory is ignored by git: the files record the commit they
were produced from, so they are run artifacts rather than source. Upload the
directory as a CI artifact when the exit run is recorded.

The producers are:

| EC | Command | What it records |
| --- | --- | --- |
| 1 | `make grafana-journeys` | Grafana datasource journeys and the OBI RED dashboard |
| 2 | `make compat-all` | PromQL, LogQL, TraceQL, Tempo differential, and cerberus seed checks |
| 3 | `make bench-gate` | Concurrent safety gate with the memory limit |
| 4 | `make metrics-contract` | Required metric names and labels |
| 5 | `make release-evidence TAG=vX.Y.Z` | Signature/attestation/SBOM verification and the README quickstart |
| 6 | `make quality-report` | Coverage, layout, workflow policy, ruleset, and checker tests |
| 7 | `make alpha-exit-check` | Scope checks, then the aggregate check |

EC1-EC3 need Docker; EC5 needs `cosign`, `gh`, `helm`, `k3d`, `kubectl`, and a
published release whose tag matches the README quickstart version. EC2 runs
the current PromQL, LogQL, TraceQL, Tempo, and cerberus seed checks; the retired
end-to-end three-way harness remains the recorded baseline in
[cerberus-baseline.md](cerberus-baseline.md), not a live re-run.

`repoctl evidence run` executes one gate and writes pass/fail evidence;
`repoctl evidence record` writes pass evidence after a compound Make target
has completed. Compound targets remove their previous evidence before running,
so a failure cannot leave an old pass file behind. `make alpha-exit-check`
first verifies EC1-EC6 with `--skip 7`, then runs the scope checks, records
EC7, and aggregates all seven. Run it on the exit commit, within the 24-hour
evidence window, with the same `GITHUB_SHA` the aggregate check uses.

The check fails closed: a missing file, malformed JSON, a mismatched EC
number, a status other than `pass`, an empty command, a timestamp older than
`--max-age` (24h by default), or a commit other than `--commit` (`GITHUB_SHA`)
all fail and name the criterion. A report is written to
`docs/alpha-exit-report.md`. An empty evidence directory fails 0/7 instead of
passing silently.

## PR traceability and freeze guard

Every pull request must link an open issue labelled `phase:alpha-exit` using
`Closes #N` or `Refs #N` in the body. Commit types `fix:`, `deps:`, `docs:`,
`security:` and `ci:` are exempt from the link requirement.

The following paths are frozen until the alpha exit apart from bug and
security fixes:

- `internal/semctx`
- `internal/app/mcp`
- `internal/checkpoint`
- `internal/app/checkpoint`
- `deploy/systemd`

A pull request that touches one of them needs the `frozen-exception` label and
a one-line reason in the body:

```text
Frozen-exception: the replay fix prevents data loss on restart
```

Fork pull requests skip the issue lookup because the job runs read-only from
the base commit; the job summary records the skip and the freeze check still
runs. For maintainers, an unreachable GitHub API fails the check closed.

The `PR traceability and freeze guard` workflow only needs `contents: read`,
`issues: read` and `pull-requests: read`.

## Red-first evidence

Every behavior PR starts with a tests-only commit that fails for the stated
reason, and the PR links the failing run. The check is defined in
`docs/alpha-exit-plan.md` under STG-1.

Pull requests labelled `type:micro` must have at least two commits. The first
commit may only touch:

- `*_test.go` and `.golden` files
- `testdata/` and `fixtures/`
- the repository `test/` tree

Implementation in the first commit, a single-commit PR, or an empty first
commit fails the guard. Docs and CI updates may carry `red-first-exempt` to
skip the check. The same `pr-guard` workflow runs this check from the pull
request commit list, so it survives a squash merge.
