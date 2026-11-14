---
name: thermos
description: "Launch both thermo-nuclear review subagents in parallel, then synthesize their findings. Use for thermos, double thermo review, or combined bug/security and code-quality branch audits."
---

# Thermos

Read the [Antigravity platform mapping](../poteto-mode/references/agy-tools.md) before dispatching subagents.

Run both thermo review passes as parallel background subagents, then synthesize their results.

## Workflow

1. Determine the review scope from the user request, PR, current branch, or changed files (`git diff --stat main...HEAD` / working tree diff).
2. Gather the diff summary and relevant changed file paths so reviewers do not guess scope.
3. Launch both subagents in the same turn with `invoke_subagent`:
   - Pass 1 (security, bugs, breaking changes, devex regressions, leaks): use `TypeName: "self"` with explicit read-only instructions ("Do not modify any file; use read-only git/code inspection only") and instruct it to read and follow [`../thermo-nuclear-review/SKILL.md`](../thermo-nuclear-review/SKILL.md).
   - Pass 2 (maintainability, abstraction quality, giant files, spaghetti growth): use `TypeName: "self"` with explicit read-only instructions and instruct it to read and follow [`../thermo-nuclear-code-quality-review/SKILL.md`](../thermo-nuclear-code-quality-review/SKILL.md).

   Set `Model` according to [`agy-tools.md`](../poteto-mode/references/agy-tools.md): default to `"inherit"`, or use the `thermos reviewers` value from `pstack-models.md` (`~/.gemini/config/pstack-models.md` or `.claude/pstack-models.md`) when that sheet exists.
4. Pass each subagent the same scoped branch/diff context and require prioritized findings with file paths, line numbers, and concrete evidence.
5. Wait for both subagent messages to return, then synthesize findings first, deduplicated across reviewers. Weight overlapping findings higher, resolve disagreements with your own code check, and keep prose brief.

If subagent summaries are already clear, do not repeat them verbatim; surface the unified verdict, highest-signal findings, and any remaining uncertainty.
