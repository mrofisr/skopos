package issueops

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
)

const planFixture = `# Plan

### Guardrails (EC6, EC7)

**AX-01 Coverage ratchet.** Blocked by: none.

- Red tests: ` + "`TestCoverageGate`" + `.
- Done when: the gate runs.

**AX-04 Traceability.** Blocked by: none (bootstrap is
exempt).

- Edge and negative: an unreachable API fails closed.

**AX-05 Issue lint.** Blocked by: AX-04.

- Done when: lint runs.

## Next
`

func TestParsePlan(t *testing.T) {
	m, err := ParsePlan(planFixture)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(m.Issues) != 3 {
		t.Fatalf("issues = %d, want 3", len(m.Issues))
	}
	byID := map[string]Issue{}
	for _, issue := range m.Issues {
		byID[issue.ID] = issue
	}
	if got := byID["AX-01"].Title; got != "AX-01: Coverage ratchet" {
		t.Errorf("AX-01 title = %q", got)
	}
	if got := byID["AX-01"].EC; got != "ec:6" {
		t.Errorf("AX-01 ec = %q", got)
	}
	if got := byID["AX-04"].Body; !strings.Contains(got, "- None (bootstrap is exempt).") {
		t.Errorf("AX-04 wrapped Blocked by was not rendered:\n%s", got)
	}
	if got := byID["AX-05"].BlockedBy; len(got) != 1 || got[0] != "AX-04" {
		t.Errorf("AX-05 blocked_by = %v", got)
	}
	if got := byID["AX-05"].EC; got != "ec:7" {
		t.Errorf("AX-05 ec = %q, want ec:7", got)
	}
	if !strings.Contains(byID["AX-01"].Body, Marker("AX-01")) {
		t.Errorf("body has no marker:\n%s", byID["AX-01"].Body)
	}
}

func TestParsePlanMissingBlockedBy(t *testing.T) {
	_, err := ParsePlan("**AX-01 No blocker line.**\n\n- Done when: never.\n")
	if err == nil || !strings.Contains(err.Error(), "Blocked by") {
		t.Fatalf("err = %v, want missing Blocked by error", err)
	}
}

func TestParseRealPlan(t *testing.T) {
	m, err := ParsePlanFile("../../../../docs/alpha-exit-plan.md")
	if err != nil {
		t.Fatalf("ParsePlanFile: %v", err)
	}
	if len(m.Issues) != 46 {
		t.Fatalf("issues = %d, want 46", len(m.Issues))
	}
	if errs := m.Validate(); len(errs) > 0 {
		t.Fatalf("plan validation: %v", errs)
	}
	byID := map[string]Issue{}
	for _, issue := range m.Issues {
		byID[issue.ID] = issue
	}
	for id, ec := range map[string]string{
		"AX-01": "ec:6",
		"AX-05": "ec:7",
		"AX-10": "ec:4",
		"AX-20": "ec:3",
		"AX-40": "ec:1",
		"AX-45": "ec:2",
		"AX-50": "ec:5",
		"AX-60": "ec:7",
		"AX-61": "ec:7",
		"AX-62": "ec:7",
	} {
		if got := byID[id].EC; got != ec {
			t.Errorf("%s ec = %q, want %q", id, got, ec)
		}
	}
	if got := byID["AX-39"].Type; got != "type:spike" {
		t.Errorf("AX-39 type = %q, want spike", got)
	}
	if got := byID["AX-55"].Owner; got != "owner:human" {
		t.Errorf("AX-55 owner = %q, want owner:human", got)
	}
	if got := byID["AX-62"].BlockedBy; len(got) != 0 {
		t.Errorf("AX-62 blocked_by = %v, want none", got)
	}
}

