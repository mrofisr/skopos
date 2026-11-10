package changes

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const testConfig = `{
  "force": [".github/workflows/ci.yml", ".github/ci-paths.json", ".golangci.yml", "internal/tools/**"],
  "groups": {
    "go": ["**/*.go", "**/go.mod", "**/go.sum", "vendor/**", "Makefile", "test/quality/**", "docs/alpha-exit-plan.md", "issues/**", "test/bench/**", "test/e2e/**", "**/testdata/**"],
    "integration": ["test/integration/**", "deploy/helm/skopos/files/**"],
    "compat": ["test/compat/**", "test/corpus/**", "docs/logql-upstream-baseline.md", "docs/promql-support.md"],
    "cerberus": ["test/compat/cerberus/**", "test/corpus/**"],
    "storage": ["deploy/**", "test/integration/s3/**"],
    "helm": ["deploy/**"],
    "policy": [".github/**", "docs/security/**", "docs/openssf-scorecard.md", "docs/**"],
    "docker": ["Dockerfile", ".dockerignore", "cmd/**"]
  },
  "jobs": {
    "go": ["go"],
    "coverage": ["go"],
    "integration": ["go", "integration"],
    "compat": ["go", "compat"],
    "cerberus": ["go", "cerberus"],
    "storage": ["storage"],
    "helm": ["helm"],
    "policy": ["go", "policy"],
    "docker": ["go", "docker"]
  }
}`

func loadTestRules(t *testing.T) *Rules {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ci-paths.json")
	if err := os.WriteFile(path, []byte(testConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	return rules
}

var allJobs = []string{"cerberus", "compat", "coverage", "docker", "go", "helm", "integration", "policy", "storage"}

func assertJobs(t *testing.T, decisions map[string]bool, wantTrue []string, wantReason bool, reason string) {
	t.Helper()
	trueSet := make(map[string]bool, len(wantTrue))
	for _, job := range wantTrue {
		trueSet[job] = true
	}
	for _, job := range allJobs {
		if decisions[job] != trueSet[job] {
			t.Errorf("job %s = %v, want %v (all: %v)", job, decisions[job], trueSet[job], decisions)
		}
	}
	if wantReason && strings.TrimSpace(reason) == "" {
		t.Errorf("reason is empty, want an explanation")
	}
	if !wantReason && reason != "" {
		t.Errorf("reason = %q, want empty", reason)
	}
}

func TestPlan(t *testing.T) {
	rules := loadTestRules(t)
	goFamily := []string{"cerberus", "compat", "coverage", "docker", "go", "integration", "policy"}
	tests := []struct {
		name     string
		files    []string
		wantTrue []string
		reason   bool
	}{
		{name: "root go file", files: []string{"main.go"}, wantTrue: goFamily},
		{name: "nested go file", files: []string{"internal/prom/engine.go"}, wantTrue: goFamily},
		{name: "root go.mod", files: []string{"go.mod"}, wantTrue: goFamily},
		{name: "nested go.mod", files: []string{"test/compat/prometheus/go.mod"}, wantTrue: goFamily},
		{name: "go.sum", files: []string{"go.sum"}, wantTrue: goFamily},
		{name: "vendor", files: []string{"vendor/github.com/x/y.go"}, wantTrue: goFamily},
		{name: "Makefile", files: []string{"Makefile"}, wantTrue: goFamily},
		{name: "coverage floor", files: []string{"test/quality/coverage-floor.json"}, wantTrue: []string{"cerberus", "compat", "coverage", "docker", "go", "integration", "policy"}},
		{name: "alpha exit plan", files: []string{"docs/alpha-exit-plan.md"}, wantTrue: goFamily},
		{name: "issue manifest", files: []string{"issues/alpha-exit.json"}, wantTrue: goFamily},
		{name: "helm values", files: []string{"deploy/helm/skopos/values.yaml"}, wantTrue: []string{"helm", "storage"}},
		{name: "helm migrate script", files: []string{"deploy/helm/skopos/files/migrate.sh"}, wantTrue: []string{"helm", "integration", "storage"}},
		{name: "clickhouse integration", files: []string{"test/integration/dedup/run.sh"}, wantTrue: []string{"integration"}},
		{name: "s3 integration", files: []string{"test/integration/s3/run.sh"}, wantTrue: []string{"integration", "storage"}},
		{name: "tempo compat", files: []string{"test/compat/tempo/run.sh"}, wantTrue: []string{"compat"}},
		{name: "cerberus compat", files: []string{"test/compat/cerberus/run.sh"}, wantTrue: []string{"cerberus", "compat"}},
		{name: "traceql corpus", files: []string{"test/corpus/traceql/corpus.go"}, wantTrue: append(append([]string{}, goFamily...), "compat", "cerberus")},
		{name: "logql baseline", files: []string{"docs/logql-upstream-baseline.md"}, wantTrue: []string{"compat", "policy"}},
		{name: "promql support matrix", files: []string{"docs/promql-support.md"}, wantTrue: []string{"compat", "policy"}},
		{name: "ci workflow", files: []string{".github/workflows/ci.yml"}, wantTrue: allJobs, reason: true},
		{name: "ci paths", files: []string{".github/ci-paths.json"}, wantTrue: allJobs, reason: true},
		{name: "linter config", files: []string{".golangci.yml"}, wantTrue: allJobs, reason: true},
		{name: "pinned tools module", files: []string{"internal/tools/go.mod"}, wantTrue: allJobs, reason: true},
		{name: "Dockerfile", files: []string{"Dockerfile"}, wantTrue: []string{"docker"}},
		{name: "dockerignore", files: []string{".dockerignore"}, wantTrue: []string{"docker"}},
		{name: "cmd file", files: []string{"cmd/gateway/main.go"}, wantTrue: goFamily},
		{name: "security policy", files: []string{"docs/security/ruleset.json"}, wantTrue: []string{"policy"}},
		{name: "osv ledger", files: []string{"docs/security/osv-exceptions.yaml"}, wantTrue: []string{"policy"}},
		{name: "scorecard review exception", files: []string{"docs/openssf-scorecard.md"}, wantTrue: []string{"policy"}},
		{name: "bench harness", files: []string{"test/bench/run.sh"}, wantTrue: goFamily},
		{name: "go testdata", files: []string{"internal/prom/testdata/gauge.sql"}, wantTrue: goFamily},
		{name: "e2e harness", files: []string{"test/e2e/obi/verify.sh"}, wantTrue: goFamily},
		{name: "checkpoint docs", files: []string{"docs/checkpoint.md"}, wantTrue: []string{"policy"}},
		{name: "unknown top-level", files: []string{"notes/guide.md"}, wantTrue: allJobs, reason: true},
		{name: "unknown script", files: []string{"scripts/release.sh"}, wantTrue: allJobs, reason: true},
		{name: "unknown readme", files: []string{"README.md"}, wantTrue: allJobs, reason: true},
		{name: "python harness", files: []string{"test/bench/bench.py"}, wantTrue: goFamily},
		{name: "empty file list", files: nil, wantTrue: allJobs, reason: true},
		{name: "rename counts both names", files: []string{"internal/a.go", "internal/b.go"}, wantTrue: goFamily},
		{name: "deletion still counts", files: []string{"internal/a.go"}, wantTrue: goFamily},
		{name: "multiple groups", files: []string{"internal/a.go", "deploy/helm/skopos/Chart.yaml"},
			wantTrue: []string{"cerberus", "compat", "coverage", "docker", "go", "helm", "integration", "policy", "storage"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decisions, reason := rules.Plan(test.files)
			assertJobs(t, decisions, test.wantTrue, test.reason, reason)
		})
	}
}

func TestGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{pattern: "**/*.go", path: "main.go", want: true},
		{pattern: "**/*.go", path: "internal/prom/engine.go", want: true},
		{pattern: "**/*.go", path: "internal/prom/engine.txt", want: false},
		{pattern: "**/go.mod", path: "go.mod", want: true},
		{pattern: "**/go.mod", path: "test/compat/prometheus/go.mod", want: true},
		{pattern: "deploy/**", path: "deploy/x", want: true},
		{pattern: "deploy/**", path: "deploy/a/b/c", want: true},
		{pattern: "deploy/**", path: "deploy", want: false},
		{pattern: "Dockerfile", path: "Dockerfile", want: true},
		{pattern: "Dockerfile", path: "other/Dockerfile", want: false},
		{pattern: "test/quality/**", path: "test/quality/coverage-floor.json", want: true},
		{pattern: "test/quality/**", path: "test/quality", want: false},
	}
	for _, test := range tests {
		compiled, err := compileGlob(test.pattern)
		if err != nil {
			t.Fatalf("compileGlob(%q): %v", test.pattern, err)
		}
		if got := compiled.MatchString(test.path); got != test.want {
			t.Errorf("glob %q against %q = %v, want %v", test.pattern, test.path, got, test.want)
		}
	}
}

