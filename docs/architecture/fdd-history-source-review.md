# FDD authoritative History source review

Status: REVIEWED
Issue: #341
Parent specification: #331
Reviewed: 2026-08-28

## Scope

This review covers only the production seam that prepares canonical Telemetry/History evidence for the existing `CHILLED_WATER_LOW_DELTA_T` FDD rule. It does not change simulator, Alarm, Work Order, Forecast, or Optimization ownership.

The local authoritative contract is `libs/telemetryhistorymodel`: FDD consumes a tenant/site/device-scoped history query for the canonical BTU supply- and return-water temperature keys, keeps Observation identity/event time/Point revision/quality/source position intact, and persists only FDD-owned Findings.

## ThingsBoard CE

Pinned baseline: ThingsBoard CE v4.3.1.1, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Upstream source already reviewed in `thingsboard-source-review.md` S07 and re-used for this seam:

- `common/data/src/main/java/org/thingsboard/server/common/data/kv/BaseReadTsKvQuery.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/kv/AggregationParams.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/AggregationTimeseriesDao.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/AbstractSqlTimeseriesDao.java`
- `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java`

Decisions:

- **ADOPT** — history reads are explicit bounded queries over requested keys and a requested time window; query bounds are part of the server contract rather than a browser convention.
- **ADAPT** — HVAC history retains Observation ID, Point identity/revision, event time, quality, acceptance and source position. FDD uses those authoritative Observation IDs as `FDDFinding.evidenceIds` rather than reducing history to anonymous numeric samples.
- **ADAPT** — the FDD owner asks Telemetry History for exactly the two canonical BTU keys and follows the History-owned opaque cursor until the authorized window is complete instead of silently evaluating a partial page.
- **REJECT** — generic Rule Engine/message enrichment as the owner of FDD evidence, caller-authored evidence arrays, and any fallback from unavailable history to latest/synthetic values.

## OpenEMS

Implementation checkpoint from #331: OpenEMS `develop` commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6`.

Relevant official source family, also recorded in `openems-source-review.md` Review 004:

- `io.openems.edge.timedata.api/src/io/openems/edge/timedata/api/Timedata.java`
- `io.openems.common/src/io/openems/common/timedata/CommonTimedataService.java`
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/TimedataRrd4jImpl.java`
- `io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ResendHistoricDataWorker.java`

Decisions:

- **ADOPT** — Timedata/history remains a separate owner from the consumer that interprets the values; downstream behavior requests explicit channels and a time range rather than owning history storage itself.
- **ADAPT** — HVAC keeps the existing high-resolution canonical Telemetry History contract instead of OpenEMS RRD4j's storage/rollup choices, because FDD evidence must retain exact Observation identity and event time.
- **ADAPT** — missing or unusable required history is a failed FDD evaluation. OpenEMS-style nullable/missing channel results are not converted into a degraded diagnostic result for this first tracer.
- **REJECT** — copying OpenEMS OSGi/component machinery or moving FDD rule ownership into Timedata.

## MyEMS

Pinned baseline: MyEMS v6.7.0, commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

Relevant official source/document groups already reviewed in `myems-source-review.md`:

- `myems-cleaning/`
- `myems-normalization/`
- `myems-aggregation/`
- `database/`, specifically the historical and FDD data-purpose separation

Decisions:

- **ADOPT** — acquisition/history facts and FDD products have separate ownership and processing lifecycles; FDD consumes historical facts and writes only FDD-owned output.
- **ADOPT** — history quality and UTC/event-time semantics remain explicit inputs to downstream analysis.
- **ADAPT** — HVAC expresses that separation through existing Telemetry History and FDD service contracts rather than copying MyEMS's physical database/module layout.
- **REJECT** — using a `latest` projection, repaired/normalized substitute, or simulator state when the required authoritative historical evidence is missing, invalid or unauthorized.

## #341 implementation consequence

The accepted production chain is:

```text
Session / platform-gateway
    -> IAM authorizes exact Device + fixed BTU keys + history window
    -> Gateway signs a History delegation grant whose presenter is fdd-service
    -> fdd-service presents its own mTLS workload identity to Telemetry Query
    -> Telemetry Query validates issuer, presenter, action and exact scope
    -> canonical Device History response
    -> FDD validates response and latest required observations
    -> CHILLED_WATER_LOW_DELTA_T evaluation
    -> FDD-owned Finding or CLEAR result
```

There is no compatibility branch: caller-supplied evidence is rejected, history authorization/query/quality failures do not fall back, and simulator code has no FDD write path.
