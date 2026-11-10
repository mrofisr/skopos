package prcheck

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

type eventFixture struct {
	Number  int
	Title   string
	Body    string
	Labels  []string
	Fork    bool
	BaseRef string
	HeadRef string
}

func writeEvent(t *testing.T, fixture eventFixture) string {
	t.Helper()
	labels := make([]map[string]string, 0, len(fixture.Labels))
	for _, label := range fixture.Labels {
		labels = append(labels, map[string]string{"name": label})
	}
	baseRef := fixture.BaseRef
	if baseRef == "" {
		baseRef = "development"
	}
	headRef := fixture.HeadRef
	if headRef == "" {
		headRef = "feature/x"
	}
	headRepo := map[string]any{"full_name": "owner/repo", "fork": fixture.Fork}
	baseRepo := map[string]any{"full_name": "owner/repo", "fork": false}
	payload := map[string]any{
		"pull_request": map[string]any{
			"number": fixture.Number,
			"title":  fixture.Title,
			"body":   fixture.Body,
			"labels": labels,
			"head":   map[string]any{"ref": headRef, "repo": headRepo},
			"base":   map[string]any{"ref": baseRef, "repo": baseRepo},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

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

func githubEnv(t *testing.T, handler http.Handler) (command.Env, *bytes.Buffer, *bytes.Buffer) {
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
			Sleep:   func(context.Context, time.Duration) error { return nil },
		})
	}
	return env, stdout, stderr
}

func issueHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/issues/57":
			_, _ = io.WriteString(w, `{"number":57,"state":"open","labels":[{"name":"phase:alpha-exit"}]}`)
		case strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = io.WriteString(w, `[{"filename":"docs/guide.md"}]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func writeFiles(t *testing.T, files ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(path, []byte(strings.Join(files, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "prcheck" {
		t.Fatalf("Name = %q, want prcheck", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestPrcheckPassesForTraceablePR(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57"})
	files := writeFiles(t, "internal/prom/engine.go")
	summary := filepath.Join(t.TempDir(), "summary.md")
	env, stdout, stderr := githubEnv(t, issueHandler(t))
	env.Getenv = func(key string) string {
		if key == "GITHUB_STEP_SUMMARY" {
			return summary
		}
		return ""
	}
	if err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files}); err != nil {
		t.Fatalf("prcheck: %v (stderr %s)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PR guard: #12", "**pass**"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("summary = %q, want %q", data, want)
		}
	}
}

func TestPrcheckFailsWithoutReference(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "no reference"})
	files := writeFiles(t, "docs/guide.md")
	env, _, stderr := githubEnv(t, issueHandler(t))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(err.Error(), "PR guard problem") {
		t.Fatalf("error = %v, want a guard failure", err)
	}
	if !strings.Contains(stderr.String(), "no Closes #N") {
		t.Fatalf("stderr = %q, want the traceability error", stderr.String())
	}
}

func TestPrcheckFetchesFilesFromGitHub(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57"})
	env, _, stderr := githubEnv(t, issueHandler(t))
	if err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath}); err != nil {
		t.Fatalf("prcheck: %v (stderr %s)", err, stderr.String())
	}
}

func TestPrcheckMicroPRLoadsCommits(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57", Labels: []string{"type:micro"}})
	files := writeFiles(t, "internal/prom/engine.go")
	env, _, stderr := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/issues/57":
			_, _ = io.WriteString(w, `{"number":57,"state":"open","labels":[{"name":"phase:alpha-exit"}]}`)
		case r.URL.Path == "/repos/owner/repo/pulls/12/commits":
			_, _ = io.WriteString(w, `[{"sha":"aaaa"},{"sha":"bbbb"}]`)
		case r.URL.Path == "/repos/owner/repo/commits/aaaa":
			_, _ = io.WriteString(w, `{"files":[{"filename":"internal/prom/engine_test.go"}]}`)
		case r.URL.Path == "/repos/owner/repo/commits/bbbb":
			_, _ = io.WriteString(w, `{"files":[{"filename":"internal/prom/engine.go"}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files}); err != nil {
		t.Fatalf("prcheck: %v (stderr %s)", err, stderr.String())
	}
}

