# iam-service

`iam-service` is the private identity seam introduced by S0 ticket 02. It is not browser-facing and contains no HVAC business state.

## Network contract

IAM serves TLS 1.3 with mandatory client-certificate verification. Its ticket-02 route is:

```text
POST /internal/v1/principal/current
```

Only the configured Gateway SPIFFE identity is accepted. Knowing the address is insufficient; clients without a trusted workload certificate fail during TLS negotiation.

## Delegated identity

Gateway sends a signed `X-Delegation-Grant`. IAM verifies the signature using the authenticated TLS peer certificate public key and requires:

- issuer and executing service equal the Gateway SPIFFE identity;
- audience exactly `iam-service`;
- one action, `principal:read`;
- one session-bound scope;
- lifetime no greater than 60 seconds;
- complete initiating user, Organization, policy, session and grant identifiers.

IAM does not accept browser-provided Principal, Role, Organization, Site or admin headers and does not forward or expand the delegation.

## Environment

- `IAM_SERVICE_ADDR`
- `IAM_TLS_CERT`
- `IAM_TLS_KEY`
- `IAM_CLIENT_CA`
- `IAM_ALLOWED_WORKLOAD_SPIFFE`
- `IAM_AUDIENCE`

## Verification

```bash
npm run test:identity
npm run build:iam
```

Durable identity records, audit writes, event publication and persistent revocation are intentionally deferred to ticket 03.
