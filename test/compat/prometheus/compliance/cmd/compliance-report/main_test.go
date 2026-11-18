package main

import "testing"

func TestMatchCategoryUnsupportedFunction(t *testing.T) {
	pol, err := loadPolicy("../../exceptions.json")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	cases := []struct {
		name   string
		record caseResult
		want   string
	}{
		{
			name: "wrapped bad query error",
			record: caseResult{
				Name:   `TestOfficialPromQLCompliance/at_modifier.test/line_163/minute(metric_@_1500)`,
				File:   "at_modifier.test",
				Action: "fail",
				Output: "Error:      \tReceived unexpected error:\n" +
					"error evaluating query \"minute(metric @ 1500)\" (line 163): prom: bad query: unsupported function \"minute\"\n",
			},
			want: "unsupported-function",
		},
		{
			name: "pre-wrap error text",
			record: caseResult{
				Name:   `TestOfficialPromQLCompliance/functions.test/line_11/resets(http_requests[5m])`,
				File:   "functions.test",
				Action: "fail",
				Output: "error evaluating query \"resets(http_requests[5m])\" (line 11): prom: unsupported function \"resets\"\n",
			},
			want: "unsupported-function",
		},
		{
			name: "wrong result stays unexpected",
			record: caseResult{
				Name:   `TestOfficialPromQLCompliance/functions.test/line_1/foo`,
				File:   "functions.test",
				Action: "fail",
				Output: "Error:      \tnot equal:\n got 2, want 3\n",
			},
			want: "",
		},
		{
			name: "expected error mismatch stays unexpected",
			record: caseResult{
				Name:   `TestOfficialPromQLCompliance/functions.test/line_2/foo`,
				File:   "functions.test",
				Action: "fail",
				Output: "expected error \"prom: unsupported function \\\"foo\\\"\" evaluating query \"foo\" (line 2), but got: ok\n",
			},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchCategory(pol, tc.record); got != tc.want {
				t.Fatalf("matchCategory() = %q, want %q", got, tc.want)
			}
		})
	}
}
