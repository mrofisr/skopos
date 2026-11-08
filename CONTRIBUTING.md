# Contributing to Skopos

Skopos is developed agentically, with human review at the points listed below.
These rules are part of ADR 0001 and are not optional.

## Working rules

1. **Tests and oracles first.** Every feature starts with a golden or
   differential test before implementation.
2. **Exact values.** Tests assert exact values, not only that a result exists.
   Cover invalid input, boundaries, and empty results.
3. **No silent skips.** No test may be skipped or added to an allow-list without
   a documented entry and human approval.
4. **Do not guess.** Record unknowns in code comments or documentation.
   Behavior that has not been verified against a reference implementation must
   not be implemented as if it were known.
5. **Human review is required for:** the SQL emitter and escaping, Helm
   `securityContext` and capabilities (especially OBI), every dependency change,
   and the release and signing pipeline.
6. **One PR, one change.** Agents do not bump dependency or image versions in a
   feature PR. Version bumps are separate, human-reviewed PRs.
7. **No copied code.** Do not copy code from cerberus, oteldb, Loki, or Tempo.
   Loki and Tempo are AGPL-licensed. They are run only as reference binaries or
   containers in CI, and comparisons are black-box.
8. **Agents use the agent identity.** Agent work uses the dedicated non-admin
   token or GitHub App described in `GOVERNANCE.md`. Agents do not push to
   `main`, create release tags, read release secrets, or modify workflows with
   an admin credential.
9. **Releases are tag-only.** Do not add a manual publish path, a key-based
   signing path, or a mutable chart release from `main`. Follow
   [docs/release.md](docs/release.md).

## Naming conventions

Workflow and job names are user-facing, so they follow one convention:

| Element | Convention | Example |
| --- | --- | --- |
| Workflow file | lowercase kebab-case | `pr-guard.yml` |
| Workflow name | sentence case, proper nouns preserved | `PR guard` |
| Job key | lowercase kebab-case | `publish-image` |
| Job display name | sentence case, first word capitalized; official lowercase project names keep their casing | `Coverage ratchet`, `cerberus three-way differential` |
| Step name | imperative sentence case; a tool-only step may keep the lowercase tool name | `Check out repository`, `actionlint` |

Job display names become required status checks. Never rename a job display
name without updating `docs/security/ruleset.json` and the live
`main-protection` ruleset. The batch rename of existing names waits for AX-52.

## Language policy

- Go is the implementation language for product code and for repository rule
  engines (manifest, coverage, PR guard, release checks).
- GitHub workflow YAML is the glue: it sets triggers and permissions and calls
  `gh` or `go run`. Business logic does not live in YAML.
- Shell in `scripts/` is limited to thin orchestration of external tools
  (release, signing, verification, SBOM). Parsing, validation, and decision
  logic belong in Go with Go tests.
- Existing Python under `test/` is frozen for the alpha. It may receive only
  fixes that keep an existing black-box harness working. New test automation is
  Go; migrating the remaining harnesses is a post-alpha decision.
- Do not add a new language runtime or a new shell test framework for
  repository checks.

## Test seams

The v0.1 alpha test seams are:

- `internal/sqlgen.Emit`: golden SQL with exact parameter lists, fuzz tests that
  prove user values never become SQL literals.
- `internal/chclient.Client.Query`: `httptest` unit tests and an integration test
  against a real ClickHouse server.
- `internal/httpapi.Handler`: `/healthz` and `/readyz` behavior, plus route
  registration for the three heads.
- `internal/prom.Engine` and `internal/prom.ClickHouseFetcher`: PromQL
  semantics, golden results, and real-ClickHouse queries.
- `internal/tempo.ParseTraceQL`, `Search`, and the OTLP JSON/protobuf response:
  TraceQL subset and wire-format tests.
- `internal/loki.Parse` and the query/labels/series handlers: LogQL subset and
  stream responses.
- Helm chart: `helm lint` and `helm template` with default and override values.

New seams need agreement in the PR description before tests are written.

## Dependency policy

- Stdlib-first. A non-stdlib dependency requires an entry in
  [docs/dependencies.md](docs/dependencies.md): reason, license, owner, and the
  pinned version.
- The runtime allow-list is the official Prometheus PromQL parser (D18) and the
  official OTLP protobuf types used for Tempo responses. Both are
  provider-official and recorded in `docs/dependencies.md`. Test dependencies
  are limited to reference implementations run black-box.
- `go.mod`, `go.sum`, and `vendor/` are committed. Regenerate vendor with
  `go mod vendor`; CI runs `go mod verify`.
- Images in the chart are pinned by digest. Collector, OBI, and ClickHouse
  versions change only in separate, human-reviewed PRs.
- No third-party Helm subcharts.

## Alpha-exit issue workflow

The alpha exit plan is [docs/alpha-exit-plan.md](docs/alpha-exit-plan.md). Every
AX ticket is generated from it; do not create an AX issue by hand or edit its
body in GitHub.

```sh
make issues-manifest        # regenerate issues/alpha-exit.json after a plan edit
make issues-manifest-check  # CI: fail when the checked-in manifest is stale
make issues-lint            # validate the manifest locally
make issues-lint-live       # read-only lint of the live alpha-exit issues
make issues-sync            # dry run; prints the labels and issues it would change
make issues-sync APPLY=1    # apply, maintainer-only
```

The sync is idempotent. It owns the `phase:*`, `ec:*`, `type:*`, `owner:*`,
`backlog:*`, and `frozen` labels and leaves unrelated labels alone. The live
lint runs from the scheduled `issues` workflow, not on fork pull requests.

## Development loop

1. Write one failing test at the agreed seam.
2. Run it and confirm the failure is the expected one.
3. Write the minimum implementation to pass it.
4. Run the focused test, then the full package tests.
5. Refactor only after the test is green.

Run `make check` before opening a PR. It includes a short SQL emitter fuzz run;
CI runs the same fuzz target as a blocking gate. Integration tests require a
ClickHouse HTTP endpoint:

```sh
docker run --rm -d --name skopos-clickhouse -p 8123:8123 -p 9000:9000 \
  clickhouse/clickhouse-server:26.8.36.3@sha256:d426d2f612e1e189c90f9a3f8dcd85a03c6851273833817a296bb327e63aa7a7
CLICKHOUSE_URL=http://127.0.0.1:8123 make test-integration
```

The integration test creates and drops its own database and user.

## Commit messages

Use the imperative mood and a short scope:

```text
sqlgen: parameterize label matchers
chclient: enforce read-only queries
helm: add OBI DaemonSet with least-privilege capabilities
docs: record the pinned exporter schema
```

Do not commit generated binaries, rendered charts, or local credentials.
