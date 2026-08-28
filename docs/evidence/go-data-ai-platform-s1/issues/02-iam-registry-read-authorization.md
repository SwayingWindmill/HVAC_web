# 02 — IAM Registry-read authorization projection

**What to build:** integrate Logto as the selected external identity provider and extend `iam-service` with the current platform-owned authorization facts and typed internal decision needed for S1 Registry reads. Link the immutable Logto issuer/subject to a platform Principal, reconcile approved onboarding inputs explicitly, resolve effective Organization and Site Scope from OrganizationMembership, RoleBinding, SiteBinding, explicit deny and Policy revision, then issue a short-lived delegation specifically for `platform-core-service`. Do not add a second business policy engine or trust browser-selected or Logto organization context as final authorization.

**Blocked by:** 01 — Contract, domain model and ownership baseline.

**Status:** in-progress

## Logto SDK adoption gate

Ticket 02 must not prefer the current hand-written OIDC/BFF implementation merely because it already exists. Before production integration, compare these two implementations under the same black-box security and operability suite:

1. Existing Gateway OIDC/BFF Session implementation with Logto as the standards-compliant external provider.
2. Logto official Go SDK `logto-io/go/v2` taking over every login, callback, token refresh, organization-token and sign-out responsibility it can safely own, while platform IAM retains HVAC business authorization.

The comparison must cover login/callback correctness, PKCE/state/nonce, server-side token storage, refresh and revocation, global and local logout, JWKS rotation, Logto outage behavior, disabled users, duplicate email with distinct subjects, Organization claims/tokens, secret exposure, dependency vulnerabilities, upgrade surface, migration/rollback and compatibility with the existing BFF Session contract. Record measured evidence and select full SDK adoption, partial SDK adoption or the existing implementation only after the comparison. Feature overlap is not a rejection criterion.

### Adoption-gate result

The reproducible POC in `pocs/logto-sdk-adoption/` selects **partial SDK adoption**. Official `logto-io/go/v2/core` protocol helpers and fixed JOSE/JWKS dependencies will replace suitable hand-written protocol plumbing. Platform code retains nonce and strict claim validation, encrypted PostgreSQL BFF Session, Audit/Outbox, cross-instance refresh coordination, logout reconciliation and all HVAC authorization facts. The high-level SDK client is not used as the authoritative durable Session state machine.

The POC initially found reachable `GO-2026-4945` through the SDK requirement `go-jose/v4 v4.0.5`; the isolated module pins `v4.1.4`, remains compatible and passes `govulncheck`. This fixed dependency is a production adoption requirement.

This closes only the SDK selection gate. Ticket 02 remains in progress until the IAM authorization projection and production identity adapter satisfy every acceptance item below.

- [ ] IAM persists or deterministically fixtures OrganizationMembership, RoleBinding, SiteBinding, explicit deny and Policy revision in its owned Schema.
- [ ] Logto is configured as the external OIDC provider for login, credentials, MFA/passkeys and external user lifecycle; Go IAM does not implement passwords or authentication factors.
- [ ] Platform Principal mapping uses configured issuer plus immutable Logto subject; mutable email/display name fields cannot merge or reassign identities.
- [ ] Logto Organization/role/custom claims are treated only as onboarding or reconciliation inputs and cannot directly grant Registry access.
- [ ] Provisioning/reconciliation into platform Membership/Bindings is explicit, idempotent, versioned and audited, with conflict and quarantine handling.
- [ ] Logto Management API access is server-side, least-privilege, secret-managed and unavailable to browsers or Core.
- [ ] Membership alone does not grant all Sites in an Organization.
- [ ] Cross-organization SiteBinding grants only the bound Site and allowed Registry actions.
- [ ] Explicit deny takes precedence over Membership, RoleBinding and SiteBinding allows.
- [ ] IAM returns a typed Registry-read decision with acting Organization, allowed Organization/Site Scope, actions, policy revision and decision reason code.
- [ ] IAM issues an audience-bound, short-lived, non-transitive delegation for `platform-core-service` with initiating principal, executing audience, `jti`, expiry and revocation semantics.
- [ ] Wrong audience, expired, revoked, malformed and policy-stale delegations fail closed.
- [ ] Client Organization/Site/role/admin/scope headers and token business claims cannot expand the IAM decision.
- [ ] Current-principal responses expose navigation context without presenting active Organization or active Site as authorization truth.
- [ ] Login, logout, disabled-user, JWKS rotation, duplicate-email/different-subject and Logto outage paths preserve safe BFF Session behavior.
- [ ] IAM runtime and migration identities remain separate from Core identities and cannot access the Core Schema.
- [ ] Authorization allow and deny decisions emit safe Audit evidence with principal, acting Organization, Site Scope, action, policy revision and trace correlation.
- [ ] Two-Organization tests cover direct membership, cross-organization SiteBinding, sibling Site denial, explicit deny, revoked membership and no-access principals.
- [ ] Revocation propagation and maximum accepted policy staleness meet the S0 identity baseline.
- [ ] Logs, traces, metrics and persisted records contain no raw token, cookie or delegation material.
