---
name: poteto-mode
description: "poteto's agent style for concise, detailed responses, deliberate subagents, unslopped prose, simple code, and verified work. Use for poteto, /poteto-mode, or end-to-end tasks following pstack playbooks."
---

# Poteto mode

Sibling skills live at `../<name>/SKILL.md` from this file; include this file's absolute path and `AGENTS.md` in every coding delegate prompt.

## Platform adaptation

- **Antigravity (`agy`)**: Read [`references/agy-tools.md`](references/agy-tools.md) for tool mappings (`invoke_subagent`, `send_message`, `manage_subagents`, `ask_question`, `schedule`). Instructions file is `AGENTS.md` (and `CLAUDE.md` mirrored rules).
- **Claude Code**: Uses native `Skill`, `Agent` (`run_in_background: true`), `AskUserQuestion`, and `CLAUDE.md`.
- **Checklists**: Use built-in task tools when available; otherwise keep an untracked `.scratch/todo.md` checklist (never `.audit/`, which `npm run repo:check` forbids) with playbook steps verbatim and `skip: <reason>` lines.

## Non-negotiables

- **First response**: State in your own words (`in-your-own-words`) the user's goal, the problem being solved, and the smallest expected change. Don't wait for confirmation. If evidence rules out the ask, show it and stop.
- **Comprehension & history**: Non-trivial subsystem change or "are we sure?" -> `how`. Defense/threshold/regression history -> `why`.
- **Empirical questions before asking**: Before using `ask_question` on "how does X behave?" or "which implementation works?", run code, tests, or a scratch probe under `.scratch/` and let facts decide. Reserve human questions for product/preference calls or irreversible gates.
- **Data shape & design**: Name the data shape and invariant first (`Model the Domain`, `typescript-best-practices`). Code crossing a function boundary or shared contract -> `architect` + `blast-radius` (`bug-fix` checks `focused-fix-exception` first).
- **Parallelism & adversarial check**: Independent slices or coverage checks -> `swarm`. Competing architectures/implementations -> `arena`. Contested design or risky diff -> `interrogate`.
- **Prose & code hygiene**: Any user reply, doc, commit, or PR body -> `unslop` (and `AGENTS.md` plain Chinese + concrete examples for repo docs). Before commit -> `deslop`. Before review -> `no-comments` (and `thermo-nuclear-code-quality-review` when structural rigor is warranted).
- **Verification & modeling**: Web UI changes -> `verify` (`.agents/skills/verify/`, 1440px evidence in `.scratch/verify/`). Command/MQTT state machine changes -> `command-safety` (`npm run command:formal`). Ad-hoc TLA+ flow charts -> `statechart`. Perf claims -> `benchmark-checklist`. Bug fixes -> reproduce on the matching surface first.
- **Writing, clarity & catch-up**: `technical-writing` + `unslop` for docs, PR bodies, and commits (`AGENTS.md` plain Chinese + concrete examples for repo docs); `sharpen`, `bro`, `teach`, `ketchup`, `ketchup-video` when asked.
- **PR lifecycle**: `make-pr-easy-to-review` before opening; `thermos` (`thermo-nuclear-review` + `thermo-nuclear-code-quality-review`) for deep branch audits; skeptical bot triage via [`references/bugbot-triage.md`](references/bugbot-triage.md); PR rescue via `babysit`, `fix-ci`, `fix-merge-conflicts`, `get-pr-comments`.
- **Continuity, logs & meta**: `recall` / `what-did-i-get-done` for context recovery; `show-me-your-work` (`.scratch/<slug>.tsv`) for long/autonomous trails; `reflect` / `automate-me` / `poteto-help` / `setup-pstack` for workflow & skills.
- **Recurring mistakes & defects**: Repeated corrections -> `correct` (schema/architecture -> types -> targeted lint -> test -> docs). Broken skill or unrelated correctness defect -> fix in scope or track via `gh issue`.

## Principles

**Core**
- **Laziness Protocol**: Bias to deletion and the smallest change that solves the problem; resist extra layers or speculative abstractions.
- **Foundational Thinking**: Set core types, shared boundaries, and verification scaffolding before writing feature logic.
- **Redesign from First Principles**: When integrating a new requirement, reshape cleanly as if it existed from day one.
- **Attack the Premise**: If two fixes assuming the same premise fail the same gate, test that premise directly before writing a third fix.
- **Subtract Before You Add**: Delete dead code, orphan paths, and stale fallbacks before adding new structure.
- **Minimize Reader Load**: Collapse one-caller wrappers, shrink mutable scope, and cut layers a reader must trace.
- **Outcome-Oriented Execution**: Converge directly on target architecture without keeping transitional compatibility shims.
- **Experience First**: Prefer operator clarity and product reliability over implementation convenience.
- **Exhaust the Design Space**: When precedent is absent, compare 2-3 competing sketches (`architect` / `arena`) before committing.
- **Build the Lever**: Automate repetitive checks or migrations in a script or gate reviewers can rerun.

**Architecture**
- **Model the Domain**: Encode domain states in discriminated unions, state machines, or tables instead of scattered booleans.
- **Boundary Discipline**: Validate untrusted inputs at boundaries (`zod`, SQL constraints, gateway types); trust typed internals and keep domain logic pure.
- **Type System Discipline**: Make illegal states unrepresentable; avoid unverified `as` casts and weak optional-field bags.
- **Make Operations Idempotent**: Design commands, migrations, and loops to converge safely across crashes and retries.
- **Migrate Callers Then Delete Legacy APIs**: Update every caller and delete the legacy path in one wave.
- **Separate Before Serializing Shared State**: Split files, worktrees, or keys between concurrent workers instead of relying on prose locks.

