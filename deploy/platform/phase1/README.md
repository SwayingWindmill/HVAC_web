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

The default single-node topology exposes only HTTPS/WSS. MQTT TLS is added only when the optional Integration profile is enabled:

```text
443   HTTPS / WSS -> Nginx -> React + energy-api (/api + /realtime) + identity-service (/identity)
8883  MQTT TLS    -> Mosquitto -> Edge Gateway   [integration profile only]
```

Local PostgreSQL, ClickHouse, Redis, Grafana, Loki, Tempo, Prometheus and all Go services publish no host ports. `identity-service` is Identity Infrastructure, not a business deployable; Nginx exposes only its `/identity` OIDC surface through the existing HTTPS boundary. The application network stays internal; the data network stays internal while state is local and permits outbound egress only when an external state endpoint is selected. Operational access to monitoring/data services must use a controlled management path such as VPN/SSH tunneling rather than additional public ports.

## Runtime inventory

`runtime-inventory.v1.json` is the machine-readable classification of every service in the canonical Compose. The default business deployables are `energy-api`, `telemetry-worker` and `metric-worker`. `scheduler` and `maintenance` are default supporting workloads; `identity-service` is Identity Infrastructure. `iot-service` plus `mqtt-broker` are optional and run only through the `integration` profile. Forecast, Optimization and FDD remain optional through the `intelligence` profile. Migration, schema preflight, identity bootstrap/admin/reconciliation and backup containers are one-shot operator actions rather than long-running product services.

Any Compose service added or removed without updating this inventory is deployment drift and must fail `npm run deployment:phase1:check`.

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

### Stateful placement

PostgreSQL, ClickHouse and Redis placement are independent runtime choices; none changes application roles:

```text
PHASE1_POSTGRES_MODE=local       -> launcher enables local-postgres
PHASE1_POSTGRES_MODE=external    -> no PostgreSQL container; migration/preflight use PHASE1_POSTGRES_HOST/PORT/SSLMODE

PHASE1_CLICKHOUSE_MODE=local     -> launcher enables local-clickhouse
PHASE1_CLICKHOUSE_MODE=external  -> no ClickHouse container; ClickHouse clients use PHASE1_CLICKHOUSE_HTTP_URL

PHASE1_REDIS_MODE=local          -> launcher enables local-redis
PHASE1_REDIS_MODE=external       -> no Redis container; Redis consumers use their configured external endpoints
```

For Stage 2 PostgreSQL, point `PHASE1_POSTGRES_HOST` at the external endpoint and update every application PostgreSQL DSN/URL in the runtime environment. `phase1-schema-preflight` remains mandatory, so the same product/schema contract is checked before application processes start.

For Stage 2 ClickHouse, set `PHASE1_CLICKHOUSE_HTTP_URL` to the protected external HTTP(S) endpoint. The canonical Compose maps that endpoint into Energy Query, telemetry/history projection, analytics projection, metric, forecast and optimization clients. Provision the existing `telemetry_history` / `analytics` schema and equivalent least-privilege service users on the external data plane before rollout. The checked-in local ClickHouse init SQL uses local/internal no-password users and must not be copied unchanged into a network-reachable external deployment; external credentials belong in deployment secrets.

For Stage 2 Redis, keep one Redis placement but preserve the existing logical separation: DB 0/prefixes for realtime and rebuildable Latest projections, DB 1 for short-lived OIDC login state, and DB 2 for shared limit counters. Configure `CENTRIFUGO_ENGINE_REDIS_ADDRESS`, `TELEMETRY_LATEST_CACHE_REDIS_URL`, `METRIC_REDIS_URL`, `OIDC_STATE_REDIS_URL`, and `LIMIT_POLICY_REDIS_URL` for the external endpoint. Telemetry/Metric Latest can be rebuilt; OIDC state and Centrifugo recovery are short-lived coordination state. This placement step does not introduce Redis Sentinel/Cluster or claim Redis HA.

