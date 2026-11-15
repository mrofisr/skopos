// Package prom implements the Prometheus-compatible query head (M3).
//
// PromQL is parsed with the official Prometheus parser (ADR D18). Evaluation
// is performed in Go over samples fetched from ClickHouse. The supported
// function list is published in docs/promql-support.md; unsupported functions
// return explicit errors.
package prom

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

// Sample is one point in a time series. T is Unix milliseconds.
type Sample struct {
	T int64
	V float64
}

// Series is a labelled time series.
type Series struct {
	Labels  map[string]string
	Samples []Sample
}

// VectorSample is one instant-vector element. T is the evaluation timestamp;
// SampleT is the original sample timestamp (used by timestamp()) and is only
// valid when SampleTValid is true. Synthetic samples produced by expressions
// have no original timestamp; timestamp() falls back to T for those.
type VectorSample struct {
	Labels       map[string]string
	T            int64
	V            float64
	SampleT      int64
	SampleTValid bool
}

// ErrBadQuery marks parse and unsupported-expression errors that are the
// caller's fault and should map to HTTP 400.
var ErrBadQuery = errors.New("prom: bad query")

// Vector is an instant vector.
type Vector []VectorSample

// Matrix is a range vector.
type Matrix []Series

// Scalar is a PromQL scalar.
type Scalar float64

// Value is a PromQL result value.
type Value interface {
	Type() string
}

func (Vector) Type() string { return "vector" }
func (Matrix) Type() string { return "matrix" }
func (Scalar) Type() string { return "scalar" }

// Selector is a normalized vector selector passed to the storage layer. The
// storage layer applies the matchers and returns label sets in Prometheus form.
type Selector struct {
	Matchers []*labels.Matcher
}

// String returns a stable cache key for the selector.
func (s Selector) String() string {
	parts := make([]string, 0, len(s.Matchers))
	for _, m := range s.Matchers {
		parts = append(parts, m.String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// ExactName returns an exact __name__ matcher if one exists.
func (s Selector) ExactName() (string, bool) {
	for _, m := range s.Matchers {
		if m.Name == labels.MetricName && m.Type == labels.MatchEqual {
			return m.Value, true
		}
	}
	return "", false
}

// Fetcher loads all series matching sel in [start, end]. It must apply the
// selector matchers and normalize labels to Prometheus names. Implementations
// should return an error rather than silently truncating when their row limit
// is reached.
type Fetcher interface {
	Fetch(ctx context.Context, sel Selector, start, end time.Time) ([]Series, error)
}

// Label helpers.

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func labelsKey(m map[string]string) string {
	switch len(m) {
	case 0:
		return ""
	case 1:
		for k, v := range m {
			return k + "=" + v + "\x00"
		}
	}
	var small [8]string
	keys := small[:0]
	if len(m) > len(small) {
		keys = make([]string, 0, len(m))
	}
	size := 0
	for k, v := range m {
		keys = append(keys, k)
		size += len(k) + len(v) + 2
	}
	sort.Strings(keys)
	var b strings.Builder
	b.Grow(size)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(0)
	}
	return b.String()
}

// sortVector sorts by label key with each key computed once. Sorting by
// labelsKey in the comparator recomputed every key O(n log n) times.
func sortVector(v Vector) {
	if len(v) < 2 {
		return
	}
	keys := make([]string, len(v))
	for i := range v {
		keys[i] = labelsKey(v[i].Labels)
	}
	sort.Sort(&vectorByKey{v: v, keys: keys})
}

// sortSeries is sortVector for fetched series.
func sortSeries(series []Series) {
	if len(series) < 2 {
		return
	}
	keys := make([]string, len(series))
	for i := range series {
		keys[i] = labelsKey(series[i].Labels)
	}
	sort.Sort(&seriesByKey{series: series, keys: keys})
}

type seriesByKey struct {
	series []Series
	keys   []string
}

func (s *seriesByKey) Len() int           { return len(s.series) }
func (s *seriesByKey) Less(i, j int) bool { return s.keys[i] < s.keys[j] }
func (s *seriesByKey) Swap(i, j int) {
	s.series[i], s.series[j] = s.series[j], s.series[i]
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
}

type vectorByKey struct {
	v    Vector
	keys []string
}

func (s *vectorByKey) Len() int           { return len(s.v) }
func (s *vectorByKey) Less(i, j int) bool { return s.keys[i] < s.keys[j] }
func (s *vectorByKey) Swap(i, j int) {
	s.v[i], s.v[j] = s.v[j], s.v[i]
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
}

func labelString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// NormalizeLabelName converts an OpenTelemetry attribute key into a Prometheus
// label name. The mapping is intentionally simple and documented: every
// character outside [A-Za-z0-9_] becomes '_', and a leading digit is prefixed
// with '_'. It is the same mapping used for metric names.
func NormalizeLabelName(name string) string {
	var b strings.Builder
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if !ok {
			b.WriteByte('_')
			continue
		}
		if i == 0 && r >= '0' && r <= '9' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
