# Operations Agent service

This package is the separately deployable TypeScript boundary for the Operations Agent.
It currently contains only the accepted modular-monolith skeleton and automated inward
dependency rules. It does not connect a browser, Platform Gateway, model provider,
LangGraph.js runtime, database, scheduler or platform tool.

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

Run the package checks from the repository root:

```bash
npm --prefix services/operations-agent-service run check
```

The emitted package is written to `services/operations-agent-service/dist` and is not
source controlled.