func TestPrcheckMicroPRFailsWhenFirstCommitIsNotTests(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57", Labels: []string{"type:micro"}})
	files := writeFiles(t, "internal/prom/engine.go")
	env, _, stderr := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/issues/57":
			_, _ = io.WriteString(w, `{"number":57,"state":"open","labels":[{"name":"phase:alpha-exit"}]}`)
		case r.URL.Path == "/repos/owner/repo/pulls/12/commits":
			_, _ = io.WriteString(w, `[{"sha":"aaaa"},{"sha":"bbbb"}]`)
		case r.URL.Path == "/repos/owner/repo/commits/aaaa":
			_, _ = io.WriteString(w, `{"files":[{"filename":"internal/prom/engine.go"}]}`)
		case r.URL.Path == "/repos/owner/repo/commits/bbbb":
			_, _ = io.WriteString(w, `{"files":[{"filename":"internal/prom/engine_test.go"}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(stderr.String(), "non-test files") {
		t.Fatalf("error = %v (stderr %s), want the red-first failure", err, stderr.String())
	}
}

func TestPrcheckForkSkipsIssueLookup(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57", Fork: true})
	files := writeFiles(t, "docs/guide.md")
	env, stdout, stderr := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files}); err != nil {
		t.Fatalf("prcheck: %v (stderr %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fork PR") {
		t.Fatalf("stdout = %q, want a fork notice", stdout.String())
	}
}

func TestPrcheckFrozenPathFails(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57"})
	files := writeFiles(t, "internal/semctx/api.go")
	env, _, stderr := githubEnv(t, issueHandler(t))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(stderr.String(), "frozen path") {
		t.Fatalf("error = %v (stderr %s), want the frozen-path failure", err, stderr.String())
	}
}

func TestPrcheckRequiresEvent(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, nil)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_EVENT_PATH") {
		t.Fatalf("error = %v, want a missing-event error", err)
	}
}

func TestPrcheckRequiresGitHub(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57"})
	files := writeFiles(t, "docs/guide.md")
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want a GitHub configuration error", err)
	}
}

func TestPrcheckRejectsNonPullRequestEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{"ref":"refs/heads/development"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--event", path})
	if err == nil || !strings.Contains(err.Error(), "pull_request") {
		t.Fatalf("error = %v, want a pull_request error", err)
	}
}

func TestNonEmptyLines(t *testing.T) {
	if got := nonEmptyLines(" a.go\n\n b.go \n"); strings.Join(got, ",") != "a.go,b.go" {
		t.Fatalf("nonEmptyLines = %v", got)
	}
}

func TestLoadEventMissingFile(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--event", filepath.Join(t.TempDir(), "missing.json")})
	if err == nil || !strings.Contains(err.Error(), "read event") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestPrcheckSummaryFailsOnGuardErrors(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "no reference"})
	files := writeFiles(t, "docs/guide.md")
	summary := filepath.Join(t.TempDir(), "summary.md")
	env, _, _ := githubEnv(t, issueHandler(t))
	env.Getenv = func(key string) string {
		if key == "GITHUB_STEP_SUMMARY" {
			return summary
		}
		return ""
	}
	_ = (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "**fail**") {
		t.Fatalf("summary = %q, want a fail result", data)
	}
}

func TestChangedFilesUsesProvidedList(t *testing.T) {
	path := writeFiles(t, "a.go", "b.go")
	files, err := changedFiles(context.Background(), nil, path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.go,b.go" {
		t.Fatalf("files = %v", files)
	}
}

func TestPrcheckRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("Run accepted an unknown flag")
	}
}

func TestPrcheckRequiresRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	data := `{"pull_request":{"number":12,"title":"x","body":"Closes #57","labels":[],"head":{"repo":null},"base":{"repo":null}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--event", path})
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("error = %v, want a repository error", err)
	}
}

func TestPrcheckFilesAPIError(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57"})
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath})
	if err == nil || !strings.Contains(err.Error(), "list pull request files") {
		t.Fatalf("error = %v, want a files API error", err)
	}
}

func TestLoadEventInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEvent(path); err == nil || !strings.Contains(err.Error(), "parse event") {
		t.Fatalf("error = %v, want a parse error", err)
	}
}

func TestIssueSourceError(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	api, err := env.NewGitHub("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (issueSource{api: api}).Issue(context.Background(), 99); err == nil {
		t.Fatal("Issue succeeded, want an error")
	}
}

