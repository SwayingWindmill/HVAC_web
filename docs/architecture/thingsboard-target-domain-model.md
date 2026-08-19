# ThingsBoard CE source audit — HVAC Web target domain model

Status: `TARGET_DOMAIN_MODEL_FINAL`

Decision issue: [裁决跨功能冲突并确定目标 Domain 模型](https://github.com/SwayingWindmill/HVAC_web/issues/240)

Reference baseline: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`

Normative decision: `docs/adr/0013-thingsboard-informed-target-domain-model.md`

Machine contract: `contracts/architecture/target-domain-model.v1.json`

## 1. Outcome

The ten ThingsBoard capability adjudications and the complete HVAC Web reverse audit are now collapsed into one target model. This document is the cross-domain index; ADR 0013 is normative when older documents disagree.

The target keeps the local mechanisms that have explicit HVAC, safety or correctness evidence and absorbs stronger ThingsBoard lifecycle/runtime patterns where the local implementation is incomplete. It does not import ThingsBoard as a runtime dependency and does not preserve existing HVAC code merely because it already exists.

The most important final decisions are:

1. `Tenant -> Site -> Space -> Asset -> Device -> Point` is the only canonical hierarchy. `PhysicalSensor` is optional; derived values are `Metric`, not synthetic Points.
2. Every business fact has one logical Domain Owner. Physical process convergence never authorizes cross-owner database writes.
3. Registry owns identity/topology/templates; cross-domain execution/configuration stays in domain-owned immutable releases referenced by Registry.
4. PostgreSQL durable Device Observation Snapshot is current Telemetry authority; Redis is a rebuildable projection only; ClickHouse history is append-only.
5. Generic Rule Runtime owns execution/state/effect intent, never Alarm/Command/Telemetry/Notification/external final effects.
6. Alarm uses orthogonal condition, acknowledgment, suppression, assignment, Work Order and Notification facts.
7. Notification business policy and external provider delivery are separate owners; external delivery uses one durable Intent/Attempt/Receipt ledger.
8. Cloud Command governance, Edge arbitration and device/PLC safety form a one-way chain; only independent readback can complete the Cloud business outcome.
9. Domain release families share immutable release semantics but remain separately owned; Platform Operations coordinates them with a Product Release Manifest instead of a God Profile.
10. Phase 1 remains `SINGLE_NODE_RECOVERABLE`; logical owners survive physical aggregation into a small number of deployables.

## 2. Resolution of the eight cross-domain conflicts from #242

| Conflict | Final decision |
| --- | --- |
| Profile / Template / Release ownership | Registry owns immutable Asset/Device Template Revision and assignments. Transport, Rule, Alarm, Presentation, Edge and Model configuration are separate immutable domain releases referenced by exact revision. No mutable God Profile. |
| Telemetry Current Authority | Telemetry Runtime PostgreSQL durable Business Snapshot is the only Current Authority. Redis is rebuildable projection/cache. ClickHouse stores append-only raw/history facts. Metric results have their own owner/revisions. |
| Rule Effect ownership | Rule Runtime persists typed Effect Intent only. The receiving Domain re-authorizes and owns final mutation/receipt. Specialized Alarm/Metric/Operations state stays in those domains. |
| Alarm state decomposition | `ACTIVE/CLEARED` condition is separate from ACK, suppression, assignment, Work Order link and Notification disposition. Recurrence creates a new Incident. |
| Cloud Command / Edge / PLC | Cloud owns intent/approval/fence/outcome; Transport owns delivery mechanics; Edge owns effective actuation/arbitration/interlock; PLC/device owns hard safety. Independent readback completes Cloud outcome. |
| Unified release language | All released artifacts are immutable, revisioned and dependency-locked. Product Release Manifest coordinates exact revisions without owning them. Rollback creates a new assignment/release decision. |
| External delivery | Outbound Delivery owns `DeliveryIntent -> DeliveryAttempt -> DeliveryReceipt -> DeadLetter/ReplayApproval`. Notification and integrations consume that shared owner. |
| Physical convergence vs logical ownership | Co-located modules keep separate schemas, ports, authorization, outbox and ownership. No cross-owner direct DB call is permitted merely because code runs in one process. |

## 3. Canonical object model

```text
Tenant
└─ Site
   ├─ Space
   │  └─ child Space
   ├─ Asset
   │  ├─ effective-dated Space placement
   │  └─ effective-dated Device bindings
   └─ Device
      ├─ Point
      └─ optional PhysicalSensor traceability
```

Key rules:

- Asset is maintainable physical business identity; Device is communication/control endpoint identity.
- Point is the canonical typed operational data/control identity owned by a Device.
- PhysicalSensor is created only when the physical probe needs independent lifecycle/calibration/traceability.
- Point subject/placement/reporting/control relationships are typed and effective-dated; free-form Relation is not the write model.
- Metric is the canonical derived-result identity. Derived values never overwrite raw Telemetry or masquerade as Device Points.
- External identifiers are bindings, never platform identity.

## 4. Owner map

| Owner | Owns |
| --- | --- |
| Identity Provider | Authentication credential lifecycle and authentication assurance |
| IAM | Tenant, Principal, Membership, Capability, Explicit Deny, delegation, authorization policy |
| Registry | Site, Space, Asset, Device, PhysicalSensor, Point, typed bindings, external bindings, Template Revision/Assignment |
| Connectivity | Integration/Transport profiles, CredentialRef lifecycle, sessions, provisioning, Desired/Reported Configuration, OTA Campaign governance |
| Telemetry Runtime | Ingest acceptance/quarantine, Presence/Freshness, durable current Device Observation Snapshot and Business Revision |
| Telemetry Query | Typed history/cursor/aggregation query product |
| Metric | Metric definition/release, runs, append-only result facts, current Metric projection |
| Command | Intent, approval, attempt, lease/fence, outcome, independent verification |
| Edge Control | Channel, Process Image, Controller, Scheduler, Arbiter, local lease/interlock, observed state and sync evidence |
| Rule Runtime | Node definitions, Rule revisions/bindings, executions, generic state, continuation, Effect Intent, trace/debug |
| Alarm | Alarm policy/evaluation, Incident, ACK, suppression, assignment, timeline/correlation |
| Notification | Audience, Template, policy, escalation, Inbox and business disposition |
| Outbound Delivery | Integration definition, Intent, Attempt, Receipt, Dead Letter and Replay Approval |
| Work Order | Work Order aggregate, timeline and Alarm/Asset links |
| Intelligence | Investigation/Evidence/Finding, Forecast result, Optimization Recommendation, Model deployment/invocation |
| Presentation | View/layout/datasource definition and derived Site summary projections |
| Platform Operations | Availability tier, limits, migration/release execution, readiness, generic durable work convention and observability |

## 5. Cross-domain write rule

Only the owner mutates authoritative state.

Allowed crossing mechanisms:

```text
caller -> owner Command Port
caller -> owner Query Port
owner transaction -> immutable Domain Outbox/Event
consumer -> rebuildable Projection
domain object -> exact {ownerId, ownerRevision} reference
```

Forbidden:

```text
module A -> module B database table
rule node -> domain table
frontend -> inferred authoritative truth
cache/queue -> promoted to business authority
shared process -> shared mutable ownership
```

This rule applies even inside `energy-api`, `iot-service`, `telemetry-worker` or any future merged deployable.

## 6. Release model

All released domain artifacts share:

```text
identity
revision
schemaVersion
digest
dependencies
DRAFT -> VALIDATED -> RELEASED -> RETIRED
createdBy / releasedBy
```

A released revision is immutable.

Registry Template Revision may reference exact released revisions, but does not inline their mutable configuration. A Product Release Manifest may lock a compatible set such as:

```text
Registry Template Revision
Transport / Desired Config Revision
Rule Revision
Alarm Policy Revision
Presentation Revision
Edge Release
Model Deployment Revision
schema/product version
```

The manifest coordinates rollout and rollback evidence. Each referenced domain remains its owner.

## 7. Telemetry and Metric authority

```text
Device/Edge observation
  -> Transport Adapter
  -> Telemetry Runtime acceptance/quarantine
  -> PostgreSQL current Device Observation Snapshot + Business Revision
  -> Outbox
       -> ClickHouse append-only raw history
       -> Redis rebuildable current projection
       -> Realtime delivery
       -> Metric/Analytics consumers
```

Rules:

- valid out-of-order history is preserved;
- out-of-order data cannot roll Current backward;
- Redis failure cannot change business truth;
- Query cursors/watermarks are explicit, not guessed from source offsets;
- Metric Result uses separate immutable Fact and monotonic Current revision;
- Dashboard/Analytics summaries expose as-of, revision, watermark, completeness and quality.

## 8. Rule effect flow

```text
Owner event/snapshot
  -> exact RuleBinding Revision
  -> RuleExecution
  -> pure/stateful typed nodes
  -> persist generic Rule state + EffectIntent
  -> target owner receives intent
  -> target owner re-authorizes/revalidates
  -> target owner commits final fact
  -> owner Receipt/Event returns to Rule trace
```

Rule Runtime may not own final Alarm, Command, Telemetry, Notification, Registry, Work Order or external-delivery facts.

Alarm condition evaluation remains Alarm-owned because Duration/Repeat/Clear/Quality/Recovery state is part of Alarm correctness. Metric calculation remains Metric-owned. Operations Investigation remains Operations-owned.

## 9. Alarm / Notification / Delivery flow

```text
Telemetry / Metric / FDD Evidence
  -> Alarm Policy Revision
  -> durable Alarm Evaluation State
  -> Alarm Incident ACTIVE/CLEARED
  -> Alarm Outbox
  -> Notification Policy / Escalation
  -> IN_APP Inbox
  -> external DeliveryIntent
  -> DeliveryAttempt
  -> provider DeliveryReceipt
  -> Notification disposition projection
```

ACK, suppression, Work Order completion and Notification delivery never fabricate Alarm recovery.

## 10. Command and Edge safety flow

```text
Principal + assurance
  -> IAM authorization
  -> Command Intent
  -> approval if required
  -> Cloud lease + fence
  -> transport attempt
  -> Edge local lease/capability
  -> Process Image
  -> Scheduler / Controller / Arbiter / Interlock
  -> effective value
  -> Driver / Protocol Bridge
  -> Device / PLC
  -> independent State/Telemetry readback
  -> Cloud Command verification
  -> final outcome
```

At each boundary the previous stage is evidence, not automatic success. The Cloud cannot write an actuator around Edge. Edge cannot enlarge Cloud authorization or extend expired Cloud intent.

## 11. Edge synchronization authority

Cloud Registry and domain release owners publish Desired revisions. Edge reports Observed facts.

```text
Published DesiredEdgeState
  -> immutable Bootstrap Snapshot
  -> chunk digest/resume/tombstone validation
  -> staging projection
  -> atomic activation
  -> ACK committed revision
  -> incremental owner-revision stream
```

No Cloud/Edge dual writer exists for the same field. Edge-side edits are proposals/observed overrides; accepted Cloud changes create a new owner revision.

## 12. Presentation boundary

The first production Dashboard/BigScreen contract is a coherent `SiteDashboardSummary`, not a client-side join over partial Device lists.

Presentation may own:

- layout;
- router state;
- typed datasource references;
- first-party widget/component references;
- navigation intents.

It may not own or infer Device, Metric, Alarm, Command or authorization facts. Any control action routes to Command Preview and is re-authorized by Command Domain.

## 13. Phase 1 physical topology interpretation

Phase 1 may continue to converge physical services for deployment simplicity. The target interpretation is:

- `energy-api`: physically hosts several public/business modules but each logical Owner keeps its schema, ports, authorization and outbox boundary;
- `iot-service`: may host MQTT ingest and Command transport/verification, but they have separate modules, queues, health and failure isolation;
- `telemetry-worker`: hosts Telemetry Runtime/history publication/current projection work without transferring authority to Redis or ClickHouse;
- `metric-worker`: owns Metric execution only;
- Edge runtime remains a distinct on-site control plane;
- selective Intelligence services may remain separate when resource/security/failure characteristics justify it.

`SINGLE_NODE_RECOVERABLE` is the only current availability claim. Multi-instance/HA is not part of this decision unless later capacity or availability evidence activates the D10 thresholds.

## 14. Explicitly superseded paths

The following are no longer valid target architecture, even if code/docs/tests still contain them:

- Organization, Area, Equipment canonical contracts or compatibility APIs;
- Sensor as a mandatory layer between Device and Point;
- Calculated Point as the default derived-data model;
- mutable cross-domain Device/Asset Profile;
- Redis Current authority or PostgreSQL-read -> Redis-write -> Redis-read correctness path;
- numeric-only/no-cursor history as final query contract;
- generic Rule direct database/network/domain side effect;
- Alarm single status `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED`;
- manual Close/Reopen as normal Alarm recovery semantics;
- provider delivery retry state inside Notification or Rule;
- transport/device ACK as Command success;
- Cloud direct actuator control around Edge Arbiter;
- Cloud/Edge dual write of the same configuration field;
- presentation/BigScreen reconstruction of site truth from sampled lists;
- AI/Optimization direct Command dispatch;
- migration/runtime fallback, dual-write or compatibility layer preserving superseded architecture;
- Real Mode fallback to Demo/Mock/ThingsBoard runtime truth.

## 15. Inputs to #244

The implementation-roadmap ticket must use this target model, not module age, as its dependency graph. At minimum it must order work around these prerequisites:

1. canonical terminology and old-contract removal;
2. ownership contract/runtime-registry repair;
3. Identity/IAM Tenant and assurance boundary;
4. Registry Writer + typed binding + immutable Template release;
5. Telemetry Current/History/Metric authority corrections;
6. Command/Edge safety-chain completion;
7. Alarm orthogonal model and stateful evaluator;
8. shared Outbound Delivery + Notification minimum loop;
9. Rule Runtime built only after owner Effect ports are stable;
10. Dashboard Summary/Presentation truthfulness;
11. Edge Fleet/sync/release and lifecycle/configuration governance;
12. real Forecast/Optimization/FDD/Cost/Settlement product chains according to business priority;
13. removal of migration/shadow/test-only production wiring after acceptance;
14. availability/HA expansion only if measured thresholds require it.

No implementation slice may reintroduce a target-forbidden compatibility path to make an intermediate test green.

## 16. Completion criteria

#240 is complete when:

- the canonical language has one answer;
- every cross-domain fact has one owner;
- all eight conflicts from #242 have explicit decisions;
- release ownership is immutable and non-God-profile;
- Telemetry, Rule, Alarm, Delivery, Command and Edge flows have one-way authority;
- Phase 1 process convergence is reconciled with logical ownership;
- superseded paths are explicit;
- ADR 0013 and the machine contract are committed as the normative inputs to #244.

All criteria above are satisfied by this decision package. Runtime implementation remains intentionally out of scope for #240.
