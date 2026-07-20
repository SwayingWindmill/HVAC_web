# S0 reproducible delivery and signed supply chain

## Ownership

Primary Owner: Platform Runtime. Secondary Owner: Developer Experience. Security Platform owns image-signature policy, vulnerability exceptions and staging NetworkPolicy review. Data Platform owns PostgreSQL migration execution and rollback-window approval.

The release engineer owns the deployment receipt, signed image digests, private render bindings and rollback decision. Application teams do not receive the migration identity or cross-service database credentials.

## Local delivery

From the repository root, install the root dependencies, ensure Docker is running, and execute:

```bash
npm run delivery:local
```

This one command validates `deploy/s0/local.env.example`, starts PostgreSQL, Redpanda, OpenTelemetry Collector and Prometheus, generates local-only PKI, then starts the OIDC test provider, IAM, Audit Ledger, Outbox Relay, the repository-owned Go Legacy compatibility fixture, Platform Gateway and HVAC Web. SIGINT or SIGTERM drains the child services and removes the isolated Docker volumes. The local `hvac-backend` directory is migration reference material only; it is excluded from Git, Docker build contexts, CI and release artifacts.

Local and test profiles generate their own Session key and workload certificates. They require no production credential. `ALLOW_PRODUCTION_EGRESS=false`, an empty ThingsBoard URL and an empty webhook URL are mandatory, so the profile cannot intentionally contact production ThingsBoard or production webhooks.

## Configuration contract

`deploy/s0/local.env.example` and `deploy/s0/staging.env.example` are checked-in, credential-free contracts. Validate both with:

```bash
npm run delivery:validate
```

The validator requires an explicit environment, monotonic configuration revision, SPIFFE trust domain, OIDC issuer/client, service audiences, Kafka-compatible broker, PostgreSQL endpoint and OTLP endpoint. URLs may not embed credentials. Checked-in password, token, private-key or secret values are rejected.

Staging templates under `deploy/s0/staging` contain only named placeholders. Private values and workload identity mounts conform to `deploy/s0/staging/bindings.schema.json` and are supplied from the release environment. Render them outside the source template directory:

```bash
npm run delivery:render -- --bindings=/secure/s0-staging-bindings.json --output=out/s0-staging
```

The renderer rejects unknown or missing bindings, refuses mutable image tags, requires every image to end in `@sha256:<digest>`, and fails when any placeholder remains. It writes a receipt containing names and file paths, never binding values. Apply only the rendered output after signature verification.

## Probes and graceful shutdown

Every Go runtime exposes loopback or pod-internal diagnostics:

- `/health/startup` proves the process and diagnostics server started.
- `/health/live` reports process liveness and does not cascade dependency failures.
- `/health/ready` returns HTTP 503 until initialization is complete and immediately returns 503 when drain starts.
- `/metrics` and `/diagnostics` remain independent from public traffic.

Gateway, IAM, Audit Ledger, Outbox Relay and OIDC fixture call `MarkReady` only after configuration and required local resources are initialized. SIGTERM calls `MarkNotReady` before HTTP shutdown or worker cancellation. HTTP servers receive a bounded drain deadline. Relay and Audit consumers stop claiming new work through context cancellation; committed Outbox and Inbox records remain the source of truth, so termination does not acknowledge uncommitted work.

The Go Legacy compatibility fixture uses the same Kubernetes startup, liveness and readiness lifecycle through its private TLS socket and performs bounded signal-driven shutdown. It proves the Gateway-to-Legacy mTLS, delegation and route ownership boundary without packaging the locally retained NestJS migration reference.

## Staging security boundary

The staging namespace enforces the Kubernetes Restricted Pod Security profile. Each workload has a distinct ServiceAccount with API token automount disabled. Containers run non-root, drop all Linux capabilities, deny privilege escalation, use a read-only root filesystem where practical, declare CPU/memory requests and limits, and use bounded termination grace periods.

`default-deny-all` blocks ingress and egress by default. Explicit policies allow DNS, public ingress to Gateway, Gateway-to-IAM/Audit/OIDC/Legacy/PostgreSQL/Collector, and the Audit/Relay data plane to PostgreSQL, Redpanda and the Collector. IAM, Audit and Legacy ingress is restricted to Gateway pods. Legacy has no public Service and remains reachable only through Gateway. Workload TLS identities and trust bundles are injected by the staging identity system during render; private service servers continue to require TLS 1.3 mTLS and the exact Gateway SPIFFE identity.

