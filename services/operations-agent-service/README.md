# Operations Agent service

This package is the separately deployable TypeScript boundary for the Operations Agent.
It contains the accepted modular-monolith boundary, Domain lifecycle model and public
Investigation Coordinator application seam. It does not yet connect a browser, Platform
Gateway, live model provider, LangGraph.js runtime, database, scheduler or platform tool.

The module direction is:

```text
adapters -> application -> domain
bootstrap -> adapters
```

Every module is imported through its `index.ts` entry. Cross-module imports into an
`internal` path are rejected by the repository boundary check.

The Domain module currently provides an immutable `OperationsInvestigation` aggregate.
It owns Agent Run lifecycle transitions, monotonic Investigation Revision checks,
non-reusable Agent Run Lease identities, Step Identity, Idempotency Key deduplication,
and cancellation rules that preserve committed Evidence, Findings and Proposed Actions.
Completed Investigations may be reopened only by creating a new Agent Run; terminal Runs
are never resumed.

The package root exports `createInvestigationCoordinator` as the only supported business
entry. The Coordinator reauthorizes every command against the authoritative Scope, owns
lifecycle commands, validates active Run/Lease/Revision authority through Domain behavior,
executes independent Owner READ batches in parallel, validates returned Owner identity,
Scope, Revision, Quality and provenance, stores only opaque Runtime Checkpoint state and serializes every
Evidence, Finding or Proposed Action effect through Step Identity and Idempotency Key.
Runtime, persistence, Owner readers, Outbox, Audit, budget, clock and identity capabilities
remain narrow Application ports with no framework or transport types. Business aggregate
writes, Outbox append and Audit append are represented by one `InvestigationTransaction`
port so a concrete persistence adapter cannot commit only part of the business mutation.

Run the package checks from the repository root:

```bash
npm --prefix services/operations-agent-service run check
```

The emitted package is written to `services/operations-agent-service/dist` and is not
source controlled.
