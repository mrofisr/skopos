package issueops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGHBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGHClientListLabels(t *testing.T) {
	bin := fakeGHBin(t, `printf '%s' '[{"name":"ec:1","color":"fbca04","description":"one"}]'`)
	c := &GHClient{Bin: bin}
	labels, err := c.ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != 1 || labels[0].Name != "ec:1" || labels[0].Color != "fbca04" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestGHClientListIssues(t *testing.T) {
	bin := fakeGHBin(t, `printf '%s' '[{"number":7,"title":"AX-01: x","state":"open","body":"body","labels":[{"name":"ec:1"},{"name":"phase:alpha-exit"}]}]'`)
	c := &GHClient{Bin: bin}
	issues, err := c.ListIssues(context.Background())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 7 || len(issues[0].Labels) != 2 {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestGHClientGetIssue(t *testing.T) {
	bin := fakeGHBin(t, `printf '%s' '{"number":57,"title":"AX-01: x","state":"open","body":"body","labels":[{"name":"phase:alpha-exit"},{"name":"ec:6"}]}'`)
	c := &GHClient{Bin: bin}
	issue, err := c.GetIssue(context.Background(), 57)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 57 || issue.State != "open" || len(issue.Labels) != 2 || issue.Labels[0] != "phase:alpha-exit" {
		t.Fatalf("issue = %#v", issue)
	}
}

func TestGHClientCreateIssueParsesNumber(t *testing.T) {
	bin := fakeGHBin(t, `printf '%s\n' 'https://github.com/o/r/issues/42'`)
	c := &GHClient{Bin: bin}
	number, err := c.CreateIssue(context.Background(), Issue{Title: "AX-01: x", Body: "body"}, []string{"ec:1"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if number != 42 {
		t.Fatalf("number = %d, want 42", number)
	}
}

func TestGHClientReportsStderr(t *testing.T) {
	bin := fakeGHBin(t, `echo 'not authenticated' >&2; exit 1`)
	c := &GHClient{Bin: bin}
	_, err := c.ListIssues(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("err = %v, want the gh stderr", err)
	}
}

func TestGHClientMissingBinary(t *testing.T) {
	c := &GHClient{Bin: filepath.Join(t.TempDir(), "does-not-exist")}
	_, err := c.ListLabels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v, want a missing binary error", err)
	}
}

func TestGHClientLabelArgs(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	bin := fakeGHBin(t, `printf '%s\n' "$@" > `+log+`
printf '%s\n' 'https://github.com/o/r/issues/1'`)
	c := &GHClient{Bin: bin}
	if err := c.AddLabels(context.Background(), 7, []string{"ec:1", "type:micro"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"issue", "edit", "7", "--add-label", "ec:1", "--add-label", "type:micro"} {
		if !strings.Contains(got, want) {
			t.Errorf("args = %q, want %q", got, want)
		}
	}
}
