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

type lokiFake struct {
	t                 *testing.T
	now               time.Time
	serviceCount      int
	acceptUnsupported bool
	lineDelta         string
	dropService       bool
	brokenFrame       bool
	referenceBroken   bool
	emptyResults      bool
	badResource       bool
	wrongUnsupported  bool
}

func (f lokiFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/ds/query":
		f.query(w, r)
	case strings.HasSuffix(r.URL.Path, "/loki/api/v1/label/service_name/values"):
		f.values(w)
	case strings.HasSuffix(r.URL.Path, "/loki/api/v1/labels"):
		if f.badResource {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `nope`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"success","data":["service_name"]}`)
	default:
		f.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f lokiFake) query(w http.ResponseWriter, r *http.Request) {
	var body lokiQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode query: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(body.Queries) != 1 {
		f.t.Errorf("queries = %d, want 1", len(body.Queries))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	expr := body.Queries[0].Expr
	uid := body.Queries[0].Datasource.UID
	if f.emptyResults {
		_, _ = fmt.Fprint(w, `{"results":{}}`)
		return
	}
	if uid == "reference-loki" && f.referenceBroken {
		_, _ = fmt.Fprint(w, `{"results":{"A":{"status":500,"error":"reference down"}}}`)
		return
	}
	if strings.Contains(expr, "unwrap") {
		if f.wrongUnsupported {
			_, _ = fmt.Fprint(w, `{"results":{"A":{"status":400,"error":"boom","frames":[]}}}`)
			return
		}
		if f.acceptUnsupported {
			writeFrames(w, []frame{logFrame(f.now, "skopos-e2e-server-0", "x")})
			return
		}
		_, _ = fmt.Fprint(w, `{"results":{"A":{"status":400,"error":"bad_data: loki: unsupported parser stage \"unwrap\"","frames":[]}}}`)
		return
	}
	if strings.Contains(expr, "count_over_time") {
		writeFrames(w, []frame{rangeFrame(f.now, 1)})
		return
	}
	frames := make([]frame, 0, 3)
	for i := 0; i < 3; i++ {
		line := `{"level":"error","status":500,"msg":"grafana journey log"}`
		if f.brokenFrame {
			line = ""
		}
		if uid == "reference-loki" {
			line += f.lineDelta
		}
		frames = append(frames, logFrame(f.now.Add(time.Duration(i)*time.Minute), "skopos-e2e-server-0", line))
	}
	writeFrames(w, frames)
}

func (f lokiFake) values(w http.ResponseWriter) {
	count := f.serviceCount
	if f.dropService {
		count--
	}
	values := make([]string, 0, count)
	for i := 0; i < count; i++ {
		values = append(values, fmt.Sprintf("skopos-e2e-server-%d", i))
	}
	encoded, _ := json.Marshal(map[string]any{"status": "success", "data": values})
	_, _ = w.Write(encoded)
}

func logFrame(now time.Time, service, line string) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{
		{Name: "labels", Type: "other"},
		{Name: "Time", Type: "time"},
		{Name: "Line", Type: "string"},
		{Name: "tsNs", Type: "string"},
	}
	labels, _ := json.Marshal(map[string]string{"service_name": service})
	ts := fmt.Sprintf("%d", now.UnixNano())
	f.Data.Values = [][]json.RawMessage{
		{labels},
		{json.RawMessage(fmt.Sprintf("%d", now.UnixMilli()))},
		{json.RawMessage(fmt.Sprintf("%q", line))},
		{json.RawMessage(fmt.Sprintf("%q", ts))},
	}
	return f
}

func rangeFrame(now time.Time, value float64) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "Time", Type: "time"}, {Name: "Value", Type: "number", Labels: map[string]string{}}}
	times := make([]json.RawMessage, 0, 12)
	values := make([]json.RawMessage, 0, 12)
	for i := 0; i < 12; i++ {
		times = append(times, json.RawMessage(fmt.Sprintf("%d", now.Add(time.Duration(i-12)*time.Minute).UnixMilli())))
		values = append(values, json.RawMessage(fmt.Sprintf("%v", value)))
	}
	f.Data.Values = [][]json.RawMessage{times, values}
	return f
}

func TestLokiJourneyPasses(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	steps, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("loki journey: %v", err)
	}
	if len(steps) != 6 {
		t.Fatalf("steps = %d, want 6", len(steps))
	}
}

func TestLokiJourneyMismatches(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		fake lokiFake
		step string
	}{
		{name: "label values", fake: lokiFake{dropService: true}, step: "label_values"},
		{name: "line filter", fake: lokiFake{lineDelta: "!"}, step: "line filter"},
		{name: "unsupported accepted", fake: lokiFake{acceptUnsupported: true}, step: "unsupported stage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fake.t = t
			test.fake.now = now
			test.fake.serviceCount = 6
			server := httptest.NewServer(test.fake)
			t.Cleanup(server.Close)
			runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
			_, err := runner.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.step) {
				t.Fatalf("error = %v, want %q", err, test.step)
			}
		})
	}
}

func TestLogEntriesRejectsMalformedFrames(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	f := logFrame(now, "skopos-e2e-server-0", "line")
	f.Data.Values = f.Data.Values[:2]
	if _, err := logEntries([]frame{f}); err == nil {
		t.Fatal("logEntries accepted a truncated frame")
	}
}

func TestCommandLokiJourney(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	prom := journeyFake{t: t, now: now, serviceCount: 6}
	loki := lokiFake{t: t, now: now, serviceCount: 6}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/datasources") && !strings.Contains(r.URL.Path, "/proxy/") {
			prom.ServeHTTP(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/health") {
			prom.ServeHTTP(w, r)
			return
		}
		loki.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	provisioning := writeProvisioning(t)
	env, stdout, _ := testEnv()
	env.Now = func() time.Time { return now }
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL,
		"--provisioning", provisioning,
		"--loki-journey",
	})
	if err != nil {
		t.Fatalf("grafana-check --loki-journey: %v", err)
	}
	for _, want := range []string{"loki journey line filter OK", "6 loki journey steps passed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestLokiJourneyDefaults(t *testing.T) {
	cfg := (LokiJourney{}).defaults()
	if cfg.SkoposUID == "" || cfg.ReferenceUID == "" || cfg.Step <= 0 || cfg.Service == "" || cfg.Expected == 0 {
		t.Fatalf("defaults are incomplete: %+v", cfg)
	}
}

func TestLokiResourceErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "http 500", body: `nope`},
		{name: "bad json", body: `{`},
		{name: "error field", body: `{"status":"error","error":"down"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.name == "http 500" {
					w.WriteHeader(http.StatusInternalServerError)
				}
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
			if _, err := runner.resource(context.Background(), "skopos-loki", "loki/api/v1/labels"); err == nil {
				t.Fatal("resource accepted a broken response")
			}
		})
	}
}

