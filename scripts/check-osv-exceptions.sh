#!/bin/sh
# Validate the dated OSV exception ledger.
#
# Used by the release preflight. The alpha ledger is empty; every finding
# blocks the release unless it is listed with a reason and an expiry date.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
exec go run ./cmd/skopos-osv-check "$@"
