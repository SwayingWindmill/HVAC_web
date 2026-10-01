# ThingsBoard Telemetry Reconciliation Source Review

## Scope

This review governs the remote acceptance integration that reads telemetry from an existing ThingsBoard CE installation and reconciles it into HVAC Web's canonical Registry and Telemetry Runtime.

ThingsBoard remains an upstream source. It does not become a browser/query dependency and it does not own HVAC canonical Device, Sensor, Point, Presence, Latest, Revision, or History facts.

## Pinned upstream

- ThingsBoard CE: `v3.9.1`
- Deployed image on the acceptance host: `thingsboard/tb-postgres:3.9.1`
- Official release: `https://github.com/thingsboard/thingsboard/releases/tag/v3.9.1`

Reviewed upstream source at the pinned release where available:

- `application/src/main/java/org/thingsboard/server/controller/TelemetryController.java`
- `ui-ngx/src/app/core/http/attribute.service.ts`
- ThingsBoard authentication API used by the Web client (`POST /api/auth/login`)

The controller contract authorizes telemetry reads through ThingsBoard's normal entity access checks and exposes latest values at:

`GET /api/plugins/telemetry/{entityType}/{entityId}/values/timeseries`

The Angular client uses that same endpoint for latest entity telemetry. The API supports selecting multiple keys in one request and `useStrictDataTypes=true`.

## Local evidence reviewed

- Existing ThingsBoard deployment contains 103 Device rows but only three currently active aggregate telemetry sources used by this integration:
  - `temperature`
  - `waterflow`
  - `ammeter`
- Those three rows are upstream aggregate/reporting sources, not three physical sensors.
- Historical ThingsBoard Device labels provide partial physical semantics for `t1..t20`; uncertain channels retain stable source-key identity rather than invented semantics.
- HVAC Registry/S2 supports the required ownership model directly:
  - Reporting Device
  - Sensor
  - Telemetry Point
  - External Binding
  - S2 Registry projection
  - Presence/Freshness policy
- Telemetry Runtime accepts `RECONCILIATION` as a first-class source path and owns quality, latest, presence, revisions, realtime publication, and history outbox behavior.

## Reproducible bootstrap reference review

The remote acceptance state is not treated as a deployment source of truth. Before formalizing it, the bootstrap shape was checked against the three reference projects used by this repository.

### ThingsBoard — authorized service/API boundary

Reviewed source:

- `application/src/main/java/org/thingsboard/server/controller/TelemetryController.java`
- `dao/src/main/java/org/thingsboard/server/dao/device/DeviceServiceImpl.java`

The relevant pattern is that Device lifecycle and telemetry access go through ThingsBoard services/controllers and their authorization checks. HVAC therefore keeps only ThingsBoard external IDs and mapping metadata in its own declarative config; the reconciler reads telemetry through ThingsBoard's official REST API. A PostgreSQL dump or `ts_kv_latest` query is not a supported bootstrap mechanism.

### OpenEMS — stable Component-ID plus explicit configuration

Reviewed source:

- `https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.controller.evse/src/io/openems/edge/controller/evse/single/Config.java`
- `https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.solaredge/src/io/openems/edge/solaredge/gridmeter/Config.java`
- `https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/readme.adoc`

OpenEMS gives every Component a stable `id`, keeps human-readable aliases separate, references other Components by ID, and its simulator accepts the complete set of Component configurations as explicit input. HVAC adopts the same useful property here: Asset, Device, Sensor, Point, projection, relationship and IAM binding identities are stable and derived from the tracked mapping slots. Reapplying the bootstrap converges the same identities instead of creating a second copy of the accepted topology.

### MyEMS — configuration database plus ordered/repeatable installation

Reviewed source:

- `https://github.com/MyEMS/myems/blob/master/database/README.md`

MyEMS keeps data sources, equipment, meters, Points and their relationships in its system configuration database, while installation/upgrade SQL is applied in an explicit order. HVAC keeps the same separation of concerns: the tracked mapping describes configuration facts, the S1 seed owns Registry/IAM facts, and the S2 seed owns Telemetry Runtime projections/policies. Runtime telemetry values are never embedded in the seed.

### Resulting local source of truth

The accepted integration is now represented by tracked, credential-free deployment assets:

- `deploy/platform/phase1/config/thingsboard-sensor-monitoring.v1.json` — stable topology and `sourceKey -> telemetryKey` mapping;
- `scripts/thingsboard-sensor-monitoring-seed.mjs` — idempotent S1/S2 bootstrap and reconciler-config generation;
- `deploy/platform/phase1/integrations/thingsboard.compose.yaml` — isolated reconciliation worker runtime;
- `scripts/phase1-thingsboard-integration.mjs` — Phase 1 prepare/seed/up/smoke orchestration.

