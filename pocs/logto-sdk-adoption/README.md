# Logto Go SDK adoption POC

This isolated module compares the existing HVAC Gateway OIDC/BFF implementation with `logto-io/go/v2 v2.2.0` under the same S1 identity and operability requirements. It does not change production routing, Session storage or IAM authorization.

## Question

Should the platform:

1. retain the existing hand-written OIDC/BFF implementation;
2. replace it with the official Logto high-level Go client; or
3. partially adopt official Logto protocol primitives while retaining platform security and HVAC authorization controls?

Feature overlap is not a rejection criterion. The decision is based on black-box behavior, persistence semantics, security maintenance and rollback cost.

## Test seams

The comparison exercises public protocol boundaries rather than SDK internals:

- authorization redirect parameters;
- callback state, PKCE and identity-value acceptance;
- discovery and JWKS replacement;
- provider outage;
- storage write failure;
- two clients refreshing through shared durable storage;
- remote revocation failure and local logout;
- organization-context claim verification;
- dependency vulnerability scanning.

The existing implementation baseline is the complete `services/platform-gateway` Go test suite, including browser-shaped OIDC/BFF tests.

## Run

```bash
npm run s1:logto:poc
```

Machine-readable evidence is written to:

```text
out/s1-ticket-02/logto-sdk-adoption.json
out/s1-ticket-02/logto-comparison.json
```

## Security pin

The Logto SDK release and its current `v2` branch require `github.com/go-jose/go-jose/v4 v4.0.5`, which is affected by `GO-2026-4945`. This POC explicitly resolves `go-jose/v4 v4.1.4`, verifies SDK compatibility and requires a clean `govulncheck` result. Production adoption must retain an equivalent fixed version until Logto publishes a release that includes the remediation.

## Decision

The measured result is **partial SDK adoption**:

- use official `logto-io/go/v2/core` protocol helpers and maintained JOSE/JWKS verification;
- retain platform-owned state TTL, return target binding, nonce, token-type and not-before validation;
- retain the encrypted PostgreSQL BFF Session, transactional Audit/Outbox and revocation model;
- add a platform refresh coordinator with cross-instance single-flight and compare-and-swap;
- treat Logto Organization data only as onboarding/reconciliation input;
- retain platform IAM as the only HVAC business authorization authority.

See `results.md` for the complete scorecard.
