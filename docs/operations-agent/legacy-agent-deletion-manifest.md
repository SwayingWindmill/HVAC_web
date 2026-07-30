# Legacy Agent deletion manifest

Status: completed

Date: 2026-07-30

Tracking: GitHub Map #118; Ticket #119 defined the manifest and Ticket #120 completed it.

This document defines the complete deletion surface for the retired Python EnergyAgent stack. It is not a migration guide. No graph, prompt, fixture, checkpoint, protocol, UI adapter or runtime behavior from the legacy stack is an input to the TypeScript Operations Agent.

## Freeze rule

Until Ticket #120 removes the legacy stack:

- do not add features, fixes, prompts, fixtures, tests or deployment support to it;
- do not copy its contracts or implementation into the new Operations Agent;
- do not use it as a fallback runtime or acceptance oracle;
- only changes required to make deletion safe are permitted.

## Classification rules

Every discovered item is classified as:

- **DELETE** — owned exclusively by the retired stack and removed entirely;
- **REWRITE** — contains both legacy assumptions and still-valid product behavior, so the legacy branch is removed while the remaining behavior is preserved;
- **PRESERVE** — independent of the retired stack and required by the existing product or ADR 0010.

## DELETE

### Python Agent implementation

Delete the entire `agents/energy-agent` tree, including:

- Python source under `src`;
- LangGraph workflow, scope and contract modules;
- Investigation state implementation;
- energy data, analysis, runtime and tool modules;
- A2UI catalog code;
- HTTP and configuration modules;
- all Python tests and JSON fixtures;
- `main.py`;
- `langgraph.json` and `langgraph.acceptance.json`;
- `pyproject.toml` and `uv.lock`;
- README content;
- all `__pycache__` and `.pyc` artifacts.

The Python source and tests are not mined for interfaces, fixtures or expected behavior.

### Reference Next.js application

Delete the entire ignored `references/energy-agent-next` tree, including:

- Next.js application and CopilotKit Runtime route;
- custom A2UI catalog and actions;
- Investigation Canvas and state components;
- acceptance scripts, screenshots, recordings and evidence;
- legacy specifications and development documentation;
- its package manifest, lockfile and build artifacts.

Remove the corresponding `.gitignore` entry when the directory is gone.

### Dedicated Copilot Runtime

Delete the entire `runtimes/copilot-runtime` tree.

The current server is exclusively coupled to a remote Python LangGraph deployment through:

- `ENERGY_AGENT_URL`;
- `ENERGY_AGENT_GRAPH_ID`;
- the `sample_agent` graph identity;
- direct health probing of port 8123;
- direct browser-facing runtime service on port 3001.

It must not be generalized into the new service. ADR 0010 places AG-UI behind Platform Gateway and treats any CopilotKit Runtime as an optional internal adapter only.

### Legacy development and verification scripts

Delete:

- `scripts/dev-energyagent.mjs`;
- `scripts/verify-energyagent-stack.mjs`;
- `scripts/verify-ai-runtime.mjs`.

Delete these root package scripts:

- `dev:energy-agent`;
- `dev:ai-runtime`;
- `dev:energyagent`;
- `verify:ai-runtime`;
- `verify:energyagent-stack`.

No replacement launcher is created in Map 0. The new TypeScript service receives its own launcher only after its application and runtime seams exist.

### Runtime-only dependency

Remove the root `@copilotkit/runtime` dependency and its lockfile entries after confirming no remaining source import exists.

Do not remove `@copilotkit/react-core`; it is used by the browser application independently of the retired server runtime.

### Legacy integration document

Delete `docs/energyagent-integration.md` after this manifest is accepted. Keeping a deprecated topology document would preserve the old stack as a discoverable reference, which the accepted decision explicitly rejects.

### Local and generated legacy references

The following are not product source and must not be treated as migration inputs:

- `.ai-bridge` context snapshots;
- `out` audit or generated files;
- local `.scratch` research that cites the old implementation;
- copies inside separate `.worktrees`.

