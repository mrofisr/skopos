package prom

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/jawaracloud/skopos/internal/plan"
)

type cacheClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *cacheClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *cacheClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func gaugeRow(name string) map[string]any {
	return map[string]any{
		"MetricName":  name,
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
		"Value":       json.Number("1"),
	}
}

func fetchUp(t *testing.T, fetcher *ClickHouseFetcher) []Series {
	t.Helper()
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return series
}

func countQueries(fake *fakeClickHouse, predicate func(string) bool) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	count := 0
	for _, body := range fake.bodies {
		if predicate(body) {
			count++
		}
	}
	return count
}

func TestMetadataCacheEmptyTableNoDataScans(t *testing.T) {
	fetcher, fake := newFakeFetcher(t, nil)
	series := fetchUp(t, fetcher)
	if len(series) != 0 {
		t.Fatalf("series = %#v, want none", series)
	}
	if got := dataTables(fake); len(got) != 0 {
		t.Fatalf("data tables = %v, want none", got)
	}
}

func TestMetadataCacheExpires(t *testing.T) {
	names := plan.DefaultTableNames()
	clock := &cacheClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{})
	fetcher.MetadataTTL = time.Minute
	fetcher.Now = clock.Now

	if series := fetchUp(t, fetcher); len(series) != 0 {
		t.Fatalf("first series = %#v, want none", series)
	}
	fake.mu.Lock()
	fake.rows[names.MetricsGauge] = []map[string]any{gaugeRow("up")}
	fake.mu.Unlock()

	if series := fetchUp(t, fetcher); len(series) != 0 {
		t.Fatalf("cached series = %#v, want none before the TTL", series)
	}
	clock.Advance(time.Minute)
	if series := fetchUp(t, fetcher); len(series) != 1 {
		t.Fatalf("expired series = %#v, want one", series)
	}
}

func TestMetadataCacheErrorIsNotStaleEmpty(t *testing.T) {
	clock := &cacheClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	fetcher, fake := newFakeFetcher(t, nil)
	fetcher.MetadataTTL = time.Minute
	fetcher.Now = clock.Now
	if series := fetchUp(t, fetcher); len(series) != 0 {
		t.Fatalf("series = %#v, want none", series)
	}
	fake.mu.Lock()
	fake.failDiscovery = true
	fake.mu.Unlock()
	clock.Advance(time.Minute)

	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	_, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if err == nil || !strings.Contains(err.Error(), "discover metric") {
		t.Fatalf("error = %v, want a discovery error", err)
	}
}

func TestMetadataCacheSingleFlight(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {gaugeRow("up")}})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if series := fetchUp(t, fetcher); len(series) != 1 {
				t.Errorf("series = %d, want 1", len(series))
			}
		}()
	}
	wg.Wait()
	discoveryCalls := countQueries(fake, func(body string) bool {
		return strings.Contains(body, "metric_filter") && strings.Contains(body, "LIMIT") &&
			!strings.Contains(body, "Value") && !strings.Contains(body, "BucketCounts")
	})
	if discoveryCalls != 4 {
		t.Fatalf("discovery queries = %d, want one probe per table (4)", discoveryCalls)
	}
}

func TestMetadataCached(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {gaugeRow("up")},
	})
	first, err := fetcher.Metadata(context.Background(), "up")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if first["up"].Type != "gauge" {
		t.Fatalf("metadata = %#v", first)
	}
	second, err := fetcher.Metadata(context.Background(), "up")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if second["up"].Type != "gauge" {
		t.Fatalf("metadata = %#v", second)
	}
	metadataQueries := countQueries(fake, func(body string) bool {
		return strings.Contains(body, "MetricDescription")
	})
	if metadataQueries != 4 {
		t.Fatalf("metadata queries = %d, want one per table (4)", metadataQueries)
	}
}