When any state service is external, the launcher opens outbound connectivity on the existing `data` network for that deployment. With PostgreSQL, ClickHouse and Redis all local, `data` remains an internal Docker network. External state is a placement/failure-domain change, not an HA claim.

## Static validation

```bash
npm run architecture:phase1:check
npm run deployment:phase1:check
```

Compose parsing can be verified without starting services. The launcher is the canonical entry because it resolves `PHASE1_DEPLOYMENT_TIER`, applies that tier's CPU/memory and ClickHouse internal-memory limits, and selects the matching observability profile:

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/development.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts config --quiet
```

For the local WSL deployment, use the tracked `wsl.override.yaml` together with the launcher below. The launcher reads the Git-ignored `runtime/db-role-credentials/roles.sql`, builds the least-privilege Identity/IAM DSNs in memory, and passes them only to the Compose process; it does not copy database-role passwords into the runtime env file or print them.

```bash
npm run deployment:phase1:wsl -- config --quiet

# Local source deployment: rebuild the current checkout's Web + first-party runtime images, then start them.
npm run deployment:phase1:wsl -- --source-deploy up -d

# Add local MQTT + iot-service only when this deployment needs Integration.
npm run deployment:phase1:wsl -- --source-deploy --integration up -d
```

Use `--source-deploy` for the local WSL product deployment so the Web bundle and first-party runtime binaries come from the same Git revision. It sets the Web build identity to the current revision before building. `PHASE1_GO_BUILD_IMAGE`, `PHASE1_GO_RUNTIME_IMAGE`, `PHASE1_GO_PROXY`, `PHASE1_WEB_BUILD_IMAGE`, `PHASE1_NGINX_RUNTIME_IMAGE`, and `PHASE1_NPM_REGISTRY` may override build sources for the local network without changing the checked-in production defaults. `PHASE1_ENV_FILE` and `PHASE1_DB_ROLE_CREDENTIALS_SQL` can override the default local runtime env and role-credential SQL paths when needed. `--simulator-acceptance` implies `--integration` because the simulator depends on the local MQTT/iot-service path.

## Startup prerequisites

Before `up -d`, the operator must provide:

- public HTTPS certificate/key under `PUBLIC_TLS_DIR`;
- MQTT CA/broker/client PKI under `MQTT_PKI_DIR` when the `integration` profile is enabled;
- internal service PKI under `INTERNAL_PKI_DIR`;
- a real non-versioned runtime environment file;
- independent PostgreSQL WAL/base-backup and ClickHouse backup locations when those state services are local; external state uses the backup contract of its data plane;
- reviewed application image versions/digests for Staging/Production.

The checked-in examples are contracts, not production secrets.

Staging and Production environment contracts require every first-party application image to be bound to an immutable `@sha256:` digest. After replacing the digest placeholders and completing the migration pre-step, Production rollout must consume the approved images without rebuilding them on the host:

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts pull
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts up -d --no-build
```

## Database migration boundary

The PostgreSQL deployment, whether local or external, creates the authentication database boundary `hvac_identity` plus the existing domain database boundaries `hvac_s0` through `hvac_s5`. Credential hashes and IdP authorization requests/codes stay in `hvac_identity`; Gateway OIDC correlation state is a one-time Redis entry with a 10-minute TTL so login can survive Gateway process restart or multi-instance routing. The browser-facing issuer stays on the public HTTPS origin while Gateway server-to-server discovery, token exchange and JWKS retrieval use `OIDC_BACKCHANNEL_BASE_URL` to reach `identity-service` directly on the internal application network. IAM authorization facts remain in `hvac_s1`. BFF Sessions default to an 8-hour absolute lifetime (`SESSION_ABSOLUTE_TTL=8h`) and a 60-minute user-idle timeout (`SESSION_IDLE_TTL=60m`); only explicit browser user activity refreshes the idle timestamp, so background telemetry traffic cannot keep an unattended session alive.

