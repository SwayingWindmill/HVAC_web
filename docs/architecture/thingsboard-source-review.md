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
## S15 — shared Outbound Delivery ledger

Date: 2026-08-19

Local issue: #256

### Upstream files reviewed

The implementation-time review re-used the pinned source evidence already captured by the D09 integrations adjudication and re-read the REST/Notification delivery paths at `v4.3.1.1` / `c2a52e46c44e308ddee430e7266b8e10eddde9c4`:

- `rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/rest/TbRestApiCallNode.java`
- `rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/rest/TbHttpClient.java`
- `rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/rest/TbRestApiCallNodeConfiguration.java`
- `rule-engine/rule-engine-components/src/test/java/org/thingsboard/rule/engine/rest/TbHttpClientTest.java`
- `rule-engine/rule-engine-components/src/test/java/org/thingsboard/rule/engine/rest/TbRestApiCallNodeTest.java`
- `SsrfSafeAddressResolverGroup.java` and its tests/configuration surface recorded by `thingsboard-ai-analytics-integrations-adjudication.md`
- `common/data/src/main/java/org/thingsboard/server/common/data/notification/NotificationRequest.java`
- `dao/src/main/java/org/thingsboard/server/dao/model/sql/NotificationRequestEntity.java`
- `dao/src/main/java/org/thingsboard/server/dao/notification/DefaultNotificationRequestService.java`
- `application/src/main/java/org/thingsboard/server/service/notification/DefaultNotificationCenter.java`
- `application/src/main/java/org/thingsboard/server/service/notification/DefaultNotificationSchedulerService.java`
- `application/src/main/java/org/thingsboard/server/service/notification/rule/DefaultNotificationRuleProcessor.java`

### Observed upstream semantics

- REST external effects are asynchronous and expose bounded concurrency, timeout and response handling seams.
- The pinned REST implementation contains a DNS-rebinding-safe resolver and redirect rejection pattern; those are useful transport-boundary protections.
- Upstream REST protection is configuration-sensitive rather than a mandatory default-deny contract, and some defaults can be effectively unbounded. HVAC therefore cannot inherit the defaults unchanged.
- Rule-node Success/Failure is an execution result, not a durable business delivery ledger. There is no authoritative `Intent -> Attempt -> Receipt -> DeadLetter/ReplayApproval` chain that can explain a crash between provider effect and local completion.
- Notification Request persistence gives durable notification-request state and scheduling/deduplication concepts, but it is not sufficient evidence for a per-provider delivery effect or a safe replay of an outcome-unknown send.
- Static connector/provider credentials belong to configuration in the reference paths; that conflicts with the HVAC `CredentialRef` boundary.

### Implementation decision

- `ADOPT`: asynchronous external-I/O boundary, explicit timeout/concurrency limits, resolved-address SSRF protection, redirect rejection, provider request/receipt identifiers, and stable idempotency keys where providers support them.
- `ADAPT`: make egress protection default-deny, require an explicit destination allowlist, pin the actual dial to the addresses that passed validation, impose hard upper bounds on body/time/concurrency/attempts, and persist only bounded response evidence.
- `REPLACE`: direct Rule Node/provider effects with a shared durable owner that commits `DeliveryIntent` and a leased `DeliveryAttempt` before the external call.
- `REJECT`: blind retry after an outcome-unknown send, treating provider acceptance as confirmed delivery, plaintext credential material in business records, redirect following, runtime fallback to a legacy direct-send path, and editing historical attempts during replay.

### S15 consequence

The S15 owner persists immutable attempt/receipt/dead-letter/replay evidence under Tenant FORCE RLS. Automatic retry is allowed only for a result proven `NOT_SENT`; a crash/lease expiry or transport result that may have reached the provider becomes `OUTCOME_UNKNOWN` and requires explicit replay approval. Replay creates a new attempt number. The first adapter is REST/Webhook only; Notification product semantics remain S16 scope.

## S07 — Typed history/query and aggregation contract

Date: 2026-08-19

Local issue: #258

### Upstream files reviewed

- `common/data/src/main/java/org/thingsboard/server/common/data/kv/BaseReadTsKvQuery.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/kv/AggregationParams.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/AggregationTimeseriesDao.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/AbstractSqlTimeseriesDao.java`
- `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java`

