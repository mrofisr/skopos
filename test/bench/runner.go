package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"
)

// sequentialResult is one row of the sequential latency report.
type sequentialResult struct {
	Name          string  `json:"name"`
	Kind          string  `json:"kind"`
	Iterations    int     `json:"iterations"`
	P50           float64 `json:"p50_ms"`
	P95           float64 `json:"p95_ms"`
	P99           float64 `json:"p99_ms"`
	Min           float64 `json:"min_ms"`
	Max           float64 `json:"max_ms"`
	Mean          float64 `json:"mean_ms"`
	ResponseBytes int     `json:"response_bytes"`
}

type sequentialReport struct {
	BaseURL    string             `json:"base_url"`
	Mode       string             `json:"mode"`
	Iterations int                `json:"iterations"`
	Warmup     int                `json:"warmup"`
	TraceID    string             `json:"trace_id"`
	Results    []sequentialResult `json:"results"`
}

// queryRow is one per-query line in the concurrent report.
type queryRow struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	LatencyRow
}

type overallRow struct {
	LatencyRow
	ThroughputRPS float64 `json:"throughput_rps"`
}

type concurrentReport struct {
	BaseURL            string            `json:"base_url"`
	Mode               string            `json:"mode"`
	Concurrency        int               `json:"concurrency"`
	DurationSeconds    float64           `json:"duration_seconds"`
	Iterations         *int              `json:"iterations"`
	Warmup             int               `json:"warmup"`
	TraceID            string            `json:"trace_id"`
	ElapsedSeconds     float64           `json:"elapsed_seconds"`
	TotalRequests      int               `json:"total_requests"`
	Errors             int               `json:"errors"`
	ErrorRatePct       float64           `json:"error_rate_pct"`
	ThroughputRPS      float64           `json:"throughput_rps"`
	Overall            overallRow        `json:"overall"`
	Queries            []queryRow        `json:"queries"`
	Resources          []resourceSummary `json:"resources"`
	ErrorsByMessage    map[string]int    `json:"errors_by_message"`
	TransportErrors    int               `json:"transport_errors"`
	Rejections         int               `json:"rejections"`
	UnexpectedStatuses int               `json:"unexpected_statuses"`
	MissingRetryAfter  int               `json:"missing_retry_after"`
}

// runSequential warms and times each query in order and writes the Markdown
// table plus the optional JSON report.
func runSequential(ctx context.Context, cfg config, client *apiClient, traceID string, now int64, stdout io.Writer, queries []query) error {
	results := make([]sequentialResult, 0, len(queries))
	for _, q := range queries {
		path, params := buildRequest(q, traceID, now)
		for i := 0; i < cfg.warmup; i++ {
			if _, _, err := client.getJSON(ctx, path, params); err != nil {
				return fmt.Errorf("%s warmup: %w", q.Name, err)
			}
		}
		samples := make([]float64, 0, cfg.iterations)
		responseBytes := 0
		for i := 0; i < cfg.iterations; i++ {
			result := measure(ctx, client, q, path, params)
			if result.err != nil {
				return fmt.Errorf("%s: %w", q.Name, result.err)
			}
			samples = append(samples, result.elapsed)
			responseBytes = result.bytes
		}
		results = append(results, sequentialResult{
			Name:          q.Name,
			Kind:          q.Kind,
			Iterations:    cfg.iterations,
			P50:           round2(percentile(samples, 50)),
			P95:           round2(percentile(samples, 95)),
			P99:           round2(percentile(samples, 99)),
			Min:           round2(percentile(samples, 0)),
			Max:           round2(percentile(samples, 100)),
			Mean:          round2(mean(samples)),
			ResponseBytes: responseBytes,
		})
	}

	fmt.Fprintln(stdout, "| Query | p50 ms | p95 ms | p99 ms | min ms | max ms | Response bytes |")
	fmt.Fprintln(stdout, "| --- | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, row := range results {
		fmt.Fprintf(stdout, "| `%s` | %.2f | %.2f | %.2f | %.2f | %.2f | %d |\n",
			row.Name, row.P50, row.P95, row.P99, row.Min, row.Max, row.ResponseBytes)
	}

	if cfg.jsonOut != "" {
		return writeJSON(cfg.jsonOut, sequentialReport{
			BaseURL:    cfg.baseURL,
			Mode:       "sequential",
			Iterations: cfg.iterations,
			Warmup:     cfg.warmup,
			TraceID:    traceID,
			Results:    results,
		})
	}
	return nil
}

// concurrentSpec is one prepared request.
type concurrentSpec struct {
	query  query
	path   string
	params url.Values
}

