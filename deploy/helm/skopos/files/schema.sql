-- Schema pinned to the OTel Collector ClickHouse exporter at
-- opentelemetry-collector-contrib v0.164.0.
--
-- The exporter is configured with create_schema: false. This job owns DDL.
-- Columns and types match the pinned exporter; indexes, TTL, the
-- ReplacingMergeTree engine, and the replay-dedup sort keys are customized.
-- Logs and metrics add a materialized _skopos_row_hash so a replayed OTLP
-- record maps to the same ClickHouse row.
-- ClickHouse 26.8 is tested; the logs table uses full-text indexes that
-- require ClickHouse 26.2+.

CREATE DATABASE IF NOT EXISTS `{{ .Values.clickhouse.database }}`;

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.traces }}` (
    Timestamp DateTime64(9) CODEC(Delta, ZSTD(3)),
    TraceId String CODEC(ZSTD(3)),
    SpanId String CODEC(ZSTD(3)),
    ParentSpanId String CODEC(ZSTD(3)),
    TraceState String CODEC(ZSTD(3)),
    SpanName LowCardinality(String) CODEC(ZSTD(3)),
    SpanKind LowCardinality(String) CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    SpanAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    Duration UInt64 CODEC(ZSTD(3)),
    StatusCode LowCardinality(String) CODEC(ZSTD(3)),
    StatusMessage String CODEC(ZSTD(3)),
    Events Nested (
        Timestamp DateTime64(9),
        Name LowCardinality(String),
        Attributes Map(LowCardinality(String), String)
    ) CODEC(ZSTD(3)),
    Links Nested (
        TraceId String,
        SpanId String,
        TraceState String,
        Attributes Map(LowCardinality(String), String)
    ) CODEC(ZSTD(3)),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_span_attr_key mapKeys(SpanAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_span_attr_value mapValues(SpanAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_duration Duration TYPE minmax GRANULARITY 1
) ENGINE = ReplacingMergeTree()
PARTITION BY toDate(Timestamp)
-- (TraceId, SpanId) is the OTel span identity. Replay re-sends the original
-- payload, so a replayed span has the same key and is collapsed by FINAL or a
-- background merge.
ORDER BY (ServiceName, SpanName, toDateTime(Timestamp), TraceId, SpanId)
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.ttl.tracesDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1, enable_block_number_column = 1, enable_block_offset_column = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.traces }}_trace_id_ts` (
    TraceId String CODEC(ZSTD(3)),
    Start DateTime CODEC(Delta, ZSTD(3)),
    End DateTime CODEC(Delta, ZSTD(3)),
    INDEX idx_trace_id TraceId TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = ReplacingMergeTree()
PARTITION BY toDate(Start)
ORDER BY (TraceId, Start, End)
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(Start) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(Start) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(Start) + toIntervalDay({{ .Values.clickhouse.ttl.tracesDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE MATERIALIZED VIEW IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.traces }}_trace_id_ts_mv`
TO `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.traces }}_trace_id_ts`
AS SELECT
    TraceId,
    min(Timestamp) AS Start,
    max(Timestamp) AS End
FROM `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.traces }}`
WHERE TraceId != ''
GROUP BY TraceId;

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.logs }}` (
    `Timestamp` DateTime64(9) COMMENT 'Event timestamp with nanosecond precision' CODEC(Delta(8), ZSTD(3)),
    `TraceId` String COMMENT 'W3C trace identifier' CODEC(ZSTD(3)),
    `SpanId` String COMMENT 'W3C span identifier' CODEC(ZSTD(3)),
    `TraceFlags` UInt8 COMMENT 'W3C trace flags',
    `SeverityText` LowCardinality(String) COMMENT 'Log severity as text' CODEC(ZSTD(3)),
    `SeverityNumber` UInt8 COMMENT 'Log severity as number (1-24)',
    `ServiceName` LowCardinality(String) COMMENT 'Service that emitted the log' CODEC(ZSTD(3)),
    `Body` String COMMENT 'Log message body' CODEC(ZSTD(3)),
    `ResourceSchemaUrl` LowCardinality(String) COMMENT 'Schema URL for the resource' CODEC(ZSTD(3)),
    `ResourceAttributes` Map(LowCardinality(String), String) COMMENT 'Resource attributes as key-value pairs' CODEC(ZSTD(3)),
    `ScopeSchemaUrl` LowCardinality(String) COMMENT 'Schema URL for the instrumentation scope' CODEC(ZSTD(3)),
    `ScopeName` String COMMENT 'Instrumentation scope name' CODEC(ZSTD(3)),
    `ScopeVersion` LowCardinality(String) COMMENT 'Instrumentation scope version' CODEC(ZSTD(3)),
    `ScopeAttributes` Map(LowCardinality(String), String) COMMENT 'Instrumentation scope attributes' CODEC(ZSTD(3)),
    `LogAttributes` Map(LowCardinality(String), String) COMMENT 'Log record attributes' CODEC(ZSTD(3)),
    `EventName` String COMMENT 'Event name for log records representing events' CODEC(ZSTD(3)),
    `__otel_materialized_k8s.cluster.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.cluster.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.container.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.container.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.deployment.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.deployment.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.namespace.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.namespace.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.node.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.node.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.pod.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.pod.name'] CODEC(ZSTD(3)),
    `__otel_materialized_k8s.pod.uid` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.pod.uid'] CODEC(ZSTD(3)),
    `__otel_materialized_deployment.environment.name` LowCardinality(String) MATERIALIZED ResourceAttributes['deployment.environment.name'] CODEC(ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    -- Logs have no OTel record id. Hash the whole row (with mapSort so
    -- attribute order is irrelevant) to give replayed records a stable key.
    -- Two byte-identical log records are indistinguishable and collapse.
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        Timestamp,
        TraceId,
        SpanId,
        TraceFlags,
        SeverityText,
        SeverityNumber,
        ServiceName,
        Body,
        ResourceSchemaUrl,
        mapSort(ResourceAttributes),
        ScopeSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        mapSort(LogAttributes),
        EventName
    ),
{{ end }}
    INDEX idx_trace_id TraceId TYPE text(tokenizer = 'array'),
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_log_attr_value mapValues(LogAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_body Body TYPE text(tokenizer = 'splitByNonAlpha'),
    INDEX idx_event_name EventName TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(Timestamp)
-- The hash is the final key component so replaying the same OTLP record is
-- collapsed by FINAL or a background merge.
ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(Timestamp) + toIntervalDay({{ .Values.clickhouse.ttl.logsDays }})
{{- end }}
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1, enable_block_number_column = 1, enable_block_offset_column = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.metricsGauge }}` (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ResourceSchemaUrl String CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(3)),
    ScopeSchemaUrl String CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    MetricName LowCardinality(String) CODEC(ZSTD(3)),
    MetricDescription String CODEC(ZSTD(3)),
    MetricUnit String CODEC(ZSTD(3)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    StartTimeUnix DateTime CODEC(Delta, ZSTD(3)),
    TimeUnix DateTime CODEC(Delta, ZSTD(3)),
    Value Float64 CODEC(ZSTD(3)),
    Flags UInt32 CODEC(ZSTD(3)),
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime,
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        mapSort(ResourceAttributes),
        ResourceSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        ScopeDroppedAttrCount,
        ScopeSchemaUrl,
        ServiceName,
        MetricName,
        MetricDescription,
        MetricUnit,
        mapSort(Attributes),
        StartTimeUnix,
        TimeUnix,
        Value,
        Flags,
        arrayMap(m -> mapSort(m), Exemplars.FilteredAttributes),
        Exemplars.TimeUnix,
        Exemplars.Value,
        Exemplars.SpanId,
        Exemplars.TraceId
    ),
{{ end }}
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(TimeUnix)
-- With dedup enabled, the row hash is the final key component. Attribute maps
-- are mapSort'ed so insertion order cannot split a duplicate.
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.ttl.metricsDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.metricsSum }}` (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ResourceSchemaUrl String CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(3)),
    ScopeSchemaUrl String CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    MetricName LowCardinality(String) CODEC(ZSTD(3)),
    MetricDescription String CODEC(ZSTD(3)),
    MetricUnit String CODEC(ZSTD(3)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    StartTimeUnix DateTime CODEC(Delta, ZSTD(3)),
    TimeUnix DateTime CODEC(Delta, ZSTD(3)),
    Value Float64 CODEC(ZSTD(3)),
    Flags UInt32 CODEC(ZSTD(3)),
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime,
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(3)),
    AggregationTemporality Int32 CODEC(ZSTD(3)),
    IsMonotonic Boolean CODEC(Delta, ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        mapSort(ResourceAttributes),
        ResourceSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        ScopeDroppedAttrCount,
        ScopeSchemaUrl,
        ServiceName,
        MetricName,
        MetricDescription,
        MetricUnit,
        mapSort(Attributes),
        StartTimeUnix,
        TimeUnix,
        Value,
        Flags,
        AggregationTemporality,
        IsMonotonic,
        arrayMap(m -> mapSort(m), Exemplars.FilteredAttributes),
        Exemplars.TimeUnix,
        Exemplars.Value,
        Exemplars.SpanId,
        Exemplars.TraceId
    ),
{{ end }}
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(TimeUnix)
-- With dedup enabled, the row hash is the final key component. Attribute maps
-- are mapSort'ed so insertion order cannot split a duplicate.
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.ttl.metricsDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.metricsHistogram }}` (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ResourceSchemaUrl String CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(3)),
    ScopeSchemaUrl String CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    MetricName LowCardinality(String) CODEC(ZSTD(3)),
    MetricDescription String CODEC(ZSTD(3)),
    MetricUnit String CODEC(ZSTD(3)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    StartTimeUnix DateTime CODEC(Delta, ZSTD(3)),
    TimeUnix DateTime CODEC(Delta, ZSTD(3)),
    Count UInt64 CODEC(Delta, ZSTD(3)),
    Sum Float64 CODEC(ZSTD(3)),
    BucketCounts Array(UInt64) CODEC(ZSTD(3)),
    ExplicitBounds Array(Float64) CODEC(ZSTD(3)),
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime,
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(3)),
    Flags UInt32 CODEC(ZSTD(3)),
    Min Float64 CODEC(ZSTD(3)),
    Max Float64 CODEC(ZSTD(3)),
    AggregationTemporality Int32 CODEC(ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        mapSort(ResourceAttributes),
        ResourceSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        ScopeDroppedAttrCount,
        ScopeSchemaUrl,
        ServiceName,
        MetricName,
        MetricDescription,
        MetricUnit,
        mapSort(Attributes),
        StartTimeUnix,
        TimeUnix,
        Count,
        Sum,
        BucketCounts,
        ExplicitBounds,
        Flags,
        Min,
        Max,
        AggregationTemporality,
        arrayMap(m -> mapSort(m), Exemplars.FilteredAttributes),
        Exemplars.TimeUnix,
        Exemplars.Value,
        Exemplars.SpanId,
        Exemplars.TraceId
    ),
{{ end }}
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(TimeUnix)
-- With dedup enabled, the row hash is the final key component. Attribute maps
-- are mapSort'ed so insertion order cannot split a duplicate.
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.ttl.metricsDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.metricsExponentialHistogram }}` (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ResourceSchemaUrl String CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(3)),
    ScopeSchemaUrl String CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    MetricName LowCardinality(String) CODEC(ZSTD(3)),
    MetricDescription String CODEC(ZSTD(3)),
    MetricUnit String CODEC(ZSTD(3)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    StartTimeUnix DateTime CODEC(Delta, ZSTD(3)),
    TimeUnix DateTime CODEC(Delta, ZSTD(3)),
    Count UInt64 CODEC(Delta, ZSTD(3)),
    Sum Float64 CODEC(ZSTD(3)),
    Scale Int32 CODEC(ZSTD(3)),
    ZeroCount UInt64 CODEC(ZSTD(3)),
    PositiveOffset Int32 CODEC(ZSTD(3)),
    PositiveBucketCounts Array(UInt64) CODEC(ZSTD(3)),
    NegativeOffset Int32 CODEC(ZSTD(3)),
    NegativeBucketCounts Array(UInt64) CODEC(ZSTD(3)),
    Exemplars Nested (
        FilteredAttributes Map(LowCardinality(String), String),
        TimeUnix DateTime,
        Value Float64,
        SpanId String,
        TraceId String
    ) CODEC(ZSTD(3)),
    Flags UInt32 CODEC(ZSTD(3)),
    Min Float64 CODEC(ZSTD(3)),
    Max Float64 CODEC(ZSTD(3)),
    AggregationTemporality Int32 CODEC(ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        mapSort(ResourceAttributes),
        ResourceSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        ScopeDroppedAttrCount,
        ScopeSchemaUrl,
        ServiceName,
        MetricName,
        MetricDescription,
        MetricUnit,
        mapSort(Attributes),
        StartTimeUnix,
        TimeUnix,
        Count,
        Sum,
        Scale,
        ZeroCount,
        PositiveOffset,
        PositiveBucketCounts,
        NegativeOffset,
        NegativeBucketCounts,
        Flags,
        Min,
        Max,
        AggregationTemporality,
        arrayMap(m -> mapSort(m), Exemplars.FilteredAttributes),
        Exemplars.TimeUnix,
        Exemplars.Value,
        Exemplars.SpanId,
        Exemplars.TraceId
    ),
{{ end }}
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(TimeUnix)
-- With dedup enabled, the row hash is the final key component. Attribute maps
-- are mapSort'ed so insertion order cannot split a duplicate.
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.ttl.metricsDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};

CREATE TABLE IF NOT EXISTS `{{ .Values.clickhouse.database }}`.`{{ .Values.clickhouse.tables.metricsSummary }}` (
    ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ResourceSchemaUrl String CODEC(ZSTD(3)),
    ScopeName String CODEC(ZSTD(3)),
    ScopeVersion String CODEC(ZSTD(3)),
    ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    ScopeDroppedAttrCount UInt32 CODEC(ZSTD(3)),
    ScopeSchemaUrl String CODEC(ZSTD(3)),
    ServiceName LowCardinality(String) CODEC(ZSTD(3)),
    MetricName LowCardinality(String) CODEC(ZSTD(3)),
    MetricDescription String CODEC(ZSTD(3)),
    MetricUnit String CODEC(ZSTD(3)),
    Attributes Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    StartTimeUnix DateTime CODEC(Delta, ZSTD(3)),
    TimeUnix DateTime CODEC(Delta, ZSTD(3)),
    Count UInt64 CODEC(Delta, ZSTD(3)),
    Sum Float64 CODEC(ZSTD(3)),
    ValueAtQuantiles Nested (
        Quantile Float64,
        Value Float64
    ) CODEC(ZSTD(3)),
    Flags UInt32 CODEC(ZSTD(3)),
{{ if .Values.clickhouse.dedup.enabled }}
    `_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(
        mapSort(ResourceAttributes),
        ResourceSchemaUrl,
        ScopeName,
        ScopeVersion,
        mapSort(ScopeAttributes),
        ScopeDroppedAttrCount,
        ScopeSchemaUrl,
        ServiceName,
        MetricName,
        MetricDescription,
        MetricUnit,
        mapSort(Attributes),
        StartTimeUnix,
        TimeUnix,
        Count,
        Sum,
        Flags,
        ValueAtQuantiles.Quantile,
        ValueAtQuantiles.Value
    ),
{{ end }}
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = {{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}
PARTITION BY toDate(TimeUnix)
-- With dedup enabled, the row hash is the final key component. Attribute maps
-- are mapSort'ed so insertion order cannot split a duplicate.
ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }})
{{- if .Values.clickhouse.storage.enabled }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.hotDays }}) TO VOLUME '{{ .Values.clickhouse.storage.coldVolume }}',
    toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.storage.totalDays }}) DELETE
{{- else }}
TTL toDateTime(TimeUnix) + toIntervalDay({{ .Values.clickhouse.ttl.metricsDays }})
{{- end }}
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }};
