// Package exitcheck aggregates the per-criterion alpha-exit evidence into one
// report and fails closed when a criterion is missing, stale, failing, or from
// a different commit.
package exitcheck

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Criterion is one alpha-exit criterion.
type Criterion struct {
	Number int
	Title  string
}

// Criteria is the fixed EC list. The numbers match docs/alpha-exit-plan.md.
var Criteria = []Criterion{
	{1, "Grafana plug-and-play"},
	{2, "Compatibility"},
	{3, "Concurrency safety"},
	{4, "Self-observability"},
	{5, "Supply chain"},
	{6, "Strict gates"},
	{7, "Scope discipline"},
}

// Evidence is one ec-N.json file.
type Evidence struct {
	EC        int       `json:"ec"`
	Status    string    `json:"status"`
	Commit    string    `json:"commit"`
	Timestamp time.Time `json:"timestamp"`
	Command   string    `json:"command"`
}

// Result is one evaluated criterion.
type Result struct {
	Criterion
	Status  string
	Command string
	Commit  string
	At      time.Time
	Problem string
}

// Command implements `repoctl exit-check`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "exit-check" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "aggregate alpha-exit evidence and fail closed"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("exit-check", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	dir := fs.String("dir", "test/quality/evidence", "evidence directory")
	commit := fs.String("commit", env.Getenv("GITHUB_SHA"), "expected commit")
	maxAge := fs.Duration("max-age", 24*time.Hour, "maximum evidence age")
	reportPath := fs.String("report", "docs/alpha-exit-report.md", "generated report")
	check := fs.Bool("check", false, "validate only; do not write the report")
	skipRaw := fs.String("skip", "", "comma-separated criteria to omit, for staged checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *maxAge <= 0 {
		return fmt.Errorf("max-age must be positive")
	}
	skip, err := parseSkip(*skipRaw)
	if err != nil {
		return err
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	results, err := EvaluateSkip(*dir, *commit, *maxAge, now, skip)
	if err != nil {
		return err
	}
	if !*check {
		if err := WriteReport(*reportPath, results, now); err != nil {
			return err
		}
	}
	failures := 0
	for _, result := range results {
		state := "pass"
		if result.Problem != "" {
			state = "fail"
			failures++
		}
		fmt.Fprintf(env.Stdout, "exit-check: EC%d %s: %s", result.Number, result.Title, state)
		if result.Problem != "" {
			fmt.Fprintf(env.Stdout, " (%s)", result.Problem)
		}
		fmt.Fprintln(env.Stdout)
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d criteria failed", failures, len(results))
	}
	fmt.Fprintf(env.Stdout, "exit-check: %d/%d criteria pass\n", len(results), len(results))
	return nil
}

// Evaluate reads and checks every criterion in dir.
func Evaluate(dir, commit string, maxAge time.Duration, now time.Time) ([]Result, error) {
	return EvaluateSkip(dir, commit, maxAge, now, nil)
}

// EvaluateSkip reads and checks every criterion except the skipped numbers.
func EvaluateSkip(dir, commit string, maxAge time.Duration, now time.Time, skip map[int]bool) ([]Result, error) {
	results := make([]Result, 0, len(Criteria))
	for _, criterion := range Criteria {
		if skip[criterion.Number] {
			continue
		}
		result := Result{Criterion: criterion}
		path := filepath.Join(dir, fmt.Sprintf("ec-%d.json", criterion.Number))
		data, err := os.ReadFile(path)
		if err != nil {
			result.Problem = "missing evidence file " + path
			results = append(results, result)
			continue
		}
		var evidence Evidence
		if err := json.Unmarshal(data, &evidence); err != nil {
			result.Problem = "malformed evidence: " + err.Error()
			results = append(results, result)
			continue
		}
		result.Status = evidence.Status
		result.Command = evidence.Command
		result.Commit = evidence.Commit
		result.At = evidence.Timestamp
		if evidence.EC != 0 && evidence.EC != criterion.Number {
			result.Problem = fmt.Sprintf("evidence says EC%d", evidence.EC)
		} else if strings.ToLower(evidence.Status) != "pass" {
			result.Problem = "status " + evidence.Status
		} else if strings.TrimSpace(evidence.Command) == "" {
			result.Problem = "command is empty"
		} else if commit != "" && evidence.Commit != commit {
			result.Problem = "commit " + evidence.Commit + " is not " + commit
		} else if evidence.Timestamp.IsZero() {
			result.Problem = "timestamp is missing"
		} else if now.Sub(evidence.Timestamp) > maxAge {
			result.Problem = "evidence is older than " + maxAge.String()
		}
		results = append(results, result)
	}
	return results, nil
}

func parseSkip(raw string) (map[int]bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	skip := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		number, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("invalid --skip %q: %w", part, err)
		}
		found := false
		for _, criterion := range Criteria {
			if criterion.Number == number {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid --skip %q: no such criterion", part)
		}
		skip[number] = true
	}
	return skip, nil
}

// WriteReport writes the generated Markdown report.
func WriteReport(path string, results []Result, now time.Time) error {
	var b strings.Builder
	b.WriteString("# Alpha exit report\n\n")
	fmt.Fprintf(&b, "Generated at %s.\n\n", now.UTC().Format(time.RFC3339))
	b.WriteString("| EC | Criterion | Status | Command | Commit | Evidence time |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, result := range results {
		state := "pass"
		if result.Problem != "" {
			state = "fail: " + result.Problem
		}
		fmt.Fprintf(&b, "| EC%d | %s | %s | %s | %s | %s |\n",
			result.Number, result.Title, state, cell(result.Command), cell(result.Commit),
			result.At.UTC().Format(time.RFC3339))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func cell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
