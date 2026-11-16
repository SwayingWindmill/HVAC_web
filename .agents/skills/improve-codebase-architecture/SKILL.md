---
name: improve-codebase-architecture
description: Scan active codebase hot spots for deepening opportunities (turning shallow modules into deep ones), present them as a visual Before/After HTML report, then grill through the picked candidate.
disable-model-invocation: true
---

# Improve Codebase Architecture

Surface architectural friction and propose **deepening opportunities**: refactors that turn shallow modules into deep ones to improve testability and AI navigability.

Read [`references/deep-module-lens.md`](references/deep-module-lens.md) before scanning for the exact architecture vocabulary (**module**, **interface**, **implementation**, **depth**, **seam**, **adapter**, **leverage**, **locality**), the deletion test, dependency categories, and replace-don't-layer testing strategy. Use these terms consistently rather than drifting into vague synonyms like "component", "service", or "wrapper".

## Process

### 1. Explore hot spots (YAGNI)

Deepening a module pays off where future changes actually land, so focus on active areas first:

- If the user named a module, subsystem, or pain point, scope to that target.
- Otherwise, inspect recent commit history (`git log --oneline -n 40`) to locate active hot spots rather than tidying dormant code.

Read `GLOSSARY.md`, `docs/agents/domain.md`, and relevant `docs/adr/*.md` files in the area first (use `codegraph_explore` to map callers/dependencies). Then dispatch a fresh read-only subagent via `invoke_subagent` (`TypeName: "research"`, `Model` per [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md)) to inspect the target paths and flag where:

- Understanding one concept requires bouncing across many small pass-through modules.
- Interfaces are nearly as complex as their implementations (**shallow**).
- Pure helpers were extracted solely for testing while bugs hide in how callers wire them (poor **locality**).
- Modules leak internal wire/schema representations across seams.
- Core behavior is hard to test through its public **interface**.

Apply the **deletion test** to suspected shallow wrappers: would deleting it concentrate complexity across callers, or simply simplify the flow?

### 2. Present candidates as a visual HTML report

Write a self-contained HTML report to `.scratch/architecture-review-<timestamp>.html` (kept untracked under `.scratch/`, never inside `apps/` or tracked docs) using [`HTML-REPORT.md`](HTML-REPORT.md) for layout, Tailwind/Mermaid CDN setup, and custom SVG diagram patterns (**Mass diagram**, **Cross-section**, **Call-graph collapse**).

For each candidate card, include:

- **Files**: real repo paths involved
- **Problem**: one crisp sentence describing the friction
- **Solution**: plain description of the deepened shape
- **Wins**: short bullets using `deep-module-lens.md` terms (**locality**, **leverage**, reduced interface surface)
- **Before / After diagram**: side-by-side visual comparison
- **Recommendation strength**: `Strong`, `Worth exploring`, or `Speculative`
- **ADR conflict callout**: only if the candidate genuinely warrants reopening an existing ADR in `docs/adr/`

End with a **Top recommendation** section. Do **not** propose detailed signatures yet; return the clickable `file:///` report link (and optionally open it locally if requested) and ask: "Which of these would you like to explore?"

### 3. Grill and hand off to `architect` / `refactoring`

Once the user picks a candidate, read [`../grilling/SKILL.md`](../grilling/SKILL.md) to walk the decision tree (constraints, dependency category, seam placement, tests to replace).

- **Domain updates**: propose updates to `GLOSSARY.md` if a new domain concept crystallizes, or offer `docs/adr/NNNN-slug.md` per `docs/agents/domain.md` if the user rejects a candidate for a durable architectural reason that future reviews should not re-suggest.
- **Alternative interfaces & execution**: when ready to design competing interfaces or implement the refactor, hand off to [`../architect/SKILL.md`](../architect/SKILL.md) (`Design it twice` + `references/design-red-flags.md`) and [`../poteto-mode/playbooks/refactoring.md`](../poteto-mode/playbooks/refactoring.md) so the change ships through `HVAC_web`'s normal verification and PR gates.
