package tempo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/internal/chclient"
	"github.com/jawaracloud/skopos/internal/plan"
)

type fakeTraceClickHouse struct {
	mu      sync.Mutex
	bodies  []string
	queries []url.Values
}

func (f *fakeTraceClickHouse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(body))
	f.queries = append(f.queries, r.URL.Query())
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	from := strings.Index(string(body), "FROM")
	spanName := strings.Index(string(body), "SpanName")
	if strings.Contains(string(body), "TraceId") && (spanName == -1 || spanName > from) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": []map[string]string{{"name": "TraceId", "type": "String"}},
			"data": []map[string]any{{"TraceId": "trace-1"}},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"meta": []map[string]string{},
		"data": []any{},
	})
}

func newTraceFetcher(t *testing.T) (*ClickHouseFetcher, *fakeTraceClickHouse) {
	t.Helper()
	fake := &fakeTraceClickHouse{}
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

func mustFilter(t *testing.T, query string) Filter {
	t.Helper()
	filter, err := ParseTraceQL(query)
	if err != nil {
		t.Fatalf("ParseTraceQL(%q): %v", query, err)
	}
	return filter
}

func TestFilterPredicateIntrinsics(t *testing.T) {
	tests := []struct {
		query string
		want  []string
		ok    bool
	}{
		{query: `{ span:name = "GET /cart" }`, want: []string{"SpanName", "span_name", "GET /cart"}, ok: true},
		{query: `{ span:name != "GET /cart" }`, want: []string{"SpanName", "span_name"}, ok: true},
		{query: `{ duration >= 100ms }`, want: []string{"Duration", "duration_ns"}, ok: true},
		{query: `{ span:status = error }`, want: []string{"StatusCode", "span_status", "Error"}, ok: true},
		{query: `{ span:kind = server }`, want: []string{"SpanKind", "span_kind", "Server"}, ok: true},
		{query: `{ span:name =~ "GET.*" }`, ok: false},
		{query: `{ span.foo = "bar" }`, want: []string{"SpanAttributes", "mapContains"}, ok: true},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			predicate, ok := FilterPredicate(mustFilter(t, test.query))
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v (predicate %#v)", ok, test.ok, predicate)
			}
			if !ok {
				return
			}
			text := fmt.Sprintf("%#v", predicate)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("predicate %s missing %q", text, want)
				}
			}
		})
	}
}

func TestFilterPredicateBooleanCombinations(t *testing.T) {
	predicate, ok := FilterPredicate(mustFilter(t, `{ span:name = "GET /cart" && span.foo = "bar" }`))
	if !ok || !strings.Contains(fmt.Sprintf("%#v", predicate), "SpanName") {
		t.Fatalf("AND with an unsupported child should push the supported child: %#v", predicate)
	}
	if _, ok := FilterPredicate(mustFilter(t, `{ span:name = "GET /cart" || span.foo =~ "bar" }`)); ok {
		t.Fatal("OR with an unsupported child must not push")
	}
}

func TestFetchRangeFilteredSQL(t *testing.T) {
	fetcher, fake := newTraceFetcher(t)
	filter := mustFilter(t, `{ span:name = "GET /cart" && duration >= 100ms }`)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fetcher.FetchRangeFiltered(context.Background(), start, start.Add(time.Hour), filter); err != nil {
		t.Fatalf("FetchRangeFiltered: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 2 {
		t.Fatalf("queries = %d, want the trace-id and span queries; bodies=%v", len(fake.bodies), fake.bodies)
	}
	first := fake.bodies[0]
	for _, want := range []string{"SpanName", "Duration", "{span_name:String}", "{duration_ns:UInt64}"} {
		if !strings.Contains(first, want) {
			t.Errorf("trace-id SQL missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "GET /cart") {
		t.Fatalf("user text was concatenated into SQL:\n%s", first)
	}
	second := fake.bodies[1]
	for _, want := range []string{"TraceId", "IN", "{trace_id_0:String}"} {
		if !strings.Contains(second, want) {
			t.Errorf("span SQL missing %q:\n%s", want, second)
		}
	}
}

func TestFetchRangeFilteredFallback(t *testing.T) {
	fetcher, fake := newTraceFetcher(t)
	filter := mustFilter(t, `{ span.foo =~ "bar" }`)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fetcher.FetchRangeFiltered(context.Background(), start, start.Add(time.Hour), filter); err != nil {
		t.Fatalf("FetchRangeFiltered: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if strings.Contains(fake.bodies[0], "{span_name:String}") || strings.Contains(fake.bodies[0], "{duration_ns:UInt64}") {
		t.Fatalf("unsupported filter was pushed:\n%s", fake.bodies[0])
	}
}

func TestIntrinsicDurationUnits(t *testing.T) {
	for _, query := range []string{`{ duration >= 100ms }`, `{ duration >= 2s }`, `{ duration >= 1m }`} {
		if _, ok := FilterPredicate(mustFilter(t, query)); !ok {
			t.Errorf("%s did not push", query)
		}
	}
}

func TestFilterPredicateAttributes(t *testing.T) {
	tests := []struct {
		query string
		want  []string
		ok    bool
	}{
		{query: `{ span.http.method = "GET" }`, want: []string{"SpanAttributes", "mapContains", "span_attr_key", "GET"}, ok: true},
		{query: `{ resource.service.name = "checkout" }`, want: []string{"ResourceAttributes", "mapContains", "resource_attr_key"}, ok: true},
		{query: `{ span.http.status_code = 500 }`, want: []string{"SpanAttributes", "toFloat64OrNull", "span_attr_number"}, ok: true},
		{query: `{ span.foo = nil }`, want: []string{"mapContains", "span_attr_present"}, ok: true},
		{query: `{ span.foo != nil }`, want: []string{"mapContains", "span_attr_present"}, ok: true},
		{query: `{ span.foo =~ "x" }`, ok: false},
		{query: `{ span.foo > "x" }`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			predicate, ok := FilterPredicate(mustFilter(t, test.query))
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v (%#v)", ok, test.ok, predicate)
			}
			if !ok {
				return
			}
			text := fmt.Sprintf("%#v", predicate)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("predicate %s missing %q", text, want)
				}
			}
		})
	}
}

func TestAttributePredicateHandlesQuotedAndUnicodeKeys(t *testing.T) {
	predicate, ok := conditionPredicate(condition{
		scope: "span", key: "a.b\"c", op: "=", value: "值",
	})
	if !ok {
		t.Fatal("attribute predicate did not push")
	}
	text := fmt.Sprintf("%#v", predicate)
	for _, want := range []string{"a.b\\\"c", "值", "mapContains"} {
		if !strings.Contains(text, want) {
			t.Errorf("predicate %s missing %q", text, want)
		}
	}
}

func TestFetchRangeFilteredAttributeSQL(t *testing.T) {
	fetcher, fake := newTraceFetcher(t)
	filter := mustFilter(t, `{ span.http.method = "GET" }`)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if _, err := fetcher.FetchRangeFiltered(context.Background(), start, start.Add(time.Hour), filter); err != nil {
		t.Fatalf("FetchRangeFiltered: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 2 {
		t.Fatalf("queries = %d, want two", len(fake.bodies))
	}
	for _, want := range []string{"SpanAttributes", "mapContains", "{span_attr_key:String}", "{span_attr_value:String}"} {
		if !strings.Contains(fake.bodies[0], want) {
			t.Errorf("trace-id SQL missing %q:\n%s", want, fake.bodies[0])
		}
	}
	if strings.Contains(fake.bodies[0], "GET") {
		t.Fatalf("attribute value leaked into SQL:\n%s", fake.bodies[0])
	}
}
