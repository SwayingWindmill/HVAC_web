# Resume storage

Pause and pickup share one project locator. Run `.agents/skills/poteto-mode/scripts/resume.ts` in WSL via a **login shell** (`wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts ..."`, using `--project /mnt/e/Code/HVAC_web` when calling from outside the repo or if your current subagent is in a Windows `Workspace: "share"` worktree, because WSL `git` cannot read Windows `gitdir: E:/...` links). It resolves the repository's Git common directory and stores checkpoints under `<git-common-dir>/pstack/resume/`. `latest.json` points to the latest complete checkpoint and records the worktree, branch, timestamp, note, and requested artifacts with SHA-256 content hashes.

Git worktrees of the same repository share this locator, and Git excludes `<git-common-dir>` from commits. Do not put checkpoints under `.audit/` (forbidden by `npm run repo:check`).

## Write a checkpoint

1. Run `wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts begin --project /mnt/e/Code/HVAC_web"` (use `<worktree>` instead only when it is a Linux/WSL worktree path) to allocate a new checkpoint directory. Write `resume.md` there with intent, verified progress, current state, and next steps.
2. Save requested artifacts in that directory (or point to durable tracked files that survive interrupting the worktree) and link them from `resume.md`. Link each local file with an inline Markdown link whose destination is the path (`[label](path)` or `[label](<path>)`), with no square brackets in the link text and no title after the path.
3. Publish with `wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts publish --project /mnt/e/Code/HVAC_web --note <resume.md> [--artifact <file> ...]"`. Register every local note link with `--artifact`. The command verifies file ownership, links, and contents before atomically replacing `latest.json`.
4. Read back with `wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts read --project /mnt/e/Code/HVAC_web"` and verify the note and artifacts open cleanly.

## Read a checkpoint

Session pickup starts with `wsl -e bash -lc "cd /mnt/e/Code/HVAC_web && node --disable-warning=ExperimentalWarning .agents/skills/poteto-mode/scripts/resume.ts read --project /mnt/e/Code/HVAC_web"` in WSL:

- Exit `0` with `kind: checkpoint`: open the saved `note` and registered `artifacts` before searching transcripts.
- Exit `2` with `kind: missing`: no checkpoint pointer exists yet; fall back to Antigravity/Claude transcripts or branch git state.
- Exit `1` with `kind: unavailable`: the checkpoint is malformed, unreadable, changed, or incomplete; diagnose the concrete gap rather than silently ignoring it.
