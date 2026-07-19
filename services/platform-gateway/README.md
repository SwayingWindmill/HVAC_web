# platform-gateway

`platform-gateway` is the only public Go service introduced by S0 ticket 01. It owns the public HTTP edge contract and contains no Organization, Site, Device, Telemetry, Command, Schedule, AI, identity, persistence, event, or audit business state.

## Public contract

The checked-in authority is `contracts/http/platform-gateway.openapi.yaml`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/health` | Read Gateway health. `includeBuild=true` includes build identity. |
| `GET` | `/api/v1/version` | Read the running build identity. |

Successful responses are typed resources without a global response envelope. Public failures use `application/problem+json` and always include `code`, `traceId`, and `retryable`.

Gateway accepts a valid `X-Request-ID`, continues a valid W3C `traceparent`, and emits both headers on every response. Structured request logs include method, path, status, duration, request ID, and trace ID only. Cookies, authorization headers, query values, and request bodies are not logged.

## Local S0 topology

From the repository root:

```bash
npm run dev:s0-gateway
```

This starts only:

```text
Browser -> HVAC Web (Vite :5173) -> platform-gateway (:8080)
```

No NestJS service, database, broker, OIDC provider, IAM service, or Audit service is started by this ticket. The launcher sets `S0_GATEWAY_ONLY=true`, so Vite registers only the `/api/v1` Gateway proxy; Legacy, Copilot, and WebSocket proxy routes are absent from this topology.

## Verification

```bash
npm run contracts:generate      # regenerate checked-in Go and TypeScript artifacts
npm run contracts:check         # fail when generated artifacts drift
npm run test:gateway            # Go black-box contract tests
npm run build:gateway           # independently build the Gateway binary
npm run lint                    # HVAC Web typecheck
npm run build                   # HVAC Web production build
npm run audit:platform-gateway  # Edge browser audit through Vite and Gateway
```

The browser audit requires Microsoft Edge and starts isolated Gateway and Vite processes on audit-only ports.

## Build identity

Release builds can inject build fields without changing source:

```bash
go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./services/platform-gateway/cmd/platform-gateway
```

The default local values are `dev`, `unknown`, and `unknown`.
