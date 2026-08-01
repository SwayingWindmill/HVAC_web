# Operations Agent framework architecture

Status: accepted design

Date: 2026-07-30

This document defines the modular architecture for the future TypeScript Operations Agent. It applies ADR 0009 and ADR 0010. It fixes boundaries and dependency rules only; no runtime, package, schema or service is implemented by this document.

## 1. Architectural intent

The Operations Agent coordinates authorized industrial investigations. It is not a replacement for Registry, Telemetry Runtime, Analytics, IAM, Command or Audit. Its product value comes from organizing evidence, analysis, findings and proposed next actions while preserving the authority of existing platform modules.

The design follows five rules:

1. Domain behavior is independent of Agent frameworks.
2. Execution state is separate from business state.
3. External facts are accessed through typed owner contracts.
4. Model output is advisory until deterministic application rules accept it.
5. UI streams project committed state rather than becoming state authority.

## 2. System context

```text
┌─────────────────────────────────────────────────────────────┐
│ apps/hvac-web                                               │
│ Operations Workspace + CopilotKit headless primitives       │
└──────────────────────────────┬──────────────────────────────┘
                               │ public HTTP / AG-UI stream
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ platform-gateway                                            │
│ Session, CSRF, Origin, Organization Scope, delegation,       │
│ public rate limits and error mapping                        │
└──────────────────────────────┬──────────────────────────────┘
                               │ internal authenticated calls
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ operations-agent-service                                    │
│ Domain + Application + replaceable adapters                 │
└───────┬───────────────┬───────────────┬───────────────┬─────┘
        │               │               │               │
        ▼               ▼               ▼               ▼
 Platform Core   Telemetry Runtime  Telemetry Query   IAM / Command / Audit
 Registry facts  Current truth      Historical product Governed decisions
                                    queries
                                            │
                                            ▼
                                      Cube / ClickHouse
                                      behind Query Service
```

Forbidden direct paths remain:

```text
Browser -> Operations Agent without Platform Gateway
Browser -> model provider
Operations Agent -> ClickHouse SQL
Operations Agent -> arbitrary Cube query
Operations Agent -> ThingsBoard read-through
Operations Agent -> provider connector
Operations Agent -> direct physical command
AG-UI or model trace -> authoritative business or audit state
```

## 3. Deployment unit and internal modules

The service is one independently deployable TypeScript modular monolith.

```text
services/operations-agent-service/
├── src/
│   ├── domain/
│   ├── application/
│   ├── runtime-langgraph/
│   ├── model/
│   ├── tools/
│   ├── persistence/
│   ├── transport-ag-ui/
│   ├── scheduling/
│   ├── observability/
│   └── bootstrap/
└── test/
    ├── domain/
    ├── application/
    ├── adapters/
    └── contract/
```

The directory names describe module responsibility, not mandatory implementation file count. Each module should expose a small public surface and hide its internal structure.

The service is implemented at `services/operations-agent-service` as an independent npm package with NodeNext TypeScript build output, one external package root, module-local `index.ts` entries and a repository-owned AST boundary check. Domain, Application, PostgreSQL persistence, the first explicit LangGraph.js `AgentExecutionRuntime`, authoritative Registry/Energy READ adapters, public/internal HTTP contracts, committed AG-UI projection, Platform Gateway integration and the first Real Shell Operations Workspace slice are implemented. A project-owned `FindingSynthesizer`, strict output validation, deterministic fallback and Fake Provider are implemented; a live external model provider, cursor-based reconnect and scheduler remain outside the current slice. Run its complete local gate with `npm run operations-agent-service:check`.

### 3.1 Dependency direction

```text
transport-ag-ui ─┐
runtime-langgraph ├──> application ───> domain
scheduling ──────┤
bootstrap ───────┘

persistence ─────> application ports / domain types
tools ───────────> application ports / domain types
model ───────────> application ports / domain types
observability ───> adapter interfaces
```

Allowed dependencies:

