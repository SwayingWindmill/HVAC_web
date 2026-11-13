---
name: thermo-nuclear-code-quality-review
description: Run an extremely strict maintainability review for abstraction quality, giant files, and spaghetti-condition growth. Use for a thermo-nuclear code quality review, thermonuclear review, deep code quality audit, or especially harsh maintainability review.
---

# Thermo-Nuclear Code Quality Review

Use this skill for an unusually strict review focused on implementation quality, maintainability, abstraction quality, and codebase health.

Above all, push reviewers to be **ambitious** about code structure. Do not stop at local cleanup opportunities. Actively look for "code judo" moves: restructurings that preserve behavior while making the implementation dramatically simpler, smaller, more direct, and clearer.

## Core Prompt

Start from this baseline:

> Perform a deep code quality audit of the current branch's changes.
> Rethink how to structure / implement the changes to meaningfully improve code quality without impacting behavior.
> Improve abstractions, modularity, reduce spaghetti conditionals, and improve clarity.
> Be ambitious: if there is a clear path to simplifying the implementation by cleanly restructuring surrounding code in scope, propose or take it.
> Measure twice, cut once.

## Non-Negotiable Additional Standards

Apply the baseline prompt above plus these review rules:

0. **Be ambitious about structural simplification.**
   - Look for reframings where whole branches, helper flags, modes, or wrapper layers disappear entirely.
   - Prefer solutions that make the architecture feel inevitable in hindsight and delete complexity instead of rearranging it.
1. **Do not let a PR push a file from under 1k lines to over 1k lines without a very strong reason.**
   - Prefer extracting cohesive helpers, subcomponents, or modules rather than sprawling past 1,000 lines.
2. **Do not allow random spaghetti growth in existing code.**
   - Treat ad-hoc `if` branches or scattered special cases inserted into unrelated paths as design bugs, not style nits.
   - Push variant logic into an explicit model, state machine, policy table, or dedicated module.
3. **Bias toward cleaning the design, not rubber-stamping "it works".**
   - Strongly prefer changes that remove moving parts altogether over changes that spread complexity across files.
4. **Prefer direct, boring, maintainable code over clever or magical abstractions.**
   - Flag thin identity wrappers, pass-through helpers, and generic indirection that hide obvious data shapes.
5. **Enforce clean type and boundary contracts.**
   - Flag unnecessary optionality, `unknown`/`any` leaks, unverified `as` casts, or silent fallback branches masking unclear invariants (`typescript-best-practices` / `npm run design:check`).
6. **Keep logic in the canonical layer and reuse existing helpers.**
   - Reject feature logic leaking into shared primitives and bespoke helpers duplicating existing utilities.
7. **Flag avoidable sequential orchestration and half-applied state.**
   - Parallelize truly independent work and keep related state mutations atomic.

## Primary Review Questions

- Is there a simpler data shape or model that deletes branches altogether?
- Did the diff add branching complexity, optional bags, or special cases where an explicit union/table belongs?
- Does this enlarge a file past healthy bounds (~1,000 lines) without clear modular structure?
- Is this logic living in its owning layer/module (`contracts/`, `modules/`, `apps/`) without leaking internals?
- Are existing canonical repo utilities reused cleanly?

## What to Flag Aggressively

- Structural complexity preserved when a cleaner reframing deletes it.
- Ad-hoc booleans, nullable modes, or conditional branches bolted onto shared flows.
- Thin wrappers adding indirection without simplifying callers.
- Cast-heavy contracts (`as`), suppressed lints, or silent fallbacks hiding boundary failures.
- Near-duplicate logic instead of parameterized helpers or data tables.

## Report Format

For broad or multi-subsystem reviews, layer outputs under `.scratch/<task>-thermo/` (never commit scratch reports):

- One concise summary report (~200 lines max): overall verdict, narrative prose findings (file:line + evidence + problem + concrete remedy), prioritized remediation order, and pointers to subsystem files (`01_<subsystem>.md`, `02_<subsystem>.md`, ...).
- Detail files carry full command outputs, verification checks, and concrete restructuring sketches.
- When fanning out parallel reviewers via sibling `../swarm/SKILL.md`, keep per-subsystem markdown files on disk and synthesize judgment into the summary rather than pasting raw dumps.

## Approval Bar

Treat these as presumptive blockers unless strongly justified:

- Pushing a file over 1,000 lines when modular decomposition is practical.
- Adding ad-hoc branching or scattering feature checks across shared code.
- Introducing unnecessary wrappers, cast-heavy contracts, or weak optionality.
- Duplicating existing repo helpers or writing in the wrong ownership layer.
- Leaving incidental complexity intact when an obvious code-judo simplification deletes it.
