package exitcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

func writeEvidence(t *testing.T, dir string, evidence Evidence) {
	t.Helper()
	writeAt(t, dir, "ec-"+strconv.Itoa(evidence.EC)+".json", evidence)
}

func writeAt(t *testing.T, dir, name string, evidence Evidence) {
	t.Helper()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func freshEvidence(now time.Time, commit string) []Evidence {
	out := make([]Evidence, 0, len(Criteria))
	for _, criterion := range Criteria {
		out = append(out, Evidence{
			EC:        criterion.Number,
			Status:    "pass",
			Commit:    commit,
			Timestamp: now.Add(-time.Minute),
			Command:   "make ec-" + strconv.Itoa(criterion.Number),
		})
	}
	return out
}

func testEnv(now time.Time) (command.Env, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return now },
	}, stdout
}

func TestEvaluateAllPass(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	for _, evidence := range freshEvidence(now, "abc") {
		writeEvidence(t, dir, evidence)
	}
	results, err := Evaluate(dir, "abc", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 7 {
		t.Fatalf("results = %d, want 7", len(results))
	}
	for _, result := range results {
		if result.Problem != "" {
			t.Fatalf("EC%d: %s", result.Number, result.Problem)
		}
	}
}

func TestEvaluateFailures(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		patch  func([]Evidence) []Evidence
		remove int
		want   string
	}{
		{name: "failing EC", patch: func(all []Evidence) []Evidence {
			all[2].Status = "fail"
			return all
		}, want: "status fail"},
		{name: "stale", patch: func(all []Evidence) []Evidence {
			all[0].Timestamp = now.Add(-48 * time.Hour)
			return all
		}, want: "older than"},
		{name: "wrong commit", patch: func(all []Evidence) []Evidence {
			all[4].Commit = "other"
			return all
		}, want: "is not abc"},
		{name: "empty command", patch: func(all []Evidence) []Evidence {
			all[1].Command = ""
			return all
		}, want: "command is empty"},
		{name: "missing timestamp", patch: func(all []Evidence) []Evidence {
			all[5].Timestamp = time.Time{}
			return all
		}, want: "timestamp is missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, evidence := range test.patch(freshEvidence(now, "abc")) {
				writeEvidence(t, dir, evidence)
			}
			results, err := Evaluate(dir, "abc", time.Hour, now)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, result := range results {
				if result.Problem != "" && strings.Contains(result.Problem, test.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("results = %+v, want a problem containing %q", results, test.want)
			}
		})
	}
}

func TestEvaluateMissingEvidence(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	all := freshEvidence(now, "abc")
	for _, evidence := range all[:6] {
		writeEvidence(t, dir, evidence)
	}
	results, err := Evaluate(dir, "abc", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if results[6].Problem == "" || !strings.Contains(results[6].Problem, "missing evidence") {
		t.Fatalf("EC7 = %+v, want missing evidence", results[6])
	}
}

func TestEvaluateMalformedAndWrongEC(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	all := freshEvidence(now, "abc")
	wrong := all[0]
	wrong.EC = 2
	writeAt(t, dir, "ec-1.json", wrong)
	for _, evidence := range all[1:] {
		writeEvidence(t, dir, evidence)
	}
	if err := os.WriteFile(filepath.Join(dir, "ec-3.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := Evaluate(dir, "abc", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Problem == "" {
		t.Fatal("wrong EC number was accepted")
	}
	if results[2].Problem == "" {
		t.Fatal("malformed evidence was accepted")
	}
}

func TestCommandWritesReport(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	for _, evidence := range freshEvidence(now, "abc") {
		writeEvidence(t, dir, evidence)
	}
	report := filepath.Join(t.TempDir(), "report.md")
	env, stdout := testEnv(now)
	err := (Command{}).Run(context.Background(), env, []string{
		"--dir", dir, "--commit", "abc", "--report", report,
	})
	if err != nil {
		t.Fatalf("exit-check: %v", err)
	}
	if !strings.Contains(stdout.String(), "7/7 criteria pass") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Alpha exit report", "| EC1 | Grafana plug-and-play | pass", "| EC7 | Scope discipline | pass"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("report missing %q:\n%s", want, body)
		}
	}
}

func TestCommandFailsClosed(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	env, _ := testEnv(now)
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", t.TempDir(), "--check"}); err == nil {
		t.Fatal("exit-check passed with no evidence")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"--dir", t.TempDir(), "--max-age", "0s", "--check"}); err == nil {
		t.Fatal("exit-check accepted a zero max-age")
	}
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "exit-check" {
		t.Fatalf("Name = %q", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}
