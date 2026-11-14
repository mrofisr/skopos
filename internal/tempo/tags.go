package tempo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

// ErrTagFallback means a tag query is not safe to push into ClickHouse.
var ErrTagFallback = errors.New("tempo: tag query not pushable")

// TagFetcher is the optional tag-discovery surface. A backend that does not
// implement it keeps the Go evaluation path.
type TagFetcher interface {
	TagNames(ctx context.Context, start, end time.Time, filter Filter, limit int) (map[string][]string, error)
	TagValues(ctx context.Context, tag string, start, end time.Time, filter Filter, limit int) ([]string, error)
}

func tagPredicate(filter Filter) (plan.Expr, bool) {
	if filter == nil {
		return nil, true
	}
	return FilterPredicate(filter)
}

// TagNames computes tag names per scope in ClickHouse.
func (f *ClickHouseFetcher) TagNames(ctx context.Context, start, end time.Time, filter Filter, limit int) (map[string][]string, error) {
	predicate, ok := tagPredicate(filter)
	if !ok {
		return nil, ErrTagFallback
	}
	out := map[string][]string{
		"intrinsic": append([]string(nil), tagIntrinsics...),
	}
	queries := map[string]plan.Expr{
		"resource": plan.F("arrayJoin", plan.F("mapKeys", plan.Col("ResourceAttributes"))),
		"span":     plan.F("arrayJoin", plan.F("mapKeys", plan.Col("SpanAttributes"))),
		"event":    plan.F("arrayJoin", plan.F("mapKeys", plan.F("arrayJoin", plan.Col("Events.Attributes")))),
		"link":     plan.F("arrayJoin", plan.F("mapKeys", plan.F("arrayJoin", plan.Col("Links.Attributes")))),
	}
	for _, scope := range []string{"resource", "span", "event", "link"} {
		values, err := f.distinctTag(ctx, queries[scope], predicate, start, end, limit)
		if err != nil {
			return nil, err
		}
		out[scope] = values
	}
	services, err := f.distinctTag(ctx, plan.Col("ServiceName"), predicate, start, end, limit)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		if service != "" {
			out["resource"] = appendUnique(out["resource"], "service.name")
			break
		}
	}
	for scope, values := range out {
		sort.Strings(values)
		if limit > 0 && len(values) > limit {
			out[scope] = values[:limit]
		}
	}
	return out, nil
}

var tagIntrinsics = []string{
	"duration", "event:name", "event:timeSinceStart",
	"instrumentation:name", "instrumentation:version", "kind", "name",
	"rootName", "rootServiceName", "span:duration", "span:kind", "span:name",
	"span:status", "span:statusMessage", "status", "statusMessage",
	"trace:duration", "trace:rootName", "trace:rootService", "traceDuration",
}

// TagValues computes one tag's values in ClickHouse.
func (f *ClickHouseFetcher) TagValues(ctx context.Context, tag string, start, end time.Time, filter Filter, limit int) ([]string, error) {
	predicate, ok := tagPredicate(filter)
	if !ok {
		return nil, ErrTagFallback
	}
	expr, ok := tagValueExpr(tag)
	if !ok {
		return nil, ErrTagFallback
	}
	values, err := f.distinctTag(ctx, expr, predicate, start, end, limit)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *ClickHouseFetcher) distinctTag(ctx context.Context, expr, predicate plan.Expr, start, end time.Time, limit int) ([]string, error) {
	query := plan.Query{
		Signal:     plan.SignalTraces,
		Table:      f.Names.Traces,
		Select:     []plan.SelectItem{{Expr: expr, Alias: "tag"}},
		Where:      predicate,
		GroupBy:    []string{"tag"},
		TimeColumn: "Timestamp",
		TimeRange:  &plan.TimeRange{Start: start, End: end, EndInclusive: true},
	}
	if limit > 0 {
		query.Limit = limit
	} else {
		query.Limit = f.MaxRows
	}
	emitted, err := f.Emitter.Emit(query)
	if err != nil {
		return nil, fmt.Errorf("tempo: build tag query: %w", err)
	}
	result, err := f.Client.Query(chclient.WithHead(ctx, "tempo"), emitted.SQL, emitted.Params)
	if err != nil {
		return nil, fmt.Errorf("tempo: tag query: %w", err)
	}
	values := make([]string, 0, result.Len())
	for i := 0; i < result.Len(); i++ {
		if value, ok := result.String(i, "tag"); ok {
			values = append(values, value)
		}
	}
	return values, nil
}

func tagValueExpr(tag string) (plan.Expr, bool) {
	ref, err := parseKey(tag)
	if err != nil {
		return nil, false
	}
	switch ref.scope {
	case "resource":
		if ref.key == "service.name" {
			return plan.F("coalesce",
				plan.Map(plan.Col("ResourceAttributes"), plan.String("tag_key", ref.key)),
				plan.Col("ServiceName"),
			), true
		}
		return plan.Map(plan.Col("ResourceAttributes"), plan.String("tag_key", ref.key)), true
	case "span":
		if ref.intrinsic {
			switch ref.key {
			case "name":
				return plan.Col("SpanName"), true
			case "duration":
				return plan.F("toString", plan.Col("Duration")), true
			case "status":
				return plan.F("lower", plan.F("replaceRegexpAll",
					plan.Col("StatusCode"),
					plan.String("status_prefix", "^STATUS_CODE_"),
					plan.String("status_replacement", ""),
				)), true
			case "kind":
				return plan.F("lower", plan.F("replaceRegexpAll",
					plan.Col("SpanKind"),
					plan.String("kind_prefix", "^SPAN_KIND_"),
					plan.String("kind_replacement", ""),
				)), true
			case "statusMessage":
				return plan.Col("StatusMessage"), true
			default:
				return nil, false
			}
		}
		return plan.Map(plan.Col("SpanAttributes"), plan.String("tag_key", ref.key)), true
	case "event":
		if ref.intrinsic && ref.key == "name" {
			return plan.F("arrayJoin", plan.Col("Events.Name")), true
		}
		if ref.intrinsic && ref.key == "timeSinceStart" {
			return nil, false
		}
		return plan.Map(
			plan.F("arrayJoin", plan.Col("Events.Attributes")),
			plan.String("tag_key", ref.key),
		), true
	case "link":
		if ref.intrinsic && ref.key == "traceID" {
			return plan.F("arrayJoin", plan.Col("Links.TraceId")), true
		}
		return plan.Map(
			plan.F("arrayJoin", plan.Col("Links.Attributes")),
			plan.String("tag_key", ref.key),
		), true
	case "instrumentation":
		switch ref.key {
		case "name":
			return plan.Col("ScopeName"), true
		case "version":
			return plan.Col("ScopeVersion"), true
		}
		return nil, false
	case "trace":
		return nil, false
	default:
		return nil, false
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
