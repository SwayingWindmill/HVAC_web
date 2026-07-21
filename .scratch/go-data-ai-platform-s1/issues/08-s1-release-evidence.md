# 08 — S1 integration and Release Evidence Bundle

**What to build:** integrate the complete S1 Organization–Site–Device read slice from browser authentication through IAM authorization, Gateway, Core PostgreSQL, Legacy migration evidence and HVAC Web. Execute the full clean-environment release gates, publish immutable signed images and produce a reproducible S1 Release Evidence Bundle that proves S2 can rely on stable Organization/Site/Device identities without reopening S1 boundaries.

**Blocked by:** 01 — Contract, domain model and ownership baseline; 02 — IAM Registry-read authorization projection; 03 — Core Registry read model and PostgreSQL isolation; 04 — Legacy mapping, backfill and quarantine; 05 — Gateway Registry API and generated clients; 06 — HVAC Web real Organization/Site/Device pages; 07 — Tenant, migration, failure and rollback gates.

**Status:** ready-after-spec-approval

- [ ] A clean environment can authenticate, list authorized Organizations, open a Site, page Equipment/Devices and view details through Gateway in the HVAC Web.
- [ ] The browser target views use real generated API calls and no target Registry Mock data in real mode.
- [ ] Cross-organization SiteBinding exposes only the delegated Site and no sibling Site or owning-Organization collection.
- [ ] Missing and unauthorized resources remain indistinguishable across public API, browser, cursor and error evidence.
- [ ] Current and previous compatible Gateway/IAM/Core/client versions interoperate through the rollback window.
- [ ] The approved internal Legacy cohort is backfilled with deterministic mappings and all ambiguous records remain visible in restricted Quarantine evidence only.
- [ ] Shadow Compare and route canary results are recorded; Route rollback is executed without Legacy writes or data-owner reversal.
- [ ] Capacity evidence records the declared dataset, staging profile, throughput, latency percentiles, RLS/query-plan cost and headroom.
- [ ] Backup/restore evidence reconstructs IAM/Core Registry data, mappings, provenance and Quarantine and reruns tenant checks.
- [ ] The Bundle includes source and image versions, environment, fixtures, OpenAPI/generated clients, DDL/migrations, tests, traces, dashboards, Audit, Shadow/Quarantine, capacity, restore, rollout and rollback results.
- [ ] The Bundle records the pinned Logto version/configuration class, issuer/subject mapping tests, external identity lifecycle evidence and proof that Logto Organization/role claims are not direct business authorization.
- [ ] Every release image includes SBOM, vulnerability/secret scans, BuildKit provenance, Cosign verification and GitHub build attestation.
- [ ] Architecture Decision Trace maps every S1 acceptance criterion to source architecture, specification, implementation, tests, CI run and evidence file.
- [ ] Known limitations are explicit and do not defer tenant isolation, resource invisibility, mapping correctness, rollback or credential safety.
- [ ] Evidence reports zero cross-tenant/sibling-Site success, zero existence disclosure, zero forged-header authorization, zero ambiguous promoted mapping, zero read-triggered ThingsBoard sync, zero business double write and zero credential leakage.
- [ ] NestJS remains private Legacy Frozen; S1 completion does not authorize deletion or new Legacy business behavior.
- [ ] No Telemetry, Realtime Delta, Command, Schedule, AI or Registry write scope has leaked into S1.
- [ ] Final review explicitly declares S1 complete and S2 ready to enter implementation specification only.
