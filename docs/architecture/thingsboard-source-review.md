# ThingsBoard source-first implementation review

Reference baseline: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Purpose: record the exact upstream source/tests re-read immediately before local implementation or refactoring. The domain adjudication documents remain the architectural decision record; this file records implementation-time source evidence required by `AGENTS.md`.

## S00 — canonical vocabulary and contract cleanup

Date: 2026-08-18

Local issue: #247

### Upstream files reviewed

- `application/src/main/java/org/thingsboard/server/controller/AssetController.java`
- `application/src/main/java/org/thingsboard/server/controller/DeviceController.java`
- `application/src/test/java/org/thingsboard/server/controller/AssetControllerTest.java`
- `application/src/test/java/org/thingsboard/server/controller/DeviceControllerTest.java`
- `application/src/test/java/org/thingsboard/server/controller/TenantControllerTest.java`

All files were read from the pinned `v4.3.1.1` tag, not from `master`.

### Observed upstream semantics

- Asset and Device are distinct first-class identities and have separate controllers/lifecycle APIs.
- Asset and Device ownership is tenant-scoped. Upstream tests explicitly assert the saved entity Tenant ID and reject cross-tenant updates/references.
- Tenant is a first-class ownership entity. The upstream API uses Tenant as the ownership boundary rather than an Organization alias.
- ThingsBoard additionally supports Customer assignment/public sharing. That behavior is not adopted for HVAC authorization because D01/D02 already rejected Customer/Public Customer as the Site/Tenant authorization model.
- Upstream exposes mutable Asset/Device Profile configuration. D02/ADR 0013 already replaces that cross-domain profile ownership with immutable Registry Template Revision plus domain-owned Release References.

### Implementation decision

- `ADOPT`: Tenant terminology as the only ownership vocabulary; separate Asset and Device identities.
- `ADAPT`: HVAC keeps the stronger typed `Tenant -> Site -> Space -> Asset -> Device -> Point` hierarchy and Site-scoped authorization/RLS rather than the generic ThingsBoard relation/customer model.
- `REJECT`: Organization aliases, Customer/Public Customer authorization, free-form relation topology, and mutable cross-domain God Profile.

### S00 consequence

The local implementation must remove active Organization/Area/Equipment machine vocabulary rather than add aliases. Public capabilities become `asset.list` / `asset.read`; Registry contracts use `Space`, `Asset`, `assetId`, and `/assets`; stale tests/fixtures must be rewritten to the current Tenant/Asset production types. Historical audit/ADR text may retain old terms only when explicitly marked historical or superseded.

## S06 — Telemetry Current authority and ingest reliability

Date: 2026-08-19

Local issue: #254

### Upstream files reviewed

- `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/latest/sql/SqlLatestInsertTsRepository.java`
- `dao/src/test/java/org/thingsboard/server/dao/sqlts/SqlTimeseriesLatestDaoTest.java`
- `application/src/main/java/org/thingsboard/server/service/queue/processing/TbRuleEngineProcessingStrategyFactory.java`
- `application/src/main/java/org/thingsboard/server/service/queue/processing/TbRuleEngineSubmitStrategyFactory.java`
- `application/src/main/java/org/thingsboard/server/service/queue/processing/SequentialByOriginatorIdTbRuleEngineSubmitStrategy.java`
- `application/src/main/java/org/thingsboard/server/service/queue/processing/SequentialByEntityIdTbRuleEngineSubmitStrategy.java`
- `application/src/test/java/org/thingsboard/server/service/queue/ruleengine/TbRuleEngineStrategyTest.java`

All files were read from the pinned `v4.3.1.1` tag at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

### Observed upstream semantics

- Latest state is persisted and read through the database Latest DAO; cache availability is not part of the correctness condition for a Latest read.
- SQL Latest updates are guarded by observation timestamp so an older sample cannot replace a newer Latest value.
- The upstream Latest DAO test explicitly verifies that saving an older timestamp does not advance the current Latest value.
- Queue submission can serialize work by originator/entity while allowing unrelated originators to progress independently.
- Processing retry has an explicit maximum retry count and capped backoff rather than an unbounded transient-failure loop.

### Implementation decision

- `ADOPT`: database-level monotonic Latest update semantics and bounded retry with capped backoff.
- `ADAPT`: PostgreSQL `device_observation_snapshots` remains the HVAC Current authority; Redis remains an optional rebuildable projection. MQTT processing is isolated by configured Gateway partition because the current adapter receives Gateway-scoped envelopes, while point/device source positions continue to own duplicate and ordering decisions in Telemetry Runtime.
- `ADAPT`: transient processing gets four total attempts (initial attempt plus three retries). A failed message is moved into bounded parking between attempts so the active worker can serve unrelated messages; exhausted retry, parking saturation, or active-queue saturation becomes an explicit dead disposition. Malformed or unauthorized messages use an explicit quarantine disposition.
- `REJECT`: PostgreSQL-read -> Redis-write -> Redis-read correctness coupling, unbounded retry, and one global processing worker that can indefinitely head-of-line block every Gateway.

### S06 consequence

Public Current single/batch reads return the PostgreSQL snapshot directly. Redis projection startup/materialization failure cannot make Current incorrect or prevent the authoritative service from starting. Accepted historical observations are retained while database timestamp guards prevent older samples from rolling Current backward. Duplicate/replay paths do not advance Business Revision. MQTT retry and parking are both bounded; parked failures vacate the active worker so unrelated devices can progress, terminal outcomes are observable as `dead`/`quarantined`, and configured Gateway partitions remain isolated.
