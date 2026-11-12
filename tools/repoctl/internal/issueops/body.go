package issueops

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// blockedSection is the heading of the blocker list in a rendered body.
const blockedSection = "## Blocked by"

var acceptanceRE = regexp.MustCompile(`^(Red tests|Edge and negative|Done when)\b`)

type planItems struct {
	describe []string
	accept   []string
}

// splitItems reads the bullets of a plan ticket. Wrapped lines join their
// bullet. A bullet that starts with a check label is an acceptance criterion,
// and every other bullet describes the work.
func splitItems(raw string) planItems {
	var items planItems
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		text := strings.Join(strings.Fields(strings.Join(current, " ")), " ")
		if acceptanceRE.MatchString(text) {
			items.accept = append(items.accept, text)
		} else {
			items.describe = append(items.describe, text)
		}
		current = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "- "):
			flush()
			current = append(current, line[len("- "):])
		default:
			current = append(current, strings.TrimSpace(line))
		}
	}
	flush()
	return items
}

// renderBody writes an issue body in the to-tickets form. The hidden marker
// lets sync find the issue again; labels carry the phase, EC, type, and owner.
func renderBody(id, parent, title, raw, blockedText string, blockedBy []string) string {
	items := splitItems(raw)
	if len(items.describe) == 0 {
		items.describe = []string{title + "."}
	}
	var b strings.Builder
	b.WriteString(Marker(id) + "\n")
	if parent != "" {
		b.WriteString("\n## Parent\n\n" + parent + "\n")
	}
	b.WriteString("\n## What to build\n\n")
	for _, text := range items.describe {
		b.WriteString("- " + text + "\n")
	}
	b.WriteString("\n## Acceptance criteria\n\n")
	for _, text := range items.accept {
		b.WriteString("- [ ] " + text + "\n")
	}
	b.WriteString("\n" + blockedSection + "\n\n")
	for _, line := range blockerLines(blockedText, blockedBy) {
		b.WriteString("- " + line + "\n")
	}
	return b.String()
}

// blockerLines lists the blockers. A ticket with none says so, and blocker
// text that names no AX ticket is kept as written.
func blockerLines(blockedText string, blockedBy []string) []string {
	switch {
	case len(blockedBy) > 0:
		return blockedBy
	case blockedText == "none":
		return []string{"None (can start immediately)."}
	default:
		return []string{capitalize(blockedText) + "."}
	}
}

func capitalize(text string) string {
	r, size := utf8.DecodeRuneInString(text)
	if r == utf8.RuneError {
		return text
	}
	return string(unicode.ToUpper(r)) + text[size:]
}
