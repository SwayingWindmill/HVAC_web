# Analytics Read Model Projector

`analytics-read-model-projector` is the independent write boundary for the
first Energy Slice:

```text
telemetry_history.counter_deltas
    -> Core MeterBinding resolver
    -> analytics.energy_interval_facts
```

The projector does not calculate counter lag, reset, rollover or recovery
semantics in Go. Those rules belong to the canonical ClickHouse view. The
first slice accepts only a uniquely resolved RELEASED/ACTIVE PRIMARY
electricity COUNTER binding.

For each accepted canonical delta, the projector writes one interval whose
energy is exactly `delta_value`. `GOOD`, `PARTIAL`/`ESTIMATED`/`MANUAL`, and
`STALE`/`INVALID`/unknown raw qualities map to `VALID`, `SUSPECT`, and
`INVALID`. NULL deltas and invalid decreases are excluded by the canonical
view; they are never written as zero-energy facts.

Facts are identified by the current observation ID. The logical idempotency
key is `(tenant_id, site_id, meter_binding_id, source_current_observation_id)`.
`fact_revision` and `rebuild_run_id` are reserved by the physical schema for
the later rebuild slice; W0 writes revision `0` with no run ID.

The Core resolver is a private mTLS route and requires a short-lived Registry
delegation grant with the dedicated `meter-binding.resolve` permission. A
grant file is preferred so IAM can rotate the grant without restarting the
projector.

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
| `ANALYTICS_CORE_CA` | Core server CA bundle |
| `ANALYTICS_CORE_TLS_CERT` | Projector mTLS certificate |
| `ANALYTICS_CORE_TLS_KEY` | Projector mTLS private key |
| `ANALYTICS_CORE_REGISTRY_GRANT` or `ANALYTICS_CORE_REGISTRY_GRANT_FILE` | Registry delegation grant |

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

- only electricity PRIMARY COUNTER facts are implemented;
- historical corrections and explicit rebuild jobs are not implemented;
- no durable projector checkpoint exists yet;
- tariff, cost, carbon, baseline, reporting and optimization slices remain on
  the Wayfinder frontier.