The production-safe Phase 1 migration runner is implemented under `migrations/`. It uses an exact 75-file allowlist, executes the reviewed canonical migration SQL bytes without runtime rewriting, keeps environment fixture and credential material outside the production sources, records migration state and source hashes in each database, and fails closed on drift or incomplete recovery state. Normal service startup first runs the one-shot Product/Schema preflight, which requires the exact product version and migration-manifest digest; there is no production skip switch.

`limit-policy.v1.json` is the versioned Phase 1 high-risk LimitPolicy. The first enforced class is Operations Agent request usage: the Gateway reserves the per-Session window atomically in Redis and fails closed if that quota authority is unavailable. The policy is mounted read-only and is referenced by `product-release.v1.json`; it is not a generic runtime policy editor.

Database roles are created without checked-in production credentials. Copy `migrations/role-credentials.sql.example` to the Git-ignored runtime path, replace every redacted value with deployment-provided credentials, then execute the migration profile before application rollout:

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts \
  --profile migration run --rm phase1-migrator
```

Use the same launcher for Stage 1 and Stage 2. It derives `local-postgres` and the data-network egress setting from `PHASE1_POSTGRES_MODE`, so operators do not manually assemble placement profiles.

`testdata`, fixture bootstrap, legacy migration execution and local-only identity bootstrap are not in the production allowlist. `npm run deployment:phase1:migration:test` performs a fresh isolated PostgreSQL run, executes the migration set twice to prove idempotency, validates migration hashes, forced RLS and database CONNECT boundaries, and verifies that Command Dispatcher receives no database role.

## Identity bootstrap

Identity bootstrap is an explicit operator action after database migration. It is not an application startup side effect and it never seeds Tenant/Site/Role authorization into the IdP.

1. Run the `identity-keygen` service from the `identity-bootstrap` profile once to create the non-versioned PKCS#8 RSA signing key under `IDENTITY_RUNTIME_DIR`. Existing key files are not overwritten.
2. Run `identity-admin` with `IDENTITY_ADMIN_DATABASE_URL` bound to the dedicated `identity_admin` database role. `create` provisions a credential-bearing identity; `reset-password` and `reset-password-random` are explicit offline recovery operations.
3. For a fresh local WSL deployment, run `npm run deployment:phase1:local-foundation` once. It idempotently creates only the reviewed local Tenant/Site plus the Registry, Telemetry, Alarm and Work Order base policies required by the IAM resolvers; it does not create simulated equipment.
4. Run `identity-reconciler` with a reviewed input document to project that identity's immutable `issuer + subject` into IAM together with explicit Tenant membership and approved Role/Site facts.
5. For the local WSL administrator account, run `npm run deployment:phase1:local-admin` after reconciliation. This idempotent local-only step uses `s1_iam_migrator` only for the additional Telemetry scope, Alarm and Work Order permissions that are not represented by the reconciliation document. It does not grant authorization in the IdP.
6. Start or restart `identity-service`. The service fails closed if its signing key file is missing or invalid.

`identity_runtime` cannot insert users. The separate `identity_admin` role exists so credential administration does not expand the long-running IdP runtime privilege set. Signing private keys, bootstrap credentials and reconciliation input containing deployment-specific authorization facts remain outside Git.

## Local central-plant simulator

WSL development does not seed simulated equipment as an application startup side effect. After the normal Phase 1 stack is up, run `npm run deployment:phase1:simulator` to idempotently bootstrap the local central-plant Registry/S2 projection, Connectivity Integration/Binding/Credential/Session state, and IAM telemetry-key grants, then start the explicit `deploy/acceptance/phase1-simulator.compose.yaml` overlay with the `simulator-acceptance` profile. The EG8200 simulator is absent from the canonical production Compose graph. The Connectivity seed stores the simulator certificate fingerprint and a SecretRef only; it never writes the private key to PostgreSQL. This acceptance-only bootstrap uses the deployment PostgreSQL administrator for Registry/Connectivity fixture writes because forced RLS remains enabled and application/migrator roles are not granted a bypass; production deployments do not run this command.

## Backup

Local PostgreSQL continuously archives WAL and exposes a `backup` profile for base backups. For external PostgreSQL, backup ownership follows that external database deployment; the repository backup container may be used only when that PostgreSQL endpoint permits `pg_basebackup` with the configured credentials. Local ClickHouse exposes the existing native backup profile; external ClickHouse backup/PITR ownership belongs to that external data plane. Details are in `backup/README.md`.

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile local-postgres --profile backup run --rm postgres-backup
docker compose -f deploy/platform/phase1/compose.yaml --profile local-clickhouse --profile backup run --rm clickhouse-backup
```

