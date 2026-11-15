package prom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/metrics"
	"github.com/mrofisr/skopos/internal/ttlcache"
)

// QueryEngine is the subset of Engine used by the HTTP API.
type QueryEngine interface {
	Query(ctx context.Context, expr string, at time.Time) (Value, error)
	QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration) (Matrix, error)
}

// SeriesFetcher is the storage surface used by the metadata endpoints.
type SeriesFetcher interface {
	Fetch(ctx context.Context, sel Selector, start, end time.Time) ([]Series, error)
	Metadata(ctx context.Context, metric string) (map[string]Metadata, error)
}

// SeriesLister is the optional label-only surface for /series. A backend that
// does not implement it keeps the Fetch path.
type SeriesLister interface {
	ListSeries(ctx context.Context, sel Selector, start, end time.Time) ([]map[string]string, error)
}

// LabelValueFetcher is the optional pushdown surface for label values. A
// backend that does not implement it keeps the Go evaluation path.
type LabelValueFetcher interface {
	LabelValues(ctx context.Context, name string, sel Selector, start, end time.Time, limit int) ([]string, error)
}

// LabelNameFetcher is the optional pushdown surface for label names.
type LabelNameFetcher interface {
	LabelNames(ctx context.Context, sel Selector, start, end time.Time, limit int) ([]string, error)
}

// API serves the Prometheus HTTP API endpoints used by Grafana.
type API struct {
	Engine   QueryEngine
	Fetcher  SeriesFetcher
	Version  string
	MaxRange time.Duration
	Now      func() time.Time
	// Timeout is the per-request query deadline. Zero disables the deadline.
	Timeout time.Duration
	// InstantCacheTTL bounds reuse of an instant result. Zero disables it.
	// The gateway sets one second; a request without an explicit time is
	// evaluated at the start of its second.
	InstantCacheTTL time.Duration

	cacheMu      sync.Mutex
	instantCache *ttlcache.Cache[instantKey, Value]
}

type instantKey struct {
	query string
	at    int64
}

// NewAPI returns a Prometheus-compatible API.
func NewAPI(engine QueryEngine, fetcher SeriesFetcher, version string) *API {
	return &API{Engine: engine, Fetcher: fetcher, Version: version, MaxRange: 7 * 24 * time.Hour, Now: time.Now}
}

// Register attaches the Prometheus routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		mux.HandleFunc(method+" /api/v1/query", a.handleQuery)
		mux.HandleFunc(method+" /api/v1/query_range", a.handleQueryRange)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		mux.HandleFunc(method+" /api/v1/series", a.handleSeries)
		mux.HandleFunc(method+" /api/v1/labels", a.handleLabels)
		mux.HandleFunc(method+" /api/v1/label/{name}/values", a.handleLabelValues)
		mux.HandleFunc(method+" /api/v1/metadata", a.handleMetadata)
		mux.HandleFunc(method+" /api/v1/query_exemplars", a.handleEmptyExemplars)
	}
	mux.HandleFunc("GET /api/v1/status/buildinfo", a.handleBuildInfo)
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// requestContext applies the configured query deadline. The returned cancel
// function is always safe to defer.
func (a *API) requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	if a.Timeout <= 0 {
		return r.Context(), func() {}
	}
	return context.WithTimeout(r.Context(), a.Timeout)
}

func (a *API) handleQuery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", "invalid form: "+err.Error())
		return
	}
	query := r.FormValue("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "query is required")
		return
	}
	explicitTime := r.FormValue("time") != ""
	at, err := a.parseTime(r.FormValue("time"), a.now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	evalAt := at
	var value Value
	if a.InstantCacheTTL > 0 {
		keyAt := at.UnixMilli()
		if !explicitTime {
			keyAt = at.Truncate(time.Second).UnixMilli()
		}
		evalAt = time.UnixMilli(keyAt)
		value, err = a.cachedInstant(ctx, instantKey{query: query, at: keyAt})
	} else {
		value, err = a.Engine.Query(ctx, query, at)
	}
	if err != nil {
		a.writeEngineError(w, r, err)
		return
	}
	data := map[string]any{"resultType": value.Type()}
	switch v := value.(type) {
	case Vector:
		data["result"] = vectorData(v)
	case Scalar:
		data["result"] = []any{float64(evalAt.UnixMilli()) / 1000, formatFloat(float64(v))}
	default:
		a.writeEngineError(w, r, fmt.Errorf("unsupported result type %T", value))
		return
	}
	writeSuccess(w, data)
}

