package tempo

// TraceQLRejectedForm is one documented rejected TraceQL shape and its stable
// error text.
type TraceQLRejectedForm struct {
	Query string
	Want  string
}

// TraceQLRejectedForms returns a copy of the documented rejected forms.
func TraceQLRejectedForms() []TraceQLRejectedForm {
	forms := make([]TraceQLRejectedForm, 0, len(traceQLRejectedForms))
	for _, form := range traceQLRejectedForms {
		forms = append(forms, TraceQLRejectedForm{Query: form.Query, Want: form.Want})
	}
	return forms
}

// TraceQLSupportedSubset is the prose suffix every unsupported-syntax error
// carries.
func TraceQLSupportedSubset() string { return traceQLSupportedSubset }
