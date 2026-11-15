SHELL := /bin/sh

GO ?= go
HELM ?= helm
GOVULNCHECK ?= govulncheck
VERSION ?= 0.0.1-alpha
BENCH_MEMORY_LIMIT_BYTES ?= 1610612736
LDFLAGS := -s -w -X github.com/mrofisr/skopos/internal/version.Version=$(VERSION)
CHART := deploy/helm/skopos
GOOS ?=
GOARCH ?=
BIN_DIR ?= bin
BINARY_EXT := $(if $(filter windows,$(GOOS)),.exe,)

.PHONY: help build build-for mutation mutation-gate alpha-exit-check test test-race test-integration test-faults fuzz vet vuln helm-lint helm-template check clean e2e-obi e2e-grafana grafana-journeys compat-all metrics-contract quality-report release-evidence storage-test bench bench-gate release release-dry-run release-check release-verify verify-release scorecard coverage coverage-report coverage-write issues-manifest issues-manifest-check issues-lint issues-lint-live issues-sync ruleset-check quickstart-test

help:
	@printf '%s\n' \
		'build             build the single skopos binary' \
		'build-for         cross-build skopos for GOOS/GOARCH into BIN_DIR' \
		'test              run unit tests' \
		'test-race         run unit tests with the race detector' \
		'test-integration  run integration tests (requires CLICKHOUSE_URL)' \
		'test-faults       run the Docker Collector/ClickHouse restart test' \
		'fuzz              run the SQL emitter fuzz target for 30s' \
		'vet               run go vet' \
		'vuln              run govulncheck' \
		'helm-lint         lint the Helm chart' \
		'helm-template     render the Helm chart' \
		'e2e-obi           run the real OBI end-to-end harness (Docker, Linux)' \
		'e2e-grafana       verify Grafana datasources, journeys, and the OBI RED dashboard (Docker)' \
		'grafana-journeys  run the Grafana journeys and record EC1 evidence' \
		'compat-all        run the reference compatibility corpora and record EC2 evidence' \
		'metrics-contract  run the metrics contract and record EC4 evidence' \
		'quality-report    run local EC6 gates and record evidence' \
		'release-evidence  verify a published release and quickstart, record EC5 evidence' \
		'storage-test      run ClickHouse + RustFS storage, backup, restore drill, and lifecycle test' \
		'bench             run the ClickHouse compression and gateway latency benchmark' \
		'bench-gate        run the concurrent safety gate and record EC3 evidence' \
		'mutation          run mutation testing (PKG=./internal/auth by default)' \
		'alpha-exit-check  verify EC1-6, record EC7, and aggregate all evidence' \
		'profile-range     capture pprof and metrics for the 6h range query' \
		'release           build and package release artifacts (VERSION=...)' \
		'release-dry-run   same as release; the release workflow publishes' \
		'release-check     release preflight: vet, tests, Helm, modules, script syntax' \
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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/skopos ./cmd/skopos

