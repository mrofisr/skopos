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
	journey := fs.Bool("journey", false, "run the Prometheus journey through Grafana's query API")
	lokiJourney := fs.Bool("loki-journey", false, "run the Loki journey through Grafana's query API")
	tempoJourney := fs.Bool("tempo-journey", false, "run the Tempo journey through Grafana's query API")
	skoposUID := fs.String("skopos-uid", "skopos-prometheus", "Skopos Prometheus datasource UID")
	referenceUID := fs.String("reference-uid", "reference-prometheus", "reference Prometheus datasource UID")
	journeyStep := fs.Duration("journey-step", 30*time.Second, "journey query step")
	lokiUID := fs.String("loki-uid", "skopos-loki", "Skopos Loki datasource UID")
	referenceLokiUID := fs.String("reference-loki-uid", "reference-loki", "reference Loki datasource UID")
	lokiService := fs.String("loki-service", "skopos-e2e-server-0", "seeded log service")
	tempoUID := fs.String("tempo-uid", "skopos-tempo", "Skopos Tempo datasource UID")
	referenceTempoUID := fs.String("reference-tempo-uid", "reference-tempo", "reference Tempo datasource UID")
	tempoTraceID := fs.String("tempo-trace-id", "5bf92f3577b34da6a3ce929d0e0e4737", "seeded trace ID")
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
	if !*journey && !*lokiJourney && !*tempoJourney {
		return nil
	}
	if *journeyStep <= 0 {
		return fmt.Errorf("journey-step must be positive, got %s", *journeyStep)
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	if *journey {
		runner := Journey{
			Client: client,
			Config: JourneyConfig{
				SkoposUID:    *skoposUID,
				ReferenceUID: *referenceUID,
				Now:          now,
				Step:         *journeyStep,
			},
		}
		steps, err := runner.Run(ctx)
		if err != nil {
			return fmt.Errorf("grafana journey: %w", err)
		}
		for _, step := range steps {
			fmt.Fprintf(env.Stdout, "grafana-check: journey %s OK (%s)\n", step.Name, step.Detail)
		}
		fmt.Fprintf(env.Stdout, "grafana-check: %d journey steps passed\n", len(steps))
	}
	if *tempoJourney {
		runner := TempoJourney{
			Client: client,
			Config: TempoConfig{
				SkoposUID:    *tempoUID,
				ReferenceUID: *referenceTempoUID,
				Now:          now,
				TraceID:      *tempoTraceID,
			},
		}
		steps, err := runner.Run(ctx)
		if err != nil {
			return fmt.Errorf("grafana tempo journey: %w", err)
		}
		for _, step := range steps {
			fmt.Fprintf(env.Stdout, "grafana-check: tempo journey %s OK (%s)\n", step.Name, step.Detail)
		}
		fmt.Fprintf(env.Stdout, "grafana-check: %d tempo journey steps passed\n", len(steps))
	}
	if *lokiJourney {
		runner := LokiJourney{
			Client: client,
			Config: LokiConfig{
				SkoposUID:    *lokiUID,
				ReferenceUID: *referenceLokiUID,
				Now:          now,
				Step:         *journeyStep,
				Service:      *lokiService,
			},
		}
		steps, err := runner.Run(ctx)
		if err != nil {
			return fmt.Errorf("grafana loki journey: %w", err)
		}
		for _, step := range steps {
			fmt.Fprintf(env.Stdout, "grafana-check: loki journey %s OK (%s)\n", step.Name, step.Detail)
		}
		fmt.Fprintf(env.Stdout, "grafana-check: %d loki journey steps passed\n", len(steps))
	}
	return nil
}

func envDefault(env command.Env, name, fallback string) string {
	if value := env.Getenv(name); value != "" {
		return value
	}
	return fallback
}
