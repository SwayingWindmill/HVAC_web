---
name: maintain-formal-verification-skill
description: "Bring a project's formal verification back in line with the code after the code changes: find the commits that touched verified code, update the model, harness, or properties to describe the code as it is now, rerun, and say whether each failure is drift in the verification or a bug in the code. Use for /maintain-formal-verification-skill, \"is the model still true to the code\", or after merges and refactors that touched verified code without rechecking it."
---

# Maintain a formal verification skill

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before following this skill. Remember any updates under `.agents/skills/<area>/` MUST be mirrored byte-for-byte to `.claude/skills/<area>/`.

A formal check only proves properties of what its model describes. When product code changes without updating the model or harness, a passing check can mask real bugs. This skill audits formal verification skills (such as `.agents/skills/command-safety/` running `npm run command:formal` over `modules/command/pkg/commandformal`) against commits made since the last recorded sync commit.

## Outcomes

Assign each check one outcome cleanly:

- **in sync**: no change since the recorded sync commit alters modeled behavior, and the check passes.
- **updated**: code changed structurally, the verification model/harness was updated from source to match, and the updated check passes (and catches injected breaks).
- **bug**: verification accurately models current code and a property fails—this is a real product bug, not verification drift.
- **blocked**: check could not be reconciled or executed; state exact blocker.

(A check can be both `updated` and `bug` if resyncing the stale model exposes a real property violation.)

## Edit scope

Edit ONLY verification artifacts: formal models, conformance harnesses, property checks, and the formal verification skill files (`.agents/skills/<area>/` mirrored to `.claude/skills/<area>/`). Never edit product code inside this maintenance pass unless explicitly asked, and NEVER weaken a property to silence a failure.

## Pass

1. **Find checks & sync commit.** Read target formal skill(s) (e.g. `command-safety/SKILL.md`), excluding runtime UI driver `verify/`. Extract properties, covered source/SQL files, bounds/assumptions, execution command (`npm run command:formal`), and last confirmed sync commit (default to the last commit touching the skill/harness if unrecorded, stating so explicitly).
2. **Baseline run.** Run the check unchanged (`npm run command:formal` in WSL) and record the exact output before any edits.
3. **Audit covered code commits.** Diff all covered product files/functions/SQL guards since the sync commit (including renamed/moved functions and newly added writers to guarded state). Classify each diff hunk:
   - **No effect**: formatting, comments, unrelated paths not part of modeled transitions/invariants.
   - **Verification drift**: transition atomicity split/merged, new guard/branch added, state domain changed, new caller reaching guarded state.
   - **Intentional promise change**: product guarantee intentionally modified per ADR/spec/tests/commit history (cite exact evidence).
   - **Suspected broken promise**: product behavior changed without documented promise change -> treat failures as bug candidates.
4. **Resync from code, not to force a pass.** Update models/harnesses/transition alignments directly from current source before rerunning; update properties only for documented intentional promise changes (recording old vs new wording and evidence). Keep bounds sound.
5. **Rerun & mutation-check.**
   - **Pass**: temporarily introduce a deliberate scratch break into the real covered code (remove/reorder a guard), confirm the formal check catches it, then revert the scratch break (`git checkout -- ...` clean!).
   - **Fail**: step through the counterexample trace in code terms. Fix modeling errors cleanly; if every step occurs in real code, report a real bug naming the introducing commit, concrete trace, and operational harm (include a scratch repro snippet if helpful, never committed to product code).
6. **Update skill & mirror.** Update covered paths, bounds, assumptions, and the sync commit (only when NOT `blocked`), run the formal skill's steps once end-to-end, mirror `.agents/skills/` <-> `.claude/skills/`, and verify `npm run repo:check`.

## Report

State per check: outcome & commits inspected, what changed in verification and why, rerun result & mutation-break confirmation (or full counterexample + harm + introducing commit for bugs), and uncovered gaps/new code needing future modeling. Keep verification sync PRs separate from product fixes.
