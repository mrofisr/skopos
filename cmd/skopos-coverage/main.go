// Command skopos-coverage reports and gates per-package statement coverage.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jawaracloud/skopos/internal/quality"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skopos-coverage:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: skopos-coverage <report|gate|write> [flags]")
	}
	switch args[0] {
	case "report":
		return runReport(args[1:])
	case "gate":
		return runGate(args[1:])
	case "write":
		return runWrite(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want report, gate, or write)", args[0])
	}
}

type options struct {
	profile     string
	floor       string
	previous    string
	previousRef string
}

func addFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.profile, "profile", "coverage.out", "Go coverage profile")
	fs.StringVar(&o.floor, "floor", "test/quality/coverage-floor.json", "coverage floor file")
	fs.StringVar(&o.previous, "previous", "", "previous floor file")
	fs.StringVar(&o.previousRef, "previous-ref", "", "git ref whose floor file is the previous floor")
}

func loadCoverage(profilePath string) (map[string]quality.Coverage, error) {
	f, err := os.Open(profilePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return quality.ParseProfile(f)
}

func loadPrevious(o options) (*quality.Floor, error) {
	if o.previous != "" && o.previousRef != "" {
		return nil, fmt.Errorf("use only one of --previous and --previous-ref")
	}
	if o.previous != "" {
		return quality.LoadFloorFile(o.previous)
	}
	if o.previousRef == "" {
		return nil, nil
	}
	out, err := exec.Command("git", "show", o.previousRef+":"+o.floor).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skopos-coverage: previous floor %s:%s unavailable; skipping the lowering check\n", o.previousRef, o.floor)
		return nil, nil
	}
	return quality.LoadFloor(bytes.NewReader(out))
}

func printTable(cov map[string]quality.Coverage, floor *quality.Floor) {
	module := quality.DefaultModule
	if floor != nil && floor.Module != "" {
		module = floor.Module
	}
	fmt.Fprintf(os.Stdout, "%-40s %9s %9s\n", "PACKAGE", "COVER", "FLOOR")
	for _, c := range quality.GatePackages(cov, module) {
		floorValue := "-"
		if floor != nil {
			if v, ok := floor.Packages[c.Package]; ok {
				floorValue = fmt.Sprintf("%.1f%%", v)
			}
		}
		fmt.Fprintf(os.Stdout, "%-40s %8.1f%% %9s\n", c.Package, c.Percent, floorValue)
	}
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor, err := quality.LoadFloorFile(o.floor)
	if err != nil {
		if os.IsNotExist(err) {
			printTable(cov, nil)
			return nil
		}
		return err
	}
	printTable(cov, floor)
	return nil
}

func runGate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor, err := quality.LoadFloorFile(o.floor)
	if err != nil {
		return err
	}
	previous, err := loadPrevious(o)
	if err != nil {
		return err
	}
	printTable(cov, floor)
	errs := floor.Gate(cov, previous)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "coverage:", err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d coverage problem(s)", len(errs))
	}
	return nil
}

func runWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cov, err := loadCoverage(o.profile)
	if err != nil {
		return err
	}
	floor := quality.RenderFloor(cov, quality.DefaultModule)
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
	fmt.Fprintf(os.Stderr, "skopos-coverage: wrote %s (%d packages)\n", o.floor, len(floor.Packages))
	return nil
}
