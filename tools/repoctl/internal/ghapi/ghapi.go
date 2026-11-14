// Package ghapi is the one GitHub REST client used by repoctl.
//
// It centralises token handling, Link pagination, Retry-After retries, and
// typed status errors so every repository check fails the same way. It never
// logs or returns the token.
package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public GitHub API.
	DefaultBaseURL = "https://api.github.com"
	// DefaultMaxRetries is the number of retries after the first attempt.
	DefaultMaxRetries = 3

	maxBodyBytes = 4 << 20
	maxPages     = 100
)

// ErrNoToken is returned when the client is built without a token.
var ErrNoToken = errors.New("ghapi: a GitHub token is required (set GITHUB_TOKEN or GH_TOKEN)")

// Config configures a Client.
type Config struct {
	BaseURL    string
	Repo       string
	Token      string
	HTTP       *http.Client
	Sleep      func(context.Context, time.Duration) error
	MaxRetries int
}

// Client is a GitHub REST client for one repository.
type Client struct {
	baseURL    *url.URL
	repo       string
	token      string
	http       *http.Client
	sleep      func(context.Context, time.Duration) error
	maxRetries int
}

// StatusError is a non-2xx GitHub response. The body is summarised and the
// token is redacted.
type StatusError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

// Error implements error.
func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("ghapi: %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("ghapi: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether the response was a 404.
func (e *StatusError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// New validates the configuration and returns a client. A token and a
// repository are required; both are checked here so a missing value fails
// before any request is made.
func New(cfg Config) (*Client, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, ErrNoToken
	}
	repo, err := normalizeRepo(cfg.Repo)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("ghapi: parse base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("ghapi: base URL scheme must be http or https, got %q", baseURL.Scheme)
	}
	if baseURL.Host == "" {
		return nil, fmt.Errorf("ghapi: base URL has no host: %q", base)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")

	httpClient := cfg.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	retries := cfg.MaxRetries
	if retries < 0 {
		return nil, fmt.Errorf("ghapi: MaxRetries must be zero or positive, got %d", retries)
	}
	if retries == 0 {
		retries = DefaultMaxRetries
	}
	return &Client{
		baseURL:    baseURL,
		repo:       repo,
		token:      token,
		http:       httpClient,
		sleep:      sleep,
		maxRetries: retries,
	}, nil
}

func normalizeRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("ghapi: repository must be owner/name, got %q", repo)
	}
	for _, part := range parts {
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '-' || r == '_' || r == '.' {
				continue
			}
			return "", fmt.Errorf("ghapi: repository %q contains an unsupported character %q", repo, r)
		}
	}
	return repo, nil
}

// RepoPath formats a repository-scoped API path.
func (c *Client) RepoPath(format string, args ...any) string {
	return "/repos/" + c.repo + "/" + fmt.Sprintf(format, args...)
}

// Get fetches one path and decodes the JSON response into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, query, nil, out)
}

// Do performs one request and decodes the JSON response into out. A nil out
// discards the body.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	target, err := c.resolve(path, query)
	if err != nil {
		return err
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("ghapi: encode request: %w", err)
		}
	}
	response, _, err := c.request(ctx, method, target.String(), payload)
	if err != nil {
		return err
	}
	if out == nil || len(response) == 0 {
		return nil
	}
	if err := json.Unmarshal(response, out); err != nil {
		return fmt.Errorf("ghapi: decode %s: %w", target.Path, err)
	}
	return nil
}

// GetAll fetches every page of a JSON array and appends the results.
func GetAll[T any](ctx context.Context, client *Client, path string, query url.Values) ([]T, error) {
	current, err := client.resolve(path, query)
	if err != nil {
		return nil, err
	}
	var out []T
	for page := 0; current != nil; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("ghapi: more than %d pages", maxPages)
		}
		body, header, err := client.request(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("ghapi: decode %s: %w", current.Path, err)
		}
		out = append(out, items...)
		next, err := nextLink(header.Get("Link"), current)
		if err != nil {
			return nil, err
		}
		current = next
	}
	return out, nil
}

func (c *Client) resolve(path string, query url.Values) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("ghapi: path must start with one slash, got %q", path)
	}
	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	target.RawQuery = query.Encode()
	return &target, nil
}

func (c *Client) request(ctx context.Context, method, rawURL string, payload []byte) ([]byte, http.Header, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(payload))
		if err != nil {
			return nil, nil, fmt.Errorf("ghapi: build request: %w", err)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		request.Header.Set("Authorization", "Bearer "+c.token)
		if payload != nil {
			request.Header.Set("Content-Type", "application/json")
		}

		response, err := c.http.Do(request)
		if err != nil {
			return nil, nil, fmt.Errorf("ghapi: %s %s: %w", method, c.redact(rawURL), err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, nil, fmt.Errorf("ghapi: read response: %w", readErr)
		}
		if closeErr != nil {
			return nil, nil, fmt.Errorf("ghapi: close response: %w", closeErr)
		}

		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return body, response.Header, nil
		}
		lastErr = &StatusError{
			StatusCode: response.StatusCode,
			Method:     method,
			Path:       c.redact(rawURL),
			Body:       c.redact(bodySummary(body)),
		}
		delay, retry := retryDelay(response, attempt)
		if !retry || attempt == c.maxRetries {
			return nil, nil, lastErr
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, nil, fmt.Errorf("ghapi: wait to retry: %w", err)
		}
	}
	return nil, nil, lastErr
}

func (c *Client) redact(text string) string {
	return strings.ReplaceAll(text, c.token, "[redacted]")
}

func retryDelay(response *http.Response, attempt int) (time.Duration, bool) {
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		delay, ok := parseRetryAfter(response.Header.Get("Retry-After"))
		return delay, ok
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return time.Duration(1<<attempt) * time.Second, true
	default:
		return 0, false
	}
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func bodySummary(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}

// nextLink parses the Link header. A malformed header fails loudly rather than
// silently truncating pagination.
func nextLink(header string, current *url.URL) (*url.URL, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, nil
	}
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(part, ";")
		target := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			return nil, fmt.Errorf("ghapi: malformed Link header segment %q", strings.TrimSpace(part))
		}
		reference := strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
		relNext := false
		for _, attribute := range segments[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(attribute), "=")
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(key), "rel") &&
				strings.Trim(strings.TrimSpace(value), `"`) == "next" {
				relNext = true
			}
		}
		if !relNext {
			continue
		}
		parsed, err := url.Parse(reference)
		if err != nil {
			return nil, fmt.Errorf("ghapi: parse Link header: %w", err)
		}
		return current.ResolveReference(parsed), nil
	}
	return nil, nil
}
