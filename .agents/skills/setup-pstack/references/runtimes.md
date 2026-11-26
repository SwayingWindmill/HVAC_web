# Other runtimes

The active role names (`feature, refactoring`, `bug-fix`, `perf-issue`, `hillclimb`, `judgment and prose`, `arena runners`, `arena cross-judge pool`, `swarm workers`, `interrogate reviewers`, plus optional `thermos reviewers` and `no-comments reviewer`) stay the same across runtimes. What differs is the sheet path, how the runtime loads it, and which model slugs and options exist. Detect models from the runtime's own tool schema and never write an unverified model slug.

| Runtime | Sheet path | How it loads | Available model names | Effort parameter |
| --- | --- | --- | --- | --- |
| Antigravity (`agy`) | `~/.gemini/config/pstack-models.md` | Include via `@[pstack-models](~/.gemini/config/pstack-models.md)` in `~/.gemini/config/GEMINI.md`, or read explicitly before dispatching role subagents | `invoke_subagent`'s `Model` enum (`inherit`, `flash_lite`, `flash`, `pro`) — default `inherit` when sheet is absent | None (`invoke_subagent` has no effort parameter; omit `# budget:` and effort tokens) |
| Claude Code | `~/.claude/rules/pstack-models.md`, or `.claude/rules/pstack-models.md` at repo root | Auto-loaded by Claude Code into every new session with **no** YAML frontmatter and no `@` include needed | `Agent` tool family slugs (`opus`, `sonnet`, `haiku`, `fable`, or `inherit-parent`/`auto`) written as `<model> <effort>` | `effort` (`low`, `medium`, `high`, `xhigh`) mapped via `large (xhigh)`, `medium (high)`, `small (medium)` with `# defaults: 3` |

Note on `HVAC_web`: this repository runs skills directly from `.agents/skills/` (mirrored 1:1 to `.claude/skills/`) without installing an external `SessionStart` routing hook, so `session hook` lines in override sheets are inert here. Migrate any legacy `~/.claude/pstack-models.md` or `.claude/pstack-models.md` file to `~/.claude/rules/pstack-models.md` (or `.claude/rules/pstack-models.md`) on re-run.
