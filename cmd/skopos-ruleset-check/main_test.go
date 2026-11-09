package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"docs/security/ruleset.json",
		"docs/openssf-scorecard.md",
		".github/workflows/ci.yml",
		".github/workflows/dependency-review.yml",
	}
	for _, relative := range files {
		data, err := os.ReadFile(filepath.Join("..", "..", relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		destination := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(destination), err)
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	return root
}

func TestRunAcceptsCurrentRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--root", "../.."}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (stderr %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "9 checks") {
		t.Fatalf("stdout = %q, want 9 checks", stdout.String())
	}
}

func TestRunDetectsRenamedJob(t *testing.T) {
	root := copyRepository(t)
	path := filepath.Join(root, ".github/workflows/ci.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	data = bytes.ReplaceAll(data, []byte("name: Go build, vet, test"), []byte("name: Go test"))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write ci.yml: %v", err)
	}
	err = run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Go build, vet, test") {
		t.Fatalf("run error = %v, want a renamed-job error", err)
	}
}

func TestRunDetectsMissingWorkflow(t *testing.T) {
	root := copyRepository(t)
	if err := os.Remove(filepath.Join(root, ".github/workflows/ci.yml")); err != nil {
		t.Fatalf("remove ci.yml: %v", err)
	}
	err := run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "read workflow") {
		t.Fatalf("run error = %v, want a missing-workflow error", err)
	}
}

func TestRunRequiresReviewExceptionDocumentation(t *testing.T) {
	root := copyRepository(t)
	path := filepath.Join(root, "docs/openssf-scorecard.md")
	if err := os.WriteFile(path, []byte("# Scorecard\n"), 0o644); err != nil {
		t.Fatalf("write scorecard doc: %v", err)
	}
	err := run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "review exception") {
		t.Fatalf("run error = %v, want a review-exception error", err)
	}
}

func TestRunRequiresReviewExceptionFile(t *testing.T) {
	root := copyRepository(t)
	if err := os.Remove(filepath.Join(root, "docs/openssf-scorecard.md")); err != nil {
		t.Fatalf("remove scorecard doc: %v", err)
	}
	err := run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "read review exception") {
		t.Fatalf("run error = %v, want a missing-document error", err)
	}
}

func TestRunRejectsWrongReviewExceptionAnchor(t *testing.T) {
	root := copyRepository(t)
	path := filepath.Join(root, "docs/security/ruleset.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ruleset: %v", err)
	}
	data = bytes.ReplaceAll(data, []byte("#review-exception-one-maintainer"), []byte("#wrong-anchor"))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	err = run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("run error = %v, want an anchor error", err)
	}
}

func TestRunMissingRuleset(t *testing.T) {
	err := run([]string{"--root", t.TempDir()}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "read ruleset") {
		t.Fatalf("run error = %v, want a missing-ruleset error", err)
	}
}

func TestRunRejectsMalformedRuleset(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs/security/ruleset.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create ruleset directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	err := run([]string{"--root", root}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ruleset.name") {
		t.Fatalf("run error = %v, want a ruleset validation error", err)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	err := run([]string{"unexpected"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("run error = %v, want an argument error", err)
	}
}
