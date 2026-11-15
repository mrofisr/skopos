package main

import "testing"

func TestSelectedQueriesSkip(t *testing.T) {
	if got := len(selectedQueries("")); got != len(queries) {
		t.Fatalf("selectedQueries(\"\") = %d queries, want %d", got, len(queries))
	}
	if got := len(selectedQueries("prom_range_6h, tempo_tags")); got != len(queries)-2 {
		t.Fatalf("skip two = %d queries, want %d", got, len(queries)-2)
	}
	for _, q := range selectedQueries("prom_range_6h, tempo_tags") {
		if q.Name == "prom_range_6h" || q.Name == "tempo_tags" {
			t.Fatalf("query %q was not skipped", q.Name)
		}
	}
}
