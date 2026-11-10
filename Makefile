SHELL := /bin/sh

GO ?= go
HELM ?= helm
GOVULNCHECK ?= govulncheck
VERSION ?= 0.1.0-alpha.1
LDFLAGS := -s -w -X main.version=$(VERSION)
CHART := deploy/helm/skopos

.PHONY: help build mcp checkpoint test test-race test-integration fuzz vet vuln helm-lint helm-template check clean e2e-obi e2e-grafana compat-cerberus storage-test bench bench-gate release release-dry-run release-check release-verify verify-release scorecard coverage coverage-report coverage-write issues-manifest issues-manifest-check issues-lint issues-lint-live issues-sync ruleset-check quickstart-test

help:
	@printf '%s\n' \
		'build             build the gateway binary' \
		'mcp               build the stdio MCP server' \
		'checkpoint        build the S3 durability checkpoint service' \
		'test              run unit tests' \
		'test-race         run unit tests with the race detector' \
		'test-integration  run integration tests (requires CLICKHOUSE_URL)' \
		'fuzz              run the SQL emitter fuzz target for 30s' \
		'vet               run go vet' \
		'vuln              run govulncheck' \
		'helm-lint         lint the Helm chart' \
		'helm-template     render the Helm chart' \
		'e2e-obi           run the real OBI end-to-end harness (Docker, Linux)' \
		'e2e-grafana       verify Grafana datasources and run the Prometheus/Loki journeys (Docker)' \
		'compat-cerberus   run the cerberus three-way differential' \
		'storage-test      run ClickHouse + RustFS tiered storage and backup test' \
		'bench             run the ClickHouse compression and gateway latency benchmark' \
		'bench-gate        run the concurrent safety gate (no transport errors, valid shedding)' \
		'profile-range     capture pprof and metrics for the 6h range query' \
		'release           build and package release artifacts (VERSION=...)' \
		'release-dry-run   same as release; the release workflow publishes' \
		'release-check     release preflight: vet, tests, Helm, vendor, script syntax' \
		'release-verify    verify a published release (TAG=vX.Y.Z): signatures, attestations, SBOMs, digest' \
		'verify-release    alias for release-verify' \
		'scorecard         run pinned OpenSSF Scorecard (needs GITHUB_AUTH_TOKEN)' \
		'coverage          run tests and enforce the per-package coverage floor' \
		'coverage-report   print per-package coverage against the floor' \
		'coverage-write    regenerate coverage-floor.json from measured coverage' \
		'issues-manifest   regenerate issues/alpha-exit.json from the plan' \
		'issues-manifest-check  fail when the checked-in manifest is stale' \
		'issues-lint       validate the issue manifest' \
		'issues-lint-live  validate the live alpha-exit issues (read-only)' \
		'issues-sync       dry-run the label and issue sync (APPLY=1 to mutate)' \
		'ruleset-check     check the main ruleset against workflow job names' \
		'quickstart-test   run the README quickstart in a clean k3d cluster' \
		'check             vet + test + fuzz + helm lint'

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/gateway ./cmd/gateway

mcp:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/skopos-mcp ./cmd/skopos-mcp

checkpoint:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/skopos-checkpoint ./cmd/skopos-checkpoint

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-integration:
	@test -n "$$CLICKHOUSE_URL" || (echo "CLICKHOUSE_URL is required" >&2; exit 1)
	$(GO) test -tags=integration ./...

fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzEmitNoUserValueLeak -fuzztime=30s ./internal/sqlgen

vet:
	$(GO) vet ./...

vuln:
	$(GOVULNCHECK) ./...

helm-lint:
	$(HELM) lint $(CHART)

helm-template:
	$(HELM) template skopos $(CHART) --namespace observability

check: vet test helm-lint fuzz ruleset-check

e2e-obi:
	./test/e2e/obi/run.sh

e2e-grafana:
	./test/e2e/obi/grafana.sh

compat-cerberus:
	./test/compat/cerberus/run.sh

storage-test:
	./test/integration/s3/run.sh

quickstart-test:
	./scripts/quickstart-test.sh

bench:
	./test/bench/run.sh

bench-gate:
	SKOPOS_BENCH_GATE=1 ./test/bench/run.sh

profile-range:
	./test/bench/profile-range.sh

release:
	VERSION=$(VERSION) ./scripts/release.sh

release-dry-run:
	VERSION=$(VERSION) ./scripts/release.sh

release-verify verify-release:
	TAG=$(TAG) VERSION=$(VERSION) ./scripts/verify-release.sh

scorecard:
	./scripts/scorecard.sh

coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./cmd/repoctl coverage gate --profile coverage.out --floor test/quality/coverage-floor.json --previous-ref origin/development

coverage-report:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./cmd/repoctl coverage report --profile coverage.out --floor test/quality/coverage-floor.json

coverage-write:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./cmd/repoctl coverage write --profile coverage.out --floor test/quality/coverage-floor.json

issues-manifest:
	$(GO) run ./cmd/repoctl issues manifest --write

issues-manifest-check:
	$(GO) run ./cmd/repoctl issues manifest --check

issues-lint:
	$(GO) run ./cmd/repoctl issues lint

issues-lint-live:
	$(GO) run ./cmd/repoctl issues lint --live

issues-sync:
	$(GO) run ./cmd/repoctl issues sync $(if $(APPLY),--apply,)

ruleset-check:
	$(GO) run ./cmd/repoctl ruleset

release-check: vet test helm-lint ruleset-check
	$(GO) mod verify
	$(GO) mod vendor
	git diff --exit-code -- vendor go.mod go.sum
	$(HELM) template skopos $(CHART) --namespace observability >/dev/null
	cd test/compat/prometheus && $(GO) mod verify && $(GO) vet ./... && $(GO) test -count=1 ./...
	sh -n scripts/quickstart-test.sh
	sh -n scripts/release.sh
	sh -n scripts/verify-release.sh
	sh -n scripts/sbom-fallback.sh
	sh -n scripts/scorecard.sh
	sh -n test/e2e/obi/run.sh
	sh -n test/e2e/obi/grafana.sh
	sh -n test/e2e/obi/seed.sh
	sh -n test/e2e/obi/verify.sh
	sh -n test/e2e/obi/load.sh
	sh -n test/e2e/obi/render-schema.sh
	sh -n test/e2e/obi/init-clickhouse.sh
	sh -n test/integration/dedup/run.sh
	sh -n test/integration/s3/run.sh
	sh -n deploy/systemd/render-schema.sh
	sh -n test/bench/run.sh
	sh -n test/bench/measure-compression.sh
	@printf '%s\n' 'release-check: ok'

clean:
	rm -rf bin
