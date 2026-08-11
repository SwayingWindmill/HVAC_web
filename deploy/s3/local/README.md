# S3 Local Integration Profile

> **Scope: LOCAL_FIXTURE.** This Kind-based profile is a local integration fixture for S3 Command behavior. It is not the canonical Phase 1 deployment and must not be used to infer that Kubernetes is required for Development, Staging or Production. Canonical Phase 1 deployment is `deploy/platform/phase1/`.

This profile runs the S3 command runtime and a local-only Web Gateway on a single-node kind cluster.

It is deliberately separate from `deploy/s3/target/` and must never be used as formal S3-09 certification evidence. Successful runtime and browser checks record `formalCertificationClaim: false`.

## Prerequisites

- Docker Desktop with the Docker Engine running
- `kubectl`
- Node.js and installed repository dependencies
- `curl`
- OpenSSL
- `sha256sum`

The launcher downloads a repository-local kind binary to `out/tools/kind` and verifies its pinned SHA-256. No system-wide kind or Helm installation is required.

## Runtime commands

```bash
bash scripts/s3-local.sh up
bash scripts/s3-local.sh status
bash scripts/s3-local.sh smoke
bash scripts/s3-local.sh logs
bash scripts/s3-local.sh down
```

The `up` command:

1. Generates a disposable local CA, workload certificates, signing keys, CSRF value, provider value, database reference and approved test cohort under `out/s3-local/`.
2. Builds eight local-only images.
3. Creates or reuses the `hvac-s3-local` kind cluster.
4. Loads images into the kind node.
5. Deploys PostgreSQL and applies the production-style S3 role/RLS migrations.
6. Deploys Command Service, Dispatcher, Verifier, the device simulator and the local Web Gateway.
7. Uses a local Device Catalog v2 that binds Tenant, Organization, Site, Device, canonical Command Point UUID and verification Point key, then submits a uniquely idempotent 24°C command and waits for `SUCCEEDED|VERIFIED`.

## Browser commands

Start the Vite development server and a loopback-only Gateway port-forward:

```bash
node scripts/s3-local-web.mjs start
node scripts/s3-local-web.mjs status
node scripts/run-s3-local-web-smoke.mjs
node scripts/s3-local-web.mjs stop
```

Open:

```text
http://127.0.0.1:5173/commands
```

The page runs with `VITE_API_MODE=real` and the explicit development-only flag `VITE_S3_LOCAL_COMMANDS=true`. The generic Commands workbench keeps mutation disabled outside Mock mode; `run-s3-local-web-smoke.mjs` submits through the same-origin local API specifically for integration verification. Route Ownership remains disabled for production traffic. Allow up to 150 seconds for the local durable Verifier to move a submitted command from `DISPATCHING` to `SUCCEEDED`.

The Web flow is:

```text
Browser
→ Vite same-origin /api/v1 proxy
→ loopback kubectl port-forward
→ local Web Gateway
→ mTLS Command Service
→ Dispatcher
→ local device simulator
→ Verifier
→ SUCCEEDED / ACKNOWLEDGED_AND_REPORTED_STATE_VERIFIED
```

The browser Gateway issues short-lived local Command Grants and read Delegations using disposable keys generated under `out/s3-local/`. Mutations require the local Session CSRF token and the exact `http://127.0.0.1:5173` Origin.

## Local simulator

The simulator exposes two internal TLS endpoints:

- A ThingsBoard CE compatible two-way RPC endpoint used by Command Dispatcher.
- An mTLS S2 Reported State endpoint restricted to the Command Verifier SPIFFE identity.

It updates an in-memory setpoint on accepted RPC and returns a later reported-state observation with an incremented business revision. This exercises the real Dispatcher, connector-evidence and Verifier paths without downloading a complete ThingsBoard deployment.

## Security boundary

- All Kubernetes Services are `ClusterIP`.
- No Ingress, NodePort, LoadBalancer, host port or public route is created.
- Browser access uses `kubectl port-forward` bound only to `127.0.0.1`.
- Local application images use `imagePullPolicy: Never`.
- Private keys and generated values stay under ignored `out/s3-local/` paths.
- Kubernetes Secret manifests rendered by `kubectl create ... --dry-run=client` also stay under `out/s3-local/rendered/`.
- Fixed local database passwords and fixture identities are local-only and are not accepted by the target profile.
- The fixture approver is independent from the submitting fixture principal, but it is not a formal human approver.

## Evidence

Successful checks produce:

```text
out/s3-local/smoke-report.json
out/s3-local/web-smoke-report.json
```

The corresponding statuses are:

```text
local-integration-passed
local-web-integration-passed
formalCertificationClaim: false
```

These reports do not satisfy target-cluster, capacity, real-device Canary or independent human-approval requirements.
