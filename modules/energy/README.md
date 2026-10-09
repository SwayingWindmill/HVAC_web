# Energy Processing Domain Module

`modules/energy` owns the Energy Processing write boundary for the current
Energy slice. Phase 1 composes its projector package into `cmd/telemetry-worker`; `cmd/energy-projector` is an explicit standalone build/integration entrypoint, not an additional default deployable:

```text
telemetry_history.counter_arrivals        (after the checkpoint, History order)
    -> telemetry_history.counter_deltas_from (the arrivals' Points, from their earliest sample)
    -> Core MeterBinding resolver
    -> analytics.energy_interval_facts
    -> analytics.energy_projection_checkpoints
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

Each poll reads at most the batch size of Counter observations that Telemetry History made
visible after the checkpoint, ordered by `history_sequence`. For each it projects its own delta
and, when it arrived late, its successor's corrected delta. Reads are bounded by the arrivals'
Points and earliest sample, not by all history (#441). The checkpoint advances only after the
facts are written; a lost advance replays one batch, and deltas whose fact already has the same
predecessor are skipped.

The Counter views use ClickHouse `SQL SECURITY DEFINER`; the projector reader can
query the bounded views without receiving direct raw-observation access.

## Data ownership

| Dataset | Access |
|---|---|
| `telemetry_history.counter_arrivals`, `counter_deltas_from` | Projector read only |
| `analytics.energy_interval_facts` | Projector insert and idempotency read |
| `analytics.energy_projection_checkpoints` | Projector read and advance |
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
- each poll still scans the affected Points' history before the earliest arrival once, for
  their last observation and per-revision maximum; memory stays bounded, time grows with that
  history (#441);
- tariff, cost, carbon, baseline, reporting and optimization slices remain on
  the Wayfinder frontier.
