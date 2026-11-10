package cigate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

const jobsConfig = `{
  "groups": {"go": ["**/*.go"]},
  "jobs": {
    "go": ["go"],
    "coverage": ["go"],
    "helm": ["go"]
  }
}`

func testEnv() (command.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(0, 0) },
	}, stdout, stderr
}

func writePaths(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ci-paths.json")
	if err := os.WriteFile(path, []byte(jobsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func needsWith(results map[string]string, outputs map[string]string) Needs {
	needs := Needs{}
	for job, result := range results {
		needs[job] = struct {
			Result  string            `json:"result"`
			Outputs map[string]string `json:"outputs"`
		}{Result: result, Outputs: nil}
	}
	entry := needs["changes"]
	entry.Outputs = outputs
	needs["changes"] = entry
	return needs
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "ci-gate" {
		t.Fatalf("Name = %q, want ci-gate", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestEvaluatePassesWhenEveryJobMatchesItsGroup(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes":  "success",
		"go":       "success",
		"coverage": "success",
		"helm":     "skipped",
	}, map[string]string{"go": "true", "coverage": "true", "helm": "false"})
	if problems := Evaluate(needs, []string{"go", "coverage", "helm"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestEvaluateFailsOnJobFailure(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "failure",
	}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], `"go" result is "failure"`) {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateFailsOnCancelledJob(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "cancelled",
	}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], "cancelled") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateFailsWhenChangesFails(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "failure",
		"go":      "success",
	}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], `job "changes"`) {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateFailsWhenChangesIsMissing(t *testing.T) {
	needs := Needs{"go": {Result: "success"}}
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], "missing") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateFailsOnWrongSkip(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "skipped",
	}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], "skipped although") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateAllowsSkipWhenGroupIsFalse(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "skipped",
	}, map[string]string{"go": "false"})
	if problems := Evaluate(needs, []string{"go"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestEvaluateFailsOnMissingJob(t *testing.T) {
	needs := needsWith(map[string]string{"changes": "success"}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], "missing from the needs context") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateFailsOnUnknownResult(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "neutral",
	}, map[string]string{"go": "true"})
	problems := Evaluate(needs, []string{"go"})
	if len(problems) != 1 || !strings.Contains(problems[0], "unknown result") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestEvaluateIgnoresJobsOutsideTheConfig(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "success",
		"other":   "failure",
	}, map[string]string{"go": "true"})
	if problems := Evaluate(needs, []string{"go"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestEvaluateSkipsMetaJobs(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "success",
		"ci-gate": "failure",
	}, map[string]string{"go": "true"})
	if problems := Evaluate(needs, []string{"go", "ci-gate"}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestCommandPasses(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes":  "success",
		"go":       "success",
		"coverage": "success",
		"helm":     "skipped",
	}, map[string]string{"go": "true", "coverage": "true", "helm": "false"})
	data, err := json.Marshal(needs)
	if err != nil {
		t.Fatal(err)
	}
	env, stdout, _ := testEnv()
	env.Getenv = func(key string) string {
		if key == "NEEDS_JSON" {
			return string(data)
		}
		return ""
	}
	if err := (Command{}).Run(context.Background(), env, []string{"--paths", writePaths(t)}); err != nil {
		t.Fatalf("ci-gate: %v", err)
	}
	if !strings.Contains(stdout.String(), "3 job(s) passed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestCommandReportsProblems(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes": "success",
		"go":      "failure",
	}, map[string]string{"go": "true"})
	data, err := json.Marshal(needs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "needs.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, stderr := testEnv()
	err = (Command{}).Run(context.Background(), env, []string{"--needs", path, "--paths", writePaths(t)})
	if err == nil || !strings.Contains(err.Error(), "CI gate problem") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(stderr.String(), `job "go" result is "failure"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCommandReadsStdin(t *testing.T) {
	needs := needsWith(map[string]string{
		"changes":  "success",
		"go":       "success",
		"coverage": "skipped",
		"helm":     "skipped",
	}, map[string]string{"go": "true"})
	data, err := json.Marshal(needs)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = old })
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--needs", "-", "--paths", writePaths(t)}); err != nil {
		t.Fatalf("ci-gate: %v", err)
	}
}

func TestCommandRequiresNeeds(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--paths", writePaths(t)})
	if err == nil || !strings.Contains(err.Error(), "needs context is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "needs.json")
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--needs", path, "--paths", writePaths(t)})
	if err == nil || !strings.Contains(err.Error(), "parse needs context") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandRejectsBadFlags(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("ci-gate accepted an unknown flag")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}
