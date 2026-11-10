package chclient

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
)

func TestRecorderCapturesStatements(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"12","read_bytes":"99"}`)
		_, _ = io.WriteString(w, `{"meta":[],"data":[]}`)
	}), nil)
	recorder := &Recorder{}
	ctx := WithRecorder(context.Background(), recorder)
	sql := "SELECT Value FROM `otel`.`otel_metrics_gauge` WHERE MetricName = {metric:String}"
	if _, err := client.Query(ctx, sql, nil); err != nil {
		t.Fatalf("Query: %v", err)
	}
	statements := recorder.Statements()
	if len(statements) != 1 {
		t.Fatalf("statements = %d, want 1", len(statements))
	}
	record := statements[0]
	if record.SQL != sql {
		t.Fatalf("SQL = %q", record.SQL)
	}
	if len(record.Tables) != 1 || record.Tables[0] != "otel.otel_metrics_gauge" {
		t.Fatalf("tables = %v", record.Tables)
	}
	if record.ReadRows != 12 || record.Error != "" {
		t.Fatalf("record = %#v", record)
	}
}

func TestRecorderCapturesErrors(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"3","read_bytes":"4"}`)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"exception":"boom"}`)
	}), nil)
	recorder := &Recorder{}
	ctx := WithRecorder(context.Background(), recorder)
	if _, err := client.Query(ctx, "SELECT 1 FROM otel.otel_logs", nil); err == nil {
		t.Fatal("Query succeeded on a 500")
	}
	statements := recorder.Statements()
	if len(statements) != 1 || statements[0].Error == "" || statements[0].ReadRows != 3 {
		t.Fatalf("statements = %#v", statements)
	}
}

func TestRecorderNilIsNoOp(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"meta":[],"data":[]}`)
	}), nil)
	if _, err := client.Query(context.Background(), "SELECT 1", nil); err != nil {
		t.Fatal(err)
	}
	if RecorderFromContext(context.Background()) != nil {
		t.Fatal("RecorderFromContext returned a recorder without one")
	}
}

func TestRecorderParallelIsolation(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-ClickHouse-Summary", `{"read_rows":"1","read_bytes":"1"}`)
		_, _ = io.WriteString(w, `{"meta":[],"data":[]}`)
	}), nil)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := &Recorder{}
			ctx := WithRecorder(context.Background(), recorder)
			if _, err := client.Query(ctx, "SELECT 1 FROM otel.otel_logs", nil); err != nil {
				t.Errorf("Query: %v", err)
				return
			}
			if statements := recorder.Statements(); len(statements) != 1 {
				t.Errorf("statements = %d, want 1", len(statements))
			}
		}()
	}
	wg.Wait()
}

func TestTablesFromSQL(t *testing.T) {
	tests := []struct {
		sql  string
		want string
	}{
		{sql: "SELECT * FROM otel.otel_logs", want: "otel.otel_logs"},
		{sql: "SELECT * FROM `otel`.`otel_traces` JOIN `otel`.`otel_logs` ON true", want: "otel.otel_traces,otel.otel_logs"},
		{sql: "SELECT * FROM otel.otel_logs JOIN otel.otel_logs ON true", want: "otel.otel_logs"},
		{sql: "SELECT 1"},
	}
	for _, test := range tests {
		got := tablesFromSQL(test.sql)
		if len(got) == 0 {
			if test.want != "" {
				t.Errorf("tablesFromSQL(%q) = none, want %q", test.sql, test.want)
			}
			continue
		}
		joined := got[0]
		for _, table := range got[1:] {
			joined += "," + table
		}
		if joined != test.want {
			t.Errorf("tablesFromSQL(%q) = %q, want %q", test.sql, joined, test.want)
		}
	}
}
