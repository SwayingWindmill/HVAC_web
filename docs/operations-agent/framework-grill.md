# AI Agents framework grilling log

Status: accepted

Date: 2026-07-30

This document records the decisions accepted before adopting an AI Agent framework for the Operations Agent. D1-D33 were confirmed on 2026-07-30. It is a design decision log, not an implementation plan. No runtime, dependency, service or schema is created by this document.

## Confirmed decisions

### D1. Execution runtime is not business authority

LangGraph.js, or any replacement runtime, may own graph execution state and Agent Execution Checkpoints only. Operations Investigation, Investigation Evidence, Investigation Finding, Proposed Action, approval, verification and tool execution receipts remain Operations Agent domain records.

External callers use an Investigation Coordinator application boundary. Graph invocation, streaming and checkpoint APIs remain internal implementation details.

### D2. One write-capable Agent Run per Investigation

Multiple Operations Investigations may execute concurrently. One Operations Investigation has at most one write-capable Agent Run at a time. A run may execute independent read-only evidence queries concurrently, but all Investigation domain writes are serialized through the Investigation Coordinator.

Agent Run Lease, Investigation Revision, stable Step Identity and idempotency are separate protections and are all required.

## Accepted framework decisions

Each item records the accepted answer and its design impact. ADR 0010 and the modular architecture document capture the resulting framework boundary.

## A. Runtime and service boundary

### D3. Is LangGraph.js the primary Agent execution runtime?

Decision: yes.

Use LangGraph.js for explicit StateGraph execution, bounded loops, checkpointing, interrupt and resume. Do not combine it with Mastra, Vercel AI SDK ToolLoopAgent or OpenAI Agents SDK as a second orchestration runtime.

Decision impact: dependency set, execution semantics, checkpoint format and operational support model.

### D4. Is the graph explicit and bounded, or a generic ReAct loop?

Decision: explicit and bounded.

The graph should contain named stages such as authorize Scope, classify request, build or revise plan, gather Evidence, evaluate readiness, synthesize Findings and validate output. The model may choose among an allowlisted set of next steps, but it does not receive an unconstrained tool loop.

Decision impact: safety, debuggability, benchmarkability and predictable cost.

### D5. Is Operations Agent deployed as a separate TypeScript service?

Decision: yes.

Create one independently deployable `operations-agent-service` behind Platform Gateway. Internally, keep it as a modular monolith with deep modules rather than splitting Energy, Telemetry, Diagnostics and Maintenance into separate Agent services.

Decision impact: deployment, scaling, ownership and migration from the current Python Agent.

### D6. Which modules are mandatory inside the service?

Decision:

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

Dependencies point inward. Domain has no LangGraph, AG-UI, CopilotKit, HTTP, database or model-provider imports. Runtime and transport are adapters around application interfaces.

Decision impact: test seams and future framework replacement cost.

## B. Public API and frontend integration

### D7. Does every browser request continue through Platform Gateway?

Decision: yes.

The browser must not call Operations Agent, CopilotKit Runtime, LangGraph server or model providers directly. Platform Gateway remains responsible for Session, CSRF, Origin, Organization Scope, delegation, rate limits and public error mapping.

Decision impact: security and consistency with existing platform ownership.

### D8. Is AG-UI the frontend event protocol?

Decision: yes, as a transport projection only.

Operations Agent domain events and records remain canonical. The AG-UI adapter projects committed application state into text, tool, activity and state events. AG-UI events are not business records and do not replace the audit ledger.

Decision impact: frontend interoperability and event mapping.

### D9. Is CopilotKit used headlessly inside the existing React/Vite application?

Decision: yes.

Use CopilotKit hooks and primitives for Agent interaction, while implementing a project-owned Operations Workspace for Plan, Evidence, Findings, Proposed Actions and operator input. Do not build a separate Next.js application and do not make `CopilotPopup` the primary product surface.

Decision impact: UX ownership and frontend architecture.

### D10. Is CopilotKit Runtime retained?

Decision: no as a public or state-owning runtime; optional as an internal adapter only when required by the selected CopilotKit integration.

It must not become a second BFF, authorization boundary, conversation authority or business-state owner.

Decision impact: number of runtime hops and duplicated platform responsibilities.

## C. Persistence, concurrency and lifecycle

### D11. Are checkpoint and business persistence physically separated?

Decision: yes.

Use the existing PostgreSQL platform, but separate schemas and database identities:

```text
agent_operations
    Investigation business records

agent_checkpoints
    LangGraph checkpoints and runtime metadata
```

The checkpoint identity cannot modify Agent business tables. The application identity cannot mutate checkpoint internals except through the runtime adapter.

Decision impact: ownership enforcement, retention and framework migration.

### D12. What is the checkpoint retention policy?

Decision: retain active and recently completed checkpoints for operational recovery; expire completed checkpoints independently of business records.

Deleting an expired checkpoint must not delete or invalidate an Investigation. Reopening a completed Investigation starts a new Agent Run from domain state rather than relying on an old checkpoint.

Decision impact: storage growth, recovery behavior and compliance.

### D13. How are retries, resumes and duplicate requests handled?

Decision: at-least-once execution with idempotent domain effects.

Every logical step has stable `investigationId`, `runId`, `stepId`, `toolExecutionId`, `idempotencyKey` and `expectedInvestigationRevision`. Runtime retries may repeat computation or reads, but committed business writes and external proposals must deduplicate.

Decision impact: correctness under process failure and interrupt replay.

### D14. What are cancellation semantics?

Decision: cancellation stops future Agent work but does not erase committed Evidence, Findings, audit references or already-created external business objects.

Cancellation must release the Agent Run Lease, mark pending steps cancelled and prevent stale workers from committing. A previously submitted Command Intent remains governed by Command Service, not cancelled implicitly by the Agent.

Decision impact: operator expectations and side-effect safety.

## D. Tool and model boundaries

### D15. Do Agent tools call only typed platform contracts?

Decision: yes.

Tools call Platform Core, Telemetry Runtime, Telemetry Query, IAM, Command and Audit through typed clients. Tools must not execute ClickHouse SQL, arbitrary Cube queries, ThingsBoard reads, provider connector calls or direct physical commands.

Decision impact: modular ownership and security.

### D16. How are tools classified by side effect?

Decision: three explicit classes.

```text
READ
    retrieves authorized facts or analyses

PROPOSE
    creates Agent-owned Proposed Actions or drafts

DOMAIN_ACTION
    submits a governed request to the owning business service
```

The model may select READ tools. PROPOSE tools require deterministic validation. DOMAIN_ACTION tools require an explicit application policy and must never bypass formal approval or Command Service.

Decision impact: tool permissions and human-in-the-loop design.

### D17. Does LangGraph interrupt count as formal approval?

Decision: no.

Interrupt is suitable for missing input, scope confirmation, plan choice and consent to create a proposal. Formal approval for a command, work order or other governed action belongs to the owning domain service and must be recorded there.

Decision impact: legal and operational authority.

### D18. Is the model provider hidden behind a project-owned interface?

Decision: yes.

Graph nodes depend on narrow interfaces such as classification, planning, step selection and finding synthesis. Provider packages remain adapters. Business modules do not import OpenAI, Anthropic or LangChain model classes.

Decision impact: provider portability, testing and cost controls.

### D19. Are prompts and structured outputs versioned?

Decision: yes.

Record prompt policy version, model identifier, model configuration, structured-output schema version and invocation digest for every material model decision. Store bounded metadata and redacted traces rather than unrestricted raw platform data.

Decision impact: reproducibility, evaluation and incident review.

## E. Memory, knowledge and multi-Agent design

### D20. What is Conversation Memory?

Decision: an input convenience, not evidence or business truth.

Conversation history may help interpret references and operator intent. Any statement used in a Finding must be converted into authorized Investigation Evidence or explicitly recorded as operator-provided information.

Decision impact: hallucination resistance and provenance.

### D21. How is document knowledge integrated?

Decision: through a governed Knowledge tool with versioned citations and authorization.

RAG output must cite document ID, version and source location. Retrieved text does not override current Telemetry, Registry, Command Capability or other authoritative operational facts.

Decision impact: manuals, SOPs and failure-mode knowledge.

### D22. Do we start with multiple Agents?

Decision: no.

Start with one Operations Coordinator and modular tool families. Introduce a separate specialist Agent only when it has an independent owner, deployment boundary, SLO, permissions or context-isolation requirement.

Decision impact: complexity, latency and evaluation surface.

### D23. Is MCP used for internal platform tools?

Decision: no by default.

Internal tools use typed service contracts. MCP is reserved for external, independently deployed or optional integrations where protocol interoperability outweighs the loss of compile-time coupling and platform-specific authorization semantics.

Decision impact: extensibility and contract governance.

