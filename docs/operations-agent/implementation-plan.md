# Operations Agent implementation plan

Status: accepted plan

Date: 2026-07-31

Tracking: GitHub Map #118; Maps 0–2 are merged through PR #149. Map 3 Runtime recovery (#151), authoritative Owner READ adapters (#152), deterministic night-energy comparison/readiness validation (#153), typed business-record persistence (#154) and Platform Gateway exposure (#155) are complete. Map 4 Operations Workspace delivery is the current frontier.

This plan turns ADR 0009, ADR 0010 and the accepted modular architecture into an implementation sequence. It deliberately starts with deletion of the retired Python Agent, then establishes an executable benchmark before introducing the TypeScript runtime.

## Delivery principles

1. The retired Python Agent is deleted, not migrated or retained as a reference.
2. Each Map ends with an independently verifiable capability and leaves the repository buildable.
3. Operations Agent business state and Agent Execution Checkpoints remain separate.
4. Existing platform modules remain authoritative for Registry, current Telemetry, historical Analytics, IAM, Command and Audit.
5. New TypeScript modules expose deep project-owned interfaces; LangGraph.js, model providers, AG-UI and persistence are adapters.
6. Authorization and safety assertions are blockers at every Map.
7. Repository-wide release certification runs once after the final Map; each Ticket runs the smallest affected checks.

## Test seams

The preferred external seams are:

- repository scripts and builds for legacy Agent removal;
- the future Operations Agent scenario validator for behavioral acceptance;
- the Investigation Coordinator interface for domain and application behavior;
- the Platform Gateway public contract for browser access;
- the AG-UI stream plus authoritative Investigation snapshot for frontend recovery;
- the typed platform-service clients for owner routing and authorization propagation.

Tests should assert observable behavior at these seams. They should not assert private graph-node names, prompt prose, provider SDK classes or checkpoint table internals.

## Map sequence

```text
Map 0  Retire the legacy Python Agent
  ↓
Map 1  Establish the Operations Agent benchmark
  ↓
Map 2  Establish the modular TypeScript service and domain seam
  ↓
Map 3  Deliver the first night-energy Investigation vertical slice
  ↓
Map 4  Deliver the Operations Workspace through AG-UI and CopilotKit
  ↓
Map 5  Add production security, audit, observability and release gates
```

Maps are ordered. A later Map may begin only when the previous Map's completion gate is satisfied.

# Map 0 — Retire the legacy Python Agent

## Destination

The repository contains no executable or referenced Python EnergyAgent stack. HVAC Web and the rest of the platform remain buildable without the old Agent, its reference Canvas or its dedicated Copilot Runtime wiring.

## Current deletion surface

The initial inventory found the following categories of legacy coupling.

### Agent implementation

- `agents/energy-agent` contains the Python LangGraph graph, prompts, deterministic mock energy data, tests, configuration and lockfile.
- `references/energy-agent-next` is an ignored local reference application coupled to the Python graph and old A2UI acceptance tests.

### Dedicated runtime and scripts

- `runtimes/copilot-runtime` registers a remote LangGraph Agent using `ENERGY_AGENT_URL`, `ENERGY_AGENT_GRAPH_ID` and the `sample_agent` graph ID.
- root package scripts start the Python graph, the dedicated runtime and the combined legacy stack.
- legacy development and verification scripts start or probe ports 8123 and 3001 and expect the Python health contract.
- the root runtime dependency is required by the dedicated legacy runtime; React CopilotKit dependencies are separately used by the browser application and are not removed merely because the old server runtime is removed.

### Frontend legacy profile

- the browser has an `energyagent` profile, environment type and profile-specific suggestions and placeholder text tied to Building A/B/C mock semantics.
- demo Vite configuration contains a dedicated `/api/v1/copilotkit` proxy to the old runtime.
- generic CopilotKit React integration may be retained only where it is independent of the old profile and useful for the future Headless Operations Workspace.

### Documentation and repository metadata

- root and application README content still describes the Python Agent topology.
- the deprecated integration document identifies the legacy assets scheduled for deletion.
- `.gitignore` contains the ignored reference Canvas path.
- generated output and private coordination files may mention the old stack but are not source-of-truth product artifacts; they must not drive implementation.

### Infrastructure and CI

The deletion Ticket must search deployment, CI, environment examples, health checks and build automation again immediately before removal. Absence of an initial match is not proof that no generated or newly added caller exists.

## Ticket 0.1 — Inventory and freeze the legacy Agent deletion surface

Blocked by: none.

What it delivers:

- one reviewed deletion manifest that identifies every source-controlled caller, script, environment variable, dependency, proxy, test, document and deployment reference owned exclusively by the legacy stack;
- a separate preserve list for generic frontend CopilotKit code or other infrastructure required by the accepted TypeScript design;
- a freeze rule stating that legacy Agent code receives no feature work while deletion is pending;
- targeted verification commands that prove non-Agent application behavior before and after deletion.

Acceptance criteria:

