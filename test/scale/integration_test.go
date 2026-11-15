//go:build integration

package scale

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIntegrationScaleGen(t *testing.T) {
	baseURL := os.Getenv("CLICKHOUSE_URL")
	if baseURL == "" {
		t.Fatal("CLICKHOUSE_URL is required for integration tests")
	}
	user := os.Getenv("CLICKHOUSE_USER")
	password := os.Getenv("CLICKHOUSE_PASSWORD")
	database := fmt.Sprintf("skopos_scale_%d", time.Now().UnixNano())

	exec := func(query string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, baseURL, strings.NewReader(query))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if user != "" {
			req.SetBasicAuth(user, password)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("query failed: status %d: %s\nquery: %s", resp.StatusCode, body, query)
		}
		return strings.TrimSpace(string(body))
	}

	exec("CREATE DATABASE `" + database + "`")
	t.Cleanup(func() { exec("DROP DATABASE IF EXISTS `" + database + "` SYNC") })
	ddl := []string{
		"CREATE TABLE `" + database + "`.`otel_metrics_sum` (" +
			"ResourceAttributes Map(String,String), ScopeName String, ScopeVersion String, " +
			"ServiceName String, MetricName String, MetricDescription String, MetricUnit String, " +
			"Attributes Map(String,String), StartTimeUnix DateTime, TimeUnix DateTime, Value Float64, " +
			"Flags UInt32, AggregationTemporality Int32, IsMonotonic UInt8) " +
			"ENGINE = MergeTree ORDER BY (ServiceName, MetricName, TimeUnix)",
		"CREATE TABLE `" + database + "`.`otel_metrics_histogram` (" +
			"ResourceAttributes Map(String,String), ScopeName String, ScopeVersion String, " +
			"ServiceName String, MetricName String, MetricDescription String, MetricUnit String, " +
			"Attributes Map(String,String), StartTimeUnix DateTime, TimeUnix DateTime, Count UInt64, " +
			"Sum Float64, BucketCounts Array(UInt64), ExplicitBounds Array(Float64), Flags UInt32, " +
			"Min Float64, Max Float64, AggregationTemporality Int32) " +
			"ENGINE = MergeTree ORDER BY (ServiceName, MetricName, TimeUnix)",
		"CREATE TABLE `" + database + "`.`otel_logs` (" +
			"Timestamp DateTime64(9), TraceId String, SpanId String, TraceFlags UInt8, " +
			"SeverityText String, SeverityNumber UInt8, ServiceName String, Body String, " +
			"ResourceSchemaUrl String, ResourceAttributes Map(String,String), ScopeSchemaUrl String, " +
			"ScopeName String, ScopeVersion String, ScopeAttributes Map(String,String), " +
			"LogAttributes Map(String,String), EventName String) ENGINE = MergeTree ORDER BY Timestamp",
		"CREATE TABLE `" + database + "`.`otel_traces` (" +
			"Timestamp DateTime64(9), TraceId String, SpanId String, ParentSpanId String, TraceState String, " +
			"SpanName String, SpanKind String, ServiceName String, ResourceAttributes Map(String,String), " +
			"ScopeName String, ScopeVersion String, SpanAttributes Map(String,String), Duration UInt64, " +
			"StatusCode String, StatusMessage String) ENGINE = MergeTree ORDER BY Timestamp",
	}
	for _, statement := range ddl {
		exec(statement)
	}

	var out, errOut strings.Builder
	err := Run(context.Background(), Env{Getenv: os.Getenv, Now: func() time.Time {
		return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	}}, []string{
		"--series", "10",
		"--services", "2",
		"--range", "30s",
		"--step", "15s",
		"--logs", "5",
		"--spans", "5",
		"--batch", "3",
		"--database", database,
	}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, errOut.String())
	}

	for table, want := range map[string]string{
		"otel_metrics_sum":       "20",
		"otel_metrics_histogram": "4",
		"otel_logs":              "5",
		"otel_traces":            "5",
	} {
		got := exec("SELECT count() FROM `" + database + "`.`" + table + "`")
		if got != want {
			t.Fatalf("%s rows = %s, want %s\n%s", table, got, want, out.String())
		}
	}
}
