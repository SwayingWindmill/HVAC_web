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

## S11 — Governed Cloud Command -> Edge -> readback chain

Date: 2026-08-19

Local issue: #270

### Upstream files reviewed

- `application/src/main/java/org/thingsboard/server/controller/RpcV2Controller.java`
- `application/src/main/java/org/thingsboard/server/actors/device/DeviceActorMessageProcessor.java`

Both files were read from ThingsBoard CE `v4.3.1.1` at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before S11 implementation.

### Observed upstream semantics

- A successful one-way RPC HTTP response means the request was sent to the device transport; it does not prove the physical effect.
- Persistent RPC has explicit `QUEUED`, `SENT`, `DELIVERED`, `SUCCESSFUL`, `TIMEOUT`, `EXPIRED`, and `FAILED` lifecycle states. A device response can make the RPC `SUCCESSFUL`, but that remains RPC completion rather than independent plant-state proof.
- Expiry, retry and restored device sessions are transport concerns. They are valuable for delivery recovery, but they do not replace HVAC execution fencing or authoritative readback.

### Implementation decision

- `ADOPT`: explicit transport delivery states, expiry handling and restart-recoverable RPC/session state.
- `ADAPT`: Edge persists a `MAY_EXECUTE` commit point before scheduling a physical action and records structured requested/effective/applied/constraint/winner/cycle evidence. A restart from `MAY_EXECUTE` returns `EDGE_OUTCOME_UNKNOWN` and never blindly repeats the physical action.
- `ADAPT`: an Edge `EXECUTED` reply with valid structured execution evidence may advance the Cloud Attempt only to `ACKNOWLEDGED`; numeric verification targets the governed Edge applied/effective value while action capabilities retain their semantic readback target such as `RUNNING` or `STOPPED`.
- `ADAPT`: explicit Edge rejection/expiry is a proven non-execution failure, while write failure or timeout remains `OUTCOME_UNKNOWN` because absence of physical effect cannot be proven.
- `REJECT`: HTTP 200, MQTT/device ACK, RPC `DELIVERED`, RPC `SUCCESSFUL`, or an Edge-declared `VERIFIED` status as Cloud business success. `SUCCEEDED` remains exclusive to fresh authoritative S2 State/Telemetry readback after acknowledgement.

### S11 consequence

Cloud Intent, approval, authorization, idempotency, lease and execution-fence facts remain immutable Cloud authority. Edge Scheduler/Arbiter/Interlock may constrain or reject the requested command without rewriting those facts. Connector and Connectivity persistence carry Edge execution evidence across restart, and the verifier reloads that durable evidence before independent S2 readback. A transport acknowledgement without governed Edge execution evidence cannot advance the Command, and ambiguous execution paths remain frozen as `OUTCOME_UNKNOWN` rather than being retried blindly.

## S17 — SiteDashboardSummary and BigScreen truthfulness

Date: 2026-08-19

Local issue: #273

### Upstream files reviewed

