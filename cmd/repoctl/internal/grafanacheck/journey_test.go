package grafanacheck

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type journeyFake struct {
	t               *testing.T
	now             time.Time
	referenceDelta  float64
	serviceCount    int
	querySeries     int
	acceptSubquery  bool
	referenceBroken bool
	dropService     bool
	missingLabel    bool
	emptyResults    bool
}

func (f journeyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/datasources":
		_, _ = fmt.Fprint(w, `[{"name":"Skopos Prometheus","uid":"skopos-prometheus","type":"prometheus"},{"name":"Prometheus","uid":"prom","type":"prometheus"},{"name":"Reference Prometheus","uid":"reference-prometheus","type":"prometheus"}]`)
	case strings.HasPrefix(r.URL.Path, "/api/datasources/uid/") && strings.HasSuffix(r.URL.Path, "/health"):
		_, _ = fmt.Fprint(w, `{"status":"OK","message":""}`)
	case r.URL.Path == "/api/ds/query":
		f.query(w, r)
	case strings.Contains(r.URL.Path, "/resources/api/v1/label/service_name/values"):
		f.values(w)
	case strings.Contains(r.URL.Path, "/resources/api/v1/labels"):
		if f.missingLabel {
			_, _ = fmt.Fprint(w, `{"status":"success","data":["__name__"]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"success","data":["__name__","service_name"]}`)
	case strings.Contains(r.URL.Path, "/resources/api/v1/series"):
		f.series(w)
	default:
		f.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f journeyFake) query(w http.ResponseWriter, r *http.Request) {
	var body queryRequest
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
	q := body.Queries[0]
	uid := q.Datasource.UID
	if uid == "reference-prometheus" && f.referenceBroken {
		_, _ = fmt.Fprint(w, `{"results":{"A":{"status":500,"error":"reference down"}}}`)
		return
	}
	if f.emptyResults {
		_, _ = fmt.Fprint(w, `{"results":{}}`)
		return
	}
	if strings.Contains(q.Expr, "10m:]") {
		if f.acceptSubquery {
			frames(w, uid, nil, 78)
			return
		}
		if uid == "skopos-prometheus" {
			_, _ = fmt.Fprint(w, `{"results":{"A":{"status":400,"error":"bad_data: prom: subqueries are not supported","frames":[]}}}`)
			return
		}
		frames(w, uid, nil, 78)
		return
	}
	if strings.Contains(q.Expr, "by (service_name)") {
		count := f.querySeries
		if count == 0 {
			count = f.serviceCount
		}
		out := make([]frame, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, instantFrame(f.now, map[string]string{
				"service_name": fmt.Sprintf("skopos-e2e-server-%d", i),
			}, 13+f.referenceDeltaFor(uid)))
		}
		writeFrames(w, out)
		return
	}
	if strings.Contains(q.Expr, "rate(") {
		frames(w, uid, nil, 0.2+f.referenceDeltaFor(uid))
		return
	}
	frames(w, uid, nil, 78+f.referenceDeltaFor(uid))
}

func (f journeyFake) referenceDeltaFor(uid string) float64 {
	if uid == "reference-prometheus" {
		return f.referenceDelta
	}
	return 0
}

func (f journeyFake) values(w http.ResponseWriter) {
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

func (f journeyFake) series(w http.ResponseWriter) {
	count := f.serviceCount
	if f.dropService {
		count--
	}
	items := make([]map[string]string, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, map[string]string{
			"__name__":     "skopos_e2e_requests_total",
			"service_name": fmt.Sprintf("skopos-e2e-server-%d", i),
		})
	}
	encoded, _ := json.Marshal(map[string]any{"status": "success", "data": items})
	_, _ = w.Write(encoded)
}

func frames(w http.ResponseWriter, uid string, labels map[string]string, value float64) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	writeFrames(w, []frame{instantFrame(now, labels, value)})
}

func instantFrame(now time.Time, labels map[string]string, value float64) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{
		{Name: "Time", Type: "time"},
		{Name: "Value", Type: "number", Labels: labels},
	}
	f.Data.Values = [][]json.RawMessage{
		{json.RawMessage(fmt.Sprintf("%d", now.UnixMilli()))},
		{json.RawMessage(fmt.Sprintf("%v", value))},
	}
	return f
}

