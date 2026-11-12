# Repository structure

This document is the human-readable map of the repository. The normative rules
are in [ADR 0004](adr/0004-repository-layout-and-language-policy.md) and
[AGENTS.md](../AGENTS.md). `internal/archtest` enforces the layout.

## Layout

```text
cmd/
  skopos/                         the single shipped binary: wiring only
tools/
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
      sbom/                       CycloneDX fallback inventory for releases
      workflowlint/               workflow trigger and timeout policy (WF-08)
      workflowsdoc/               generated docs/workflows.md (WF-11)
internal/                         product libraries
  app/                            mode dispatch and composition roots
  appenv/                         injected process environment
  version/                        link-time build version
  auth/ chclient/ config/ frontend/ httpapi/ loki/ metrics/ plan/ prom/ sqlgen/ tempo/
  checkpoint/ semctx/             frozen product packages
  archtest/                       tests that enforce this layout
  tools/                          separate module: pinned dev tools (RS-39, if adopted)
test/                             black-box harnesses, build-tagged Go
  bench/ compat/ e2e/ integration/ grafana/
test/quality/                     coverage floor, layout violations, quality data
scripts/                          thin orchestration of external tools only
  bench/ compat/ deploy/ e2e/ integration/ quickstart/ release/ scorecard/
deploy/ docs/
AGENTS.md CONTRIBUTING.md
```

`SKOPOS_MODE` selects the role of the single binary: `all` (default),
`gateway` (alias for `all`), `query`, `worker` (legacy alias for `query`),
`frontend`, `mcp`, or `checkpoint`. The mode implementations live under
`internal/app/<mode>` and receive an `appenv.Env`, so `cmd/skopos/main.go`
contains only signal wiring and the call to `app.Main`.

## Rules at a glance

| Rule | Enforced by |
| --- | --- |
| `cmd/` contains only `skopos` | `TestCmdContainsOnlyAllowedBinaries` |
| Repository tooling does not live in `internal/` | `TestNoRepoToolingInInternal` |
| `repoctl` imports only allowlisted product packages | `TestRepoctlImportsOnlyAllowedProductPackages` |
| No `util`, `common`, `helpers`, `misc`, or `types` packages | `TestNoUtilPackages` |
| No Python source | `TestNoPythonFiles` |
| Shell scripts live under `scripts/`, except the Helm `migrate.sh` asset | `TestShellScriptsAreCentralized` |
| Every `cmd/*/main.go` stays under 50 lines | `TestMainIsSmall` |

Known violations are listed in
[`test/quality/layout-violations.txt`](../test/quality/layout-violations.txt).
The list is a ratchet: a new violation fails, and a listed violation that no
longer exists also fails, which forces the list to shrink. It is currently
empty.

## Tool migration

The nine former `cmd/skopos-*` tooling binaries have been consolidated into
`tools/repoctl`. Each move was a pure refactor: the rule engine moved under
`tools/repoctl/internal/`, its tests moved with it, and the Makefile,
workflows, and docs call `go run ./tools/repoctl <command>`.

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

`internal/tools` is a third module. It pins developer tools such as the
mutation tester (gremlins) and is kept out of the root module's dependency
graph because nothing in it is shipped.
