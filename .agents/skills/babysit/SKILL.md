---
name: babysit
description: Watch an open PR — fix failing CI, handle review comments, resolve conflicts, and drive it to a mergeable green state. Use after opening a PR or when asked to shepherd CI/comments on a PR.
---

# Babysit a PR

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before running background checks (`schedule` replaces `loop`, `ask_question` replaces `AskUserQuestion`; run `gh` in WSL and `git push` on Windows per `AGENTS.md`).

Drive an open PR to green checks and resolved review threads using `gh`.

## Steps

1. **Fetch PR state (WSL `gh`).**

   ```bash
   gh pr view <number> --json number,title,state,mergeable,reviewDecision,statusCheckRollup,mergeStateStatus,comments,reviews
   ```

2. **Triage in priority order.**
   - **Merge conflicts** (`mergeStateStatus == DIRTY`): run sibling `../fix-merge-conflicts/SKILL.md`. Only force-push (`--force-with-lease`) on an isolated branch you own.
   - **Failing CI checks** (`statusCheckRollup` failure): run sibling `../fix-ci/SKILL.md`. Root-cause the failure cleanly, verify locally in WSL, commit, and push via Windows `git push`.
   - **Human review comments**: run sibling `../get-pr-comments/SKILL.md`, then fix genuine defects/issues cleanly and quote what changed. If a comment hinges on an ambiguous product decision, stop and ask the user via `ask_question`.
   - **Automated review bots**: triage `fix` / `dismiss` / `ask` skeptically per [`../poteto-mode/references/bugbot-triage.md`](../poteto-mode/references/bugbot-triage.md). Default to asking the user on security, auth, migration, data-integrity, or high-severity findings.

3. **Watch checks cleanly.**
   - Active CI run: watch via `gh pr checks --watch` in WSL (blocks until completion, avoiding unnecessary timer chatter).
   - Asynchronous follow-ups: use Antigravity's `schedule` tool rather than shell `sleep`.

4. **When to stop / merge.**
   - All required CI checks are green, conflicts are resolved, and actionable review comments are addressed -> per `AGENTS.md` and `docs/agents/issue-delivery.md`, automatically merge with `gh pr merge --squash --delete-branch` and sync local `main`. Pause first only if human review blockers, ambiguous product questions, or `AGENTS.md` human boundaries remain (triggering production deploys, resetting or destroying data, or sending external channel messages).
   - After 3 failed fix -> push -> recheck cycles without turning green -> stop, summarize remaining failures with exact logs/outputs, and ask the user.

5. **Report.** Summarize fixes committed (SHAs), review comments addressed or dismissed with evidence, and final PR / merge status.

## Hard rules

- Never weaken assertions just to turn a red test green.
- Never bypass git hooks (`--no-verify`) or skip required CI gates.
- Never mark a draft PR ready (`gh pr ready`) while checks fail or unaddressed review blockers remain.
- Apply `interrogate` before pushing if a fix introduces a non-trivial design shift, and `unslop` on any PR comments written.
