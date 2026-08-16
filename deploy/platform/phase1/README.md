# Phase 1 canonical deployment

`deploy/platform/phase1` is the canonical deployment entry for `SE-ARCH-DEPLOY-001 V1.0 CURRENT`《智慧能源系统总体部署架构设计 V1（单服务器基线）》.

The Phase 1 runtime model is:

```text
1 Linux Server
+
Docker Compose
```

Kubernetes/Kustomize assets elsewhere in the repository are future-stage or certification references. They are not required to run or accept Phase 1.

## Public boundary

Only two host-facing ports are part of the canonical topology:

```text
443   HTTPS / WSS -> Nginx -> React + energy-api (/api + /realtime) + identity-service (/identity)
8883  MQTT TLS    -> Mosquitto -> Edge Gateway
```

PostgreSQL, ClickHouse, Redis, Grafana, Loki, Tempo, Prometheus and all Go services stay on internal Compose networks. `identity-service` is Identity Infrastructure, not a business deployable; Nginx exposes only its `/identity` OIDC surface through the existing HTTPS boundary. Operational access to monitoring/data services must use a controlled management path such as VPN/SSH tunneling rather than additional public ports.

## Environment contract

Checked-in examples exist for:

```text
environments/development.runtime.env.example
environments/testing.runtime.env.example
environments/staging.runtime.env.example
environments/production.runtime.env.example
```

Copy the required example to a non-versioned `*.runtime.env`, replace every `[REDACTED_SECRET]`, and point `PHASE1_ENV_FILE` at it. Real runtime env files and `deploy/platform/phase1/runtime/` are ignored by Git.

Development/Testing/Staging remain fail-closed for production egress. Production examples must never contain Demo/fixture/test identity endpoints.

## Static validation

```bash
npm run architecture:phase1:check
npm run deployment:phase1:check
```

Compose parsing can be verified without starting services:

```bash
docker compose \
  --env-file deploy/platform/phase1/environments/development.runtime.env \
  -f deploy/platform/phase1/compose.yaml \
  config --quiet
```

## Startup prerequisites

Before `up -d`, the operator must provide:

- public HTTPS certificate/key under `PUBLIC_TLS_DIR`;
- MQTT CA/broker/client PKI under `MQTT_PKI_DIR`;
- internal service PKI under `INTERNAL_PKI_DIR`;
- a real non-versioned runtime environment file;
- independent PostgreSQL WAL/base-backup and ClickHouse backup locations;
- reviewed application image versions/digests for Staging/Production.

The checked-in examples are contracts, not production secrets.

Staging and Production environment contracts require every first-party application image to be bound to an immutable `@sha256:` digest. After replacing the digest placeholders and completing the migration pre-step, Production rollout must consume the approved images without rebuilding them on the host:

```bash
docker compose \
  --env-file deploy/platform/phase1/environments/production.runtime.env \
  -f deploy/platform/phase1/compose.yaml \
  pull

docker compose \
  --env-file deploy/platform/phase1/environments/production.runtime.env \
  -f deploy/platform/phase1/compose.yaml \
  up -d --no-build
```

## Database migration boundary

The single PostgreSQL server creates the authentication database boundary `hvac_identity` plus the existing domain database boundaries `hvac_s0` through `hvac_s5`. Credential hashes and transient OIDC authorization state stay in `hvac_identity`; IAM authorization facts remain in `hvac_s1`.

The production-safe S0-S5 migration runner is implemented under `migrations/`. It uses an exact 44-file allowlist, reuses the canonical domain migration SQL, strips historical local-only credential lines and environment fixture seed blocks from the production execution stream, records the hash of the executed SQL in each database, and fails closed on migration drift.

Database roles are created without checked-in production credentials. Copy `migrations/role-credentials.sql.example` to the Git-ignored runtime path, replace every redacted value with deployment-provided credentials, then execute the migration profile before application rollout:

```bash
docker compose \
  --env-file deploy/platform/phase1/environments/production.runtime.env \
  -f deploy/platform/phase1/compose.yaml \
  --profile migration run --rm phase1-migrator
```

`testdata`, fixture bootstrap, legacy migration execution and local-only identity bootstrap are not in the production allowlist. `npm run deployment:phase1:migration:test` performs a fresh isolated PostgreSQL run, executes the migration set twice to prove idempotency, validates migration hashes, forced RLS and database CONNECT boundaries, and verifies that Command Dispatcher receives no database role.

## Identity bootstrap

Identity bootstrap is an explicit operator action after database migration. It is not an application startup side effect and it never seeds Tenant/Site/Role authorization into the IdP.

