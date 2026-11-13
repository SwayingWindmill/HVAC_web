---
name: interrogate
description: "Use for \"interrogate\", \"adversarial review\", \"multi-model review\", \"challenge this\", \"stress test this code\", \"find blind spots\", or \"tear this apart\". Adversaries try to break a change or design, and only findings proven by a run survive."
---

# Interrogate

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before dispatching adversaries. On Claude Code, spawn parallel `Agent` calls.

Adversaries try to break a change, spec interpretation, or design. A finding survives only when a real run proves it. Report proven findings cleanly; do not smuggle unrequested fixes during the interrogate pass itself.

## Frame

Scope is whatever the user named; otherwise it is the current branch diff from `merge-base` with `main` through the working tree. First restate the intent using [`../in-your-own-words/SKILL.md`](../in-your-own-words/SKILL.md) against `AGENTS.md`, `GLOSSARY.md`, relevant `docs/specs/`, and `docs/adr/`. Ask the user only if intent remains ambiguous.

## Fan out

Launch 2-3 parallel adversarial subagents via `invoke_subagent` (`TypeName: "self"`, `Workspace: "share"` so each can run repros safely in isolation; `Model: "inherit"` by default unless the user specifies diverse models). Give each adversary the target scope, the restated intent, relevant repo contracts/spec paths, and this brief:

> Break this change or design against the stated intent and repository contracts (`AGENTS.md`, specs, ownership boundaries, formal invariants). Prove every finding with a real command execution (a failing test, a repro script in `.scratch/` or your isolated workspace, `go test`, `npm run` check, or observed command failure). For a design sketch, write a minimal executable probe or trace real callers against existing code. Do not report style opinions, speculative "what-ifs", or anything you did not actually execute and observe failing. Report each finding with a title, exact command run, printed output, and what the intent/spec requires instead. "No verified findings" is a valid outcome.

Clean up or ignore adversary worktrees until verification completes (`manage_subagents`).

## Verify

Independently rerun the command behind each reported finding on the actual branch working tree (or confirm the isolated repro genuinely reproduces against current code). Keep a finding only if the observed failure is real and violates intent, safety, or repo contracts; drop speculative or invalid claims and deduplicate shared root causes. Clean up child subagents (`manage_subagents`) and temporary repro files afterward.

## Output

- **Intent**: the `in-your-own-words` restatement.
- **Findings**: each verified finding with exact command, observed failing output, which intent/contract it breaks, and which adversary caught it (or "No verified findings").
- **Dropped**: count of unverified/duplicate claims dropped per adversary and why.
