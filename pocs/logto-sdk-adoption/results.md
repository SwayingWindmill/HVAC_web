# Logto Go SDK adoption results

Date: 2026-07-21

SDK under test: `logto-io/go/v2 v2.2.0`, MIT.

## Scorecard

| Requirement | Existing Gateway baseline | Official high-level Logto client | Selection |
|---|---|---|---|
| Authorization Code + S256 PKCE | Pass | Pass | Official Core helper is reusable. |
| State validation | Pass | Pass | Keep platform TTL, one-time use and return-target binding. |
| Nonce generation and validation | Pass | Fail: no nonce is sent or validated | Keep platform control. |
| Issuer, audience, signature and expiry | Pass | Pass | Adopt maintained Core/JOSE verification behind platform validation. |
| Token type and not-before enforcement | Pass | Fail in measured callback | Keep platform control. |
| JWKS key replacement | Pass | Pass | Official Core/JOSE is suitable. |
| Provider outage | Explicit safe error | Fails closed | Official discovery helper is suitable with platform Problem Details. |
| Durable Session write failure | Transaction returns an error | Fail: `Storage.SetItem` cannot report failure | Keep PostgreSQL Session adapter outside the high-level client. |
| Cross-instance refresh coordination | Platform design requires one refresh/CAS | Fail: two clients issued two refreshes | Add platform refresh coordinator. |
| Remote revocation failure | Must be observable and reconciled | Fail: `SignOut` ignored HTTP 500 | Keep platform logout/reconciliation control. |
| Local credential clear and end-session URI | Local Session revocation exists | Pass | Reuse official revocation/end-session helpers, not the full logout state machine. |
| Organization-context claim verification | Organization claims cannot grant HVAC access | Fail: helper decoded attacker-signed claims without verification | Never use helper output as HVAC authorization; verify onboarding inputs separately. |
| Opaque HttpOnly BFF Cookie | Pass | Requires adapter | Keep platform BFF boundary. |
| Transactional Audit/Outbox | Pass | Not provided | Keep platform Session mutation path. |
| Known dependency vulnerabilities | Existing production scans pass | Initial scan failed on `GO-2026-4945` | Require `go-jose/v4 v4.1.4` or newer fixed release. |

## Security finding

`logto-io/go/v2 v2.2.0` and the current upstream `v2` branch require `github.com/go-jose/go-jose/v4 v4.0.5`. `govulncheck` found reachable `GO-2026-4945`. The isolated module upgrades the dependency to `v4.1.4`; the SDK tests remain compatible and the vulnerability scan then reports no vulnerabilities.

This override is a production adoption gate, not an optional POC convenience.

## Decision

Select **partial SDK adoption**.

### Adopt

- `logto-io/go/v2/core` for maintained Logto discovery and OAuth/OIDC protocol requests;
- fixed `go-jose/v4` for JOSE/JWKS parsing and signature verification;
- official revocation and end-session URI primitives behind platform error handling;
- official refresh request construction behind a platform coordinator.

### Retain or add in platform code

- one-time state with TTL and safe return target;
- nonce generation and constant-time validation;
- token type, not-before, issuer, audience, expiry and immutable issuer-plus-subject checks;
- encrypted PostgreSQL BFF Session and transactional Audit/Outbox;
- refresh rotation compare-and-swap and cross-instance single-flight;
- observable revoke failure, local Session termination and Logto global logout reconciliation;
- platform Principal mapping and HVAC Membership/Binding/Policy authorization.

### Do not adopt

- the high-level Logto client as the authoritative durable Session state machine;
- unsafe Organization claim decoding as authorization evidence;
- Logto Organization or role values as direct HVAC Organization, Site, Equipment or Device permission.

## Rollback

The production adapter must sit behind the existing Gateway identity interface. Rollback switches the protocol implementation to the existing tested path without changing the browser cookie, PostgreSQL Session schema, public Principal contract or platform IAM facts.
