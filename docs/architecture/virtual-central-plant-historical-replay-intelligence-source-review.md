# Virtual Central Plant — Historical Replay / Intelligence Ingestion Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research Historical Replay and intelligence ingestion seam`
Reviewed: 2026-08-28

## Question

How should controlled historical timestamps from the shared Plant/Scenario model enter the existing ingest/history pipeline so Energy analytics and Forecast can consume authoritative history, and what server-owned provenance must exist before Optimization can use those products without direct simulator writes?

## Primary local sources reviewed

- `modules/iot/pkg/adapter/processor.go`
- `modules/telemetry/pkg/telemetry/ingest.go`
- `modules/telemetry/pkg/telemetry/ingest_store.go`
- `modules/telemetry/pkg/telemetry/source_server.go`
- `modules/telemetry/pkg/telemetry/history.go`
- `modules/telemetry/pkg/telemetry/history_postgres.go`
- `modules/telemetry/internal/history/client.go`
- `infra/telemetry/clickhouse/init/004-counter-semantics.sql`
- `modules/energy/internal/clickhouse/client.go`
- `modules/energy/internal/energy/projector.go`
- `modules/energy/internal/energy/rebuild.go`
- `infra/telemetry/clickhouse/init/006-forecast-series.sql`
- `infra/registry/postgres/init/009e-forecast-model-v2.sql`
- `services/forecast-service/internal/forecast/model.go`
- `services/forecast-service/internal/forecast/jobs.go`
- `infra/registry/postgres/init/009f-optimization-model-v2.sql`
- `services/optimization-service/internal/optimization/model.go`
- `services/optimization-service/internal/optimization/jobs.go`
- `docs/architecture/thingsboard-ai-analytics-integrations-adjudication.md`

## Executive decision

Historical Replay is a separate runner that may reuse the same Plant/Scenario physics, but it must stop at a **Telemetry-owned historical admission boundary**.

It must not:

- accelerate the live runtime clock;
- pretend historical observations were received in the past;
- mutate current Telemetry/Presence/Business Revision as a side effect of backfill;
- call Forecast or Optimization with simulator-authored feature arrays, baselines or snapshot IDs;
- write ClickHouse/analytics/Forecast/Optimization tables directly.

The desired downstream flow is:

```text
Historical Replay runner
    -> Telemetry-owned historical admission
    -> telemetry_history observations / event-time projections
    -> Energy / Metric owner recomputation
    -> server-owned Forecast input snapshot builder
    -> Forecast snapshot
    -> server-owned Optimization input snapshot builder
    -> Optimization
```

The current repository already has most of the **storage/provenance models after admission**, but it lacks three runtime preparation seams:

1. a safe history-only Telemetry admission path for controlled backfill;
2. a Forecast input-snapshot/job builder that reads authoritative history/analytics;
3. an Optimization input-snapshot/job builder that freezes authoritative Forecast/current-state/topology/tariff inputs.

## 1. MQTT `replay` is transport metadata, not Historical Replay semantics

The IoT telemetry envelope has a `replay` boolean and the adapter exposes it in `ProcessingResult`, primarily for observability/metrics.

When the adapter constructs a Telemetry observation it still uses ordinary `SourcePath: PUSH` and calls the same Runtime `AcceptObservation`. The `replay` bit is not forwarded as a special Telemetry admission mode.

Therefore:

- `replay=true` does not mean “history-only”;
- it does not disable latest/Presence effects;
- it does not change quality/freshness semantics;
- it is not an authorization to bypass source ordering.

**Decision:** do not overload the existing MQTT replay bit to implement Historical Replay.

## 2. Telemetry History already preserves late event-time facts correctly

Telemetry `AcceptObservation` evaluates source position and current ordering, but every position-advancing observation also gets a history outbox intent.

For a canonical point, an observation whose `sampledAt` is not newer than the current latest is classified `OUT_OF_ORDER`; it does not replace latest state. Crucially, the source observation still retains its typed value and the history outbox still carries it.

The ClickHouse History reader explicitly includes:

```text
acceptance_status IN ('ACCEPTED', 'OUT_OF_ORDER')
```

and orders history by:

```text
telemetry_key, sampled_at, observation_id
```

The canonical counter-delta view does the same and states in source comments that deltas are derived from raw historical facts in **event-time order**, without consulting latest state.

This means the history product is already designed for late/backfilled observations and is the right owner for Historical Replay output.

**ADOPT:** event-time history, immutable observation identity, source position, point revision and `ACCEPTED/OUT_OF_ORDER` provenance.

