package issueops

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

const (
	defaultPlan     = "docs/alpha-exit-plan.md"
	defaultManifest = "issues/alpha-exit.json"
)

// Command implements `repoctl issues`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "issues" }

// Summary implements command.Command.
func (Command) Summary() string { return "generate, lint, and sync the alpha-exit backlog" }

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: repoctl issues <manifest|sync|lint> [flags]")
	}
	switch args[0] {
	case "manifest":
		return runManifest(env, args[1:])
	case "sync":
		return runSync(ctx, env, args[1:])
	case "lint":
		return runLint(ctx, env, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want manifest, sync, or lint)", args[0])
	}
}

func runManifest(env command.Env, args []string) error {
	fs := flag.NewFlagSet("issues manifest", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	plan := fs.String("plan", defaultPlan, "alpha exit plan markdown")
	out := fs.String("out", defaultManifest, "generated manifest path")
	check := fs.Bool("check", false, "fail when the checked-in manifest is stale")
	write := fs.Bool("write", false, "write the generated manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *check && *write {
		return fmt.Errorf("use only one of --check and --write")
	}
	m, err := ParsePlanFile(*plan)
	if err != nil {
		return err
	}
	if errs := m.Validate(); len(errs) > 0 {
		return fmt.Errorf("plan produced an invalid manifest: %v", errs)
	}
	data, err := MarshalManifest(m)
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
		fmt.Fprintf(env.Stderr, "issues-manifest: %s is current (%d issues)\n", *out, len(m.Issues))
		return nil
	case *write:
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(env.Stderr, "issues-manifest: wrote %s (%d issues)\n", *out, len(m.Issues))
		return nil
	default:
		_, err := env.Stdout.Write(data)
		return err
	}
}

func runSync(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("issues sync", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	manifestPath := fs.String("manifest", defaultManifest, "generated manifest")
	apply := fs.Bool("apply", false, "apply mutations; the default is a dry run")
	repo := fs.String("repo", "", "GitHub repository (owner/name); defaults to GITHUB_REPOSITORY")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	client, err := newClient(env, *repo)
	if err != nil {
		return err
	}
	return Sync(ctx, client, m, *apply, env.Stdout)
}

func runLint(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("issues lint", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	manifestPath := fs.String("manifest", defaultManifest, "generated manifest")
	live := fs.Bool("live", false, "also lint the live repository (read-only)")
	repo := fs.String("repo", "", "GitHub repository (owner/name); defaults to GITHUB_REPOSITORY")
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
		client, err := newClient(env, *repo)
		if err != nil {
			return err
		}
		issues, err := client.ListIssues(ctx)
		if err != nil {
			return err
		}
		errs = append(errs, LintLive(issues)...)
	}
	if len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(env.Stderr, "issues-lint:", err)
		}
		return fmt.Errorf("%d problem(s) found", len(errs))
	}
	fmt.Fprintf(env.Stderr, "issues-lint: ok (%d issues)\n", len(m.Issues))
	return nil
}

func newClient(env command.Env, repo string) (*Client, error) {
	if env.NewGitHub == nil {
		return nil, fmt.Errorf("GitHub access is not configured")
	}
	api, err := env.NewGitHub(repo)
	if err != nil {
		return nil, err
	}
	return NewClient(api), nil
}

func loadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}
