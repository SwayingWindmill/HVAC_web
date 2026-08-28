# ADR 0001 — S1 Registry contract, domain and ownership baseline

Status: accepted

Date: 2026-07-21

Issue: #32

Superseded in part by ADR 0005 and ADR 0013. UUIDv7, cursor/RLS and single-owner Registry principles remain accepted. Organization/Equipment public terminology and Legacy fallback assumptions are historical; the canonical model is Tenant/Site/Space/Asset/Device/Point.

## Context

S1 needs a stable Organization–Site–Equipment–Device read contract before IAM authorization, Core runtime, Legacy migration, Gateway routing or HVAC Web work can begin. The baseline must preserve S0's single-writer ownership, resource invisibility, reproducible generation and rollback rules while preventing Legacy or ThingsBoard identifiers from becoming public platform identity.

## Decision

### Public contract

The Gateway OpenAPI adds exactly eight read operations:

- `GET /api/v1/organizations`
- `GET /api/v1/organizations/{organizationId}`
- `GET /api/v1/organizations/{organizationId}/sites`
- `GET /api/v1/sites/{siteId}`
- `GET /api/v1/sites/{siteId}/equipment`
- `GET /api/v1/equipment/{equipmentId}`
- `GET /api/v1/sites/{siteId}/devices`
- `GET /api/v1/devices/{deviceId}`

Collections are direct `{items,nextCursor,hasMore}` DTOs with no global success envelope and no default exact count. Missing and unauthorized detail resources share `404 RESOURCE_NOT_FOUND`.

### Identity and model

- All public business IDs are immutable owner-generated UUIDv7.
- `Site` has one owning Organization and a validated IANA timezone.
- `Equipment` is a maintainable business asset.
- `Device` is an IoT endpoint.
- Equipment and Device are independent identities connected only by versioned `DeviceBinding` records.
- Active `ExternalBinding` uniqueness is `(integration_instance_id, external_entity_type, external_id)`.
- Raw Legacy and ThingsBoard IDs remain internal mapping data.

### Cursor

Cursor revision 1 is:

```text
base64url(canonical-json-payload).base64url(hmac-sha256(payload))
```

Required claims are `v`, `route`, `scopeHash`, `filterHash`, `order`, `last` and `queryRevision`. The cursor is integrity protected but not confidential. Every page request is re-authorized; route, Scope, filters and query revision must still match current truth. Default ordering is `displayName COLLATE "C", id`.

### Problem Details

The initial Registry codes are:

| Code | Status | Retryable |
|---|---:|---:|
| `RESOURCE_NOT_FOUND` | 404 | no |
| `CURSOR_INVALID` | 400 | no |
| `REGISTRY_UNAVAILABLE` | 503 | yes |
| `REGISTRY_TIMEOUT` | 504 | yes |
| `MAPPING_INVALID` | 409 | no |
| `MAPPING_QUARANTINED` | 409 | no |

### PostgreSQL ownership and RLS

- `iam-service` uniquely writes Schema `iam`.
- `platform-core-service` uniquely writes Schema `core_registry`.
- Migration and runtime roles are distinct, `NOLOGIN`, `NOBYPASSRLS` group identities.
- Runtime roles do not own tables or policies.
- Core tables use mandatory application predicates plus forced PostgreSQL RLS.
- Organization access and explicitly delegated Site access are represented separately.
- Tenant-leading keyset indexes support Organization/Site filtering before display-name ordering.
- `MigrationQuarantine` is inaccessible to ordinary Core runtime reads.

### Legacy migration states

Mappings use `DISCOVERED`, `MAPPED`, `VERIFIED`, `QUARANTINED` and `RETIRED`. Ambiguous Asset-to-Equipment conversion cannot have a target resource ID and remains in Quarantine. Reads cannot trigger synchronization or business double writes.

### Route ownership

All eight S1 routes initially remain `legacy-hvac-backend` primary with `platform-core-service` as the candidate owner. The permitted sequence is:

1. `LEGACY_PRIMARY_GO_SHADOW`
2. `GO_CANARY_LEGACY_SHADOW`
3. `GO_PRIMARY_LEGACY_READ_FALLBACK`
4. `GO_PRIMARY`

Shadow has `side_effect_policy=NONE`. Legacy fallback is read-only and forbidden after `AUTHORIZATION_DENIED` or `RESOURCE_NOT_FOUND`.

### Query implementation

A disposable `sqlc v1.31.1` POC generates deterministic `pgx/v5` code and proves explicit Scope predicates can be retained. S1 nevertheless keeps direct `pgx` for the initial runtime because transaction-local RLS setup and the small query set do not justify a second production generator yet.

## Consequences

Ticket 02 may implement IAM facts against the frozen IAM Schema. Ticket 03 may implement the Core service against the frozen Core Schema and generated contract. Ticket 04 may implement migration execution without redefining mapping states. Ticket 05 may implement `RegistryServerInterface`; Ticket 01 deliberately does not make the current Gateway handler implement it.

Changing public IDs, Equipment/Device identity, cursor claims, ownership writers, fallback conditions or mapping states requires a new ADR and compatible revision rather than an in-place edit.

## Architecture Decision Trace

| Ticket 01 criterion | Architecture/spec source | Baseline artifact | Verification |
|---|---|---|---|
| Eight read endpoints and direct collections | S1 Public HTTP contract | `contracts/http/platform-gateway.openapi.yaml` | `contracts:check`, `s1:registry:check` |
| UUIDv7 and revisions | S1 Domain model | OpenAPI + `s1-registry-model.v1.json` + DDL checks | static baseline and PostgreSQL constraints |
| Site IANA timezone | S1 Domain model | `sites_iana_timezone` trigger | invalid-timezone PostgreSQL negative test |
| Equipment/Device separation | S1 Domain model | separate tables/DTOs plus `device_bindings` | model and DDL checks |
| ExternalBinding active uniqueness | S1 Domain model | partial unique index | duplicate-insert PostgreSQL negative test |
| Stable Problem Details | S1 Public HTTP contract | OpenAPI `x-stable-codes` and model lock | generator/static checks |
| Opaque Cursor binding | S1 Public HTTP contract | model cursor claims and generated clients | HMAC tamper and cross-Scope tests |
| IAM/Core Schema ownership | S1 PostgreSQL ownership | Data Ownership Registry and bootstrap roles | ownership checker and schema-owner SQL |
| Route ownership and fallback | S1 Legacy migration | Route Ownership Registry revision 2 | ownership checker |
| DDL, RLS and indexes | S1 PostgreSQL ownership and RLS | `infra/registry/postgres/init` | real PostgreSQL integration test |
| Two-Organization fixtures | S1 Testing Decisions | deterministic fixture SQL | owner/delegated/no-access RLS counts |
| Legacy mapping states | S1 Legacy mapping | model + DDL + fixtures | static check and Quarantine SQL assertion |
| sqlc versus pgx POC | S1 reuse assessment | `pocs/s1-sqlc` | deterministic generation and compile test |
| Reproducible contract generation | S0 delivery convention | generator v5 and checked-in outputs | `contracts:check` |
| Decision trace | S1 Ticket 01 | this ADR | Markdown review and CI evidence |
