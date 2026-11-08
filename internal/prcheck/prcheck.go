// Package prcheck enforces alpha-exit traceability and frozen-path rules on
// pull requests.
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

// Input is one pull request.
type Input struct {
	Title  string
	Body   string
	Labels []string
	Files  []string
	Fork   bool
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
	"cmd/skopos-mcp",
	"internal/checkpoint",
	"cmd/skopos-checkpoint",
	"deploy/systemd",
}

const (
	alphaLabel      = "phase:alpha-exit"
	frozenLabel     = "frozen-exception"
	frozenReasonKey = "Frozen-exception:"
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
	if !exemptTypeRE.MatchString(strings.TrimSpace(in.Title)) {
		errs, notices := checkTraceability(ctx, in, source)
		result.Errors = append(result.Errors, errs...)
		result.Notices = append(result.Notices, notices...)
	}
	result.Errors = append(result.Errors, checkFrozen(in)...)
	return result
}

func checkTraceability(ctx context.Context, in Input, source Source) ([]error, []string) {
	refs := references(in.Body)
	if len(refs) == 0 {
		if keywordRE.MatchString(in.Body) {
			return []error{fmt.Errorf("malformed Closes/Refs reference: use Closes #N or Refs #N")}, nil
		}
		return []error{fmt.Errorf("body has no Closes #N or Refs #N reference to an open %s issue", alphaLabel)}, nil
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
		if !hasLabel(issue.Labels, alphaLabel) {
			errs = append(errs, fmt.Errorf("issue #%d is not labelled %s", number, alphaLabel))
			continue
		}
		if !strings.EqualFold(issue.State, "open") {
			errs = append(errs, fmt.Errorf("issue #%d is %s; reference an open %s issue", number, strings.ToLower(issue.State), alphaLabel))
			continue
		}
		valid = true
	}
	if valid {
		return nil, nil
	}
	if len(errs) == 0 {
		errs = append(errs, fmt.Errorf("body has no Closes #N or Refs #N reference to an open %s issue", alphaLabel))
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
