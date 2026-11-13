---
name: fix-merge-conflicts
description: Resolve merge conflicts non-interactively, validate build and tests, and finalize conflict resolution
---

# Fix merge conflicts

## Trigger

Branch has unresolved merge or rebase conflicts against its target base branch and needs a clean, verified resolution.

## Workflow

1. Identify all conflicting files (`git status`, grep for `<<<<<<<`, `=======`, `>>>>>>>`).
2. Inspect both incoming and current intent (never blindly run `--ours`/`--theirs` across unknown files).
3. Resolve each conflict minimally, preserving intended behavior and repository contracts (`AGENTS.md`, module ownership, schema migrations).
4. Regenerate lockfiles or generated code using repository tools (`npm install`, `go mod tidy`, OpenAPI generators) instead of hand-editing generated sections. Remember `.agents/skills/` and `.claude/skills/` must remain byte-for-byte mirrored.
5. Verify build, `npm run check`, and affected unit tests in WSL.
6. Stage resolved files cleanly, complete merge/rebase, and summarize choices.

## Guardrails

- Never leave conflict markers or broken syntax behind.
- Avoid unrelated refactors inside conflict-resolution commits.
- Never force-push shared branches; verify `HEAD` diff before pushing.

## Output

- Conflicting files resolved
- Non-trivial conflict choices explained
- Verification commands run and passing results