- `domain` depends only on TypeScript and project-owned domain primitives.
- `application` depends on `domain` and project-owned ports.
- adapters depend on application ports and may use external libraries.
- `bootstrap` composes adapters and application services.

Forbidden dependencies:

- `domain` importing LangGraph, LangChain, AG-UI, CopilotKit, HTTP, SQL, ORM or provider packages;
- `application` importing concrete PostgreSQL, model-provider or transport implementations;
- tools importing another tool implementation rather than an application port;
- frontend protocol types appearing in persisted business records;
- public HTTP contracts exposing graph node, checkpoint or provider message types.

## 4. Domain module

The Domain module owns the language and invariants of Operations Investigations.

### 4.1 Aggregate boundary

The primary aggregate is `OperationsInvestigation`.

It governs:

```text
Investigation identity and lifecycle
Investigation Revision
authorized operational Scope reference
current high-level Plan
Evidence membership and classification
Finding membership and status
Proposed Action membership and status
active Agent Run identity
cancellation and completion rules
```

Large Evidence payloads or analytical datasets are not embedded in the aggregate. The aggregate stores bounded references, provenance and status required to enforce invariants.

### 4.2 Domain records

```text
OperationsInvestigation
InvestigationPlan
InvestigationStep
InvestigationEvidence
InvestigationFinding
ProposedAction
VerificationResult
ToolExecutionReceipt
OperatorProvidedInformation
```

These are product concepts, not LangGraph state objects.

The initial Domain implementation now owns Investigation creation, Agent Run start/pause/resume/cancel/complete/fail transitions, explicit reopening of completed Investigations through a new Run, monotonic Investigation Revision checks, non-reusable Agent Run Lease identities and idempotent Evidence/Finding/Proposed Action membership. Step Identity and Idempotency Key remain separate value types: one Step may produce multiple effects, while one Idempotency Key can bind only one effect.

### 4.3 Finding classification

A Finding must identify one of:

```text
FACT
ALGORITHM_RESULT
INFERENCE
HYPOTHESIS
UNABLE_TO_CONCLUDE
```

The domain prevents a hypothesis from being promoted to a confirmed fact without accepted supporting Evidence. A failed readiness gate requires `UNABLE_TO_CONCLUDE` or a request for additional Evidence rather than fabricated certainty.

### 4.4 Proposed Action semantics

A Proposed Action is reviewable advice. It is not:

- formal approval;
- a Command Intent;
- a command execution attempt;
- a work order unless accepted by the owning module;
- evidence that a physical effect occurred.

The domain may record that a proposal was accepted for submission. The resulting external domain object remains owned by its service.

## 5. Application module

The Application module is the only supported orchestration boundary for external adapters.

### 5.1 Primary interface

The intended deep interface is conceptually:

```ts
interface InvestigationCoordinator {
  start(command: StartInvestigation): Promise<InvestigationView>;
  continue(command: ContinueInvestigation): Promise<InvestigationView>;
  submitInput(command: SubmitInvestigationInput): Promise<InvestigationView>;
  cancel(command: CancelInvestigation): Promise<InvestigationView>;
  get(query: GetInvestigation): Promise<InvestigationView>;
}
```

Exact transport schemas are deferred. The important constraint is that callers never invoke LangGraph or checkpoint APIs directly.

The initial implementation exports `createInvestigationCoordinator` from the package root. Its public commands cover creation, start, completed-Investigation reopen, READ advancement, effect commit, pause, resume, cancel, complete, fail and query. Every command reauthorizes the Investigation's authoritative Scope through an application port before state or data is returned. Runtime planning can return only typed parallel READ batches, an inability-to-conclude result and opaque Checkpoint state. It cannot return or persist Investigation Evidence, Findings or Proposed Actions. Those effects require a Coordinator command carrying the active Run, Agent Run Lease, expected Investigation Revision, Step Identity and Idempotency Key. Exact retries return the existing committed effect without another Repository write.

