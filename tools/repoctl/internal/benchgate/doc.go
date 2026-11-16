// Package benchgate compares a test/bench JSON report to a checked-in
// baseline and fails when latency, throughput, or errors regress beyond the
// tolerances in ADR 0009.
//
// The gate is relative, not an absolute SLO: it protects a measured baseline
// from regressions. The pilot and the beta acceptance check own the absolute
// budgets.
package benchgate
