package workflowsdoc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

const ciWorkflow = `name: CI
on:
  pull_request:
    branches:
      - development
      - main
  workflow_dispatch:
permissions:
  contents: read
concurrency:
  group: ci
  cancel-in-progress: true
jobs:
  go:
    name: Go build
    runs-on: ubuntu-24.04
    timeout-minutes: 10
  gate:
    name: CI gate
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`

const releaseWorkflow = `name: Release
on:
  push:
    tags:
      - "v*"
permissions:
  contents: read
jobs:
  release:
    name: Release
    runs-on: ubuntu-24.04
    timeout-minutes: 10
`

const rulesetDocument = `{
  "required_workflows": ["ci.yml"],
  "allow_extra_jobs": true,
  "ruleset": {
    "name": "main-protection",
    "rules": [
      {
        "type": "required_status_checks",
        "parameters": {
          "required_status_checks": [
            {"context": "CI gate", "integration_id": 15368}
          ]
        }
      }
    ]
  }
}`

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(ciWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yml"), []byte(releaseWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesetPath := filepath.Join(root, "ruleset.json")
	if err := os.WriteFile(rulesetPath, []byte(rulesetDocument), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, rulesetPath
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

func TestGenerate(t *testing.T) {
	dir, rulesetPath := fixture(t)
	content, err := Generate(dir, rulesetPath)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, want := range []string{
		"| `ci.yml` | `pull_request`, `workflow_dispatch` | `Go build`, `CI gate` | `CI gate` |",
		"| `release.yml` | `push` | `Release` | — |",
		"| `CI gate` | `ci.yml` | `gate` |",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("document missing %q:\n%s", want, content)
		}
	}
}

func TestCommandWritesAndChecks(t *testing.T) {
	dir, rulesetPath := fixture(t)
	out := filepath.Join(t.TempDir(), "workflows.md")
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{
		"--dir", dir, "--ruleset", rulesetPath, "--out", out,
	}); err != nil {
		t.Fatalf("workflows-doc: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{
		"--dir", dir, "--ruleset", rulesetPath, "--out", out, "--check",
	}); err != nil {
		t.Fatalf("workflows-doc --check: %v", err)
	}
	if !strings.Contains(stdout.String(), "is current") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	if err := os.WriteFile(out, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ = testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--dir", dir, "--ruleset", rulesetPath, "--out", out, "--check",
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want a stale-document error", err)
	}
}

func TestGenerateMissingInputs(t *testing.T) {
	if _, err := Generate(filepath.Join(t.TempDir(), "missing"), "missing.json"); err == nil {
		t.Fatal("Generate succeeded without a directory")
	}
	dir, _ := fixture(t)
	if _, err := Generate(dir, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("Generate succeeded without a ruleset")
	}
	empty := t.TempDir()
	if _, err := Generate(empty, "missing.json"); err == nil {
		t.Fatal("Generate succeeded without workflows")
	}
}

func TestGenerateRejectsMalformedWorkflow(t *testing.T) {
	dir, rulesetPath := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "bad.yml"), []byte("not a workflow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, rulesetPath); err == nil {
		t.Fatal("Generate succeeded on a malformed workflow")
	}
}

func TestCommandCheckMissingOutput(t *testing.T) {
	dir, rulesetPath := fixture(t)
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--dir", dir, "--ruleset", rulesetPath,
		"--out", filepath.Join(t.TempDir(), "missing.md"), "--check",
	})
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestCommandRejectsBadFlags(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("workflows-doc accepted an unknown flag")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}

func TestRepositoryDocumentIsCurrent(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{
		"--dir", filepath.Join(root, ".github", "workflows"),
		"--ruleset", filepath.Join(root, "docs", "security", "ruleset.json"),
		"--out", filepath.Join(root, "docs", "workflows.md"),
		"--check",
	}); err != nil {
		t.Fatalf("docs/workflows.md is stale: %v", err)
	}
}
