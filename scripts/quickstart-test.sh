#!/bin/sh
# Run the README quickstart verbatim against the published chart.
#
# Usage:
#   make quickstart-test
#   ./scripts/quickstart-test.sh
#
# The doc-test extracts the shell block tagged `quickstart` from README.md,
# fails before creating a cluster when the chart version is not in the OCI
# registry, downloads the matching values.release.yaml from the GitHub release,
# creates a clean k3d cluster, runs the extracted block verbatim, and checks the
# Helm release status.
#
# This is the EC5 evidence command. It is intentionally red until the chart
# version named by the README is published.
#
# Requires: go, helm, k3d, kubectl, gh (authenticated).
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

repo=${REPO:-${GITHUB_REPOSITORY:-jawaracloud/skopos}}
cluster=${SKOPOS_QUICKSTART_CLUSTER:-skopos-quickstart}

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "quickstart-test: missing required command: $1" >&2
    exit 1
  }
}
need go
need helm
need k3d
need kubectl
need gh

workdir=$(mktemp -d)
cluster_created=0

cleanup() {
  status=$?
  set +e
  if [ "$cluster_created" = "1" ]; then
    if [ "$status" -ne 0 ] || [ "${SKOPOS_QUICKSTART_KEEP:-0}" != "1" ]; then
      echo "quickstart-test: deleting k3d cluster $cluster" >&2
      k3d cluster delete "$cluster" >/dev/null 2>&1
    else
      echo "quickstart-test: keeping k3d cluster $cluster because SKOPOS_QUICKSTART_KEEP=1"
    fi
  fi
  rm -rf "$workdir"
  exit "$status"
}
trap cleanup EXIT INT TERM

echo "quickstart-test: extracting the README quickstart"
go run ./cmd/repoctl quickstart \
  --readme README.md \
  --script "$workdir/quickstart.sh" \
  --version-file "$workdir/version"

version=$(cat "$workdir/version")
tag="v$version"
echo "quickstart-test: README names chart version $version"

# A private kubeconfig keeps the user's context untouched.
export KUBECONFIG="$workdir/kubeconfig"

echo "quickstart-test: downloading values.release.yaml from $repo release $tag"
gh release download "$tag" --repo "$repo" --pattern values.release.yaml --dir "$workdir" --clobber
test -s "$workdir/values.release.yaml" || {
  echo "quickstart-test: values.release.yaml is missing from release $tag" >&2
  exit 1
}

echo "quickstart-test: creating k3d cluster $cluster"
k3d cluster delete "$cluster" >/dev/null 2>&1 || true
k3d cluster create "$cluster" \
  --wait \
  --timeout 180s \
  --agents 1 >/dev/null
cluster_created=1

echo "quickstart-test: running the README block verbatim"
(
  cd "$workdir"
  sh -eu quickstart.sh
)

echo "quickstart-test: checking the Helm release"
helm status skopos --namespace observability >/dev/null
kubectl -n observability rollout status statefulset/skopos-clickhouse --timeout=180s
kubectl -n observability rollout status deployment/skopos-gateway --timeout=180s
kubectl -n observability get pods

echo "quickstart-test: quickstart for $version succeeded"
