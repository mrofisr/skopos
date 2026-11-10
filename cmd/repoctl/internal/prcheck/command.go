package prcheck

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ghapi"
)

// Command implements `repoctl prcheck`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "prcheck" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "enforce alpha-exit traceability and frozen paths on pull requests"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("prcheck", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	eventPath := fs.String("event", "", "pull_request event JSON (default GITHUB_EVENT_PATH)")
	filesPath := fs.String("files", "", "newline-separated changed files ('-' for stdin); fetched from GitHub when empty")
	repo := fs.String("repo", "", "owner/repository (default GITHUB_REPOSITORY)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *eventPath
	if path == "" {
		path = env.Getenv("GITHUB_EVENT_PATH")
	}
	if path == "" {
		return fmt.Errorf("--event or GITHUB_EVENT_PATH is required")
	}
	ev, err := loadEvent(path)
	if err != nil {
		return err
	}
	if ev.PullRequest.Number == 0 {
		return fmt.Errorf("event is not a pull_request event")
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
	if repoName == "" {
		repoName = env.Getenv("GITHUB_REPOSITORY")
	}
	if repoName == "" && pr.Base.Repo != nil {
		repoName = pr.Base.Repo.FullName
	}
	if repoName == "" {
		return fmt.Errorf("--repo or GITHUB_REPOSITORY is required")
	}

	api, err := newClient(env, repoName)
	if err != nil {
		return err
	}
	files, err := changedFiles(ctx, api, *filesPath, pr.Number)
	if err != nil {
		return err
	}

	var commits []Commit
	if hasLabel(labels, microLabel) && !hasLabel(labels, redFirstExemptLabel) {
		filesAPI := api
		if pr.Head.Repo != nil && pr.Head.Repo.FullName != "" && pr.Head.Repo.FullName != repoName {
			filesAPI, err = newClient(env, pr.Head.Repo.FullName)
			if err != nil {
				return err
			}
		}
		commits, err = loadCommits(ctx, api, filesAPI, pr.Number)
		if err != nil {
			return fmt.Errorf("load pull request commits: %w", err)
		}
	}

	result := Evaluate(ctx, Input{
		Title:   pr.Title,
		Body:    body,
		Labels:  labels,
		Files:   files,
		Commits: commits,
		Fork:    fork,
		BaseRef: pr.Base.Ref,
		HeadRef: pr.Head.Ref,
	}, issueSource{api: api})

	writeSummary(env, result, fork, pr.Number)
	for _, notice := range result.Notices {
		fmt.Fprintln(env.Stdout, "pr-guard:", notice)
	}
	for _, err := range result.Errors {
		fmt.Fprintln(env.Stderr, "pr-guard:", err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("%d PR guard problem(s)", len(result.Errors))
	}
	return nil
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
			Ref  string      `json:"ref"`
			Repo *repository `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref  string      `json:"ref"`
			Repo *repository `json:"repo"`
		} `json:"base"`
	} `json:"pull_request"`
}

type repository struct {
	FullName string `json:"full_name"`
	Fork     bool   `json:"fork"`
}

func loadEvent(path string) (event, error) {
	var ev event
	data, err := os.ReadFile(path)
	if err != nil {
		return ev, fmt.Errorf("read event: %w", err)
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, fmt.Errorf("parse event: %w", err)
	}
	return ev, nil
}

type issueSource struct {
	api *ghapi.Client
}

// Issue implements Source.
func (s issueSource) Issue(ctx context.Context, number int) (Issue, error) {
	var payload struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := s.api.Get(ctx, s.api.RepoPath("issues/%d", number), nil, &payload); err != nil {
		return Issue{}, err
	}
	labels := make([]string, 0, len(payload.Labels))
	for _, label := range payload.Labels {
		labels = append(labels, label.Name)
	}
	return Issue{Number: payload.Number, State: payload.State, Labels: labels}, nil
}

type pullFile struct {
	Filename string `json:"filename"`
}

func changedFiles(ctx context.Context, api *ghapi.Client, filesPath string, number int) ([]string, error) {
	if filesPath != "" {
		return readFiles(filesPath)
	}
	files, err := ghapi.GetAll[pullFile](ctx, api, api.RepoPath("pulls/%d/files", number),
		url.Values{"per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list pull request files: %w", err)
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		if file.Filename != "" {
			names = append(names, file.Filename)
		}
	}
	return names, nil
}

type commitRef struct {
	SHA string `json:"sha"`
}

type commitDetail struct {
	Files []struct {
		Filename string `json:"filename"`
	} `json:"files"`
}

func loadCommits(ctx context.Context, pullAPI, filesAPI *ghapi.Client, number int) ([]Commit, error) {
	refs, err := ghapi.GetAll[commitRef](ctx, pullAPI, pullAPI.RepoPath("pulls/%d/commits", number),
		url.Values{"per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list commits: %w", err)
	}
	commits := make([]Commit, 0, len(refs))
	for _, ref := range refs {
		var detail commitDetail
		if err := filesAPI.Get(ctx, filesAPI.RepoPath("commits/%s", ref.SHA), nil, &detail); err != nil {
			return nil, fmt.Errorf("commit %s: %w", ref.SHA, err)
		}
		files := make([]string, 0, len(detail.Files))
		for _, file := range detail.Files {
			files = append(files, file.Filename)
		}
		commits = append(commits, Commit{SHA: ref.SHA, Files: files})
	}
	return commits, nil
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

func newClient(env command.Env, repo string) (*ghapi.Client, error) {
	if env.NewGitHub == nil {
		return nil, fmt.Errorf("GitHub access is not configured")
	}
	return env.NewGitHub(repo)
}

func writeSummary(env command.Env, result Result, fork bool, number int) {
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
	path := env.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(text)
	_ = f.Close()
}
