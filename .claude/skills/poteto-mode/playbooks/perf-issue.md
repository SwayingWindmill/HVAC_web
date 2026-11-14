### Perf issue

**You own the measurement story. Plan, review, verify the numbers.** Tie every fix to a measurement; never guess from source instead of profiling.

1. Capture a baseline trace/measurement on the matching surface (Go `pprof`, browser trace via [`verify`](../../verify/SKILL.md) / Playwright, `node --cpu-prof`, or realistic load script). Vet the baseline and every subsequent number with [`benchmark-checklist`](../../benchmark-checklist/SKILL.md).
2. Run [`how`](../../how/SKILL.md) to ground hypotheses. Never claim a ceiling without measuring first. Try performance mantras cheapest-first:
   1. Don't do it (drop work whose result nothing consumes).
   2. Do it, but don't do it again (cache/reuse safely).
   3. Do it less (batch, narrow queries, trim allocations).
   4. Do it later (defer off critical path).
   5. Do it when nobody waits (background/async safely).
   6. Do it concurrently (within real I/O or core bounds).
   7. Do it cheaper (tighten inner algorithm/data representation).
   Stop as soon as an earlier mantra meets the target.
3. Plan the fix from the profile. If it crosses a function or module boundary, run [`architect`](../../architect/SKILL.md) first. Delegate or implement in small verified units (`Model` per [`agy-tools.md`](../references/agy-tools.md)), inspect the diff, and capture a post-fix trace.
4. Parse and compare before/after artifacts objectively. `"Inconclusive"` or wrong-surface benchmarks do not pass; flag gaps explicitly.
5. Cite the verified before/after metric and measurement scope in the PR body.
6. Run [`opening-a-pr.md`](opening-a-pr.md).

For sustained iterative optimization against a target rather than a one-off fix, use [`hillclimb.md`](hillclimb.md).

**Reply:** baseline number, post-fix number, delta, limiter, and artifact paths.
