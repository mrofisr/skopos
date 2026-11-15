package prom

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/plan"
)

func seriesBody(fake *fakeClickHouse, table string) string {
	for i, seen := range fake.seen {
		if seen == table && strings.Contains(fake.bodies[i], "GROUP BY") {
			return fake.bodies[i]
		}
	}
	return ""
}

func TestListSeriesGaugeGoldenSQL(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "up",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	labels, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(labels) != 1 || labels[0]["__name__"] != "up" || labels[0]["service_name"] != "checkout" {
		t.Fatalf("labels = %#v", labels)
	}
	got := seriesBody(fake, names.MetricsGauge)
	want, err := os.ReadFile("testdata/series-gauge.sql")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("series SQL changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestListSeriesPushesServiceName(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":  "up",
		"ServiceName": "checkout",
		"Attributes":  map[string]any{},
	}
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	if _, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}, at.Add(-time.Minute), at); err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	body := seriesBody(fake, names.MetricsGauge)
	if !strings.Contains(body, "{service_filter_0:String}") {
		t.Fatalf("service filter missing:\n%s", body)
	}
}

func TestListSeriesHistogramExpansion(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":     "rpc_duration",
		"ServiceName":    "checkout",
		"Attributes":     map[string]any{},
		"ExplicitBounds": []any{json.Number("0.1"), json.Number("0.5")},
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	labels, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "rpc_duration.*"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	got := make(map[string]bool, len(labels))
	for _, label := range labels {
		got[label["__name__"]+"/"+label["le"]] = true
	}
	for _, want := range []string{
		"rpc_duration_bucket/0.1",
		"rpc_duration_bucket/0.5",
		"rpc_duration_bucket/+Inf",
		"rpc_duration_count/",
		"rpc_duration_sum/",
	} {
		if !got[want] {
			t.Fatalf("missing %q in %#v", want, labels)
		}
	}
	if len(labels) != 5 {
		t.Fatalf("labels = %#v, want 5", labels)
	}
}

func TestListSeriesSummaryExpansion(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":                "rpc_duration",
		"ServiceName":               "checkout",
		"Attributes":                map[string]any{},
		"ValueAtQuantiles.Quantile": []any{json.Number("0.5"), json.Number("0.99")},
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsSummary: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	labels, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "rpc_duration.*"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	got := make(map[string]bool, len(labels))
	for _, label := range labels {
		got[label["__name__"]+"/"+label["quantile"]] = true
	}
	for _, want := range []string{
		"rpc_duration/0.5",
		"rpc_duration/0.99",
		"rpc_duration_count/",
		"rpc_duration_sum/",
	} {
		if !got[want] {
			t.Fatalf("missing %q in %#v", want, labels)
		}
	}
	if len(labels) != 4 {
		t.Fatalf("labels = %#v, want 4", labels)
	}
}

func TestListSeriesEmptyHistogramBounds(t *testing.T) {
	names := plan.DefaultTableNames()
	row := map[string]any{
		"MetricName":     "rpc_duration",
		"ServiceName":    "checkout",
		"Attributes":     map[string]any{},
		"ExplicitBounds": []any{},
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsHistogram: {row}})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	labels, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "rpc_duration.*"),
	}}, at.Add(-time.Minute), at)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	got := make(map[string]bool, len(labels))
	for _, label := range labels {
		got[label["__name__"]+"/"+label["le"]] = true
	}
	for _, want := range []string{"rpc_duration_bucket/+Inf", "rpc_duration_count/", "rpc_duration_sum/"} {
		if !got[want] {
			t.Fatalf("missing %q in %#v", want, labels)
		}
	}
	if len(labels) != 3 {
		t.Fatalf("labels = %#v, want 3", labels)
	}
}

func TestListSeriesScanLimitIsBadQuery(t *testing.T) {
	names := plan.DefaultTableNames()
	rows := make([]map[string]any, 0, 1000)
	for i := 0; i < 1000; i++ {
		rows = append(rows, map[string]any{
			"MetricName": "up", "ServiceName": "checkout", "Attributes": map[string]any{},
		})
	}
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{names.MetricsGauge: rows})
	at := time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC)
	_, err := fetcher.ListSeries(context.Background(), Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
	}}, at.Add(-time.Minute), at)
	if !errors.Is(err, ErrBadQuery) {
		t.Fatalf("err = %v, want ErrBadQuery", err)
	}
}
