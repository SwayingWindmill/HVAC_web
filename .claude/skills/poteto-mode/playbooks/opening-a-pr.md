### Opening a PR

Invoked at the end of every other playbook (unless the task forbids opening a PR). Follow `AGENTS.md` and `docs/agents/issue-delivery.md`.

Work from a git worktree off trunk, stop child subagents (`manage_subagents`), and keep unrelated work, `.audit/`, `.scratch/`, and `[DEBUG-*]` markers out of git. Rebase into small, landable commits (`type(scope): subject`, LF line endings). Before committing, run [`deslop`](../../deslop/SKILL.md) over the diff, [`no-comments`](../../no-comments/SKILL.md) over touched code paths, [`open-code-review-delegate`](../../open-code-review-delegate/SKILL.md) when touching >10 files or multiple domains, [`make-pr-easy-to-review`](../../make-pr-easy-to-review/SKILL.md) (with a `changelog.d/YYYY-MM-DD-<slug>.md` fragment for user-visible changes), [`unslop`](../../unslop/SKILL.md) on PR copy/docs, and `npm run preflight` in WSL (`wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && npm run preflight"`).

Title: `type(scope): subject`, naming a real symbol when one carries the change. Body: a one-minute briefing under 40 lines with these `##` headings in order, dropping empty ones: `## Why`, `## What changed`, `## Scope`, `## Tradeoffs` (only rejected alternatives a reviewer would otherwise ask about), `## Blast Radius`, `## Verification`.

Stack follow-ups by base branch. Push from Windows (`git push -u origin <branch>`) and open the PR ready (never `--draft`) via WSL `gh pr create`. By `AGENTS.md`, solo PRs immediately watch CI (`gh pr checks --watch`, using `babysit` / `fix-ci` / `fix-merge-conflicts` if needed) and squash-merge (`gh pr merge --squash --delete-branch`) once every check is green; stop only for human boundaries or explicit held/stacked queues.
