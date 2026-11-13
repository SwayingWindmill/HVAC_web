---
name: arena
description: "Spawn N parallel candidates at the same task, pick a base, graft the strongest parts of the losers into it. Use for /arena, 'arena this', 'throw it in the arena', or when one attempt at a non-trivial artifact would lock in the wrong shape."
---

# Arena

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before following this skill. On Claude Code, spawn parallel `Agent` calls.

Open a checklist with one entry per phase (Frame, Fan out, Cross-judge, Pick, Graft, Verify) before launching anything.

## Phase A: Frame

Every candidate receives the identical prompt, so the prompt is the contract.

1. State the exact artifact each candidate must produce.
2. Derive the rubric: 3-6 concrete gradeable criteria for success on *this* task (held for Phase D picking; candidates only see the task).
3. Choose runner count and subagents via `invoke_subagent` (`TypeName: "self"`, `Model: "inherit"` unless the user requests diverse models such as `pro` / `flash`, plus `Workspace: "share"` or isolated scratch/worktree outputs). Spawn 2-4 runners depending on how broad the design space is.
4. Give each candidate an exclusive output path or git worktree (e.g., `.scratch/arena-<slug>/candidate-<n>/` or `Workspace: "share"`), per **Separate Before Serializing Shared State** in `poteto-mode`.

## Phase B: Fan out

Spawn all N candidates in parallel in one message (`invoke_subagent`), giving each the identical task, pointers to grounding docs (`AGENTS.md`, `GLOSSARY.md`, `docs/specs/`), its exclusive output path, and instructions to include a brief rationale noting alternatives considered and rejected.

If a candidate fails to produce output, continue with `N - 1` and record the dropout.

## Phase C: Cross-judge

After Phase B candidates finish, spawn one independent read-only judge subagent (`TypeName: "research"`, or `"self"` read-only if checking git/CLI output; prefer a different model if the user requested multi-model diversity) to score every candidate path against the Phase A rubric and recommend a base with rationale. Never spawn the judge while candidates are still editing. If no independent judge can run, note that explicitly and pick manually in Phase D.

## Phase D: Pick a base

Read every candidate end to end. Score criterion by criterion against the rubric and compare with the cross-judge's verdict. Agreement builds confidence; disagreement highlights ambiguity or bias—read both rationales closely before deciding.

Pick the base a future maintainer can extend most cleanly without breaking invariants. On ties, favor simpler boundaries and fewer moving pieces (**Laziness Protocol**).

## Phase E: Graft

Inspect each non-winning candidate once more for specific strengths worth porting into the chosen base (usually 1-2 targeted ideas per candidate, not frankenstein concatenation). Fold grafts in coherently (**Redesign from First Principles**).

If all candidates converged on the same shape, ship the consensus shape directly with no grafting. If candidates diverged wildly, Phase A was underspecified—reframe the task and re-run instead of averaging incompatible designs.

## Phase F: Verify

Verify the synthesized artifact against real execution (**Prove It Works**: `npm run check`, unit tests, `verify`, or `command-safety` as applicable), then clean up temporary arena branches/worktrees (`manage_subagents`).

## Outputs

Return the synthesized artifact plus a brief synthesis note citing: the chosen base and why, the cross-judge verdict (or why skipped), each graft and its source candidate, rejected approaches, any convergence/dropouts, and real verification output.
