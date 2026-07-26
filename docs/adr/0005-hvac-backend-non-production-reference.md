# ADR 0005 — `hvac-backend` is a non-production reference, not a Go platform dependency

Status: accepted

Date: 2026-07-26

## Context

The repository contains `hvac-backend`, a NestJS implementation of earlier HVAC functions. The Go platform was intentionally designed as a new platform boundary with independently owned identity, registry, telemetry, command, automation and AI services. Treating the NestJS implementation as a production migration source, fallback owner or architectural template adds complexity that is not justified when it has not become the production system of record.

The previously documented S1/S2 migration phases and the original S7 `Legacy Cohort Cutover` assumed a live Legacy production estate. That assumption is no longer valid.

## Decision

### Role of `hvac-backend`

`hvac-backend` is retained only as a non-production behavioral reference and optional local comparison fixture.

It is not:

- an architectural template for Go service boundaries, data models or deployment topology;
- a production route owner or request fallback;
- a source of platform identity, authorization or ownership truth;
- a migration source whose database or Redis state must be imported;
- a control owner, scheduler owner or command completion authority;
- a required dependency for production rollout, backup, restore or disaster recovery.

No new Go code may depend on NestJS modules, DTOs, tables, Redis keys, credentials or direct ThingsBoard call patterns.

### Active route and data ownership

Active public routes must be owned by Go services. `legacy-hvac-backend` may appear only in archived phase fixtures, differential tests or historical evidence and must never be selected by the active Route Ownership Registry.

Registry routes are Core-owned with no runtime Legacy fallback. Telemetry current-state routes are Telemetry Runtime-owned with no request fallback. Historical telemetry, if required, must be implemented as a Go-owned query boundary or explicitly declared unavailable; it cannot silently remain a NestJS dependency.

### Revised S7

S7 is renamed from `Legacy Cohort Cutover Slice` to `Production Cohort Rollout & Operational Hardening Slice`.

S7 now delivers:

- internal-to-production cohort promotion;
- capacity and failure-injection certification;
- multi-AZ and restore evidence;
- route and feature kill switches;
- deterministic rollback to the previous Go release or disabled capability;
- single-writer and command-fence verification;
- credential, route and dependency minimization;
- final production acceptance and operational ownership.

S7 does not require Legacy snapshot backfill, CDC, dual-read shadow comparison, Legacy route fallback, Legacy write revocation or a zero-Legacy-traffic observation window.

### Historical migration assets

Existing S1/S2 Legacy phase fixtures, migration services, shadow comparator code and retirement evidence are classified as historical/non-production assets until separately removed. They may be used for regression comparison but are not release gates for the current architecture.

Their presence must not cause the production Gateway, CI deployment configuration or runtime services to require `LEGACY_URL`, NestJS credentials or a Legacy database.

## Consequences

- S0–S2 architecture remains valid where it establishes Go ownership, identity, telemetry semantics, authorization, recovery and observability.
- Legacy-specific route phases in ADR 0001 and Legacy production-retirement assumptions in S2 operations documents are superseded by this ADR.
- The active Route Ownership Registry must contain no Legacy owner or Legacy fallback.
- S3 remains the next business slice after S2 production evidence because safe control is still missing.
- S4 and S5 may proceed after their declared S3/S2 prerequisites; S6 still requires S3 and S5.
- Production readiness is still a hard release gate. Removing Legacy migration does not remove capacity, security, rollback, restore, tenant isolation or command-safety requirements.

## Required verification

The repository must provide an executable check proving that:

1. the active Route Ownership Registry selects no `legacy-hvac-backend` owner or fallback;
2. active S1 Registry routes have no runtime read fallback;
3. the current production rollout plan contains no Legacy migration prerequisite;
4. S7 retains cohort, rollback, restore, security and command-fence gates;
5. `hvac-backend` is not required by the production deployment path.
