# ADR 0010: HA and restore-based disaster recovery

- Status: Accepted for the beta planning line; drill A passed, drills B and C pending
- Date: 2026-11-16
- Related: [ADR 0006](0006-beta-gate.md),
  [ADR 0007](0007-frozen-surfaces-maintenance.md),
  [storage.md](../storage.md),
  [resource-review.md](../resource-review.md),
  [beta-gates.md](../beta-gates.md)

## Context

ADR 0006 requires HA evidence before beta:

- at least two gateway replicas;
- ClickHouse replication **or** a proven restore path;
- a node-failure drill; and
- a restore drill.

The current lab is one 4 vCPU / 24 GiB k0s node. ClickHouse is one
StatefulSet with one PVC, the gateway has one replica, and the Collector has
one replica. The chart already supports more replicas, PDBs, and topology
spread, but the defaults stay at one so a default install does not claim HA.

The restore path is real, not hypothetical:

- `clickhouse.backup` runs a full `BACKUP DATABASE` to a separate S3
  destination and writes a latest-backup marker.
- `clickhouse.backup.restoreDrill` restores the latest backup into a
  temporary database on the live ClickHouse, checks every table, and drops
  it. It passes on the host and in CI.
- The drill restores on the **same** ClickHouse. It does not prove recovery
  from a lost PVC or node, and it does not prove that a gateway can be
  repointed at the restored database.

The gateway is stateless apart from per-replica metadata and instant caches.
The frontend/query tiers are stateless. The Collector has a disk-backed queue
per replica. OBI is a DaemonSet, one pod per node. Scaling the gateway and
Collector is a deployment change, not a code change.

## Decision

### 1. The beta HA profile

The beta profile is an overlay on the release values. The default install
stays single-replica and single-node.

```yaml
gateway:
  enabled: true
  replicaCount: 2
  resources:
    requests:
      cpu: 50m
      memory: 128Mi
    limits:
      cpu: "1"
      memory: 1Gi
  podDisruptionBudget:
    enabled: true
    maxUnavailable: 1

collector:
  replicas: 2
  podDisruptionBudget:
    enabled: true
    maxUnavailable: 1

clickhouse:
  backup:
    enabled: true
    schedule: "0 * * * *"
    lifecycle:
      enabled: true
      retentionDays: 7
    restoreDrill:
      enabled: true
```

The repository carries these replica and PDB overrides in
[`deploy/helm/skopos/values.beta.yaml`](../../deploy/helm/skopos/values.beta.yaml).
The S3 backup block stays an operator overlay because it needs a destination
and credentials; the file documents the target schedule.

The topology-spread helper already adds a `kubernetes.io/hostname` spread
with `whenUnsatisfiable: ScheduleAnyway` when the replica count is above one.
On the single-node lab both replicas share the node; on a multi-node cluster
they spread. The gateway's per-replica admission limit stays at the chart
default for now; the HA profile needs a fresh 30-client baseline because the
1 s instant cache is per replica and two replicas see fewer repeat requests.
That benchmark update is a B-04 follow-up, not this ADR.

If the deployment uses `distributed.enabled=true`, both the frontend and query
tiers need two replicas and the same PDB/spread policy. The single gateway
tier is the simpler beta profile.

### 2. ClickHouse stays single-node

The beta gate accepts a proven restore path instead of replication. This ADR
chooses the restore path:

- ClickHouse remains one StatefulSet with one PVC.
- The S3 backup is the disaster-recovery copy; the gateway never queries it.
- The restore path is the supported recovery mechanism for PVC or node loss.
- ClickHouse replication and Keeper are deferred. They need a Keeper quorum
  (three nodes) and at least two ClickHouse replicas, a `ReplicatedMergeTree`
  schema, and a chart change. The single-node lab cannot prove node-failure
  HA with them.

### 3. RPO and RTO

- **RPO:** the beta profile backs up hourly, so the recovery point is at most
  one hour old. The default daily schedule remains for non-HA installs.
- **RTO:** provisional target **one hour** for the 100k / 585 MiB dataset,
  measured from the start of the restore to a gateway serving smoke queries.
  The fresh-instance drill records the actual time. If the target is missed,
  the response is a faster restore path or a second node, not a smaller
  measured claim.

An hourly full backup of the 100k dataset is about 585 MiB; seven days of
retention is about 98 GiB of backup objects. Measure the real object count and
size in the drill. If the pilot data grows beyond
that envelope, move to a longer schedule with a documented RPO or add
incremental backups; do not silently keep 24 full copies per day.

### 4. Drills and evidence

Three drills produce the gate evidence. The safe drills run before the
maintenance-window drill; every result goes into
[beta-gates.md](../beta-gates.md).

#### A. Gateway pod failure (no data loss)

1. Deploy the beta profile; wait for two gateway replicas to be Ready.
2. Start a continuous query loop against the gateway Service and record the
   baseline error rate and p95.
3. Delete one gateway pod. The Deployment recreates it; the Service keeps the
   ready replica in rotation.
4. Record the errors during the event, the time with one ready replica, and
   the time to two Ready replicas.
5. Acceptance: no sustained 5xx or connection resets beyond the in-flight
   request; one replica serves throughout; two replicas are Ready within 30 s;
   the PDB prevents a voluntary eviction from taking the last replica.