func TestManifestValidate(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			Labels: AlphaLabels(),
			Issues: []Issue{{
				ID:        "AX-01",
				Title:     "AX-01: Test",
				Phase:     "phase:alpha-exit",
				EC:        "ec:6",
				Type:      "type:micro",
				BlockedBy: []string{},
				Body:      Marker("AX-01") + "\n\n## Blocked by\n\n- None (can start immediately).\n",
			}},
		}
	}
	if errs := base().Validate(); len(errs) > 0 {
		t.Fatalf("valid manifest: %v", errs)
	}

	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"empty ec", func(m *Manifest) { m.Issues[0].EC = "" }, "ec:1..ec:7"},
		{"two ec labels", func(m *Manifest) { m.Issues[0].EC = "ec:6,ec:7" }, "ec:1..ec:7"},
		{"missing blocked by", func(m *Manifest) { m.Issues[0].Body = Marker("AX-01") }, "Blocked by"},
		{"self cycle", func(m *Manifest) { m.Issues[0].BlockedBy = []string{"AX-01"} }, "blocks itself"},
		{"unknown blocker", func(m *Manifest) { m.Issues[0].BlockedBy = []string{"AX-99"} }, "unknown issue"},
		{"duplicate id", func(m *Manifest) { m.Issues = append(m.Issues, m.Issues[0]) }, "duplicate issue id"},
		{"unknown owner", func(m *Manifest) { m.Issues[0].Owner = "owner:robot" }, "owner:robot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			errs := m.Validate()
			if len(errs) == 0 {
				t.Fatalf("Validate accepted an invalid manifest")
			}
			if !strings.Contains(strings.Join(errorStrings(errs), " "), tc.want) {
				t.Fatalf("errors = %v, want %q", errs, tc.want)
			}
		})
	}
}

func TestManifestValidateCycle(t *testing.T) {
	m := &Manifest{
		Labels: AlphaLabels(),
		Issues: []Issue{
			{ID: "AX-01", Title: "AX-01: A", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{"AX-02"}, Body: Marker("AX-01") + "\n\n## Blocked by\n\n- AX-02\n"},
			{ID: "AX-02", Title: "AX-02: B", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{"AX-01"}, Body: Marker("AX-02") + "\n\n## Blocked by\n\n- AX-01\n"},
		},
	}
	errs := m.Validate()
	if len(errs) == 0 || !strings.Contains(strings.Join(errorStrings(errs), " "), "cycle") {
		t.Fatalf("errors = %v, want a cycle error", errs)
	}
}

func TestManifestValidateUnknownBlockerIsNotACycle(t *testing.T) {
	m := &Manifest{
		Labels: AlphaLabels(),
		Issues: []Issue{
			{ID: "AX-01", Title: "AX-01: A", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{"AX-99"}, Body: Marker("AX-01") + "\n\n## Blocked by\n\n- AX-99\n"},
			{ID: "AX-02", Title: "AX-02: B", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{"AX-99"}, Body: Marker("AX-02") + "\n\n## Blocked by\n\n- AX-99\n"},
		},
	}
	errs := m.Validate()
	joined := strings.Join(errorStrings(errs), " ")
	if strings.Contains(joined, "cycle") {
		t.Fatalf("unknown blockers were reported as a cycle: %v", errs)
	}
	if !strings.Contains(joined, "unknown issue") {
		t.Fatalf("errors = %v, want unknown issue errors", errs)
	}
}

func TestLintLive(t *testing.T) {
	valid := RemoteIssue{
		Number: 1,
		Title:  "AX-01: Test",
		State:  "open",
		Body:   Marker("AX-01") + "\n\n## Blocked by\n\n- None (can start immediately).\n",
		Labels: []string{"phase:alpha-exit", "ec:6", "type:micro"},
	}
	cases := []struct {
		name  string
		issue RemoteIssue
		want  string
	}{
		{"valid", valid, ""},
		{"no ec", withLabels(valid, "phase:alpha-exit", "type:micro"), "exactly one ec"},
		{"two ec", withLabels(valid, "phase:alpha-exit", "ec:6", "ec:7", "type:micro"), "exactly one ec"},
		{"missing blocked by", withBody(valid, Marker("AX-01")), "Blocked by"},
		{"missing marker", withBody(valid, "## Blocked by\n\n- None (can start immediately).\n"), "marker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := LintLive([]RemoteIssue{tc.issue})
			if tc.want == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if !strings.Contains(strings.Join(errorStrings(errs), " "), tc.want) {
				t.Fatalf("errors = %v, want %q", errs, tc.want)
			}
		})
	}
}

func TestLintLiveReadsOnlyTheBlockedBySection(t *testing.T) {
	labels := []string{"phase:alpha-exit", "ec:6", "type:micro"}
	clean := RemoteIssue{
		Number: 1,
		Title:  "AX-01: A",
		State:  "open",
		Body:   Marker("AX-01") + "\n\nThe text mentions AX-99 but does not block on it.\n\n## Blocked by\n\n- None (can start immediately).\n",
		Labels: labels,
	}
	if errs := LintLive([]RemoteIssue{clean}); len(errs) > 0 {
		t.Fatalf("errors = %v, want none: AX-99 is outside the section", errs)
	}
	unknown := RemoteIssue{
		Number: 2,
		Title:  "AX-02: B",
		State:  "open",
		Body:   Marker("AX-02") + "\n\n## Blocked by\n\n- AX-98\n",
		Labels: labels,
	}
	errs := LintLive([]RemoteIssue{unknown})
	if !strings.Contains(strings.Join(errorStrings(errs), " "), "blocked by unknown issue AX-98") {
		t.Fatalf("errors = %v, want an unknown blocker error", errs)
	}
}

