package prom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
	"github.com/mrofisr/skopos/internal/sqlgen"
)

// groupedSampleWindow bounds the sample grouping used for instant queries.
// Longer windows keep the per-sample query so the result-row limit still
// bounds memory.
const groupedSampleWindow = 15 * time.Minute

// ClickHouseFetcher loads gauge, sum, and histogram samples from ClickHouse.
//
// Metric and label names are normalized to Prometheus names by replacing
// characters outside [A-Za-z0-9_] with '_'. The metric unit is not appended
// yet; this is documented in docs/promql-support.md.
type ClickHouseFetcher struct {
	Client  *chclient.Client
	Schema  *plan.Schema
	Emitter *sqlgen.Emitter
	Names   plan.TableNames
	MaxRows int

	// MetadataTTL caches metric discovery, non-empty flags, and metadata.
	// Zero means the 30s default.
	MetadataTTL time.Duration
	// Now defaults to time.Now; tests inject a fake clock.
	Now func() time.Time

	cacheMu sync.Mutex
	cache   *metricCache
}

// NewClickHouseFetcher builds a fetcher and validates its table names.
func NewClickHouseFetcher(client *chclient.Client, schema *plan.Schema, names plan.TableNames, maxRows int) (*ClickHouseFetcher, error) {
	if client == nil {
		return nil, fmt.Errorf("prom: clickhouse client is required")
	}
	if schema == nil {
		return nil, fmt.Errorf("prom: schema is required")
	}
	if maxRows <= 0 {
		return nil, fmt.Errorf("prom: max rows must be positive")
	}
	for _, name := range []string{names.MetricsGauge, names.MetricsSum, names.MetricsHistogram, names.MetricsSummary, names.MetricsExponentialHistogram} {
		if _, ok := schema.Table(name); !ok {
			return nil, fmt.Errorf("prom: schema has no table %q", name)
		}
	}
	emitter, err := sqlgen.New(schema, sqlgen.Limits{MaxLimit: maxRows, MaxRange: 7 * 24 * time.Hour})
	if err != nil {
		return nil, err
	}
	return &ClickHouseFetcher{Client: client, Schema: schema, Emitter: emitter, Names: names, MaxRows: maxRows, MetadataTTL: 30 * time.Second, Now: time.Now}, nil
}

