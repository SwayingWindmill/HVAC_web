### Worktree and workspace cleanup

**This playbook deletes user state, so every safety gate below is mandatory.**

1. **Snapshot and audit in WSL.** Run `df -h /` (and `df -h .`), then run:
   ```sh
   node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/worktree-audit.ts
   ```
   It enumerates worktrees from `git worktree list --porcelain -z` (never hand-typed globs) and classifies each into `hold-locked`, `hold-wip`, `hold-untracked`, `hold-reflog`, `hold-ignored`, `hold-open-pr`, `verify-recent-chat`, `safe`, or `review`.
2. **Treat buckets as advice, not blind deletion permission.** Cross-check against active/pinned chats or subagent workspaces (`manage_subagents action: "list"`).
3. **Verify `verify-recent-chat` and `review` rows before pruning.** Inspect referenced recent transcripts (`<appDataDir>/brain/.../transcript.jsonl` or `~/.claude/projects/`) inside read-only subagents so bulk transcripts stay out of main context.
4. **Pause and get explicit user approval before removing anything with state at risk:**
   - `wip:N` / `untracked:N`: inspect with `git -C <path> status --porcelain --untracked-files=all --ignore-submodules=none` and `git -C <path> diff HEAD --ignore-submodules=none --submodule=log`.
   - `ignored:N`: inspect non-disposable ignored files (like `.env`) with `git -C <path> status --porcelain --ignored=matching`.
   - `reflog:N`: commits reachable only via that worktree's HEAD reflog; rescue wanted ones with `git branch <name> <sha>` first.
   - `hold-locked`: inspect lock reason and do not delete around git locks.
   Only confirmed clean + merged + inactive worktrees can be pruned cleanly.
5. **Prune confirmed clean worktrees.** Run `git worktree remove <path>` (or `--force` only when already verified safe), delete any leftover build dir inside removed tree, then run `git worktree prune`. Confirm reclaimed disk space with `df -h`.
6. **Optional cache cleanup.** Reclaim `.scratch/` stale artifacts or unused Docker/Playwright/build caches only when confirmed unneeded with the user.

**Reply:** `df -h` before/after and reclaimed space, worktrees pruned, and exact one-line hold reason for each kept worktree.
