# 02 — Authenticated principal loop through Gateway and IAM

**What to build:** let a real browser complete OIDC Authorization Code Flow with PKCE through the Gateway, receive only an opaque BFF Session cookie, and call a typed current-principal endpoint. Gateway must call the private `iam-service` with mTLS Workload Identity and a short-lived delegated user context, so the response proves both user authentication and internal service authentication end to end.

**Blocked by:** 01 — Contract-first Gateway bootstrap.

**Status:** completed

- [x] Local/test includes a deterministic OIDC provider or fixture that exercises the same issuer, audience, nonce, PKCE and JWKS validation path expected in staging.
- [x] Browser login and callback create an opaque `HttpOnly`, `Secure` and appropriate `SameSite` BFF Session; no access, refresh or ID token is stored in localStorage, sessionStorage or URL state.
- [x] The authenticated current-principal API is available only through Gateway and uses generated contract types.
- [x] Gateway calls private `iam-service` over authenticated mTLS rather than trusting browser-supplied identity fields.
- [x] Internal identity records initiating principal, executing service principal, acting Organization context, audience, policy revision and expiry as distinct fields.
- [x] A short-lived delegation grant is audience-, action- and scope-bound and cannot be forwarded or expanded by IAM.
- [x] Forged `X-Principal`, role, Organization, Site and admin headers are ignored or rejected.
- [x] Invalid issuer, audience, token type, signature, nonce, PKCE verifier, expiry and not-before cases return stable Problem Details.
- [x] CSRF and Origin protections cover cookie-authenticated state-changing endpoints.
- [x] Logout and administrative session revocation invalidate subsequent API access; tests verify the configured revocation propagation objective.
- [x] Direct browser or unauthenticated network access to IAM fails even when the caller knows the internal address.
- [x] The HVAC Web displays authenticated principal/session state through the generated client without introducing business navigation changes.
- [x] Logs, traces and errors do not expose tokens, authorization codes, cookies, grants or OIDC provider secrets.
- [x] Black-box tests run through a real redirect/callback flow and verify both successful and rejected identity paths.

