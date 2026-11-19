#!/bin/sh
# Migrate the signal tables to the current schema.
#
# The init Job applies schema.sql first and then runs this script on every
# install and upgrade. Two properties can differ from the template:
#
#   1. The engine: pre-dedup tables are MergeTree, current reliable tables are
#      ReplacingMergeTree with the replay row hash.
#   2. The metrics sort key: pre-B-04 tables start with the hour bucket and
#      ServiceName; current tables start with the five-minute bucket and order
#      MetricName second so the instant-rate predicate prunes parts.
#
# Fresh installs already match, so this is a no-op. A table that differs is
# renamed aside, schema.sql recreates it from the current template, and the old
# rows are copied back. The new table is live before the copy starts, so ingest
# keeps working; the Collector retries inserts during the short rename window.
#
# Re-running the Job is safe. A table that already matches is skipped, and a
# leftover __skopos_pre_dedup table means an earlier run was interrupted: the
# copy is repeated and the leftover is dropped. Repeated copies collapse on the
# ReplacingMergeTree key.
set -eu

database='{{ .Values.clickhouse.database }}'
traces='{{ .Values.clickhouse.tables.traces }}'
logs='{{ .Values.clickhouse.tables.logs }}'
metrics_gauge='{{ .Values.clickhouse.tables.metricsGauge }}'
metrics_sum='{{ .Values.clickhouse.tables.metricsSum }}'
metrics_histogram='{{ .Values.clickhouse.tables.metricsHistogram }}'
metrics_exponential_histogram='{{ .Values.clickhouse.tables.metricsExponentialHistogram }}'
metrics_summary='{{ .Values.clickhouse.tables.metricsSummary }}'
trace_id_ts="${traces}_trace_id_ts"
trace_id_ts_mv="${traces}_trace_id_ts_mv"
dedup_enabled='{{ .Values.clickhouse.dedup.enabled }}'

legacy_suffix='__skopos_pre_dedup'
# The metrics key is shared by all five tables; the row hash suffix is profile
# dependent, so only the stable prefix is compared.
metrics_key='toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName,'

cli() {
  clickhouse-client --host "$CLICKHOUSE_HOST" --port 9000 \
    --user "$CLICKHOUSE_ADMIN_USER" --password "$CLICKHOUSE_ADMIN_PASSWORD" "$@"
}

engine() {
  cli --query "SELECT engine FROM system.tables WHERE database = '${database}' AND name = '$1'"
}

sorting_key() {
  cli --query "SELECT sorting_key FROM system.tables WHERE database = '${database}' AND name = '$1'"
}

exists() {
  [ -n "$(engine "$1")" ]
}

# matches_key reports whether a table's sort key already starts with the
# required prefix. An empty prefix means the table's key is not constrained.
matches_key() {
  [ -z "$1" ] && return 0
  case "$(sorting_key "$2")" in
    "$1"*) return 0 ;;
    *) return 1 ;;
  esac
}

apply_schema() {
  cli --multiquery < /schema/schema.sql
}

backfill() {
  table="$1"
  legacy="${table}${legacy_suffix}"
  if ! exists "$legacy"; then
    return 0
  fi
  current=$(engine "$table")
  expected='MergeTree'
  if [ "$dedup_enabled" = 'true' ]; then
    expected='ReplacingMergeTree'
  fi
  if [ "$current" != "$expected" ]; then
    echo "migrate: ${legacy} exists but ${table} is '${current}', refusing to continue" >&2
    exit 1
  fi
  echo "migrate: backfilling ${table} from ${legacy}"
  cli --query "INSERT INTO \`${database}\`.\`${table}\` SELECT * FROM \`${database}\`.\`${legacy}\`"
  cli --query "DROP TABLE \`${database}\`.\`${legacy}\`"
}

