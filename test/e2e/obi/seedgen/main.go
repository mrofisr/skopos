// Command seedgen writes the deterministic seed used by the Grafana journey.
// The same samples are inserted into ClickHouse and loaded into the reference
// Prometheus, so a query through Grafana must return the same frames from
// both datasources.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	metricName  = "skopos_e2e_requests_total"
	metricUnit  = "1"
	scopeName   = "skopos/grafana-journey"
	scopeVer    = "1.0"
	serviceBase = "skopos-e2e-server-"
	logBody     = "grafana journey log"
)

// Sample is one counter sample for one service.
type Sample struct {
	Service string
	Time    time.Time
	Value   int
}

// Log is one log line for the Loki journey.
type Log struct {
	Service string
	Time    time.Time
	Body    string
}

// logs returns services x samples log lines ending at now. Even samples are
// info and odd samples are errors so a level filter has something to select.
func logs(now time.Time, services, samples int, step time.Duration) []Log {
	if services < 1 {
		services = 1
	}
	if samples < 2 {
		samples = 2
	}
	out := make([]Log, 0, services*samples)
	base := now.UTC().Truncate(time.Second).Add(-time.Duration(samples-1) * step)
	for service := 0; service < services; service++ {
		name := fmt.Sprintf("%s%d", serviceBase, service)
		for sample := 0; sample < samples; sample++ {
			level, status := "info", 200
			if sample%2 == 1 {
				level, status = "error", 500
			}
			out = append(out, Log{
				Service: name,
				Time:    base.Add(time.Duration(sample) * step),
				Body:    fmt.Sprintf(`{"level":%q,"status":%d,"msg":%q}`, level, status, logBody),
			})
		}
	}
	return out
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

func writeLogSQL(w io.Writer, entries []Log) error {
	if _, err := io.WriteString(w, `INSERT INTO otel_logs
  (Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName,
   Body, LogAttributes, ResourceAttributes)
VALUES
`); err != nil {
		return err
	}
	for i, entry := range entries {
		comma := ","
		if i == len(entries)-1 {
			comma = ";"
		}
		if _, err := fmt.Fprintf(w,
			"(toDateTime64('%s', 9), '', '', '', 0, '%s', '%s', map(), map('service.name', '%s'))%s\n",
			sqlTimeNanos(entry.Time), entry.Service, entry.Body, entry.Service, comma,
		); err != nil {
			return err
		}
	}
	return nil
}

// writeLokiPush writes the same entries as a Loki push request body.
func writeLokiPush(w io.Writer, entries []Log) error {
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	byService := make(map[string]*stream)
	order := make([]string, 0)
	for _, entry := range entries {
		s := byService[entry.Service]
		if s == nil {
			s = &stream{Stream: map[string]string{"service_name": entry.Service}}
			byService[entry.Service] = s
			order = append(order, entry.Service)
		}
		s.Values = append(s.Values, [2]string{
			fmt.Sprintf("%d", entry.Time.UnixNano()), entry.Body,
		})
	}
	sort.Strings(order)
	payload := struct {
		Streams []*stream `json:"streams"`
	}{}
	for _, service := range order {
		payload.Streams = append(payload.Streams, byService[service])
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(encoded)
	return err
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

func sqlTimeNanos(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000000000")
}

func main() {
	sqlPath := flag.String("sql", "", "write the ClickHouse seed SQL here")
	openMetricsPath := flag.String("openmetrics", "", "write the OpenMetrics seed here")
	lokiPushPath := flag.String("loki-push", "", "write the Loki push body here")
	nowText := flag.String("now", "", "sample base time in RFC3339 (default: now)")
	services := flag.Int("services", 6, "number of services")
	samples := flag.Int("samples", 13, "samples per service")
	step := flag.Duration("step", 30*time.Second, "interval between samples")
	flag.Parse()

	if *sqlPath == "" || *openMetricsPath == "" || *lokiPushPath == "" {
		fmt.Fprintln(os.Stderr, "seedgen: --sql, --openmetrics, and --loki-push are required")
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
	entries := logs(now, *services, *samples, *step)
	if err := writeFile(*sqlPath, func(w io.Writer) error {
		if err := writeSQL(w, rows); err != nil {
			return err
		}
		return writeLogSQL(w, entries)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "seedgen: %v\n", err)
		os.Exit(1)
	}
	if err := writeFile(*lokiPushPath, func(w io.Writer) error { return writeLokiPush(w, entries) }); err != nil {
		fmt.Fprintf(os.Stderr, "seedgen: %v\n", err)
		os.Exit(1)
	}
	if err := writeFile(*openMetricsPath, func(w io.Writer) error { return writeOpenMetrics(w, rows) }); err != nil {
		fmt.Fprintf(os.Stderr, "seedgen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("seedgen: wrote %d samples and %d log lines for %d services\n",
		len(rows), len(entries), *services)
}

func writeFile(path string, write func(io.Writer) error) error {
	var b strings.Builder
	if err := write(&b); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
