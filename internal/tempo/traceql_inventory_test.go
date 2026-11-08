package tempo

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestRejectedFormsMatchDocs makes the rejected-forms tables in
// docs/traceql-support.md executable. Every documented query must return the
// documented error, and the inventory in traceql_inventory.go must match the
// tables exactly: adding a rejected form without documenting it, or documenting
// one without adding it to the inventory, fails here.
func TestRejectedFormsMatchDocs(t *testing.T) {
	docs := readTraceQLDocs(t)
	documented, accepted := parseRejectedForms(t, docs)
	if len(documented) == 0 {
		t.Fatal("docs contain no rejected forms")
	}
	if len(accepted) == 0 {
		t.Fatal("docs contain no accepted form")
	}

	inventory := make(map[string]string, len(traceQLRejectedForms))
	for _, form := range traceQLRejectedForms {
		if _, ok := inventory[form.Query]; ok {
			t.Fatalf("inventory contains %q more than once", form.Query)
		}
		inventory[form.Query] = form.Want
	}

	for query, want := range inventory {
		documentedWant, ok := documented[query]
		if !ok {
			t.Errorf("rejected form %q is not documented in docs/traceql-support.md", query)
			continue
		}
		if documentedWant != want {
			t.Errorf("rejected form %q: docs say %q, inventory says %q", query, documentedWant, want)
		}
		assertRejected(t, query, want)
	}
	for query := range documented {
		if _, ok := inventory[query]; !ok {
			t.Errorf("documented rejected form %q is missing from traceQLRejectedForms", query)
		}
	}

	for _, query := range accepted {
		filter, err := ParseTraceQL(query)
		if err != nil {
			t.Errorf("documented accepted form %q: %v", query, err)
			continue
		}
		if filter == nil {
			t.Errorf("documented accepted form %q parsed to a nil filter", query)
		}
	}
}

// TestRejectedFormVariants keeps whitespace and nesting from changing the
// outcome. Outer and collapsed whitespace must produce the same documented
// error; wrapping the form in a spanset must still fail closed.
func TestRejectedFormVariants(t *testing.T) {
	for _, form := range traceQLRejectedForms {
		t.Run(form.Query, func(t *testing.T) {
			for name, variant := range map[string]string{
				"outer whitespace":     "\n\t " + form.Query + "  \n",
				"collapsed whitespace": strings.Join(strings.Fields(form.Query), " "),
			} {
				t.Run(name, func(t *testing.T) {
					assertRejected(t, variant, form.Want)
				})
			}
			t.Run("nested in spanset", func(t *testing.T) {
				filter, err := ParseTraceQL("{ " + form.Query + " }")
				if err == nil {
					t.Fatalf("ParseTraceQL(%q) = %#v, want an error", "{ "+form.Query+" }", filter)
				}
				if !strings.HasPrefix(err.Error(), "tempo: ") {
					t.Fatalf("error = %q, want a tempo: error", err)
				}
			})
		})
	}
}

func assertRejected(t *testing.T, query, want string) {
	t.Helper()
	filter, err := ParseTraceQL(query)
	if err == nil {
		t.Fatalf("ParseTraceQL(%q) = %#v, want an error", query, filter)
	}
	if !strings.HasPrefix(err.Error(), "tempo: "+want) {
		t.Fatalf("ParseTraceQL(%q) error = %q, want prefix %q", query, err, "tempo: "+want)
	}
}

func readTraceQLDocs(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../docs/traceql-support.md")
	if err != nil {
		t.Fatalf("read docs: %v", err)
	}
	return string(data)
}

// parseRejectedForms returns the query-to-error map for the rejected forms and
// the queries documented as accepted. It reads only the Rejected query shapes
// section so unrelated tables cannot be mistaken for TraceQL examples.
func parseRejectedForms(t *testing.T, docs string) (map[string]string, []string) {
	t.Helper()
	const (
		start = "### Rejected query shapes"
		end   = "## Tag-based search"
	)
	startIndex := strings.Index(docs, start)
	if startIndex < 0 {
		t.Fatalf("docs are missing the %q section", start)
	}
	section := docs[startIndex+len(start):]
	if endIndex := strings.Index(section, end); endIndex >= 0 {
		section = section[:endIndex]
	}

	rejected := map[string]string{}
	accepted := []string{}
	mode := ""
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := splitMarkdownRow(line)
		if len(cells) == 0 || isTableSeparator(cells) {
			continue
		}
		switch cells[0] {
		case "Operator":
			mode = "operator"
			continue
		case "Query":
			mode = "query"
			continue
		}
		switch mode {
		case "operator":
			if len(cells) < 3 {
				t.Fatalf("malformed operator row: %q", line)
			}
			operators := splitList(cells[0])
			if len(operators) == 0 {
				t.Fatalf("operator row has no operators: %q", line)
			}
			for _, op := range operators {
				query := strings.Replace(cells[2], operators[0], op, 1)
				want := fmt.Sprintf("TraceQL structural operator %q is not supported", op)
				addDocumentedForm(t, rejected, query, want)
			}
		case "query":
			if len(cells) < 2 || cells[0] == "" || cells[1] == "" {
				continue
			}
			if strings.HasPrefix(cells[1], "Accepted") {
				accepted = append(accepted, cells[0])
				continue
			}
			addDocumentedForm(t, rejected, cells[0], cells[1])
		}
	}
	return rejected, accepted
}

func addDocumentedForm(t *testing.T, forms map[string]string, query, want string) {
	t.Helper()
	if previous, ok := forms[query]; ok && previous != want {
		t.Fatalf("docs document %q with both %q and %q", query, previous, want)
	}
	forms[query] = want
}

// splitMarkdownRow splits a table row on unescaped pipes. Escaped pipes are
// literal data, as in {a} \|\| {b} and pipeline examples.
func splitMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := []string{}
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cell.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, cleanCell(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteByte(line[i])
	}
	cells = append(cells, cleanCell(cell.String()))
	return cells
}

func cleanCell(cell string) string {
	cell = strings.ReplaceAll(cell, "`", "")
	cell = strings.ReplaceAll(cell, `\|`, "|")
	return strings.TrimSpace(cell)
}

func isTableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if strings.Trim(cell, ":- ") != "" {
			return false
		}
	}
	return true
}

func splitList(cell string) []string {
	parts := strings.Split(cell, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
