package quality

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

type fakeRunner struct {
	output []byte
	err    error
	calls  [][]string
}

func (f *fakeRunner) Run(context.Context, string, ...string) error { return nil }

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.output, f.err
}

func testEnv() (command.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(0, 0) },
		Run:    &fakeRunner{},
	}, stdout, stderr
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "coverage" {
		t.Fatalf("Name = %q, want coverage", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestCoverageReportPrintsTable(t *testing.T) {
	profile := writeTemp(t, "coverage.out", fixtureProfile)
	floor := writeTemp(t, "floor.json", `{"module":"github.com/mrofisr/skopos","packages":{"internal/auth":100,"internal/sqlgen":50}}`)
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--profile", profile, "--floor", floor}); err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, want := range []string{"PACKAGE", "internal/auth", "100.0%", "internal/sqlgen", "50.0%"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestCoverageGateFailsBelowFloor(t *testing.T) {
	profile := writeTemp(t, "coverage.out", fixtureProfile)
	floor := writeTemp(t, "floor.json", `{"module":"github.com/mrofisr/skopos","packages":{"internal/auth":100,"internal/sqlgen":60}}`)
	env, _, stderr := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"gate", "--profile", profile, "--floor", floor})
	if err == nil {
		t.Fatal("gate passed, want a floor error")
	}
	if !strings.Contains(stderr.String(), "below the floor") {
		t.Fatalf("stderr = %q, want the floor error", stderr.String())
	}
}

func TestCoverageGateUsesRenamesForThePreviousFloor(t *testing.T) {
	profile := writeTemp(t, "coverage.out", `mode: set
github.com/mrofisr/skopos/cmd/new/main.go:1.1,2.2 2 1
`)
	floor := writeTemp(t, "floor.json", `{"module":"github.com/mrofisr/skopos","packages":{"cmd/new":50}}`)
	previous := writeTemp(t, "previous.json", `{"module":"github.com/mrofisr/skopos","packages":{"cmd/old":50}}`)
	renames := writeTemp(t, "renames.json", `{"cmd/old":"cmd/new"}`)
	env, _, stderr := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"gate", "--profile", profile, "--floor", floor,
		"--previous", previous, "--renames", renames,
	})
	if err != nil {
		t.Fatalf("gate: %v (stderr %s)", err, stderr.String())
	}
}

func TestCoverageWrite(t *testing.T) {
	profile := writeTemp(t, "coverage.out", fixtureProfile)
	floor := filepath.Join(t.TempDir(), "quality", "floor.json")
	env, _, stderr := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"write", "--profile", profile, "--floor", floor}); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(floor)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"internal/auth": 100`, `"internal/sqlgen": 50`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("floor = %s, want %q", data, want)
		}
	}
	if !strings.Contains(stderr.String(), "wrote") {
		t.Fatalf("stderr = %q, want a write notice", stderr.String())
	}
}

func TestCoverageUnknownSubcommand(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("error = %v, want unknown subcommand", err)
	}
}

func TestCoverageReportMissingProfile(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"report", "--profile", filepath.Join(t.TempDir(), "missing.out")})
	if err == nil {
		t.Fatal("report succeeded, want a missing-profile error")
	}
}

func TestApplyRenames(t *testing.T) {
	previous := &Floor{Module: "m", Packages: map[string]float64{
		"cmd/old":  10,
		"cmd/both": 10,
		"cmd/new":  20,
	}}
	got := applyRenames(previous, map[string]string{
		"cmd/old":  "cmd/newer",
		"cmd/both": "cmd/new",
	})
	if got.Packages["cmd/newer"] != 10 {
		t.Fatalf("cmd/newer = %v, want 10", got.Packages["cmd/newer"])
	}
	if got.Packages["cmd/new"] != 20 {
		t.Fatalf("cmd/new = %v, want the higher floor 20", got.Packages["cmd/new"])
	}
	if _, ok := got.Packages["cmd/old"]; ok {
		t.Fatal("old path still present")
	}
}

func TestApplyRenamesNil(t *testing.T) {
	if got := applyRenames(nil, map[string]string{"a": "b"}); got != nil {
		t.Fatalf("applyRenames(nil) = %#v, want nil", got)
	}
	previous := &Floor{Packages: map[string]float64{"a": 1}}
	if got := applyRenames(previous, nil); got != previous {
		t.Fatal("applyRenames with no renames copied the floor")
	}
}

func TestLoadPreviousUsesRunner(t *testing.T) {
	runner := &fakeRunner{output: []byte(`{"module":"github.com/mrofisr/skopos","packages":{"cmd/old":50}}`)}
	env, _, _ := testEnv()
	env.Run = runner
	previous, err := loadPrevious(context.Background(), env, options{floor: "floor.json", previousRef: "origin/development"})
	if err != nil {
		t.Fatalf("loadPrevious: %v", err)
	}
	if previous.Packages["cmd/old"] != 50 {
		t.Fatalf("previous = %#v", previous)
	}
	if len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != "git show origin/development:floor.json" {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestLoadPreviousUnavailableSkips(t *testing.T) {
	runner := &fakeRunner{err: errors.New("not found")}
	env, _, stderr := testEnv()
	env.Run = runner
	previous, err := loadPrevious(context.Background(), env, options{floor: "floor.json", previousRef: "origin/development"})
	if err != nil || previous != nil {
		t.Fatalf("loadPrevious = %#v, %v; want nil, nil", previous, err)
	}
	if !strings.Contains(stderr.String(), "skipping the lowering check") {
		t.Fatalf("stderr = %q, want a warning", stderr.String())
	}
}

func TestLoadPreviousMutuallyExclusive(t *testing.T) {
	env, _, _ := testEnv()
	_, err := loadPrevious(context.Background(), env, options{previous: "a", previousRef: "b"})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("error = %v, want a mutual-exclusion error", err)
	}
}

func TestLoadPreviousRequiresRunner(t *testing.T) {
	env, _, _ := testEnv()
	env.Run = nil
	_, err := loadPrevious(context.Background(), env, options{previousRef: "origin/development"})
	if err == nil || !strings.Contains(err.Error(), "command runner") {
		t.Fatalf("error = %v, want a runner error", err)
	}
}

func TestLoadRenames(t *testing.T) {
	path := writeTemp(t, "renames.json", `{"cmd/old":"cmd/new"}`)
	renames, err := loadRenames(path)
	if err != nil {
		t.Fatalf("loadRenames: %v", err)
	}
	if renames["cmd/old"] != "cmd/new" {
		t.Fatalf("renames = %#v", renames)
	}
	if missing, err := loadRenames(filepath.Join(t.TempDir(), "missing.json")); err != nil || missing != nil {
		t.Fatalf("missing renames = %#v, %v; want nil, nil", missing, err)
	}
	invalid := writeTemp(t, "invalid.json", `{`)
	if _, err := loadRenames(invalid); err == nil {
		t.Fatal("loadRenames accepted invalid JSON")
	}
}
