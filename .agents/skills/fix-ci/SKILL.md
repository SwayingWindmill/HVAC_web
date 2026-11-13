---
name: fix-ci
description: Find failing PR checks, inspect GitHub Actions logs or check links, and apply focused fixes
---

# Fix CI

## Trigger

Branch or PR CI is failing and needs a direct, verified path back to green checks.

## Workflow

1. Resolve the active PR in WSL and inspect failing checks:
   ```bash
   gh pr checks --json name,bucket,state,workflow,link
   ```
2. Extract the first actionable error from failed GitHub Actions runs (`gh run view <run-id> --log-failed`) or named check links. Never guess from check names alone.
3. Reproduce the failing check locally in WSL if feasible (using `scripts/domain-task-matrix.ts` / `npm run check`), apply the smallest root-cause fix, and confirm the local reproduction passes.
4. Push via Windows `git push`, re-check with `gh pr checks --watch`, and repeat cleanly until green.

## Guardrails

- Fix one root failure cleanly at a time; never silence lints, skip tests, or weaken assertions to force a pass.
- Prefer minimal low-risk fixes over broad unrelated refactors.
- Keep `gh pr checks` as the source of truth for overall PR CI state.

## Output

- Failing job name, run/check link, and exact root error observed
- Local verification command and fix committed
- Updated CI status
