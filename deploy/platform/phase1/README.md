# Phase 1 canonical deployment

`deploy/platform/phase1` is the canonical deployment entry for the first stage defined by `架构规划/智慧能源系统部署与运维架构设计.md`.

The Phase 1 runtime model is:

```text
Linux Server(s)
+
Docker Compose
```

Kubernetes/Kustomize assets elsewhere in the repository are future-stage or certification references. They are not required to run or accept Phase 1.

## Public boundary

Only two host-facing ports are part of the canonical topology:

```text
443   HTTPS / WSS -> Nginx -> React + Platform Gateway / Centrifugo
8883  MQTT TLS    -> Mosquitto -> Edge Gateway
```

PostgreSQL, ClickHouse, Redis, Grafana, Loki, Tempo, Prometheus and all Go services stay on internal Compose networks. Operational access to monitoring/data services must use a controlled management path such as VPN/SSH tunneling rather than additional public ports.

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

The single PostgreSQL server creates the existing domain database boundaries `hvac_s0` through `hvac_s5` so current domain migrations do not need to be rewritten around new database names.

The production-safe S0-S5 migration runner is implemented under `migrations/`. It uses an exact 34-file allowlist, reuses the canonical domain migration SQL, strips historical local-only credential lines and environment fixture seed blocks from the production execution stream, records the hash of the executed SQL in each database, and fails closed on migration drift.

Database roles are created without checked-in production credentials. Copy `migrations/role-credentials.sql.example` to the Git-ignored runtime path, replace every redacted value with deployment-provided credentials, then execute the migration profile before application rollout:

```bash
docker compose \
  --env-file deploy/platform/phase1/environments/production.runtime.env \
  -f deploy/platform/phase1/compose.yaml \
  --profile migration run --rm phase1-migrator
```

`testdata`, fixture bootstrap, legacy migration execution and local-only identity bootstrap are not in the production allowlist. `npm run deployment:phase1:migration:test` performs a fresh isolated PostgreSQL run, executes the migration set twice to prove idempotency, validates migration hashes, forced RLS and database CONNECT boundaries, and verifies that Command Dispatcher receives no database role.

## Backup

PostgreSQL continuously archives WAL and exposes a `backup` profile for base backups. ClickHouse exposes a native backup profile. Details are in `backup/README.md`.

```bash
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm postgres-backup
docker compose -f deploy/platform/phase1/compose.yaml --profile backup run --rm clickhouse-backup
```

Backup configuration does not itself prove RPO/RTO. A timestamped disaster-recovery drill remains a formal acceptance requirement.

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

## Optional compatibility backbone

`redpanda-compat` and `outbox-relay-compat` are only enabled with:

```bash
--profile compat-event-backbone
```

Kafka/Redpanda is therefore not a Phase 1 platform prerequisite. It can be reevaluated in the later architecture phase described by the deployment design.

## Remaining explicit gaps

The alignment matrix intentionally keeps these as `MISSING`:

- formal measured RPO/RTO drill;
- real Optimization Service runtime.

Do not close those items with placeholders or synthetic attestations.
