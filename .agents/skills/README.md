# Shared AI skills

This directory is the repo-owned source of truth for reusable AI agent
workflows. Only content under `.agents/skills/` is committed. Agent-specific
folders such as `.claude/skills/`, `.codex/skills/`, and `.github/skills/` are
local configuration and stay untracked.

`AGENTS.md` and `CONTRIBUTING.md` are the always-loaded rules. A skill is an
on-demand runbook for a repeatable task: it names the inputs, the exact files,
the commands, the verification, and the guardrails. The description is loaded at
startup; the body is loaded only when the task matches.

## Layout

```text
.agents/skills/
  README.md                 this catalog and the shared rules
  authoring.md              how to add or update a skill
  templates/skill/SKILL.md.template  starting point for a new skill
  <skill-name>/SKILL.md     one skill
  <skill-name>/references/  optional detail loaded on demand
  <skill-name>/examples/    optional short examples
  <skill-name>/scripts/     optional deterministic automation
```

Each skill keeps the top level small. Put bulky detail in `references/`, short
runnable examples in `examples/`, and deterministic automation in `scripts/`.

## Catalog

| Skill | Use when | Main files |
| --- | --- | --- |
| [corpus-case](corpus-case/SKILL.md) | Adding compatibility coverage or fixing a divergence in PromQL, LogQL, or TraceQL | `test/corpus/`, `test/compat/prometheus/`, `internal/{prom,loki,tempo}/` |
| [support-matrix](support-matrix/SKILL.md) | A query feature, endpoint, deployment mode, or MCP capability changes | `docs/support-matrix.md`, the per-language support pages, `README.md` |
| [release-verify](release-verify/SKILL.md) | Preparing or verifying a release, or changing release and signing docs | `docs/release.md`, `scripts/release/`, `repoctl release-verify` |
| [security-review](security-review/SKILL.md) | Reviewing a security-sensitive diff | SQL emitter, auth, chart security contexts, NetworkPolicy, OBI, MCP, release |
| [flake-triage](flake-triage/SKILL.md) | A test fails intermittently, CI flakes, or a quarantine is proposed | the failing package, CI logs, `make test` and `make test-race` |

## How an agent should use a skill

1. Read `AGENTS.md` and `CONTRIBUTING.md` first. They are the rules.
2. If the request matches a description above, read that `SKILL.md` and follow
   its workflow. Do not skip its verification steps.
3. Use the `Makefile` and `repoctl` as the source of truth for commands. A
   skill may name a command; the `Makefile` defines it.
4. Report the exact commands and results, including checks you could not run.
5. If no skill matches, use the normal contribution loop in `CONTRIBUTING.md`.

## Adding a skill

Read [authoring.md](authoring.md), copy
[templates/skill/SKILL.md.template](templates/skill/SKILL.md.template) to
`<skill-name>/SKILL.md`, and update the catalog above in the same pull request.

## Rules

- **Agent-agnostic.** Do not put Codex-only, Claude-only, Copilot-only, or
  model-specific metadata in a shared skill. Local agent folders stay untracked.
- **One skill per repeatable task.** Do not create a skill for a one-off change
  or a single file.
- **Concise body.** Keep `SKILL.md` under about 5,000 tokens. Move detail into
  `references/` and load it only when needed.
- **No Python.** Scripts are Go where a decision is involved, or POSIX shell
  under 80 lines for thin orchestration. No `jq`, `awk`, or `sed` pipeline
  decides pass or fail. This is the ADR 0004 language policy.
- **No secrets.** Do not include tokens, passwords, kubeconfigs, cluster names,
  customer data, or machine-specific paths. Use placeholders such as `$TAG`.
- **Commands run from the repo root** unless the skill says otherwise.
- **Skills are not a second source of truth.** The `Makefile`, `CONTRIBUTING.md`,
  the ADRs, `deploy/helm/skopos/values.yaml`, and the committed corpora remain
  authoritative.
