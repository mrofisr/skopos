package codescanning

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl codescan`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "codescan" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "fail when an open code-scanning alert is high or critical"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("codescan", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	repo := fs.String("repo", "", "repository in owner/name form (default GITHUB_REPOSITORY)")
	fixture := fs.String("fixture", "", "read alerts from a JSON file instead of the API")
	ignoreTool := fs.String("ignore-tool", "Scorecard", "comma-separated scanner names to ignore (posture findings)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var alerts []Alert
	if *fixture != "" {
		data, err := os.ReadFile(*fixture)
		if err != nil {
			return fmt.Errorf("read fixture: %w", err)
		}
		if err := json.Unmarshal(data, &alerts); err != nil {
			return fmt.Errorf("decode fixture: %w", err)
		}
	} else {
		repoName := *repo
		if repoName == "" {
			repoName = env.Getenv("GITHUB_REPOSITORY")
		}
		if repoName == "" {
			return fmt.Errorf("repository is required: pass --repo or set GITHUB_REPOSITORY")
		}
		if env.NewGitHub == nil {
			return fmt.Errorf("GitHub access is not configured")
		}
		api, err := env.NewGitHub(repoName)
		if err != nil {
			return err
		}
		alerts, err = OpenAlerts(ctx, api)
		if err != nil {
			return err
		}
	}

	ignored := make(map[string]struct{})
	for _, name := range strings.Split(*ignoreTool, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			ignored[strings.ToLower(name)] = struct{}{}
		}
	}
	considered := alerts[:0]
	for _, alert := range alerts {
		if _, ok := ignored[strings.ToLower(alert.Tool.Name)]; ok {
			continue
		}
		considered = append(considered, alert)
	}
	if err := Evaluate(considered); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "codescan: %d open alerts, %d ignored scanners, 0 high or critical\n",
		len(alerts), len(alerts)-len(considered))
	return nil
}
