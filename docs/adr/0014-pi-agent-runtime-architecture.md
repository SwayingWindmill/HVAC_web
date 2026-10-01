# ADR 0014 — Pi-based Agent runtime architecture

Status: accepted

Date: 2026-09-03

Supersedes: ADR 0010 runtime/framework selection and LangGraph-specific execution/checkpoint design

Reference review: `docs/architecture/pi-agent-source-review.md`

## Context

The platform needs a Pi-based Agent runtime that can rapidly deliver useful Site-scoped investigation capabilities without carrying forward graph-era orchestration or adding framework-neutral abstractions that have no current consumer.

The target is not a mechanical LangGraph replacement. Existing Agent code, persistence shapes, graph boundaries, model-specific abstractions and frontend integration have no incumbency preference. They may be deleted or redesigned when the Pi reference or a simpler product model produces a stronger result.

The platform has several non-negotiable industrial constraints independent of any Agent framework:

- Tenant/Site/user authorization cannot be chosen by model output;
- authoritative Registry, Telemetry, Energy, Alarm, FDD, Work Order and Command facts stay with their owning domains;
- retrieved data is untrusted model input, not executable instruction;
- model output cannot itself mean physical execution, approval or authoritative sensor truth;
- direct device actuation is never a generic Agent Tool;
- external effects are not assumed exactly-once across process failure;
- raw credentials and hidden model reasoning are not product/audit records;
- the browser never receives provider credentials or framework-specific authority.

Pi `v0.84.4` is selected because the stable `Agent` API provides a small stateful model/tool loop, event streaming and provider-neutral `pi-ai` integration without requiring the product to adopt Pi Coding Agent or the evolving durable Harness v2 state machine.

## Decision

### 1. Pi is the execution engine, not the product state model

The first production Agent runtime uses exact-pinned:

```text
@earendil-works/pi-agent-core 0.84.4
@earendil-works/pi-ai         0.84.4
```

Both packages come from upstream gitHead `b79e4cc834970cca69daebffab7df1da7d1e52c4` and are upgraded together.

The product does not expose Pi types in persistence, HTTP, Gateway, Web or business contracts.

Pi owns one execution loop while it is running:

```text
context -> model -> tool(s) -> model -> terminal outcome
```

The application owns session identity, trusted scope, durable transcript, typed artifacts, operator interaction state, policy and public events.

### 2. Use Pi directly behind the service runtime boundary

Do not create a speculative universal Agent engine abstraction. The Operations Agent service owns one Pi runtime module that constructs and runs `@earendil-works/pi-agent-core` `Agent` instances directly.

The runtime input contains finalized conversation context, one resolved Pi model reference, the currently allowed semantic Tool set, trusted execution context, policy/budget and an AbortSignal.

Application/domain code still does not import Pi types because model/tool-loop mechanics are not business facts. This separation exists for maintainability and testability, not to support another hypothetical harness.

Cancellation uses the run AbortSignal. Durable resume means creating a fresh Pi `Agent` from the last committed Agent Session state; Pi internal objects are not persisted.

### 3. The product state model is simplified to Session / Run / Artifact

The first Agent product model is:

```text
AgentSession
  id
  tenantId
  siteId
  agentDefinitionId
  createdBy
  status
  revision
  createdAt / updatedAt

AgentRun
  id
  sessionId
  modelRef
  status
  startedAt / finishedAt
  usage
  failureCode?

AgentMessage
  finalized user-visible messages only

AgentToolExecution
  tool identity
  bounded validated arguments digest
  result/provenance summary
  status / timing / usage

AgentArtifact
  EVIDENCE_REF
  FINDING
  PROPOSAL
  INPUT_REQUEST
  LIMITATION
```

This replaces the need to model the framework graph and a second framework checkpoint world as product concepts.

A Session is the durable user task/conversation container. A Run is one model/tool execution attempt against that Session. An Artifact is a typed, inspectable product result created from authoritative Tool facts or operator input.

