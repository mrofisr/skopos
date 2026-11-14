package grafanacheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// LokiConfig drives the Loki journey.
type LokiConfig struct {
	SkoposUID    string
	ReferenceUID string
	Now          time.Time
	Step         time.Duration
	Window       time.Duration
	Service      string
	Expected     int
}

// LokiJourney runs log queries through Grafana's Loki datasource and compares
// them with the reference Loki that was seeded from the same generator.
type LokiJourney struct {
	Client Client
	Config LokiConfig
}

type lokiQueryRequest struct {
	Queries []lokiQuery `json:"queries"`
	From    string      `json:"from"`
	To      string      `json:"to"`
}

type lokiQuery struct {
	RefID      string          `json:"refId"`
	Datasource queryDatasource `json:"datasource"`
	Expr       string          `json:"expr"`
	QueryType  string          `json:"queryType"`
	MaxLines   int             `json:"maxLines"`
	EditorMode string          `json:"editorMode"`
	IntervalMS int64           `json:"intervalMs"`
	MaxPoints  int             `json:"maxDataPoints"`
}

type logEntry struct {
	Time    string
	Line    string
	Service string
}

// Run executes the Loki journey steps in order.
func (j LokiJourney) Run(ctx context.Context) ([]JourneyStep, error) {
	cfg := j.defaults()
	steps := []struct {
		name string
		run  func(context.Context, LokiConfig) (string, error)
	}{
		{"labels", j.labels},
		{"label_values variable", j.labelValues},
		{"line filter", j.lineFilter},
		{"json parser", j.jsonParser},
		{"count_over_time", j.metric},
		{"unsupported stage", j.unsupported},
	}
	out := make([]JourneyStep, 0, len(steps))
	for _, step := range steps {
		detail, err := step.run(ctx, cfg)
		if err != nil {
			return out, fmt.Errorf("%s: %w", step.name, err)
		}
		out = append(out, JourneyStep{Name: step.name, Detail: detail})
	}
	return out, nil
}

