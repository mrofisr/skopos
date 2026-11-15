package scale

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedEnv() Env {
	return Env{
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
	}
}

func TestRunDryRun(t *testing.T) {
	var out, errOut strings.Builder
	err := Run(context.Background(), fixedEnv(), []string{"--seed", "42", "--dry-run"}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, errOut.String())
	}
	for _, want := range []string{
		"profile=10k",
		"seed=42",
		"series=10000",
		"services=10",
		"samples=240",
		"metric_samples=2400000",
		"histogram_rows=2400",
		"logs=10000",
		"spans=10000",
		"hash=89094c7a42891a57",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunDryRunOverrides(t *testing.T) {
	var out, errOut strings.Builder
	err := Run(context.Background(), fixedEnv(), []string{
		"--series", "100",
		"--services", "5",
		"--range", "1m",
		"--step", "10s",
		"--logs", "7",
		"--spans", "9",
		"--dry-run",
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, errOut.String())
	}
	for _, want := range []string{
		"series=100",
		"services=5",
		"samples=6",
		"metric_samples=600",
		"histogram_rows=30",
		"logs=7",
		"spans=9",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "hash=89094c7a42891a57") {
		t.Fatalf("overrides kept the 10k hash:\n%s", out.String())
	}
}

func TestRunInvalid(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown profile", []string{"--profile", "nope", "--dry-run"}},
		{"negative series", []string{"--series", "-1", "--dry-run"}},
		{"zero services", []string{"--services", "0", "--dry-run"}},
		{"series below services", []string{"--series", "1", "--services", "2", "--dry-run"}},
		{"zero batch", []string{"--batch", "0", "--dry-run"}},
		{"invalid database", []string{"--database", "bad-name", "--dry-run"}},
		{"negative logs", []string{"--logs", "-1", "--dry-run"}},
		{"zero step", []string{"--step", "0", "--dry-run"}},
		{"zero range", []string{"--range", "0", "--dry-run"}},
		{"bad now", []string{"--now", "not-a-time", "--dry-run"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut strings.Builder
			if err := Run(context.Background(), fixedEnv(), tc.args, &out, &errOut); err == nil {
				t.Fatalf("Run succeeded:\n%s", out.String())
			}
		})
	}
}

type insertRecorder struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *insertRecorder) handle(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query().Get("query")
	table := ""
	for _, name := range []string{"otel_metrics_sum", "otel_metrics_histogram", "otel_logs", "otel_traces"} {
		if strings.Contains(query, name) {
			table = name
			break
		}
	}
	body, _ := io.ReadAll(req.Body)
	rows := 0
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}
	r.mu.Lock()
	r.counts[table] += rows
	r.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (r *insertRecorder) snapshot() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.counts))
	for table, count := range r.counts {
		out[table] = count
	}
	return out
}

func TestRunWrites(t *testing.T) {
	recorder := &insertRecorder{counts: make(map[string]int)}
	server := httptest.NewServer(http.HandlerFunc(recorder.handle))
	defer server.Close()

	var out, errOut strings.Builder
	err := Run(context.Background(), fixedEnv(), []string{
		"--series", "4",
		"--services", "2",
		"--range", "30s",
		"--step", "15s",
		"--logs", "3",
		"--spans", "4",
		"--batch", "2",
		"--url", server.URL,
		"--database", "otel",
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, errOut.String())
	}
	want := map[string]int{
		"otel_metrics_sum":       8,
		"otel_metrics_histogram": 4,
		"otel_logs":              3,
		"otel_traces":            4,
	}
	got := recorder.snapshot()
	for table, count := range want {
		if got[table] != count {
			t.Fatalf("%s rows = %d, want %d (all: %#v)", table, got[table], count, got)
		}
	}
	if got[""] != 0 {
		t.Fatalf("rows went to an unknown table: %#v", got)
	}
}
