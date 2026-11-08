// Package osvexceptions validates the dated OSV/govulncheck exception ledger.
// The file uses a deliberately small YAML subset so the checker stays
// stdlib-only and cannot silently accept an ambiguous document.
package osvexceptions

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Exception is one accepted OSV finding.
type Exception struct {
	ID      string
	Reason  string
	Expires string
}

// Ledger is the parsed exception file.
type Ledger struct {
	Exceptions []Exception
}

var advisoryID = regexp.MustCompile(`^(GO-[0-9]{4}-[0-9]{4,}|CVE-[0-9]{4}-[0-9]{4,}|GHSA(-[0-9A-Za-z]{4}){3}|OSV-[0-9]{4}-[0-9]+)$`)

// Parse reads the strict subset documented in docs/security/osv-exceptions.yaml:
//
//	exceptions: []
//	exceptions:
//	  - id: GO-2026-0001
//	    reason: one line of justification
//	    expires: 2027-01-31
//
// Comments, blank lines, and indentation are accepted. Unknown keys, values
// outside the subset, and content before the exceptions key are rejected.
func Parse(data []byte) (Ledger, error) {
	var ledger Ledger
	sawExceptions := false
	closed := false
	current := -1
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if strings.HasPrefix(trimmed, "exceptions:") {
			if sawExceptions {
				return Ledger{}, fmt.Errorf("line %d: duplicate exceptions key", index+1)
			}
			sawExceptions = true
			switch rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "exceptions:")); rest {
			case "":
			case "[]":
				closed = true
			default:
				return Ledger{}, fmt.Errorf("line %d: exceptions must be a list", index+1)
			}
			continue
		}
		if !sawExceptions {
			return Ledger{}, fmt.Errorf("line %d: content before the exceptions key", index+1)
		}
		if closed {
			return Ledger{}, fmt.Errorf("line %d: content after exceptions: []", index+1)
		}
		if strings.HasPrefix(trimmed, "-") {
			if indent < 2 {
				return Ledger{}, fmt.Errorf("line %d: list item must be indented", index+1)
			}
			ledger.Exceptions = append(ledger.Exceptions, Exception{})
			current = len(ledger.Exceptions) - 1
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if rest == "" {
				continue
			}
			key, value, ok := splitField(rest)
			if !ok {
				return Ledger{}, fmt.Errorf("line %d: expected key: value", index+1)
			}
			if err := assign(&ledger.Exceptions[current], key, value); err != nil {
				return Ledger{}, fmt.Errorf("line %d: %w", index+1, err)
			}
			continue
		}
		key, value, ok := splitField(trimmed)
		if !ok {
			return Ledger{}, fmt.Errorf("line %d: expected key: value", index+1)
		}
		if current < 0 || indent < 4 {
			return Ledger{}, fmt.Errorf("line %d: field outside an exception", index+1)
		}
		if err := assign(&ledger.Exceptions[current], key, value); err != nil {
			return Ledger{}, fmt.Errorf("line %d: %w", index+1, err)
		}
	}
	if !sawExceptions {
		return Ledger{}, fmt.Errorf("missing exceptions key")
	}
	return ledger, nil
}

func splitField(line string) (key, value string, ok bool) {
	index := strings.Index(line, ":")
	if index <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:index])
	value = strings.TrimSpace(line[index+1:])
	if key == "" {
		return "", "", false
	}
	return key, unquote(value), true
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func assign(exception *Exception, key, value string) error {
	if value == "" {
		return fmt.Errorf("field %q has an empty value", key)
	}
	switch key {
	case "id":
		exception.ID = value
	case "reason":
		exception.Reason = value
	case "expires":
		exception.Expires = value
	default:
		return fmt.Errorf("unknown field %q", key)
	}
	return nil
}

// Validate checks every exception and reports all problems at once. An
// exception is valid through its expiry date, inclusive.
func Validate(ledger Ledger, now time.Time) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	problems := make([]string, 0)
	seen := make(map[string]int, len(ledger.Exceptions))
	for index, exception := range ledger.Exceptions {
		position := index + 1
		id := strings.TrimSpace(exception.ID)
		label := fmt.Sprintf("exception %d", position)
		if id != "" {
			label += " (" + id + ")"
		}
		switch {
		case id == "":
			problems = append(problems, label+": id is required")
		case !advisoryID.MatchString(id):
			problems = append(problems, label+": unknown advisory id")
		case seen[id] != 0:
			problems = append(problems, fmt.Sprintf("%s: duplicate id (first at exception %d)", label, seen[id]))
		default:
			seen[id] = position
		}
		if strings.TrimSpace(exception.Reason) == "" {
			problems = append(problems, label+": reason is required")
		}
		if strings.TrimSpace(exception.Expires) == "" {
			problems = append(problems, label+": expires is required")
			continue
		}
		expires, err := time.Parse("2006-01-02", exception.Expires)
		if err != nil {
			problems = append(problems, label+": expires must be YYYY-MM-DD")
			continue
		}
		if expires.Before(today) {
			problems = append(problems, fmt.Sprintf("%s: expired on %s", label, exception.Expires))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("osv exceptions: %s", strings.Join(problems, "; "))
	}
	return nil
}
