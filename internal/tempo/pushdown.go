package tempo

import (
	"strings"

	"github.com/jawaracloud/skopos/internal/plan"
)

// FilterPredicate compiles the safe intrinsic subset of a TraceQL filter into a
// ClickHouse predicate. It returns false when the filter cannot be pushed
// safely; callers then fetch the time range and evaluate in Go.
func FilterPredicate(filter Filter) (plan.Expr, bool) {
	return filterPredicate(filter)
}

func filterPredicate(filter Filter) (plan.Expr, bool) {
	switch typed := filter.(type) {
	case nil:
		return nil, false
	case condition:
		return conditionPredicate(typed)
	case andFilter:
		predicates := make([]plan.Expr, 0, len(typed))
		for _, child := range typed {
			if predicate, ok := filterPredicate(child); ok {
				predicates = append(predicates, predicate)
			}
		}
		return combinePredicates("AND", predicates)
	case orFilter:
		predicates := make([]plan.Expr, 0, len(typed))
		for _, child := range typed {
			predicate, ok := filterPredicate(child)
			if !ok {
				return nil, false
			}
			predicates = append(predicates, predicate)
		}
		return combinePredicates("OR", predicates)
	default:
		return nil, false
	}
}

func combinePredicates(operator string, predicates []plan.Expr) (plan.Expr, bool) {
	switch len(predicates) {
	case 0:
		return nil, false
	case 1:
		return predicates[0], true
	default:
		combined := predicates[0]
		for _, predicate := range predicates[1:] {
			combined = plan.Bin(operator, combined, predicate)
		}
		return combined, true
	}
}

func conditionPredicate(condition condition) (plan.Expr, bool) {
	if condition.nilValue || condition.regex != nil || condition.op == "contains" {
		return nil, false
	}
	if !condition.intrinsic || (condition.scope != "span" && condition.scope != "intrinsic") {
		return nil, false
	}
	switch condition.key {
	case "name":
		if condition.op != "=" && condition.op != "!=" {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("SpanName"), plan.String("span_name", condition.value)), true
	case "duration":
		if condition.duration == nil {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("Duration"), plan.Uint("duration_ns", uint64(*condition.duration))), true
	case "status":
		value := enumSQLValue("status", condition.value)
		if value == "" || (condition.op != "=" && condition.op != "!=") {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("StatusCode"), plan.String("span_status", value)), true
	case "kind":
		value := enumSQLValue("kind", condition.value)
		if value == "" || (condition.op != "=" && condition.op != "!=") {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("SpanKind"), plan.String("span_kind", value)), true
	default:
		return nil, false
	}
}

func enumSQLValue(kind, value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch kind {
	case "status":
		switch value {
		case "ERROR":
			return "Error"
		case "OK":
			return "Ok"
		case "UNSET":
			return "Unset"
		}
	case "kind":
		switch value {
		case "SERVER":
			return "Server"
		case "CLIENT":
			return "Client"
		case "PRODUCER":
			return "Producer"
		case "CONSUMER":
			return "Consumer"
		case "INTERNAL":
			return "Internal"
		case "UNSPECIFIED":
			return "Unspecified"
		}
	}
	return ""
}
