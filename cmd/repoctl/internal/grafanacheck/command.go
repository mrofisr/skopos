package grafanacheck

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

// Command implements `repoctl grafana-check`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "grafana-check" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "verify the provisioned Grafana datasources and their health"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("grafana-check", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	grafanaURL := fs.String("grafana-url",
		envDefault(env, "GRAFANA_URL", "http://127.0.0.1:13000"), "Grafana base URL")
	provisioning := fs.String("provisioning",
		envDefault(env, "GRAFANA_PROVISIONING", "deploy/examples/grafana/provisioning"),
		"provisioning file or directory")
	username := fs.String("username", env.Getenv("GRAFANA_USERNAME"), "Grafana basic auth user")
	password := fs.String("password", env.Getenv("GRAFANA_PASSWORD"), "Grafana basic auth password")
	timeout := fs.Duration("timeout", 30*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %s", *timeout)
	}

	datasources, err := ParseProvisioningPath(*provisioning)
	if err != nil {
		return err
	}
	client := Client{
		BaseURL:  *grafanaURL,
		Username: *username,
		Password: *password,
		HTTP:     &http.Client{Timeout: *timeout},
	}
	checked, err := client.Check(ctx, datasources)
	if err != nil {
		return err
	}
	for _, datasource := range checked {
		fmt.Fprintf(env.Stdout, "grafana-check: %s (%s) OK\n", datasource.Name, datasource.UID)
	}
	fmt.Fprintf(env.Stdout, "grafana-check: %d datasources healthy\n", len(checked))
	return nil
}

func envDefault(env command.Env, name, fallback string) string {
	if value := env.Getenv(name); value != "" {
		return value
	}
	return fallback
}
