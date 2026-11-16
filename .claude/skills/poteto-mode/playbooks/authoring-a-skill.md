### Authoring or modifying a skill

**You own the skill's voice.**

1. Read Antigravity's built-in `agy-customizations` skill (authoring guidance, discovery paths, and conventions) and `AGENTS.md` repo skill rules.
2. Validate the skill: frontmatter has `name` and `description`, referenced files exist, cross-skill links resolve, and there are zero JS/MJS/CJS files or `.audit/` paths.
3. Add or run checks if structural; skip if subjective.
4. Mirror `.agents/skills/<name>/` byte-for-byte to `.claude/skills/<name>/` (LF line endings) and run `npm run repo:check`, then [`opening-a-pr.md`](opening-a-pr.md).

## Writing for agents

- **Two loads and context pointers**: always-loaded `description` fields and `AGENTS.md` pointers spend **context load** on every turn, while user-invoked (`disable-model-invocation: true`) skills spend **cognitive load** (human remembers to invoke them). Front-load trigger words in model-invoked descriptions, list distinct trigger branches once without synonyms, and hide branch-specific reference behind relative pointers (**progressive disclosure**).
- **Invocation cut**: set `disable-model-invocation: true` for manual or disruptive workflows (interviews, architecture reports, questionnaires) so they do not auto-fire over `poteto-mode`. Use model-invocation only when autonomous or cross-skill reach is required. When too many manual skills pile up, route them from [`poteto-help`](../../poteto-help/SKILL.md).
- **Steps and premature completion**: end each step with a checkable, exhaustive completion criterion (`"every modified caller checked"`, not `"understanding reached"`). When visible later steps tempt an agent to rush the current step, split across a real context boundary (subagent dispatch or handoff).
- **Leading words vs negation trap**: anchor behavior on positive concrete targets (`"write one-line comments explaining why"`) and high-signal pretrained terms (`tight`, `red`, `frontier`) instead of standalone prohibitions (`"don't X"`), which activate the forbidden concept in context.
- **Environment-as-cache and no-op pruning**: keep each rule in a single source of truth. Do not copy `package.json` scripts, directory trees, or CLI `--help` output into skill prose; cache only unwritten conventions, gotchas, and rationale. Delete any sentence that does not change model behavior relative to its default.

When in doubt, delete. Keep only prose that changes a decision. Tell the agent to do the thing and skip filler. Point at structural sources (types, READMEs, static gates, config) per [`correct`](../../correct/SKILL.md), and delegate to sibling skills by relative path rather than restating them.

**Reply:** summary of the skill, key design decisions, validation notes.
