package tempo

import (
	"strings"
	"testing"
)

func TestTraceQLInventoryAccessors(t *testing.T) {
	forms := TraceQLRejectedForms()
	if len(forms) == 0 {
		t.Fatal("rejected forms are empty")
	}
	for _, form := range forms {
		if form.Query == "" || form.Want == "" {
			t.Fatalf("incomplete form: %#v", form)
		}
	}
	forms[0].Query = "mutated"
	if TraceQLRejectedForms()[0].Query == "mutated" {
		t.Fatal("TraceQLRejectedForms returned an aliased slice")
	}
	if subset := TraceQLSupportedSubset(); !strings.Contains(subset, "supported subset") {
		t.Fatalf("supported subset = %q", subset)
	}
}
