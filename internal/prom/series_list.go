package prom

import (
	"context"
	"fmt"
	"sort"
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

// ListSeries returns the distinct label sets for a selector without reading
// samples. The /series handler only needs labels; selecting every sample was
// the dominant cost in the 10k-profile run.
func (f *ClickHouseFetcher) ListSeries(ctx context.Context, sel Selector, start, end time.Time) ([]map[string]string, error) {
	gaugeFilter, sumFilter, histFilter, summaryFilter := nameFilters(sel)
	service := serviceFilter(sel)
	tables, err := f.candidateTables(ctx, sel)
	if err != nil {
		return nil, err
	}
	found := make(map[string]map[string]string)
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
		if err := f.listSeriesTable(ctx, name, kind, filter, start, end, sel.Matchers, found); err != nil {
			return nil, err
		}
	}
	out := make([]map[string]string, 0, len(found))
	for _, labels := range found {
		out = append(out, labels)
	}
	sort.Slice(out, func(i, j int) bool { return labelsKey(out[i]) < labelsKey(out[j]) })
	if len(out) == 0 && hasMetricNameMatcher(sel) {
		if err := f.probeExponentialHistogram(ctx, sel, start, end); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (f *ClickHouseFetcher) listSeriesTable(ctx context.Context, table string, kind tableKind, filter plan.Expr, start, end time.Time, matchers []*promlabels.Matcher, out map[string]map[string]string) error {
	selects := []plan.SelectItem{
		{Expr: plan.Col("MetricName")},
		{Expr: plan.Col("ServiceName")},
		{Expr: plan.Col("Attributes")},
	}
	groupBy := []string{"MetricName", "ServiceName", "Attributes"}
	switch kind {
	case tableHistogram:
		selects = append(selects, plan.SelectItem{Expr: plan.Col("ExplicitBounds")})
		groupBy = append(groupBy, "ExplicitBounds")
	case tableSummary:
		selects = append(selects, plan.SelectItem{Expr: plan.Col("ValueAtQuantiles.Quantile")})
		groupBy = append(groupBy, "ValueAtQuantiles.Quantile")
	}
	query := plan.Query{
		Signal:     plan.SignalMetrics,
		Table:      table,
		Select:     selects,
		Where:      filter,
		GroupBy:    groupBy,
		SkipFinal:  true,
		TimeColumn: "TimeUnix",
		TimeRange: &plan.TimeRange{
			Start:        start,
			End:          end,
			EndInclusive: true,
		},
		Limit: f.MaxRows,
	}
	emitted, err := f.Emitter.Emit(query)
	if err != nil {
		return fmt.Errorf("prom: build %s series query: %w", table, err)
	}
	result, err := f.Client.Query(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
	if err != nil {
		return fmt.Errorf("prom: query %s series: %w", table, err)
	}
	if result.Len() >= f.MaxRows {
		return fmt.Errorf("%w: series query reached the %d-row limit; narrow the time range or selector", ErrBadQuery, f.MaxRows)
	}
	for i := 0; i < result.Len(); i++ {
		metric, _ := result.String(i, "MetricName")
		service, _ := result.String(i, "ServiceName")
		base := baseLabels(metric, service, attributes(result, i))
		switch kind {
		case tableGauge, tableSum:
			addLabelSet(out, matchers, base)
		case tableHistogram:
			bounds, err := floatSlice(result.Rows[i]["ExplicitBounds"])
			if err != nil {
				return fmt.Errorf("prom: decode ExplicitBounds: %w", err)
			}
			for _, bound := range bounds {
				labels := cloneLabels(base)
				labels["__name__"] = base["__name__"] + "_bucket"
				labels["le"] = formatBound(bound)
				addLabelSet(out, matchers, labels)
			}
			inf := cloneLabels(base)
			inf["__name__"] = base["__name__"] + "_bucket"
			inf["le"] = "+Inf"
			addLabelSet(out, matchers, inf)
			addCountSumLabels(out, matchers, base)
		case tableSummary:
			quantiles, err := floatSlice(result.Rows[i]["ValueAtQuantiles.Quantile"])
			if err != nil {
				return fmt.Errorf("prom: decode ValueAtQuantiles.Quantile: %w", err)
			}
			for _, quantile := range quantiles {
				labels := cloneLabels(base)
				labels["quantile"] = formatBound(quantile)
				addLabelSet(out, matchers, labels)
			}
			addCountSumLabels(out, matchers, base)
		}
	}
	return nil
}

func addLabelSet(out map[string]map[string]string, matchers []*promlabels.Matcher, labels map[string]string) {
	if !matchesAll(matchers, labels) {
		return
	}
	out[labelsKey(labels)] = labels
}

func addCountSumLabels(out map[string]map[string]string, matchers []*promlabels.Matcher, base map[string]string) {
	for _, suffix := range []string{"_count", "_sum"} {
		labels := cloneLabels(base)
		labels["__name__"] = base["__name__"] + suffix
		addLabelSet(out, matchers, labels)
	}
}
