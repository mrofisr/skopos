// This file estimates admission cost from range request parameters. Instant
// requests cost one unit; range requests cost points divided by the configured
// points-per-slot budget.
package gateway

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const tempoSearchStep = time.Minute

// requestCost returns the admission weight for one request. Invalid or
// incomplete parameters fall back to one so the handler can return its normal
// 400 without being rejected by admission first.
func requestCost(path string, values url.Values, pointsPerSlot int) int {
	if pointsPerSlot <= 0 {
		return 1
	}
	switch {
	case strings.HasSuffix(path, "/query_range") &&
		(strings.HasPrefix(path, "/api/v1/") || strings.HasPrefix(path, "/loki/")):
		return rangeCost(values, pointsPerSlot)
	case path == "/api/search" || path == "/api/v2/search":
		return durationCost(values, tempoSearchStep, pointsPerSlot)
	default:
		return 1
	}
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
