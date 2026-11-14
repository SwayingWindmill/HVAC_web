### Trace forensics

**You own the diagnosis from the existing artifact.**

Distinct from [`runtime-forensics.md`](runtime-forensics.md), which instruments a live process: here the capture (`.cpuprofile`, `.heapsnapshot`, Playwright trace `.zip`, Go `pprof`, logs) already exists. Read it rather than guessing or re-running needlessly.

1. Identify the artifact format and inspect large captures inside a subagent (`guard the context window`) so huge JSON dumps stay out of main context.
2. Convert or index raw trace samples into a queryable form (e.g., Go `go tool pprof -top/-traces`, Python/Node summary script in `.scratch/`, Playwright trace inspector).
3. Narrow down to the dominant hotspot, retainer chain to GC root, or stalling span.
4. Map hot frames back to exact source file, symbol, and line numbers (state explicitly if debug symbols or source maps are missing).
5. If before/after paired captures exist, diff them directly to confirm; otherwise report the finding honestly as the strongest hypothesis supported by the trace.
6. Hand back a cited diagnosis with no speculative code edits unless asked. Keep checkpoint concise (`throughput checkpoint: n/a, read-only forensics`).

**Reply:** artifact format and path, reduced finding, exact source location, and whether paired captures confirmed the cause.