The current Application ports cover authorization decisions, the business Investigation Repository, an atomic Investigation Transaction, Agent execution planning, Runtime Checkpoints, Registry/Telemetry/Analytics/Command READ owners, a durable per-Run resource budget guard, Outbox append, Audit recording, clock and identity generation. The Investigation Transaction commits the new aggregate revision, Outbox event and Audit record as one unit; Map 2.4 must implement that contract in PostgreSQL. The ports contain no LangGraph, database, AG-UI, CopilotKit or model-provider types.

### 5.2 Application responsibilities

```text
resolve and validate caller Scope
acquire and release Agent Run Lease
load Investigation at expected revision
start or resume the runtime through a port
commit Evidence, Findings and Proposed Actions
coordinate idempotency and Tool Execution Receipts
publish committed application events
map domain errors to transport-neutral results
```

### 5.3 Ports

The Application module should define narrow ports such as:

```text
InvestigationRepository
InvestigationTransaction
AgentExecutionRuntime
InvestigationModel
RegistryReader
CurrentTelemetryReader
EnergyAnalyticsReader
AuthorizationDecisionReader
CommandCapabilityReader
DomainActionSubmitter
KnowledgeReader
ApplicationEventPublisher
AgentRunScheduler
Clock
IdGenerator
```

A port is added only when a real seam exists. Domain-specific readers are preferred over one generic `ToolClient` or `PlatformClient` interface.

### 5.4 Deterministic Site night-energy analysis

The package root also exposes `analyzeSiteNightEnergy` as a framework-independent Application
seam. It accepts only the project-owned Site Scope, local night window and versioned Energy
Series contract shared with the Telemetry Query Service adapter. Model-generated totals,
generic query payloads and unvalidated result shapes are rejected before analysis.

The `site-night-energy-comparison/v1` algorithm resolves target and baseline local windows
through the Registry timezone, including seven-hour spring-forward and nine-hour fall-back
windows. It requires contiguous hourly buckets and matching elapsed window durations. Dataset
Revision, data and aggregate Watermarks, Partial, requested/actual Granularity and Quality
Summary are blockers before any confirmatory Site Finding is created. A zero baseline,
non-finite energy or missing bucket also produces a structured `UNABLE_TO_CONCLUDE` result.

A supported result contains small FACT and ALGORITHM_RESULT drafts, a Site-only Finding draft,
the adopted windows, metadata summaries and a canonical SHA-256 Analysis reference digest.
Raw Energy Series points are hashed but are not copied into the result. Site aggregate change
never establishes Equipment causality: Equipment attribution remains `UNABLE_TO_CONCLUDE`
with REQUIRED_NEXT requests for canonical Equipment energy bindings and comparable
Equipment-level series. The analyzer cannot create Proposed Actions, Formal Approvals, Command
Intents or physical execution claims.

## 6. Runtime LangGraph adapter

LangGraph.js is an adapter implementing `AgentExecutionRuntime`.

### 6.1 Explicit graph

The initial graph shape is bounded:

```text
START
  │
  ▼
authorize_scope
  │
  ▼
classify_request
  │
  ▼
build_or_revise_plan
  │
  ▼
select_next_step ◄────────────────────────┐
  │                                       │
  ├── gather_registry_evidence            │
  ├── gather_current_evidence             │
  ├── gather_analytics_evidence            │
  ├── run_deterministic_analysis           │
  └── request_operator_input               │
  │                                       │
  ▼                                       │
evaluate_evidence ── needs more evidence ─┘
  │
  ▼
synthesize_findings
  │
  ▼
validate_findings
  │
  ├── invalid or unsupported -> revise plan
  ├── operator input required -> interrupt
  └── complete -> END
```

Graph changes may refine these nodes, but they must preserve bounded transitions and application-owned effects.

The first implemented adapter uses the exact `@langchain/langgraph` 1.4.8 release under its MIT license. It was selected because its `StateGraph` supplies the requested explicit node/edge execution runtime without replacing the project-owned Domain, Application, authorization or persistence contracts. The graph contains explicit validation, next-Step selection, READ-plan emission and terminal nodes and implements only the project-owned `AgentExecutionRuntime` port. The package root and the Application module do not expose LangGraph state, node names, Checkpoint classes or provider messages.

