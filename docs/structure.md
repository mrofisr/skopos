# Repository structure

This document is the human-readable map of the repository. The normative rules
are in [ADR 0004](adr/0004-repository-layout-and-language-policy.md) and
[AGENTS.md](../AGENTS.md). `internal/archtest` enforces the layout, and a
future ticket (RS-38) will generate the directory table so this file cannot
drift.

## Target layout

```text
cmd/                              binaries, shipped or developer
  gateway/                        shipped: HTTP gateway (composition root pending RS-31)
  skopos-checkpoint/              shipped, frozen
  skopos-mcp/                     shipped, frozen
  repoctl/                        the one developer tool
    internal/                     tool-private packages
      changes/                    changed-path to CI job mapping (WF-03)
      cigate/                     aggregate CI result gate (WF-06)
      command/                    Command contract, Env, registry, exit codes
      ghapi/                      the one GitHub client
      codescanning/               code-scanning alert gate
      quality/                    coverage report and ratchet (RS-05)
      issueops/                   alpha-exit manifest, lint, and sync (RS-06)
      prcheck/                    PR traceability and freeze guard (RS-07)
      osvexceptions/              dated OSV exception ledger (RS-09)
      grafanacheck/               Grafana datasource health (AX-52)
      quickstart/                 README quickstart doc-test (AX-54)
      releaseverify/              release signature and digest verification (AX-53)
      ruleset/                    ruleset-to-workflow consistency (AX-52)
      workflowlint/               workflow trigger and timeout policy (WF-08)
      workflowsdoc/               generated docs/workflows.md (WF-11)
internal/                         product libraries
  app/                            composition roots (RS-31)
  auth/ chclient/ config/ frontend/ httpapi/ loki/ metrics/ plan/ prom/ sqlgen/ tempo/
  checkpoint/ semctx/             frozen product packages
  archtest/                       tests that enforce this layout
  tools/                          separate module: pinned dev tools (RS-39, if adopted)
test/                             black-box harnesses, build-tagged Go
  bench/ compat/ e2e/ integration/ grafana/
test/quality/                     coverage floor, layout violations, quality data
scripts/                          thin orchestration of external tools only
deploy/ docs/ vendor/
AGENTS.md CONTRIBUTING.md
```

## Rules at a glance

| Rule | Enforced by |
| --- | --- |
| `cmd/` contains only `gateway`, `skopos-checkpoint`, `skopos-mcp`, and `repoctl` | `TestCmdContainsOnlyAllowedBinaries` |
| Repository tooling does not live in `internal/` | `TestNoRepoToolingInInternal` |
| `repoctl` imports only allowlisted product packages | `TestRepoctlImportsOnlyAllowedProductPackages` |
| No `util`, `common`, `helpers`, `misc`, or `types` packages | `TestNoUtilPackages` |
| No new Python | `TestNoPythonFiles` |
| `repoctl`'s `main` stays small | `TestRepoctlMainIsSmall` |

Known violations are listed in
[`test/quality/layout-violations.txt`](../test/quality/layout-violations.txt).
The list is a ratchet: a new violation fails, and a listed violation that no
longer exists also fails, which forces the list to shrink.

## Tool migration

The nine tooling binaries below have been consolidated into `repoctl`. Each
move was a pure refactor: the rule engine moves under `cmd/repoctl/internal/`,
its tests move with it, and the Makefile, workflows, and docs call
`go run ./cmd/repoctl <command>`.

| Old binary | New command | Status |
| --- | --- | --- |
| `cmd/skopos-coverage` | `repoctl coverage` | moved (RS-05) |
| `cmd/skopos-issues` | `repoctl issues` | moved (RS-06) |
| `cmd/skopos-prcheck` | `repoctl prcheck` | moved (RS-07) |
| `cmd/skopos-code-scanning` | `repoctl codescan` | moved (RS-08) |
| `cmd/skopos-osv-check` | `repoctl osv` | moved (RS-09) |
| `cmd/skopos-grafana-check` | `repoctl grafana-check` | moved (AX-52) |
| `cmd/skopos-quickstart` | `repoctl quickstart` | moved (AX-54) |
| `cmd/skopos-release-verify` | `repoctl release-verify` | moved (AX-53) |
| `cmd/skopos-ruleset-check` | `repoctl ruleset` | moved (AX-52) |

## Nested Go modules

The root module is `github.com/jawaracloud/skopos`. Two test harnesses are
separate modules because they pin reference implementations:

- `test/compat/prometheus` (Prometheus parser and reference binary)
- `test/e2e/obi/app` (OBI demo application)

`internal/tools` is a third module if RS-39 is adopted. It is not vendored
because nothing in it is shipped.
