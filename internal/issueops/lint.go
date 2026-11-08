package issueops

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var ecLabelRE = regexp.MustCompile(`^ec:[1-7]$`)

// LintLive checks the open alpha-exit issues against the label and template
// rules. It is read-only.
func LintLive(issues []RemoteIssue) []error {
	var errs []error
	byID := map[string]RemoteIssue{}
	for _, issue := range issues {
		id := IssueIDFromBody(issue.Body)
		if id == "" {
			id = IssueIDFromTitle(issue.Title)
		}
		if id == "" {
			continue
		}
		if other, ok := byID[id]; ok {
			errs = append(errs, fmt.Errorf("%s is present on both #%d and #%d", id, other.Number, issue.Number))
			continue
		}
		byID[id] = issue
	}

	graph := map[string]Issue{}
	for _, issue := range issues {
		if issue.State != "open" || !hasLabel(issue.Labels, "phase:alpha-exit") {
			continue
		}
		ecs := labelsWithPrefix(issue.Labels, "ec:")
		if len(ecs) != 1 {
			errs = append(errs, fmt.Errorf("#%d: exactly one ec:1..ec:7 label is required, got %v", issue.Number, ecs))
		}
		typed := hasLabel(issue.Labels, "type:micro") || hasLabel(issue.Labels, "type:spike")
		if !typed {
			continue
		}
		id := IssueIDFromBody(issue.Body)
		if id == "" {
			id = IssueIDFromTitle(issue.Title)
		}
		if id == "" {
			errs = append(errs, fmt.Errorf("#%d: typed alpha-exit issue has no AX id", issue.Number))
			continue
		}
		if IssueIDFromBody(issue.Body) == "" {
			errs = append(errs, fmt.Errorf("#%d (%s): body is missing the skopos-issue marker", issue.Number, id))
		}
		if !strings.Contains(issue.Body, "Blocked by:") {
			errs = append(errs, fmt.Errorf("#%d (%s): body is missing a Blocked by section", issue.Number, id))
			continue
		}
		refs := blockedRefs(issue.Body)
		graph[id] = Issue{ID: id, BlockedBy: refs}
		for _, ref := range refs {
			if ref == id {
				errs = append(errs, fmt.Errorf("#%d (%s): issue blocks itself", issue.Number, id))
				continue
			}
			if _, ok := byID[ref]; !ok {
				errs = append(errs, fmt.Errorf("#%d (%s): blocked by unknown issue %s", issue.Number, id, ref))
			}
		}
	}
	for _, err := range checkCycles(graph) {
		errs = append(errs, err)
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func labelsWithPrefix(labels []string, prefix string) []string {
	var out []string
	for _, label := range labels {
		if strings.HasPrefix(label, prefix) {
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

var blockedLineRE = regexp.MustCompile(`(?m)^Blocked by:\s*(.*)$`)

func blockedRefs(body string) []string {
	m := blockedLineRE.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	return blockedIssueRE.FindAllString(m[1], -1)
}
