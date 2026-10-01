# Pi Agent source review

Status: accepted reference baseline

Date: 2026-09-03

## Reference pin

The adopted upstream reference is Earendil Works Pi:

- repository: `https://github.com/earendil-works/pi`
- release: `v0.84.4`
- npm packages: `@earendil-works/pi-agent-core@0.84.4`, `@earendil-works/pi-ai@0.84.4`
- npm `gitHead`: `b79e4cc834970cca69daebffab7df1da7d1e52c4`
- runtime floor: Node `>=22.19.0`
- project runtime floor: Node `>=22.22.0`

The package versions must be exact pins while Pi remains on the fast-moving `0.x` release line. The repository lockfile remains the installation ground truth.

## Upstream material reviewed

The review covered the official release documentation, source and tests around the production embedding seam rather than the coding-agent product shell:

- root `README.md` — package split and permission/containerization boundary;
- `packages/agent/src/agent.ts` — `Agent` lifecycle and stable embedding surface;
- `packages/agent/src/types.ts` — Agent state, tools, hooks and execution mode contracts;
- `packages/agent/test/e2e.test.ts` — direct `Agent` use with deterministic provider responses;
- `packages/agent/docs/harness-v2-state-machine.md` — durable harness direction and explicit non-canonical status;
- `packages/agent/src/harness/session/session.ts` and `packages/agent/src/harness/reducer.ts` — durable session/recovery concepts under active development;
- `packages/ai/README.md` — `Models`, provider factories, auth, streaming, faux provider, cross-provider context and bundle guidance.

This review treats current HVAC Agent code as unverified incumbent code. Existing LangGraph, OpenAI-specific, checkpoint or CopilotKit integration is retained only when it independently satisfies the target product contract better than the adopted Pi-informed design.

## Relevant upstream behavior

### Agent Core

`@earendil-works/pi-agent-core` is a stateful Agent runtime built on `pi-ai`. The stable embedding path uses `Agent`, a model, a system prompt and a tool set. The runtime emits Agent events and owns the model/tool loop for one in-memory Agent state.

Pi exposes tool lifecycle hooks and supports tool execution ordering. These are useful integration seams, but they are not an authorization system. Upstream explicitly states that Pi inherits the filesystem, process, network and credential permissions of the process that launched it unless the embedding application adds stronger boundaries.

### Model runtime

`@earendil-works/pi-ai` uses a `Models` collection with provider-owned catalogs, auth and streaming. The release supports OpenAI, DeepSeek, Anthropic, Google and many other providers. Provider-specific factories such as `@earendil-works/pi-ai/providers/openai` load only that provider's catalog/lazy API wrapper.

The `providers/all` entry point deliberately imports all built-in provider factories and catalogs. The release documentation recommends individual provider factories when bundle/runtime surface matters and warns against the legacy `compat` entry point for new bundled applications.

### Tests

Pi ships a deterministic faux provider that can script text, thinking and tool calls without external credentials. Upstream Agent tests exercise the Agent against fake provider behavior instead of paid provider APIs. This is the preferred pattern for HVAC runtime contract tests.

### Durable Harness

Pi is actively developing a richer durable `AgentHarness`. The `harness-v2-state-machine.md` document explicitly says it is a working handoff document and not yet the canonical implementation specification. Its design contains valuable ideas — explicit effect intent, replay safety, total operation state, usage ledgers and recoverable session records — but those APIs are not an acceptable production dependency for the first HVAC Pi release.

## ADOPT

### 1. `Agent` as the first production Pi execution primitive

Use the stable `Agent` API for the first runtime. Do not build the first product slice on the non-canonical Harness v2 state machine.

Reason: smallest supported embedding surface, direct upstream tests, less framework-owned durable state, and easier replacement later.

### 2. `pi-ai` provider collection and individual provider factories

Use `createModels()` and register only explicitly supported providers. The initial provider set should be minimal and server-only. Do not import `providers/all`.

Reason: provider neutrality without importing the complete model ecosystem.

### 3. Deterministic faux provider testing

Use Pi's faux provider for Agent loop/tool/event tests. External provider tests are optional environment-bound integration tests and must not be the primary correctness gate.

Reason: deterministic, fast and credential-free tests matching upstream practice.

### 4. Pi tool loop and read parallelism

Let Pi own model -> tool -> model iteration. Independent HVAC READ capabilities may execute in parallel; side-effecting/proposal operations are serialized.

Reason: a real Agent should select evidence-gathering capabilities instead of encoding another static graph in a different API.

### 5. Tool failure as an actual error

