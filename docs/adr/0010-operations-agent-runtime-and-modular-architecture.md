# Operations Agent runtime and modular architecture

Status: accepted

The Operations Agent will be implemented as a separately deployable TypeScript service using LangGraph.js as its primary, replaceable execution runtime. The service will remain a modular monolith: project-owned Domain and Application modules define Operations Investigation behavior, while LangGraph.js, model providers, PostgreSQL checkpointing, AG-UI and platform service clients are adapters around those interfaces. This keeps Agent framework state separate from business authority and lets the platform replace the runtime without rewriting Investigation records or public contracts.

## Decision

The selected framework stack is:

```text
Agent execution runtime     LangGraph.js with an explicit, bounded StateGraph
Frontend event protocol     AG-UI as a projection of committed application state
Frontend interaction        CopilotKit headless primitives in the existing React/Vite app
Structured validation       Zod at transport, model-output and tool boundaries
Execution checkpointing     PostgreSQL in a dedicated checkpoint schema
Business persistence        PostgreSQL in a separate Operations Agent schema
Public ingress              Existing Platform Gateway
Domain facts and actions    Existing platform-owned Go services
```

LangGraph.js owns Agent Execution Checkpoints, graph position, bounded control flow, interrupts and resume. It does not own Operations Investigation, Investigation Evidence, Investigation Finding, Proposed Action, approval, verification, Tool Execution Receipt or any Registry, Telemetry, Analytics, IAM, Command or Audit fact.

One Operations Investigation has at most one write-capable Agent Run. Independent read-only evidence queries may run concurrently inside that run, while business writes are serialized through the Investigation Coordinator using an Agent Run Lease, Investigation Revision, stable Step Identity and idempotency keys.

The graph is explicit and bounded rather than a generic unconstrained ReAct loop. Model decisions are limited to project-defined stages and allowlisted transitions. Deterministic code owns authorization handling, Scope validation, data-readiness gates, numerical analysis, evidence validation, action safety and business-state transitions.

## Modular service boundary

The service is an independently deployable modular monolith with inward-pointing dependencies:

```text
operations-agent-service
├── domain
├── application
├── runtime-langgraph
├── model
├── tools
├── persistence
├── transport-ag-ui
├── scheduling
└── observability
```

`domain` imports no framework, transport, database, provider or platform-client package. `application` coordinates domain interfaces and transactions. All external technologies and platform services are adapters. LangGraph and AG-UI types never appear in domain entities, application commands, public HTTP contracts or persisted business records.

External callers use the Investigation Coordinator application boundary. Graph invocation, checkpoint and provider APIs remain internal.

## Platform integration

Every browser request continues through Platform Gateway. The browser does not call Operations Agent, LangGraph, CopilotKit Runtime or model providers directly. Platform Gateway continues to own Session, CSRF, Origin validation, Organization Scope, delegation, public rate limits and public error mapping.

AG-UI is a transport projection only. Committed Investigation state is projected into AG-UI text, tool, activity and state events. AG-UI events and model traces are not authoritative business records or audit entries. Reconnect begins with an authoritative Investigation snapshot and then bounded incremental events.

CopilotKit is used headlessly inside the existing React/Vite application. The project owns the Operations Workspace for Plan, Evidence, Findings, Proposed Actions, verification and operator input. CopilotKit Runtime is not a public BFF or state owner; it may exist only as an internal adapter if required by the chosen client integration.

## Persistence and lifecycle

Business and execution persistence are physically separated:

```text
agent_operations
    Operations Investigation business records

agent_checkpoints
    LangGraph checkpoints and runtime metadata
```

They use separate database identities and retention policies. Completed checkpoints may expire independently; deleting them does not delete or invalidate Investigation business records. Reopening a completed Investigation starts a new Agent Run from authoritative domain state.

Execution is at least once, with idempotent domain effects. Every logical step carries stable Investigation, Run, Step and Tool Execution identities, an idempotency key and an expected Investigation Revision. Runtime retries may repeat computation and reads but cannot duplicate committed Evidence, Findings, Proposed Actions or governed external requests.

Cancellation stops future Agent work, releases the run lease and blocks stale writers. It does not erase committed records and does not implicitly cancel a Command Intent or other object owned by another domain.

## Tool and model boundaries

