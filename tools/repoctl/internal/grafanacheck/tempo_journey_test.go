package grafanacheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type tempoFake struct {
	t                *testing.T
	now              time.Time
	traceCount       int
	spanCount        int
	referenceDelta   string
	acceptStructural bool
	emptyReference   bool
	badResource      bool
	badTagValues     bool
	referenceBroken  bool
	emptyResults     bool
	wrongStructural  bool
}

func (f tempoFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/ds/query":
		f.query(w, r)
	case strings.HasSuffix(r.URL.Path, "/api/v2/search/tags"):
		f.tags(w)
	case strings.HasSuffix(r.URL.Path, "/api/v2/search/tag/span:name/values"):
		if f.badTagValues {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `nope`)
			return
		}
		_, _ = fmt.Fprint(w, `{"tagValues":[{"type":"string","value":"GET /cart"},{"type":"string","value":"SELECT orders"}]}`)
	default:
		f.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f tempoFake) query(w http.ResponseWriter, r *http.Request) {
	var body tempoQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode query: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	q := body.Queries[0]
	uid := q.Datasource.UID
	if f.emptyResults {
		_, _ = fmt.Fprint(w, `{"results":{}}`)
		return
	}
	if uid == "reference-tempo" && f.referenceBroken {
		_, _ = fmt.Fprint(w, `{"results":{"A":{"status":500,"error":"reference down"}}}`)
		return
	}
	if strings.Contains(q.Query, ">>") {
		if f.wrongStructural {
			_, _ = fmt.Fprint(w, `{"results":{"A":{"status":500,"error":"boom","frames":[]}}}`)
			return
		}
		if f.acceptStructural {
			writeFrames(w, []frame{tempoSearchFrame(f.now, "t1", "cart", "GET /cart")})
			return
		}
		_, _ = fmt.Fprint(w, `{"results":{"A":{"status":500,"error":"tempo: TraceQL structural operator is not supported","frames":[]}}}`)
		return
	}
	if q.QueryType == "traceId" {
		frames := make([]frame, 0, f.spanCount)
		for i := 0; i < f.spanCount; i++ {
			frames = append(frames, traceSpanFrame(f.now, "t1", fmt.Sprintf("s%d", i), "GET /cart", "cart", f.referenceDeltaFor(uid)))
		}
		writeFrames(w, frames)
		return
	}
	if uid == "reference-tempo" && f.emptyReference {
		writeFrames(w, nil)
		return
	}
	frames := make([]frame, 0, f.traceCount)
	for i := 0; i < f.traceCount; i++ {
		frames = append(frames, tempoSearchFrame(f.now, fmt.Sprintf("t%d", i), "cart", "GET /cart"+f.referenceDeltaFor(uid)))
	}
	writeFrames(w, frames)
}

func (f tempoFake) referenceDeltaFor(uid string) string {
	if uid == "reference-tempo" {
		return f.referenceDelta
	}
	return ""
}

func (f tempoFake) tags(w http.ResponseWriter) {
	tags := `["deployment.environment.name","service.name"]`
	if f.badResource {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `nope`)
		return
	}
	_, _ = fmt.Fprintf(w, `{"scopes":[{"name":"resource","tags":%s}]}`, tags)
}

func tempoSearchFrame(now time.Time, id, service, name string) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{
		{Name: "traceID", Type: "string"},
		{Name: "startTime", Type: "time"},
		{Name: "traceService", Type: "string"},
		{Name: "traceName", Type: "string"},
		{Name: "traceDuration", Type: "number"},
	}
	f.Data.Values = [][]json.RawMessage{
		{json.RawMessage(fmt.Sprintf("%q", id))},
		{json.RawMessage(fmt.Sprintf("%d", now.UnixMilli()))},
		{json.RawMessage(fmt.Sprintf("%q", service))},
		{json.RawMessage(fmt.Sprintf("%q", name))},
		{json.RawMessage("300")},
	}
	return f
}

