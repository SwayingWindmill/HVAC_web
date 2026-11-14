### Session pickup

**You own the resume point. Read the prior trail, don't redo it.**

1. Check for a structured checkpoint first per [`resume-storage.md`](../references/resume-storage.md): run `wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts read --project /mnt/e/Code/HVAC_web"` in WSL.
   - If `kind: checkpoint`, open `note` and all registered `artifacts` before hunting elsewhere.
   - If `kind: missing`, fall back to the workspace's active history: Antigravity `<appDataDir>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl` (`transcript_full.jsonl` when truncated, matching workspace URI) or Claude Code `~/.claude/projects/<slug>/*.jsonl`, or pushed branch/PR history (`gh pr view` / `git log`). If a checkpoint is `unavailable` (corrupt/drifted), diagnose that gap first. Parse huge transcripts inside a subagent so raw dumps stay out of main context.
2. Reconstruct operational state cleanly: current branch/worktree, `git log` / `git diff main...HEAD`, open todos, and recorded human decisions per [`../../why/references/epistemics.md`](../../why/references/epistemics.md#claims-about-human-decisions).
3. Diff done vs pending: identify the exact resume point and do not repeat completed investigations or re-run already verified work unless artifacts drifted.
4. Spot-verify inherited claims against real code/artifacts (a prior agent's summary alone is not proof).
5. Hand execution to the matching playbook (`feature.md`, `bug-fix.md`, `babysit.md`, etc.) cleanly from the resume point.

**Reply:** where the prior session stopped, what was inherited vs re-checked, exact resume point, and next action.
