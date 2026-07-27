# S3 Local Integration Profile

This profile runs the S3 command runtime on a single-node kind cluster for local smoke and integration testing.

It is deliberately separate from `deploy/s3/target/` and must never be used as formal S3-09 certification evidence. A successful run records `local-integration-passed` with `formalCertificationClaim: false`.

## Prerequisites

- Docker Desktop with the Docker Engine running
- `kubectl`
- `curl`
- OpenSSL
- `sha256sum`

The launcher downloads a repository-local kind binary to `out/tools/kind` and verifies its pinned SHA-256. No system-wide kind or Helm installation is required.

## Commands

```bash
bash scripts/s3-local.sh up
bash scripts/s3-local.sh status
bash scripts/s3-local.sh smoke
bash scripts/s3-local.sh logs
bash scripts/s3-local.sh down
```

The `up` command performs these stages:

1. Generates a disposable local CA, workload certificates, provider value, database reference and approved test cohort under `out/s3-local/`.
2. Builds six local-only images.
3. Creates or reuses the `hvac-s3-local` kind cluster.
4. Loads images into the kind node.
5. Deploys PostgreSQL and applies the production-style S3 role/RLS migrations.
6. Deploys Command Service, Dispatcher, Verifier and the local device simulator.
7. Submits a uniquely idempotent 24°C command and waits for `SUCCEEDED|VERIFIED`.

## Local simulator

The simulator exposes two internal TLS endpoints:

- A ThingsBoard CE compatible two-way RPC endpoint used by Command Dispatcher.
- An mTLS S2 Reported State endpoint restricted to the Command Verifier SPIFFE identity.

It updates an in-memory setpoint on an accepted RPC and returns a later reported-state observation with an incremented business revision. This exercises the real Dispatcher, connector-evidence and Verifier code paths without downloading a complete ThingsBoard deployment.

## Security boundary

- All Services are `ClusterIP`.
- No Ingress, NodePort, LoadBalancer, host port or public route is created.
- Local application images use `imagePullPolicy: Never`.
- Private keys and generated values stay under ignored `out/s3-local/` paths.
- Kubernetes Secret manifests rendered by `kubectl create ... --dry-run=client` also stay under `out/s3-local/rendered/`.
- The fixed local database passwords are test-only and are not accepted by the target profile.

## Evidence

After a successful smoke run:

```text
out/s3-local/smoke-report.json
```

The report is local integration evidence only. It does not satisfy target-cluster, capacity, real-device Canary or independent-approval requirements.