func writeFrames(w http.ResponseWriter, frames []frame) {
	encoded, _ := json.Marshal(map[string]any{
		"results": map[string]any{"A": map[string]any{"status": 200, "frames": frames}},
	})
	_, _ = w.Write(encoded)
}

func journeyClient(t *testing.T, fake journeyFake) Client {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return Client{BaseURL: server.URL, HTTP: server.Client()}
}

func TestJourneyPasses(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6}
	runner := Journey{Client: journeyClient(t, fake), Config: JourneyConfig{Now: now}}
	steps, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("journey: %v", err)
	}
	if len(steps) != 7 {
		t.Fatalf("steps = %d, want 7", len(steps))
	}
}

func TestJourneyFrameMismatch(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6, referenceDelta: 1}
	runner := Journey{Client: journeyClient(t, fake), Config: JourneyConfig{Now: now}}
	_, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "instant query") {
		t.Fatalf("error = %v, want an instant-query mismatch", err)
	}
}

func TestJourneySeriesCountMismatch(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6, querySeries: 5}
	runner := Journey{Client: journeyClient(t, fake), Config: JourneyConfig{Now: now}}
	_, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "six-series") {
		t.Fatalf("error = %v, want a six-series failure", err)
	}
}

func TestJourneySubqueryAcceptedFails(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6, acceptSubquery: true}
	runner := Journey{Client: journeyClient(t, fake), Config: JourneyConfig{Now: now}}
	_, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "subquery") {
		t.Fatalf("error = %v, want a subquery failure", err)
	}
}

func TestResolveRateInterval(t *testing.T) {
	tests := []struct {
		step, scrape, want time.Duration
	}{
		{step: 30 * time.Second, scrape: 30 * time.Second, want: 2 * time.Minute},
		{step: 5 * time.Minute, scrape: 30 * time.Second, want: 5*time.Minute + 30*time.Second},
		{step: time.Minute, scrape: time.Minute, want: 4 * time.Minute},
	}
	for _, test := range tests {
		if got := resolveRateInterval(test.step, test.scrape); got != test.want {
			t.Errorf("resolveRateInterval(%s, %s) = %s, want %s", test.step, test.scrape, got, test.want)
		}
	}
}

func TestCompareFramesRejections(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	base := instantFrame(now, map[string]string{"service_name": "a"}, 1)
	tests := []struct {
		name string
		got  []frame
		want []frame
	}{
		{name: "series count", got: []frame{base}, want: nil},
		{name: "missing series", got: []frame{base}, want: []frame{instantFrame(now, map[string]string{"service_name": "b"}, 1)}},
		{name: "point count", got: []frame{twoPointFrame(now)}, want: []frame{instantFrame(now, map[string]string{}, 1)}},
		{
			name: "time",
			got:  []frame{instantFrame(now, map[string]string{"service_name": "a"}, 1)},
			want: []frame{instantFrame(now.Add(time.Minute), map[string]string{"service_name": "a"}, 1)},
		},
		{
			name: "value",
			got:  []frame{instantFrame(now, map[string]string{"service_name": "a"}, 1)},
			want: []frame{instantFrame(now, map[string]string{"service_name": "a"}, 2)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := compareFrames(test.got, test.want, time.Second); err == nil {
				t.Fatal("compareFrames accepted a mismatch")
			}
		})
	}
}

func TestCompareFramesAcceptsTimeTolerance(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	got := []frame{instantFrame(now, map[string]string{"service_name": "a"}, 1)}
	want := []frame{instantFrame(now.Add(500*time.Millisecond), map[string]string{"service_name": "a"}, 1)}
	if err := compareFrames(got, want, time.Second); err != nil {
		t.Fatalf("compareFrames: %v", err)
	}
}

func TestFrameSeriesRejectsMismatchedLengths(t *testing.T) {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "Time"}, {Name: "Value"}}
	f.Data.Values = [][]json.RawMessage{
		{json.RawMessage("1"), json.RawMessage("2")},
		{json.RawMessage("1")},
	}
	if _, err := frameSeries([]frame{f}); err == nil {
		t.Fatal("frameSeries accepted mismatched arrays")
	}
}

