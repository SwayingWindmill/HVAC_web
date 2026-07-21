# S1 reuse-first assessment

Date: 2026-07-21

Scope: external identity, authorization modeling, PostgreSQL access/migrations, OpenAPI client generation, pagination and Legacy migration support for the S1 Organization–Site–Device read slice.

## Evaluation criteria

Candidates were evaluated by technical fit, active maintenance, documentation, license and security posture, integration cost, operational burden, replaceability and whether they would duplicate S0 capabilities.

Star count was treated only as a weak ecosystem signal, not as a selection criterion.

## Candidates

### External identity providers

| Candidate | License | Strengths | Integration and maintenance cost | S1 decision |
|---|---|---|---|---|
| Logto (`logto-io/logto`) | MPL-2.0 | OIDC/OAuth, B2B Organizations, MFA/passkeys, enterprise SSO, Management API and an existing `logto_subject` migration seam in this repository. | Requires a clear mapping between Logto identity Organizations and platform business Organizations; its claims cannot express SiteBinding or resource ownership. | **Selected** for external authentication and user lifecycle. Keep platform IAM as business authorization authority. |
| ZITADEL (`zitadel/zitadel`) | AGPL-3.0 with component exceptions | Strong B2B Organization, project grant, API-first and Go-based operational model. | Stricter license review, a second Organization/Project model and higher migration/operational cost. | Retain as the primary fallback if Logto fails the POC or future delegation requirements exceed it. |
| Keycloak (`keycloak/keycloak`) | Apache-2.0 | Mature OIDC/SAML, LDAP/AD federation, enterprise adoption and extensive operational knowledge. | Heavier Java/Quarkus runtime and a complex Realm/Organization/Role configuration model. | Use only when LDAP/SAML enterprise federation becomes the dominant requirement. |
| Ory Kratos + Hydra | Apache-2.0 core projects | Headless, modular, Go-based identity and OAuth components. | Multiple services, databases and custom UI; B2B organization capabilities increase integration and licensing complexity. | Do not integrate in S1. |

### Business authorization and delivery components

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

- Logto as the external OIDC identity, credential, MFA/passkey, enterprise federation and user-lifecycle provider;
- existing OIDC/BFF Session, Principal Context, Workload Identity and delegation libraries;
- IAM-owned typed authorization facts and decisions;
- `pgx/v5` plus explicit SQL and PostgreSQL RLS;
- the locked `scripts/generate-platform-contracts.mjs` OpenAPI pipeline;
- existing Problem Details, Route/Data Ownership, Audit, Outbox/Inbox, OpenTelemetry and supply-chain gates;
- owner-specific migration images and expand-contract release practice;
- native PostgreSQL keyset queries and opaque signed/versioned cursors implemented as project-specific contract behavior.

No new relationship-authorization service, second business policy store, ORM, migration framework, generic repository abstraction or pagination framework is introduced by the S1 specification.

## Logto boundary

- Trusted identity key: configured Logto issuer plus immutable subject.
- Platform Principal mapping: server-side, auditable and independent of mutable email/display-name fields.
- Logto responsibilities: authentication, credentials, MFA/passkeys, external identity federation, account lifecycle and external session protocols.
- Platform IAM responsibilities: Principal business mapping, OrganizationMembership, RoleBinding, SiteBinding, explicit deny, Policy revision, delegation and business authorization decisions.
- Domain-owner responsibilities: re-evaluate the delegated Scope against authoritative resource ownership.
- Logto Organization/role claims: onboarding or reconciliation inputs only; never direct Registry authorization.
- Management API: server-side only, least privilege, secret-managed, rate-limited and audited.

## Conditional POC

Ticket 01 may compare `sqlc` with direct `pgx` for the exact S1 list/detail queries. The POC must remain disposable and may only be adopted when all of the following are true:

- generated code is deterministic and checked by the existing delivery gate;
- transaction-local RLS Scope remains explicit and testable;
- SQL remains owned by `platform-core-service` rather than a shared generic data layer;
- current and previous contract compatibility is unaffected;
- the added tool and maintenance surface are smaller than the handwritten query layer it replaces.

Failure to satisfy any condition means S1 continues with direct `pgx`.
