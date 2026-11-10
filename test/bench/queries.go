package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// query is one benchmark request. Minutes is expanded into start/end relative
// to the run's fixed "now" so every client hits the same time range.
type query struct {
	Name    string
	Kind    string
	Path    string
	Params  map[string]string
	Minutes int
}

// queries mirrors the Prometheus, Tempo, and Loki workload measured by the
// Python harness this port replaces. Keep the list and order stable: the
// parity fixtures are recorded against it.
var queries = []query{
	{
		Name:   "prom_instant_count",
		Kind:   "prom",
		Path:   "/api/v1/query",
		Params: map[string]string{"query": "sum(rate(http_requests_total[5m]))"},
	},
	{
		Name:   "prom_instant_by_service",
		Kind:   "prom",
		Path:   "/api/v1/query",
		Params: map[string]string{"query": "sum by (service_name) (rate(http_requests_total[5m]))"},
	},
	{
		Name: "prom_histogram_p95",
		Kind: "prom",
		Path: "/api/v1/query",
		Params: map[string]string{
			"query": "histogram_quantile(0.95, sum by (le) (rate(http_request_duration_bucket[5m])))",
		},
	},
	{
		Name:    "prom_range_6h",
		Kind:    "prom_range",
		Path:    "/api/v1/query_range",
		Params:  map[string]string{"query": "sum(rate(http_requests_total[5m]))", "step": "60s"},
		Minutes: 360,
	},
	{
		Name:    "prom_series",
		Kind:    "prom",
		Path:    "/api/v1/series",
		Params:  map[string]string{"match[]": `http_requests_total{service_name="service-3"}`},
		Minutes: 60,
	},
	{
		Name:   "prom_label_values",
		Kind:   "prom",
		Path:   "/api/v1/label/service_name/values",
		Params: map[string]string{},
	},
	{
		Name: "tempo_search",
		Kind: "tempo",
		Path: "/api/search",
		Params: map[string]string{
			"q":     `{resource.service.name="service-3"}`,
			"limit": "20",
		},
		Minutes: 60,
	},
	{
		Name:   "tempo_trace_by_id",
		Kind:   "tempo",
		Path:   "/api/traces/{trace_id}",
		Params: map[string]string{},
	},
	{
		Name:    "tempo_tags",
		Kind:    "tempo",
		Path:    "/api/v2/search/tags",
		Params:  map[string]string{},
		Minutes: 60,
	},
	{
		Name: "loki_query_range",
		Kind: "loki",
		Path: "/loki/api/v1/query_range",
		Params: map[string]string{
			"query": `{service_name="service-3"} |= "failed"`,
			"limit": "100",
		},
		Minutes: 60,
	},
}

// buildRequest substitutes the trace ID and expands Minutes into start/end.
func buildRequest(q query, traceID string, now int64) (string, url.Values) {
	path := strings.ReplaceAll(q.Path, "{trace_id}", traceID)
	params := make(url.Values, len(q.Params)+3)
	for key, value := range q.Params {
		params.Set(key, value)
	}
	if q.Minutes > 0 {
		params.Set("start", strconv.FormatInt(now-int64(q.Minutes)*60, 10))
		params.Set("end", strconv.FormatInt(now, 10))
	}
	return path, params
}

// statusError is a non-2xx response. Keeping the status separate lets the
// benchmark and its tests classify failures without parsing error strings.
type statusError struct {
	Code       int
	Status     string
	RetryAfter string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("HTTP %d %s", e.Code, e.Status)
}

// apiClient issues benchmark requests against one gateway.
type apiClient struct {
	baseURL string
	client  *http.Client
}

func newAPIClient(baseURL string, timeout time.Duration) *apiClient {
	return &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: timeout,
			// The Python harness opened a new connection per request. Keep that
			// methodology so re-run latencies stay comparable with the recorded
			// benchmark tables.
			Transport: &http.Transport{
				Proxy:             http.ProxyFromEnvironment,
				DisableKeepAlives: true,
			},
		},
	}
}

// getJSON issues one GET request and returns the raw body size and decoded
// object. Non-2xx responses are typed errors.
func (c *apiClient) getJSON(ctx context.Context, path string, params url.Values) (int, map[string]any, error) {
	endpoint := c.baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return 0, nil, &statusError{
			Code:       response.StatusCode,
			Status:     response.Status,
			RetryAfter: response.Header.Get("Retry-After"),
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, nil, fmt.Errorf("decode response: %w", err)
	}
	return len(body), payload, nil
}

// checkResponse applies the same success contract as the gateway clients: an
// explicit error is a failure, and each head has a required success shape.
func checkResponse(kind string, payload map[string]any) error {
	if status, _ := payload["status"].(string); status == "error" {
		if message, ok := payload["error"].(string); ok && message != "" {
			return fmt.Errorf("%s", message)
		}
		return fmt.Errorf("gateway returned an error")
	}
	switch kind {
	case "prom", "prom_range":
		if status, _ := payload["status"].(string); status != "success" {
			return fmt.Errorf("unexpected Prometheus response")
		}
	case "tempo":
		for _, key := range []string{"traces", "scopes", "resourceSpans"} {
			if _, ok := payload[key]; ok {
				return nil
			}
		}
		return fmt.Errorf("unexpected Tempo response")
	case "loki":
		if status, _ := payload["status"].(string); status != "success" {
			return fmt.Errorf("unexpected Loki response")
		}
	}
	return nil
}

// measurement is one timed request and enough response detail for the
// concurrency gate to distinguish deliberate shedding from a real failure.
type measurement struct {
	elapsed    float64
	bytes      int
	status     int
	retryAfter bool
	err        error
}

// measure issues one request. The response size is zero when the request
// failed, matching the Python harness.
func measure(ctx context.Context, client *apiClient, q query, path string, params url.Values) measurement {
	started := time.Now()
	result := measurement{status: http.StatusOK}
	payload, err := func() (map[string]any, error) {
		size, payload, err := client.getJSON(ctx, path, params)
		if err != nil {
			return nil, err
		}
		result.bytes = size
		return payload, nil
	}()
	if err == nil {
		err = checkResponse(q.Kind, payload)
	}
	result.elapsed = float64(time.Since(started).Microseconds()) / 1000.0
	result.err = err
	var status *statusError
	if errors.As(err, &status) {
		result.status = status.Code
		result.retryAfter = status.RetryAfter != ""
	}
	return result
}

// resolveTraceID finds one trace for the trace-by-ID query.
func resolveTraceID(ctx context.Context, client *apiClient, now int64) (string, error) {
	params := url.Values{
		"q":     {`{resource.service.name="service-3"}`},
		"start": {strconv.FormatInt(now-3600, 10)},
		"end":   {strconv.FormatInt(now, 10)},
		"limit": {"1"},
	}
	_, payload, err := client.getJSON(ctx, "/api/search", params)
	if err != nil {
		return "", err
	}
	traces, _ := payload["traces"].([]any)
	if len(traces) == 0 {
		return "", nil
	}
	first, _ := traces[0].(map[string]any)
	traceID, _ := first["traceID"].(string)
	return traceID, nil
}