func TestRawFloatAndCloseEnough(t *testing.T) {
	if value, err := rawFloat(json.RawMessage("null")); err != nil || !math.IsNaN(value) {
		t.Fatalf("null = %v, %v; want NaN", value, err)
	}
	if _, err := rawFloat(json.RawMessage(`"x"`)); err == nil {
		t.Fatal("rawFloat accepted a string")
	}
	if !closeEnough(math.NaN(), math.NaN()) {
		t.Fatal("NaN should equal NaN")
	}
	if closeEnough(math.NaN(), 1) {
		t.Fatal("NaN should not equal a number")
	}
	if !closeEnough(1.0, 1.0+1e-12) {
		t.Fatal("values within tolerance should match")
	}
}

func TestJourneyQueryErrors(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "missing result", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"results":{}}`) }},
		{name: "http error", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(test.handler))
			defer server.Close()
			runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: JourneyConfig{Now: now}}
			if _, err := runner.Run(context.Background()); err == nil {
				t.Fatal("journey accepted a broken query response")
			}
		})
	}
}

func TestJourneyResourceErrors(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "error field", body: `{"status":"error","error":"down"}`, status: http.StatusOK},
		{name: "bad json", body: `{`, status: http.StatusOK},
		{name: "http error", body: `nope`, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}, Config: JourneyConfig{Now: now}}
			_, err := runner.resource(context.Background(), "skopos-prometheus", "api/v1/labels", runner.defaults())
			if test.name == "http error" {
				if err == nil {
					t.Fatal("resource accepted HTTP 500")
				}
				return
			}
			if err == nil {
				t.Fatal("resource accepted a broken body")
			}
		})
	}
}

func TestJourneyDefaults(t *testing.T) {
	cfg := (Journey{}).defaults()
	if cfg.SkoposUID == "" || cfg.ReferenceUID == "" || cfg.Step <= 0 || cfg.Metric == "" || cfg.ExpectedSeries == 0 {
		t.Fatalf("defaults are incomplete: %+v", cfg)
	}
	if _, err := (Journey{Client: Client{BaseURL: "http://127.0.0.1:1"}}).Run(context.Background()); err == nil {
		t.Fatal("journey with an unreachable Grafana succeeded")
	}
}

func TestCommandJourney(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	provisioning := writeProvisioning(t)
	env, stdout, _ := testEnv()
	env.Now = func() time.Time { return now }
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL,
		"--provisioning", provisioning,
		"--journey",
	})
	if err != nil {
		t.Fatalf("grafana-check --journey: %v", err)
	}
	for _, want := range []string{"journey instant query OK", "7 journey steps passed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func twoPointFrame(now time.Time) frame {
	var f frame
	f.Schema.Fields = []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Labels map[string]string `json:"labels"`
	}{{Name: "Time"}, {Name: "Value"}}
	f.Data.Values = [][]json.RawMessage{
		{json.RawMessage(fmt.Sprintf("%d", now.UnixMilli())), json.RawMessage(fmt.Sprintf("%d", now.Add(time.Minute).UnixMilli()))},
		{json.RawMessage("1"), json.RawMessage("2")},
	}
	return f
}

func TestJourneyStepFailures(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	type stepFunc func(context.Context, Journey, JourneyConfig) (string, error)
	tests := []struct {
		name string
		fake journeyFake
		step stepFunc
	}{
		{name: "range reference error", fake: journeyFake{referenceBroken: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) { return j.rangeQuery(ctx, cfg) }},
		{name: "label values differ", fake: journeyFake{dropService: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) {
			return j.labelValues(ctx, cfg)
		}},
		{name: "missing label", fake: journeyFake{missingLabel: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) {
			return j.metricLabels(ctx, cfg)
		}},
		{name: "series differ", fake: journeyFake{dropService: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) { return j.series(ctx, cfg) }},
		{name: "six series reference error", fake: journeyFake{referenceBroken: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) { return j.sixSeries(ctx, cfg) }},
		{name: "subquery without a result", fake: journeyFake{emptyResults: true}, step: func(ctx context.Context, j Journey, cfg JourneyConfig) (string, error) { return j.subquery(ctx, cfg) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.fake.t = t
			test.fake.now = now
			test.fake.serviceCount = 6
			runner := Journey{Client: journeyClient(t, test.fake), Config: JourneyConfig{Now: now}}
			if _, err := test.step(context.Background(), runner, runner.defaults()); err == nil {
				t.Fatal("step accepted a broken response")
			}
		})
	}
}
