#!/bin/sh
# Fail when any open GitHub code-scanning alert is high or critical.
#
# Used by the release preflight. Requires GITHUB_TOKEN or GH_TOKEN with
# security-events read access:
#
#   GITHUB_TOKEN="$(gh auth token)" scripts/check-code-scanning.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
exec go run ./cmd/skopos-code-scanning "$@"