func TestLoadRulesValidation(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{name: "malformed", json: `{`, want: "parse CI paths"},
		{name: "no groups", json: `{"jobs":{"go":["go"]}}`, want: "no groups"},
		{name: "no jobs", json: `{"groups":{"go":["**"]}}`, want: "no jobs"},
		{name: "empty group", json: `{"groups":{"go":[]},"jobs":{"go":["go"]}}`, want: "no patterns"},
		{name: "unknown group", json: `{"groups":{"go":["**"]},"jobs":{"go":["nope"]}}`, want: "unknown group"},
		{name: "empty job", json: `{"groups":{"go":["**"]},"jobs":{"":["go"]}}`, want: "empty name"},
		{name: "empty group name", json: `{"groups":{"":["**"]},"jobs":{"go":[""]}}`, want: "empty name"},
		{name: "job without groups", json: `{"groups":{"go":["**"]},"jobs":{"go":[]}}`, want: "no groups"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ci-paths.json")
			if err := os.WriteFile(path, []byte(test.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRules(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := LoadRules(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "read CI paths") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestJobNames(t *testing.T) {
	rules := loadTestRules(t)
	names := rules.JobNames()
	expected := append([]string(nil), allJobs...)
	sort.Strings(names)
	sort.Strings(expected)
	if strings.Join(names, ",") != strings.Join(expected, ",") {
		t.Fatalf("jobs = %v, want %v", names, expected)
	}
}

func TestCommittedConfigMatchesJobs(t *testing.T) {
	rules, err := LoadRules("../../../../.github/ci-paths.json")
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	// The config must cover every ci.yml job name. This list is the contract
	// from docs/ci-contract.md; adding a job without a path group fails here.
	want := []string{"cerberus", "compat", "coverage", "docker", "go", "helm", "integration", "policy", "storage"}
	names := rules.JobNames()
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("ci-paths jobs = %v, want %v", names, want)
	}
}
