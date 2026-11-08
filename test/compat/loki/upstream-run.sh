#!/bin/sh
# Start the reference Loki and a Skopos gateway, then run the pinned upstream
# LogQL corpus against both. This is the one-command reproduction path.
#
#   test/compat/loki/upstream-run.sh
#   test/compat/loki/upstream-run.sh --report docs/logql-upstream-baseline.md
#
# The gateway uses the ClickHouse service from test/bench/docker-compose.yml.
# The benchmark data volume is preserved; the logs table is truncated before
# the synthetic upstream entries are inserted.
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$DIR/../../.." && pwd)
BENCH_COMPOSE="docker compose -f $ROOT/test/bench/docker-compose.yml"
LOKI_URL=${LOKI_URL:-http://127.0.0.1:13100}
GATEWAY_URL=${GATEWAY_URL:-http://127.0.0.1:28082}
CLICKHOUSE_URL=${CLICKHOUSE_URL:-http://127.0.0.1:18124}

cleanup() {
	if [ "${KEEP_UP:-0}" = "1" ]; then
		echo "KEEP_UP=1: leaving Loki and the bench stack running" >&2
		return
	fi
	$BENCH_COMPOSE down --remove-orphans >/dev/null 2>&1 || true
	"$DIR/run.sh" down >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# Start from a clean reference so repeated runs cannot double the fixture.
"$DIR/run.sh" down >/dev/null 2>&1 || true
"$DIR/run.sh" up
$BENCH_COMPOSE up -d --build clickhouse clickhouse-init gateway

i=0
until curl -fsS "$GATEWAY_URL/healthz" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -ge 150 ]; then
		echo "gateway did not become ready at $GATEWAY_URL" >&2
		$BENCH_COMPOSE logs --tail=50 gateway clickhouse-init >&2 || true
		exit 1
	fi
	sleep 2
done

i=0
until curl -fsS "$CLICKHOUSE_URL/?query=SELECT%201" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -ge 150 ]; then
		echo "ClickHouse did not become ready at $CLICKHOUSE_URL" >&2
		exit 1
	fi
	sleep 2
done

"$DIR/upstream.sh" run \
	--loki-url "$LOKI_URL" \
	--gateway-url "$GATEWAY_URL" \
	--clickhouse-url "$CLICKHOUSE_URL" \
	--truncate \
	"$@"
