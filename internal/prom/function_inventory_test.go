package prom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

// TestEveryParserFunctionIsClassified pins the PromQL surface to the vendored
// Prometheus parser. A parser upgrade that adds or removes a function fails
// here until the inventory in functions.go is updated.
func TestEveryParserFunctionIsClassified(t *testing.T) {
	classes := []map[string]struct{}{
		supportedFunctions,
		explicitErrorFunctions,
		parserDisabledFunctions,
	}
	for name := range parser.Functions {
		class := -1
		for i, set := range classes {
			if _, ok := set[name]; !ok {
				continue
			}
			if class >= 0 {
				t.Errorf("promql function %q is classified more than once", name)
				break
			}
			class = i
		}
		if class < 0 {
			t.Errorf("promql function %q is new; classify it as supported, explicit-error, or parser-disabled", name)
		}
	}
	for i, set := range classes {
		for name := range set {
			if _, ok := parser.Functions[name]; !ok {
				t.Errorf("class %d function %q is no longer in the parser table", i, name)
			}
		}
	}
}

// TestSupportedFunctionsEvaluate guards against the inventory drifting from the
// evaluator: a name classified as supported must not fall through to the
// unsupported-function default.
func TestSupportedFunctionsEvaluate(t *testing.T) {
	for _, name := range sortedFunctionNames(supportedFunctions) {
		fn := parser.Functions[name]
		query := sampleCall(fn)
		t.Run(name, func(t *testing.T) {
			if _, err := testEngine().Query(context.Background(), query, atFive()); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		})
	}
}

// TestExplicitErrorFunctionsReturnPrometheusError checks the evaluator-rejected
// functions through the HTTP API: each must return a Prometheus-shaped bad_data
// error, never a 200 with an empty result.
func TestExplicitErrorFunctionsReturnPrometheusError(t *testing.T) {
	assertEveryFunctionFails(t, explicitErrorFunctions, "unsupported function")
}

// TestParserDisabledFunctionsReturnPrometheusError checks the experimental
// functions that the default parser configuration rejects. They are still
// explicit bad_data errors, never empty successes.
func TestParserDisabledFunctionsReturnPrometheusError(t *testing.T) {
	assertEveryFunctionFails(t, parserDisabledFunctions, "is not enabled")
}

func assertEveryFunctionFails(t *testing.T, set map[string]struct{}, want string) {
	t.Helper()
	api := testAPI(testEngine(), fakeSeriesFetcher{})
	for _, name := range sortedFunctionNames(set) {
		fn := parser.Functions[name]
		query := sampleCall(fn)
		t.Run(name, func(t *testing.T) {
			if _, err := testEngine().Query(context.Background(), query, atFive()); err == nil {
				t.Fatalf("%s: want an explicit error", query)
			} else if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error = %q, want %q", query, err, want)
			}

			rec := do(t, api, "/api/v1/query?query="+url.QueryEscape(query))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status = %d, body = %s", query, rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s: decode: %v", query, err)
			}
			if body["status"] != "error" || body["errorType"] != "bad_data" {
				t.Fatalf("%s: body = %#v, want status=error errorType=bad_data", query, body)
			}
			message, _ := body["error"].(string)
			if !strings.Contains(message, name) {
				t.Fatalf("%s: error = %q, want the function name", query, message)
			}
		})
	}
}

func sortedFunctionNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sampleCall builds a syntactically and type-correct call from the parser's
// argument table. The second argument of info is a bare label selector.
func sampleCall(fn *parser.Function) string {
	args := make([]string, 0, len(fn.ArgTypes))
	for i, typ := range fn.ArgTypes {
		switch typ {
		case parser.ValueTypeVector:
			if fn.Name == "info" && i == 1 {
				args = append(args, `{service_name="checkout"}`)
			} else {
				args = append(args, "http_requests_total")
			}
		case parser.ValueTypeScalar:
			args = append(args, "1")
		case parser.ValueTypeMatrix:
			args = append(args, "http_requests_total[1m]")
		case parser.ValueTypeString:
			args = append(args, `"x"`)
		}
	}
	return fn.Name + "(" + strings.Join(args, ", ") + ")"
}

func TestFunctionInventoryIsComplete(t *testing.T) {
	inventory := FunctionInventory()
	if len(inventory) != len(supportedFunctions)+len(explicitErrorFunctions)+len(parserDisabledFunctions) {
		t.Fatalf("inventory length = %d, want %d", len(inventory),
			len(supportedFunctions)+len(explicitErrorFunctions)+len(parserDisabledFunctions))
	}
	seen := make(map[string]bool, len(inventory))
	for _, function := range inventory {
		if seen[function.Name] {
			t.Errorf("function %q appears twice", function.Name)
		}
		seen[function.Name] = true
		switch function.Class {
		case FunctionSupported:
			if _, ok := supportedFunctions[function.Name]; !ok {
				t.Errorf("%q is supported but not in supportedFunctions", function.Name)
			}
		case FunctionExplicitError:
			if _, ok := explicitErrorFunctions[function.Name]; !ok {
				t.Errorf("%q is an explicit error but not in explicitErrorFunctions", function.Name)
			}
		case FunctionParserDisabled:
			if _, ok := parserDisabledFunctions[function.Name]; !ok {
				t.Errorf("%q is parser-disabled but not in parserDisabledFunctions", function.Name)
			}
		default:
			t.Errorf("%q has unknown class %q", function.Name, function.Class)
		}
	}
}
