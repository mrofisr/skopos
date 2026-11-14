package changes

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

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
	"github.com/mrofisr/skopos/tools/repoctl/internal/ghapi"
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

func writePaths(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ci-paths.json")
	if err := os.WriteFile(path, []byte(testConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type eventOptions struct {
	Number    int
	BaseRef   string
	Labels    []string
	Repo      string
	NoPR      bool
	Malformed bool
}

func writeEvent(t *testing.T, options eventOptions) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	if options.Malformed {
		if err := os.WriteFile(path, []byte(`{`), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	payload := map[string]any{}
	if !options.NoPR {
		number := options.Number
		if number == 0 {
			number = 12
		}
		baseRef := options.BaseRef
		if baseRef == "" {
			baseRef = "development"
		}
		labels := make([]map[string]string, 0, len(options.Labels))
		for _, label := range options.Labels {
			labels = append(labels, map[string]string{"name": label})
		}
		payload["pull_request"] = map[string]any{
			"number": number,
			"base":   map[string]any{"ref": baseRef},
			"labels": labels,
		}
	}
	if options.Repo != "" {
		payload["repository"] = map[string]any{"full_name": options.Repo}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

func outputFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.txt")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readOutput(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func filesHandler(t *testing.T, pages ...[]string) http.Handler {
	t.Helper()
	page := 0
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls/12/files") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if page >= len(pages) {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		items := make([]map[string]string, 0, len(pages[page]))
		for _, name := range pages[page] {
			items = append(items, map[string]string{"filename": name})
		}
		if page+1 < len(pages) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, "http://"+r.Host, r.URL.Path, page+2))
		}
		page++
		_ = json.NewEncoder(w).Encode(items)
	})
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "changes" {
		t.Fatalf("Name = %q, want changes", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestChangesFetchesFilesAndWritesOutputs(t *testing.T) {
	env, _, stderr := githubEnv(t, filesHandler(t, []string{"internal/prom/engine.go"}))
	eventPath := writeEvent(t, eventOptions{Repo: "owner/repo"})
	output := outputFile(t, "preexisting=value\n")
	summary := filepath.Join(t.TempDir(), "summary.md")
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", eventPath,
		"--paths", writePaths(t),
		"--output", output,
		"--summary", summary,
	})
	if err != nil {
		t.Fatalf("changes: %v (stderr %s)", err, stderr.String())
	}
	values := readOutput(t, output)
	if values["preexisting"] != "value" {
		t.Fatalf("output = %v, want the existing line preserved", values)
	}
	for _, job := range []string{"go", "coverage", "integration", "compat", "cerberus", "policy", "docker"} {
		if values[job] != "true" {
			t.Errorf("%s = %q, want true", job, values[job])
		}
	}
	for _, job := range []string{"helm", "storage"} {
		if values[job] != "false" {
			t.Errorf("%s = %q, want false", job, values[job])
		}
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "| `go` | yes |") {
		t.Fatalf("summary = %q", data)
	}
}

func TestChangesCountsRenames(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"filename":"internal/b.go","previous_filename":"internal/a.go"}]`)
	})
	env, _, _ := githubEnv(t, handler)
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	if readOutput(t, output)["go"] != "true" {
		t.Fatal("rename did not trigger the go job")
	}
}

func TestChangesPaginates(t *testing.T) {
	env, _, _ := githubEnv(t, filesHandler(t,
		[]string{"internal/prom/engine.go"},
		[]string{"deploy/helm/skopos/values.yaml"},
	))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	values := readOutput(t, output)
	for _, job := range []string{"go", "helm", "storage"} {
		if values[job] != "true" {
			t.Errorf("%s = %q, want true", job, values[job])
		}
	}
	if values["cerberus"] != "true" {
		t.Errorf("cerberus = %q, want the go coupling", values["cerberus"])
	}
}

func TestChangesBaseMainRunsEverythingWithoutAPI(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s", r.URL.Path)
	}))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo", BaseRef: "main"}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	assertAllTrue(t, readOutput(t, output))
}

func TestChangesCIFullLabelRunsEverything(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s", r.URL.Path)
	}))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo", Labels: []string{"ci:full"}}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	assertAllTrue(t, readOutput(t, output))
}

func TestChangesNonPullRequestEventRunsEverything(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s", r.URL.Path)
	}))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{NoPR: true}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	assertAllTrue(t, readOutput(t, output))
}

func TestChangesNoFilesRunsEverything(t *testing.T) {
	env, _, _ := githubEnv(t, filesHandler(t, []string{}))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	assertAllTrue(t, readOutput(t, output))
}

func TestChangesFileLimitRunsEverything(t *testing.T) {
	names := make([]string, 0, 3000)
	for i := 0; i < 3000; i++ {
		names = append(names, fmt.Sprintf("internal/file%d.go", i))
	}
	env, _, _ := githubEnv(t, filesHandler(t, names))
	output := outputFile(t, "")
	if err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", output,
	}); err != nil {
		t.Fatal(err)
	}
	assertAllTrue(t, readOutput(t, output))
}

func TestChangesAPIError(t *testing.T) {
	env, _, _ := githubEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", outputFile(t, ""),
	})
	if err == nil || !strings.Contains(err.Error(), "list pull request files") {
		t.Fatalf("error = %v, want a files API error", err)
	}
}

func TestChangesReadsFilesFlag(t *testing.T) {
	env, stdout, _ := testEnv()
	files := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(files, []byte("Dockerfile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Command{}).Run(context.Background(), env, []string{
		"--paths", writePaths(t),
		"--files", files,
	}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "docker=true\n") || !strings.Contains(output, "go=false\n") || !strings.Contains(output, "helm=false\n") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestChangesReadsStdin(t *testing.T) {
	env, stdout, _ := testEnv()
	old := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = old })
	if _, err := writer.WriteString("internal/a.go\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (Command{}).Run(context.Background(), env, []string{
		"--paths", writePaths(t),
		"--files", "-",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "go=true\n") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestChangesRequiresGitHub(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Repo: "owner/repo"}),
		"--paths", writePaths(t),
		"--output", outputFile(t, ""),
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want a GitHub configuration error", err)
	}
}

func TestChangesRequiresRepository(t *testing.T) {
	env, _, _ := testEnv()
	env.NewGitHub = func(string) (*ghapi.Client, error) {
		t.Fatal("NewGitHub called without a repository")
		return nil, nil
	}
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{}),
		"--paths", writePaths(t),
		"--output", outputFile(t, ""),
	})
	if err == nil || !strings.Contains(err.Error(), "GITHUB_REPOSITORY") {
		t.Fatalf("error = %v, want a repository error", err)
	}
}

func TestChangesRequiresEventOrFiles(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--paths", writePaths(t),
		"--output", outputFile(t, ""),
	})
	if err == nil || !strings.Contains(err.Error(), "or --files is required") {
		t.Fatalf("error = %v, want an event error", err)
	}
}

func TestChangesMalformedEvent(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", writeEvent(t, eventOptions{Malformed: true}),
		"--paths", writePaths(t),
	})
	if err == nil || !strings.Contains(err.Error(), "parse event") {
		t.Fatalf("error = %v, want a parse error", err)
	}
}

func TestChangesMissingEventFile(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--event", filepath.Join(t.TempDir(), "missing.json"),
		"--paths", writePaths(t),
	})
	if err == nil || !strings.Contains(err.Error(), "read event") {
		t.Fatalf("error = %v, want a read error", err)
	}
}

func TestChangesMissingPathsFile(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--files", filepath.Join(t.TempDir(), "files.txt"),
		"--paths", filepath.Join(t.TempDir(), "missing.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "read CI paths") {
		t.Fatalf("error = %v, want a paths error", err)
	}
}

func TestChangesRejectsBadFlags(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("changes accepted an unknown flag")
	}
	if err := (Command{}).Run(context.Background(), env, []string{"extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v, want an argument error", err)
	}
}

func TestChangesOutputOpenError(t *testing.T) {
	env, _, _ := testEnv()
	files := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(files, []byte("internal/a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := (Command{}).Run(context.Background(), env, []string{
		"--paths", writePaths(t),
		"--files", files,
		"--output", filepath.Join(t.TempDir(), "missing", "output.txt"),
	})
	if err == nil || !strings.Contains(err.Error(), "write GitHub output") {
		t.Fatalf("error = %v, want an output error", err)
	}
}

func TestChangesSummaryOpenError(t *testing.T) {
	env, _, _ := testEnv()
	files := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(files, []byte("internal/a.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := (Command{}).Run(context.Background(), env, []string{
		"--paths", writePaths(t),
		"--files", files,
		"--output", outputFile(t, ""),
		"--summary", filepath.Join(t.TempDir(), "missing", "summary.md"),
	})
	if err == nil || !strings.Contains(err.Error(), "write step summary") {
		t.Fatalf("error = %v, want a summary error", err)
	}
}

func TestReadFilesMissing(t *testing.T) {
	if _, err := readFiles(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("readFiles succeeded, want an error")
	}
}

func assertAllTrue(t *testing.T, values map[string]string) {
	t.Helper()
	for _, job := range allJobs {
		if values[job] != "true" {
			t.Errorf("%s = %q, want true (all: %v)", job, values[job], values)
		}
	}
}