func TestLintLiveCycle(t *testing.T) {
	issues := []RemoteIssue{
		{Number: 1, Title: "AX-01: A", State: "open", Body: Marker("AX-01") + "\n\n## Blocked by\n\n- AX-02\n", Labels: []string{"phase:alpha-exit", "ec:6", "type:micro"}},
		{Number: 2, Title: "AX-02: B", State: "open", Body: Marker("AX-02") + "\n\n## Blocked by\n\n- AX-01\n", Labels: []string{"phase:alpha-exit", "ec:6", "type:micro"}},
	}
	errs := LintLive(issues)
	if !strings.Contains(strings.Join(errorStrings(errs), " "), "cycle") {
		t.Fatalf("errors = %v, want cycle", errs)
	}
}

func TestLintLiveParentOnlyNeedsEC(t *testing.T) {
	issues := []RemoteIssue{{
		Number: 25,
		Title:  "Label discovery pushdown",
		State:  "open",
		Body:   "Parent issue tracked by AX-33.",
		Labels: []string{"phase:alpha-exit", "ec:3"},
	}}
	if errs := LintLive(issues); len(errs) > 0 {
		t.Fatalf("parent issue errors = %v, want none", errs)
	}
}

func TestLintLiveParentWithoutECFails(t *testing.T) {
	issues := []RemoteIssue{{
		Number: 25,
		Title:  "Label discovery pushdown",
		State:  "open",
		Body:   "Parent issue tracked by AX-33.",
		Labels: []string{"phase:alpha-exit"},
	}}
	errs := LintLive(issues)
	if !strings.Contains(strings.Join(errorStrings(errs), " "), "exactly one ec") {
		t.Fatalf("errors = %v, want the ec error", errs)
	}
}

func withLabels(issue RemoteIssue, labels ...string) RemoteIssue {
	issue.Labels = labels
	return issue
}

func withBody(issue RemoteIssue, body string) RemoteIssue {
	issue.Body = body
	return issue
}

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}

func testManifest() *Manifest {
	return &Manifest{
		Labels: AlphaLabels(),
		Issues: []Issue{{
			ID:        "AX-01",
			Title:     "AX-01: Test",
			Phase:     "phase:alpha-exit",
			EC:        "ec:6",
			Type:      "type:micro",
			BlockedBy: []string{},
			Body:      Marker("AX-01") + "\n\n## Blocked by\n\n- None (can start immediately).\n",
		}},
	}
}

func twoTicketManifest() *Manifest {
	return &Manifest{
		Labels: AlphaLabels(),
		Issues: []Issue{
			{ID: "AX-01", Title: "AX-01: first", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{}, Body: Marker("AX-01") + "\n\n## Blocked by\n\n- None (can start immediately).\n"},
			{ID: "AX-02", Title: "AX-02: second", Phase: "phase:alpha-exit", EC: "ec:6", Type: "type:micro", BlockedBy: []string{"AX-01"}, Body: Marker("AX-02") + "\n\n## Blocked by\n\n- AX-01\n"},
		},
	}
}

type fakeGH struct {
	labels   []Label
	issues   []RemoteIssue
	blockers map[int][]RemoteIssue
	next     int
	actions  []string
}

func (f *fakeGH) ListLabels(context.Context) ([]Label, error) { return f.labels, nil }

func (f *fakeGH) UpsertLabel(_ context.Context, label Label) error {
	f.actions = append(f.actions, "upsert-label:"+label.Name)
	for i := range f.labels {
		if f.labels[i].Name == label.Name {
			f.labels[i] = label
			return nil
		}
	}
	f.labels = append(f.labels, label)
	return nil
}

func (f *fakeGH) ListIssues(context.Context) ([]RemoteIssue, error) { return f.issues, nil }

func (f *fakeGH) CreateIssue(_ context.Context, issue Issue, labels []string) (int, error) {
	f.next++
	f.actions = append(f.actions, "create:"+issue.ID)
	f.issues = append(f.issues, RemoteIssue{
		Number: f.next,
		ID:     int64(1000 + f.next),
		Title:  issue.Title,
		State:  "open",
		Body:   issue.Body,
		Labels: labels,
	})
	return f.next, nil
}

