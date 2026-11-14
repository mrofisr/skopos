#!/bin/sh
# Run the supported compatibility corpora and reference differentials. This is
# the EC2 evidence gate; it does not replace the records in docs/beta-gates.md.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"

echo "compat-all: PromQL differential oracle"
(
	cd test/compat/prometheus
	go test -count=1 ./...
)

echo "compat-all: official Prometheus PromQL compliance suite"
./scripts/compat/prometheus-compliance.sh

echo "compat-all: LogQL corpus and upstream drift"
go test ./internal/loki -run '^TestLogQLCorpus$' -count=1
./scripts/compat/loki-upstream.sh verify

echo "compat-all: TraceQL corpus"
go test ./internal/tempo -run '^TestCorpus' -count=1

echo "compat-all: Tempo differential"
./scripts/compat/tempo.sh

echo "compat-all: cerberus seed and schema guards"
go test ./test/compat/cerberus/... -count=1

echo "compat-all: ok"