The implementation read these files directly from the pinned ThingsBoard CE `v4.3.1.1` checkout at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` immediately before implementation:

- `common/data/src/main/java/org/thingsboard/server/common/data/Dashboard.java`
- `application/src/main/java/org/thingsboard/server/controller/DashboardController.java`
- `ui-ngx/src/app/shared/models/dashboard.models.ts`
- `ui-ngx/src/app/core/api/alias-controller.ts`
- `ui-ngx/src/app/core/api/widget-subscription.ts`

### Observed upstream semantics

- ThingsBoard models Dashboard state, layout and time window explicitly, and resolves datasource aliases separately from the stored Dashboard definition.
- Alias changes invalidate prior resolutions and re-resolve affected aliases; Dashboard state changes invalidate only state-bound aliases instead of silently retaining stale entity targets.
- Widget subscriptions have an explicit start/update/unsubscribe lifecycle. Unsubscribe stops Entity/Alarm subscriptions, clears listener state and releases the subscribed state.
- The generic `DashboardConfiguration` intentionally remains open-ended and can carry arbitrary widget/configuration fields. That flexibility is useful for a low-code platform but is broader than the fixed first-party HVAC operations product boundary.

### Implementation decision

- `ADOPT`: explicit presentation state/time context, bounded subscription lifecycle, deterministic cleanup on navigation/scope change, and re-resolution/reconciliation when the presentation target changes.
- `ADAPT`: replace generic alias/widget datasource resolution with one versioned `SiteDashboardSummary` projection scoped by the authenticated Tenant and validated Site. Every meaningful value carries explicit source/quality/watermark semantics and Site local-calendar calculations use the Registry IANA timezone.
- `REPLACE`: browser-side Site truth reconstructed from sampled Device lists, Presence batches and rolling 24-hour Energy queries with a server-side Presentation projection assembled only through authorized owner query ports. Dashboard and BigScreen consume the same summary contract/query identity.
- `REPLACE`: an unconditional visual “Live/healthy” interpretation with explicit `READY/ATTENTION/NO_DATA/PARTIAL/STALE/SUSPECT/UNAVAILABLE/NOT_AUTHORIZED/NOT_INTEGRATED` states. `ATTENTION` means complete data with known operational concern; incomplete or unknown Device population never publishes a Site availability denominator or percentage.
- `REJECT`: arbitrary Dashboard/Widget JavaScript/HTML/CSS/resources, Presentation-owned business facts or authorization, direct control actions, Demo/Mock fallback in Real mode, and fixed 24-hour arithmetic for a Site-local calendar day.

### S17 consequence

Presentation becomes a logical owner of derived presentation projections only; Registry, Telemetry, Metric/Analytics, Alarm, Command and authorization facts remain with their existing owners. Phase 1 may physically co-locate the projection with `energy-api/platform-gateway`, but the Summary implementation consumes owner query ports rather than write/read owner schemas directly. Live presentation uses a bounded `text/event-stream` replacement stream only after a REST `SiteDashboardSummary` handshake: the browser supplies that Snapshot's `generatedAt` as `baseGeneratedAt`, accepts an owner-issued replacement only when the base still matches its current Snapshot, and performs a fresh REST reconciliation before reopening the stream after disconnect, malformed data or base mismatch. The Gateway revalidates the durable BFF Session on the configured revocation objective independently of the lower-frequency Summary refresh, so stream lifetime cannot weaken Session revocation. The stream is recovery acceleration, not a new durable fact authority or a browser-side recomputation path.

## S20 — Rule Runtime core

Date: 2026-08-19

Local issue: #280

### Upstream files reviewed

All implementation-time source was re-read from ThingsBoard CE `v4.3.1.1` at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`:

- `dao/src/main/java/org/thingsboard/server/dao/service/validator/RuleChainDataValidator.java`
- `dao/src/test/java/org/thingsboard/server/dao/service/validator/RuleChainDataValidatorTest.java`
- `application/src/main/java/org/thingsboard/server/actors/ruleChain/RuleNodeActorMessageProcessor.java`
- `application/src/main/java/org/thingsboard/server/service/queue/ruleengine/TbRuleEngineQueueConsumerManager.java`
- `application/src/test/java/org/thingsboard/server/service/queue/ruleengine/TbRuleEngineStrategyTest.java`

### Observed upstream semantics

- Rule Chain validation rejects invalid entity metadata and direct same-chain loops before execution; Rule Node construction/configuration is class/reflection based and node lifecycle is explicit through init/destroy/re-init.
- Rule Node execution supports a per-message execution count guard, but an upstream value of zero means unlimited execution rather than a hard production bound.
- Queue processing separates submit strategy from processing/ack strategy. Burst, Batch and sequential-by-originator execution are explicit behaviors, and the upstream tests exercise failure/timeout retry decisions for each strategy.
- Queue commit happens only after the processing strategy decides the current pack may commit. A failed/timeout pack may be reprocessed according to queue policy.
- Upstream Rule Engine message/node identities and mutable chain/configuration lifecycle are not sufficient evidence for deterministic replay, immutable release pinning or crash-safe exactly-identifiable HVAC owner effects.

### Implementation decision

