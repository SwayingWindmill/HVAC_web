# Historical Replay Source Review

Status: IMPLEMENTED / #343

Date: 2026-08-29

## Scope

Issue #343 adds a deterministic Historical Replay runner for the Virtual Central Plant and a Telemetry-owned admission path that can persist generated observations into authoritative History without mutating Current truth.

The source review was performed before the production path was implemented. Existing HVAC code was not treated as authority.

## ThingsBoard CE

Pinned release: `v4.3.1.1`

Pinned commit: `c2a52e46c44e308ddee430e7266b8e10eddde9c4`

Official source/tests reviewed through the pinned project record and rechecked for this ticket:

- `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/latest/sql/SqlLatestInsertTsRepository.java`
- `dao/src/test/java/org/thingsboard/server/dao/sqlts/SqlTimeseriesLatestDaoTest.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/kv/BaseReadTsKvQuery.java`

Observed semantics:

- Timeseries persistence has an explicit history-only path (`saveWithoutLatest`) separate from Latest persistence.
- Latest updates are guarded by observation timestamp; an older historical sample is not allowed to roll Latest backward.
- Historical reads are explicit time-series queries rather than Current-state reconstruction.

Decision:

- `ADOPT`: History and Latest are separate write semantics; replay/backfill must have a first-class history-only owner path.
- `ADAPT`: HVAC Replay goes through Telemetry Runtime validation, canonical Device/Point binding, source-position/idempotency evidence, and the existing History outbox before ClickHouse projection.
- `REJECT`: a caller-controlled `historyOnly`/`replay` flag on the normal live-ingest path, direct ClickHouse writes, or permitting old samples to rely on Latest timestamp guards as the only protection against Current mutation.

## OpenEMS

Pinned release: `2026.7.0`

Pinned commit: `2e2792d`

Official source reviewed through `docs/architecture/openems-source-review.md` Review 004:

- `io.openems.edge.timedata.api/src/io/openems/edge/timedata/api/Timedata.java`
- `io.openems.common/src/io/openems/common/timedata/CommonTimedataService.java`
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/TimedataRrd4jImpl.java`
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/RecordWorker.java`
- `io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/SendChannelValuesWorker.java`
- `io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ResendHistoricDataWorker.java`

Observed semantics:

- Local Timedata ownership, live publishing, and historic resend are separate responsibilities.
- Historic resend queries an explicit historical time/channel range and advances successful resend evidence only after transport success.
- The reference implementation does not require historic recovery to masquerade as a live Channel update.

Decision:

- `ADOPT`: historical recovery/replay is a separate worker/path from live publishing.
- `ADAPT`: the HVAC runner generates deterministic high-resolution observations from the canonical Virtual Plant/Scenario instead of replaying an OpenEMS Timedata store, then hands each observation to the Telemetry owner.
- `ADAPT`: replay is intentionally sequential (maximum in-flight admission = 1). This is a bounded recovery workload, not a throughput path.
- `REJECT`: simulator/runner ownership of Telemetry/History storage, reuse of the live MQTT path merely to obtain persistence, or any direct Current/Presence side effect during historical recovery.

## MyEMS

Pinned release: `v6.7.0`

Pinned commit: `be6e6ce8ddeac57afb04bddb9621501fb555cab0`

Official source/documentation reviewed through the pinned project record:

- `database/`
- `myems-cleaning/`
- `myems-normalization/`
- `myems-aggregation/`
- official `database/README.md`

Observed semantics:

- Historical storage and latest-value tables are distinct concerns.
- Historical facts carry quality/time semantics and feed later cleaning/normalization/aggregation instead of being replaced by a mutable latest cache.
- Acquisition and historical processing are separate modules.

Decision:

- `ADOPT`: preserve raw authoritative historical facts separately from Current/latest projections.
- `ADAPT`: HVAC uses PostgreSQL Telemetry evidence + durable History outbox + ClickHouse History rather than copying MyEMS physical database layout.
- `REJECT`: overwriting raw history during replay, treating a latest cache as History authority, or coupling replay correctness to downstream normalization/aggregation.

## #343 resulting contract

The implemented path is:

```text
canonical Plant + Scenario
        |
        v
eg8200-history-replay
        |
        | HTTPS + dedicated mTLS workload identity
        v
Telemetry Runtime /internal/v1/telemetry/history-replay/observations:accept
        |
        | server-owned HISTORY_REPLAY provenance
        | server-owned receivedAt / partition / deterministic UUIDv7 event identity
        | Registry Device + historical Point binding validation
        v
telemetry_runtime.source_observations
        |
        v
telemetry_runtime.telemetry_history_outbox
        |
        v
ClickHouse telemetry_history.observations
```

The following are explicit invariants:

- the runner never writes PostgreSQL or ClickHouse directly;
- the normal live source route rejects `sourcePath=HISTORY_REPLAY`;
- the replay route fixes `ExternalEntityType=DEVICE` and `SourcePath=HISTORY_REPLAY` server-side;
- the current Device binding is resolved at server receipt time, while Point identity/revision is resolved at the historical sample time;
- historical age is not classified as live-source lag/STALE merely because the sample is old;
- type, unit, range, source quality, mapping, and future-clock validation remain enforced;
- Replay never sets `ReplaceLatest`, `EmitPresenceSignal`, or `ReevaluateSnapshot` and therefore cannot advance Latest, Presence, Device Snapshot, or Business Revision;
- accepted/rejected/quarantined Replay evidence uses the same Telemetry source-observation + History-outbox durability boundary as other authoritative History evidence;
- a stable Replay dataset UUID plus Device-scoped offset sequence produces deterministic source event identity, so retry/restart is idempotent;
- the runner is not present in production compose and requires both the dedicated `TELEMETRY_ALLOWED_HISTORICAL_REPLAY_SPIFFE` workload identity and an exact integration binding; an otherwise authorized live MQTT source cannot invoke Replay.

## Verification

Focused behavior coverage:

- dedicated HTTP admission owns replay provenance/event identity and live admission cannot impersonate replay;
- evaluator proves a 30-day-old valid replay remains `ACCEPTED/GOOD` while future-clock evidence still fails;
- real PostgreSQL store proves accepted Replay evidence and retry idempotency without Current mutation;
- real PostgreSQL outbox -> ClickHouse relay proves `HISTORY_REPLAY` remains visible as authoritative History while Latest, Snapshot revision/hash, and Presence remain unchanged;
- canonical Virtual Plant runner test proves identical request sequence for identical Plant/Scenario, dataset, start, and duration.

The database/History acceptance command is `npm run historical-replay:integration`.
