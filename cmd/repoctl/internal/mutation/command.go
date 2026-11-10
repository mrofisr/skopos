// Package mutation parses mutation-testing reports and reports efficacy.
package mutation

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

// Command implements `repoctl mutation`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "mutation" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "parse a mutation report and report efficacy"
}

type report struct {
	Files []struct {
		Mutations []struct {
			Status string `json:"status"`
		} `json:"mutations"`
	} `json:"files"`
}

// Counts holds mutation outcomes.
type Counts struct {
	Killed     int
	Lived      int
	TimedOut   int
	NotCovered int
	NotViable  int
	Skipped    int
}

// Efficacy is killed over killed plus lived, as a percentage.
func (c Counts) Efficacy() (float64, error) {
	total := c.Killed + c.Lived
	if total == 0 {
		return 0, fmt.Errorf("mutation report has no killed or lived mutants")
	}
	return 100 * float64(c.Killed) / float64(total), nil
}

// Parse reads a gremlins JSON report.
func Parse(data []byte) (Counts, error) {
	var decoded report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Counts{}, fmt.Errorf("decode mutation report: %w", err)
	}
	var counts Counts
	for _, file := range decoded.Files {
		for _, mutation := range file.Mutations {
			switch strings.ToUpper(strings.TrimSpace(mutation.Status)) {
			case "KILLED":
				counts.Killed++
			case "LIVED":
				counts.Lived++
			case "TIMED OUT", "TIMEDOUT":
				counts.TimedOut++
			case "NOT COVERED", "NOTCOVERED":
				counts.NotCovered++
			case "NOT VIABLE", "NOTVIABLE":
				counts.NotViable++
			case "SKIPPED":
				counts.Skipped++
			default:
				return Counts{}, fmt.Errorf("unknown mutation status %q", mutation.Status)
			}
		}
	}
	return counts, nil
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	if len(args) == 0 || args[0] != "report" {
		return fmt.Errorf("usage: repoctl mutation report --input mutation.json")
	}
	fs := flag.NewFlagSet("mutation report", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	input := fs.String("input", "mutation.json", "gremlins JSON report")
	threshold := fs.Float64("threshold-efficacy", 0, "fail when efficacy is below this percentage")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *threshold < 0 || *threshold > 100 {
		return fmt.Errorf("threshold-efficacy must be between 0 and 100")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read mutation report %s: %w", *input, err)
	}
	counts, err := Parse(data)
	if err != nil {
		return err
	}
	efficacy, err := counts.Efficacy()
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout,
		"mutation: killed=%d lived=%d timed_out=%d not_covered=%d not_viable=%d skipped=%d efficacy=%.1f%%\n",
		counts.Killed, counts.Lived, counts.TimedOut, counts.NotCovered, counts.NotViable, counts.Skipped, efficacy)
	if *threshold > 0 && efficacy < *threshold {
		return fmt.Errorf("mutation efficacy %.1f%% is below the %.1f%% threshold", efficacy, *threshold)
	}
	return nil
}
