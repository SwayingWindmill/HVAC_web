### Hillclimb

**You own the metric and the experiment's integrity. Supervise and review. Delegate the attempts.** For sustained, iterative improvement of one measurable thing against a target (use `bug-fix.md` or `perf-issue.md` for one-off fixes).

Core discipline: one change, one measurement, keep or revert. Never stack untested changes, and never claim a win from code inspection alone (prove it works with real runs).

1. **Ground the workload and architecture first.** Run [`how`](../../how/SKILL.md) over the target, name the realistic workload dimensions that move the result (data size, concurrency, DB volume), and pick a case that reproduces the user's complaint. Fix one metric, the improvement direction, and a checkable stop predicate pairing a target with a minimum iteration floor (e.g. "at least 50% lower latency than baseline across at least 10 iterations").
2. **Build, vet, and freeze the measurement harness.** Confirm the harness separates contrasting workloads, then vet it with [`benchmark-checklist`](../../benchmark-checklist/SKILL.md) before freezing (ensure it prints error count and work-done count). Record the baseline metric (median of N runs) and confirm existing regression checks (`npm run domain:run` / Go tests / web tests) are green before any edit.
3. **Open the decision log via [`show-me-your-work`](../../show-me-your-work/SKILL.md).** Keep `.scratch/<slug>-hillclimb.tsv` out of git, one row per attempt: `id`, `hypothesis`, `change`, `before`, `after`, `delta`, `tests`, `verdict` (`kept` or `reverted`), `note`. Read it before each attempt.
4. **Ground every hypothesis in architecture.** Each hypothesis names a specific mechanism ("defer X off hot path because Y"), never blind tuning. Order perf hypotheses using step 2 of [`perf-issue.md`](perf-issue.md).
5. **Loop, one hypothesis per iteration:**
   - Delegate the scoped change via `invoke_subagent` (`TypeName: "self"`, `Workspace: "share"` when running parallel hypotheses) while keeping main context focused on supervision and diffs.
   - Measure before and after with the frozen harness and run the regression gate.
   - Keep only when the metric moves past noise, errors = 0, and regression checks stay green; otherwise revert cleanly.
   - One commit per accepted fix (`git add <files>`, never `git add -A`), logging every iteration whether kept or reverted.
   - If running unattended, borrow the `schedule` wake mechanism from [`autonomous-run.md`](autonomous-run.md).
6. **Push past the first plateau.** On consecutive rejects, pivot mechanism category, combine near-misses, or re-profile. Keep correctness and simplicity ahead of raw score; keep simplifications that preserve the metric.
7. **Stop when the predicate is met**, or surface a genuine plateau if only marginal ideas remain without relaxing the stop rule.
8. Run [`opening-a-pr.md`](opening-a-pr.md) with the accepted commits stacked cleanly.

**Reply:** the metric and target, baseline to final with percent delta, iterations run (`kept` vs `reverted`), each accepted fix on one line, decision log path, and next idea if pushed further.