1. Run the `identity-keygen` service from the `identity-bootstrap` profile once to create the non-versioned PKCS#8 RSA signing key under `IDENTITY_RUNTIME_DIR`. Existing key files are not overwritten.
2. Run `identity-admin` with `IDENTITY_ADMIN_DATABASE_URL` bound to the dedicated `identity_admin` database role. `create` provisions a credential-bearing identity; `reset-password` and `reset-password-random` are explicit offline recovery operations.
3. Run `identity-reconciler` with a reviewed input document to project that identity's immutable `issuer + subject` into IAM together with explicit Tenant membership and approved Role/Site facts.
4. Start or restart `identity-service`. The service fails closed if its signing key file is missing or invalid.

`identity_runtime` cannot insert users. The separate `identity_admin` role exists so credential administration does not expand the long-running IdP runtime privilege set. Signing private keys, bootstrap credentials and reconciliation input containing deployment-specific authorization facts remain outside Git.

## Backup

PostgreSQL continuously archives WAL and exposes a `backup` profile for base backups. ClickHouse exposes a native backup profile. Details are in `backup/README.md`.

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm postgres-backup
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm clickhouse-backup
```

`SE-OPS-009 V1.0 CURRENT CANDIDATE` now defines the Phase 1 recovery objectives and `recovery/` contains the machine target contract, whole-server runbook and drill evidence format. Backup configuration does not itself prove attainment: each production deployment must still run a timestamped disaster-recovery drill with real backup artifacts.

## Application Scheduler

`SE-PLATFORM-006 V1.0 CURRENT CANDIDATE` is implemented as an independent `scheduler` process backed by PostgreSQL durable state. The authoritative chain is `schedule_definitions -> job_instances -> job_attempts`; Scheduler owns timing, deduplication, Misfire/Retry coordination and expired-Lease recovery, while Domain Workers own business execution.

`metric-worker` no longer scans Metric bindings to decide when work should run. It claims `METRIC_WINDOW_CALC`, `METRIC_RECALC` and `METRIC_BACKFILL` jobs from PostgreSQL with `FOR UPDATE SKIP LOCKED`, records Attempts, renews Leases and uses the existing Metric Engine for calculation and persistence. Backfill claim execution is bounded to one concurrent job on the single-server baseline.

Scheduler management HTTP routes remain `DESIGN_PROPOSED` in the source design. Until those URIs enter the OpenAPI contract, an operator can provision a reviewed schedule with `scheduler/schedule-definition.sql.example`; this does not make direct PostgreSQL access a public application API. PostgreSQL/ClickHouse backup scheduling remains outside the Application Scheduler and continues to use the infrastructure backup mechanism.

## Observability

The Phase 1 Monitoring Platform is deliberately single-instance:

```text
OTel Collector
├─ Metrics -> Prometheus
├─ Logs    -> Loki
└─ Traces  -> Tempo
              ↓
           Grafana
```

This follows the document requirement without introducing a multi-node observability cluster in Phase 1.

## Single-server storage and resource boundary

The canonical Compose keeps PostgreSQL, ClickHouse, Redis and MQTT data on configurable host paths (`POSTGRES_DATA_DIR`, `CLICKHOUSE_DATA_DIR`, `REDIS_DATA_DIR`, `MQTT_DATA_DIR`) instead of opaque Docker named volumes. Production should bind these paths to the planned server data disk, and should place PostgreSQL WAL/base backups plus ClickHouse backups on storage independent from the primary data directories.

All long-running canonical containers have bounded CPU/memory settings and Docker JSON log rotation. `node-exporter` supplies host CPU/memory/disk/load/network metrics to Prometheus; disk usage alerts fire at the deployment baseline thresholds of 80% warning and 90% critical.

Kafka/Redpanda is not present in the canonical Phase 1 Compose. Any historical Kafka compatibility assets elsewhere in the repository are non-canonical future/certification references.

## Remaining production evidence

The Phase 1 alignment matrix has no architecture item left in `MISSING`: Scheduler coordination and the RPO/RTO target definition are both represented by explicit machine contracts and runbooks.

This does not waive site evidence. A production deployment may claim the `SE-OPS-009` targets only after a real timestamped restore drill verifies the external backup, recovery hardware/path, actual PostgreSQL recovery point, component restoration times, Control reconciliation, Scheduler/Outbox recovery and final business validation. A local container-only test is insufficient.

Optimization is optional in `SE-ARCH-DEPLOY-001 V1.0 CURRENT` and remains deferred until a deployment actually requires it.
