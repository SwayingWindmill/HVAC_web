### Pause safely

**You own a clean stop. Leave a checkpoint a cold-start agent can resume from.** This is explicit only. On "keep going", "don't stop", or "keep working", do not pause.

1. Stop at a safe boundary. Finish the current atomic edit or back out cleanly. Start nothing new and stop active nested subagents (`manage_subagents` with `Action: "list"` / `"kill"`).
2. Take no irreversible action to pause (no new PR, no push unless already pushed, no merge).
3. Make in-flight edits durable on the feature branch as a clear `wip:` commit so nothing is lost (noting in the commit body if checks are currently failing).
4. Write `resume.md` off-context using [`resume-storage.md`](../references/resume-storage.md): allocate a directory in WSL login shell (`wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts begin --project /mnt/e/Code/HVAC_web"`, using `--project /mnt/e/Code/HVAC_web` even from a Windows `Workspace: "share"` worktree), capturing intent, verified progress, current state, next steps, key files, and gotchas. Link any [`show-me-your-work`](../../show-me-your-work/SKILL.md) log path under `.scratch/` instead of duplicating it.
5. Preserve any user-requested artifacts (checklists, open questions, specs) beside `resume.md` (or linked in the repo if durable), publish with `resume.ts publish --project /mnt/e/Code/HVAC_web --note <resume.md> [--artifact <file> ...]`, and verify immediately with `resume.ts read --project /mnt/e/Code/HVAC_web` before reporting ready.

**Reply:** where the work paused, paths saved on disk, commits made and working-tree status, and the exact first action on resume.
