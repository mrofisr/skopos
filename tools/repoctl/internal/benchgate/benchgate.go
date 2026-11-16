package benchgate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

const schemaVersion = 1

// Command implements `repoctl bench-gate`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "bench-gate" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "compare a bench run to its baseline and fail on regressions"
}

type options struct {
	baseline            string
	current             stringList
	profile             string
	datasetHash         string
	image               string
	host                string
	p50Tolerance        float64
	p95Tolerance        float64
	throughputTolerance float64
}

// Run implements command.Command.
func (Command) Run(_ context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("bench-gate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var opts options
	fs.StringVar(&opts.baseline, "baseline", "", "baseline JSON file")
	fs.Var(&opts.current, "current", "current bench JSON file (repeat up to three times)")
	fs.StringVar(&opts.profile, "profile", "", "benchmark profile name")
	fs.StringVar(&opts.datasetHash, "dataset-hash", "", "dataset hash the run used")
	fs.StringVar(&opts.image, "image", "", "gateway image the run used")
	fs.StringVar(&opts.host, "host", "", "host class the run used")
	fs.Float64Var(&opts.p50Tolerance, "p50-tolerance", 0.15, "allowed p50 regression; 0.15 is 15%")
	fs.Float64Var(&opts.p95Tolerance, "p95-tolerance", 0.20, "allowed p95 regression; 0.20 is 20%")
	fs.Float64Var(&opts.throughputTolerance, "throughput-tolerance", 0.15, "allowed throughput regression; 0.15 is 15%")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if opts.baseline == "" {
		return errors.New("--baseline is required")
	}
	if len(opts.current) == 0 {
		return errors.New("at least one --current file is required")
	}
	if len(opts.current) > 3 {
		return errors.New("at most three --current files are supported")
	}
	for name, value := range map[string]string{
		"profile":      opts.profile,
		"dataset-hash": opts.datasetHash,
		"image":        opts.image,
		"host":         opts.host,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}
	if err := checkTolerance("p50", opts.p50Tolerance); err != nil {
		return err
	}
	if err := checkTolerance("p95", opts.p95Tolerance); err != nil {
		return err
	}
	if err := checkTolerance("throughput", opts.throughputTolerance); err != nil {
		return err
	}

	baseline, base, err := loadBaseline(opts.baseline)
	if err != nil {
		return err
	}
	if err := checkMetadata(opts, baseline); err != nil {
		return err
	}
	runs, err := loadCurrent(opts.current, base.Mode)
	if err != nil {
		return err
	}
	summary, findings, err := compare(base, runs, tolerances{
		p50:        opts.p50Tolerance,
		p95:        opts.p95Tolerance,
		throughput: opts.throughputTolerance,
	})
	if err != nil {
		return err
	}

	printSummary(env.Stdout, opts, baseline, base.Mode, len(runs), summary)
	if len(findings) > 0 {
		for _, finding := range findings {
			fmt.Fprintf(env.Stdout, "  regression: %s\n", finding)
		}
		return fmt.Errorf("%d bench regression(s): %s", len(findings), strings.Join(findings, "; "))
	}
	fmt.Fprintf(env.Stdout, "bench-gate: pass (%d queries, %d run(s))\n", len(summary.Rows), len(runs))
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func checkTolerance(name string, value float64) error {
	if value < 0 || value >= 1 {
		return fmt.Errorf("--%s-tolerance must be in [0, 1), got %g", name, value)
	}
	return nil
}

type baselineFile struct {
	Schema      int             `json:"schema"`
	Profile     string          `json:"profile"`
	DatasetHash string          `json:"dataset_hash"`
	Image       string          `json:"image"`
	Host        string          `json:"host"`
	CapturedAt  string          `json:"captured_at"`
	Bench       json.RawMessage `json:"bench"`
}

type report struct {
	Mode          string       `json:"mode"`
	Results       []latencyRow `json:"results"`
	Queries       []latencyRow `json:"queries"`
	Overall       latencyRow   `json:"overall"`
	ThroughputRPS float64      `json:"throughput_rps"`
	Errors        int          `json:"errors"`
}

type latencyRow struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Requests int     `json:"requests"`
	Errors   int     `json:"errors"`
	P50      float64 `json:"p50_ms"`
	P95      float64 `json:"p95_ms"`
	P99      float64 `json:"p99_ms"`
	Mean     float64 `json:"mean_ms"`
}

func loadBaseline(path string) (baselineFile, report, error) {
	var file baselineFile
	data, err := os.ReadFile(path)
	if err != nil {
		return file, report{}, fmt.Errorf("read baseline: %w", err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, report{}, fmt.Errorf("parse baseline: %w", err)
	}
	if file.Schema != schemaVersion {
		return file, report{}, fmt.Errorf("baseline schema %d, want %d", file.Schema, schemaVersion)
	}
	if len(file.Bench) == 0 {
		return file, report{}, errors.New("baseline has no bench report")
	}
	var bench report
	if err := json.Unmarshal(file.Bench, &bench); err != nil {
		return file, report{}, fmt.Errorf("parse baseline bench: %w", err)
	}
	if err := validateReport(bench); err != nil {
		return file, report{}, fmt.Errorf("baseline: %w", err)
	}
	return file, bench, nil
}

func checkMetadata(opts options, baseline baselineFile) error {
	switch {
	case baseline.Profile != opts.profile:
		return fmt.Errorf("baseline profile %q, want %q", baseline.Profile, opts.profile)
	case baseline.DatasetHash != opts.datasetHash:
		return fmt.Errorf("baseline dataset hash %q, want %q", baseline.DatasetHash, opts.datasetHash)
	case baseline.Image != opts.image:
		return fmt.Errorf("baseline image %q, want %q", baseline.Image, opts.image)
	case baseline.Host != opts.host:
		return fmt.Errorf("baseline host %q, want %q", baseline.Host, opts.host)
	}
	return nil
}

func loadCurrent(paths []string, mode string) ([]report, error) {
	runs := make([]report, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read current %q: %w", path, err)
		}
		var run report
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, fmt.Errorf("parse current %q: %w", path, err)
		}
		if err := validateReport(run); err != nil {
			return nil, fmt.Errorf("current %q: %w", path, err)
		}
		if run.Mode != mode {
			return nil, fmt.Errorf("current %q: mode %q, want %q", path, run.Mode, mode)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func validateReport(run report) error {
	switch run.Mode {
	case "sequential":
		if len(run.Results) == 0 {
			return errors.New("sequential report has no results")
		}
		for _, row := range run.Results {
			if row.P50 <= 0 || row.P95 <= 0 {
				return fmt.Errorf("query %q has no positive p50/p95", row.Name)
			}
		}
	case "concurrent":
		if len(run.Queries) == 0 {
			return errors.New("concurrent report has no queries")
		}
		if run.Overall.P50 <= 0 || run.Overall.P95 <= 0 {
			return errors.New("concurrent report has no positive overall p50/p95")
		}
		if run.ThroughputRPS <= 0 {
			return errors.New("concurrent report has no positive throughput")
		}
		for _, row := range run.Queries {
			if row.P50 <= 0 || row.P95 <= 0 {
				return fmt.Errorf("query %q has no positive p50/p95", row.Name)
			}
		}
	default:
		return fmt.Errorf("unknown mode %q", run.Mode)
	}
	return nil
}

type tolerances struct {
	p50        float64
	p95        float64
	throughput float64
}

type rowResult struct {
	Name          string
	BaseP50       float64
	CurrentP50    float64
	BaseP95       float64
	CurrentP95    float64
	BaseErrors    int
	CurrentErrors int
	P50Fail       bool
	P95Fail       bool
	ErrorFail     bool
}

type summary struct {
	Rows              []rowResult
	BaseThroughput    float64
	CurrentThroughput float64
	ThroughputFail    bool
	HasThroughput     bool
}

func compare(baseline report, runs []report, tolerance tolerances) (summary, []string, error) {
	baseRows := rowsFor(baseline)
	baseByName := make(map[string]latencyRow, len(baseRows))
	for _, row := range baseRows {
		if row.Name == "" {
			return summary{}, nil, errors.New("baseline contains a row without a name")
		}
		baseByName[row.Name] = row
	}

	currentMaps := make([]map[string]latencyRow, len(runs))
	for i, run := range runs {
		currentMaps[i] = make(map[string]latencyRow, len(rowsFor(run)))
		for _, row := range rowsFor(run) {
			currentMaps[i][row.Name] = row
		}
		for _, name := range sortedNames(currentMaps[i]) {
			if _, ok := baseByName[name]; !ok {
				return summary{}, nil, fmt.Errorf("current run %d has unexpected query %q", i+1, name)
			}
		}
		for name := range baseByName {
			if _, ok := currentMaps[i][name]; !ok {
				return summary{}, nil, fmt.Errorf("current run %d is missing query %q", i+1, name)
			}
		}
	}

	var result summary
	findings := make([]string, 0)
	for _, baseRow := range baseRows {
		values := make([]latencyRow, 0, len(currentMaps))
		for _, current := range currentMaps {
			values = append(values, current[baseRow.Name])
		}
		p50s := make([]float64, len(values))
		p95s := make([]float64, len(values))
		errs := make([]int, len(values))
		for i, value := range values {
			p50s[i] = value.P50
			p95s[i] = value.P95
			errs[i] = value.Errors
		}
		currentP50 := median(p50s)
		currentP95 := median(p95s)
		currentErrors := maxInt(errs)
		row := rowResult{
			Name:          baseRow.Name,
			BaseP50:       baseRow.P50,
			CurrentP50:    currentP50,
			BaseP95:       baseRow.P95,
			CurrentP95:    currentP95,
			BaseErrors:    baseRow.Errors,
			CurrentErrors: currentErrors,
		}
		if currentP50 > limit(baseRow.P50, tolerance.p50) {
			row.P50Fail = true
			findings = append(findings, fmt.Sprintf("%s: p50 %.1f ms > %.1f ms (%+.1f%%)",
				baseRow.Name, currentP50, limit(baseRow.P50, tolerance.p50), percent(baseRow.P50, currentP50)))
		}
		if currentP95 > limit(baseRow.P95, tolerance.p95) {
			row.P95Fail = true
			findings = append(findings, fmt.Sprintf("%s: p95 %.1f ms > %.1f ms (%+.1f%%)",
				baseRow.Name, currentP95, limit(baseRow.P95, tolerance.p95), percent(baseRow.P95, currentP95)))
		}
		if currentErrors > baseRow.Errors {
			row.ErrorFail = true
			findings = append(findings, fmt.Sprintf("%s: errors %d > %d", baseRow.Name, currentErrors, baseRow.Errors))
		}
		result.Rows = append(result.Rows, row)
	}

	if baseline.Mode == "concurrent" {
		result.HasThroughput = true
		result.BaseThroughput = baseline.ThroughputRPS
		throughputs := make([]float64, len(runs))
		for i, run := range runs {
			throughputs[i] = run.ThroughputRPS
		}
		result.CurrentThroughput = median(throughputs)
		if result.CurrentThroughput < result.BaseThroughput*(1-tolerance.throughput) {
			result.ThroughputFail = true
			findings = append(findings, fmt.Sprintf("throughput: %.2f req/s < %.2f req/s (%.1f%%)",
				result.CurrentThroughput, result.BaseThroughput*(1-tolerance.throughput),
				percent(result.BaseThroughput, result.CurrentThroughput)))
		}
	}
	return result, findings, nil
}

func rowsFor(run report) []latencyRow {
	if run.Mode == "sequential" {
		return run.Results
	}
	rows := make([]latencyRow, 0, len(run.Queries)+1)
	rows = append(rows, run.Queries...)
	overall := run.Overall
	overall.Name = "overall"
	return append(rows, overall)
}

func sortedNames(rows map[string]latencyRow) []string {
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func limit(base, tolerance float64) float64 {
	return base * (1 + tolerance)
}

func percent(base, current float64) float64 {
	if base == 0 {
		return 0
	}
	return (current - base) / base * 100
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func maxInt(values []int) int {
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max
}

func printSummary(w io.Writer, opts options, baseline baselineFile, mode string, runs int, result summary) {
	fmt.Fprintf(w, "bench-gate: %s %s\n", opts.profile, mode)
	fmt.Fprintf(w, "baseline: %s, image %s, dataset %s, host %s\n",
		baseline.CapturedAt, baseline.Image, baseline.DatasetHash, baseline.Host)
	fmt.Fprintf(w, "runs: %d\n\n", runs)
	fmt.Fprintln(w, "| Query | p50 baseline | p50 current | delta | p95 baseline | p95 current | delta | errors | status |")
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |")
	for _, row := range result.Rows {
		status := "ok"
		if row.P50Fail || row.P95Fail || row.ErrorFail {
			status = "FAIL"
		}
		fmt.Fprintf(w, "| `%s` | %.1f | %.1f | %+.1f%% | %.1f | %.1f | %+.1f%% | %d | %s |\n",
			row.Name, row.BaseP50, row.CurrentP50, percent(row.BaseP50, row.CurrentP50),
			row.BaseP95, row.CurrentP95, percent(row.BaseP95, row.CurrentP95),
			row.CurrentErrors, status)
	}
	if result.HasThroughput {
		status := "ok"
		if result.ThroughputFail {
			status = "FAIL"
		}
		fmt.Fprintf(w, "\nthroughput: %.2f req/s (baseline %.2f, %+.1f%%) %s\n",
			result.CurrentThroughput, result.BaseThroughput,
			percent(result.BaseThroughput, result.CurrentThroughput), status)
	}
}
