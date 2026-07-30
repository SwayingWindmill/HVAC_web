# Operations Agent service

This package is the separately deployable TypeScript boundary for the Operations Agent.
It currently contains only the accepted modular-monolith skeleton and automated inward
dependency rules. It does not connect a browser, Platform Gateway, model provider,
LangGraph.js runtime, database, scheduler or platform tool.

The module direction is:

```text
adapters -> application -> domain
bootstrap -> adapters
```

Every module is imported through its `index.ts` entry. Cross-module imports into an
`internal` path are rejected by the repository boundary check.

Run the package checks from the repository root:

```bash
npm --prefix services/operations-agent-service run check
```

The emitted package is written to `services/operations-agent-service/dist` and is not
source controlled.
