- Do not preserve backward compatibility. Remove obsolete paths instead of
  adding compatibility layers, fallbacks, or migrations.
- Choose the simplest implementation that fully meets the current
  requirements. Avoid speculative abstractions, configuration, and
  indirection.
- Grow the system in layers. Start from the smallest version that works end
  to end, and add each new capability on top of a product that already
  works. Never trade a working product for unfinished complexity.
- Keep components modular and concerns clearly separated.
- Prefer established, well-maintained libraries when they reduce overall
  complexity or improve reliability. Do not reimplement common
  functionality without a clear reason.
- Lean on the dependencies already in the project before writing your own
  implementation or adding packages. Do not assume a library lacks a
  capability without checking its documentation and types.
- Make architectural decisions for the long term. Do not accept a stopgap
  that only works for now and is meant to be replaced later.
- Reuse-first implementation: before introducing new infrastructure, libraries,
  or tooling, search GitHub and the existing dependency set for maintained
  solutions that satisfy the requirement; document the choice and pin the selected version or commit.
- Source-first reference implementation rule: when a target architecture decision
  is derived from an external reference implementation, pin an official upstream
  release/tag and commit, then read the relevant official source code, upstream
  tests, and official documentation before implementing or refactoring that
  concern. This rule also applies retroactively to local modules that were already
  written before the source review: existing code has no incumbency preference and
  remains UNVERIFIED until compared against the pinned reference implementation.
  Do not implement or retain an adopted mechanism from prose or architecture
  diagrams alone. Record the reviewed upstream files and the resulting
  ADOPT/ADAPT/REJECT decisions in the project architecture source-review record.
  If local behavior materially conflicts with the reference implementation and
  there is no documented, evidence-backed reason that the local behavior is safer,
  simpler, more maintainable, or required by HVAC/domain constraints, the pinned
  reference implementation behavior wins and the local code must be refactored to
  match it. Preserve project-specific differences only when that justification is
  explicit and reviewed. Do not copy upstream source verbatim unless license and
  provenance have been explicitly reviewed. For the current Edge Control Plane
  reference, this rule applies to OpenEMS.
- No meaningless tests. Add or retain a test only when it protects a concrete
  current product contract, safety invariant, data invariant, authorization
  boundary, externally observable behavior, or a previously observed realistic
  regression. Do not add tests merely for coverage, trivial getters/setters,
  implementation details, duplicate permutations, exhaustive fixture combinations,
  obsolete compatibility behavior, or assertions that cannot catch a meaningful
  product failure. When a contract changes, update or delete stale tests instead of
  adding compatibility code to satisfy them. Prefer the smallest direct behavioral
  test set that proves the required behavior.
- No speculative defensive programming. Add a validation, guard, fallback, retry,
  default, coercion, recovery branch, or exception handler only for a concrete
  reachable failure mode at a real trust boundary, external I/O boundary,
  persistence-corruption boundary, concurrency boundary, or safety-critical
  invariant. Do not defend against impossible states already excluded by trusted
  types, schemas, database constraints, or an upstream owner contract. Do not add
  redundant validation at every layer, silent fallbacks, catch-and-ignore logic,
  permissive defaults, or "just in case" branches. Prefer a clear failure over
  masking an invalid state. Every defensive branch must have an identifiable
  failure mode that justifies its existence; otherwise remove it.
- No gate inflation. Protect each current architectural/product invariant with the
  smallest authoritative gate that can fail for a meaningful reason. Do not add
  a new permanent CI gate, package script chain, snapshot gate, or ticket/stage
  gate when an existing domain/invariant gate can own the check. Temporary
  certification or migration gates must be removed or folded into the stable
  domain task matrix after their purpose is complete. Avoid running unrelated
  domains "just in case"; affected-path classification should select only the
  capabilities whose contracts can actually change.
