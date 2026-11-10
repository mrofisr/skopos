package quickstart

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

const readmeFixture = "# Skopos\n\n```sh quickstart\nhelm install skopos oci://ghcr.io/jawaracloud/charts/skopos --version 0.1.0-alpha.2\nkubectl get pods\n```\n"

type fakeRunner struct {
	calls [][]string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.err
}

func (f *fakeRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, nil
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

func writeReadme(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte(readmeFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "quickstart" {
		t.Fatalf("Name = %q, want quickstart", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestQuickstartWritesScriptAndVersion(t *testing.T) {
	readme := writeReadme(t)
	script := filepath.Join(t.TempDir(), "quickstart.sh")
	versionFile := filepath.Join(t.TempDir(), "version.txt")
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{
		"--readme", readme, "--script", script, "--version-file", versionFile,
	}); err != nil {
		t.Fatalf("quickstart: %v", err)
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#!/bin/sh", "helm install skopos", "kubectl get pods"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("script = %q, want %q", body, want)
		}
	}
	version, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != "0.1.0-alpha.2" {
		t.Fatalf("version = %q", version)
	}
	if !strings.Contains(stdout.String(), "0.1.0-alpha.2 is published") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestQuickstartVerifiesTheChart(t *testing.T) {
	readme := writeReadme(t)
	runner := &fakeRunner{}
	env, _, _ := testEnv()
	env.Run = runner
	if err := (Command{}).Run(context.Background(), env, []string{"--readme", readme}); err != nil {
		t.Fatalf("quickstart: %v", err)
	}
	want := "helm show chart oci://ghcr.io/jawaracloud/charts/skopos --version 0.1.0-alpha.2"
	if len(runner.calls) != 1 || strings.Join(runner.calls[0], " ") != want {
		t.Fatalf("calls = %v, want %q", runner.calls, want)
	}
}

func TestQuickstartFailsWhenTheChartIsMissing(t *testing.T) {
	readme := writeReadme(t)
	env, _, _ := testEnv()
	env.Run = &fakeRunner{err: errors.New("manifest unknown")}
	err := (Command{}).Run(context.Background(), env, []string{"--readme", readme})
	if err == nil || !strings.Contains(err.Error(), "not available in the registry") {
		t.Fatalf("error = %v, want a registry error", err)
	}
}

func TestQuickstartCheckCanBeSkipped(t *testing.T) {
	readme := writeReadme(t)
	runner := &fakeRunner{}
	env, _, _ := testEnv()
	env.Run = runner
	if err := (Command{}).Run(context.Background(), env, []string{"--readme", readme, "--check=false"}); err != nil {
		t.Fatalf("quickstart: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %v, want none", runner.calls)
	}
}

func TestQuickstartRequiresARunner(t *testing.T) {
	readme := writeReadme(t)
	env, _, _ := testEnv()
	env.Run = nil
	err := (Command{}).Run(context.Background(), env, []string{"--readme", readme})
	if err == nil || !strings.Contains(err.Error(), "command runner") {
		t.Fatalf("error = %v, want a runner error", err)
	}
}

func TestQuickstartMissingReadme(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--readme", filepath.Join(t.TempDir(), "missing.md")})
	if err == nil || !strings.Contains(err.Error(), "read README") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestQuickstartRejectsUnexpectedArguments(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}

func TestQuickstartRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("quickstart accepted an unknown flag")
	}
}

func TestQuickstartVersionFileError(t *testing.T) {
	readme := writeReadme(t)
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--readme", readme,
		"--version-file", filepath.Join(t.TempDir(), "missing", "version.txt"),
	})
	if err == nil || !strings.Contains(err.Error(), "write version file") {
		t.Fatalf("error = %v, want a version-file error", err)
	}
}

func TestQuickstartRejectsMalformedReadme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("```sh quickstart\nhelm install x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--readme", path})
	if err == nil || !strings.Contains(err.Error(), "unclosed quickstart fence") {
		t.Fatalf("error = %v, want an unclosed-fence error", err)
	}
}

func TestQuickstartRejectsPlanWithoutVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	body := "```sh quickstart\nhelm install skopos oci://ghcr.io/jawaracloud/charts/skopos\n```\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--readme", path})
	if err == nil || !strings.Contains(err.Error(), "exactly one --version") {
		t.Fatalf("error = %v, want a version-plan error", err)
	}
}

func TestQuickstartScriptDirectoryError(t *testing.T) {
	readme := writeReadme(t)
	parent := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--readme", readme,
		"--script", filepath.Join(parent, "quickstart.sh"),
	})
	if err == nil || !strings.Contains(err.Error(), "create script directory") {
		t.Fatalf("error = %v, want a script-directory error", err)
	}
}

func TestQuickstartScriptWriteError(t *testing.T) {
	readme := writeReadme(t)
	dir := t.TempDir()
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--readme", readme,
		"--script", dir,
	})
	if err == nil || !strings.Contains(err.Error(), "write script") {
		t.Fatalf("error = %v, want a script-write error", err)
	}
}
