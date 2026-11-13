# Antigravity tool mapping for pstack (HVAC_web)

This repository runs dual-harness pure-Markdown skills under `.agents/skills/` (Antigravity) and `.claude/skills/` (Claude Code), kept byte-for-byte identical via `npm run repo:check`. Resolve Claude Code idioms named in pstack skills here.

## Tool actions

| pstack / Claude action | Antigravity equivalent |
|------------------------|------------------------|
| Read a file | `read_file` (use `StartLine` / `EndLine` slices per `AGENTS.md`) |
| Create / edit a file | `write_file` / `edit_file` (sync `.agents/skills/` and `.claude/skills/` when editing skills) |
| Run a shell command | `run_command` and `manage_task` (run builds, Go, Playwright, and `gh` in WSL `/mnt/e/Code/HVAC_web`; run `git push` on Windows) |
| Search code & call graph | `codegraph_explore` first, then single `git grep -n` / `git ls-files` commands |
| Fetch web / docs | `search_web` or `curl` via `run_command` |
| Invoke a skill (`Skill`, `/name`) | `read_file` on its `<skills>` path (`poteto-mode/SKILL.md` is kept under 10 KB to avoid truncation) |
| Dispatch subagents (`Agent` / `Task`) | `invoke_subagent` with `TypeName`, `Role`, `Prompt`, `Model`, and `Workspace` (multiple calls in one turn run in parallel; end turn to await) |
| Steer or stop subagents | `send_message` (`Recipient: conversationId`) and `manage_subagents` (`list`, `kill`, `kill_all`) |
| Track tasks (`TaskCreate` / `TodoWrite`) | Keep an untracked checklist in `.scratch/todo.md` or `<appDataDir>/brain/<conversation-id>/scratch/todo.md` (never `.audit/`) |
| Ask user (`AskUserQuestion`) | `ask_question` (`questions: [{ question, options, is_multi_select }]`) |
| Recurring/delayed check (`loop`) | `schedule` (`DurationSeconds` or `CronExpression`) |
| MCP tool (`mcp__<server>__<tool>`) | `call_mcp_tool` after reading `<appDataDir>/mcp/<serverName>/<toolName>.json` |
| Session transcript lookup | `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` |

## Subagent policy

- **Writable / shell / MCP work**: Use `TypeName: "self"`. Pass `Workspace: "share"` (or create an isolated git worktree) whenever multiple workers write code or run builds concurrently.
- **Read-only inspection / review**: Use `TypeName: "research"` when no shell command, file edit, or MCP call is required. When a reviewer needs `run_command` (e.g., `interrogate` running repro scripts or `comment-sicko` running `git diff`), use `TypeName: "self"` with explicit instructions not to modify application code outside scope.
- **Model selection**: Default to `Model: "inherit"` unless the user explicitly asks for another model (`flash`, `flash_lite`, `pro`). Do not look for external `pstack-models.md` or `/setup-pstack`.
- **Brief hygiene**: Pass `AGENTS.md` and required `SKILL.md` paths (not full file dumps). Always end subagents holding shared/branched workspaces via `manage_subagents` before committing or merging.

## Repository skill equivalents

| Upstream reference | Repository equivalent |
|--------------------|-----------------------|
| App UI driver / `verify` | Project [`../../verify/SKILL.md`](../../verify/SKILL.md) (Playwright 1440px against port `5190`/`5194` or `https://localhost:8443`, evidence in `.scratch/verify/`) |
| Formal invariant driver | Project [`../../command-safety/SKILL.md`](../../command-safety/SKILL.md) (`npm run command:formal`) |
| `comment-sicko` agent (`no-comments`) | Read [`../../no-comments/references/comment-sicko-prompt.md`](../../no-comments/references/comment-sicko-prompt.md) and pass it to `invoke_subagent` |
| Skill authoring guidance | Built-in `agy-customizations` skill, keeping `AGENTS.md` + `npm run repo:check` mirror & LF rules |
| Governance & static gates | `scripts/domain-task-matrix.ts`, `npm run check`, `npm run repo:check`, `npm run design:check` |

## Instructions & skill sync rule

- Workspace instructions live in `AGENTS.md` (and `CLAUDE.md`).
- Whenever creating or updating any skill under `.agents/skills/`, mirror the exact bytes (LF line endings, zero JS/MJS scripts inside `.agents/`) to `.claude/skills/`.
