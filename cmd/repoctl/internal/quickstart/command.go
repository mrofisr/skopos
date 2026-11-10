package quickstart

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

// Command implements `repoctl quickstart`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "quickstart" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "extract and verify the README quickstart against the chart registry"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	readme := fs.String("readme", "README.md", "README with the quickstart block")
	script := fs.String("script", "", "write the extracted commands to this file")
	versionFile := fs.String("version-file", "", "write the chart version to this file")
	check := fs.Bool("check", true, "verify the chart version exists in the registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	data, err := os.ReadFile(*readme)
	if err != nil {
		return fmt.Errorf("read README: %w", err)
	}
	blocks, err := Extract(data)
	if err != nil {
		return err
	}
	plan, err := Build(blocks)
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
		if err := verifyChart(ctx, env, *readme, plan); err != nil {
			return err
		}
	}
	fmt.Fprintf(env.Stdout, "quickstart: %s version %s is published (%d command(s))\n",
		plan.ChartRef, plan.Version, len(plan.Commands))
	return nil
}

func writeScript(path string, plan Plan) error {
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

func verifyChart(ctx context.Context, env command.Env, readme string, plan Plan) error {
	if env.Run == nil {
		return fmt.Errorf("registry check needs a command runner")
	}
	if err := env.Run.Run(ctx, "helm", "show", "chart", plan.ChartRef, "--version", plan.Version); err != nil {
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