func TestLokiStepErrors(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		fake lokiFake
		step func(context.Context, LokiJourney, LokiConfig) (string, error)
	}{
		{name: "labels missing", fake: lokiFake{badResource: true}, step: func(ctx context.Context, j LokiJourney, cfg LokiConfig) (string, error) { return j.labels(ctx, cfg) }},
		{name: "metric reference error", fake: lokiFake{referenceBroken: true}, step: func(ctx context.Context, j LokiJourney, cfg LokiConfig) (string, error) { return j.metric(ctx, cfg) }},
		{name: "unsupported missing result", fake: lokiFake{emptyResults: true}, step: func(ctx context.Context, j LokiJourney, cfg LokiConfig) (string, error) {
			return j.unsupported(ctx, cfg)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fake.t = t
			test.fake.now = now
			test.fake.serviceCount = 6
			server := httptest.NewServer(test.fake)
			defer server.Close()
			runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
			if _, err := test.step(context.Background(), runner, runner.defaults()); err == nil {
				t.Fatal("step accepted a broken response")
			}
		})
	}
}

func TestLogEntriesRejectsBadValues(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		patch func(*frame)
	}{
		{name: "labels", patch: func(f *frame) { f.Data.Values[0][0] = json.RawMessage(`"x"`) }},
		{name: "line", patch: func(f *frame) { f.Data.Values[2][0] = json.RawMessage(`{}`) }},
		{name: "time", patch: func(f *frame) { f.Data.Values[3][0] = json.RawMessage(`{}`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := logFrame(now, "skopos-e2e-server-0", "line")
			test.patch(&f)
			if _, err := logEntries([]frame{f}); err == nil {
				t.Fatal("logEntries accepted a bad value")
			}
		})
	}
}

func TestLogEntriesTimeFallback(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	f := logFrame(now, "skopos-e2e-server-0", "line")
	// Drop tsNs and use the numeric Time field instead.
	f.Schema.Fields = f.Schema.Fields[:3]
	f.Data.Values = f.Data.Values[:3]
	entries, err := logEntries([]frame{f})
	if err != nil {
		t.Fatalf("logEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Time != fmt.Sprintf("%d", now.UnixMilli()) {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestLokiJSONParserReferenceError(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6, referenceBroken: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	if _, err := runner.jsonParser(context.Background(), runner.defaults()); err == nil {
		t.Fatal("jsonParser accepted a broken reference")
	}
}

func TestLokiMetricMissingResult(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6, emptyResults: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	if _, err := runner.metric(context.Background(), runner.defaults()); err == nil {
		t.Fatal("metric accepted an empty result")
	}
}

func TestCompareLogs(t *testing.T) {
	base := []logEntry{{Time: "1", Line: "a", Service: "s"}}
	if err := compareLogs(base, base); err != nil {
		t.Fatalf("equal logs: %v", err)
	}
	if err := compareLogs(nil, base); err == nil {
		t.Fatal("compareLogs accepted a different count")
	}
	if err := compareLogs(base, []logEntry{{Time: "1", Line: "b", Service: "s"}}); err == nil {
		t.Fatal("compareLogs accepted a different line")
	}
}

func TestLokiLineFilterReferenceError(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6, referenceBroken: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	if _, err := runner.lineFilter(context.Background(), runner.defaults()); err == nil {
		t.Fatal("lineFilter accepted a broken reference")
	}
}

func TestLogEntriesArrayMismatch(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	f := logFrame(now, "skopos-e2e-server-0", "line")
	f.Data.Values[2] = f.Data.Values[2][:0]
	if _, err := logEntries([]frame{f}); err == nil {
		t.Fatal("logEntries accepted arrays of different lengths")
	}
}

func TestLokiJourneyLabelFailure(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6, badResource: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	if _, err := runner.Run(context.Background()); err == nil {
		t.Fatal("journey accepted broken labels")
	}
}

func TestLokiUnsupportedMessageMismatch(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := lokiFake{t: t, now: now, serviceCount: 6, wrongUnsupported: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	runner := LokiJourney{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: LokiConfig{Now: now}}
	if _, err := runner.unsupported(context.Background(), runner.defaults()); err == nil {
		t.Fatal("unsupported accepted an unrelated error")
	}
}

func TestLogEntriesSkipsFramesWithoutLabels(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	f := logFrame(now, "skopos-e2e-server-0", "line")
	f.Schema.Fields = f.Schema.Fields[1:]
	entries, err := logEntries([]frame{f})
	if err != nil {
		t.Fatalf("logEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(entries))
	}
}
