# Operations Agent service

This package is the separately deployable TypeScript boundary for the Operations Agent.
It contains the accepted modular-monolith boundary, Domain lifecycle model, public
Investigation Coordinator application seam, PostgreSQL persistence adapter, the first
explicit LangGraph.js `AgentExecutionRuntime` adapter and authoritative Registry/Energy
READ adapters. It does not yet connect a browser, Platform Gateway, live model provider or
scheduler.

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

The LangGraph adapter compiles a project-owned `StateGraph` with explicit validation,
selection, READ-plan emission and terminal transitions. Its versioned `runtime-state/v1`
contains only Investigation/Run identity, Runtime Revision, the next Step index and completed
Step identities. The Coordinator loads and authorizes the Operations Investigation before it
loads this opaque Checkpoint. Repeating the same Checkpoint is deterministic; a mismatched
Run, Runtime Revision, state prefix or external position fails closed. Runtime nodes emit
READ plans only and cannot commit Evidence, Findings or Proposed Actions.

The `tools` module implements two narrow authoritative READ boundaries:

```text
registry.getSite / registry.listSiteEquipment
  -> Platform Core Service Registry routes only

analytics.getEnergySeries
  -> Telemetry Query Service Energy Series product route only
```

The Coordinator injects the current Investigation/Run identity, complete Scope, authorization
decision, delegation grant and correlation information. Registry results are strictly decoded
from the project Site and Equipment DTOs. Energy results retain Dataset Revision, data and
aggregate watermarks, partial state, requested/actual granularity and Quality Summary. Both
readers apply bounded response sizes and timeouts, map upstream failures to stable Application
errors and keep unauthorized resources nondiscoverable. The boundary check rejects direct
ClickHouse, Cube, ThingsBoard and Command API paths in Operations Agent tools.

PostgreSQL is split into two independently migrated and authorized Schemas:

```text
agent_operations
  Operations Investigation snapshots, committed effect journal, Outbox and Audit
  runtime identity: operations_agent_operations_runtime

agent_checkpoints
  opaque Runtime Checkpoints and expiry metadata only
  runtime identity: operations_agent_checkpoints_runtime
```

The Operations transaction locks the current Investigation row, checks the expected
Revision and optional Run Lease, rejects rewritten Step/Idempotency history, and commits the
new snapshot, Effect, Outbox and Audit atomically. Checkpoint deletion or expiry has no
foreign key or cascade into business records. The persisted snapshot is restored through
Domain validation and contains no LangGraph type.

The deterministic application acceptance environment supplies a scripted Fake Runtime,
typed Fake Owner readers, an in-memory business transaction and a disposable Fake
Checkpoint store. It exercises the package only through `createInvestigationCoordinator`.
The Runtime receives an isolated Investigation View, so attempted mutation cannot change the
authoritative aggregate or Owner Scope validation.

Run the package checks from the repository root:

```bash
npm --prefix services/operations-agent-service run check
npm --prefix services/operations-agent-service run test:acceptance
npm run operations-agent-service:postgres

OPERATIONS_AGENT_OPERATIONS_MIGRATOR_DATABASE_URL=... \
  npm --prefix services/operations-agent-service run migrate:operations
OPERATIONS_AGENT_CHECKPOINTS_MIGRATOR_DATABASE_URL=... \
  npm --prefix services/operations-agent-service run migrate:checkpoints
```

The emitted package is written to `services/operations-agent-service/dist` and is not
source controlled.
