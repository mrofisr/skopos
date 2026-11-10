package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPassAndFail(t *testing.T) {
	pass := render(report{TotalRequests: 10, Errors: 4, Rejections: 4})
	if !strings.Contains(pass, "| Gate | **pass** |") {
		t.Fatalf("pass block = %q", pass)
	}
	fail := render(report{TotalRequests: 10, TransportErrors: 1})
	if !strings.Contains(fail, "| Gate | **fail** |") {
		t.Fatalf("fail block = %q", fail)
	}
}

func TestReplaceBlock(t *testing.T) {
	document := "# Benchmarks\n\n" + beginMarker + "\nold\n" + endMarker + "\n\n## Tail\n"
	updated, err := replaceBlock(document, beginMarker+"\nnew\n"+endMarker+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(updated, "old") || !strings.Contains(updated, "new") || !strings.Contains(updated, "## Tail") {
		t.Fatalf("updated = %q", updated)
	}
	appended, err := replaceBlock("# Benchmarks\n", beginMarker+"\nnew\n"+endMarker+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(appended, "new") {
		t.Fatalf("appended = %q", appended)
	}
	if _, err := replaceBlock(beginMarker+"\nno end\n", "block"); err == nil {
		t.Fatal("replaceBlock accepted malformed markers")
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "gate.json")
	if err := os.WriteFile(input, []byte(`{"total_requests":10,"errors":4,"rejections":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(dir, "benchmarks.md")
	document := "# Benchmarks\n\n" + beginMarker + "\nold\n" + endMarker + "\n"
	if err := os.WriteFile(reportPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"--input", input, "--report", reportPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d (stderr %s)", code, stderr.String())
	}
	updated, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "**pass**") || strings.Contains(string(updated), "old") {
		t.Fatalf("report = %q", updated)
	}
}

func TestRunErrors(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run without input = %d, want 2", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--input", filepath.Join(t.TempDir(), "missing.json")}, &stdout, &stderr); code != 1 {
		t.Fatalf("run with missing input = %d, want 1", code)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "gate.json")
	if err := os.WriteFile(input, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--input", input}, &stdout, &stderr); code != 1 {
		t.Fatalf("run with malformed input = %d, want 1", code)
	}
}
