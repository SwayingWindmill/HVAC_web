# iam-service

`iam-service` is the private platform authorization seam. `identity-service` owns authentication credentials and exposes the platform OIDC identity boundary; IAM owns the immutable issuer/subject mapping and the HVAC authorization projection. IAM is not browser-facing and never validates or stores login credentials.

## Network contract

IAM serves TLS 1.3 with mandatory client-certificate verification. The Gateway workload is accepted for these routes:

```text
POST /internal/v1/principal/current
POST /internal/v1/registry-read/decision
POST /internal/v1/registry-read/grant-status
```

Knowing the address is insufficient. Gateway routes require a trusted client certificate whose SPIFFE identity exactly matches `IAM_ALLOWED_WORKLOAD_SPIFFE`, plus a bounded delegation signed by that workload certificate. The grant-status route is a separate Core-only workload endpoint and accepts only the exact `IAM_CORE_WORKLOAD_SPIFFE` identity.

## Current Principal

`POST /internal/v1/principal/current` requires:

- audience `iam-service`;
- exactly one action, `principal:read`;
- exactly one `session:{id}` scope;
- initiating issuer/subject, session, policy and delegation identifiers;
- no forwarding or transitive expansion.

The response exposes navigation context. Its Tenant context is not itself an authorization grant.

## Registry-read decision

`POST /internal/v1/registry-read/decision` requires the same Gateway and session boundary with the inbound action `registry:authorize`. The JSON body contains only:

```json
{
  "tenantId": "018f1e00-0000-7000-8000-000000000001",
  "action": "site.read"
}
```

IAM resolves the configured issuer plus immutable subject into a platform Principal, verifies active TenantMembership for the requested Tenant, applies RoleBinding and SiteBinding actions within that same Tenant, and then applies explicit deny. Membership alone grants no Registry resources. Bindings for another Tenant are ignored and cannot widen the requested Tenant scope. The Tenant identifier must be a UUIDv7 and is rejected before the database boundary when malformed.

The typed response contains allow or deny, platform Principal, Tenant, allowed and denied Site scope, one concrete action, policy revision, reason code and decision time. Deny is a successful authorization evaluation and therefore returns HTTP 200 without a downstream delegation.

For allow decisions IAM signs a maximum-30-second delegation for `platform-core-service`. It is bound to:

- IAM issuer and Gateway presenter SPIFFE identities;
- one audience and one concrete Registry action;
- initiating platform Principal and external issuer/subject;
- Tenant and allowed/denied Site scope;
- policy revision and allow reason;
- parent Gateway delegation, Session, issue/expiry time and unique identifier;
- non-transitive semantics.

`libs/registryauth` is the Core-side verification contract. Verification requires a current policy revision and an online revocation check and fails closed for malformed, oversized, wrong-audience, wrong-presenter, expired, revoked, stale-policy or contradictory-scope grants.

## Registry grant status

`POST /internal/v1/registry-read/grant-status` accepts only the configured Core workload over verified mTLS. The bounded request contains the Tenant ID and grant `jti`; it never contains the raw grant. IAM returns the current active Registry policy revision and whether the identifier has an unexpired revocation record. The status lookup uses the same read-only IAM runtime connection and Tenant RLS context, and any database or policy failure makes Core authorization unavailable.

## Production authorization store

Set `IAM_DATABASE_URL` to use the frozen `iam` PostgreSQL Schema. The connection must authenticate exactly as `s1_iam_runtime`; postgres, migration and Core identities are rejected during pool creation. Each decision uses one repeatable-read, read-only transaction:

1. `iam.resolve_principal_identity` resolves only exact external issuer plus subject and returns no mutable profile fields.
2. IAM sets transaction-local Principal and Tenant RLS context.
3. Membership, RoleBinding, SiteBinding, deny and active policy revision are read from the same snapshot.
4. Unknown stored Registry actions, missing active policy or database/RLS failure make authorization unavailable rather than broadening access.

The runtime identity has no Core Schema access and no IAM mutation privilege. `IAM_DATABASE_URL` and `IAM_S1_AUTHORIZATION_FIXTURE` are mutually exclusive. With neither configured, IAM remains deny-all rather than falling back to fixture data.

