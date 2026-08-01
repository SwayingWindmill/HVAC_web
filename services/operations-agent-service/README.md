# Operations Agent service

This package is the separately deployable TypeScript boundary for the Operations Agent.
It contains the accepted modular-monolith boundary, Domain lifecycle model, public
Investigation Coordinator application seam, deterministic night-energy analysis, typed
business-record contracts, PostgreSQL persistence adapter, the first explicit LangGraph.js
`AgentExecutionRuntime` adapter, authoritative Registry/Energy READ adapters and the
Site night-energy application/HTTP contract and committed AG-UI event projection exposed through
Platform Gateway. The existing React/Vite Real Shell now consumes the first bounded Operations
Workspace slice through CopilotKit Headless. A project-owned `FindingSynthesizer`, strict structured
output validation, deterministic fallback, Fake Provider and default-disabled OpenAI Responses API
adapter are implemented; reconnect cursor and scheduler remain out of this slice.

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
and cancellation rules that preserve committed Evidence, Analysis References, Findings,
Tool Execution Receipts and Proposed Actions. Completed Investigations may be reopened only
by creating a new Agent Run; terminal Runs are never resumed.

The package root exports `createInvestigationCoordinator`, the framework-independent
`analyzeSiteNightEnergy` Application seam and the versioned typed business-record contracts.
The Coordinator reauthorizes every command against the authoritative Scope, owns lifecycle
commands, validates active Run/Lease/Revision authority through Domain behavior, executes
independent Owner READ batches in parallel, validates returned Owner identity, Scope,
Revision, Quality and provenance, stores only opaque Runtime Checkpoint state and serializes
Evidence, Analysis Reference, Finding, Tool Execution Receipt and Proposed Action effects
through Step Identity and Idempotency Key. Evidence, Analysis Reference, Finding and Tool
Receipt effects require a validated typed record; exact replay returns the persisted content
without advancing Revision or duplicating journals. Runtime, persistence, Owner readers,
Outbox, Audit, durable per-Run resource budget, clock and identity capabilities remain narrow Application ports with
no framework or transport types. Business aggregate writes, typed record insert, Outbox
append and Audit append are represented by one `InvestigationTransaction` port so a concrete
persistence adapter cannot commit only part of the business mutation.

The LangGraph adapter compiles a project-owned `StateGraph` with explicit validation,
selection, READ-plan emission and terminal transitions. Its versioned `runtime-state/v1`
contains only Investigation/Run identity, Runtime Revision, the next Step index and completed
Step identities. The Coordinator loads and authorizes the Operations Investigation before it
loads this opaque Checkpoint. Repeating the same Checkpoint is deterministic; a mismatched
Run, Runtime Revision, state prefix or external position fails closed. Runtime nodes emit
READ plans only and cannot commit Evidence, Findings or Proposed Actions.

The logical Tool catalog and trusted Runtime Context are versioned under
`contracts/operations-agent/`. Generated Domain/Application constants define the Runtime READ
union, Receipt owner mapping, exact context keys and immutable control policy. The package
`contracts` check also verifies Benchmark, Web and OpenAPI projections before TypeScript or tests run.

The first Model slice is limited to Finding presentation. `FindingSynthesizer` receives exact Scope,
committed Evidence, committed Analysis References and a deterministic fallback statement. The
Application layer accepts only the exact `finding-synthesis-output/v1` shape, rechecks every Evidence
identity, rejects execution claims and confirmed conclusions when the deterministic result is unable
to conclude, and falls back on missing configuration, timeout, Provider failure or invalid output.
Finding kind, conclusion, analysis authority and committed effects remain deterministic. Bounded
invocation provenance is stored atomically inside the Finding JSONB record and restored through
Domain validation; public HTTP and AG-UI projections omit it. Raw prompts and raw Provider responses
are never persisted.

The production OpenAI adapter uses the official SDK, sends one strict JSON Schema request through the
Responses API, sets `store: false`, exposes no Tools, streaming or background mode, and binds retries
to a deterministic idempotency key derived from the bounded input and non-secret configuration digest.
A real Provider attempt first consumes one durable Run `modelInvocations` unit; budget exhaustion blocks
the external request and Finding commit. It is disabled unless all of the following server environment
values are valid:

```text
OPERATIONS_AGENT_FINDING_MODEL_PROVIDER=openai
OPERATIONS_AGENT_FINDING_MODEL=<exact model identifier>
OPERATIONS_AGENT_FINDING_MODEL_ALLOWLIST=<comma-separated exact identifiers>
OPERATIONS_AGENT_FINDING_MODEL_TIMEOUT_MS=<100..30000, default 5000>
OPERATIONS_AGENT_FINDING_MODEL_MAX_OUTPUT_TOKENS=<64..2048, default 512>
OPENAI_API_KEY=<server credential>
```