## 3. The ordinary live ingest endpoint is not a safe Historical Replay boundary

Reusing normal `AcceptObservation` unchanged has important side effects.

### Current-state contamination

An old sample becomes `OUT_OF_ORDER` only when a newer latest value already exists. If a replayed key has no current latest value yet, the old sample may be classified `ACCEPTED`, which causes:

- `ReplaceLatest = true`;
- Presence signal emission;
- Device Observation Snapshot reevaluation;
- possible Business Revision advancement.

Historical backfill must not establish current runtime truth merely because a point happened to be empty.

### Wrong freshness semantics

The source HTTP server assigns `ReceivedAt` from the actual current server receive time. This is correct provenance and must remain true.

But ordinary ingest compares `SampledAt` against `ReceivedAt`; sufficiently old observations receive `SOURCE_LAG_EXCEEDED` and `STALE` quality.

That is correct for a late live source but not sufficient to describe an intentional controlled historical import. In Energy projection, source `STALE` maps to invalid fact quality, so blindly using live freshness semantics would make replay-generated historical energy facts unusable or misleading.

### Binding semantics are already time-aware but split by fact type

Device integration binding validity is checked using `ReceivedAt`, while Point binding validity is checked using `SampledAt`. This is a meaningful distinction for live ingest.

Historical Replay therefore needs an explicit owner-reviewed rule for how integration/source authorization at import time and historical Point identity as-of event time combine. It must not fake `ReceivedAt` to force an old device binding to match.

**Decision:** reuse the Telemetry history model and mapping rules, but do not treat ordinary live admission side effects as the Historical Replay contract.

The exact minimal history-only admission/provenance contract should graduate to a Wayfinder decision rather than be guessed in this research ticket.

## 4. Energy analytics is structurally ready for event-time replay

The energy projector reads the canonical `telemetry_history.counter_deltas` view, which:

- includes accepted and out-of-order observations;
- orders by `sampled_at` and observation identity;
- respects Point revision boundaries;
- does not use latest state.

The projector resolves meter binding at the current observation's `sampledAt`, builds interval facts from previous/current event-time observations, and has explicit correction/rebuild semantics when a late observation changes a previously projected predecessor relationship.

`correctionRevision` increments the energy fact revision when the same logical current observation acquires a different valid predecessor, and the projector can emit rebuild events for corrections.

So the correct Historical Replay integration is not “write energy facts from the simulator.” It is:

```text
historical counter observations
    -> Telemetry History
    -> counter_deltas
    -> existing Energy projector
    -> energy_interval_facts / corrections
```

**ADOPT:** existing event-time delta and correction/rebuild semantics.

**Gap:** intentional replay quality must not be collapsed into ordinary live-source `STALE` if that makes otherwise valid historical facts invalid. The exact provenance/quality vocabulary belongs to the Historical Replay admission decision.

## 5. Forecast already has strong immutable provenance tables, but no runtime input builder

The Registry schema already defines `forecast_input_snapshots` with:

- deployment ID;
- model version;
- feature-set version;
- topology version;
- `latest_data_time`;
- weather issue time;
- metric-version references;
- feature values;
- input checksum;
- capture time.

`forecast_jobs` has a foreign key to an exact input snapshot, and published forecast snapshots preserve job/deployment/model/input provenance.

This is the correct server-owned model.

The Forecast runtime Request likewise carries `InputSnapshotID`, model/feature/topology versions, forecast origin and observations, and rejects no-input forecasts.

However, repository-wide source review finds no production runtime that inserts `forecast_input_snapshots` or `forecast_jobs`; those inserts appear in Registry tests. The Forecast worker claims jobs that already exist, and its validation checks that the scheduler payload is internally well-formed and scope-consistent.

ClickHouse already provisions a least-privilege `forecast_service_reader` with SELECT access to:

- `telemetry_history.observations`;
- `telemetry_history.counter_deltas`;
- `analytics.energy_interval_facts`;
- metric result facts;
- forecast series.

But current Forecast service code does not use that reader to construct the immutable input snapshot.

**Gap F — Forecast input preparation is missing.**

A server-owned preparation path must query authoritative history/analytics, freeze the exact feature values/version references/checksum into `forecast_input_snapshots`, create the Forecast job, and only then let the worker execute it.

**Reject:** Simulator -> direct Forecast POST carrying simulator-authored observations and a fabricated `InputSnapshotID`. That would bypass the provenance model already present in the database.

## 6. Optimization has the right frozen-input model, but its builder is also missing

`optimization_input_snapshots` already models the desired boundary:

- BUILDING -> SEALED state;
- immutable checksum once SEALED;
- exact policy version;
- topology version;
- load Forecast snapshot;
- optional PV Forecast snapshot;
- tariff version;
- current state;
- safety constraints;
- maintenance constraints;
- manual locks;
- capture time.

Optimization recommendations have foreign keys to both the run and the exact input snapshot. Control dispatch is additionally guarded by approval/current-state revalidation semantics.

The Optimization Request requires these identities plus `InputChecksum`, Forecast snapshot IDs, policy/topology/tariff/deployment revisions and a baseline/constraints/response model.

But repository-wide review finds no production runtime that inserts `optimization_input_snapshots`; current inserts are Registry tests. The worker validates the scheduler payload syntactically and against job scope, not by independently assembling the source facts.

**Gap G — Optimization input preparation is missing.**

Optimization must consume a server-owned SEALED snapshot produced from authoritative Forecast and current production owners. Historical Replay does not call Optimization and does not populate Optimization tables.

## 7. Replay clock semantics

The previously confirmed Wayfinder decision remains correct:

- live Virtual Plant uses 1x wall-clock;
- Historical Replay is a different runner;
- both may reuse the same deterministic Plant/Scenario physical model;
- only the replay runner controls a historical simulation clock.

For each generated historical observation:

- `SampledAt` represents the replayed physical event time;
- actual ingest `ReceivedAt` remains the real server receive time;
- source/event identity must be deterministic/unique enough for idempotent retry;
- no future/current state should be inferred from the replay clock.

Do **not** accelerate the live runtime or emit future timestamps into current Telemetry to obtain training history faster.

## 8. No direct ClickHouse or intelligence writes

Although direct inserts would be technically easy for an acceptance tool, they would bypass:

- Registry Point binding/revision semantics;
- Telemetry source evidence and observation identity;
- history outbox/retry/idempotency;
- Energy correction/rebuild behavior;
- Forecast feature/input checksum provenance;
- Optimization SEALED input provenance.

**Decision:** Historical Replay must use one owner admission seam. Downstream projections remain owner-driven.

## Target authority chain

```text
Shared Plant / Scenario model
        |
        +----------------------------+
        |                            |
        v                            v
Live runner                     Historical Replay runner
1x wall clock                   controlled historical clock
        |                            |
        v                            v
DeviceAdapter / Edge             typed historical observations
        |                            |
        v                            v
MQTT / IoT                  Telemetry-owned history admission
        |                            |
        v                            +--> immutable source/history evidence
Telemetry current truth          +--> NO latest / Presence / BusinessRevision mutation
                                     |
                                     v
                             telemetry_history
                                     |
                   +-----------------+------------------+
                   |                                    |
                   v                                    v
             Energy / Metric                      Forecast input builder
             owner projections                   (currently missing)
                                                        |
                                                        v
                                              forecast_input_snapshot
                                                        |
                                                        v
                                                Forecast worker
                                                        |
                                                        v
                                                 Forecast snapshot
                                                        |
                                                        v
                                            Optimization input builder
                                                (currently missing)
                                                        |
                                                        v
                                          SEALED optimization snapshot
                                                        |
                                                        v
                                                Optimization worker
```

## Fog cleared by this research

The map can now state confidently:

1. Historical Replay and live simulation are different runners sharing physics only.
2. Replay terminates at a Telemetry-owned history admission seam.
3. Existing History/Counter/Energy models are designed to preserve and recompute event-time late facts.
4. Existing MQTT `replay` is not that admission seam.
5. Direct Forecast/Optimization writes from the simulator are forbidden.
6. Forecast and Optimization already have the correct immutable provenance schemas, but server-owned input builders/job preparation are missing.

The remaining design question is now narrow enough for HITL decision:

> What exact minimal Historical Replay admission/provenance contract should Telemetry expose so controlled backfill reaches history without mutating current state, while preserving real received time, historical event time, canonical Point mapping, intentional-backfill quality/provenance and idempotent source identity?

## Decision

The existing architecture should be **extended at owner boundaries, not bypassed**:

- keep `receivedAt` truthful;
- keep historical `sampledAt` controlled by the replay clock;
- reuse canonical Registry/Point and Telemetry History semantics;
- add no simulator write path into analytics or intelligence products;
- let Energy/Metric owners recompute from history;
- build Forecast inputs server-side into immutable `forecast_input_snapshots`;
- build Optimization inputs server-side into SEALED `optimization_input_snapshots` referencing published Forecast snapshots and current authoritative state.

This is the smallest design that preserves the project's existing authority model and avoids defensive or simulator-specific complexity.
