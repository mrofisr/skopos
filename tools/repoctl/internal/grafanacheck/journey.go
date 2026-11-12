package grafanacheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// JourneyConfig drives the Prometheus journey. Times are injected so tests do
// not depend on the wall clock.
type JourneyConfig struct {
	SkoposUID      string
	ReferenceUID   string
	Now            time.Time
	Step           time.Duration
	ScrapeInterval time.Duration
	Window         time.Duration
	Metric         string
	ExpectedSeries int
}

// Journey runs the Prometheus journey through Grafana's own query API and
// compares every frame with the reference Prometheus datasource.
type Journey struct {
	Client Client
	Config JourneyConfig
}

// JourneyStep is one completed step.
type JourneyStep struct {
	Name   string
	Detail string
}

type queryRequest struct {
	Queries []query `json:"queries"`
	From    string  `json:"from"`
	To      string  `json:"to"`
}

type query struct {
	RefID      string          `json:"refId"`
	Datasource queryDatasource `json:"datasource"`
	Expr       string          `json:"expr"`
	Instant    bool            `json:"instant"`
	Range      bool            `json:"range"`
	IntervalMS int64           `json:"intervalMs"`
	MaxPoints  int             `json:"maxDataPoints"`
}

type queryDatasource struct {
	UID string `json:"uid"`
}

type queryResponse struct {
	Results map[string]queryResult `json:"results"`
}

type queryResult struct {
	Status int     `json:"status"`
	Error  string  `json:"error"`
	Frames []frame `json:"frames"`
}

type frame struct {
	Schema struct {
		Name   string `json:"name"`
		Fields []struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Labels map[string]string `json:"labels"`
		} `json:"fields"`
	} `json:"schema"`
	Data struct {
		Values [][]json.RawMessage `json:"values"`
	} `json:"data"`
}

type point struct {
	T int64
	V float64
}