## F. Scheduling and long-running work

### D24. Is Temporal introduced in the first version?

Decision: no.

LangGraph checkpointing handles one Investigation's resumable execution. A small PostgreSQL-backed scheduler or queue may trigger scheduled or delayed investigations. Temporal is reconsidered only when multi-day cross-service workflows, event waits, compensation or versioned durable workflow migration become a demonstrated requirement.

Decision impact: operational footprint and workflow ownership.

### D25. What can a scheduler own?

Decision: only when to start or resume work.

A scheduler may enqueue an Investigation or verification run. It does not own Investigation steps, Findings, approvals or command state. Duplicate delivery is expected and handled through idempotency and the Agent Run Lease.

Decision impact: separation between scheduling and execution.

## G. Events, observability and security

### D26. How are UI events published reliably?

Decision: publish from committed application state, with an outbox or equivalent durable handoff where loss matters.

The Agent must not report an Evidence or Finding as committed to the browser before the domain transaction commits. Reconnect returns an authoritative Investigation snapshot followed by bounded incremental events.

Decision impact: consistency between UI and database.

### D27. What is the relationship between tracing and audit?

Decision: tracing diagnoses execution; Audit Ledger records governed business events.

OpenTelemetry and model traces may record timing, tokens, graph nodes and redacted input/output digests. Audit records actor, authorization decision, proposal, approval and domain action references. Neither replaces the other.

Decision impact: compliance and operational debugging.

### D28. What prompt-injection boundary is required?

Decision: all retrieved text and tool output is untrusted data, never instruction.

The Agent must separate system policy, tool contracts and retrieved content; allowlist tools; validate structured outputs; constrain resource Scope; redact secrets; and prevent documents or telemetry strings from requesting new permissions or actions.

Decision impact: security model and knowledge ingestion.

### D29. What resource budgets are enforced?

Decision: per-run limits for model calls, tool calls, elapsed time, concurrent reads, query range, returned rows or buckets and output size.

Budget exhaustion produces a typed partial or unable-to-conclude result, not an unbounded retry loop.

Decision impact: cost, availability and denial-of-service resistance.

## H. Evaluation, rollout and migration

### D30. What gates must pass before framework adoption?

Decision: the initial benchmark must cover Scope, authorization, data quality, evidence provenance, diagnostic correctness and action safety. Framework adoption requires deterministic blocker checks plus targeted model evaluation.

Decision impact: objective selection rather than framework preference.

### D31. What is the minimum technical proof?

Decision: one end-to-end Investigation proving:

1. explicit LangGraph.js execution;
2. PostgreSQL checkpoint recovery after process restart;
3. no duplicate domain writes after retry or interrupt resume;
4. parallel read-only evidence collection with serialized domain writes;
5. all data access through typed platform tools;
6. AG-UI streaming through Platform Gateway;
7. CopilotKit headless integration in React/Vite;
8. strict separation of checkpoint and Investigation state;
9. no Command Intent creation;
10. benchmark blocker criteria pass.

Decision impact: go or no-go for the runtime ADR.

### D32. How is the current Python Agent retired?

Decision: delete the Python Agent before TypeScript framework implementation begins. Remove its graph, checkpoints, prompts, mock-data assumptions, runtime wiring, deployment configuration and fallback route. Do not use the old implementation as a behavioral or architectural reference; use the accepted ADRs, platform contracts and repository-owned benchmark as the only design inputs.

Decision impact: the project accepts a clean break and has no legacy Agent runtime fallback during the initial TypeScript build.

### D33. What is the rollback unit?

Decision: route ownership for the Operations Agent capability, not per-tool fallback within one Investigation.

An Investigation is handled by one runtime revision. Mid-run fallback to another graph version is forbidden. After the first TypeScript release exists, a failed run may be restarted under a new runtime revision from authoritative Investigation state; initial implementation has no legacy Agent fallback.

Decision impact: reproducibility and safe rollout.

## Decision sequence

The decisions were resolved in this dependency order:

```text
D3-D6   runtime and modular service boundary
D7-D10  gateway, AG-UI and CopilotKit integration
D11-D14 persistence and lifecycle
D15-D19 tools and model abstraction
D20-D25 memory, multi-Agent and scheduling
D26-D29 events, observability and security
D30-D33 evaluation, migration and rollback
```

The resulting decisions are fixed by ADR 0010 and `docs/operations-agent/framework-architecture.md`. Framework implementation remains a separate future task.
