#!/bin/sh
# Verify a published release and run the README quickstart against it. This is
# the EC5 evidence gate.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"

tag=${TAG:-${1:-}}
if [ -z "$tag" ]; then
	echo "usage: TAG=vX.Y.Z $0" >&2
	exit 1
fi
tag=${tag#v}

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT INT TERM
go run ./tools/repoctl quickstart \
	--readme README.md \
	--script "$workdir/quickstart.sh" \
	--version-file "$workdir/version" >/dev/null
readme_version=$(cat "$workdir/version")
if [ "$readme_version" != "$tag" ]; then
	echo "release-evidence: README quickstart names $readme_version, not $tag" >&2
	exit 1
fi

echo "release-evidence: verifying $tag"
TAG=$tag ./scripts/release/verify-release.sh

echo "release-evidence: README quickstart"
./scripts/quickstart/quickstart-test.sh

echo "release-evidence: ok"
