# Pi Agent implementation plan

Status: accepted plan

Date: 2026-09-03

Authority: ADR 0014 and `docs/architecture/pi-agent-source-review.md`

## Goal

Replace the current graph-oriented Agent runtime with a Pi-based model/tool Agent that is useful end to end, simple enough to develop quickly and safe for an industrial energy platform. Pi is the selected runtime; the plan does not add abstractions for another hypothetical harness.

This is not a compatibility migration. Existing Agent implementation details are not acceptance criteria. Stale LangGraph-specific code, schemas, tests and documents are removed when the Pi path becomes authoritative.

## Product tracer bullet

The first production slice answers one Site-scoped investigation question with real authorized data:

> Why was this Site's overnight energy behavior abnormal, and what evidence supports the conclusion?

The Agent must be able to:

```text
receive operator question
  -> inspect Site context
  -> choose authorized Energy/Asset READ Tools
  -> run independent reads in parallel when useful
  -> handle incomplete/unavailable data honestly
  -> request operator input when necessary OR
  -> call investigation.complete with typed evidence references
  -> persist the finalized Session/Run/Artifacts
  -> stream project-owned SSE events through Gateway
```

The first slice has no direct physical command or generic write Tool.

## Delivery principles

1. Build the smallest end-to-end Pi product first; do not build a generic multi-agent platform.
2. Use Pi `Agent`, not Coding Agent and not non-canonical Harness v2 persistence.
3. Keep Pi types inside the runtime module; product Session/Tool/Artifact contracts remain project-owned because they are business/application concepts, not because another harness is planned.
4. Tenant/Site/principal/capabilities come only from trusted runtime context.
5. Register only Tools the current principal/Site can use; reauthorize again at execution.
6. Owner APIs remain authoritative; model output is never a substitute for Registry/Telemetry/Energy/Alarm/FDD/Work Order/Command truth.
7. READs may be parallel; proposal/interaction/terminal operations are sequential.
8. Persist finalized facts, not model chain-of-thought or raw provider traffic.
9. External effects use explicit replay policy; exactly-once is never assumed.
10. Do not keep permanent dual runtimes. Pi wins the acceptance gate, then LangGraph-specific code is deleted.
11. Tests protect observable product/security/recovery contracts only; do not snapshot prompts or private Pi implementation details.
12. Each Ticket leaves the affected service buildable and runs the narrowest meaningful verification.

## Planned destination

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

This layout is allowed to get smaller during implementation. A folder/module exists only when it has a real responsibility.

## Ticket sequence

```text
PI-00  Retire CopilotKit and AG-UI
  ↓
PI-01  Product Agent contracts and Session model
  ↓
PI-02  Pi Agent runtime spike with faux provider
  ↓
PI-03  Production model runtime and provider policy
  ↓
PI-04  Semantic HVAC Tool runtime and first READ tools
  ↓
PI-05  First complete Pi investigation vertical slice
  ↓
PI-06  Durable Session/Run persistence and crash recovery
  ↓
PI-07  Direct Gateway SSE + Web integration + operator input
  ↓
PI-08  Tool expansion, security and observability hardening
  ↓
PI-09  Production cutover and LangGraph retirement
```

Only adjacent work that is genuinely independent may overlap. Do not start PI-09 cleanup while the Pi slice still relies on old runtime behavior.

# PI-00 — Retire CopilotKit and AG-UI

Blocked by: none.

## Purpose

Remove the two Agent/UI protocol layers that are no longer part of the target architecture before Pi is introduced, so the new runtime does not inherit their state/event assumptions.

## Deliverables

- remove `@copilotkit/react-core` and `rxjs` from the Web when no other production consumer remains;
- replace the `AbstractAgent`/`CopilotKit` Operations bridge with a small project-owned SSE runner/hook;
- replace `transport-ag-ui` with `transport-events` using project-owned Operations/Agent event names;
- rename Web contracts and parsers from `AgUi` terminology to project Agent event terminology;
- update public/internal OpenAPI descriptions/schemas to the project event stream;
- remove CopilotKit-specific browser warning suppression and obsolete active design guidance;
- keep historical ADR/evidence documents intact but mark current authority through ADR 0014.

## Event rule during transition

PI-00 does not invent the final Pi event richness. It keeps only the currently observable event behavior needed by the existing Operations Workspace, expressed as project events over SSE. PI-07 may extend `hvac.agent.event/v1` when the Pi Session product requires additional deltas.

