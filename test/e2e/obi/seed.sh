#!/bin/sh
# Seed the Grafana harness tables with one OBI-shaped trace. The journey
# samples come from the generated GRAFANA_SEED_SQL file, so ClickHouse and the
# reference Prometheus/Loki share exactly the same data.
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

echo "seed: inserted one trace"
