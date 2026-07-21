# 05 — Gateway Registry API and generated clients

**What to build:** expose the accepted S1 Organization/Site/Equipment/Device list and detail contracts through `platform-gateway`. Reuse the S0 BFF Session, current Principal resolution, Workload Identity, delegation, Problem Details, trace and ownership registries. Route each request to one approved owner, normalize Legacy only inside the anti-corruption boundary and regenerate Go/TypeScript artifacts.

**Blocked by:** 02 — IAM Registry-read authorization projection; 03 — Core Registry read model and PostgreSQL isolation; 04 — Legacy mapping, backfill and quarantine.

**Status:** ready-after-spec-approval

- [ ] Gateway implements all eight accepted Registry routes under `/api/v1` from the checked-in OpenAPI.
- [ ] Browser requests require the existing authenticated BFF Session and current non-revoked Principal.
- [ ] Gateway obtains a current Registry-read decision/delegation from the trusted IAM seam and never trusts active UI selections as Scope.
- [ ] Internal Core calls use valid mTLS Workload Identity and the `platform-core-service` audience.
- [ ] Each normal request calls only one domain owner after identity resolution; no unbounded Gateway fan-out is introduced.
- [ ] Route Ownership revisions implement the four approved Legacy/Go read phases with deterministic cohort selection.
- [ ] Legacy fallback is read-only, explicitly configured and never occurs after an authorization denial or resource-not-found result.
- [ ] Unknown, conflicting or regressed Route Ownership revisions fail closed.
- [ ] Organization, Site, Equipment and Device responses expose only platform UUIDv7 and approved canonical fields.
- [ ] Missing and unauthorized resources return identical `404 RESOURCE_NOT_FOUND` Problem Details without existence clues.
- [ ] Cursor parsing verifies integrity, version, route, Scope, filters, ordering and query revision; every page is re-authorized.
- [ ] Invalid limits, cursor tampering, unsupported revision and malformed UUIDs return stable safe Problem Details.
- [ ] Generated Go and TypeScript outputs are reproducible and CI fails on drift.
- [ ] Current and previous compatible generated clients interoperate through the rollback window.
- [ ] Gateway logs, traces and Audit evidence record route/policy/data owner revisions without credentials or raw Legacy payloads.
- [ ] Black-box tests cover success, pagination, wrong method, malformed input, forged headers, cross-tenant access, Legacy timeout, Core timeout and route rollback.
