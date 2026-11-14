#!/bin/sh
# Render the Helm chart's ClickHouse DDL with the chart defaults used by the
# Docker Compose harness. The harness intentionally reuses the chart schema so
# a schema regression breaks the end-to-end test instead of a private copy.
#
# SKOPOS_DEDUP=false renders the fast profile; the harness defaults to the
# reliable profile.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
harness="$root/test/e2e/obi"
src="$root/deploy/helm/skopos/files/migrate.sh"
migrate_out="$harness/.render/migrate.sh"

mkdir -p "$harness/.render"

SKOPOS_DEDUP=${SKOPOS_DEDUP:-true} "$root/scripts/deploy/render-schema.sh" \
  >"$harness/.render/schema.sql"

sed \
  -e 's|{{ .Values.clickhouse.database }}|otel|g' \
  -e 's|{{ .Values.clickhouse.tables.traces }}|otel_traces|g' \
  -e 's|{{ .Values.clickhouse.tables.logs }}|otel_logs|g' \
  -e 's|{{ .Values.clickhouse.tables.metricsGauge }}|otel_metrics_gauge|g' \
  -e 's|{{ .Values.clickhouse.tables.metricsSum }}|otel_metrics_sum|g' \
  -e 's|{{ .Values.clickhouse.tables.metricsHistogram }}|otel_metrics_histogram|g' \
  -e 's|{{ .Values.clickhouse.tables.metricsExponentialHistogram }}|otel_metrics_exponential_histogram|g' \
  -e 's|{{ .Values.clickhouse.tables.metricsSummary }}|otel_metrics_summary|g' \
  -e "s|{{ .Values.clickhouse.dedup.enabled }}|${SKOPOS_DEDUP:-true}|g" \
  "$src" >"$migrate_out"

if grep -q '{{' "$harness/.render/schema.sql" "$migrate_out"; then
  echo "render-schema: unreplaced Helm placeholder" >&2
  grep -n '{{' "$harness/.render/schema.sql" "$migrate_out" >&2
  exit 1
fi

sh -n "$migrate_out"
echo "rendered $harness/.render/schema.sql and $migrate_out"
