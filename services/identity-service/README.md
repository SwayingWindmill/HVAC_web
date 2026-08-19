# Identity Service

`identity-service` is Phase 1 Identity Infrastructure. It authenticates platform-managed credentials and exposes the minimal standard OIDC surface required by `energy-api` / Platform Gateway.

It is **not** the platform authorization system. Tenant membership, Site bindings, Roles, Policies and explicit denies remain owned by IAM. The immutable identity handoff is standard OIDC `issuer + subject`.

## Implemented scope

- Username/password authentication.
- Argon2id password hashing.
- OIDC Discovery.
- Authorization Code flow with PKCE S256.
- `state` and `nonce` binding through the Gateway flow.
- Single-use, short-lived authorization codes.
- RS256 ID tokens and JWKS.
- Minimal logout redirect endpoint.
- Failed-password lockout: five failed attempts lock the user for 15 minutes.
- Offline administrator user creation and password reset through `cmd/identity-admin`; there is no public registration endpoint.

The service intentionally does not implement Refresh Token usage, social login, MFA, Passkeys, email/SMS login, password recovery, SAML, enterprise federation, or Tenant/Site authorization. Add those only when product requirements require them.

## Persistence

Credentials and OIDC authorization artifacts live in the separate `hvac_identity` PostgreSQL database under the `identity` schema. IAM does not store password material. The `identity_directory_reader` database role can read only `id`, `display_name`, `email`, and `status` for reconciliation.

## Runtime configuration

Required environment variables:

- `IDENTITY_ISSUER`
- `IDENTITY_CLIENT_ID`
- `IDENTITY_REDIRECT_URI`
- `IDENTITY_POST_LOGOUT_REDIRECT_URI`
- `IDENTITY_DATABASE_URL`
- `IDENTITY_SIGNING_KEY_FILE`

`IDENTITY_SIGNING_KEY_FILE` must point to a deployment-provided PKCS#8 RSA private key file. The service fails closed when the file is missing or invalid.

Optional listener settings:

- `IDENTITY_ADDR` (default `:19095`)
- `IDENTITY_DIAGNOSTICS_ADDR` (default `:19085`)

In Phase 1 Docker Compose the public issuer is served through nginx under `/identity/`; the service itself is not published as an additional host port.

## User provisioning

`cmd/identity-admin` is an offline administrative command and must connect with the dedicated `identity_admin` database role, not `identity_runtime`. Supply the database URL and user fields through runtime environment variables; do not commit credentials or initial passwords to Git.

Supported operations are `create`, `reset-password`, and `reset-password-random`. The random reset mode generates the replacement credential inside the command and prints it once. After creating a user, reconcile its immutable `issuer + subject` into IAM with `services/iam-service/cmd/identity-reconciler` and an explicitly approved Tenant/Role/Site seed. The IdP never assigns platform authorization.

## Signing-key behavior

Phase 1 runs a single `identity-service` instance and loads its RSA signing key from `IDENTITY_SIGNING_KEY_FILE`. `cmd/signing-keygen` creates a PKCS#8 RSA key for a deployment runtime directory; the private key must never be committed. The `kid` is deterministically derived from the public key, so service restarts preserve the JWKS identity while the same key file is mounted. ID tokens live for five minutes. Deliberate key rotation is an operator action and must replace the deployment key file in a controlled rollout.
