### Shipping

Resolve the repo driver skill first (`.agents/skills/verify/SKILL.md`, `.agents/skills/command-safety/SKILL.md`, or `scripts/domain-task-matrix.ts`).

**You own what lands. Verify each PR independently, land only the contiguous verified run from the bottom up, then keep your hands off the stack.** Landing requires merge authority (in `HVAC_web`, solo PRs via [`opening-a-pr.md`](opening-a-pr.md) follow `AGENTS.md` auto-merge when CI is green; stacked PR chains follow this gated flow).

1. **Resolve and inspect the chain before merging.** Capture bottom-to-top PR numbers, head branches, head SHAs, base branches, base SHAs, and destination (`main`). Read [`merge-safety.md`](../references/merge-safety.md) before merging, arming auto-merge, rebasing, or retargeting. Assign an independent verifier subagent per PR (`invoke_subagent`, `TypeName: "research"` or read-only `"self"`) to test the real behavior (`verify` / `command:formal` / domain checks) and record `PASS`, `PASS+NOTES`, or `FAIL` along with head SHA, base SHA, base branch, and stable `git patch-id` (`git diff <base>..<head> | git patch-id --stable`). Green CI alone is necessary but not sufficient for stacked behavior changes.
2. **Land only the contiguous verified prefix from the bottom PR up.** Stop before the first PR without `PASS` / `PASS+NOTES` (if an independent higher PR wants to land alone, retarget it onto `main` and re-verify cleanly).
3. **Re-check verdicts after restacks.** Compare `git patch-id` against the verified SHA after rebasing a child onto updated `main`. If behavior changed or applicability to `main` is uncertain, re-run verification checks on the new head.
4. **Merge bottom-up one PR at a time with head guard** (run `gh` inside WSL `wsl -e bash -lc` and `git rebase` / `git push` from Windows on Windows worktrees):
   - Inspect live state via `gh pr view <pr> --json number,state,headRefName,headRefOid,baseRefName,autoMergeRequest,mergeStateStatus` right before merging.
   - Cancel any stale auto-merge or queue entry on descendants before rewriting/retargeting (`gh pr merge <desc> --disable-auto`).
   - Merge with `gh pr merge <pr> --squash --match-head-commit <verified-head> --delete-branch`.
   - Read back `gh pr view <pr> --json state,headRefOid,baseRefName,mergeCommit`, fetch `origin/main`, and verify `git merge-base --is-ancestor <merge-commit> origin/main`.
   - Retarget the next child PR to `main` (`gh pr edit <next-pr> --base main`), rebase onto `origin/main` cleanly from the recorded old parent tip, push from Windows with `--force-with-lease=refs/heads/<child>:<captured-remote-head>`, wait for green CI (`gh pr checks <next-pr> --watch`), and check `git patch-id` before advancing.
5. **Leave uninvolved stack branches untouched while a frontier PR lands.** Watch CI with `gh pr checks --watch` (plus `schedule` only if asynchronous bots remain).
6. **Stop at the verified ceiling.** Extending further starts a fresh pass from step 1.

**Reply:** verified run and ceiling, per-PR verdicts/reviewers, landed PRs and merge commits verified on `main`, and what remains unmerged.
