# metrics-storage Specification

## Purpose

Defines how metric samples are physically ordered in ClickHouse and migrated
between key shapes, so time-bounded reads prune to small row ranges while
replay deduplication stays intact across deployment profiles.

## Requirements

### Requirement: Five-minute time-bucket sort key
The system SHALL order every metrics table by a sort key whose leading time
component is a five-minute bucket of the sample timestamp, followed by the
metric name, and SHALL retain the sample timestamp and the replay row hash in
the key for the reliable profile.

#### Scenario: Fresh install renders the bucketed key
- **WHEN** the chart schema is applied to a fresh database in either the reliable or the fast profile
- **THEN** all five metrics tables have a sort key starting with the five-minute bucket of the sample time and containing the metric name and the sample timestamp

#### Scenario: Reliable profile keeps the row hash in the key
- **WHEN** the schema is rendered with deduplication enabled
- **THEN** each metrics sort key also contains the replay row hash

### Requirement: Online re-key migration
The system SHALL migrate existing hour-keyed metrics tables to the bucketed
key during upgrade without stopping ingest, SHALL run for both the reliable
and the fast profile, and SHALL be safe to re-run.

#### Scenario: Upgrade preserves rows and results
- **WHEN** a database with hour-keyed metrics tables is upgraded
- **THEN** every row is present in the re-keyed tables and query results are unchanged

#### Scenario: Ingest continues during migration
- **WHEN** samples arrive while the migration copies rows
- **THEN** inserts are accepted or retried, and the samples are present after the migration completes

#### Scenario: Re-run is a no-op
- **WHEN** the migration runs again on a database whose tables already use the bucketed key
- **THEN** it completes without copying rows or changing data

#### Scenario: Interrupted migration recovers
- **WHEN** a previous migration was interrupted and left a renamed-aside table
- **THEN** re-running the migration completes the copy and removes the leftover table

#### Scenario: Fast profile is re-keyed too
- **WHEN** deduplication is disabled and the metrics tables still use the hour key
- **THEN** the upgrade re-keys them to the bucketed key

### Requirement: Replay-dedup identity preserved
The re-keyed reliable-profile tables SHALL preserve replay-dedup identity:
replayed duplicates of the same sample collapse to one row on read, and
distinct samples remain distinct.

#### Scenario: Duplicate replay collapses after migration
- **WHEN** the same sample row is inserted twice into a re-keyed reliable-profile table
- **THEN** a deduplicated read returns the sample once

#### Scenario: Distinct samples survive dedup
- **WHEN** two samples differ in timestamp or value
- **THEN** both rows remain after deduplication