- `ADOPT`: publish-time graph validation, explicit typed node lifecycle, separation of execution from acknowledgement, and an ordering seam equivalent to sequential-by-originator for the same business subject.
- `ADAPT`: replace class/reflection nodes with a closed versioned `NodeDefinition` catalog; replace generic relation strings with typed input/output ports; replace mutable active Rule Chain configuration with immutable released `RuleRevision` plus append-only `RuleBinding` revisions.
- `REPLACE`: random/ephemeral execution identity with deterministic `executionId`, `workItemId`, `effectId` and `continuationId`; pack-level retry with bounded per-work retry and terminal `DEAD`/`QUARANTINED`; in-memory delay with a durable continuation record; node-owned external/domain writes with persisted typed Effect Intent addressed to the owner domain.
- `REJECT`: arbitrary JS/TBEL/class loading, credential reads, direct database/network/owner mutation from Rule nodes, zero-as-unlimited attempts, blind replay of ambiguous effects, and debug/replay paths that can call the real Effect Sink.

### S20 consequence

The S20 runtime compiles a released immutable plan before execution, pins each execution to the exact Rule Revision and Binding revision, and serializes the same Tenant/Site/subject ordering key under lease/fence. Authoritative Rule State is separate from any one Execution and is keyed by Tenant + Rule Revision + Node Instance + Scope Key; CAS updates and the corresponding Execution transition evidence commit in one storage transaction, so concurrent executions cannot overwrite each other. Node evaluation is pure with respect to business owners: owner reads go through exact snapshot ports and owner writes become stable Effect Intents. Work, continuation, state-transition, effect and trace evidence are durable at execution boundaries. Before an owner call, an Effect is durably marked `DISPATCHING`; if the process dies before the receipt is persisted, restart freezes that Effect as `AMBIGUOUS` instead of blindly replaying it. Replay accepts frozen snapshot facts only, cannot accept a live Effect Sink, and records simulated effects without invoking production owners.
## S21 — Rule management UI

Date: 2026-08-20

Local issue: #286

### Upstream files reviewed

The implementation re-read the official ThingsBoard source from CE `v4.3.1.1` at commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before building the Rule management surface:

- `ui-ngx/src/app/modules/home/pages/rulechain/rulechain-page.component.ts`
- `dao/src/test/java/org/thingsboard/server/dao/service/validator/RuleChainDataValidatorTest.java`
- the pinned S20 source set already recorded above for `RuleChainDataValidator`, Rule Node execution and queue processing, because S21 manages the same execution artifact rather than introducing a second Rule model.

### Observed upstream semantics

- The Rule Chain page is a real visual graph editor: it maintains a node library, flowchart callbacks, selected-node editing, graph validation and an explicit dirty flag. Model changes mark the page dirty and validation can identify invalid nodes before save.
- Rule Node configuration is edited through node-specific forms, and the UI has dedicated debug/test affordances. This is preferable to asking operators to edit one opaque Rule JSON document.
- Saving upstream Rule Chain metadata is a mutable operation with version-conflict handling. The generic platform also permits component/class-driven nodes, scripting-oriented nodes and dynamic link labels, which are intentionally broader than this product's governed automation boundary.
- The upstream validator test confirms that validation is a server-side concern rather than a browser-only decision. S20's pinned runtime source further shows why execution safety, retries and effects must remain in the Rule owner rather than in the graph editor.

### Implementation decision

- `ADOPT`: visual catalog-driven graph construction, explicit node/edge editing, dirty-state protection, server-side validation before publication, and read-only execution/debug evidence as operator feedback.
- `ADAPT`: the node library is exactly the S20 `core.v1` typed `NodeDefinition` catalog. Each node exposes only descriptor-declared typed configuration fields, and required owner permissions are derived from catalog metadata instead of being freely typed by the browser.
- `REPLACE`: mutable Rule Chain save becomes browser draft -> Rule Runtime compile/validate -> immutable `RuleRevision` release -> append-only `RuleBinding` assignment. Rollback is another binding revision targeting a previous immutable release, not mutation of released content.
- `ADAPT`: ThingsBoard's test/debug idea becomes S20 `ModeReplay` simulation over an in-memory execution store and optional frozen owner facts. The replay runtime receives no live Effect Sink, so an effect-capable graph records `SIMULATED` evidence but cannot mutate Alarm or another owner.
- `ADAPT`: management authorization is the explicit `rule.manage` capability plus authoritative Registry Site visibility and normal BFF CSRF protection. Rule ID is a Tenant-local resource selector, not a new authorization scope dimension.
- `REJECT`: raw component/class names, arbitrary JavaScript/TBEL or other executable scripts, free-form credential fields, browser-side permission invention, direct owner/database/network effects from the editor, Demo/Mock Rule fallback in Real mode, and UI claims that trace/dead/quarantine state is authoritative without reading S20 owner evidence.

