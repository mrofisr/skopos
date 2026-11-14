#!/bin/sh
# Run the local EC6 quality gates: coverage ratchet, layout, workflow policy,
# ruleset consistency, and the tests for the PR/issue checkers. PR-specific
# traceability and red-first enforcement still run in the PR guard workflow.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"

echo "quality-report: coverage ratchet"
go test -coverprofile=coverage.out ./...
go run ./tools/repoctl coverage gate \
	--profile coverage.out \
	--floor test/quality/coverage-floor.json \
	--previous-ref origin/development

echo "quality-report: layout ratchet"
go test ./internal/archtest -count=1

echo "quality-report: workflow policy"
go run ./tools/repoctl workflow-lint
go run ./tools/repoctl ruleset

echo "quality-report: PR and issue checker tests"
go test ./tools/repoctl/internal/prcheck ./tools/repoctl/internal/issueops -count=1

echo "quality-report: ok"
