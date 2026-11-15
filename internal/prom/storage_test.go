package prom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

// fakeClickHouse answers the four metric table queries with canned JSON rows.
type fakeClickHouse struct {
	mu            sync.Mutex
	rows          map[string][]map[string]any
	labelValues   map[string][]string
	labelNames    map[string][]string
	seen          []string
	bodies        []string
	valueBodies   []string
	rateRows      []map[string]any
	failDiscovery bool
	failData      bool
	failValuesAt  int
	valueCalls    int
	failNamesAt   int
	nameCalls     int
}

func (f *fakeClickHouse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	query := string(data)
	if strings.Contains(query, "lagInFrame") {
		f.mu.Lock()
		rows := f.rateRows
		f.mu.Unlock()
		writeRows(w, query, rows)
		return
	}
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
		writeRows(w, query, rows)
		return
	}
	if strings.Contains(query, "DISTINCT") || strings.Contains(query, "AS `value`") {
		f.mu.Lock()
		f.valueCalls++
		call := f.valueCalls
		values := f.labelValues[table]
		f.valueBodies = append(f.valueBodies, query)
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
		writeRows(w, query, rows)
		return
	}
	if f.failData && (strings.Contains(query, "Value") || strings.Contains(query, "BucketCounts") || strings.Contains(query, "ValueAtQuantiles")) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"exception":"Code: 396. DB::Exception: Limit for result exceeded, max rows: 100000, current rows: 104960. (TOO_MANY_ROWS_OR_BYTES)"}`)
		return
	}
	f.mu.Lock()
	f.seen = append(f.seen, table)
	f.bodies = append(f.bodies, query)
	f.mu.Unlock()
	rows := f.rows[table]
	if strings.Contains(query, "groupArray") {
		rows = groupRows(rows)
	}
	writeRows(w, query, rows)
}

// groupRows collapses per-sample rows into the grouped shape the short-window
// query requests.
func groupRows(rows []map[string]any) []map[string]any {
	type group struct {
		metric  any
		service any
		attrs   any
		times   []any
		values  []any
	}
	order := make([]string, 0)
	groups := make(map[string]*group)
	for _, row := range rows {
		key := fmt.Sprintf("%v\x00%v\x00%v", row["MetricName"], row["ServiceName"], row["Attributes"])
		g := groups[key]
		if g == nil {
			g = &group{
				metric:  row["MetricName"],
				service: row["ServiceName"],
				attrs:   row["Attributes"],
			}
			groups[key] = g
			order = append(order, key)
		}
		g.times = append(g.times, row["TimeUnix"])
		g.values = append(g.values, row["Value"])
	}
	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		g := groups[key]
		out = append(out, map[string]any{
			"MetricName":  g.metric,
			"ServiceName": g.service,
			"Attributes":  g.attrs,
			"Times":       g.times,
			"Values":      g.values,
		})
	}
	return out
}

// writeRows answers in the format the client asked for. Compact responses use
// arrays; row-map responses keep the existing shape.
func writeRows(w http.ResponseWriter, query string, rows []map[string]any) {
	columns := make([]string, 0)
	seen := make(map[string]struct{})
	for _, row := range rows {
		for name := range row {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			columns = append(columns, name)
		}
	}
	sort.Strings(columns)
	meta := make([]map[string]string, len(columns))
	for i, name := range columns {
		meta[i] = map[string]string{"name": name, "type": ""}
	}
	w.Header().Set("Content-Type", "application/json")
	if !strings.Contains(query, "FORMAT JSONCompact") {
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": rows})
		return
	}
	data := make([][]any, len(rows))
	for i, row := range rows {
		values := make([]any, len(columns))
		for j, name := range columns {
			values[j] = row[name]
		}
		data[i] = values
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta, "data": data})
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

func dataBodies(fake *fakeClickHouse) map[string]string {
	bodies := make(map[string]string)
	for index, table := range fake.seen {
		body := fake.bodies[index]
		if strings.Contains(body, "Value") || strings.Contains(body, "BucketCounts") || strings.Contains(body, "ValueAtQuantiles") {
			bodies[table] = body
		}
	}
	return bodies
}

func TestFetchPushesServiceNameEqualMatcher(t *testing.T) {
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
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("series = %#v, want one", series)
	}
	body := dataBodies(fake)[names.MetricsGauge]
	if !strings.Contains(body, "`ServiceName` = {service_filter_0:String}") {
		t.Fatalf("service_name predicate missing:\n%s", body)
	}
	if !strings.Contains(body, "`ServiceName` = {service_empty_0:String}") {
		t.Fatalf("empty service fallback missing:\n%s", body)
	}
}

func TestFetchPushesServiceNameRegexMatcher(t *testing.T) {
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
		promlabels.MustNewMatcher(promlabels.MatchRegexp, "service_name", "check.*"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	body := dataBodies(fake)[names.MetricsGauge]
	if !strings.Contains(body, "match(`ServiceName`, {service_regex_filter_0:String})") {
		t.Fatalf("service_name regex predicate missing:\n%s", body)
	}
	if !strings.Contains(body, "`ServiceName` = {service_empty_0:String}") {
		t.Fatalf("empty service fallback missing:\n%s", body)
	}
}

func TestFetchLeavesNegativeServiceNameMatcherInGo(t *testing.T) {
	names := plan.DefaultTableNames()
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	for _, negative := range []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchNotEqual, "service_name", "cart"),
		promlabels.MustNewMatcher(promlabels.MatchNotRegexp, "service_name", "cart"),
	} {
		row := map[string]any{
			"MetricName":  "up",
			"TimeUnix":    "2026-11-01 00:10:00",
			"ServiceName": "checkout",
			"Attributes":  map[string]any{},
			"Value":       json.Number("1"),
		}
		fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
		series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
			promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
			negative,
		}}, at.Add(-time.Minute), at)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(series) != 1 {
			t.Fatalf("%s series = %#v, want one", negative, series)
		}
		body := dataBodies(fake)[names.MetricsGauge]
		if strings.Contains(body, "{service_filter") || strings.Contains(body, "{service_regex_filter") {
			t.Fatalf("%s was pushed into SQL:\n%s", negative, body)
		}
	}
}

func TestFetchUsesUniqueServiceNameParameters(t *testing.T) {
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
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "cart"),
		promlabels.MustNewMatcher(promlabels.MatchRegexp, "service_name", "check.*"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 0 {
		t.Fatalf("series = %#v, want none", series)
	}
	body := dataBodies(fake)[names.MetricsGauge]
	for _, param := range []string{
		"{service_filter_0:String}",
		"{service_filter_1:String}",
		"{service_regex_filter_0:String}",
		"{service_empty_0:String}",
		"{service_empty_1:String}",
		"{service_empty_2:String}",
	} {
		if !strings.Contains(body, param) {
			t.Fatalf("parameter %s missing:\n%s", param, body)
		}
	}
}

func TestFetchPushesServiceNameWhenNameFilterIsNil(t *testing.T) {
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
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsGauge: {gauge}, names.MetricsSum: {gauge},
		names.MetricsHistogram: {histogram}, names.MetricsSummary: {summary},
	})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "foo|bar"),
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	bodies := dataBodies(fake)
	if len(bodies) != 4 {
		t.Fatalf("data tables = %v, want all four", fake.seen)
	}
	for table, body := range bodies {
		if !strings.Contains(body, "`ServiceName` = {service_filter_0:String}") {
			t.Fatalf("table %s missing service predicate:\n%s", table, body)
		}
	}
}

func TestFetchMatchesServiceNameAttributeWhenColumnIsEmpty(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "up",
		"TimeUnix":    "2026-11-01 00:10:00",
		"ServiceName": "",
		"Attributes":  map[string]any{"service.name": "checkout"},
		"Value":       json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("series = %#v, want one", series)
	}
	if got := series[0].Labels["service_name"]; got != "checkout" {
		t.Fatalf("service_name = %q, want checkout from the attribute", got)
	}
	body := dataBodies(fake)[names.MetricsGauge]
	if !strings.Contains(body, "`ServiceName` = {service_empty_0:String}") {
		t.Fatalf("empty service fallback missing:\n%s", body)
	}
}

func TestFetchResultLimitIsBadQuery(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName": "up", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
		"Attributes": map[string]any{}, "Value": json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	fake.failData = true
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	_, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if !errors.Is(err, ErrBadQuery) {
		t.Fatalf("err = %v, want ErrBadQuery", err)
	}
	if !strings.Contains(err.Error(), "result-row limit") {
		t.Fatalf("err = %v, want the result-row limit message", err)
	}
}

func TestFetchScanLimitIsBadQuery(t *testing.T) {
	names := plan.DefaultTableNames()
	rows := make([]map[string]any, 0, 1000)
	for i := 0; i < 1000; i++ {
		rows = append(rows, map[string]any{
			"MetricName": "up", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
			"Attributes": map[string]any{}, "Value": json.Number("1"),
		})
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: rows})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	_, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if !errors.Is(err, ErrBadQuery) {
		t.Fatalf("err = %v, want ErrBadQuery", err)
	}
	if !strings.Contains(err.Error(), "scan limit") {
		t.Fatalf("err = %v, want the scan-limit message", err)
	}
}

func TestGaugeSelectorWithServiceNameGoldenSQL(t *testing.T) {
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
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := dataBodies(fake)[names.MetricsGauge]
	want, err := os.ReadFile("testdata/gauge-service.sql")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("gauge SQL with service_name changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestShortWindowGroupsSamplesBySeries(t *testing.T) {
	names := plan.DefaultTableNames()
	rows := []map[string]any{
		{"MetricName": "up", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout", "Attributes": map[string]any{}, "Value": json.Number("1")},
		{"MetricName": "up", "TimeUnix": "2026-11-01 00:09:00", "ServiceName": "checkout", "Attributes": map[string]any{}, "Value": json.Number("2")},
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: rows})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	series, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(series) != 1 || len(series[0].Samples) != 2 {
		t.Fatalf("series = %#v, want one series with two samples", series)
	}
	if series[0].Samples[0].T >= series[0].Samples[1].T {
		t.Fatalf("samples are not time-ordered: %#v", series[0].Samples)
	}
	if series[0].Samples[0].V != 2 || series[0].Samples[1].V != 1 {
		t.Fatalf("samples = %#v, want 2 then 1", series[0].Samples)
	}
	var grouped bool
	for _, body := range fake.bodies {
		if strings.Contains(body, "groupArray") {
			grouped = true
		}
	}
	if !grouped {
		t.Fatal("short-window fetch did not use groupArray")
	}
}

func TestLongWindowKeepsPerSampleQuery(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName": "up", "TimeUnix": "2026-11-01 00:10:00", "ServiceName": "checkout",
		"Attributes": map[string]any{}, "Value": json.Number("1"),
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 1, 10, 0, 0, time.UTC)
	if _, err := fetcher.Fetch(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Hour), at); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, body := range fake.bodies {
		if strings.Contains(body, "groupArray") {
			t.Fatalf("long-window fetch used groupArray:\n%s", body)
		}
	}
}
