// Command skopos-quickstart extracts the README block tagged quickstart and
// fails when the chart version it names is not in the OCI registry. The k3d
// harness consumes the generated script and version file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jawaracloud/skopos/internal/quickstart"
)

func main() {
	if err := runWithRunner(os.Args[1:], os.Stdout, os.Stderr, shellRunner); err != nil {
		fmt.Fprintf(os.Stderr, "check-quickstart: %v\n", err)
		os.Exit(1)
	}
}

type runner func(context.Context, string, ...string) error

func shellRunner(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func runWithRunner(args []string, stdout, stderr io.Writer, run runner) error {
	flags := flag.NewFlagSet("check-quickstart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	readme := flags.String("readme", "README.md", "README with the quickstart block")
	script := flags.String("script", "", "write the extracted commands to this file")
	versionFile := flags.String("version-file", "", "write the chart version to this file")
	check := flags.Bool("check", true, "verify the chart version exists in the registry")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	data, err := os.ReadFile(*readme)
	if err != nil {
		return fmt.Errorf("read README: %w", err)
	}
	blocks, err := quickstart.Extract(data)
	if err != nil {
		return err
	}
	plan, err := quickstart.Build(blocks)
	if err != nil {
		return err
	}

	if *script != "" {
		if err := writeScript(*script, plan); err != nil {
			return err
		}
	}
	if *versionFile != "" {
		if err := os.WriteFile(*versionFile, []byte(plan.Version+"\n"), 0o644); err != nil {
			return fmt.Errorf("write version file: %w", err)
		}
	}

	if *check {
		if err := verifyChart(*readme, plan, run); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "check-quickstart: %s version %s is published (%d command(s))\n", plan.ChartRef, plan.Version, len(plan.Commands))
	return nil
}

func writeScript(path string, plan quickstart.Plan) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create script directory: %w", err)
	}
	body := "#!/bin/sh\nset -eu\n\n"
	for _, command := range plan.Commands {
		body += command + "\n\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		return fmt.Errorf("write script: %w", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("chmod script: %w", err)
	}
	return nil
}

func verifyChart(readme string, plan quickstart.Plan, run runner) error {
	if run == nil {
		return errors.New("registry check needs a command runner")
	}
	if err := run(context.Background(), "helm", "show", "chart", plan.ChartRef, "--version", plan.Version); err != nil {
		return fmt.Errorf(
			"chart %s version %s is not available in the registry (README %s): %w",
			plan.ChartRef,
			plan.Version,
			readme,
			err,
		)
	}
	return nil
}
