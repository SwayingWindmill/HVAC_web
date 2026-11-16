---
name: grilling
description: "Interview the user relentlessly round-by-round across a design tree until open branches are settled. Use ONLY when the user explicitly asks to be grilled ('grill me', '/grilling', 'stress-test this plan/idea'), or when called by improve-codebase-architecture. Do NOT auto-trigger on standard poteto-mode bug-fix, feature, or refactoring tasks."
---

# Grilling

Interview the user relentlessly until you reach a shared understanding of an open plan, architecture choice, or idea. Map the space as a **design tree**: every decision branches into the decisions that hang off it.

Note for `poteto-mode` coexistence: normal `feature.md`, `bug-fix.md`, and `refactoring.md` runs follow `Never Block on the Human`. Use `grilling` only when the user explicitly requests a grilling / stress-test session or when [`../improve-codebase-architecture/SKILL.md`](../improve-codebase-architecture/SKILL.md) enters its grilling loop.

## Round-by-round frontier loop

Work the tree in **rounds**. The **frontier** is every decision whose prerequisites are already settled: questions you can ask _now_ without guessing at answers you have not heard yet. Ask the whole frontier in one numbered round, attach your recommended answer to each question, and wait for the user's answers before computing the next round.

Format each round cleanly:

```markdown
❓ **Q1** - **<question title>**: <question body, including options if applicable>

➡️ <your recommended answer>

---

❓ **Q2** - **<question title>**: <question body>

➡️ <your recommended answer>
```

Word each question so "yes" accepts your recommended answer. When a round consists purely of compact closed-set choices, you may pair or deliver them via `ask_question`; keep open-ended tradeoffs in the numbered frontier list so the user can reply by number.

## Facts vs decisions

Finding **facts** is your job, never the user's. When a frontier question depends on code, contracts, configs, or docs in the workspace, dispatch a read-only background subagent via `invoke_subagent` (`TypeName: "research"`, `Model` per [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md)) to verify it instead of asking the user. Do not block unrelated frontier questions on a running lookup: ask the settled branches now and hold only the questions downstream of that subagent until it reports back.

## Repository domain alignment

Inside `HVAC_web`, read `GLOSSARY.md`, `docs/agents/domain.md`, and relevant files in `docs/adr/` as decisions surface:

- Challenge ambiguous terms against `GLOSSARY.md` and note missing terms cleanly.
- If a settled decision satisfies all three ADR conditions in `docs/agents/domain.md` (hard to reverse, surprising without context, real tradeoff among alternatives), offer to write `docs/adr/NNNN-slug.md`.

The grilling session ends when the frontier is empty and nothing remains silently assumed. Do not start editing implementation code until the user confirms shared understanding or asks to proceed.
