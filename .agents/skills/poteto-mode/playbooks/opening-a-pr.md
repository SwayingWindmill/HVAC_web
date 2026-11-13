### Opening a PR

Invoked at the end of Bug fix, Feature, and Refactoring (unless the task explicitly forbids opening a PR). Follow `AGENTS.md` and `docs/agents/issue-delivery.md` strictly.

**Workspace & subagents.** Before staging or committing, inspect `manage_subagents` (`list`) and stop every child agent holding a shared/worktree directory. Confirm `git status` shows only intended changes and zero `.audit/`, `.scratch/` artifacts, or `[DEBUG-*]` markers.

**Commits & hygiene.** Commit cleanly with Conventional Commits (`type(scope): subject`, no trailing period; LF line endings). Before opening or updating the PR:
1. Run `/deslop` over the branch diff against `main`.
2. Run `/no-comments` over touched code paths.
3. Run `/make-pr-easy-to-review` (check commit ordering, `CHANGELOG.md` for user-visible changes, and `docs/HANDOFF.md` under `28 KiB`).
4. Apply `/unslop` and verify all relevant `scripts/domain-task-matrix.ts` gates + `npm run check` passed locally in WSL after your last edit.

**PR body.** Keep the PR body concise and scannable (under ~40 lines when squashed):
- `## Why`: 1-3 sentences stating the problem and approach.
- `## What changed`: 1-3 crisp bullets naming real symbols/paths.
- `## Scope`: what this covers and explicitly leaves out.
- `## Tradeoffs`: only non-obvious rejected options (skip if none).
- `## Blast Radius`: who/what this touches and why it is safe or risky.
- `## Verification`: real commands run after the final edit and their observed counts/outcomes (`verify` screenshots referenced cleanly if UI changed).

**Push, open, watch CI, and auto-merge (Repository rule).**
- Push the feature branch using **Windows `git push`** (`git push -u origin <branch>`).
- Create the ready (non-draft) PR via **WSL `gh`** (`gh pr create --title "..." --body "..."`, referencing closing/related issues as needed).
- Do **not** stop at "PR opened". Immediately follow `gh pr checks --watch` (or invoke `babysit` / `fix-ci` / `fix-merge-conflicts` if CI fails or conflicts arise) until every GitHub Actions check is green.
- Once all checks are green and reviewable findings are resolved, auto-merge using the repository convention: `gh pr merge --squash --delete-branch`, then pull/update local `main` and remove temporary scratch/worktree directories.
- **Always pause first** only if crossing `AGENTS.md` human boundaries: production deploy trigger, destructive data reset, or external channel messages.
