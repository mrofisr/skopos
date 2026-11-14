package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
	"github.com/mrofisr/skopos/tools/repoctl/internal/exitcheck"
)

type fakeRunner struct {
	runName   string
	runArgs   []string
	runErr    error
	output    []byte
	outputErr error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.runName = name
	f.runArgs = append([]string(nil), args...)
	return f.runErr
}

func (f *fakeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return f.output, f.outputErr
}

func testEnv(runner *fakeRunner, getenv func(string) string) command.Env {
	return command.Env{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Getenv: getenv,
		Now:    func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) },
		Run:    runner,
	}
}

func readEvidence(t *testing.T, path string) exitcheck.Evidence {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev exitcheck.Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestRunWritesPassingEvidence(t *testing.T) {
	runner := &fakeRunner{}
	env := testEnv(runner, func(string) string { return "abc123" })
	path := filepath.Join(t.TempDir(), "ec-3.json")
	err := (Command{}).Run(context.Background(), env, []string{
		"run", "--ec", "3", "--command", "make bench-gate", "--output", path,
		"--", "echo", "gate-output",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.runName != "echo" || strings.Join(runner.runArgs, " ") != "gate-output" {
		t.Fatalf("runner = %q %v", runner.runName, runner.runArgs)
	}
	ev := readEvidence(t, path)
	if ev.EC != 3 || ev.Status != "pass" || ev.Commit != "abc123" || ev.Command != "make bench-gate" {
		t.Fatalf("evidence = %+v", ev)
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("timestamp is zero")
	}
}

func TestRunRecordsFailure(t *testing.T) {
	runner := &fakeRunner{runErr: errors.New("boom")}
	env := testEnv(runner, func(string) string { return "abc123" })
	path := filepath.Join(t.TempDir(), "ec-3.json")
	err := (Command{}).Run(context.Background(), env, []string{
		"run", "--ec", "3", "--command", "make bench-gate", "--output", path,
		"--", "false",
	})
	if err == nil || !strings.Contains(err.Error(), "gate failed") {
		t.Fatalf("error = %v", err)
	}
	ev := readEvidence(t, path)
	if ev.Status != "fail" {
		t.Fatalf("status = %q, want fail", ev.Status)
	}
}

func TestRecordDefaultsCommitToGit(t *testing.T) {
	runner := &fakeRunner{output: []byte("deadbeef\n")}
	env := testEnv(runner, func(string) string { return "" })
	path := filepath.Join(t.TempDir(), "ec-7.json")
	if err := (Command{}).Run(context.Background(), env, []string{
		"record", "--ec", "7", "--command", "make alpha-exit-check", "--output", path,
	}); err != nil {
		t.Fatal(err)
	}
	if ev := readEvidence(t, path); ev.Commit != "deadbeef" {
		t.Fatalf("commit = %q", ev.Commit)
	}
}

func TestEvidenceSatisfiesExitCheck(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	runner := &fakeRunner{}
	env := testEnv(runner, func(string) string { return "abc123" })
	env.Now = func() time.Time { return now }
	dir := t.TempDir()
	for number := 1; number <= 7; number++ {
		path := filepath.Join(dir, "ec-"+strconv.Itoa(number)+".json")
		if err := (Command{}).Run(context.Background(), env, []string{
			"record", "--ec", strconv.Itoa(number),
			"--command", "make gate-" + strconv.Itoa(number),
			"--output", path,
		}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := exitcheck.Evaluate(dir, "abc123", time.Hour, now)
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

func TestRejectsInvalidInput(t *testing.T) {
	runner := &fakeRunner{}
	env := testEnv(runner, func(string) string { return "abc123" })
	for _, args := range [][]string{
		{"run", "--ec", "8", "--command", "make x", "--", "true"},
		{"run", "--ec", "1", "--", "true"},
		{"record", "--ec", "1"},
		{"unknown"},
	} {
		if err := (Command{}).Run(context.Background(), env, args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
