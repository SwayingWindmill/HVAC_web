# Virtual Central Plant — Minimal Historical Replay Admission Contract Decision

Status: WAYFINDER DECISION
Wayfinder map: `wayfinder: virtual central plant`
Decision ticket: `Decide minimal Historical Replay admission contract`
Decided: 2026-08-28

## Decision

Historical Replay is a separate runner that submits controlled historical observations to a Telemetry-owned history-only admission path. It reuses the existing canonical Observation, Device/Point binding, source-position, source-observation and telemetry-history outbox model, but it is explicitly forbidden from mutating current Device runtime truth.

Historical Replay is not MQTT replay, not a Forecast/Optimization input API and not a direct ClickHouse writer.

## Admission surface

Add one internal Telemetry owner surface dedicated to historical observations, for example:

```text
POST /internal/v1/telemetry/history/observations:accept
```

The owner method is explicit rather than a magic flag on live ingestion:

```text
AcceptHistoricalObservation(...)
```

The server fixes the provenance to:

```text
SourcePath = HISTORY_REPLAY
```

A caller cannot label an ordinary live request as `HISTORY_REPLAY`.

The endpoint uses the existing trusted workload identity and source authorization mechanism with a dedicated authorized Historical Replay integration instance. It remains internal and does not use the MQTT envelope `replay` flag.

## Identity and time semantics

Historical Replay preserves two different time responsibilities.

### Real admission time

`receivedAt` is the actual time Telemetry accepted the replayed observation.

It is used for:

- authenticating the current Historical Replay source workload;
- validating the currently authorized replay integration;
- durable ingest provenance;
- transaction/outbox timing.

`receivedAt` is never forged to look historical.

### Historical event time

`sampledAt` is the controlled historical event time produced by the Historical Replay runner.

It is used for:

- resolving the historically applicable Point binding and Point revision;
- historical ordering and projections;
- downstream event-time Energy/Metric processing.

For v1 the admission accepts only:

```text
externalEntityType = DEVICE
```

because Device owns the canonical Point relationship. Asset-level replay is not added speculatively.

A Point binding that is retired today may still be used when it was valid at `sampledAt`. The replay integration itself must still be currently authorized at import time.

## Validation and quality

Historical Replay keeps the normal Point and value contract checks:

- valid JSON value;
- declared value type matches the Point contract;
- unit matches;
- numeric range checks apply;
- wire/source trust continues to influence quality;
- future-clock protection remains active;
- mapping conflicts/quarantine remain authoritative.

The one live-only freshness rule that does not apply is source lag:

```text
receivedAt - sampledAt > MaxSourceLag
```

Historical age is intentional on this path and must not by itself produce `SOURCE_LAG_EXCEEDED` or `STALE`.

No new `BACKFILLED`, `SIMULATED` or similar Telemetry Quality is introduced. `HISTORY_REPLAY` is provenance, not measurement quality.

A valid trusted historical observation may therefore have `GOOD` quality. A source-quality problem may still produce `PARTIAL`; invalid type/unit/range/future-clock inputs remain rejected or quarantined according to the existing owner rules.

## History-only side effects

The admission is explicitly limited to historical persistence and source evidence.

Allowed:

```text
advance source position
persist source observation
persist telemetry history outbox intent
resolve canonical Device / Point / Point revision
preserve sampledAt / truthful receivedAt
preserve acceptance status / quality / provenance
```

Forbidden:

```text
ReplaceLatest
EmitPresenceSignal
ReevaluateDeviceSnapshot
advance BusinessRevision
mutate current Device runtime state
```

This prohibition applies even when the replayed Point has no existing current value. A historical observation never bootstraps current truth.

A successful receipt therefore remains compatible with the existing Observation contract while making the lack of current-state mutation explicit:

```text
observationId    = <uuid>
status           = ACCEPTED
quality          = GOOD | PARTIAL | ...
deviceId         = <canonical device>
businessRevision = 0
stateChanged     = false
```

No `BACKFILLED` acceptance status is added. Being accepted into Telemetry history and becoming current latest are separate concerns.

## Deterministic replay identity and idempotency

Historical Replay reuses the existing `SourcePosition` model:

```text
SourcePosition {
  partition
  offset
  eventId
}
```

Each replay run/dataset has one stable non-business provenance identifier:

```text
replayDatasetId = UUIDv7
```

The same dataset ID survives retries and runner restarts.

The canonical partition convention is:

```text
history-replay/<replayDatasetId>/<deviceExternalId>
```

Within one partition, offsets are monotonically increasing integers following the canonical replay-record order.

`eventId` is deterministic for one logical replay observation and is regenerated identically after retry/restart from the stable tuple:

```text
replayDatasetId + partition + offset
```

The generated value must satisfy the platform UUIDv7 source-event requirement while remaining stable for that tuple. Payload hash is evidence, not event identity.

The existing source-position/event deduplication remains the only replay idempotency mechanism. No separate Replay Offset Store or generic replay cursor framework is introduced.

## v1 transport shape

v1 accepts one historical observation per admission request and per Telemetry transaction.

```text
one request
  → one historical Observation
  → one atomic Telemetry transaction
  → one history outbox intent
```

The Historical Replay runner owns ordering and bounded concurrency.

A batch admission API is deferred until real replay workload demonstrates a throughput requirement. v1 therefore avoids partial-batch success semantics, per-record batch receipts, batch retry rules and speculative size limits.

Any future bulk transport must preserve the exact same single-observation domain semantics.

## Downstream boundary

Historical Replay terminates at Telemetry-owned history:

```text
Plant / Scenario physical model
        ↓ controlled historical clock
Historical Replay runner
        ↓
Telemetry HISTORY_REPLAY admission
        ↓
telemetry_history
        ↓
Energy / Metric projections
        ↓
Forecast-owned input snapshot builder
        ↓
Forecast
        ↓
Optimization-owned SEALED input snapshot builder
        ↓
Optimization
```

The runner must not:

- write ClickHouse directly;
- write Energy/Metric facts directly;
- call Forecast evaluation with simulator-built feature snapshots;
- call Optimization with simulator-built input snapshots;
- mutate current Telemetry latest/Presence/Business Revision;
- overload MQTT `replay=true` as a history-import contract.

## Complexity boundary

v1 does not introduce:

- a generic ingestion-mode framework;
- compatibility fallbacks between live and historical paths;
- fake historical `receivedAt`;
- new historical quality enums merely to label provenance;
- batch ingestion;
- Asset replay;
- direct downstream intelligence writes.

The implementation should share existing Telemetry primitives below the two explicit owner entry points rather than duplicate the Observation/History model.
