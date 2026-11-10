package loki

// Construct is one LogQL construct and whether the pinned grammar is
// implemented or explicitly rejected. The inventory is executable evidence for
// docs/logql-support.md.
type Construct struct {
	Name  string
	Query string
	Class Classification
}

// Classification is supported or error.
type Classification string

const (
	Supported Classification = "supported"
	Rejected  Classification = "error"
)

// Inventory returns the LogQL constructs this build classifies. A construct is
// either parsed successfully or rejected with an explicit error; it is never
// silently ignored.
func Inventory() []Construct {
	selector := `{service_name="checkout"}`
	return []Construct{
		{Name: "line filter contains", Query: selector + ` |= "failed"`, Class: Supported},
		{Name: "line filter not contains", Query: selector + ` != "failed"`, Class: Supported},
		{Name: "line filter regex", Query: selector + ` |~ "fail.*"`, Class: Supported},
		{Name: "line filter not regex", Query: selector + ` !~ "fail.*"`, Class: Supported},

		{Name: "label equals", Query: selector + ` | level="error"`, Class: Supported},
		{Name: "label not equals", Query: selector + ` | level!="debug"`, Class: Supported},
		{Name: "label regex", Query: selector + ` | level=~"err(or)?"`, Class: Supported},
		{Name: "label not regex", Query: selector + ` | level!~"debug"`, Class: Supported},
		{Name: "label greater", Query: selector + ` | status > 500`, Class: Supported},
		{Name: "label greater or equal", Query: selector + ` | status >= 500`, Class: Supported},
		{Name: "label less", Query: selector + ` | status < 500`, Class: Supported},
		{Name: "label less or equal", Query: selector + ` | status <= 500`, Class: Supported},

		{Name: "json parser", Query: selector + ` | json`, Class: Supported},
		{Name: "json field selection", Query: selector + ` | json level="level"`, Class: Supported},
		{Name: "logfmt parser", Query: selector + ` | logfmt`, Class: Supported},
		{Name: "logfmt flags", Query: selector + ` | logfmt --strict`, Class: Rejected},
		{Name: "pattern parser", Query: selector + ` | pattern "<_>"`, Class: Rejected},
		{Name: "regexp parser", Query: selector + ` | regexp "(?P<level>\\w+)"`, Class: Rejected},
		{Name: "unpack parser", Query: selector + ` | unpack`, Class: Rejected},
		{Name: "ip parser", Query: selector + ` | ip`, Class: Rejected},
		{Name: "pack parser", Query: selector + ` | pack`, Class: Rejected},

		{Name: "line_format stage", Query: selector + ` | line_format "{{.level}}"`, Class: Rejected},
		{Name: "label_format stage", Query: selector + ` | label_format level=status`, Class: Rejected},
		{Name: "drop stage", Query: selector + ` | drop level`, Class: Rejected},
		{Name: "keep stage", Query: selector + ` | keep level`, Class: Rejected},
		{Name: "decolorize stage", Query: selector + ` | decolorize`, Class: Rejected},
		{Name: "unwrap stage", Query: selector + ` | unwrap duration`, Class: Rejected},
		{Name: "label_filter stage", Query: selector + ` | label_filter level`, Class: Rejected},

		{Name: "count_over_time", Query: `count_over_time(` + selector + `[5m])`, Class: Supported},
		{Name: "rate", Query: `rate(` + selector + `[5m])`, Class: Supported},
		{Name: "bytes_rate", Query: `bytes_rate(` + selector + `[5m])`, Class: Rejected},
		{Name: "bytes_over_time", Query: `bytes_over_time(` + selector + `[5m])`, Class: Rejected},
		{Name: "sum_over_time", Query: `sum_over_time(` + selector + `[5m])`, Class: Rejected},
		{Name: "avg_over_time", Query: `avg_over_time(` + selector + `[5m])`, Class: Rejected},
		{Name: "max_over_time", Query: `max_over_time(` + selector + `[5m])`, Class: Rejected},
		{Name: "min_over_time", Query: `min_over_time(` + selector + `[5m])`, Class: Rejected},
		{Name: "quantile_over_time", Query: `quantile_over_time(0.95, ` + selector + `[5m])`, Class: Rejected},
		{Name: "stdvar_over_time", Query: `stdvar_over_time(` + selector + `[5m])`, Class: Rejected},

		{Name: "sum aggregation", Query: `sum(rate(` + selector + `[5m]))`, Class: Supported},
		{Name: "sum by aggregation", Query: `sum by (level) (rate(` + selector + `[5m]))`, Class: Supported},
		{Name: "avg aggregation", Query: `avg(rate(` + selector + `[5m]))`, Class: Rejected},
		{Name: "max aggregation", Query: `max(rate(` + selector + `[5m]))`, Class: Rejected},
		{Name: "min aggregation", Query: `min(rate(` + selector + `[5m]))`, Class: Rejected},
		{Name: "count aggregation", Query: `count(rate(` + selector + `[5m]))`, Class: Rejected},
		{Name: "topk aggregation", Query: `topk(3, rate(` + selector + `[5m]))`, Class: Rejected},

		{Name: "constant vector", Query: `vector(1)`, Class: Supported},
		{Name: "constant vector arithmetic", Query: `vector(1)+vector(1)`, Class: Supported},
		{Name: "constant vector scalar arithmetic", Query: `vector(1)*2`, Class: Rejected},
		{Name: "nested constant vector arithmetic", Query: `vector(1)+vector(1)+vector(1)`, Class: Rejected},

		{Name: "offset modifier", Query: selector + ` offset 5m`, Class: Rejected},
		{Name: "bool modifier", Query: `sum(rate(` + selector + `[5m])) > bool 1`, Class: Rejected},
		{Name: "vector matching", Query: `sum(rate(` + selector + `[5m])) / on(level) sum(rate(` + selector + `[5m]))`, Class: Rejected},
		{Name: "query sharding", Query: selector + ` >> ` + selector, Class: Rejected},
	}
}

// Classify reports whether query parses and, when it fails, that the failure is
// an explicit error rather than a silent empty result.
func Classify(query string) Classification {
	if _, err := Parse(query); err != nil {
		return Rejected
	}
	return Supported
}
