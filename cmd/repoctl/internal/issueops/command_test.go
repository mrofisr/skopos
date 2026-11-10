package issueops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ghapi"
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

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func fixtureManifest() Manifest {
	return Manifest{
		GeneratedFrom: "docs/alpha-exit-plan.md",
		Labels: []Label{
			{Name: "phase:alpha-exit", Color: "5319e7", Description: "alpha exit"},
			{Name: "ec:1", Color: "fbca04", Description: "one"},
			{Name: "type:micro", Color: "0e8a16", Description: "micro"},
		},
		Issues: []Issue{{
			ID:        "AX-01",
			Title:     "AX-01: first",
			Phase:     "phase:alpha-exit",
			EC:        "ec:1",
			Type:      "type:micro",
			BlockedBy: []string{},
			Body:      "Blocked by: none\n\n<!-- skopos-issue: AX-01 -->",
		}},
	}
}

func writeManifest(t *testing.T, m Manifest) string {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "alpha-exit.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func clientEnv(t *testing.T, handler http.Handler) (command.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	env, stdout, stderr := testEnv()
	env.NewGitHub = func(string) (*ghapi.Client, error) {
		return ghapi.New(ghapi.Config{
			BaseURL: server.URL,
			Repo:    "owner/repo",
			Token:   "token",
			HTTP:    server.Client(),
		})
	}
	return env, stdout, stderr
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "issues" {
		t.Fatalf("Name = %q, want issues", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestManifestWriteThenCheck(t *testing.T) {
	root := repoRoot(t)
	plan := filepath.Join(root, "docs", "alpha-exit-plan.md")
	out := filepath.Join(t.TempDir(), "alpha-exit.json")
	env, _, stderr := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"manifest", "--plan", plan, "--out", out, "--write"}); err != nil {
		t.Fatalf("manifest --write: %v", err)
	}
	if !strings.Contains(stderr.String(), "wrote") {
		t.Fatalf("stderr = %q, want a write notice", stderr.String())
	}
	env, _, stderr = testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"manifest", "--plan", plan, "--out", out, "--check"}); err != nil {
		t.Fatalf("manifest --check: %v", err)
	}
	if !strings.Contains(stderr.String(), "is current") {
		t.Fatalf("stderr = %q, want a current notice", stderr.String())
	}
}

func TestManifestCheckFailsWhenStale(t *testing.T) {
	root := repoRoot(t)
	plan := filepath.Join(root, "docs", "alpha-exit-plan.md")
	out := filepath.Join(t.TempDir(), "alpha-exit.json")
	if err := os.WriteFile(out, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"manifest", "--plan", plan, "--out", out, "--check"})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want a stale-manifest error", err)
	}
}

func TestManifestRejectsCheckAndWrite(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"manifest", "--check", "--write"})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("error = %v, want a mutual-exclusion error", err)
	}
}

func TestSyncDryRun(t *testing.T) {
	manifest := writeManifest(t, fixtureManifest())
	env, stdout, _ := clientEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/labels":
			_, _ = io.WriteString(w, `[{"name":"phase:alpha-exit","color":"5319e7","description":"alpha exit"},{"name":"ec:1","color":"fbca04","description":"one"},{"name":"type:micro","color":"0e8a16","description":"micro"}]`)
		case r.URL.Path == "/repos/owner/repo/issues":
			_, _ = io.WriteString(w, `[]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"sync", "--manifest", manifest}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for _, want := range []string{"would create issue AX-01", "not applied"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestSyncApplyCreatesIssue(t *testing.T) {
	manifest := writeManifest(t, fixtureManifest())
	created := false
	env, stdout, _ := clientEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/labels":
			_, _ = io.WriteString(w, `[{"name":"phase:alpha-exit","color":"5319e7","description":"alpha exit"},{"name":"ec:1","color":"fbca04","description":"one"},{"name":"type:micro","color":"0e8a16","description":"micro"}]`)
		case r.URL.Path == "/repos/owner/repo/issues" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `[]`)
		case r.URL.Path == "/repos/owner/repo/issues" && r.Method == http.MethodPost:
			created = true
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"number":9}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"sync", "--manifest", manifest, "--apply"}); err != nil {
		t.Fatalf("sync --apply: %v", err)
	}
	if !created {
		t.Fatal("sync did not create the issue")
	}
	if !strings.Contains(stdout.String(), "applied create issue AX-01") {
		t.Fatalf("stdout = %q, want an applied notice", stdout.String())
	}
}

func TestSyncRequiresGitHub(t *testing.T) {
	manifest := writeManifest(t, fixtureManifest())
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"sync", "--manifest", manifest})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want a GitHub configuration error", err)
	}
}

func TestLintLiveCommand(t *testing.T) {
	manifest := writeManifest(t, fixtureManifest())
	body := "Blocked by: none\n\n<!-- skopos-issue: AX-01 -->"
	env, _, stderr := clientEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"number":9,"title":"AX-01: first","state":"open","body":%q,"labels":[{"name":"phase:alpha-exit"},{"name":"ec:1"},{"name":"type:micro"}]}]`, body)
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"lint", "--manifest", manifest, "--live"}); err != nil {
		t.Fatalf("lint --live: %v", err)
	}
	if !strings.Contains(stderr.String(), "issues-lint: ok") {
		t.Fatalf("stderr = %q, want an ok notice", stderr.String())
	}
}

func TestLintReportsManifestProblems(t *testing.T) {
	m := fixtureManifest()
	m.Issues[0].EC = "ec:9"
	manifest := writeManifest(t, m)
	env, _, stderr := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"lint", "--manifest", manifest})
	if err == nil {
		t.Fatal("lint passed, want an invalid-manifest error")
	}
	if !strings.Contains(stderr.String(), "ec:1..ec:7") {
		t.Fatalf("stderr = %q, want the ec label problem", stderr.String())
	}
}

func TestUnknownSubcommand(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("error = %v, want unknown subcommand", err)
	}
}
