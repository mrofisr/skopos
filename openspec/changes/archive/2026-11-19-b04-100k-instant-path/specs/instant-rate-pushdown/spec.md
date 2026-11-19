# Spec Delta

## Purpose

Computes instant rate queries in the store with bounded, bucket-pruned reads
and reference-identical results, so high-cardinality workloads stay correct,
predictable, and explicitly failing rather than silently empty.

## ADDED Requirements

### Requirement: Bucket-pruned instant reads
Instant rate pushdown SHALL restrict its scan to the five-minute buckets that
intersect the query window. Rows read SHALL be bounded by the samples in those
buckets, not by the whole hour containing the window.

#### Scenario: Five-minute window at the 100k profile
- **WHEN** an instant sum(rate(...[5m])) runs against the 100k reference profile
- **THEN** query accounting shows rows read on the order of the samples in the touched buckets (about 600k), not the 6.36M rows of the hour-keyed baseline

#### Scenario: Window crossing a bucket boundary
- **WHEN** the rate window spans two five-minute buckets
- **THEN** only the rows of those two buckets are read

#### Scenario: Read bound is deterministic in PR CI
- **WHEN** the pushdown tests run against the fixture dataset
- **THEN** the recorded rows read match the checked-in bound without relying on wall-clock time

### Requirement: Reference parity
The pushdown SHALL return results identical to the reference Prometheus engine
for every supported instant rate construct, including counter resets,
extrapolation boundaries, and grouping by service.

#### Scenario: Differential suites stay at zero divergences
- **WHEN** the 212-case differential oracle and the official compliance suite run
- **THEN** there are 0 divergences and 0 unexpected failures

#### Scenario: Counter reset inside the window
- **WHEN** a counter resets within the rate window
- **THEN** the pushdown result equals the reference result

#### Scenario: Grouped rate by service
- **WHEN** sum by (service_name) (rate(...)) runs
- **THEN** every per-service value equals the reference value

### Requirement: Parameterized SQL safety
User-supplied text SHALL never be concatenated into emitted SQL. Selector
filters, time windows, and bucket bounds SHALL be bound as parameters.

#### Scenario: Fuzz property covers the bucket-aware paths
- **WHEN** the emitter fuzz job runs against the bucket-aware rate and sample paths
- **THEN** no user-supplied text appears as a SQL literal

#### Scenario: Metacharacters in a selector
- **WHEN** a selector contains quotes or SQL metacharacters
- **THEN** the query returns the correct result or an explicit error, never altered SQL semantics

### Requirement: Explicit errors, never silent empties
When the pushdown cannot execute within the configured budgets, the system
SHALL return an explicit error in the Prometheus shape, never an empty 200.

#### Scenario: Row budget exceeded
- **WHEN** the estimated read exceeds the sample row budget
- **THEN** the answer is an explicit 4xx error before execution starts

#### Scenario: Store error surfaces as an API error
- **WHEN** ClickHouse returns an error for the pushdown query
- **THEN** the gateway answers with a Prometheus-shaped error, not an empty result
