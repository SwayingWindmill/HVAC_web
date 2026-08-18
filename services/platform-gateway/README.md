# platform-gateway

`platform-gateway` is the only public Go platform service. It owns the browser-facing HTTP edge contract, durable BFF Session transaction boundary, Audit Intent and Transactional Outbox. It contains no Tenant, Site, Device, Telemetry, Command, Schedule or AI business-domain state.

## Public contract

The checked-in authority is `contracts/http/platform-gateway.openapi.yaml`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/health` | Read Gateway health. `includeBuild=true` includes build identity. |
| `GET` | `/api/v1/version` | Read the running build identity. |
| `GET` | `/api/v1/platform/status` | Read the Gateway-owned native Go platform status. |
| `POST` | `/api/v1/auth/login` | Begin OIDC Authorization Code Flow with PKCE. |
| `GET` | `/api/v1/auth/callback` | Complete the OIDC callback and create a BFF Session. |
| `GET` | `/api/v1/principal` | Read the authenticated principal and actor chain through IAM. |
| `POST` | `/api/v1/auth/logout` | Revoke the current BFF Session with Origin and CSRF protection, then return the trusted OIDC end-session URL in `Location` so the browser can clear the provider SSO session. |
| `POST` | `/api/v1/auth/sessions/{sessionId}/revoke` | Administratively revoke a session and commit its Audit message. |
| `GET` | `/api/v1/audit/session-events/{messageId}` | Read one Tenant-scoped append-only Session Audit record. |

Successful responses are typed resources without a global response envelope. Public failures use `application/problem+json` and include stable `code`, `traceId`, `retryable` and safe detail.

Gateway accepts a valid `X-Request-ID`, continues a valid W3C `traceparent`, and emits both headers on every response. Structured request logs contain safe route templates, method, status, duration, request ID and trace ID only. Cookies, authorization headers, identity headers, query values, authorization codes, grants and request bodies are not logged.

## Identity boundary

OIDC tokens are exchanged and encrypted on the server. The browser receives only the opaque `__Host-hvac_session` cookie with `Secure`, `HttpOnly`, `Path=/` and `SameSite=Lax`.

Gateway implements the standard OIDC Authorization Code flow with S256 PKCE, Discovery, code exchange, JWKS retrieval and JOSE signature verification. The protocol boundary is provider-neutral; the Phase 1 deployment uses the platform-owned `identity-service` as the default issuer.

The OIDC provider is deliberately not the BFF Session or authorization authority. Gateway retains one-time state with TTL and local `returnTo` binding, nonce generation and constant-time validation, strict JWT and token-type checks, `nbf` enforcement, bounded provider responses, encrypted durable Session storage, audit/outbox transactions and platform-owned Tenant authorization. Provider role or custom claims are never Registry authorization truth.

Gateway calls private IAM through TLS 1.3 mutual authentication. It signs a short-lived delegation bound to the Gateway SPIFFE identity, IAM audience, one action, one session scope, acting Tenant, policy revision and expiry. Public caller-supplied identity or delegation headers are rejected.

Detailed identity and threat-boundary documentation is in `docs/security/s0-authenticated-principal.md`.

## Durable Session and Audit boundary

When identity is enabled, production configuration requires `GATEWAY_DATABASE_URL` and `AUDIT_URL`. In-memory Session storage or missing Audit Ledger is allowed only through explicit S0 test compatibility flags.

Session creation, logout and administrative revocation use one PostgreSQL transaction for Session state, Audit Intent and Protobuf Outbox. A successful mutation exposes its stable `X-Audit-Message-ID`; cookie issuance occurs only after commit. Gateway queries private Audit Ledger with a separate mTLS delegation constrained to `audit:read` and the current Tenant.

The complete transaction, Relay, Inbox, RLS and hash-chain model is documented in `docs/security/s0-durable-session-audit.md`.

## Route and Data Ownership boundary

Gateway loads `contracts/ownership/route-ownership.v1.json` as an immutable validated snapshot. Every public request resolves exactly one deterministic method/path owner before handler execution; active ownership has no percentage cohort, shadow owner, Legacy owner or request fallback. The applied registry revision is emitted as `X-Route-Policy-Revision`.

Accepted registry changes require monotonic registry and route revisions. Gateway persists an append-only route policy audit before atomically replacing the snapshot, so in-flight requests retain their original decision and only future requests observe the new policy.

Legacy routing adapters are not part of the active Gateway. Historical migration contracts remain evidence only and are rejected by the active ownership parser.

See `docs/security/s0-route-data-ownership.md` for Registry, Data Ownership, rollback, anti-corruption, timeout and circuit-breaker details.

## Local S0 topologies

Contract-only topology:

```bash
npm run dev:s0-gateway
```

Authenticated principal topology:

```bash
npm run dev:s0-auth
```

The authenticated topology generates a temporary test CA and starts only HTTPS HVAC Web, HTTPS OIDC fixture, Gateway and mTLS-only IAM. It uses explicit ticket-02 compatibility flags and does not start a database or broker.

Durable Session and Audit topology:

```bash
npm run dev:s0-durable
```

This starts PostgreSQL, Redpanda, OIDC, IAM, Audit Ledger, Outbox Relay, the frozen private NestJS health adapter, Gateway and HTTPS HVAC Web in an isolated local topology.

## Verification

```bash
npm run contracts:generate
npm run contracts:check
npm run events:check
npm run ownership:check
npm run test:gateway
npm run test:identity
npm run test:durable-unit
npm run test:ownership
npm run test:legacy-compatibility
npm run test:durable-postgres
npm run build:gateway
npm run build:legacy-compatibility
npm run build:iam
npm run build:audit-ledger
npm run build:outbox-relay
npm run build:oidc-fixture
npm run lint
npm run build
npm run audit:platform-gateway
npm run audit:auth-principal
npm run audit:durable-session
npm run audit:route-ownership
```

The durable browser audit requires Docker plus a CDP-compatible Edge, Chrome or Chromium browser. All audit topologies use isolated processes and operating-system temporary directories.

## Build identity

Release builds can inject build fields without changing source:

```bash
go build -ldflags "-X main.version=0.3.0 -X main.commit=$(git rev-parse HEAD) -X main.builtAt=$(date -u +%Y-%m-%dT%H:%M:%SZ)" ./services/platform-gateway/cmd/platform-gateway
```

The default local values are `dev`, `unknown`, and `unknown`.
