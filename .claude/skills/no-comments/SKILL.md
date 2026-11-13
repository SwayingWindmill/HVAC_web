---
name: no-comments
description: "Spawn the comment-sicko reviewer, remove ungrounded noise comments and symptom workarounds inside the diff, and offer structural encodings for real constraints."
---

# No comments

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before following this skill.

Review comments in the current diff ruthlessly and act on valid findings without breaking unrelated existing documentation (`AGENTS.md`).

## Scope

Use the caller's named files or diff; otherwise use the current branch diff against `main` including staged and unstaged changes.

## Steps

1. **Dispatch the comment reviewer.** Launch `invoke_subagent` (`TypeName: "self"`, read-only brief telling it NOT to edit application files directly; on Claude Code use `Agent`) with [`references/comment-sicko-prompt.md`](references/comment-sicko-prompt.md) as its prompt and point it to the exact diff scope. Keep `Model: "inherit"` unless asked otherwise.
2. **Audit its report and proposed deletions.** Reject any attempt to edit application logic directly, expand past scope, invent reasons, or strip comments explicitly protected by `AGENTS.md` (unrelated existing docstrings, license headers, verified external/protocol `HOLD` rules, public API contracts, or issue/ADR links).
   - If the reviewer proposes removing a lint or TypeScript suppression (`@ts-ignore`, `eslint-disable`), remember `npm run design:check` already enforces strict limits outside `apps/hvac-web/src/components/ui/`: fix the root type/design issue instead of hiding behind suppressions.
   - Before deleting or restoring a dubious `IMPORTANT` / `do not remove` comment inside changed code, check its symbol via `how` or `why`. Leave a concrete vendor/protocol constraint as a verified `HOLD`; remove stale narration or workaround excuses.
3. **Fix valid structural flags cleanly.** When a comment only existed to apologize for confusing code, rename, extract, or invoke `architect` on the surrounding scope rather than rewording the comment.
4. **Fix root causes, never symptom guards.** Apply the smallest root-cause cleanup inside scope (**Fix Root Causes**, **Redesign from First Principles** in `poteto-mode`).
5. **Offer encodings for real constraints.** If a comment enforces a rule code could check (e.g. invariant comment `do not reorder X`), offer or implement the cheapest type, unit test, or `scripts/check-repository-governance.ts` / `design:check` check (`correct`), then delete the redundant comment.
6. **Clean up & report.** Stop the reviewer subagent (`manage_subagents`), then summarize deleted comments, preserved `HOLD`s, root-cause fixes, and constraint encodings offered or added.
