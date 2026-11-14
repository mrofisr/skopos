package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The stub bodies are byte-for-byte the responses used to record the Python
// fixtures in testdata. Keep them in sync: response_bytes is part of parity.
var stubBodies = map[string]string{
	"/api/search":                       `{"traces":[{"traceID":"abc123"}]}`,
	"/api/traces/abc123":                `{"resourceSpans":[]}`,
	"/api/v1/query":                     `{"status":"success","data":{"resultType":"vector","result":[]}}`,
	"/api/v1/query_range":               `{"status":"success","data":{"resultType":"matrix","result":[]}}`,
	"/api/v1/series":                    `{"status":"success","data":[]}`,
	"/api/v1/label/service_name/values": `{"status":"success","data":[]}`,
	"/api/v2/search/tags":               `{"scopes":[]}`,
	"/loki/api/v1/query_range":          `{"status":"success","data":{"resultType":"streams","result":[]}}`,
}

type stubMode int

const (
	stubHealthy stubMode = iota
	stubErrors
	stubDropped
	stubNoTraces
	stubSearchFailure
	stubShedding
	stubSheddingNoRetryAfter
)

func newStub(t *testing.T, mode stubMode) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch mode {
		case stubErrors:
			switch path {
			case "/api/v1/series":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"message":"boom"}`)
				return
			case "/loki/api/v1/query_range":
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"message":"slow down"}`)
				return
			}
		case stubDropped:
			if path == "/api/v1/series" {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("stub response writer does not support hijacking")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				return
			}
		case stubNoTraces:
			if path == "/api/search" {
				_, _ = io.WriteString(w, `{"traces":[]}`)
				return
			}
		case stubSearchFailure:
			if path == "/api/search" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"message":"boom"}`)
				return
			}
		case stubShedding, stubSheddingNoRetryAfter:
			if path == "/api/search" {
				_, _ = io.WriteString(w, `{"traces":[{"traceID":"abc123"}]}`)
				return
			}
			if mode == stubShedding {
				w.Header().Set("Retry-After", "1")
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"slow down"}`)
			return
		}
		body, ok := stubBodies[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// sequentialBaseline is the subset of the Python JSON report the parity test
// compares. Latency is intentionally absent.
type sequentialBaseline struct {
	Mode       string `json:"mode"`
	Iterations int    `json:"iterations"`
	Warmup     int    `json:"warmup"`
	TraceID    string `json:"trace_id"`
	Results    []struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Iterations    int    `json:"iterations"`
		ResponseBytes int    `json:"response_bytes"`
	} `json:"results"`
}

type concurrentBaseline struct {
	Mode            string  `json:"mode"`
	Concurrency     int     `json:"concurrency"`
	DurationSeconds float64 `json:"duration_seconds"`
	Iterations      *int    `json:"iterations"`
	Warmup          int     `json:"warmup"`
	TraceID         string  `json:"trace_id"`
	TotalRequests   int     `json:"total_requests"`
	Errors          int     `json:"errors"`
	ErrorRatePct    float64 `json:"error_rate_pct"`
	Queries         []struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Requests int    `json:"requests"`
		Errors   int    `json:"errors"`
	} `json:"queries"`
	ErrorsByMessage map[string]int `json:"errors_by_message"`
}

func TestSequentialMatchesPythonBaseline(t *testing.T) {
	server := newStub(t, stubHealthy)
	out := filepath.Join(t.TempDir(), "seq.json")
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--iterations", "2",
		"--warmup", "1",
		"--json-out", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "| Query | p50 ms |") {
		t.Fatalf("stdout = %q, want the latency table", stdout.String())
	}

	got := readJSON[sequentialReport](t, out)
	want := readJSON[sequentialBaseline](t, "testdata/baseline-sequential.json")
	if got.Mode != want.Mode || got.Iterations != want.Iterations ||
		got.Warmup != want.Warmup || got.TraceID != want.TraceID {
		t.Fatalf("report header = %#v, want %#v", got, want)
	}
	if len(got.Results) != len(want.Results) {
		t.Fatalf("results = %d, want %d", len(got.Results), len(want.Results))
	}
	for index, row := range got.Results {
		expected := want.Results[index]
		if row.Name != expected.Name || row.Kind != expected.Kind ||
			row.Iterations != expected.Iterations ||
			row.ResponseBytes != expected.ResponseBytes {
			t.Errorf("result %d = %#v, want %#v", index, row, expected)
		}
	}
}