## Acceptance criteria

- active Web source imports no `@copilotkit/*` or `rxjs`;
- active service source contains no `transport-ag-ui` module or `OperationsAgUi*` type;
- active public/internal OpenAPI no longer describes AG-UI;
- the existing Operations Workspace still restores the authoritative snapshot, reconnects, handles revocation and shows Tool activity through direct React state;
- root production dependencies contain no CopilotKit package when no other consumer exists;
- TypeScript, Operations Workspace tests, service tests and production Web build pass.

# PI-01 — Product Agent contracts and Session model

Blocked by: none.

## Purpose

Define the product Session/Tool/Artifact model without importing Pi or LangGraph into those product contracts. This Ticket removes graph/checkpoint concepts from the target data model while leaving actual model/tool-loop execution to the Pi runtime module.

## Deliverables

- `AgentRunContext`, `AgentModelRef` and `AgentRunBudget`;
- `AgentToolDefinition`, execution mode and replay policy;
- project-owned runtime event contracts consumed by the Pi module and Gateway transport;
- `AgentSession`, `AgentRun`, `AgentMessage`, `AgentToolExecution`, `AgentArtifact` contracts;
- Session state transitions covering ACTIVE, WAITING_FOR_INPUT, COMPLETED, FAILED/CANCELLED as actually required by the first product slice;
- `hvac.agent.event/v1` internal event vocabulary;
- strict terminal artifact contracts for `investigation.complete` and `investigation.request_input`;
- no framework persistence or HTTP implementation yet.

## Key decisions

- Pi runtime execution is one run + AbortSignal; no generic harness interface is added;
- a new Run reconstructs from committed Session state rather than resuming framework internals;
- only one active write-producing Run exists per Session;
- messages/artifacts are append-oriented product records;
- an Artifact references owner facts with identity/provenance, not copied authority.

## Acceptance criteria

- no contract imports `@earendil-works/pi-*`, LangGraph, CopilotKit or AG-UI;
- trusted Tenant/Site/principal/capabilities are structurally separate from model Tool arguments;
- replay policy is required for every Tool definition;
- the terminal result can represent both supported finding and unable-to-conclude without parsing free-form Markdown;
- no speculative multi-agent/subagent/plugin APIs are introduced;
- direct command/setpoint execution has no Tool contract.

## Verification

Use TypeScript typecheck plus a small behavioral test for valid Session transitions and terminal artifact validation. Do not add a permanent new repository gate.

# PI-02 — Pi Agent runtime spike with faux provider

Blocked by: PI-01.

## Purpose

Prove that the adopted stable Pi `Agent` API can run the project Session/Tool contracts cleanly before any real provider or owner API is involved.

## Deliverables

- exact dependencies:
  - `@earendil-works/pi-agent-core@0.84.4`
  - `@earendil-works/pi-ai@0.84.4`;
- a `runtime-pi` module that constructs and runs Pi `Agent` directly;
- project Tool -> Pi Tool translation;
- Pi event -> `hvac.agent.event/v1` translation;
- AbortSignal/cancel behavior;
- one fake semantic READ Tool plus `investigation.complete`;
- deterministic test flow driven by Pi `fauxProvider()`.

## Spike scenario

The faux model must:

```text
user question
  -> tool call: site.get_context
  -> receive valid Tool result
  -> tool call: investigation.complete
  -> engine terminates with typed completed Artifact
```

Add one failure case where the READ Tool throws and the result is observed as a Tool failure rather than valid empty data.

## Acceptance criteria

- production source uses `Agent`, not Pi Coding Agent;
- no filesystem/bash/generic fetch/browser Tool is registered;
- no `providers/all` or `compat` import appears in production runtime source;
- faux provider test uses no API key/network call;
- Pi thinking/reasoning content is not emitted in the project public event vocabulary;
- engine cancellation stops the active provider/tool loop and produces a project-owned terminal status;
- Pi types remain inside `engine/pi` plus the narrow adapter implementation.

## Exit decision

If the Pi adapter requires framework types in application contracts or cannot express the simple Tool loop without reimplementing its internals, stop and reassess before PI-03. Do not build compatibility code around a failed spike.

# PI-03 — Production model runtime and provider policy

Blocked by: PI-02.

## Purpose

Turn the fake Agent into a provider-neutral server runtime using Pi's intended provider/model APIs without creating a provider matrix.

