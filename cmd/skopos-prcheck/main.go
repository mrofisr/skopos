// Command skopos-prcheck enforces the alpha-exit pull request guard.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/jawaracloud/skopos/internal/issueops"
	"github.com/jawaracloud/skopos/internal/prcheck"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skopos-prcheck:", err)
		os.Exit(1)
	}
}

type event struct {
	PullRequest struct {
		Number int     `json:"number"`
		Title  string  `json:"title"`
		Body   *string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Head struct {
			Repo *repository `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo *repository `json:"repo"`
		} `json:"base"`
	} `json:"pull_request"`
}

type repository struct {
	FullName string `json:"full_name"`
	Fork     bool   `json:"fork"`
}

type githubSource struct {
	client *issueops.GHClient
}

func (s githubSource) Issue(ctx context.Context, number int) (prcheck.Issue, error) {
	issue, err := s.client.GetIssue(ctx, number)
	if err != nil {
		return prcheck.Issue{}, err
	}
	return prcheck.Issue{Number: issue.Number, State: issue.State, Labels: issue.Labels}, nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("skopos-prcheck", flag.ContinueOnError)
	eventPath := fs.String("event", os.Getenv("GITHUB_EVENT_PATH"), "pull_request event JSON")
	filesPath := fs.String("files", "", "newline-separated changed files ('-' for stdin)")
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "owner/repository")
	ghBin := fs.String("gh", "gh", "gh binary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *eventPath == "" {
		return fmt.Errorf("--event or GITHUB_EVENT_PATH is required")
	}
	if *filesPath == "" {
		return fmt.Errorf("--files is required")
	}

	var ev event
	data, err := os.ReadFile(*eventPath)
	if err != nil {
		return fmt.Errorf("read event: %w", err)
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("parse event: %w", err)
	}
	if ev.PullRequest.Number == 0 {
		return fmt.Errorf("event is not a pull_request event")
	}
	files, err := readFiles(*filesPath)
	if err != nil {
		return err
	}

	pr := ev.PullRequest
	body := ""
	if pr.Body != nil {
		body = *pr.Body
	}
	labels := make([]string, 0, len(pr.Labels))
	for _, label := range pr.Labels {
		labels = append(labels, label.Name)
	}
	fork := pr.Head.Repo == nil || pr.Base.Repo == nil ||
		pr.Head.Repo.Fork || pr.Head.Repo.FullName != pr.Base.Repo.FullName
	repoName := *repo
	if repoName == "" && pr.Base.Repo != nil {
		repoName = pr.Base.Repo.FullName
	}
	ctx := context.Background()
	commits := []prcheck.Commit(nil)
	if hasLabel(labels, "type:micro") && !hasLabel(labels, "red-first-exempt") {
		if repoName == "" {
			return fmt.Errorf("--repo or GITHUB_REPOSITORY is required for the red-first check")
		}
		filesRepo := repoName
		if pr.Head.Repo != nil && pr.Head.Repo.FullName != "" {
			filesRepo = pr.Head.Repo.FullName
		}
		commits, err = loadCommits(ctx, *ghBin, repoName, filesRepo, pr.Number)
		if err != nil {
			return fmt.Errorf("load pull request commits: %w", err)
		}
	}

	client := issueops.NewGHClient(repoName)
	client.Bin = *ghBin
	result := prcheck.Evaluate(ctx, prcheck.Input{
		Title:   pr.Title,
		Body:    body,
		Labels:  labels,
		Files:   files,
		Commits: commits,
		Fork:    fork,
	}, githubSource{client: client})

	writeSummary(result, fork, pr.Number)
	for _, notice := range result.Notices {
		fmt.Fprintln(os.Stdout, "pr-guard:", notice)
	}
	for _, err := range result.Errors {
		fmt.Fprintln(os.Stderr, "pr-guard:", err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("%d PR guard problem(s)", len(result.Errors))
	}
	return nil
}

func readFiles(path string) ([]string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read changed files: %w", err)
	}
	return nonEmptyLines(string(data)), nil
}

func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

func loadCommits(ctx context.Context, ghBin, pullRepo, filesRepo string, number int) ([]prcheck.Commit, error) {
	out, err := ghAPI(ctx, ghBin, "--paginate",
		fmt.Sprintf("repos/%s/pulls/%d/commits", pullRepo, number),
		"--jq", ".[].sha")
	if err != nil {
		return nil, err
	}
	shas := strings.Fields(string(out))
	commits := make([]prcheck.Commit, 0, len(shas))
	for _, sha := range shas {
		files, err := ghAPI(ctx, ghBin,
			fmt.Sprintf("repos/%s/commits/%s", filesRepo, sha),
			"--jq", ".files[].filename")
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", sha, err)
		}
		commits = append(commits, prcheck.Commit{SHA: sha, Files: nonEmptyLines(string(files))})
	}
	return commits, nil
}

func ghAPI(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, append([]string{"api"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh api: %s", msg)
	}
	return stdout.Bytes(), nil
}

func writeSummary(result prcheck.Result, fork bool, number int) {
	lines := []string{
		fmt.Sprintf("## PR guard: #%d", number),
		"",
	}
	if fork {
		lines = append(lines, "- Fork PR: issue lookup skipped; freeze check still runs.")
	}
	if len(result.Errors) == 0 {
		lines = append(lines, "- Result: **pass**")
	} else {
		lines = append(lines, "- Result: **fail**")
		for _, err := range result.Errors {
			lines = append(lines, "- "+err.Error())
		}
	}
	text := strings.Join(lines, "\n") + "\n"
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
	}
}
