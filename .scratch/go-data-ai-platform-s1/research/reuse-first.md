# S1 reuse-first assessment

Date: 2026-07-21

Scope: authorization modeling, PostgreSQL access/migrations, OpenAPI client generation, pagination and Legacy migration support for the S1 Organization–Site–Device read slice.

## Evaluation criteria

Candidates were evaluated by technical fit, active maintenance, documentation, license and security posture, integration cost, operational burden, replaceability and whether they would duplicate S0 capabilities.

Star count was treated only as a weak ecosystem signal, not as a selection criterion.

## Candidates

| Candidate | License | Strengths | Integration and maintenance cost | S1 decision |
|---|---|---|---|---|
| OpenFGA (`openfga/openfga`) | Apache-2.0 | Mature Zanzibar-inspired fine-grained authorization engine, Go SDK, PostgreSQL storage and model testing tools. Fits relationship-based Organization/Site access conceptually. | Adds a new authorization service, tuple/model lifecycle, storage, deployment, consistency and reconciliation between IAM facts and FGA tuples. S1 already has an accepted IAM ownership boundary. | Reference its model-testing practices only. Do not integrate in S1. |
| SpiceDB (`authzed/spicedb`) | Apache-2.0 | Mature relationship authorization database with expressive schema and strong permission lookup capabilities. | Adds a distributed authorization datastore and operational control plane. Synchronizing Membership/SiteBinding with Core data creates dual-write or projection-consistency work beyond the read slice. | Reference consistency and schema-testing ideas only. Do not integrate in S1. |
| Casbin (`apache/casbin`) | Apache-2.0 | Lightweight embedded Go authorization library supporting RBAC, ABAC and tenant/domain models. Easier to replace than an external service. | Policy persistence and role mappings would still duplicate IAM-owned facts. Explicit-deny, SiteBinding validity, current policy revision and revocation semantics require a project-specific adapter. | Reference matcher tests only. Keep project-owned typed authorization decisions. |
| PostgreSQL Row-Level Security | PostgreSQL license | Native second enforcement layer, transaction-local Scope, no extra service, aligned with accepted architecture. | RLS policies and session variables require careful tests and query-plan review, but no new runtime dependency. | Use as defense in depth together with mandatory application predicates. |
| `sqlc-dev/sqlc` | MIT | Generates type-safe Go from explicit SQL and works well with PostgreSQL and keyset queries. | Adds another generation pipeline and configuration surface; existing S0 code already uses pinned `pgx/v5` directly. Benefits must be proven against the small S1 query set. | Ticket 01 may run a bounded POC. Adopt only if generated diff, RLS context and query ownership stay simpler than direct `pgx`. |
| `pressly/goose` | MIT | Mature SQL/Go migration CLI with transactional migrations and broad database support. | Adds a migration framework beside the existing dedicated S0 migrator and release evidence path. Migration ownership and rollback evidence would need re-integration. | Do not add in S1. Extend the existing owner-specific migrator convention. |
| `oapi-codegen/oapi-codegen` | Apache-2.0 | Mature OpenAPI-to-Go generation for models, clients and server boilerplate. | Replacing the current locked custom generator would create large generated diffs and duplicate compatibility work during the first business slice. | Keep as a future generator-replacement candidate. Do not switch in S1 without a separate ADR. |
| `openapi-ts/openapi-typescript` | MIT | Mature TypeScript generation ecosystem, with typed fetch tooling and active releases. | Current S0 generator already produces TypeScript types, client calls and Zod runtime validation. Adding another tool would split contract authority. | Do not add in S1. Reuse the locked S0 generator and Zod validation. |

## Final choice

S1 will reuse the platform components already proven by S0:

- existing OIDC/BFF Session, Principal Context, Workload Identity and delegation libraries;
- IAM-owned typed authorization facts and decisions;
- `pgx/v5` plus explicit SQL and PostgreSQL RLS;
- the locked `scripts/generate-platform-contracts.mjs` OpenAPI pipeline;
- existing Problem Details, Route/Data Ownership, Audit, Outbox/Inbox, OpenTelemetry and supply-chain gates;
- owner-specific migration images and expand-contract release practice;
- native PostgreSQL keyset queries and opaque signed/versioned cursors implemented as project-specific contract behavior.

No new authorization service, policy store, ORM, migration framework, generic repository abstraction or pagination framework is introduced by the S1 specification.

## Conditional POC

Ticket 01 may compare `sqlc` with direct `pgx` for the exact S1 list/detail queries. The POC must remain disposable and may only be adopted when all of the following are true:

- generated code is deterministic and checked by the existing delivery gate;
- transaction-local RLS Scope remains explicit and testable;
- SQL remains owned by `platform-core-service` rather than a shared generic data layer;
- current and previous contract compatibility is unaffected;
- the added tool and maintenance surface are smaller than the handwritten query layer it replaces.

Failure to satisfy any condition means S1 continues with direct `pgx`.