All files were reviewed against the pinned ThingsBoard CE `v4.3.1.1` release at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`, not `master`.

### Observed upstream semantics

- A time-series read query carries an explicit aggregation policy, result `limit`, and `order`; result bounding is part of the query contract rather than an after-the-fact UI convention.
- Aggregation parameters distinguish fixed millisecond intervals from calendar intervals and carry a `ZoneId` for calendar interpretation.
- The DAO boundary accepts an explicit `ReadTsKvQuery` per key and returns bounded query results; aggregation remains a query concern rather than a mutation of Latest state.
- The upstream model supports typed KV values. Numeric aggregation therefore cannot be treated as the only historical representation.
- Upstream calendar/time-zone support is useful, but its generic per-key time-series identity is weaker than HVAC's required Observation/Point/Source identity and does not replace HVAC Counter reset/rollover semantics.

### Implementation decision

- `ADOPT`: explicit query limits/order, typed raw history, bounded aggregate result counts, and calendar aggregation driven by an explicit IANA time zone.
- `ADAPT`: raw HVAC history pages are ordered by `(telemetryKey, sampledAt, observationId)` and use an opaque cursor bound to the exact query scope plus a fixed projection snapshot. This preserves distinct same-timestamp Observations and makes repeated pagination stable while new rows are projected.
- `ADAPT`: HVAC returns Observation identity, acceptance status, Point/Sensor identity, Point revision, quality, and source position. `ACCEPTED` and valid `OUT_OF_ORDER` facts remain queryable even when they did not advance Current.
- `ADAPT`: aggregation is Point-type-aware: TELEMETRY uses gauge statistics, COUNTER uses reset/rollover/revision/unit/quality-aware deltas, and STATE returns typed last-state/change semantics. Site calendar buckets are evaluated in the requested IANA time zone, including DST boundaries.
- `REJECT`: numeric-only history, source offset masquerading as revision, static `datasetRevision`, `max(sampled_at)` as a projector watermark, fixed-millisecond day/month business boundaries, and compatibility fields for the superseded v1 History response.

### S07 consequence

The public History contract is version 2 and returns a flat typed Observation page plus `projectionWatermark` and `nextCursor`; no `series`, `maxPointsPerKey`, pseudo `revision`, `datasetRevision`, `dataWatermark`, `partial`, or `truncatedKeys` compatibility fields remain. Aggregate History is a separate governed route with explicit granularity, Site time zone and quality policy. Numeric charts consume NUMBER Observations only, while STRING/BOOLEAN/JSON facts remain available through the same raw History API.

## S18 — Registry administration UI and import/export

Date: 2026-08-19

Local issue: #264

### Upstream files reviewed

The implementation used a sparse local checkout of the pinned ThingsBoard CE `v4.3.1.1` release at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` and read the administration UI immediately before implementation:

- `ui-ngx/src/app/modules/home/pages/device/device.component.ts`
- `ui-ngx/src/app/modules/home/pages/device/devices-table-config.resolver.ts`
- `ui-ngx/src/app/modules/home/pages/device-profile/device-profiles-table-config.resolver.ts`
- adjacent Asset/Profile/entity administration components from the same pinned tree as needed for the table/action/import-export patterns.

### Observed upstream semantics

- ThingsBoard centralizes entity-list columns, fetch/save/delete functions and action enablement in reusable table configuration rather than letting every page invent its own entity lifecycle behavior.
- Device administration separates form edit state from the loaded server entity and explicitly marks profile changes dirty; administration actions are enabled according to scope/authority.
- Device Profile administration exposes create/import/export as explicit operator actions and refreshes the authoritative list after completion.
- ThingsBoard also exposes Customer/Public assignment, mutable Profiles and direct delete semantics. Those conflict with the HVAC typed Registry owner, immutable TemplateRevision and dependency-aware retirement model.

### Implementation decision