Authoritative business resources remain external owner facts. An Agent Artifact may reference them but does not silently become Registry, Telemetry, Alarm, Work Order, FDD or Command truth.

### 4. Do not persist Pi internal state in the first production release

Persist the finalized transcript and product records needed to reconstruct the next run.

On process failure:

```text
last committed Session revision
        +
finalized messages/tool results/artifacts
        ↓
create a fresh Pi Agent
        ↓
continue in a new AgentRun
```

A provider stream interrupted before finalization is not replayed as if it completed. The interrupted Run is recorded as interrupted/failed and a later Run continues from committed state.

Do not adopt Pi Harness v2 persistence until its upstream API is canonical and a measured requirement justifies replacing project code.

### 5. One AgentDefinition describes behavior; it is not a dynamic plugin system

The first Agent is an Operations investigation Agent. Its definition contains:

```text
id
system policy version
allowed semantic Tools
model policy
run budget
terminal Tools
```

Agent definitions are code/config owned by the service and released with the application. Runtime plugin installation, arbitrary skills, filesystem extensions and dynamic third-party Tools are out of scope.

When a second genuinely independent Agent exists, common definition/runtime code may be extracted. No shared framework package is created solely for hypothetical reuse.

### 6. Model configuration uses `pi-ai` provider factories

Use `createModels()` and explicit provider factory imports. Do not use `providers/all` or `compat` in production runtime code.

The first release enables one production provider/model pair through an exact server allowlist. A second provider is added only for a concrete deployment/evaluation requirement.

Configuration is provider-neutral:

```text
AGENT_MODEL_PROVIDER
AGENT_MODEL_ID
AGENT_MODEL_ALLOWLIST
AGENT_MODEL_THINKING_LEVEL
AGENT_MODEL_TIMEOUT_MS
AGENT_MODEL_MAX_OUTPUT_TOKENS
```

Provider credentials remain server secrets. The Web never calls a model provider directly.

### 7. Semantic HVAC Tools are the primary capability boundary

A Tool is defined by the project, then adapted into Pi.

Minimum project contract:

```ts
type AgentToolExecutionMode = 'parallel' | 'sequential';
type AgentToolReplayPolicy = 'safe' | 'idempotent' | 'never';

interface AgentToolDefinition {
  name: string;
  description: string;
  inputSchema: JsonSchema;
  executionMode: AgentToolExecutionMode;
  replayPolicy: AgentToolReplayPolicy;
  requiredCapabilities: readonly string[];
}
```

Execution receives trusted runtime context separately from model arguments:

```ts
interface AgentRunContext {
  tenantId: string;
  siteId: string;
  principalId: string;
  capabilities: readonly string[];
  sessionId: string;
  runId: string;
  correlationId: string;
}
```

`tenantId`, `siteId`, principal identity, credentials and grants are never normal model-selected Tool arguments.

### 8. First Tool catalog is read-heavy and product-semantic

The first useful catalog is intentionally small:

```text
site.get_context
assets.list
assets.get
telemetry.get_current
telemetry.query_series
energy.query_series
energy.compare_periods
alarms.search
alarms.get
fdd.list_findings
fdd.get_evidence
work_orders.search
work_orders.get
investigation.request_input
investigation.complete
```

Tools call typed owner APIs. The model never receives raw SQL, ClickHouse/Cube query access, ThingsBoard endpoints, arbitrary HTTP, filesystem, process or shell capability.

Independent READ Tools may run in parallel. Interaction/terminal/proposal Tools are sequential.

### 9. Authorization happens before registration and again at execution

The allowed Tool set for a Run is derived from trusted principal/Site capabilities before the Pi Agent is created. A Tool the user cannot possibly invoke should normally not be registered for that Run.

At execution, the Tool adapter again invokes the authoritative owner with server-controlled scope/delegation. The owner remains the final authorization/data-visibility authority.

