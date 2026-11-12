package workflowlint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/tools/repoctl/internal/command"
)

const validWorkflow = `name: Test
on:
  pull_request:
    branches:
      - development
      - main
  workflow_dispatch:
permissions:
  contents: read
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    name: Go
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`

func analyze(t *testing.T, body string) Report {
	t.Helper()
	report, err := Analyze([]byte(body))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return report
}

func rules(problems []Problem) []string {
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		out = append(out, problem.Rule)
	}
	return out
}

func TestValidWorkflowPasses(t *testing.T) {
	report := analyze(t, validWorkflow)
	if problems := Problems(report); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if report.Name != "Test" {
		t.Fatalf("Name = %q", report.Name)
	}
	if strings.Join(report.Jobs, ",") != "go" {
		t.Fatalf("jobs = %v", report.Jobs)
	}
}

func TestBarePushFails(t *testing.T) {
	body := `on:
  push:
permissions:
  contents: read
jobs:
  go:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "bare-push") {
		t.Fatalf("problems = %v, want bare-push", rules(problems))
	}
}

func TestPushWithTagsPasses(t *testing.T) {
	body := `on:
  push:
    tags:
      - "v*"
permissions:
  contents: read
jobs:
  release:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	if problems := Problems(analyze(t, body)); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestPullRequestWithoutBranchesFails(t *testing.T) {
	body := `on:
  pull_request:
permissions:
  contents: read
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "pull-request-branches") {
		t.Fatalf("problems = %v, want pull-request-branches", rules(problems))
	}
}

func TestPullRequestWithoutConcurrencyFails(t *testing.T) {
	body := `on:
  pull_request:
    branches:
      - main
permissions:
  contents: read
jobs:
  go:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "pull-request-concurrency") {
		t.Fatalf("problems = %v, want pull-request-concurrency", rules(problems))
	}
}

func TestPathFilterFails(t *testing.T) {
	body := `on:
  pull_request:
    branches:
      - main
    paths:
      - "docs/**"
permissions:
  contents: read
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "no-path-filters") {
		t.Fatalf("problems = %v, want no-path-filters", rules(problems))
	}
}

func TestScheduleWithoutDispatchFails(t *testing.T) {
	body := `on:
  schedule:
    - cron: "0 3 * * *"
permissions:
  contents: read
jobs:
  lint:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "schedule-dispatch") {
		t.Fatalf("problems = %v, want schedule-dispatch", rules(problems))
	}
}

func TestMissingJobTimeoutFails(t *testing.T) {
	body := `on:
  pull_request:
    branches:
      - main
permissions:
  contents: read
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    runs-on: ubuntu-24.04
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "job-timeout") {
		t.Fatalf("problems = %v, want job-timeout", rules(problems))
	}
}

func TestMissingPermissionsFails(t *testing.T) {
	body := `on:
  pull_request:
    branches:
      - main
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`
	problems := Problems(analyze(t, body))
	if !strings.Contains(strings.Join(rules(problems), ","), "top-level-permissions") {
		t.Fatalf("problems = %v, want top-level-permissions", rules(problems))
	}
}

func TestMalformedWorkflowsFail(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "no on block", body: "jobs:\n  go:\n    timeout-minutes: 1\n", want: "no on block"},
		{name: "no jobs block", body: "on:\n  push:\n    tags: [v1]\n", want: "no jobs block"},
		{name: "tabs", body: "on:\n\tpush:\n", want: "tabs"},
		{name: "bad top level", body: "on:\n  push:\n    tags: [v1]\n- bad\n", want: "expected a top-level key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Analyze([]byte(test.body)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInlineBranches(t *testing.T) {
	body := `on:
  pull_request: {branches: [main]}
permissions:
  contents: read
concurrency:
  group: test
  cancel-in-progress: true
jobs:
  go:
    timeout-minutes: 10
`
	if problems := Problems(analyze(t, body)); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

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

func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommandPasses(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "ci.yml", validWorkflow)
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", dir}); err != nil {
		t.Fatalf("workflow-lint: %v", err)
	}
	if !strings.Contains(stdout.String(), "1 workflows follow") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestCommandReportsProblems(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "bad.yml", "on:\n  push:\npermissions:\n  contents: read\njobs:\n  go:\n    timeout-minutes: 1\n")
	env, _, stderr := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "workflow problem") {
		t.Fatalf("error = %v", err)
	}
	for _, want := range []string{"bad.yml", "bare-push"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestCommandReportsParseErrors(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "bad.yml", "no workflow here\n")
	env, _, stderr := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--dir", dir})
	if err == nil || !strings.Contains(stderr.String(), "parse") {
		t.Fatalf("error = %v (stderr %s), want a parse error", err, stderr.String())
	}
}

func TestCommandRequiresWorkflows(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "no workflows") {
		t.Fatalf("error = %v, want a no-workflows error", err)
	}
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", filepath.Join(t.TempDir(), "missing")}); err == nil || !strings.Contains(err.Error(), "read workflows") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestCommandRejectsBadFlags(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("workflow-lint accepted an unknown flag")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}

func TestRepositoryWorkflowsPass(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	env, _, stderr := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", filepath.Join(root, ".github", "workflows")}); err != nil {
		t.Fatalf("repository workflows: %v (stderr %s)", err, stderr.String())
	}
}
