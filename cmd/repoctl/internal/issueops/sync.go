package issueops

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// RemoteIssue is the subset of a GitHub issue this tool needs.
type RemoteIssue struct {
	ID     int64
	Number int
	Title  string
	State  string
	Body   string
	Labels []string
}

// GitHub is the small surface of the GitHub API used by sync and lint.
type GitHub interface {
	ListLabels(ctx context.Context) ([]Label, error)
	UpsertLabel(ctx context.Context, label Label) error
	ListIssues(ctx context.Context) ([]RemoteIssue, error)
	CreateIssue(ctx context.Context, issue Issue, labels []string) (int, error)
	UpdateIssue(ctx context.Context, number int, issue Issue) error
	AddLabels(ctx context.Context, number int, labels []string) error
	RemoveLabels(ctx context.Context, number int, labels []string) error
	ListBlockedBy(ctx context.Context, number int) ([]RemoteIssue, error)
	AddBlockedBy(ctx context.Context, number int, blockerID int64) error
	RemoveBlockedBy(ctx context.Context, number int, blockerID int64) error
}

type actionKind int

const (
	actionUpsertLabel actionKind = iota
	actionCreateIssue
	actionUpdateIssue
	actionAddLabels
	actionRemoveLabels
	actionAddBlockedBy
	actionRemoveBlockedBy
)

type action struct {
	kind    actionKind
	label   Label
	issue   Issue
	num     int
	names   []string
	blocker RemoteIssue
}

func (a action) String() string {
	switch a.kind {
	case actionUpsertLabel:
		return "label " + a.label.Name
	case actionCreateIssue:
		return fmt.Sprintf("create issue %s", a.issue.ID)
	case actionUpdateIssue:
		return fmt.Sprintf("update issue #%d body/title (%s)", a.num, a.issue.ID)
	case actionAddLabels:
		return fmt.Sprintf("issue #%d add labels %s", a.num, strings.Join(a.names, ","))
	case actionRemoveLabels:
		return fmt.Sprintf("issue #%d remove labels %s", a.num, strings.Join(a.names, ","))
	case actionAddBlockedBy:
		return fmt.Sprintf("issue #%d blocked by #%d", a.num, a.blocker.Number)
	case actionRemoveBlockedBy:
		return fmt.Sprintf("issue #%d no longer blocked by #%d", a.num, a.blocker.Number)
	default:
		return "unknown action"
	}
}

// SyncPlan computes the label, issue, and blocker mutations needed to reach the
// manifest. blockers maps each open managed issue number to its current blockers.
func SyncPlan(m *Manifest, remoteLabels []Label, remoteIssues []RemoteIssue, blockers map[int][]RemoteIssue) ([]action, error) {
	if errs := m.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid manifest: %v", errs)
	}
	var actions []action

	localLabels := make(map[string]Label, len(remoteLabels))
	for _, label := range remoteLabels {
		localLabels[label.Name] = label
	}
	for _, want := range m.Labels {
		have, ok := localLabels[want.Name]
		if !ok || !sameLabel(have, want) {
			actions = append(actions, action{kind: actionUpsertLabel, label: want})
		}
	}

	byID := map[string]RemoteIssue{}
	byNumber := map[int]RemoteIssue{}
	for _, issue := range remoteIssues {
		byNumber[issue.Number] = issue
		id := managedID(issue)
		if id == "" {
			continue
		}
		if other, ok := byID[id]; ok {
			return nil, fmt.Errorf("%s is present on both #%d and #%d", id, other.Number, issue.Number)
		}
		byID[id] = issue
	}

	for _, want := range m.Issues {
		wantLabels := LabelsFor(want)
		have, ok := byID[want.ID]
		if !ok {
			actions = append(actions, action{kind: actionCreateIssue, issue: want})
			continue
		}
		if have.State == "open" && (have.Title != want.Title || !sameBody(have.Body, want.Body)) {
			actions = append(actions, action{kind: actionUpdateIssue, issue: want, num: have.Number})
		}
		add, remove := labelDiff(have.Labels, wantLabels)
		if len(add) > 0 {
			actions = append(actions, action{kind: actionAddLabels, num: have.Number, names: add})
		}
		if len(remove) > 0 {
			actions = append(actions, action{kind: actionRemoveLabels, num: have.Number, names: remove})
		}
		if have.State == "open" {
			actions = append(actions, blockerActions(want, have.Number, byID, blockers[have.Number])...)
		}
	}

	for _, existing := range m.Existing {
		have, ok := byNumber[existing.Number]
		if !ok {
			return nil, fmt.Errorf("existing issue #%d is not visible to the token", existing.Number)
		}
		current := labelSet(have.Labels)
		var add []string
		for _, name := range existing.Add {
			if !current[name] {
				add = append(add, name)
			}
		}
		var remove []string
		for _, name := range existing.Remove {
			if current[name] {
				remove = append(remove, name)
			}
		}
		if len(add) > 0 {
			actions = append(actions, action{kind: actionAddLabels, num: have.Number, names: add})
		}
		if len(remove) > 0 {
			actions = append(actions, action{kind: actionRemoveLabels, num: have.Number, names: remove})
		}
	}
	return actions, nil
}

