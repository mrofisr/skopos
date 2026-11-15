package prom

import (
	"context"
	"errors"
	"testing"
	"time"
)

type rateFetcherStub struct {
	rateCalls  int
	fetchCalls int
	byService  bool
	start, end time.Time
	vector     Vector
	rateErr    error
	samples    []Series
}

func (f *rateFetcherStub) Fetch(context.Context, Selector, time.Time, time.Time) ([]Series, error) {
	f.fetchCalls++
	return f.samples, nil
}

func (f *rateFetcherStub) FetchRate(_ context.Context, _ Selector, start, end time.Time, byService bool) (Vector, error) {
	f.rateCalls++
	f.start, f.end, f.byService = start, end, byService
	return f.vector, f.rateErr
}

func TestEngineRatePushdown(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fetcher := &rateFetcherStub{vector: Vector{{Labels: map[string]string{}, V: 42}}}
	engine := NewEngine(fetcher)

	value, err := engine.Query(context.Background(), "sum(rate(up[5m]))", at)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if fetcher.rateCalls != 1 || fetcher.fetchCalls != 0 {
		t.Fatalf("calls = rate %d fetch %d, want rate 1 fetch 0", fetcher.rateCalls, fetcher.fetchCalls)
	}
	if fetcher.byService {
		t.Fatal("no-group query asked for service grouping")
	}
	if !fetcher.start.Equal(at.Add(-5*time.Minute)) || !fetcher.end.Equal(at) {
		t.Fatalf("window = %s..%s", fetcher.start, fetcher.end)
	}
	vector, ok := value.(Vector)
	if !ok || len(vector) != 1 || vector[0].V != 42 {
		t.Fatalf("value = %#v", value)
	}
	if vector[0].T != at.UnixMilli() || len(vector[0].Labels) != 0 {
		t.Fatalf("sample = %#v", vector[0])
	}
}

func TestEngineRatePushdownByService(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fetcher := &rateFetcherStub{vector: Vector{{
		Labels: map[string]string{"service_name": "checkout"}, V: 2,
	}}}
	engine := NewEngine(fetcher)
	value, err := engine.Query(context.Background(), "sum by (service_name) (rate(up[5m]))", at)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !fetcher.byService || fetcher.rateCalls != 1 {
		t.Fatalf("byService = %v, rate calls = %d", fetcher.byService, fetcher.rateCalls)
	}
	vector := value.(Vector)
	if len(vector) != 1 || vector[0].Labels["service_name"] != "checkout" || vector[0].T != at.UnixMilli() {
		t.Fatalf("value = %#v", value)
	}
}

func TestEngineRatePushdownFallsBack(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	samples := []Series{{
		Labels:  map[string]string{"__name__": "up", "job": "api"},
		Samples: []Sample{{T: at.Add(-2 * time.Minute).UnixMilli(), V: 1}, {T: at.Add(-time.Minute).UnixMilli(), V: 2}},
	}}
	tests := []string{
		"sum by (job) (rate(up[5m]))",
		"sum without (job) (rate(up[5m]))",
		"sum(rate(up[1h]))",
		"sum(irate(up[5m]))",
		"rate(up[5m])",
	}
	for _, query := range tests {
		fetcher := &rateFetcherStub{samples: samples, vector: Vector{{Labels: map[string]string{}, V: 1}}}
		engine := NewEngine(fetcher)
		if _, err := engine.Query(context.Background(), query, at); err != nil {
			t.Errorf("%s: %v", query, err)
			continue
		}
		if fetcher.fetchCalls == 0 {
			t.Errorf("%s did not use the sample fallback", query)
		}
		if fetcher.rateCalls != 0 {
			t.Errorf("%s used the rate pushdown (%d calls)", query, fetcher.rateCalls)
		}
	}
}

func TestEngineRatePushdownUnsupportedFetcher(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fetcher := &plainFetcher{samples: []Series{{
		Labels:  map[string]string{"__name__": "up"},
		Samples: []Sample{{T: at.Add(-2 * time.Minute).UnixMilli(), V: 1}, {T: at.Add(-time.Minute).UnixMilli(), V: 2}},
	}}}
	engine := NewEngine(fetcher)
	if _, err := engine.Query(context.Background(), "sum(rate(up[5m]))", at); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if fetcher.calls == 0 {
		t.Fatal("unsupported fetcher did not fall back to Fetch")
	}
}

func TestEngineRatePushdownError(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	fetcher := &rateFetcherStub{rateErr: errors.New("boom")}
	engine := NewEngine(fetcher)
	if _, err := engine.Query(context.Background(), "sum(rate(up[5m]))", at); err == nil {
		t.Fatal("Query succeeded on a rate fetch error")
	}
	if fetcher.fetchCalls != 0 {
		t.Fatal("rate fetch error fell back to Fetch")
	}
}

type plainFetcher struct {
	calls   int
	samples []Series
}

func (f *plainFetcher) Fetch(context.Context, Selector, time.Time, time.Time) ([]Series, error) {
	f.calls++
	return f.samples, nil
}
