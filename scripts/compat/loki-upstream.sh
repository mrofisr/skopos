#!/bin/sh
# Fetch the pinned upstream Loki benchmark corpus and verify it against the
# committed copy.
#
#   scripts/compat/loki-upstream.sh verify   regenerate and diff the committed corpus
#   scripts/compat/loki-upstream.sh refresh  rewrite the committed corpus
#
# The corpus is not a static upstream file: Loki generates it from
# pkg/logql/bench.(*GeneratorConfig).GenerateTestCases with a fixed seed. This
# script extracts the pinned source and runs that exact generator.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
UPSTREAM=$ROOT/test/compat/loki/upstream
VERSION=3.5.7
COMMIT=d5b382b90738505d8bf7217ff3a1a8c90fc09b7e
ARCHIVE_SHA256=a3ffbdc68f3481444c16a7733e4640718af502bcef25d592e77d03da388c4aa1
URL=https://github.com/grafana/loki/archive/refs/tags/v${VERSION}.tar.gz

CACHE=${SKOPOS_UPSTREAM_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/skopos/loki-upstream}
ARCHIVE=$CACHE/loki-$VERSION.tar.gz
SRC=$CACHE/loki-$VERSION
COMMITTED=$UPSTREAM/corpus.json
MODE=${1:-verify}
[ $# -gt 0 ] && shift

fetch() {
	mkdir -p "$CACHE"
	if [ ! -f "$ARCHIVE" ]; then
		echo "downloading Loki $VERSION source archive" >&2
		curl -fsSL --retry 3 -o "$ARCHIVE.tmp" "$URL"
		mv "$ARCHIVE.tmp" "$ARCHIVE"
	fi
	echo "$ARCHIVE_SHA256  $ARCHIVE" | sha256sum -c - >/dev/null
	if [ ! -d "$SRC" ]; then
		mkdir -p "$SRC"
		tar xzf "$ARCHIVE" -C "$SRC" --strip-components=1
	fi
	cp "$UPSTREAM/corpus_dump_test.go" "$SRC/pkg/logql/bench/zz_skopos_corpus_dump_test.go"
}

generate() {
	out=$1
	(
		cd "$SRC"
		SKOPOS_UPSTREAM_CORPUS_OUT=$out \
		SKOPOS_LOKI_VERSION=$VERSION \
		SKOPOS_LOKI_COMMIT=$COMMIT \
		SKOPOS_LOKI_ARCHIVE_SHA256=$ARCHIVE_SHA256 \
			go test -tags skopos_upstream -count=1 -run TestSkoposDumpCorpus ./pkg/logql/bench >/dev/null
	)
}

case "$MODE" in
verify)
	fetch
	tmp=$(mktemp)
	trap 'rm -f "$tmp"' EXIT
	generate "$tmp"
	if ! diff -u "$COMMITTED" "$tmp"; then
		echo "upstream corpus drift: run $0 refresh and review the diff" >&2
		exit 1
	fi
	echo "ok: corpus matches Loki $VERSION ($COMMIT)"
	;;
refresh)
	fetch
	generate "$COMMITTED.tmp"
	mv "$COMMITTED.tmp" "$COMMITTED"
	echo "refreshed $COMMITTED from Loki $VERSION ($COMMIT)"
	;;
*)
	echo "usage: $0 [verify|refresh]" >&2
	exit 2
	;;
esac
