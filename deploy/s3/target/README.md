# S3 Target Runtime deployment

This directory is the render-before-apply deployment template for the S3 Command target environment.

It deliberately does not create an Ingress, LoadBalancer or NodePort. The Command endpoint is internal only:

```text
https://command-service.s3-certification.svc.cluster.local:8447
```

The previous preflight SHA `784a15ad2bbf2ae47196ee8ca7e54fd45c96c58d` does not contain this runtime. After these changes are reviewed and committed, freeze the new commit SHA and use that SHA for target capacity certification and the real Device Canary.

## External platform prerequisites

The platform team must provide all of the following before rendering:

- A trusted Kubernetes context for the approved target-equivalent cluster.
- Namespace labels used by `networkpolicies.yaml`:
  - Gateway namespace: `s3.hvac/gateway-client=true`
  - PostgreSQL namespace: `data.hvac/postgres=true`
  - ThingsBoard namespace: `s3.hvac/thingsboard-control=true`
  - S2 runtime namespace: `s2.hvac/runtime=true`
  - OTEL namespace: `observability.hvac/collector=true` and `observability.hvac/scraper=true`
- cert-manager CSI and a private Issuer/ClusterIssuer that can issue short-lived X.509 certificates with these URI SANs:
  - `spiffe://hvac.local/command-service`
  - `spiffe://hvac.local/command-dispatcher`
  - `spiffe://hvac.local/command-verifier`
- cert-manager CSI must use TokenRequest identity and cert-manager approver-policy, or an equivalent admission/approval control. Each of the three ServiceAccounts must be restricted to its exact Issuer, URI SAN, DNS SAN set, key usages and maximum 24-hour duration. Do not grant a cluster-wide wildcard CertificateRequest approval.
- The command-service certificate must also contain the DNS SAN `command-service.s3-certification.svc.cluster.local`.
- The S2 Telemetry Runtime client CA bundle must trust the Issuer used by `command-verifier`; no unrelated public or tenant CA may be added to that trust bundle.
- Cloud Workload Identity for access to the external provider credential store; this is separate from the mTLS certificate identity.
- A Secrets Store CSI provider capable of mounting one ThingsBoard provider credential as a file. The credential value must not appear in Git, ConfigMaps, environment-variable literals, logs or certification attestations.
- If SPIRE Workload API is selected instead of cert-manager CSI, the Go binaries must first be changed to consume the Workload API socket, or a trusted materializer must expose compatible certificate files. The checked-in templates intentionally match the current file-based TLS loaders.
- A target PostgreSQL database with separate migration and runtime principals.
- One approved Organization, Site and internal Device, all represented by canonical UUIDv7 identifiers.
- Immutable S2 production-certification evidence and ThingsBoard mapping/binding evidence for the exact Device.

## Build immutable images

Use the repository generic Go image and bind every image to a digest:

```bash
# Command Service
docker build -f deploy/s0/images/go-service.Dockerfile \
  --build-arg SERVICE_PACKAGE=./services/command-service/cmd/command-service \
  -t <registry>/command-service:<new-sha> .

# Dispatcher
docker build -f deploy/s0/images/go-service.Dockerfile \
  --build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-dispatcher \
  -t <registry>/command-dispatcher:<new-sha> .

# Verifier; the command is part of the command-dispatcher Go module
docker build -f deploy/s0/images/go-service.Dockerfile \
  --build-arg SERVICE_PACKAGE=./services/command-dispatcher/cmd/command-verifier \
  -t <registry>/command-verifier:<new-sha> .

docker build -f deploy/s3/images/command-migrator.Dockerfile \
  -t <registry>/command-migrator:<new-sha> .
```

Local Docker builds are verification-only. Formal target images must be published by `.github/workflows/s3-ticket-09.yml` from an `s3-v*` tag or explicit workflow dispatch. The workflow builds with BuildKit SBOM and maximum provenance, scans for embedded secrets, signs and verifies each digest with Cosign OIDC, then publishes the `s3-target-image-manifest` artifact.

Resolve and record each pushed image digest from that aggregated manifest. Do not render mutable tags such as `latest`, local image IDs or unverified registry references.

## Render the target package