This proves pod-failure HA on one node. It does not prove node-failure HA.

**Result (2026-11-16):** two gateway replicas on `dev-1a60b00`. Deleting one
pod left the other endpoint serving; `2/2` replicas were Ready again in
**8 s**; the Service endpoints moved from one to two; the PDB reported
`currentHealthy: 2`, `desiredHealthy: 1`, `disruptionsAllowed: 1`. A 75 s
query loop through the Service returned **327/327 HTTP 200**, maximum 69 ms,
with no connection resets. The loop evaluated an empty window because the
B-03 dataset ended before the drill, so the latency is not a load-under-failure
measurement; the HA-profile 30-client baseline is still required.

#### B. Fresh-instance restore (the missing restore evidence)

1. Prerequisites: the backup bucket and credentials, a latest-backup marker,
   and a target ClickHouse with the same storage configuration as the backup.
2. Deploy a fresh ClickHouse instance (a new namespace or a new PVC) with the
   chart. Keep the live `otel` database empty; the restore target is
   `otel_restore`.
3. Restore the latest backup with the command shape in
   [storage.md](../storage.md#restore):
   `RESTORE DATABASE otel AS otel_restore FROM S3(...)`.
4. Compare the table sets, run `SELECT count()` on every table, and compare the
   newest timestamp with the backup marker.
5. Point a gateway at the restored database
   (`clickhouse.database=otel_restore`) and run one Prom instant query, one
   Prom range query, one series query, one Loki range query, and one Tempo
   search.
6. Record the RTO from the start of the restore to the first successful smoke
   query, and the RPO from the backup timestamp to the drill start.
7. Acceptance: every table is present and queryable; the smoke queries return
   200; RTO ≤ 1 h; RPO ≤ 1 h; no credentials appear in logs.

This is the "proven restore path" evidence. It does not need the old
ClickHouse instance or its PVC.

#### C. Node failure (maintenance window)

1. Announce a maintenance window. The single-node lab has no failover, so the
   drill starts with an outage.
2. Stop the node (power off, or stop the k0s controller and containerd).
   Confirm the gateway and ClickHouse are unavailable and the Service has no
   endpoints.
3. Provision a fresh node or wipe the old node's k0s state and PVC. Do not
   reuse the old ClickHouse volume.
4. Deploy the beta profile with the backup secret; restore the latest backup
   into a fresh `otel_restore`; point the gateway at it.
5. Run the same smoke queries as drill B. Record RTO from node stop to the
   first successful query, and RPO from the latest backup.
6. Acceptance: recovery works without the old node or PVC; RTO ≤ 1 h; RPO ≤ 1 h;
   the drill report names the backup timestamp and the lost window.
7. Clean up the restore namespace/database and keep the evidence.

This is a **disaster-recovery drill**, not node-failure HA. A single node
cannot keep serving through a node loss. The beta gate row must say
"restore-based DR; node-failure HA open" until a second node and ClickHouse
replication exist.

### 5. What would close node-failure HA

A follow-up ADR and infrastructure ticket, not this one:

- a second and third node for a Keeper quorum, with two ClickHouse replicas;
  the exact topology is a follow-up decision;
- ClickHouse Keeper and `ReplicatedMergeTree` tables, with the schema and
  migration path;
- two gateway replicas spread across nodes, with the PDB;
- two Collector replicas spread across nodes;
- a node-drain drill and a ClickHouse replica-loss drill.

Until then, the beta claim is honest: the gateway survives a pod failure, and
the data survives a node loss through the S3 backup, with a measured restore
time.

## Consequences

- A gateway pod failure is tolerated; a node failure is a restore event with
  downtime. The beta HA row stays partial: gateway HA and the restore path are
  evidenced, while node-failure HA waits for the multi-node topology.
- The beta profile adds a second gateway and Collector replica, hourly
  backups, a 7-day backup retention, and the existing restore-drill CronJob.
  The default chart remains single-node and single-replica.
- The 100k HA profile needs its own 30-client bench baseline. Two gateway
  replicas reduce the per-replica instant-cache hit rate; the B-03 baseline is
  a one-replica measurement.
- The checkpoint remains optional and maintenance-only (ADR 0007). It reduces
  the recent-window replay gap when enabled, but it is not the backup and it
  does not replace the restore drill.
- No code or chart change is made by this ADR. The profile, render coverage,
  and drill evidence follow in separate changes.

## Alternatives

- **Add ClickHouse replication now.** Rejected for this step: it needs a
  multi-node lab, Keeper, a replicated schema, and a chart migration; the beta
  gate accepts a proven restore path.
- **Scale the gateway only.** Rejected as HA evidence: a node loss still takes
  ClickHouse and the gateway with it unless the data is restorable.
- **Keep the same-instance restore drill only.** Rejected: it proves the backup
  is readable, not that a fresh node can serve from it. Drill B is the missing
  evidence.
- **Keep daily backups.** Rejected for the beta profile: a 24-hour RPO is too
  large for a pilot error budget. Hourly backups with a 7-day retention keep
  the RPO at one hour; measure the storage cost before widening the schedule.
- **Claim node-failure HA from a single node.** Rejected: the node failure is
  an outage. The drill measures recovery; it does not create redundancy.
