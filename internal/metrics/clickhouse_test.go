package metrics

import (
	"testing"
	"time"
)

func TestClickHouseMetrics(t *testing.T) {
	registry := NewRegistry("test")
	clickhouse := NewClickHouse(registry)
	clickhouse.QueryDone("prom", "ok", 25*time.Millisecond, 10, 100)
	clickhouse.QueryDone("prom", "error", 5*time.Millisecond, 0, 0)
	clickhouse.FinalFallback("prom", "retried")
	clickhouse.FinalFallback("loki", "skipped")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]float64)
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			key := family.GetName()
			for _, label := range metric.GetLabel() {
				key += "/" + label.GetName() + "=" + label.GetValue()
			}
			switch {
			case metric.GetCounter() != nil:
				values[key] = metric.GetCounter().GetValue()
			case metric.GetHistogram() != nil:
				values[key] = float64(metric.GetHistogram().GetSampleCount())
			}
		}
	}
	for key, want := range map[string]float64{
		"skopos_clickhouse_queries_total/head=prom/result=ok":             1,
		"skopos_clickhouse_queries_total/head=prom/result=error":          1,
		"skopos_clickhouse_read_rows_total/head=prom":                     10,
		"skopos_clickhouse_read_bytes_total/head=prom":                    100,
		"skopos_clickhouse_query_duration_seconds/head=prom":              2,
		"skopos_clickhouse_final_fallback_total/action=retried/head=prom": 1,
		"skopos_clickhouse_final_fallback_total/action=skipped/head=loki": 1,
	} {
		if values[key] != want {
			t.Errorf("%s = %v, want %v", key, values[key], want)
		}
	}
}
