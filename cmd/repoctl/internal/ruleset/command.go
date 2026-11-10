package ruleset

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

const (
	reviewExceptionHeading = "## Review exception: one maintainer"
	reviewExceptionAnchor  = "review-exception-one-maintainer"
)

// Command implements `repoctl ruleset`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "ruleset" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "validate the main ruleset against the required workflow job names"
}

// Run implements command.Command.
func (Command) Run(_ context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("ruleset", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	root := fs.String("root", ".", "repository root")
	rulesetPath := fs.String("ruleset", "docs/security/ruleset.json", "ruleset document relative to --root")
	workflowsDir := fs.String("workflows", ".github/workflows", "workflow directory relative to --root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	config, err := Load(filepath.Join(*root, *rulesetPath))
	if err != nil {
		return err
	}
	workflows := make(map[string][]byte, len(config.RequiredWorkflows))
	for _, name := range config.RequiredWorkflows {
		data, err := os.ReadFile(filepath.Join(*root, *workflowsDir, name))
		if err != nil {
			return fmt.Errorf("read workflow %q: %w", name, err)
		}
		workflows[name] = data
	}
	if err := Validate(config, workflows); err != nil {
		return err
	}
	if err := checkReviewException(*root, config.ReviewException.DocumentedAt); err != nil {
		return err
	}
	fmt.Fprintf(
		env.Stdout,
		"check-ruleset: %s requires %d checks across %d workflows\n",
		config.Ruleset.Name,
		len(config.RequiredChecks()),
		len(config.RequiredWorkflows),
	)
	return nil
}

func checkReviewException(root, documentedAt string) error {
	path, anchor, _ := strings.Cut(documentedAt, "#")
	if path == "" {
		return fmt.Errorf("review_exception.documented_at %q has no file path", documentedAt)
	}
	if anchor != "" && anchor != reviewExceptionAnchor {
		return fmt.Errorf("review_exception.documented_at anchor = %q, want %q", anchor, reviewExceptionAnchor)
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return fmt.Errorf("read review exception: %w", err)
	}
	if !bytes.Contains(data, []byte(reviewExceptionHeading)) {
		return fmt.Errorf("%s does not document the review exception", path)
	}
	return nil
}
