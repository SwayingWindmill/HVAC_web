# Analytics Read Model Projector

`analytics-read-model-projector` is the independent write boundary between raw
ClickHouse telemetry history and product-ready analytical facts.

```text
telemetry_history.observations
    -> analytics-read-model-projector
        -> analytics.energy_interval_facts
```

It is intentionally separate from Telemetry Runtime, the History Projector,
Cube Core and the Query Service.

## First read model

The initial slice supports the canonical aggregate HVAC electricity meter:

```text
telemetry key: hvac_meter.energy
source unit:   kWh
energy type:   electricity
```

For every pair of adjacent accepted cumulative readings on the same tenant,
site, device and telemetry key, the projector creates one additive interval
fact.

| Source condition | Fact result |
|---|---|
| Current value is greater than or equal to previous value | Difference in kWh |
| Either source reading is `SUSPECT` | `SUSPECT` fact |
| Meter resets or rolls back | `0 kWh`, `SUSPECT`, `METER_RESET_OR_ROLLBACK` |
| Cumulative value is negative | `0 kWh`, `INVALID`, `NEGATIVE_CUMULATIVE_VALUE` |
| Source quality is unsupported or invalid | `INVALID`, `SOURCE_QUALITY_INVALID` |

The current observation ID is the fact identity. `source_offset` becomes the
numeric dataset revision, and `current_sampled_at` becomes the authoritative
data watermark.

Intervals are attributed to the time bucket containing `period_end`. The first
slice does not prorate one interval across multiple hour/day/month boundaries.

## Processing semantics

The ClickHouse Reader executes a fixed query that:

- filters to accepted `hvac_meter.energy` observations in `kWh`;
- uses `lagInFrame` to pair adjacent cumulative readings;
- excludes rows with missing Organization, Site or Device identity;
- anti-joins `analytics.energy_interval_facts` on the current observation ID;
- returns at most the configured batch size.

The Writer:

- validates every fact;
- writes one JSONEachRow batch;
- generates a deterministic SHA-256 batch deduplication token;
- waits for ClickHouse async insertion to complete.

The anti-join and ClickHouse deduplication token make repeated projection
idempotent within the configured ClickHouse deduplication window.

## Data ownership

| Dataset | Access |
|---|---|
| `telemetry_history.observations` | Read only |
| `analytics.energy_interval_facts` | Insert and fact-identity read |

The local ClickHouse fixture provisions separate identities:

- `analytics_projector_reader`;
- `analytics_projector_writer`;
- `cube_analytics_reader`.

The local identities have no password only because the ClickHouse endpoint is
bound to loopback for development. Production must use authenticated identities,
TLS and separately managed credentials.

## Environment

Required:

| Variable | Purpose |
|---|---|
| `ANALYTICS_CLICKHOUSE_HTTP_URL` | ClickHouse HTTP origin |

Optional:

| Variable | Default |
|---|---|
| `ANALYTICS_SOURCE_DATABASE` | `telemetry_history` |
| `ANALYTICS_SOURCE_TABLE` | `observations` |
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

Diagnostics expose:

```text
GET /health/live
GET /health/ready
GET /metrics
```

## Verification

```bash
npm run test:analytics
npm run analytics:history:integration
npm run build:analytics-read-model-projector
```

The integration test starts the pinned ClickHouse fixture, creates the schema and
identities, inserts cumulative readings, validates normal and reset intervals,
and confirms that a second projector pass creates no additional facts.

## Current limitations

- only cumulative HVAC electricity in `kWh` is supported;
- historical corrections and explicit rebuild jobs are not implemented;
- the initial candidate scan uses a window query plus anti-join rather than a
  durable projector checkpoint;
- tariff, cost, baseline, water, gas and cooling-energy facts are separate future
  read models.
