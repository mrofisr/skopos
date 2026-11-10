#!/bin/sh
# Seed the Grafana harness tables from the generated SQL, traces JSON, and
# Loki push body, so ClickHouse and the reference backends share exactly the
# same data.
set -eu

host=${CLICKHOUSE_HOST:-clickhouse}
database=${CLICKHOUSE_DATABASE:-otel}
user=${CLICKHOUSE_ADMIN_USER:-default}

if [ -n "${GRAFANA_TRACES_JSON:-}" ]; then
  clickhouse-client \
    --host "$host" \
    --database "$database" \
    --user "$user" \
    --query "INSERT INTO otel_traces FORMAT JSONEachRow" <"$GRAFANA_TRACES_JSON"
  echo "seed: inserted the TraceQL corpus rows from $GRAFANA_TRACES_JSON"
fi

if [ -n "${GRAFANA_SEED_SQL:-}" ]; then
  clickhouse-client \
    --host "$host" \
    --database "$database" \
    --user "$user" \
    --multiquery <"$GRAFANA_SEED_SQL"
  echo "seed: inserted the Grafana journey samples from $GRAFANA_SEED_SQL"
fi

