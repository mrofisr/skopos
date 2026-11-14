# Skopos: alpha exit plan and micro-ticket backlog

Draft for maintainer review · 8 Nov 2026 · based on `main` at 2f91d99 (shallow clone) and issues #24–#47

## 1. The rule

Alpha exits when `make alpha-exit-check` prints 7/7 PASS on a clean checkout. Nothing else counts, and nothing outside the seven criteria may block the exit. What to build after the exit is decided by data from a real deployment, not by guesses made now.

Decisions already made (yours):

- Reference scale is today's benchmark: about 560 series, 2.66M rows, a 4 vCPU / 7.6 GiB host. The 10k-series and 100k-series profiles become post-alpha tickets (section 7).
- Frozen until exit (bug and security fixes only): AI context and MCP, the checkpoint WAL, systemd.
- Strict test gate on every ticket: red-first, edge and negative cases, deterministic perf proxies, coverage floor, mutation testing.
- Compatibility is defined the simple way (EC1 and EC2): Grafana's own datasources work, and nothing is ever silently wrong. It is not a feature checklist.

Decisions I made for you are listed in section 8. Flip any of them.

## 2. Alpha versus beta

| Alpha proves | Beta proves |
| --- | --- |
| Grafana's Prometheus, Loki and Tempo datasources work against it | It stays fast at 10k and then 100k series |
| It never returns a wrong or silently empty answer | Latency SLOs set from observed data |
| It degrades safely under load (no dropped connections, no OOM) | HA ClickHouse, failover and restore drills |
| You can observe it in a real deployment | Authorization and tenant isolation |
| It can be installed and verified from a clean machine | A pilot on real cardinality |

Explicit non-goals until beta: scale targets, latency budgets, HA, tenancy, TraceQL structural operators, pipelines and metrics queries, LogQL feature expansion, and any new work on the frozen areas. Kibana is also a non-goal: it talks to Elasticsearch, not to Prometheus, Loki or Tempo style APIs, so supporting it would be a different gateway head, not a compatibility fix.

## 3. Exit criteria

Each criterion is binary and has one evidence command.

| ID | Pass condition | Evidence | Tickets |
| --- | --- | --- | --- |
| EC1 Grafana plug-and-play | With a pinned Grafana, the Prometheus, Loki and Tempo datasources pass their health check and a scripted journey (query, labels, variables, trace view). The OBI RED dashboard has 0 query errors and at least one non-empty series per panel. | `make grafana-journeys` | AX-40 to AX-44 |
| EC2 Correct or explicit error | 0 divergences from the reference engines on the supported subset (PromQL oracle, official compliance suite, LogQL upstream baseline, TraceQL corpus, three-way run). Every construct in the inventories either matches the reference or returns an explicit error, never an empty 200. | `make compat-all` | AX-45 to AX-49 |
| EC3 Survives concurrency (safety, not speed) | At the reference scale, 30 clients for 60 s: 0 dropped connections, 0 unclassified 5xx, every rejection is a deliberate 429 or 503 with Retry-After, no OOMKill, gateway peak memory under its limit. Latency is recorded and published but not gated. | `make bench-gate` | AX-20 to AX-25, AX-30 to AX-39 |
| EC4 Observable | `/metrics` exposes request rate, errors and duration per head, in-flight and rejected counts, ClickHouse queries, rows read and duration, and an unsupported-query counter. The contract test passes in single and distributed modes. | `make metrics-contract` | AX-10 to AX-14 |
| EC5 Releasable and verifiable | Signed image and chart with SBOM and provenance verify from a clean machine. README quickstart commands run verbatim against the published chart. 0 open high code-scanning alerts. OSV clean or covered by dated exceptions. | `make release-verify TAG=...` and `make quickstart-test` | AX-50 to AX-55 |
| EC6 Strict gates are live | Coverage ratchet enforced in CI, mutation job green on the in-scope packages, PR traceability and red-first checks required. | `make quality-report` | AX-01 to AX-06 |
| EC7 Scope discipline | Every open `phase:alpha-exit` issue maps to exactly one EC and is closed, frozen paths are untouched apart from logged exceptions, and the aggregate check is 7/7. | `make alpha-exit-check` | AX-05, AX-60, AX-61 |

EC4 exists because your plan depends on it: you cannot observe real-world performance after alpha without the gateway reporting on itself.