### 6.2 Untrusted-content boundary

Operator text, bounded Operator notes, Owner-returned labels or metadata, retrieved content and model output are untrusted data. They never become Runtime control, authorization context, Scope, budget policy, logical Tool catalog or business-effect policy. The Application layer is the only control source.

Before Runtime execution, the Coordinator creates `trusted-runtime-context/v1`. It contains only Investigation and active Run identities, exact authorized Scope, immutable Revision, Runtime Revision, a fixed READ-only Tool allowlist and explicit policy literals. The context and its nested Scope and Tool list are frozen. Full Investigation records, accepted Operator notes, Owner payloads, Evidence statements, Findings, prompts and provider messages are excluded.

The canonical definitions live in `contracts/operations-agent/tool-catalog.v1.json` and `trusted-runtime-context.v1.schema.json`. Repository generation projects the Runtime Tool union, Receipt owner map, exact keys and policy literals into Domain, Application, Benchmark and Web artifacts. The generation check also compares the public Tool Receipt enum and internal Tool Authorization variants, so aliases, case changes or unsupported Tool additions cannot enter one boundary independently.

The LangGraph adapter validates the exact context again at runtime. Extra fields, forged source or trust markers, duplicate or unsupported Tools and malformed Scope fail with `TRUST_BOUNDARY_INVALID`. Runtime output crosses a second exact contract in the Coordinator: only bounded planning results, batches, READ requests and Checkpoint metadata are accepted. An extra field, unsupported Tool, invalid fixed input or Scope widening attempt produces `UNTRUSTED_CONTENT_REJECTED` before authorization exchange, Owner work, Checkpoint persistence, Outbox publication, Audit recording or business effects.

Arbitrary prompt fields and raw untrusted content are rejected by transport and record schemas. The typed `operatorNote` remains a bounded committed Operator fact for auditability and display, but is never fed back into Runtime control. AG-UI and Platform Gateway reject Runtime policy fields, prompts, instructions, model output and raw Owner payloads from public projections.

### 6.3 Runtime state

Runtime state contains only what is needed to continue execution:

```text
program identity
Investigation identity
Agent Run identity
immutable Runtime Revision
next Step index
ordered completed Step identities
```

Runtime state must not become the only copy of committed Evidence, Findings or Proposed Actions.

The initial `runtime-state/v1` encodes that bounded state as opaque JSON in the existing project-owned Checkpoint repository. Recovery first restores and authorizes the Operations Investigation, verifies the active Run and Lease, then loads a Checkpoint for the same Investigation, Run and Runtime Revision. Checkpoint identity, state prefix and external position must agree or recovery fails closed. Reusing the same Checkpoint produces the same next READ Plan.

### 6.4 Interrupt rules

LangGraph interrupt is allowed for:

- missing operator input;
- Scope clarification;
- investigation direction choice;
- confirmation to create a proposal;
- safe continuation of an expensive analysis.

Interrupt is not formal approval for Command, work order or another governed action.

A node containing an interrupt may replay on resume. Therefore side-effecting application operations must occur in separate idempotent nodes before or after the interrupt, never as an unguarded effect in the same replayable section.

### 6.5 Runtime revision

Every new Investigation is assigned one runtime revision. A run does not switch graph implementation mid-execution. Restart under another revision loads authoritative Investigation state and begins a new Agent Run.

## 7. Model module

The Model module hides provider and LangChain model classes behind project-owned interfaces.

Recommended capability interfaces:

```text
RequestClassifier
InvestigationPlanner
NextStepSelector
FindingSynthesizer
ResponseComposer
```

The interface should use Zod-validated project types and return structured decisions. A single large `invoke(messages)` interface is not sufficient for core product decisions because it hides policy and makes evaluation difficult.

