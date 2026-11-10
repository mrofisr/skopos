#!/bin/sh
# Seed the Grafana harness tables with one OBI-shaped log and trace. The
# Grafana journey samples come from the generated GRAFANA_SEED_SQL file, so
# ClickHouse and the reference Prometheus share exactly the same metrics.
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

if [ -n "${GRAFANA_SEED_SQL:-}" ]; then
  clickhouse-client \
    --host "$host" \
    --database "$database" \
    --user "$user" \
    --multiquery <"$GRAFANA_SEED_SQL"
  echo "seed: inserted the Grafana journey samples from $GRAFANA_SEED_SQL"
fi

echo "seed: inserted one log and trace"