// Run executes the journey and returns the steps in order. The first failure
// names the step.
func (j Journey) Run(ctx context.Context) ([]JourneyStep, error) {
	cfg := j.defaults()
	steps := []struct {
		name string
		run  func(context.Context, JourneyConfig) (string, error)
	}{
		{"instant query", j.instant},
		{"range query with resolved $__rate_interval", j.rangeQuery},
		{"label_values variable", j.labelValues},
		{"metric browser labels", j.metricLabels},
		{"metric browser series", j.series},
		{"six-series panel", j.sixSeries},
		{"unsupported subquery", j.subquery},
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

func (j Journey) defaults() JourneyConfig {
	return j.Config.withDefaults()
}

func (cfg JourneyConfig) withDefaults() JourneyConfig {
	if cfg.SkoposUID == "" {
		cfg.SkoposUID = "skopos-prometheus"
	}
	if cfg.ReferenceUID == "" {
		cfg.ReferenceUID = "reference-prometheus"
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	if cfg.Step <= 0 {
		cfg.Step = 30 * time.Second
	}
	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 30 * time.Second
	}
	if cfg.Window <= 0 {
		cfg.Window = 6 * time.Minute
	}
	if cfg.Metric == "" {
		cfg.Metric = "skopos_e2e_requests_total"
	}
	if cfg.ExpectedSeries <= 0 {
		cfg.ExpectedSeries = 6
	}
	return cfg
}

func (j Journey) instant(ctx context.Context, cfg JourneyConfig) (string, error) {
	expr := "sum(" + cfg.Metric + ")"
	got, err := j.query(ctx, cfg.SkoposUID, expr, cfg, true)
	if err != nil {
		return "", err
	}
	want, err := j.query(ctx, cfg.ReferenceUID, expr, cfg, true)
	if err != nil {
		return "", err
	}
	if err := compareFrames(got, want, time.Second); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	return fmt.Sprintf("%s matches the reference", expr), nil
}

func (j Journey) rangeQuery(ctx context.Context, cfg JourneyConfig) (string, error) {
	interval := resolveRateInterval(cfg.Step, cfg.ScrapeInterval)
	expr := fmt.Sprintf("sum(rate(%s[%s]))", cfg.Metric, interval)
	got, err := j.query(ctx, cfg.SkoposUID, expr, cfg, false)
	if err != nil {
		return "", err
	}
	want, err := j.query(ctx, cfg.ReferenceUID, expr, cfg, false)
	if err != nil {
		return "", err
	}
	if err := compareFrames(got, want, time.Second); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	return fmt.Sprintf("%s matches the reference", expr), nil
}

func (j Journey) labelValues(ctx context.Context, cfg JourneyConfig) (string, error) {
	got, err := j.resource(ctx, cfg.SkoposUID, "api/v1/label/service_name/values", cfg)
	if err != nil {
		return "", err
	}
	want, err := j.resource(ctx, cfg.ReferenceUID, "api/v1/label/service_name/values", cfg)
	if err != nil {
		return "", err
	}
	if err := compareStringSets("label values", got, want); err != nil {
		return "", err
	}
	if len(got) != cfg.ExpectedSeries {
		return "", fmt.Errorf("label values = %v, want %d services", got, cfg.ExpectedSeries)
	}
	return fmt.Sprintf("%d service values match the reference", len(got)), nil
}

func (j Journey) metricLabels(ctx context.Context, cfg JourneyConfig) (string, error) {
	got, err := j.resource(ctx, cfg.SkoposUID, "api/v1/labels", cfg)
	if err != nil {
		return "", err
	}
	want, err := j.resource(ctx, cfg.ReferenceUID, "api/v1/labels", cfg)
	if err != nil {
		return "", err
	}
	for _, label := range []string{"__name__", "service_name"} {
		if !contains(got, label) {
			return "", fmt.Errorf("skopos labels %v do not contain %q", got, label)
		}
		if !contains(want, label) {
			return "", fmt.Errorf("reference labels %v do not contain %q", want, label)
		}
	}
	return "label names match the reference", nil
}

func (j Journey) series(ctx context.Context, cfg JourneyConfig) (string, error) {
	got, err := j.seriesFor(ctx, cfg.SkoposUID, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.seriesFor(ctx, cfg.ReferenceUID, cfg)
	if err != nil {
		return "", err
	}
	if err := compareStringSets("series", got, want); err != nil {
		return "", err
	}
	if len(got) != cfg.ExpectedSeries {
		return "", fmt.Errorf("series = %v, want %d", got, cfg.ExpectedSeries)
	}
	return fmt.Sprintf("%d series match the reference", len(got)), nil
}

func (j Journey) seriesFor(ctx context.Context, uid string, cfg JourneyConfig) ([]string, error) {
	path := "api/v1/series?match[]=" + url.QueryEscape(cfg.Metric)
	items, err := j.resourceValues(ctx, uid, path, cfg)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		var labels map[string]string
		if err := json.Unmarshal(raw, &labels); err != nil {
			return nil, fmt.Errorf("series entry: %w", err)
		}
		out = append(out, labelKey(labels))
	}
	sort.Strings(out)
	return out, nil
}

func (j Journey) sixSeries(ctx context.Context, cfg JourneyConfig) (string, error) {
	expr := fmt.Sprintf("sum by (service_name) (%s)", cfg.Metric)
	got, err := j.query(ctx, cfg.SkoposUID, expr, cfg, true)
	if err != nil {
		return "", err
	}
	want, err := j.query(ctx, cfg.ReferenceUID, expr, cfg, true)
	if err != nil {
		return "", err
	}
	if err := compareFrames(got, want, time.Second); err != nil {
		return "", fmt.Errorf("%s: %w", expr, err)
	}
	if len(got) < cfg.ExpectedSeries {
		return "", fmt.Errorf("%s returned %d series, want at least %d", expr, len(got), cfg.ExpectedSeries)
	}
	return fmt.Sprintf("%d series match the reference", len(got)), nil
}

func (j Journey) subquery(ctx context.Context, cfg JourneyConfig) (string, error) {
	expr := fmt.Sprintf("sum(rate(%s[5m]))[10m:]", cfg.Metric)
	results, status, body, err := j.doQuery(ctx, cfg.SkoposUID, expr, cfg, true)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK && len(results) == 0 {
		return "", fmt.Errorf("ds/query: HTTP %d: %s", status, bodySummary(body))
	}
	result, ok := results["A"]
	if !ok {
		return "", errors.New("no result for refId A")
	}
	if result.Error == "" && result.Status == http.StatusOK {
		return "", errors.New("subquery was accepted; want an explicit error")
	}
	if !strings.Contains(strings.ToLower(result.Error), "subquer") {
		return "", fmt.Errorf("error %q does not mention the subquery", result.Error)
	}
	return fmt.Sprintf("explicit error: %s", strings.TrimSpace(result.Error)), nil
}

func (j Journey) query(ctx context.Context, uid, expr string, cfg JourneyConfig, instant bool) ([]frame, error) {
	results, status, body, err := j.doQuery(ctx, uid, expr, cfg, instant)
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

func (j Journey) doQuery(ctx context.Context, uid, expr string, cfg JourneyConfig, instant bool) (map[string]queryResult, int, []byte, error) {
	body := queryRequest{
		Queries: []query{{
			RefID:      "A",
			Datasource: queryDatasource{UID: uid},
			Expr:       expr,
			Instant:    instant,
			Range:      !instant,
			IntervalMS: cfg.Step.Milliseconds(),
			MaxPoints:  100,
		}},
		From: fmt.Sprintf("%d", cfg.Now.Add(-cfg.Window).UnixMilli()),
		To:   fmt.Sprintf("%d", cfg.Now.UnixMilli()),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, nil, err
	}
	status, response, err := j.post(ctx, "/api/ds/query", encoded)
	if err != nil {
		return nil, 0, nil, err
	}
	var decoded queryResponse
	if err := json.Unmarshal(response, &decoded); err != nil {
		if status != http.StatusOK {
			return nil, status, response, nil
		}
		return nil, status, response, fmt.Errorf("decode ds/query: %w", err)
	}
	return decoded.Results, status, response, nil
}

func (j Journey) resource(ctx context.Context, uid, path string, cfg JourneyConfig) ([]string, error) {
	items, err := j.resourceValues(ctx, uid, path, cfg)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("resource value: %w", err)
		}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func (j Journey) resourceValues(ctx context.Context, uid, path string, cfg JourneyConfig) ([]json.RawMessage, error) {
	query := url.Values{}
	query.Set("start", fmt.Sprintf("%d", cfg.Now.Add(-cfg.Window).Unix()))
	query.Set("end", fmt.Sprintf("%d", cfg.Now.Unix()))
	full := "/api/datasources/uid/" + url.PathEscape(uid) + "/resources/" + path
	separator := "?"
	if strings.Contains(full, "?") {
		separator = "&"
	}
	status, body, err := j.get(ctx, full+separator+query.Encode())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", path, status, bodySummary(body))
	}
	var decoded struct {
		Data   []json.RawMessage `json:"data"`
		Error  string            `json:"error"`
		Status string            `json:"status"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if decoded.Error != "" {
		return nil, fmt.Errorf("%s: %s", path, decoded.Error)
	}
	return decoded.Data, nil
}

func (j Journey) get(ctx context.Context, path string) (int, []byte, error) {
	return j.Client.get(ctx, j.httpClient(), path, nil)
}

func (j Journey) post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	return postJSON(ctx, j.Client, path, body)
}

func (j Journey) httpClient() *http.Client {
	return httpClientFor(j.Client)
}

func httpClientFor(client Client) *http.Client {
	if client.HTTP != nil {
		return client.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func postJSON(ctx context.Context, client Client, path string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(client.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if client.Username != "" {
		request.SetBasicAuth(client.Username, client.Password)
	}
	response, err := httpClientFor(client).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, responseBody, nil
}

// resolveRateInterval mirrors Grafana's $__rate_interval: at least four scrape
// intervals, and at least one point plus a scrape interval.
func resolveRateInterval(step, scrape time.Duration) time.Duration {
	interval := 4 * scrape
	if candidate := step + scrape; candidate > interval {
		interval = candidate
	}
	return interval
}

func compareFrames(got, want []frame, timeTolerance time.Duration) error {
	gotSeries, err := frameSeries(got)
	if err != nil {
		return err
	}
	wantSeries, err := frameSeries(want)
	if err != nil {
		return err
	}
	if len(gotSeries) != len(wantSeries) {
		return fmt.Errorf("series count = %d, reference has %d", len(gotSeries), len(wantSeries))
	}
	keys := make([]string, 0, len(gotSeries))
	for key := range gotSeries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		gotPoints := gotSeries[key]
		wantPoints, ok := wantSeries[key]
		if !ok {
			return fmt.Errorf("series %s is missing from the reference", key)
		}
		if len(gotPoints) != len(wantPoints) {
			return fmt.Errorf("series %s has %d points, reference has %d", key, len(gotPoints), len(wantPoints))
		}
		for i := range gotPoints {
			if delta := gotPoints[i].T - wantPoints[i].T; delta < -timeTolerance.Milliseconds() || delta > timeTolerance.Milliseconds() {
				return fmt.Errorf("series %s point %d time = %d, reference %d", key, i, gotPoints[i].T, wantPoints[i].T)
			}
			if !closeEnough(gotPoints[i].V, wantPoints[i].V) {
				return fmt.Errorf("series %s point %d value = %v, reference %v", key, i, gotPoints[i].V, wantPoints[i].V)
			}
		}
	}
	return nil
}

func frameSeries(frames []frame) (map[string][]point, error) {
	out := make(map[string][]point, len(frames))
	for _, f := range frames {
		labels := map[string]string{}
		if len(f.Schema.Fields) > 1 {
			labels = f.Schema.Fields[1].Labels
		}
		key := labelKey(labels)
		if key == "" {
			key = f.Schema.Name
		}
		if len(f.Data.Values) < 2 {
			out[key] = nil
			continue
		}
		var times, values []json.RawMessage
		times = f.Data.Values[0]
		values = f.Data.Values[1]
		if len(times) != len(values) {
			return nil, fmt.Errorf("series %s has %d times and %d values", key, len(times), len(values))
		}
		points := make([]point, 0, len(times))
		for i := range times {
			t, err := rawFloat(times[i])
			if err != nil {
				return nil, fmt.Errorf("series %s time %d: %w", key, i, err)
			}
			v, err := rawFloat(values[i])
			if err != nil {
				return nil, fmt.Errorf("series %s value %d: %w", key, i, err)
			}
			points = append(points, point{T: int64(t), V: v})
		}
		out[key] = points
	}
	return out, nil
}

func rawFloat(raw json.RawMessage) (float64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "null" || text == "" {
		return math.NaN(), nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func closeEnough(got, want float64) bool {
	if math.IsNaN(got) || math.IsNaN(want) {
		return math.IsNaN(got) && math.IsNaN(want)
	}
	diff := math.Abs(got - want)
	return diff <= 1e-9 || diff <= 1e-9*math.Abs(want)
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key == "__name__" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
		b.WriteByte(',')
	}
	return b.String()
}

func compareStringSets(name string, got, want []string) error {
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("%s = %v, reference %v", name, got, want)
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
