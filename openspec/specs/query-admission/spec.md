# query-admission Specification

## Purpose

Bounds concurrent query work per signal head so expensive queries cannot
saturate the store, while every rejection stays deliberate, typed, retryable,
and observable.

## Requirements

### Requirement: Work-based admission cost
The system SHALL weight each admitted query by its estimated work. Instant
queries SHALL be weighted by the cardinality and time window of the data they
read, so an expensive instant rate query consumes more admission budget than a
cheap instant query. Weighting SHALL be configurable and disableable.

#### Scenario: Expensive instant rate weighs more
- **WHEN** an instant rate query over a high-cardinality selector and a cheap instant query are submitted with weighting enabled
- **THEN** the rate query consumes strictly more admission budget than the cheap query

#### Scenario: Cheap queries keep the minimum weight
- **WHEN** a query that reads little data is submitted with weighting enabled
- **THEN** its admission weight is the minimum of one slot

#### Scenario: Unknown cardinality falls back safely
- **WHEN** the cardinality estimate for an instant rate query is unavailable
- **THEN** the query is admitted with a configured conservative weight, never with weight zero

#### Scenario: Weighting can be disabled
- **WHEN** the operator disables work-based weighting
- **THEN** every request weighs one slot and admission is count-based

#### Scenario: Invalid cost configuration is rejected
- **WHEN** a negative or malformed cost configuration is supplied
- **THEN** startup fails with a clear error instead of running with undefined weights

### Requirement: Deliberate rejection contract
When admission cannot admit a request, the system SHALL reject it in the
head's error shape with a Retry-After hint, SHALL count the rejection, and
SHALL NOT drop the connection or return an unclassified 5xx.

#### Scenario: Capacity rejection is typed and retryable
- **WHEN** the in-flight budget and the wait queue are exhausted
- **THEN** the Prometheus head answers 503 with errorType unavailable, the Loki and Tempo heads answer 429, each with Retry-After, and the admission rejection counter increments

#### Scenario: Over-capacity query is a client error
- **WHEN** a single request's weight exceeds the total admission capacity
- **THEN** the answer is 400 with a bad-data error in the head's shape, not a retryable capacity rejection

#### Scenario: Probes are exempt
- **WHEN** /healthz, /readyz, or /metrics is requested while admission is full
- **THEN** the request is served without admission

#### Scenario: Distributed rejection passes through
- **WHEN** a query worker rejects a request in distributed mode
- **THEN** the frontend returns the worker's rejection unchanged, not a 502

### Requirement: Budget release on completion and failure
Admitted budget SHALL be released when the request finishes, including handler
panics and client disconnects, so admission capacity cannot leak.

#### Scenario: Panic releases budget
- **WHEN** a handler panics after admission
- **THEN** the budget is released and subsequent requests are admitted

#### Scenario: Disconnect releases budget
- **WHEN** the client disconnects while the query is running
- **THEN** the budget is released and the queued waiters are served

#### Scenario: Queue wait expires without leaking
- **WHEN** a queued request exceeds its maximum wait or its context is cancelled
- **THEN** the request is rejected or cancelled and no budget remains held for it
