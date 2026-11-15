package prom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/plan"
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

type countingEngine struct {
	mu    sync.Mutex
	calls int
	at    []time.Time
	value Value
	err   error
}

func (e *countingEngine) Query(_ context.Context, _ string, at time.Time) (Value, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.at = append(e.at, at)
	return e.value, e.err
}

func (e *countingEngine) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) (Matrix, error) {
	return nil, nil
}

func (e *countingEngine) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func cacheAPI(t *testing.T, engine QueryEngine, now func() time.Time, ttl time.Duration) http.Handler {
	t.Helper()
	api := NewAPI(engine, fakeSeriesFetcher{}, "test-version")
	api.Now = now
	api.Timeout = time.Second
	api.InstantCacheTTL = ttl
	mux := http.NewServeMux()
	api.Register(mux)
	return mux
}

func TestInstantCacheExplicitTime(t *testing.T) {
	engine := &countingEngine{value: Scalar(1)}
	h := cacheAPI(t, engine, time.Now, time.Second)
	path := "/api/v1/query?query=up&time=2026-11-15T12:00:00Z"
	do(t, h, path)
	do(t, h, path)
	if got := engine.count(); got != 1 {
		t.Fatalf("engine calls = %d, want 1 for the same explicit time", got)
	}
	do(t, h, "/api/v1/query?query=up&time=2026-11-15T12:00:01Z")
	if got := engine.count(); got != 2 {
		t.Fatalf("engine calls = %d, want 2 for a new time", got)
	}
}

func TestInstantCacheRoundsNoTime(t *testing.T) {
	clock := &cacheClock{now: time.Date(2026, 11, 15, 12, 0, 0, 100_000_000, time.UTC)}
	engine := &countingEngine{value: Scalar(1)}
	h := cacheAPI(t, engine, clock.Now, time.Second)
	path := "/api/v1/query?query=up"
	do(t, h, path)
	do(t, h, path)
	if got := engine.count(); got != 1 {
		t.Fatalf("engine calls = %d, want 1 inside the second", got)
	}
	if got := engine.at[0]; !got.Equal(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("evaluation time = %s, want the start of the second", got)
	}

	clock.Advance(1100 * time.Millisecond)
	do(t, h, path)
	if got := engine.count(); got != 2 {
		t.Fatalf("engine calls = %d, want 2 after the TTL", got)
	}
}

func TestInstantCacheDisabledByDefault(t *testing.T) {
	engine := &countingEngine{value: Scalar(1)}
	h := cacheAPI(t, engine, time.Now, 0)
	do(t, h, "/api/v1/query?query=up")
	do(t, h, "/api/v1/query?query=up")
	if got := engine.count(); got != 2 {
		t.Fatalf("engine calls = %d, want 2 with the cache disabled", got)
	}
}

func TestInstantCacheDoesNotCacheErrors(t *testing.T) {
	engine := &countingEngine{err: errors.New("boom")}
	h := cacheAPI(t, engine, time.Now, time.Second)
	do(t, h, "/api/v1/query?query=up")
	do(t, h, "/api/v1/query?query=up")
	if got := engine.count(); got != 2 {
		t.Fatalf("engine calls = %d, want 2 for an error", got)
	}
}

func TestInstantCacheKeysByQuery(t *testing.T) {
	engine := &countingEngine{value: Scalar(1)}
	h := cacheAPI(t, engine, time.Now, time.Second)
	do(t, h, "/api/v1/query?query=up")
	do(t, h, "/api/v1/query?query=other")
	if got := engine.count(); got != 2 {
		t.Fatalf("engine calls = %d, want 2 for two queries", got)
	}
}