## Deliverables

- `createModels()` based runtime composition;
- one explicit production provider factory;
- exact model allowlist and configuration parser;
- model reference validation against the registered provider/model catalog;
- server-only credential resolution;
- model timeout/output/thinking policy;
- usage and provider/model metadata normalization;
- explicit test provider injection seam using `fauxProvider()`.

## Initial configuration

```text
AGENT_MODEL_PROVIDER
AGENT_MODEL_ID
AGENT_MODEL_ALLOWLIST
AGENT_MODEL_THINKING_LEVEL
AGENT_MODEL_TIMEOUT_MS
AGENT_MODEL_MAX_OUTPUT_TOKENS
```

The selected first provider should match the deployment environment actually used for the first production slice. Do not enable OpenAI + DeepSeek + Anthropic simultaneously merely because Pi supports them.

## Acceptance criteria

- unsupported provider/model fails at service composition before a Run starts;
- missing production credential fails before external model work, unless the service is explicitly configured for fake/test mode;
- model identity comes from server configuration/AgentDefinition, never browser input;
- no provider credential appears in HTTP responses, logs, Agent events or persisted Session records;
- provider-specific code is contained in model composition and not in business/Tool modules;
- adding a future second provider requires provider registration/config, not rewriting Tool/Application code.

## Verification

Run typecheck and deterministic model-runtime tests. One optional live-provider smoke test may exist behind explicit environment variables but is not a required unit gate.

# PI-04 — Semantic HVAC Tool runtime and first READ Tools

Blocked by: PI-03.

## Purpose

Establish the real industrial capability boundary and connect Pi to authoritative platform owners without exposing raw infrastructure to the model.

## First Tools

Implement only the smallest set needed for the tracer bullet:

```text
site.get_context
assets.list
energy.query_series
energy.compare_periods
```

Add `telemetry.get_current` only if the first scenario demonstrably needs it.

## Deliverables

- Tool catalog filtered by trusted capabilities before Pi Agent construction;
- project Tool executor receiving `AgentRunContext` separately from validated model args;
- owner-specific adapters using typed service contracts;
- bounded Tool result schemas including source identity/revision/quality/completeness/provenance required by the first scenario;
- per-Tool bounds for range, records, bytes and timeout;
- READ Tools marked parallel + replay safe;
- runtime Tool counters and concurrency limit;
- owner failures mapped to stable Agent Tool failure codes.

## Security behavior

A Tool request may contain Device/Asset/query selectors allowed by its schema, but the executor injects current Tenant/Site/principal/delegation from trusted context.

The Tool must not accept:

```text
tenantId
siteId override
principalId
capability grant
credential
raw URL
SQL
owner service address
```

from model-generated arguments.

## Acceptance criteria

- unauthorized Tools are absent from the Pi Tool set;
- direct executor invocation still reauthorizes through the owner path;
- cross-Site selector attempts cannot escape the current Site;
- missing/partial/poor-quality data remains distinguishable from zero/normal data;
- Tool output is bounded and does not dump an entire raw time-series payload into model context;
- parallel independent READ calls do not create shared mutable business state;
- real Tool errors are failures, not strings that masquerade as success.

# PI-05 — First complete Pi investigation vertical slice

Blocked by: PI-04.

## Purpose

Ship the first genuinely useful model-driven investigation before adding durability/platform breadth.

## Behavior

The Operations Agent receives a Site-scoped operator question and may choose among the allowed Site/Asset/Energy Tools. It must finish with exactly one of:

```text
investigation.complete
investigation.request_input
```

`investigation.complete` validates:

- outcome;
- summary bounds;
- evidence/owner references;
- limitations;
- recommended next actions;
- no unsupported physical-execution claim.

`investigation.request_input` creates one typed input request and ends the current Run cleanly.

## Prompt/system policy

Keep the system policy short and explicit. It should define role, Tool-use expectations, untrusted-data rule, evidence requirement and prohibited execution claims. Do not encode authorization or owner routing in prompt prose when code can enforce it.

Prompt text is versioned only when the product behavior depends on a changed policy; tests assert behavior, not exact wording.

## Run budget

Enforce concrete first-slice limits for:

- model calls/turns;
- Tool calls;
- wall-clock runtime;
- parallel Tool count;
- query range/records/bytes;
- output tokens.

Budget exhaustion produces an unable-to-conclude/failure result and stops another model turn.

## Acceptance scenarios

