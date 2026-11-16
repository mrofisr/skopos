package benchgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

func row(name string, p50, p95 float64) latencyRow {
	return latencyRow{Name: name, P50: p50, P95: p95}
}

func sequential(rows ...latencyRow) report {
	return report{Mode: "sequential", Results: rows}
}

func concurrent(overall latencyRow, throughput float64, rows ...latencyRow) report {
	return report{
		Mode:          "concurrent",
		Overall:       overall,
		Queries:       rows,
		ThroughputRPS: throughput,
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func writeBaseline(t *testing.T, profile, hash, image, host string, bench report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	file := baselineFile{
		Schema:      schemaVersion,
		Profile:     profile,
		DatasetHash: hash,
		Image:       image,
		Host:        host,
		CapturedAt:  "2026-11-16",
		Bench:       mustJSON(t, bench),
	}
	if err := os.WriteFile(path, mustJSON(t, file), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return path
}

func writeReport(t *testing.T, name string, value report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, mustJSON(t, value), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}

func runGate(t *testing.T, baseline string, currents ...string) (string, string, error) {
	t.Helper()
	args := []string{
		"--baseline", baseline,
		"--profile", "test",
		"--dataset-hash", "hash",
		"--image", "img",
		"--host", "host",
	}
	for _, current := range currents {
		args = append(args, "--current", current)
	}
	var stdout, stderr strings.Builder
	err := (Command{}).Run(context.Background(), command.Env{Stdout: &stdout, Stderr: &stderr}, args)
	return stdout.String(), stderr.String(), err
}

func TestWithinTolerancePasses(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q", 110, 220)))
	stdout, _, err := runGate(t, baseline, current)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout, "pass") {
		t.Fatalf("stdout does not report a pass: %q", stdout)
	}
}

func TestP95RegressionFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q", 100, 250)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "q") || !strings.Contains(err.Error(), "p95") {
		t.Fatalf("error = %v, want a p95 regression for q", err)
	}
}

func TestP50RegressionFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q", 120, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "p50") {
		t.Fatalf("error = %v, want a p50 regression", err)
	}
}

func TestThroughputRegressionFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host",
		concurrent(row("overall", 100, 200), 100, row("q", 100, 200)))
	current := writeReport(t, "current.json",
		concurrent(row("overall", 100, 200), 80, row("q", 100, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "throughput") {
		t.Fatalf("error = %v, want a throughput regression", err)
	}
}

func TestNewErrorFails(t *testing.T) {
	baseQuery := row("q", 100, 200)
	baseQuery.Errors = 0
	currentQuery := baseQuery
	currentQuery.Errors = 1
	baseline := writeBaseline(t, "test", "hash", "img", "host",
		concurrent(row("overall", 100, 200), 100, baseQuery))
	current := writeReport(t, "current.json",
		concurrent(row("overall", 100, 200), 100, currentQuery))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "error") {
		t.Fatalf("error = %v, want an error regression", err)
	}
}

func TestMissingQueryFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host",
		sequential(row("q1", 100, 200), row("q2", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q1", 100, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "q2") {
		t.Fatalf("error = %v, want a missing query", err)
	}
}

func TestExtraQueryFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q1", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q1", 100, 200), row("q2", 100, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "q2") {
		t.Fatalf("error = %v, want an unexpected query", err)
	}
}

func TestMedianOfThreeRuns(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	first := writeReport(t, "first.json", sequential(row("q", 100, 210)))
	second := writeReport(t, "second.json", sequential(row("q", 100, 230)))
	third := writeReport(t, "third.json", sequential(row("q", 100, 260)))
	// The median p95 is 230; the maximum 260 would fail the 20% budget.
	if _, _, err := runGate(t, baseline, first, second, third); err != nil {
		t.Fatalf("median run failed: %v", err)
	}
}

func TestMetadataMismatchFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "other-hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q", 100, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "dataset hash") {
		t.Fatalf("error = %v, want a dataset hash mismatch", err)
	}
}

func TestModeMismatchFails(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json",
		concurrent(row("overall", 100, 200), 100, row("q", 100, 200)))
	_, _, err := runGate(t, baseline, current)
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("error = %v, want a mode mismatch", err)
	}
}

func TestSchemaMismatchFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	data := mustJSON(t, map[string]any{
		"schema": 2,
		"bench":  sequential(row("q", 100, 200)),
	})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	current := writeReport(t, "current.json", sequential(row("q", 100, 200)))
	_, _, err := runGate(t, path, current)
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("error = %v, want a schema mismatch", err)
	}
}

func TestCurrentRequired(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	_, _, err := runGate(t, baseline)
	if err == nil || !strings.Contains(err.Error(), "current") {
		t.Fatalf("error = %v, want a current-file requirement", err)
	}
}

func TestToleranceRange(t *testing.T) {
	baseline := writeBaseline(t, "test", "hash", "img", "host", sequential(row("q", 100, 200)))
	current := writeReport(t, "current.json", sequential(row("q", 100, 200)))
	args := []string{
		"--baseline", baseline,
		"--current", current,
		"--profile", "test",
		"--dataset-hash", "hash",
		"--image", "img",
		"--host", "host",
		"--p95-tolerance", "1.5",
	}
	var stdout, stderr strings.Builder
	err := (Command{}).Run(context.Background(), command.Env{Stdout: &stdout, Stderr: &stderr}, args)
	if err == nil || !strings.Contains(err.Error(), "tolerance") {
		t.Fatalf("error = %v, want a tolerance range error", err)
	}
}
