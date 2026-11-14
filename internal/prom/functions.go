package prom

// supportedFunctions is the closed set of PromQL functions the evaluator
// implements. The behavior is wired in evalCall; the inventory test checks
// that every name here still evaluates and that every other function in the
// pinned parser table fails explicitly.
var supportedFunctions = map[string]struct{}{
	"abs":                {},
	"absent":             {},
	"absent_over_time":   {},
	"avg_over_time":      {},
	"ceil":               {},
	"clamp":              {},
	"clamp_max":          {},
	"clamp_min":          {},
	"count_over_time":    {},
	"exp":                {},
	"floor":              {},
	"histogram_quantile": {},
	"increase":           {},
	"irate":              {},
	"label_join":         {},
	"label_replace":      {},
	"last_over_time":     {},
	"ln":                 {},
	"log10":              {},
	"log2":               {},
	"max_over_time":      {},
	"min_over_time":      {},
	"present_over_time":  {},
	"quantile_over_time": {},
	"rate":               {},
	"round":              {},
	"scalar":             {},
	"sort":               {},
	"sort_desc":          {},
	"sqrt":               {},
	"stddev_over_time":   {},
	"stdvar_over_time":   {},
	"sum_over_time":      {},
	"time":               {},
	"timestamp":          {},
	"vector":             {},
}

// explicitErrorFunctions is the closed set of parser functions that Skopos
// deliberately does not implement. Each one must return an explicit
// unsupported-function error through the API, never an empty success.
//
// When upgrading the Prometheus parser, regenerate the difference between
// parser.Functions and supportedFunctions and review the new entries.
var explicitErrorFunctions = map[string]struct{}{
	"acos":               {},
	"acosh":              {},
	"asin":               {},
	"asinh":              {},
	"atan":               {},
	"atanh":              {},
	"changes":            {},
	"cos":                {},
	"cosh":               {},
	"day_of_month":       {},
	"day_of_week":        {},
	"day_of_year":        {},
	"days_in_month":      {},
	"deg":                {},
	"delta":              {},
	"deriv":              {},
	"first_over_time":    {},
	"histogram_avg":      {},
	"histogram_count":    {},
	"histogram_fraction": {},
	"histogram_stddev":   {},
	"histogram_stdvar":   {},
	"histogram_sum":      {},
	"hour":               {},
	"idelta":             {},
	"minute":             {},
	"month":              {},
	"pi":                 {},
	"predict_linear":     {},
	"rad":                {},
	"resets":             {},
	"sgn":                {},
	"sin":                {},
	"sinh":               {},
	"tan":                {},
	"tanh":               {},
	"year":               {},
}

// parserDisabledFunctions are experimental parser functions that the default
// parser configuration rejects with "function ... is not enabled" before the
// evaluator sees them. They are still explicit errors, never empty successes.
var parserDisabledFunctions = map[string]struct{}{
	"double_exponential_smoothing": {},
	"end":                          {},
	"histogram_quantiles":          {},
	"info":                         {},
	"mad_over_time":                {},
	"max_of":                       {},
	"min_of":                       {},
	"range":                        {},
	"sort_by_label":                {},
	"sort_by_label_desc":           {},
	"start":                        {},
	"start_timestamp":              {},
	"step":                         {},
	"ts_of_first_over_time":        {},
	"ts_of_last_over_time":         {},
	"ts_of_max_over_time":          {},
	"ts_of_min_over_time":          {},
}
