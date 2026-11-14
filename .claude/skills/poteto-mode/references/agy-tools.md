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
| Invoke a skill (`Skill`, `/name`) | `read_file` on its `<skills>` path (`poteto-mode/SKILL.md` is kept well under `24,000` bytes to avoid loader truncation) |
| Dispatch subagents (`Agent` / `Task`) | `invoke_subagent` with `TypeName`, `Role`, `Prompt`, `Model`, and `Workspace` (`"inherit"`, `"share"` isolated worktree, or `"branch"` clone; multiple calls run in parallel; end turn to await). Note `"share"` does not inherit untracked main-repo `.scratch/` files |
| Steer or stop subagents | `send_message` (`Recipient: conversationId`) and `manage_subagents` (`Action`: `"list"`, `"kill"` with required `ConversationIds: [...]`, `"kill_all"`) |
| Track tasks (`TaskCreate` / `TodoWrite`) | Keep an untracked checklist in `.scratch/todo.md` or `<appDataDir>/brain/<conversation-id>/scratch/todo.md` (never `.audit/`) |
| Ask user (`AskUserQuestion`) | `ask_question` (`questions: [{ question, options, is_multi_select }]`) |
| Recurring/delayed check (`loop`) | `schedule` (`Prompt` plus `DurationSeconds` or `CronExpression`; returns immediately so end turn to receive wakeups; never shell `sleep`) |
| MCP tool (`mcp__<server>__<tool>`) | `mcp_<serverName>_<toolName>` or `call_mcp_tool` after reading `<appDataDir>/mcp/<serverName>/<toolName>.json` (`~/.gemini/config/mcp_config.json`) |
| Session transcript lookup | `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` (`transcript_full.jsonl` when `truncated_fields` is present) |

## Subagent policy

- **Writable / shell / network work**: Use `TypeName: "self"`. Pass `Workspace: "share"` (or create an isolated git worktree) whenever multiple workers edit code or run builds concurrently.
- **Read-only inspection / review**: Use `TypeName: "research"` (has `read_file`, `codegraph_explore`, read-only `run_command`, and MCP read tools; cannot edit/write files or access external network). When a reviewer needs `write_file`, `.scratch/` temp files, or network (`gh`), use `TypeName: "self"` with explicit instructions not to modify application code outside scope.
- **Windows + WSL `Workspace: "share"` rule**: On Windows Antigravity, `Workspace: "share"` and Windows `git worktree add` create worktrees whose `.git` pointers store Windows paths (`E:/...`). Inside a Windows worktree, WSL `git` fails (`fatal: not a git repository`) and WSL `gh` requires `-R <owner>/<repo>` (or run `gh` from `/mnt/e/Code/HVAC_web`); run `git` commands inside that worktree via Windows `run_command`, keep builds/tests in WSL, and **never** run `git worktree prune` in WSL while Windows worktrees exist.
- **Model selection**: Default to `Model: "inherit"` unless the user explicitly asks for another model (`flash`, `flash_lite`, `pro`) or explicitly configures `~/.gemini/config/pstack-models.md` (included from `~/.gemini/config/GEMINI.md`) via [`../../setup-pstack/SKILL.md`](../../setup-pstack/SKILL.md). Keep `"inherit"` whenever no override sheet exists.
- **Brief hygiene**: Pass `AGENTS.md` and required `SKILL.md` paths (not full file dumps). Always end subagents holding shared/branched workspaces via `manage_subagents` before committing or merging.

## Repository skill equivalents

| Upstream reference | Repository equivalent |
|--------------------|-----------------------|
| App UI driver / `verify` | Project [`../../verify/SKILL.md`](../../verify/SKILL.md) (Playwright 1440px against port `5190`/`5194` or `https://localhost:8443`, evidence in `.scratch/verify/`) |
| Formal invariant driver | Project [`../../command-safety/SKILL.md`](../../command-safety/SKILL.md) (`npm run command:formal`) |
| `comment-sicko` agent (`no-comments`) | Read [`../../no-comments/references/comment-sicko-prompt.md`](../../no-comments/references/comment-sicko-prompt.md) and pass it to `invoke_subagent` |
| Skill authoring guidance | Built-in `agy-customizations` skill, keeping `AGENTS.md` + `npm run repo:check` mirror & LF rules |
| Governance & static gates | `scripts/domain-task-matrix.ts`, `npm run check`, `npm run repo:check`, `npm run design:check` |
| Skill helper scripts (WSL Node 24+ / Bash) | `scripts/{check-plan.ts,resume.ts,worktree-audit.ts}`, `show-me-your-work/scripts/log.sh`, `statechart/scripts/statechart` |

## Instructions & skill sync rule

- Workspace instructions live in `AGENTS.md` (and `CLAUDE.md`).
- Whenever creating or updating any skill under `.agents/skills/`, mirror the exact bytes (LF line endings, zero JS/MJS scripts inside `.agents/`) to `.claude/skills/`.
