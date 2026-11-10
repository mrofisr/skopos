package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateIsMonotonicPerService(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	samples := generate(now, 2, 3, 30*time.Second)
	if len(samples) != 6 {
		t.Fatalf("len = %d, want 6", len(samples))
	}
	last := map[string]int{}
	for _, sample := range samples {
		if sample.Value <= last[sample.Service] {
			t.Fatalf("%s value %d is not increasing", sample.Service, sample.Value)
		}
		last[sample.Service] = sample.Value
	}
	first := samples[0]
	if got, want := first.Time, now.Add(-time.Minute); !got.Equal(want) {
		t.Fatalf("first time = %s, want %s", got, want)
	}
	lastSample := samples[len(samples)-1]
	if got, want := lastSample.Time, now; !got.Equal(want) {
		t.Fatalf("last time = %s, want %s", got, want)
	}
}

func TestWriteSQL(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	samples := generate(now, 1, 3, 30*time.Second)
	var out bytes.Buffer
	if err := writeSQL(&out, samples); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"INSERT INTO otel_metrics_sum",
		"map('service.name', 'skopos-e2e-server-0')",
		"'skopos_e2e_requests_total'",
		"toDateTime('2026-11-10 06:59:00')",
		"toDateTime('2026-11-10 07:00:00'), 3, 0, 2, 1);",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SQL missing %q:\n%s", want, text)
		}
	}
	if got := strings.Count(text, "'skopos_e2e_requests_total'"); got != 3 {
		t.Errorf("metric appears %d times, want 3 rows", got)
	}
}

func TestWriteOpenMetrics(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	samples := generate(now, 1, 3, 30*time.Second)
	var out bytes.Buffer
	if err := writeOpenMetrics(&out, samples); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.HasPrefix(text, "# TYPE skopos_e2e_requests_total counter\n") {
		t.Fatalf("missing TYPE line:\n%s", text)
	}
	if !strings.HasSuffix(text, "# EOF\n") {
		t.Fatalf("missing EOF line:\n%s", text)
	}
	if got := strings.Count(text, "\nskopos_e2e_requests_total{"); got != 3 {
		t.Fatalf("sample lines = %d, want 3:\n%s", got, text)
	}
	for _, want := range []string{
		`skopos_e2e_requests_total{service_name="skopos-e2e-server-0"} 1 1794293940`,
		`skopos_e2e_requests_total{service_name="skopos-e2e-server-0"} 3 1794294000`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("OpenMetrics missing %q:\n%s", want, text)
		}
	}
}

func TestGenerateRejectsDegenerateCounts(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	samples := generate(now, 0, 0, 30*time.Second)
	if len(samples) != 2 {
		t.Fatalf("len = %d, want 2 (one service, two samples)", len(samples))
	}
}

func TestLogsAlternateLevel(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	entries := logs(now, 1, 3, 30*time.Second)
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3", len(entries))
	}
	for i, entry := range entries {
		want := "info"
		if i%2 == 1 {
			want = "error"
		}
		if !strings.Contains(entry.Body, `"level":"`+want+`"`) {
			t.Errorf("entry %d body = %s, want level %s", i, entry.Body, want)
		}
	}
	if got, want := entries[0].Time, now.Add(-time.Minute); !got.Equal(want) {
		t.Fatalf("first time = %s, want %s", got, want)
	}
}

func TestWriteLogSQL(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	entries := logs(now, 1, 3, 30*time.Second)
	var out bytes.Buffer
	if err := writeLogSQL(&out, entries); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"INSERT INTO otel_logs",
		"toDateTime64('2026-11-10 06:59:00.000000000', 9)",
		"map('service.name', 'skopos-e2e-server-0')",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SQL missing %q:\n%s", want, text)
		}
	}
	if strings.HasSuffix(strings.TrimSpace(text), ",") {
		t.Error("SQL does not end with a semicolon")
	}
}

func TestWriteLokiPush(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	entries := logs(now, 2, 2, 30*time.Second)
	var out bytes.Buffer
	if err := writeLokiPush(&out, entries); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("push body: %v", err)
	}
	if len(payload.Streams) != 2 {
		t.Fatalf("streams = %d, want 2", len(payload.Streams))
	}
	if got := payload.Streams[0].Stream["service_name"]; got != "skopos-e2e-server-0" {
		t.Fatalf("first service = %q", got)
	}
	if got := len(payload.Streams[0].Values); got != 2 {
		t.Fatalf("values = %d, want 2", got)
	}
	if payload.Streams[0].Values[0][1] == "" {
		t.Fatal("empty log line")
	}
}