- every reference to the Python Agent, the old remote LangGraph runtime, the `energyagent` frontend profile and the reference Canvas is classified as delete, rewrite or preserve;
- the manifest traces root package scripts, frontend configuration, runtime wiring, documentation, dependencies, deployment and CI;
- ignored and untracked legacy assets are explicitly included even when Git would not delete them automatically;
- preserve decisions explain why the asset is independent of the retired stack and how it fits ADR 0010;
- no source code is deleted in this Ticket.

## Ticket 0.2 — Delete the legacy Agent and leave the repository green

Blocked by: Ticket 0.1.

What it delivers:

- removal of the Python Agent, old reference application and all exclusively owned wiring;
- removal or rewrite of root scripts, environment types, frontend profile branches, proxies, runtime server code, dependencies, tests and documentation identified by the manifest;
- retention of generic CopilotKit browser primitives only when they remain decoupled from the old runtime and are justified by ADR 0010;
- a repository that builds and tests without Python, `uv`, the old Agent ports or the retired health contract.

Acceptance criteria:

- no production or development route can start or contact the Python Agent;
- no source file references `sample_agent`, `ENERGY_AGENT_URL`, `ENERGY_AGENT_GRAPH_ID`, the `energyagent` frontend profile or Building A/B/C mock prompt assumptions;
- no package script starts or verifies the retired stack;
- old runtime-only dependencies are removed from the manifest and lockfile while required browser dependencies remain;
- ignored local reference assets are removed from the working tree and their ignore rule is removed when no longer needed;
- documentation points only to the accepted Operations Agent architecture;
- the smallest affected frontend builds, lint checks and repository policy checks pass;
- unrelated user changes on the current branch are preserved.

## Map 0 completion gate

Status: completed by Ticket #120 on 2026-07-30.

- the legacy implementation and every exclusive caller are absent;
- a final repository search finds no active legacy identifiers;
- existing demo and real HVAC Web builds pass without the retired stack;
- no fallback route to the Python Agent exists.

# Map 1 — Establish the Operations Agent benchmark

## Destination

The repository can express and deterministically validate Operations Agent scenarios independently of LangGraph.js or any model provider.

Planned tracer bullets:

1. Define a versioned scenario contract and dependency-DAG validator.
2. Add the Site night-energy insufficient-attribution scenario.
3. Add stale-current-telemetry, nondiscoverable authorization and proposal-only action-safety scenarios.
4. Add a benchmark runner that reports blocker dimensions separately from scored usefulness.

Completion gate:

Status: completed by Ticket #127 on 2026-07-30.

- malformed scenarios fail deterministically;
- all initial scenarios encode authoritative owner routing, Evidence requirements and forbidden paths;
- the repository Runner discovers all scenarios, executes blocker-first validation and writes `operations-agent-benchmark-report/v1`;
- blocker failure prevents scoring and returns a non-zero CI status;
- framework code is not required to run the benchmark.

# Map 2 — Establish the modular TypeScript service and domain seam

## Destination

A separately deployable TypeScript service compiles with enforced inward dependencies and exposes an Investigation Coordinator backed by domain and application tests, without requiring a live model.

Planned tracer bullets:

1. Scaffold the service and enforce deep module imports — completed by #140.
2. Implement Operations Investigation lifecycle, Revision, Agent Run Lease and idempotent Step semantics — completed by #141.
3. Define the Investigation Coordinator and narrow application ports — completed by #142.
4. Establish separate business and checkpoint persistence identities and migration ownership — completed by #144.
5. Add a deterministic fake runtime and fake owner adapters for application acceptance tests — completed by #145.

Status: completed by Ticket #145 on 2026-07-30.

- the package-root Coordinator seam owns the complete application acceptance path;
- scripted Fake Runtime steps can request only declared read plans and cannot mutate authoritative Investigation state;
- typed Fake Owner results preserve Scope, Owner, Revision, Quality and provenance metadata;
- Fake Checkpoint deletion preserves business records and completed Investigations reopen through a new Agent Run;
- exact replay, stale Lease, stale Revision, duplicate effect and repository CAS conflict paths are deterministic.

Current persistence state:

- `agent_operations` owns validated framework-independent Investigation snapshots, committed effects, Outbox and Audit;
- `agent_checkpoints` owns opaque Runtime Checkpoints and expiry metadata only;
- each Schema has an independent migrator login, runtime login and migration command;
- PostgreSQL integration tests enforce Revision, Lease and effect conflicts plus cross-Schema permission denial;
- deleting or expiring Checkpoints leaves Investigation business records intact.

Completion gate:

- Domain imports no framework or adapter package;
- callers exercise Investigation behavior only through the Coordinator seam;
- concurrent stale writes and duplicate Step effects are rejected or deduplicated;
- business records survive removal of fake checkpoints.

# Map 3 — Deliver the first night-energy Investigation vertical slice

## Destination

An authorized operator can start a Site night-energy Investigation that queries historical energy only through Telemetry Query Service, validates dataset metadata, records Evidence and produces a supported Site-level Finding or an unable-to-conclude equipment attribution.

