package mutation

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

const reportFixture = `{"files":[{"mutations":[{"status":"KILLED"},{"status":"LIVED"},{"status":"TIMED OUT"}]}]}`

func writeReport(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mutation.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testEnv() (command.Env, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
		Getenv: func(string) string { return "" },
	}, stdout
}

func TestParseEfficacy(t *testing.T) {
	counts, err := Parse([]byte(reportFixture))
	if err != nil {
		t.Fatal(err)
	}
	if counts.Killed != 1 || counts.Lived != 1 || counts.TimedOut != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	efficacy, err := counts.Efficacy()
	if err != nil {
		t.Fatal(err)
	}
	if efficacy != 50 {
		t.Fatalf("efficacy = %v, want 50", efficacy)
	}
}

func TestParseRejectsMalformedAndUnknown(t *testing.T) {
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Fatal("Parse accepted invalid JSON")
	}
	if _, err := Parse([]byte(`{"files":[{"mutations":[{"status":"UNKNOWN"}]}]}`)); err == nil {
		t.Fatal("Parse accepted an unknown status")
	}
	if _, err := Parse([]byte(`{"files":[]}`)); err != nil {
		t.Fatalf("empty report: %v", err)
	}
}

func TestCountsEfficacyWithoutMutants(t *testing.T) {
	if _, err := (Counts{}).Efficacy(); err == nil {
		t.Fatal("Efficacy accepted zero killed+lived")
	}
}

func TestCommandReport(t *testing.T) {
	path := writeReport(t, reportFixture)
	env, stdout := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--input", path}); err != nil {
		t.Fatalf("mutation report: %v", err)
	}
	for _, want := range []string{"killed=1", "lived=1", "efficacy=50.0%"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestCommandThreshold(t *testing.T) {
	path := writeReport(t, reportFixture)
	env, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--input", path, "--threshold-efficacy", "60"}); err == nil {
		t.Fatal("threshold 60 passed at 50% efficacy")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--input", path, "--threshold-efficacy", "40"}); err != nil {
		t.Fatalf("threshold 40: %v", err)
	}
}

func TestCommandErrors(t *testing.T) {
	env, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, nil); err == nil {
		t.Fatal("usage was accepted")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--input", filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("missing report was accepted")
	}
	path := writeReport(t, `{`)
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--input", path}); err == nil {
		t.Fatal("malformed report was accepted")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"report", "--threshold-efficacy", "101"}); err == nil {
		t.Fatal("threshold 101 was accepted")
	}
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "mutation" {
		t.Fatalf("Name = %q", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

const gateFixture = `{"files":[{"file_name":"basic.go","mutations":[
  {"type":"ARITHMETIC_BASE","status":"KILLED","line":10,"column":1},
  {"type":"ARITHMETIC_BASE","status":"KILLED","line":11,"column":1},
  {"type":"ARITHMETIC_BASE","status":"KILLED","line":12,"column":1},
  {"type":"CONDITIONALS_NEGATION","status":"LIVED","line":20,"column":1},
  {"type":"CONDITIONALS_NEGATION","status":"LIVED","line":21,"column":1}
]}]}`

func TestGateFailsBelowFloor(t *testing.T) {
	parsed, err := ParseReport([]byte(gateFixture))
	if err != nil {
		t.Fatal(err)
	}
	err = Gate(parsed, 70, Allowlist{})
	if err == nil || !strings.Contains(err.Error(), "basic.go:20") {
		t.Fatalf("error = %v, want an unallowlisted survivor", err)
	}
	if !strings.Contains(err.Error(), "60.0%") {
		t.Fatalf("error = %v, want the efficacy", err)
	}
}

func TestGateAllowlistedSurvivorsPass(t *testing.T) {
	parsed, err := ParseReport([]byte(gateFixture))
	if err != nil {
		t.Fatal(err)
	}
	allowlist := Allowlist{Survivors: []AllowlistEntry{
		{File: "basic.go", Line: 20, Type: "CONDITIONALS_NEGATION", Reason: "clock skew guard"},
		{File: "basic.go", Line: 21, Type: "CONDITIONALS_NEGATION", Reason: "clock skew guard"},
	}}
	if err := Gate(parsed, 70, allowlist); err != nil {
		t.Fatalf("Gate: %v", err)
	}
}

func TestGateRejectsAllowlistWithoutReason(t *testing.T) {
	parsed, err := ParseReport([]byte(gateFixture))
	if err != nil {
		t.Fatal(err)
	}
	allowlist := Allowlist{Survivors: []AllowlistEntry{
		{File: "basic.go", Line: 20, Type: "CONDITIONALS_NEGATION"},
	}}
	err = Gate(parsed, 70, allowlist)
	if err == nil || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("error = %v, want a missing reason", err)
	}
}

func TestGateRejectsZeroMutants(t *testing.T) {
	parsed, err := ParseReport([]byte(`{"files":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := Gate(parsed, 70, Allowlist{}); err == nil {
		t.Fatal("Gate accepted a report with no killed or lived mutants")
	}
}

func TestCommandGate(t *testing.T) {
	path := writeReport(t, gateFixture)
	env, stdout := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"gate", "--input", path, "--floor", "70"}); err == nil {
		t.Fatal("gate passed at 60% efficacy")
	}
	allow := filepath.Join(t.TempDir(), "allow.json")
	body := `{"survivors":[
	  {"file":"basic.go","line":20,"type":"CONDITIONALS_NEGATION","reason":"clock skew"},
	  {"file":"basic.go","line":21,"type":"CONDITIONALS_NEGATION","reason":"clock skew"}
	]}`
	if err := os.WriteFile(allow, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Command{}).Run(context.Background(), env, []string{"gate", "--input", path, "--floor", "70", "--allowlist", allow}); err != nil {
		t.Fatalf("gate with allowlist: %v", err)
	}
	if !strings.Contains(stdout.String(), "efficacy=60.0%") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if err := (Command{}).Run(context.Background(), env, []string{"nope"}); err == nil {
		t.Fatal("unknown subcommand was accepted")
	}
}

func TestLoadAllowlistErrors(t *testing.T) {
	if _, err := LoadAllowlist(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("LoadAllowlist accepted a missing file")
	}
	path := writeReport(t, `{`)
	if _, err := LoadAllowlist(path); err == nil {
		t.Fatal("LoadAllowlist accepted invalid JSON")
	}
}
