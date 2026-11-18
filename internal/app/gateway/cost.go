// This file estimates admission cost from request parameters. Range requests
// cost points divided by the configured points-per-slot budget; instant
// requests cost the estimated rows they read divided by rows-per-slot. Each
// weight is at least one slot, and weighting can be switched off entirely.
package gateway

import (
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

const (
	tempoSearchStep = time.Minute
	// instantSampleInterval is the assumed scrape interval used to turn an
	// instant query's range selectors into an estimated row count. It is a
	// conservative proxy, not a claim about any particular deployment.
	instantSampleInterval = 15 * time.Second
	maxWeight             = 1 << 30
)

// CostModel holds the admission work estimates. Cardinality is injected by
// the prom cache; a nil function or a zero answer means "unknown" and uses
// the configured fallback weight.
type CostModel struct {
	Enabled        bool
	PointsPerSlot  int
	RowsPerSlot    int
	FallbackWeight int
	Cardinality    func(context.Context, string) int
}

// requestCost returns the admission weight for one request. Invalid or
// incomplete parameters fall back to one so the handler can return its normal
// 400 without being rejected by admission first.
func (m CostModel) requestCost(ctx context.Context, path string, values url.Values) int {
	if !m.Enabled {
		return 1
	}
	switch {
	case strings.HasSuffix(path, "/query_range") &&
		(strings.HasPrefix(path, "/api/v1/") || strings.HasPrefix(path, "/loki/")):
		if m.PointsPerSlot <= 0 {
			return 1
		}
		return rangeCost(values, m.PointsPerSlot)
	case path == "/api/search" || path == "/api/v2/search":
		if m.PointsPerSlot <= 0 {
			return 1
		}
		return durationCost(values, tempoSearchStep, m.PointsPerSlot)
	case path == "/api/v1/query":
		if m.RowsPerSlot <= 0 {
			return 1
		}
		return m.instantCost(ctx, values)
	default:
		return 1
	}
}

// requestCost is the range/search-only form used by callers that have no
// instant cardinality source. It keeps the pre-instant-model semantics:
// a non-positive pointsPerSlot disables weighting.
func requestCost(path string, values url.Values, pointsPerSlot int) int {
	return CostModel{Enabled: pointsPerSlot > 0, PointsPerSlot: pointsPerSlot}.requestCost(context.Background(), path, values)
}

// instantCost weights an instant query by the rows it is estimated to read:
// the cardinality of its selectors times the number of samples in the largest
// range selector. Unknown cardinality uses the conservative fallback weight.
func (m CostModel) instantCost(ctx context.Context, values url.Values) int {
	raw := strings.TrimSpace(values.Get("query"))
	if raw == "" {
		return 1
	}
	expr, err := parser.NewParser(parser.Options{}).ParseExpr(raw)
	if err != nil {
		// The handler will return its normal 400; admission must not reject
		// first on a query it cannot understand.
		return 1
	}
	var (
		cardinality int64
		window      time.Duration
	)
	seen := make(map[string]struct{})
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		switch n := node.(type) {
		case *parser.MatrixSelector:
			if n.Range > window {
				window = n.Range
			}
		case *parser.VectorSelector:
			if n.Name == "" {
				return nil
			}
			if _, ok := seen[n.Name]; ok {
				return nil
			}
			seen[n.Name] = struct{}{}
			if m.Cardinality != nil {
				cardinality += int64(m.Cardinality(ctx, n.Name))
			}
		}
		return nil
	})
	if cardinality <= 0 {
		return clampWeight(m.FallbackWeight)
	}
	// Bound both terms before multiplying so a hostile query cannot overflow
	// the weight calculation.
	if cardinality > 1<<30 {
		cardinality = 1 << 30
	}
	samples := float64(1)
	if window > 0 {
		samples = math.Ceil(float64(window) / float64(instantSampleInterval))
		if samples > 1<<20 {
			samples = 1 << 20
		}
	}
	rows := float64(cardinality) * samples
	if rows > 1<<50 {
		rows = 1 << 50
	}
	return clampWeight(int(math.Ceil(rows / float64(m.RowsPerSlot))))
}

func clampWeight(weight int) int {
	if weight < 1 {
		return 1
	}
	if weight > maxWeight {
		return maxWeight
	}
	return weight
}

func rangeCost(values url.Values, pointsPerSlot int) int {
	step, ok := parseStep(values.Get("step"))
	if !ok {
		return 1
	}
	return durationCost(values, step, pointsPerSlot)
}

func durationCost(values url.Values, step time.Duration, pointsPerSlot int) int {
	start, ok := parseTime(values.Get("start"))
	if !ok {
		return 1
	}
	end, ok := parseTime(values.Get("end"))
	if !ok || !end.After(start) || step <= 0 {
		return 1
	}
	points := int64(math.Floor(end.Sub(start).Seconds()/step.Seconds())) + 1
	if points < 1 {
		return 1
	}
	// A single request can never exceed the limiter's int capacity in a useful
	// way; cap far above any real budget to keep the conversion safe.
	if points > 1<<30 {
		points = 1 << 30
	}
	weight := int(math.Ceil(float64(points) / float64(pointsPerSlot)))
	if weight < 1 {
		return 1
	}
	return weight
}

func parseTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return time.Time{}, false
		}
		whole := math.Floor(seconds)
		nanos := int64(math.Round((seconds - whole) * 1e9))
		return time.Unix(int64(whole), nanos), true
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseStep(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return duration, duration > 0
	}
	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}
