package gateway

import (
	"context"
	"net/url"
	"testing"
)

// The instant cost model must distinguish a 100k-series rate from a cheap
// instant, fall back safely when cardinality is unknown, and make weighting
// switchable. The range and search paths must keep their existing weights.
func TestInstantCostModel(t *testing.T) {
	expensive := "sum(rate(http_requests_total[5m]))"
	cheap := "up"
	sixHoursAgo := "0"
	now := "21600"

	model := func(cardinality func(context.Context, string) int, enabled bool) CostModel {
		return CostModel{
			Enabled:        enabled,
			PointsPerSlot:  100,
			RowsPerSlot:    10_000,
			FallbackWeight: 30,
			Cardinality:    cardinality,
		}
	}
	known := func(n int) func(context.Context, string) int {
		return func(context.Context, string) int { return n }
	}

	tests := []struct {
		name   string
		model  CostModel
		path   string
		values url.Values
		want   int
	}{
		{
			name:   "expensive instant rate",
			model:  model(known(100_000), true),
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   200, // 100k series * 20 samples / 10k rows per slot
		},
		{
			name:   "cheap instant keeps the minimum weight",
			model:  model(known(10), true),
			path:   "/api/v1/query",
			values: url.Values{"query": {cheap}},
			want:   1,
		},
		{
			name:   "single series keeps the minimum weight",
			model:  model(known(1), true),
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   1,
		},
		{
			name:   "unknown cardinality uses the fallback",
			model:  model(nil, true),
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   30,
		},
		{
			name:   "zero cardinality uses the fallback",
			model:  model(known(0), true),
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   30,
		},
		{
			name:   "fallback is never zero",
			model:  CostModel{Enabled: true, RowsPerSlot: 10_000, Cardinality: nil},
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   1,
		},
		{
			name: "selector labels do not hide the metric name",
			model: model(func(_ context.Context, name string) int {
				if name != "http_requests_total" {
					t.Errorf("cardinality lookup = %q, want http_requests_total", name)
				}
				return 100_000
			}, true),
			path:   "/api/v1/query",
			values: url.Values{"query": {`sum(rate(http_requests_total{job="api"}[5m]))`}},
			want:   200,
		},
		{
			name: "multiple selectors use the largest window and summed cardinality",
			model: model(func(_ context.Context, name string) int {
				switch name {
				case "a", "b":
					return 50_000
				default:
					return 0
				}
			}, true),
			path:   "/api/v1/query",
			values: url.Values{"query": {"rate(a[5m]) + sum(b)"}},
			want:   200, // 100k series * 20 samples / 10k rows per slot
		},
		{
			name:   "malformed query falls back so the handler answers 400",
			model:  model(known(100_000), true),
			path:   "/api/v1/query",
			values: url.Values{"query": {"sum(rate("}},
			want:   1,
		},
		{
			name:   "missing query falls back",
			model:  model(known(100_000), true),
			path:   "/api/v1/query",
			values: url.Values{},
			want:   1,
		},
		{
			name:   "weighting disabled makes instant count-based",
			model:  model(known(100_000), false),
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   1,
		},
		{
			name:   "weighting disabled makes range count-based",
			model:  model(known(100_000), false),
			path:   "/api/v1/query_range",
			values: rangeValues(sixHoursAgo, now, "60s"),
			want:   1,
		},
		{
			name:   "range weighting is unchanged",
			model:  model(nil, true),
			path:   "/api/v1/query_range",
			values: rangeValues(sixHoursAgo, now, "60s"),
			want:   4,
		},
		{
			name:   "tempo search weighting is unchanged",
			model:  model(nil, true),
			path:   "/api/search",
			values: url.Values{"start": {sixHoursAgo}, "end": {now}},
			want:   4,
		},
		{
			name:   "instant rowsPerSlot zero disables instant weighting",
			model:  CostModel{Enabled: true, PointsPerSlot: 100, Cardinality: known(100_000)},
			path:   "/api/v1/query",
			values: url.Values{"query": {expensive}},
			want:   1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.model.requestCost(context.Background(), test.path, test.values); got != test.want {
				t.Fatalf("requestCost = %d, want %d", got, test.want)
			}
		})
	}
}
