// Package ruleset validates the checked-in desired state of the repository's
// main branch ruleset against the job display names GitHub reports as required
// status checks.
package ruleset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Config is the document stored in docs/security/ruleset.json. The review
// exception and required workflow list are repository metadata; Ruleset is the
// payload applied to the GitHub ruleset API.
type Config struct {
	ReviewException   ReviewException `json:"review_exception"`
	RequiredWorkflows []string        `json:"required_workflows"`
	// AllowExtraJobs permits workflow jobs that are not individually required
	// checks. It is how ci-gate replaces the per-job requirements without
	// failing every other job in the same workflow.
	AllowExtraJobs bool    `json:"allow_extra_jobs,omitempty"`
	Ruleset        Ruleset `json:"ruleset"`
}

// ReviewException records the accepted solo-maintainer exception. It is not
// part of the GitHub API payload.
type ReviewException struct {
	RequiredApprovingReviewCount int    `json:"required_approving_review_count"`
	Reason                       string `json:"reason"`
	DocumentedAt                 string `json:"documented_at"`
}

// Ruleset is the desired branch ruleset.
type Ruleset struct {
	Name         string        `json:"name"`
	Target       string        `json:"target"`
	Enforcement  string        `json:"enforcement"`
	Conditions   Conditions    `json:"conditions"`
	Rules        []Rule        `json:"rules"`
	BypassActors []BypassActor `json:"bypass_actors"`
}

// Conditions limits the ruleset to the main branch.
type Conditions struct {
	RefName RefName `json:"ref_name"`
}

// RefName is the branch selector.
type RefName struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// Rule is one ruleset rule. Parameters is decoded by rule type.
type Rule struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// BypassActor is an actor allowed to bypass the ruleset.
type BypassActor struct {
	ActorID    int64  `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

// PullRequestParameters is the GitHub API shape for the pull_request rule.
type PullRequestParameters struct {
	RequiredApprovingReviewCount               int                  `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush                  bool                 `json:"dismiss_stale_reviews_on_push"`
	RequiredReviewers                          []json.RawMessage    `json:"required_reviewers"`
	RequireCodeOwnerReview                     bool                 `json:"require_code_owner_review"`
	DismissalRestriction                       DismissalRestriction `json:"dismissal_restriction"`
	RequireLastPushApproval                    bool                 `json:"require_last_push_approval"`
	RequiredReviewThreadResolution             bool                 `json:"required_review_thread_resolution"`
	RequireExtraApprovalForUnattributedChanges bool                 `json:"require_extra_approval_for_unattributed_changes"`
	AllowedMergeMethods                        []string             `json:"allowed_merge_methods"`
}

// DismissalRestriction is the review-dismissal selector.
type DismissalRestriction struct {
	Enabled       bool              `json:"enabled"`
	AllowedActors []json.RawMessage `json:"allowed_actors"`
}

// StatusCheckParameters is the GitHub API shape for required_status_checks.
type StatusCheckParameters struct {
	StrictRequiredStatusChecksPolicy bool          `json:"strict_required_status_checks_policy"`
	DoNotEnforceOnCreate             bool          `json:"do_not_enforce_on_create"`
	RequiredStatusChecks             []StatusCheck `json:"required_status_checks"`
}

// StatusCheck is one required status check. The GitHub Actions integration ID
// is stored so applying the file keeps the checks scoped to Actions.
type StatusCheck struct {
	Context       string `json:"context"`
	IntegrationID int64  `json:"integration_id"`
}

// Parse decodes a ruleset document. Unknown fields are rejected so a typo
// cannot silently drop a guardrail.
func Parse(data []byte) (Config, error) {
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("trailing JSON data")
		}
		return Config{}, err
	}
	return config, nil
}

// Load reads and decodes a ruleset document.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read ruleset: %w", err)
	}
	config, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return config, nil
}

// RequiredChecks returns the required status check contexts in file order.
func (config Config) RequiredChecks() []string {
	for _, rule := range config.Ruleset.Rules {
		if rule.Type != "required_status_checks" {
			continue
		}
		var parameters StatusCheckParameters
		if err := json.Unmarshal(rule.Parameters, &parameters); err != nil {
			return nil
		}
		checks := make([]string, 0, len(parameters.RequiredStatusChecks))
		for _, check := range parameters.RequiredStatusChecks {
			checks = append(checks, check.Context)
		}
		return checks
	}
	return nil
}