Pi `beforeToolCall` and `afterToolCall` hooks may implement runtime policy, budget accounting and result normalization, but hooks are not the sole security boundary.

### 10. Model-selected completion is structured through terminal Tools

A normal Agent response is not automatically a completed investigation.

The Agent finishes a product task by calling one of the project-owned terminal Tools:

```text
investigation.complete
investigation.request_input
```

`investigation.complete` accepts a strict structure such as:

```text
outcome: SUPPORTED_FINDING | UNABLE_TO_CONCLUDE
summary
evidenceRefs[]
limitations[]
recommendedNext[]
```

It validates all referenced Evidence/owner facts and creates typed Agent Artifacts before terminating the run.

`investigation.request_input` creates an INPUT_REQUEST Artifact and terminates the current run in a waiting-for-input Session state. The operator response starts a new Run from committed Session state.

This avoids depending on framework-specific interrupt/checkpoint semantics for human-in-the-loop behavior.

### 11. Model output can propose; it cannot directly actuate

The first runtime has no direct physical command Tool.

Future side-effect capabilities are introduced as typed proposals first:

```text
proposal.create_work_order
proposal.create_control_change
proposal.create_optimization_plan
```

A proposal is not approval or execution. The owning domain performs its own authorization, revision, idempotency, approval, lease/fence and verification flow.

A direct `command.execute`, generic RPC, arbitrary write/setpoint or raw transport Tool is forbidden in the Agent runtime.

### 12. Replay policy is explicit

External effects cannot generally be made exactly once across process crashes.

Every Tool declares one replay policy:

```text
safe
  pure/authorized READ may be repeated

idempotent
  may be repeated only with a durable stable idempotency identity

never
  cannot be automatically repeated after an uncertain effect
```

The initial catalog is `safe` except terminal/session writes, which are local transactional/idempotent application operations. Direct `never` physical effects are not registered.

### 13. Tool outputs are bounded evidence, not raw data dumps

Tools return compact structured results with provenance/revision/quality metadata. Large time series remain owned by the source service; the Agent receives the smallest summary or bounded slice necessary for the task.

Each Tool has explicit maximum records/bytes/time range. This controls context growth, provider cost and prompt-injection surface.

Text from owner data, labels, notes, alarm descriptions and user fields is treated as untrusted data.

### 14. Run budgets are enforced in runtime, not prompt prose

Every Run has hard limits for at least:

```text
model calls
Tool calls
wall clock
parallel Tool concurrency
query range
records/bytes returned
input tokens
output tokens
```

Pi's turn/tool lifecycle is used to enforce these limits. Budget exhaustion produces a typed unable-to-conclude/failure state instead of allowing an unbounded loop.

The exact numbers are product policy and may differ by AgentDefinition/model; they are not model-controlled.

### 15. Public events are framework-neutral

Pi events are mapped into `hvac.agent.event/v1` before leaving the service.

The minimum event vocabulary is:

```text
session.snapshot
run.started
assistant.delta
tool.started
tool.completed
artifact.created
input.required
run.completed
run.failed
```

Framework reasoning/thinking deltas are not part of the public contract by default.

Platform Gateway remains the public browser ingress and forwards the project-owned `hvac.agent.event/v1` SSE stream directly. AG-UI is not part of the target architecture.

### 16. Web consumes the project Agent protocol directly

CopilotKit is removed from the product architecture. HVAC Web owns the investigation UI with normal React state and generated project API clients.

The browser consumes durable Session snapshots plus `hvac.agent.event/v1` SSE events directly through Platform Gateway. No CopilotKit Provider, `AbstractAgent`, headless agent bridge, AG-UI adapter or framework-specific browser state remains.

The `/ai` and `/operations` information architecture may be simplified as part of the Pi product implementation, but the Web must have one clear investigation experience backed by the project Session/event protocol.

### 17. Observability separates runtime telemetry from business Audit

