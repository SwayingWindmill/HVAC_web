### Worktree and workspace cleanup

**This playbook deletes user state, so every safety gate below is mandatory.**

1. **Snapshot and audit in WSL (login shell).** Run:
   ```sh
   wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && df -h / && df -h . && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/worktree-audit.ts"
   ```
   It enumerates worktrees from `git worktree list --porcelain -z` (translating Windows drive paths to `/mnt/<drive>/...` so live Windows worktrees are audited/marked `review` instead of misclassified `prunable`) and classifies each into `hold-locked`, `hold-wip`, `hold-untracked`, `hold-reflog`, `hold-ignored`, `hold-open-pr`, `verify-recent-chat`, `safe`, or `review`.
2. **Treat buckets as advice, not blind deletion permission.** Cross-check against active/pinned chats or subagent workspaces (`manage_subagents` with `Action: "list"`).
3. **Verify `verify-recent-chat` and `review` rows before pruning.** Inspect referenced recent transcripts (`<appDataDir>/brain/<id>/.system_generated/logs/transcript.jsonl` or `~/.claude/projects/`) inside read-only subagents so bulk transcripts stay out of main context. If a `review` row is a Windows-created worktree (`C:/...` or `E:/...`) audited from WSL, inspect its `git status` / `git log` using **Windows `git`** before deciding.
4. **Pause and get explicit user approval before removing anything with state at risk:**
   - `wip:N` / `untracked:N`: inspect with `git -C <path> status --porcelain --untracked-files=all --ignore-submodules=none` and `git -C <path> diff HEAD --ignore-submodules=none --submodule=log` (on Windows for Windows worktrees).
   - `ignored:N`: inspect non-disposable ignored files (like `.env`) with `git -C <path> status --porcelain --ignored=matching`.
   - `reflog:N`: commits reachable only via that worktree's HEAD reflog; rescue wanted ones with `git branch <name> <sha>` first.
   - `hold-locked`: inspect lock reason and do not delete around git locks.
   Only confirmed clean + merged + inactive worktrees can be pruned cleanly.
5. **Prune confirmed clean worktrees on the OS that owns them.** Remove Windows-created worktrees (`C:\...`, `E:\...`) from **Windows** `run_command` (`git worktree remove <path>` then `git worktree prune`), and **never** run `git worktree prune` in WSL while any live Windows-created worktree exists (WSL `git` cannot stat DOS paths and will prune live Windows worktrees!). Delete any leftover build dir inside removed trees, then confirm reclaimed disk space with `df -h` in WSL.
6. **Optional cache cleanup.** Reclaim `.scratch/` stale artifacts or unused Docker/Playwright/build caches only when confirmed unneeded with the user.

**Reply:** `df -h` before/after and reclaimed space, worktrees pruned, and exact one-line hold reason for each kept worktree.
