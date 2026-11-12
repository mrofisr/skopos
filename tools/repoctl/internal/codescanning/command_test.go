package codescanning

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/tools/repoctl/internal/command"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/ghapi"
)

func testEnv() (command.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(0, 0) },
	}, stdout, stderr
}

func fixture(t *testing.T, alerts []Alert) string {
	t.Helper()
	data, err := json.Marshal(alerts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "codescan" {
		t.Fatalf("Name = %q, want codescan", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestCodescanPassesWithCleanFixture(t *testing.T) {
	path := fixture(t, []Alert{
		{Number: 1, State: "open", Rule: Rule{ID: "js/a", SecuritySeverityLevel: "low"}},
		{Number: 2, State: "closed", Rule: Rule{ID: "js/b", SecuritySeverityLevel: "high"}},
	})
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--fixture", path}); err != nil {
		t.Fatalf("codescan: %v", err)
	}
	if !strings.Contains(stdout.String(), "codescan: 2 open alerts") {
		t.Fatalf("stdout = %q, want the alert count", stdout.String())
	}
}

func TestCodescanFailsOnHighFixture(t *testing.T) {
	path := fixture(t, []Alert{
		{Number: 7, State: "open", Rule: Rule{ID: "js/prototype-pollution", SecuritySeverityLevel: "high"}},
	})
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--fixture", path})
	if err == nil || !strings.Contains(err.Error(), "#7") {
		t.Fatalf("error = %v, want the blocking alert", err)
	}
}

func TestCodescanReadsTheAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/code-scanning/alerts" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"number":1,"state":"open","rule":{"id":"js/a","security_severity_level":"low"}}]`)
	}))
	t.Cleanup(server.Close)
	env, stdout, _ := testEnv()
	env.NewGitHub = func(string) (*ghapi.Client, error) {
		return ghapi.New(ghapi.Config{
			BaseURL: server.URL,
			Repo:    "owner/repo",
			Token:   "token",
			HTTP:    server.Client(),
		})
	}
	if err := (Command{}).Run(context.Background(), env, []string{"--repo", "owner/repo"}); err != nil {
		t.Fatalf("codescan: %v", err)
	}
	if !strings.Contains(stdout.String(), "1 open alerts") {
		t.Fatalf("stdout = %q, want the alert count", stdout.String())
	}
}

func TestCodescanUsesRepositoryFromEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(server.Close)
	env, _, _ := testEnv()
	env.Getenv = func(key string) string {
		if key == "GITHUB_REPOSITORY" {
			return "owner/repo"
		}
		return ""
	}
	env.NewGitHub = func(repo string) (*ghapi.Client, error) {
		if repo != "owner/repo" {
			t.Errorf("repo = %q, want owner/repo", repo)
		}
		return ghapi.New(ghapi.Config{BaseURL: server.URL, Repo: repo, Token: "token", HTTP: server.Client()})
	}
	if err := (Command{}).Run(context.Background(), env, nil); err != nil {
		t.Fatalf("codescan: %v", err)
	}
}

func TestCodescanRequiresRepository(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, nil)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("error = %v, want a repository error", err)
	}
}

func TestCodescanRequiresGitHubClient(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--repo", "owner/repo"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want a GitHub configuration error", err)
	}
}

func TestCodescanMissingFixture(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--fixture", filepath.Join(t.TempDir(), "missing.json")})
	if err == nil || !strings.Contains(err.Error(), "read fixture") {
		t.Fatalf("error = %v, want a fixture read error", err)
	}
}

func TestCodescanInvalidFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--fixture", path})
	if err == nil || !strings.Contains(err.Error(), "decode fixture") {
		t.Fatalf("error = %v, want a fixture decode error", err)
	}
}

func TestCodescanAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	env, _, _ := testEnv()
	env.NewGitHub = func(string) (*ghapi.Client, error) {
		return ghapi.New(ghapi.Config{
			BaseURL: server.URL,
			Repo:    "owner/repo",
			Token:   "token",
			HTTP:    server.Client(),
			Sleep:   func(context.Context, time.Duration) error { return nil },
		})
	}
	err := (Command{}).Run(context.Background(), env, []string{"--repo", "owner/repo"})
	if err == nil || !strings.Contains(err.Error(), "fetch code-scanning alerts") {
		t.Fatalf("error = %v, want an API error", err)
	}
}

func TestCodescanIgnoresScorecardPostureAlerts(t *testing.T) {
	path := fixture(t, []Alert{
		{Number: 2, State: "open", Tool: Tool{Name: "Scorecard"}, Rule: Rule{ID: "VulnerabilitiesID", SecuritySeverityLevel: "high"}},
		{Number: 3, State: "open", Tool: Tool{Name: "Scorecard"}, Rule: Rule{ID: "CodeReviewID", SecuritySeverityLevel: "high"}},
	})
	env, stdout, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--fixture", path}); err != nil {
		t.Fatalf("codescan: %v", err)
	}
	if !strings.Contains(stdout.String(), "2 ignored scanners") {
		t.Fatalf("stdout = %q, want the ignored count", stdout.String())
	}
}

func TestCodescanStillFailsOnCodeQLHigh(t *testing.T) {
	path := fixture(t, []Alert{
		{Number: 9, State: "open", Tool: Tool{Name: "CodeQL"}, Rule: Rule{ID: "go/sql-injection", SecuritySeverityLevel: "high"}},
	})
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--fixture", path})
	if err == nil || !strings.Contains(err.Error(), "#9") {
		t.Fatalf("error = %v, want the CodeQL alert", err)
	}
}

func TestCodescanIgnoreToolCanBeDisabled(t *testing.T) {
	path := fixture(t, []Alert{
		{Number: 2, State: "open", Tool: Tool{Name: "Scorecard"}, Rule: Rule{ID: "VulnerabilitiesID", SecuritySeverityLevel: "high"}},
	})
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--fixture", path, "--ignore-tool", ""})
	if err == nil || !strings.Contains(err.Error(), "#2") {
		t.Fatalf("error = %v, want the Scorecard alert when the filter is off", err)
	}
}