- `ADOPT`: a dedicated Registry administration workspace, server-backed entity tables, permission-gated operator actions, explicit import/export entry points, and protected dirty form state.
- `ADAPT`: use the generated HVAC owner contract with stable cursor/lazy Space loading and Device Point pagination; every update carries `expectedRevision`, and a stale Revision is shown as an explicit conflict that requires reloading authoritative data.
- `REPLACE`: mutable Profile editing with browser draft -> immutable `TemplateRevision` release -> new assignment interval. Rollback is another assignment to an already released Revision, never mutation of release history.
- `REPLACE`: direct relation/customer assignment with typed `rebind` kinds owned by Registry; direct delete with the dependency-aware retirement saga.
- `ADAPT`: import becomes owner-side dry-run -> immutable reviewed plan -> commit; controlled export is derived only from the authoritative SiteAssetModel and intentionally omits free-form Sensor/Point metadata and credential-bearing configuration.
- `REJECT`: Customer/Public sharing as Registry topology authority, arbitrary relation editing, browser-side topology reconstruction, hard-delete UI, mock/demo Registry fallback, and compatibility calls to superseded API shapes.

### S18 consequence

Real System Management now exposes a Registry workspace for Site/Space/Asset/Device/Point lifecycle operations, lazy Space hierarchy, Device Point lists, typed rebind, immutable Template release/assignment/rollback, dry-run/commit import, and controlled export. Write affordances follow the current Capability set, while the backend owner remains authoritative for IAM/Tenant/Site scope, revision conflict, binding rules, import validity and retirement dependencies. Dirty Registry drafts register with the existing Protected Scope so Site switching cannot silently discard operator edits.

## S13 — Alarm orthogonal aggregate migration

Date: 2026-08-19

Local issue: #265

### Upstream files reviewed

- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/Alarm.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/AlarmStatus.java`
- `dao/src/main/java/org/thingsboard/server/dao/alarm/BaseAlarmService.java`
- `dao/src/main/resources/sql/schema-functions.sql`
- `dao/src/test/java/org/thingsboard/server/dao/service/AlarmServiceTest.java`
- `application/src/test/java/org/thingsboard/server/cf/AlarmRulesTest.java`
- `application/src/test/java/org/thingsboard/server/edge/AlarmEdgeTest.java`

All files were read directly from the pinned ThingsBoard CE `v4.3.1.1` checkout at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before S13 implementation.

### Observed upstream semantics

- ThingsBoard stores `acknowledged` and `cleared` as independent Alarm facts and derives the four display statuses (`ACTIVE_UNACK`, `ACTIVE_ACK`, `CLEARED_UNACK`, `CLEARED_ACK`) from those facts. ACK therefore does not clear the physical condition, and clear does not imply acknowledgement.
- ACK and clear paths lock the existing Alarm row and are naturally idempotent when the corresponding fact is already present.
- Active-Alarm create/update first queries the business condition and then inserts when absent. The pinned implementation does not provide a database uniqueness boundary that makes two simultaneous first creates converge on one active Alarm.
- Upstream keeps Alarm severity on the Alarm aggregate and tests stateful Alarm rule behavior, but its generic entity model does not provide the HVAC fingerprint, current/peak severity pair, Site-scoped incident correlation, or Work Order linking semantics required by D06.

### Implementation decision

- `ADOPT`: acknowledgement and clear as orthogonal facts; naturally idempotent ACK/clear behavior; transactional locking when mutating an existing Alarm Incident; durable Alarm history/evidence rather than deriving state only in the UI.
- `ADAPT`: replace the derived four-value status as the canonical model with explicit `condition=ACTIVE|CLEARED`, independent acknowledgement/suppression/assignment, `currentSeverity` plus monotonic `peakSeverity`, stable SHA-256 HVAC fingerprint, immutable timeline, and explicit Incident correlation/link evidence.
- `ADAPT`: close the upstream simultaneous-first-create gap with a PostgreSQL partial unique index on `(tenant_id, site_id, fingerprint) WHERE condition='ACTIVE'` plus conflict convergence. Recovery is bound to both the fingerprint and the specific Incident correlation, so a delayed clear from an older Incident cannot clear a later recurrence. Recovery removes the active uniqueness claimant; a later recurrence creates a new Alarm ID and Incident correlation while retaining the same fingerprint.
- `REJECT`: arbitrary operator Close/Reopen, Work Order completion as a clear signal, runtime dual Alarm models, first-create correctness based only on application `SELECT` followed by `INSERT`, and compatibility status fields that can diverge from physical condition facts.

### S13 consequence

The runtime Alarm aggregate is schema version 2 and no longer contains `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED`, `CLOSE`, `REOPEN`, or JSON transition-state compatibility fields. PostgreSQL owns one-active-per-fingerprint uniqueness, Tenant FORCE RLS remains mandatory, system timeline rows are append-only, and operator ACK/suppression cannot mutate the physical condition. The one-shot migration creates pre-S13 aggregate/idempotency backups, an Incident identity map and migration report, rejects legacy `REOPEN` or duplicate-active fingerprint history that must first be reconciled, then removes the retired runtime columns rather than operating old and new models together. S14 remains responsible for evaluator policy and clear-predicate execution; S13 only provides the governed `Publish` / `ClearActive` owner seams.

### S13 verification evidence

- PostgreSQL 16 upgrade test with a populated pre-S13 Alarm (ACK + suppression + three legacy transitions + idempotency row) migrated to the orthogonal aggregate with `1 -> 1` identity preservation and three immutable timeline rows; the offline rollback restored the original `MAJOR|SUPPRESSED|v3` aggregate, all three legacy transitions and the idempotency row, then removed the S13-only tables/columns.
- A clean PostgreSQL 16 runtime initialized through migrations `001`–`005` plus the canonical seed passed the Alarm Service PostgreSQL integration suite, including simultaneous first create convergence, recurrence, stale old-Incident recovery rejection and Tenant RLS.
- The append-only database trigger was exercised directly: an `UPDATE` against `alarm_timeline` failed with `alarm timeline is append-only`.

## S09 — Transport retry, Session, Binding and Credential lifecycle

Date: 2026-08-19

Local issue: #263

### Upstream files reviewed

- `common/transport/transport-api/src/main/java/org/thingsboard/server/common/transport/service/DefaultTransportService.java`
- `common/transport/mqtt/src/main/java/org/thingsboard/server/transport/mqtt/session/AbstractGatewaySessionHandler.java`
- `dao/src/main/java/org/thingsboard/server/dao/device/DeviceCredentialsServiceImpl.java`

All files were read from ThingsBoard CE `v4.3.1.1` at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

### Observed upstream semantics

- Transport authentication and Session registration are explicit service concerns. Session identifiers are registered/deregistered independently from telemetry persistence, and credential changes can trigger transport-side session invalidation.
- Gateway Sessions maintain child-device connection state and can rebuild that state after reconnect. This is useful for restart recovery, but the pinned Gateway path also supports `GetOrCreateDeviceFromGateway`, where an unknown child may become a new Device identity as a side effect of transport traffic.
- Device Credentials are first-class records with explicit create/update/delete notification paths. Upstream can store credential material directly according to credential type; that storage shape is broader than the HVAC Secret boundary.
- Transport acknowledgements and session existence do not prove the HVAC business command effect. The upstream transport/session machinery therefore cannot replace Command Attempt fencing, durable connector evidence, or authoritative readback verification.

### Implementation decision

- `ADOPT`: explicit Transport/Session ownership, restart-rebuildable Session state, Credential lifecycle notifications/invalidation, and a single transport boundary serving telemetry and command capabilities.
- `ADAPT`: Connectivity owns `TransportProfile`, `IntegrationInstance`, `CredentialRef`, `DeviceBinding`, `GatewayChildBinding`, one-time `Enrollment`, logical Gateway `Session`, connector ownership leases, and durable command-reply correlation. These are Tenant-RLS owner records rather than static process configuration.
- `ADAPT`: the current single `iot-service` process keeps telemetry ingress and command delivery as separate fault domains. Telemetry failure does not terminate the command loop; command initialization or ownership loss does not terminate telemetry. Each module exposes independent readiness.
- `ADAPT`: MQTT command publishing uses a durable `PREPARED -> MAY_COMMIT -> REPLIED -> RESOLVED` correlation. `MAY_COMMIT` is persisted before calling the MQTT client. After that point a crash or ambiguous publish result is never automatically republished; a persisted late reply is reused after restart.
- `ADAPT`: `CredentialRef` persists only a SecretRef, certificate fingerprint, or one-way token hash plus revision/validity/lifecycle state. Revocation and expiry invalidate logical Sessions, and both uplink and command routing require an active Session backed by an active CredentialRef. Broker TLS client-certificate enforcement remains the network authentication boundary; the application Session is the immediate authorization gate after a credential is revoked.
- `REJECT`: `GetOrCreateDeviceFromGateway`, unknown-child auto-registration, plaintext/recoverable credential values, static Device-to-external-ID routing maps, in-memory fence/result authority, and a second standalone command transport path.

### S09 consequence

MQTT traffic can only resolve a pre-registered Registry Device through an active Connectivity Binding; unknown Gateway children are quarantined and no transport code can insert Registry identity. Enrollment can only consume an unexpired one-time challenge for a Device that already has an active Binding. Connector ownership carries a durable generation/lease, command replies survive process restart, and the only state from which a physical publish may occur is `PREPARED`. Local central-plant bootstrap now seeds the same Connectivity owner model using the simulator certificate fingerprint while keeping private-key material outside the database. Additional protocols remain deferred; S09 establishes the MQTT-first lifecycle and recovery boundary only.

## S14 — Stateful Alarm evaluator

Date: 2026-08-19

Local issue: #272

### Upstream files reviewed

- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/rule/AlarmRule.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/rule/condition/AlarmCondition.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/rule/condition/DurationAlarmCondition.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/alarm/rule/condition/RepeatingAlarmCondition.java`
- the numeric/no-data/complex condition and schedule classes adjacent to that condition package
- `application/src/main/java/org/thingsboard/server/service/cf/ctx/state/alarm/AlarmRuleState.java`
- `application/src/test/java/org/thingsboard/server/cf/AlarmRulesTest.java`
- `rule-engine/rule-engine-components/src/test/java/org/thingsboard/rule/engine/profile/AlarmRuleStateTest.java`