Every bracketed field is mandatory. Rendering must replace every placeholder, including the Secrets Store CSI driver and its standard class-reference attribute. The source template cannot spell that attribute directly because the repository credential scanner deliberately blocks secret-looking field names.

Required rendering inputs include:

- Image digests.
- cert-manager CSI Issuer name, kind and API group; the driver mounts `tls.crt` and `tls.key` with the exact URI/DNS SANs declared in each workload.
- Immutable certificate approval-policy references for Command Service, Dispatcher and Verifier ServiceAccounts.
- PostgreSQL runtime DSN projected as `/var/run/hvac/database/command-service-dsn`; it must not be stored in a Deployment environment variable.
- PostgreSQL migrator service configuration projected as `/var/run/hvac/database/pg_service.conf`, containing the `s3-command-migrator` service section selected by `PGSERVICE`.
- Cloud Workload Identity annotations.
- `[SECRETS_STORE_CSI_DRIVER]` rendered to the installed standard Secrets Store CSI driver.
- `[SECRETS_STORE_CLASS_ATTRIBUTE]` rendered to the standard Secrets Store CSI class-reference attribute.
- Provider-specific object rendering that creates exactly these files: ThingsBoard `credential`, Command Service `command-service-dsn`, and migrator `pg_service.conf`. Projected files must be readable by only the declared workload group (`65532` for runtime Pods and `999` for the migrator); do not rely on world-readable modes.
- Exact approved Cohort UUIDv7 identifiers.
- ThingsBoard Integration ID, external Device ID, Binding Revision and Mapping Revision.
- Approval ticket and immutable evidence references.
- Current Command Policy and emergency revocation revisions.

After rendering, fail if any `[` placeholder remains:

```bash
rg '\[[A-Z0-9_]+\]' <rendered-directory>
```

The command must return no matches.

## Validate before applying

```bash
npm run s3:target-runtime:check
kubectl kustomize deploy/s3/target >/tmp/s3-target-template.yaml
```

`kubectl kustomize` verifies the source template structure only. It does not make an unrendered template deployable.

Verify the operator context without reading Secret values:

```bash
node scripts/check-s3-target-context.mjs \
  --context=<approved-context> \
  --namespace=s3-certification
```

The operator identity must be able to inspect Deployments, Services, EndpointSlices and logs, but should not be able to read arbitrary Kubernetes Secrets.

## Deployment order

1. Create and label all dependency namespaces.
2. Install cert-manager CSI and configure the approved private Issuer/ClusterIssuer used by the three mTLS identities.
3. Configure cloud Workload Identity, install Secrets Store CSI and render all three classes in `secret-provider-class.yaml`: ThingsBoard credential, Command Service runtime database and migration database.
4. Render and apply the Namespace, ServiceAccounts, immutable ConfigMaps, Service, PDBs and NetworkPolicies.
5. Build and run `s3-command-migration`; retain Job logs and image digest.
6. Deploy Command Service and wait for all three replicas to become Ready.
7. Patch and roll out S2 Telemetry Runtime with the exact Cohort configuration.
8. Deploy Command Verifier and confirm it can read only the zero-parameter S2 reported-state endpoint.
9. Deploy Command Dispatcher last. Until this step, no target Device side effect can occur.
10. Run the Context readiness checker again and capture its non-secret report.

## Security invariants

- Command Service is the only long-running S3 workload with PostgreSQL access.
- Dispatcher and Verifier have no PostgreSQL connection string and no PostgreSQL network egress.
- Only Dispatcher can reach ThingsBoard.
- Only Verifier can reach the S2 reported-state endpoint.
- The Cohort file contains exactly one Organization, one Site and one Device.
- Mapping status must be `VERIFIED`; `LOCAL_VERIFIED` is rejected.
- Provider credentials are read from a mounted file. Logs and attestations contain only `workload://` or `secret://` references.
- A ThingsBoard RPC acknowledgement is not business success. A Command becomes verified only after S2 reports a newer authoritative Business Revision with the requested setpoint.
- Public Command routes remain disabled and production traffic remains 0% until the formal certification policy authorizes a later rollout.

## Certification boundary

Deployment readiness is not formal S3-09 completion. Formal certification still requires target load evidence, all required crash points, ten zero counters, 6–12 operator-confirmed real Device commands, at least 240 real elapsed minutes, rollback evidence and two distinct post-canary approvals.
