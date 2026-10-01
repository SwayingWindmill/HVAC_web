# 2026-09-07 remote test Identity credential drift

## Context

After the PostgreSQL WAL/disk incident was repaired, the remote test deployment returned the expected anonymous `401 AUTHENTICATION_REQUIRED`. A real browser acceptance was then started to verify the complete sign-in -> Principal -> Site -> dashboard/assets/alarms path.

## Runtime evidence

- `POST /api/v1/auth/login` correctly redirected the browser to the first-party Identity authorization page.
- The Identity login form rendered normally with username/password fields and a server-issued challenge.
- The remote runtime `identity-local-credentials.json` referenced username `hvacadmin`, but the remote `hvac_identity.identity.users` table contained no such user.
- The remote Identity database contained one current user: `admin`.
- `admin` was `ACTIVE`, had `failed_attempts = 0`, was not locked, and had a recent successful login timestamp.
- The canonical browser-acceptance credential file `runtime/local-admin.credentials` also failed authentication, so its password no longer matched the current Identity record.

No password hash or replacement password was read or recorded during diagnosis.

## Root cause

The test deployment had more than one non-versioned credential artifact maintained outside a single lifecycle command. The Identity database, `runtime/local-admin.credentials`, and the obsolete `runtime/identity-local-credentials.json` could therefore drift independently. Browser acceptance consumed a stale artifact even though the actual `admin` identity remained active.

This is an operations-contract defect, not an OIDC, Principal, account-lockout, MFA, or route-audit failure.

## Corrective action

- `runtime/local-admin.credentials` is the only canonical development/testing administrator credential artifact.
- `npm run deployment:phase1:identity-reset` is the standard development/testing recovery entrypoint.
- That command invokes `identity-admin` with `reset-password-random`, captures the replacement credential without printing it, and atomically rewrites `runtime/local-admin.credentials` with mode `0600`.
- The recovery command rejects environments other than `development` and `testing` before invoking Identity administration.
- Operators must not maintain a second local/test credential file by hand.
- The Phase 1 deployment checker verifies the recovery entrypoint, environment boundary, canonical credential path, file-permission handling, and non-printing behavior.

## Remote follow-up

The remote password itself was not changed during this investigation because the current automation execution boundary did not permit credential-reset actions. The remote Identity account remains `ACTIVE`; an authorized operator can run the checked-in development/testing recovery command on the test host, or provide the current test credential for final browser acceptance.

Once a valid credential is available, rerun the real browser acceptance and require all of the following before closing the incident:

1. OIDC callback completes.
2. `/api/v1/principal` returns 200.
3. `/api/v1/sites` returns the authorized Site list.
4. Site dashboard renders without bootstrap/audit failures.
5. Device Center renders.
6. Alarm Center renders.
