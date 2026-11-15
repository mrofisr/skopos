package chclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type recordedQuery struct {
	head      string
	result    string
	duration  time.Duration
	readRows  uint64
	readBytes uint64
}

type recordedFallback struct {
	head   string
	action string
}

type recordingObserver struct {
	calls     []recordedQuery
	fallbacks []recordedFallback
}

func (o *recordingObserver) QueryDone(head, result string, duration time.Duration, readRows, readBytes uint64) {
	o.calls = append(o.calls, recordedQuery{
		head: head, result: result, duration: duration, readRows: readRows, readBytes: readBytes,
	})
}

func (o *recordingObserver) FinalFallback(head, action string) {
	o.fallbacks = append(o.fallbacks, recordedFallback{head: head, action: action})
}

func testClient(t *testing.T, handler http.Handler, observer QueryObserver) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:  server.URL,
		Database: "otel",
		Observer: observer,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestQueryObserverRecordsSummary(t *testing.T) {
	observer := &recordingObserver{}
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"7","read_bytes":"42"}`)
		_, _ = io.WriteString(w, `{"meta":[],"data":[]}`)
	}), observer)
	ctx := WithHead(context.Background(), "prom")
	if _, err := client.Query(ctx, "SELECT 1", nil); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(observer.calls) != 1 {
		t.Fatalf("observer calls = %d, want 1", len(observer.calls))
	}
	call := observer.calls[0]
	if call.head != "prom" || call.result != "ok" || call.readRows != 7 || call.readBytes != 42 {
		t.Fatalf("call = %#v", call)
	}
}

func TestQueryObserverMarksErrors(t *testing.T) {
	observer := &recordingObserver{}
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"3","read_bytes":"9"}`)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"exception":"boom"}`)
	}), observer)
	if _, err := client.Query(WithHead(context.Background(), "tempo"), "SELECT 1", nil); err == nil {
		t.Fatal("Query succeeded on a 500")
	}
	if len(observer.calls) != 1 {
		t.Fatalf("observer calls = %d, want 1", len(observer.calls))
	}
	call := observer.calls[0]
	if call.head != "tempo" || call.result != "error" || call.readRows != 3 || call.readBytes != 9 {
		t.Fatalf("call = %#v", call)
	}
}

func TestQueryObserverMissingHeaderIsZero(t *testing.T) {
	observer := &recordingObserver{}
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"meta":[],"data":[]}`)
	}), observer)
	if _, err := client.Query(context.Background(), "SELECT 1", nil); err != nil {
		t.Fatal(err)
	}
	if len(observer.calls) != 1 || observer.calls[0].head != "unknown" {
		t.Fatalf("calls = %#v", observer.calls)
	}
	if observer.calls[0].readRows != 0 || observer.calls[0].readBytes != 0 {
		t.Fatalf("missing header counters = %#v", observer.calls[0])
	}
}

func TestParseSummary(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		raw       string
		rows      uint64
		bytesRead uint64
	}{
		{raw: `{"read_rows":"7","read_bytes":"42"}`, rows: 7, bytesRead: 42},
		{raw: `{"read_rows":"0","read_bytes":"0"}`},
		{raw: ""},
		{raw: "not json"},
		{raw: `{"read_rows":"x","read_bytes":"42"}`},
		{raw: `{"read_rows":7,"read_bytes":42}`},
	}
	for _, test := range tests {
		rows, bytesRead := parseSummary(test.raw, logger)
		if rows != test.rows || bytesRead != test.bytesRead {
			t.Errorf("parseSummary(%q) = %d/%d, want %d/%d", test.raw, rows, bytesRead, test.rows, test.bytesRead)
		}
	}
}
