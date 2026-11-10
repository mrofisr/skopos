package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// percentile uses the nearest-rank definition from the Python harness:
// ceil(pct/100 * n) - 1, clamped to the first sample.
func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := max(0, int(math.Ceil(pct/100.0*float64(len(ordered))))-1)
	return ordered[index]
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

// LatencyRow is the shared statistics block for one scope.
type LatencyRow struct {
	Requests     int     `json:"requests"`
	Errors       int     `json:"errors"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	P50          float64 `json:"p50_ms"`
	P95          float64 `json:"p95_ms"`
	P99          float64 `json:"p99_ms"`
	Min          float64 `json:"min_ms"`
	Max          float64 `json:"max_ms"`
	Mean         float64 `json:"mean_ms"`
}

func newLatencyRow(samples []float64, requests, errors int) LatencyRow {
	row := LatencyRow{
		Requests: requests,
		Errors:   errors,
		P50:      round2(percentile(samples, 50)),
		P95:      round2(percentile(samples, 95)),
		P99:      round2(percentile(samples, 99)),
	}
	if requests > 0 {
		row.ErrorRatePct = round2(100.0 * float64(errors) / float64(requests))
	}
	if len(samples) > 0 {
		row.Min = round2(percentile(samples, 0))
		row.Max = round2(percentile(samples, 100))
		row.Mean = round2(mean(samples))
	}
	return row
}

// recorder accumulates per-query samples and error messages. Mutex-guarded so
// the concurrent runner can share one recorder across clients.
type recorder struct {
	mu       sync.Mutex
	samples  map[string][]float64
	requests map[string]int
	errors   map[string]int
	messages map[string]int

	transportErrors   int
	rejections        int
	unexpected        int
	missingRetryAfter int
}

func newRecorder(names []string) *recorder {
	rec := &recorder{
		samples:  make(map[string][]float64, len(names)),
		requests: make(map[string]int, len(names)),
		errors:   make(map[string]int, len(names)),
		messages: make(map[string]int),
	}
	for _, name := range names {
		rec.samples[name] = nil
	}
	return rec
}

func (r *recorder) record(name string, result measurement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[name] = append(r.samples[name], result.elapsed)
	r.requests[name]++
	if result.err != nil {
		r.errors[name]++
		var status *statusError
		switch {
		case errors.As(result.err, &status) && (status.Code == 429 || status.Code == 503):
			r.rejections++
			if !result.retryAfter {
				r.missingRetryAfter++
			}
		case errors.As(result.err, &status):
			r.unexpected++
		default:
			r.transportErrors++
		}
		message := classifyError(result.err) + ": " + result.err.Error()
		if len(message) > 160 {
			message = message[:157] + "..."
		}
		r.messages[message]++
	}
}

// gateCounts returns the concurrency-gate counters.
func (r *recorder) gateCounts() (transport, rejections, unexpected, missingRetryAfter int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transportErrors, r.rejections, r.unexpected, r.missingRetryAfter
}

func (r *recorder) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests, errors := 0, 0
	for _, count := range r.requests {
		requests += count
	}
	for _, count := range r.errors {
		errors += count
	}
	return requests, errors
}

func (r *recorder) snapshot(names []string) (map[string][]float64, map[string]int, map[string]int, map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	samples := make(map[string][]float64, len(names))
	requests := make(map[string]int, len(names))
	errors := make(map[string]int, len(names))
	for _, name := range names {
		samples[name] = append([]float64(nil), r.samples[name]...)
		requests[name] = r.requests[name]
		errors[name] = r.errors[name]
	}
	messages := make(map[string]int, len(r.messages))
	for message, count := range r.messages {
		messages[message] = count
	}
	return samples, requests, errors, messages
}

var (
	sizePattern = regexp.MustCompile(`^([0-9.]+)\s*([A-Za-z]+)$`)
	sizeUnits   = map[string]float64{
		"B":   1,
		"kB":  1000,
		"KB":  1000,
		"MB":  1000 * 1000,
		"GB":  1000 * 1000 * 1000,
		"KiB": 1024,
		"MiB": 1024 * 1024,
		"GiB": 1024 * 1024 * 1024,
		"TiB": 1024 * 1024 * 1024 * 1024,
	}
)

// parseSize converts docker stats memory text such as "12.5MiB / 7.6GiB" to
// bytes. Unparseable text yields zero, matching the Python harness.
func parseSize(text string) float64 {
	first := strings.TrimSpace(strings.SplitN(text, "/", 2)[0])
	match := sizePattern.FindStringSubmatch(first)
	if match == nil {
		return 0
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}
	return value * sizeUnits[match[2]]
}

func humanBytes(value float64) string {
	for _, unit := range []string{"B", "KiB", "MiB", "GiB"} {
		if value < 1024 || unit == "GiB" {
			if unit == "B" {
				return fmt.Sprintf("%.0f %s", value, unit)
			}
			return fmt.Sprintf("%.1f %s", value, unit)
		}
		value /= 1024
	}
	return fmt.Sprintf("%.1f GiB", value)
}

// commandRunner runs docker for the resource sampler.
type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// resourceSample is one docker stats line.
type resourceSample struct {
	cpuCores float64
	memory   float64
}

// dockerStatsSampler samples CPU and memory until stop is called.
type dockerStatsSampler struct {
	containers []string
	interval   time.Duration
	runner     commandRunner

	mu      sync.Mutex
	samples map[string][]resourceSample
	stop    chan struct{}
	done    chan struct{}
}

func newDockerStatsSampler(containers []string, interval time.Duration, runner commandRunner) *dockerStatsSampler {
	if interval < 200*time.Millisecond {
		interval = 200 * time.Millisecond
	}
	if runner == nil {
		runner = execRunner{}
	}
	return &dockerStatsSampler{
		containers: append([]string(nil), containers...),
		interval:   interval,
		runner:     runner,
		samples:    make(map[string][]resourceSample),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

func (s *dockerStatsSampler) start() {
	go func() {
		defer close(s.done)
		for {
			started := time.Now()
			s.sampleOnce(context.Background())
			wait := s.interval - time.Since(started)
			if wait < 0 {
				wait = 0
			}
			timer := time.NewTimer(wait)
			select {
			case <-s.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

func (s *dockerStatsSampler) sampleOnce(ctx context.Context) {
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}"}, s.containers...)
	output, err := s.runner.Output(ctx, "docker", args...)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64)
		if err != nil {
			continue
		}
		s.samples[fields[0]] = append(s.samples[fields[0]], resourceSample{
			cpuCores: cpu / 100.0,
			memory:   parseSize(fields[2]),
		})
	}
}

func (s *dockerStatsSampler) stopAndWait() {
	close(s.stop)
	select {
	case <-s.done:
	case <-time.After(20 * time.Second):
	}
}

type resourceSummary struct {
	Container       string  `json:"container"`
	Samples         int     `json:"samples"`
	CPUAvgCores     float64 `json:"cpu_avg_cores"`
	CPUPeakCores    float64 `json:"cpu_peak_cores"`
	MemoryAvgBytes  int64   `json:"memory_avg_bytes"`
	MemoryPeakBytes int64   `json:"memory_peak_bytes"`
}

func (s *dockerStatsSampler) summarize() []resourceSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.samples))
	for name := range s.samples {
		names = append(names, name)
	}
	sort.Strings(names)
	resources := make([]resourceSummary, 0, len(names))
	for _, name := range names {
		points := s.samples[name]
		if len(points) == 0 {
			continue
		}
		cpu := make([]float64, 0, len(points))
		memory := make([]float64, 0, len(points))
		for _, point := range points {
			cpu = append(cpu, point.cpuCores)
			memory = append(memory, point.memory)
		}
		resources = append(resources, resourceSummary{
			Container:       name,
			Samples:         len(points),
			CPUAvgCores:     round2(mean(cpu)),
			CPUPeakCores:    round2(percentile(cpu, 100)),
			MemoryAvgBytes:  int64(mean(memory)),
			MemoryPeakBytes: int64(percentile(memory, 100)),
		})
	}
	return resources
}