// runConcurrent runs a mixed workload across clients for a duration or a
// fixed number of passes per client.
func runConcurrent(ctx context.Context, cfg config, client *apiClient, traceID string, now int64, stdout, stderr io.Writer, queries []query) error {
	specs := make([]concurrentSpec, 0, len(queries))
	names := make([]string, 0, len(queries))
	for _, q := range queries {
		path, params := buildRequest(q, traceID, now)
		specs = append(specs, concurrentSpec{query: q, path: path, params: params})
		names = append(names, q.Name)
	}
	for _, spec := range specs {
		for i := 0; i < cfg.warmup; i++ {
			if _, _, err := client.getJSON(ctx, spec.path, spec.params); err != nil {
				return fmt.Errorf("%s warmup: %w", spec.query.Name, err)
			}
		}
	}

	rec := newRecorder(names)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var deadline time.Time
	if cfg.duration > 0 {
		deadline = time.Now().Add(time.Duration(cfg.duration * float64(time.Second)))
	}

	var wg sync.WaitGroup
	for index := 0; index < cfg.concurrency; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			offset := index % len(specs)
			passes := 0
			for runCtx.Err() == nil {
				if !deadline.IsZero() && !time.Now().Before(deadline) {
					return
				}
				for step := 0; step < len(specs); step++ {
					if runCtx.Err() != nil {
						return
					}
					if !deadline.IsZero() && !time.Now().Before(deadline) {
						return
					}
					spec := specs[(offset+step)%len(specs)]
					rec.record(spec.query.Name, measure(runCtx, client, spec.query, spec.path, spec.params))
				}
				passes++
				if deadline.IsZero() && passes >= cfg.iterations {
					return
				}
			}
		}(index)
	}

	sampler := (*dockerStatsSampler)(nil)
	if cfg.dockerStats && len(cfg.dockerContainers) > 0 {
		sampler = newDockerStatsSampler(cfg.dockerContainers, time.Duration(cfg.statsInterval*float64(time.Second)), nil)
		sampler.start()
	}

	started := time.Now()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	nextProgress := started.Add(time.Duration(cfg.progressInterval * float64(time.Second)))
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			goto finished
		case <-ticker.C:
			if cfg.progressInterval > 0 && !time.Now().Before(nextProgress) {
				requests, errors := rec.counts()
				fmt.Fprintf(stderr, "bench: progress %.0fs requests=%d errors=%d\n",
					time.Since(started).Seconds(), requests, errors)
				nextProgress = nextProgress.Add(time.Duration(cfg.progressInterval * float64(time.Second)))
			}
		}
	}

