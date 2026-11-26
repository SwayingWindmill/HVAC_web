---
name: setup-pstack
description: Configure which models pstack uses per role. Detects available models and writes the current runtime's override sheet. Use for /setup-pstack, "configure pstack models", "pstack budget", or changing pstack's model choices.
---

# Setup pstack

On Antigravity, read [`agy-tools.md`](../poteto-mode/references/agy-tools.md) before following this skill. Read [Other runtimes](references/runtimes.md) when configuring another runtime.

Write the current runtime's per-role model override sheet. In `HVAC_web`:

- When **no** override sheet exists (`~/.gemini/config/pstack-models.md` on Antigravity, or `~/.claude/rules/pstack-models.md` / `.claude/rules/pstack-models.md` on Claude Code), Antigravity keeps the safe default `Model: "inherit"` on every `invoke_subagent` call (matching `AGENTS.md`).
- Only override subagent models at runtime when you explicitly create an override sheet with this skill or explicitly specify a model in the user prompt.
- Note that `HVAC_web` omits upstream `SessionStart` hook injection (`session hook` lines are inert here).

## Sheet locations

- **Antigravity (`agy`)**: `~/.gemini/config/pstack-models.md` (included from `~/.gemini/config/GEMINI.md` via `@[pstack-models](~/.gemini/config/pstack-models.md)`, or read explicitly before dispatching role subagents — Antigravity loads `~/.gemini/config/GEMINI.md`, not `AGENTS.md`, for user global rules).
- **Claude Code**: `~/.claude/rules/pstack-models.md` (auto-loaded by Claude Code into every new session with **no** YAML frontmatter and no `@` include needed in `CLAUDE.md`; or `.claude/rules/pstack-models.md` at repository root when committed for the team). When re-running, migrate or remove any legacy `~/.claude/pstack-models.md` or `.claude/pstack-models.md` file after writing the rules path.

## Steps

### 1. Detect available models

- **Antigravity**: read the `Model` enum of `invoke_subagent` in the active tool schema (`inherit`, `flash_lite`, `flash`, `pro`). Notice `invoke_subagent` does not accept an effort parameter, so omit effort tokens (`@<level>`, `default effort`, and `# budget:`).
- **Claude Code**: enumerate the accepted `model` family slugs (`opus`, `sonnet`, `haiku`, `fable`, plus `inherit-parent` or `auto` aliases) and valid `effort` levels (`low`, `medium`, `high`, `xhigh`) from the `Agent` tool schema. Write real choices as `<model> <effort>` (for example, `opus xhigh`). Never write a model slug not confirmed in the active runtime.

### 2. Load current state

Ask whether to write a user-level sheet (for all personal sessions) or a project sheet (Claude Code `.claude/rules/pstack-models.md`). Read any existing sheet first (including legacy `~/.claude/pstack-models.md`) and drop retired roles (`how explorer`, `how explainer`, `how critics`, `why investigators`, `why synthesizer`, `reflect tooling`, `reflect judgment, divergent, synthesizer`, `architect runners`, `hardest tasks`). Tell the user which retired lines were dropped.

On Claude Code, if an existing sheet lacks `# defaults: 3`, convert `# budget: unlimited` to `large` (all roles cap at `xhigh`), replace legacy `grok-*` defaults or old 3-vendor `arena cross-judge pool` Fable+Opus+Gemini defaults with the current two-vendor pool (`opus high, inherit-parent` or verified non-Claude sibling), and move any `gemini-4-neon-high` panel entry to `inherit-parent` unless the user chose a custom value.

### 3. Budget, map, and confirm

- **On Claude Code**, offer the three reasoning budgets via `ask_question` (or `AskUserQuestion`):
  - `large — xhigh reasoning` (`xhigh` for code/judgment, `high` for `swarm workers` and panel Claude seats)
  - `medium — high reasoning` (steps each role down one rung on `xhigh > high > medium > low`)
  - `small — medium reasoning` (steps each role down two rungs)
- **On Antigravity**, skip the budget step completely because `invoke_subagent` has no effort parameter.

Present each active role and let the user confirm or adjust via `ask_question`:

- `feature, refactoring`, `bug-fix`, `perf-issue`, `hillclimb`
- `judgment and prose`
- `arena runners`, `arena cross-judge pool`, `interrogate reviewers` (list length sets panel fan-out; `arena cross-judge pool` tries one non-parent vendor entry in list order)
- `swarm workers`
- Optional repository review roles: `thermos reviewers`, `no-comments reviewer`

### 4. Validate and write the override sheet

Validate every entry against the runtime's detected model set before writing. Overwrite the file cleanly (no YAML frontmatter in `~/.claude/rules/pstack-models.md`).

Antigravity shape (`~/.gemini/config/pstack-models.md`):

```markdown
# pstack model configuration

Per-role model overrides for pstack skills. On Antigravity, valid values are `inherit`, `flash_lite`, `flash`, `pro`. Omit or delete a line to fall back to `inherit` (`Model: "inherit"`).

feature, refactoring: inherit
bug-fix: inherit
perf-issue: inherit
hillclimb: inherit
judgment and prose: inherit
arena runners: inherit
arena cross-judge pool: inherit
swarm workers: inherit
interrogate reviewers: inherit
thermos reviewers: inherit
no-comments reviewer: inherit
```

Claude Code shape (`~/.claude/rules/pstack-models.md`, no frontmatter):

```markdown
# pstack model configuration. One line per role. Delete a line to fall back to defaults.
# `inherit-parent` or `auto`: omit `model` on Agent so the role runs on the parent session model.
# defaults: 3
# budget: large (xhigh)
feature, refactoring: opus xhigh
bug-fix: opus xhigh
perf-issue: opus xhigh
hillclimb: opus xhigh
judgment and prose: opus xhigh
arena runners: fable high, opus high, inherit-parent
arena cross-judge pool: opus high, inherit-parent
swarm workers: opus high
interrogate reviewers: fable high, opus high, inherit-parent
thermos reviewers: opus high
no-comments reviewer: opus high
```

### 5. Confirm

Tell the user where the override sheet was written, remind them how it loads into context (`@[pstack-models](~/.gemini/config/pstack-models.md)` in `~/.gemini/config/GEMINI.md` for Antigravity, or auto-loaded from `~/.claude/rules/pstack-models.md` / `.claude/rules/pstack-models.md` for Claude Code), and confirm that verification skills for `HVAC_web` already exist:

- App E2E / UI proof: [`verify`](../verify/SKILL.md)
- Control-command / MQTT formal check: [`command-safety`](../command-safety/SKILL.md)
