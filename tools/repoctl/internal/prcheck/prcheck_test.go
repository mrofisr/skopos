package prcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeSource struct {
	issues map[int]Issue
	err    error
	calls  []int
}

func (f *fakeSource) Issue(_ context.Context, number int) (Issue, error) {
	f.calls = append(f.calls, number)
	if f.err != nil {
		return Issue{}, f.err
	}
	issue, ok := f.issues[number]
	if !ok {
		return Issue{}, errors.New("not found")
	}
	return issue, nil
}

func alphaIssue(number int) Issue {
	return Issue{Number: number, State: "open", Labels: []string{"phase:alpha-exit", "ec:6"}}
}

func postAlphaIssue(number int) Issue {
	return Issue{Number: number, State: "open", Labels: []string{"backlog:post-alpha"}}
}

func betaIssue(number int) Issue {
	return Issue{Number: number, State: "open", Labels: []string{"phase:beta"}}
}

func joined(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, " | ")
}

func containsNotice(notices []string, want string) bool {
	for _, notice := range notices {
		if strings.Contains(notice, want) {
			return true
		}
	}
	return false
}

func TestEvaluateFixtures(t *testing.T) {
	tests := []struct {
		name       string
		input      Input
		issues     map[int]Issue
		wantErr    string
		wantNotice string
	}{
		{
			name:   "valid reference to an open alpha issue",
			input:  Input{Title: "quality: add a guard", Body: "Closes #57"},
			issues: map[int]Issue{57: alphaIssue(57)},
		},
		{
			name:   "valid reference to an open post-alpha issue",
			input:  Input{Title: "quality: add a guard", Body: "Refs #58"},
			issues: map[int]Issue{58: postAlphaIssue(58)},
		},
		{
			name:   "valid reference to an open beta issue",
			input:  Input{Title: "bench: bound the instant path", Body: "Closes #61"},
			issues: map[int]Issue{61: betaIssue(61)},
		},
		{
			name:    "missing reference",
			input:   Input{Title: "quality: add a guard", Body: "No issue link here"},
			wantErr: "no Closes #N or Refs #N reference",
		},
		{
			name:  "closed issue",
			input: Input{Title: "quality: add a guard", Body: "Closes #57"},
			issues: map[int]Issue{57: {
				Number: 57, State: "closed", Labels: []string{"phase:alpha-exit"},
			}},
			wantErr: "is closed",
		},
		{
			name:  "closed post-alpha issue",
			input: Input{Title: "quality: add a guard", Body: "Closes #58"},
			issues: map[int]Issue{58: {
				Number: 58, State: "closed", Labels: []string{"backlog:post-alpha"},
			}},
			wantErr: "is closed",
		},
		{
			name:  "closed beta issue",
			input: Input{Title: "bench: bound the instant path", Body: "Closes #61"},
			issues: map[int]Issue{61: {
				Number: 61, State: "closed", Labels: []string{"phase:beta"},
			}},
			wantErr: "is closed",
		},
		{
			name:  "unlabelled issue",
			input: Input{Title: "quality: add a guard", Body: "Refs #24"},
			issues: map[int]Issue{24: {
				Number: 24, State: "open", Labels: nil,
			}},
			wantErr: "not labelled phase:alpha-exit, phase:beta or backlog:post-alpha",
		},
		{
			name:  "exempt commit type",
			input: Input{Title: "docs: describe the PR guard", Body: "No issue link"},
		},
		{
			name: "frozen path without the label",
			input: Input{
				Title: "checkpoint: fix replay",
				Body:  "Closes #57",
				Files: []string{"internal/checkpoint/wal.go"},
			},
			issues:  map[int]Issue{57: alphaIssue(57)},
			wantErr: "frozen-exception",
		},
		{
			name: "frozen path with the label and a reason",
			input: Input{
				Title:  "checkpoint: fix replay",
				Body:   "Closes #57\n\nFrozen-exception: fixes a WAL replay corruption bug",
				Labels: []string{"frozen-exception"},
				Files:  []string{"internal/checkpoint/wal.go"},
			},
			issues: map[int]Issue{57: alphaIssue(57)},
		},
		{
			name:    "malformed reference",
			input:   Input{Title: "quality: add a guard", Body: "Closes 57"},
			wantErr: "malformed Closes/Refs reference",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeSource{issues: test.issues}
			result := Evaluate(context.Background(), test.input, source)
			got := joined(result.Errors)
			if test.wantErr == "" && got != "" {
				t.Fatalf("unexpected errors: %s", got)
			}
			if test.wantErr != "" && !strings.Contains(got, test.wantErr) {
				t.Fatalf("errors = %q, want %q", got, test.wantErr)
			}
			if test.wantNotice != "" && !containsNotice(result.Notices, test.wantNotice) {
				t.Fatalf("notices = %v, want %q", result.Notices, test.wantNotice)
			}
		})
	}
}

