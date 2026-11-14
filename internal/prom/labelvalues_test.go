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

func newValuesFetcher(t *testing.T, rows map[string][]map[string]any, values map[string][]string) (*ClickHouseFetcher, *fakeClickHouse) {
	t.Helper()
	fake := &fakeClickHouse{rows: make(map[string][]map[string]any), labelValues: values}
	for table := range values {
		fake.rows[table] = []map[string]any{gaugeRow("up")}
	}
	for table, tableRows := range rows {
		fake.rows[table] = tableRows
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client, err := chclient.New(chclient.Config{BaseURL: server.URL, Database: "otel"})
	if err != nil {
		t.Fatal(err)
	}
	names := plan.DefaultTableNames()
	fetcher, err := NewClickHouseFetcher(client, plan.DefaultSchema("otel", names), names, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return fetcher, fake
}

func matchAll() Selector {
	return Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, ".*"),
	}}
}

func TestLabelValuesPushdownMergesTables(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newValuesFetcher(t,
		map[string][]map[string]any{
			names.MetricsGauge: {gaugeRow("up")},
			names.MetricsSum:   {gaugeRow("shared")},
		},
		map[string][]string{
			names.MetricsGauge: {"up", "shared"},
			names.MetricsSum:   {"shared"},
		})
	values, err := fetcher.LabelValues(context.Background(), "__name__", matchAll(),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}
	if strings.Join(values, ",") != "shared,up" {
		t.Fatalf("values = %v, want [shared up]", values)
	}
}

func TestLabelValuesPushdownKnownAndAttributeLabels(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newValuesFetcher(t,
		map[string][]map[string]any{names.MetricsGauge: {gaugeRow("up")}},
		map[string][]string{names.MetricsGauge: {"checkout"}})
	for _, name := range []string{"service_name", "level"} {
		values, err := fetcher.LabelValues(context.Background(), name,
			Selector{Matchers: []*promlabels.Matcher{
				promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
			}},
			time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 0)
		if err != nil {
			t.Fatalf("LabelValues(%s): %v", name, err)
		}
		if len(values) != 1 || values[0] != "checkout" {
			t.Fatalf("LabelValues(%s) = %v", name, values)
		}
	}
}

func TestLabelValuesPushdownErrorIsNotPartial(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newValuesFetcher(t,
		map[string][]map[string]any{
			names.MetricsGauge:     {gaugeRow("up")},
			names.MetricsSum:       {gaugeRow("up")},
			names.MetricsHistogram: {gaugeRow("up")},
		},
		map[string][]string{
			names.MetricsGauge:     {"a"},
			names.MetricsSum:       {"b"},
			names.MetricsHistogram: {"c"},
		})
	fake.failValuesAt = 2
	values, err := fetcher.LabelValues(context.Background(), "__name__", matchAll(),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 0)
	if err == nil || !strings.Contains(err.Error(), "values down") {
		t.Fatalf("error = %v, want a values query error", err)
	}
	if values != nil {
		t.Fatalf("values = %v, want no partial result", values)
	}
}

func TestLabelValuesPushdownLimit(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newValuesFetcher(t,
		map[string][]map[string]any{names.MetricsGauge: {gaugeRow("up")}},
		map[string][]string{names.MetricsGauge: {"e", "d", "c", "b", "a"}})
	values, err := fetcher.LabelValues(context.Background(), "__name__",
		Selector{Matchers: []*promlabels.Matcher{
			promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
		}},
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(values, ",") != "a,b" {
		t.Fatalf("values = %v, want [a b]", values)
	}
}

func TestLabelValuesPushdownSafety(t *testing.T) {
	nameOnly := Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchRegexp, promlabels.MetricName, "foo.*"),
	}}
	if !LabelValuesPushdown(nameOnly) {
		t.Fatal("name-only selector was not pushdown-safe")
	}
	other := Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, "service_name", "checkout"),
	}}
	if LabelValuesPushdown(other) {
		t.Fatal("non-name selector was pushdown-safe")
	}
	negative := Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchNotEqual, promlabels.MetricName, "foo"),
	}}
	if LabelValuesPushdown(negative) {
		t.Fatal("negative name selector was pushdown-safe")
	}
}

func TestLabelValuesAPIPushdown(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newValuesFetcher(t,
		map[string][]map[string]any{names.MetricsGauge: {gaugeRow("up")}},
		map[string][]string{names.MetricsGauge: {"up", "other"}})
	api := NewAPI(fakeQueryEngine{}, fetcher, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := do(t, mux, `/api/v1/label/__name__/values?match[]=%7B__name__%3D~%22.*%22%7D&start=1793491200&end=1793491500`)
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
	if strings.Join(body.Data, ",") != "other,up" {
		t.Fatalf("data = %v", body.Data)
	}
}

type recordingLabelFetcher struct {
	fakeSeriesFetcher
	called bool
}

func (f *recordingLabelFetcher) LabelValues(context.Context, string, Selector, time.Time, time.Time, int) ([]string, error) {
	f.called = true
	return []string{"should-not-be-used"}, nil
}

func TestLabelValuesNonNameMatcherFallsBack(t *testing.T) {
	fetcher := &recordingLabelFetcher{fakeSeriesFetcher: fakeSeriesFetcher{series: []Series{{
		Labels: map[string]string{"service_name": "checkout"},
	}}}}
	api := NewAPI(fakeQueryEngine{}, fetcher, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC) }
	mux := http.NewServeMux()
	api.Register(mux)
	rec := do(t, mux, `/api/v1/label/service_name/values?match[]=%7Bservice_name%3D%22checkout%22%7D&start=1793491200&end=1793491500`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if fetcher.called {
		t.Fatal("LabelValues pushdown ran for a non-name matcher")
	}
}

// A label-values query must return every distinct value in the table, not one
// arbitrary value. The old pushdown selected any(DISTINCT value) and so
// returned a single row.
func TestLabelValuesPushdownUsesGroupBy(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newValuesFetcher(t,
		map[string][]map[string]any{names.MetricsSum: {gaugeRow("skopos_e2e_requests_total")}},
		map[string][]string{names.MetricsSum: {"server-0", "server-1", "server-2"}})
	values, err := fetcher.LabelValues(context.Background(), "service_name",
		Selector{Matchers: []*promlabels.Matcher{
			promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "skopos_e2e_requests_total"),
		}},
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 0)
	if err != nil {
		t.Fatalf("LabelValues: %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("values = %v, want three values", values)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.valueBodies) == 0 {
		t.Fatal("no label-values query was issued")
	}
	body := fake.valueBodies[0]
	if strings.Contains(body, "any(") {
		t.Errorf("label-values query still uses any(): %s", body)
	}
	if !strings.Contains(body, "GROUP BY") {
		t.Errorf("label-values query has no GROUP BY: %s", body)
	}
}
