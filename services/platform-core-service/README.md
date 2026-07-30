# platform-core-service

`platform-core-service` is the private S1 Registry read boundary. It reads the platform-owned `core_registry` PostgreSQL Schema and returns frozen Organization, Site, Equipment and Device representations to `platform-gateway`. Public route ownership remains with the Legacy service until Ticket 05; this service is not browser-facing and does not perform migration or business double writes.

## Internal routes

All routes are `GET` under `/internal/v1/registry`:

- `/organizations`
- `/organizations/{organizationId}`
- `/organizations/{organizationId}/sites`
- `/sites/{siteId}`
- `/sites/{siteId}/equipment`
- `/equipment/{equipmentId}`
- `/sites/{siteId}/devices`
- `/devices/{deviceId}`

The caller must present a verified client certificate whose single SPIFFE URI exactly matches `CORE_ALLOWED_WORKLOAD_SPIFFE`, normally `spiffe://hvac.local/platform-gateway`. Each request must also carry `X-Delegation-Grant`, signed by IAM for one concrete Registry action and audience `platform-core-service`.

Before a database query, Core calls IAM's mTLS-only `/internal/v1/registry-read/grant-status` endpoint using its own workload identity. IAM returns only the current Registry policy revision and whether the grant `jti` is revoked. Core then rejects invalid signature, wrong issuer/presenter/audience/action, expired, excessive-lifetime, transitive, revoked, stale-policy and contradictory-scope grants.

## PostgreSQL boundary

`CORE_DATABASE_URL` must authenticate exactly as `s1_core_service`. That login can only activate the `s1_core_runtime` group role. Every Registry query runs in one repeatable-read, read-only transaction:

1. `SET LOCAL ROLE s1_core_runtime`.
2. Set transaction-local `app.authorized_organization_ids` and `app.authorized_site_ids` from the verified grant.
3. Query a forced-RLS Core table.
4. Commit the read snapshot.

Organization and explicit Site scope are independent. A cross-Organization Site grant exposes only that Site and its Equipment/Devices; it does not expose the owning Organization or sibling Sites. Every query also applies the signed denied-Organization and denied-Site arrays as mandatory predicates, so an Organization-level allow cannot recover an explicitly denied Site and a denied Organization suppresses any Site-level allow beneath it. Missing and unauthorized detail records both return `RESOURCE_NOT_FOUND`. The runtime cannot read IAM facts, migration provenance or migration quarantine.

Collections use bounded `(display_name COLLATE "C", id)` keyset pagination. Cursors are HMAC-SHA256 protected and bound to route resource, parent, concrete action, policy revision and effective Organization/Site scope. Authorization is rechecked on every page.

## Environment

Required:

- `CORE_DATABASE_URL`
- `CORE_TLS_CERT`
- `CORE_TLS_KEY`
- `CORE_CLIENT_CA`
- `CORE_IAM_CA`
- `CORE_IAM_GRANT_CERT`
- `CORE_IAM_ENDPOINT`
- `CORE_CURSOR_HMAC_KEY` — raw URL-safe base64 for at least 32 bytes

Optional:

- `CORE_SERVICE_ADDR` (default `127.0.0.1:18445`)
- `CORE_DIAGNOSTICS_ADDR` (default `127.0.0.1:19084`)
- `CORE_GRANT_ISSUER` (default IAM SPIFFE ID)
- `CORE_ALLOWED_WORKLOAD_SPIFFE` (default Gateway SPIFFE ID)
- `CORE_AUDIENCE` (default `platform-core-service`)
- `OTEL_EXPORTER_OTLP_ENDPOINT`

Logs and telemetry contain route templates, status, duration and trace correlation only. They do not contain raw grants, cookies, credentials or mutable user profile fields.

## Verification

```bash
npm run test:identity
npm run build:core
npm run s1:registry:postgres
npm run s1:registry-core
```
