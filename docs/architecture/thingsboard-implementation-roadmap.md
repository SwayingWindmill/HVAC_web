# ThingsBoard CE adjudication — dependency-ordered HVAC Web implementation roadmap

Status: `IMPLEMENTATION_ROADMAP_FINAL`

Decision issue: [生成分阶段替换与实施路线](https://github.com/SwayingWindmill/HVAC_web/issues/244)

Reference baseline: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`

Normative target: `docs/adr/0013-thingsboard-informed-target-domain-model.md`

Machine plan: `contracts/architecture/thingsboard-implementation-roadmap.v1.json`

## 1. Purpose

This roadmap converts the completed ThingsBoard source adjudication into implementation order. It is not a rewrite-by-service list and it does not grant existing code priority merely because it already exists.

The order is governed by five rules. Phase 1 remains `SINGLE_NODE_RECOVERABLE`; multi-instance/HA work is not on the default route.

1. **Authority before features.** A downstream feature cannot be stabilized on top of an ambiguous owner, stale vocabulary, cache authority or cross-domain write.
2. **Replace unsafe semantics before adding breadth.** Existing `REPLACE` behavior is removed before new protocol/provider/widget breadth is added.
3. **Owner ports before generic automation.** Rule Runtime and Presentation are intentionally late because they must consume stable domain facts/effect ports rather than inventing them.
4. **No runtime compatibility architecture.** One-shot data/schema conversion is allowed; dual-write, fallback reads, old-route compatibility and permanent bridge layers are not.
5. **Source-first remains mandatory.** Before implementing an adopted/adapted ThingsBoard or OpenEMS behavior, use the pinned official source/tests already referenced by the adjudication and update source-review evidence if the implementation depends on semantics not yet recorded.

## 2. Delivery unit

The unit of implementation is a **slice**, not a whole bounded context. Each slice must have:

- one explicit target owner;
- exact dependencies;
- an observable behavior change;
- schema/contract changes if needed;
- a removal list for superseded behavior;
- the smallest necessary behavior tests;
- rollout and rollback evidence.

A slice is complete only when its superseded path is removed or explicitly scheduled for the final retirement wave. “New path works while old path remains default” is not completion.

## 3. Test policy

This roadmap does not require large omnibus certification chains for every slice. The minimum evidence is behavior-focused:

1. domain invariant/unit tests for the new state machine or owner rule;
2. one persistence integration test when a durable boundary changes;
3. authorization/isolation negative tests for public or cross-site/tenant writes;
4. one owner-to-consumer contract test when a cross-domain port changes;
5. one critical-path end-to-end test only where the slice crosses physical/device or external-provider boundaries.

Legacy tests that assert superseded Organization/Area/Equipment, old Alarm status, cache authority or fallback routes must be deleted or rewritten; they are not preserved as compatibility requirements.

## 4. Dependency graph

```text
W0 Target lock / cleanup
  S00 canonical vocabulary + contract cleanup
  S01 ownership registry/runtime contract repair
  S02 product/schema migration + readiness + minimal platform policy
            |
            +------------------------+
            |                        |
W1 Security / Registry               | W2 Data authority
  S03 auth assurance boundary        |   S06 telemetry current authority
  S04 tenant IAM/admin foundation    |   S07 typed history/query
  S05 registry writer/templates      |   S08 metric revision/lifecycle
            |                        |
            +------------+-----------+
                         |
W3 Control / Connectivity / Edge     |
  S09 transport retry/session/credential lifecycle
  S10 edge control foundation verification
  S11 governed command-to-edge verification
  S12 edge fleet/sync/release/desired config/OTA
                         |
             +-----------+-----------+
             |                       |
W4 Alarm / Delivery                  | W5 Presentation / local products
  S13 alarm orthogonal aggregate     |   S17 site summary/dashboard truth
  S14 alarm stateful evaluator       |   S18 registry admin UX/import-export
  S15 outbound delivery ledger       |   S19 work-order/settlement projections
  S16 notification minimum loop      |
             |                       |
             +-----------+-----------+
                         |
W6 Rule / Intelligence
  S20 rule runtime core
  S21 rule management UI
  S22 model registry + real forecast/FDD/optimization chain
                         |
W7 Lifecycle / retirement
  S23 housekeeping/retention/tenant retirement
  S24 remove legacy/shadow/simulator production wiring
  S25 release reconciliation and final Real-mode cutover
                         |
W8 Evidence-triggered only
  S26 multi-instance/HA/additional protocols/providers/mobile/widget market
```

Waves allow parallel work only where dependencies permit. They are not calendar promises.

## 5. Wave 0 — lock target semantics and remove architectural ambiguity

### S00 — Canonical vocabulary and public-contract cleanup

**Target:** one machine and code vocabulary: `Tenant -> Site -> Space -> Asset -> Device -> Point`.

**Changes**

- remove `OrganizationID`, `ActingOrganizationID`, `OrganizationMembership` and obsolete fixtures/assertions;
- replace Registry contract/storage/API names that still expose Area/Equipment with Space/Asset;
- regenerate OpenAPI clients and update Real routes/components without compatibility DTOs;
- remove old ThingsBoard HTTP/RPC preparation stubs from the simulator and docs;
- mark historical ADR/API text as superseded instead of keeping active dual names.

**Minimum tests**

- contract generation/check;
- IAM/Registry compilation and focused tenant/site authorization tests;
- one Registry cursor/404 invisibility test using the new vocabulary;
- Real build graph check proving no old compatibility API is reachable.

**Rollback evidence:** pre-release DB/schema backup if physical rename is performed; generated-contract diff; no runtime rollback to old routes. If migration fails, restore the release as a unit.

### S01 — Ownership registry and cross-owner boundary repair

**Depends on:** S00.

**Changes**

- repair `libs/ownershipregistry` failing Scope/Migration Phase tests;
- make ADR 0013 owner map the machine authority;
- assert no cross-owner schema writes even inside physically merged deployables;
- remove stale route ownership entries such as superseded Command paths rather than aliasing them;
- define owner port/event/reference forms for Registry, Telemetry, Metric, Command, Alarm, Delivery and Rule effects.

**Minimum tests**

- `ownership:check`;
- focused `libs/ownershipregistry` tests;
- negative static/runtime test for cross-owner write attempts;
- route ownership tests for removed paths.

**Rollback evidence:** previous machine contract and schema ownership snapshot; rollback is whole release only, never re-enable stale aliases.

### S02 — Product/schema compatibility, migrator, readiness and minimum platform policy

**Depends on:** S00, S01.

**Changes**

- replace runtime SQL sanitization and split migration-record windows with reviewed one-shot migrations and atomic step state where possible;
- add Product/Schema preflight and fail-closed startup on unsupported versions;
- replace manual readiness booleans with required-dependency/queue-aware readiness;
- introduce versioned LimitPolicy for the first high-risk classes: authentication/token, control writes, telemetry ingest, Operations Agent and expensive query;
- establish immutable ProductReleaseManifest validation without runtime Git or mutable God Profile.

**Minimum tests**

- migration integration: apply, crash/restart boundary, already-applied, incompatible-version reject;
- readiness dependency failure/recovery;
- one fail-closed high-risk rate-policy test;
- Phase 1 deployment check.

**Rollback evidence:** schema backup/restore evidence; migration step journal; exact image/schema manifest; rollback or forward-fix procedure per irreversible step.

## 6. Wave 1 — identity, authorization and Registry lifecycle

### S03 — Authentication-assurance boundary

**Depends on:** S02.

**Target:** production authentication is a maintained standards-compliant OIDC/OAuth2 boundary; HVAC IAM remains authorization owner.

**Changes**

- stop expanding the current minimal `identity-service` as an unconstrained general-purpose IdP;
- select/integrate a maintained OIDC implementation behind the existing BFF boundary, or constrain the local service to a deliberately minimal, independently reviewed standards surface;
- implement MFA/passkey or equivalent high-assurance mechanism and explicit R3/R4 step-up evidence;
- persist login, failure, lock, reset, MFA and step-up security events through durable Audit intent/outbox;
- keep long-lived bearer credentials out of browser storage.

**Minimum tests**

- session/BFF flow;
- MFA/step-up required for one R3/R4 action;
- enumeration/expiry/replay negative cases;
- audit failure must prevent a security-sensitive change from being declared successful.

**Rollback evidence:** IdP client/config revision, signing-key/certificate rotation plan and BFF session invalidation procedure.

### S04 — Tenant IAM/admin foundation

**Depends on:** S03, S01.

**Changes**

- eliminate hard-coded Role authorization branches;
- implement Capability Catalog revision, multi-Tenant context switch, Tenant/Principal/Membership/Role Template/Site Binding/Explicit Deny administration;
- add scoped API Credential/Service Account lifecycle with one-time secret display and hash-at-rest;
- add Audit Search projection for Tenant/Actor/Action/Resource/Outcome/Time.

**Minimum tests**

- cross-tenant/cross-site BOLA negatives;
- context-switch session/CSRF/realtime cleanup;
- stale/revoked high-risk grants fail;
- API Credential rotation/revocation.

**Rollback evidence:** IAM policy/catalog revision and session revocation plan. Never restore Organization compatibility.

### S05 — Registry Writer, typed bindings and immutable Template Revision

**Depends on:** S01, S04.

**Changes**

- implement governed create/update/rebind/retire for Site/Space/Asset/Device/Point and optional PhysicalSensor;
- enforce typed role/cardinality/effective-interval/cross-site constraints in DB and domain service;
- implement Expected Revision, Idempotency, Audit and Outbox;
- implement immutable TemplateRevision/Assignment plus exact Release References;
- add cursor queries/tree children/device point detail;
- implement Import dry-run/plan/commit, External ID resolution and row-level errors;
- implement Retirement Saga entry points; hard delete remains forbidden for commissioned objects with dependencies.

**Minimum tests**

- stale revision/idempotency conflict;
- Space cycle and invalid binding cardinality;
- cross-site binding rejection;
- released Template immutability and rollback by new assignment;
- import dry-run/commit equivalence for one fixture.

**Rollback evidence:** migration/input plan, before/after revisions and registry snapshot. Rollback uses a new assignment or restore, never mutate a released revision.

## 7. Wave 2 — correct data authority before higher-level features

### S06 — Telemetry Current authority and ingest reliability

**Depends on:** S00, S01, S02. S05 may proceed in parallel because S00/S01 already freeze Point/binding identity and ownership semantics.

**Changes**

- make PostgreSQL durable Device Observation Snapshot the only Current authority;
- remove PostgreSQL-read -> Redis-write -> Redis-read correctness path;
- keep Redis as optional rebuildable projection with source revision/tombstone;
- replace unbounded transient retry with per-device/partition bounded retry, parking/dead/quarantine and queue saturation semantics;
- preserve duplicate/out-of-order acceptance semantics without rolling Current backward.

**Minimum tests**

- Redis unavailable does not change Current correctness;
- duplicate replay does not advance Business Revision;
- valid out-of-order history retained but Current does not regress;
- poison device/message cannot head-of-line block an unrelated device.

**Rollback evidence:** authoritative PostgreSQL snapshot/rebuild procedure for Redis; queue/dead records. No rollback to Redis authority.

### S07 — Typed history/query and aggregation contract

**Depends on:** S06.

**Changes**

- replace numeric-only/no-cursor query with typed STRING/BOOLEAN/JSON/numeric history;
- return stable cursor, Observation identity, acceptance, quality, source position and point revision;
- expose valid out-of-order observations;
- separate Counter/Gauge/State aggregation semantics;
- implement real dataset/projector watermark and site-timezone/calendar boundaries.

**Minimum tests**

- same timestamp multiple observations preserved;
- stable pagination under repeated reads;
- counter reset/rollover and quality policy;
- DST/calendar-boundary aggregation;
- old pseudo revision/watermark fields are absent.

**Rollback evidence:** query contract revision and client regeneration. Because old query semantics are wrong, rollback is whole release/restore, not a fallback endpoint.

### S08 — Metric result revision and lifecycle worker

**Depends on:** S06, S07.

**Changes**

- remove fixed `revision=1` and accidental ReplacingMergeTree current selection;
- persist append-only Metric Result Fact/Run/Provenance and monotonic current Metric revision;
- enforce definition release, dependency DAG, unit/type/quality policy;
- implement scheduled/backfill/recalc with stable Result identity and reconcile;
- implement first data lifecycle worker primitives: claim/lease/hold/archive/tombstone/retry/audit.

**Minimum tests**

- repeated recalculation preserves all result facts and one current revision;
- dependency cycle/revision mismatch rejected;
- cross-store partial completion reconciles without new Result ID;
- legal hold blocks deletion; archive failure cannot delete source.

**Rollback evidence:** immutable metric facts plus current projection rebuild; lifecycle tombstone/audit log.

## 8. Wave 3 — control, connectivity and Edge safety

### S09 — Transport retry/session/credential lifecycle

**Depends on:** S02, S04, S05, S06.

**Changes**

- separate telemetry ingress and command delivery fault domains inside `iot-service` even when physically co-located;
- make command reply correlation and connector ownership recoverable across restart;
- add CredentialRef, rotation, revocation, expiry and session invalidation;
- add durable IntegrationInstance/TransportProfile/DeviceBinding/GatewayChildBinding;
- implement pre-provisioned one-time enrollment; no auto-created Registry identity;
- keep MQTT first; additional protocols remain deferred.

**Minimum tests**

- connector crash before/after publish does not duplicate physical effect;
- revoked credential kills session;
- unknown child/device cannot be auto-created;
- bounded retry/dead behavior does not block unrelated device.

**Rollback evidence:** session/credential revision, active ownership lease state, no secret in logs/config export.

### S10 — Edge control foundation verification and source alignment

**Depends on:** S00, S01. It may proceed in parallel with S03-S09 because its durable Device/Point vocabulary and owner boundary are already frozen.

**Changes**

- treat pre-source-review Edge code as unverified until mapped against pinned OpenEMS source/tests;
- finish/verify Channel, Process Image, IPO Cycle, Controller, Scheduler, Arbiter, Capability Profile, Driver/Bridge, Timedata and simulator/real-driver parity;
- fix current cycle-duration/clock and safety-state evidence failures;
- ensure simulator replaces driver/physical behavior only, not production Controller/Scheduler/Command/Telemetry logic.

**Minimum tests**

- all controllers see one immutable cycle snapshot;
- deterministic priority/arbitration;
- stale input, rejected write, interlock and local lease expiry;
- real/simulated driver contract parity.

**Rollback evidence:** signed/known-good Edge runtime revision and deterministic simulator fixture.

### S11 — Governed Cloud Command -> Edge -> readback chain

**Depends on:** S09, S10, S04, S05, S06.

**Changes**

- retain Cloud Intent/approval/idempotency/lease/fence/outcome authority;
- dispatch leased intent through transport only after pre-send fence;
- Edge applies capability/local lease/scheduler/arbiter/interlock and records requested/effective/constraint/winner/cycle;
- require independent State/Telemetry readback before Cloud business success;
- preserve `OUTCOME_UNKNOWN` when effect cannot be proven.

**Minimum tests**

- expired/stale fence never advances;
- Edge constrains/rejects command without changing Cloud approval fact;
- transport/device ACK alone cannot complete Command;
- independent readback mismatch produces failed/inconclusive outcome;
- restart recovery across the attempt boundary.

**Rollback evidence:** immutable Command intent/attempt/outcome plus Edge evidence; last-known-safe local policy. Never bypass Edge on rollback.

### S12 — Edge Fleet, sync, release, Desired Config and signed OTA

**Depends on:** S05, S09, S10, S11, S02.

**Changes**

- introduce Edge identity/enrollment/rotation and version/capability handshake;
- implement DesiredEdgeState/ObservedEdgeState and signed immutable EdgeRelease;
- implement bootstrap snapshot revision + chunk resume + digest + tombstone + atomic activation;
- implement incremental owner-revision stream, contiguous ACK cursor and quarantine;
- add desired/reported configuration revision and safe activation/rollback;
- add offline capacity/watermarks and priority evidence retention;
- add signed OTA artifact/release/campaign with compatibility, canary/wave/pause, local preflight and rollback.

**Minimum tests**

- interrupted bootstrap resumes without mutating active state;
- bad item quarantines without silent cursor advance;
- Cloud/Edge dual-write of one field rejected;
- reconnect chooses delta or full snapshot correctly;
- unsigned/incompatible OTA cannot activate;
- offline disk pressure preserves safety/control/audit evidence before diagnostics.

**Rollback evidence:** previous signed EdgeRelease, observed active/staged/previous revision, digest and health/readback verification.

## 9. Wave 4 — Alarm, external delivery and Notification

### S13 — Alarm orthogonal aggregate migration

**Depends on:** S04, S05, S06.

**Changes**

- replace `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED` aggregate model with `ACTIVE/CLEARED` condition plus separate ACK, suppression, severity, assignment, timeline and links;
- add stable fingerprint and one-active-incident partial uniqueness;
- recovery then recurrence creates a new Incident/correlation, never reopens historical row;
- delete arbitrary Close/Reopen compatibility behavior.

**Minimum tests**

- concurrent first create produces one active Incident;
- ACK does not clear; suppression does not clear; Work Order completion does not clear;
- recurrence produces new Incident;
- immutable system timeline.

**Rollback evidence:** one-shot old->new Alarm migration report and pre-migration backup. No runtime dual model.

### S14 — Stateful Alarm evaluator

**Depends on:** S13, S06, S08 as Metric inputs become available.

**Changes**

- implement versioned AlarmPolicyRevision with typed compare/range/hysteresis/duration/repeat/no-data/stale/AND/OR/clear predicate/site schedule;
- persist evaluation state, nextEvaluationAt, quality blocker and timer claim/lease;
- restore correctly after restart and policy revision switch.

**Minimum tests**

- duration/repeat/hysteresis through restart;
- bad/stale/missing quality cannot falsely clear active condition;
- DST/site schedule;
- policy revision switch is deterministic.

**Rollback evidence:** previous released Alarm policy assignment and persisted evaluation evidence; rollback is a new assignment.

### S15 — Shared Outbound Delivery ledger

**Depends on:** S02, S04. It uses the platform SecretRef/CredentialRef boundary but does not depend on device-transport credential rollout in S09.

**Changes**

- implement IntegrationDefinition and durable `DeliveryIntent -> DeliveryAttempt -> DeliveryReceipt -> DeadLetter/ReplayApproval`;
- first adapter REST/Webhook with default SSRF/DNS rebinding defense, no redirect, bounded body/time/concurrency and destination allowlist;
- CredentialRef only; no secret in business payloads;
- explicit `NOT_SENT/MAYBE_SENT/ACCEPTED_NOT_CONFIRMED/DELIVERED/FAILED` classes where applicable.

**Minimum tests**

- intent persisted before effect;
- worker restart/lease expiry/idempotency;
- SSRF/metadata IP/DNS rebinding/redirect rejection;
- outcome unknown does not blind retry a potentially completed effect;
- dead-letter replay is a governed new attempt, not history edit.

**Rollback evidence:** immutable delivery ledger and provider receipt; disable adapter/release without deleting evidence.

### S16 — Notification minimum product loop

**Depends on:** S13, S14, S15, S04.

**Changes**

- implement Audience, immutable TemplateRevision, NotificationPolicyRevision, escalation stages and Inbox;
- first channel IN_APP; external EMAIL/REST adapter uses S15 owner rather than direct send;
- Alarm Created/Severity Changed/ACK/Cleared triggers and durable cancellation of future stages;
- mandatory safety notification separate from advisory preference.

**Minimum tests**

- one Intent per source/rule/stage under replay;
- delayed stage uses frozen template/recipient snapshot;
- ACK/Clear races with stage claim are explainable from DB state;
- notification read does not change Alarm ACK/condition;
- mandatory safety policy cannot be opted out by ordinary user preference.

**Rollback evidence:** immutable notification policy/template assignments and Delivery receipts.

## 10. Wave 5 — truthful presentation and local product completion

### S17 — SiteDashboardSummary and BigScreen truthfulness

**Depends on:** S05, S06, S07, S08, S13.

**Changes**

- replace browser sampling/full-list aggregation with coherent server-side SiteDashboardSummary;
- include asOf/generatedAt/dataWatermark/aggregateWatermark/completeness/quality/denominator policy;
- Dashboard and BigScreen consume same authoritative summary/projection family;
- remove Real BigScreen demo identity and all fabricated healthy/default values;
- control actions navigate to Command Preview only.

**Minimum tests**

- partial/unauthorized data never displays as zero/healthy/full-site ratio;
- site-local day calculation around timezone/DST;
- reconnect reconciles REST snapshot before live delta;
- Real graph has no Demo/Mock fallback.

**Rollback evidence:** presentation revision only; underlying owner facts unaffected.

### S18 — Registry administration UI and import/export

**Depends on:** S05, S04.

**Changes**

- Site/Space/Asset/Device/Point lifecycle UI using generated owner contracts;
- cursor/lazy tree, device point list, Template draft/release/assign/rollback;
- dirty guard, expected revision/conflict handling;
- import dry-run/commit/error view and controlled export without secrets.

**Minimum tests**

- one create/rebind/retire operator flow;
- stale revision conflict is visible and safe;
- no browser-side topology authority;
- import cannot bypass IAM/tenant/site constraints.

**Rollback evidence:** owner revisions and import plan; UI rollback cannot restore obsolete API shapes.

### S19 — Work Order / Settlement / Cost projections

**Depends on:** S05, S08, S13 for linked facts as needed.

**Changes**

- preserve Work Order as local domain but repair service-level regression and formal Alarm links;
- build authoritative cost/settlement/reconciliation read models with explicit dataset revision/quality/watermark;
- do not let Work Order completion mutate Alarm recovery.

**Minimum tests**

- Alarm/Work Order link semantics;
- settlement/cost recomputation idempotency and revision;
- missing/partial source quality remains visible.

**Rollback evidence:** append-only/revisioned source and read-model rebuild procedure.

## 11. Wave 6 — generic automation only after owner boundaries are stable

### S20 — Rule Runtime core

**Depends on:** S05, S06, S08, S11, S13, S15 and the corresponding owner effect ports.

**Changes**

- implement NodeDefinition catalog, immutable RuleRevision/Binding, compile validation, typed ports, deterministic IDs, ordering key, lease/fence, CAS state, durable continuation/timer, bounded retry/dead/quarantine and effect intent references;
- implement pure typed transform/filter/math and owner snapshot reads first;
- implement owner intent nodes only through stable owner ports;
- no arbitrary JS/TBEL, credential read or direct DB/network side effect;
- simulation/debug replay uses frozen facts and effect sink only.

**Minimum tests**

- graph validation: unreachable/cycle/type/budget/permission failures;
- same event+rule+binding gives stable execution/work/effect identities;
- crash/restart at continuation/effect boundary;
- dead/quarantine does not become success;
- replay produces no real side effects.

**Rollback evidence:** immutable RuleRevision and prior assignment; active executions remain pinned to their original revision.

### S21 — Rule management UI

**Depends on:** S20, S18.

**Changes**

- graph editor only for approved typed catalog;
- validate/diff/test/simulate/release/assign/retire lifecycle;
- no raw class name, arbitrary script or credential fields;
- display trace/dead/quarantine evidence with scoped access.

**Minimum tests**

- invalid graph cannot release;
- released revision immutable;
- simulation does not create domain effects;
- capability/scope restrictions on management actions.

**Rollback evidence:** prior released rule assignment.

### S22 — Model Registry and real Intelligence product chains

**Depends on:** S07, S08, S15, S02, S04. Optimization physical action also depends on S11.

**Changes**

- introduce ModelDefinition/DeploymentRevision/CredentialRef/DataEgressPolicy/Invocation provenance;
- keep Operations Agent evidence-first/read-only tool boundary;
- wire actual Forecast workers, Gateway APIs and Real UI with explicit model/fallback/uncertainty/quality;
- implement FDD findings with evidence/model/rule revision and explicit Alarm/Work Order linking;
- replace ESS `NO_DISPATCH` placeholder with HVAC Recommendation domain; Recommendation never directly dispatches Command;
- add cost/benefit/constraint/verification plan before recommendation approval.

**Minimum tests**

- provider outage/invalid schema/budget/data-egress failure is explicit;
- fallback is visibly different from model output;
- no-input does not fabricate forecast;
- Recommendation requires independent current-state revalidation before Command creation;
- model secret never enters UI/audit payload.

**Rollback evidence:** immutable deployment revision; disable/roll back deployment without modifying historical findings/results.

## 12. Wave 7 — lifecycle completion and removal of superseded runtime

### S23 — Housekeeping, retention and Tenant retirement

**Depends on:** S02 plus owners whose lifecycle is being managed; core rollout after S05/S06/S08/S13/S15.

**Changes**

- unify typed durable maintenance work for retention/archive, certificate expiry, outbox/inbox cleanup, projection repair and dead-work disposition;
- implement Tenant Policy/Usage view and Retirement Saga across owner domains;
- support legal hold/tombstone/proof/retry and explicit incomplete retirement state.

**Minimum tests**

- hold blocks deletion;
- worker restart resumes lease safely;
- one owner failure prevents Tenant retirement from being declared complete;
- certificate/secret expiry event is queryable and actionable.

**Rollback evidence:** work attempts, tombstones, archive proof and owner retirement ledger.

### S24 — Remove Legacy/Shadow/Simulator production wiring

**Depends on:** replacement owner acceptance for Registry/Telemetry/Edge and Real-mode routes.

**Changes**

- delete production wiring for `legacy-migration-service` after final one-shot migration/verification;
- delete `telemetry-shadow-comparator` production path after authoritative Telemetry query/current rollout evidence;
- ensure `oidc-test-provider`, test PKI and EG8200 Simulator remain test/acceptance only;
- remove stale build/deploy/ownership entries and docs that imply fallback.

**Minimum tests**

- deployment manifest contains no retired production service;
- Real graph/bundle cannot reach test/demo/simulator truth;
- owner routes have exactly one production owner.

**Rollback evidence:** database backup and release manifest, not service fallback. Retired migrators may be retained as source/history but not runtime dependencies.

### S25 — Release reconciliation and final Real-mode cutover

**Depends on:** all production-priority slices above.

**Changes**

- ProductReleaseManifest locks exact domain release/schema revisions;
- validate no target-forbidden path remains reachable;
- reconcile owner projections and Edge desired/observed state;
- run critical smoke paths: login/context, registry read/write, telemetry current/history, governed command/readback, alarm, notification/inbox, dashboard;
- generate rollback/restore evidence per changed durable boundary.

**Minimum tests**

- target-domain contract/ownership/contract generation;
- critical smoke only, not every historical omnibus gate;
- restore/rebuild proof for PostgreSQL/Redis projections and relevant Edge release rollback.

**Rollback evidence:** signed/exact release manifest, DB backup/restore proof, previous domain assignments and previous signed Edge release.

## 13. Wave 8 — explicitly deferred until evidence exists

### S26 — Capacity/protocol/provider expansion

Do not schedule by default.

Trigger evidence is required before any of these become implementation work:

- multi-instance services, Kafka/Pulsar, actor/partition runtime or database HA;
- Redis cluster/sentinel as a scaling choice;
- CoAP, LwM2M, SNMP, Sparkplug, BACnet/OPC UA beyond confirmed site/device demand;
- Slack/Teams/SMS/mobile push beyond a named operational workflow/provider/SLO;
- native mobile application;
- generic widget/plugin/connector marketplace;
- arbitrary AI provider breadth or generic AI calculated fields.

The trigger must cite measured capacity, availability requirement, device BOM/protocol matrix, customer integration contract or explicit product requirement. “ThingsBoard supports it” is not a trigger.

## 14. Migration rules

### 14.1 Schema and data conversion

Allowed:

- reviewed one-shot migrations;
- table/column rename or replacement in a coordinated release;
- backfill into a new authoritative schema before cutover when the old runtime is stopped from writing;
- read-only validation/shadow comparison as release evidence;
- restore from pre-release backup if the release fails.

Forbidden:

- normal-runtime dual writes;
- request-time fallback from new owner to old owner;
- long-lived compatibility DTOs/routes;
- silently interpreting old enum/field names;
- automatic migration on ordinary read path.

### 14.2 Cutover sequence for a replaced authority

```text
freeze admission to affected writes
-> drain/stop old writer
-> backup / capture source revision
-> run one-shot migration
-> validate invariant counts/digests/revisions
-> start new owner on exact schema/product revision
-> rebuild disposable projections
-> run focused smoke/negative tests
-> promote release
-> retire old writer/route
```

If the new owner cannot pass validation, restore the unit. Do not leave both owners live.

## 15. Rollout gates

Each production slice uses the following gate categories, only where applicable:

- **Contract gate:** generated contract and machine ownership agree.
- **Authority gate:** exactly one owner/writer for each migrated fact.
- **Security gate:** cross-tenant/site and privileged-action negatives.
- **Durability gate:** crash/restart at the new durable boundary.
- **Safety gate:** required for Command/Edge/OTA or other physical effects.
- **Truthfulness gate:** required for presentation/intelligence; unavailable/partial/fallback is not shown as healthy/real.
- **Restore gate:** required when a schema or durable fact authority changes.

A green unrelated test suite cannot compensate for a failed gate in the changed boundary.

## 16. Recommended implementation sequence now

The first implementation batch after this roadmap is **not Rule Engine or feature breadth**. It should be:

1. S00 canonical vocabulary/contract cleanup;
2. S01 ownership registry/runtime boundary repair;
3. S02 migrator/schema/readiness/minimum platform policy;
4. after W0, the immediate parallel frontier is S03 (authentication assurance), S06 (Telemetry Current authority), S10 (Edge source-aligned foundation) and S15 (Outbound Delivery foundation); S04 follows S03, and S05 follows S04.

The current in-progress Alarm/Command/Edge code may be retained as uncommitted/prototype work, but it must be re-evaluated against the slice that owns it before merge. Existing implementation effort does not move a later slice earlier in the dependency graph.

## 17. Completion criteria for #244

This roadmap is complete when:

- every `REPLACE`, `MISSING`, `REMOVE` and material `ADAPT` item from the reverse audit has a slice or explicit evidence-triggered defer;
- slices have dependency order and no known ownership cycle;
- each replacement has an old-path removal rule;
- migration never requires permanent dual-write/fallback compatibility;
- behavior tests are bounded and relevant to the changed authority;
- rollout/rollback evidence is defined for durable and physical-effect changes;
- the roadmap ends with removal of Legacy/Shadow/Simulator production wiring;
- HA/protocol/provider breadth remains evidence-triggered rather than imitation-driven.

All criteria above are satisfied by this package. Runtime implementation starts only after this planning map is closed.
