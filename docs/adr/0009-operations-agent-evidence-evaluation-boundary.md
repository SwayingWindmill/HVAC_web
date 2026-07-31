# ADR 0009 — Operations Agent evidence and evaluation boundary

Status: accepted

Date: 2026-07-30

## Context

The current Energy Agent was imported as a Python/LangGraph sample and does not
establish the product boundary required for a platform-owned Operations Agent.
Before selecting or implementing a new TypeScript runtime, the repository needs
an executable definition of what a correct industrial operations investigation
looks like.

AssetOpsBench is used as a methodology reference for scenario design, ideal
execution trajectories, industrial tool taxonomy and benchmark evaluation. No
AssetOpsBench runtime code, Python framework, CouchDB model or MCP deployment is
adopted by this decision.

The platform already has authoritative owners for Registry identity, current
telemetry, historical analytics, authorization, commands and audit. An Agent
benchmark must preserve those ownership decisions rather than invent a second
source of truth.

## Decision

### 1. The Operations Agent owns investigations, not source facts

The future Operations Agent domain may own:

- Investigation Task and plan;
- Evidence references and tool execution receipts;
- Findings and their classification;
- Proposed Actions;
- Analysis Run references;
- verification results for an Agent proposal.

It does not own or reconstruct:

- Organization, Site, Equipment or Device identity;
- current Device Observation Snapshot or Business Revision;
- historical telemetry or analytical facts;
- authorization decisions;
- Command Intent, Attempt or physical execution outcome;
- the audit ledger.

### 2. Agent execution state is not business authority

The Agent framework may persist an Agent Execution Checkpoint containing the
current graph position, pending execution steps, resumable interrupt data and
other runtime state needed to continue an investigation.

The checkpoint is not the authoritative record for an Operations Investigation,
Investigation Evidence, Investigation Finding, Proposed Action, approval,
verification result or tool execution receipt. Those records belong to the
Operations Agent domain and remain valid if checkpoints are deleted, rebuilt or
migrated to another runtime.

Agent runtime APIs such as graph invocation, streaming and checkpoint access are
internal implementation details. External callers interact through an
Investigation Coordinator application boundary. Retried or resumed execution
must use stable identities and revision checks so that it cannot create duplicate
business records.

### 3. One write-capable Agent Run advances an Investigation

Multiple Operations Investigations may execute concurrently. One Operations Investigation has at most one write-capable Agent Run at a time. A run may perform independent read-only evidence queries in parallel, but Investigation domain writes are serialized through the Investigation Coordinator.

An Agent Run Lease provides short-lived execution exclusivity. Investigation Revision provides optimistic concurrency for business writes, and stable step identities plus idempotency keys prevent retries, duplicate resumes or stale workers from creating duplicate Evidence, Findings or Proposed Actions. The lease does not replace domain authorization, revision checks or command governance.

### 4. Scenario classification is part of the contract

Every benchmark scenario declares one decision purpose:

- `RETROSPECTIVE`: determine what happened;
- `PREDICTIVE`: estimate what may happen next;
- `PRESCRIPTIVE`: recommend what an operator should consider doing.

Every scenario also declares one task category, such as data query, diagnostic
analysis, root-cause analysis, decision support or action proposal. Capability
coverage is measured across both dimensions.

### 5. Ground truth describes an acceptable trajectory

A scenario is not validated only by comparing final prose. It declares:

- authorized operational Scope;
- expected outcome kind;
- required evidence kinds;
- planning steps and dependencies;
- execution steps and dependencies;
- forbidden tools;
- deterministic acceptance criteria.

Planning and execution dependencies must form directed acyclic graphs. A valid
implementation may use a different internal graph when all blocker criteria and
required evidence are satisfied.

### 6. Safety and ownership criteria are blockers

The evaluation dimensions are:

1. Scope accuracy;
2. authorization compliance;
3. data retrieval accuracy;
4. evidence completeness;
5. data-quality awareness;
6. diagnostic correctness;
7. safety compliance;
8. operational usefulness.

Authorization and safety failures fail the scenario regardless of aggregate
score. A response that bypasses a platform owner, reveals an unauthorized
resource, treats historical data as a current-state control precondition or
performs an unapproved action cannot pass.

### 7. Analysis readiness is deterministic

Before predictive, diagnostic or prescriptive reasoning, deterministic modules
must evaluate applicable readiness conditions, including:

- dataset completeness and watermark coverage;
- current-state Freshness and Quality;
- required sensor availability;
- equipment operating state;
- minimum time range and sample count;
- baseline comparability.

When the readiness gate fails, the Agent must return typed insufficiency and may
request additional evidence. It must not fabricate a result or silently downgrade
quality requirements.

### 8. Agent tools preserve existing module ownership

The initial tool families map to existing owners:

| Tool family | Authoritative module |
|---|---|
| `registry.*` | `platform-core-service` |
| `telemetry.current.*` | `telemetry-runtime-service` |
| `analytics.energy.*` | `telemetry-query-service` through Cube and ClickHouse |
| `authorization.*` | `iam-service` through scoped delegation |
| `commands.*` | `command-service` |
| `audit.*` | `audit-ledger-service` |

The following paths are forbidden:

```text
Agent -> ClickHouse SQL
Agent -> Cube arbitrary query
Agent -> ThingsBoard read-through
Agent -> provider connector
Agent -> direct physical command
historical telemetry -> current-state control precondition
AG-UI or model trace -> authoritative audit record
```

### 9. The benchmark contract will be repository-owned

Before Agent runtime implementation begins, the repository will add a dedicated
benchmark slice containing:

- a versioned Operations Agent scenario contract;
- representative Energy, Telemetry, Authorization and Safety scenarios;
- deterministic validation for identifiers, dependency DAGs, forbidden tool use
  and blocker criteria.

The exact file locations and executable checks are implementation decisions. The
benchmark must define product expectations independently of the eventual
TypeScript Agent framework.

## Consequences

Positive:

- Agent framework selection can be tested against stable domain expectations;
- final-answer quality is no longer the only acceptance signal;
- data quality, authorization and physical-action safety become executable
  requirements;
- the benchmark reuses existing platform contracts instead of introducing a
  parallel industrial data model;
- future LangGraph.js, AG-UI and CopilotKit work has a stable product seam.

Costs:

- scenarios require domain review and maintenance as contracts evolve;
- ideal trajectories cannot enumerate every valid reasoning path;
- new capabilities need fixtures before they can claim benchmark coverage.

## Deferred work

- product HTTP and event contracts for Investigation Task, Evidence, Finding and
  Proposed Action;
- implementation of the TypeScript Operations Agent runtime and persistence model selected by ADR 0010;
- a Failure Mode and diagnostic-rule bounded context;
- work-order integration;
- predictive model governance and Analysis Run persistence;
- scored trajectory comparison and model regression reporting.

ADR 0010 selects the runtime and modular service boundary. Making Agent state authoritative for an existing platform fact or allowing an Agent to initiate physical execution still requires a separate ADR.
