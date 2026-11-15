# Authoring shared skills

Shared skills are written once under `.agents/skills/` and consumed directly by
agents that support the shared `.agents` convention.

## Rules

- Create each skill at `.agents/skills/<skill-name>/`.
- Use a lowercase, hyphenated name that matches the directory name.
- Keep `SKILL.md` agent-agnostic. Do not put Codex-only, Claude-only, or
  Copilot-only metadata in the shared body.
- Do not commit `.codex/`, `.claude/`, `.github/skills`, or other local agent
  folders.
- Keep `SKILL.md` concise. Move bulky detail into `references/`, `examples/`, or
  `scripts/`.
- Prefer a bundled script for deterministic, repetitive work instead of long
  procedural prose.
- Use Go for scripts that make a decision. Use POSIX shell under 80 lines only
  for thin orchestration of external tools. Do not add Python; ADR 0004 forbids
  it.
- Markdown files do not need the Kubernetes Apache 2.0 boilerplate header. Add
  `license: Apache-2.0` to the frontmatter instead.
- Do not include secrets, tokens, kubeconfigs, cluster names, customer data, or
  machine-specific paths.

## Suggested process

1. Copy `.agents/skills/templates/skill/SKILL.md.template` to
   `.agents/skills/<skill-name>/SKILL.md`.
2. Update the `name` and `description` in the frontmatter.
3. Replace the template body with:
   - when the skill should be used;
   - what inputs it expects;
   - the exact workflow the agent should follow;
   - how to verify the result;
   - the guardrails that prevent a false pass.
4. Add supporting files only when they materially reduce ambiguity or repeated
   agent effort.
5. Update [README.md](README.md) so the catalog stays current.
6. Run the commands in the skill once against the current tree. A skill that
   points at a missing file or a stale command is worse than no skill.
7. Open a pull request that links the skill change to its motivation.

## Description field

The `description` is the primary signal an agent uses to decide whether to load
the skill. It is loaded at startup for every skill, so it must be concise and
specific.

Follow the [Agent Skills specification](https://agentskills.io/specification).

### Requirements

- At most 1024 characters.
- Describe **what** the skill does and **when** to use it.
- Include the words a user is likely to say: `corpus`, `support matrix`,
  `release`, `security review`, `flake`, and the relevant file or package names.

### Structure

Write the description in two parts:

1. **What it does** — a concise summary of the workflow.
2. **When to use it** — trigger phrases starting with "Use when...".

Good:

```yaml
description: >-
  Add or change a PromQL, LogQL, or TraceQL corpus case and prove it against the
  reference implementation. Use when adding compatibility coverage, fixing a
  divergence, or refreshing a corpus after a reference version bump.
```

Poor — no triggers:

```yaml
description: Helps with compatibility tests.
```

Poor — no what:

```yaml
description: Use when the user asks about tests.
```

## Progressive disclosure

| Layer | Loaded when | Budget |
| --- | --- | --- |
| `name` + `description` | Startup (all skills) | about 100 tokens per skill |
| `SKILL.md` body | Skill activated | under about 5,000 tokens |
| `references/`, `examples/`, `scripts/` | On demand during execution | no fixed limit |

Put the decision procedure and the guardrails in `SKILL.md`. Put long reference
material, full examples, and large scripts in the subdirectories.

## Body shape

Use these sections unless the task genuinely needs a different shape:

```markdown
## When to use

## Inputs

## Workflow

## Verification

## Guardrails

## References
```

The workflow is numbered and imperative. The verification section names exact
commands and the result that counts as a pass. The guardrails section names the
shortcuts that would produce a false pass.

## Scripts

- Accept explicit inputs; do not assume the caller's working directory.
- Prefer `go run ./tools/repoctl <command>` over a new script when the logic is a
  repository check. `repoctl` is the one developer tool.
- Keep shell scripts under 80 lines and under `scripts/<purpose>/` if they are
  repository assets. A skill-local script under `.agents/skills/<name>/scripts/`
  is allowed when it only serves that workflow.
- Do not parse or decide pass/fail with `jq`, `awk`, or `sed`.
- Do not write to the network or a cluster unless the skill says so and the
  command is read-only by default.

## Review checklist

- [ ] The name matches the directory and is lowercase-hyphenated.
- [ ] The description says what the skill does and when to use it.
- [ ] `SKILL.md` names the exact files and commands.
- [ ] The commands work from the repo root.
- [ ] The verification section distinguishes a pass from a partial run.
- [ ] The guardrails forbid updating expectations from the implementation under
      test.
- [ ] The catalog in `README.md` is updated.
- [ ] No secrets, local paths, or agent-specific metadata.
