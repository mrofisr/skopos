package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/metrics"
)

func rangeValues(start, end, step string) url.Values {
	return url.Values{"start": {start}, "end": {end}, "step": {step}}
}

func TestRequestCost(t *testing.T) {
	sixHoursAgo := "0"
	now := "21600"
	tests := []struct {
		name          string
		path          string
		values        url.Values
		pointsPerSlot int
		want          int
	}{
		{name: "instant prom", path: "/api/v1/query", pointsPerSlot: 100, want: 1},
		{name: "prom range 6h 60s", path: "/api/v1/query_range", values: rangeValues(sixHoursAgo, now, "60s"), pointsPerSlot: 100, want: 4},
		{name: "prom range numeric step", path: "/api/v1/query_range", values: rangeValues(sixHoursAgo, now, "60"), pointsPerSlot: 100, want: 4},
		{name: "loki range 6h 60s", path: "/loki/api/v1/query_range", values: rangeValues(sixHoursAgo, now, "1m"), pointsPerSlot: 100, want: 4},
		{name: "tempo search 6h", path: "/api/search", values: url.Values{"start": {sixHoursAgo}, "end": {now}}, pointsPerSlot: 100, want: 4},
		{name: "missing step", path: "/api/v1/query_range", values: url.Values{"start": {sixHoursAgo}, "end": {now}}, pointsPerSlot: 100, want: 1},
		{name: "invalid time", path: "/api/v1/query_range", values: rangeValues("not-a-time", now, "60s"), pointsPerSlot: 100, want: 1},
		{name: "weighting disabled", path: "/api/v1/query_range", values: rangeValues(sixHoursAgo, now, "60s"), pointsPerSlot: 0, want: 1},
		{name: "rfc3339", path: "/api/v1/query_range", values: rangeValues("2026-11-11T00:00:00Z", "2026-11-11T06:00:00Z", "60s"), pointsPerSlot: 100, want: 4},
		{name: "huge range", path: "/api/v1/query_range", values: rangeValues("0", "86400", "15s"), pointsPerSlot: 100, want: 58},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requestCost(test.path, test.values, test.pointsPerSlot); got != test.want {
				t.Fatalf("requestCost = %d, want %d", got, test.want)
			}
		})
	}
}

func TestAdmissionRejectsRangeOverBudgetButAllowsInstant(t *testing.T) {
	httpMetrics := metrics.NewHTTP(metrics.NewRegistry("test"))
	middleware, err := newAdmissionMiddleware(config.Config{
		MaxInflight:   4,
		QueueDepth:    1,
		QueueWait:     10 * time.Millisecond,
		PointsPerSlot: 10,
	}, httpMetrics)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api/v1/query_range", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	server := httptest.NewServer(middleware.Wrap(mux))
	t.Cleanup(server.Close)

	// 6h at 60s is 361 points; at 10 points per slot it needs 37 slots, more
	// than the total budget. Waiting cannot help, so it is a bad-data error,
	// not a retryable 503.
	response, err := server.Client().Get(server.URL + "/api/v1/query_range?" + rangeValues("0", "21600", "60s").Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("range status = %d, want 400", response.StatusCode)
	}
	if got := response.Header.Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty for an over-capacity query", got)
	}

	response, err = server.Client().Get(server.URL + "/api/v1/query")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("instant status = %d, want 200", response.StatusCode)
	}
}