The files were read directly from the official ThingsBoard repository at pinned CE `v4.3.1.1` commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before S14 implementation.

### Observed upstream semantics

- Alarm rule conditions distinguish simple, duration and repeating behavior. Duration retains the first matching time and schedules the remaining delay; repeating rules count matching new events and reset their accumulated state when the condition becomes false.
- Stateful Alarm rule evaluation explicitly records event count/first-event time/last-check time and supports scheduled reevaluation. Rule-expression/count changes discard state that is no longer valid for the new rule.
- Alarm schedules are interpreted through Java time-zone-aware `ZoneId`/`ZonedDateTime` local windows, including windows that cross midnight. Upstream tests exercise changing schedules, duration/repeat state and no-data evaluation.
- The pinned runtime schedules reevaluation with process-local scheduled futures. That is useful execution machinery, but it is not durable restart authority and does not provide the database lease/fence semantics required by the HVAC roadmap.

### Implementation decision

- `ADOPT`: simple/duration/repeating trigger semantics, reset of incompatible accumulated state, explicit scheduled reevaluation, and IANA time-zone/site-local schedule evaluation.
- `ADAPT`: evaluate typed compare/range/hysteresis/no-data/stale/AND/OR predicates against owner snapshots and return the three-state `MATCHED | NOT_MATCHED | INDETERMINATE` result required by the HVAC Alarm architecture. Canonical Telemetry quality (`GOOD/PARTIAL/ESTIMATED/MANUAL/STALE/INVALID`) gates ordinary predicates; missing, stale, invalid or otherwise untrusted input cannot falsely clear an Active Incident.
- `ADAPT`: policy changes are immutable `AlarmPolicyRevision` plus append-only assignment revisions. Duration/repeat candidates reset on a policy revision switch while an already active S13 Incident remains correlated until the new explicit clear predicate proves recovery.
- `REPLACE`: process-local scheduled futures with durable PostgreSQL `nextEvaluationAt`, persisted snapshot/state, `FOR UPDATE SKIP LOCKED` claim, expiring lease and monotonically increasing fence. A new owner snapshot or committed state version invalidates an older timer claim.
- `ADAPT`: S14 effects execute through the existing S13 `Publish` / Incident-bound `Clear` implementation in the same Alarm owner transaction as evaluation-state/evidence persistence, eliminating a crash window between effect and state commit.
- `REJECT`: arbitrary scripts/dynamic untyped predicates, browser evaluation authority, Work Order completion as recovery, untrusted-quality false clear, process-local timer state as restart authority, Close/Reopen compatibility, and a second Alarm effect implementation.

### S14 consequence