The current implementation exposes only `FindingSynthesizer`. It receives exact Investigation Scope, committed Evidence, committed Analysis References and the deterministic statement. A Provider candidate can refine the bounded statement and select a subset of already committed Evidence identities; it cannot return Tools, Scope, Finding kind, conclusion, approvals or effects. Application validation requires the exact `finding-synthesis-output/v1` shape, rejects unknown or duplicate Evidence, extra fields, execution claims and confirmatory claims when deterministic analysis is unable to conclude, and falls back on missing configuration, timeout, Provider error or invalid output. The Model module contains both a Fake Provider for tests and a default-disabled OpenAI Responses API adapter behind the same project interface.

The accepted synthesis decision persists bounded invocation provenance atomically inside the Finding business record: source, Provider/model identifiers, configuration digest, policy/schema versions, application-computed input/output digests, latency, bounded metering, trace identity and fallback reason. Exact Domain validation rejects extra fields and contradictory states. Public Investigation and AG-UI projections explicitly omit this provenance, and raw prompts or raw Provider responses are never stored.

Every material decision records bounded metadata:

```text
prompt policy version
structured-output schema version
model provider and identifier
model configuration digest
input digest
output digest
latency and token usage
trace correlation identity
```

Raw platform datasets, secrets and unrestricted prompts must not be persisted by default.

The OpenAI adapter is an optional composition concern, not a Domain or Application dependency. It uses
the official SDK, `store: false`, strict JSON Schema output, no Tools, no streaming, no background mode,
zero SDK retries and one deterministic idempotency key derived from the bounded request plus the
non-secret configuration digest. Runtime enablement requires an explicit Provider, an exact model
identifier present in an explicit allowlist, the standard server credential, and bounded timeout/output
settings. Every real Provider attempt first consumes the active Run's durable `modelInvocations`
budget under a stable Finding operation identity; exhaustion prevents the external request and Finding
commit. Missing configuration preserves deterministic behavior; partial, contradictory or unsupported
configuration fails closed before a coordinator or Provider request is created.

## 8. Tool modules

Tools are application adapters grouped by domain owner, not by model-provider format.

```text
tools/
├── registry/
├── current-telemetry/
├── energy-analytics/
├── authorization/
├── command/
├── audit/
└── knowledge/
```

Each tool module contains:

```text
application-facing port implementation
strict project-owned request and DTO validation
scoped delegation and correlation propagation
fixed owner-product call
owner-error translation
Evidence provenance mapping
bounded response, timeout, logging and metrics
```

Tool modules do not contain investigation planning or Finding synthesis.

The first implemented READ adapters expose only `registry.getSite`,
`registry.listSiteEquipment` and `analytics.getEnergySeries`. Registry calls are fixed to the
Platform Core Service Site and Site Equipment routes. Historical energy calls are fixed to the
Telemetry Query Service Energy Series product contract with Organization, Site, time range,
timezone, granularity, electricity energy type and Quality Policy. The Coordinator injects the
current authorization decision, complete Scope, Investigation/Run identity and correlation
context; the Runtime cannot supply delegation or service routing. Responses are strictly
validated and mapped to project-owned `OwnerReadResult` metadata. Direct ClickHouse, Cube,
ThingsBoard and Command API paths are rejected by the source boundary gate.

### 8.1 Tool classes

#### READ

Retrieves authorized facts or governed analysis. READ tools may execute concurrently when independent and within per-run budgets.

#### PROPOSE

Creates an Agent-owned proposal or draft after deterministic application validation. It cannot call provider connectors or claim a physical result.

#### DOMAIN_ACTION

Submits a governed request to an owning service. It requires an explicit application use case, current authorization and the owner's approval and idempotency contracts. DOMAIN_ACTION is disabled from generic model selection by default.

### 8.2 Tool naming

Logical names should describe platform capabilities:

```text
registry.resolve-scope
registry.get-equipment
telemetry.current.get-device-snapshot
analytics.energy.query-series
analytics.energy.compare-periods
commands.get-capability
commands.submit-intent
knowledge.search-governed-documents
```

Names such as `execute_sql`, provider RPC methods or ThingsBoard resource names are forbidden at the Agent boundary.

## 9. Persistence modules

### 9.1 Business persistence

