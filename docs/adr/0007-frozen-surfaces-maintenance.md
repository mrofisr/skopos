# ADR 0007: Keep the frozen surfaces in maintenance mode (B-08)

- Status: Accepted for the beta planning line
- Date: 2026-11-16
- Related: [alpha-exit-plan.md](../alpha-exit-plan.md),
  [ADR 0003](0003-replay-deduplication.md),
  [ADR 0005](0005-obi-wrapper-speed-and-durability.md),
  [ADR 0006](0006-beta-gate.md), [checkpoint.md](../checkpoint.md),
  [systemd.md](../systemd.md)

## Context

Alpha exit is complete and the beta gate in ADR 0006 puts scale (B-01 to
B-03), latency budgets (B-04), HA, authorization, the pilot, OBI RED coverage,
and the cost envelope on the critical path. The following surfaces were frozen
until alpha exit:

- AI context and MCP (`internal/semctx`, `internal/app/mcp`).
- The checkpoint WAL (`internal/checkpoint`, `internal/app/checkpoint`).
- systemd (`deploy/systemd`).

B-08 asks whether each is kept, maintained only, or extracted into a separate
module or repository.

The evidence for keeping them in-tree:

- The checkpoint is **optional and disabled by default**
  (`checkpoint.enabled: false`). It is a recent-window durability layer, not a
  query source; it is stdlib-only and has no third-party client. The
  implementation and tests are about 3,200 lines.
- The systemd path is a documentation and unit-file deployment for hosts
  without Kubernetes. It ships the same single binary and does not add a
  runtime dependency.
- AI context and MCP are optional surfaces (`context.enabled` defaults off;
  MCP is a mode). The semantic context is also used by the metrics context
  collector, so it is not an isolated experiment.
- There is no external module, repository, or user asking for a separate
  release cadence. Extraction would add a second module, version coordination,
  and a new release surface for no beta gate.

## Decision

1. **Maintain-only, in-tree.** Keep all three surfaces where they are. New
   features, checkpoint format changes, and new MCP tools wait until after the
   beta gate or a pilot demonstrates demand.
2. Bug fixes, security fixes, documentation corrections, and compatibility
   fixes are allowed. The frozen-path exception process still applies.
3. Do not extract a module or repository now. Revisit this ADR at the beta gate
   or if the pilot produces an external reuse requirement.
4. The checkpoint stays off by default. The durable Collector queue and
   retry behavior from ADR 0005 remain the supported ingestion path; the
   checkpoint remains the optional recent-window replay layer.
5. The systemd path stays supported but maintenance-only. Correct any drift
   against the single-binary layout as a documentation/unit fix.
6. AI context and MCP stay available but maintenance-only. No new tools or
   context features are scheduled by this decision.

## Consequences

- The beta gate does not depend on these surfaces, so they do not compete with
  B-03/B-04/HA/auth/pilot for engineering time.
- Replay deduplication remains a product property: the checkpoint stays
  available, so the reliable ClickHouse profile remains the default (see
  ADR 0008).
- The repository keeps one module and one shipped binary. No new release
  cadence is introduced.
- The frozen-path exceptions and their tests remain in force for
  maintenance changes.
- A future extraction is still possible; it needs a new ADR with an external
  consumer and a release plan.

## Alternatives

- **Keep active.** Rejected: the beta gate has no requirement for new
  checkpoint, systemd, or MCP features, and active work would displace scale
  and reliability evidence.
- **Extract now.** Rejected: no external reuse, one-module simplicity, and
  release coordination cost with no beta benefit.
- **Delete.** Rejected: the checkpoint is a durability differentiator for the
  OBI wrapper, the systemd path is a real non-Kubernetes deployment option,
  and the semantic context feeds the metrics context collector.
