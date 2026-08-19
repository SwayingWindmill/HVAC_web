# ThingsBoard source-first implementation review

Reference baseline: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Purpose: record the exact upstream source/tests re-read immediately before local implementation or refactoring. The domain adjudication documents remain the architectural decision record; this file records implementation-time source evidence required by `AGENTS.md`.

## S00 — canonical vocabulary and contract cleanup

Date: 2026-08-18

Local issue: #247

### Upstream files reviewed

- `application/src/main/java/org/thingsboard/server/controller/AssetController.java`
- `application/src/main/java/org/thingsboard/server/controller/DeviceController.java`
- `application/src/test/java/org/thingsboard/server/controller/AssetControllerTest.java`
- `application/src/test/java/org/thingsboard/server/controller/DeviceControllerTest.java`
- `application/src/test/java/org/thingsboard/server/controller/TenantControllerTest.java`

All files were read from the pinned `v4.3.1.1` tag, not from `master`.

### Observed upstream semantics

- Asset and Device are distinct first-class identities and have separate controllers/lifecycle APIs.
- Asset and Device ownership is tenant-scoped. Upstream tests explicitly assert the saved entity Tenant ID and reject cross-tenant updates/references.
- Tenant is a first-class ownership entity. The upstream API uses Tenant as the ownership boundary rather than an Organization alias.
- ThingsBoard additionally supports Customer assignment/public sharing. That behavior is not adopted for HVAC authorization because D01/D02 already rejected Customer/Public Customer as the Site/Tenant authorization model.
- Upstream exposes mutable Asset/Device Profile configuration. D02/ADR 0013 already replaces that cross-domain profile ownership with immutable Registry Template Revision plus domain-owned Release References.

### Implementation decision

- `ADOPT`: Tenant terminology as the only ownership vocabulary; separate Asset and Device identities.
- `ADAPT`: HVAC keeps the stronger typed `Tenant -> Site -> Space -> Asset -> Device -> Point` hierarchy and Site-scoped authorization/RLS rather than the generic ThingsBoard relation/customer model.
- `REJECT`: Organization aliases, Customer/Public Customer authorization, free-form relation topology, and mutable cross-domain God Profile.

### S00 consequence

The local implementation must remove active Organization/Area/Equipment machine vocabulary rather than add aliases. Public capabilities become `asset.list` / `asset.read`; Registry contracts use `Space`, `Asset`, `assetId`, and `/assets`; stale tests/fixtures must be rewritten to the current Tenant/Asset production types. Historical audit/ADR text may retain old terms only when explicitly marked historical or superseded.