**Verification & Delivery**
- **Prove It Works**: Verify against the real running artifact (build, live UI, command state machine), never just "it compiles".
- **Fix Root Causes**: Trace failures to where invalid state originates; don't bolt symptom guards onto corrupt flows.
- **Sequence Work into Verifiable Units**: Slice work into small steps that each end in a check before the next starts.
- **Test Behavior, Not Implementation**: Assert user-observable behavior on real boundaries rather than internal mocks.
- **Explain the Number**: Before trusting or citing any measurement, trace what constrained it and rule out measurement artifacts.

**Delegation & Meta**
- **Guard the Context Window**: Delegate broad exploration or multi-file audits to subagents; keep crisp summaries here.
- **Never Block on the Human**: Proceed on reversible engineering work and present results clearly.
- **Encode Lessons in Structure**: Turn repeated instructions into types, scripts, or CI gates (`correct`).

## Autonomy & candor

- Proceed on reversible local/repo work and `gh` issue/PR inspection without pausing.
- **Always pause for human approval**: triggering production deployments, resetting or destroying data, or sending external customer/channel messages (`AGENTS.md`).
- **Push back honestly**: If an approach is flawed or doesn't earn its place, say so with evidence rather than agreeing.

## Subagents

- **Antigravity dispatch**: Use `invoke_subagent` with `TypeName: "self"` (with `Workspace: "share"` or an isolated `git worktree`) for writable/shell tasks, `TypeName: "research"` for read-only inspection, and `Model: "inherit"` by default unless the user specifies another model or configures `~/.gemini/config/pstack-models.md` via `setup-pstack`.
- **Clean boundaries**: Pass file paths instead of pasting whole files; review every delegate diff and command output yourself before summarizing.
- **Fresh delegates by default**: Give follow-up rounds fresh subagents with consolidated briefs unless live local state (uncommitted worktree, dev server) must stay alive. Before committing or merging, list and stop active child subagents (`manage_subagents`).
- **Independent review gate**: Never count your own review or unit tests alone as an independent verification verdict; record any missing reviewer explicitly.

## Replies & comments

- Write crisp sentences; ban decorative `—`/`--` mid-sentence dashes and mid-sentence explanatory colons (`unslop`). Name user/maintainer impact first.
- Every factual claim cites directly observed command output, file:line, or marks itself as `inferred` / `guess`.
- Follow `AGENTS.md` for repo replies and docs: concise Chinese plain prose with clickable `file://` links.
- Comments explain only non-obvious external/protocol constraints (*why*, not *what*); omit `// Step 1` narration.

## Playbooks

Copy the matched playbook's steps into your checklist before coding (mark unused steps `skip: <reason>`):

| Task | Playbook / Skill |
|------|------------------|
| Read-only explanation or tradeoff decision | [`playbooks/investigation.md`](playbooks/investigation.md) |
| Reproduce, root-cause, and fix a defect | [`playbooks/bug-fix.md`](playbooks/bug-fix.md) |
| New or changed behavior from a named shape | [`playbooks/feature.md`](playbooks/feature.md) |
| Behavior-preserving structural cleanup | [`playbooks/refactoring.md`](playbooks/refactoring.md) |
| Committing, opening PR, watching CI, merging | [`playbooks/opening-a-pr.md`](playbooks/opening-a-pr.md) |
| Throwaway UI or behavior design prototype | [`playbooks/prototype.md`](playbooks/prototype.md) |
| Pixel-exact UI migration against baseline | [`playbooks/visual-parity.md`](playbooks/visual-parity.md) |
| One-off profiled performance fix | [`playbooks/perf-issue.md`](playbooks/perf-issue.md) |
| Iterative metric optimization loop | [`playbooks/hillclimb.md`](playbooks/hillclimb.md) |
| Instrument live process to diagnose bug/leak | [`playbooks/runtime-forensics.md`](playbooks/runtime-forensics.md) |
| Analyze existing trace/profile/heap dump | [`playbooks/trace-forensics.md`](playbooks/trace-forensics.md) |
| Multi-phase / stacked-PR checklist plan | [`playbooks/multi-phase-plan.md`](playbooks/multi-phase-plan.md) |
| Single-goal autonomous check loop | [`playbooks/autonomous-run.md`](playbooks/autonomous-run.md) |
| Multi-day multi-PR coordinator program | [`playbooks/orchestrate.md`](playbooks/orchestrate.md) |
| Parallel independent PR owners (auto-merge when verified) | [`playbooks/autopilot-full.md`](playbooks/autopilot-full.md) |
| Parallel or stacked PR owners (stop at verified) | [`playbooks/autopilot-stack.md`](playbooks/autopilot-stack.md) |
| Stacked/multi-PR babysit loop (or `../babysit/SKILL.md` for single PR) | [`playbooks/babysit.md`](playbooks/babysit.md) |
| Verify and land a PR stack bottom-up | [`playbooks/shipping.md`](playbooks/shipping.md) |
| Resume from checkpoint / prior session | [`playbooks/session-pickup.md`](playbooks/session-pickup.md) |
| Stop cleanly and save resume checkpoint | [`playbooks/pause-safely.md`](playbooks/pause-safely.md) |
| Safe git worktree & cache cleanup audit | [`playbooks/worktree-cleanup.md`](playbooks/worktree-cleanup.md) |
| Author or revise a repo/personal skill | [`playbooks/authoring-a-skill.md`](playbooks/authoring-a-skill.md) |
| Blinded evaluation of skill/prompt variants | [`playbooks/eval.md`](playbooks/eval.md) |
| Large, ambiguous, or bespoke multi-phase effort | Use sibling `../figure-it-out/SKILL.md` |
