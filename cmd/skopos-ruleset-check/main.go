// Command skopos-ruleset-check validates docs/security/ruleset.json against
// the job display names in the required workflows. The CI policy job and
// make ruleset-check call it.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jawaracloud/skopos/internal/ruleset"
)

const reviewExceptionHeading = "## Review exception: one maintainer"
const reviewExceptionAnchor = "review-exception-one-maintainer"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "check-ruleset: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("check-ruleset", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	rulesetPath := flags.String("ruleset", "docs/security/ruleset.json", "ruleset document relative to --root")
	workflowsDir := flags.String("workflows", ".github/workflows", "workflow directory relative to --root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	config, err := ruleset.Load(filepath.Join(*root, *rulesetPath))
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
	if err := ruleset.Validate(config, workflows); err != nil {
		return err
	}
	if err := checkReviewException(*root, config.ReviewException.DocumentedAt); err != nil {
		return err
	}
	fmt.Fprintf(
		stdout,
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