`agent_operations` is the authority for Operations Agent records. The initial physical schema contains:

```text
investigations
    framework-independent OperationsInvestigationSnapshot
    current Revision and active Run/Lease projection

investigation_effects
    Step Identity, Idempotency Key, effect kind and business record identity

application_outbox
audit_records
```

One `InvestigationTransaction` locks the current Investigation row and atomically validates Revision, optional Run Lease and appended Effect metadata before writing the new snapshot, Effect row, Outbox event and Audit record. The runtime login can update the Investigation aggregate but can only append to Effect, Outbox and Audit tables. Scope, identity, creation time and prior effect history are immutable.

Plans, detailed model traces and verification-result tables remain deferred until a vertical slice requires them. Per-Run resource counters are no longer deferred: run_resource_budgets stores the immutable policy revision, fixed limits, monotonic counters and typed exhaustion, while run_resource_budget_operations deduplicates stable logical operations. They are deliberately not hidden inside Runtime Checkpoints.

### 9.2 Checkpoint persistence

`agent_checkpoints` is independently owned by the checkpoint migrator and contains only opaque Runtime Checkpoints plus lookup and expiry metadata. It never stores resource limits, counters, accepted budget operation identities or exhaustion state. It has no foreign key or cascading delete into `agent_operations`. Its runtime database identity cannot read or modify `agent_operations`, and the Operations runtime identity cannot access `agent_checkpoints`.

The initial adapter stores an opaque state string and Runtime Revision without importing or serializing LangGraph internals. A later LangGraph adapter may define the opaque payload, but business recovery always starts by restoring the validated `OperationsInvestigationSnapshot` from `agent_operations`.

### 9.3 Concurrency controls

Four distinct mechanisms are required:

```text
Agent Run Lease
    short-lived exclusive execution claim

Investigation Revision
    optimistic business-write concurrency

Step Identity
    stable identity of one logical investigation step

Idempotency Key
    deduplicates retried effects and external submissions
```

No single mechanism replaces the others.

### 9.4 Retention

Business records follow product and audit retention requirements. Checkpoints follow operational recovery requirements and may expire after completion. Expired checkpoints do not affect business records.

## 10. AG-UI transport module

The AG-UI adapter converts committed application views and events into frontend protocol events.

Suggested mappings:

```text
Investigation started or resumed   RUN_STARTED
Plan and progress                  ACTIVITY_SNAPSHOT / ACTIVITY_DELTA
read-only tool execution           TOOL_CALL_* events
operator-facing explanation        TEXT_MESSAGE_* events
UI projection state                STATE_SNAPSHOT / STATE_DELTA
run completion                     RUN_FINISHED
```

The adapter must not emit a committed Evidence, Finding or Proposed Action before its domain transaction commits. Event loss requiring recovery uses an outbox or equivalent durable handoff. Reconnect returns an authoritative Investigation view before deltas.

AG-UI payloads expose only authorized, presentation-safe fields. Internal authorization decisions, raw prompts, secrets and unrestricted tool payloads are not streamed.

The first implemented transport slice returns a finite, revision-addressed SSE batch rather than a long-lived stream. It starts with `RUN_STARTED`, publishes one committed `STATE_SNAPSHOT`, maps committed Tool Receipts into bounded `TOOL_CALL_*` activity and ends with `RUN_FINISHED`. Platform Gateway applies the same Session, Site visibility, delegation, timeout, response-bound and nondiscoverability controls as the Investigation HTTP API, then independently validates the event lifecycle and field whitelist. Long-lived deltas, reconnect cursors and missed-event replay are deliberately deferred to the next Map 4 slice.

## 11. Frontend module

The existing React/Vite app owns the Operations Workspace.

Suggested feature boundary:

```text
apps/hvac-web/src/features/operations-agent/
├── api/
├── model/
├── workspace/
├── plan/
├── evidence/
├── findings/
├── proposed-actions/
├── operator-input/
└── copilot/
```