Tool execution failures must surface as typed failures rather than successful text that says `error`. The Agent must be able to distinguish unavailable data from valid empty business data.

### 6. Provider-neutral usage and model metadata

Capture provider/model identity, latency and token/usage accounting from Pi's normalized model interface, subject to the project's data-minimization rules.

## ADAPT

### 1. Tool definitions remain HVAC-owned

Pi's tool types are adapter types, not the business capability contract. The project defines the semantic tool catalog and maps it into Pi at the runtime boundary.

The model sees semantic capabilities such as:

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
investigation.complete
```

It never sees raw REST paths, SQL, ClickHouse, ThingsBoard addresses, database handles or arbitrary HTTP.

### 2. Trusted Scope is runtime context, not model input

Tenant, Site, principal identity, capabilities, delegation and correlation identities are supplied by the server execution context. They are not normal model-generated tool parameters.

Tool execution combines validated model arguments with trusted runtime context and reauthorizes at the owning service boundary.

### 3. Pi hooks are policy preflight, not final authority

`beforeToolCall` may enforce Agent-local allowlists, budget and execution policy. The actual Tool executor must still call the authoritative owner using server-owned identity/scope and must accept only owner-validated results.

A Pi hook cannot become the only authorization or OT safety boundary.

### 4. Pi events are translated to project events

No Pi event/type is public API. Pi events are projected into a versioned HVAC Agent event contract before Gateway/Web exposure.

This keeps Pi event mechanics out of browser and business contracts while allowing each layer to evolve around its own product responsibility.

### 5. Durable state stays project-owned in the first release

Persist only the minimum durable Agent session/run facts needed to reconstruct a Pi `Agent` state after restart. Do not copy Pi Harness v2's complete storage model before the upstream API is canonical.

Use the upstream Harness design only as a source of replay-safety principles: record repeat-sensitive external-effect intent, require idempotency or explicit safe replay, and never claim exactly-once behavior that the external system cannot provide.

### 6. Structured terminal outcome

A dedicated `investigation.complete` Tool converts the free-form Agent loop into a typed product result. It validates outcome, evidence references, limitations and required-next actions before completion.

The terminal product result is an application/business record; the final LLM prose is not automatically authoritative.

## REJECT

### 1. Pi Coding Agent runtime in production HVAC

Do not depend on `@earendil-works/pi-coding-agent`, Pi TUI, coding-agent extensions, filesystem tools, bash tools, generic edit/write tools or generic browser/network tools.

Reason: they solve a coding-agent problem and would unnecessarily expose process/filesystem/network authority in an industrial service.

### 2. `providers/all` and `compat` in production code

Do not register all providers by default and do not use the legacy compatibility entry point in new runtime code.

### 3. Model-controlled Tenant/Site/authorization

Reject any Tool API where the model chooses the authoritative Tenant, Site, principal, capability grant or credential reference.

### 4. Direct physical execution Tools

The first Pi Agent receives no direct `command.execute`, setpoint write, arbitrary RPC or transport Tool. A model may produce a typed proposal. Any later write path must go through the owning domain's approval, lease/fence, idempotency and verification contract.

### 5. Hidden reasoning persistence

Do not persist model chain-of-thought/reasoning streams, raw provider request bodies, raw provider responses or secrets as business/Audit facts. Persist only the minimum user-visible transcript, finalized Tool facts/references, usage metadata and typed product outcomes required by the product.

### 6. Pi-specific types outside the adapter boundary

Application commands, persistence schemas, Gateway/OpenAPI contracts and Web source must not import Pi types.

## Initial provider policy

Start with one production provider, not a broad provider matrix. The first implementation should use one exact allowlisted provider/model end to end and a faux provider in tests. Add a second provider only when there is a concrete deployment or evaluation requirement.

`pi-ai` is adopted because it removes provider lock-in; provider plurality is not itself a release requirement.

## Upgrade policy

- pin `pi-agent-core` and `pi-ai` exactly;
- upgrade both together from the same Pi release/gitHead;
- before each version bump, review upstream `packages/agent`, `packages/ai`, release notes and affected tests;
- run the project Agent benchmark and production contract tests before accepting an upgrade;
- do not adopt a newly published Pi subsystem merely because it exists — it must replace project complexity or solve a current requirement.

## Result

Pi is adopted as the current Agent execution engine, not as the HVAC business architecture. The project will lean on Pi for the parts it does well — provider-neutral model execution, Agent loop, tool execution and events — while keeping industrial scope, authorization, business truth, approval, audit and public contracts outside the Pi dependency boundary.
