package issueops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	planHeadingRE  = regexp.MustCompile(`^\*\*(AX-\d+) (.+)\.\*\*(.*)$`)
	planSectionRE  = regexp.MustCompile(`^#{2,3} `)
	blockedByRE    = regexp.MustCompile(`Blocked by:\s*(.+?)\.(?:\s|$)`)
	parentRE       = regexp.MustCompile(`\(parent #\d+\)`)
	parentNumberRE = regexp.MustCompile(`\(parent #(\d+)\)`)
	cutLineRE      = regexp.MustCompile(`\(cut-line\)`)
	ticketNumberRE = regexp.MustCompile(`^AX-(\d+)$`)
	blockedIssueRE = regexp.MustCompile(`AX-\d+`)
	blockedHeading = "Blocked by:"
)

// ParsePlanFile reads the alpha exit plan and extracts every AX ticket.
func ParsePlanFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePlan(string(data))
}

// ParsePlan extracts every AX ticket from the plan markdown.
func ParsePlan(data string) (*Manifest, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	manifest := &Manifest{
		GeneratedFrom: "docs/alpha-exit-plan.md",
		Labels:        AlphaLabels(),
		Existing:      LegacyRelabels(),
	}

	for i := 0; i < len(lines); i++ {
		m := planHeadingRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		id := m[1]
		title := m[2]
		rest := strings.TrimSpace(m[3])

		// A heading paragraph may wrap before the Blocked by sentence.
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" && planHeadingRE.FindStringSubmatch(lines[j]) == nil && !planSectionRE.MatchString(lines[j]) {
			rest += " " + strings.TrimSpace(lines[j])
			j++
		}
		rest = strings.Join(strings.Fields(rest), " ")

		blockedText := ""
		if bm := blockedByRE.FindStringSubmatch(rest); bm != nil {
			blockedText = strings.TrimSpace(strings.TrimSuffix(bm[1], "."))
		}
		if blockedText == "" {
			return nil, fmt.Errorf("%s: heading has no Blocked by sentence", id)
		}

		// Body starts after the blank line that follows the heading paragraph.
		start := j
		for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
			start++
		}
		end := start
		for end < len(lines) {
			if planHeadingRE.FindStringSubmatch(lines[end]) != nil || planSectionRE.MatchString(lines[end]) {
				break
			}
			end++
		}
		rawBody := strings.Join(lines[start:end], "\n")
		rawBody = strings.TrimRight(rawBody, " \t\n")
		if rawBody == "" {
			return nil, fmt.Errorf("%s: ticket has no body", id)
		}

		num, err := ticketNumber(id)
		if err != nil {
			return nil, err
		}
		ec, err := exitCriterion(num)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		issueType := "type:micro"
		if num == 39 {
			issueType = "type:spike"
		}

		parent := ""
		if pm := parentNumberRE.FindStringSubmatch(title); pm != nil {
			parent = "#" + pm[1]
		}
		title = parentRE.ReplaceAllString(title, "")
		title = cutLineRE.ReplaceAllString(title, "")
		title = strings.Join(strings.Fields(title), " ")
		if title == "" {
			return nil, fmt.Errorf("%s: empty title", id)
		}

		body := fmt.Sprintf("%s\nParent: %s\nEC: %s\nType: %s\nPlan: docs/alpha-exit-plan.md\n\n%s\n\n%s\n",
			Marker(id), valueOrNone(parent), strings.TrimPrefix(ec, "ec:"), strings.TrimPrefix(issueType, "type:"), blockedHeading+" "+blockedText+".", rawBody)
		if !strings.Contains(body, "Blocked by:") {
			return nil, fmt.Errorf("%s: rendered body lost the Blocked by section", id)
		}

		issue := Issue{
			ID:        id,
			Title:     id + ": " + title,
			Phase:     "phase:alpha-exit",
			EC:        ec,
			Type:      issueType,
			BlockedBy: blockedIssueRE.FindAllString(blockedText, -1),
			Body:      body,
		}
		if issue.BlockedBy == nil {
			issue.BlockedBy = []string{}
		}
		manifest.Issues = append(manifest.Issues, issue)

		// Skip the body so nested **AX- strings cannot be mistaken for headings.
		i = end - 1
	}
	if len(manifest.Issues) == 0 {
		return nil, fmt.Errorf("no AX tickets found in plan")
	}
	sort.Slice(manifest.Issues, func(i, j int) bool {
		return manifest.Issues[i].ID < manifest.Issues[j].ID
	})
	return manifest, nil
}

func ticketNumber(id string) (int, error) {
	m := ticketNumberRE.FindStringSubmatch(id)
	if m == nil {
		return 0, fmt.Errorf("invalid ticket id %q", id)
	}
	return strconv.Atoi(m[1])
}

// exitCriterion maps the ticket number ranges in the plan to one EC label.
func exitCriterion(n int) (string, error) {
	switch {
	case n >= 1 && n <= 6:
		// AX-05 is the label/issue lint that EC7 depends on.
		if n == 5 {
			return "ec:7", nil
		}
		return "ec:6", nil
	case n >= 10 && n <= 14:
		return "ec:4", nil
	case n >= 20 && n <= 39:
		return "ec:3", nil
	case n >= 40 && n <= 44:
		return "ec:1", nil
	case n >= 45 && n <= 49:
		return "ec:2", nil
	case n >= 50 && n <= 55:
		return "ec:5", nil
	case n == 60 || n == 61:
		return "ec:7", nil
	default:
		return "", fmt.Errorf("no exit criterion mapping for AX-%d", n)
	}
}

func valueOrNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// MarshalManifest renders a manifest as stable, indented JSON.
func MarshalManifest(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
