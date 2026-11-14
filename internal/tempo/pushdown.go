package tempo

import (
	"strconv"
	"strings"

	"github.com/mrofisr/skopos/internal/plan"
)

// FilterPredicate compiles the safe intrinsic subset of a TraceQL filter into a
// ClickHouse predicate. It returns false when the filter cannot be pushed
// safely; callers then fetch the time range and evaluate in Go.
func FilterPredicate(filter Filter) (plan.Expr, bool) {
	namer := &paramNamer{counts: make(map[string]int)}
	return filterPredicate(filter, namer)
}

type paramNamer struct {
	counts map[string]int
}

// next keeps the first name stable and suffixes repeats, so two conditions on
// the same attribute key cannot collide in one emitted query.
func (n *paramNamer) next(prefix string) string {
	n.counts[prefix]++
	if n.counts[prefix] == 1 {
		return prefix
	}
	return prefix + "_" + strconv.Itoa(n.counts[prefix])
}

func filterPredicate(filter Filter, namer *paramNamer) (plan.Expr, bool) {
	switch typed := filter.(type) {
	case nil:
		return nil, false
	case condition:
		return conditionPredicate(typed, namer)
	case andFilter:
		predicates := make([]plan.Expr, 0, len(typed))
		for _, child := range typed {
			if predicate, ok := filterPredicate(child, namer); ok {
				predicates = append(predicates, predicate)
			}
		}
		return combinePredicates("AND", predicates)
	case orFilter:
		predicates := make([]plan.Expr, 0, len(typed))
		for _, child := range typed {
			predicate, ok := filterPredicate(child, namer)
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

func conditionPredicate(condition condition, namer *paramNamer) (plan.Expr, bool) {
	if condition.regex != nil || condition.op == "contains" {
		return nil, false
	}
	if !condition.intrinsic {
		switch condition.scope {
		case "span", "resource":
			return attributePredicate(condition, namer)
		default:
			return nil, false
		}
	}
	if condition.scope != "span" && condition.scope != "intrinsic" {
		return nil, false
	}
	switch condition.key {
	case "name":
		if condition.op != "=" && condition.op != "!=" {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("SpanName"), plan.String(namer.next("span_name"), condition.value)), true
	case "duration":
		if condition.duration == nil {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("Duration"), plan.Uint(namer.next("duration_ns"), uint64(*condition.duration))), true
	case "status":
		value := enumSQLValue("status", condition.value)
		if value == "" || (condition.op != "=" && condition.op != "!=") {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("StatusCode"), plan.String(namer.next("span_status"), value)), true
	case "kind":
		value := enumSQLValue("kind", condition.value)
		if value == "" || (condition.op != "=" && condition.op != "!=") {
			return nil, false
		}
		return plan.Bin(condition.op, plan.Col("SpanKind"), plan.String(namer.next("span_kind"), value)), true
	default:
		return nil, false
	}
}

func attributePredicate(condition condition, namer *paramNamer) (plan.Expr, bool) {
	if condition.key == "" {
		return nil, false
	}
	column := "SpanAttributes"
	prefix := "span_attr"
	if condition.scope == "resource" {
		column = "ResourceAttributes"
		prefix = "resource_attr"
	}
	value := plan.Map(plan.Col(column), plan.String(namer.next(prefix+"_key"), condition.key))
	present := plan.F("mapContains", plan.Col(column), plan.String(namer.next(prefix+"_present_key"), condition.key))

	if condition.nilValue {
		return plan.Bin("=", present, plan.Bool(namer.next(prefix+"_present"), condition.op == "!=")), true
	}
	if condition.number != nil {
		switch condition.op {
		case "=", "!=", ">", "<", ">=", "<=":
			number := plan.F("toFloat64OrNull", value)
			comparison := plan.Bin(condition.op, number, plan.Float(namer.next(prefix+"_number"), *condition.number))
			return plan.Bin("AND", present, comparison), true
		}
		return nil, false
	}
	if condition.duration != nil {
		return nil, false
	}
	switch condition.op {
	case "=", "!=":
		comparison := plan.Bin(condition.op, value, plan.String(namer.next(prefix+"_value"), condition.value))
		return plan.Bin("AND", present, comparison), true
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
