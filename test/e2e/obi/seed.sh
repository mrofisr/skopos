#!/bin/sh
# Seed the Grafana harness tables with one OBI-shaped metric, log, and trace.
# The datasource health checks do not require data, but seeded rows keep the
# stack useful for the journey tickets and catch a broken schema early.
set -eu

host=${CLICKHOUSE_HOST:-clickhouse}
database=${CLICKHOUSE_DATABASE:-otel}
user=${CLICKHOUSE_ADMIN_USER:-default}
span_id=0123456789abcdef
trace_id=0123456789abcdef0123456789abcdef

query() {
  clickhouse-client \
    --host "$host" \
    --database "$database" \
    --user "$user" \
    --query "$1"
}

query "INSERT INTO otel_metrics_gauge
  (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes, ResourceAttributes)
  VALUES
  ('skopos-e2e-server', 'http_server_request_duration_count',
   now(), now() - INTERVAL 1 MINUTE, 1,
   {'http.request.method':'GET','http.response.status_code':'200'},
   {'service.name':'skopos-e2e-server'})"

query "INSERT INTO otel_logs
  (Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName, Body,
   LogAttributes, ResourceAttributes)
  VALUES
  (now64(9), '$trace_id', '$span_id', 'INFO', 9, 'skopos-e2e-server',
   'grafana harness seed', {'log.level':'INFO'},
   {'service.name':'skopos-e2e-server'})"

query "INSERT INTO otel_traces
  (Timestamp, TraceId, SpanId, SpanName, ServiceName, Duration, SpanKind,
   StatusCode, ResourceAttributes, SpanAttributes)
  VALUES
  (now64(9), '$trace_id', '$span_id', 'GET /hello', 'skopos-e2e-server',
   1000000, 'SPAN_KIND_SERVER', 'STATUS_CODE_OK',
   {'service.name':'skopos-e2e-server'}, {'http.request.method':'GET'})"

echo "seed: inserted one metric, log, and trace"
