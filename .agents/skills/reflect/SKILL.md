---
name: reflect
description: Spawn three parallel review subagents over the active transcript, surface learnings, and route each to a concrete edit on an existing skill. Use when the user says reflect.
disable-model-invocation: true
---

# Reflect

Mine the current conversation for durable learnings, then route them into skill edits.

Invoke when the user says "reflect" or "/reflect". Skip when the conversation is trivial, off-topic, or already covered by an existing skill the parent followed correctly. One-offs are not learnings.

## 1. Locate the active transcript

Before fanning out, find this conversation's own transcript (never glob across unrelated workspaces):

- **Antigravity**: `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` (when `truncated_fields` is present on a step, inspect the corresponding line in `transcript_full.jsonl`).
- **Claude Code**: `~/.claude/projects/<slug>/<uuid>.jsonl` matching this conversation's opening user prompt.

If no transcript path is readable in the current sandbox, pass a tight, factual session digest instead.

## 2. Spawn three reviewers in parallel

Dispatch three parallel `invoke_subagent` calls (see [`agy-tools.md`](../poteto-mode/references/agy-tools.md)). Use `TypeName: "self"` with explicit read-only instructions when reviewers need MCP lookups or shell inspection (`research` suffices if local file reading is enough).

Set `Model` per [`agy-tools.md`](../poteto-mode/references/agy-tools.md): default to `"inherit"`, or read the matching row (`reflect judgment, divergent, synthesizer`, `reflect tooling`) from `pstack-models.md` (`~/.gemini/config/pstack-models.md` or `.claude/pstack-models.md`) when configured via `/setup-pstack`.

| Lens | Role line | Default `Model` | Prompt template |
|---|---|---|---|
| Judgment | `reflect judgment, divergent, synthesizer` | `inherit` (or sheet override) | [`references/judgment-reviewer.md`](references/judgment-reviewer.md) |
| Tooling | `reflect tooling` | `inherit` (or sheet override) | [`references/tooling-reviewer.md`](references/tooling-reviewer.md) |
| Divergent | `reflect judgment, divergent, synthesizer` | `inherit` (or sheet override) | [`references/divergent-reviewer.md`](references/divergent-reviewer.md) |

Pass each template verbatim, substituting the transcript path or digest where marked.

## 3. Synthesize

Launch one `invoke_subagent` call (`TypeName: "self"` with read-only instructions if MCP spot-checks are needed, else `"research"`). Pass [`references/synthesizer.md`](references/synthesizer.md) verbatim with each reviewer's full output inlined where marked. It returns an `Accepted / Rejected / Backlog` list.

## 4. Structural enforcement check

Move any `Accepted` item that a lint rule, script, metadata flag, or static/CI check (`AGENTS.md`, `scripts/domain-task-matrix.ts`, [`correct`](../correct/SKILL.md)) would enforce more reliably than skill prose to `Backlog`.

## 5. Apply

Present the synthesizer's full `Accepted / Rejected / Backlog` output and wait for explicit user approval before applying any `Accepted` edit. Skill changes affect future sessions across the repository; do not auto-apply.

Only the `Accepted` edits wait for approval; track `Backlog` items if the user asks or open issues where appropriate.

Follow each approved row's Routing:

- Trivial existing-skill edit (a one-line bullet, tightened sentence, or stale fact corrected): edit `.agents/skills/<skill>/` directly.
- Substantive existing-skill edit or `new skill: <kebab-name>`: read the built-in `agy-customizations` skill and [`authoring-a-skill.md`](../poteto-mode/playbooks/authoring-a-skill.md) to draft cleanly.
- `tune description: <skill path>`: refine frontmatter `description` triggers per `agy-customizations`.

Whenever modifying any file under `.agents/skills/`, mirror the exact bytes (LF line endings, zero JS files) to `.claude/skills/` and run `npm run repo:check` in WSL before declaring done.

## 6. Summarize for the user

Short list, no preamble:

- Edits applied: `<skill path>`. What changed, one line each.
- New skills created: `<skill path>`. One line each (rare).
- Backlog: `<item title>`. One line each.
- Dropped: one line per rejected finding + reason from the synthesizer.