Record bounded operational metadata such as:

```text
agentDefinitionId
session/run hashed correlation
provider/model
latency
token/usage counts
Tool name/status/duration
budget dimension/outcome
```

Do not emit raw secrets, hidden reasoning, unbounded Tool payloads or full provider request/response bodies into telemetry.

Business Audit records only governed user/business actions and artifact/proposal lifecycle events that need auditability. Model tracing is not automatically an Audit fact.

## Target module shape

The first implementation stays inside the Operations Agent deployable until another Agent proves a reusable package is needed:

```text
services/operations-agent-service/src/
├── agent/
│   ├── definition.ts
│   ├── session.ts
│   ├── events.ts
│   ├── policy.ts
│   └── artifacts.ts
├── runtime-pi/
│   ├── pi-runtime.ts
│   ├── pi-models.ts
│   ├── pi-tools.ts
│   └── pi-events.ts
├── tools/
│   ├── catalog.ts
│   ├── registry-tools.ts
│   ├── telemetry-tools.ts
│   ├── energy-tools.ts
│   ├── alarm-tools.ts
│   ├── fdd-tools.ts
│   └── work-order-tools.ts
├── persistence/
├── transport-http/
├── transport-events/
└── observability/
```

This is a destination layout, not a compatibility requirement. Existing files may be deleted or moved directly; no LangGraph compatibility adapter is required.

## Migration rule

Do not run two authoritative Agent architectures indefinitely.

During development, the old runtime may exist only long enough to provide comparison evidence. New Pi runs use new Session/Run semantics; an active old-framework run is never silently resumed under Pi.

Cutover is complete when the Pi vertical slice passes the framework-independent Agent benchmark, recovery/security acceptance and Web/Gateway acceptance. Then remove LangGraph-specific source, dependency, checkpoints, scripts and documentation rather than retaining fallback code.

## Consequences

### Benefits

- fastest path from static graph orchestration to a real model/tool Agent loop;
- provider-neutral model runtime without broad provider imports;
- smaller product state model than graph + checkpoint + separate conversation abstractions;
- explicit industrial authorization/replay/safety boundaries;
- deterministic Pi loop tests via faux provider;
- Pi-specific model/tool-loop mechanics stay inside the runtime module instead of leaking into business state;
- no premature generic Agent platform or multi-agent plugin system.

### Costs

- the application must persist finalized Session/Run state because stable Pi `Agent` is in-memory;
- process failure restarts a new run from committed state rather than resuming a provider stream byte-for-byte;
- Tool schemas/provenance/budget policy require deliberate product design;
- later adoption of a mature Pi durable Harness requires an explicit migration rather than leaking unstable framework storage into current business tables.

## Rejected alternatives

- **Retain LangGraph and wrap it with Pi model APIs** — rejected because this keeps graph orchestration as the primary mental model and prevents Pi from providing the simpler Agent loop being adopted.
- **Adopt Pi Coding Agent** — rejected because coding-agent filesystem/process/network capabilities and UI/extension runtime are not appropriate production HVAC authority.
- **Adopt Pi Harness v2 immediately** — rejected for the first release because the upstream state-machine document is explicitly non-canonical and would introduce more framework-owned durability than the current product needs.
- **Build a generic `agent-runtime-service` before the first Pi vertical slice** — rejected as speculative infrastructure. Keep the runtime in the Operations Agent service until a real product boundary requires extraction.
- **Retain CopilotKit or AG-UI around Pi** — rejected. They add a second Agent/event abstraction without product value once HVAC Web consumes the project Session/SSE protocol directly.
- **Let the model call MCP/raw HTTP internally** — rejected for core platform capabilities. Typed owner APIs are simpler, safer and preserve domain authority.

## Acceptance of this ADR

Implementation follows `docs/operations-agent/pi-agent-implementation-plan.md`. ADR 0010 remains historical context for the former LangGraph architecture but is no longer the runtime selection authority.
