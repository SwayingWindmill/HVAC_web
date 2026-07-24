# S2 Gateway Snapshot and bounded batch

## Reuse-first decision

Ticket 05 extends the repository's digest-locked `scripts/generate-s2-telemetry-contracts.mjs` pipeline rather than introducing a second OpenAPI toolchain. `oapi-codegen/oapi-codegen` and `ogen-go/ogen` were evaluated as mature Apache-2.0 alternatives, but adopting either here would add generator binaries, templates, upgrade policy, lock-file changes, and duplicate model ownership for two routes already represented by the accepted S2 contract. The existing generator now emits the Telemetry Runtime models, a Gateway server seam, and the HVAC Web TypeScript client from the same contract digest.

The runtime request path uses Go's standard `net/http` client. No new transport, retry, router, or browser data library is introduced.

## Public request boundary

The public routes are:

- `GET /api/v1/devices/{deviceId}/observation-snapshot`
- `POST /api/v1/telemetry/observation-snapshots:batchGet`

The GET route accepts an omitted or empty key selection for Presence-only evaluation, or one exact ordered comma-separated key selection. The POST route accepts 1–100 ordered request items. The Gateway enforces 64 keys per Device, 100 Devices, 2,048 total key selections, unique request IDs, unique keys, strict JSON, and a bounded request body before IAM or Telemetry Runtime is called.

Browser callers authenticate only with the HttpOnly BFF Session. Session POST requests require an exact public Origin and the encrypted Session's CSRF token. Caller-supplied principal, role, Organization, Site, admin, scope, or delegation headers are rejected at the edge. Cookies and CSRF tokens are never forwarded to IAM or Telemetry Runtime.

Service callers use direct TLS 1.3 client authentication. Gateway accepts the acting `X-Organization-ID` only when the request carries a successfully verified certificate with exactly one SPIFFE URI and no BFF Session cookie. A presented but unverified certificate, a missing/invalid Organization, or any caller-supplied role, Site, admin, principal, scope, or delegation header fails before IAM. The optional incoming mTLS listener is enabled only when `GATEWAY_SERVER_CERT`, `GATEWAY_SERVER_KEY`, and `GATEWAY_CLIENT_CA` are configured together; browser Sessions can still connect without a client certificate.

## Authorization and mTLS chain

For each request, Platform Gateway derives the initiating identity and acting Organization from either the durable Session or the verified workload SPIFFE identity, signs a non-user-editable `telemetry:authorize` delegation, and asks IAM for the exact Device/key scope. Gateway validates IAM's principal, subject, issuer, action, scope digest, authorized targets, policy revision, correlation fields, grant lifetime, audience, presenter, and actor chain before forwarding the grant.

Gateway calls Telemetry Runtime over TLS 1.3 with the authenticated Gateway workload certificate and the short-lived IAM grant. Runtime remains responsible for cryptographic grant verification, single-use/revocation enforcement, and authoritative Snapshot evaluation. Browser or service caller authority is not converted into trusted downstream HTTP headers.

## Error and nondiscovery semantics

The public contract uses stable Problem Details codes:

- `RESOURCE_NOT_FOUND` for both missing and unauthorized Devices.
- `TELEMETRY_KEY_INVALID` for invalid or unauthorized telemetry keys.
- `TELEMETRY_REQUEST_INVALID` for malformed selections and JSON.
- `TELEMETRY_BATCH_LIMIT_EXCEEDED` for request bounds.
- `TELEMETRY_AUTHORIZATION_UNAVAILABLE` for IAM or grant-decision failures.
- `TELEMETRY_UNAVAILABLE` for owner or Telemetry Runtime failures.
- `TELEMETRY_TIMEOUT` for bounded owner timeouts.

Missing and unauthorized Device paths share status, code, title, detail, retryability, and schema. Device IDs and key selections are represented in logs and metrics only by the registered route template; request scope, telemetry values, Session cookies, CSRF tokens, and grants are not logged.

Stale, missing, rejected-only, policy-not-configured, and upstream-unavailable conditions remain typed Snapshot data states. Gateway validates the complete Snapshot union, enum, timestamp, quality, availability, revision, and ordering semantics rather than translating those states into transport errors.

## Rollout boundary

Both routes remain owned by `telemetry-runtime-service` with `platform-gateway` as public ingress. The applied ownership registry remains `expand-baseline`, `R0-contract-only`, and `rollout.mode=disabled`, so default production traffic is **0%**. The route manager can later select deterministic cohorts by registry revision and the reviewed R3–R7 phases, but this ticket does not change the applied percentage.

There is no request-level fallback, read fallback owner, Legacy owner, Mock owner, ThingsBoard read-through, or shadow side effect. Owner, IAM, and Runtime failures fail closed.

The browser acceptance audit does not alter the applied registry. It starts the production Gateway binary with an explicit test-only un-routed fixture flag that is accepted only when both Session and route audit stores are memory-only and the Telemetry Runtime URL is loopback HTTPS. This fixture mode is absent by default, cannot target a remote Runtime, and exists only to execute the R0 code seam while the production registry remains at 0%.

## Out of scope

- Centrifugo bootstrap, recovery, or live subscriptions.
- HVAC Web page migration to the generated client.
- Legacy current-state route retirement or reverse synchronization.
- Historical telemetry warehouse APIs.
- Enabling any production S2 cohort above 0%.
