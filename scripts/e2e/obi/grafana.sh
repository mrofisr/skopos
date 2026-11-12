#!/bin/sh
# Run the Grafana datasource harness on the shared OBI Compose stack.
#
# Usage:
#   ./scripts/e2e/obi/grafana.sh
#   ./scripts/e2e/obi/grafana.sh --self-test stopped
#   ./scripts/e2e/obi/grafana.sh --self-test auth
#
# The default run starts only ClickHouse, the seed job, the gateway, and
# Grafana. It provisions the three datasources from the example file and
# asserts every datasource health check passes. No service is privileged, so a
# standard CI runner can run it.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
harness="$root/test/e2e/obi"
compose_file="$harness/docker-compose.yml"
project=skopos-grafana-e2e

mode=verify
case ${1:-} in
  "") ;;
  --self-test)
    mode=${2:-}
    case "$mode" in
      stopped|auth) ;;
      *) echo "usage: $0 [--self-test stopped|auth]" >&2; exit 2 ;;
    esac
    ;;
  *)
    echo "usage: $0 [--self-test stopped|auth]" >&2
    exit 2
    ;;
esac

export SKOPOS_E2E_GATEWAY_PORT=${SKOPOS_E2E_GATEWAY_PORT:-18082}
export SKOPOS_E2E_CLICKHOUSE_HTTP_PORT=${SKOPOS_E2E_CLICKHOUSE_HTTP_PORT:-18124}
export SKOPOS_E2E_CLICKHOUSE_NATIVE_PORT=${SKOPOS_E2E_CLICKHOUSE_NATIVE_PORT:-19001}
export SKOPOS_E2E_GRAFANA_PORT=${SKOPOS_E2E_GRAFANA_PORT:-13000}

gateway_url=http://127.0.0.1:${SKOPOS_E2E_GATEWAY_PORT}
grafana_url=http://127.0.0.1:${SKOPOS_E2E_GRAFANA_PORT}
prometheus_url=http://127.0.0.1:${SKOPOS_E2E_PROMETHEUS_PORT:-18093}
loki_url=http://127.0.0.1:${SKOPOS_E2E_LOKI_PORT:-18094}
tempo_url=http://127.0.0.1:${SKOPOS_E2E_TEMPO_PORT:-18095}
tempo_otlp_url=http://127.0.0.1:${SKOPOS_E2E_TEMPO_OTLP_PORT:-18096}
provisioning_out="$harness/.render/grafana/provisioning/datasources"
render_out="$harness/.render"
check_log="$harness/.render/grafana-check.log"

if [ "$mode" = auth ]; then
  export SKOPOS_E2E_GATEWAY_AUTH=true
  export SKOPOS_E2E_AUTH_USERNAME=${SKOPOS_E2E_AUTH_USERNAME:-viewer}
  export SKOPOS_E2E_AUTH_PASSWORD=${SKOPOS_E2E_AUTH_PASSWORD:-skopos-e2e-secret}
  export SKOPOS_E2E_PROVISION_PASSWORD=${SKOPOS_E2E_PROVISION_PASSWORD:-wrong-password}
fi

compose() {
  docker compose -p "$project" -f "$compose_file" "$@"
}

cleanup() {
  status=$?
  set +e
  if [ "$status" -ne 0 ]; then
    echo "grafana: harness failed (exit $status); keeping containers for inspection" >&2
    compose ps >&2
    for svc in clickhouse gateway grafana loki prometheus seed tempo; do
      echo "----- $svc logs (tail) -----" >&2
      compose logs --no-color --tail=40 "$svc" >&2
    done
    echo "grafana: inspect with: docker compose -p $project -f $compose_file logs" >&2
    echo "grafana: remove with:   docker compose -p $project -f $compose_file down -v" >&2
  elif [ "${SKOPOS_E2E_KEEP:-0}" = "1" ]; then
    echo "grafana: keeping stack because SKOPOS_E2E_KEEP=1"
    echo "grafana: gateway: $gateway_url  grafana: $grafana_url"
  else
    compose down -v --remove-orphans >/dev/null 2>&1
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "grafana: missing required command: $1" >&2
    exit 1
  }
}
need docker
need curl
need go
need awk
docker compose version >/dev/null 2>&1 || {
  echo "grafana: docker compose plugin is required" >&2
  exit 1
}

echo "grafana: rendering chart schema"
"$root/scripts/e2e/obi/render-schema.sh"

