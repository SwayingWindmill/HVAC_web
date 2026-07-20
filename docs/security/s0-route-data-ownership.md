# S0 Route and Data Ownership

## Purpose

S0 exposes one public API boundary through `platform-gateway`. Route ownership and data write ownership are explicit, versioned contracts rather than conventions embedded in proxy code or service memory.

The authoritative registries are:

- `contracts/ownership/route-ownership.v1.json`
- `contracts/ownership/data-ownership.v1.json`
- `contracts/ownership/ownership.v1.lock.json`

`npm run ownership:check` rejects ambiguous ownership, revision rollback, invalid rollout dimensions and unauthorized cross-service database writers.

## Route Ownership Registry

Every public route declares:

- HTTP method and `/api/v1` path template;
- one declared owner;
- a monotonic route revision;
- rollout mode and fallback owner;
- compatibility mode;
- the only scope dimensions permitted to influence routing.

Gateway loads an immutable validated snapshot. A request reads exactly one snapshot and resolves exactly one owner before executing a handler. Missing ownership, overlapping templates, conflicting ownership or an invalid cohort configuration fails closed.

The applied registry revision is returned in `X-Route-Policy-Revision`. Representations whose behavior depends on routing also contain `routePolicyRevision` and `routeRevision`.

## Stable cohort selection

Percentage rollout uses a SHA-256 bucket derived from:

```text
cohortSalt + NUL + actingOrganizationId + NUL + initiatingPrincipalSubject
```

Organization and principal are read from the authenticated durable BFF Session. Client cookies, query parameters and identity headers cannot select a cohort. Repeated requests for the same business identity therefore remain stable until a newer policy revision is applied.

## Policy changes and rollback

The registry watcher accepts only a higher registry revision. A route owner, compatibility mode or rollout change also requires a higher route revision.

Before an accepted snapshot becomes active, Gateway appends a `ROUTE_POLICY_CHANGED` record to PostgreSQL. Audit persistence failure leaves the previous snapshot active. Atomic snapshot replacement means an in-flight request keeps its original decision while later requests use the new revision.

Rollback is represented by a new higher revision selecting the previous owner. Writing an older registry revision is rejected and does not change active routing.

## Auditable route decisions

Each applied decision is recorded in `gateway.route_audit_records` before the target handler executes. Records contain only:

- route template, selected owner and revisions;
- compatibility mode and optional cohort bucket;
- initiating principal and acting Organization;
- executing Gateway workload identity;
- policy, request correlation and trace identifiers;
- event time.

The table has an append-only database trigger. The Gateway runtime has `SELECT` and `INSERT` only. Session cookie values, CSRF values, tokens, authorization codes and delegation grants are never stored.

## Frozen Legacy read path

The only S0 Legacy-owned demonstration route is:

```text
GET /api/v1/platform/status
```

Gateway may satisfy it through the existing NestJS `GET /api/v1/health` endpoint. No Legacy business endpoint or write capability was added.

In `LEGACY_PRIVATE_MODE=true`, NestJS:

- loads only the existing health controller and service;
- binds to a private address;
- requires TLS 1.3 mutual authentication;
- accepts only the configured Gateway SPIFFE workload identity;
- accepts only `GET /api/v1/health`;
- rejects browser cookies, authorization and caller-supplied identity/admin headers;
- verifies a signed delegation constrained to audience `legacy-hvac-backend`, action `legacy:platform-status:read`, one Organization scope and a lifetime no longer than 60 seconds.

Gateway does not forward browser headers. The delegation contains a one-way Audit Aggregate ID instead of the raw BFF Session identifier.

## Anti-corruption layer

The Legacy health envelope contains Legacy-specific code/message fields, process uptime, memory details and internal trace data. Gateway extracts only the allowlisted status, version and timestamp and returns the generated `PlatformStatusResponse`:

- `service: platform-status`;
- `implementation: legacy` or `go`;
- normalized status and version;
- applied route revisions and compatibility mode.

Legacy envelopes and internal implementation details never become part of the public contract.

## Failure behavior

The Legacy client has a bounded request timeout and a local circuit breaker.

- deadline exceeded: HTTP 504, `LEGACY_TIMEOUT`;
- open circuit: HTTP 503, `LEGACY_CIRCUIT_OPEN`;
- transport unavailable or rejected/invalid response: stable retryable Problem Details.

The circuit opens after the configured number of consecutive failures and makes no upstream request until its open interval expires.

## Data Ownership Registry

Every schema, event family and projection declares exactly one writer. The initial ownership boundary is:

- `gateway` schema: `platform-gateway`;
- `audit_ledger` schema: `audit-ledger-service`;
- `legacy` schema: `legacy-hvac-backend`;
- Session security event family: `platform-gateway`;
- route-decision event family/projection: `platform-gateway`;
- append-only Session Audit projection: `audit-ledger-service`.

Relay and query identities receive only narrow non-writer access. PostgreSQL integration tests prove Gateway cannot write Audit Ledger tables and Relay cannot write Session state.

## Verification

```bash
npm run ownership:check
npm run contracts:check
npm run test:ownership
npm run test:legacy-boundary
npm run test:durable-postgres
npm run build:gateway
npm run build:legacy-private
npm run lint
npm run build
npm run audit:platform-gateway
npm run audit:auth-principal
npm run audit:durable-session
npm run audit:route-ownership
```

The durable browser audit begins with a real OIDC login, observes Legacy ownership through the generated client, attempts a forged cohort cookie, verifies direct browser access to Legacy fails, applies a higher revision selecting Go, rejects a registry revision rollback, and then continues the existing Outbox/Audit failure-recovery tests.