func TestEvaluateForkSkipsIssueLookup(t *testing.T) {
	source := &fakeSource{err: errors.New("API unreachable")}
	result := Evaluate(context.Background(), Input{
		Title: "quality: fix a guard",
		Body:  "Closes #57",
		Fork:  true,
	}, source)
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %s, want none", joined(result.Errors))
	}
	if len(source.calls) != 0 {
		t.Fatalf("lookup calls = %v, want none", source.calls)
	}
	if !containsNotice(result.Notices, "fork PR") || !containsNotice(result.Notices, "issue lookup skipped") {
		t.Fatalf("notices = %v, want the fork skip notice", result.Notices)
	}
}

func TestEvaluateForkStillChecksFrozenPaths(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title: "checkpoint: fix a guard",
		Body:  "Closes #57",
		Fork:  true,
		Files: []string{"internal/app/checkpoint/run.go"},
	}, &fakeSource{})
	if !strings.Contains(joined(result.Errors), "frozen-exception") {
		t.Fatalf("errors = %s, want the freeze error", joined(result.Errors))
	}
}

func TestEvaluateAPIFailureFailsClosed(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title: "quality: fix a guard",
		Body:  "Closes #57",
	}, &fakeSource{err: errors.New("API unreachable")})
	if !strings.Contains(joined(result.Errors), "lookup issue #57") {
		t.Fatalf("errors = %s, want a fail-closed lookup error", joined(result.Errors))
	}
}

func TestEvaluateFrozenExceptionNeedsReason(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title:  "checkpoint: fix a guard",
		Body:   "Closes #57",
		Labels: []string{"frozen-exception"},
		Files:  []string{"internal/checkpoint/wal.go"},
	}, &fakeSource{issues: map[int]Issue{57: alphaIssue(57)}})
	if !strings.Contains(joined(result.Errors), "Frozen-exception:") {
		t.Fatalf("errors = %s, want the missing reason error", joined(result.Errors))
	}
}

func TestEvaluateAcceptsOneOpenAlphaAmongReferences(t *testing.T) {
	result := Evaluate(context.Background(), Input{
		Title: "quality: fix a guard",
		Body:  "Closes #57\nRefs #24",
	}, &fakeSource{issues: map[int]Issue{
		57: alphaIssue(57),
		24: {Number: 24, State: "open", Labels: []string{"phase:beta"}},
	}})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %s, want none", joined(result.Errors))
	}
}

func TestEvaluatePromotionPR(t *testing.T) {
	source := &fakeSource{err: errors.New("must not be called")}
	result := Evaluate(context.Background(), Input{
		Title:   "release: promote development",
		Body:    "no reference",
		BaseRef: "main",
		HeadRef: "development",
		Files:   []string{"internal/semctx/api.go", "deploy/systemd/skopos-gateway.service"},
	}, source)
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %s, want none", joined(result.Errors))
	}
	if len(source.calls) != 0 {
		t.Fatalf("lookup calls = %v, want none", source.calls)
	}
	if !containsNotice(result.Notices, "promotion PR") {
		t.Fatalf("notices = %v, want the promotion notice", result.Notices)
	}
}

func TestEvaluateOtherPRIntoMainIsChecked(t *testing.T) {
	source := &fakeSource{issues: map[int]Issue{57: alphaIssue(57)}}
	result := Evaluate(context.Background(), Input{
		Title:   "feat: x",
		Body:    "no reference",
		BaseRef: "main",
		HeadRef: "feature/x",
	}, source)
	if len(result.Errors) == 0 || !strings.Contains(joined(result.Errors), "no Closes #N") {
		t.Fatalf("errors = %v, want the traceability failure", result.Errors)
	}
}
