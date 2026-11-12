package changes

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Config is .github/ci-paths.json. Groups are path globs; Jobs names the
// groups that make each CI job run. Force paths always run the full matrix.
type Config struct {
	Force  []string            `json:"force"`
	Groups map[string][]string `json:"groups"`
	Jobs   map[string][]string `json:"jobs"`
}

// Rules is a compiled Config.
type Rules struct {
	force  []*regexp.Regexp
	groups map[string][]*regexp.Regexp
	jobs   map[string][]string
	names  []string
}

// LoadRules reads and validates a path-group document.
func LoadRules(path string) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CI paths: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse CI paths: %w", err)
	}
	if len(config.Groups) == 0 {
		return nil, fmt.Errorf("CI paths: no groups defined")
	}
	if len(config.Jobs) == 0 {
		return nil, fmt.Errorf("CI paths: no jobs defined")
	}
	rules := &Rules{
		groups: make(map[string][]*regexp.Regexp, len(config.Groups)),
		jobs:   make(map[string][]string, len(config.Jobs)),
	}
	for _, pattern := range config.Force {
		compiled, err := compileGlob(pattern)
		if err != nil {
			return nil, fmt.Errorf("CI paths: force pattern %q: %w", pattern, err)
		}
		rules.force = append(rules.force, compiled)
	}
	for name, patterns := range config.Groups {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("CI paths: group with an empty name")
		}
		if len(patterns) == 0 {
			return nil, fmt.Errorf("CI paths: group %q has no patterns", name)
		}
		for _, pattern := range patterns {
			compiled, err := compileGlob(pattern)
			if err != nil {
				return nil, fmt.Errorf("CI paths: group %q pattern %q: %w", name, pattern, err)
			}
			rules.groups[name] = append(rules.groups[name], compiled)
		}
	}
	for job, groups := range config.Jobs {
		if strings.TrimSpace(job) == "" {
			return nil, fmt.Errorf("CI paths: job with an empty name")
		}
		if len(groups) == 0 {
			return nil, fmt.Errorf("CI paths: job %q has no groups", job)
		}
		for _, group := range groups {
			if _, ok := rules.groups[group]; !ok {
				return nil, fmt.Errorf("CI paths: job %q refers to unknown group %q", job, group)
			}
		}
		rules.jobs[job] = append([]string(nil), groups...)
		rules.names = append(rules.names, job)
	}
	sort.Strings(rules.names)
	return rules, nil
}

// JobNames returns the configured jobs in sorted order.
func (r *Rules) JobNames() []string { return append([]string(nil), r.names...) }

// Plan returns one run/skip decision per job. An empty file list, a forced
// path, or a path that matches no group runs everything: a new top-level
// directory can never silently skip checks.
func (r *Rules) Plan(files []string) (map[string]bool, string) {
	decisions := make(map[string]bool, len(r.jobs))
	if len(files) == 0 {
		for _, job := range r.names {
			decisions[job] = true
		}
		return decisions, "no changed files"
	}
	triggered := make(map[string]bool, len(r.groups))
	for _, file := range files {
		normalized := normalizePath(file)
		if matchAny(r.force, normalized) {
			for _, job := range r.names {
				decisions[job] = true
			}
			return decisions, "forced by " + normalized
		}
		matched := false
		for name, patterns := range r.groups {
			if matchAny(patterns, normalized) {
				triggered[name] = true
				matched = true
			}
		}
		if !matched {
			for _, job := range r.names {
				decisions[job] = true
			}
			return decisions, "unknown path " + normalized
		}
	}
	for _, job := range r.names {
		for _, group := range r.jobs[job] {
			if triggered[group] {
				decisions[job] = true
				break
			}
		}
	}
	return decisions, ""
}

func normalizePath(path string) string {
	return strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "./")
}

func matchAny(patterns []*regexp.Regexp, path string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

// compileGlob translates the small glob dialect used by ci-paths.json:
// ** crosses directories, * and ? stay within one segment.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var builder strings.Builder
	builder.WriteString("^")
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					builder.WriteString("(?:.*/)?")
				} else {
					builder.WriteString(".*")
				}
			} else {
				builder.WriteString("[^/]*")
			}
		case '?':
			builder.WriteString("[^/]")
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	builder.WriteString("$")
	return regexp.Compile(builder.String())
}

// MatchGroups returns the groups whose patterns match path.
func (r *Rules) MatchGroups(path string) []string {
	normalized := normalizePath(path)
	var matched []string
	for name, patterns := range r.groups {
		if matchAny(patterns, normalized) {
			matched = append(matched, name)
		}
	}
	sort.Strings(matched)
	return matched
}
