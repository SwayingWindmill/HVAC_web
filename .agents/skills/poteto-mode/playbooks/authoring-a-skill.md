### Authoring or modifying a skill

**You own the skill's voice.**

1. Read Antigravity's built-in `agy-customizations` skill (authoring guidance, discovery paths, and conventions) and `AGENTS.md` repo skill rules.
2. Validate the skill: frontmatter has `name` and `description`, referenced files exist, cross-skill links resolve, and there are zero JS/MJS files or `.audit/` paths.
3. Add or run checks if structural; skip if subjective.
4. Mirror `.agents/skills/<name>/` byte-for-byte to `.claude/skills/<name>/` (LF line endings) and run `npm run repo:check`, then [`opening-a-pr.md`](opening-a-pr.md).

When in doubt, delete. Keep only prose that changes a decision. Tell the agent to do the thing and skip filler. Point at structural sources (types, READMEs, static gates, config) per [`correct`](../../correct/SKILL.md), and delegate to sibling skills by relative path rather than restating them.

**Reply:** summary of the skill, key design decisions, validation notes.
