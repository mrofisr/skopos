package osvexceptions

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

func testEnv() (command.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) },
	}, stdout, stderr
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "osv" {
		t.Fatalf("Name = %q, want osv", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestOsvValidLedger(t *testing.T) {
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--file", "testdata/valid.yaml"}); err != nil {
		t.Fatalf("osv: %v", err)
	}
	if !strings.Contains(stdout.String(), "osv: 2 exceptions, 0 expired") {
		t.Fatalf("stdout = %q, want the exception count", stdout.String())
	}
}

func TestOsvExpiredLedger(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--file", "testdata/expired.yaml"})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want an expiry error", err)
	}
}

func TestOsvUsesInjectedClock(t *testing.T) {
	env, _, _ := testEnv()
	env.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	err := (Command{}).Run(context.Background(), env, []string{"--file", "testdata/valid.yaml"})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %v, want the injected clock to expire the ledger", err)
	}
}

func TestOsvNowOverridesTheClock(t *testing.T) {
	env, _, _ := testEnv()
	env.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	if err := (Command{}).Run(context.Background(), env, []string{"--file", "testdata/valid.yaml", "--now", "2020-01-01"}); err != nil {
		t.Fatalf("osv: %v", err)
	}
}

func TestOsvRejectsInvalidNow(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--now", "09/11/2026"})
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("error = %v, want a date-format error", err)
	}
}

func TestOsvMissingLedger(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--file", filepath.Join(t.TempDir(), "missing.yaml")})
	if err == nil || !strings.Contains(err.Error(), "read ledger") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestOsvMalformedLedger(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--file", "testdata/unknown-field.yaml"})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want a parse error", err)
	}
}

func TestOsvRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("osv accepted an unknown flag")
	}
}