func (f *fakeGH) UpdateIssue(_ context.Context, number int, issue Issue) error {
	f.actions = append(f.actions, "update:"+issue.ID)
	for i := range f.issues {
		if f.issues[i].Number == number {
			f.issues[i].Title = issue.Title
			f.issues[i].Body = issue.Body
		}
	}
	return nil
}

func (f *fakeGH) AddLabels(_ context.Context, number int, labels []string) error {
	f.actions = append(f.actions, "add:"+join(labels))
	for i := range f.issues {
		if f.issues[i].Number == number {
			f.issues[i].Labels = append(f.issues[i].Labels, labels...)
		}
	}
	return nil
}

func (f *fakeGH) RemoveLabels(_ context.Context, number int, labels []string) error {
	f.actions = append(f.actions, "remove:"+join(labels))
	for i := range f.issues {
		if f.issues[i].Number == number {
			f.issues[i].Labels = removeAll(f.issues[i].Labels, labels)
		}
	}
	return nil
}

func (f *fakeGH) ListBlockedBy(_ context.Context, number int) ([]RemoteIssue, error) {
	return f.blockers[number], nil
}

func (f *fakeGH) AddBlockedBy(_ context.Context, number int, blockerID int64) error {
	f.actions = append(f.actions, "block:"+strconv.Itoa(number))
	if f.blockers == nil {
		f.blockers = map[int][]RemoteIssue{}
	}
	f.blockers[number] = append(f.blockers[number], f.issueByID(blockerID))
	return nil
}

func (f *fakeGH) RemoveBlockedBy(_ context.Context, number int, blockerID int64) error {
	f.actions = append(f.actions, "unblock:"+strconv.Itoa(number))
	var kept []RemoteIssue
	for _, blocker := range f.blockers[number] {
		if blocker.ID != blockerID {
			kept = append(kept, blocker)
		}
	}
	if f.blockers == nil {
		f.blockers = map[int][]RemoteIssue{}
	}
	f.blockers[number] = kept
	return nil
}

func (f *fakeGH) issueByID(id int64) RemoteIssue {
	for _, issue := range f.issues {
		if issue.ID == id {
			return issue
		}
	}
	return RemoteIssue{ID: id}
}

func join(labels []string) string { return strings.Join(labels, ",") }

func removeAll(have, remove []string) []string {
	drop := map[string]bool{}
	for _, name := range remove {
		drop[name] = true
	}
	out := have[:0]
	for _, name := range have {
		if !drop[name] {
			out = append(out, name)
		}
	}
	return out
}

