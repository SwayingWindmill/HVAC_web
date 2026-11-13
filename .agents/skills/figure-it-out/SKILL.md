---
name: figure-it-out
description: "Design an auditable playbook when no narrower one fits: a large migration, an ambitious multi-part change, or work a human reviews after stepping away. Scales rigor to the task, runs a hypothesis loop, and logs decisions. Use for /figure-it-out or 'figure it out'."
---

# Figure it out

When the task matches no narrower playbook, design one. The deliverable before any code is the workflow itself: a sequence of phases that scales rigor to the task, runs the scientific method, and leaves a decision trail a human can audit after stepping away.

## Start

Read [`../poteto-mode/SKILL.md`](../poteto-mode/SKILL.md) (especially **Principles** and **Subagents**) and open a checklist (`.scratch/todo.md` if no built-in todo tool is active, never `.audit/`) with the phases below.

## Phase A: Frame

Ground first, then commit. Don't start the run until you can state:

- The definition of done as a falsifiable predicate (**Prove It Works** in `poteto-mode`).
- Scope, quantified: rough units and effort, plus blockers surfacing during grounding.
- The rigor level, biased high. One-way doors and high blast radius get more; reversible low-stakes steps get less. Rigor is gates and artifacts, not "try harder".

Present framing and tradeoffs before committing to a long run. Reversible work proceeds without blocking (**Never Block on the Human**), while a multi-hour run or irreversible action earns a checkpoint.

## Phase B: Design the workflow

Decompose into atomic, independently-landable units. Sequence riskiest-unknown-first. Build verification scaffolding before feature edits (**Foundational Thinking**).

- Build the verification harness before code changes, with pre-change baseline captured as `before -> after`.
- For one-way-door architecture choices, run `architect` (and `arena` if competing shapes need bakeoffs). Skip for straightforward mechanical work (**Laziness Protocol**).
- Decide what fans out across clean seams (`swarm`), giving each writable worker an isolated `Workspace: "share"` or separate git worktree (**Separate Before Serializing Shared State**). Follow [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) for subagent dispatch.
- Write down the phase list clearly and put its steps into your checklist before Phase C.

## Phase C: Run the loop

Each unit is an experiment. State the hypothesis, make the smallest change, measure against the predicate on the real artifact (`verify`, `npm run command:formal`, test suites), keep if improved, revert if not (**Sequence Work into Verifiable Units**).

- Verify by inspecting the actual artifact or test output, never a delegate self-report. When something passes too easily, suspect the observation path first.
- Pair delegated work with an independent check. If a worker games a gate, reset and harden the contract; if the gate itself is wrong, fix it in its own change.
- Report verdicts strictly as `VERIFIED`, `NOT VERIFIED`, or `INCONCLUSIVE`. Never hide an inconclusive or failing result.

## Phase D: Keep the audit trail

Maintain an explicit decision trail covering hypotheses, commands run, verdicts, and pivots. Store scratch logs in `.scratch/` for ephemeral runs, or summarize in PR/doc notes (keeping `docs/HANDOFF.md` under 28 KiB and never writing `.audit/`).

## Phase E: Verify and hand back

Check the whole against the Phase A predicate on the real system. Encode any recurring failure pattern into a type constraint, script, or CI check via `correct` (**Encode Lessons in Structure**).

**Reply:** the designed playbook, rigor level and why, decision-trail location, what is verified against the predicate, and what remains open.
