package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

func newNamesFetcher(t *testing.T, names map[string][]string) (*ClickHouseFetcher, *fakeClickHouse) {
	t.Helper()
	fake := &fakeClickHouse{rows: make(map[string][]map[string]any), labelNames: names}
	for table := range names {
		fake.rows[table] = []map[string]any{gaugeRow("up")}
	}
	server := newFakeServer(t, fake)
	client := newTestClient(t, server.URL)
	defaults := plan.DefaultTableNames()
	fetcher, err := NewClickHouseFetcher(client, plan.DefaultSchema("otel", defaults), defaults, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return fetcher, fake
}

func labelNamesCall(t *testing.T, fetcher *ClickHouseFetcher, start, end time.Time) []string {
	t.Helper()
	values, err := fetcher.LabelNames(context.Background(), matchAll(), start, end, 0)
	if err != nil {
		t.Fatalf("LabelNames: %v", err)
	}
	return values
}

func TestLabelNamesPushdown(t *testing.T) {
	defaults := plan.DefaultTableNames()
	fetcher, _ := newNamesFetcher(t, map[string][]string{
		defaults.MetricsGauge: {"level", "host"},
		defaults.MetricsSum:   {"level"},
	})
	values := labelNamesCall(t, fetcher,
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC))
	sort.Strings(values)
	want := "__name__,host,level,service_name"
	if strings.Join(values, ",") != want {
		t.Fatalf("names = %v, want %s", values, want)
	}
}

func TestLabelNamesCacheSecondCall(t *testing.T) {
	defaults := plan.DefaultTableNames()
	fetcher, fake := newNamesFetcher(t, map[string][]string{defaults.MetricsGauge: {"level"}})
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	labelNamesCall(t, fetcher, start, end)
	fake.mu.Lock()
	first := fake.nameCalls
	fake.mu.Unlock()
	labelNamesCall(t, fetcher, start, end)
	fake.mu.Lock()
	second := fake.nameCalls
	fake.mu.Unlock()
	if first == 0 || second != first {
		t.Fatalf("label-name queries = %d then %d, want a cache hit", first, second)
	}
}

func TestLabelNamesCacheExpires(t *testing.T) {
	clock := &cacheClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	defaults := plan.DefaultTableNames()
	fetcher, fake := newNamesFetcher(t, map[string][]string{defaults.MetricsGauge: {"level"}})
	fetcher.MetadataTTL = time.Minute
	fetcher.Now = clock.Now
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	if values := labelNamesCall(t, fetcher, start, end); !strings.Contains(strings.Join(values, ","), "level") {
		t.Fatalf("initial names = %v", values)
	}
	fake.mu.Lock()
	fake.labelNames[defaults.MetricsGauge] = []string{"host"}
	fake.mu.Unlock()
	clock.Advance(30 * time.Second)
	if values := labelNamesCall(t, fetcher, start, end); strings.Contains(strings.Join(values, ","), "host") {
		t.Fatalf("cached names = %v, want no host before the TTL", values)
	}
	clock.Advance(30 * time.Second)
	if values := labelNamesCall(t, fetcher, start, end); !strings.Contains(strings.Join(values, ","), "host") {
		t.Fatalf("expired names = %v, want host", values)
	}
}

func TestLabelNamesErrorIsNotPartial(t *testing.T) {
	defaults := plan.DefaultTableNames()
	fetcher, fake := newNamesFetcher(t, map[string][]string{
		defaults.MetricsGauge: {"level"},
		defaults.MetricsSum:   {"host"},
	})
	fake.mu.Lock()
	fake.failNamesAt = 2
	fake.mu.Unlock()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	values, err := fetcher.LabelNames(context.Background(), matchAll(), start, start.Add(10*time.Minute), 0)
	if err == nil || !strings.Contains(err.Error(), "names down") {
		t.Fatalf("error = %v, want a label-names error", err)
	}
	if values != nil {
		t.Fatalf("values = %v, want no partial result", values)
	}
}

func TestLabelNamesAPIPushdown(t *testing.T) {
	defaults := plan.DefaultTableNames()
	fetcher, _ := newNamesFetcher(t, map[string][]string{defaults.MetricsGauge: {"level"}})
	api := NewAPI(fakeQueryEngine{}, fetcher, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := do(t, mux, `/api/v1/labels?match[]=%7B__name__%3D~%22.*%22%7D&start=1793491200&end=1793491500`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	sort.Strings(body.Data)
	if strings.Join(body.Data, ",") != "__name__,level,service_name" {
		t.Fatalf("data = %v", body.Data)
	}
}

func TestLabelNamesNonNameMatcherFallsBack(t *testing.T) {
	fetcher := &recordingLabelFetcher{fakeSeriesFetcher: fakeSeriesFetcher{series: []Series{{
		Labels: map[string]string{"service_name": "checkout"},
	}}}}
	api := NewAPI(fakeQueryEngine{}, fetcher, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := do(t, mux, `/api/v1/labels?match[]=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if fetcher.called {
		t.Fatal("LabelNames pushdown ran for a non-name matcher")
	}
}

func newFakeServer(t *testing.T, fake *fakeClickHouse) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return server
}

func newTestClient(t *testing.T, baseURL string) *chclient.Client {
	t.Helper()
	client, err := chclient.New(chclient.Config{BaseURL: baseURL, Database: "otel"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestLabelNamesCacheKey(t *testing.T) {
	defaults := plan.DefaultTableNames()
	clock := &cacheClock{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	fetcher, fake := newNamesFetcher(t, map[string][]string{defaults.MetricsGauge: {"level"}})
	fetcher.MetadataTTL = time.Minute
	fetcher.Now = clock.Now
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	labelNamesCall(t, fetcher, start, end)
	fake.mu.Lock()
	first := fake.nameCalls
	fake.mu.Unlock()

	// A different time bucket misses the cache.
	labelNamesCall(t, fetcher, start.Add(2*time.Minute), end.Add(2*time.Minute))
	fake.mu.Lock()
	second := fake.nameCalls
	fake.mu.Unlock()
	if second <= first {
		t.Fatalf("different time bucket reused the cache: %d then %d", first, second)
	}

	// A different selector misses the cache.
	selector := Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}
	if _, err := fetcher.LabelNames(context.Background(), selector, start, end, 0); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	third := fake.nameCalls
	fake.mu.Unlock()
	if third <= second {
		t.Fatalf("different selector reused the cache: %d then %d", second, third)
	}
}
