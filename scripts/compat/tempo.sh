#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
TEMPO_COMPAT=1 go test ./test/compat/tempo -run TestDifferential -v "$@"
