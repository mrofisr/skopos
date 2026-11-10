// Package codescanning evaluates GitHub code-scanning alerts for the release
// gate. It only consumes the REST API; it never dismisses or mutates alerts.
package codescanning

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ghapi"
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

// OpenAlerts returns every open code-scanning alert. Any transport, status, or
// decode error is returned so callers fail closed.
func OpenAlerts(ctx context.Context, api *ghapi.Client) ([]Alert, error) {
	alerts, err := ghapi.GetAll[Alert](ctx, api, api.RepoPath("code-scanning/alerts"),
		url.Values{"state": {"open"}, "per_page": {"100"}})
	if err != nil {
		return nil, fmt.Errorf("fetch code-scanning alerts: %w", err)
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