func TestConcurrentMatchesPythonBaseline(t *testing.T) {
	server := newStub(t, stubHealthy)
	out := filepath.Join(t.TempDir(), "concurrent.json")
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--concurrency", "2",
		"--iterations", "2",
		"--warmup", "1",
		"--json-out", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Concurrent mixed workload: 2 clients") {
		t.Fatalf("stdout = %q, want the concurrent summary", stdout.String())
	}

	got := readJSON[concurrentReport](t, out)
	want := readJSON[concurrentBaseline](t, "testdata/baseline-concurrent.json")
	if got.Mode != want.Mode || got.Concurrency != want.Concurrency ||
		got.DurationSeconds != want.DurationSeconds || got.Warmup != want.Warmup ||
		got.TraceID != want.TraceID || got.TotalRequests != want.TotalRequests ||
		got.Errors != want.Errors || got.ErrorRatePct != want.ErrorRatePct {
		t.Fatalf("report = %#v, want %#v", got, want)
	}
	if got.Iterations == nil || want.Iterations == nil || *got.Iterations != *want.Iterations {
		t.Fatalf("iterations = %v, want %v", got.Iterations, want.Iterations)
	}
	if len(got.Queries) != len(want.Queries) {
		t.Fatalf("queries = %d, want %d", len(got.Queries), len(want.Queries))
	}
	for index, row := range got.Queries {
		expected := want.Queries[index]
		if row.Name != expected.Name || row.Kind != expected.Kind ||
			row.Requests != expected.Requests || row.Errors != expected.Errors {
			t.Errorf("query %d = %#v, want %#v", index, row, expected)
		}
	}
	if len(got.Resources) != 0 {
		t.Fatalf("resources = %#v, want none without --docker-stats", got.Resources)
	}
}

func classCounts(messages map[string]int, class func(string) string) map[string]int {
	counts := make(map[string]int)
	for message, count := range messages {
		counts[class(message)] += count
	}
	return counts
}

func TestConcurrentErrorClassesMatchPython(t *testing.T) {
	server := newStub(t, stubErrors)
	out := filepath.Join(t.TempDir(), "errors.json")
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--concurrency", "2",
		"--iterations", "1",
		"--warmup", "0",
		"--json-out", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d, stderr = %s", code, stderr.String())
	}
	got := readJSON[concurrentReport](t, out)
	want := readJSON[concurrentBaseline](t, "testdata/baseline-errors.json")
	if got.TotalRequests != want.TotalRequests || got.Errors != want.Errors {
		t.Fatalf("counts = %d/%d, want %d/%d", got.TotalRequests, got.Errors, want.TotalRequests, want.Errors)
	}
	gotClasses := classCounts(got.ErrorsByMessage, func(message string) string {
		return strings.SplitN(message, ":", 2)[0]
	})
	wantClasses := classCounts(want.ErrorsByMessage, pythonErrorClass)
	for class, count := range wantClasses {
		if gotClasses[class] != count {
			t.Errorf("class %s = %d, want %d (got %v)", class, gotClasses[class], count, gotClasses)
		}
	}
}

func TestConcurrentDroppedConnectionMatchesPython(t *testing.T) {
	server := newStub(t, stubDropped)
	out := filepath.Join(t.TempDir(), "dropped.json")
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--concurrency", "2",
		"--iterations", "1",
		"--warmup", "0",
		"--json-out", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d, stderr = %s", code, stderr.String())
	}
	got := readJSON[concurrentReport](t, out)
	want := readJSON[concurrentBaseline](t, "testdata/baseline-dropped.json")
	if got.TotalRequests != want.TotalRequests || got.Errors != want.Errors {
		t.Fatalf("counts = %d/%d, want %d/%d", got.TotalRequests, got.Errors, want.TotalRequests, want.Errors)
	}
	if gotClasses := classCounts(got.ErrorsByMessage, func(message string) string {
		return strings.SplitN(message, ":", 2)[0]
	}); gotClasses["connection_reset"] != want.Errors {
		t.Fatalf("classes = %v, want %d connection_reset errors", gotClasses, want.Errors)
	}
}

