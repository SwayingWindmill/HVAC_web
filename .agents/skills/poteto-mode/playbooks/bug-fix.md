### Bug fix

**You own this task. Plan, review, verify.** Stay scientific: every shipped line traces to runtime evidence. "Might help" belt-and-suspenders guards do not ship; revert hypothesis code when evidence refutes it.

Before the fan-out in steps 2-3, check the [focused-fix exception](#focused-fix-exception). Never print runtime secrets or full configuration bundles when capturing diagnostics (`[REDACTED]` / non-sensitive fields only).

1. **Reproduce it yourself** on the matching surface via `verify` (UI), `npm run command:formal` / Go/Vitest runner, or local command execution. Ask the user only when a control surface genuinely cannot reach the target, stating why. Synthesize triggers or narrow logs if needed until it fires.
2. **Binary-search the cause.** Seed hypotheses with `how` and `why`, then eliminate halves of the problem space using runtime evidence. Do not guess. Use `schedule` only if driving a slow background hunt. Confirm the exact failing mechanism before designing the fix.
3. **Plan the fix.** Run `architect` first if the fix crosses function boundaries or changes interfaces, ownership, schema, migrations, concurrency, transactions, or routing contracts. Delegate scoped implementation to a worker with explicit paths and criteria (`invoke_subagent`).
4. **Verify on the same surface.** Confirm the original repro now passes (`verify` before/after evidence when UI). An inconclusive or wrong-surface pass is not done.
5. **Stage regression test before fix** via the `tdd` skill when a cheap local test path exists (**Sequence Work into Verifiable Units**). For security or data-integrity invariants, require an independent correctness reviewer before marking merge-ready (record explicitly if unavailable).
6. Run [Opening a PR](opening-a-pr.md).

**Reply:** what broke, root cause with evidence, minimal fix, and how you verified (quote concise failing -> passing assertions/counts without secrets or customer data).

#### Focused-fix exception

One scoped delegate may investigate the reproduced failure with a local read before invoking `how`/`why` and `architect` ONLY when all hold:

- Runtime evidence proves the cause inside one subsystem.
- Interfaces, signatures, ownership, schema, migrations, security/auth/crypto, concurrency, transactions, and routing stay unchanged.
- A cheap local regression check covers the failure and original-surface verification is available.
- No competing hypothesis or contradictory evidence remains.

Note these conditions in your checklist before taking the fast path; revert to full steps 2-3 if any condition breaks.