Planned tracer bullets:

1. Add the explicit LangGraph.js runtime adapter and PostgreSQL checkpoint recovery — completed by #151.
2. Add typed Registry and Energy Analytics READ adapters — completed by #152.
3. Implement deterministic period comparison and readiness validation — completed by #153.
4. Persist Evidence, Analysis references, Findings and Tool Execution Receipts idempotently — completed by #154.
5. Expose the Investigation application contract through Platform Gateway — completed by #155.

Current persistence state after #154:

- schema-versioned Evidence, Analysis Reference, Finding and Tool Execution Receipt records are validated before commit;
- the typed record, Investigation Revision, committed effect, Outbox event and Audit record commit in one Operations transaction;
- exact retries return the persisted record without advancing Revision or duplicating journals;
- stale Revision, stale Lease, conflicting record identity and duplicate Tool request/attempt identities fail closed;
- raw Energy Series points, arbitrary payloads, sensitive Receipt metadata and supported Equipment attribution are rejected;
- process restart and Checkpoint deletion preserve authoritative business records.

Current Gateway state after #155:

- Platform Gateway owns the public start, get, advance and cancel routes and enforces Session, Origin/CSRF, Site visibility, bounded payloads, timeout and per-Session rate limits;
- Gateway signs a short-lived Site-scoped Operations service delegation and exposes a separate mTLS-only Tool Authorization route for exact Registry and Energy grants;
- Registry grants are signed directly by IAM for the Operations Agent mTLS presenter and remain non-transitive; Energy grants remain Gateway-issued but bind `executingService` to the actual Operations Agent presenter and Scope to the complete normalized query digest;
- Operations Agent exchanges the service delegation immediately before each fixed Owner READ, including recovery replay after a persisted Checkpoint;
- public and internal contracts expose only authoritative Investigation views and typed business records, never Leases, Checkpoints, LangGraph state or raw Energy points;
- exact PostgreSQL restart replay completes the Investigation without duplicate records, effects, Outbox events or Audit records.

Completion gate:

Status: completed by Ticket #155 on 2026-07-31.

- process restart resumes the run without duplicate domain writes;
- independent READ calls may run concurrently while writes remain serialized;
- Partial, Dataset Revision, Watermark and Quality change the accepted result;
- the Agent cannot query ClickHouse, Cube or ThingsBoard directly;
- no Command Intent is created.

# Map 4 — Deliver the Operations Workspace

## Destination

The existing React/Vite application displays and resumes an Operations Investigation through Platform Gateway using AG-UI events and CopilotKit Headless primitives.

Planned tracer bullets:

1. Project committed Investigation state into AG-UI snapshots and deltas.
2. Add authoritative reconnect and replay behavior through Platform Gateway.
3. Add the project-owned Plan, Evidence and Findings workspace.
4. Add bounded operator input and interrupt resume.
5. Remove popup-only assumptions from the primary Operations Agent experience.

Map 4.1 establishes the first bounded vertical slice: each authorized connection receives a finite SSE batch containing `RUN_STARTED`, one authoritative committed `STATE_SNAPSHOT`, bounded read-only Tool activity and `RUN_FINISHED`. The Operations Agent projects only the current committed Investigation View; Platform Gateway validates the event whitelist and the existing React/Vite Real Shell consumes it through a Site-scoped CopilotKit Headless agent. Cursor-based reconnect, missed-event replay and long-lived deltas remain Map 4.2 work.

Completion gate:

- reload and reconnect recover from the authoritative Investigation view;
- the UI never presents uncommitted Evidence or Findings as durable;
- unauthorized fields, raw prompts and internal tool payloads are not streamed;
- the feature works in the existing Vite application without a separate Next.js runtime.

# Map 5 — Add production gates

## Destination

The Operations Agent has enforceable authorization, safety, resource, audit, observability and release evidence suitable for controlled production rollout.

Planned tracer bullets:

1. Add prompt-injection and untrusted-content controls.
2. Add per-run model, tool, time, query and payload budgets.
3. Add OpenTelemetry correlation and redacted model/tool metrics.
4. Add Audit Ledger records for governed business events.
5. Add authorization-negative, retry, restart, concurrency and stream-recovery suites.
6. Add affected-domain CI and release certification evidence.

Completion gate:

- benchmark authorization and safety blockers pass;
- budget exhaustion yields a typed partial or unable-to-conclude result;
- Trace and Audit are demonstrably separate;
- release evidence proves restart safety, idempotency and tenant isolation;
- the full affected-domain verification matrix passes once at Map completion.

## Explicitly out of scope for this implementation sequence

- retaining or comparing the Python Agent;
- multiple specialist Agent services;
- Temporal;
- MCP for internal platform tools;
- arbitrary SQL or Cube access;
- Agent-owned authorization or direct physical command execution;
- automatic production promotion of model-generated failure modes;
- a separate Next.js Agent application.
