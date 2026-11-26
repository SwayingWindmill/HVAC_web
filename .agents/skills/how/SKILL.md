---
name: how
description: "Use for \"how does X work\", code walkthroughs before changing something, and placement / ownership / layering questions (\"where should this live\", \"which package owns this\", \"is this the right layer\"). Explains subsystem architecture, runtime flow, onboarding mental models. Use why for motivation."
---

# How

Explore the codebase to answer "how does X work?" questions.

If the scope is ambiguous, state your interpretation and explore. The user can redirect.

## 1. Explain

For narrow single-module lookups, use `codegraph_explore`, parallel `read_file` slices, and `git grep -n` directly. For broader subsystems, spawn one read-only subagent (`invoke_subagent` with `TypeName: "research"` on Antigravity, or `Agent` with `subagent_type: "Explore"` on Claude Code; see [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md)) that explores and explains in one pass. Give it the user's question and this brief:

> You are writing an architectural explanation for a senior engineer. Explore the codebase and write an explanation that a senior engineer unfamiliar with this area could read and walk away with a solid mental model. They should understand the architecture well enough to start working in it confidently. You have read-only access to the codebase.

## 2. Present

Present the explanation clearly. Light edits for clarity or context from the conversation are fine. Do not substantially rewrite a subagent's findings.
