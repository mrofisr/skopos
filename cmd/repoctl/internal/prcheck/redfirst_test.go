package prcheck

import (
	"context"
	"strings"
	"testing"
)

func TestRedFirstFixtures(t *testing.T) {
	tests := []struct {
		name    string
		labels  []string
		commits []Commit
		wantErr string
	}{
		{
			name:   "test-only first commit",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/coverage_test.go"}},
				{SHA: "b", Files: []string{"internal/quality/coverage.go"}},
			},
		},
		{
			name:   "implementation in the first commit",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/coverage.go"}},
				{SHA: "b", Files: []string{"internal/quality/coverage_test.go"}},
			},
			wantErr: "first commit",
		},
		{
			name:   "single-commit PR",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/coverage_test.go"}},
			},
			wantErr: "at least two commits",
		},
		{
			name:   "testdata fixture",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/testdata/floor.json"}},
				{SHA: "b", Files: []string{"internal/quality/coverage.go"}},
			},
		},
		{
			name:   "test fixture tree",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a", Files: []string{"test/quality/coverage-floor.json"}},
				{SHA: "b", Files: []string{"internal/quality/coverage.go"}},
			},
		},
		{
			name:   "exempt label",
			labels: []string{"type:micro", "red-first-exempt"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/coverage.go"}},
			},
		},
		{
			name:   "not a micro PR",
			labels: []string{"type:spike"},
			commits: []Commit{
				{SHA: "a", Files: []string{"internal/quality/coverage.go"}},
			},
		},
		{
			name:   "empty first commit",
			labels: []string{"type:micro"},
			commits: []Commit{
				{SHA: "a"},
				{SHA: "b", Files: []string{"internal/quality/coverage.go"}},
			},
			wantErr: "empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := checkRedFirst(test.labels, test.commits)
			got := joined(errs)
			if test.wantErr == "" && got != "" {
				t.Fatalf("unexpected errors: %s", got)
			}
			if test.wantErr != "" && !strings.Contains(got, test.wantErr) {
				t.Fatalf("errors = %q, want %q", got, test.wantErr)
			}
		})
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA(""); got != "?" {
		t.Errorf("shortSHA(empty) = %q, want ?", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA(short) = %q, want abc", got)
	}
	if got := shortSHA("abcdef1234567890"); got != "abcdef123456" {
		t.Errorf("shortSHA(long) = %q, want the first 12 characters", got)
	}
}

func TestEvaluateRequiresSourceForMaintainers(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title: "quality: add a guard",
		Body:  "Closes #57",
	}, nil)
	if !strings.Contains(joined(result.Errors), "no GitHub source configured") {
		t.Fatalf("errors = %s, want the missing source error", joined(result.Errors))
	}
}

func TestReferencesDeduplicates(t *testing.T) {
	got := references("Closes #57\nRefs #57\nCloses #0\nRefs #999999999999999999999999")
	if len(got) != 1 || got[0] != 57 {
		t.Fatalf("references = %v, want [57]", got)
	}
}

func TestEvaluateRunsRedFirstForMicroPRs(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title:  "quality: add a guard",
		Body:   "Closes #57",
		Labels: []string{"type:micro"},
		Commits: []Commit{
			{SHA: "a", Files: []string{"internal/quality/coverage.go"}},
		},
	}, &fakeSource{issues: map[int]Issue{57: alphaIssue(57)}})
	if !strings.Contains(joined(result.Errors), "at least two commits") {
		t.Fatalf("errors = %s, want the red-first error", joined(result.Errors))
	}
}
