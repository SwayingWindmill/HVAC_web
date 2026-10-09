# Workload Principal source review (2026-10-09)

Scope: how an unattended workload, here the energy read-model projector, gets authorization to read an owner's HTTP interface without a user session. The decision is recorded in ADR 0017.

## Reviewed sources

| Source | Pinned reference | What it does |
| --- | --- | --- |
| Keycloak service accounts | `keycloak/keycloak@26.0.0` (`632f214aa2d668e8b59920c3dfcc449da68254a4`), `docs/documentation/server_admin/topics/clients/oidc/service-accounts.adoc` | A confidential client has its own service account with ordinary role mappings. Client-credentials authentication returns an access token only: "There is no refresh token returned and there is also no user session created" (L52); the client re-authenticates when the token expires (L53). |
| OAuth 2.0 client credentials | RFC 6749 §4.4 and §4.4.3 | The client acts on its own behalf for resources "previously arranged with the authorization server"; no refresh token is issued. |
| OAuth 2.0 mutual-TLS client authentication | RFC 8705 §2 | The client authenticates to the authorization server with its TLS client certificate instead of a shared secret. |
| OAuth 2.0 token exchange | RFC 8693 §1.1, §4.1, §4.4 | `act` and `may_act` express one party acting for another, distinct from a client acting for itself. |
| ThingsBoard rule engine | `thingsboard/thingsboard@v4.2.2.6` (`06bb41f0afd85fd44aa27311dcf24cd5ce883be1`), `rule-engine/rule-engine-api/src/main/java/org/thingsboard/rule/engine/api/TbContext.java` | Rule nodes get `getTenantId()` (L257) and DAO services such as `getAssetService()`/`getDeviceService()` (L267–269) directly; trust comes from running inside the platform process, with no per-call authorization. |

## Decisions

- **ADOPT** the client-credentials shape (Keycloak, RFC 6749 §4.4): the workload is its own principal with ordinary role bindings, and it obtains a short-lived token by authenticating itself, with no refresh token and no session. Here the token is the existing 30-second Registry grant and the workload re-asks IAM before expiry.
- **ADOPT** mutual-TLS client authentication (RFC 8705): the IAM route takes the subject from the verified mTLS peer identity, which the platform already issues to every workload, instead of a client secret.
- **ADAPT** principal identity: instead of a separate service-account table keyed by client ID, the principal is an `iam.principals` row whose issuer is the SPIFFE trust domain and whose subject is the SPIFFE ID, so the existing Registry evaluation, policy revision and decision audit apply unchanged.
- **REJECT** identity-only trust inside the owner (ThingsBoard `TbContext`): it would let one compromised workload read every Tenant on that route and remove the per-Tenant revocation and audit Registry was designed around. Pipelines that receive already-resolved facts with the request keep using identity alone (ADR 0016).
- **REJECT** standing delegation via `may_act` (RFC 8693): unattended processing would depend on a named administrator's account staying active.
