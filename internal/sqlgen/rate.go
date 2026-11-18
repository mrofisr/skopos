package sqlgen

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrofisr/skopos/internal/plan"
)

// RateQuery is the fixed-shape pushdown for an instant sum(rate(...)).
//
// The inner query applies FINAL, the selector filter, and the left-open
// Prometheus range, then computes the per-series first/last values, sample
// interval, and counter-reset sum. The outer query applies the same
// extrapolatedRate formula as the Go engine and sums the result. Identifiers
// come from the schema; the filter and time window are parameters.
type RateQuery struct {
	Table string
	Where plan.Expr
	Start time.Time
	End   time.Time
	// GroupByService groups the final sum by service_name, matching
	// `sum by (service_name) (rate(...))`. The service label uses the
	// ServiceName column, falling back to the service.name attribute, as
	// baseLabels does.
	GroupByService bool
}

// EmitRate renders q as a parameterized rate pushdown statement.
func (e *Emitter) EmitRate(q RateQuery) (Result, error) {
	table, ok := e.schema.Table(q.Table)
	if !ok {
		return Result{}, fmt.Errorf("sqlgen: unknown table %q", q.Table)
	}
	if table.Signal != "" && table.Signal != plan.SignalMetrics {
		return Result{}, fmt.Errorf("sqlgen: rate query table %q is not a metrics table", q.Table)
	}
	if !q.End.After(q.Start) {
		return Result{}, errors.New("sqlgen: rate query end must be after start")
	}
	if q.End.Sub(q.Start) > e.limits.MaxRange {
		return Result{}, fmt.Errorf("sqlgen: rate query range %s exceeds maximum %s", q.End.Sub(q.Start), e.limits.MaxRange)
	}

	r := &renderer{
		schema:  e.schema,
		table:   table,
		params:  make([]plan.Param, 0, 8),
		seen:    make(map[string]struct{}),
		aliases: make(map[string]struct{}),
	}
	columns := make(map[string]string, 5)
	for _, name := range []string{"MetricName", "ServiceName", "Attributes", "TimeUnix", "Value"} {
		ref, err := r.reference(name)
		if err != nil {
			return Result{}, fmt.Errorf("sqlgen: rate query: %w", err)
		}
		columns[name] = ref
	}
	var whereSQL string
	if q.Where != nil {
		s, err := r.expr(q.Where, false)
		if err != nil {
			return Result{}, fmt.Errorf("sqlgen: rate where: %w", err)
		}
		whereSQL = s
	}
	start, err := r.addGenerated(plan.Time("__start", q.Start))
	if err != nil {
		return Result{}, err
	}
	end, err := r.addGenerated(plan.Time("__end", q.End))
	if err != nil {
		return Result{}, err
	}
	rangeSeconds, err := r.addGenerated(plan.Float("__range", q.End.Sub(q.Start).Seconds()))
	if err != nil {
		return Result{}, err
	}
	// The pushdown is metrics-only and the range is end-inclusive, matching
	// the WHERE below. Pin the buckets so the primary key can skip the rest of
	// the hour.
	bucketFrom, bucketTo := bucketBounds(q.Start, q.End, true)
	bucketStart, err := r.addGenerated(plan.Time("__bucket_start", bucketFrom))
	if err != nil {
		return Result{}, err
	}
	bucketEnd, err := r.addGenerated(plan.Time("__bucket_end", bucketTo))
	if err != nil {
		return Result{}, err
	}

	metric := columns["MetricName"]
	service := columns["ServiceName"]
	attrs := columns["Attributes"]
	timeColumn := columns["TimeUnix"]
	value := columns["Value"]

	var inner strings.Builder
	fmt.Fprintf(&inner, `SELECT
    %[1]s, %[2]s, %[3]s, %[4]s, %[5]s,
    lagInFrame(%[5]s) OVER (PARTITION BY %[1]s, %[2]s, %[3]s ORDER BY %[4]s) AS prev
FROM %[6]s.%[7]s FINAL
WHERE %[4]s > %[8]s AND %[4]s <= %[9]s
  AND toStartOfFiveMinutes(%[4]s) >= %[10]s
  AND toStartOfFiveMinutes(%[4]s) <= %[11]s`, metric, service, attrs, timeColumn, value,
		quoteIdent(e.schema.Database), quoteIdent(q.Table), start, end, bucketStart, bucketEnd)
	if whereSQL != "" {
		inner.WriteString("\n  AND (")
		inner.WriteString(whereSQL)
		inner.WriteString(")")
	}

	grouped := fmt.Sprintf(`SELECT
    %[1]s, %[2]s, %[3]s,
    min(%[4]s) AS FirstT,
    argMin(%[5]s, %[4]s) AS FirstV,
    max(%[4]s) AS LastT,
    argMax(%[5]s, %[4]s) AS LastV,
    count() AS N,
    sumIf(prev, %[5]s < prev) AS ResetSum
FROM (%[6]s)
GROUP BY %[1]s, %[2]s, %[3]s`, metric, service, attrs, timeColumn, value, inner.String())

	middle := fmt.Sprintf(`SELECT *,
    dateDiff('millisecond', FirstT, LastT) / 1000 AS sampled,
    (dateDiff('millisecond', FirstT, LastT) / 1000) / (N - 1) AS avg,
    ((dateDiff('millisecond', FirstT, LastT) / 1000) / (N - 1)) * 1.1 AS threshold,
    dateDiff('millisecond', %[1]s, FirstT) / 1000 AS dStart0,
    dateDiff('millisecond', LastT, %[2]s) / 1000 AS dEnd0,
    LastV - FirstV + ResetSum AS increase
FROM (%[3]s)
WHERE N >= 2 AND FirstT < LastT`, start, end, grouped)

	rates := fmt.Sprintf(`SELECT *,
    if(increase > 0 AND FirstV >= 0 AND sampled * FirstV / increase < if(dStart0 >= threshold, avg / 2, dStart0),
       sampled * FirstV / increase,
       if(dStart0 >= threshold, avg / 2, dStart0)) AS startAdj,
    if(dEnd0 >= threshold, avg / 2, dEnd0) AS endAdj
FROM (%s)`, middle)

	formula := fmt.Sprintf("if(sampled <= 0, 0, increase * ((sampled + startAdj + endAdj) / sampled) / %s)", rangeSeconds)
	var outer string
	if q.GroupByService {
		groupName := fmt.Sprintf("if(%s != '', %s, %s['service.name'])", service, service, attrs)
		outer = fmt.Sprintf(`SELECT
    %s AS GroupName,
    count() AS SeriesCount,
    sum(%s) AS value
FROM (%s)
GROUP BY GroupName`, groupName, formula, rates)
	} else {
		outer = fmt.Sprintf(`SELECT
    count() AS SeriesCount,
    sum(%s) AS value
FROM (%s)`, formula, rates)
	}
	return Result{SQL: outer, Params: r.params}, nil
}
