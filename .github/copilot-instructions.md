# AI code review guidelines

Read [AGENTS.md](../AGENTS.md) and [CONTRIBUTING.md](../CONTRIBUTING.md) first.
They are the project rules. This file only tells a review bot what to prioritize
and when to stay silent.

## Review philosophy

- Comment only when you have high confidence that a real issue exists.
- One sentence per finding when possible. State the problem, why it matters, and
  the smallest fix.
- Focus on lines the pull request actually changes. Do not flag pre-existing
  style in code that was only moved.
- When uncertain, stay silent. A false positive is worse than a missed nit.

## Priority areas

### Security and correctness

- SQL or identifier injection in `internal/sqlgen` or a query head.
- Missing authentication on a route that should be protected, or a new route
  that bypasses the auth wrapper.
- Credentials, tokens, or telemetry leaked through logs, errors, status pages, or
  fixtures.
- Unbounded request size, response size, query time, memory, rows read, result
  rows, in-flight requests, or queue depth.
- A panic on malformed input, a leaked HTTP body or goroutine, or a context that
  is not cancelled.
- A Helm change that weakens `securityContext`, capabilities, NetworkPolicy, or
  the read-only ClickHouse user.

### Architecture and tests

- A new package that duplicates an existing responsibility, or a new `util`,
  `common`, `helpers`, `misc`, or `types` package.
- Product code importing `tools/repoctl/internal/...`, a second `cmd/` binary, a
  new Python file, or logic added to a shell script.
- A behavior change without a test, an expected value updated from the
  implementation under test, or a test skipped without a documented reason.
- A dependency, image, or action that is not pinned, or a dependency bump mixed
  into a feature pull request.

## Skip these

CI already owns them; do not add review noise:

- formatting and lint that `gofmt`, `go vet`, or the configured linters catch;
- generated files such as `docs/workflows.md` and rendered charts;
- test failures that the CI log already explains;
- missing dependencies that `go mod tidy` or Dependabot handles;
- version bumps in a dedicated dependency pull request.

## Response format

When you find an issue:

1. State the problem in one sentence.
2. State the impact in one sentence, if it is not obvious.
3. Give a concrete fix or a code snippet.

Do not use "could be clearer", "consider", or other non-actionable phrasing.
If the change is correct and the tests are adequate, approve without comment.