CopilotKit Headless hooks and primitives provide the transport-facing Agent interaction seam. Project components own domain presentation, accessibility and operator controls. The main surface is the Investigation Workspace, not a popup-only chatbot; browser chat history, popup state and Demo mock agents are not durable-state owners and are excluded from the Real build graph.

The primary Site-scoped Real Shell Agent surface is available at `/sites/{siteId}/operations` and is linked directly from Site navigation and Dashboard. It creates, opens, advances or cancels one authorized Investigation through Platform Gateway, runs a self-managed CopilotKit Headless agent against the Gateway event endpoint and renders Plan progress, committed Evidence, Analysis References, Findings, bounded read-only Tool activity and typed Operator Input with project-owned components. The stream is registered as protected Site state so Site change, logout, policy-driven purge or route leave aborts the Agent run and clears the projection.

Frontend state is a projection. Reload, reconnect and route return recover from the authoritative Investigation list, detail and event APIs rather than relying on local CopilotKit state. A terminal reload performs no repeat mutation, and cancellation remains a governed server mutation rather than a local UI status change.

## 12. Scheduling module

The first version does not use Temporal.

A PostgreSQL-backed scheduler or queue may:

- start a scheduled Investigation;
- resume a delayed verification;
- retry a transient run trigger;
- launch a daily or periodic operational review.

It may not own Investigation Plan, steps, Findings, approvals or command state. Delivery is at least once and must be deduplicated through the Application module and Agent Run Lease.

Temporal is reconsidered only after demonstrated requirements for multi-day cross-service waits, event-driven workflow continuation, compensation, complex fan-out/fan-in or workflow-version migration.

## 13. Observability and audit

### 13.1 Observability

Operations telemetry is a diagnostic side channel, never an authority or recovery store. The Platform Gateway, Operations Agent Service, Runtime, logical Tool boundary and fixed Owner calls propagate validated W3C traceparent and tracestate values. Every outbound boundary starts a child span instead of copying the parent span identity.

The span tree uses fixed names:

~~~text
operations.gateway.upstream
operations.http.request
operations.authorization
operations.runtime.plan
operations.runtime.step
operations.model.call
operations.tool.call
operations.owner.request
operations.budget.check
operations.business.commit
operations.run.terminal
operations.stream.recovery
~~~

Investigation, Run, Step and request identities are not exported directly. Each service derives a stable SHA-256 correlation value with a fixed type prefix. This lets a restart, Checkpoint recovery or event-stream reconnect produce a new trace while remaining queryable by the same durable Investigation, Run or Step correlation. Recovery positions, Last-Event-ID values and opaque Checkpoint state are not telemetry attributes.

Only fixed categories and bounded numbers may be exported: operation and result class, logical Tool, fixed Owner, recovery mode and reason, budget dimension, duration, retry count, record count, payload bytes and model token counts. Raw prompts, model completions, operator text, Owner payloads, authorization grants, cookies, CSRF values, tokens, secrets and unrestricted error messages are rejected before export.

Metrics use fixed low-cardinality labels only. Request IDs, Investigation IDs, Run IDs, Step IDs, cursors, resource identifiers and arbitrary content are forbidden as labels. Runtime validation rejects both unknown label keys and values outside the fixed operation, outcome, Owner, Tool, recovery and budget catalogs.

Export is asynchronous and bounded. Queue pressure drops diagnostic spans and increments diagnostics; exporter failure or timeout is caught and counted. Telemetry cannot change the Investigation transaction, resource budget, Checkpoint, retry decision, Outbox, Audit record or public HTTP result. Telemetry fields are absent from all authoritative business and Audit schemas.

The deterministic telemetry-boundary benchmark hard-fails when W3C child propagation, restart or reconnect correlation, redaction, bounded cardinality, exporter isolation or authority separation is removed.

### 13.2 Audit

Audit Ledger records governed business facts such as:

```text
Investigation creation and cancellation
accepted operator-provided information
Proposed Action creation
formal approval reference
DOMAIN_ACTION submission reference
authorization decision reference
```

Tracing does not replace Audit, and Audit does not need unrestricted model traces.

## 14. Security model