func traceSpanFrame(now time.Time, traceID, spanID, name, service, delta string) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{
		{Name: "traceID", Type: "string"},
		{Name: "spanID", Type: "string"},
		{Name: "parentSpanID", Type: "string"},
		{Name: "operationName", Type: "string"},
		{Name: "serviceName", Type: "string"},
		{Name: "kind", Type: "string"},
		{Name: "statusCode", Type: "string"},
		{Name: "startTime", Type: "time"},
		{Name: "duration", Type: "number"},
	}
	f.Data.Values = [][]json.RawMessage{
		{json.RawMessage(fmt.Sprintf("%q", traceID))},
		{json.RawMessage(fmt.Sprintf("%q", spanID))},
		{json.RawMessage(`""`)},
		{json.RawMessage(fmt.Sprintf("%q", name))},
		{json.RawMessage(fmt.Sprintf("%q", service))},
		{json.RawMessage(`"server"`)},
		{json.RawMessage(`"ok"`)},
		{json.RawMessage(fmt.Sprintf("%d", now.UnixMilli()))},
		{json.RawMessage("100")},
	}
	if delta != "" {
		f.Data.Values[3][0] = json.RawMessage(fmt.Sprintf("%q", name+delta))
	}
	return f
}

func TestTempoJourneyPasses(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, traceCount: 3, spanCount: 2}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now}}
	steps, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("tempo journey: %v", err)
	}
	if len(steps) != 6 {
		t.Fatalf("steps = %d, want 6", len(steps))
	}
}

func TestTempoJourneyMismatches(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		fake tempoFake
		step string
	}{
		{name: "trace name", fake: tempoFake{traceCount: 3, spanCount: 2, referenceDelta: "!"}, step: "tag search"},
		{name: "structural accepted", fake: tempoFake{traceCount: 3, spanCount: 2, acceptStructural: true}, step: "unsupported structural"},
		{name: "tag values", fake: tempoFake{traceCount: 3, spanCount: 2, badTagValues: true}, step: "tag-value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fake.t = t
			test.fake.now = now
			server := httptest.NewServer(test.fake)
			defer server.Close()
			runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now, Timeout: 50 * time.Millisecond}}
			_, err := runner.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.step) {
				t.Fatalf("error = %v, want %q", err, test.step)
			}
		})
	}
}

func TestTempoJourneyDefaults(t *testing.T) {
	cfg := (TempoJourney{}).defaults()
	if cfg.SkoposUID == "" || cfg.ReferenceUID == "" || cfg.TraceID == "" || cfg.Timeout <= 0 {
		t.Fatalf("defaults are incomplete: %+v", cfg)
	}
}

func TestTempoReferenceEmpty(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, traceCount: 3, spanCount: 2, emptyReference: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now, Timeout: 50 * time.Millisecond}}
	if _, err := runner.tagSearch(context.Background(), runner.defaults()); err == nil {
		t.Fatal("tagSearch accepted an empty reference")
	}
}

func TestCompareSearchAndSpanRows(t *testing.T) {
	search := []searchRow{{TraceID: "a"}}
	if err := compareSearchRows(search, search); err != nil {
		t.Fatalf("equal search rows: %v", err)
	}
	if err := compareSearchRows(nil, search); err == nil {
		t.Fatal("compareSearchRows accepted a different count")
	}
	spans := []spanRow{{SpanID: "s"}}
	if err := compareSpans(spans, spans); err != nil {
		t.Fatalf("equal spans: %v", err)
	}
	if err := compareSpans(nil, spans); err == nil {
		t.Fatal("compareSpans accepted a different count")
	}
}

func TestTempoStepErrors(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	type stepFunc func(context.Context, TempoJourney, TempoConfig) (string, error)
	tests := []struct {
		name string
		fake tempoFake
		step stepFunc
	}{
		{name: "tags", fake: tempoFake{badResource: true}, step: func(ctx context.Context, j TempoJourney, cfg TempoConfig) (string, error) { return j.tags(ctx, cfg) }},
		{name: "tag values", fake: tempoFake{badTagValues: true}, step: func(ctx context.Context, j TempoJourney, cfg TempoConfig) (string, error) {
			return j.tagValues(ctx, cfg)
		}},
		{name: "trace by id", fake: tempoFake{referenceBroken: true, spanCount: 2}, step: func(ctx context.Context, j TempoJourney, cfg TempoConfig) (string, error) {
			return j.traceByID(ctx, cfg)
		}},
		{name: "unsupported", fake: tempoFake{emptyResults: true}, step: func(ctx context.Context, j TempoJourney, cfg TempoConfig) (string, error) {
			return j.unsupported(ctx, cfg)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fake.t = t
			test.fake.now = now
			server := httptest.NewServer(test.fake)
			defer server.Close()
			runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now, Timeout: 50 * time.Millisecond}}
			if _, err := test.step(context.Background(), runner, runner.defaults()); err == nil {
				t.Fatal("step accepted a broken response")
			}
		})
	}
}

func TestTempoTagValueBadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{`)
	}))
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	if _, err := runner.tagValueSet(context.Background(), "reference-tempo", "span:name"); err == nil {
		t.Fatal("tagValueSet accepted bad JSON")
	}
}

func TestTempoResourceTagsMissingScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"scopes":[{"name":"span","tags":["x"]}]}`)
	}))
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	if _, err := runner.resourceScopeTags(context.Background(), "reference-tempo"); err == nil {
		t.Fatal("resourceScopeTags accepted a missing resource scope")
	}
}

func TestTempoUnsupportedMessageMismatch(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, wrongStructural: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now}}
	if _, err := runner.unsupported(context.Background(), runner.defaults()); err == nil {
		t.Fatal("unsupported accepted an unrelated error")
	}
}

func TestTempoTraceWithoutSpans(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, spanCount: 0}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now}}
	if _, err := runner.traceByID(context.Background(), runner.defaults()); err == nil {
		t.Fatal("traceByID accepted an empty trace")
	}
}

func TestFrameRowsSkipMissingFields(t *testing.T) {
	var search frame
	search.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "startTime", Type: "time"}}
	search.Data.Values = [][]json.RawMessage{{json.RawMessage("1")}}
	if rows, err := searchRows([]frame{search}); err != nil || len(rows) != 0 {
		t.Fatalf("searchRows = %v, %v", rows, err)
	}
	var spans frame
	spans.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "operationName", Type: "string"}}
	spans.Data.Values = [][]json.RawMessage{{json.RawMessage(`"x"`)}}
	if rows, err := spanRows([]frame{spans}); err != nil || len(rows) != 0 {
		t.Fatalf("spanRows = %v, %v", rows, err)
	}
}

func TestCommandTempoJourney(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	prom := journeyFake{t: t, now: now, serviceCount: 6}
	tempo := tempoFake{t: t, now: now, traceCount: 3, spanCount: 2}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/datasources") && !strings.Contains(r.URL.Path, "/proxy/") {
			prom.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/health") {
			prom.ServeHTTP(w, r)
			return
		}
		tempo.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	provisioning := writeProvisioning(t)
	env, stdout, _ := testEnv()
	env.Now = func() time.Time { return now }
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL,
		"--provisioning", provisioning,
		"--tempo-journey",
	})
	if err != nil {
		t.Fatalf("grafana-check --tempo-journey: %v", err)
	}
	for _, want := range []string{"tempo journey trace by ID OK", "6 tempo journey steps passed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestTempoTraceQLNeedsTwoTraces(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, traceCount: 1, spanCount: 1}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now, Timeout: 50 * time.Millisecond}}
	if _, err := runner.traceQLSearch(context.Background(), runner.defaults()); err == nil {
		t.Fatal("traceQLSearch accepted a single trace")
	}
}

func TestTempoTagSearchReferenceError(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := tempoFake{t: t, now: now, traceCount: 3, spanCount: 2, referenceBroken: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := TempoJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: TempoConfig{Now: now, Timeout: 50 * time.Millisecond}}
	if _, err := runner.tagSearch(context.Background(), runner.defaults()); err == nil {
		t.Fatal("tagSearch accepted a broken reference")
	}
}

func TestTempoOptionalFieldsAndMismatches(t *testing.T) {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "spanID", Type: "string"}}
	f.Data.Values = [][]json.RawMessage{{json.RawMessage(`"s1"`)}}
	rows, err := spanRows([]frame{f})
	if err != nil {
		t.Fatalf("spanRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "" || rows[0].TraceID != "" {
		t.Fatalf("rows = %+v", rows)
	}
	if err := compareStringSets("x", []string{"a"}, []string{"b"}); err == nil {
		t.Fatal("compareStringSets accepted a mismatch")
	}
}
