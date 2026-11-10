// Package archtest contains tests that enforce the repository layout described
// in ADR 0004 and AGENTS.md. The tests read `go list -json ./...` and compare
// the result with the shrink-only baseline in
// test/quality/layout-violations.txt.
package archtest
