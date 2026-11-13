---
name: make-pr-easy-to-review
description: Prepare PRs for review by cleaning noisy history, improving PR descriptions, and adding reviewer guidance without changing code behavior. Use for "make this easy to review", "tidy this PR", "clean up commits", or "annotate the diff".
---

# Make PR Easy to Review

Prepare a PR so a reviewer can quickly understand intent, important files, and blast radius without behavior changes.

## Workflow

1. Resolve the target PR from URL or current branch (`gh pr view` in WSL).
2. Inspect commits, diff size, changed paths, generated files, `CHANGELOG.md` / `docs/HANDOFF.md` alignment, and PR description.
3. Identify reviewability issues: noisy WIP commits, stale descriptions, unrelated edits mixed in, missing test/verification notes, or unclear entry points.
4. Propose or confirm the cleanup plan before rewriting pushed history.
5. Apply safe improvements, then confirm the resulting tree matches the pre-cleanup tree.

## History Cleanup

When rewriting local unpushed commits (or with explicit plan on a private branch):

```bash
gh pr view <PR> --json title,headRefName,baseRefName,state,commits
git fetch origin <headRefName> <baseRefName>
ORIGINAL_TREE=$(git rev-parse HEAD^{tree})
```

Clean commit groupings typically follow dependency order:

1. Schema / migrations / generated API contracts.
2. Core module / domain logic.
3. Wiring, gateway, and integration.
4. UI or operator surface changes.
5. Tests, docs, and handoff/changelog updates.

After rebasing or squashing, verify tree content identity before force-pushing:

```bash
echo "Original tree: $ORIGINAL_TREE"
echo "Current tree:  $(git rev-parse HEAD^{tree})"
```

Do not push if the tree changed unintentionally. Use Windows `git push` per `AGENTS.md`.

## Reviewer Guidance

Keep behavior unchanged while sharpening the PR description (`unslop` prose):

- Add a tight TL;DR matching the actual diff and `git diff --stat`.
- Separate core logic changes from generated artifacts or mechanical cleanups.
- Call out blast radius, migration ordering, security/ownership impact, and real local checks run.
- Link tracked GitHub issues or ADRs explaining non-obvious decisions.

## Guardrails

- Never hide behavior changes inside a "cleanup" PR.
- Never skip git/repo hooks (`--no-verify`).
- If a PR mixes unrelated concerns too heavily to tidy with notes, recommend splitting it cleanly.
