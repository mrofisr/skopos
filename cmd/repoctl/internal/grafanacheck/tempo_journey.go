package grafanacheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// TempoConfig drives the Tempo journey.
type TempoConfig struct {
	SkoposUID    string
	ReferenceUID string
	Now          time.Time
	Window       time.Duration
	Limit        int
	TraceID      string
	Timeout      time.Duration
}

// TempoJourney runs Grafana's Tempo queries and compares them with the
// reference Tempo that was seeded from the same TraceQL corpus.
type TempoJourney struct {
	Client Client
	Config TempoConfig
}

type tempoQueryRequest struct {
	Queries []tempoQuery `json:"queries"`
	From    string       `json:"from"`
	To      string       `json:"to"`
}

type tempoQuery struct {
	RefID      string          `json:"refId"`
	Datasource queryDatasource `json:"datasource"`
	Query      string          `json:"query"`
	QueryType  string          `json:"queryType"`
	Limit      int             `json:"limit"`
}

type searchRow struct {
	TraceID  string
	Service  string
	Name     string
	Duration string
	Start    string
}

type spanRow struct {
	TraceID  string
	SpanID   string
	ParentID string
	Name     string
	Service  string
	Kind     string
	Status   string
	Start    string
	Duration string
}

// Run executes the Tempo journey steps in order.
func (j TempoJourney) Run(ctx context.Context) ([]JourneyStep, error) {
	cfg := j.defaults()
	steps := []struct {
		name string
		run  func(context.Context, TempoConfig) (string, error)
	}{
		{"tag search", j.tagSearch},
		{"traceql search", j.traceQLSearch},
		{"trace by ID", j.traceByID},
		{"tag autocomplete", j.tags},
		{"tag-value autocomplete", j.tagValues},
		{"unsupported structural operator", j.unsupported},
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

func (j TempoJourney) defaults() TempoConfig {
	cfg := j.Config
	if cfg.SkoposUID == "" {
		cfg.SkoposUID = "skopos-tempo"
	}
	if cfg.ReferenceUID == "" {
		cfg.ReferenceUID = "reference-tempo"
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Minute
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 100
	}
	if cfg.TraceID == "" {
		cfg.TraceID = "5bf92f3577b34da6a3ce929d0e0e4737"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return cfg
}

func (j TempoJourney) tagSearch(ctx context.Context, cfg TempoConfig) (string, error) {
	query := `{ resource.service.name = "cart" }`
	got, err := j.search(ctx, cfg.SkoposUID, query, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.waitForSearch(ctx, cfg.ReferenceUID, query, cfg)
	if err != nil {
		return "", err
	}
	if err := compareSearchRows(got, want); err != nil {
		return "", fmt.Errorf("%s: %w", query, err)
	}
	return fmt.Sprintf("%d traces match the reference", len(got)), nil
}

func (j TempoJourney) traceQLSearch(ctx context.Context, cfg TempoConfig) (string, error) {
	query := `{ span.http.method = "GET" }`
	got, err := j.search(ctx, cfg.SkoposUID, query, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.waitForSearch(ctx, cfg.ReferenceUID, query, cfg)
	if err != nil {
		return "", err
	}
	if err := compareSearchRows(got, want); err != nil {
		return "", fmt.Errorf("%s: %w", query, err)
	}
	if len(got) < 2 {
		return "", fmt.Errorf("%s returned %d traces, want at least 2", query, len(got))
	}
	return fmt.Sprintf("%d traces match the reference", len(got)), nil
}

func (j TempoJourney) traceByID(ctx context.Context, cfg TempoConfig) (string, error) {
	got, err := j.spans(ctx, cfg.SkoposUID, cfg.TraceID, cfg)
	if err != nil {
		return "", err
	}
	want, err := j.spans(ctx, cfg.ReferenceUID, cfg.TraceID, cfg)
	if err != nil {
		return "", err
	}
	if err := compareSpans(got, want); err != nil {
		return "", err
	}
	if len(got) == 0 {
		return "", fmt.Errorf("trace %s returned no spans", cfg.TraceID)
	}
	return fmt.Sprintf("%d spans match the reference", len(got)), nil
}

func (j TempoJourney) tags(ctx context.Context, cfg TempoConfig) (string, error) {
	got, err := j.resourceScopeTags(ctx, cfg.SkoposUID)
	if err != nil {
		return "", err
	}
	want, err := j.resourceScopeTags(ctx, cfg.ReferenceUID)
	if err != nil {
		return "", err
	}
	if err := compareStringSets("resource tags", got, want); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d resource tags match the reference", len(got)), nil
}

func (j TempoJourney) tagValues(ctx context.Context, cfg TempoConfig) (string, error) {
	got, err := j.tagValueSet(ctx, cfg.SkoposUID, "span:name")
	if err != nil {
		return "", err
	}
	want, err := j.tagValueSet(ctx, cfg.ReferenceUID, "span:name")
	if err != nil {
		return "", err
	}
	if err := compareStringSets("span:name values", got, want); err != nil {
		return "", err
	}
	if len(got) == 0 {
		return "", fmt.Errorf("span:name returned no values")
	}
	return fmt.Sprintf("%d span:name values match the reference", len(got)), nil
}

func (j TempoJourney) unsupported(ctx context.Context, cfg TempoConfig) (string, error) {
	query := `{ span:name = "GET /cart" } >> { span:name = "SELECT orders" }`
	status, results, body, err := j.doQuery(ctx, cfg.SkoposUID, query, "traceql", cfg)
	if err != nil {
		return "", err
	}
	result, ok := results["A"]
	if !ok {
		return "", fmt.Errorf("ds/query: HTTP %d: %s", status, bodySummary(body))
	}
	if result.Error == "" && result.Status == http.StatusOK {
		return "", fmt.Errorf("structural operator was accepted; want an explicit error")
	}
	if !strings.Contains(strings.ToLower(result.Error), "structural") {
		return "", fmt.Errorf("error %q does not name the structural operator", result.Error)
	}
	return fmt.Sprintf("explicit error: %s", strings.TrimSpace(result.Error)), nil
}

func (j TempoJourney) search(ctx context.Context, uid, query string, cfg TempoConfig) ([]searchRow, error) {
	frames, err := j.query(ctx, uid, query, "traceql", cfg)
	if err != nil {
		return nil, err
	}
	return searchRows(frames)
}

func (j TempoJourney) waitForSearch(ctx context.Context, uid, query string, cfg TempoConfig) ([]searchRow, error) {
	deadline := time.Now().Add(cfg.Timeout)
	var last error
	for {
		rows, err := j.search(ctx, uid, query, cfg)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
		last = err
		if time.Now().After(deadline) {
			if last != nil {
				return nil, fmt.Errorf("reference Tempo did not return %s within %s: %w", query, cfg.Timeout, last)
			}
			return nil, fmt.Errorf("reference Tempo did not return %s within %s", query, cfg.Timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (j TempoJourney) spans(ctx context.Context, uid, traceID string, cfg TempoConfig) ([]spanRow, error) {
	frames, err := j.query(ctx, uid, traceID, "traceId", cfg)
	if err != nil {
		return nil, err
	}
	return spanRows(frames)
}

func (j TempoJourney) query(ctx context.Context, uid, query, queryType string, cfg TempoConfig) ([]frame, error) {
	status, results, body, err := j.doQuery(ctx, uid, query, queryType, cfg)
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

func (j TempoJourney) doQuery(ctx context.Context, uid, query, queryType string, cfg TempoConfig) (int, map[string]queryResult, []byte, error) {
	body := tempoQueryRequest{
		Queries: []tempoQuery{{
			RefID:      "A",
			Datasource: queryDatasource{UID: uid},
			Query:      query,
			QueryType:  queryType,
			Limit:      cfg.Limit,
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

func (j TempoJourney) resourceScopeTags(ctx context.Context, uid string) ([]string, error) {
	full := "/api/datasources/proxy/uid/" + url.PathEscape(uid) + "/api/v2/search/tags"
	status, body, err := j.Client.get(ctx, httpClientFor(j.Client), full, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("search tags: HTTP %d: %s", status, bodySummary(body))
	}
	var decoded struct {
		Scopes []struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode search tags: %w", err)
	}
	for _, scope := range decoded.Scopes {
		if scope.Name == "resource" {
			sort.Strings(scope.Tags)
			return scope.Tags, nil
		}
	}
	return nil, fmt.Errorf("search tags has no resource scope")
}

func (j TempoJourney) tagValueSet(ctx context.Context, uid, tag string) ([]string, error) {
	full := "/api/datasources/proxy/uid/" + url.PathEscape(uid) + "/api/v2/search/tag/" + url.PathEscape(tag) + "/values"
	status, body, err := j.Client.get(ctx, httpClientFor(j.Client), full, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("tag values: HTTP %d: %s", status, bodySummary(body))
	}
	var decoded struct {
		TagValues []struct {
			Value string `json:"value"`
		} `json:"tagValues"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode tag values: %w", err)
	}
	values := make([]string, 0, len(decoded.TagValues))
	for _, value := range decoded.TagValues {
		values = append(values, value.Value)
	}
	sort.Strings(values)
	return values, nil
}

func searchRows(frames []frame) ([]searchRow, error) {
	out := make([]searchRow, 0)
	for _, f := range frames {
		index := fieldIndex(f)
		traceAt, ok := index["traceID"]
		if !ok {
			continue
		}
		rows := len(f.Data.Values[traceAt])
		serviceAt, _ := index["traceService"]
		nameAt, _ := index["traceName"]
		durationAt, _ := index["traceDuration"]
		startAt, _ := index["startTime"]
		for row := 0; row < rows; row++ {
			out = append(out, searchRow{
				TraceID:  rawText(f, traceAt, row),
				Service:  optionalText(f, serviceAt, row),
				Name:     optionalText(f, nameAt, row),
				Duration: optionalText(f, durationAt, row),
				Start:    optionalText(f, startAt, row),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TraceID < out[j].TraceID })
	return out, nil
}

func spanRows(frames []frame) ([]spanRow, error) {
	out := make([]spanRow, 0)
	for _, f := range frames {
		index := fieldIndex(f)
		spanAt, ok := index["spanID"]
		if !ok {
			continue
		}
		rows := len(f.Data.Values[spanAt])
		get := func(name string) int {
			if at, ok := index[name]; ok {
				return at
			}
			return -1
		}
		traceAt, parentAt, nameAt, serviceAt := get("traceID"), get("parentSpanID"), get("operationName"), get("serviceName")
		kindAt, statusAt, startAt, durationAt := get("kind"), get("statusCode"), get("startTime"), get("duration")
		for row := 0; row < rows; row++ {
			out = append(out, spanRow{
				TraceID:  optionalText(f, traceAt, row),
				SpanID:   rawText(f, spanAt, row),
				ParentID: optionalText(f, parentAt, row),
				Name:     optionalText(f, nameAt, row),
				Service:  optionalText(f, serviceAt, row),
				Kind:     optionalText(f, kindAt, row),
				Status:   optionalText(f, statusAt, row),
				Start:    optionalText(f, startAt, row),
				Duration: optionalText(f, durationAt, row),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpanID < out[j].SpanID })
	return out, nil
}

func fieldIndex(f frame) map[string]int {
	index := make(map[string]int, len(f.Schema.Fields))
	for i, field := range f.Schema.Fields {
		index[field.Name] = i
	}
	return index
}

func rawText(f frame, at, row int) string {
	if at < 0 || at >= len(f.Data.Values) || row >= len(f.Data.Values[at]) {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(f.Data.Values[at][row])), `"`)
}

func optionalText(f frame, at, row int) string {
	if at < 0 {
		return ""
	}
	return rawText(f, at, row)
}

func compareSearchRows(got, want []searchRow) error {
	if len(got) != len(want) {
		return fmt.Errorf("trace count = %d, reference has %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("trace %d = %+v, reference %+v", i, got[i], want[i])
		}
	}
	return nil
}

func compareSpans(got, want []spanRow) error {
	if len(got) != len(want) {
		return fmt.Errorf("span count = %d, reference has %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("span %d = %+v, reference %+v", i, got[i], want[i])
		}
	}
	return nil
}
