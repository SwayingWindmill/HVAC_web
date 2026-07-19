# EnergyAgent integration

HVAC Web uses EnergyAgent as a remote, read-only AI operations assistant through a CopilotKit Runtime boundary.

## Runtime topology

```text
HVAC Web (Vite :5173)
  -> /api/v1/copilotkit
Copilot Runtime (:3001)
  -> default Agent / sample_agent graph
EnergyAgent LangGraph (:8123)
```

The Vite proxy routes only `/api/v1/copilotkit` to the AI Runtime. Other `/api/v1` requests continue to target the HVAC backend on port 3000.

## Configuration

EnergyAgent loads its model credentials from shell variables or `agents/energy-agent/.env.local`:

```dotenv
OPENAI_API_KEY=
OPENAI_MODEL=
ENERGY_DATA_SEED=20260716
```

Optional Runtime variables:

```dotenv
AI_RUNTIME_HOST=127.0.0.1
AI_RUNTIME_PORT=3001
AI_RUNTIME_BASE_PATH=/api/v1/copilotkit
ENERGY_AGENT_URL=http://127.0.0.1:8123
ENERGY_AGENT_GRAPH_ID=sample_agent
LANGSMITH_API_KEY=
```

For a separately managed frontend process, enable remote EnergyAgent mode with:

```dotenv
VITE_COPILOTKIT_RUNTIME_URL=/api/v1/copilotkit
VITE_AI_AGENT_PROFILE=energyagent
```

## Development

Start the Agent, Runtime, and HVAC Web together:

```bash
npm run dev:energyagent
```

The services can also be started independently:

```bash
npm run dev:energy-agent
npm run dev:ai-runtime
npm run dev:web
```

When starting them independently, configure the two Vite variables above before `npm run dev:web`.

The production host lives under `apps/hvac-web`. The original Next.js Canvas is retained under `references/energy-agent-next` as a reference and acceptance adapter, while the canonical Python Agent lives under `agents/energy-agent`.

Verify the Agent and Runtime together without making a model request:

```bash
npm run verify:energyagent-stack
```

## Health checks

```text
EnergyAgent:     GET http://127.0.0.1:8123/health
Copilot Runtime: GET http://127.0.0.1:3001/health
Runtime info:    GET http://127.0.0.1:3001/api/v1/copilotkit/info
```

The Runtime health endpoint reports `503 degraded` until the Python Agent is reachable.

## Supported first-turn scope

The current EnergyAgent graph analyzes its deterministic Building A/B/C dataset. A natural-language request must include:

- one Building: `Building A`, `Building B`, or `Building C`;
- one time range: `昨天`, `最近 24 小时`, `最近 7 天`, or two ISO 8601 boundaries.

EnergyAgent mode changes the HVAC assistant's first-turn suggestions and placeholder so requests satisfy this contract.

## Safety boundary

This integration exposes analysis and evidence only. It does not register equipment control, work-order mutation, alarm acknowledgement, or optimization dispatch tools. Any future write action must pass authenticated backend authorization, audit logging, and explicit human approval.