### S21 consequence

Rule Runtime remains the single logical owner of Rule revisions, bindings, simulation semantics and execution evidence. Phase 1 physically co-locates the management adapter in the Platform Gateway process, but persistence and lifecycle code live in the `rule-runtime-service` module and connect under the Rule Runtime RLS identity; route and data ownership registries still name `rule-runtime-service` as the owner. The Gateway contributes only BFF Session/capability/CSRF checks, Registry-backed Site visibility and public-contract adaptation. The Real system page consumes generated Rule APIs only, protects unsaved drafts, and exposes validate/diff/test/simulate/release/assign/rollback/retire plus Site-scoped execution evidence without creating a second Rule authority.

## S12 — Edge Fleet, sync, Desired Config and signed OTA

Date: 2026-08-19

Local issue: #279

### Upstream files reviewed

ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`:

- `application/src/main/java/org/thingsboard/server/service/edge/rpc/EdgeSyncCursor.java`
- `common/edge-api/src/main/proto/edge.proto`

ThingsBoard Edge `v4.3.1.1`, commit `04d9daf4a557a13483de2310e35b6493aff751fc`:

- `application/src/main/java/org/thingsboard/server/service/cloud/event/postgres/PostgresCloudEventUplinkRetriever.java`

The files above were read directly from the pinned official repositories before S12 implementation.

### Observed upstream semantics

- ThingsBoard models Edge connection/version/max-message-size negotiation and uses typed bidirectional Edge messages with explicit response identifiers. This gives a useful transport/session boundary instead of treating reconnect as an untyped socket event.
- Full synchronization is driven through an ordered cursor of typed fetchers, while incremental Edge events are durably retained in PostgreSQL and retrieved from a persisted queue position. The upstream event retriever also contains explicit timestamp/sequence compensation logic for imperfect event ordering.
- The pinned `edge.proto` still permits a routing-key/secret connection shape, a boolean `fullSync` request and deprecated compatibility fields. The full-sync cursor itself is process-local and does not provide an immutable snapshot revision, chunk digest, resumable staging set or atomic activation contract.
- Upstream delivery acknowledgement proves replication progress, not HVAC owner authority. It therefore cannot justify Cloud and Edge both mutating the same governed field.

### Implementation decision

- `ADOPT`: typed bidirectional sync messages, explicit Edge runtime/version/capability/max-payload negotiation, durable incremental delivery records, acknowledgement identities and a retained reconnect cursor.
- `ADAPT`: MQTT+mTLS/`CredentialRef` remains the transport identity boundary established by S09. Fleet handshake additionally binds the active logical Session and credential revision; no second static Edge secret is introduced.
- `ADAPT`: replace boolean full-sync with immutable `DesiredEdgeState -> EdgeRelease -> SnapshotRevision`, digest-bound chunks, resumable staging, tombstones and atomic activation. Reconnect chooses resumable snapshot, full snapshot or delta from durable revision/cursor evidence.
- `ADAPT`: delivery items carry one Cloud owner domain, ordering key, owner revision, payload digest and tombstone. Independent keys may apply while a bad item is quarantined, but the contiguous committed cursor stops until an explicit governed disposition closes that gap.
- `REJECT`: process-local full-sync cursor as durability authority, static routing secret, deprecated version fallbacks, silent cursor advance across quarantine, generic entity replication and Cloud writes to Edge-owned observed/control/telemetry/audit fields.

### S12 consequence

`connectivity` now owns durable EdgeNode/enrollment/identity binding/handshake, immutable signed EdgeRelease and Snapshot facts, Desired/Observed state, sync/cursor/quarantine state and signed OTA campaign facts. The Edge-side `libs/edgefleet` Replica keeps staging separate from active state, resumes interrupted snapshots from disk, enforces one-writer owner domains, and retains `ACTIVE/STAGED/PREVIOUS` rollback evidence. Signed release/OTA verification occurs locally against trusted public keys; private signing keys and recoverable credentials never enter business persistence.

## S16 — Notification minimum product loop

Date: 2026-08-19

Local issue: #277

### Upstream files reviewed

- `common/data/src/main/java/org/thingsboard/server/common/data/notification/rule/trigger/AlarmTrigger.java`
- `application/src/main/java/org/thingsboard/server/service/notification/rule/trigger/AlarmTriggerProcessor.java`
- `common/data/src/main/java/org/thingsboard/server/common/data/notification/rule/EscalatedNotificationRuleRecipientsConfig.java`
- `dao/src/main/java/org/thingsboard/server/dao/notification/DefaultNotifications.java`
- `application/src/main/java/org/thingsboard/server/service/notification/rule/DefaultNotificationRuleProcessor.java`
- `application/src/main/java/org/thingsboard/server/service/ws/notification/DefaultNotificationCommandsHandler.java`

All files were read directly from the official ThingsBoard repository at pinned CE `v4.3.1.1` commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` before S16 implementation.

