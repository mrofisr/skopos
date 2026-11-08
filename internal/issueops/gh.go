package issueops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// GHClient talks to GitHub through the gh CLI so authentication stays in one
// place and the tool needs no token handling of its own.
type GHClient struct {
	Repo string
	Bin  string
}

// NewGHClient returns a client for one repository.
func NewGHClient(repo string) *GHClient {
	return &GHClient{Repo: repo, Bin: "gh"}
}

func (c *GHClient) args(args ...string) []string {
	if c.Repo == "" {
		return args
	}
	return append(args, "--repo", c.Repo)
}

func (c *GHClient) run(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	bin := c.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, c.args(args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// ListLabels returns every repository label.
func (c *GHClient) ListLabels(ctx context.Context) ([]Label, error) {
	out, err := c.run(ctx, "", "label", "list", "--limit", "1000", "--json", "name,color,description")
	if err != nil {
		return nil, err
	}
	var labels []Label
	if err := json.Unmarshal(out, &labels); err != nil {
		return nil, fmt.Errorf("parse gh label list: %w", err)
	}
	return labels, nil
}

// UpsertLabel creates or updates one label.
func (c *GHClient) UpsertLabel(ctx context.Context, label Label) error {
	_, err := c.run(ctx, "", "label", "create", label.Name,
		"--color", strings.TrimPrefix(label.Color, "#"),
		"--description", label.Description,
		"--force")
	return err
}

type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListIssues returns every open and closed issue in the repository.
func (c *GHClient) ListIssues(ctx context.Context) ([]RemoteIssue, error) {
	out, err := c.run(ctx, "", "issue", "list", "--state", "all", "--limit", "1000",
		"--json", "number,title,state,body,labels")
	if err != nil {
		return nil, err
	}
	var raw []ghIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh issue list: %w", err)
	}
	issues := make([]RemoteIssue, 0, len(raw))
	for _, issue := range raw {
		labels := make([]string, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			labels = append(labels, label.Name)
		}
		issues = append(issues, RemoteIssue{
			Number: issue.Number,
			Title:  issue.Title,
			State:  issue.State,
			Body:   issue.Body,
			Labels: labels,
		})
	}
	return issues, nil
}

func (c *GHClient) bodyFile(body string) (string, error) {
	f, err := os.CreateTemp("", "skopos-issue-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// CreateIssue creates one issue and returns its number.
func (c *GHClient) CreateIssue(ctx context.Context, issue Issue, labels []string) (int, error) {
	path, err := c.bodyFile(issue.Body)
	if err != nil {
		return 0, err
	}
	defer os.Remove(path)
	args := []string{"issue", "create", "--title", issue.Title, "--body-file", path}
	for _, label := range labels {
		args = append(args, "--label", label)
	}
	out, err := c.run(ctx, "", args...)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return 0, fmt.Errorf("gh issue create returned no URL")
	}
	number, err := strconv.Atoi(fields[len(fields)-1][strings.LastIndex(fields[len(fields)-1], "/")+1:])
	if err != nil {
		return 0, fmt.Errorf("parse created issue number from %q: %w", strings.TrimSpace(string(out)), err)
	}
	return number, nil
}

// UpdateIssue updates an issue title and body.
func (c *GHClient) UpdateIssue(ctx context.Context, number int, issue Issue) error {
	path, err := c.bodyFile(issue.Body)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	_, err = c.run(ctx, "", "issue", "edit", strconv.Itoa(number),
		"--title", issue.Title, "--body-file", path)
	return err
}

// AddLabels adds labels to one issue.
func (c *GHClient) AddLabels(ctx context.Context, number int, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, label := range labels {
		args = append(args, "--add-label", label)
	}
	_, err := c.run(ctx, "", args...)
	return err
}

// RemoveLabels removes labels from one issue.
func (c *GHClient) RemoveLabels(ctx context.Context, number int, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	args := []string{"issue", "edit", strconv.Itoa(number)}
	for _, label := range labels {
		args = append(args, "--remove-label", label)
	}
	_, err := c.run(ctx, "", args...)
	return err
}