Alarm evaluation is now an Alarm-owned durable state machine. Released policy content is SHA-256 bound to its canonical payload, policy/assignment revisions are append-only and contiguous, and rollback is a new assignment revision to a previously released policy. The owner release seam prevents one policy family from changing the fingerprint identity or one assignment stream from changing subject/policy family. Timer work is Tenant-RLS scoped and lease/fence protected, and each committed evaluation appends evidence containing the exact policy/assignment/input revision and snapshot. S13 remains the sole Alarm Incident authority; evaluator state and Incident Publish/Clear are committed together, so restart/retry cannot turn a timer replay into a duplicate business effect.

### S14 verification evidence

- A clean PostgreSQL 16 runtime initialized through Alarm migrations `001`–`006` plus canonical seed passed the complete Alarm Service integration suite, including duration restart, distinct-repeat counting, no-data scheduling, stale/invalid/missing false-clear blocking, DST schedule behavior, timer supersession, lease expiry/reclaim, S13 clear/recurrence and Tenant RLS.
- The runtime owner seam released policy revision 1, appended revision 2, switched the assignment, then rolled back by appending assignment revision 3 to revision 1; each switch reset incompatible duration state instead of inheriting a prior candidate.
- Direct database checks proved all four S14 tables use FORCE RLS; runtime may INSERT immutable releases/assignments but may not UPDATE/DELETE them, and the immutable trigger rejects mutation even under the migrator role.
## S19 — Work Order / Settlement / Cost projections

Date: 2026-08-19

Local issue: #274

### Upstream files reviewed

- `common/data/src/main/java/org/thingsboard/server/common/data/relation/EntityRelation.java`
- `common/dao-api/src/main/java/org/thingsboard/server/dao/relation/RelationService.java`
- `dao/src/main/java/org/thingsboard/server/dao/relation/BaseRelationService.java`
- `common/dao-api/src/main/java/org/thingsboard/server/dao/alarm/AlarmService.java`
- `dao/src/main/java/org/thingsboard/server/dao/alarm/BaseAlarmService.java`
- Alarm DAO/service tests that explicitly invoke `clearAlarm(...)` through the Alarm owner service.

All files were read from ThingsBoard CE `v4.3.1.1` at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before S19 implementation.

### Observed upstream semantics

- Entity relationships are explicit tenant-scoped relation records with a from/to identity and typed relationship; relation persistence does not silently mutate the lifecycle of either related entity.
- Alarm clear/recovery is an explicit Alarm-service operation. Upstream tests clear an Alarm by invoking the Alarm owner service rather than by completing an unrelated linked entity.
- ThingsBoard CE has no equivalent of the HVAC Work Order aggregate with the local assignment/task/evidence/versioned lifecycle contract, and it has no equivalent of the HVAC Settlement/Cost accounting model.

### Implementation decision

- `ADOPT`: explicit typed relationship semantics and the rule that Alarm recovery remains owned by the Alarm domain.
- `ADAPT`: the existing Work Order `SourceReference` is the formal Alarm link. S19 makes authoritative source IDs UUIDv7 at the database boundary, removes the stale `EQUIPMENT` source-domain value, adds reverse Alarm-link lookup, and keeps source links append-only. Work Order completion/reopen changes only the Work Order aggregate and preserves the Alarm link unchanged.
- `ADAPT`: Settlement remains a local accounting domain. Existing immutable `settlement_snapshots` stay the history authority; S19 adds an explicit PostgreSQL Current projection carrying dataset revision, source Metric revisions, source watermark, missing bindings, quality/completeness and cost. Change Candidate identity is deduplicated by base Snapshot plus calculation digest.
- `REJECT`: Work Order completion clearing/recovering Alarm state, generic free-form relationship mutation, accidental latest-row selection as Settlement Current authority, and silently treating a missing released Metric binding as complete data.

### S19 consequence

Work Order remains independently operable while Alarm links are formal, immutable relationship evidence. Settlement/cost recomputation becomes traceable to exact Metric revisions and a source watermark; missing or partial inputs remain visible in quality/completeness; immutable Snapshot history can rebuild the Current projection. Phase1 now includes the canonical `009a` topology/metering, `009c` Metric, and `009b` Settlement foundation in the dependency order proven by a fresh PostgreSQL install.