### Observed upstream semantics

- Alarm notification rules distinguish `CREATED`, `SEVERITY_CHANGED`, `ACKNOWLEDGED` and `CLEARED` trigger types instead of treating every Alarm occurrence as a notification event.
- Escalated recipient configuration is an ordered delay table. The rule processor freezes the target/template choice into a scheduled request and executes later stages after the configured delay.
- A clear-triggered rule may remove still-unsent scheduled notification requests. This correctly stops future escalation, but deletion does not preserve the durable cancellation evidence required by the HVAC architecture.
- In-App notification read state is stored and mutated independently from the source Alarm state. Marking a notification read is not an Alarm acknowledgement.
- The upstream Notification processor owns delivery-method execution and related retry behavior. That responsibility conflicts with the already established HVAC S15 outbound-delivery owner.

### Implementation decision

- `ADOPT`: explicit Alarm lifecycle trigger types, ordered delayed escalation, frozen notification content/recipient resolution before delayed execution, and Notification-local unread/read state.
- `ADAPT`: Alarm writes an immutable, version-bound Notification outbox row in the same Alarm owner transaction for CREATED, real severity change, first ACK and Clear. A lease/fence relay transfers that owner event into Notification without Notification reading Alarm tables.
- `ADAPT`: scheduled future stages are changed to durable `CANCELLED` intents rather than deleted rows. ACK/Clear can cancel `SCHEDULED` or already `CLAIMED` future stages; the old worker loses its fence, making the race explainable from database state.
- `ADAPT`: `AudienceRevision`, `TemplateRevision` and `NotificationPolicyRevision` are SHA-256-bound immutable releases. Each source-event/assignment-revision/stage has one durable Notification Intent containing the frozen recipient and rendered template snapshot. A database trigger makes those snapshot fields immutable after insert, and scheduler/runtime roles receive column-level UPDATE rights only for lifecycle/lease fields.
- `ADAPT`: ordinary user preference can suppress advisory stages only. `mandatorySafety=true` bypasses the ordinary preference table, so a safety notification cannot be opted out by the recipient.
- `REPLACE`: external EMAIL/REST execution and provider retry are delegated to the S15 outbound-delivery owner. Notification commits an `EXTERNAL_SUBMITTED` handoff before invoking S15 and uses the Notification Intent ID as the S15 idempotency key; restart recovery reuses that same business identity. S15 `OUTCOME_UNKNOWN` remains `OUTCOME_UNKNOWN` at the Notification business layer.
- `REJECT`: direct provider SDK/network sends from Notification, Notification reads of `alarm_runtime`, deleting cancelled stage history, browser-selected principal IDs, Notification read implicitly ACKing Alarm, and a second provider retry/dead-letter implementation.

### S16 consequence

Notification is now an independently owned durable business domain in `notification_runtime`. The public product surface is intentionally minimal: the authenticated principal can list their own Inbox and mark one item read through Gateway Session + Origin/CSRF + signed exact principal/item scope. Policy/Audience/Template management remains an internal owner seam in S16 rather than an unreviewed public administration API. Alarm remains the only Alarm condition/ACK authority, and S15 remains the only external delivery attempt/receipt authority.