At minimum protect these meaningful cases with faux-model behavior and real Tool adapters/fakes:

1. sufficient energy evidence -> supported finding;
2. incomplete data -> unable to conclude;
3. Agent asks for operator input -> WAITING_FOR_INPUT;
4. malicious instruction embedded in an Asset/owner text field does not grant a forbidden Tool or cross-Site access;
5. Tool budget exhaustion stops the run;
6. model attempts a nonexistent/unauthorized capability and cannot execute it.

## Completion gate

At this point the project must have a useful Pi Agent without relying on LangGraph for the new Session.

# PI-06 — Durable Session/Run persistence and crash recovery

Blocked by: PI-05.

## Purpose

Make the Pi product recover across service restart without adopting Pi Harness v2 internals.

## Deliverables

PostgreSQL persistence for the minimum product records:

```text
agent_sessions
agent_runs
agent_messages
agent_tool_executions
agent_artifacts
```

Exact physical naming may adapt to repository database conventions, but the logical model stays Session/Run/Message/ToolExecution/Artifact.

## Transaction rules

- Session revision and one-active-Run constraint are enforced durably;
- finalized message/tool/artifact append and Session state transition commit atomically where one logical result requires them together;
- start/cancel/complete are idempotent at the public application seam;
- a crashed in-flight provider generation becomes an interrupted Run, not a fabricated completed message;
- the next Run reconstructs Pi context from committed finalized messages/tool results only;
- safe READ Tools may rerun after a new Run starts;
- future idempotent proposal Tools must use stable operation identities before external effects.

## No compatibility rule

Do not keep old LangGraph checkpoint tables as the Pi persistence model. Old checkpoint state is not interpreted as a Pi Session. Development/test data may be dropped. Any production-data preservation requirement must be explicitly proven before cutover; otherwise obsolete runtime schemas are removed in PI-09.

## Acceptance criteria

- kill/restart between completed turns -> new Run continues from committed Session state;
- kill during model stream -> partial assistant output is not committed as final truth;
- kill during safe READ -> later Run may repeat it without duplicating a business mutation;
- cancellation prevents stale run completion from advancing the Session;
- no Pi internal object serialization is stored in product tables;
- no hidden reasoning is persisted.

# PI-07 — Direct Gateway SSE, Web integration and operator input

Blocked by: PI-06.

## Purpose

Expose the durable Pi Agent product through the existing public Gateway using the project-owned Session API and SSE event stream directly.

## Public product surface

Provide Site-scoped APIs for the minimum current UX:

```text
create/list/get Agent Session
append operator message / start Run
cancel Run
submit requested operator input
read Session snapshot
stream current Run events
```

Exact REST paths follow project public API conventions and are generated into Web clients.

## Event transport

Service emits `hvac.agent.event/v1`. Gateway validates and forwards only allowed public fields.

Required event behavior:

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

Reconnect always starts from durable Session snapshot. Incremental event replay is added only if product UX requires events that cannot be reconstructed from the snapshot.

## Web decision

HVAC Web uses normal React state and generated project APIs/events directly. CopilotKit and AG-UI are not part of the Web architecture.

The `/ai` vs `/operations` information architecture is reviewed here rather than preserved automatically. Prefer one clear investigation destination with contextual entry points from Assets/Energy/Alarm/FDD.

## Operator input

An INPUT_REQUEST Artifact contains a typed request schema/choices. Submitting operator input appends a user-visible durable message/artifact response and starts a new Run.

The model cannot mark its own request approved or synthesize operator identity.

## Acceptance criteria

- browser never receives Pi/provider credentials or Pi-specific event types;
- Site switch/revocation cancels/invalidates protected Agent resources;
- reconnect/reload restores the durable Session without duplicating a run mutation;
- operator input is attributable to the authenticated principal;
- no hidden reasoning is rendered by default;
- accessibility and streaming state are tested at the browser seam only where observable behavior matters.

# PI-08 — Tool expansion, security and observability hardening

Blocked by: PI-07.

## Purpose

Expand from one useful energy investigation into a practical operations investigation while validating production threat and resource boundaries.

## Tool expansion order

Add Tools only where owner contracts are ready and the Agent has a real scenario:

```text
telemetry.get_current
telemetry.query_series
alarms.search
alarms.get
fdd.list_findings
fdd.get_evidence
work_orders.search
work_orders.get
```

