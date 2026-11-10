package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIsDeterministic(t *testing.T) {
	first, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Generate is not deterministic")
	}
}

func TestMatrixCoversKnownRows(t *testing.T) {
	content, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| rate | supported | TestSupportedFunctionsEvaluate |",
		"| pattern parser | error | TestEveryLogQLConstructClassified |",
		"TraceQL structural operator",
		"| Maximum scan rows | 200000 | `internal/config` |",
		"| Maximum query range | 168h0m0s | `internal/config` |",
		"| Maximum response bytes | 67108864 (64 MiB) | `internal/config` |",
		"| In-flight cap | unset |",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("document missing %q", want)
		}
	}
}

func TestValidateRows(t *testing.T) {
	valid := []row{{Group: "g", Construct: "c", Status: "s", Test: "T"}}
	if err := validateRows(valid); err != nil {
		t.Fatalf("validateRows: %v", err)
	}
	if err := validateRows(nil); err == nil {
		t.Fatal("validateRows accepted an empty matrix")
	}
	if err := validateRows([]row{{Group: "g", Construct: "c", Status: "s"}}); err == nil || !strings.Contains(err.Error(), "no test reference") {
		t.Fatalf("error = %v, want a missing-test error", err)
	}
	if err := validateRows([]row{{Group: "g", Construct: "c", Status: "", Test: "T"}}); err == nil {
		t.Fatal("validateRows accepted an empty status")
	}
	if err := validateRows(append(valid, valid[0])); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("error = %v, want a duplicate error", err)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 512, want: "512 B"},
		{value: 1024, want: "1024 (1 KiB)"},
		{value: 1 << 20, want: "1048576 (1 MiB)"},
		{value: 64 << 20, want: "67108864 (64 MiB)"},
	}
	for _, test := range tests {
		if got := formatBytes(test.value); got != test.want {
			t.Errorf("formatBytes(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestEscape(t *testing.T) {
	if got := escape("a|b\nc"); got != `a\|b c` {
		t.Fatalf("escape = %q", got)
	}
}

func TestRunWritesAndChecks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "support-matrix.md")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d (stderr %s)", code, stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--out", out, "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run --check = %d (stderr %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is current") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	stale := "<!-- BEGIN GENERATED SUPPORT MATRIX -->\nstale\n<!-- END GENERATED SUPPORT MATRIX -->\n"
	if err := os.WriteFile(out, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--out", out, "--check"}, &stdout, &stderr); code != 1 {
		t.Fatalf("run --check = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "stale") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunMissingOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--out", filepath.Join(t.TempDir(), "missing.md"), "--check"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "read") {
		t.Fatalf("run = %d (stderr %s), want a read error", code, stderr.String())
	}
}

func TestRunRejectsBadFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--nope"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run = %d, want 2", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"extra"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("run = %d (stderr %s), want 2", code, stderr.String())
	}
}

func TestRepositoryDocumentIsCurrent(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--out", filepath.Join(root, "docs", "support-matrix.md"), "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("docs/support-matrix.md is stale: %s", stderr.String())
	}
}

func TestReplaceAndExtractBlock(t *testing.T) {
	block := beginMarker + "\nnew\n" + endMarker + "\n"
	document := "# Title\n\n" + beginMarker + "\nold\n" + endMarker + "\n\n## Tail\n"
	updated, err := replaceBlock(document, block)
	if err != nil {
		t.Fatalf("replaceBlock: %v", err)
	}
	if !strings.Contains(updated, "new") || strings.Contains(updated, "old") {
		t.Fatalf("updated document = %q", updated)
	}
	if !strings.Contains(updated, "## Tail") {
		t.Fatal("replaceBlock dropped the surrounding document")
	}
	extracted, err := extractBlock(updated)
	if err != nil {
		t.Fatalf("extractBlock: %v", err)
	}
	if extracted != block {
		t.Fatalf("extracted = %q, want %q", extracted, block)
	}

	appended, err := replaceBlock("# Title\n", block)
	if err != nil {
		t.Fatalf("replaceBlock append: %v", err)
	}
	if !strings.Contains(appended, "new") || !strings.Contains(appended, "# Title") {
		t.Fatalf("appended document = %q", appended)
	}
}

func TestMalformedMarkers(t *testing.T) {
	if _, err := replaceBlock(beginMarker+"\nno end\n", "block"); err == nil {
		t.Fatal("replaceBlock accepted a missing end marker")
	}
	if _, err := extractBlock("no markers"); err == nil {
		t.Fatal("extractBlock accepted a document without markers")
	}
}