ThingsBoard tenant credentials remain deployment secrets supplied by the existing ThingsBoard environment and are not copied into Registry metadata, generated config, or Git.

## Decisions

### ADOPT — ThingsBoard's authorized telemetry REST contract

The reconciler reads latest telemetry through the official ThingsBoard API rather than querying ThingsBoard persistence tables.

For each aggregate source, one request selects all mapped keys. The integration therefore performs three source reads per poll, not one request per Point.

Authentication uses ThingsBoard's normal login/token flow. Credentials are deployment secrets and are not stored in Registry mapping metadata.

### REJECT — direct `ts_kv_latest` / PostgreSQL read-through

Directly querying ThingsBoard tables is rejected as the durable integration design even though it was useful for initial acceptance proof.

Reasons:

- `ts_kv_latest`, `key_dictionary`, and related tables are ThingsBoard implementation details, not the public provider contract.
- Direct database access bypasses ThingsBoard entity authorization.
- It requires sharing database/network credentials and couples HVAC upgrades to ThingsBoard schema internals.
- It makes a future ThingsBoard upgrade materially riskier for no product benefit.

### ADAPT — aggregate ThingsBoard Devices to HVAC Device/Sensor/Point authority

The three ThingsBoard Devices remain external reporting identities only. HVAC creates three reporting Devices and models the contained measurements as independent Sensors and Points.

Current remote mapping:

- `temperature`: 20 temperature Sensors/Points (`t1..t20`), source scale stored explicitly in mapping metadata.
- `waterflow`: one flow Sensor with `flowMeter` and `flowVelocity` Points.
- `ammeter`: one energy-meter Sensor with `powerTotal` and `combinedActiveTotalElectricalEnergy`; accumulated active energy is a Counter Point.

ThingsBoard UUIDs are retained only as external IDs. They never become HVAC Device IDs.

### ADOPT — canonical Telemetry Runtime acceptance

The reconciler does not write `latest_accepted_telemetry`, Presence, ClickHouse, Redis, or realtime transport directly.

Every changed upstream observation is submitted through the existing S2 acceptance endpoint with:

- a dedicated integration instance,
- a dedicated SPIFFE/mTLS client identity,
- `sourcePath=RECONCILIATION`,
- the ThingsBoard Device UUID as external identity,
- the mapped telemetry key,
- upstream sample timestamp,
- deterministic UUIDv7 event identity.

This preserves one owner for mapping, quality, freshness, Presence, latest state, revisions, realtime publication, and history projection.

## ClickHouse replay finding

The acceptance rollout also exposed a separate existing History sink defect.

Pinned runtime:

- ClickHouse `26.3.12.3`

Reviewed upstream defect/fix:

- `ClickHouse/ClickHouse#110604` — partitioned MergeTree + `insert_deduplication_token` + partial async-insert deduplication can fail with `Invalid partition key size: 0`.
- `ClickHouse/ClickHouse#110651` — upstream fix for the partial async-insert deduplication path.

The deployed 26.3 runtime reproduced the same error exactly during history replay. The local History sink already sends one observation per HTTP request with bounded parallelism, so ClickHouse async buffering adds no required owner capability.

Decision: **ADAPT** the sink to synchronous deduplicated inserts:

- keep `insert_deduplication_token=<observation_id>`;
- set `async_insert=0`;
- set `insert_deduplicate=1`;
- remove `wait_for_async_insert` and `async_insert_deduplicate`.

This preserves idempotent replay while avoiding the affected upstream async partial-dedup path. No additional application retry layer is introduced.

A separate forward ClickHouse migration creates the previously configured-but-missing `telemetry_history_writer` identity with only the INSERT and materialized-view source-column SELECT privileges required for `telemetry_history.observations`.

## Acceptance evidence

During remote acceptance the canonical path produced:

- 24 mapped latest telemetry Points with `GOOD` quality;
- three Device snapshots;
- source-activity-derived Presence without fabricated online state;
- continuous revision growth as ThingsBoard data changed;
- History outbox fully converged to `PUBLISHED` after the ClickHouse writer and synchronous-dedup fixes;
- raw ClickHouse history row count equal to unique `observation_id` count;
- populated 1-minute, 15-minute, hourly numeric rollups and Counter deltas.

The integration therefore preserves the intended ownership boundary:

`ThingsBoard -> authorized reconciliation source -> Registry mapping -> Telemetry Runtime -> Redis/Postgres/ClickHouse/realtime -> HVAC Web`