func TestSequentialReportsDroppedConnection(t *testing.T) {
	server := newStub(t, stubDropped)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--iterations", "1",
		"--warmup", "0",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "bench:") {
		t.Fatalf("stderr = %q, want a failure message", stderr.String())
	}
}

func TestNoTraceID(t *testing.T) {
	server := newStub(t, stubNoTraces)
	var stdout, stderr strings.Builder
	code := run([]string{"--base-url", server.URL}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no trace ID available") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTraceIDLookupFailure(t *testing.T) {
	server := newStub(t, stubSearchFailure)
	var stdout, stderr strings.Builder
	code := run([]string{"--base-url", server.URL}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "could not resolve a trace ID") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInvalidFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "concurrency", args: []string{"--concurrency", "0"}, want: "--concurrency"},
		{name: "duration", args: []string{"--duration", "-1"}, want: "--duration"},
		{name: "warmup", args: []string{"--warmup", "-1"}, want: "--warmup"},
		{name: "iterations", args: []string{"--iterations", "0"}, want: "--iterations"},
		{name: "timeout", args: []string{"--timeout", "0"}, want: "--timeout"},
		{name: "unknown flag", args: []string{"--nope"}, want: "flag provided"},
		{name: "positional", args: []string{"extra"}, want: "unexpected arguments"},
		{name: "empty container list", args: []string{"--docker-containers"}, want: "at least one value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if code := run(test.args, &stdout, &stderr); code != 2 {
				t.Fatalf("run = %d, want 2 (stderr %s)", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run = %d, want 0", code)
	}
}

func TestDockerStatsSampler(t *testing.T) {
	runner := fakeDockerRunner{output: "gateway\t12.5%\t100MiB / 1GiB\nclickhouse\t50%\t1.5GiB / 2GiB\nbad line\n"}
	sampler := newDockerStatsSampler([]string{"gateway", "clickhouse"}, 0, runner)
	sampler.sampleOnce(t.Context())
	resources := sampler.summarize()
	if len(resources) != 2 {
		t.Fatalf("resources = %#v, want 2", resources)
	}
	if resources[0].Container != "clickhouse" || resources[0].CPUAvgCores != 0.5 {
		t.Fatalf("first resource = %#v", resources[0])
	}
	if resources[0].MemoryPeakBytes != int64(1.5*1024*1024*1024) {
		t.Fatalf("memory peak = %d", resources[0].MemoryPeakBytes)
	}
}

type fakeDockerRunner struct {
	output string
	err    error
}

func (f fakeDockerRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte(f.output), f.err
}

func TestDockerStatsSamplerIgnoresErrors(t *testing.T) {
	sampler := newDockerStatsSampler(nil, 0, fakeDockerRunner{err: fmt.Errorf("docker missing")})
	sampler.sampleOnce(t.Context())
	if resources := sampler.summarize(); len(resources) != 0 {
		t.Fatalf("resources = %#v, want none", resources)
	}
}

func TestExpandListFlag(t *testing.T) {
	got, err := expandListFlag([]string{"--docker-containers", "a", "b", "--iterations", "2"}, "--docker-containers")
	if err != nil {
		t.Fatal(err)
	}
	want := "--docker-containers=a --docker-containers=b --iterations 2"
	if strings.Join(got, " ") != want {
		t.Fatalf("expanded = %v, want %q", got, want)
	}
	if _, err := expandListFlag([]string{"--docker-containers", "--stats-interval", "1"}, "--docker-containers"); err == nil {
		t.Fatal("expandListFlag accepted an empty list")
	}
}

func TestParseFlagsDockerContainers(t *testing.T) {
	var stderr strings.Builder
	cfg, err := parseFlags([]string{"--docker-containers", "a", "b", "--iterations", "2"}, &stderr)
	if err != nil {
		t.Fatalf("parseFlags: %v (stderr %s)", err, stderr.String())
	}
	if strings.Join(cfg.dockerContainers, ",") != "a,b" || cfg.iterations != 2 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestWriteJSONError(t *testing.T) {
	dir := t.TempDir()
	if err := writeJSON(dir, concurrentReport{}); err == nil {
		t.Fatal("writeJSON succeeded on a directory path")
	}
}

func TestRunSequentialWarmupFailure(t *testing.T) {
	server := newStub(t, stubErrors)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL,
		"--iterations", "1",
		"--warmup", "1",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "warmup") {
		t.Fatalf("stderr = %q, want a warmup failure", stderr.String())
	}
}

func TestRecorderSnapshotIsCopy(t *testing.T) {
	rec := newRecorder([]string{"a"})
	rec.record("a", measurement{elapsed: 1.5, status: 200})
	samples, requests, errors, messages := rec.snapshot([]string{"a"})
	samples["a"][0] = 99
	if rec.samples["a"][0] != 1.5 {
		t.Fatal("snapshot aliased the recorder samples")
	}
	if requests["a"] != 1 || errors["a"] != 0 || len(messages) != 0 {
		t.Fatalf("snapshot = %v %v %v", requests, errors, messages)
	}
}

func TestConcurrentRecorderIsRaceFree(t *testing.T) {
	rec := newRecorder([]string{"a"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rec.record("a", measurement{elapsed: float64(j), status: 200})
			}
		}()
	}
	wg.Wait()
	if requests, errors := rec.counts(); requests != 400 || errors != 0 {
		t.Fatalf("counts = %d/%d", requests, errors)
	}
}

