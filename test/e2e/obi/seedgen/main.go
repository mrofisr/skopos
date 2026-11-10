// Command seedgen writes the deterministic seed used by the Grafana journey.
// The same samples are inserted into ClickHouse and loaded into the reference
// Prometheus, so a query through Grafana must return the same frames from
// both datasources.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	metricName  = "skopos_e2e_requests_total"
	metricUnit  = "1"
	scopeName   = "skopos/grafana-journey"
	scopeVer    = "1.0"
	serviceBase = "skopos-e2e-server-"
)

// Sample is one counter sample for one service.
type Sample struct {
	Service string
	Time    time.Time
	Value   int
}

// generate returns services x samples samples ending at now.
func generate(now time.Time, services, samples int, step time.Duration) []Sample {
	if services < 1 {
		services = 1
	}
	if samples < 2 {
		samples = 2
	}
	out := make([]Sample, 0, services*samples)
	base := now.UTC().Truncate(time.Second).Add(-time.Duration(samples-1) * step)
	for service := 0; service < services; service++ {
		name := fmt.Sprintf("%s%d", serviceBase, service)
		for sample := 0; sample < samples; sample++ {
			out = append(out, Sample{
				Service: name,
				Time:    base.Add(time.Duration(sample) * step),
				Value:   sample + 1,
			})
		}
	}
	return out
}

func writeSQL(w io.Writer, samples []Sample) error {
	if _, err := io.WriteString(w, `INSERT INTO otel_metrics_sum
  (ResourceAttributes, ResourceSchemaUrl, ScopeName, ScopeVersion,
   ScopeAttributes, ScopeDroppedAttrCount, ScopeSchemaUrl, ServiceName,
   MetricName, MetricDescription, MetricUnit, Attributes, StartTimeUnix,
   TimeUnix, Value, Flags, AggregationTemporality, IsMonotonic)
VALUES
`); err != nil {
		return err
	}
	for i, sample := range samples {
		comma := ","
		if i == len(samples)-1 {
			comma = ";"
		}
		if _, err := fmt.Fprintf(w,
			"(map('service.name', '%s'), '', '%s', '%s', map(), 0, '', '%s', '%s', 'synthetic Grafana journey counter', '%s', map(), toDateTime('%s'), toDateTime('%s'), %d, 0, 2, 1)%s\n",
			sample.Service, scopeName, scopeVer, sample.Service, metricName,
			metricUnit, sqlTime(sample.Time.Add(-time.Hour)), sqlTime(sample.Time),
			sample.Value, comma,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeOpenMetrics(w io.Writer, samples []Sample) error {
	if _, err := fmt.Fprintf(w, "# TYPE %s counter\n", metricName); err != nil {
		return err
	}
	for _, sample := range samples {
		if _, err := fmt.Fprintf(w, "%s{service_name=%q} %d %d\n",
			metricName, sample.Service, sample.Value, sample.Time.Unix()); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "# EOF\n")
	return err
}

func sqlTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func main() {
	sqlPath := flag.String("sql", "", "write the ClickHouse seed SQL here")
	openMetricsPath := flag.String("openmetrics", "", "write the OpenMetrics seed here")
	nowText := flag.String("now", "", "sample base time in RFC3339 (default: now)")
	services := flag.Int("services", 6, "number of services")
	samples := flag.Int("samples", 13, "samples per service")
	step := flag.Duration("step", 30*time.Second, "interval between samples")
	flag.Parse()

	if *sqlPath == "" || *openMetricsPath == "" {
		fmt.Fprintln(os.Stderr, "seedgen: --sql and --openmetrics are required")
		os.Exit(2)
	}
	if *step <= 0 {
		fmt.Fprintln(os.Stderr, "seedgen: --step must be positive")
		os.Exit(2)
	}
	now := time.Now()
	if *nowText != "" {
		parsed, err := time.Parse(time.RFC3339, *nowText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seedgen: --now: %v\n", err)
			os.Exit(2)
		}
		now = parsed
	}
	rows := generate(now, *services, *samples, *step)
	if err := writeFile(*sqlPath, func(w io.Writer) error { return writeSQL(w, rows) }); err != nil {
		fmt.Fprintf(os.Stderr, "seedgen: %v\n", err)
		os.Exit(1)
	}
	if err := writeFile(*openMetricsPath, func(w io.Writer) error { return writeOpenMetrics(w, rows) }); err != nil {
		fmt.Fprintf(os.Stderr, "seedgen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("seedgen: wrote %d samples for %d services\n", len(rows), *services)
}

func writeFile(path string, write func(io.Writer) error) error {
	var b strings.Builder
	if err := write(&b); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