echo "grafana: rendering datasource provisioning"
mkdir -p "$provisioning_out"
if [ -n "${SKOPOS_E2E_PROVISION_PASSWORD:-}" ]; then
  sed 's|http://skopos-gateway.observability.svc:8080|http://gateway:18081|g' \
    "$root/deploy/examples/grafana/provisioning/datasources/skopos.yaml" \
    | awk -v user="$SKOPOS_E2E_AUTH_USERNAME" -v pass="$SKOPOS_E2E_PROVISION_PASSWORD" '
    /^    url: / {
      print
      print "    basicAuth: true"
      print "    basicAuthUser: " user
      print "    secureJsonData:"
      print "      basicAuthPassword: " pass
      next
    }
    { print }
  ' >"$provisioning_out/skopos.yaml"
else
  sed 's|http://skopos-gateway.observability.svc:8080|http://gateway:18081|g' \
    "$root/deploy/examples/grafana/provisioning/datasources/skopos.yaml" >"$provisioning_out/skopos.yaml"
fi

if ! grep -q 'http://gateway:18081' "$provisioning_out/skopos.yaml"; then
  echo "grafana: rendered provisioning does not point at the compose gateway" >&2
  exit 1
fi
cp "$harness/grafana-reference-datasource.yaml" "$provisioning_out/reference.yaml"

echo "grafana: generating the shared seed"
mkdir -p "$render_out/prometheus"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go run ./test/e2e/obi/seedgen \
  --sql "$render_out/grafana-seed.sql" \
  --openmetrics "$render_out/prometheus/grafana-seed.om" \
  --loki-push "$render_out/loki-push.json" \
  --traces-json "$render_out/grafana-traces.json" \
  --tempo-otlp "$render_out/grafana-traces.otlp.json" \
  --now "$now"

echo "grafana: starting a clean stack"
compose down -v --remove-orphans >/dev/null 2>&1 || true
compose run --rm --no-deps --entrypoint promtool prometheus \
  tsdb create-blocks-from openmetrics /seed/grafana-seed.om /prometheus
compose up -d --build clickhouse clickhouse-init seed gateway grafana prometheus loki tempo

wait_for() {
  url=$1
  name=$2
  limit=$3
  i=0
  until curl -fsS "$url" >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge "$limit" ]; then
      echo "grafana: $name did not become ready at $url" >&2
      exit 1
    fi
    sleep 2
  done
  echo "grafana: $name ready"
}

wait_for "$gateway_url/readyz" gateway 90
wait_for "$grafana_url/api/health" grafana 90
wait_for "$prometheus_url/-/ready" prometheus 90
wait_for "$loki_url/ready" loki 90
wait_for "$tempo_url/ready" tempo 90

echo "grafana: pushing the shared trace seed to Tempo"
curl -fsS -X POST -H 'Content-Type: application/json' \
  --data-binary "@$render_out/grafana-traces.otlp.json" \
  "$tempo_otlp_url/v1/traces" >/dev/null

echo "grafana: pushing the shared log seed to Loki"
curl -fsS -X POST -H 'Content-Type: application/json' \
  --data-binary "@$render_out/loki-push.json" \
  "$loki_url/loki/api/v1/push" >/dev/null

run_check() {
  go run ./tools/repoctl grafana-check \
    --grafana-url "$grafana_url" \
    --provisioning "$root/deploy/examples/grafana/provisioning/datasources/skopos.yaml" \
    "$@"
}

expect_failure() {
  want=$1
  mkdir -p "$(dirname "$check_log")"
  set +e
  run_check >"$check_log" 2>&1
  status=$?
  set -e
  cat "$check_log"
  if [ "$status" -eq 0 ]; then
    echo "grafana: self-test failed: checker passed unexpectedly" >&2
    return 1
  fi
  if ! grep -Eqi "$want" "$check_log"; then
    echo "grafana: self-test failed: output does not match $want" >&2
    return 1
  fi
  echo "grafana: self-test passed: $want"
}

case "$mode" in
  verify)
    run_check --journey --loki-journey --tempo-journey \
      --dashboard "$root/deploy/examples/grafana/dashboards/obi-red.json"
    ;;
  stopped)
    echo "grafana: stopping the gateway to prove the health check fails"
    compose stop gateway >/dev/null
    expect_failure 'unhealthy|connection refused'
    ;;
  auth)
    echo "grafana: checking that wrong credentials with auth enabled fail"
    expect_failure '401|unauthorized'
    ;;
esac

echo "grafana: datasource harness passed"
