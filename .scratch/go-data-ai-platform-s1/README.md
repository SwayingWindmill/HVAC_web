# S1 implementation tracker

Formal spec: `spec.md`

Reuse-first assessment: `research/reuse-first.md`

## Approval gate

The S1 specification was explicitly accepted on 2026-07-21. Ticket 01 is the only current implementation frontier.

## Dependency graph

```text
01 Contract, domain model and ownership baseline
├─ 02 IAM registry-read authorization projection
├─ 03 Core registry read model and PostgreSQL isolation
└─ 04 Legacy mapping, backfill and quarantine

02 + 03 + 04
       ↓
05 Gateway registry API and generated clients
       ↓
06 HVAC Web real Organization/Site/Device pages

02 + 03 + 04 + 05 + 06
       ↓
07 Tenant, migration, failure and rollback gates
       ↓
08 S1 Release Evidence Bundle
```

## Current frontier

- Ticket 01 — Contract, domain model and ownership baseline.
- Tickets 02–08 remain blocked by the dependency graph.
- Complete one frontier ticket at a time; do not bypass declared blockers.

## Rules

- The highest test seam is the public Gateway contract or a real browser journey.
- IAM remains the unique owner of Principal, OrganizationMembership, RoleBinding, SiteBinding and Policy.
- Logto is the selected external identity provider for login, credentials, MFA/passkeys, enterprise federation and external user lifecycle. Logto Organization or role claims are not HVAC data-authorization truth.
- `platform-core-service` remains the unique owner of Organization, Site, Equipment, Device, DeviceBinding, ExternalBinding and S1 migration mappings.
- Client headers, active UI selections and token business claims are never authorization truth.
- New public IDs are immutable platform UUIDv7 values; Legacy and ThingsBoard IDs stay behind controlled mappings.
- Legacy is Shadow or explicit read-only fallback only. S1 must not introduce business double writes.
- A read request must never trigger a ThingsBoard full synchronization.
- S1 does not implement Telemetry, Realtime Delta, Command, Schedule, AI, Registry writes or Legacy deletion.
- Do not mark S1 complete until Ticket 08 publishes and verifies the S1 Release Evidence Bundle.
