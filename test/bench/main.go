// Command bench measures Skopos gateway latency for representative
// Prometheus, Tempo, and Loki queries against a running gateway.
//
// It is the Go port of bench.py (RS-20). Sequential mode warms and times each
// query; concurrent mode runs a mixed workload across a client pool. The JSON
// report is the machine-readable contract used by the parity fixtures and, in
// a later ticket, the AX-25 concurrency gate.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type config struct {
	baseURL          string
	iterations       int
	warmup           int
	concurrency      int
	duration         float64
	timeout          float64
	progressInterval float64
	dockerStats      bool
	dockerContainers []string
	statsInterval    float64
	jsonOut          string
	gate             bool
	memoryLimitBytes int64
	oomKilled        bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point. Exit codes match the Python harness: 0 on
// success, 1 on a runtime failure, 2 on invalid flags.
func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "bench:", err)
		return 2
	}

	now := time.Now().Unix()
	client := newAPIClient(cfg.baseURL, time.Duration(cfg.timeout*float64(time.Second)))
	ctx := context.Background()
	traceID, err := resolveTraceID(ctx, client, now)
	if err != nil {
		fmt.Fprintf(stderr, "bench: could not resolve a trace ID: %v\n", err)
		return 1
	}
	if traceID == "" {
		fmt.Fprintln(stderr, "bench: no trace ID available for tempo_trace_by_id")
		return 1
	}

	if cfg.concurrency == 1 && cfg.duration <= 0 {
		err = runSequential(ctx, cfg, client, traceID, now, stdout)
	} else {
		err = runConcurrent(ctx, cfg, client, traceID, now, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, "bench:", err)
		return 1
	}
	return 0
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	var cfg config
	expanded, err := expandListFlag(args, "--docker-containers")
	if err != nil {
		return cfg, err
	}

	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.baseURL, "base-url", "http://127.0.0.1:28082", "gateway base URL")
	fs.IntVar(&cfg.iterations, "iterations", 30, "timed iterations per query")
	fs.IntVar(&cfg.warmup, "warmup", 3, "warmup requests per query")
	fs.IntVar(&cfg.concurrency, "concurrency", 1, "number of concurrent mixed-workload clients")
	fs.Float64Var(&cfg.duration, "duration", 0, "seconds to run the mixed workload; 0 uses --iterations passes per client")
	fs.Float64Var(&cfg.timeout, "timeout", 120, "per-request timeout in seconds")
	fs.Float64Var(&cfg.progressInterval, "progress-interval", 10, "seconds between progress lines (0 disables)")
	fs.BoolVar(&cfg.dockerStats, "docker-stats", false, "sample CPU and memory with docker stats during concurrent mode")
	fs.Var((*stringList)(&cfg.dockerContainers), "docker-containers", "container IDs or names to sample with --docker-stats")
	fs.Float64Var(&cfg.statsInterval, "stats-interval", 1, "seconds between docker stats samples")
	fs.StringVar(&cfg.jsonOut, "json-out", "", "write the JSON report to this file")
	fs.BoolVar(&cfg.gate, "gate", false, "fail when the concurrent run has transport errors, unexpected statuses, or missing Retry-After")
	fs.Int64Var(&cfg.memoryLimitBytes, "memory-limit-bytes", 0, "fail when a sampled container peak exceeds this limit")
	fs.BoolVar(&cfg.oomKilled, "oom-killed", false, "fail the gate when a container was OOM-killed")
	if err := fs.Parse(expanded); err != nil {
		return cfg, err
	}
	if fs.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	switch {
	case cfg.concurrency < 1:
		return cfg, errors.New("--concurrency must be at least 1")
	case cfg.duration < 0:
		return cfg, errors.New("--duration must not be negative")
	case cfg.warmup < 0:
		return cfg, errors.New("--warmup must not be negative")
	case cfg.iterations < 1:
		return cfg, errors.New("--iterations must be at least 1")
	case cfg.timeout <= 0:
		return cfg, errors.New("--timeout must be positive")
	case cfg.gate && cfg.concurrency == 1 && cfg.duration <= 0:
		return cfg, errors.New("--gate requires concurrent mode (--concurrency > 1 or --duration > 0)")
	case cfg.memoryLimitBytes < 0:
		return cfg, errors.New("--memory-limit-bytes must not be negative")
	}
	return cfg, nil
}

// stringList collects a flag that accepts one or more space-separated values.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

// expandListFlag rewrites `--name a b c` into repeated `--name=a` flags so the
// standard flag package can accept the nargs="+" form used by the Python CLI.
func expandListFlag(args []string, name string) ([]string, error) {
	out := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == name {
			values := make([]string, 0, 4)
			index++
			for index < len(args) && !strings.HasPrefix(args[index], "-") {
				values = append(values, args[index])
				index++
			}
			index--
			if len(values) == 0 {
				return nil, fmt.Errorf("%s needs at least one value", name)
			}
			for _, value := range values {
				out = append(out, name+"="+value)
			}
			continue
		}
		out = append(out, arg)
	}
	return out, nil
}