Ticket #120 must not edit separate worktrees. Local generated snapshots may be regenerated or removed when convenient, but they do not block deletion once active source, documentation and executable configuration are clean.

## REWRITE

### Frontend Agent configuration

Rewrite `apps/hvac-web/src/ai/config.ts`:

- remove `VITE_AI_AGENT_PROFILE` handling;
- remove the `energyagent` profile and `ENERGY_AGENT_PROFILE_ENABLED`;
- remove the current arbitrary remote Runtime toggle based on `VITE_COPILOTKIT_RUNTIME_URL`;
- preserve the product assistant identity.

Until Map 4 adds a Platform-Gateway-owned Operations Agent route, the existing UI uses the local read-only `HvacMockAgent` only.

Rewrite `apps/hvac-web/src/vite-env.d.ts` to remove:

- `VITE_AI_AGENT_PROFILE`;
- `VITE_COPILOTKIT_RUNTIME_URL`.

A future gateway-backed configuration must be introduced under a new contract rather than reusing the direct Runtime environment switch.

### Frontend provider

Rewrite `apps/hvac-web/src/ai/AiProvider.tsx` to remove the direct remote Runtime branch and always register the current local self-managed Agent during the gap before Map 4.

Preserve the CopilotKit Provider, thread history bridge and existing UI ownership. Do not replace this with a new Agent backend during Ticket #120.

### Frontend context

Rewrite `apps/hvac-web/src/ai/context.ts` to remove:

- Building A/B/C legacy prompts;
- the legacy input placeholder;
- conditional prompt and placeholder selection based on the retired profile.

Preserve route-aware HVAC prompts, Scope labels and existing product context.

### Vite development proxy

Rewrite `apps/hvac-web/vite.shared.config.ts` to remove:

- `AI_RUNTIME_PROXY_TARGET`;
- the dedicated demo `/api/v1/copilotkit` proxy to port 3001.

Preserve Platform Gateway, legacy application API and websocket proxy behavior unrelated to the retired Agent. A future Operations Agent route is added through Platform Gateway, not a direct Vite-to-Agent proxy.

### Root package and lockfile

Rewrite `package.json` and `package-lock.json` using targeted edits:

- remove only the scripts and dependency listed in this manifest;
- preserve every unrelated script, dependency and current branch change;
- regenerate lockfile state through the package manager rather than manually deleting arbitrary transitive blocks.

### Product documentation

Rewrite these documents to remove active legacy topology and direct Runtime instructions:

- `README.md`;
- `apps/hvac-web/README.md`;
- `docs/copilotkit.md`;
- `DESIGN.md`.

Required resulting semantics:

- current browser AI remains a local read-only demo capability after legacy deletion;
- no Python Agent or dedicated Runtime exists;
- remote Operations Agent integration is deferred to the accepted Platform Gateway, AG-UI and CopilotKit Headless architecture;
- the authoritative target design is ADR 0009, ADR 0010 and `docs/operations-agent/framework-architecture.md`.

## PRESERVE

### Browser CopilotKit capability

Preserve `@copilotkit/react-core` and the existing browser-side CopilotKit integration that is independent of the Python Agent, including:

- the CopilotKit Provider after it is made local-only;
- `CopilotContextBridge`;
- global assistant and `/ai` workspace surfaces;
- thread history and session handling;
- `HvacMockAgent` and its local read-only AG-UI stream;
- existing frontend tools and Generative UI cards;
- shared Zod schemas;
- the Vite `vendor-copilotkit` chunk rule.

These assets remain temporary product UI and test seams. Preserving them does not authorize reuse of the legacy server Runtime or make the local Mock Agent an Operations Investigation authority.

### Platform modules and contracts

Preserve all Registry, Telemetry Runtime, Telemetry Query, IAM, Command, Audit and Platform Gateway services and contracts. The old Agent does not own them and its deletion must not alter their behavior.

### Operations Agent architecture documents

Preserve:

- ADR 0009;
- ADR 0010;
- the framework architecture;
- the framework grilling log;
- the benchmark design;
- the implementation plan;
- this deletion manifest;
- the root domain glossary additions.

