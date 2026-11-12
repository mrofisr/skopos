package issueops

import (
	"strings"
	"testing"
)

const sectionFixture = `# Plan

**AX-06 Child of an umbrella (parent #36).** Blocked by: AX-01.

- Today the gateway writes the response too late.
- Red tests: ` + "`TestChild`" + `.
- Edge and negative: a zero value is rejected.
- Done when: the child passes.

## Next
`

const ownerFixture = "**AX-31 Human sign-off (parent #37) (owner:human).** Blocked by: none.\n\n- Done when: the maintainer signs off.\n"

const externalFixture = "**AX-32 External gate.** Blocked by: every EC evidence command.\n\n- Done when: the gate reads the evidence.\n"

func parsedIssues(t *testing.T, plan string) map[string]Issue {
	t.Helper()
	m, err := ParsePlan(plan)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	byID := map[string]Issue{}
	for _, issue := range m.Issues {
		byID[issue.ID] = issue
	}
	return byID
}

func TestRenderBodyUsesToTicketsSections(t *testing.T) {
	body := parsedIssues(t, sectionFixture)["AX-06"].Body
	last := -1
	for _, heading := range []string{"## Parent", "## What to build", "## Acceptance criteria", "## Blocked by"} {
		at := strings.Index(body, heading)
		if at <= last {
			t.Fatalf("heading %q is missing or out of order:\n%s", heading, body)
		}
		last = at
	}
	for _, want := range []string{"#36", "- Today the gateway writes the response too late.", "- AX-01"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, legacy := range []string{"Parent: ", "EC: ", "Type: ", "Plan: ", "Blocked by: "} {
		if strings.Contains(body, legacy) {
			t.Errorf("body keeps the legacy header %q:\n%s", legacy, body)
		}
	}
}

func TestRenderBodyOmitsParentWithoutOne(t *testing.T) {
	body := parsedIssues(t, planFixture)["AX-01"].Body
	if strings.Contains(body, "## Parent") {
		t.Errorf("AX-01 has no parent but its body has a Parent section:\n%s", body)
	}
}

func TestAcceptanceItemsBecomeCheckboxes(t *testing.T) {
	body := parsedIssues(t, planFixture)["AX-01"].Body
	for _, want := range []string{"- [ ] Red tests: `TestCoverageGate`.", "- [ ] Done when: the gate runs."} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "\n- Done when:") {
		t.Errorf("Done when stayed a plain bullet:\n%s", body)
	}
}

func TestWhatToBuildFallsBackToTheTitle(t *testing.T) {
	body := parsedIssues(t, planFixture)["AX-01"].Body
	if !strings.Contains(body, "## What to build\n\n- Coverage ratchet.\n") {
		t.Errorf("a ticket with no description should say what it builds by its title:\n%s", body)
	}
}

func TestOwnerMarkerSetsOwnerLabel(t *testing.T) {
	m, err := ParsePlan(ownerFixture)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	issue := m.Issues[0]
	if issue.Title != "AX-31: Human sign-off" {
		t.Errorf("title = %q, want the owner and parent markers removed", issue.Title)
	}
	if issue.Owner != "owner:human" {
		t.Errorf("owner = %q, want owner:human", issue.Owner)
	}
	if got := LabelsFor(issue); !hasLabel(got, "owner:human") {
		t.Errorf("labels = %v, want owner:human", got)
	}
	if errs := m.Validate(); len(errs) > 0 {
		t.Errorf("Validate: %v", errs)
	}
}

func TestBlockedTextWithoutAXIDsIsRendered(t *testing.T) {
	issue := parsedIssues(t, externalFixture)["AX-32"]
	if len(issue.BlockedBy) != 0 {
		t.Errorf("blocked_by = %v, want none", issue.BlockedBy)
	}
	if !strings.Contains(issue.Body, "## Blocked by\n\n- Every EC evidence command.\n") {
		t.Errorf("non-AX blocker text was not rendered:\n%s", issue.Body)
	}
}

func TestParseRealPlanRendersEveryTicket(t *testing.T) {
	m, err := ParsePlanFile("../../../../docs/alpha-exit-plan.md")
	if err != nil {
		t.Fatalf("ParsePlanFile: %v", err)
	}
	byID := map[string]Issue{}
	for _, issue := range m.Issues {
		byID[issue.ID] = issue
		for _, heading := range []string{"## What to build", "## Acceptance criteria", "## Blocked by"} {
			if !strings.Contains(issue.Body, heading) {
				t.Errorf("%s lacks %s", issue.ID, heading)
			}
		}
		if !strings.Contains(issue.Body, "- [ ] ") {
			t.Errorf("%s has no acceptance checkbox", issue.ID)
		}
		if strings.Contains(issue.Body, "\nParent: ") || strings.Contains(issue.Body, "\nEC: ") {
			t.Errorf("%s keeps the legacy header", issue.ID)
		}
	}
	if !strings.Contains(byID["AX-55"].Body, "## Parent\n\n#36\n") {
		t.Errorf("AX-55 body has no parent section for #36:\n%s", byID["AX-55"].Body)
	}
	if strings.Contains(byID["AX-20"].Body, "## Parent") {
		t.Errorf("AX-20 has no parent but its body has a Parent section")
	}
	if !strings.Contains(byID["AX-62"].Body, "## Parent\n\n#37\n") {
		t.Errorf("AX-62 body has no parent section for #37:\n%s", byID["AX-62"].Body)
	}
}