`SE-OPS-009 V1.0 CURRENT CANDIDATE` now defines the Phase 1 recovery objectives and `recovery/` contains the machine target contract, whole-server runbook and drill evidence format. Backup configuration does not itself prove attainment: each production deployment must still run a timestamped disaster-recovery drill with real backup artifacts.

## Application Scheduler

`SE-PLATFORM-006 V1.0 CURRENT CANDIDATE` is implemented as an independent `scheduler` process backed by PostgreSQL durable state. The authoritative chain is `schedule_definitions -> job_instances -> job_attempts`; Scheduler owns timing, deduplication, Misfire/Retry coordination and expired-Lease recovery, while Domain Workers own business execution.

`metric-worker` no longer scans Metric bindings to decide when work should run. It claims `METRIC_WINDOW_CALC`, `METRIC_RECALC` and `METRIC_BACKFILL` jobs from PostgreSQL with `FOR UPDATE SKIP LOCKED`, records Attempts, renews Leases and uses the existing Metric Engine for calculation and persistence. Backfill claim execution is bounded to one concurrent job on the single-server baseline.

Scheduler management HTTP routes remain `DESIGN_PROPOSED` in the source design. Until those URIs enter the OpenAPI contract, an operator can provision a reviewed schedule with `scheduler/schedule-definition.sql.example`; this does not make direct PostgreSQL access a public application API. PostgreSQL/ClickHouse backup scheduling remains outside the Application Scheduler and continues to use the infrastructure backup mechanism.

## Deployment tiers and observability profiles

Phase 1 keeps one Compose file with explicit profiles instead of one all-inclusive default. The machine contract is `deployment-tiers.v1.json`.

```text
observability-core  = Prometheus + node-exporter + Grafana
observability-logs  = core + OTel Collector + Loki
observability-full  = logs + Tempo
local-postgres      = Stage 1 local PostgreSQL placement
local-clickhouse    = Stage 1 local ClickHouse placement
local-redis         = Stage 1 local Redis placement
integration         = iot-service + mqtt-broker
intelligence        = forecast-service + optimization-service + fdd-service
```

Select the deployment tier in the runtime environment; do not pass an observability profile separately:

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/production.runtime.env \
  node --experimental-strip-types scripts/phase1-compose.ts up -d --no-build
```

`PHASE1_DEPLOYMENT_TIER` is authoritative. `PHASE1_OBSERVABILITY_PROFILE` must match the tier contract and controls the OTel Collector pipeline (`observability/otel-collector/{core,logs,full}.yaml`). Both launchers validate and inject the selected resource/profile contract. The tier also caps ClickHouse's internal allocator below the container memory limit.

The recommended production baselines:

| Tier | Observability profile | Notes |
|---|---|---|
| `single-lite` | `observability-logs` | 8C/16G target; centralized metrics + logs, no centralized traces |
| `single-full` | `observability-full` | 16C/32G target; adds single-instance Tempo |

`observability-core` is the minimal Tier 0/demo form. The current Compose no longer starts Tempo or the intelligence services by default; they are profile-gated.

## Observability

The Phase 1 Monitoring Platform is deliberately single-instance and tiered:

```text
observability-core
  Metrics -> Prometheus -> Grafana
  Host    -> node-exporter -> Prometheus

observability-logs
  + Docker JSON logs -> OTel Collector -> Loki -> Grafana

observability-full
  + OTLP traces -> OTel Collector -> Tempo -> Grafana
