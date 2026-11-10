// Command gate-report renders a bench-gate JSON result into the generated
// section of docs/benchmarks.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	beginMarker = "<!-- BEGIN GENERATED BENCH GATE -->"
	endMarker   = "<!-- END GENERATED BENCH GATE -->"
)

type report struct {
	TotalRequests      int `json:"total_requests"`
	Errors             int `json:"errors"`
	TransportErrors    int `json:"transport_errors"`
	Rejections         int `json:"rejections"`
	UnexpectedStatuses int `json:"unexpected_statuses"`
	MissingRetryAfter  int `json:"missing_retry_after"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	input := fs.String("input", "", "bench-gate JSON result")
	output := fs.String("report", "docs/benchmarks.md", "Markdown report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *input == "" {
		fmt.Fprintln(stderr, "gate-report: --input is required")
		return 2
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(stderr, "gate-report: read %s: %v\n", *input, err)
		return 1
	}
	var result report
	if err := json.Unmarshal(data, &result); err != nil {
		fmt.Fprintf(stderr, "gate-report: parse %s: %v\n", *input, err)
		return 1
	}
	document, err := os.ReadFile(*output)
	if err != nil {
		fmt.Fprintf(stderr, "gate-report: read %s: %v\n", *output, err)
		return 1
	}
	block := render(result)
	updated, err := replaceBlock(string(document), block)
	if err != nil {
		fmt.Fprintf(stderr, "gate-report: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*output, []byte(updated), 0o644); err != nil {
		fmt.Fprintf(stderr, "gate-report: write %s: %v\n", *output, err)
		return 1
	}
	fmt.Fprintf(stdout, "gate-report: updated %s\n", *output)
	return 0
}

func render(result report) string {
	status := "pass"
	if result.TransportErrors > 0 || result.UnexpectedStatuses > 0 || result.MissingRetryAfter > 0 {
		status = "fail"
	}
	var builder strings.Builder
	builder.WriteString(beginMarker + "\n")
	builder.WriteString("| Metric | Value |\n| --- | ---: |\n")
	fmt.Fprintf(&builder, "| Requests | %d |\n", result.TotalRequests)
	fmt.Fprintf(&builder, "| Errors | %d |\n", result.Errors)
	fmt.Fprintf(&builder, "| Transport errors | %d |\n", result.TransportErrors)
	fmt.Fprintf(&builder, "| Deliberate rejections | %d |\n", result.Rejections)
	fmt.Fprintf(&builder, "| Unexpected statuses | %d |\n", result.UnexpectedStatuses)
	fmt.Fprintf(&builder, "| Missing Retry-After | %d |\n", result.MissingRetryAfter)
	fmt.Fprintf(&builder, "| Gate | **%s** |\n", status)
	builder.WriteString(endMarker + "\n")
	return builder.String()
}

func replaceBlock(document, block string) (string, error) {
	begin := strings.Index(document, beginMarker)
	end := strings.Index(document, endMarker)
	if begin < 0 && end < 0 {
		return document + "\n" + block, nil
	}
	if begin < 0 || end < 0 || end < begin {
		return "", fmt.Errorf("markers in the benchmark report are malformed")
	}
	return document[:begin] + block + document[end+len(endMarker)+1:], nil
}
