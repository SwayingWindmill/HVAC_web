# Deep-Module Lens

Shared vocabulary and deepening criteria for `improve-codebase-architecture` (aligned with [`../../architect/references/design-red-flags.md`](../../architect/references/design-red-flags.md)).

## Vocabulary

Use these terms consistently across exploration notes and HTML report cards:

- **Module**: anything with an interface and an implementation (function, package, Go pkg, TS feature slice, or tier-spanning module). Avoid drifting into "unit", "component", or "service".
- **Interface**: everything a caller or test must know to use the module correctly (signatures, invariants, ordering constraints, error modes, configuration, performance traits).
- **Implementation**: the internal code inside the module.
- **Depth**: leverage at the interface, meaning how much behavior a caller or test exercises per unit of interface it must learn. **Deep** = small interface + rich behavior; **shallow** = wide interface + thin pass-through.
- **Seam**: the location where a module's interface lives so behavior can vary without editing callers.
- **Adapter**: a concrete implementation plugged into a seam (e.g. Postgres store vs in-memory fake).
- **Leverage**: caller benefit from depth (one implementation pays back across N callers and M tests).
- **Locality**: maintainer benefit from depth (changes, bugs, and verification concentrate in one place).

## Core principles

- **The deletion test**: imagine deleting the module. If complexity vanishes, it was a shallow pass-through. If complexity reappears across N callers, it earns its keep.
- **The interface is the test surface**: callers and tests cross the same seam. Needing to test past the interface signals the wrong module boundary.
- **One adapter means a hypothetical seam; two adapters mean a real one**: do not add an abstraction seam unless something varies across it (typically production + test stand-in).

## Dependency categories (how to deepen safely)

1. **In-process**: pure logic and in-memory state, zero I/O. Merge shallow wrappers and test directly through the deepened interface.
2. **Local-substitutable**: dependencies with local test stand-ins (in-memory FS, local test DB/fixture). Keep the seam internal; test against the stand-in through the module interface.
3. **Remote but owned (Ports & Adapters)**: internal services across network/process boundaries. Define a port at the seam; inject transport adapters (HTTP/gRPC/MQTT in prod, in-memory adapter in tests).
4. **True external (Mock)**: third-party services outside our control. Accept an injected port and supply a mock adapter only at that boundary.

## Testing strategy: replace, don't layer

- Delete redundant unit tests tied to dissolved shallow wrappers once behavior tests sit at the deepened module interface.
- Assert observable outcomes through the interface so tests survive internal refactors.
