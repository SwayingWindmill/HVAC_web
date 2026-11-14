# Prompts worth copying

Swap in real paths, domains, and done checks.

## Understand

- `Use poteto-mode: read <issue/thread> and restate the problem in your own words before touching code.`
- `/in-your-own-words <long or messy ask>`
- `/sharpen <the prompt you are about to send>`
- `Use poteto-mode to investigate why <symptom> happens. Give facts checked and hypotheses; do not edit code yet.`
- `Use how to trace <subsystem>, then use why to check ADRs and git history for <threshold or rule>.`
- `Recall my work on <topic> from the last week, then inspect <file or PR>.`
- `Teach me why HVAC_web enforces <constraint> and what tradeoffs it makes.`
- `Use poteto-mode with session-pickup: read the decision log and branch state on <branch>, then continue.`

## Build

- Bug: `Use poteto-mode: repro <symptom> on <route/module> first, then fix and verify.`
- Web UI bug: `Use poteto-mode: repro with verify at 1440x900 against recorded gateway or live stack, fix it, and save before/after screenshots under .scratch/verify/.`
- Bug with cheap test: `Use poteto-mode: repro <bug> first, use tdd to write the failing unit test, fix it, and re-run.`
- Feature: `Use poteto-mode: add <behavior>. Keep <existing output> unchanged and verify both.`
- Refactor: `Use poteto-mode: consolidate <modules> with zero behavior change. Record baseline test outputs before and after.`
- Perf: `Use poteto-mode (perf-issue): <operation> takes <time> on <fixture>. Profile it, fix the measured limiter, and vet numbers with benchmark-checklist.`

## Design and plan

- `Use poteto-mode with the prototype playbook: build two throwaway options for <UI/flow> and show screenshots to compare.`
- `Use poteto-mode: design <feature> with architect (checkpoint before editing code).`
- `Use arena for competing approaches to <problem>, then graft the best solution.`
- `Use poteto-mode with multi-phase-plan: break <migration> into small verifiable PRs with exact checks per phase.`
- `Run statechart on a minimal TLA+ spec of <protocol/flow> in .scratch/statechart/ and show me any invariant violation paths.`

## Review and ship

- `Run interrogate on the current branch diff against main, proving any claimed bug with code.`
- `Run thermos on this branch for combined security/bug and code-quality audits.`
- `Run swarm across modules under modules/ and cmd/ to audit <check>, returning one consolidated report.`
- `Check command-safety: run npm run command:formal and verify all command state invariants.`
- `Use poteto-mode: babysit PR #<number> until all CI checks pass and review comments are resolved.`
- `Use poteto-mode (shipping): land stacked PRs #<n1> -> #<n2> safely with rebase + CI checks.`

## Away and back

- `Use poteto-mode (autonomous-run): achieve <goal> from a clean branch off main, log decisions with show-me-your-work under .scratch/<slug>.tsv, run npm run check, and open/watch the PR.`
- `Show me your work on .scratch/<slug>.tsv — check the log against the transcript and give the Attention section.`
- `/ketchup — catch me up on this chat and list anything waiting on my decision.`
- `/reflect — mine this session's transcript for durable skill improvements and propose edits before changing anything.`
- `/bro — restate your last reply in plain everyday words.`