## 4. Strict test gate

A micro-ticket is one PR with one observable behavior, at most about a working day and about 400 changed non-test lines, independently revertable, with one named primary test. If it grows past that, split it.

Every micro-ticket must satisfy all seven gates:

1. **STG-1 Red-first.** The tests named in the ticket are committed first and fail for the stated reason; the PR links the failing run.
2. **STG-2 Edge and negative.** At least one boundary case and one failure-path case per behavior.
3. **STG-3 Existing gates stay green.** Build, vet, test, race, fuzz, integration, Helm, policy and the differential oracles.
4. **STG-4 Deterministic perf proxies only in PR CI.** Statements issued, tables touched, rows read, EXPLAIN index use. Never wall-clock. Latency lives in `make bench-gate` and nightly runs.
5. **STG-5 Coverage ratchet.** Package coverage must be at or above its floor in `test/quality/coverage-floor.json`, and floors only move up. Target 80% for sqlgen, auth, config, httpapi, plan, chclient and frontend once the baseline is known.
6. **STG-6 Mutation.** In-scope packages (sqlgen, auth, config, httpapi, plan, plus the new admission and ttlcache) keep at least 70% mutant efficacy; surviving mutants are listed with a reason in `test/quality/mutation-allowlist.yaml`. It runs nightly, and a ticket touching an in-scope package is not done until the nightly run is green. prom, loki and tempo are excluded at first (large, and already covered by differential oracles) and piloted in beta.
7. **STG-7 Docs in the same PR.** Generated docs (support matrix, limits) regenerate and CI fails on a diff. Hand-written status claims are not allowed where they can be generated.

The 70% and 80% figures are proposals; AX-01 and AX-02 measure the baselines first.

Ticket template: Title, Parent (existing issue or new), EC, Blocked by, Red tests (named), Edge and negative cases, Done when.

**Adding tickets later.** If any part of this plan needs more work than one micro-ticket (a spike finds a new cause, a review finds a gap, a ticket grows past the size limit), do not widen the existing ticket. Create a new, separate micro-ticket instead:

- One behavior and one PR per ticket, inside the size limit above. If it still does not fit, split it again before it is opened.
- Use the ticket template: title, parent, EC, blocked by, named red tests, edge and negative cases, done when.
- Give it a parent (an existing ticket or an EC), the label `type:micro` and exactly one `ec:*` label. A ticket that traces to no EC gets `backlog:post-alpha` instead (AX-04 and AX-05 enforce this).
- State what blocks it and what it blocks, and keep the dependency chain free of cycles.
- It meets all seven gates (STG-1 to STG-7), with no exception for 'small' or 'quick' fixes.
- Record the addition in this document by PR (rule 3 in section 8), so the plan and the tickets stay one list.

## 5. What happens to the existing tickets

| Existing | Action | Replaced or covered by |
| --- | --- | --- |
| #24 closed | Stays closed. Amend criterion 1: alpha uses today's dataset as the reference scale; larger profiles move to beta. | B-01 to B-03 |
| #25 | Split | AX-30, AX-31, AX-32 |
| #26 | Split | AX-30, AX-33, AX-34 |
| #27 | Moves to beta. It touches replay deduplication, which is frozen, and its latency targets need real data. | B-06 |
| #28 | Split | AX-35 to AX-38 |
| #29 | Split | AX-20 to AX-25, AX-39 |
| #30, #31 closed | Stay closed. Add inventory tests so they cannot drift. | AX-45, AX-47 |
| #32 | Already satisfied by its own criteria: the upstream baseline shows 83 of 83 cases matching with none unsupported. Close after the inventory ticket; feature expansion is demand-driven. | AX-46, AX-47, B-07 |
| #33 closed | Closed by explicit rejection, which is acceptable for alpha. Make that explicit and test it; real implementation is demand-driven. | AX-48, B-07 |
| #34 | Replace with a generated matrix | AX-49 |
| #35 | Split. The 'at least one review' criterion cannot be met by a sole maintainer, so it is scoped to beta and the exception stays documented. | AX-50, AX-51, AX-52 |
| #36 | Split and decouple from #29, so alpha.2 ships early and fixes the broken quickstart. | AX-53, AX-54, AX-55 |
| #37 | Becomes a script, not a checklist | AX-60, AX-61 |
| #38 to #47 | Unchanged (beta and production). Their entry gate is AX-61. | none |