`createEnvironmentConfiguredSiteNightEnergyInvestigationCoordinator` is the production composition
entry. Missing Provider configuration keeps the deterministic fallback. Partial configuration,
unsupported Providers, model/allowlist mismatch, conflicting explicit injection and conflicting
timeout policy fail during composition before any investigation or external request is created.

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

Platform Gateway owns the browser-facing Site Investigation routes and applies the BFF Session,
Origin/CSRF, Site-visibility, request-size, timeout and per-Session rate boundaries. It signs a
short-lived Site-scoped Operations service delegation for the internal Operations Agent HTTP
contract. Immediately before each concrete Registry or Energy READ, the Operations Agent calls
the Gateway's mTLS-only Tool Authorization route to exchange that service delegation for the
exact Owner grant. IAM signs Registry grants directly for the Operations Agent presenter and
keeps them non-transitive; Gateway signs Energy grants with Operations Agent as the explicit
executingService and the normalized query digest as Scope. This exchange also applies during
Checkpoint recovery replay. Browser headers, raw Energy points, Leases, Checkpoints and
LangGraph state never cross the public Investigation contract.

The internal and public Investigation APIs also expose a GET `/events` route. It does not stream
Runtime state: it reads the current authorized Investigation View and returns a finite SSE batch
with one committed `STATE_SNAPSHOT`, bounded read-only Tool activity and lifecycle events. Both
service and Gateway validate the whitelist; the Gateway adds `no-store, no-transform`, disables
proxy buffering and rejects malformed or unsafe events before they reach the browser. Cursor-based
reconnect and missed-event replay are intentionally left to the following Map 4 slice.

`analyzeSiteNightEnergy` consumes that shared versioned Energy Series contract plus a typed
Registry Site Scope. It deterministically resolves local target and baseline night windows,
including daylight-saving elapsed durations, and requires contiguous hourly buckets. Dataset
Revision, both Watermarks, Partial, Granularity, Quality Policy, missing buckets, non-finite
energy and zero baseline all gate confirmation. Complete benchmark data produces FACT and
ALGORITHM_RESULT drafts plus a Site-only Finding; Equipment attribution remains structured
`UNABLE_TO_CONCLUDE` with REQUIRED_NEXT binding and Equipment-series requests. The result
contains a canonical SHA-256 Analysis reference and metadata summaries, not raw point arrays,
and has no Proposed Action or Command path.

PostgreSQL is split into two independently migrated and authorized Schemas:

```text
agent_operations
  Operations Investigation snapshots, typed business records, committed effect journal,
  Outbox, Audit, per-Run resource budgets and accepted budget operation identities
  runtime identity: operations_agent_operations_runtime

agent_checkpoints
  opaque Runtime Checkpoints and expiry metadata only
  runtime identity: operations_agent_checkpoints_runtime
```

The per-Run resource guard stores an immutable policy revision and fixed limits for model
invocations, Tool requests, wall-clock duration, query range/buckets, Owner records and payload
bytes. It locks the Run budget row for every check and journals stable logical operation IDs, so
concurrent workers serialize, exact retries do not double count, and process restart or Checkpoint
deletion cannot reset counters. Exhaustion blocks new external work and business effects and
projects only a bounded PARTIAL or UNABLE_TO_CONCLUDE result through Gateway and AG-UI.

The Operations transaction locks the current Investigation row, checks the expected
Revision and optional Run Lease, rejects rewritten Step/Idempotency history, validates
record Scope and support references, and commits the new snapshot, typed record, Effect,
Outbox and Audit atomically. Tool Receipts additionally enforce bounded metadata and unique
Owner/Request/Attempt identities. Checkpoint deletion or expiry has no foreign key or cascade
into business records. The persisted snapshot is restored through Domain validation and
contains no LangGraph type.

The deterministic application acceptance environment supplies a scripted Fake Runtime,
typed Fake Owner readers, an in-memory business transaction and a disposable Fake
Checkpoint store. It exercises the package only through `createInvestigationCoordinator`.
The Runtime receives an isolated Investigation View, so attempted mutation cannot change the
authoritative aggregate or Owner Scope validation.

Run the package checks from the repository root:

```bash
npm run operations-agent:contracts:check
npm --prefix services/operations-agent-service run check
npm --prefix services/operations-agent-service run test:acceptance
npm run operations-agent-service:postgres
npm run operations-agent:gateway:check
npm run test:gateway

OPERATIONS_AGENT_OPERATIONS_MIGRATOR_DATABASE_URL=... \
  npm --prefix services/operations-agent-service run migrate:operations
OPERATIONS_AGENT_CHECKPOINTS_MIGRATOR_DATABASE_URL=... \
  npm --prefix services/operations-agent-service run migrate:checkpoints
```

The emitted package is written to `services/operations-agent-service/dist` and is not
source controlled.
