### Feature

**You own the design. Plan, review, verify.** Delegate implementation where beneficial and stay in the lead.

1. `how` over the affected subsystem (`why` if touching historical defenses or thresholds, `GLOSSARY.md` + `docs/adr/` check).
2. `architect` for parallel design exploration when crossing function/module boundaries, plus `blast-radius` when touching shared contracts or existing invariants.
3. Write the throughput checkpoint as four todo items (write `n/a: <reason>` if single-file/no fan-out rather than dropping items):
   - **Blocking first steps.** Gates/grounding before fan-out.
   - **Independent workstreams.** Disjoint files/services/layers parallelize; shared writes serialize.
   - **Shared mutable state.** Split the target cleanly before parallelizing (**Separate Before Serializing Shared State** in `poteto-mode`).
   - **Smallest safe decomposition.** If one worker is best, name why.
4. Delegate coding with an explicit scope: file paths, named data shape and organizing structure (**Model the Domain**: discriminated union / state machine / table / typed boundary chosen before implementation), and success criteria.
   - When multiple valid implementation shapes exist, run the sibling `arena` skill and pick a coherent base.
   - Give parallel writers isolated workspaces (`Workspace: "share"` or separate `git worktree add <path> -b <branch> HEAD`). Never rely on prose alone to prevent concurrent writes to the same working tree.
   - Follow **Comments** rules and keep edits minimal; align shared primitives across consumers and verify each.
5. Verify on the matching surface: web UI via `verify` at 1440px (`.scratch/verify/`), command/MQTT invariants via `command-safety` (`npm run command:formal`), backend/CLI/unit via `scripts/domain-task-matrix.ts` commands in WSL. "Inconclusive" or wrong-surface is not a pass.
6. Rebase/organize into small verifiable commits (**Sequence Work into Verifiable Units**).
7. If the design is contested or risky, run `interrogate` before opening/merging.
8. Run [Opening a PR](opening-a-pr.md).

Code-coupled work (one feature, one migration) belongs to one owner who fans out internally after blocking steps. Use fresh subagents at phase boundaries.

**Reply:** what changed for operators/maintainers, what you chose and why, the throughput checkpoint, open decisions or follow-ups, and verified evidence.
