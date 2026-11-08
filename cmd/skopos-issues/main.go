// Command skopos-issues generates, lints, and syncs the alpha-exit backlog.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jawaracloud/skopos/internal/issueops"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "skopos-issues:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: skopos-issues <manifest|sync|lint> [flags]")
	}
	switch args[0] {
	case "manifest":
		return runManifest(args[1:])
	case "sync":
		return runSync(args[1:])
	case "lint":
		return runLint(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want manifest, sync, or lint)", args[0])
	}
}

func runManifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	plan := fs.String("plan", "docs/alpha-exit-plan.md", "alpha exit plan markdown")
	out := fs.String("out", "issues/alpha-exit.json", "generated manifest path")
	check := fs.Bool("check", false, "fail when the checked-in manifest is stale")
	write := fs.Bool("write", false, "write the generated manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := issueops.ParsePlanFile(*plan)
	if err != nil {
		return err
	}
	if errs := m.Validate(); len(errs) > 0 {
		return fmt.Errorf("plan produced an invalid manifest: %v", errs)
	}
	data, err := issueops.MarshalManifest(m)
	if err != nil {
		return err
	}
	switch {
	case *check:
		current, err := os.ReadFile(*out)
		if err != nil {
			return fmt.Errorf("read %s: %w", *out, err)
		}
		if !bytes.Equal(current, data) {
			return fmt.Errorf("%s is stale; run make issues-manifest", *out)
		}
		fmt.Fprintf(os.Stderr, "issues-manifest: %s is current (%d issues)\n", *out, len(m.Issues))
		return nil
	case *write:
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "issues-manifest: wrote %s (%d issues)\n", *out, len(m.Issues))
		return nil
	default:
		_, err := os.Stdout.Write(data)
		return err
	}
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "issues/alpha-exit.json", "generated manifest")
	apply := fs.Bool("apply", false, "apply mutations; the default is a dry run")
	repo := fs.String("repo", "", "GitHub repository (owner/name); defaults to the current repo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	gh := issueops.NewGHClient(*repo)
	return issueops.Sync(context.Background(), gh, m, *apply, os.Stdout)
}

func runLint(args []string) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "issues/alpha-exit.json", "generated manifest")
	live := fs.Bool("live", false, "also lint the live repository (read-only)")
	repo := fs.String("repo", "", "GitHub repository (owner/name); defaults to the current repo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	var errs []error
	if problems := m.Validate(); len(problems) > 0 {
		errs = append(errs, problems...)
	}
	if *live {
		gh := issueops.NewGHClient(*repo)
		issues, err := gh.ListIssues(context.Background())
		if err != nil {
			return err
		}
		errs = append(errs, issueops.LintLive(issues)...)
	}
	if len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, "issues-lint:", err)
		}
		return fmt.Errorf("%d problem(s) found", len(errs))
	}
	fmt.Fprintf(os.Stderr, "issues-lint: ok (%d issues)\n", len(m.Issues))
	return nil
}

func loadManifest(path string) (*issueops.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m issueops.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}
