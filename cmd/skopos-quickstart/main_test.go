package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

func fakeRunner(calls *[]call, err error) func(context.Context, string, ...string) error {
	return func(_ context.Context, name string, args ...string) error {
		*calls = append(*calls, call{name: name, args: slices.Clone(args)})
		return err
	}
}

func writeREADME(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	return path
}

const validREADME = "```sh quickstart\n" +
	"helm install skopos oci://registry.example/charts/skopos \\\n" +
	"  --version 0.1.0-alpha.2 \\\n" +
	"  -f values.release.yaml\n" +
	"```\n"

func TestRunWritesScriptAndVersion(t *testing.T) {
	readme := writeREADME(t, validREADME)
	dir := t.TempDir()
	script := filepath.Join(dir, "quickstart.sh")
	versionFile := filepath.Join(dir, "version")
	var calls []call
	var stdout, stderr bytes.Buffer
	err := runWithRunner([]string{
		"--readme", readme,
		"--script", script,
		"--version-file", versionFile,
	}, &stdout, &stderr, fakeRunner(&calls, nil))
	if err != nil {
		t.Fatalf("runWithRunner: %v (stderr %s)", err, stderr.String())
	}
	wantCall := call{name: "helm", args: []string{"show", "chart", "oci://registry.example/charts/skopos", "--version", "0.1.0-alpha.2"}}
	if len(calls) != 1 || !slices.Equal(calls[0].args, wantCall.args) || calls[0].name != wantCall.name {
		t.Fatalf("calls = %#v, want %#v", calls, wantCall)
	}
	scriptData, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	for _, want := range []string{"#!/bin/sh", "set -eu", "helm install skopos", "--version 0.1.0-alpha.2"} {
		if !strings.Contains(string(scriptData), want) {
			t.Errorf("script = %q, want %q", scriptData, want)
		}
	}
	versionData, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("read version file: %v", err)
	}
	if string(versionData) != "0.1.0-alpha.2\n" {
		t.Fatalf("version file = %q", versionData)
	}
	if !strings.Contains(stdout.String(), "oci://registry.example/charts/skopos") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunFailsWhenChartVersionIsMissing(t *testing.T) {
	readme := writeREADME(t, validREADME)
	var calls []call
	err := runWithRunner([]string{
		"--readme", readme,
		"--script", filepath.Join(t.TempDir(), "quickstart.sh"),
	}, io.Discard, io.Discard, fakeRunner(&calls, errors.New("manifest unknown")))
	if err == nil {
		t.Fatal("runWithRunner = nil, want a registry error")
	}
	for _, want := range []string{"0.1.0-alpha.2", "manifest unknown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestRunSkipsRegistryCheck(t *testing.T) {
	readme := writeREADME(t, validREADME)
	var calls []call
	err := runWithRunner([]string{
		"--readme", readme,
		"--check=false",
		"--script", filepath.Join(t.TempDir(), "quickstart.sh"),
	}, io.Discard, io.Discard, fakeRunner(&calls, errors.New("must not be called")))
	if err != nil {
		t.Fatalf("runWithRunner: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %#v, want none", calls)
	}
}

func TestRunRejectsMalformedVersion(t *testing.T) {
	readme := writeREADME(t, strings.ReplaceAll(validREADME, "0.1.0-alpha.2", "v0.1.0-alpha.2"))
	var calls []call
	err := runWithRunner([]string{"--readme", readme}, io.Discard, io.Discard, fakeRunner(&calls, nil))
	if err == nil || !strings.Contains(err.Error(), "invalid chart version") {
		t.Fatalf("runWithRunner error = %v, want an invalid-version error", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %#v, want none", calls)
	}
}

func TestRunRejectsMissingReadme(t *testing.T) {
	err := runWithRunner([]string{"--readme", filepath.Join(t.TempDir(), "absent.md")}, io.Discard, io.Discard, fakeRunner(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "read README") {
		t.Fatalf("runWithRunner error = %v, want a read error", err)
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	err := runWithRunner([]string{"unexpected"}, io.Discard, io.Discard, fakeRunner(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("runWithRunner error = %v, want an argument error", err)
	}
}

func TestRunAcceptsRealReadme(t *testing.T) {
	var calls []call
	var stdout bytes.Buffer
	err := runWithRunner([]string{
		"--readme", "../../README.md",
		"--script", filepath.Join(t.TempDir(), "quickstart.sh"),
	}, &stdout, io.Discard, fakeRunner(&calls, nil))
	if err != nil {
		t.Fatalf("runWithRunner: %v", err)
	}
	if len(calls) != 1 || calls[0].args[2] != "oci://ghcr.io/jawaracloud/charts/skopos" || calls[0].args[4] != "0.1.0-alpha.2" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestRunFailsWhenScriptCannotBeWritten(t *testing.T) {
	readme := writeREADME(t, validREADME)
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	var calls []call
	err := runWithRunner([]string{
		"--readme", readme,
		"--check=false",
		"--script", filepath.Join(parent, "quickstart.sh"),
	}, io.Discard, io.Discard, fakeRunner(&calls, nil))
	if err == nil || !strings.Contains(err.Error(), "create script directory") {
		t.Fatalf("runWithRunner error = %v, want a script-directory error", err)
	}
}

func TestRunFailsWhenVersionFileCannotBeWritten(t *testing.T) {
	readme := writeREADME(t, validREADME)
	var calls []call
	err := runWithRunner([]string{
		"--readme", readme,
		"--check=false",
		"--version-file", filepath.Join(t.TempDir(), "missing", "version"),
	}, io.Discard, io.Discard, fakeRunner(&calls, nil))
	if err == nil || !strings.Contains(err.Error(), "write version file") {
		t.Fatalf("runWithRunner error = %v, want a version-file error", err)
	}
}