func TestChangedFilesSkipsEmptyNames(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"filename":""},{"filename":"a.go"}]`)
	}))
	api, err := env.NewGitHub("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	files, err := changedFiles(context.Background(), api, "", 12)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.go" {
		t.Fatalf("files = %v", files)
	}
}

func TestChangedFilesAPIError(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	api, err := env.NewGitHub("owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changedFiles(context.Background(), api, "", 12); err == nil {
		t.Fatal("changedFiles succeeded, want an error")
	}
}

func TestLoadCommitsErrors(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		api, err := env.NewGitHub("owner/repo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadCommits(context.Background(), api, api, 12); err == nil {
			t.Fatal("loadCommits succeeded, want an error")
		}
	})
	t.Run("detail", func(t *testing.T) {
		env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/commits") {
				_, _ = io.WriteString(w, `[{"sha":"aaaa"}]`)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		api, err := env.NewGitHub("owner/repo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadCommits(context.Background(), api, api, 12); err == nil || !strings.Contains(err.Error(), "commit aaaa") {
			t.Fatalf("error = %v, want a commit detail error", err)
		}
	})
}

func TestReadFilesFromStdin(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	if _, err := w.WriteString("a.go\nb.go\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := readFiles("-")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.go,b.go" {
		t.Fatalf("files = %v", files)
	}
}

func TestReadFilesMissing(t *testing.T) {
	if _, err := readFiles(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("readFiles succeeded, want an error")
	}
}

func TestWriteSummaryOpenError(t *testing.T) {
	env, _, _ := testEnv()
	env.Getenv = func(key string) string {
		if key == "GITHUB_STEP_SUMMARY" {
			return filepath.Join(t.TempDir(), "missing", "summary.md")
		}
		return ""
	}
	writeSummary(env, Result{}, false, 1)
}

func TestPrcheckHeadRepoClientError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.json")
	data := `{"pull_request":{"number":12,"title":"feat: x","body":"Closes #57","labels":[{"name":"type:micro"}],"head":{"repo":{"full_name":"other/repo","fork":true}},"base":{"repo":{"full_name":"owner/repo"}}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	files := writeFiles(t, "docs/guide.md")
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	base := env.NewGitHub
	env.NewGitHub = func(repo string) (*ghapi.Client, error) {
		if repo == "other/repo" {
			return nil, fmt.Errorf("no client for %s", repo)
		}
		return base(repo)
	}
	err := (Command{}).Run(context.Background(), env, []string{"--event", path, "--files", files})
	if err == nil || !strings.Contains(err.Error(), "no client for other/repo") {
		t.Fatalf("error = %v, want the head-repo client error", err)
	}
}

func TestPrcheckCommitLoadError(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{Number: 12, Title: "feat: x", Body: "Closes #57", Labels: []string{"type:micro"}})
	files := writeFiles(t, "docs/guide.md")
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(err.Error(), "load pull request commits") {
		t.Fatalf("error = %v, want a commit load error", err)
	}
}

func TestPrcheckPromotionSkipsTraceability(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{
		Number: 12, Title: "release: promote development", Body: "no reference",
		BaseRef: "main", HeadRef: "development",
	})
	files := writeFiles(t, "internal/checkpoint/checkpoint.go")
	env, stdout, stderr := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	if err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files}); err != nil {
		t.Fatalf("prcheck: %v (stderr %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "promotion PR") {
		t.Fatalf("stdout = %q, want a promotion notice", stdout.String())
	}
}

func TestPrcheckOtherPRIntoMainIsChecked(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{
		Number: 12, Title: "feat: x", Body: "no reference",
		BaseRef: "main", HeadRef: "feature/x",
	})
	files := writeFiles(t, "docs/guide.md")
	env, _, stderr := githubEnv(t, issueHandler(t))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(stderr.String(), "no Closes #N") {
		t.Fatalf("error = %v (stderr %s), want the traceability failure", err, stderr.String())
	}
}

func TestPrcheckDevelopmentPRIsUnchanged(t *testing.T) {
	eventPath := writeEvent(t, eventFixture{
		Number: 12, Title: "feat: x", Body: "no reference",
		BaseRef: "development", HeadRef: "feature/x",
	})
	files := writeFiles(t, "docs/guide.md")
	env, _, stderr := githubEnv(t, issueHandler(t))
	err := (Command{}).Run(context.Background(), env, []string{"--event", eventPath, "--files", files})
	if err == nil || !strings.Contains(stderr.String(), "no Closes #N") {
		t.Fatalf("error = %v (stderr %s), want the traceability failure", err, stderr.String())
	}
}
