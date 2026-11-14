# Word the prompt

A prompt states the intent and the check for done. The playbook supplies the steps, so a few plain sentences beat a spec.

## Put in

- The goal. Say what is wrong or what you want.
- The done check. It must pass or fail ("make it better" or a duration are not checks).
- The proof to show. Ask for real command output (`npm run check`, `npm run verify:live`, `npm run command:formal`), screenshots in `.scratch/verify/`, or benchmark runs.
- What you already know. A symptom, repro step, log, or file path saves a search.
- Real constraints ("repro first", "don't change code yet", "zero behavior change", "let me review before proceeding").

## Leave out

- The how. Say what outcome you want so the agent can choose the cleanest implementation.
- A manual list of skills unless overriding a default choice.
- Your hypothesis about root cause until the agent restates the problem first.

## Load the context first

- For a noisy bug report, ask for `in-your-own-words` first so misreadings surface before code exists.
- In a fresh chat, use `recall` on recent work before starting.
- Before modifying unfamiliar Go/TS modules, ask `how` for runtime flow and `why` for design rationale and ADRs.
- Ask `teach` when you want tradeoffs explained plainly with diagrams.

## Design before the plan

- Never accept the first design blindly. Prototype options (`prototype` playbook) with screenshots or running checks before committing to architecture.
- Ask for `multi-phase-plan` only after open questions are settled. Every plan step ends with a concrete check.

## Before stepping away

- State autonomous instructions clearly (base branch, done predicate, decision log via `show-me-your-work`, stop conditions if blocked).
- Pre-answer merge/PR boundaries (`AGENTS.md` auto-PR & squash merge when CI passes vs stop-before-merge if you want manual review).
- Use `schedule` (cron / timer) only with explicit checkable predicates.

## Steer in one line

- Restate scope: "The goal is only to repro first; do not change code yet."
- Require proof: "Show me real test/Playwright/formal output, not just build success."
