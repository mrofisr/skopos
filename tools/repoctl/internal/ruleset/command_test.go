package ruleset

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

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

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "ruleset" {
		t.Fatalf("Name = %q, want ruleset", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestRulesetValidatesTheRepository(t *testing.T) {
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--root", repoRoot(t)}); err != nil {
		t.Fatalf("ruleset: %v", err)
	}
	if !strings.Contains(stdout.String(), "check-ruleset:") {
		t.Fatalf("stdout = %q, want the ruleset summary", stdout.String())
	}
}

func TestRulesetRejectsUnexpectedArguments(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}

func TestRulesetRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("ruleset accepted an unknown flag")
	}
}

func TestRulesetMissingRuleset(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--root", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "ruleset") {
		t.Fatalf("error = %v, want a load error", err)
	}
}

func TestRulesetMissingWorkflow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	document := `{"required_workflows":["absent.yml"],"ruleset":{"name":"main"}}`
	if err := os.WriteFile(filepath.Join(dir, "ruleset.json"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--root", root})
	if err == nil || !strings.Contains(err.Error(), `read workflow "absent.yml"`) {
		t.Fatalf("error = %v, want a missing-workflow error", err)
	}
}

func TestCheckReviewException(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "docs", "maintainers.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte(reviewExceptionHeading+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkReviewException(root, "docs/maintainers.md#"+reviewExceptionAnchor); err != nil {
		t.Fatalf("checkReviewException: %v", err)
	}
	if err := checkReviewException(root, "docs/maintainers.md"); err != nil {
		t.Fatalf("checkReviewException without an anchor: %v", err)
	}
	if err := checkReviewException(root, "#"+reviewExceptionAnchor); err == nil || !strings.Contains(err.Error(), "no file path") {
		t.Fatalf("error = %v, want a missing-path error", err)
	}
	if err := checkReviewException(root, "docs/maintainers.md#other"); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("error = %v, want an anchor error", err)
	}
	if err := checkReviewException(root, "docs/missing.md"); err == nil || !strings.Contains(err.Error(), "read review exception") {
		t.Fatalf("error = %v, want a read error", err)
	}
	other := filepath.Join(root, "docs", "other.md")
	if err := os.WriteFile(other, []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkReviewException(root, "docs/other.md"); err == nil || !strings.Contains(err.Error(), "does not document") {
		t.Fatalf("error = %v, want a documentation error", err)
	}
}

func TestRulesetReportsValidationProblems(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	document := `{"required_workflows":[],"ruleset":{"name":"main"}}`
	if err := os.WriteFile(filepath.Join(dir, "ruleset.json"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--root", root})
	if err == nil || !strings.Contains(err.Error(), "at least one required workflow") {
		t.Fatalf("error = %v, want a validation error", err)
	}
}

func TestRulesetReportsTheReviewException(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ruleset.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range validWorkflows() {
		if err := os.WriteFile(filepath.Join(workflowDir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env, _, _ := testEnv()
	err = (Command{}).Run(context.Background(), env, []string{"--root", root})
	if err == nil || !strings.Contains(err.Error(), "read review exception") {
		t.Fatalf("error = %v, want a review-exception error", err)
	}
}
