package prom

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/jawaracloud/skopos/internal/chclient"
	"github.com/jawaracloud/skopos/internal/plan"
)

// fakeClickHouse answers the four metric table queries with canned JSON rows.
type fakeClickHouse struct {
	mu            sync.Mutex
	rows          map[string][]map[string]any
	labelValues   map[string][]string
	labelNames    map[string][]string
	seen          []string
	bodies        []string
	failDiscovery bool
	failValuesAt  int
	valueCalls    int
	failNamesAt   int
	nameCalls     int
}

func (f *fakeClickHouse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	query := string(data)
	table := ""
	for _, name := range []string{
		plan.DefaultTableNames().MetricsGauge,
		plan.DefaultTableNames().MetricsSum,
		plan.DefaultTableNames().MetricsHistogram,
		plan.DefaultTableNames().MetricsSummary,
		plan.DefaultTableNames().MetricsExponentialHistogram,
	} {
		if strings.Contains(query, "`"+name+"`") {
			table = name
			break
		}
	}
	if f.failDiscovery && strings.Contains(query, "metric_filter") && strings.Contains(query, "LIMIT") {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"exception":"discovery down"}`)
		return
	}
	if strings.Contains(query, "mapKeys") {
		f.mu.Lock()
		f.nameCalls++
		call := f.nameCalls
		names := f.labelNames[table]
		f.mu.Unlock()
		if f.failNamesAt > 0 && call == f.failNamesAt {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"exception":"names down"}`)
			return
		}
		rows := make([]map[string]any, 0, len(names))
		for _, name := range names {
			rows = append(rows, map[string]any{"name": name})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": []map[string]string{{"name": "name", "type": "String"}},
			"data": rows,
		})
		return
	}
	if strings.Contains(query, "DISTINCT") {
		f.mu.Lock()
		f.valueCalls++
		call := f.valueCalls
		values := f.labelValues[table]
		f.mu.Unlock()
		if f.failValuesAt > 0 && call == f.failValuesAt {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"exception":"values down"}`)
			return
		}
		rows := make([]map[string]any, 0, len(values))
		for _, value := range values {
			rows = append(rows, map[string]any{"value": value})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": []map[string]string{{"name": "value", "type": "String"}},
			"data": rows,
		})
		return
	}
	f.mu.Lock()
	f.seen = append(f.seen, table)
	f.bodies = append(f.bodies, query)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"meta": []map[string]string{{"name": "MetricName", "type": "String"}},
		"data": f.rows[table],
	})
}

func newFakeFetcher(t *testing.T, rows map[string][]map[string]any) (*ClickHouseFetcher, *fakeClickHouse) {
	t.Helper()
	fake := &fakeClickHouse{rows: rows}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client, err := chclient.New(chclient.Config{BaseURL: server.URL, Database: "otel"})
	if err != nil {
		t.Fatalf("chclient.New: %v", err)
	}
	names := plan.DefaultTableNames()
	fetcher, err := NewClickHouseFetcher(client, plan.DefaultSchema("otel", names), names, 1000)
	if err != nil {
		t.Fatalf("NewClickHouseFetcher: %v", err)
	}
	return fetcher, fake
}

func TestFetchSummaryExpandsPrometheusSeries(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":                "rpc_duration",
		"TimeUnix":                  "2026-11-01 00:10:00",
		"ServiceName":               "checkout",
		"Attributes":                map[string]any{"server": "a"},
		"Count":                     json.Number("10"),
		"Sum":                       json.Number("1.5"),
		"ValueAtQuantiles.Quantile": []any{json.Number("0.5"), json.Number("0.99")},
		"ValueAtQuantiles.Value":    []any{json.Number("0.2"), json.Number("0.4")},
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsSummary: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)

	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "rpc_duration"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("series = %#v, want two quantile series", series)
	}
	sort.Slice(series, func(i, j int) bool { return series[i].Labels["quantile"] < series[j].Labels["quantile"] })
	if got := series[0].Labels["quantile"]; got != "0.5" {
		t.Fatalf("quantile label = %q, want 0.5", got)
	}
	if got := series[0].Samples[0].V; got != 0.2 {
		t.Fatalf("quantile value = %v, want 0.2", got)
	}
	if series[0].Labels["server"] != "a" || series[0].Labels["service_name"] != "checkout" {
		t.Fatalf("labels = %#v", series[0].Labels)
	}

	series, err = fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "rpc_duration_count"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch count: %v", err)
	}
	if len(series) != 1 || series[0].Samples[0].V != 10 {
		t.Fatalf("count series = %#v", series)
	}
	if got := series[0].Labels["__name__"]; got != "rpc_duration_count" {
		t.Fatalf("count name = %q", got)
	}
	if len(fake.seen) == 0 {
		t.Fatal("fetcher made no queries")
	}
}

func TestFetchExponentialHistogramReturnsExplicitError(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName": "rpc_latency",
		"TimeUnix":   "2026-11-01 00:10:00",
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsExponentialHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	_, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "rpc_latency"),
	}}, at.Add(-time.Minute), at)
	if err == nil {
		t.Fatal("expected an explicit exponential histogram error")
	}
	if !strings.Contains(err.Error(), "exponential histogram") || !strings.Contains(err.Error(), "rpc_latency") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchExponentialHistogramRespectsNonNameMatchers(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "rpc_latency",
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsExponentialHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	selector := Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "rpc_latency"),
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "other"),
	}}
	if _, err := fetcher.Fetch(context.Background(), selector, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("non-matching label selector should not error: %v", err)
	}
	selector.Matchers[1] = promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout")
	if _, err := fetcher.Fetch(context.Background(), selector, at.Add(-time.Minute), at); err == nil {
		t.Fatal("expected an exponential histogram error for the matching selector")
	}
}

func TestFetchEmptySelectorDoesNotProbeExponentialHistogram(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{"MetricName": "rpc_latency", "TimeUnix": "2026-11-01 00:10:00"}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsExponentialHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("series = %#v", series)
	}
	if strings.Contains(strings.Join(fake.seen, ","), names.MetricsExponentialHistogram) {
		t.Fatalf("empty selector probed the exponential histogram table: %v", fake.seen)
	}
}

func dataTables(fake *fakeClickHouse) []string {
	var tables []string
	for index, body := range fake.bodies {
		if strings.Contains(body, "MetricDescription") {
			continue
		}
		if strings.Contains(body, "Value") || strings.Contains(body, "BucketCounts") || strings.Contains(body, "ValueAtQuantiles") {
			tables = append(tables, fake.seen[index])
		}
	}
	return tables
}

func TestGaugeSelectorDoesNotTouchHistogramOrSummary(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "up",
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
		"Value":       json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("series = %#v", series)
	}
	if got := dataTables(fake); len(got) != 1 || got[0] != names.MetricsGauge {
		t.Fatalf("data tables = %v, want only the gauge table", got)
	}
}

func TestUnknownMetricReadsZeroRows(t *testing.T) {
	fetcher, fake := newFakeFetcher(t, nil)
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "missing"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("series = %#v, want none", series)
	}
	if got := dataTables(fake); len(got) != 0 {
		t.Fatalf("data tables = %v, want no data scans", got)
	}
}

func TestDerivedHistogramNameRoutesToHistogram(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":     "rpc_duration",
		"TimeUnix":       "2026-11-01 00:10:00",
		"ServiceName":    "checkout",
		"Attributes":     map[string]any{},
		"Count":          json.Number("1"),
		"Sum":            json.Number("1"),
		"BucketCounts":   []any{json.Number("1")},
		"ExplicitBounds": []any{},
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "rpc_duration_bucket"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := dataTables(fake); len(got) != 1 || got[0] != names.MetricsHistogram {
		t.Fatalf("data tables = %v, want only the histogram table", got)
	}
}

func TestNamePresentInTwoTablesQueriesBoth(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "shared_metric",
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
		"Value":       json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {row},
		names.MetricsSum:   {row},
	})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "shared_metric"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := dataTables(fake)
	if len(got) != 2 || got[0] != names.MetricsGauge || got[1] != names.MetricsSum {
		t.Fatalf("data tables = %v, want gauge and sum", got)
	}
}

func TestRegexAndNegativeMatchersFallBackToAllTables(t *testing.T) {
	names := plan.DefaultTableNames()
	gauge := map[string]any{
		"MetricName": "foo", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
		"Attributes": map[string]any{}, "Value": json.Number("1"),
	}
	histogram := map[string]any{
		"MetricName": "foo", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
		"Attributes": map[string]any{}, "Count": json.Number("1"), "Sum": json.Number("1"),
		"BucketCounts": []any{json.Number("1")}, "ExplicitBounds": []any{},
	}
	summary := map[string]any{
		"MetricName": "foo", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
		"Attributes": map[string]any{}, "Count": json.Number("1"), "Sum": json.Number("1"),
		"ValueAtQuantiles.Quantile": []any{json.Number("0.5")},
		"ValueAtQuantiles.Value":    []any{json.Number("1")},
	}
	rows := map[string][]map[string]any{
		names.MetricsGauge: {gauge}, names.MetricsSum: {gauge},
		names.MetricsHistogram: {histogram}, names.MetricsSummary: {summary},
	}
	for _, matcher := range []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "foo|bar"),
		promlabels.MustNewMatcher(promlabels.MatchNotEqual, promlabels.MetricName, "foo"),
	} {
		fetcher, fake := newFakeFetcher(t, rows)
		at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
		if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{matcher}}, at.Add(-time.Minute), at); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := dataTables(fake); len(got) != 4 {
			t.Fatalf("matcher %s data tables = %v, want all four", matcher, got)
		}
	}
}

func TestGaugeSelectorGoldenSQL(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "up",
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
		"Value":       json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := ""
	for index, body := range fake.bodies {
		if fake.seen[index] == names.MetricsGauge && strings.Contains(body, "Value") {
			got = body
		}
	}
	if got == "" {
		t.Fatal("no gauge data query recorded")
	}
	want, err := os.ReadFile("testdata/gauge.sql")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("gauge SQL changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