// blockerActions adds the native blocked-by edges a ticket needs and removes
// edges to managed tickets that the plan no longer names. A blocker with no
// issue yet is skipped, so the next sync adds its edge.
func blockerActions(want Issue, num int, byID map[string]RemoteIssue, current []RemoteIssue) []action {
	var actions []action
	desired := map[int64]bool{}
	for _, dep := range want.BlockedBy {
		blocker, ok := byID[dep]
		if !ok {
			continue
		}
		desired[blocker.ID] = true
		if !hasBlocker(current, blocker.ID) {
			actions = append(actions, action{kind: actionAddBlockedBy, num: num, blocker: blocker})
		}
	}
	for _, blocker := range current {
		if desired[blocker.ID] || managedID(blocker) == "" {
			continue
		}
		actions = append(actions, action{kind: actionRemoveBlockedBy, num: num, blocker: blocker})
	}
	return actions
}

func hasBlocker(blockers []RemoteIssue, id int64) bool {
	for _, blocker := range blockers {
		if blocker.ID == id {
			return true
		}
	}
	return false
}

// managedID returns the AX id that marks an issue as managed by this tool.
func managedID(issue RemoteIssue) string {
	if id := IssueIDFromBody(issue.Body); id != "" {
		return id
	}
	return IssueIDFromTitle(issue.Title)
}

var checkedBoxRE = regexp.MustCompile(`(?m)^([ \t]*- )\[[xX]\]`)

// sameBody compares two bodies with checkbox ticks ignored, so a tick made on
// GitHub is not a change that the sync has to undo.
func sameBody(have, want string) bool {
	return checkedBoxRE.ReplaceAllString(have, "${1}[ ]") == checkedBoxRE.ReplaceAllString(want, "${1}[ ]")
}

// Sync applies (or, in dry-run mode, reports) the sync plan.
func Sync(ctx context.Context, gh GitHub, m *Manifest, apply bool, out io.Writer) error {
	labels, err := gh.ListLabels(ctx)
	if err != nil {
		return err
	}
	issues, err := gh.ListIssues(ctx)
	if err != nil {
		return err
	}
	blockers := map[int][]RemoteIssue{}
	for _, issue := range issues {
		if issue.State != "open" || managedID(issue) == "" {
			continue
		}
		current, err := gh.ListBlockedBy(ctx, issue.Number)
		if err != nil {
			return err
		}
		blockers[issue.Number] = current
	}
	actions, err := SyncPlan(m, labels, issues, blockers)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		fmt.Fprintln(out, "issues-sync: no changes")
		return nil
	}
	for _, act := range actions {
		if apply {
			if err := runAction(ctx, gh, act); err != nil {
				return fmt.Errorf("%s: %w", act, err)
			}
			fmt.Fprintf(out, "applied %s\n", act)
		} else {
			fmt.Fprintf(out, "would %s\n", act)
		}
	}
	if !apply {
		fmt.Fprintf(out, "issues-sync: %d change(s) not applied (pass --apply)\n", len(actions))
	}
	return nil
}

func runAction(ctx context.Context, gh GitHub, act action) error {
	switch act.kind {
	case actionUpsertLabel:
		return gh.UpsertLabel(ctx, act.label)
	case actionCreateIssue:
		_, err := gh.CreateIssue(ctx, act.issue, LabelsFor(act.issue))
		return err
	case actionUpdateIssue:
		return gh.UpdateIssue(ctx, act.num, act.issue)
	case actionAddLabels:
		return gh.AddLabels(ctx, act.num, act.names)
	case actionRemoveLabels:
		return gh.RemoveLabels(ctx, act.num, act.names)
	case actionAddBlockedBy:
		return gh.AddBlockedBy(ctx, act.num, act.blocker.ID)
	case actionRemoveBlockedBy:
		return gh.RemoveBlockedBy(ctx, act.num, act.blocker.ID)
	default:
		return fmt.Errorf("unknown action")
	}
}

func sameLabel(a, b Label) bool {
	return strings.EqualFold(strings.TrimPrefix(a.Color, "#"), strings.TrimPrefix(b.Color, "#")) &&
		a.Description == b.Description
}

func labelSet(labels []string) map[string]bool {
	set := make(map[string]bool, len(labels))
	for _, label := range labels {
		set[label] = true
	}
	return set
}

func labelDiff(current, desired []string) (add, remove []string) {
	have := labelSet(current)
	want := labelSet(desired)
	for _, name := range desired {
		if !have[name] {
			add = append(add, name)
		}
	}
	for _, name := range current {
		if ManagedLabel(name) && !want[name] {
			remove = append(remove, name)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	return add, remove
}