// cachedInstant lazily builds the bounded instant cache. The loader gets a
// timeout equal to the API query budget, so a cache miss cannot outlive the
// request deadline by more than the configured timeout.
func (a *API) cachedInstant(ctx context.Context, key instantKey) (Value, error) {
	a.cacheMu.Lock()
	cache := a.instantCache
	if cache == nil {
		loadTimeout := a.Timeout
		if loadTimeout <= 0 {
			loadTimeout = 30 * time.Second
		}
		var err error
		cache, err = ttlcache.New(ttlcache.Options[instantKey, Value]{
			TTL:         a.InstantCacheTTL,
			MaxEntries:  64,
			LoadTimeout: loadTimeout,
			Clock:       a.Now,
			Load: func(ctx context.Context, key instantKey) (Value, error) {
				return a.Engine.Query(ctx, key.query, time.UnixMilli(key.at))
			},
		})
		if err != nil {
			a.cacheMu.Unlock()
			return nil, err
		}
		a.instantCache = cache
	}
	a.cacheMu.Unlock()
	return cache.Get(ctx, key)
}

func (a *API) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", "invalid form: "+err.Error())
		return
	}
	query := r.FormValue("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "query is required")
		return
	}
	start, err := a.parseRequiredTime(r.FormValue("start"), "start")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	end, err := a.parseRequiredTime(r.FormValue("end"), "end")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if end.Before(start) {
		writeError(w, http.StatusBadRequest, "bad_data", "end is before start")
		return
	}
	if a.MaxRange > 0 && end.Sub(start) > a.MaxRange {
		writeError(w, http.StatusBadRequest, "bad_data", fmt.Sprintf("query range exceeds %s", a.MaxRange))
		return
	}
	step, err := parseStep(r.FormValue("step"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	matrix, err := a.Engine.QueryRange(ctx, query, start, end, step)
	if err != nil {
		a.writeEngineError(w, r, err)
		return
	}
	result := make([]any, 0, len(matrix))
	for _, series := range matrix {
		values := make([]any, 0, len(series.Samples))
		for _, sample := range series.Samples {
			values = append(values, []any{float64(sample.T) / 1000, formatFloat(sample.V)})
		}
		result = append(result, map[string]any{"metric": series.Labels, "values": values})
	}
	writeSuccess(w, map[string]any{"resultType": "matrix", "result": result})
}

func (a *API) handleSeries(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", "invalid form: "+err.Error())
		return
	}
	start, end, err := a.rangeFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	selectors, err := parseSelectors(r.Form["match[]"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if len(selectors) == 0 {
		writeError(w, http.StatusBadRequest, "bad_data", "match[] is required")
		return
	}
	seen := make(map[string]map[string]string)
	if lister, ok := a.Fetcher.(SeriesLister); ok {
		for _, sel := range selectors {
			series, err := lister.ListSeries(ctx, sel, start, end)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, labels := range series {
				seen[labelsKey(labels)] = labels
			}
		}
	} else {
		for _, sel := range selectors {
			series, err := a.Fetcher.Fetch(ctx, sel, start, end)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, s := range series {
				seen[labelsKey(s.Labels)] = s.Labels
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	writeSuccess(w, out)
}

func (a *API) handleLabels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", "invalid form: "+err.Error())
		return
	}
	start, end, err := a.rangeFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	selectors, err := parseSelectors(r.Form["match[]"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if len(selectors) == 0 {
		selectors = []Selector{{}}
	}
	limit := 0
	if raw := r.FormValue("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "bad_data", "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}

	names := make(map[string]struct{})
	if fetcher, ok := a.Fetcher.(LabelNameFetcher); ok && allLabelValuesPushdown(selectors) {
		for _, sel := range selectors {
			found, err := fetcher.LabelNames(ctx, sel, start, end, limit)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, name := range found {
				names[name] = struct{}{}
			}
		}
	} else {
		for _, sel := range selectors {
			series, err := a.Fetcher.Fetch(ctx, sel, start, end)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, s := range series {
				for name := range s.Labels {
					names[name] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	writeSuccess(w, out)
}

func (a *API) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", "invalid form: "+err.Error())
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_data", "label name is required")
		return
	}
	start, end, err := a.rangeFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	selectors, err := parseSelectors(r.Form["match[]"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if len(selectors) == 0 {
		selectors = []Selector{{}}
	}
	limit := 0
	if raw := r.FormValue("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "bad_data", "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}

	values := make(map[string]struct{})
	if fetcher, ok := a.Fetcher.(LabelValueFetcher); ok && allLabelValuesPushdown(selectors) {
		for _, sel := range selectors {
			found, err := fetcher.LabelValues(ctx, name, sel, start, end, limit)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, value := range found {
				values[value] = struct{}{}
			}
		}
	} else {
		for _, sel := range selectors {
			series, err := a.Fetcher.Fetch(ctx, sel, start, end)
			if err != nil {
				a.writeEngineError(w, r, err)
				return
			}
			for _, s := range series {
				if v, ok := s.Labels[name]; ok && v != "" {
					values[v] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	writeSuccess(w, out)
}

func allLabelValuesPushdown(selectors []Selector) bool {
	for _, sel := range selectors {
		if !LabelValuesPushdown(sel) {
			return false
		}
	}
	return true
}

func (a *API) handleMetadata(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.requestContext(r)
	defer cancel()
	metric := r.FormValue("metric")
	metadata, err := a.Fetcher.Metadata(ctx, metric)
	if err != nil {
		a.writeEngineError(w, r, err)
		return
	}
	out := make(map[string][]map[string]string, len(metadata))
	for name, item := range metadata {
		out[name] = []map[string]string{{"type": item.Type, "help": item.Help, "unit": item.Unit}}
	}
	writeSuccess(w, out)
}

func (a *API) handleEmptyExemplars(w http.ResponseWriter, r *http.Request) {
	writeSuccess(w, []any{})
}

func (a *API) handleBuildInfo(w http.ResponseWriter, r *http.Request) {
	writeSuccess(w, map[string]string{
		"version":   a.Version,
		"revision":  "",
		"branch":    "",
		"buildUser": "",
		"buildDate": "",
		"goVersion": runtime.Version(),
	})
}

func (a *API) rangeFromRequest(r *http.Request) (time.Time, time.Time, error) {
	now := a.now()
	start, err := a.parseTime(r.FormValue("start"), now.Add(-time.Hour))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("start: %w", err)
	}
	end, err := a.parseTime(r.FormValue("end"), now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("end: %w", err)
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end is before start")
	}
	if a.MaxRange > 0 && end.Sub(start) > a.MaxRange {
		return time.Time{}, time.Time{}, fmt.Errorf("query range exceeds %s", a.MaxRange)
	}
	return start, end, nil
}

func (a *API) parseRequiredTime(value, name string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", name)
	}
	t, err := a.parseTime(value, time.Time{})
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", name, err)
	}
	return t, nil
}

func (a *API) parseTime(value string, fallback time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
	}
	whole, frac := math.Modf(seconds)
	return time.Unix(int64(whole), int64(frac*1e9)).UTC(), nil
}

func parseStep(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("step is required")
	}
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("step must be positive")
		}
		return d, nil
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("invalid step %q", value)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

const maxSelectors = 20

func parseSelectors(values []string) ([]Selector, error) {
	if len(values) > maxSelectors {
		return nil, fmt.Errorf("too many match[] selectors: maximum is %d", maxSelectors)
	}
	out := make([]Selector, 0, len(values))
	for _, value := range values {
		matchers, err := parser.NewParser(parser.Options{}).ParseMetricSelector(value)
		if err != nil {
			return nil, fmt.Errorf("invalid match[] %q: %w", value, err)
		}
		out = append(out, Selector{Matchers: matchers})
	}
	return out, nil
}

func vectorData(vector Vector) []any {
	out := make([]any, 0, len(vector))
	for _, sample := range vector {
		out = append(out, map[string]any{
			"metric": sample.Labels,
			"value":  []any{float64(sample.T) / 1000, formatFloat(sample.V)},
		})
	}
	return out
}

func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}

func writeSuccess(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
}

func writeError(w http.ResponseWriter, status int, errorType, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "error",
		"errorType": errorType,
		"error":     message,
	})
}

// writeEngineError maps a query or storage failure to the response shape the
// pinned Prometheus API uses: timeouts are 503 with errorType "timeout", and a
// client cancellation is the non-standard 499 with errorType "canceled".
func (a *API) writeEngineError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		message := "query timed out"
		if a.Timeout > 0 {
			message = "query timed out in " + a.Timeout.String()
		}
		writeError(w, http.StatusServiceUnavailable, "timeout", message)
	case errors.Is(err, context.Canceled):
		writeError(w, 499, "canceled", "query was canceled")
	case isExecutionTimeout(err):
		writeError(w, http.StatusServiceUnavailable, "timeout", "query timed out")
	case errors.Is(err, ErrBadQuery):
		if feature := unsupportedFeature(err); feature != "" {
			metrics.RecordUnsupported(r.Context(), feature)
		}
		writeError(w, http.StatusBadRequest, "bad_data", err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, "execution", "query execution failed")
	}
}

// isExecutionTimeout reports whether ClickHouse timed the query out. The
// context deadline is handled separately; this catches the server-side
// max_execution_time error.
func isExecutionTimeout(err error) bool {
	var clickhouseErr *chclient.Error
	if !errors.As(err, &clickhouseErr) {
		return false
	}
	return clickhouseErr.Code == "159" ||
		strings.Contains(clickhouseErr.Message, "TIMEOUT_EXCEEDED") ||
		strings.Contains(clickhouseErr.Message, "Timeout exceeded")
}

// unsupportedFeature is the closed set of unsupported-query labels.
func unsupportedFeature(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "subquer"):
		return "subquery"
	case strings.Contains(message, "unsupported function"):
		return "function"
	case strings.Contains(message, "unsupported aggregation"):
		return "aggregation"
	case strings.Contains(message, "unsupported result type"):
		return ""
	case strings.Contains(message, "unsupported"):
		return "unsupported"
	default:
		return ""
	}
}
