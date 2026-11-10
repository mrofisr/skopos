package prom

import "sort"

// FunctionClass is how the PromQL surface treats one parser function.
type FunctionClass string

const (
	// FunctionSupported is implemented by the evaluator.
	FunctionSupported FunctionClass = "supported"
	// FunctionExplicitError is parsed but rejected with a Prometheus-shaped
	// unsupported-function error.
	FunctionExplicitError FunctionClass = "explicit error"
	// FunctionParserDisabled is rejected by the default parser configuration.
	FunctionParserDisabled FunctionClass = "parser-disabled"
)

// FunctionInfo is one classified PromQL function.
type FunctionInfo struct {
	Name  string
	Class FunctionClass
}

// FunctionInventory returns the classified function list in name order.
func FunctionInventory() []FunctionInfo {
	functions := make([]FunctionInfo, 0, len(supportedFunctions)+len(explicitErrorFunctions)+len(parserDisabledFunctions))
	for name := range supportedFunctions {
		functions = append(functions, FunctionInfo{Name: name, Class: FunctionSupported})
	}
	for name := range explicitErrorFunctions {
		functions = append(functions, FunctionInfo{Name: name, Class: FunctionExplicitError})
	}
	for name := range parserDisabledFunctions {
		functions = append(functions, FunctionInfo{Name: name, Class: FunctionParserDisabled})
	}
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	return functions
}
