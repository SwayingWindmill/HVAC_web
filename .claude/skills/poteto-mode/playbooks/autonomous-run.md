### Autonomous run

**You own the exit condition. Define done, then drive to it without stopping.**

1. State the exit condition as a checkable predicate before the first iteration (tests green, repro fixed, `npm run check` passing, all N PRs merged, pixel-diff zero).
2. Pick the wake mechanism using Antigravity's `schedule` tool (timer/cron) rather than shell `sleep`. An event to watch (CI, a merge, a ref advancing) gets a watcher subagent (`invoke_subagent`) or `gh pr checks --watch`, with a fallback heartbeat if needed. Set subagent `Model` per [`agy-tools.md`](../references/agy-tools.md).
3. Sequence work in small verifiable units: each iteration makes the smallest change the evidence justifies, verifies it against the predicate, commits if it advances, and discards changes that did not help.
4. Mid-run discoveries that support the goal are yours: address broken skills, related bugs, flaky verifiers, review noise, or tooling failures via `poteto-mode` (putting unrelated fixes in their own PR). Do not park reversible work or ask unnecessary questions; surface only irreversible gates (production deploy, data reset, external messages) or a real dead end.
5. Checkpoint every iteration via [`show-me-your-work`](../../show-me-your-work/SKILL.md) under `.scratch/<task-slug>.tsv`, logging one row for what changed and whether the predicate moved.
6. Stop when the predicate is met. A plateau is not a stop: pivot your approach to push past it, surface a genuine dead end rather than spinning, and never relax the predicate to declare victory.

**Reply:** the exit condition, iterations run, what landed, what was discarded, and final predicate state.
