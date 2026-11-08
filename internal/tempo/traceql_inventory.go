package tempo

// traceQLRejectedForm is one documented query shape the gateway rejects and
// the stable error text it returns, without the tempo: prefix and the
// supported-subset suffix.
type traceQLRejectedForm struct {
	Query string
	Want  string
}

// traceQLRejectedForms is the closed inventory of rejected forms documented in
// docs/traceql-support.md. TestRejectedFormsMatchDocs parses the docs tables
// and requires this list to match them exactly: adding a form here fails until
// the docs are updated, and adding a table row fails until the form is listed
// here.
var traceQLRejectedForms = []traceQLRejectedForm{
	{`{ } >> { span:name = "x" }`, `TraceQL structural operator ">>" is not supported`},
	{`{ } << { span:name = "x" }`, `TraceQL structural operator "<<" is not supported`},
	{`{ } ~ { span:name = "x" }`, `TraceQL structural operator "~" is not supported`},
	{`{ } > { span:name = "x" }`, `TraceQL structural operator ">" is not supported`},
	{`{ } < { span:name = "x" }`, `TraceQL structural operator "<" is not supported`},
	{`{ } !>> { span:name = "x" }`, `TraceQL structural operator "!>>" is not supported`},
	{`{ } !<< { span:name = "x" }`, `TraceQL structural operator "!<<" is not supported`},
	{`{ } !> { span:name = "x" }`, `TraceQL structural operator "!>" is not supported`},
	{`{ } !< { span:name = "x" }`, `TraceQL structural operator "!<" is not supported`},

	{`{a} && {b}`, `TraceQL multiple spansets are not supported (found "&&" between spansets)`},
	{`{a} || {b}`, `TraceQL multiple spansets are not supported (found "||" between spansets)`},
	{`{a} {b}`, `TraceQL multiple spansets are not supported`},
	{`{a} & {b}`, `TraceQL multiple spansets are not supported (found "&" between spansets)`},
	{`{ {a} }`, `TraceQL nested spansets are not supported`},

	{`{a} | count() > 1`, `TraceQL pipeline "| count()" is not supported`},
	{`{a} | by(span:name)`, `TraceQL pipeline "| by()" is not supported`},
	{`{a} | select(span:name)`, `TraceQL pipeline "| select()" is not supported`},
	{`{a} | coalesce()`, `TraceQL pipeline "| coalesce()" is not supported`},
	{`{a} | count()`, `TraceQL pipeline "| count()" is not supported`},
	{`count()`, `TraceQL pipeline "| count()" is not supported`},

	{`{ } | rate()`, `TraceQL metrics query "| rate()" is not supported`},
	{`rate()`, `TraceQL metrics query "| rate()" is not supported`},
	{`{ } | count_over_time()`, `TraceQL metrics query "| count_over_time()" is not supported`},
	{`{ } | quantile_over_time(duration, .99)`, `TraceQL metrics query "| quantile_over_time()" is not supported`},
	{`{ } | histogram_over_time(duration)`, `TraceQL metrics query "| histogram_over_time()" is not supported`},
	{`{ } | compare()`, `TraceQL metrics query "| compare()" is not supported`},

	{`{ (span:name = "x") }`, `TraceQL grouping, functions, and aggregations are not supported (found "(")`},
	{`{ span:count() > 1 }`, `TraceQL grouping, functions, and aggregations are not supported (found "(")`},

	{`{ span:name = "x"`, `TraceQL spanset is not closed: missing '}'`},
	{`{ span:name = "x" } foo`, `TraceQL unexpected text after the spanset: "foo"`},
	{`{ span:name = GET /cart }`, `TraceQL value "GET /cart" contains spaces; quote string values`},
	{`{ span:foo }`, `TraceQL condition "span:foo" has no operator`},
}
