# Runtime threat model

This document covers the runtime security of a deployed Skopos stack. The
supply-chain threat model is [threat-model.md](threat-model.md); the security
policy, supported versions, and current limitations are [SECURITY.md](../../SECURITY.md).

It is a working document. Update it when a trust boundary changes: a new
endpoint, auth mode, ingest path, credential, storage destination, MCP tool, or
release path.

## Assets

- Telemetry data: metrics, traces, logs, and the labels and attributes attached
  to them. This data can contain request paths, user identifiers, and error text.
- Query availability and capacity. A single expensive query can starve other
  users of the shared ClickHouse instance.
- Credentials: the gateway Basic auth credential, the OTLP bearer token, the
  ClickHouse admin and gateway passwords, S3 access keys, and the MCP HTTP
  credential.
- The checkpoint WAL and its S3 segments; the ClickHouse backup prefix.
- The context catalog and the observed metadata returned for unknown signals.
- Release artifacts and the identities that can publish them; see the
  supply-chain threat model.

## Actors and trust boundaries

| Actor | Trust | Can affect |
| --- | --- | --- |
| External user or Grafana | Untrusted until authenticated | Query traffic, result data |
| Operator | Trusted | Chart values, credentials, network policy, storage |
| Public frontend or gateway | Trusted service, exposed to the user network | Auth, admission, query routing |
| Query workers | Trusted service, internal only | ClickHouse reads |
| OBI | Privileged node agent | Host telemetry, eBPF programs, OTLP export |
| Collector | Trusted service | Ingest, batching, ClickHouse writes, checkpoint fan-out |
| ClickHouse | Trusted data store | Telemetry storage and query execution |
| S3-compatible store | External trust | Cold data, checkpoints, backups |
| MCP client | Untrusted until authenticated | Context reads, tool calls, queries |
| GitHub Actions and registry | Supply-chain trust | Build, signature, publish |

The main boundaries are:

1. User or Grafana to the public gateway or frontend.
2. Public frontend to the internal query tier.
3. Query tier to ClickHouse.
4. OBI and other producers to the Collector.
5. Collector to ClickHouse and to the checkpoint service.
6. Checkpoint and backup jobs to S3.
7. MCP client to the MCP server and the context API.
8. Repository and CI to the published image and chart.

## Threats and controls

