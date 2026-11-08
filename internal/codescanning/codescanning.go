// Package codescanning evaluates GitHub code-scanning alerts for the release
// gate. It only consumes the REST API; it never dismisses or mutates alerts.
package codescanning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.github.com"
	maxPages       = 100
	maxBodyBytes   = 4 << 20
)

// Rule is the code-scanning rule attached to an alert.
type Rule struct {
	ID                    string `json:"id"`
	Severity              string `json:"severity"`
	SecuritySeverityLevel string `json:"security_severity_level"`
}

// Alert is the subset of a code-scanning alert used by the release gate.
type Alert struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Rule    Rule   `json:"rule"`
}

// Client fetches alerts from the GitHub REST API.
type Client struct {
	BaseURL string
	Repo    string
	Token   string
	HTTP    *http.Client
}

// OpenAlerts returns every open code-scanning alert, following Link
// pagination. Any transport, status, or decode error is returned so callers
// fail closed.
func (c Client) OpenAlerts(ctx context.Context) ([]Alert, error) {
	if strings.TrimSpace(c.Repo) == "" {
		return nil, errors.New("codescanning: repository is required")
	}
	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	current, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("codescanning: parse API URL: %w", err)
	}
	current.Path = strings.TrimRight(current.Path, "/") + "/repos/" + strings.Trim(c.Repo, "/") + "/code-scanning/alerts"
	query := current.Query()
	query.Set("state", "open")
	query.Set("per_page", "100")
	current.RawQuery = query.Encode()

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	alerts := make([]Alert, 0)
	for page := 0; current != nil; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("codescanning: more than %d pages of alerts", maxPages)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("codescanning: build request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("codescanning: fetch alerts: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("codescanning: read alerts: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("codescanning: close response: %w", closeErr)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("codescanning: GitHub API returned %d: %s", resp.StatusCode, bodySummary(body))
		}
		var pageAlerts []Alert
		if err := json.Unmarshal(body, &pageAlerts); err != nil {
			return nil, fmt.Errorf("codescanning: decode alerts: %w", err)
		}
		alerts = append(alerts, pageAlerts...)

		next, err := nextLink(resp.Header.Get("Link"), current)
		if err != nil {
			return nil, err
		}
		if next == "" {
			current = nil
			continue
		}
		current, err = url.Parse(next)
		if err != nil {
			return nil, fmt.Errorf("codescanning: parse next page URL: %w", err)
		}
	}
	return alerts, nil
}

// Evaluate fails when any open alert is high or critical. Closed alerts and
// alerts without a security severity are ignored.
func Evaluate(alerts []Alert) error {
	blocking := make([]Alert, 0)
	for _, alert := range alerts {
		if alert.State != "" && !strings.EqualFold(alert.State, "open") {
			continue
		}
		severity := strings.ToLower(strings.TrimSpace(alert.Rule.SecuritySeverityLevel))
		if severity == "high" || severity == "critical" {
			blocking = append(blocking, alert)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	sort.Slice(blocking, func(i, j int) bool { return blocking[i].Number < blocking[j].Number })
	details := make([]string, 0, len(blocking))
	for _, alert := range blocking {
		rule := alert.Rule.ID
		if rule == "" {
			rule = "unknown rule"
		}
		details = append(details, fmt.Sprintf("#%d %s (%s)",
			alert.Number, rule, strings.ToLower(alert.Rule.SecuritySeverityLevel)))
	}
	return fmt.Errorf("found %d open code-scanning alert(s) at high or critical severity: %s",
		len(blocking), strings.Join(details, "; "))
}

func nextLink(header string, current *url.URL) (string, error) {
	for _, link := range strings.Split(header, ",") {
		parts := strings.Split(link, ";")
		if len(parts) < 2 {
			continue
		}
		target := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		relNext := false
		for _, attribute := range parts[1:] {
			if strings.TrimSpace(attribute) == `rel="next"` {
				relNext = true
			}
		}
		if !relNext {
			continue
		}
		reference, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">"))
		if err != nil {
			return "", fmt.Errorf("codescanning: parse Link header: %w", err)
		}
		return current.ResolveReference(reference).String(), nil
	}
	return "", nil
}

func bodySummary(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}