# migrate rewrites one table when its engine or sort key differs from the
# template. The key prefix argument is empty for tables whose key is unchanged.
migrate() {
  table="$1"
  key_prefix="$2"
  legacy="${table}${legacy_suffix}"
  current=$(engine "$table")

  if [ -z "$current" ]; then
    if exists "$legacy"; then
      echo "migrate: recreating ${table} after an interrupted rename"
      apply_schema
      backfill "$table"
      return
    fi
    echo "migrate: ${table} does not exist" >&2
    exit 1
  fi

  # Finish an interrupted run before deciding whether another rewrite is
  # needed. The backfill is a no-op when no leftover exists.
  backfill "$table"

  current=$(engine "$table")
  rewrite='false'
  if ! matches_key "$key_prefix" "$table"; then
    rewrite='true'
  fi
  if [ "$dedup_enabled" = 'true' ] && [ "$current" = 'MergeTree' ]; then
    rewrite='true'
  fi
  if [ "$rewrite" = 'false' ]; then
    return
  fi

  case "$current" in
    MergeTree|ReplacingMergeTree)
      if exists "$legacy"; then
        # backfill above drops the leftover, so this only fires when a
        # concurrent run left a new one. Refuse rather than copy twice.
        echo "migrate: both ${table} and ${legacy} exist" >&2
        exit 1
      fi
      echo "migrate: rewriting ${table} to the current engine and key"
      cli --query "RENAME TABLE \`${database}\`.\`${table}\` TO \`${database}\`.\`${legacy}\`"
      apply_schema
      backfill "$table"
      ;;
    *)
      echo "migrate: unsupported engine '${current}' for ${table}" >&2
      exit 1
      ;;
  esac
}

# The trace index is a materialized-view target, not a query table. Its view is
# attached to the source table by UUID, so drop it before either trace table is
# renamed and let schema.sql recreate it against the new tables.
ts_legacy="${trace_id_ts}${legacy_suffix}"
traces_legacy="${traces}${legacy_suffix}"
traces_engine=$(engine "$traces")
ts_engine=$(engine "$trace_id_ts")

traces_rewrite='false'
if [ "$dedup_enabled" = 'true' ] && [ "$traces_engine" = 'MergeTree' ]; then
  traces_rewrite='true'
fi
ts_rewrite='false'
if [ "$dedup_enabled" = 'true' ] && [ "$ts_engine" = 'MergeTree' ]; then
  ts_rewrite='true'
fi

if [ "$traces_rewrite" = 'true' ] || [ "$ts_rewrite" = 'true' ] || \
  exists "$traces_legacy" || { [ -z "$ts_engine" ] && exists "$ts_legacy"; }; then
  echo "migrate: deduplicating trace tables"
  cli --query "DROP VIEW IF EXISTS \`${database}\`.\`${trace_id_ts_mv}\`"

  if [ "$ts_rewrite" = 'true' ]; then
    cli --query "RENAME TABLE \`${database}\`.\`${trace_id_ts}\` TO \`${database}\`.\`${ts_legacy}\`"
  fi

  migrate "$traces" ''
  apply_schema

  if exists "$ts_legacy"; then
    # The old index was derived from the old traces table. Drop it; the
    # rebuild below recreates the current projection.
    cli --query "DROP TABLE \`${database}\`.\`${ts_legacy}\`"
  fi

  # Make the trace index match the new traces table. This also repairs an
  # earlier run that stopped after truncating it. The materialized view is
  # already recreated and serves new inserts after this point.
  echo "migrate: rebuilding ${trace_id_ts} from ${traces}"
  cli --query "TRUNCATE TABLE \`${database}\`.\`${trace_id_ts}\`"
  cli --query "INSERT INTO \`${database}\`.\`${trace_id_ts}\` (TraceId, Start, End) SELECT TraceId, min(Timestamp) AS Start, max(Timestamp) AS End FROM \`${database}\`.\`${traces}\` WHERE TraceId != '' GROUP BY TraceId"
fi

# The logs key already starts with the five-minute bucket; only the engine can
# need migration. The five metrics tables need the key rewrite in both
# profiles, including the fast profile where the engine is already correct.
migrate "$logs" ''
for table in "$metrics_gauge" "$metrics_sum" "$metrics_histogram" \
  "$metrics_exponential_histogram" "$metrics_summary"; do
  migrate "$table" "$metrics_key"
done

echo "migrate: schema is ready"
