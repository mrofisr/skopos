package issueops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mrofisr/skopos/tools/repoctl/internal/ghapi"
)

// Client adapts the shared GitHub client to the small surface used by sync and
// lint. Every request goes through ghapi, so pagination, retries, and token
// redaction have one implementation.
type Client struct {
	api *ghapi.Client
}

// NewClient returns a client backed by api.
func NewClient(api *ghapi.Client) *Client {
	return &Client{api: api}
}

// ListLabels returns every repository label.
func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	labels, err := ghapi.GetAll[Label](ctx, c.api, c.api.RepoPath("labels"), url.Values{"per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	return labels, nil
}

// UpsertLabel creates or updates one label. GitHub answers 422 when the label
// already exists, so the create is retried as an update.
func (c *Client) UpsertLabel(ctx context.Context, label Label) error {
	payload := Label{
		Name:        label.Name,
		Color:       trimColor(label.Color),
		Description: label.Description,
	}
	err := c.api.Do(ctx, http.MethodPost, c.api.RepoPath("labels"), nil, payload, nil)
	if err == nil {
		return nil
	}
	var status *ghapi.StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("create label %q: %w", label.Name, err)
	}
	if err := c.api.Do(ctx, http.MethodPatch, c.api.RepoPath("labels/%s", label.Name), nil, payload, nil); err != nil {
		return fmt.Errorf("update label %q: %w", label.Name, err)
	}
	return nil
}

func trimColor(color string) string {
	if len(color) > 0 && color[0] == '#' {
		return color[1:]
	}
	return color
}

// remoteIssue is the subset of the GitHub issue payload this tool reads. The
// pull_request field is present only on issues that are really pull requests.
type remoteIssue struct {
	ID          int64  `json:"id"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"`
	Body        string `json:"body"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r remoteIssue) toRemote() RemoteIssue {
	labels := make([]string, 0, len(r.Labels))
	for _, label := range r.Labels {
		labels = append(labels, label.Name)
	}
	return RemoteIssue{
		ID:     r.ID,
		Number: r.Number,
		Title:  r.Title,
		State:  r.State,
		Body:   r.Body,
		Labels: labels,
	}
}

// ListIssues returns every open and closed issue in the repository. The issues
// endpoint also returns pull requests, so those are filtered out.
func (c *Client) ListIssues(ctx context.Context) ([]RemoteIssue, error) {
	raw, err := ghapi.GetAll[remoteIssue](ctx, c.api, c.api.RepoPath("issues"),
		url.Values{"state": {"all"}, "per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	issues := make([]RemoteIssue, 0, len(raw))
	for _, issue := range raw {
		if issue.PullRequest != nil {
			continue
		}
		issues = append(issues, issue.toRemote())
	}
	return issues, nil
}

// GetIssue returns one issue by number.
func (c *Client) GetIssue(ctx context.Context, number int) (RemoteIssue, error) {
	var issue remoteIssue
	if err := c.api.Get(ctx, c.api.RepoPath("issues/%d", number), nil, &issue); err != nil {
		return RemoteIssue{}, fmt.Errorf("get issue #%d: %w", number, err)
	}
	return issue.toRemote(), nil
}

// CreateIssue creates one issue and returns its number.
func (c *Client) CreateIssue(ctx context.Context, issue Issue, labels []string) (int, error) {
	payload := struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels,omitempty"`
	}{Title: issue.Title, Body: issue.Body, Labels: labels}
	var created struct {
		Number int `json:"number"`
	}
	if err := c.api.Do(ctx, http.MethodPost, c.api.RepoPath("issues"), nil, payload, &created); err != nil {
		return 0, fmt.Errorf("create issue %s: %w", issue.ID, err)
	}
	if created.Number == 0 {
		return 0, fmt.Errorf("create issue %s: response has no number", issue.ID)
	}
	return created.Number, nil
}

// UpdateIssue updates an issue title and body.
func (c *Client) UpdateIssue(ctx context.Context, number int, issue Issue) error {
	payload := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: issue.Title, Body: issue.Body}
	if err := c.api.Do(ctx, http.MethodPatch, c.api.RepoPath("issues/%d", number), nil, payload, nil); err != nil {
		return fmt.Errorf("update issue #%d: %w", number, err)
	}
	return nil
}

// AddLabels adds labels to one issue.
func (c *Client) AddLabels(ctx context.Context, number int, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	payload := struct {
		Labels []string `json:"labels"`
	}{Labels: labels}
	if err := c.api.Do(ctx, http.MethodPost, c.api.RepoPath("issues/%d/labels", number), nil, payload, nil); err != nil {
		return fmt.Errorf("add labels to issue #%d: %w", number, err)
	}
	return nil
}

// RemoveLabels removes labels from one issue.
func (c *Client) RemoveLabels(ctx context.Context, number int, labels []string) error {
	for _, label := range labels {
		if err := c.api.Do(ctx, http.MethodDelete, c.api.RepoPath("issues/%d/labels/%s", number, label), nil, nil, nil); err != nil {
			return fmt.Errorf("remove label %q from issue #%d: %w", label, number, err)
		}
	}
	return nil
}

// ListBlockedBy returns the issues that block one issue.
func (c *Client) ListBlockedBy(ctx context.Context, number int) ([]RemoteIssue, error) {
	raw, err := ghapi.GetAll[remoteIssue](ctx, c.api, c.api.RepoPath("issues/%d/dependencies/blocked_by", number), url.Values{"per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("list blockers of issue #%d: %w", number, err)
	}
	blockers := make([]RemoteIssue, 0, len(raw))
	for _, blocker := range raw {
		blockers = append(blockers, blocker.toRemote())
	}
	return blockers, nil
}

// AddBlockedBy records that blockerID blocks one issue. The id is the
// blocker's database id, not its number.
func (c *Client) AddBlockedBy(ctx context.Context, number int, blockerID int64) error {
	payload := struct {
		IssueID int64 `json:"issue_id"`
	}{IssueID: blockerID}
	if err := c.api.Do(ctx, http.MethodPost, c.api.RepoPath("issues/%d/dependencies/blocked_by", number), nil, payload, nil); err != nil {
		return fmt.Errorf("block issue #%d by %d: %w", number, blockerID, err)
	}
	return nil
}

// RemoveBlockedBy removes the blocker with the given database id from one issue.
func (c *Client) RemoveBlockedBy(ctx context.Context, number int, blockerID int64) error {
	if err := c.api.Do(ctx, http.MethodDelete, c.api.RepoPath("issues/%d/dependencies/blocked_by/%d", number, blockerID), nil, nil, nil); err != nil {
		return fmt.Errorf("unblock issue #%d from %d: %w", number, blockerID, err)
	}
	return nil
}
