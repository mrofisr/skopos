package prom

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/plan"
)

func rateSelector(name string) Selector {
	return Selector{Matchers: []*promlabels.Matcher{
		promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, name),
	}}
}

func TestFetchRate(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, fake := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsSum: {{"MetricName": "up"}},
	})
	start := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	fake.rateRows = []map[string]any{{"SeriesCount": json.Number("2"), "value": json.Number("2.5")}}
	vector, err := fetcher.FetchRate(context.Background(), rateSelector("up"), start, end, false)
	if err != nil {
		t.Fatalf("FetchRate: %v", err)
	}
	if len(vector) != 1 || vector[0].V != 2.5 || vector[0].T != end.UnixMilli() || len(vector[0].Labels) != 0 {
		t.Fatalf("vector = %#v", vector)
	}

	fake.rateRows = []map[string]any{
		{"GroupName": "checkout", "SeriesCount": json.Number("2"), "value": json.Number("2.5")},
		{"GroupName": "", "SeriesCount": json.Number("1"), "value": json.Number("1.5")},
	}
	vector, err = fetcher.FetchRate(context.Background(), rateSelector("up"), start, end, true)
	if err != nil {
		t.Fatalf("FetchRate by service: %v", err)
	}
	if len(vector) != 2 {
		t.Fatalf("by-service vector = %#v", vector)
	}
	var checkout, empty *VectorSample
	for i := range vector {
		switch vector[i].Labels["service_name"] {
		case "checkout":
			checkout = &vector[i]
		case "":
			empty = &vector[i]
		}
	}
	if checkout == nil || checkout.V != 2.5 {
		t.Fatalf("checkout = %#v", checkout)
	}
	if empty == nil || empty.V != 1.5 || len(empty.Labels) != 0 {
		t.Fatalf("empty service = %#v", empty)
	}

	fake.rateRows = []map[string]any{{"SeriesCount": json.Number("0"), "value": json.Number("0")}}
	vector, err = fetcher.FetchRate(context.Background(), rateSelector("up"), start, end, false)
	if err != nil {
		t.Fatalf("FetchRate empty: %v", err)
	}
	if len(vector) != 0 {
		t.Fatalf("empty vector = %#v", vector)
	}
}

func TestFetchRateUnsupported(t *testing.T) {
	names := plan.DefaultTableNames()
	fetcher, _ := newFakeFetcher(t, map[string][]map[string]any{
		names.MetricsSum: {{"MetricName": "up"}},
	})
	start := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		sel  Selector
		end  time.Time
	}{
		{"other matcher", Selector{Matchers: []*promlabels.Matcher{
			promlabels.MustNewMatcher(promlabels.MatchEqual, promlabels.MetricName, "up"),
			promlabels.MustNewMatcher(promlabels.MatchEqual, "job", "api"),
		}}, start.Add(5 * time.Minute)},
		{"long range", rateSelector("up"), start.Add(16 * time.Minute)},
		{"histogram table", rateSelector("up_bucket"), start.Add(5 * time.Minute)},
	} {
		_, err := fetcher.FetchRate(context.Background(), tc.sel, start, tc.end, false)
		if !errors.Is(err, ErrRateUnsupported) {
			t.Errorf("%s: error = %v, want ErrRateUnsupported", tc.name, err)
		}
	}
}
