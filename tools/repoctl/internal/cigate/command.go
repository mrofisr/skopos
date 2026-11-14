package cigate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/changes"
	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl ci-gate`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "ci-gate" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "fail when a CI job failed, was cancelled, or was skipped incorrectly"
}

// Run implements command.Command.
func (Command) Run(_ context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("ci-gate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	needsPath := fs.String("needs", "", "JSON file with the GitHub needs context ('-' for stdin; default NEEDS_JSON)")
	pathsPath := fs.String("paths", ".github/ci-paths.json", "path-group document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	data, err := readNeeds(*needsPath, env)
	if err != nil {
		return err
	}
	var needs Needs
	if err := json.Unmarshal(data, &needs); err != nil {
		return fmt.Errorf("parse needs context: %w", err)
	}
	rules, err := changes.LoadRules(*pathsPath)
	if err != nil {
		return err
	}

	problems := Evaluate(needs, rules.JobNames())
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(env.Stderr, "ci-gate:", problem)
		}
		return fmt.Errorf("%d CI gate problem(s)", len(problems))
	}
	fmt.Fprintf(env.Stdout, "ci-gate: %d job(s) passed the path and result checks\n", len(rules.JobNames()))
	return nil
}

// Needs is the JSON GitHub exposes as the `needs` context.
type Needs map[string]struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

func readNeeds(path string, env command.Env) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	if path != "" {
		return os.ReadFile(path)
	}
	if value := env.Getenv("NEEDS_JSON"); value != "" {
		return []byte(value), nil
	}
	return nil, errors.New("needs context is required: pass --needs or set NEEDS_JSON")
}

// Evaluate applies the gate rule. It returns every problem, sorted, so one run
// reports all broken jobs.
func Evaluate(needs Needs, jobs []string) []string {
	var problems []string
	change, ok := needs["changes"]
	if !ok {
		problems = append(problems, `job "changes" is missing from the needs context`)
	} else if change.Result != "success" {
		problems = append(problems, fmt.Sprintf(`job "changes" result is %q, want "success"`, change.Result))
	}
	outputs := map[string]string{}
	if ok {
		outputs = change.Outputs
	}
	for _, job := range jobs {
		if job == "changes" || job == "ci-gate" {
			continue
		}
		entry, ok := needs[job]
		if !ok {
			problems = append(problems, fmt.Sprintf("job %q is missing from the needs context", job))
			continue
		}
		expected := strings.EqualFold(outputs[job], "true")
		switch entry.Result {
		case "success":
		case "skipped":
			if expected {
				problems = append(problems, fmt.Sprintf("job %q was skipped although its path group is true", job))
			}
		case "failure", "cancelled":
			problems = append(problems, fmt.Sprintf("job %q result is %q", job, entry.Result))
		default:
			problems = append(problems, fmt.Sprintf("job %q has an unknown result %q", job, entry.Result))
		}
	}
	sort.Strings(problems)
	return problems
}
