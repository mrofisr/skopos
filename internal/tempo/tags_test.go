package tempo

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type tagFetcherStub struct {
	fakeFetcher
	namesCalls  int
	valuesCalls int
	names       map[string][]string
	values      []string
	err         error
}

func (f *tagFetcherStub) TagNames(context.Context, time.Time, time.Time, Filter, int) (map[string][]string, error) {
	f.namesCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.names, nil
}

func (f *tagFetcherStub) TagValues(context.Context, string, time.Time, time.Time, Filter, int) ([]string, error) {
	f.valuesCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.values, nil
}

func TestTagNamesPushdownSQL(t *testing.T) {
	fetcher, fake := newTraceFetcher(t)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	names, err := fetcher.TagNames(context.Background(), start, start.Add(time.Hour), nil, 100)
	if err != nil {
		t.Fatalf("TagNames: %v", err)
	}
	for _, scope := range []string{"span", "event", "link", "resource", "intrinsic"} {
		if _, ok := names[scope]; !ok {
			t.Errorf("missing scope %q", scope)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	joined := strings.Join(fake.bodies, "\n")
	for _, want := range []string{"SpanAttributes", "ResourceAttributes", "Events.Attributes", "Links.Attributes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tag-name SQL missing %q", want)
		}
	}
}

func TestTagValuesPushdownSQL(t *testing.T) {
	fetcher, fake := newTraceFetcher(t)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		tag  string
		want string
	}{
		{tag: "span:name", want: "GET /cart"},
		{tag: "event:name", want: "exception"},
		{tag: "event.exception.type", want: "BadGateway"},
		{tag: "link:traceID", want: "trace-3"},
		{tag: "link.rel", want: "retry"},
		{tag: "resource.service.name", want: "checkout"},
		{tag: "span:status", want: "error"},
		{tag: "span:kind", want: "server"},
	}
	for _, test := range tests {
		values, err := fetcher.TagValues(context.Background(), test.tag, start, start.Add(time.Hour), nil, 0)
		if err != nil {
			t.Fatalf("TagValues(%s): %v", test.tag, err)
		}
		if len(values) != 1 || values[0] != test.want {
			t.Errorf("TagValues(%s) = %v, want %s", test.tag, values, test.want)
		}
	}
	if _, err := fetcher.TagValues(context.Background(), "trace:rootService", start, start.Add(time.Hour), nil, 0); !errors.Is(err, ErrTagFallback) {
		t.Fatalf("trace:rootService error = %v, want ErrTagFallback", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) == 0 {
		t.Fatal("no tag-value SQL was emitted")
	}
}

func TestTagCacheSecondCall(t *testing.T) {
	stub := &tagFetcherStub{
		names:  map[string][]string{"span": {"name"}},
		values: []string{"GET /cart"},
	}
	api := NewAPI(stub, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest("GET", "/api/v2/search/tags", nil)
	if _, err := api.tags(request); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("GET", "/api/v2/search/tags", nil)
	if _, err := api.tags(request); err != nil {
		t.Fatal(err)
	}
	if stub.namesCalls != 1 {
		t.Fatalf("TagNames calls = %d, want one cache hit", stub.namesCalls)
	}

	valuesRequest := httptest.NewRequest("GET", "/api/v2/search/tag/span:name/values", nil)
	valuesRequest.SetPathValue("tag", "span:name")
	if _, err := api.tagValues(valuesRequest); err != nil {
		t.Fatal(err)
	}
	valuesRequest = httptest.NewRequest("GET", "/api/v2/search/tag/span:name/values", nil)
	valuesRequest.SetPathValue("tag", "span:name")
	if _, err := api.tagValues(valuesRequest); err != nil {
		t.Fatal(err)
	}
	if stub.valuesCalls != 1 {
		t.Fatalf("TagValues calls = %d, want one cache hit", stub.valuesCalls)
	}
}

func TestTagCacheFallsBack(t *testing.T) {
	stub := &tagFetcherStub{
		fakeFetcher: fakeFetcher{spans: []Span{{SpanName: "GET /cart", TraceID: "trace"}}},
		err:         ErrTagFallback,
	}
	api := NewAPI(stub, "test")
	api.Now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := api.tags(httptest.NewRequest("GET", "/api/v2/search/tags", nil)); err != nil {
		t.Fatalf("tags fallback: %v", err)
	}
	request := httptest.NewRequest("GET", "/api/v2/search/tag/span:name/values", nil)
	request.SetPathValue("tag", "span:name")
	if _, err := api.tagValues(request); err != nil {
		t.Fatalf("tagValues fallback: %v", err)
	}
}