Do not add all Tools in one batch without an Agent scenario proving their usefulness.

## Proposal path

If product scope requires one write-adjacent capability, add one proposal Tool — preferably `proposal.create_work_order` — before any control proposal.

It must be:

- sequential;
- idempotent;
- reviewable;
- non-executing;
- owned by Agent/session until the Work Order domain explicitly accepts a governed create command.

Do not add `command.execute` in this Ticket.

## Threat scenarios

Protect concrete failures:

- prompt injection in Registry labels/notes/Alarm text/Work Order text;
- model requests cross-Tenant or cross-Site identifiers;
- forged Tool arguments outside schema;
- owner returns scope/revision inconsistent with request;
- provider timeout/error/context overflow;
- Tool timeout;
- model/tool budget exhaustion;
- cancellation race;
- replay of idempotent proposal after process failure;
- oversized Tool output;
- unavailable/partial telemetry mistaken for zero/normal.

## Observability

Emit bounded metrics/traces for:

```text
run duration/outcome
model calls/provider/model/token usage
Tool calls/status/duration
Tool concurrency
budget exhaustion dimension
owner error class
session/run hashed correlation
```

No raw secret, hidden reasoning, unbounded Tool payload or full model request/response belongs in telemetry.

## Acceptance criteria

- security tests operate at trust boundaries, not by duplicating every possible string permutation;
- production failure is diagnosable without logging private raw prompts/provider payloads;
- added Tool scenarios pass the framework-neutral Agent benchmark;
- model/provider changes cannot bypass Tool authorization or business-owner policy.

# PI-09 — Production cutover and LangGraph retirement

Blocked by: PI-08.

## Purpose

Make Pi the only active Operations Agent runtime and delete obsolete architecture rather than keeping a permanent fallback.

## Cutover gate

Before deletion, Pi must pass:

- first-slice and expanded Agent benchmark blockers;
- typecheck/build for the service;
- deterministic faux-provider Agent tests;
- persistence/restart/cancel acceptance;
- Gateway authorization/event acceptance;
- Web investigation flow acceptance;
- narrow security and dependency audit;
- production model smoke test in the target environment when credentials are available.

## Delete/rewrite surface

Classify and remove all runtime-only artifacts whose purpose disappeared, including as applicable:

- `@langchain/langgraph` dependency;
- `runtime-langgraph` source;
- graph runtime/checkpoint-specific contracts;
- obsolete checkpoint migrations/schema/credentials;
- model-specific `OpenAiFindingSynthesizer` abstraction if `pi-ai` now owns provider execution;
- stale tests that assert graph node/checkpoint implementation details;
- old LangGraph runtime scripts/configuration;
- documentation that still presents ADR 0010 as current;
- any obsolete event/runtime bridge retained solely for the old LangGraph implementation.

Do not delete a business/security capability merely because its current implementation lived near LangGraph. Re-implement the requirement in the simpler Pi product model when it is still needed.

## No dual-mode rule

After cutover:

```text
LangGraph active runtime = 0
Pi compatibility fallback = none
old runtime route = none
```

An in-flight old-runtime run is not converted. Cutover requires a release boundary where new Sessions start only under Pi.

## Final acceptance

- active source contains no LangGraph runtime dependency/path;
- new Agent Sessions run only through Pi;
- Pi packages remain exact pinned;
- application/Gateway/Web contracts contain no Pi types;
- old runtime persistence is removed when no retained product fact requires it;
- repository governance and affected domain checks pass;
- the source review and ADR are the current Agent architecture references.

# What is deliberately deferred

The following are not prerequisites for the first Pi production release:

- generic multi-agent runtime;
- subagents/delegation between agents;
- runtime plugin installation;
- arbitrary skills/extensions;
- Pi Coding Agent;
- Pi Harness v2 durable backend;
- background jobs lasting hours/days;
- branch/fork Agent sessions;
- direct Command execution;
- broad model-provider matrix;
- MCP for internal first-party platform Tools.

Each is reconsidered only when a current product requirement appears.

# Expected development rhythm

## Fast first milestone

PI-00 through PI-05 are the rapid-development milestone. The target is a direct Web/Gateway path without CopilotKit/AG-UI plus a working Pi Agent against real authorized Site/Energy capabilities before building broad persistence or platform abstractions.

## Production milestone

PI-06 through PI-09 convert that vertical slice into a durable, browser-accessible, production-safe runtime and then remove the obsolete architecture.

