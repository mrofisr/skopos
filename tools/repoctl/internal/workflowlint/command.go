// Package workflowlint checks the repository workflows against the trigger
// policy in docs/ci-contract.md. It reads the small YAML subset the repository
// uses instead of adding a YAML dependency.
package workflowlint

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl workflow-lint`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "workflow-lint" }

// Summary implements command.Command.
func (Command) Summary() string { return "enforce the workflow trigger and timeout policy" }

// Run implements command.Command.
func (Command) Run(_ context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("workflow-lint", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dir := fs.String("dir", ".github/workflows", "workflow directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		return fmt.Errorf("read workflows: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("no workflows under %s", *dir)
	}

	problems := 0
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			return fmt.Errorf("read workflow %q: %w", name, err)
		}
		report, err := Analyze(data)
		if err != nil {
			fmt.Fprintf(env.Stderr, "%s: parse: %v\n", name, err)
			problems++
			continue
		}
		for _, problem := range Problems(report) {
			fmt.Fprintf(env.Stderr, "%s: %s: %s\n", name, problem.Rule, problem.Message)
			problems++
		}
	}
	if problems > 0 {
		return fmt.Errorf("%d workflow problem(s)", problems)
	}
	fmt.Fprintf(env.Stdout, "workflow-lint: %d workflows follow the trigger policy\n", len(names))
	return nil
}

// Report is the policy-relevant shape of one workflow.
type Report struct {
	Name                string
	Triggers            []string
	PullRequestBranches bool
	PathFilters         []string
	HasConcurrency      bool
	HasPermissions      bool
	Schedule            bool
	Dispatch            bool
	Jobs                []string
	JobTitles           map[string]string
	MissingTimeouts     []string
	PushHasFilter       bool
}

// Problem is one policy violation.
type Problem struct {
	Rule    string
	Message string
}

// Analyze parses the subset of YAML needed by the policy rules.
func Analyze(data []byte) (Report, error) {
	report := Report{JobTitles: make(map[string]string)}
	section := ""
	trigger := ""
	inJobs := false
	job := ""
	jobHasTimeout := false
	seenOn := false
	seenJobs := false

	finishJob := func() {
		if job != "" && !jobHasTimeout {
			report.MissingTimeouts = append(report.MissingTimeouts, job)
		}
		jobHasTimeout = false
	}

	lines := strings.Split(string(data), "\n")
	for index, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if strings.ContainsRune(line, '\t') {
			return report, fmt.Errorf("line %d: tabs are not supported", index+1)
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			finishJob()
			if inJobs {
				inJobs = false
			}
			key, value, ok := cutKey(trimmed)
			if !ok {
				return report, fmt.Errorf("line %d: expected a top-level key", index+1)
			}
			section = key
			switch key {
			case "on", "true":
				seenOn = true
			case "jobs":
				seenJobs = true
				inJobs = true
			case "concurrency":
				report.HasConcurrency = true
			case "permissions":
				report.HasPermissions = true
			case "name":
				report.Name = strings.TrimSpace(value)
			}
			continue
		}

		if section == "on" || (seenOn && section == "on") {
			handleTriggerLine(&report, trimmed, indent, &trigger)
			continue
		}
		if inJobs {
			switch indent {
			case 2:
				finishJob()
				if !strings.HasSuffix(trimmed, ":") {
					return report, fmt.Errorf("line %d: expected a job key", index+1)
				}
				job = strings.TrimSuffix(trimmed, ":")
				if job == "" {
					return report, fmt.Errorf("line %d: empty job key", index+1)
				}
				report.Jobs = append(report.Jobs, job)
				jobHasTimeout = false
			case 4:
				if strings.HasPrefix(trimmed, "timeout-minutes:") {
					jobHasTimeout = true
				}
				if strings.HasPrefix(trimmed, "name:") {
					report.JobTitles[job] = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
				}
			}
		}
	}
	finishJob()
	if !seenOn {
		return report, fmt.Errorf("workflow has no on block")
	}
	if !seenJobs {
		return report, fmt.Errorf("workflow has no jobs block")
	}
	return report, nil
}

func cutKey(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

func handleTriggerLine(report *Report, trimmed string, indent int, trigger *string) {
	if indent == 2 {
		key, value, ok := cutKey(trimmed)
		if !ok {
			return
		}
		*trigger = key
		report.Triggers = append(report.Triggers, key)
		switch key {
		case "schedule":
			report.Schedule = true
		case "workflow_dispatch":
			report.Dispatch = true
		case "push":
			if strings.Contains(value, "branches") || strings.Contains(value, "tags") {
				report.PushHasFilter = true
			}
		case "pull_request":
			if strings.Contains(value, "branches") {
				report.PullRequestBranches = true
			}
		}
		return
	}
	if indent < 4 {
		return
	}
	switch {
	case strings.HasPrefix(trimmed, "branches:"):
		if *trigger == "pull_request" {
			report.PullRequestBranches = true
		}
		if *trigger == "push" {
			report.PushHasFilter = true
		}
	case strings.HasPrefix(trimmed, "tags:"):
		if *trigger == "push" {
			report.PushHasFilter = true
		}
	case strings.HasPrefix(trimmed, "paths:"), strings.HasPrefix(trimmed, "paths-ignore:"):
		report.PathFilters = append(report.PathFilters, trimmed)
	}
}

// Problems applies the policy from docs/ci-contract.md.
func Problems(report Report) []Problem {
	var problems []Problem
	has := func(name string) bool {
		for _, trigger := range report.Triggers {
			if trigger == name {
				return true
			}
		}
		return false
	}
	if has("push") && !report.PushHasFilter {
		problems = append(problems, Problem{"bare-push", "push must declare branches or tags"})
	}
	if has("pull_request") && !report.PullRequestBranches {
		problems = append(problems, Problem{"pull-request-branches", "pull_request must declare branches"})
	}
	if has("pull_request") && !report.HasConcurrency {
		problems = append(problems, Problem{"pull-request-concurrency", "a pull_request workflow must declare concurrency"})
	}
	if len(report.PathFilters) > 0 {
		problems = append(problems, Problem{"no-path-filters", "required workflows must not use paths or paths-ignore filters"})
	}
	if report.Schedule && !report.Dispatch {
		problems = append(problems, Problem{"schedule-dispatch", "a scheduled workflow must also declare workflow_dispatch"})
	}
	for _, job := range report.MissingTimeouts {
		problems = append(problems, Problem{"job-timeout", fmt.Sprintf("job %q has no timeout-minutes", job)})
	}
	if !report.HasPermissions {
		problems = append(problems, Problem{"top-level-permissions", "the workflow must declare top-level permissions"})
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Rule != problems[j].Rule {
			return problems[i].Rule < problems[j].Rule
		}
		return problems[i].Message < problems[j].Message
	})
	return problems
}