func TestSyncIdempotent(t *testing.T) {
	gh := &fakeGH{next: 10}
	m := testManifest()
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	first := len(gh.actions)
	if first == 0 {
		t.Fatal("first sync made no changes")
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(gh.actions) != first {
		t.Fatalf("second sync changed %d more actions: %v", len(gh.actions)-first, gh.actions[first:])
	}
}

func TestSyncDryRunDoesNotMutate(t *testing.T) {
	gh := &fakeGH{}
	if err := Sync(context.Background(), gh, testManifest(), false, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(gh.actions) != 0 || len(gh.labels) != 0 || len(gh.issues) != 0 {
		t.Fatalf("dry run mutated the fake: actions=%v labels=%v issues=%v", gh.actions, gh.labels, gh.issues)
	}
}

func TestSyncReconcilesManagedLabels(t *testing.T) {
	m := testManifest()
	gh := &fakeGH{
		labels: m.Labels,
		issues: []RemoteIssue{{
			Number: 7,
			Title:  m.Issues[0].Title,
			State:  "open",
			Body:   m.Issues[0].Body,
			Labels: []string{"phase:alpha-exit", "ec:7", "type:micro", "enhancement"},
		}},
	}
	err := Sync(context.Background(), gh, m, true, io.Discard)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got := labelSet(gh.issues[0].Labels)
	if got["ec:7"] || !got["ec:6"] || !got["enhancement"] {
		t.Fatalf("labels = %v, want ec:6, enhancement, and no ec:7", gh.issues[0].Labels)
	}
}

func TestSyncAppliesLegacyRelabels(t *testing.T) {
	m := testManifest()
	m.Existing = []ExistingIssue{{Number: 42, Add: []string{"owner:human"}, Remove: []string{"ready-for-agent"}}}
	gh := &fakeGH{
		labels: m.Labels,
		issues: []RemoteIssue{{Number: 42, Title: "human work", State: "open", Labels: []string{"ready-for-agent"}}},
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got := labelSet(gh.issues[0].Labels)
	if got["ready-for-agent"] || !got["owner:human"] {
		t.Fatalf("labels = %v, want owner:human and no ready-for-agent", gh.issues[0].Labels)
	}
}

func TestSyncIgnoresCheckboxState(t *testing.T) {
	m := testManifest()
	ticked := strings.Replace(m.Issues[0].Body, "## Blocked by", "## Acceptance criteria\n\n- [ ] Done when: x.\n\n## Blocked by", 1)
	m.Issues[0].Body = ticked
	remote := strings.Replace(ticked, "- [ ]", "- [x]", 1)
	gh := &fakeGH{
		labels: m.Labels,
		issues: []RemoteIssue{{Number: 7, Title: m.Issues[0].Title, State: "open", Body: remote, Labels: LabelsFor(m.Issues[0])}},
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := strings.Join(gh.actions, ","); strings.Contains(got, "update:") {
		t.Fatalf("a ticked box caused a body update: %s", got)
	}
	if !strings.Contains(gh.issues[0].Body, "- [x]") {
		t.Fatalf("the tick was reset: %q", gh.issues[0].Body)
	}
}

func TestSyncSkipsBodiesOfClosedIssues(t *testing.T) {
	m := testManifest()
	legacy := Marker("AX-01") + "\nParent: none\n"
	gh := &fakeGH{
		labels: m.Labels,
		issues: []RemoteIssue{{Number: 7, Title: m.Issues[0].Title, State: "closed", Body: legacy, Labels: LabelsFor(m.Issues[0])}},
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if gh.issues[0].Body != legacy {
		t.Fatalf("closed issue body was rewritten: %q", gh.issues[0].Body)
	}
	if got := strings.Join(gh.actions, ","); strings.Contains(got, "update:") {
		t.Fatalf("closed issue got an update action: %s", got)
	}
}

func TestSyncRewritesBodiesOfOpenIssues(t *testing.T) {
	m := testManifest()
	gh := &fakeGH{
		labels: m.Labels,
		issues: []RemoteIssue{{Number: 7, Title: m.Issues[0].Title, State: "open", Body: Marker("AX-01") + "\nParent: none\n", Labels: LabelsFor(m.Issues[0])}},
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if gh.issues[0].Body != m.Issues[0].Body {
		t.Fatalf("open issue body = %q, want the generated body", gh.issues[0].Body)
	}
}

func TestSyncAddsAndRemovesNativeBlockedBy(t *testing.T) {
	m := twoTicketManifest()
	one := RemoteIssue{Number: 1, ID: 101, Title: "AX-01: first", State: "open", Body: m.Issues[0].Body, Labels: LabelsFor(m.Issues[0])}
	two := RemoteIssue{Number: 2, ID: 102, Title: "AX-02: second", State: "open", Body: m.Issues[1].Body, Labels: LabelsFor(m.Issues[1])}
	stale := RemoteIssue{Number: 3, ID: 103, Title: "AX-09: retired", State: "open", Body: Marker("AX-09") + "\n"}
	human := RemoteIssue{Number: 4, ID: 104, Title: "human ticket", State: "open", Body: "no marker"}
	gh := &fakeGH{
		labels:   m.Labels,
		issues:   []RemoteIssue{one, two, stale, human},
		blockers: map[int][]RemoteIssue{2: {stale, human}},
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got := map[int]bool{}
	for _, blocker := range gh.blockers[2] {
		got[blocker.Number] = true
	}
	if !got[1] || !got[4] || got[3] || len(gh.blockers[2]) != 2 {
		t.Fatalf("blockers of #2 = %v, want #1 and the human ticket #4 only", gh.blockers[2])
	}
}

func TestSyncAddsEdgesToTicketsCreatedInAnEarlierRun(t *testing.T) {
	m := twoTicketManifest()
	one := RemoteIssue{Number: 1, ID: 101, Title: "AX-01: first", State: "open", Body: m.Issues[0].Body, Labels: LabelsFor(m.Issues[0])}
	gh := &fakeGH{labels: m.Labels, next: 10, issues: []RemoteIssue{one}}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if got := gh.blockers[11]; len(got) != 0 {
		t.Fatalf("an edge was added in the run that created its ticket: %v", got)
	}
	if err := Sync(context.Background(), gh, m, true, io.Discard); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := gh.blockers[11]; len(got) != 1 || got[0].ID != 101 {
		t.Fatalf("blockers of #11 after the second sync = %v, want AX-01", got)
	}
}
