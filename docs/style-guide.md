# Documentation style guide

This guide covers the Markdown documentation under `docs/` and the top-level
README. The goal is documentation a new operator or contributor can read once
and act on.

## Page types

Choose one type per page. Do not mix a tutorial, a reference list, and an
architecture decision in the same document.

| Type | Purpose | Example |
| --- | --- | --- |
| Tutorial | Teach by doing, in order | `docs/install.md` |
| How-to | Solve one operational task | `docs/storage.md` |
| Reference | State exactly what exists | `docs/support-matrix.md` |
| Explanation | Explain why the system works this way | `docs/adr/` |
| Proposal | Argue for a change before it exists | `docs/proposals/` |

## Language

- Use active voice. Say who does what: "the gateway queries ClickHouse", not
  "ClickHouse is queried by the gateway".
- Use the present tense: "the collector writes", not "the collector will write".
- Address the reader as "you". Avoid "we" unless you are describing a decision
  the project made.
- Write full sentences. Do not chain clauses with semicolons and parentheses.
- Define a term before you use it, and use the project's existing word. Write
  `TraceQL`, not "the trace query language", if the product uses `TraceQL`.
- Avoid contractions: write "do not", "it is", "you will".
- Avoid marketing words: "leverage", "utilize", "seamless", "powerful". Say what
  the thing does.
- Be honest about limits. "Alpha", "not supported", and "not tested" are useful
  sentences, not weaknesses.

## Structure

- One H1 per page. Use sentence case for H2 and H3: `## Query limits`, not
  `## Query Limits`.
- Put the most important information first. A reader should not need to reach
  the bottom to learn the limitation.
- Use a table for a matrix of options, statuses, or defaults. Use a numbered
  list for a procedure. Use a bullet list for conditions.
- Keep paragraphs short. One idea per paragraph.
- Add a short "Before you start" or "Prerequisites" section to a procedure.
- End a procedure with the observable result: what the reader should see, and
  what to do if they do not.

## Formatting

- Wrap prose at about 80 columns. Tables and code blocks may be wider.
- Use fenced code blocks with a language tag: `sh`, `go`, `yaml`, `json`, `text`.
- Show a standalone command without a prompt. If a block shows a command and
  its output, prefix the command with `$` and leave the output unprefixed:

```sh
$ make check
ok  	github.com/mrofisr/skopos/internal/prom
```

- Use inline code for a command, file name, flag, field, metric, or value.
- Use relative links within `docs/`. Use a full URL only for an external source.
- Make link text descriptive. Do not write "here" or "click here".
- Use a blockquote for a note or warning:

> **Warning:** Basic auth is only safe over TLS.

- Do not add a table of contents to a short page. A long reference page may have
  one, but keep it in step with the headings and update it in the same change.

## Code examples

- Every command must be tested in a clean environment before it is documented.
- Prefer a real, complete command over a fragment. If a value is a placeholder,
  make that obvious with `<angle-brackets>` and explain it in the text.
- Do not paste a large generated file into a page. Link it or show the relevant
  lines.
- Keep examples current with the chart and flags. A stale example is worse than
  no example.
- If a command needs a cluster, a token, or a container runtime, say so before
  the command.

## Terminology

| Term | Use |
| --- | --- |
| Skopos | The project and the gateway binary |
| OBI | OpenTelemetry eBPF Instrumentation; do not expand it on every use |
| OpenTelemetry Collector | The Collector, after the first full mention |
| ClickHouse | The database; capitalize it |
| Grafana | The user-managed dashboard layer |
| PromQL, TraceQL, LogQL | The query languages; capitalize exactly |
| Prometheus, Loki, Tempo | The compatible APIs; capitalize |
| Kubernetes | Capitalize |
| Helm, Docker, systemd | Capitalize the product name; follow its own casing |
| namespace, pod, service, controller | Lowercase in prose |
| HTTP, HTTPS, TLS, MCP, SQL | Uppercase |

## Generated and source-of-truth documents

Do not hand-edit a generated file. Check the generator first.

| File | Source |
| --- | --- |
| `docs/workflows.md` | `go run ./tools/repoctl workflows-doc` |
| `docs/alpha-exit-report.md` | `make alpha-exit-check` |
| `docs/support-matrix.md` | Manual; keep it in step with the tests |
| `deploy/helm/skopos/values.yaml` | Manual; the chart values are the source of truth |
| `docs/security/ruleset.json` | The live ruleset snapshot; update deliberately |

When a document and the code disagree, the code and the tests win. Fix the
document, or open an issue if the code is wrong.

## AI-drafted prose

AI-drafted documentation gets the same review as hand-written prose. Before
opening a pull request, read every new paragraph and remove:

- passive constructions with no actor;
- sentences that restate the heading;
- hedging such as "this should typically" or "it may be possible";
- stacked qualifiers and nested parentheses;
- claims that are not backed by a test or a source link.

## Review checklist

- [ ] The page has one purpose and one H1.
- [ ] The first paragraph says what the page is for.
- [ ] Commands have language tags and were run.
- [ ] Links are relative and descriptive.
- [ ] Limits and unsupported behavior are explicit.
- [ ] A generated file was not edited by hand.
- [ ] The terminology matches the table above.
- [ ] `make check` passes, or the pull request states what could not run.