## 6. Micro-tickets for alpha exit

Start in parallel: AX-01, AX-02, AX-04, AX-05, AX-10, AX-20, AX-22, AX-30, AX-40, AX-45, AX-46, AX-48, AX-50, AX-51, AX-53. The longest chain is AX-10, AX-12, AX-13, then the work-reduction tickets, then AX-25, AX-60 and AX-61.

### Guardrails (EC6, EC7)

**AX-01 Coverage ratchet.** Blocked by: none.

- Red tests: `TestCoverageGate_FailsBelowFloor` (script test with a fixture profile under the floor).
- Edge and negative: a package missing from the floor file fails; lowering a floor fails the check.
- Done when: `make coverage` prints per-package coverage, `test/quality/coverage-floor.json` holds the baseline rounded down, the `coverage` CI job is required, and one follow-up ticket exists per critical package below 80%.

**AX-02 Mutation harness (includes the tool choice).** Blocked by: none.

- Red tests: `TestMutationReport_ParsesEfficacy` (fixture tool output gives the expected score); malformed output exits non-zero.
- Edge and negative: a tool missing from PATH gives a clear error, never a pass.
- Done when: two candidate Go mutation tools (for example gremlins and go-mutesting) are run on `internal/auth`, one is chosen by killed mutants and run time, its exact version is pinned the way CI pins govulncheck and zizmor, `make mutation PKG=./internal/auth` works, and the choice is recorded in `docs/quality.md`.

**AX-03 Mutation gate (nightly).** Blocked by: AX-02.

- Red tests: `TestMutationGate_FailsBelowFloor` (60% against a 70% floor fails); an allowlisted survivor passes; an allowlist entry with no reason fails.
- Edge and negative: a package with zero mutants is reported, not silently passed.
- Done when: a scheduled workflow runs on sqlgen, auth, config, httpapi and plan, baselines are recorded, and every package under 70% has a follow-up test ticket.

**AX-04 PR traceability and freeze guard.** Blocked by: none.

- Rule: a PR body must contain `Closes #N` or `Refs #N` for an issue labelled `phase:alpha-exit`, unless the commit type is fix, deps, docs, security or ci. A PR touching a frozen path (`internal/semctx`, `cmd/skopos-mcp`, `internal/checkpoint`, `cmd/skopos-checkpoint`, `deploy/systemd`) needs the label `frozen-exception` and a one-line reason.
- Red tests: a table test with eight fixtures (valid, missing reference, closed issue, non-alpha issue, exempt type, frozen path without label, frozen path with label, malformed body).
- Edge and negative: the API being unreachable fails closed. The workflow needs `issues: read` and `pull-requests: read`, reviewed under the existing zizmor policy.

**AX-05 Label model and issue lint.** Blocked by: none.

- Create `phase:alpha-exit`, `phase:beta`, `phase:prod`, `ec:1` to `ec:7`, `frozen`, `backlog:post-alpha`, `type:micro`, `type:spike`; relabel #24 to #47; drop `ready-for-agent` from blocked or human-only tickets (#40, #45, #46, #47).
- Red tests: an alpha-exit issue with no `ec:*` label fails; with two fails; a missing 'Blocked by' section fails; a blocked-by cycle fails.
- Done when: `make issues-lint` passes against the live repo (read-only) and runs inside `alpha-exit-check`.

**AX-06 Red-first commit check.** Blocked by: AX-04.

