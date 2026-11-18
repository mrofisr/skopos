package prom

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

func metadataRow(name string, series int) map[string]any {
	return map[string]any{
		"MetricName":        name,
		"MetricDescription": "help text",
		"MetricUnit":        "",
		"SeriesCount":       json.Number(strconv.Itoa(series)),
	}
}

func metadataStatements(fake *fakeClickHouse) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.bodies...)
}

// The cardinality estimate must be part of the cached metadata refresh: one
// grouped query per metrics table, within the existing result-row budget, and
// no statement on a cache hit.
func TestCardinalityEstimatePiggybacksOnMetadataRefresh(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {metadataRow("up", 100_000)},
	})

	if got := fetcher.Cardinality(context.Background(), "up"); got != 100_000 {
		t.Fatalf("Cardinality = %d, want 100000", got)
	}
	statements := metadataStatements(fake)
	if len(statements) != 4 {
		t.Fatalf("metadata refresh issued %d statements, want 4 (one per metrics table)", len(statements))
	}
	withSeriesCount := countQueries(fake, func(body string) bool {
		return strings.Contains(body, "uniqExact") && strings.Contains(body, "SeriesCount")
	})
	if withSeriesCount != 4 {
		t.Fatalf("statements carrying the cardinality expression = %d, want 4", withSeriesCount)
	}
	for _, body := range statements {
		if !strings.Contains(body, "__limit") {
			t.Fatalf("metadata statement is not row-bounded:\n%s", body)
		}
	}
}

func TestCardinalityCacheHitAddsNoStatement(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {metadataRow("up", 100_000)},
	})
	ctx := context.Background()
	if got := fetcher.Cardinality(ctx, "up"); got != 100_000 {
		t.Fatalf("first Cardinality = %d, want 100000", got)
	}
	before := len(metadataStatements(fake))
	if _, err := fetcher.Metadata(ctx, "up"); err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if got := fetcher.Cardinality(ctx, "up"); got != 100_000 {
		t.Fatalf("cached Cardinality = %d, want 100000", got)
	}
	if after := len(metadataStatements(fake)); after != before {
		t.Fatalf("cache hits issued %d extra statements", after-before)
	}
}

func TestCardinalityAddsNoRecorderStatement(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {metadataRow("up", 100_000)},
	})
	recorder := &chclient.Recorder{}
	ctx := chclient.WithRecorder(context.Background(), recorder)
	if got := fetcher.Cardinality(ctx, "up"); got != 100_000 {
		t.Fatalf("Cardinality = %d, want 100000", got)
	}
	statements := recorder.Statements()
	if len(statements) == 0 {
		t.Fatal("metadata refresh issued no recorded statements")
	}
	for _, record := range statements {
		if !strings.Contains(record.SQL, "uniqExact") {
			t.Fatalf("unexpected non-metadata statement in the refresh:\n%s", record.SQL)
		}
	}
	before := len(recorder.Statements())
	if got := fetcher.Cardinality(ctx, "up"); got != 100_000 {
		t.Fatalf("cached Cardinality = %d, want 100000", got)
	}
	if after := len(recorder.Statements()); after != before {
		t.Fatalf("a cache hit issued %d extra statements", after-before)
	}
}

func TestCardinalityColdCacheIsUnknown(t *testing.T) {
	fetcher, _ := newFakeFetcher(t, nil)
	if got := fetcher.Cardinality(context.Background(), "missing"); got != 0 {
		t.Fatalf("cold Cardinality = %d, want 0 (unknown, use the fallback weight)", got)
	}
}

func TestCardinalityNormalizesMetricName(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {metadataRow("http_requests_total", 42)},
	})
	if got := fetcher.Cardinality(context.Background(), "http.requests.total"); got != 42 {
		t.Fatalf("Cardinality = %d, want 42", got)
	}
}
