# 03 — Core Registry read model and PostgreSQL isolation

**What to build:** create the production-shaped `platform-core-service` and its owner-specific PostgreSQL Schema for Organization, Site, Equipment, Device, DeviceBinding and ExternalBinding. Implement explicit SQL list/detail reads with keyset pagination, transaction-local authorization Scope and RLS as a second enforcement layer. Core must validate the executing workload and delegated user Scope before returning any resource.

**Blocked by:** 01 — Contract, domain model and ownership baseline.

**Status:** ready-after-spec-approval

- [ ] `platform-core-service` is an independently buildable binary, image and Deployment with private-only service exposure.
- [ ] Core uses a separate PostgreSQL Schema, runtime account, migration account, pool and ownership declaration.
- [ ] Organization, Site, Equipment, Device, DeviceBinding and ExternalBinding DDL matches the accepted model and UUIDv7 rules.
- [ ] All tenant rows carry immutable `organization_id`; Site-scoped rows also carry immutable `site_id`.
- [ ] Runtime SQL always applies effective Organization/Site predicates before RLS.
- [ ] Every read transaction sets transaction-local authorized Scope and RLS independently rejects unauthorized rows.
- [ ] The runtime account cannot `BYPASSRLS`, own tenant tables, alter policies or access the IAM Schema.
- [ ] Owner-Organization access and cross-organization SiteBinding access work without exposing sibling Sites or owner-wide collections.
- [ ] List queries use indexed keyset pagination, deterministic ordering and immutable `id` tie-breakers; they do not run exact total counts by default.
- [ ] Cursor validation binds route, Scope, filters, ordering and query revision, and re-authorizes every page request.
- [ ] Missing and unauthorized detail resources produce the same internal not-found result for Gateway normalization.
- [ ] Core validates mTLS workload identity plus delegation audience, action, expiry, revocation and Scope before database access.
- [ ] Core emits safe Audit/trace context for allowed and denied reads without logging raw delegation material.
- [ ] PostgreSQL integration tests prove both application predicates and RLS stop cross-tenant access when the other layer is intentionally misconfigured in a test fixture.
- [ ] Query-plan tests verify tenant-leading indexes for the architecture-scale synthetic Registry dataset.
- [ ] Shutdown, readiness, database loss and recovery behavior follow S0 service conventions.