```

All application metrics are scraped directly from each Go diagnostics endpoint by Prometheus. The OTel Collector is required only for centralized logs/traces, so removing it in `observability-core` does not remove alerting or dashboards. No multi-node observability cluster is introduced in Phase 1.

## Single-server storage and resource boundary

The canonical Compose keeps PostgreSQL, ClickHouse, Redis and MQTT data on configurable host paths (`POSTGRES_DATA_DIR`, `CLICKHOUSE_DATA_DIR`, `REDIS_DATA_DIR`, `MQTT_DATA_DIR`) instead of opaque Docker named volumes. Production should bind these paths to the planned server data disk, and should place PostgreSQL WAL/base backups plus ClickHouse backups on storage independent from the primary data directories.

All long-running canonical containers have bounded CPU/memory settings and Docker JSON log rotation. `node-exporter` supplies host CPU/memory/disk/load/network metrics to Prometheus; disk usage alerts fire at the deployment baseline thresholds of 80% warning and 90% critical.

Kafka/Redpanda is not present in the canonical Phase 1 Compose. Any historical Kafka compatibility assets elsewhere in the repository are non-canonical future/certification references.

## Availability and recovery evidence

Phase 1 is `SINGLE_NODE_RECOVERABLE`, not HA. `availability-tier.v1.json` is the machine contract that:

- enumerates PostgreSQL/ClickHouse/Redis/MQTT/Centrifugo/observability single points of failure;
- records each component upgrade path and trigger condition; and
- defines the Stage 0 -> Stage 3 monolith-to-multi-instance path that follows the ThingsBoard `service.type` topology-switch pattern while keeping PostgreSQL Outbox/Job leases as the durable backbone.

The frozen RPO/RTO values remain recovery objectives. `recovery/attainment.v1.json` defaults to `TARGET_DEFINED` with `productionClaimAllowed=false`. A production deployment may claim the `SE-OPS-009` targets only after a real timestamped restore drill verifies the external backup, recovery hardware/path, actual PostgreSQL recovery point, component restoration times, Control reconciliation, Scheduler/Outbox recovery and final business validation. A local container-only test is insufficient, and numeric availability SLOs must cite the availability tier and measured evidence.

After verifying a real whole-server drill record, generate and validate the time-bounded attainment claim explicitly:

```bash
npm run deployment:phase1:recovery:verify -- --file=/evidence/drill-record.json --attainment-output=/evidence/attainment.json
npm run deployment:phase1:recovery:claim:check -- --file=/evidence/attainment.json --drill-record=/evidence/drill-record.json
```

The staging process-failure drill is separate from disaster recovery. It kills and restarts each critical container, checks its recovery bound and reruns the critical smoke set, but always records `productionClaimAllowed=false`.

Stage 1 owner separation follows ThingsBoard's explicit topology-role selection pattern without adopting its in-memory queue or Kafka/actor runtime. It runs the existing same-version owner binaries and leaves only Notification embedded in `energy-api`. It requires the `single-full` tier:

```bash
PHASE1_ENV_FILE=deploy/platform/phase1/environments/staging.runtime.env \
  PHASE1_DEPLOYMENT_TIER=single-full \
  node --experimental-strip-types scripts/phase1-compose.ts --owner-split config --quiet

PHASE1_ENV_FILE=deploy/platform/phase1/environments/staging.runtime.env \
  npm run deployment:phase1:owner-split:drill -- \
  --confirm-staging-owner-split --output=/evidence/owner-split.json
```

Configuration success is not certification: Stage 1 stays `implemented-runtime-drill-required` until the staging live-contract drill passes. The operator must copy `owner-split-release.v1.json.example` and `owner-split-live-journeys.v1.json.example` into the Git-ignored runtime directory, replace every placeholder, bind the complete `energy-api` + Owner digest set to one product/source revision, set the approved manifest SHA-256, and provide authenticated live API journeys covering every extracted Owner. The evidence records no authorization headers or response bodies.

Optimization is optional in `SE-ARCH-DEPLOY-001 V1.0 CURRENT` and remains deferred until a deployment actually requires it.
