//go:build integration

package prom

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

type sampleOnlyFetcher struct {
	fetcher *ClickHouseFetcher
}

func (s sampleOnlyFetcher) Fetch(ctx context.Context, sel Selector, start, end time.Time) ([]Series, error) {
	return s.fetcher.Fetch(ctx, sel, start, end)
}

func TestIntegrationRatePushdownParity(t *testing.T) {
	baseURL := os.Getenv("CLICKHOUSE_URL")
	if baseURL == "" {
		t.Fatal("CLICKHOUSE_URL is required for integration tests")
	}
	user := os.Getenv("CLICKHOUSE_USER")
	password := os.Getenv("CLICKHOUSE_PASSWORD")
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	database := "skopos_rate_it_" + suffix
	admin := &http.Client{Timeout: 15 * time.Second}
	exec := func(query string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, baseURL, strings.NewReader(query))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if user != "" {
			req.SetBasicAuth(user, password)
		}
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("query failed: status %d: %s\nquery: %s", resp.StatusCode, body, query)
		}
	}
	exec("CREATE DATABASE `" + database + "`")
	t.Cleanup(func() { exec("DROP DATABASE IF EXISTS `" + database + "` SYNC") })
	for _, table := range []string{"otel_metrics_sum", "otel_metrics_gauge", "otel_metrics_histogram", "otel_metrics_summary", "otel_metrics_exponential_histogram"} {
		exec("CREATE TABLE `" + database + "`.`" + table + "` (" +
			"MetricName LowCardinality(String), TimeUnix DateTime64(9), Value Float64, " +
			"ServiceName LowCardinality(String), Attributes Map(String, String)) " +
			"ENGINE = MergeTree ORDER BY (MetricName, TimeUnix)")
	}

	rows := strings.Join([]string{
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":5,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":15,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":30,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":40,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":5,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":15,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
	}, "\n")
	exec("INSERT INTO `" + database + "`.`otel_metrics_sum` FORMAT JSONEachRow\n" + rows)

	client, err := chclient.New(chclient.Config{
		BaseURL:  baseURL,
		Database: database,
		User:     user,
		Password: password,
	})
	if err != nil {
		t.Fatalf("chclient.New: %v", err)
	}
	names := plan.DefaultTableNames()
	fetcher, err := NewClickHouseFetcher(client, plan.DefaultSchema(database, names), names, 200_000)
	if err != nil {
		t.Fatalf("NewClickHouseFetcher: %v", err)
	}
	fast := NewEngine(fetcher)
	sample := NewEngine(sampleOnlyFetcher{fetcher: fetcher})
	at := time.Date(2026, 11, 15, 12, 4, 0, 0, time.UTC)

	for _, query := range []string{
		"sum(rate(http_requests_total[5m]))",
		"sum by (service_name) (rate(http_requests_total[5m]))",
	} {
		fastValue, err := fast.Query(context.Background(), query, at)
		if err != nil {
			t.Fatalf("%s fast: %v", query, err)
		}
		sampleValue, err := sample.Query(context.Background(), query, at)
		if err != nil {
			t.Fatalf("%s sample: %v", query, err)
		}
		assertVectorsEqual(t, query, fastValue.(Vector), sampleValue.(Vector))
	}
}

func assertVectorsEqual(t *testing.T, name string, got, want Vector) {
	t.Helper()
	sort.Slice(got, func(i, j int) bool { return labelsKey(got[i].Labels) < labelsKey(got[j].Labels) })
	sort.Slice(want, func(i, j int) bool { return labelsKey(want[i].Labels) < labelsKey(want[j].Labels) })
	if len(got) != len(want) {
		t.Fatalf("%s: got %d samples, want %d (%#v vs %#v)", name, len(got), len(want), got, want)
	}
	for i := range got {
		if labelsKey(got[i].Labels) != labelsKey(want[i].Labels) {
			t.Fatalf("%s: labels %v, want %v", name, got[i].Labels, want[i].Labels)
		}
		scale := math.Max(1, math.Max(math.Abs(got[i].V), math.Abs(want[i].V)))
		if math.Abs(got[i].V-want[i].V) > 1e-9*scale {
			t.Fatalf("%s: value %v, want %v", name, got[i].V, want[i].V)
		}
	}
}