# build-for keeps cross-compilation in the Makefile so CI and local runs use
# the same flags. The Go tool needs CGO disabled to cross-compile without a
# target C toolchain. The CI matrix calls this once per target.
build-for:
	@test -n "$(GOOS)" || (echo "GOOS is required" >&2; exit 1)
	@test -n "$(GOARCH)" || (echo "GOARCH is required" >&2; exit 1)
	mkdir -p $(BIN_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/skopos$(BINARY_EXT) ./cmd/skopos

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
	./scripts/e2e/obi/run.sh

e2e-grafana:
	./scripts/e2e/obi/grafana.sh

grafana-journeys:
	rm -f test/quality/evidence/ec-1.json
	./scripts/e2e/obi/grafana.sh
	$(GO) run ./tools/repoctl evidence record --ec 1 --command "make grafana-journeys"

compat-all:
	rm -f test/quality/evidence/ec-2.json
	./scripts/compat/all.sh
	$(GO) run ./tools/repoctl evidence record --ec 2 --command "make compat-all"

metrics-contract:
	$(GO) run ./tools/repoctl evidence run --ec 4 --command "make metrics-contract" -- $(GO) test ./internal/metrics -run '^TestMetricsContract$$' -count=1

quality-report:
	rm -f test/quality/evidence/ec-6.json
	./scripts/quality/report.sh
	$(GO) run ./tools/repoctl evidence record --ec 6 --command "make quality-report"

release-evidence:
	@test -n "$(TAG)" || (echo "TAG is required" >&2; exit 1)
	rm -f test/quality/evidence/ec-5.json
	TAG=$(TAG) ./scripts/release/evidence.sh
	$(GO) run ./tools/repoctl evidence record --ec 5 --command "make release-evidence TAG=$(TAG)"

storage-test:
	./scripts/integration/s3.sh

quickstart-test:
	./scripts/quickstart/quickstart-test.sh

bench:
	./scripts/bench/run.sh

bench-gate:
	rm -f test/quality/evidence/ec-3.json
	SKOPOS_BENCH_GATE=1 SKOPOS_BENCH_MEMORY_LIMIT_BYTES=$(BENCH_MEMORY_LIMIT_BYTES) ./scripts/bench/run.sh
	$(GO) run ./tools/repoctl evidence record --ec 3 --command "make bench-gate"

test-faults:
	SKOPOS_FAULTS_TEST=1 $(GO) test ./test/integration/faults -count=1 -v

profile-range:
	./scripts/bench/profile-range.sh

PKG ?= ./internal/auth

mutation:
	mkdir -p bin
	cd internal/tools && $(GO) build -o ../../bin/gremlins github.com/go-gremlins/gremlins/cmd/gremlins
	./bin/gremlins unleash --workers 4 --timeout-coefficient 10 --output mutation.json $(PKG)
	$(GO) run ./tools/repoctl mutation report --input mutation.json

alpha-exit-check:
	rm -f test/quality/evidence/ec-7.json
	$(GO) run ./tools/repoctl exit-check --check --skip 7
	$(GO) test ./internal/archtest -count=1
	$(GO) run ./tools/repoctl issues manifest --check
	$(GO) run ./tools/repoctl issues lint
	$(GO) run ./tools/repoctl ruleset
	$(GO) run ./tools/repoctl evidence record --ec 7 --command "make alpha-exit-check"
	$(GO) run ./tools/repoctl exit-check

mutation-gate:
	mkdir -p bin
	cd internal/tools && $(GO) build -o ../../bin/gremlins github.com/go-gremlins/gremlins/cmd/gremlins
	./bin/gremlins unleash --workers 4 --timeout-coefficient 10 --output mutation.json $(PKG)
	$(GO) run ./tools/repoctl mutation gate --input mutation.json --floor 70 --allowlist test/quality/mutation-allowlist.json

release:
	VERSION=$(VERSION) ./scripts/release/release.sh

release-dry-run:
	VERSION=$(VERSION) ./scripts/release/release.sh

release-verify verify-release:
	TAG=$(TAG) VERSION=$(VERSION) ./scripts/release/verify-release.sh

scorecard:
	./scripts/scorecard/scorecard.sh

coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./tools/repoctl coverage gate --profile coverage.out --floor test/quality/coverage-floor.json --previous-ref origin/development

coverage-report:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./tools/repoctl coverage report --profile coverage.out --floor test/quality/coverage-floor.json

coverage-write:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) run ./tools/repoctl coverage write --profile coverage.out --floor test/quality/coverage-floor.json

issues-manifest:
	$(GO) run ./tools/repoctl issues manifest --write

issues-manifest-check:
	$(GO) run ./tools/repoctl issues manifest --check

issues-lint:
	$(GO) run ./tools/repoctl issues lint

issues-lint-live:
	$(GO) run ./tools/repoctl issues lint --live

issues-sync:
	$(GO) run ./tools/repoctl issues sync $(if $(APPLY),--apply,)

ruleset-check:
	$(GO) run ./tools/repoctl ruleset

release-check: vet test helm-lint ruleset-check
	$(GO) mod verify
	$(HELM) template skopos $(CHART) --namespace observability >/dev/null
	cd test/compat/prometheus && $(GO) mod verify && $(GO) vet ./... && $(GO) test -count=1 ./...
	sh -n scripts/quickstart/quickstart-test.sh
	sh -n scripts/release/release.sh
	sh -n scripts/release/verify-release.sh
	sh -n scripts/scorecard/scorecard.sh
	sh -n scripts/e2e/obi/run.sh
	sh -n scripts/e2e/obi/grafana.sh
	sh -n scripts/e2e/obi/seed.sh
	sh -n scripts/e2e/obi/verify.sh
	sh -n scripts/e2e/obi/load.sh
	sh -n scripts/e2e/obi/render-schema.sh
	sh -n scripts/e2e/obi/init-clickhouse.sh
	sh -n scripts/integration/dedup.sh
	bash -n scripts/integration/s3.sh
	sh -n scripts/deploy/render-schema.sh
	sh -n scripts/bench/run.sh
	sh -n scripts/bench/measure-compression.sh
	sh -n scripts/compat/loki-upstream.sh
	sh -n scripts/compat/tempo.sh
	sh -n scripts/compat/all.sh
	sh -n scripts/quality/report.sh
	sh -n scripts/release/evidence.sh
	@printf '%s\n' 'release-check: ok'

clean:
	rm -rf bin
