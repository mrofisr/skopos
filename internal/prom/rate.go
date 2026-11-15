package prom

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
	"github.com/mrofisr/skopos/internal/sqlgen"
)

// maxRatePushdownRange bounds the instant rate pushdown. Longer windows keep
// the sample path so the result-row limit still bounds memory.
const maxRatePushdownRange = 15 * time.Minute

// ErrRateUnsupported tells the engine to use the sample path for a selector or
// table shape the rate pushdown cannot represent exactly.
var ErrRateUnsupported = errors.New("prom: rate pushdown unsupported")

// RateFetcher is the optional fast path for an instant sum(rate(...)).
type RateFetcher interface {
	FetchRate(ctx context.Context, sel Selector, start, end time.Time, byService bool) (Vector, error)
}

var _ RateFetcher = (*ClickHouseFetcher)(nil)

// FetchRate runs the fixed rate pushdown per candidate table and merges the
// scalar or service groups.
func (f *ClickHouseFetcher) FetchRate(ctx context.Context, sel Selector, start, end time.Time, byService bool) (Vector, error) {
	if !end.After(start) || end.Sub(start) > maxRatePushdownRange {
		return nil, ErrRateUnsupported
	}
	for _, matcher := range sel.Matchers {
		if matcher.Name != "__name__" {
			return nil, ErrRateUnsupported
		}
	}
	tables, err := f.candidateTables(ctx, sel)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, ErrRateUnsupported
	}
	for _, table := range tables {
		if table != tableGauge && table != tableSum {
			return nil, ErrRateUnsupported
		}
	}
	gaugeFilter, sumFilter, _, _ := nameFilters(sel)

	var (
		totalCount int64
		totalValue float64
		groups     = make(map[string]float64)
	)
	for _, table := range tables {
		var (
			name   string
			filter plan.Expr
		)
		switch table {
		case tableGauge:
			name, filter = f.Names.MetricsGauge, gaugeFilter
		case tableSum:
			name, filter = f.Names.MetricsSum, sumFilter
		}
		nonEmpty, err := f.cacheFor().nonEmpty.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		if !nonEmpty {
			continue
		}
		emitted, err := f.Emitter.EmitRate(sqlgen.RateQuery{
			Table:          name,
			Where:          filter,
			Start:          start,
			End:            end,
			GroupByService: byService,
		})
		if err != nil {
			return nil, fmt.Errorf("prom: build rate query: %w", err)
		}
		result, err := f.Client.QueryCompact(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
		if err != nil {
			return nil, fmt.Errorf("prom: rate query %s: %w", name, err)
		}
		if byService {
			for i := 0; i < result.Len(); i++ {
				count, _ := result.Int64(i, "SeriesCount")
				if count <= 0 {
					continue
				}
				value, ok := result.Float64(i, "value")
				if !ok {
					return nil, fmt.Errorf("prom: rate query %s returned no value", name)
				}
				group, _ := result.String(i, "GroupName")
				groups[group] += value
			}
			continue
		}
		if result.Len() == 0 {
			continue
		}
		count, _ := result.Int64(0, "SeriesCount")
		if count <= 0 {
			continue
		}
		value, ok := result.Float64(0, "value")
		if !ok {
			return nil, fmt.Errorf("prom: rate query %s returned no value", name)
		}
		totalCount += count
		totalValue += value
	}

	if byService {
		out := make(Vector, 0, len(groups))
		for group, value := range groups {
			labels := make(map[string]string, 1)
			if group != "" {
				labels["service_name"] = group
			}
			out = append(out, VectorSample{Labels: labels, T: end.UnixMilli(), V: value})
		}
		sortVector(out)
		return out, nil
	}
	if totalCount == 0 {
		return Vector{}, nil
	}
	return Vector{{Labels: map[string]string{}, T: end.UnixMilli(), V: totalValue}}, nil
}

// queryRate recognizes the exact instant shapes the pushdown supports:
// sum(rate(<selector>[range])) and sum by (service_name)(rate(...)).
func (e *Engine) queryRate(ctx context.Context, expr parser.Expr, at time.Time) (Vector, bool, error) {
	agg, ok := expr.(*parser.AggregateExpr)
	if !ok || agg.Op != parser.SUM || agg.Without || agg.Param != nil {
		return nil, false, nil
	}
	byService := false
	switch len(agg.Grouping) {
	case 0:
	case 1:
		if agg.Grouping[0] != "service_name" {
			return nil, false, nil
		}
		byService = true
	default:
		return nil, false, nil
	}
	call, ok := agg.Expr.(*parser.Call)
	if !ok || call.Func.Name != "rate" || len(call.Args) != 1 {
		return nil, false, nil
	}
	matrix, ok := call.Args[0].(*parser.MatrixSelector)
	if !ok {
		return nil, false, nil
	}
	vs, ok := matrix.VectorSelector.(*parser.VectorSelector)
	if !ok {
		return nil, false, nil
	}
	if vs.OriginalOffset != 0 || vs.Timestamp != nil || vs.StartOrEnd != 0 {
		return nil, false, nil
	}
	if matrix.Range <= 0 || matrix.Range > maxRatePushdownRange {
		return nil, false, nil
	}
	fetcher, ok := e.Fetcher.(RateFetcher)
	if !ok {
		return nil, false, nil
	}
	start := at.Add(-matrix.Range)
	vec, err := fetcher.FetchRate(ctx, selectorFromVector(vs), start, at, byService)
	if errors.Is(err, ErrRateUnsupported) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	for i := range vec {
		vec[i].T = at.UnixMilli()
	}
	sortVector(vec)
	return vec, true, nil
}