func TestGateFailsOnDroppedConnections(t *testing.T) {
	server := newStub(t, stubDropped)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL, "--concurrency", "2", "--iterations", "1",
		"--warmup", "0", "--gate",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "transport error") {
		t.Fatalf("run = %d (stderr %s), want a transport gate failure", code, stderr.String())
	}
}

func TestGateFailsOnBare500(t *testing.T) {
	server := newStub(t, stubErrors)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL, "--concurrency", "2", "--iterations", "1",
		"--warmup", "0", "--gate",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unexpected non-2xx") {
		t.Fatalf("run = %d (stderr %s), want a bare-500 gate failure", code, stderr.String())
	}
}

func TestGatePassesOnDeliberateShedding(t *testing.T) {
	server := newStub(t, stubShedding)
	out := filepath.Join(t.TempDir(), "gate.json")
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL, "--concurrency", "2", "--iterations", "1",
		"--warmup", "0", "--gate", "--json-out", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run = %d (stderr %s), want a pass", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bench-gate: passed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	report := readJSON[concurrentReport](t, out)
	if report.TransportErrors != 0 || report.UnexpectedStatuses != 0 || report.MissingRetryAfter != 0 {
		t.Fatalf("report gate counters = %#v", report)
	}
	if report.Rejections == 0 {
		t.Fatal("report recorded no deliberate rejections")
	}
}

func TestGateFailsOnMissingRetryAfter(t *testing.T) {
	server := newStub(t, stubSheddingNoRetryAfter)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL, "--concurrency", "2", "--iterations", "1",
		"--warmup", "0", "--gate",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "without Retry-After") {
		t.Fatalf("run = %d (stderr %s), want a missing Retry-After failure", code, stderr.String())
	}
}

func TestGateFailsOnOOMKill(t *testing.T) {
	server := newStub(t, stubHealthy)
	var stdout, stderr strings.Builder
	code := run([]string{
		"--base-url", server.URL, "--concurrency", "2", "--iterations", "1",
		"--warmup", "0", "--gate", "--oom-killed",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "OOM-killed") {
		t.Fatalf("run = %d (stderr %s), want an OOM failure", code, stderr.String())
	}
}

func TestGateRequiresConcurrentMode(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run([]string{"--gate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "concurrent mode") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestGateProblemsMemory(t *testing.T) {
	problems := gateProblems(0, 0, 0, 0, []resourceSummary{
		{Container: "gateway", MemoryPeakBytes: 200},
	}, config{memoryLimitBytes: 100})
	if len(problems) != 1 || !strings.Contains(problems[0], "exceeds the limit") {
		t.Fatalf("problems = %v", problems)
	}
	if problems := gateProblems(0, 0, 0, 0, nil, config{}); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}
