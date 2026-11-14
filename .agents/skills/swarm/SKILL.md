---
name: swarm
description: "Fan out N parallel workers, drain them, and return one report. Use for /swarm, 'swarm this', or parallel coverage, races, gauntlets, and exploration."
---

# Swarm

On Antigravity, read [`../poteto-mode/references/agy-tools.md`](../poteto-mode/references/agy-tools.md) before dispatching workers. Antigravity also has a built-in peer-board `swarm` tool: prefer `invoke_subagent` (this skill's default parent-supervised pattern returning structured `PASS` / `ISSUES` / `BLOCKED` verdicts) unless workers specifically need peer-to-peer coordination on the shared `swarm` board (`workspace: "share"` for writers). On Claude Code, spawn parallel `Agent` calls with `run_in_background: true`.

## Start

Open a checklist (`.scratch/todo.md` on Antigravity; never `.audit/`) with one entry per phase before launching workers:

1. Frame
2. Fan out
3. Aggregate
4. Report

## Phase A: Frame

1. State the done predicate and the exact artifact or report the swarm must return.
2. Choose the shape: partition work into disjoint slices, race N workers on identical briefs, or mix both. For races, declare the selection rule (`first pass`, `rank all`, or `best-of`) up front.
3. Choose subagent types per `agy-tools.md` (`TypeName: "self"` for writable/shell/MCP workers, `TypeName: "research"` for read-only explorers; default `Model: "inherit"` unless the user asks for another model).
4. Give each writable worker an isolated output path, branch, or `Workspace: "share"` / git worktree so workers never race on the same working tree. When verifying or benchmarking commits, name exact SHAs and measurement method (sample count, unit, order) in every brief.

## Phase B: Fan out

Launch all N workers in parallel in a single response (`invoke_subagent` on Antigravity; `Agent` on Claude Code) and end the turn to wait for results.

When a worker starts from a non-default branch or dirty state that must be isolated, give it its own worktree (`Workspace: "share"` or `git worktree add`).

Every brief must stand alone: state the goal, scope, exact slice or race arm, verification command, and return format (`PASS`, `ISSUES`, or `BLOCKED` with verbatim evidence). Workers finding defects must report every provable issue, not stop at the first. If a worker drops out, proceed with `N - 1` and record the gap.

## Phase C: Aggregate

Inspect every returned result. Reject reports that omit requested SHAs, commands, or measurement methods; respawn once if needed, and record a gap (never a pass) on a second miss. For partitioned coverage, confirm every slice returned. For races, apply the declared selection rule cleanly. Clean up child subagents (`manage_subagents`) before touching git commits.

## Phase D: Report

Present one consolidated summary with a result table, issue one-liners citing files/commands, any gaps or dropouts, and the winning race rule when applicable.