## Explicit onboarding reconciliation

`PostgresReconciliationStore` is a separate management-plane boundary that accepts only the `s1_iam_reconciler` database identity. It applies a complete, approved Principal authorization snapshot and records a SHA-256 input hash against `(source_system, source_key, source_version)`.

- The same version and hash records `NO_CHANGE` without rewriting authorization facts.
- A stale version, same-version hash conflict, source-to-Principal reassignment, issuer/subject reassignment or Principal-ID reassignment records `QUARANTINED` evidence and does not mutate active facts.
- Mutable email and display name can update an existing immutable issuer/subject mapping. Matching email never merges different subjects.
- Applied snapshots replace the reconciled Principal's Membership, RoleBinding, SiteBinding and explicit-deny facts in one serializable transaction.
- Audit events persist source/version/hash, safe identifiers, result/reason and fact counts. They do not persist provider tokens, browser cookies, client credentials or raw delegations.

The reconciler role is RLS-bound, cannot access `core_registry`, cannot mutate policies and is the only runtime identity allowed to write the reconciliation ledger and quarantine tables. The normal `s1_iam_runtime` decision identity cannot read them.

`cmd/identity-reconciler` is the non-HTTP onboarding boundary for the platform-owned identity directory. It reads one bounded JSON command from stdin, loads only the selected user's immutable ID, display name, email and lifecycle status through the read-only `identity_directory_reader` database role, and constructs the complete `ReconciliationRequest` from explicitly approved platform Tenant, RoleBinding, SiteBinding and deny facts. No IdP role or organization claim can become authorization truth.

Run it as a secured management job with:

- `IDENTITY_DIRECTORY_DATABASE_URL`
- `IAM_RECONCILER_DATABASE_URL`
- `IDENTITY_ISSUER`

The identity directory connection is read-only and cannot read credential material. IAM reconciliation remains versioned, idempotent and fail-closed.

## Authorization fixtures

The default runtime store is deny-all. S1 deterministic facts are enabled only when all of the following are explicitly configured:

```text
IAM_S1_AUTHORIZATION_FIXTURE=true
IAM_EXTERNAL_SUBJECT_ISSUER=https://configured-identity-issuer.example
```

The fixture covers direct Tenant membership plus role/action bindings, SiteBinding, explicit deny, membership-without-action, revoked membership and unmapped subjects. It exists for deterministic S1 integration and must not be enabled as an implicit production fallback.

## Header and claim boundary

IAM rejects client-supplied Principal, Role, Tenant, Site, admin or scope headers, including prefixed variants. Mutable email, display name and identity-provider claims do not select or expand Registry authorization facts. Registry decisions use only the verified issuer/subject plus platform-owned Membership/Binding/Policy data. A trusted Gateway caller may request a Registry grant for an explicitly allowlisted downstream presenter such as Operations Agent; IAM signs that presenter directly and rejects arbitrary presenter expansion.

Decision telemetry contains safe identifiers, action, reason, policy revision, scope counts and trace correlation. It does not contain provider tokens, cookies, raw delegations, email or display name.

## Environment

- `IAM_SERVICE_ADDR`
- `IAM_TLS_CERT`
- `IAM_TLS_KEY`
- `IAM_CLIENT_CA`
- `IAM_ALLOWED_WORKLOAD_SPIFFE`
- `IAM_CORE_WORKLOAD_SPIFFE`
- `IAM_AUDIENCE`
- `IAM_REGISTRY_GRANT_AUDIENCE`
- `IAM_OPERATIONS_AGENT_SPIFFE`
- `IAM_POLICY_REVISION`
- `IAM_DATABASE_URL`
- `IAM_S1_AUTHORIZATION_FIXTURE`
- `IAM_EXTERNAL_SUBJECT_ISSUER`

The Registry delegation issuer is derived from the IAM TLS certificate SPIFFE URI rather than a mutable environment value.

## Verification

```bash
npm run test:identity
npm run s1:registry:postgres
npm run test:security-negative
npm run build:iam
```
