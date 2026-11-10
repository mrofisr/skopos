#!/bin/sh
# Capture pprof and metrics for the 6 h range query on the bench stack.
# The stack is torn down on success; on failure it is kept for inspection.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$here/../.." && pwd)
out=${SKOPOS_PROFILE_OUT:-"$root/test/bench/out"}
gateway=${SKOPOS_BENCH_GATEWAY_URL:-http://127.0.0.1:${SKOPOS_BENCH_GATEWAY_PORT:-28082}}
compose="docker compose -p skopos-bench -f $here/docker-compose.yml"

cleanup() {
  status=$?
  set +e
  if [ "$status" -ne 0 ]; then
    echo "profile-range: failed (exit $status); keeping the stack" >&2
    $compose ps >&2
    $compose logs --no-color --tail=20 clickhouse gateway >&2
  else
    $compose down -v --remove-orphans >/dev/null 2>&1
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$out"
SKOPOS_BENCH_PROFILE=1 SKOPOS_BENCH_KEEP=1 SKOPOS_BENCH_CONCURRENCY=0 \
  "$here/run.sh"

# A 6 h range query: sum(rate()) over a five-minute window, one-minute step.
query='sum(rate(http_requests_total%5B5m%5D))'
start=$(($(date +%s) - 21600))
end=$(date +%s)
url="$gateway/api/v1/query_range?query=$query&start=$start&end=$end&step=60s"

load() {
  deadline=$(( $(date +%s) + 90 ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    for _ in 1 2 3 4 5 6 7 8; do
      curl -fsS "$url" >/dev/null &
    done
    wait
  done
}
load &
loader=$!

sleep 10
curl -fsS "$gateway/debug/pprof/profile?seconds=30" -o "$out/range-query-cpu.pprof"
curl -fsS "$gateway/debug/pprof/heap" -o "$out/range-query-heap.pprof"
curl -fsS "$gateway/metrics" >"$out/range-query-metrics.prom"
wait "$loader" || true

echo
echo "profile-range: wrote $out/range-query-{cpu,heap}.pprof and metrics"
if command -v go >/dev/null 2>&1; then
  go tool pprof -top -nodecount=15 "$out/range-query-cpu.pprof" 2>/dev/null || true
fi
echo
echo "profile-range: inspect with"
echo "  go tool pprof -http=:8081 $out/range-query-cpu.pprof"
