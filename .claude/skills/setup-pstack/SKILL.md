---
name: setup-pstack
description: Configure which models pstack uses per role. Detects available models and writes the current runtime's override sheet. Use for /setup-pstack or changing pstack's per-role model choices.
---

# Setup pstack

On Antigravity, read [`agy-tools.md`](../poteto-mode/references/agy-tools.md) before following this skill. Read [Other runtimes](references/runtimes.md) when configuring another runtime.

Write the current runtime's per-role model override sheet. In `HVAC_web`:
- When **no** override sheet exists (`~/.gemini/config/pstack-models.md` on Antigravity, or `~/.claude/pstack-models.md` / `.claude/pstack-models.md` on Claude Code), Antigravity keeps the safe default `Model: "inherit"` on every `invoke_subagent` call (matching `AGENTS.md`).
- Only override subagent models at runtime when you explicitly create an override sheet with this skill or explicitly specify a model in the user prompt.
- Note that `HVAC_web` intentionally omits upstream `SessionStart` routing hook injection (`session hook` lines are not active in this repository).

## Sheet locations

- **Antigravity (`agy`)**: `~/.gemini/config/pstack-models.md` (included from `~/.gemini/config/GEMINI.md` via `@[pstack-models](~/.gemini/config/pstack-models.md)`, or read explicitly before dispatching role subagents — Antigravity loads `~/.gemini/config/GEMINI.md`, not `AGENTS.md`, for user global rules).
- **Claude Code**: `~/.claude/pstack-models.md` (or `.claude/pstack-models.md` at the repository root when committed for the team; included via `@~/.claude/pstack-models.md` or `@.claude/pstack-models.md` in `CLAUDE.md`).

## Steps

### 1. Detect available models

- **Antigravity**: read the `Model` enum of `invoke_subagent` in the active tool schema (`inherit`, `flash_lite`, `flash`, `pro`). Notice `invoke_subagent` does not accept an effort parameter, so omit `@<level>` suffixes and `default effort`.
- **Claude Code**: use the `Agent` tool's accepted family slugs (`opus`, `sonnet`, `haiku`, plus `inherit-parent` or `auto` aliases) and valid effort levels (`low`, `medium`, `high`, `xhigh`, `max` where supported). Never write a model slug not confirmed in the active runtime.

### 2. Load current state

Ask whether to write a user-level sheet (for all personal sessions) or a project sheet (Claude Code `.claude/pstack-models.md`). Read any existing sheet first and drop retired roles (`how critics`, etc.).

### 3. Map and confirm

Present each role and let the user pick via `ask_question` (or keep `inherit` / `inherit-parent` everywhere):

- `feature, refactoring`, `bug-fix`, `perf-issue`, `hillclimb`
- `judgment and prose`, `how explorer`, `how explainer`, `why investigators`, `why synthesizer`
- `reflect tooling`, `reflect judgment, divergent, synthesizer`
- `arena runners`, `arena cross-judge pool`, `architect runners`, `interrogate reviewers` (list length sets panel fan-out count)
- `swarm workers`, `thermos reviewers`, `no-comments reviewer`

On Claude Code, also ask whether to set `default effort: session` or a specific level; on Antigravity, skip effort settings completely.

### 4. Validate and write the override sheet

Validate every entry against the runtime's detected model set before writing:

```markdown
# pstack model configuration

Per-role model overrides for pstack skills. On Antigravity, valid values are `inherit`, `flash_lite`, `flash`, `pro`. Omit or delete a line to fall back to `inherit` (`Model: "inherit"`).

feature, refactoring: inherit
bug-fix: inherit
perf-issue: inherit
hillclimb: inherit
judgment and prose: inherit
how explorer: inherit
how explainer: inherit
why investigators: inherit
why synthesizer: inherit
reflect tooling: inherit
reflect judgment, divergent, synthesizer: inherit
arena runners: inherit
arena cross-judge pool: inherit
swarm workers: inherit
architect runners: inherit
interrogate reviewers: inherit
thermos reviewers: inherit
no-comments reviewer: inherit
```

### 5. Confirm

Tell the user where the override sheet was written, remind them how it loads into context (`@[pstack-models](~/.gemini/config/pstack-models.md)` in `~/.gemini/config/GEMINI.md`, or `@~/.claude/pstack-models.md` / `@.claude/pstack-models.md` in `CLAUDE.md`), and confirm that verification skills for `HVAC_web` already exist:

- App E2E / UI proof: [`verify`](../verify/SKILL.md)
- Control-command / MQTT formal check: [`command-safety`](../command-safety/SKILL.md)