func (j LokiJourney) defaults() LokiConfig {
	cfg := j.Config
	if cfg.SkoposUID == "" {
		cfg.SkoposUID = "skopos-loki"
	}
	if cfg.ReferenceUID == "" {
		cfg.ReferenceUID = "reference-loki"
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	if cfg.Step <= 0 {
		cfg.Step = 30 * time.Second
	}
	if cfg.Window <= 0 {
		cfg.Window = 6 * time.Minute
	}
	if cfg.Service == "" {
		cfg.Service = "skopos-e2e-server-0"
	}
	if cfg.Expected <= 0 {
		cfg.Expected = 6
	}
	return cfg
}

func (j LokiJourney) labels(ctx context.Context, cfg LokiConfig) (string, error) {
	got, err := j.resource(ctx, cfg.SkoposUID, "loki/api/v1/labels")
	if err != nil {
		return "", err
	}
	want, err := j.resource(ctx, cfg.ReferenceUID, "loki/api/v1/labels")
	if err != nil {
		return "", err
	}
	for _, label := range []string{"service_name"} {
		if !contains(got, label) {
			return "", fmt.Errorf("skopos labels %v do not contain %q", got, label)
		}
		if !contains(want, label) {
			return "", fmt.Errorf("reference labels %v do not contain %q", want, label)
		}
	}
	return "label names match the reference", nil
}

func (j LokiJourney) labelValues(ctx context.Context, cfg LokiConfig) (string, error) {
	got, err := j.resource(ctx, cfg.SkoposUID, "loki/api/v1/label/service_name/values")
	if err != nil {
		return "", err
	}
	want, err := j.resource(ctx, cfg.ReferenceUID, "loki/api/v1/label/service_name/values")
	if err != nil {
		return "", err
	}
	if err := compareStringSets("label values", got, want); err != nil {
		return "", err
	}
	if len(got) != cfg.Expected {
		return "", fmt.Errorf("label values = %v, want %d services", got, cfg.Expected)
	}
	return fmt.Sprintf("%d service values match the reference", len(got)), nil
}

func (j LokiJourney) lineFilter(ctx context.Context, cfg LokiConfig) (string, error) {
	// Every generated line contains "grafana journey log"; the filter still
	// exercises the line-filter path and must keep all of them.
	expr := fmt.Sprintf(`{service_name=%q} |= "grafana journey log"`, cfg.Service)
	got, err := j.logs(ctx, cfg.SkoposUID, expr, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.logs(ctx, cfg.ReferenceUID, expr, cfg)
	if err != nil {
		return "", err
	}
	if err := compareLogs(got, want); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	return fmt.Sprintf("%d log lines match the reference", len(got)), nil
}

func (j LokiJourney) jsonParser(ctx context.Context, cfg LokiConfig) (string, error) {
	expr := fmt.Sprintf(`{service_name=%q} | json | level="error"`, cfg.Service)
	got, err := j.logs(ctx, cfg.SkoposUID, expr, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.logs(ctx, cfg.ReferenceUID, expr, cfg)
	if err != nil {
		return "", err
	}
	if err := compareLogs(got, want); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	if len(got) == 0 {
		return "", fmt.Errorf("%s returned no log lines", expr)
	}
	return fmt.Sprintf("%d error log lines match the reference", len(got)), nil
}

func (j LokiJourney) metric(ctx context.Context, cfg LokiConfig) (string, error) {
	expr := fmt.Sprintf("sum(count_over_time({service_name=%q}[1m]))", cfg.Service)
	got, err := j.query(ctx, cfg.SkoposUID, expr, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.query(ctx, cfg.ReferenceUID, expr, cfg)
	if err != nil {
		return "", err
	}
	if err := compareFrames(got, want, time.Second); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	return fmt.Sprintf("%s matches the reference", expr), nil
}

func (j LokiJourney) unsupported(ctx context.Context, cfg LokiConfig) (string, error) {
	expr := fmt.Sprintf(`{service_name=%q} | unwrap duration`, cfg.Service)
	status, results, body, err := j.doQuery(ctx, cfg.SkoposUID, expr, cfg)
	if err != nil {
		return "", err
	}
	result, ok := results["A"]
	if !ok {
		return "", fmt.Errorf("ds/query: HTTP %d: %s", status, bodySummary(body))
	}
	if result.Error == "" && result.Status == http.StatusOK {
		return "", errors.New("unsupported stage was accepted; want an explicit error")
	}
	message := strings.ToLower(result.Error)
	if !strings.Contains(message, "unsupported") || !strings.Contains(message, "stage") {
		return "", fmt.Errorf("error %q does not name the unsupported stage", result.Error)
	}
	return fmt.Sprintf("explicit error: %s", strings.TrimSpace(result.Error)), nil
}

func (j LokiJourney) logs(ctx context.Context, uid, expr string, cfg LokiConfig) ([]logEntry, error) {
	frames, err := j.query(ctx, uid, expr, cfg)
	if err != nil {
		return nil, err
	}
	return logEntries(frames)
}

func (j LokiJourney) query(ctx context.Context, uid, expr string, cfg LokiConfig) ([]frame, error) {
	status, results, body, err := j.doQuery(ctx, uid, expr, cfg)
	if err != nil {
		return nil, err
	}
	result, ok := results["A"]
	if !ok {
		return nil, fmt.Errorf("ds/query: HTTP %d: %s", status, bodySummary(body))
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s: %s", uid, result.Error)
	}
	return result.Frames, nil
}

func (j LokiJourney) doQuery(ctx context.Context, uid, expr string, cfg LokiConfig) (int, map[string]queryResult, []byte, error) {
	body := lokiQueryRequest{
		Queries: []lokiQuery{{
			RefID:      "A",
			Datasource: queryDatasource{UID: uid},
			Expr:       expr,
			QueryType:  "range",
			MaxLines:   1000,
			EditorMode: "code",
			IntervalMS: cfg.Step.Milliseconds(),
			MaxPoints:  100,
		}},
		From: fmt.Sprintf("%d", cfg.Now.Add(-cfg.Window).UnixMilli()),
		To:   fmt.Sprintf("%d", cfg.Now.UnixMilli()),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, nil, err
	}
	status, response, err := postJSON(ctx, j.Client, "/api/ds/query", encoded)
	if err != nil {
		return 0, nil, nil, err
	}
	var decoded queryResponse
	if err := json.Unmarshal(response, &decoded); err != nil {
		if status != http.StatusOK {
			return status, nil, response, nil
		}
		return status, nil, response, fmt.Errorf("decode ds/query: %w", err)
	}
	return status, decoded.Results, response, nil
}

func (j LokiJourney) resource(ctx context.Context, uid, path string) ([]string, error) {
	full := "/api/datasources/proxy/uid/" + url.PathEscape(uid) + "/" + path
	status, body, err := j.Client.get(ctx, httpClientFor(j.Client), full, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", path, status, bodySummary(body))
	}
	var decoded struct {
		Data  []string `json:"data"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("%s: %s", path, decoded.Error)
	}
	return decoded.Data, nil
}

func logEntries(frames []frame) ([]logEntry, error) {
	out := make([]logEntry, 0)
	for _, f := range frames {
		index := make(map[string]int, len(f.Schema.Fields))
		for i, field := range f.Schema.Fields {
			index[field.Name] = i
		}
		labelsAt, ok := index["labels"]
		if !ok {
			continue
		}
		lineAt, hasLine := index["Line"]
		timeAt, hasTime := index["tsNs"]
		if !hasTime {
			timeAt, hasTime = index["Time"]
		}
		if !hasLine || !hasTime {
			return nil, fmt.Errorf("log frame is missing Line or a timestamp field")
		}
		if len(f.Data.Values) <= lineAt || len(f.Data.Values) <= timeAt {
			return nil, errors.New("log frame has fewer value arrays than fields")
		}
		if len(f.Data.Values[labelsAt]) != len(f.Data.Values[lineAt]) ||
			len(f.Data.Values[labelsAt]) != len(f.Data.Values[timeAt]) {
			return nil, errors.New("log frame arrays have different lengths")
		}
		for row := 0; row < len(f.Data.Values[labelsAt]); row++ {
			var labels map[string]string
			if err := json.Unmarshal(f.Data.Values[labelsAt][row], &labels); err != nil {
				return nil, fmt.Errorf("decode log labels: %w", err)
			}
			var line string
			if err := json.Unmarshal(f.Data.Values[lineAt][row], &line); err != nil {
				return nil, fmt.Errorf("decode log line: %w", err)
			}
			var ts string
			if err := json.Unmarshal(f.Data.Values[timeAt][row], &ts); err != nil {
				var number float64
				if err := json.Unmarshal(f.Data.Values[timeAt][row], &number); err != nil {
					return nil, fmt.Errorf("decode log time: %w", err)
				}
				ts = fmt.Sprintf("%d", int64(number))
			}
			out = append(out, logEntry{Time: ts, Line: line, Service: labels["service_name"]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

func compareLogs(got, want []logEntry) error {
	if len(got) != len(want) {
		return fmt.Errorf("log line count = %d, reference has %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("log line %d = %+v, reference %+v", i, got[i], want[i])
		}
	}
	return nil
}
