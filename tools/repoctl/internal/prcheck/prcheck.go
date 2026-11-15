// Package prcheck enforces alpha-exit/post-alpha traceability and frozen-path
// rules on pull requests.
package prcheck

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Issue is the subset of a GitHub issue that the guard needs.
type Issue struct {
	Number int
	State  string
	Labels []string
}

// Source looks up one issue. It must return an error when the lookup fails so
// the guard can fail closed for maintainers.
type Source interface {
	Issue(ctx context.Context, number int) (Issue, error)
}

// Commit is one pull request commit and the files it touched. GitHub returns
// commits oldest first, so the first element is the first commit.
type Commit struct {
	SHA   string
	Files []string
}

// Input is one pull request.
type Input struct {
	Title   string
	Body    string
	Labels  []string
	Files   []string
	Commits []Commit
	Fork    bool
	// BaseRef and HeadRef identify a promotion pull request. The
	// development-to-main promotion is a release step, not a feature PR, so
	// it is exempt from traceability; the freeze and red-first rules still
	// apply.
	BaseRef string
	HeadRef string
}

// Result is the outcome of the guard. Notices are non-blocking messages, such
// as the fork skip. Errors block the pull request.
type Result struct {
	Errors  []error
	Notices []string
}

// FrozenPaths are frozen until the alpha exit apart from bug and security
// fixes with a logged exception.
var FrozenPaths = []string{
	"internal/semctx",
	"internal/app/mcp",
	"internal/checkpoint",
	"internal/app/checkpoint",
	"deploy/systemd",
}

const (
	alphaLabel          = "phase:alpha-exit"
	postAlphaLabel      = "backlog:post-alpha"
	frozenLabel         = "frozen-exception"
	frozenReasonKey     = "Frozen-exception:"
	microLabel          = "type:micro"
	redFirstExemptLabel = "red-first-exempt"
)

var (
	referenceRE    = regexp.MustCompile(`(?i)\b(?:closes|refs)\s+#([0-9]+)\b`)
	keywordRE      = regexp.MustCompile(`(?i)\b(?:closes|refs)\b`)
	exemptTypeRE   = regexp.MustCompile(`(?i)^(fix|deps|docs|security|ci)(\([^)]*\))?!?:`)
	frozenReasonRE = regexp.MustCompile(`(?im)^[ \t]*frozen-exception:[ \t]*(\S.*?)[ \t]*$`)
)

// Evaluate applies the traceability rule and the frozen-path rule. The
// traceability rule is skipped for exempt commit types. The freeze rule always
// runs, including for fork pull requests.
func Evaluate(ctx context.Context, in Input, source Source) Result {
	var result Result
	switch {
	case isPromotion(in):
		result.Notices = append(result.Notices,
			"promotion PR: traceability and frozen-path checks skipped")
	case !exemptTypeRE.MatchString(strings.TrimSpace(in.Title)):
		errs, notices := checkTraceability(ctx, in, source)
		result.Errors = append(result.Errors, errs...)
		result.Notices = append(result.Notices, notices...)
		result.Errors = append(result.Errors, checkFrozen(in)...)
	default:
		result.Errors = append(result.Errors, checkFrozen(in)...)
	}
	result.Errors = append(result.Errors, checkRedFirst(in.Labels, in.Commits)...)
	return result
}

func isPromotion(in Input) bool {
	return in.BaseRef == "main" && in.HeadRef == "development"
}

func checkRedFirst(labels []string, commits []Commit) []error {
	if !hasLabel(labels, microLabel) || hasLabel(labels, redFirstExemptLabel) {
		return nil
	}
	if len(commits) < 2 {
		return []error{fmt.Errorf("%s PR needs at least two commits: the first commit must be tests-only", microLabel)}
	}
	first := commits[0]
	if len(first.Files) == 0 {
		return []error{fmt.Errorf("%s first commit %s is empty; commit the tests first", microLabel, shortSHA(first.SHA))}
	}
	var nonTest []string
	for _, file := range first.Files {
		if !isRedFirstFile(file) {
			nonTest = append(nonTest, file)
		}
	}
	if len(nonTest) > 0 {
		sort.Strings(nonTest)
		return []error{fmt.Errorf("%s first commit %s touches non-test files: %s",
			microLabel, shortSHA(first.SHA), strings.Join(nonTest, ", "))}
	}
	return nil
}

func isRedFirstFile(file string) bool {
	file = strings.TrimPrefix(filepath.ToSlash(file), "./")
	if strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, ".golden") {
		return true
	}
	for _, root := range []string{"testdata", "test", "fixtures"} {
		if file == root || strings.HasPrefix(file, root+"/") || strings.Contains(file, "/"+root+"/") {
			return true
		}
	}
	return false
}

func shortSHA(sha string) string {
	if sha == "" {
		return "?"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func checkTraceability(ctx context.Context, in Input, source Source) ([]error, []string) {
	refs := references(in.Body)
	if len(refs) == 0 {
		if keywordRE.MatchString(in.Body) {
			return []error{fmt.Errorf("malformed Closes/Refs reference: use Closes #N or Refs #N")}, nil
		}
		return []error{fmt.Errorf("body has no Closes #N or Refs #N reference to an open %s or %s issue", alphaLabel, postAlphaLabel)}, nil
	}
	if in.Fork {
		return nil, []string{"fork PR: issue lookup skipped; freeze check still runs"}
	}
	if source == nil {
		return []error{fmt.Errorf("lookup issue: no GitHub source configured")}, nil
	}

	var errs []error
	valid := false
	for _, number := range refs {
		issue, err := source.Issue(ctx, number)
		if err != nil {
			errs = append(errs, fmt.Errorf("lookup issue #%d: %w", number, err))
			continue
		}
		if !hasLabel(issue.Labels, alphaLabel) && !hasLabel(issue.Labels, postAlphaLabel) {
			errs = append(errs, fmt.Errorf("issue #%d is not labelled %s or %s", number, alphaLabel, postAlphaLabel))
			continue
		}
		if !strings.EqualFold(issue.State, "open") {
			errs = append(errs, fmt.Errorf("issue #%d is %s; reference an open %s or %s issue", number, strings.ToLower(issue.State), alphaLabel, postAlphaLabel))
			continue
		}
		valid = true
	}
	if valid {
		return nil, nil
	}
	return errs, nil
}

func checkFrozen(in Input) []error {
	var frozen []string
	for _, file := range in.Files {
		file = strings.TrimPrefix(filepath.ToSlash(file), "./")
		for _, root := range FrozenPaths {
			if file == root || strings.HasPrefix(file, root+"/") {
				frozen = append(frozen, file)
				break
			}
		}
	}
	if len(frozen) == 0 {
		return nil
	}
	sort.Strings(frozen)
	paths := strings.Join(frozen, ", ")
	if !hasLabel(in.Labels, frozenLabel) {
		return []error{fmt.Errorf("frozen path %s requires the %s label and a one-line %s reason", paths, frozenLabel, frozenReasonKey)}
	}
	if !frozenReasonRE.MatchString(in.Body) {
		return []error{fmt.Errorf("%s label requires a one-line %s <reason> in the PR body", frozenLabel, frozenReasonKey)}
	}
	return nil
}

func references(body string) []int {
	matches := referenceRE.FindAllStringSubmatch(body, -1)
	seen := map[int]bool{}
	refs := make([]int, 0, len(matches))
	for _, match := range matches {
		number, err := strconv.Atoi(match[1])
		if err != nil || number <= 0 || seen[number] {
			continue
		}
		seen[number] = true
		refs = append(refs, number)
	}
	return refs
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}
