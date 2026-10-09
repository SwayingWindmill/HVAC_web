# Energy Processing Domain Module

`modules/energy` owns the Energy Processing write boundary for the current
Energy slice. Phase 1 composes its projector package into `cmd/telemetry-worker`; `cmd/energy-projector` is an explicit standalone build/integration entrypoint, not an additional default deployable:

```text
telemetry_history.counter_deltas
    -> Core MeterBinding resolver
    -> analytics.energy_interval_facts
```

The projector does not calculate counter lag, reset, rollover or recovery
semantics in Go. Those rules belong to the canonical ClickHouse view. It
accepts only a uniquely resolved RELEASED/ACTIVE PRIMARY COUNTER binding of
any energy type; a counter without such a binding stops projection with an
explicit error.

For each accepted canonical delta, the projector writes one interval whose
energy is exactly `delta_value`. `GOOD`, `PARTIAL`/`ESTIMATED`/`MANUAL`, and
`STALE`/`INVALID`/unknown raw qualities map to `VALID`, `SUSPECT`, and
`INVALID`. NULL deltas and invalid decreases are excluded by the canonical
view; they are never written as zero-energy facts.

Facts are identified by the current observation ID. The logical idempotency
key is `(tenant_id, site_id, meter_binding_id, source_current_observation_id)`.
Initial facts use `fact_revision=0`. Late-arrival corrections increment the prior revision, attach a `rebuild_run_id`, and persist
RUN_STARTED/RUN_PERSISTED_CHUNK/RUN_COMPLETED or RUN_FAILED rebuild evidence.

The Core resolver is a private mTLS route and requires a short-lived Registry
grant with the dedicated `meter-binding.resolve` permission. The projector is
its own Workload Principal (ADR 0017): it asks IAM for one grant per Tenant over
mTLS and renews it before it expires.

The `counter_deltas` view uses ClickHouse `SQL SECURITY DEFINER`; the projector
reader can query the canonical view without receiving direct raw-observation
access.

## Data ownership

| Dataset | Access |
|---|---|
| `telemetry_history.counter_deltas` | Projector read only |
| `analytics.energy_interval_facts` | Projector insert and idempotency read |
| Core `meter_bindings` | Core-owned resolver read |

## Environment

Required:

| Variable | Purpose |
|---|---|
| `ANALYTICS_CLICKHOUSE_HTTP_URL` | ClickHouse HTTP origin |
| `ANALYTICS_CORE_REGISTRY_URL` | Platform Core HTTP origin |
| `ANALYTICS_IAM_URL` | IAM HTTP origin that issues the projector's Registry grants |
| `ANALYTICS_INTERNAL_CA` | Internal CA bundle for Core and IAM |
| `ANALYTICS_INTERNAL_TLS_CERT` | Projector mTLS certificate (its Workload Principal identity) |
| `ANALYTICS_INTERNAL_TLS_KEY` | Projector mTLS private key |

Optional defaults:

| Variable | Default |
|---|---|
| `ANALYTICS_SOURCE_DATABASE` | `telemetry_history` |
| `ANALYTICS_SOURCE_TABLE` | `counter_deltas` |
| `ANALYTICS_DATABASE` | `analytics` |
| `ANALYTICS_ENERGY_TABLE` | `energy_interval_facts` |
| `ANALYTICS_CLICKHOUSE_READER_USERNAME` | Empty |
| `ANALYTICS_CLICKHOUSE_READER_PASSWORD` | Empty |
| `ANALYTICS_CLICKHOUSE_WRITER_USERNAME` | Empty |
| `ANALYTICS_CLICKHOUSE_WRITER_PASSWORD` | Empty |
| `ANALYTICS_CLICKHOUSE_CA` | System trust store |
| `ANALYTICS_PROJECTOR_BATCH_SIZE` | `256` |
| `ANALYTICS_PROJECTOR_POLL_INTERVAL` | `500ms` |
| `ANALYTICS_PROJECTOR_DIAGNOSTICS_ADDR` | `127.0.0.1:19089` |

## Verification

```bash
npm run test:analytics
npm run analytics:history:check
npm run analytics:history:integration
npm run build:analytics-read-model-projector
```

The integration test inserts accepted and out-of-order counter observations,
resolves a test binding, checks increase/reset output and confirms a second
projection pass is idempotent.

## Current limitations

- Energy Series queries read electricity facts only; other energy types are projected but not queried;
- late-arrival predecessor corrections and rebuild evidence are implemented; arbitrary operator-selected historical backfill/rebuild scopes are not;
- no durable projector checkpoint exists yet;
- tariff, cost, carbon, baseline, reporting and optimization slices remain on
  the Wayfinder frontier.
