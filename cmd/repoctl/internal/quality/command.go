package quality

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

// Command implements `repoctl coverage`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "coverage" }

// Summary implements command.Command.
func (Command) Summary() string { return "report, gate, and write per-package coverage" }

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: repoctl coverage <report|gate|write> [flags]")
	}
	switch args[0] {
	case "report":
		return runReport(env, args[1:])
	case "gate":
		return runGate(ctx, env, args[1:])
	case "write":
		return runWrite(env, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want report, gate, or write)", args[0])
	}
}

type options struct {
	profile     string
	floor       string
	renames     string
	previous    string
	previousRef string
}

func addFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.profile, "profile", "coverage.out", "Go coverage profile")
	fs.StringVar(&o.floor, "floor", "test/quality/coverage-floor.json", "coverage floor file")
	fs.StringVar(&o.renames, "renames", "test/quality/coverage-renames.json", "package rename map applied to the previous floor")
	fs.StringVar(&o.previous, "previous", "", "previous floor file")
	fs.StringVar(&o.previousRef, "previous-ref", "", "git ref whose floor file is the previous floor")
}

func loadCoverage(profilePath string) (map[string]Coverage, error) {
	f, err := os.Open(profilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseProfile(f)
}

func loadPrevious(ctx context.Context, env command.Env, o options) (*Floor, error) {
	if o.previous != "" && o.previousRef != "" {
		return nil, fmt.Errorf("use only one of --previous and --previous-ref")
	}
	if o.previous != "" {
		return LoadFloorFile(o.previous)
	}
	if o.previousRef == "" {
		return nil, nil
	}
	if env.Run == nil {
		return nil, fmt.Errorf("--previous-ref requires a command runner")
	}
	out, err := env.Run.Output(ctx, "git", "show", o.previousRef+":"+o.floor)
	if err != nil {
		fmt.Fprintf(env.Stderr, "repoctl coverage: previous floor %s:%s unavailable; skipping the lowering check\n", o.previousRef, o.floor)
		return nil, nil
	}
	return LoadFloor(bytes.NewReader(out))
}

func loadRenames(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var renames map[string]string
	if err := json.Unmarshal(data, &renames); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return renames, nil
}

// applyRenames moves previous-floor entries to their new package paths. A
// rename is not a removed package, so the ratchet must not treat it as one.
// If both paths exist, the higher floor wins.
func applyRenames(previous *Floor, renames map[string]string) *Floor {
	if previous == nil || len(renames) == 0 {
		return previous
	}
	out := &Floor{Module: previous.Module, Packages: make(map[string]float64, len(previous.Packages))}
	for pkg, value := range previous.Packages {
		if renamed, ok := renames[pkg]; ok {
			pkg = renamed
		}
		if existing, ok := out.Packages[pkg]; !ok || value > existing {
			out.Packages[pkg] = value
		}
	}
	return out
}

func printTable(w io.Writer, cov map[string]Coverage, floor *Floor) {
	module := DefaultModule
	if floor != nil && floor.Module != "" {
		module = floor.Module
	}
	fmt.Fprintf(w, "%-40s %9s %9s\n", "PACKAGE", "COVER", "FLOOR")
	for _, c := range GatePackages(cov, module) {
		floorValue := "-"
		if floor != nil {
			if v, ok := floor.Packages[c.Package]; ok {
				floorValue = fmt.Sprintf("%.1f%%", v)
			}
		}
		fmt.Fprintf(w, "%-40s %8.1f%% %9s\n", c.Package, c.Percent, floorValue)
	}
}

func runReport(env command.Env, args []string) error {
	fs := flag.NewFlagSet("coverage report", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor, err := LoadFloorFile(o.floor)
	if err != nil {
		if os.IsNotExist(err) {
			printTable(env.Stdout, cov, nil)
			return nil
		}
		return err
	}
	printTable(env.Stdout, cov, floor)
	return nil
}

func runGate(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("coverage gate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor, err := LoadFloorFile(o.floor)
	if err != nil {
		return err
	}
	previous, err := loadPrevious(ctx, env, o)
	if err != nil {
		return err
	}
	renames, err := loadRenames(o.renames)
	if err != nil {
		return err
	}
	previous = applyRenames(previous, renames)
	printTable(env.Stdout, cov, floor)
	errs := floor.Gate(cov, previous)
	for _, err := range errs {
		fmt.Fprintln(env.Stderr, "coverage:", err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d coverage problem(s)", len(errs))
	}
	return nil
}

func runWrite(env command.Env, args []string) error {
	fs := flag.NewFlagSet("coverage write", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor := RenderFloor(cov, DefaultModule)
	var buf bytes.Buffer
	if err := floor.Write(&buf); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.floor), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(o.floor, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(env.Stderr, "repoctl coverage: wrote %s (%d packages)\n", o.floor, len(floor.Packages))
	return nil
}