Agent tools call only typed platform contracts. The Agent does not execute ClickHouse SQL, arbitrary Cube queries, ThingsBoard reads, provider connector calls or direct physical commands.

Tools are classified as:

```text
READ
    retrieve authorized platform facts or governed analyses

PROPOSE
    create Agent-owned, reviewable Proposed Actions or drafts

DOMAIN_ACTION
    submit a governed request to the owning business service
```

The model may select allowlisted READ tools. PROPOSE operations require deterministic validation. DOMAIN_ACTION operations require explicit application policy and the owning domain's authorization, approval and idempotency rules. A LangGraph interrupt is never formal business approval.

Model providers are hidden behind narrow project-owned interfaces for request classification, planning, step selection and Finding synthesis. Prompt policy, model configuration, structured-output schema and invocation digests are versioned. Conversation history is input context only; statements used in a Finding must become authorized Evidence or explicitly identified operator-provided information.

Internal platform tools use typed service clients rather than MCP. MCP is reserved for optional or independently deployed external integrations where interoperability justifies the additional protocol boundary.

## Scheduling, security and operations

Temporal is not introduced in the first version. LangGraph checkpointing handles one Investigation's resumable execution. A small PostgreSQL-backed scheduler or queue may decide when to start or resume a run, but it cannot own Investigation steps, Findings, approvals or command state.

Tracing and Audit remain separate. OpenTelemetry diagnoses execution through W3C child spans and stable hashed Investigation, Run and Step correlations. It records only fixed categories, bounded counts and timings; raw prompts, completions, operator text, Owner payloads, identities, cursors, grants and secrets are rejected. Export is asynchronous and failure-isolated, and telemetry never enters business records, Checkpoints, Outbox or Audit. Operations Audit uses one strict versioned event: successful mutation intent is atomic with the business transaction, while authorization denial and budget exhaustion use deterministic standalone identities. Delivery is lease-based, retryable and non-authoritative. The Audit Ledger mTLS owner deduplicates exact event identities, hashes Investigation aggregate identities, appends an Organization-scoped hash chain and stores no Trace context, prompts, raw text, payloads, Checkpoints, Leases or secrets.

All retrieved text and tool output is untrusted data, not instruction. The implementation must enforce allowlisted tools, structured-output validation, exact resource Scope, secret redaction, prompt-injection isolation and per-run budgets for model calls, tool calls, elapsed time, concurrent reads, query range, result size and output size. Budget exhaustion returns a typed partial or unable-to-conclude outcome.

## Evaluation and migration

Framework adoption requires a repository-owned benchmark covering Scope, authorization, data quality, evidence provenance, diagnostic correctness and action safety. The first technical proof must demonstrate restart recovery, idempotent interrupt resume, read-only parallelism with serialized writes, typed platform tools, AG-UI through Platform Gateway, CopilotKit headless integration and strict separation between checkpoint and Investigation state.

The current Python Agent will be removed before the TypeScript Operations Agent is implemented. Its graph, checkpoints, prompts, mock-data assumptions, transport wiring and deployment configuration are not migration inputs and are not retained as a fallback runtime. The new service is designed from ADR 0009, this ADR and the repository-owned benchmark only. Future TypeScript runtime revisions may be rolled back as complete route owners, but an Investigation never switches runtime revision mid-run.

## Considered options

- **Mastra** was rejected as the primary runtime because its storage, workflow, memory, authorization and observability surface would overlap existing platform ownership.
- **Vercel AI SDK ToolLoopAgent** was rejected as the primary runtime because the product needs explicit resumable investigation graphs rather than a generic tool loop. It may not be combined as a second orchestrator.
- **OpenAI Agents SDK** was rejected as the primary runtime because the design is provider-neutral and centered on explicit industrial investigation control flow rather than handoffs or provider-hosted capabilities.
- **Temporal** was deferred because the first version does not yet require cross-service multi-day durable workflows, compensation or workflow-version migration.
- **Multiple specialist Agents** were deferred until independent ownership, deployment, SLO, permission or context-isolation requirements exist.

## Consequences

The selected design adds two intentional persistence models and requires idempotency at every write seam. In return, it preserves platform ownership, supports controlled recovery, keeps framework lock-in behind adapters and provides a clear path from a single Operations Coordinator to later specialist capabilities without prematurely distributing the system.
