// Package issueops keeps the alpha-exit label model and issue backlog in sync
// with the repository. The plan document is the human source of truth; the
// generated manifest is what the sync and lint commands consume.
package issueops

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Label is a GitHub label managed by this tool.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// Issue is one AX micro-ticket or spike in the alpha-exit backlog.
type Issue struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Phase     string   `json:"phase"`
	EC        string   `json:"ec"`
	Type      string   `json:"type"`
	Owner     string   `json:"owner,omitempty"`
	BlockedBy []string `json:"blocked_by"`
	Body      string   `json:"body"`
}

// ExistingIssue is a one-time label migration for a pre-existing issue.
type ExistingIssue struct {
	Number int      `json:"number"`
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

// Manifest is the generated issue backlog.
type Manifest struct {
	GeneratedFrom string          `json:"generated_from"`
	Labels        []Label         `json:"labels"`
	Issues        []Issue         `json:"issues"`
	Existing      []ExistingIssue `json:"existing,omitempty"`
}

// Marker returns the hidden body marker that identifies a synced issue.
func Marker(id string) string {
	return "<!-- skopos-issue: " + id + " -->"
}

var markerRE = regexp.MustCompile(`<!--\s*skopos-issue:\s*(AX-\d+)\s*-->`)

// IssueIDFromBody extracts the AX id from a synced issue body.
func IssueIDFromBody(body string) string {
	if m := markerRE.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

var titleIDRE = regexp.MustCompile(`^(AX-\d+):`)

// IssueIDFromTitle extracts the AX id from an issue title.
func IssueIDFromTitle(title string) string {
	if m := titleIDRE.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return ""
}

// LabelsFor returns the labels a manifest issue must carry.
func LabelsFor(issue Issue) []string {
	labels := []string{issue.Phase, issue.EC, issue.Type}
	if issue.Owner != "" {
		labels = append(labels, issue.Owner)
	}
	return labels
}

// ManagedLabel reports whether this tool owns the label and may remove it
// during reconciliation.
func ManagedLabel(name string) bool {
	if name == "frozen" {
		return true
	}
	for _, prefix := range []string{"phase:", "ec:", "type:", "owner:", "backlog:"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Validate checks the manifest rules that do not need the live repository.
func (m *Manifest) Validate() []error {
	var errs []error
	if len(m.Issues) == 0 {
		errs = append(errs, fmt.Errorf("manifest has no issues"))
	}
	seen := map[string]bool{}
	for _, issue := range m.Issues {
		if issue.ID == "" {
			errs = append(errs, fmt.Errorf("issue with empty id"))
			continue
		}
		if seen[issue.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate issue id", issue.ID))
		}
		seen[issue.ID] = true
		if !strings.HasPrefix(issue.Title, issue.ID+":") {
			errs = append(errs, fmt.Errorf("%s: title %q does not start with %q", issue.ID, issue.Title, issue.ID+":"))
		}
		if issue.Phase != "phase:alpha-exit" && issue.Phase != "phase:beta" && issue.Phase != "phase:prod" {
			errs = append(errs, fmt.Errorf("%s: invalid phase label %q", issue.ID, issue.Phase))
		}
		if !regexp.MustCompile(`^ec:[1-7]$`).MatchString(issue.EC) {
			errs = append(errs, fmt.Errorf("%s: exactly one ec:1..ec:7 label is required, got %q", issue.ID, issue.EC))
		}
		if issue.Type != "type:micro" && issue.Type != "type:spike" {
			errs = append(errs, fmt.Errorf("%s: invalid type label %q", issue.ID, issue.Type))
		}
		if !strings.Contains(issue.Body, Marker(issue.ID)) {
			errs = append(errs, fmt.Errorf("%s: body is missing the %s marker", issue.ID, Marker(issue.ID)))
		}
		if !strings.Contains(issue.Body, blockedSection) {
			errs = append(errs, fmt.Errorf("%s: body is missing a Blocked by section", issue.ID))
		}
		if issue.Owner != "" && issue.Owner != "owner:human" {
			errs = append(errs, fmt.Errorf("%s: invalid owner label %q", issue.ID, issue.Owner))
		}
		for _, dep := range issue.BlockedBy {
			if dep == issue.ID {
				errs = append(errs, fmt.Errorf("%s: issue blocks itself", issue.ID))
			}
		}
	}

	byID := make(map[string]Issue, len(m.Issues))
	for _, issue := range m.Issues {
		byID[issue.ID] = issue
	}
	for _, issue := range m.Issues {
		for _, dep := range issue.BlockedBy {
			if _, ok := byID[dep]; !ok {
				errs = append(errs, fmt.Errorf("%s: blocked by unknown issue %s", issue.ID, dep))
			}
		}
	}
	errs = append(errs, checkCycles(byID)...)

	labels := map[string]bool{}
	for _, label := range m.Labels {
		if label.Name == "" {
			errs = append(errs, fmt.Errorf("label with empty name"))
			continue
		}
		if labels[label.Name] {
			errs = append(errs, fmt.Errorf("duplicate label %s", label.Name))
		}
		labels[label.Name] = true
		if !regexp.MustCompile(`^[0-9a-fA-F]{6}$`).MatchString(label.Color) {
			errs = append(errs, fmt.Errorf("label %s: color %q is not a six-digit hex value", label.Name, label.Color))
		}
	}
	for _, issue := range m.Issues {
		for _, label := range LabelsFor(issue) {
			if !labels[label] {
				errs = append(errs, fmt.Errorf("%s: label %s is not defined in the manifest", issue.ID, label))
			}
		}
	}

	numbers := map[int]bool{}
	for _, existing := range m.Existing {
		if existing.Number <= 0 {
			errs = append(errs, fmt.Errorf("existing issue number %d is invalid", existing.Number))
		}
		if numbers[existing.Number] {
			errs = append(errs, fmt.Errorf("existing issue #%d is listed twice", existing.Number))
		}
		numbers[existing.Number] = true
		for _, label := range existing.Add {
			if !labels[label] {
				errs = append(errs, fmt.Errorf("existing issue #%d: added label %s is not defined in the manifest", existing.Number, label))
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

func checkCycles(byID map[string]Issue) []error {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := map[string]int{}
	var errs []error
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		switch state[id] {
		case done:
			return
		case visiting:
			errs = append(errs, fmt.Errorf("blocked-by cycle: %s", strings.Join(append(path, id), " -> ")))
			return
		}
		state[id] = visiting
		issue, ok := byID[id]
		if !ok {
			state[id] = done
			return
		}
		for _, dep := range issue.BlockedBy {
			visit(dep, append(path, id))
		}
		state[id] = done
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		visit(id, nil)
	}
	return errs
}

// AlphaLabels is the complete label set created by the bootstrap sync.
func AlphaLabels() []Label {
	return []Label{
		{Name: "phase:alpha-exit", Color: "0e8a16", Description: "Required for the alpha exit gate"},
		{Name: "phase:beta", Color: "1d76db", Description: "Beta milestone work, starts after alpha exit"},
		{Name: "phase:prod", Color: "5319e7", Description: "Production readiness work, after beta"},
		{Name: "ec:1", Color: "fbca04", Description: "Exit criterion 1: Grafana plug-and-play"},
		{Name: "ec:2", Color: "fbca04", Description: "Exit criterion 2: correct or explicit error"},
		{Name: "ec:3", Color: "fbca04", Description: "Exit criterion 3: survives concurrency"},
		{Name: "ec:4", Color: "fbca04", Description: "Exit criterion 4: observable"},
		{Name: "ec:5", Color: "fbca04", Description: "Exit criterion 5: releasable and verifiable"},
		{Name: "ec:6", Color: "fbca04", Description: "Exit criterion 6: strict gates are live"},
		{Name: "ec:7", Color: "fbca04", Description: "Exit criterion 7: scope discipline"},
		{Name: "type:micro", Color: "c2e0c6", Description: "One observable behavior, one PR"},
		{Name: "type:spike", Color: "fef2c0", Description: "Time-boxed investigation with a written deliverable"},
		{Name: "frozen", Color: "b60205", Description: "Touches a path frozen until alpha exit"},
		{Name: "backlog:post-alpha", Color: "ededed", Description: "Not started before AX-61"},
		{Name: "owner:human", Color: "d93f0b", Description: "Requires a maintainer or external action"},
	}
}

// LegacyRelabels is the one-time migration of the pre-alpha tickets to the
// label model. It stays in the manifest until every listed issue is closed.
func LegacyRelabels() []ExistingIssue {
	return []ExistingIssue{
		{Number: 24, Add: []string{"phase:beta"}, Remove: []string{"ready-for-agent"}},
		{Number: 25, Add: []string{"phase:alpha-exit", "ec:3"}, Remove: []string{"ready-for-agent"}},
		{Number: 26, Add: []string{"phase:alpha-exit", "ec:3"}, Remove: []string{"ready-for-agent"}},
		{Number: 27, Add: []string{"phase:beta", "backlog:post-alpha"}, Remove: []string{"ready-for-agent"}},
		{Number: 28, Add: []string{"phase:alpha-exit", "ec:3"}, Remove: []string{"ready-for-agent"}},
		{Number: 29, Add: []string{"phase:alpha-exit", "ec:3"}, Remove: []string{"ready-for-agent"}},
		{Number: 30, Add: []string{"phase:alpha-exit", "ec:2"}, Remove: []string{"ready-for-agent"}},
		{Number: 31, Add: []string{"phase:alpha-exit", "ec:2"}, Remove: []string{"ready-for-agent"}},
		{Number: 32, Add: []string{"phase:alpha-exit", "ec:2"}, Remove: []string{"ready-for-agent"}},
		{Number: 33, Add: []string{"phase:alpha-exit", "ec:2"}, Remove: []string{"ready-for-agent"}},
		{Number: 34, Add: []string{"phase:alpha-exit", "ec:2"}, Remove: []string{"ready-for-agent"}},
		{Number: 35, Add: []string{"phase:alpha-exit", "ec:5"}, Remove: []string{"ready-for-agent"}},
		{Number: 36, Add: []string{"phase:alpha-exit", "ec:5"}, Remove: []string{"ready-for-agent"}},
		{Number: 37, Add: []string{"phase:alpha-exit", "ec:7"}, Remove: []string{"ready-for-agent"}},
		{Number: 38, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 39, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 40, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 41, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 42, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 43, Add: []string{"phase:beta", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 44, Add: []string{"phase:prod", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 45, Add: []string{"phase:prod", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 46, Add: []string{"phase:prod", "owner:human"}, Remove: []string{"ready-for-agent"}},
		{Number: 47, Add: []string{"phase:prod", "owner:human"}, Remove: []string{"ready-for-agent"}},
	}
}