Security controls include:

- all browser access through Platform Gateway;
- exact Organization and resource Scope propagation;
- fresh authorization at data and action boundaries;
- nondiscoverable handling for unauthorized resources;
- allowlisted tools and transitions;
- Zod validation of transport, model and tool data;
- retrieved content treated as untrusted data;
- prompt-injection isolation between policy and evidence;
- secret and sensitive-field redaction;
- no provider or source-system identifiers as canonical platform identity;
- separate database identities for business and checkpoint persistence;
- formal approval in the owning domain, never in LangGraph alone.

## 15. Resource budgets

Every Agent Run receives one immutable, versioned Application policy with explicit limits for:

~~~text
model invocations
read Tool requests
wall-clock duration
maximum historical query range
query buckets
Owner records
Owner payload bytes
~~~

The Coordinator checks the budget before Runtime planning, before each READ batch, after each Owner response and before a new business effect. Stable logical operation identities make exact retries inert. PostgreSQL row locks serialize concurrent checks for the same Run, and the operation journal prevents two processes from consuming the same logical operation twice. A policy revision or any limit cannot change after the Run starts, even when the caller supplies the same revision name.

Budget state belongs to agent_operations, not agent_checkpoints. Process restart, Checkpoint deletion and Checkpoint expiry therefore cannot reset counters. Exhaustion is persisted as one typed dimension with the exact consumed and limit values. Once exhausted, no further model call, Owner read or business effect may begin.

The authoritative public projection exposes only policyRevision, outcome, exhaustedDimension, consumed and limit. Internal counters, full limits, operation identities and timestamps are rejected by AG-UI and Platform Gateway. If committed Evidence already exists the result is PARTIAL; otherwise it is UNABLE_TO_CONCLUDE. The Real Operations Workspace displays this terminal Run condition and disables further advancement.

Four repository-owned deterministic scenarios independently certify wall-clock, Tool-request, query-range and payload exhaustion. Mutations that remove the policy, reset counters across restart, double-count exact retries, allow caller overrides, continue external work, continue business effects or report the wrong typed outcome fail closed.

## 16. Evaluation boundary

The framework must be evaluated against repository-owned scenarios rather than free-form demonstrations. Required blocker coverage includes:

```text
Scope accuracy
authorization compliance
data-owner routing
current-state Freshness and Quality
historical dataset revision and watermarks
Evidence provenance
unsupported root-cause refusal
proposal versus formal action separation
idempotent retry and resume
```

LLM-as-judge may assess operational usefulness only after deterministic blockers pass.

## 17. Initial proof and rollout

The first proof is one narrow end-to-end Investigation for a Site-level night-energy increase. It must demonstrate:

1. explicit LangGraph.js control flow;
2. PostgreSQL checkpoint recovery after process restart;
3. no duplicate domain effects after retry or interrupt resume;
4. parallel READ tools with serialized domain writes;
5. Energy data only through Telemetry Query Service;
6. data quality and watermark-aware conclusions;
7. AG-UI streaming through Platform Gateway;
8. CopilotKit headless integration in React/Vite;
9. checkpoint and business schema isolation;
10. no Command Intent creation.

The current Python Agent is deleted before framework implementation begins. Its graph, checkpoints, prompts, mock data, runtime contracts and deployment wiring are not used as design references or fallback paths. The TypeScript Operations Agent is accepted against the repository-owned benchmark and platform contracts. Future TypeScript runtime revisions are promoted or rolled back as complete route owners; no Investigation switches runtime revision mid-run and no per-tool fallback crosses revisions.

## 18. Explicitly deferred

The following are not part of the first framework adoption:

```text
multiple specialist Agent services
Temporal workflow infrastructure
MCP for internal platform services
Agent-owned authorization
Agent-owned command execution
arbitrary SQL or Cube access
production model training or promotion
automatic acceptance of LLM-generated failure modes
independent Next.js Agent application
CopilotKit Runtime as a public BFF
```

Any change to these decisions requires a new architectural review and, where the choice is hard to reverse, a new ADR.