### S16 verification evidence

- A clean PostgreSQL 16 `notification_runtime` initialized from the S16 migration passed replay, frozen-recipient/template, delayed-stage cancellation, mandatory-safety preference, cross-Tenant scheduler and external handoff recovery tests. All nine Notification tables use FORCE RLS; the scheduler can update only lifecycle/lease columns on `notification_intent` and cannot read Inbox rows.
- A separate PostgreSQL 16 Alarm database initialized through migrations `001`–`007` proved exactly four business source events for create -> unchanged occurrence -> severity change -> ACK -> clear: `CREATED`, `SEVERITY_CHANGED`, `ACKNOWLEDGED`, `CLEARED`. The outbox relay reclaimed expired work with a higher fence and rejected stale completion.
- Direct mutation of released Notification policy or the frozen Notification Intent recipient/template/body snapshot is rejected by database triggers. Notification source code contains no `alarm_runtime` access; Alarm-to-Notification transfer is through the dedicated outbox relay seam, and external disposition is read through the S15 owner store seam.

## S24 — Retire Legacy/Shadow/Simulator production wiring

Date: 2026-08-20

Local issue: #295

S24 introduces no new upstream behavior to copy. It is a retirement slice whose source-first inputs are the already-pinned S05/S09/S11/S12 owner implementations plus the repository's completed Registry, Telemetry and Edge acceptance evidence. Re-reading a generic ThingsBoard migration or Edge fallback path would be the wrong authority for deciding whether an HVAC compatibility path may remain.

### Implementation decision

- `ADOPT`: retain historical migration/shadow tools only when they remain useful as one-shot or offline acceptance evidence.
- `REPLACE`: production simulator/test wiring with explicit acceptance-only overlays or externally supplied real identity configuration.
- `REJECT`: runtime fallback to `legacy-hvac-backend`, previous-writer schema compatibility, production `telemetry-shadow-comparator`, production `oidc-test-provider`, and canonical production Compose wiring for EG8200 Simulator.

### S24 consequence

Current route ownership is single-owner/native, the four Telemetry current-state routes are fully cut over to `telemetry-runtime-service`, and Phase 1 no longer includes a simulator service. Test IdP and simulator sources remain available for test/acceptance use, while Shadow rollout artifacts live under `deploy/acceptance`. Rollback is release/database recovery evidence only; no retired service is a rollback mechanism.

## S25 — Release reconciliation and final Real-mode cutover

Date: 2026-08-20

Local issue: #299

S25 adds no new generic upstream platform behavior. Its source-first authority is the already reviewed and implemented Registry, Telemetry, Connectivity, Command, Edge, Alarm, Notification and Dashboard owner slices plus their pinned ThingsBoard/OpenEMS decisions. The release step therefore locks exact repository facts instead of introducing a second release-control abstraction.

### Implementation decision

- `ADOPT`: an exact product release manifest that binds current route/data ownership revisions, generated HTTP/MQTT contracts, production migration manifest, LimitPolicy and retirement evidence by digest.
- `ADAPT`: durable recovery to the actual HVAC authority split: PostgreSQL is restored from backup, Redis Latest is rebuilt from PostgreSQL business-state snapshots/owner events, and Edge rolls back only to a previous signed Edge release.
- `REPLACE`: the remaining Telemetry current-snapshot PostgreSQL read path with the already implemented Redis Latest projection. Redis keeps business-revision CAS and now atomically records the Tenant+Device -> Site index needed for authorized reads.
- `REJECT`: Redis snapshots as business authority, PostgreSQL fallback on Redis miss/outage, mutable release profiles, runtime Git discovery, Legacy/Shadow/Test/Simulator fallback, and revival of historical omnibus capacity/canary gates.

### S25 consequence

Real-mode current Telemetry reads are now Redis-Latest-only after IAM authorization; presence-only reads expose no telemetry values, requested-key reads return only authorized keys, Redis miss preserves the existing typed not-found contract, and Redis unavailability fails closed rather than falling back to PostgreSQL. The final release manifest is exact and machine-checked, while PostgreSQL restore, Registry restore/replay, Redis rebuild/CAS and signed Edge rollback each have targeted evidence.
