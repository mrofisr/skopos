# Coding guidelines

These are the day-to-day Go conventions for Skopos. [`AGENTS.md`](../AGENTS.md)
is the normative layout and language policy; [`CONTRIBUTING.md`](../CONTRIBUTING.md)
is the contribution workflow. This page explains how the code should read and
what a reviewer should expect.

## Package and file organization

- Name a package for the responsibility it owns: `prom`, `loki`, `tempo`,
  `sqlgen`, `chclient`, `admission`, `checkpoint`. Do not add `util`, `common`,
  `helpers`, `misc`, or `types`.
- Keep the dependency direction one way: a query head depends on the plan and
  client packages, not the other way around. `internal/app` wires the modes.
- Every package has a `doc.go` with a package comment. The query heads also have
  a `README.md` that states the supported surface and links the support matrix.
- Split a file when it has more than one reason to change. The repository review
  threshold is about 600 lines; a file that crosses it needs a reason to stay
  whole.
- Put tests beside the code (`*_test.go`). Put black-box harnesses under `test/`.
  Do not add test-only helpers to a product package when a `_test.go` file in the
  same package will do.

## Context and errors

- Any function that does I/O or can block takes `context.Context` as its first
  parameter.
- Wrap errors with `%w` and a short operation prefix:

```go
return nil, fmt.Errorf("chclient: query %s: %w", name, err)
```

- Use `errors.Is` and `errors.As`, not string matching or direct type
  assertions.
- Define sentinel errors for conditions a caller must distinguish:
  `ErrBadQuery`, `ErrRejected`, `ErrCorruptRecord`. Keep the message lowercase
  and prefixed with the package name.
- Do not panic for control flow. A panic is reserved for a programmer invariant
  that cannot be recovered. `os.Exit` belongs only in `main`.
- Return the user an explicit error for an unsupported query or endpoint. Never
  return an empty result that looks successful.

## Logging

- Use `log/slog` with structured key/value pairs. Do not format a log line with
  `fmt.Sprintf`.
- Log messages are lowercase and have no trailing punctuation. Put variable data
  in keys:

```go
logger.Info("query completed", "head", "prom", "duration", elapsed)
```

- Do not log query text, credentials, bearer tokens, S3 keys, or full request
  bodies at info level. Use debug for detail that is safe to retain.
- Log a failure once, at the layer that has the context to explain it. Do not
  log and return the same error at every level.
- In an HTTP handler, use the request context and the request-scoped logger.
  Never log a user-controlled string without bounding it.

## Metrics

- Use the `skopos_` prefix for process metrics. Name counters `_total`,
  histograms and summaries with their unit (`_seconds`, `_bytes`), and gauges
  without a suffix.
- Do not use query text, trace IDs, user IDs, or unbounded error strings as
  label values. A label set must have a small, known cardinality.
- Prefer a small fixed set of labels and a separate metric over a dynamic label.
- Document a new metric in `docs/metrics.md` and its contract test.
- The Prometheus client is an approved dependency. A new non-stdlib dependency
  still needs an entry in [`docs/dependencies.md`](dependencies.md).

## Configuration and injection

- Read configuration once at the composition root. Pass the resulting values or
  a small options struct into the package; do not read the environment from deep
  code.
- Inject the clock, environment, filesystem, HTTP client, and command runner
  when the package needs them. This is what makes the tests deterministic.
- No package-level mutable state and no `init()`. Immutable sentinel errors and
  lookup tables are fine.
- Validate configuration at startup and fail with a message that names the
  setting and the expected form. Do not accept an invalid value and fail later
  on the first request.

## Concurrency and lifecycle

- Every goroutine has an owner and a way to stop. Tie it to a context, a
  `WaitGroup`, or a `Close` method.
- Respect context cancellation in loops and in outbound requests. Do not use
  `context.Background()` inside a request path.
- Close HTTP response bodies and files on every return path. A leaked body is a
  connection leak.
- Protect shared state with a mutex or a channel, and document which one owns
  the state.
- Run the race detector before claiming a concurrency change is safe:
  `make test-race`.

## HTTP handlers and query heads

- Bound the request body with `http.MaxBytesReader` or the configured limit.
  Bound the response and the result rows. Reject an oversized request before
  doing work.
- Keep `/healthz` and `/readyz` open for probes. Protect query, version, context,
  and `/metrics` routes with the same auth wrapper when auth is enabled.
- Return a stable status code and a machine-readable error body. Do not leak an
  internal error string to the client.
- Apply admission before the query reaches ClickHouse, and return `429` or `503`
  with `Retry-After` when the queue is full.
- A query head owns its parsing and response shaping. The shared SQL emitter
  owns parameterization; a head must not build SQL by concatenation.
- Add every new endpoint to `docs/support-matrix.md` and the route registration
  test.

## SQL and identifiers

- User values are always query parameters (`?`). Never interpolate a user value
  into SQL.
- Identifiers come from a whitelist or a validated identifier helper. Do not
  trust a label or table name because it came from a query parser.
- The gateway's ClickHouse user is read-only. Do not add a write path to the
  query client.
- Keep the execution, memory, rows-read, and result-row limits enforced in one
  place. A query setting must not be able to raise them.
- The fuzz test in `internal/sqlgen` is a gate, not a suggestion. A change to the
  emitter needs a new case if it changes the grammar or escaping.

## Tests

- Write the failing test first. The first commit on a `type:micro` pull request
  is tests-only; the PR guard checks it.
- Assert exact values. A test that only checks "no error" or "result is not
  empty" does not prove the behavior.
- Use table-driven tests with named cases:

```go
for _, tc := range []struct {
    name string
    // ...
}{...} {
    t.Run(tc.name, func(t *testing.T) {
        t.Parallel()
        // ...
    })
}
```

- Cover the invalid input, the boundary, and the empty result. A happy path alone
  is incomplete.
- Use `t.TempDir()` for files, `httptest` for HTTP, and `testing.F` for parser or
  emitter fuzzing. Do not write to the repository tree from a test.
- Poll with a deadline instead of sleeping. A fixed `time.Sleep` is a flake
  waiting for a slower machine.
- Do not skip a test or add an allow-list entry without a documented reason and
  human approval. `CONTRIBUTING.md` is explicit about this.
- A bug fix needs a regression test that fails without the fix.

## Helm and YAML

- Every container has `resources.requests`. Set `resources.limits` when you can
  explain the failure mode; do not copy a limit from another chart.
- Keep `securityContext` non-root, read-only where possible, and drop all
  capabilities unless the component needs one. Every added capability needs a
  comment and a human review.
- Add a `PodDisruptionBudget` for a replicated workload, or document why the
  workload is singleton and cannot be disrupted.
- Pin images by digest for a release. Do not add a floating tag.
- Do not add a third-party Helm subchart. Keep the chart self-contained.
- Add a comment above a value that is not obvious. The values file is user-facing
  documentation.
- Run `make helm-lint` and `make helm-template` after a chart change. Add a
  `values.schema.json` entry for a new value when the type or enum matters.

## Review checklist

- [ ] The package and file names describe the responsibility.
- [ ] Every I/O path takes a context and wraps errors with `%w`.
- [ ] No user value reaches SQL as a literal.
- [ ] Logs and metrics have bounded, non-sensitive labels.
- [ ] The test covers the invalid, boundary, and empty cases.
- [ ] A behavior change updates the support matrix and the proof.
- [ ] A chart change has requests, security context, and a render test.
- [ ] `make check` passes, or the pull request states exactly what could not run.
