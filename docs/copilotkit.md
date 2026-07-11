# CopilotKit integration

> This document describes the current frontend integration baseline. The target Agent product, runtime, context, tool, approval, audit, and rollout architecture is defined in `docs/ai-agent-product-technical-design.md`.

The frontend supports two AI modes:

1. **Local fallback mode** — used when `VITE_COPILOTKIT_RUNTIME_URL` is empty. The global Ant Design drawer uses the existing mock streaming assistant and remains available on every AppShell page.
2. **CopilotKit mode** — enabled when `VITE_COPILOTKIT_RUNTIME_URL` points to a trusted CopilotKit Runtime endpoint, for example `/api/v1/copilotkit`.

Example local environment value:

```bash
VITE_COPILOTKIT_RUNTIME_URL=/api/v1/copilotkit
```

The runtime endpoint must be implemented by the backend. It is responsible for model credentials, authentication, RBAC, tool authorization, audit logging, and human approval for write operations. Model credentials must never be exposed through Vite environment variables.

The current frontend integration exposes:

- current route and page description;
- selected building and role;
- work-order, FDD, and optimization summary counts;
- permitted application routes;
- a permission-aware `navigate_to_page` frontend action;
- an `open_ai_workspace` frontend action.

No device-control action is exposed.
