# platform-gateway

`platform-gateway` is the only public Go platform service. It owns the browser-facing HTTP edge contract and contains no Organization, Site, Device, Telemetry, Command, Schedule, AI, persistence, event or audit business state.

## Public contract

The checked-in authority is `contracts/http/platform-gateway.openapi.yaml`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/health` | Read Gateway health. `includeBuild=true` includes build identity. |
| `GET` | `/api/v1/version` | Read the running build identity. |
| `GET` | `/api/v1/auth/login` | Begin OIDC Authorization Code Flow with PKCE. |
| `GET` | `/api/v1/auth/callback` | Complete the OIDC callback and create a BFF Session. |
| `GET` | `/api/v1/principal` | Read the authenticated principal and actor chain through IAM. |
| `POST` | `/api/v1/auth/logout` | Revoke the current session with Origin and CSRF protection. |
| `POST` | `/api/v1/auth/sessions/{sessionId}/revoke` | Administratively revoke a session. |

Successful responses are typed resources without a global response envelope. Public failures use `application/problem+json` and include stable `code`, `traceId`, `retryable` and safe detail.

Gateway accepts a valid `X-Request-ID`, continues a valid W3C `traceparent`, and emits both headers on every response. Structured request logs contain safe route templates, method, status, duration, request ID and trace ID only. Cookies, authorization headers, identity headers, query values, authorization codes, grants and request bodies are not logged.

## Identity boundary

OIDC tokens are exchanged and encrypted on the server. The browser receives only the opaque `__Host-hvac_session` cookie with `Secure`, `HttpOnly`, `Path=/` and `SameSite=Lax`.

Gateway calls private IAM through TLS 1.3 mutual authentication. It signs a short-lived delegation bound to the Gateway SPIFFE identity, IAM audience, one action, one session scope, acting Organization, policy revision and expiry. Public caller-supplied identity or delegation headers are rejected.

Detailed identity and threat-boundary documentation is in `docs/security/s0-authenticated-principal.md`.

## Local S0 topologies

Contract-only topology:

```bash
npm run dev:s0-gateway
```

Authenticated principal topology:

```bash
npm run dev:s0-auth
```

The authenticated topology generates a temporary test CA and starts only HTTPS HVAC Web, HTTPS OIDC fixture, Gateway and mTLS-only IAM. It does not start NestJS, Copilot Runtime, a database, broker or later S0 services.

## Verification

```bash
npm run contracts:generate
npm run contracts:check
npm run test:gateway
npm run test:identity
npm run build:gateway
npm run build:iam
npm run build:oidc-fixture
npm run lint
npm run build
npm run audit:platform-gateway
npm run audit:auth-principal
```

The browser audits require Microsoft Edge and use isolated processes and operating-system temporary directories.

## Build identity

Release builds can inject build fields without changing source:

```bash
go build -ldflags "-X main.version=0.2.0 -X main.commit=$(git rev-parse HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./services/platform-gateway/cmd/platform-gateway
```

The default local values are `dev`, `unknown`, and `unknown`.
