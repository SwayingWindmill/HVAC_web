# 02 — IAM Registry-read authorization projection

**What to build:** extend `iam-service` with the current authorization facts and typed internal decision needed for S1 Registry reads. Resolve effective Organization and Site Scope from OrganizationMembership, RoleBinding, SiteBinding, explicit deny and Policy revision, then issue a short-lived delegation specifically for `platform-core-service`. Do not add a second policy engine or trust browser-selected context.

**Blocked by:** 01 — Contract, domain model and ownership baseline.

**Status:** ready-after-spec-approval

- [ ] IAM persists or deterministically fixtures OrganizationMembership, RoleBinding, SiteBinding, explicit deny and Policy revision in its owned Schema.
- [ ] Membership alone does not grant all Sites in an Organization.
- [ ] Cross-organization SiteBinding grants only the bound Site and allowed Registry actions.
- [ ] Explicit deny takes precedence over Membership, RoleBinding and SiteBinding allows.
- [ ] IAM returns a typed Registry-read decision with acting Organization, allowed Organization/Site Scope, actions, policy revision and decision reason code.
- [ ] IAM issues an audience-bound, short-lived, non-transitive delegation for `platform-core-service` with initiating principal, executing audience, `jti`, expiry and revocation semantics.
- [ ] Wrong audience, expired, revoked, malformed and policy-stale delegations fail closed.
- [ ] Client Organization/Site/role/admin/scope headers and token business claims cannot expand the IAM decision.
- [ ] Current-principal responses expose navigation context without presenting active Organization or active Site as authorization truth.
- [ ] IAM runtime and migration identities remain separate from Core identities and cannot access the Core Schema.
- [ ] Authorization allow and deny decisions emit safe Audit evidence with principal, acting Organization, Site Scope, action, policy revision and trace correlation.
- [ ] Two-Organization tests cover direct membership, cross-organization SiteBinding, sibling Site denial, explicit deny, revoked membership and no-access principals.
- [ ] Revocation propagation and maximum accepted policy staleness meet the S0 identity baseline.
- [ ] Logs, traces, metrics and persisted records contain no raw token, cookie or delegation material.
