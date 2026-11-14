#!/bin/sh
# Verify a published Skopos release.
#
# Usage:
#   make release-verify TAG=v0.1.0-alpha.2
#   TAG=v0.1.0-alpha.2 ./scripts/release/verify-release.sh
#   ./scripts/release/verify-release.sh 0.1.0-alpha.2
#
# Verifies the image and chart signatures, provenance attestations, SBOM
# assets, and the image digest pinned by values.release.yaml. The values file
# is downloaded from the GitHub release so a clean machine verifies the same
# bytes users install.
#
# Requires: cosign, gh (authenticated), helm.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

tag=${TAG:-${VERSION:-${1:-}}}
if [ -z "$tag" ]; then
  echo "usage: TAG=<tag> $0 [tag]" >&2
  exit 1
fi
tag=${tag#v}
version=$tag
tag="v$version"

repo=${REPO:-${GITHUB_REPOSITORY:-mrofisr/skopos}}
image_repository=${IMAGE_REPOSITORY:-ghcr.io/${repo%%/*}/skopos-gateway}
chart_ref=${CHART_REF:-ghcr.io/${repo%%/*}/charts/skopos}

for tool in cosign gh helm; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "verify-release: missing required command: $tool" >&2
    exit 1
  }
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

gh release download "$tag" \
  --repo "$repo" \
  --pattern values.release.yaml \
  --dir "$tmp"

go run ./tools/repoctl release-verify \
  --repo "$repo" \
  --image-repository "$image_repository" \
  --chart-ref "$chart_ref" \
  --tag "$tag" \
  --values "$tmp/values.release.yaml"

echo "verify-release: render chart with values.release.yaml"
helm template skopos "oci://${chart_ref}" \
  --version "$version" \
  --namespace observability \
  -f "$tmp/values.release.yaml" >/dev/null

echo "verify-release: ok"
