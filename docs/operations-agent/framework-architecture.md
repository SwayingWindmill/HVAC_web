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

The initial service skeleton is implemented at `services/operations-agent-service`. It is an independent npm package with NodeNext TypeScript build output, one external package root, module-local `index.ts` entries and a repository-owned AST boundary check. At this stage it has no LangGraph.js, model, database, AG-UI, scheduler, browser or Platform Gateway integration. Run its complete local gate with `npm run operations-agent-service:check`.

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

The current Application ports cover authorization decisions, the business Investigation Repository, an atomic Investigation Transaction, Agent execution planning, Runtime Checkpoints, Registry/Telemetry/Analytics/Command READ owners, budget, Outbox append, Audit recording, clock and identity generation. The Investigation Transaction commits the new aggregate revision, Outbox event and Audit record as one unit; Map 2.4 must implement that contract in PostgreSQL. The ports contain no LangGraph, database, AG-UI, CopilotKit or model-provider types.

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

### 6.2 Runtime state

Runtime state contains only what is needed to continue execution:

```text
Investigation ID
Agent Run ID
runtime revision
current node or pending transition
Plan reference or bounded execution copy
pending and completed Step identities
Evidence identities already observed
pending interrupt data
bounded conversation context
model decision metadata
```

Runtime state must not become the only copy of committed Evidence, Findings or Proposed Actions.

### 6.3 Interrupt rules

LangGraph interrupt is allowed for:

- missing operator input;
- Scope clarification;
- investigation direction choice;
- confirmation to create a proposal;
- safe continuation of an expensive analysis.

Interrupt is not formal approval for Command, work order or another governed action.

A node containing an interrupt may replay on resume. Therefore side-effecting application operations must occur in separate idempotent nodes before or after the interrupt, never as an unguarded effect in the same replayable section.

### 6.4 Runtime revision

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
input normalization and Zod validation
scoped delegation propagation
owner-client call
owner-error translation
Evidence provenance mapping
bounded logging and metrics
```

Tool modules do not contain investigation planning or Finding synthesis.

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

`agent_operations` is the authority for Operations Agent records. The future storage design should support:

```text
investigations
investigation_plans
investigation_steps
investigation_evidence
investigation_findings
proposed_actions
verification_results
tool_execution_receipts
agent_runs
model_invocations
application_outbox
```

This list is a conceptual design, not an approved physical schema.

### 9.2 Checkpoint persistence

`agent_checkpoints` is owned by the runtime adapter. It contains LangGraph checkpoint tables and minimal runtime metadata. Its database identity cannot modify `agent_operations`.

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

CopilotKit hooks and primitives provide conversation and Agent interaction. Project components own domain presentation and accessibility. The main surface is the Investigation workspace, not a popup-only chatbot.

Frontend state is a projection. Reload and reconnect must recover from the authoritative Investigation API rather than relying only on local CopilotKit state.

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

OpenTelemetry should cover:

```text
Investigation and Agent Run correlation
runtime node and transition timing
model latency and token usage
tool-call latency and owner error class
lease acquisition and conflict
revision conflict and idempotency deduplication
budget consumption
AG-UI stream lifecycle
```

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

Every Agent Run has explicit limits for:

```text
model invocations
model tokens
read tool calls
write or proposal attempts
parallel reads
elapsed time
historical query range
returned buckets or rows
Evidence count and payload size
final output size
```

Budget exhaustion is an expected typed outcome. The Agent may preserve collected Evidence and return a partial or unable-to-conclude result. It must not enter an unbounded retry or planning loop.

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
