# S1 Registry routing cutover

S1 moves the eight public Registry read routes from the private Legacy HVAC backend to `platform-core-service`. The Gateway remains the only public ingress and IAM remains the authorization authority. Migration execution and quarantine resolution are offline operations; public requests never migrate or double-write business data.

## Frozen phase assets

Apply only the checked-in Route Ownership Registry assets, in order:

1. `contracts/ownership/s1-registry-phases/01-legacy-primary-go-shadow.json`
   - Legacy serves the client response.
   - Core receives a bounded, side-effect-free shadow request.
2. `contracts/ownership/s1-registry-phases/02-go-canary-legacy-shadow.json`
   - A deterministic Organization + Principal cohort serves from Core.
   - The opposite implementation receives the shadow request.
3. `contracts/ownership/s1-registry-phases/03-go-primary-legacy-read-fallback.json`
   - Core serves all traffic.
   - One Legacy read fallback is permitted only after a retryable Core transport, timeout, or 5xx failure.
4. `contracts/ownership/s1-registry-phases/04-go-primary.json`
   - Core is the only Registry owner.
   - No shadow or Legacy fallback remains.

The active `contracts/ownership/route-ownership.v1.json` is the phase 4 asset. Hot reload accepts only an adjacent phase with a higher registry and route revision. Route removal, phase skipping, policy regression, or an incomplete audit actor chain is rejected without changing the active snapshot.

## Authorization and request boundary

For every public Registry request, Gateway:

1. Validates the BFF Session and derives the acting Organization and immutable Principal identity.
2. Rejects malformed UUIDv7 path IDs, unknown or duplicate query parameters, invalid limits, and malformed cursors before backend execution.
3. Requests exactly one concrete IAM action such as `device.read`.
4. Forwards only the returned `X-Delegation-Grant` to Core. Browser cookies and identity headers are never forwarded.
5. Calls Core with verified workload TLS, a bounded timeout, no redirects, request/trace correlation, and the route policy revision.

IAM denial fails closed. Public detail denial and missing resources both return `404 RESOURCE_NOT_FOUND`. Authorization failure, invalid input, mapping invisibility, and resource-not-found results never trigger Legacy fallback.

## Runtime configuration

Gateway requires these values while Core can be selected:

- `CORE_URL`
- `CORE_SERVER_CA`
- `CORE_SERVER_NAME`
- `IAM_CLIENT_CERT`
- `IAM_CLIENT_KEY`
- `CORE_REGISTRY_TIMEOUT`
- `REGISTRY_SHADOW_TIMEOUT`
- `REGISTRY_MAX_RESPONSE_BYTES`
- `REGISTRY_MAX_SHADOW_CONCURRENT`

Keep Legacy configuration available through the observation and rollback window even after phase 4:

- `LEGACY_URL`
- `LEGACY_SERVER_CA`
- `LEGACY_SERVER_NAME`
- `LEGACY_AUDIENCE`
- `LEGACY_TIMEOUT`

Core and Legacy HTTP clients reject redirects. Production must not use `S1_ALLOW_NO_CORE` or `S0_ALLOW_NO_LEGACY` while the corresponding owner can be selected.

## Shadow comparison evidence

Shadow requests are detached from client cancellation only inside the short shadow timeout. They cannot alter the client response. Gateway records only:

- route template and policy revisions
- primary and shadow owners
- status codes and stable problem codes
- SHA-256 of each response body
- canonical semantic equality
- approved Organization, Principal, workload and trace evidence

Response bodies, cookies, grants, authorization codes and credentials are never persisted in route audit records.

## Fallback matrix

Fallback is allowed only in phase 3 and only when Core returns a retryable transport error, timeout or normalized 5xx result. It is forbidden after:

- IAM denial or IAM unavailability
- malformed path/query/cursor input
- `RESOURCE_NOT_FOUND`
- `MAPPING_INVALID` or `MAPPING_QUARANTINED`
- any successful Core response

At most one Legacy request is attempted. Its result is normalized through the same frozen DTO boundary and audited as `ROUTE_FALLBACK_EXECUTED`.

## Verification and rollback

Before advancing a phase, require:

- `npm run ownership:check`
- `npm run s1:registry:check`
- `npm run test:registry-routing`
- `npm run build:energy-api`
- Ticket 05 PostgreSQL routing evidence
- zero unexplained shadow mismatches for the approved observation window

Rollback copies only the immediately previous phase policy into a new registry revision and increments every changed route revision; historical phase assets are never re-applied with regressed revisions. A rollback never bypasses IAM, changes public IDs, exposes quarantine data, or enables Registry writes.

## Zero invariants

- Zero request-time migration or synchronization.
- Zero business double writes.
- Zero fallback after authorization denial or resource invisibility.
- Zero raw resource IDs in route labels or log message templates.
- Zero response bodies or credentials in comparison evidence.
- Zero public Registry ownership outside Core after `GO_PRIMARY`.