## Environment variables and ports to eliminate

Remove all active code, scripts and user-facing documentation for:

```text
ENERGY_AGENT_URL
ENERGY_AGENT_GRAPH_ID
AI_RUNTIME_HOST
AI_RUNTIME_PORT
AI_RUNTIME_BASE_PATH
AI_RUNTIME_PROXY_TARGET
VITE_AI_AGENT_PROFILE
VITE_COPILOTKIT_RUNTIME_URL
```

Remove legacy ownership of:

```text
8123  Python LangGraph Agent
3001  dedicated Copilot Runtime
/api/v1/copilotkit  direct demo proxy to the dedicated Runtime
sample_agent  legacy graph identity
```

The `/api/v1/copilotkit` string may return only in a future Platform Gateway contract created by a later Map. Ticket #120 does not preserve the current direct-proxy behavior.

## Final active-source search

After deletion, search active source, configuration and user-facing documentation for these tokens:

```text
agents/energy-agent
references/energy-agent-next
runtimes/copilot-runtime
energyagent
EnergyAgent
ENERGY_AGENT_
sample_agent
VITE_AI_AGENT_PROFILE
AI_RUNTIME_PROXY_TARGET
dev:energy-agent
dev:energyagent
dev:ai-runtime
verify:ai-runtime
verify:energyagent-stack
Building A
Building B
Building C
127.0.0.1:8123
127.0.0.1:3001
```

Matches are acceptable only inside this manifest, accepted architecture records explaining the deletion, separate worktrees or non-authoritative generated/private snapshots. No executable source, active product documentation, package script, environment example, deployment asset or CI workflow may retain them.

## Verification matrix

Run the same minimum behavior checks before and after Ticket #120:

```text
npm run lint
npm run test:rms-real-build-audit
npm run build:demo
npm run build:real
npm run rms:real:graph
npm run rms:real:bundle
npm run security:dependency-audit
npm run security:licenses
```

Purpose:

- `lint` proves the rewritten frontend configuration remains type-safe;
- the RMS Vite audit proves real-mode routing and build ownership are unchanged;
- demo and real builds prove the browser remains buildable without the legacy runtime;
- real graph and bundle checks prevent accidental legacy code inclusion or real-mode regression;
- dependency audit and license checks validate removal of the server Runtime dependency and lockfile update.

Ticket #120 may add a narrowly targeted static policy test for forbidden legacy identifiers when that is the highest stable seam. It must not add tests that encode the old Agent's behavior.

## Deletion execution order

Ticket #120 should execute in this order to keep failures local and understandable:

1. capture baseline verification results;
2. remove Python and reference application directories;
3. remove dedicated Runtime and legacy scripts;
4. remove package scripts and runtime dependency, then regenerate the lockfile;
5. rewrite frontend configuration, provider, context and Vite proxy;
6. rewrite or delete active documentation;
7. remove the obsolete ignore rule;
8. run final token searches;
9. run the verification matrix;
10. review the complete diff for unrelated changes.

## Completion statement for Ticket #119

The deletion surface is now explicit. Ticket #120 has no remaining architectural decision: it performs the classified deletions and rewrites, preserves the listed generic capabilities, and proves the repository remains green.

## Ticket #120 execution result

Completed on 2026-07-30:

- the tracked Python Agent, dedicated Copilot Runtime, legacy scripts and integration document were deleted;
- the ignored Next.js reference application was removed from the working tree;
- the browser no longer exposes a remote Runtime environment switch or direct Vite proxy and now uses only the local read-only `HvacMockAgent`;
- `@copilotkit/runtime` and its 257 exclusively reachable packages were removed; the production dependency count fell from 835 to 608;
- active source and user-facing documentation contain no executable legacy Agent identifier or fallback route;
- all eight commands in the verification matrix passed;
- the final production dependency audit reported zero low, moderate, high or critical findings.

Matches retained in this document, the implementation plan, separate worktrees, private context snapshots or unrelated Legacy backend fixtures are historical or independently owned and are not active Operations Agent wiring.
