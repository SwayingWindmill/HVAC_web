# Other runtimes

The role names stay the same across runtimes. What differs is the sheet path, how the runtime loads it, and which model slugs/options exist. Detect models from the runtime's own tool schema and never write an unverified model slug.

| Runtime | Sheet path | How it loads | Available model names | Effort parameter |
| --- | --- | --- | --- | --- |
| Antigravity (`agy`) | `~/.gemini/config/pstack-models.md` | Paste or reference in `~/.gemini/config/AGENTS.md`, or read before dispatching role subagents | `invoke_subagent`'s `Model` enum (`inherit`, `flash_lite`, `flash`, `pro`) — default `inherit` when sheet is absent | None (`invoke_subagent` has no effort parameter; omit `default effort` and `@<level>`) |
| Claude Code | `~/.claude/pstack-models.md`, or `.claude/pstack-models.md` at repo root | `@~/.claude/pstack-models.md` in `~/.claude/CLAUDE.md` (or `@.claude/pstack-models.md` in root `CLAUDE.md`) | `Agent` tool slugs (`opus`, `sonnet`, `haiku`, or `inherit-parent`/`auto`) | `effort` (`low`, `medium`, `high`, `xhigh`, `max`, or `session`) |

Note on `HVAC_web`: this repository runs skills directly from `.agents/skills/` (mirrored 1:1 to `.claude/skills/`) without installing an external `SessionStart` routing hook, so `session hook` lines in override sheets are inert here.
