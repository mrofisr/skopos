---
name: security-review
description: >-
  Review a Skopos diff for security issues in SQL generation, auth, tenant
  isolation, secrets, network policy, OBI privileges, MCP tools, and the release
  path. Use when reviewing or authoring a security-sensitive change.
license: Apache-2.0
---

# Security review

Review the diff, not the whole repository. Cite exact lines, state the exploit
path, and separate a real vulnerability from a hardening preference.

## When to use

- The change touches the SQL emitter, identifier handling, query limits, or the
  ClickHouse user.
- The change touches auth, TLS, MCP tools, the context API, or secret handling.
- The change touches Helm `securityContext`, capabilities, NetworkPolicy, OBI,
  ClickHouse credentials, S3 credentials, or the checkpoint.
- The change touches release signing, attestations, SBOMs, or dependencies.

Use the [runtime threat model](../../../docs/security/runtime-threat-model.md)
and the [supply-chain threat model](../../../docs/security/threat-model.md) as
the baseline. `SECURITY.md` records the current limitations.

## Categories

Scan every category that the diff can affect.

| Category | What to check |
| --- | --- |
| SQL and identifier injection | User values are query parameters; identifiers come from a whitelist; no string concatenation into SQL; the fuzz test still proves no user value becomes a literal |
| Authn and authz | Basic auth applies to every query, version, context, and `/metrics` route that should be protected; `/healthz` and `/readyz` stay open; no auth bypass through a wrapper, redirect, or alternate path |
| Tenant isolation | One shared ClickHouse user means no per-user row isolation; no change may imply isolation the deployment does not provide |
| Secret handling | No credentials in ConfigMaps, logs, errors, fixtures, or query strings; existing Secrets are preferred; inline values are clearly marked |
| Query DoS | Request body, response size, in-flight requests, queue depth, execution time, memory, rows read, and result rows stay bounded; a limit cannot be changed by a query setting the read-only user permits |
| Paths and object keys | Checkpoint and S3 keys cannot escape their prefix; file paths are rooted; archive or segment names are validated |
| Malformed input | Invalid JSON, protobuf, query text, timestamps, and sizes return an error, not a panic; nil maps and slices are handled |
| Network policy | Ingress and egress match the documented posture; a new component has a policy or a documented exception; no default-allow is introduced silently |
| OBI and host access | Capabilities are the smallest set for the enabled features; `hostPID`, tracefs, and cgroup mounts are justified; enabling context propagation adds `NET_ADMIN` and `SYS_ADMIN` and needs explicit review |
| MCP and agent tools | Tool inputs are bounded; a tool cannot read arbitrary files or query arbitrary URLs; stdio is local-only; HTTP has Basic auth and sits behind TLS; responses respect `SKOPOS_MCP_MAX_BYTES` |
| Information disclosure | Logs do not include query text, credentials, tokens, or full request bodies at info level; pprof is off by default and not exposed without auth |
| Supply chain | Images and actions are pinned by digest or SHA; a dependency has a `docs/dependencies.md` entry; an OSV exception is dated and justified |

## Method

1. Read the diff and the linked issue or proposal.
2. Map each changed file to the categories above.
3. For each finding, name the file and line, the violated invariant, and the
   concrete exploit path. If you cannot describe the path, it is a hardening
   note, not a vulnerability.
4. Classify severity:
   - **High**: SQL injection, auth bypass, credential or telemetry disclosure,
     host compromise, arbitrary code execution, supply-chain bypass.
   - **Medium**: unbounded resource use, weakened TLS or NetworkPolicy, missing
     validation that crashes a request path, secret exposure with limited
     reach.
   - **Low**: defense-in-depth gap with no demonstrated path.
5. Report only findings that survive the exploit-path test. Do not pad the
   review with style or generic advice.

## Report format

```text
### Findings

- <High|Medium|Low> | <title>: <one-sentence exploit path> (<file>:<line>)

### Recommendations

### Recommendation 1: <short title>
**Problem**: <what is wrong>
**Reason**: <what breaks and who can exploit it>
**Solution**: <specific fix, with a before/after snippet when useful>
**Locations**: <file>:<line> (one per line)
```

## Verification

```sh
go test ./internal/sqlgen/... -run 'Test|Fuzz' -count=1
go test ./internal/auth/... ./internal/httpapi/... -count=1
make test-race
make helm-lint
make helm-template
go run ./tools/repoctl osv
go run ./tools/repoctl codescan
```

Add a focused regression test for every fixed vulnerability. A security fix
without a test is not complete.

## Guardrails

- Do not propose disabling TLS verification, dropping auth, widening RBAC, or
  loosening a NetworkPolicy to make a test pass.
- Do not report a theoretical issue without a concrete path. False positives
  destroy trust in the review.
- Do not change the threat model to match the code. Change the code or record
  the residual risk explicitly.
- The human review gates in `CONTRIBUTING.md` still apply: SQL emitter and
  escaping, Helm security contexts and capabilities, dependency changes, and the
  release pipeline.

## References

- `docs/security/runtime-threat-model.md`
- `docs/security/threat-model.md`
- `docs/security/supply-chain.md`
- `SECURITY.md`
- `CONTRIBUTING.md` working rule 5
