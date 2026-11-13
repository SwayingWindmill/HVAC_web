### Refactoring

Resolve app verification through `poteto-mode`'s Non-negotiables (`verify`, `command-safety`, or local test suites).

**You own the contract. Structure changes; behavior does not.** If cleanup reveals a missing feature or real bug, split it out cleanly rather than smuggling behavior changes inside a refactor. Large cross-cutting overhauls route to `figure-it-out`.

1. **Pin the behavior contract first.** Run `how` over the affected subsystem, then write a characterization test, snapshot, or equivalence check before moving code. Typecheck/lint alone is not a behavior pin.
2. **Name the missing structure** (`Model the Domain` in `poteto-mode`). Leave boring local code alone; a refactor must delete branches or impossible states rather than adding indirection.
3. **Name the target shape** (`Foundational Thinking`, `Redesign from First Principles`). Run `architect` if reshaping module/function boundaries.
4. **Subtract before you add.** Delete dead code, collapse one-caller wrappers, drop redundant checks (**Subtract Before You Add**, **Laziness Protocol**), and revert speculative cleanups.
5. **Migrate callers and delete legacy APIs in one wave.** Zero compatibility shims or parallel old/new paths. Spot-check renames in strings, docs, fixtures, and scripts (`git grep`). Delegate mechanical edits safely (`invoke_subagent` with `Workspace: "share"` if parallel).
6. **Prove equivalence on real artifacts** (`Prove It Works`), not just compilation: diff old vs new outputs, run tests, or verify live via `verify`.
7. **Verify lower reader load** (`Minimize Reader Load`). If complexity merely moved sideways without reducing cognitive load, revert it.
8. Rebase into small verifiable commits (**Sequence Work into Verifiable Units**) and run [Opening a PR](opening-a-pr.md).

**Reply:** structure changed, pin used, equivalence proof, reader-load reduction, and anything reverted. Zero new behavior.
