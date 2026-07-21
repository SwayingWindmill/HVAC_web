# 01 — Contract, domain model and ownership baseline

**What to build:** freeze the first S1 public contract, Core Registry domain model, PostgreSQL ownership plan, two-Organization fixtures and migration states before service implementation begins. Extend the existing OpenAPI and ownership registries with Organization, Site, Equipment, Device, DeviceBinding, ExternalBinding, opaque Cursor and stable Registry Problem Details. Produce reviewed DDL and query plans, but keep the implementation narrow and read-only.

**Blocked by:** Explicit acceptance of `../spec.md`.

**Status:** ready-after-spec-approval

- [ ] The public OpenAPI defines all eight S1 list/detail endpoints and typed collection DTOs without a global success envelope.
- [ ] Organization, Site, Equipment, Device, DeviceBinding and ExternalBinding schemas use platform UUIDv7 identifiers and explicit revisions.
- [ ] Site requires an IANA timezone and exactly one owning Organization.
- [ ] Equipment and Device remain separate identities connected by versioned DeviceBinding records.
- [ ] ExternalBinding active uniqueness is defined for IntegrationInstance, external entity type and external ID.
- [ ] Stable Problem Details codes include resource invisibility, invalid cursor, unavailable Registry and invalid mapping states without exposing internals.
- [ ] Cursor format, version, signing/integrity behavior, Scope binding, filter binding, ordering and tie-breaker rules are documented and testable.
- [ ] IAM and Core Schema, runtime-account and migration-account ownership are declared in Data Ownership Registry.
- [ ] Route Ownership Registry declares each S1 path, allowed Scope dimensions, Go/Legacy phases, fallback constraints and initial revision.
- [ ] Reviewed PostgreSQL DDL includes tenant columns, RLS policies, supporting indexes, uniqueness constraints and expand-contract rollback compatibility.
- [ ] Two owning Organizations, multiple Sites, one cross-organization SiteBinding, explicit deny and no-access fixtures are defined with deterministic UUIDs.
- [ ] Legacy mapping states and provenance fields are fixed; ambiguous Asset/Device conversion cannot be represented as verified.
- [ ] A bounded `sqlc` versus direct `pgx` POC is recorded; no new tool is adopted unless it is demonstrably simpler and deterministic.
- [ ] Contract generation remains reproducible and produces no handwritten duplicate Go/TypeScript DTOs.
- [ ] Architecture Decision Trace maps every Ticket 01 choice to the accepted architecture and S1 specification.
