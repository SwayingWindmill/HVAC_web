# S0 authenticated principal loop

Ticket 02 establishes the first authenticated browser-to-service identity loop without introducing durable session, audit, event, or business-domain state.

## Trust boundaries

```text
Browser
  HTTPS + opaque Secure HttpOnly SameSite=Lax cookie
      |
      v
HVAC Web / platform-gateway public edge
  OIDC Authorization Code + PKCE
  server-side encrypted provider tokens
  signed short-lived delegation
      |
      | TLS 1.3 mutual authentication
      v
private iam-service
```

The browser can reach only the public `/api/v1` Gateway contract. It does not receive an IAM route, client certificate, access token, refresh token, ID token, authorization code after callback completion, or delegation grant.

## Browser authentication flow

1. `GET /api/v1/auth/login` creates one-time server-side state containing a PKCE verifier, nonce, local `returnTo`, and creation time.
2. Gateway redirects to the configured OIDC Authorization endpoint with Authorization Code Flow, PKCE S256, state, and nonce.
3. `GET /api/v1/auth/callback` consumes state exactly once and exchanges the code from the server.
4. Gateway validates discovery issuer, JWKS signature, algorithm, key ID, issuer, audience, token use, token type, nonce, expiry, and not-before.
5. Provider tokens are encrypted with AES-GCM and remain only in the server-side session record.
6. The browser receives an opaque `__Host-hvac_session` cookie with `Secure`, `HttpOnly`, `Path=/`, and `SameSite=Lax`.
7. Gateway redirects to the local return path, removing callback code and state from browser URL state.

The ticket-02 session store is intentionally in-memory. Durable sessions, transactional revocation and audit/outbox behavior belong to ticket 03.

## Current principal and actor chain

`GET /api/v1/principal` requires a valid BFF Session. Gateway signs a delegation grant with its mTLS workload private key and calls `POST /internal/v1/principal/current` on IAM.

The grant is valid for no more than 60 seconds and binds:

- initiating subject and subject issuer;
- executing Gateway SPIFFE identity;
- acting Organization;
- IAM audience;
- exactly one action, `principal:read`;
- exactly one session scope;
- policy revision;
- session ID, grant ID, issue time and expiry.

IAM verifies both the TLS client certificate chain and the grant signature using the peer certificate public key. It rejects audience changes, action or scope expansion, forwarding under another executing service, invalid signatures, expired grants and grants with excessive lifetime.

The public response keeps these fields distinct:

- initiating user principal;
- executing service principal;
- acting Organization context;
- audience;
- policy revision;
- delegation expiry;
- BFF Session ID, expiry, CSRF token and revocation objective.

## Public edge protections

Gateway rejects caller-provided `X-Principal`, role, Organization, Site, admin and delegation headers. Cookie-authenticated state changes require both:

- exact public `Origin` match;
- constant-time comparison of the session-bound `X-CSRF-Token`.

Logout and administrative revocation update the in-memory session table synchronously. The configured propagation objective is one second; black-box tests verify denial on the next request.

## Stable public identity errors

Representative Problem Details codes include:

- `AUTHENTICATION_REQUIRED`
- `SESSION_INVALID`
- `FORGED_IDENTITY_HEADER`
- `OIDC_STATE_INVALID`
- `OIDC_PKCE_VALIDATION_FAILED`
- `OIDC_ISSUER_INVALID`
- `OIDC_AUDIENCE_INVALID`
- `OIDC_TOKEN_TYPE_INVALID`
- `OIDC_SIGNATURE_INVALID`
- `OIDC_NONCE_INVALID`
- `OIDC_TOKEN_EXPIRED`
- `OIDC_TOKEN_NOT_ACTIVE`
- `ORIGIN_NOT_ALLOWED`
- `CSRF_TOKEN_INVALID`
- `IAM_UNAVAILABLE`
- `IAM_IDENTITY_REJECTED`
- `SESSION_REVOCATION_FORBIDDEN`

Public errors never include tokens, authorization codes, cookies, grants, provider secrets or raw IAM responses.

## Local deterministic topology

```bash
npm run dev:s0-auth
```

This generates an ephemeral local CA and starts only:

```text
HTTPS OIDC fixture
HTTPS HVAC Web
platform-gateway
mTLS-only iam-service
```

The launcher does not start NestJS, Copilot Runtime, a database, a broker, an observability collector or later S0 services. The test CA and private keys are created under the operating-system temporary directory and removed on shutdown.

The local fixture supports success, admin login, JWKS rotation and deterministic rejection cases using `login_hint` values exercised only by automated tests.

## Verification

```bash
npm run contracts:check
npm run test:identity
npm run build:energy-api
npm run build:iam-owner
npm run build:oidc-fixture
npm run lint
npm run build
npm run audit:auth-principal
```

The API tests exercise real redirect/callback requests, PKCE, nonce, issuer, audience, signature, expiry, not-before, token type, mTLS, delegation limits, CSRF, Origin, logout and administrative revocation. The Edge audit verifies the browser flow, cookie flags, generated client, storage hygiene and IAM network isolation.