- For PRs labelled `type:micro`, the first commit may only touch `*_test.go`, `testdata/` or fixtures (read from the PR's commit list so it survives squash merges).
- Red tests: a test-only first commit passes; implementation in the first commit fails; a single-commit PR fails.
- Edge and negative: docs and ci changes are exempt with the label `red-first-exempt`.

### Observability (EC4)

**AX-10 Shared metrics registry and `/metrics` merge.** Blocked by: none. Freeze exception (logged).

- Today `GET /metrics` is registered by `internal/semctx` and exposes only AI-context coverage gauges. Move the route to the gateway and register those gauges in the same registry, unchanged.
- Dependency decision: use `prometheus/client_golang`, already in go.sum as an indirect dependency, and update `docs/dependencies.md`. If you veto it, use stdlib text exposition.
- Red tests: `TestMetrics_ExposesBuildInfo`, `TestMetrics_ContextGaugesUnchanged` (golden of the old output), `TestMetrics_RequiresAuthWhenEnabled`, `TestFrontendMetrics_NotProxiedToWorkers`.
- Edge and negative: output passes promlint; concurrent scrapes are clean under `-race`.

**AX-11 HTTP RED metrics and unsupported-query counter.** Blocked by: AX-10.

- Metrics: `skopos_http_requests_total{head,route,code}`, `skopos_http_request_duration_seconds`, `skopos_http_in_flight`, `skopos_unsupported_queries_total{head,feature}`.
- Red tests: `TestRouteLabelUsesPattern`, `TestUnknownPathsDoNotGrowSeries` (1,000 random paths add at most one series), `TestUnsupportedFeatureCounted` (a PromQL subquery and a TraceQL structural operator each add one with a stable label).
- Edge and negative: label values come from a closed set; a recovered panic counts as a 500.
- Why: the unsupported counter is the demand signal that decides what to build after alpha.

**AX-12 ClickHouse accounting metrics.** Blocked by: AX-10.

- Capture the ClickHouse HTTP summary header (rows and bytes read; confirm its name against the pinned server in the test) and export `skopos_clickhouse_queries_total{head,result}`, a duration histogram, `read_rows_total` and `read_bytes_total`.
- Red tests (integration, real ClickHouse): a known fixture query increases rows read by exactly the value ClickHouse reports; a ClickHouse error counts as result=error without a panic.
- Edge and negative: a missing header counts zero rows and logs at debug.

**AX-13 Per-request query recorder (test seam).** Blocked by: AX-12.

- A recorder in the ClickHouse client records statements, tables touched and rows read for one request via context; available to tests only.
- Red tests: records N statements against a fake server; 200 parallel requests never see each other's records under `-race`; a nil recorder is a no-op.
- Why: this is what makes STG-4 possible for AX-31 to AX-38.

**AX-14 Optional ServiceMonitor and scrape annotations (cut-line).** Blocked by: AX-10.

- Helm value `metrics.serviceMonitor.enabled`, false by default.
- Red tests (Helm render): the default renders no ServiceMonitor; enabled renders the right kind, port and path.
- Edge and negative: enabled with an empty selector fails `helm template` with a clear message.

### Stability: safety first (EC3)

**AX-20 Timeout invariants.** Blocked by: none.

- Today `WriteTimeout` is a hard-coded 30 s in `cmd/gateway/main.go`, while the query timeout and the ClickHouse max execution time are configurable; the benchmark shows heavy requests dropped as connection resets at 30 s.
- Derive `WriteTimeout = QueryTimeout + 5 s` and refuse to start when `MaxExecutionTime > QueryTimeout`.
- Red tests: `TestServerTimeouts_DerivedFromQueryTimeout`, `TestConfig_RejectsExecutionTimeAboveQueryTimeout`.
- Edge and negative: equal values are accepted; zero and negative are rejected.

**AX-21 Deadline to structured error, per head.** Blocked by: AX-20.

- The per-request deadline equals the query timeout. On expiry each head answers in its own error shape as the pinned reference engine does (Prometheus JSON with `errorType: timeout`; Loki and Tempo as the references return, verified by the differential harness rather than guessed).
- Red tests: a slow fake ClickHouse (1 s) with a 200 ms timeout, one test per head: the client gets an HTTP response, not a reset, within 200 ms plus margin; the body parses in that head's format; the fake sees the query cancelled.
- Edge and negative: a client disconnect cancels the query; a deadline during response writing never writes headers twice. Split per head if this exceeds 400 lines.

**AX-22 `internal/admission` limiter.** Blocked by: none.

- Bounded in-flight slots per head plus a bounded wait queue with a maximum wait.
- Red tests: `TestLimiter_RejectsBeyondCapAndQueue`, `TestLimiter_ReleasesOnPanicAndCancel`, `TestLimiter_NoGoroutineLeak`, `TestLimiter_FIFONoStarvation`.
- Edge and negative: cap 1 and a large cap; queue 0; zero wait; negative values rejected. In mutation scope.

**AX-23 Wire the limiter.** Blocked by: AX-11, AX-21, AX-22.

- Env `SKOPOS_MAX_INFLIGHT`, `SKOPOS_QUEUE_DEPTH`, `SKOPOS_QUEUE_WAIT`. `/healthz`, `/readyz` and `/metrics` are exempt. A rejection uses the head's error shape plus `Retry-After` and increments `skopos_admission_rejected_total{head}`.
- Red tests: integration with a slow fake backend, cap 4 and queue 4, a burst of 20 concurrent requests: at most 8 admitted, the rest rejected with Retry-After, 0 connection errors, probes still answer; Helm render maps values to env and rejects invalid combinations.
- Edge and negative: in distributed mode the frontend passes a worker's rejection through unchanged, not as a 502.

**AX-24 Memory budget.** Blocked by: AX-23.

- The chart sets `GOMEMLIMIT` as a fraction of `resources.limits.memory` (override with `gateway.goMemLimit`). Startup warns when worst-case buffering (`MaxInFlight × MaxResponseBytes`) exceeds the configured memory limit.
- Red tests: `TestWorstCaseBuffering_WarnsWhenOverLimit`; Helm render: limits set gives a `GOMEMLIMIT` at or under the limit, no limits gives no variable, an invalid unit fails with a clear message.
- Edge and negative: Mi, Gi, M and G units.

**AX-25 Concurrency gate (EC3 evidence).** Blocked by: AX-11, AX-23, AX-24.

- Extend the `test/bench` concurrent run with assertions (`make bench-gate`): 0 transport errors, every non-2xx is a 429 or 503 with Retry-After, no OOMKill, gateway peak memory under its limit. Latency goes to JSON, not a gate. Add a scaled-down deterministic CI job using the slow fake backend.
- Red tests: harness self-tests against a stub that drops connections (must fail), one that returns a bare 500 (must fail), and one that sheds correctly (must pass).
- Done when: the result is written to a generated section of `docs/benchmarks.md`.

### Work reduction on the endpoints that failed (EC3)

These target the endpoints that produced errors in the 30-client benchmark: label values, Tempo search, Tempo tags and the 6 h range query. They all use the recorder from AX-13, so the perf checks are deterministic (STG-4).

**AX-30 `internal/ttlcache`.** Blocked by: none.

- A generic bounded TTL cache with single-flight loading and an injectable clock.
- Red tests: 100 goroutines asking for one key cause one loader call; TTL expiry with a fake clock; the maximum entry count evicts instead of growing; a loader error is not cached.
- Edge and negative: TTL 0 disables caching; clean under `-race`. In mutation scope.

**AX-31 Metric-name to table routing (parent #25).** Blocked by: AX-13.

- An exact `__name__` matcher queries only the tables that can hold that name. A regex or absent `__name__` falls back to all tables. Derived histogram and summary names (`_bucket`, `_sum`, `_count`, quantile) route to the right table.
- Red tests: `TestGaugeSelector_DoesNotTouchHistogramOrSummary` (the recorder shows only the gauge table), golden SQL, `TestUnknownMetric_ReadsZeroRows`; the 212-case PromQL oracle stays at 0 divergences.
- Edge and negative: a name present in two tables queries both; `!=` and alternation matchers fall back safely.

**AX-32 Metadata and emptiness cache (parent #25).** Blocked by: AX-30, AX-31.

- Cache metric type, unit and description plus a per-table non-empty flag, with a documented TTL (`SKOPOS_METADATA_TTL`, proposed default 30 s).
- Red tests: an empty table causes 0 data scans (recorder); a table that gains data is picked up after the TTL (fake clock); a metadata error returns an error, never a stale empty result.
- Edge and negative: concurrent first requests cause one metadata query.

**AX-33 Label values pushdown (parent #26).** Blocked by: AX-13, AX-30.

- `/api/v1/label/<name>/values` (including `__name__`) is computed with `SELECT DISTINCT` in ClickHouse across the relevant tables, honoring `match[]` and the time range. Go no longer fetches series to compute it.
- Red tests: a property test on a randomized dataset where the new path equals the old Go path (kept until the old one is removed); no duplicates or drops for gauge, sum and histogram labels; a failure on the second of three table queries returns an API error, never a partial result; rows read is bounded (recorder).
- Edge and negative: empty result; the limit is respected at high cardinality.

**AX-34 Label names pushdown and short cache (parent #26, cut-line).** Blocked by: AX-33.

- `/api/v1/labels` gets the same treatment, with a cache whose TTL and invalidation are documented and whose size is bounded.
- Red tests: results equal the old path; a second call inside the TTL issues 0 statements; the TTL expires correctly.
- Edge and negative: the cache key includes `match[]` and the time-range bucket; an error clears the entry.

**AX-35 Tempo intrinsics pushdown (parent #28).** Blocked by: AX-13.

- Span intrinsics (name, duration, status, kind) inside `{ ... }` compile to SQL `WHERE`, so rows are filtered before they reach Go.
- Red tests: golden SQL per operator; the 55-case Tempo corpus stays at 0 diffs; rows read is bounded by matching spans (recorder).
- Edge and negative: regex operators whose semantics differ between RE2 and ClickHouse fall back to an explicit documented limit; duration units; extend the fuzz job to the new emitter paths (property: user-supplied text is never concatenated into SQL).

**AX-36 Tempo attribute filters pushdown (parent #28).** Blocked by: AX-35.

- `span.` and `resource.` attribute filters (map lookups) in SQL, with type-coercion rules documented.
- Red tests: as AX-35, plus the missing-versus-empty-versus-null cases from the corpus; the fuzz property is extended to attribute keys.
- Edge and negative: attribute keys with dots, quotes and unicode; numeric versus string comparison.

**AX-37 Tempo ordering and limit pushdown (parent #28).** Blocked by: AX-35.

- `limit`, `spss` and newest-first ordering are resolved in ClickHouse before rows reach Go.
- Red tests: `TestSearchLimit_ReadsBoundedRows` (recorder); ordering matches the reference on the corpus.
- Edge and negative: limit 0, 1 and the maximum; ties in start time are ordered by a stable secondary key.

**AX-38 Tempo tag discovery pushdown (parent #28).** Blocked by: AX-30, AX-35.

- Tags, scopes and tag values computed in ClickHouse with a limit and a short cache.
- Red tests: the 5 scope cases and 10 tag-value cases in the corpus are unchanged; rows read is bounded; a second call inside the TTL issues 0 statements.
- Edge and negative: empty dataset; a high-cardinality tag is capped.

**AX-39 Spike: why the 6 h range query fails under load (cut-line).** Blocked by: AX-11, AX-12.

- Time-boxed to one day: pprof plus the new metrics from the 30-client run. Deliverable is `docs/perf/range-query-findings.md` with ranked causes, a reproducible `make profile-range`, and at least one micro-ticket per cause.
- Red tests: with the profiling flag off there is no pprof route; with it on, the route requires auth.
- Done when: another person can reproduce the findings from the document. No other code merges.

### Grafana plug-and-play and correct-or-explicit-error (EC1, EC2)

**AX-40 Grafana harness.** Blocked by: none.

- A compose stack with a Grafana image pinned by digest, the gateway, ClickHouse and seed data. It provisions the three datasources from `deploy/examples/grafana/provisioning` and asserts each datasource's health check passes. Grafana runs as a black-box container, the same pattern as the pinned Loki and Tempo references; no code is linked.
- Red tests: harness self-tests: a stopped gateway fails with a clear message; wrong credentials with auth enabled fail.
- Edge and negative: runs on a standard CI runner without privileged mode.

**AX-41 Prometheus journey.** Blocked by: AX-40.

- Through Grafana's own query API: an instant query, a range query with `$__rate_interval` resolved, a `label_values()` variable query and the metric browser's label calls, with frames compared to the reference Prometheus on the same seed.
- Red tests: each step asserts HTTP 200 and frame equality with the reference.
- Edge and negative: a deliberately unsupported query (a subquery) produces an error message Grafana can show, not an empty panel; a panel with six or more series.

**AX-42 Loki journey.** Blocked by: AX-40, AX-46.

- Labels and label values, a stream selector with a line filter and `| json`, and the log-volume `count_over_time` query that Explore sends, compared to the reference Loki.
- Red tests: as AX-41, one assertion per step.
- Edge and negative: an unsupported stage returns an explicit error.

**AX-43 Tempo journey.** Blocked by: AX-40.

- Search by tags and by TraceQL, trace by ID as Grafana requests it, tag and tag-value autocomplete, compared to the reference Tempo.
- Red tests: as AX-41.
- Edge and negative: a structural operator returns an explicit error.

**AX-44 OBI RED dashboard as the finite acceptance list.** Blocked by: AX-40.

- Commit `deploy/examples/grafana/dashboards/obi-red.json`: request rate, errors and duration percentiles from `http_server_request_duration_seconds_*` and the client-side equivalents. This dashboard is the product promise (no-code observability into Grafana), so it is the acceptance test.
- Red tests: the harness extracts every panel target and runs it through Grafana's query API on OBI-shaped seed data: 0 errors, at least one non-empty series per panel. Adding a panel with an unsupported function fails CI and names the function.
- Edge and negative: dashboard variables (namespace, service) resolve through `label_values`. The real OBI end-to-end run stays manual; the seed data uses OBI's metric and label names.

**AX-45 PromQL function inventory.** Blocked by: none.

- A test iterates every function in the vendored Prometheus parser's function table and classifies each as supported or explicit-error. Upgrading Prometheus to a version that adds a function fails the test until it is classified.
- Red tests: `TestEveryParserFunctionIsClassified`.
- Edge and negative: every explicit-error function returns a Prometheus-shaped error, never a 200 with an empty result.

**AX-46 LogQL richer fixture.** Blocked by: none.

- The upstream baseline seeds 30 near-identical log lines, which its own Limitations section calls out as weak evidence. Seed varied levels, JSON and logfmt fields, multi-line entries, unicode and empty bodies.
- Red tests: `TestFixtureExercisesParsers` (parser-stage cases must yield non-trivial, distinct results); the baseline is re-run and every mismatch becomes a fix ticket.
- Edge and negative: entries with identical timestamps; a very long line.

**AX-47 LogQL inventory (parent #32).** Blocked by: AX-46.

- Enumerate the stages and functions of the pinned Loki grammar (3.5.7) as supported or explicit-error, so any unsupported construct (for example `unwrap` or `decolorize`, whichever apply) errors instead of being ignored.
- Red tests: `TestEveryLogQLConstructClassified`.
- Edge and negative: the error body is in Loki's shape. Done when #32 is closed as satisfied, with feature work deferred to B-07.

**AX-48 TraceQL inventory (parent #33).** Blocked by: none.

- Make the rejected-forms table in `docs/traceql-support.md` executable: the test parses the table and checks that each example returns the documented error.
- Red tests: `TestRejectedFormsMatchDocs`.
- Edge and negative: adding an undocumented rejected form fails the test; whitespace and nesting variants of each form.

**AX-49 Generated support matrix and limits.** Blocked by: AX-45, AX-47, AX-48.

- `docs/support-matrix.md` and a Limits section are generated from the inventory results and the config defaults (maximum range, scan-row cap, response-size cap, in-flight cap). CI fails on a diff. This replaces hand-written status claims, such as the note that the k3d smoke test is not automated.
- Red tests: a golden test for the generator; changing a config default without regenerating fails CI; a matrix row with no test reference makes the generator error.

### Release (EC5)

**AX-50 Code-scanning gate.** Blocked by: none.

- `scripts/check-code-scanning.sh` fails when any open alert is high or critical; the release workflow calls it.
- Red tests: stubbed API fixtures: 0 alerts passes, 1 high fails, an API error fails closed.

**AX-51 OSV exceptions with expiry.** Blocked by: none.

- `docs/security/osv-exceptions.yaml` entries need an id, a reason and an expiry date; a script fails on an expired or reason-less entry.
- Red tests: fixtures for valid, expired, missing reason and unknown id.

**AX-52 Ruleset as code (cut-line).** Blocked by: none.

- Store the `main` ruleset in `docs/security/ruleset.json` and check that its required checks equal the job names in `ci.yml`. Record the solo-maintainer review exception already accepted in `docs/openssf-scorecard.md`.
- Red tests: renaming a CI job fails the check; adding a required check only in the ruleset fails it.

**AX-53 Release verification script (parent #36).** Blocked by: none.

- `make release-verify TAG=` verifies image and chart signatures against the release workflow identity, checks that SBOM and provenance exist, and checks that the digest in `values.release.yaml` equals the image digest.
- Red tests: an unsigned image fails; a digest mismatch fails; a missing SBOM fails; the correct fixture passes.

**AX-54 README quickstart doc-test (parent #36).** Blocked by: AX-53.

- Extract the README shell blocks tagged `quickstart`, run them in a clean k3d against the published chart, and fail if the README names a chart version that does not exist in the registry. Today the README says 0.1.0-alpha.2 while the Releases page lists only `skopos-0.1.0-alpha.1`.
- Red tests: it fails today, which is the point; a malformed version string fails with a clear message.

**AX-55 Cut alpha.2 (parent #36).** Blocked by: AX-50, AX-51, AX-53, AX-54.

- Tag `v0.1.0-alpha.2` through the release workflow and attach the AX-53 output. This is deliberately decoupled from #29 and #34 so the quickstart works now; the exit candidate reuses the same pipeline.
- Done when: AX-53 and AX-54 are green against the published artifacts and the Dockerfile `ARG VERSION` default is updated.

### Gate (EC7)

**AX-60 `make alpha-exit-check` (parent #37).** Blocked by: every EC evidence command.

- Aggregates the machine-readable results of EC1 to EC7 into one table, exits non-zero if any fails, and writes `docs/alpha-exit-report.md` (generated).
- Red tests: fixture bundles: all pass gives 0; each single failing EC gives non-zero and names it; a missing evidence file fails (fail closed); evidence older than the configured age fails.

**AX-61 Declare exit (parent #37).** Blocked by: AX-60.

- A script updates the generated status sections in the README and `docs/beta-gates.md`, the Beta milestone is opened, and the unfreeze review (B-08) starts.
- Done when: AX-60 prints 7/7 on a clean checkout of the exit commit, linked in #37.

## 7. Post-alpha tickets

None of these start before AX-61. They exist so that each idea you are tempted to build now has a place to wait.

- **B-01 Scale harness.** A parameterized dataset generator (series, services, log rate, span rate) with a 10k-series, 10-service profile, one command, deterministic seed. Red tests: the same seed gives an identical dataset hash; counts match the profile; an invalid profile fails.
- **B-02 10k profile run and ranked bottleneck report.** Run on the reference host with the AX-11 and AX-12 metrics and pprof. The top causes become micro-tickets created from the data, not guessed. Entry: B-01.
- **B-03 100k-series, 50-service profile.** The same method. Entry: B-02, plus pilot data from #40 where available.
- **B-04 Latency budgets from data.** Set p50 and p95 per query kind from B-02, B-03 and the pilot, then add a relative regression gate to the nightly bench. Replaces the numeric targets in old #27 and #28.
- **B-05 Mutation scope expansion.** Pilot mutation testing on prom, loki and tempo critical files and decide the floors.
- **B-06 FINAL and deduplication cost (was #27).** Write an ADR first, then implement. Entry: B-02 shows it among the top three costs, and B-08 has decided the checkpoint's status.
- **B-07 LogQL and TraceQL expansion (was #32 and #33).** Demand-driven: entry is `skopos_unsupported_queries_total` from a real deployment showing the feature is actually requested. One micro-ticket per feature.
- **B-08 Unfreeze review.** For AI context and MCP, the checkpoint WAL and systemd, decide keep, maintain-only, or extract into a separate module or repository. Record the decision as an ADR.

## 8. Rules, cut line and assumptions to veto

**Anti-FOMO rules**

1. No PR without a trace to an exit criterion (enforced by AX-04).
2. New ideas get the label `backlog:post-alpha` and are looked at only at AX-61.
3. Scope changes only by a PR to this document that names the reason and the EC it serves.
4. Set the exit date now. If it slips, cut from the cut line below; do not extend the date.
5. Features are decided by observed demand: the unsupported-query counter and the bench reports choose the beta work.

**Cut line.** If the time-box is hit, cut in this order: AX-14, AX-52, AX-39, AX-34.

**Assumptions I made for you (veto any)**

1. \#27 moves to beta (B-06) because it touches frozen replay deduplication and needs real data.
2. \#32 and #33 count as satisfied through explicit errors; feature expansion is demand-driven (B-07).
3. Latency is recorded but not gated at alpha exit; EC3 is about safe failure, not speed.
4. AX-10 uses `prometheus/client_golang` as a direct dependency.
5. AX-10 moves `/metrics` from the AI-context package to the gateway, a logged freeze exception.
6. The 70% mutation floor and 80% coverage target are proposals until AX-01 and AX-02 measure baselines.
7. A pinned Grafana image runs in CI as a black-box container.
8. The solo-maintainer review exception stays through alpha; the 'at least one review' rule from #35 is scoped to beta.
9. alpha.2 ships before the exit, decoupled from #29 and #34.
10. Kibana is out of scope.