finished:
	elapsed := time.Since(started).Seconds()
	cancel()
	if sampler != nil {
		sampler.stopAndWait()
	}

	samples, requests, errors, messages := rec.snapshot(names)
	allSamples := make([]float64, 0)
	totalRequests, totalErrors := 0, 0
	for _, name := range names {
		allSamples = append(allSamples, samples[name]...)
		totalRequests += requests[name]
		totalErrors += errors[name]
	}
	overall := overallRow{LatencyRow: newLatencyRow(allSamples, totalRequests, totalErrors)}
	if elapsed > 0 {
		overall.ThroughputRPS = round2(float64(totalRequests) / elapsed)
	}

	rows := make([]queryRow, 0, len(queries))
	for _, q := range queries {
		rows = append(rows, queryRow{
			Name:       q.Name,
			Kind:       q.Kind,
			LatencyRow: newLatencyRow(samples[q.Name], requests[q.Name], errors[q.Name]),
		})
	}

	fmt.Fprintf(stdout, "Concurrent mixed workload: %d clients, %d requests in %.1fs (%.2f req/s)\n",
		cfg.concurrency, totalRequests, elapsed, overall.ThroughputRPS)
	fmt.Fprintf(stdout, "Errors: %d (%.2f%%)\n", totalErrors, overall.ErrorRatePct)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "| Scope | Requests | Errors | Error rate | p50 ms | p95 ms | p99 ms | mean ms |")
	fmt.Fprintln(stdout, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	fmt.Fprintf(stdout, "| overall | %d | %d | %.2f%% | %.2f | %.2f | %.2f | %.2f |\n",
		overall.Requests, overall.Errors, overall.ErrorRatePct, overall.P50, overall.P95, overall.P99, overall.Mean)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "| Query | Kind | Requests | Errors | Error rate | p50 ms | p95 ms | p99 ms | mean ms |")
	fmt.Fprintln(stdout, "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, row := range rows {
		fmt.Fprintf(stdout, "| `%s` | %s | %d | %d | %.2f%% | %.2f | %.2f | %.2f | %.2f |\n",
			row.Name, row.Kind, row.Requests, row.Errors, row.ErrorRatePct,
			row.P50, row.P95, row.P99, row.Mean)
	}

	var resources []resourceSummary
	if sampler != nil {
		resources = sampler.summarize()
	}
	printResources(stdout, resources)

	if len(messages) > 0 {
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Top errors:")
		for _, message := range topMessages(messages, 5) {
			fmt.Fprintf(stderr, "  %dx %s\n", messages[message], message)
		}
	}

	transportErrors, rejections, unexpected, missingRetryAfter := rec.gateCounts()
	if cfg.jsonOut != "" {
		var iterations *int
		if cfg.duration <= 0 {
			value := cfg.iterations
			iterations = &value
		}
		if err := writeJSON(cfg.jsonOut, concurrentReport{
			BaseURL:            cfg.baseURL,
			Mode:               "concurrent",
			Concurrency:        cfg.concurrency,
			DurationSeconds:    cfg.duration,
			Iterations:         iterations,
			Warmup:             cfg.warmup,
			TraceID:            traceID,
			ElapsedSeconds:     round2(elapsed),
			TotalRequests:      totalRequests,
			Errors:             totalErrors,
			ErrorRatePct:       overall.ErrorRatePct,
			ThroughputRPS:      overall.ThroughputRPS,
			Overall:            overall,
			Queries:            rows,
			Resources:          resources,
			ErrorsByMessage:    messages,
			TransportErrors:    transportErrors,
			Rejections:         rejections,
			UnexpectedStatuses: unexpected,
			MissingRetryAfter:  missingRetryAfter,
		}); err != nil {
			return err
		}
	}
	if cfg.gate {
		problems := gateProblems(transportErrors, rejections, unexpected, missingRetryAfter, resources, cfg)
		if len(problems) > 0 {
			for _, problem := range problems {
				fmt.Fprintln(stderr, "bench-gate:", problem)
			}
			return fmt.Errorf("bench gate failed with %d problem(s)", len(problems))
		}
		fmt.Fprintf(stdout, "bench-gate: passed (%d requests, %d deliberate rejections, 0 transport errors)\n",
			totalRequests, rejections)
	}
	return nil
}

// gateProblems applies the EC3 safety rules to one concurrent run.
func gateProblems(transport, rejections, unexpected, missing int, resources []resourceSummary, cfg config) []string {
	var problems []string
	if transport > 0 {
		problems = append(problems, fmt.Sprintf("%d transport error(s)", transport))
	}
	if unexpected > 0 {
		problems = append(problems, fmt.Sprintf("%d unexpected non-2xx response(s)", unexpected))
	}
	if missing > 0 {
		problems = append(problems, fmt.Sprintf("%d rejection(s) without Retry-After", missing))
	}
	if cfg.oomKilled {
		problems = append(problems, "a benchmark container was OOM-killed")
	}
	if cfg.memoryLimitBytes > 0 {
		for _, resource := range resources {
			if resource.MemoryPeakBytes > cfg.memoryLimitBytes {
				problems = append(problems, fmt.Sprintf(
					"container %s peak memory %d exceeds the limit %d",
					resource.Container, resource.MemoryPeakBytes, cfg.memoryLimitBytes))
			}
		}
	}
	return problems
}

func printResources(w io.Writer, resources []resourceSummary) {
	if len(resources) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Container | Samples | CPU avg (cores) | CPU peak (cores) | Memory avg | Memory peak |")
	fmt.Fprintln(w, "| --- | ---: | ---: | ---: | ---: | ---: |")
	for _, row := range resources {
		fmt.Fprintf(w, "| `%s` | %d | %.2f | %.2f | %s | %s |\n",
			row.Container, row.Samples, row.CPUAvgCores, row.CPUPeakCores,
			humanBytes(float64(row.MemoryAvgBytes)), humanBytes(float64(row.MemoryPeakBytes)))
	}
}

// topMessages returns the most frequent messages, breaking ties by text so the
// output is deterministic.
func topMessages(messages map[string]int, limit int) []string {
	keys := make([]string, 0, len(messages))
	for message := range messages {
		keys = append(keys, message)
	}
	sort.Slice(keys, func(i, j int) bool {
		if messages[keys[i]] != messages[keys[j]] {
			return messages[keys[i]] > messages[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func writeJSON(path string, value any) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return fmt.Errorf("write JSON: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}
