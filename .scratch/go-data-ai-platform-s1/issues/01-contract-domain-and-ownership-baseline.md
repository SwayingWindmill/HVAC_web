# 01 — Contract, domain model and ownership baseline

**What to build:** freeze the first S1 public contract, Core Registry domain model, PostgreSQL ownership plan, two-Organization fixtures and migration states before service implementation begins. Extend the existing OpenAPI and ownership registries with Organization, Site, Equipment, Device, DeviceBinding, ExternalBinding, opaque Cursor and stable Registry Problem Details. Produce reviewed DDL and query plans, but keep the implementation narrow and read-only.

**Blocked by:** None. `../spec.md` was explicitly accepted on 2026-07-21.

**Status:** completed

**GitHub Issue:** #32

- [x] The public OpenAPI defines all eight S1 list/detail endpoints and typed collection DTOs without a global success envelope.
- [x] Organization, Site, Equipment, Device, DeviceBinding and ExternalBinding schemas use platform UUIDv7 identifiers and explicit revisions.
- [x] Site requires an IANA timezone and exactly one owning Organization.
- [x] Equipment and Device remain separate identities connected by versioned DeviceBinding records.
- [x] ExternalBinding active uniqueness is defined for IntegrationInstance, external entity type and external ID.
- [x] Stable Problem Details codes include resource invisibility, invalid cursor, unavailable Registry and invalid mapping states without exposing internals.
- [x] Cursor format, version, signing/integrity behavior, Scope binding, filter binding, ordering and tie-breaker rules are documented and testable.
- [x] IAM and Core Schema, runtime-account and migration-account ownership are declared in Data Ownership Registry.
- [x] Route Ownership Registry declares each S1 path, allowed Scope dimensions, Go/Legacy phases, fallback constraints and initial revision.
- [x] Reviewed PostgreSQL DDL includes tenant columns, RLS policies, supporting indexes, uniqueness constraints and expand-contract rollback compatibility.
- [x] Two owning Organizations, multiple Sites, one cross-organization SiteBinding, explicit deny and no-access fixtures are defined with deterministic UUIDs.
- [x] Legacy mapping states and provenance fields are fixed; ambiguous Asset/Device conversion cannot be represented as verified.
- [x] A bounded `sqlc` versus direct `pgx` POC is recorded; no new tool is adopted unless it is demonstrably simpler and deterministic.
- [x] Contract generation remains reproducible and produces no handwritten duplicate Go/TypeScript DTOs.
- [x] Architecture Decision Trace maps every Ticket 01 choice to the accepted architecture and S1 specification.

## Completion evidence

- Public contract: `contracts/http/platform-gateway.openapi.yaml`, generator v5 and checked-in Go/TypeScript clients.
- Domain and migration lock: `contracts/registry/s1-registry-model.v1.json`.
- Ownership: Route/Data Ownership Registry revision 2 with Legacy-primary, Core-candidate migration phases and fail-closed fallback constraints.
- PostgreSQL: `infra/s1-registry/postgres/init/` with separate IAM/Core owners, mandatory RLS, deterministic two-Organization fixtures and migration Quarantine.
- Reuse decision: reproducible `sqlc v1.31.1` POC retained as evidence; direct `pgx` remains the initial implementation choice.
- Architecture Decision Trace: `docs/adr/0001-s1-registry-contract-domain-ownership.md`.
- Local gates: `npm run s1:ticket-01`, `npm run lint`, `npm run build`, `npm run test:ownership`, `npm run security:licenses` and `npm run security:dependency-audit` passed on 2026-07-21.