// Validate checks the ruleset guardrails and that its required checks are
// exactly the job display names of the required workflows.
func Validate(config Config, workflows map[string][]byte) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if config.Ruleset.Name != "main-protection" {
		add("ruleset.name must be %q", "main-protection")
	}
	if config.Ruleset.Target != "branch" {
		add("ruleset.target must be %q", "branch")
	}
	if config.Ruleset.Enforcement != "active" {
		add("ruleset.enforcement must be %q", "active")
	}
	if !slices.Equal(config.Ruleset.Conditions.RefName.Include, []string{"refs/heads/main"}) {
		add("ruleset.conditions.ref_name.include must be exactly %q", []string{"refs/heads/main"})
	}
	if len(config.Ruleset.Conditions.RefName.Exclude) != 0 {
		add("ruleset.conditions.ref_name.exclude must be empty")
	}
	if len(config.Ruleset.BypassActors) != 0 {
		add("ruleset.bypass_actors must be empty")
	}

	seenedWorkflows := map[string]bool{}
	for _, name := range config.RequiredWorkflows {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			add("required workflow %q must be a file name", name)
			continue
		}
		if seenedWorkflows[name] {
			add("duplicate required workflow %q", name)
		}
		seenedWorkflows[name] = true
	}
	if len(config.RequiredWorkflows) == 0 {
		add("at least one required workflow is required")
	}

	var pullRequest *PullRequestParameters
	var statusChecks *StatusCheckParameters
	rules := map[string]bool{}
	for _, rule := range config.Ruleset.Rules {
		if rules[rule.Type] {
			add("duplicate ruleset rule %q", rule.Type)
			continue
		}
		rules[rule.Type] = true
		switch rule.Type {
		case "deletion", "non_fast_forward", "required_linear_history":
		case "pull_request":
			if len(rule.Parameters) == 0 {
				add("pull_request.parameters is required")
				continue
			}
			var parameters PullRequestParameters
			if err := strictUnmarshal(rule.Parameters, &parameters); err != nil {
				add("decode pull_request parameters: %v", err)
				continue
			}
			pullRequest = &parameters
		case "required_status_checks":
			if len(rule.Parameters) == 0 {
				add("required_status_checks.parameters is required")
				continue
			}
			var parameters StatusCheckParameters
			if err := strictUnmarshal(rule.Parameters, &parameters); err != nil {
				add("decode required_status_checks parameters: %v", err)
				continue
			}
			statusChecks = &parameters
		default:
			add("unknown ruleset rule %q", rule.Type)
		}
	}
	for _, required := range []string{"deletion", "non_fast_forward", "required_linear_history", "pull_request", "required_status_checks"} {
		if !rules[required] {
			add("ruleset is missing required rule %q", required)
		}
	}

	if pullRequest != nil {
		if pullRequest.RequiredApprovingReviewCount != 0 {
			add("pull_request.required_approving_review_count must be 0, got %d", pullRequest.RequiredApprovingReviewCount)
		}
		if len(pullRequest.RequiredReviewers) != 0 {
			add("pull_request.required_reviewers must be empty")
		}
		if pullRequest.DismissalRestriction.Enabled || len(pullRequest.DismissalRestriction.AllowedActors) != 0 {
			add("pull_request.dismissal_restriction must be disabled and empty")
		}
		if len(pullRequest.AllowedMergeMethods) == 0 {
			add("pull_request.allowed_merge_methods is required")
		}
		for _, method := range pullRequest.AllowedMergeMethods {
			switch method {
			case "merge":
				add(`pull_request.allowed_merge_methods must not include "merge"`)
			case "squash", "rebase":
			default:
				add("unknown pull_request merge method %q", method)
			}
		}
	}

	if config.ReviewException.RequiredApprovingReviewCount != 0 {
		add("review exception must document zero required approvals, got %d", config.ReviewException.RequiredApprovingReviewCount)
	}
	if config.ReviewException.Reason == "" {
		add("review_exception.reason is required")
	}
	if config.ReviewException.DocumentedAt == "" {
		add("review_exception.documented_at is required")
	}
	if pullRequest != nil && config.ReviewException.RequiredApprovingReviewCount != pullRequest.RequiredApprovingReviewCount {
		add(
			"review_exception.required_approving_review_count (%d) does not match the pull_request rule (%d)",
			config.ReviewException.RequiredApprovingReviewCount,
			pullRequest.RequiredApprovingReviewCount,
		)
	}

	var requiredChecks []string
	if statusChecks != nil {
		if !statusChecks.StrictRequiredStatusChecksPolicy {
			add("required_status_checks.strict_required_status_checks_policy must be true")
		}
		if statusChecks.DoNotEnforceOnCreate {
			add("required_status_checks.do_not_enforce_on_create must be false")
		}
		for _, check := range statusChecks.RequiredStatusChecks {
			requiredChecks = append(requiredChecks, check.Context)
		}
	}
	if len(requiredChecks) == 0 {
		add("at least one required status check is required")
	}
	checkSeen := map[string]bool{}
	for _, check := range requiredChecks {
		if check == "" {
			add("required status check context must not be empty")
			continue
		}
		if checkSeen[check] {
			add("duplicate required check %q", check)
		}
		checkSeen[check] = true
	}

	jobs := map[string]string{}
	for _, name := range config.RequiredWorkflows {
		data, ok := workflows[name]
		if !ok {
			add("required workflow %q is missing", name)
			continue
		}
		names, err := WorkflowJobNames(data)
		if err != nil {
			add("parse %s: %v", name, err)
			continue
		}
		for _, job := range names {
			if previous, ok := jobs[job]; ok {
				add("duplicate workflow job %q in %s and %s", job, previous, name)
				continue
			}
			jobs[job] = name
		}
	}
	for _, check := range requiredChecks {
		if check == "" {
			continue
		}
		if _, ok := jobs[check]; !ok {
			add("required check %q is not a job in the required workflows", check)
		}
	}
	if !config.AllowExtraJobs {
		for job, workflow := range jobs {
			if !checkSeen[job] {
				add("workflow job %q in %s is not a required check", job, workflow)
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("ruleset check failed:\n  - %s", strings.Join(problems, "\n  - "))
}

func strictUnmarshal(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	return nil
}

// WorkflowJobNames extracts the display name of every job in a workflow. Jobs
// without a name use their key, mirroring the check name GitHub reports. Only
// the simple mapping form used by this repository is accepted; anything else
// fails closed.
func WorkflowJobNames(data []byte) ([]string, error) {
	var names []string
	inJobs := false
	jobIndex := -1
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.ContainsRune(line, '\t') {
			return nil, fmt.Errorf("line %d: tabs are not supported", index+1)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if !inJobs {
			if indent == 0 && trimmed == "jobs:" {
				inJobs = true
			}
			continue
		}
		if indent == 0 {
			break
		}
		switch indent {
		case 2:
			if !strings.HasSuffix(trimmed, ":") {
				return nil, fmt.Errorf("line %d: expected a job key", index+1)
			}
			key := strings.TrimSuffix(trimmed, ":")
			if key == "" || strings.ContainsAny(key, " \"'") {
				return nil, fmt.Errorf("line %d: invalid job key %q", index+1, key)
			}
			names = append(names, key)
			jobIndex = len(names) - 1
		case 4:
			if jobIndex < 0 || !strings.HasPrefix(trimmed, "name:") {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
			name, err := unquoteYAML(value)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", index+1, err)
			}
			names[jobIndex] = name
		}
	}
	if !inJobs {
		return nil, errors.New("workflow has no jobs block")
	}
	if len(names) == 0 {
		return nil, errors.New("workflow jobs block is empty")
	}
	return names, nil
}

func unquoteYAML(value string) (string, error) {
	if value == "" {
		return "", errors.New("name is empty")
	}
	if strings.HasPrefix(value, `"`) {
		if !strings.HasSuffix(value, `"`) {
			return "", fmt.Errorf("malformed quoted name %q", value)
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("malformed quoted name %q", value)
		}
		if unquoted == "" {
			return "", errors.New("name is empty")
		}
		return unquoted, nil
	}
	if strings.HasPrefix(value, "'") {
		if !strings.HasSuffix(value, "'") || len(value) < 2 {
			return "", fmt.Errorf("malformed quoted name %q", value)
		}
		unquoted := strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		if unquoted == "" {
			return "", errors.New("name is empty")
		}
		return unquoted, nil
	}
	if before, _, ok := strings.Cut(value, " #"); ok {
		value = strings.TrimSpace(before)
	}
	if value == "" {
		return "", errors.New("name is empty")
	}
	return value, nil
}
