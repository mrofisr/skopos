package changes

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
	"github.com/mrofisr/skopos/tools/repoctl/internal/ghapi"
)

// Command implements `repoctl changes`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "changes" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "map a pull request's changed paths to CI job groups"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	eventPath := fs.String("event", "", "pull_request event JSON (default GITHUB_EVENT_PATH)")
	repo := fs.String("repo", "", "owner/repository (default GITHUB_REPOSITORY)")
	pathsPath := fs.String("paths", ".github/ci-paths.json", "path-group document")
	filesPath := fs.String("files", "", "newline-separated changed files ('-' for stdin); fetched from GitHub when empty")
	outputPath := fs.String("output", "", "GitHub output file (default GITHUB_OUTPUT)")
	summaryPath := fs.String("summary", "", "step summary file (default GITHUB_STEP_SUMMARY)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	rules, err := LoadRules(*pathsPath)
	if err != nil {
		return err
	}

	resolvedEvent := *eventPath
	if resolvedEvent == "" {
		resolvedEvent = env.Getenv("GITHUB_EVENT_PATH")
	}
	if resolvedEvent == "" && *filesPath == "" {
		return fmt.Errorf("--event, GITHUB_EVENT_PATH, or --files is required")
	}
	ev, err := loadEvent(resolvedEvent)
	if err != nil {
		return err
	}
	reason := ""
	switch {
	case ev.PullRequest != nil && ev.PullRequest.Base.Ref == "main":
		reason = "base is main"
	case ev.PullRequest != nil && hasLabel(ev.PullRequest.Labels, "ci:full"):
		reason = "label ci:full"
	case ev.PullRequest == nil && *filesPath == "":
		reason = "not a pull_request event"
	}

	var files []string
	switch {
	case *filesPath != "":
		files, err = readFiles(*filesPath)
		if err != nil {
			return err
		}
	default:
		if reason != "" {
			// No API call is needed: every job already runs.
			break
		}
		if ev.PullRequest == nil {
			return fmt.Errorf("pull_request event is required to list changed files")
		}
		repoName := *repo
		if repoName == "" {
			repoName = env.Getenv("GITHUB_REPOSITORY")
		}
		if repoName == "" && ev.Repository != nil {
			repoName = ev.Repository.FullName
		}
		if repoName == "" {
			return fmt.Errorf("--repo or GITHUB_REPOSITORY is required")
		}
		api, err := newClient(env, repoName)
		if err != nil {
			return err
		}
		files, err = changedFiles(ctx, api, ev.PullRequest.Number)
		if err != nil {
			return err
		}
		if len(files) >= 3000 {
			reason = "GitHub file limit reached"
		}
	}

	decisions := make(map[string]bool, len(rules.JobNames()))
	planReason := ""
	if reason != "" {
		for _, job := range rules.JobNames() {
			decisions[job] = true
		}
		planReason = reason
	} else {
		decisions, planReason = rules.Plan(files)
	}

	return writeResult(env, rules, decisions, planReason, *outputPath, *summaryPath)
}

type event struct {
	PullRequest *struct {
		Number int `json:"number"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func loadEvent(path string) (event, error) {
	var ev event
	if path == "" {
		return ev, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ev, fmt.Errorf("read event: %w", err)
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, fmt.Errorf("parse event: %w", err)
	}
	return ev, nil
}

func hasLabel(labels []struct {
	Name string `json:"name"`
}, want string) bool {
	for _, label := range labels {
		if label.Name == want {
			return true
		}
	}
	return false
}

type pullFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
}

func changedFiles(ctx context.Context, api *ghapi.Client, number int) ([]string, error) {
	raw, err := ghapi.GetAll[pullFile](ctx, api, api.RepoPath("pulls/%d/files", number),
		url.Values{"per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list pull request files: %w", err)
	}
	files := make([]string, 0, len(raw)*2)
	for _, file := range raw {
		if file.Filename != "" {
			files = append(files, file.Filename)
		}
		if file.PreviousFilename != "" && file.PreviousFilename != file.Filename {
			files = append(files, file.PreviousFilename)
		}
	}
	return files, nil
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
	lines := strings.Split(string(data), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if name := strings.TrimSpace(line); name != "" {
			files = append(files, name)
		}
	}
	return files, nil
}

func newClient(env command.Env, repo string) (*ghapi.Client, error) {
	if env.NewGitHub == nil {
		return nil, fmt.Errorf("GitHub access is not configured")
	}
	return env.NewGitHub(repo)
}

func writeResult(env command.Env, rules *Rules, decisions map[string]bool, reason, outputPath, summaryPath string) error {
	if outputPath == "" {
		outputPath = env.Getenv("GITHUB_OUTPUT")
	}
	if summaryPath == "" {
		summaryPath = env.Getenv("GITHUB_STEP_SUMMARY")
	}

	var output strings.Builder
	names := rules.JobNames()
	sort.Strings(names)
	for _, job := range names {
		fmt.Fprintf(&output, "%s=%t\n", job, decisions[job])
	}
	if outputPath != "" {
		file, err := os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("write GitHub output: %w", err)
		}
		if _, err := file.WriteString(output.String()); err != nil {
			_ = file.Close()
			return fmt.Errorf("write GitHub output: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("write GitHub output: %w", err)
		}
	} else {
		if _, err := io.WriteString(env.Stdout, output.String()); err != nil {
			return err
		}
	}

	if summaryPath != "" {
		var summary strings.Builder
		summary.WriteString("## CI path filter\n\n")
		if reason != "" {
			fmt.Fprintf(&summary, "Full matrix: %s.\n\n", reason)
		}
		summary.WriteString("| Job | Run |\n| --- | --- |\n")
		for _, job := range names {
			status := "no"
			if decisions[job] {
				status = "yes"
			}
			fmt.Fprintf(&summary, "| `%s` | %s |\n", job, status)
		}
		file, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("write step summary: %w", err)
		}
		if _, err := file.WriteString(summary.String()); err != nil {
			_ = file.Close()
			return fmt.Errorf("write step summary: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("write step summary: %w", err)
		}
	}
	return nil
}
