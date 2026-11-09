// Command skopos-grafana-check verifies that Grafana provisioned the Skopos
// datasources and that each datasource passes its health check. The
// e2e-grafana harness calls it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jawaracloud/skopos/internal/grafanacheck"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "grafana-check: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("skopos-grafana-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	grafanaURL := flags.String("grafana-url",
		envDefault("GRAFANA_URL", "http://127.0.0.1:13000"), "Grafana base URL")
	provisioning := flags.String("provisioning",
		envDefault("GRAFANA_PROVISIONING", "deploy/examples/grafana/provisioning"),
		"provisioning file or directory")
	username := flags.String("username", os.Getenv("GRAFANA_USERNAME"), "Grafana basic auth user")
	password := flags.String("password", os.Getenv("GRAFANA_PASSWORD"), "Grafana basic auth password")
	timeout := flags.Duration("timeout", 30*time.Second, "per-request timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %s", *timeout)
	}

	datasources, err := grafanacheck.ParseProvisioningPath(*provisioning)
	if err != nil {
		return err
	}
	client := grafanacheck.Client{
		BaseURL:  *grafanaURL,
		Username: *username,
		Password: *password,
		HTTP:     &http.Client{Timeout: *timeout},
	}
	checked, err := client.Check(context.Background(), datasources)
	if err != nil {
		return err
	}
	for _, datasource := range checked {
		fmt.Fprintf(stdout, "grafana-check: %s (%s) OK\n", datasource.Name, datasource.UID)
	}
	fmt.Fprintf(stdout, "grafana-check: %d datasources healthy\n", len(checked))
	return nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
