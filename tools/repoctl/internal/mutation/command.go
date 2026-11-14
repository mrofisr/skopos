// Package mutation parses mutation-testing reports and reports efficacy.
package mutation

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl mutation`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "mutation" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "parse a mutation report and report efficacy"
}

type reportFile struct {
	FileName  string `json:"file_name"`
	Mutations []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"mutations"`
}

type report struct {
	Files []reportFile `json:"files"`
}

// Survivor is one LIVED mutant.
type Survivor struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Type   string `json:"type"`
}

// Report is the parsed mutation report: counts plus the surviving mutants.
type Report struct {
	Counts    Counts
	Survivors []Survivor
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

// Parse reads a gremlins JSON report and returns only the counts.
func Parse(data []byte) (Counts, error) {
	parsed, err := ParseReport(data)
	return parsed.Counts, err
}

// ParseReport reads a gremlins JSON report.
func ParseReport(data []byte) (Report, error) {
	var decoded report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Report{}, fmt.Errorf("decode mutation report: %w", err)
	}
	var parsed Report
	for _, file := range decoded.Files {
		for _, mutation := range file.Mutations {
			switch strings.ToUpper(strings.TrimSpace(mutation.Status)) {
			case "KILLED":
				parsed.Counts.Killed++
			case "LIVED":
				parsed.Counts.Lived++
				parsed.Survivors = append(parsed.Survivors, Survivor{
					File: file.FileName, Line: mutation.Line, Column: mutation.Column, Type: mutation.Type,
				})
			case "TIMED OUT", "TIMEDOUT":
				parsed.Counts.TimedOut++
			case "NOT COVERED", "NOTCOVERED":
				parsed.Counts.NotCovered++
			case "NOT VIABLE", "NOTVIABLE":
				parsed.Counts.NotViable++
			case "SKIPPED":
				parsed.Counts.Skipped++
			default:
				return Report{}, fmt.Errorf("unknown mutation status %q", mutation.Status)
			}
		}
	}
	return parsed, nil
}

// AllowlistEntry accepts one surviving mutant with a stated reason.
type AllowlistEntry struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// Allowlist is the checked-in survivor allowlist.
type Allowlist struct {
	Survivors []AllowlistEntry `json:"survivors"`
}

// Gate fails when efficacy is below floor and a survivor is not allowlisted
// with a reason.
func Gate(parsed Report, floor float64, allowlist Allowlist) error {
	for _, entry := range allowlist.Survivors {
		if strings.TrimSpace(entry.Reason) == "" {
			return fmt.Errorf("allowlist entry %s:%d %s has no reason", entry.File, entry.Line, entry.Type)
		}
	}
	efficacy, err := parsed.Counts.Efficacy()
	if err != nil {
		return err
	}
	if efficacy >= floor {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowlist.Survivors))
	for _, entry := range allowlist.Survivors {
		allowed[fmt.Sprintf("%s\x00%d\x00%s", entry.File, entry.Line, entry.Type)] = struct{}{}
	}
	var blocked []string
	for _, survivor := range parsed.Survivors {
		key := fmt.Sprintf("%s\x00%d\x00%s", survivor.File, survivor.Line, survivor.Type)
		if _, ok := allowed[key]; ok {
			continue
		}
		blocked = append(blocked, fmt.Sprintf("%s:%d %s", survivor.File, survivor.Line, survivor.Type))
	}
	if len(blocked) > 0 {
		return fmt.Errorf("mutation efficacy %.1f%% is below the %.1f%% floor; unallowlisted survivors: %s",
			efficacy, floor, strings.Join(blocked, ", "))
	}
	return nil
}

// LoadAllowlist reads an allowlist file.
func LoadAllowlist(path string) (Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Allowlist{}, fmt.Errorf("read mutation allowlist %s: %w", path, err)
	}
	var allowlist Allowlist
	if err := json.Unmarshal(data, &allowlist); err != nil {
		return Allowlist{}, fmt.Errorf("decode mutation allowlist: %w", err)
	}
	return allowlist, nil
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: repoctl mutation report|gate [flags]")
	}
	switch args[0] {
	case "report":
		return runReport(env, args[1:])
	case "gate":
		return runGate(env, args[1:])
	default:
		return fmt.Errorf("unknown mutation subcommand %q (want report or gate)", args[0])
	}
}

func runReport(env command.Env, args []string) error {
	fs := flag.NewFlagSet("mutation report", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	input := fs.String("input", "mutation.json", "gremlins JSON report")
	threshold := fs.Float64("threshold-efficacy", 0, "fail when efficacy is below this percentage")
	if err := fs.Parse(args); err != nil {
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

func runGate(env command.Env, args []string) error {
	fs := flag.NewFlagSet("mutation gate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	input := fs.String("input", "mutation.json", "gremlins JSON report")
	floor := fs.Float64("floor", 70, "minimum efficacy percentage")
	allowlistPath := fs.String("allowlist", "", "allowlist JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *floor < 0 || *floor > 100 {
		return fmt.Errorf("floor must be between 0 and 100")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read mutation report %s: %w", *input, err)
	}
	parsed, err := ParseReport(data)
	if err != nil {
		return err
	}
	var allowlist Allowlist
	if *allowlistPath != "" {
		allowlist, err = LoadAllowlist(*allowlistPath)
		if err != nil {
			return err
		}
	}
	if err := Gate(parsed, *floor, allowlist); err != nil {
		return err
	}
	efficacy, _ := parsed.Counts.Efficacy()
	fmt.Fprintf(env.Stdout, "mutation gate: efficacy=%.1f%% floor=%.1f%% allowed=%d\n",
		efficacy, *floor, len(allowlist.Survivors))
	return nil
}
