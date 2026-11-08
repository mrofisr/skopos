package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

const fixtureDir = "../../internal/osvexceptions/testdata/"

func TestRunValidFixture(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"--file", fixtureDir + "valid.yaml", "--now", "2026-11-09"}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "2 exceptions, 0 expired") {
		t.Fatalf("stdout = %q, want a summary", stdout.String())
	}
}

func TestRunEmptyFixture(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"--file", fixtureDir + "empty.yaml", "--now", "2026-11-09"}, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "0 exceptions, 0 expired") {
		t.Fatalf("stdout = %q, want a clean summary", stdout.String())
	}
}

func TestRunExpiredFixtureFails(t *testing.T) {
	err := run([]string{"--file", fixtureDir + "expired.yaml", "--now", "2026-11-09"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded with an expired exception, want an error")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("error = %q, want an expiry error", err)
	}
}

func TestRunMissingFileFails(t *testing.T) {
	err := run([]string{"--file", fixtureDir + "absent.yaml"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded without the exceptions file, want an error")
	}
}

func TestRunRejectsBadNow(t *testing.T) {
	err := run([]string{"--file", fixtureDir + "valid.yaml", "--now", "yesterday"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run accepted an invalid --now, want an error")
	}
}
