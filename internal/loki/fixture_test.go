package loki

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFixtureExercisesParsers keeps the LogQL corpus from regressing to a set of
// near-identical lines. It requires entries that exercise multi-line, unicode,
// empty, and long bodies, identical timestamps, and JSON and logfmt parser
// cases that produce non-trivial, distinct label sets.
func TestFixtureExercisesParsers(t *testing.T) {
	corpus := readFixtureCorpus(t)
	entries := make([]LogEntry, 0, len(corpus.Entries))
	for _, raw := range corpus.Entries {
		timestamp, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
		if err != nil {
			t.Fatalf("entry timestamp %q: %v", raw.Timestamp, err)
		}
		entries = append(entries, LogEntry{Timestamp: timestamp, Body: raw.Body, Labels: raw.Labels})
	}

	features := map[string]bool{}
	timestamps := map[string]int{}
	for _, entry := range entries {
		timestamps[entry.Timestamp.String()]++
		switch {
		case entry.Body == "":
			features["empty"] = true
		case strings.Contains(entry.Body, "\n"):
			features["multi-line"] = true
		case hasNonASCII(entry.Body):
			features["unicode"] = true
		case len(entry.Body) >= 1024:
			features["long"] = true
		}
	}
	for _, want := range []string{"empty", "multi-line", "unicode", "long"} {
		if !features[want] {
			t.Errorf("corpus has no entry with a %s body", want)
		}
	}
	duplicates := false
	for _, count := range timestamps {
		if count > 1 {
			duplicates = true
			break
		}
	}
	if !duplicates {
		t.Error("corpus has no entries with identical timestamps")
	}

	matched := make([]bool, len(entries))
	parserCases := map[string]int{}
	parserSignatures := map[string]map[string]bool{}
	for _, tc := range corpus.Cases {
		if tc.Kind != "log" {
			continue
		}
		query, err := Parse(tc.Query)
		if err != nil {
			t.Fatalf("%s: Parse: %v", tc.Name, err)
		}
		start := parseCorpusTime(t, tc.Start)
		end := parseCorpusTime(t, tc.End)
		parser := parserStage(tc.Query)
		signatures := map[string]bool{}
		for i, entry := range entries {
			if entry.Timestamp.Before(start) || entry.Timestamp.After(end) {
				continue
			}
			labels, ok := query.Evaluate(entry)
			if !ok {
				continue
			}
			matched[i] = true
			signatures[labelsKey(labels)] = true
		}
		if parser == "" {
			continue
		}
		if len(signatures) == 0 {
			t.Errorf("%s: %s parser case %q matches no entries", tc.Name, parser, tc.Query)
		}
		parserCases[parser]++
		if parserSignatures[parser] == nil {
			parserSignatures[parser] = map[string]bool{}
		}
		for signature := range signatures {
			parserSignatures[parser][signature] = true
		}
	}
	for _, parser := range []string{"json", "logfmt"} {
		if parserCases[parser] < 2 {
			t.Errorf("corpus has %d %s parser cases, want at least 2", parserCases[parser], parser)
		}
		if len(parserSignatures[parser]) < 2 {
			t.Errorf("%s parser cases produce %d distinct label sets, want at least 2", parser, len(parserSignatures[parser]))
		}
	}

	for i, entry := range entries {
		switch {
		case entry.Body == "":
			if !matched[i] {
				t.Errorf("empty-body entry %s is not matched by any case", entry.Timestamp)
			}
		case strings.Contains(entry.Body, "\n"):
			if !matched[i] {
				t.Errorf("multi-line entry %s is not matched by any case", entry.Timestamp)
			}
		case hasNonASCII(entry.Body):
			if !matched[i] {
				t.Errorf("unicode entry %s is not matched by any case", entry.Timestamp)
			}
		case len(entry.Body) >= 1024:
			if !matched[i] {
				t.Errorf("long entry %s is not matched by any case", entry.Timestamp)
			}
		}
	}
}

// TestUpstreamSeedExercisesParsers applies the same diversity requirement to
// the synthetic bodies seeded into both Loki and ClickHouse for the pinned
// upstream corpus.
func TestUpstreamSeedExercisesParsers(t *testing.T) {
	data, err := os.ReadFile("../../test/compat/loki/upstream/seed.json")
	if err != nil {
		t.Fatalf("read upstream seed: %v", err)
	}
	var seed struct {
		Variants []struct {
			Level string `json:"level"`
			Body  string `json:"body"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatalf("decode upstream seed: %v", err)
	}
	if len(seed.Variants) < 8 {
		t.Fatalf("upstream seed has %d variants, want at least 8", len(seed.Variants))
	}
	features := map[string]bool{}
	levels := map[string]bool{}
	for _, variant := range seed.Variants {
		levels[variant.Level] = true
		switch {
		case variant.Body == "":
			features["empty"] = true
		case strings.Contains(variant.Body, "\n"):
			features["multi-line"] = true
		case hasNonASCII(variant.Body):
			features["unicode"] = true
		case len(variant.Body) >= 1024:
			features["long"] = true
		}
		if strings.Contains(variant.Body, "|") {
			t.Errorf("variant body contains a pipe: %q", variant.Body)
		}
		if strings.Contains(variant.Body, "{") && !json.Valid([]byte(variant.Body)) {
			t.Errorf("variant body looks like JSON but is invalid: %q", variant.Body)
		}
		if strings.Contains(variant.Body, "level=") && !strings.Contains(variant.Body, "msg=") {
			t.Errorf("logfmt variant is missing msg: %q", variant.Body)
		}
	}
	for _, want := range []string{"empty", "multi-line", "unicode", "long"} {
		if !features[want] {
			t.Errorf("upstream seed has no %s body", want)
		}
	}
	if len(levels) < 3 {
		t.Errorf("upstream seed covers %d levels, want at least 3", len(levels))
	}
}

func readFixtureCorpus(t *testing.T) corpusFile {
	t.Helper()
	data, err := os.ReadFile("../../test/corpus/logql/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var corpus corpusFile
	if err := decoder.Decode(&corpus); err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	return corpus
}

func parserStage(query string) string {
	switch {
	case strings.Contains(query, "| json"):
		return "json"
	case strings.Contains(query, "| logfmt"):
		return "logfmt"
	default:
		return ""
	}
}

func hasNonASCII(text string) bool {
	for _, r := range text {
		if r > 127 {
			return true
		}
	}
	return false
}
