### Runtime forensics

**You own the diagnosis. Instrument the live process, don't theorize from source.** The deliverable is a cited diagnosis, not a fix.

1. Capture a real live signal on the matching surface (Go `pprof`, browser performance/console/network trace via [`verify`](../../verify/SKILL.md) / Playwright, `node --cpu-prof`, or process metrics). Save artifacts under `.scratch/forensics/`.
2. Reduce large captures inside a subagent (`guard the context window`) to extract the concrete hot path, leaked retainer chain, or stuck goroutine/event loop.
3. Prove the mechanism before declaring root cause (controlled trigger, live runtime evaluation, or minimal reversible instrumentation that isolates the firing condition).
4. Map the finding back to exact file, symbol, and line numbers.
5. Keep checkpoint brief (`throughput checkpoint: n/a, read-only forensics`) unless fixes were explicitly requested.

**Reply:** signal captured, reduced finding, how mechanism was proved, exact source location, and `.scratch/forensics/` artifact paths. Hand back to `bug-fix.md` or `perf-issue.md` once cause is known.
