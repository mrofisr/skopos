package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFixtureWithoutFindings(t *testing.T) {
	path := writeFixture(t, `[]`)
	var stdout bytes.Buffer
	if err := run([]string{"--fixture", path}, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "0 high or critical") {
		t.Fatalf("stdout = %q, want a clean summary", stdout.String())
	}
}

func TestRunFixtureWithHighAlertFails(t *testing.T) {
	path := writeFixture(t, `[{"number":7,"state":"open","rule":{"id":"js/x","security_severity_level":"high"}}]`)
	err := run([]string{"--fixture", path}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded with an open high alert, want an error")
	}
	if !strings.Contains(err.Error(), "#7") {
		t.Fatalf("error = %q, want the alert number", err)
	}
}

func TestRunAPIFailureFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	t.Setenv("GITHUB_TOKEN", "test-token")
	err := run([]string{"--api-url", server.URL, "--repo", "acme/skopos"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded when the API failed, want an error")
	}
}

func TestRunRequiresARepositoryWithoutFixture(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "")
	err := run(nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded without a repository, want an error")
	}
	if !strings.Contains(err.Error(), "repository") {
		t.Fatalf("error = %q, want a repository error", err)
	}
}

func writeFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
