package osvexceptions

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFixtureValidation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		file     string
		want     string
		wantLen  int
		parseErr bool
	}{
		{name: "empty", file: "empty.yaml"},
		{name: "valid", file: "valid.yaml", wantLen: 2},
		{name: "expired", file: "expired.yaml", want: "expired", wantLen: 1},
		{name: "missing reason", file: "missing-reason.yaml", want: "reason is required", wantLen: 1},
		{name: "unknown id", file: "unknown-id.yaml", want: "unknown advisory id", wantLen: 1},
		{name: "duplicate id", file: "duplicate-id.yaml", want: "duplicate", wantLen: 2},
		{name: "missing expires", file: "missing-expires.yaml", want: "expires is required", wantLen: 1},
		{name: "unknown field", file: "unknown-field.yaml", want: "unknown field", parseErr: true},
		{name: "malformed", file: "malformed.yaml", want: "exceptions", parseErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + test.file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			ledger, err := Parse(data)
			if test.parseErr {
				if err == nil {
					t.Fatalf("Parse(%s) = %#v, want an error", test.file, ledger)
				}
				if !strings.Contains(err.Error(), test.want) {
					t.Fatalf("Parse error = %q, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%s): %v", test.file, err)
			}
			if len(ledger.Exceptions) != test.wantLen {
				t.Fatalf("exceptions = %d, want %d", len(ledger.Exceptions), test.wantLen)
			}
			err = Validate(ledger, now)
			if test.want == "" {
				if err != nil {
					t.Fatalf("Validate(%s): %v", test.file, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate(%s) = nil, want %q", test.file, test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	ledger := Ledger{Exceptions: []Exception{
		{ID: "GO-2026-0001", Expires: "2020-01-01"},
		{ID: "GO-2026-0002", Reason: "has a reason"},
	}}
	err := Validate(ledger, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("Validate = nil, want an error")
	}
	for _, want := range []string{"expired", "reason is required", "expires is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestValidateAcceptsAnExceptionExpiringToday(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 30, 0, 0, time.UTC)
	ledger := Ledger{Exceptions: []Exception{{
		ID:      "GO-2026-0006",
		Reason:  "Valid through the expiry date.",
		Expires: "2026-11-09",
	}}}
	if err := Validate(ledger, now); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestParseRejectsContentBeforeExceptions(t *testing.T) {
	if _, err := Parse([]byte("id: GO-2026-0001\n")); err == nil {
		t.Fatal("Parse accepted a top-level id")
	}
}
