// Command skopos-code-scanning fails when any open GitHub code-scanning alert
// is high or critical. The release preflight calls it through
// scripts/check-code-scanning.sh.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jawaracloud/skopos/internal/codescanning"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "check-code-scanning: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("check-code-scanning", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"), "repository in owner/name form")
	apiURL := flags.String("api-url", envDefault("GITHUB_API_URL", "https://api.github.com"), "GitHub API base URL")
	fixture := flags.String("fixture", "", "read alerts from a JSON file instead of the API")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var alerts []codescanning.Alert
	if *fixture != "" {
		data, err := os.ReadFile(*fixture)
		if err != nil {
			return fmt.Errorf("read fixture: %w", err)
		}
		if err := json.Unmarshal(data, &alerts); err != nil {
			return fmt.Errorf("decode fixture: %w", err)
		}
	} else {
		if *repo == "" {
			return errors.New("repository is required: pass --repo or set GITHUB_REPOSITORY")
		}
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = os.Getenv("GH_TOKEN")
		}
		if token == "" {
			return errors.New("GITHUB_TOKEN or GH_TOKEN is required")
		}
		client := codescanning.Client{
			BaseURL: *apiURL,
			Repo:    *repo,
			Token:   token,
			HTTP:    &http.Client{Timeout: 30 * time.Second},
		}
		fetched, err := client.OpenAlerts(context.Background())
		if err != nil {
			return err
		}
		alerts = fetched
	}

	if err := codescanning.Evaluate(alerts); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "check-code-scanning: %d open alerts, 0 high or critical\n", len(alerts))
	return nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
