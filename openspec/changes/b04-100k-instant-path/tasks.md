# Tasks

Cross-cutting rules for every task group: one concern per PR, red-first (the
named tests are committed first and fail for the stated reason), at most about
400 changed non-test lines per PR, edge and negative cases included, existing
gates stay green, deterministic perf proxies only in PR CI, coverage ratchet
respected, and docs land in the same PR as the behavior they describe
(STG-1…STG-7). Lab mutations and baseline approvals are human checkpoints.

## 1. Prerequisites: beta traceability

- [x] 1.1 Red-first: add prcheck table fixtures (open `phase:beta` issue passes, `phase:alpha-exit` still passes, unlabelled issue fails, closed beta issue fails) and verify they fail against the current guard for the stated reason
- [x] 1.2 Implement `phase:beta` acceptance in `tools/repoctl/internal/prcheck` (frozen-path guard unchanged) and verify `go test ./tools/repoctl/internal/prcheck` passes
- [x] 1.3 Open the tracking issue for this change with `phase:beta` (maintainer approval) and verify subsequent PR bodies reference it — #64 (opened with `phase:beta`; PRs for this change must reference #64)

## 2. Schema and migration (metrics-storage)

- [x] 2.1 Red-first: schema-render golden tests asserting the five-minute bucket key on all five metrics tables in both profiles (reliable profile keeps the row hash) and verify they fail against the current hour key
- [x] 2.2 Red-first: integration migration tests against real ClickHouse (upgrade preserves rows and results, ingest continues during the copy, re-run is a no-op, interrupted run recovers, fast profile is re-keyed) and verify they fail on the hour-keyed fixture
- [x] 2.3 Implement the `schema.sql` key change and the `migrate.sh` key detection/migration for both profiles, update the schema renderers, and verify 2.1/2.2 pass plus `make helm-lint` and `make helm-template`
- [x] 2.4 Add replay-dedup parity tests on the re-keyed tables (duplicate replay collapses, distinct samples survive) and verify they pass under `make test-integration`
- [x] 2.5 Lab upgrade drill on the 100k dataset (human checkpoint): row counts, ingest continuity, re-run no-op, backfill duration; verify the drill notes are committed to the B-04 report draft

## 3. Bucket-aware emitters (instant-rate-pushdown)

- [x] 3.1 Red-first: golden SQL tests for the parameterized bucket predicate on the rate and sample paths (window inside one bucket, window crossing a boundary) and verify they fail
- [x] 3.2 Red-first: recorder row-bound tests on the fixture dataset (deterministic, no wall-clock) and fuzz targets for the bucket-aware paths, and verify they fail
- [x] 3.3 Implement the bucket predicates in `internal/sqlgen` and the `internal/prom` routing, regenerate goldens in the same PR, and verify 3.1/3.2 pass plus `make fuzz`
- [x] 3.4 Verify parity: `make compat-all` shows 0 divergences and 0 unexpected failures (212-case oracle, compliance suite, corpora), with explicit counter-reset and grouped-rate cases — 710 passed / 1,533 classified / 0 unexpected; the stale `unsupported-function` error regex was repaired (see #63); all other corpora pass
- [x] 3.5 Verify the generated-doc checks pass unchanged (support surface is unchanged); update any hand-written read-path description only where it now misstates behavior

## 4. Cost-aware admission (query-admission)

- [x] 4.1 Red-first: cost-model table tests (expensive instant rate weighs more than a cheap instant, minimum weight 1, unknown cardinality uses the conservative fallback, disabled weighting is count-based, invalid config rejected at startup) and verify they fail
- [x] 4.2 Implement the instant cost seam with the fallback weight plus env/config/chart validation, and verify 4.1 passes and Helm render tests map the values and reject invalid combinations
- [x] 4.3 Red-first: cardinality-estimate tests (per-metric counts piggyback on the cached metadata refresh within its row budget, a cache hit adds no statement, cold cache falls back) and verify they fail
- [x] 4.4 Implement the per-metric cardinality in the metadata refresh and wire the estimate into the cost model, and verify 4.3 passes, the recorder shows no new per-request scan, and `make test-integration` passes
- [x] 4.5 Integration burst tests with a slow fake backend (deliberate 429/503 + Retry-After only, probes answer under load, distributed rejection passes through unchanged, panic and disconnect release budget) and verify they pass under `-race`
- [x] 4.6 Document the new knobs in `docs/metrics.md` and `docs/install.md`, regenerate the limits section if defaults changed, and verify the generated-doc diff check passes

## 5. Measurement and decision (lab)

- [x] 5.1 Regenerate the 100k profile on the lab (seed 42) and verify the dataset hash is `b900affe5ce4b9ec` after `OPTIMIZE ... FINAL`
- [x] 5.2 Run candidate A (bucketed key + tuned cost-aware admission): 30-client mix, three runs, median per-query p95; verify the JSON results, `system.query_log` rows/bytes, and resource peaks are recorded
- [x] 5.3 Run candidate B (bucketed key + lab-only rollup materialized view, hand-routed queries, no product code): same mix plus counter-reset/late-data/dedup parity checks on the corpus; verify measurements and parity notes are recorded
- [x] 5.4 Write `docs/adr/0011-100k-instant-path.md` comparing A and B with the decision rule from design.md; if the rollup wins, open the follow-up change instead of implementing it; verify maintainer acceptance of the ADR
- [x] 5.5 Write `docs/benchmarks/b04-100k-instant.md` and update `docs/benchmarks.md`; verify another person can reproduce the runs from the documented commands

## 6. Evidence and gate updates (integration checks)

- [x] 6.1 Update the checked-in bench baseline from the winning candidate through a reviewed PR (human approval) and verify `go run ./tools/repoctl bench-gate --profile 100k` passes against it — PR #65 (refs #64); gate passes against the three candidate-A runs
- [x] 6.2 Update the `docs/beta-gates.md` latency row to match the recorded evidence (100k absolute budgets stay pilot-defined) and verify every claim links to its evidence artifact
- [x] 6.3 Full gate sweep: `make check`, `make test-race`, `make compat-all`, `make bench-gate`, coverage ratchet, and the nightly mutation report for in-scope packages; verify all green and the evidence files recorded — mutation 94.0% (sqlgen), 94.3% (config), evidence in `test/quality/evidence/`

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied
  (`openspec archive b04-100k-instant-path`), syncing the three new capability
  specs into `openspec/specs/`.
- If the rollup won the measurement, start the follow-up change with its own
  proposal and design; do not implement it inside this change.
- Enabling the nightly 100k gate trigger waits for the self-hosted
  `skopos-bench` runner — a separate, human-approved change per ADR 0009.