The checked-in templates are marked `render-before-apply`. They are evidence and a controlled input, not a location for cluster credentials.

## Database migration identity

`000-bootstrap-identities.sql` creates the dedicated `s0_migrator` and service-specific runtime roles. `001-s0-durable.sql` runs with `SET LOCAL ROLE s0_migrator`. Runtime binaries never execute schema migration and do not own schemas. The staging migration Job uses the separate `s0-migrator` ServiceAccount and signed migrator image with `ON_ERROR_STOP=1`.

Run the migration Job before a rollout. Do not grant the migration identity to Gateway, Relay or Audit pods. A failed Job stops the release; application rollout must not compensate by running DDL during startup.

## Signed supply chain

`.github/workflows/s0-supply-chain.yml` runs contract checks, Go tests, the PostgreSQL compatibility suite, frontend type/build, Gitleaks, CodeQL, Go vulnerability analysis, npm dependency audit and production-license checks. Full dependency reports remain visible as CI evidence. The blocking production gate rejects every critical vulnerability and any increase above `deploy/s0/security/dependency-audit-baseline.json`; that reviewed exception expires on 2026-08-15 and is assigned to ticket 07 for remediation rather than becoming a permanent waiver.

Release tags and explicit release dispatches build the same Dockerfiles used for staging. Go binaries, including the Legacy compatibility fixture, are reproducible-oriented static builds with `CGO_ENABLED=0`, `-trimpath` and disabled VCS embedding, then copied into a distroless non-root image. The workflow pushes images to GHCR, emits BuildKit SBOM and maximum provenance attestations, signs each immutable digest with keyless Cosign, verifies the certificate identity and OIDC issuer, and publishes GitHub build provenance.

A staging binding may reference only an image digest that passed `cosign verify`. A mutable tag is never an acceptable deployment input.

## Rolling update and rollback

Gateway, IAM, Audit and Legacy use two replicas with `maxUnavailable: 0`, `maxSurge: 1` and a PodDisruptionBudget with `minAvailable: 1`. Readiness is removed before drain, so endpoints stop routing new work before SIGTERM completes. Relay permits one unavailable worker because Outbox claims expire and another owner can resume them without losing committed work.

The deterministic rollout model is executed with:

```bash
npm run audit:s0-rollout
```

Release procedure:

1. Verify all new digests and provenance, run the migration Job, and retain the previous compatible digest set.
2. Roll out Gateway-facing stateless services first, waiting for ready replicas before draining old replicas.
3. Roll out Relay and Audit workers one at a time and watch Outbox age, consumer lag and Audit ingestion alerts.
4. Run the browser observability audit against staging.
5. To roll back, surge a previous compatible replica and wait for readiness before draining a current replica. Repeat until every workload uses the previous digest.
6. Do not reverse an expand migration during the rollback window.

## Expand-contract compatibility

Schema expansion must remain readable and writable by the previous compatible application version for the declared rollback window. New `traceparent` columns therefore retain a safe empty-string default; current writers populate the value while previous writers may omit it.

`infra/s0-durable/postgres/compatibility/previous-writer.sql` executes previous-style Gateway/Outbox and Audit inserts that omit the new column under their real runtime roles. `npm run test:durable-postgres` runs this transaction before current Go integration tests and rolls it back after proving the defaults. A later contract migration may remove compatibility defaults only after the rollback window closes and no previous binary is deployable.

## Recovery

If local startup fails, run the topology command again; it removes the isolated Compose project and volumes before startup. Inspect PostgreSQL and Redpanda health first, then the service diagnostics ports documented in the README.

If staging migration fails, leave application workloads on the previous digest, collect Job logs, correct only an additive migration and rerun the Job. Never bypass `ON_ERROR_STOP` or grant DDL to a runtime identity.

If a rollout degrades, stop progression, verify readiness and mTLS failures, then follow the rollback sequence above. Outbox or Audit lag must be handled using the S0 observability runbook. A signature or provenance verification failure is a release stop: rebuild from a reviewed commit rather than overriding policy.