| Threat | Target | Invariant | Controls and residual risk |
| --- | --- | --- | --- |
| Unauthenticated query access | Telemetry data, capacity | A publicly reachable query surface must require authentication | Basic auth is optional and disabled by default; the chart services are `ClusterIP`. Residual: an operator who exposes the gateway without auth or an authenticating edge is open. |
| Shared credential abuse | All query data | One credential must not imply per-user authorization | One operator-managed Basic auth credential; no per-user identity, audit, or row isolation. Residual: anyone with the credential can read every signal. Rotate it and keep it behind TLS. |
| Cross-tenant query | Telemetry data | A tenant must not read another tenant's data | The gateway uses one read-only ClickHouse user; there is no row-level or database-per-tenant isolation in v0.1. Do not use one deployment for mutually untrusted tenants. |
| SQL or identifier injection | ClickHouse, data integrity | User input never becomes SQL syntax | Values are query parameters; identifiers come from a whitelist; the SQL emitter has a fuzz test that asserts user values do not appear as literals. Residual: a parser or identifier-whitelist bug. Review every emitter change. |
| Query resource exhaustion | ClickHouse, gateway | The gateway bounds every query before and during execution | ClickHouse limits: 30s execution, 1GiB memory, 10M rows read, 200k result rows. Gateway limits: 64MiB response, 1MiB request, 16 in-flight requests, 32 queued, 2s queue wait. Residual: many queries within the limits can still saturate a single node. |
| Limit bypass through query settings | ClickHouse | A query cannot silently disable the gateway's limits | The gateway user is `readonly = 2` and is created with the limit defaults; the client sends the limits on every query. Residual: `readonly = 2` permits query settings, so review any change to the SQL guard or the ClickHouse user settings. |
| OTLP spoofing | Telemetry integrity | Only trusted producers can write telemetry | OTLP bearer auth and TLS/mTLS are optional and off by default. NetworkPolicy restricts ingress where enabled. Residual: any workload that can reach the Collector can inject or forge telemetry. Enable auth and TLS for an untrusted network. |
| OTLP token or credential theft | Telemetry integrity | Tokens are not exposed to unrelated workloads | The token is stored in a Secret and injected into OBI. Plaintext OTLP exposes it to a network observer. Rotate the token and enable TLS or mTLS. |
| OBI host compromise | Node and cluster | OBI runs with the smallest capability set that works | OBI uses `hostPID`, tracefs and cgroup mounts, and a narrow capability set. Enabling trace-context propagation adds `NET_ADMIN` and `SYS_ADMIN`. Residual: OBI is a privileged node agent; treat a compromise as a node compromise. |
| ClickHouse credential exposure | Telemetry data | The gateway credential cannot write or administer | The gateway uses a generated read-only user. ClickHouse credentials live in a Secret. The default chart does not enable encryption at rest or TLS between components. Use platform secret management and network controls. |
| S3 credential exposure | Cold data, checkpoints, backups | Storage credentials are scoped and not rendered into ConfigMaps | Checkpoint, cold-tier, and backup values prefer an existing Secret; inline values are rendered into a Secret. Residual: a Secret reader can read the bucket prefix it is scoped to. Use separate credentials per prefix where the platform allows it. |
| Checkpoint injection | Replay integrity | Only the Collector can append to the checkpoint | The checkpoint endpoint is internal and is not covered by the OTLP token. Residual: an attacker who can reach the checkpoint service can inject records. Keep the service ClusterIP and restrict ingress. Replay is deduplicated on read, but does not authenticate the writer. |
| Backup or restore abuse | Backup data, ClickHouse | Backup data is not a query path and restore is isolated | Backups go to a separate S3 prefix. The restore drill restores into a temporary database, verifies it, then drops it. Residual: a reader of the backup bucket can read the full telemetry history. Restrict bucket access and lifecycle. |
| MCP tool abuse | Context data, query capacity | An MCP client cannot exceed the gateway's auth and query limits | stdio is local-only. HTTP MCP has optional Basic auth and runs behind an external TLS terminator; the server does not terminate TLS itself. Responses are bounded by `SKOPOS_MCP_MAX_BYTES`. Residual: an exposed HTTP MCP server without auth lets a client read context and run the tools it exposes. |
| Prompt injection through context data | AI SRE agent | Context is data, not instructions | The catalog is operator-authored; observed metadata is labeled `known:false` and `observed_only`. An agent must treat returned descriptions and telemetry as untrusted input. Human review is required for observed-only suggestions. |
| Information disclosure through logs or pprof | Credentials, telemetry | Logs and debug surfaces do not expose secrets | pprof is off by default; `profileAllowUnauth` is a local/benchmark escape hatch. Do not log query text, tokens, or full request bodies at info level. Residual: a misconfigured debug deployment. |
| Cardinality or label explosion | ClickHouse memory and disk | Ingested cardinality is bounded by configuration | TTL and retention are set per signal; the Collector queue and memory limiter bound in-flight data. Residual: an untrusted producer can still create many series within the retention window. Restrict ingest and monitor disk. |
| Supply-chain compromise | Image, chart, release | Only reviewed, signed artifacts are published | See [threat-model.md](threat-model.md) and [supply-chain.md](supply-chain.md). |

## Out of scope

- Compromise of the Kubernetes control plane, the container runtime, or the host
  kernel.
- Compromise of ClickHouse itself, or a malicious ClickHouse administrator.
- Compromise of Grafana or the user's identity provider.
- A malicious maintainer with full repository or release authority.
- Denial of service by the cloud provider, the registry, or the network.
- The internal security of OBI's eBPF programs; review OBI's own security model.

## Review triggers

Review this document when any of the following changes:

- a new public endpoint, auth mode, or MCP tool;
- a new ingest protocol or producer;
- a new credential, storage destination, or S3 prefix;
- a change to the ClickHouse user, limits, or retention;
- a change to OBI capabilities, host mounts, or context propagation;
- a change to the release, signing, or registry path.

Record the review in the pull request that makes the change. If a control is
weaker than this document claims, fix the control or record the residual risk
here; do not silently broaden the claim.