// Fetch implements Fetcher.
func (f *ClickHouseFetcher) Fetch(ctx context.Context, sel Selector, start, end time.Time) ([]Series, error) {
	gaugeFilter, sumFilter, histFilter, summaryFilter := nameFilters(sel)
	service := serviceFilter(sel)
	tables, err := f.candidateTables(ctx, sel)
	if err != nil {
		return nil, err
	}
	series := make(map[string]*Series)
	for _, table := range tables {
		var (
			name   string
			kind   tableKind
			filter plan.Expr
		)
		switch table {
		case tableGauge:
			name, kind, filter = f.Names.MetricsGauge, tableGauge, gaugeFilter
		case tableSum:
			name, kind, filter = f.Names.MetricsSum, tableSum, sumFilter
		case tableHistogram:
			name, kind, filter = f.Names.MetricsHistogram, tableHistogram, histFilter
		case tableSummary:
			name, kind, filter = f.Names.MetricsSummary, tableSummary, summaryFilter
		}
		filter = andFilters(filter, service)
		nonEmpty, err := f.cacheFor().nonEmpty.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		if !nonEmpty {
			continue
		}
		if err := f.fetchTable(ctx, name, kind, filter, start, end, series); err != nil {
			return nil, err
		}
	}
	out := make([]Series, 0, len(series))
	for key, s := range series {
		if !matchesAll(sel.Matchers, s.Labels) {
			delete(series, key)
			continue
		}
		sort.Slice(s.Samples, func(i, j int) bool { return s.Samples[i].T < s.Samples[j].T })
		out = append(out, *s)
	}
	sortSeries(out)
	if len(out) == 0 && hasMetricNameMatcher(sel) {
		if err := f.probeExponentialHistogram(ctx, sel, start, end); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// candidateTables narrows an exact metric name to the tables that can hold it.
// A regex, an absent name, or a negative name matcher falls back to all tables.
func (f *ClickHouseFetcher) candidateTables(ctx context.Context, sel Selector) ([]tableKind, error) {
	all := []tableKind{tableGauge, tableSum, tableHistogram, tableSummary}
	exact, regex := metricNameMatchers(sel)
	if regex != "" || exact == "" {
		return all, nil
	}
	name := NormalizeLabelName(exact)
	switch {
	case strings.HasSuffix(name, "_bucket"):
		return []tableKind{tableHistogram}, nil
	case strings.HasSuffix(name, "_sum"), strings.HasSuffix(name, "_count"):
		return []tableKind{tableHistogram, tableSummary}, nil
	}
	if hasNegativeMetricMatcher(sel) {
		return all, nil
	}
	return f.cacheFor().discovery.Get(ctx, name)
}

func hasNegativeMetricMatcher(sel Selector) bool {
	for _, matcher := range sel.Matchers {
		if matcher.Name != promlabels.MetricName {
			continue
		}
		if matcher.Type == promlabels.MatchNotEqual || matcher.Type == promlabels.MatchNotRegexp {
			return true
		}
	}
	return false
}

// discoverTables asks each table whether it holds the exact normalized name.
// The queries select only MetricName and LIMIT 1, so they are metadata probes,
// not data scans. AX-32 caches this.
func (f *ClickHouseFetcher) probeTables(ctx context.Context, name string) ([]tableKind, error) {
	tables := []struct {
		kind tableKind
		name string
	}{
		{tableGauge, f.Names.MetricsGauge},
		{tableSum, f.Names.MetricsSum},
		{tableHistogram, f.Names.MetricsHistogram},
		{tableSummary, f.Names.MetricsSummary},
	}
	found := make([]tableKind, 0, len(tables))
	for _, table := range tables {
		query := plan.Query{
			Signal:    plan.SignalMetrics,
			Table:     table.name,
			Select:    []plan.SelectItem{{Expr: plan.Col("MetricName")}},
			Where:     plan.Bin("=", normalizedMetricName(), plan.String("metric_filter", name)),
			Limit:     1,
			SkipFinal: true,
		}
		emitted, err := f.Emitter.Emit(query)
		if err != nil {
			return nil, fmt.Errorf("prom: build metric discovery query: %w", err)
		}
		result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
		if err != nil {
			return nil, fmt.Errorf("prom: discover metric in %s: %w", table.name, err)
		}
		if result.Len() > 0 {
			found = append(found, table.kind)
		}
	}
	return found, nil
}

func hasMetricNameMatcher(sel Selector) bool {
	exact, regex := metricNameMatchers(sel)
	return exact != "" || regex != ""
}

type tableKind int

const (
	tableGauge tableKind = iota
	tableSum
	tableHistogram
	tableSummary
)

func (f *ClickHouseFetcher) fetchTable(ctx context.Context, table string, kind tableKind, filter plan.Expr, start, end time.Time, out map[string]*Series) error {
	grouped := (kind == tableGauge || kind == tableSum) && end.Sub(start) <= groupedSampleWindow
	var selects []plan.SelectItem
	if grouped {
		selects = []plan.SelectItem{
			{Expr: plan.Col("MetricName")},
			{Expr: plan.Col("ServiceName")},
			{Expr: plan.Col("Attributes")},
			{Expr: plan.Agg("groupArray", plan.Col("TimeUnix")), Alias: "Times"},
			{Expr: plan.Agg("groupArray", plan.Col("Value")), Alias: "Values"},
		}
	} else {
		selects = []plan.SelectItem{
			{Expr: plan.Col("MetricName")},
			{Expr: plan.Col("TimeUnix")},
			{Expr: plan.Col("ServiceName")},
			{Expr: plan.Col("Attributes")},
		}
		switch kind {
		case tableGauge, tableSum:
			selects = append(selects, plan.SelectItem{Expr: plan.Col("Value")})
		case tableHistogram:
			selects = append(selects,
				plan.SelectItem{Expr: plan.Col("Count")},
				plan.SelectItem{Expr: plan.Col("Sum")},
				plan.SelectItem{Expr: plan.Col("BucketCounts")},
				plan.SelectItem{Expr: plan.Col("ExplicitBounds")},
			)
		case tableSummary:
			selects = append(selects,
				plan.SelectItem{Expr: plan.Col("Count")},
				plan.SelectItem{Expr: plan.Col("Sum")},
				plan.SelectItem{Expr: plan.Col("ValueAtQuantiles.Quantile")},
				plan.SelectItem{Expr: plan.Col("ValueAtQuantiles.Value")},
			)
		}
	}
	query := plan.Query{
		Signal:     plan.SignalMetrics,
		Table:      table,
		Select:     selects,
		Where:      filter,
		TimeColumn: "TimeUnix",
		TimeRange: &plan.TimeRange{
			Start:        start,
			End:          end,
			EndInclusive: true,
		},
		Limit: f.MaxRows,
	}
	if grouped {
		query.GroupBy = []string{"MetricName", "ServiceName", "Attributes"}
	}
	emitted, err := f.Emitter.Emit(query)
	if err != nil {
		return fmt.Errorf("prom: build %s query: %w", table, err)
	}
	result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
	if err != nil {
		if isResultLimit(err) {
			return fmt.Errorf("%w: query result exceeds the ClickHouse result-row limit; narrow the time range or metric selector", ErrBadQuery)
		}
		return fmt.Errorf("prom: query %s: %w", table, err)
	}
	if result.Len() >= f.MaxRows {
		return fmt.Errorf("%w: query reached the %d-row scan limit; narrow the time range or metric selector", ErrBadQuery, f.MaxRows)
	}
	if grouped {
		return f.addGroupedRows(result, out)
	}
	for i := 0; i < result.Len(); i++ {
		switch kind {
		case tableGauge, tableSum:
			f.addGaugeRow(result, i, out)
		case tableHistogram:
			if err := f.addHistogramRow(result, i, out); err != nil {
				return err
			}
		case tableSummary:
			if err := f.addSummaryRow(result, i, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// addGroupedRows expands short-window series rows. ClickHouse returns one row
// per series with parallel time and value arrays, which avoids repeating the
// labels for every sample.
func (f *ClickHouseFetcher) addGroupedRows(result *chclient.Result, out map[string]*Series) error {
	total := 0
	for i := 0; i < result.Len(); i++ {
		times, err := timeSlice(cellValue(result, i, "Times"))
		if err != nil {
			return fmt.Errorf("prom: decode Times: %w", err)
		}
		values, err := floatSlice(cellValue(result, i, "Values"))
		if err != nil {
			return fmt.Errorf("prom: decode Values: %w", err)
		}
		if len(times) != len(values) {
			return fmt.Errorf("prom: grouped series has %d times and %d values", len(times), len(values))
		}
		total += len(values)
		if total >= f.MaxRows {
			return fmt.Errorf("%w: query reached the %d-row scan limit; narrow the time range or metric selector", ErrBadQuery, f.MaxRows)
		}
		metric, _ := result.String(i, "MetricName")
		service, _ := result.String(i, "ServiceName")
		labels := baseLabels(metric, service, attributes(result, i))
		points := make([]Sample, len(times))
		for j := range times {
			points[j] = Sample{T: times[j], V: values[j]}
		}
		sort.Slice(points, func(a, b int) bool { return points[a].T < points[b].T })
		key := labelsKey(labels)
		series := out[key]
		if series == nil {
			series = &Series{Labels: labels}
			out[key] = series
		}
		series.Samples = append(series.Samples, points...)
	}
	return nil
}

func timeSlice(v any) ([]int64, error) {
	switch values := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]int64, 0, len(values))
		for _, value := range values {
			s, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected time type %T", value)
			}
			ms, err := parseTimeString(s)
			if err != nil {
				return nil, err
			}
			out = append(out, ms)
		}
		return out, nil
	case []string:
		out := make([]int64, 0, len(values))
		for _, value := range values {
			ms, err := parseTimeString(value)
			if err != nil {
				return nil, err
			}
			out = append(out, ms)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected array type %T", v)
	}
}

func parseTimeString(s string) (int64, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", time.RFC3339Nano} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("invalid TimeUnix %q", s)
}

func (f *ClickHouseFetcher) addGaugeRow(result *chclient.Result, row int, out map[string]*Series) {
	metric, _ := result.String(row, "MetricName")
	service, _ := result.String(row, "ServiceName")
	value, ok := result.Float64(row, "Value")
	if !ok {
		return
	}
	ts, err := resultTime(result, row)
	if err != nil {
		return
	}
	labels := baseLabels(metric, service, attributes(result, row))
	addSample(out, labels, Sample{T: ts, V: value})
}

func (f *ClickHouseFetcher) addHistogramRow(result *chclient.Result, row int, out map[string]*Series) error {
	metric, _ := result.String(row, "MetricName")
	service, _ := result.String(row, "ServiceName")
	ts, err := resultTime(result, row)
	if err != nil {
		return nil
	}
	base := baseLabels(metric, service, attributes(result, row))
	name := base["__name__"]
	counts, err := floatSlice(cellValue(result, row, "BucketCounts"))
	if err != nil {
		return fmt.Errorf("prom: decode BucketCounts: %w", err)
	}
	bounds, err := floatSlice(cellValue(result, row, "ExplicitBounds"))
	if err != nil {
		return fmt.Errorf("prom: decode ExplicitBounds: %w", err)
	}
	cumulative := 0.0
	for i, bound := range bounds {
		if i < len(counts) {
			cumulative += counts[i]
		}
		labels := cloneLabels(base)
		labels["__name__"] = name + "_bucket"
		labels["le"] = formatBound(bound)
		addSample(out, labels, Sample{T: ts, V: cumulative})
	}
	if len(counts) > len(bounds) {
		cumulative += counts[len(bounds)]
	}
	labels := cloneLabels(base)
	labels["__name__"] = name + "_bucket"
	labels["le"] = "+Inf"
	addSample(out, labels, Sample{T: ts, V: cumulative})

	if count, ok := result.Float64(row, "Count"); ok {
		labels := cloneLabels(base)
		labels["__name__"] = name + "_count"
		addSample(out, labels, Sample{T: ts, V: count})
	}
	if sum, ok := result.Float64(row, "Sum"); ok {
		labels := cloneLabels(base)
		labels["__name__"] = name + "_sum"
		addSample(out, labels, Sample{T: ts, V: sum})
	}
	return nil
}

func (f *ClickHouseFetcher) addSummaryRow(result *chclient.Result, row int, out map[string]*Series) error {
	metric, _ := result.String(row, "MetricName")
	service, _ := result.String(row, "ServiceName")
	ts, err := resultTime(result, row)
	if err != nil {
		return nil
	}
	base := baseLabels(metric, service, attributes(result, row))
	name := base["__name__"]

	if count, ok := result.Float64(row, "Count"); ok {
		labels := cloneLabels(base)
		labels["__name__"] = name + "_count"
		addSample(out, labels, Sample{T: ts, V: count})
	}
	if sum, ok := result.Float64(row, "Sum"); ok {
		labels := cloneLabels(base)
		labels["__name__"] = name + "_sum"
		addSample(out, labels, Sample{T: ts, V: sum})
	}
	quantiles, err := floatSlice(cellValue(result, row, "ValueAtQuantiles.Quantile"))
	if err != nil {
		return fmt.Errorf("prom: decode ValueAtQuantiles.Quantile: %w", err)
	}
	values, err := floatSlice(cellValue(result, row, "ValueAtQuantiles.Value"))
	if err != nil {
		return fmt.Errorf("prom: decode ValueAtQuantiles.Value: %w", err)
	}
	for i, quantile := range quantiles {
		if i >= len(values) {
			break
		}
		labels := cloneLabels(base)
		labels["quantile"] = formatBound(quantile)
		addSample(out, labels, Sample{T: ts, V: values[i]})
	}
	return nil
}

// probeExponentialHistogram returns an explicit error if a selector that
// produced no supported samples names an OTel exponential histogram. The probe
// is bounded and only runs for selectors with a metric-name matcher.
func (f *ClickHouseFetcher) probeExponentialHistogram(ctx context.Context, sel Selector, start, end time.Time) error {
	filter := metricNameFilter(sel)
	limit := 1000
	if f.MaxRows > 0 && f.MaxRows < limit {
		limit = f.MaxRows
	}
	query := plan.Query{
		Signal: plan.SignalMetrics,
		Table:  f.Names.MetricsExponentialHistogram,
		Select: []plan.SelectItem{
			{Expr: plan.Col("MetricName")},
			{Expr: plan.Col("ServiceName")},
			{Expr: plan.Col("Attributes")},
		},
		Where:      filter,
		TimeColumn: "TimeUnix",
		TimeRange: &plan.TimeRange{
			Start:        start,
			End:          end,
			EndInclusive: true,
		},
		Limit:     limit,
		SkipFinal: true,
	}
	emitted, err := f.Emitter.Emit(query)
	if err != nil {
		return fmt.Errorf("prom: build exponential histogram probe: %w", err)
	}
	result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
	if err != nil {
		return fmt.Errorf("prom: exponential histogram probe: %w", err)
	}
	for i := 0; i < result.Len(); i++ {
		name, _ := result.String(i, "MetricName")
		service, _ := result.String(i, "ServiceName")
		labels := baseLabels(name, service, attributes(result, i))
		family := labels["__name__"]
		candidates := []string{family}
		if !strings.HasSuffix(family, "_count") && !strings.HasSuffix(family, "_sum") {
			candidates = append(candidates, family+"_count", family+"_sum")
		}
		for _, candidate := range candidates {
			labels["__name__"] = candidate
			if matchesAll(sel.Matchers, labels) {
				return fmt.Errorf("%w: metric %q is an OpenTelemetry exponential histogram; exponential histograms and native histogram functions are not supported", ErrBadQuery, candidate)
			}
		}
	}
	return nil
}
func nameFilters(sel Selector) (gauge, sum, histogram, summary plan.Expr) {
	exact, regex := metricNameMatchers(sel)
	normalized := normalizedMetricName()
	if exact != "" {
		name := NormalizeLabelName(exact)
		base := stripHistogramSuffix(name)
		gauge = plan.Bin("=", normalized, plan.String("metric_filter", name))
		sum = plan.Bin("=", normalized, plan.String("metric_filter", name))
		histogram = plan.Bin("=", normalized, plan.String("metric_filter", base))
		summary = plan.Bin("=", normalized, plan.String("metric_filter", base))
		return gauge, sum, histogram, summary
	}
	if regex != "" {
		gauge = plan.F("match", normalized, plan.String("metric_regex_filter", regex))
		sum = plan.F("match", normalized, plan.String("metric_regex_filter", regex))
		// Histogram bucket and summary quantile selectors usually include a
		// suffix while the tables store the base name. Do not apply a name
		// filter here; the Go matcher filters the expanded series.
		return gauge, sum, nil, nil
	}
	return nil, nil, nil, nil
}

// serviceFilter pushes exact and regex service_name matchers into SQL. The
// ServiceName column leads the metric table primary key, so the exact branch is
// a narrow range scan. Each branch also matches an empty ServiceName because
// baseLabels falls back to a service.name attribute when the column is empty;
// matchesAll still verifies the exact Prometheus semantics. Regex is an
// over-approximation because ClickHouse match() is not anchored. Negative
// matchers stay in Go because they also select series where the label is
// absent.
func serviceFilter(sel Selector) plan.Expr {
	var filter plan.Expr
	exact, regex, empty := 0, 0, 0
	for _, matcher := range sel.Matchers {
		if matcher.Name != "service_name" {
			continue
		}
		var term plan.Expr
		switch matcher.Type {
		case promlabels.MatchEqual:
			term = plan.Bin("OR",
				plan.Bin("=", plan.Col("ServiceName"),
					plan.String(fmt.Sprintf("service_filter_%d", exact), matcher.Value)),
				plan.Bin("=", plan.Col("ServiceName"),
					plan.String(fmt.Sprintf("service_empty_%d", empty), "")),
			)
			exact++
			empty++
		case promlabels.MatchRegexp:
			term = plan.Bin("OR",
				plan.F("match", plan.Col("ServiceName"),
					plan.String(fmt.Sprintf("service_regex_filter_%d", regex), matcher.Value)),
				plan.Bin("=", plan.Col("ServiceName"),
					plan.String(fmt.Sprintf("service_empty_%d", empty), "")),
			)
			regex++
			empty++
		default:
			continue
		}
		filter = andFilters(filter, term)
	}
	return filter
}

func andFilters(left, right plan.Expr) plan.Expr {
	switch {
	case left == nil:
		return right
	case right == nil:
		return left
	default:
		return plan.Bin("AND", left, right)
	}
}

func metricNameFilter(sel Selector) plan.Expr {
	exact, regex := metricNameMatchers(sel)
	normalized := normalizedMetricName()
	if exact != "" {
		return plan.Bin("=", normalized, plan.String("metric_filter", stripHistogramSuffix(NormalizeLabelName(exact))))
	}
	if regex != "" {
		return plan.F("match", normalized, plan.String("metric_regex_filter", regex))
	}
	return nil
}

func metricNameMatchers(sel Selector) (exact, regex string) {
	for _, m := range sel.Matchers {
		if m.Name != promlabels.MetricName {
			continue
		}
		switch m.Type {
		case promlabels.MatchEqual:
			if exact == "" {
				exact = m.Value
			}
		case promlabels.MatchRegexp:
			if regex == "" {
				regex = m.Value
			}
		}
	}
	return exact, regex
}

func normalizedMetricName() plan.Expr {
	return plan.F("replaceRegexpAll",
		plan.Col("MetricName"),
		plan.String("norm_pattern", `[^A-Za-z0-9_]`),
		plan.String("norm_replacement", "_"),
	)
}

func stripHistogramSuffix(name string) string {
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func baseLabels(metric, service string, attrs map[string]string) map[string]string {
	labels := map[string]string{"__name__": NormalizeLabelName(metric)}
	if service != "" {
		labels["service_name"] = service
	}
	for k, v := range attrs {
		name := NormalizeLabelName(k)
		if name == "__name__" || name == "" {
			continue
		}
		if _, exists := labels[name]; exists {
			continue
		}
		labels[name] = v
	}
	return labels
}

func addSample(out map[string]*Series, labels map[string]string, sample Sample) {
	key := labelsKey(labels)
	s := out[key]
	if s == nil {
		s = &Series{Labels: cloneLabels(labels)}
		out[key] = s
	}
	s.Samples = append(s.Samples, sample)
}

func attributes(result *chclient.Result, row int) map[string]string {
	v, ok := result.Value(row, "Attributes")
	if !ok {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}

func cellValue(result *chclient.Result, row int, column string) any {
	v, _ := result.Value(row, column)
	return v
}

func resultTime(result *chclient.Result, row int) (int64, error) {
	s, ok := result.String(row, "TimeUnix")
	if !ok {
		return 0, fmt.Errorf("missing TimeUnix")
	}
	return parseTimeString(s)
}

func floatSlice(v any) ([]float64, error) {
	switch values := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]float64, 0, len(values))
		for _, value := range values {
			f, err := toFloat(value)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
		return out, nil
	case []float64:
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected array type %T", v)
	}
}

func toFloat(v any) (float64, error) {
	switch value := v.(type) {
	case json.Number:
		return value.Float64()
	case float64:
		return value, nil
	case float32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case int:
		return float64(value), nil
	case string:
		return strconv.ParseFloat(value, 64)
	default:
		return 0, fmt.Errorf("unexpected number type %T", v)
	}
}

func formatBound(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// isResultLimit reports whether ClickHouse rejected a result because it
// exceeded max_result_rows; the API turns this into a bad query, not a 500.
func isResultLimit(err error) bool {
	var clickhouseErr *chclient.Error
	if !errors.As(err, &clickhouseErr) {
		return false
	}
	return clickhouseErr.Code == "396" || strings.Contains(clickhouseErr.Message, "TOO_MANY_ROWS_OR_BYTES")
}

func matchesAll(matchers []*promlabels.Matcher, labels map[string]string) bool {
	for _, m := range matchers {
		if !m.Matches(labels[m.Name]) {
			return false
		}
	}
	return true
}

// Metadata is the Prometheus metadata record for one metric name.
type Metadata struct {
	Type string
	Help string
	Unit string
}

// Metadata returns metric metadata from the gauge, sum, and histogram tables.
// Metadata returns metric metadata, cached when metric is non-empty.
func (f *ClickHouseFetcher) Metadata(ctx context.Context, metric string) (map[string]Metadata, error) {
	if metric == "" {
		return f.loadMetadata(ctx, "")
	}
	metadata, err := f.cacheFor().metadata.Get(ctx, NormalizeLabelName(metric))
	if err != nil {
		return nil, err
	}
	return cloneMetadata(metadata), nil
}

func (f *ClickHouseFetcher) loadMetadata(ctx context.Context, metric string) (map[string]Metadata, error) {
	out := make(map[string]Metadata)
	tables := []struct {
		name     string
		kind     tableKind
		typeName string
	}{
		{f.Names.MetricsGauge, tableGauge, "gauge"},
		{f.Names.MetricsSum, tableSum, "counter"},
		{f.Names.MetricsHistogram, tableHistogram, "histogram"},
		{f.Names.MetricsSummary, tableSummary, "summary"},
	}
	for _, table := range tables {
		var filter plan.Expr
		if metric != "" {
			filter = plan.Bin("=",
				plan.F("replaceRegexpAll",
					plan.Col("MetricName"),
					plan.String("norm_pattern", `[^A-Za-z0-9_]`),
					plan.String("norm_replacement", "_"),
				),
				plan.String("metric_filter", NormalizeLabelName(metric)),
			)
		}
		query := plan.Query{
			Signal: plan.SignalMetrics,
			Table:  table.name,
			Select: []plan.SelectItem{
				{Expr: plan.Col("MetricName")},
				{Expr: plan.Agg("any", plan.Col("MetricDescription")), Alias: "help"},
				{Expr: plan.Agg("any", plan.Col("MetricUnit")), Alias: "unit"},
			},
			Where:     filter,
			GroupBy:   []string{"MetricName"},
			Limit:     f.MaxRows,
			SkipFinal: true,
		}
		emitted, err := f.Emitter.Emit(query)
		if err != nil {
			return nil, fmt.Errorf("prom: build metadata query: %w", err)
		}
		result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
		if err != nil {
			return nil, fmt.Errorf("prom: metadata query %s: %w", table.name, err)
		}
		if result.Len() >= f.MaxRows {
			return nil, fmt.Errorf("prom: metadata query reached the %d-row limit", f.MaxRows)
		}
		for i := 0; i < result.Len(); i++ {
			name, _ := result.String(i, "MetricName")
			help, _ := result.String(i, "help")
			unit, _ := result.String(i, "unit")
			out[NormalizeLabelName(name)] = Metadata{Type: table.typeName, Help: help, Unit: unit}
		}
	}
	return out, nil
}

// LabelValues returns cached distinct label values for selectors that only
// constrain __name__. The cache TTL is 15 seconds; callers still fall back to
// Fetch for any other matcher.
func (f *ClickHouseFetcher) LabelValues(ctx context.Context, name string, sel Selector, start, end time.Time, limit int) ([]string, error) {
	bucket := int64(labelValuesTTL / time.Second)
	if bucket < 1 {
		bucket = 1
	}
	key := labelValuesKey{
		Name:     name,
		Selector: sel.String(),
		Start:    start.Unix() / bucket,
		End:      end.Unix() / bucket,
		Limit:    limit,
	}
	cache := f.cacheFor()
	cache.labelValuesMu.Lock()
	cache.labelValuesRequests[key] = labelValuesRequest{name: name, selector: sel, start: start, end: end, limit: limit}
	cache.labelValuesMu.Unlock()
	values, err := cache.labelValues.Get(ctx, key)
	cache.labelValuesMu.Lock()
	delete(cache.labelValuesRequests, key)
	cache.labelValuesMu.Unlock()
	return values, err
}

// loadLabelValues computes the distinct values. Callers reach it through the
// cache.
func (f *ClickHouseFetcher) loadLabelValues(ctx context.Context, name string, sel Selector, start, end time.Time, limit int) ([]string, error) {
	tables, err := f.candidateTables(ctx, sel)
	if err != nil {
		return nil, err
	}
	valueExpr, err := labelValueExpr(name)
	if err != nil {
		return nil, err
	}
	values := make(map[string]struct{})
	for _, table := range tables {
		tableName := ""
		switch table {
		case tableGauge:
			tableName = f.Names.MetricsGauge
		case tableSum:
			tableName = f.Names.MetricsSum
		case tableHistogram:
			tableName = f.Names.MetricsHistogram
		case tableSummary:
			tableName = f.Names.MetricsSummary
		}
		nonEmpty, err := f.cacheFor().nonEmpty.Get(ctx, tableName)
		if err != nil {
			return nil, err
		}
		if !nonEmpty {
			continue
		}
		query := plan.Query{
			Signal: plan.SignalMetrics,
			Table:  tableName,
			Select: []plan.SelectItem{{
				Expr:  valueExpr,
				Alias: "value",
			}},
			GroupBy:    []string{"value"},
			Where:      metricNameFilter(sel),
			SkipFinal:  true,
			TimeColumn: "TimeUnix",
			TimeRange: &plan.TimeRange{
				Start:        start,
				End:          end,
				EndInclusive: true,
			},
		}
		if limit > 0 {
			query.Limit = limit
		}
		emitted, err := f.Emitter.Emit(query)
		if err != nil {
			return nil, fmt.Errorf("prom: build label values query: %w", err)
		}
		result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
		if err != nil {
			return nil, fmt.Errorf("prom: label values query %s: %w", tableName, err)
		}
		for i := 0; i < result.Len(); i++ {
			value, ok := result.String(i, "value")
			if !ok || value == "" {
				continue
			}
			if name == promlabels.MetricName {
				value = NormalizeLabelName(value)
			}
			values[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func labelValueExpr(name string) (plan.Expr, error) {
	switch name {
	case promlabels.MetricName:
		return plan.Col("MetricName"), nil
	case "service_name":
		return plan.Col("ServiceName"), nil
	default:
		if strings.ContainsAny(name, "`{}[]") || name == "" {
			return nil, fmt.Errorf("prom: invalid label name %q", name)
		}
		return plan.Map(plan.Col("Attributes"), plan.String("label_key", name)), nil
	}
}

// LabelValuesPushdown reports whether a selector is safe for the SQL path.
func LabelValuesPushdown(sel Selector) bool {
	for _, matcher := range sel.Matchers {
		if matcher.Name != promlabels.MetricName {
			return false
		}
		if matcher.Type != promlabels.MatchEqual && matcher.Type != promlabels.MatchRegexp {
			return false
		}
	}
	return true
}

// LabelNames returns label names for name-only selectors. Other selectors are
// handled by the API fallback.
func (f *ClickHouseFetcher) LabelNames(ctx context.Context, sel Selector, start, end time.Time, limit int) ([]string, error) {
	ttl := f.MetadataTTL
	if ttl <= 0 {
		ttl = defaultMetadataTTL
	}
	bucket := int64(ttl / time.Second)
	if bucket < 1 {
		bucket = 1
	}
	key := labelNamesKey{
		Selector: sel.String(),
		Start:    start.Unix() / bucket,
		End:      end.Unix() / bucket,
		Limit:    limit,
	}
	cache := f.cacheFor()
	cache.labelMu.Lock()
	cache.labelRequests[key] = labelNamesRequest{selector: sel, start: start, end: end, limit: limit}
	cache.labelMu.Unlock()
	values, err := cache.labelNames.Get(ctx, key)
	cache.labelMu.Lock()
	delete(cache.labelRequests, key)
	cache.labelMu.Unlock()
	return values, err
}

func (f *ClickHouseFetcher) loadLabelNames(ctx context.Context, sel Selector, start, end time.Time, limit int) ([]string, error) {
	tables, err := f.candidateTables(ctx, sel)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{})
	add := func(name string) {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	for _, table := range tables {
		tableName := ""
		known := []string{}
		switch table {
		case tableGauge:
			tableName = f.Names.MetricsGauge
			known = []string{"__name__", "service_name"}
		case tableSum:
			tableName = f.Names.MetricsSum
			known = []string{"__name__", "service_name"}
		case tableHistogram:
			tableName = f.Names.MetricsHistogram
			known = []string{"__name__", "service_name", "le"}
		case tableSummary:
			tableName = f.Names.MetricsSummary
			known = []string{"__name__", "service_name", "quantile"}
		}
		nonEmpty, err := f.cacheFor().nonEmpty.Get(ctx, tableName)
		if err != nil {
			return nil, err
		}
		if !nonEmpty {
			continue
		}
		for _, name := range known {
			add(name)
		}
		query := plan.Query{
			Signal: plan.SignalMetrics,
			Table:  tableName,
			Select: []plan.SelectItem{{
				Expr:  plan.F("arrayJoin", plan.F("mapKeys", plan.Col("Attributes"))),
				Alias: "name",
			}},
			Where:      metricNameFilter(sel),
			GroupBy:    []string{"name"},
			SkipFinal:  true,
			TimeColumn: "TimeUnix",
			TimeRange: &plan.TimeRange{
				Start:        start,
				End:          end,
				EndInclusive: true,
			},
		}
		if limit > 0 {
			query.Limit = limit
		}
		emitted, err := f.Emitter.Emit(query)
		if err != nil {
			return nil, fmt.Errorf("prom: build label names query: %w", err)
		}
		result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
		if err != nil {
			return nil, fmt.Errorf("prom: label names query %s: %w", tableName, err)
		}
		for i := 0; i < result.Len(); i++ {
			name, ok := result.String(i, "name")
			if ok {
				add(name)
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
	return out, nil
}
