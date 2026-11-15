package scale

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mrofisr/skopos/internal/plan"
)

// Env supplies the process environment and clock to Run.
type Env struct {
	Getenv func(string) string
	Now    func() time.Time
}

// Run parses the scale-gen flags and either prints the dataset or writes it to
// ClickHouse. It returns an error instead of exiting so tests can exercise it.
func Run(ctx context.Context, env Env, args []string, stdout, stderr io.Writer) error {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	fs := flag.NewFlagSet("scale-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		profileName = fs.String("profile", Profile10k, "profile: 10k or 100k")
		seed        = fs.Int64("seed", 42, "deterministic seed")
		series      = fs.Int("series", 0, "override series count")
		services    = fs.Int("services", 0, "override service count")
		logs        = fs.Int("logs", -1, "override log line count")
		spans       = fs.Int("spans", -1, "override span count")
		step        = fs.Duration("step", 0, "override sample step")
		window      = fs.Duration("range", 0, "override sample range")
		nowText     = fs.String("now", "", "end time in RFC3339 (default: now)")
		url         = fs.String("url", env.Getenv("CLICKHOUSE_URL"), "ClickHouse HTTP URL")
		user        = fs.String("user", env.Getenv("CLICKHOUSE_USER"), "ClickHouse user")
		password    = fs.String("password", env.Getenv("CLICKHOUSE_PASSWORD"), "ClickHouse password")
		database    = fs.String("database", "otel", "ClickHouse database")
		batch       = fs.Int("batch", 5000, "rows per insert")
		dryRun      = fs.Bool("dry-run", false, "print counts and hash without writing")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	profile, err := Lookup(*profileName)
	if err != nil {
		return err
	}
	if set["series"] {
		if *series <= 0 {
			return fmt.Errorf("scale-gen: --series must be positive")
		}
		profile.Series = *series
	}
	if set["services"] {
		if *services <= 0 {
			return fmt.Errorf("scale-gen: --services must be positive")
		}
		profile.Services = *services
	}
	if set["logs"] {
		if *logs < 0 {
			return fmt.Errorf("scale-gen: --logs must not be negative")
		}
		profile.Logs = *logs
	}
	if set["spans"] {
		if *spans < 0 {
			return fmt.Errorf("scale-gen: --spans must not be negative")
		}
		profile.Spans = *spans
	}
	if set["step"] {
		if *step <= 0 {
			return fmt.Errorf("scale-gen: --step must be positive")
		}
		profile.Step = *step
	}
	if set["range"] {
		if *window <= 0 {
			return fmt.Errorf("scale-gen: --range must be positive")
		}
		profile.Range = *window
	}
	profile.Samples = int(profile.Range / profile.Step)
	if *batch <= 0 {
		return fmt.Errorf("scale-gen: --batch must be positive")
	}
	if !plan.ValidIdent(*database) {
		return fmt.Errorf("scale-gen: invalid database %q", *database)
	}
	now := env.Now().UTC()
	if *nowText != "" {
		parsed, err := time.Parse(time.RFC3339, *nowText)
		if err != nil {
			return fmt.Errorf("scale-gen: --now: %w", err)
		}
		now = parsed.UTC()
	}
	dataset, err := Generate(profile, *seed)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout,
		"profile=%s seed=%d now=%s series=%d services=%d samples=%d metric_samples=%d histogram_rows=%d logs=%d spans=%d hash=%s\n",
		profile.Name, dataset.Seed, now.Format(time.RFC3339), dataset.MetricSeries, profile.Services,
		profile.Samples, dataset.MetricSamples, profile.Services*profile.Samples, dataset.LogLines,
		dataset.Spans, dataset.Hash())
	if *dryRun {
		return nil
	}
	if *url == "" {
		return fmt.Errorf("scale-gen: --url or CLICKHOUSE_URL is required unless --dry-run")
	}
	writer := &Writer{
		URL:      *url,
		User:     *user,
		Password: *password,
		Database: *database,
		Batch:    *batch,
	}
	return writer.Write(ctx, dataset, now)
}
