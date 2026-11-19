package sqlgen

import (
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/plan"
)

func TestEmitRateGolden(t *testing.T) {
	e := testEmitter(t)
	where := plan.Bin("=", plan.Col("MetricName"), plan.String("metric_filter", "http_requests_total"))
	start := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	got, err := e.EmitRate(RateQuery{Table: "otel_metrics_sum", Where: where, Start: start, End: end})
	if err != nil {
		t.Fatalf("EmitRate: %v", err)
	}
	checkGolden(t, "rate", got)

	got, err = e.EmitRate(RateQuery{Table: "otel_metrics_sum", Where: where, Start: start, End: end, GroupByService: true})
	if err != nil {
		t.Fatalf("EmitRate by service: %v", err)
	}
	checkGolden(t, "rate_service", got)
}

func TestEmitRateBucketPredicate(t *testing.T) {
	e := testEmitter(t)
	where := plan.Bin("=", plan.Col("MetricName"), plan.String("metric_filter", "http_requests_total"))
	cases := []struct {
		name  string
		start time.Time
		end   time.Time
	}{
		{"rate_bucket_inside", time.Date(2026, 11, 15, 13, 1, 0, 0, time.UTC), time.Date(2026, 11, 15, 13, 4, 0, 0, time.UTC)},
		{"rate_bucket_crossing", time.Date(2026, 11, 15, 13, 3, 0, 0, time.UTC), time.Date(2026, 11, 15, 13, 8, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.EmitRate(RateQuery{Table: "otel_metrics_sum", Where: where, Start: tc.start, End: tc.end})
			if err != nil {
				t.Fatalf("EmitRate: %v", err)
			}
			checkGolden(t, tc.name, got)
		})
	}
}

func TestEmitRateValidation(t *testing.T) {
	e := testEmitter(t)
	start := time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC)
	if _, err := e.EmitRate(RateQuery{Table: "missing", Start: start, End: start.Add(time.Minute)}); err == nil {
		t.Fatal("accepted an unknown table")
	}
	if _, err := e.EmitRate(RateQuery{Table: "otel_metrics_sum", Start: start, End: start}); err == nil {
		t.Fatal("accepted an empty range")
	}
	if _, err := e.EmitRate(RateQuery{Table: "otel_logs", Start: start, End: start.Add(time.Minute)}); err == nil {
		t.Fatal("accepted a logs table")
	}
}
