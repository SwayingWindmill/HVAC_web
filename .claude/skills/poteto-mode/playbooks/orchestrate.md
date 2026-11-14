### Orchestrate

Resolve the repo driver skill first (`.agents/skills/verify/SKILL.md`, `.agents/skills/command-safety/SKILL.md`, or `scripts/domain-task-matrix.ts`).

**You run the program, never the code. Decide which agent does each piece of work, then check what each one produces.** For multi-day programs spanning many PRs and subagents (use `autonomous-run.md` for one task driven to a predicate, `figure-it-out` for one bespoke multi-part migration).

Spawn workers via `invoke_subagent` (`TypeName: "self"`, `Workspace: "share"` for concurrent writers, `Model` per [`agy-tools.md`](../references/agy-tools.md)). Keep program state under `.scratch/orchestrate/<slug>/notes.md`, outside git commits. Use `schedule` (cron/timer) or `gh pr checks --watch` for notifications instead of shell `sleep` polling.

#### Topology

- **One owner per PR.** Only the owner pushes, rebases, or retargets its branch. Closing or rebasing a base PR affects every child above it, so only the coordinator authorizes stack topology changes.
- **A fresh worker per independent unit.** Give every kickoff full context and explicit paths it owns or must not touch. Never spawn shared writers on the same working tree without `Workspace: "share"` or dedicated git worktrees.
- **Keep delegation shallow.** Stop at coordinator -> sub-coordinator -> worker at most, launching next units as slots finish rather than blocking on huge batches.
- **Message workers only for continuation on the same task;** spawn fresh workers in isolated workspaces for independent work. Close/kill completed shared-workspace workers before committing or merging (`manage_subagents`).
- **Each PR gets an independent verifier** that did not author the code. Merge only after verifying at the exact head SHA.
- **Start with one unit** end-to-end (kickoff -> build -> verify -> PR) before fanning out to catch brief bugs once instead of N times.

#### Supervision

- **Each kickoff names concrete pass/fail evidence** (domain checks, Playwright screenshots, `command:formal`, exact log/output). If you cannot name the proof, the unit is under-scoped.
- **Inspect diffs and artifacts yourself,** never only a worker's summary. Green CI alone is an input, not full proof.
- **Reject scope drift early;** park unrelated cleanup into follow-up issues/PRs.
- **Verify live GitHub state with `gh pr view` and `gh pr checks`** before reporting PR status. Account for every spawned worker cleanly.
- **Replace stalled or looping workers** based on commits/PR/file side effects; after two failed attempts on the same failure mode, shrink or replan the unit.
- **Ask the user only for genuine product choices, dead ends after replan, or `AGENTS.md` irreversible boundaries** (production deploy, data reset, external channel messages).

**Reply:** what landed against the goal, each PR's live `gh` state and link, dropped/parked units, and open user decisions.
