# ADR 0013 — ThingsBoard-informed target domain model and ownership boundaries

Status: accepted

Date: 2026-08-18

Issue: #240

## Context

The ThingsBoard CE v4.3.1.1 source audit and HVAC Web reverse audit are complete. Ten capability-domain adjudications and the cross-domain reverse audit agree on many local strengths, but they also expose conflicts that cannot remain as parallel interpretations:

- early Registry ADRs still use Organization / Area / Equipment while the current canonical language is Tenant / Space / Asset;
- ThingsBoard-style mutable cross-domain Profiles conflict with the project's single-owner bounded contexts;
- Redis, PostgreSQL, ClickHouse, Metric results and presentation projections need unambiguous truth ownership;
- generic Rule execution must not become the owner of Alarm, Command, Notification, Telemetry or external side effects;
- Alarm condition, acknowledgment, suppression, assignment, work-order linkage and notification disposition are different facts;
- Cloud Command governance must not bypass Edge arbitration or device/PLC safety;
- Template, Rule, Alarm, Edge, Presentation and Model releases need one release language without becoming one mutable God Profile;
- Notification and external integrations need one durable outbound-delivery ownership model;
- Phase 1 may physically merge modules into a small number of deployables without merging their logical ownership.

Existing HVAC Web code has no incumbency preference. When a local behavior conflicts materially with the fixed ThingsBoard reference and lacks an explicit HVAC, safety, complexity or measured-performance justification, the adjudicated target behavior wins.

## Decision

### 1. Canonical domain language

The canonical business hierarchy is:

```text
Tenant
└─ Site
   ├─ Space (recursive spatial containment)
   ├─ Asset (maintainable physical business object)
   └─ Device (addressable communication/control endpoint)
      └─ Point (canonical typed data/control point)
```

`PhysicalSensor` is optional. It exists only for a real probe with an independent identification, installation, replacement, calibration or traceability lifecycle. It never replaces Point as the canonical measurement identity and is never required between Device and Point.

Calculated/derived results are `Metric` facts, not synthetic Device Points. Point remains the operational data/control identity owned by Registry and referenced by Telemetry, Command and Edge.

Organization, Area and Equipment are no longer canonical API, contract or code vocabulary. Display labels may be localized, but compatibility DTOs, fallback routes and dual domain names are forbidden.

### 2. Ownership is logical, not process-shaped

Every authoritative resource has exactly one bounded-context owner. The owner alone may mutate its authoritative state. Other contexts may:

- call the owner's command/query port;
- consume owner-authored immutable events;
- maintain explicitly rebuildable projections;
- store references containing owner identity and revision.

Physical process convergence does not change this rule. Modules co-located in one deployable may not write each other's schemas or bypass owner ports.

### 3. Registry owns identity, topology and immutable templates

Registry owns:

- Site, Space, Asset, Device, optional PhysicalSensor and Point identity/lifecycle;
- typed, effective-dated placement/subject/control/reporting bindings;
- external identity mappings;
- immutable Asset/Device Template Revisions and their effective assignments;
- stable references from a Template Revision to released artifacts owned by other domains.

Registry does not own transport credentials, active transport sessions, Rule execution state, Alarm lifecycle, Notification delivery, Edge runtime state or provider secrets.

A Template Revision may describe stable classification, required capabilities, expected Point schema and references. It may not embed mutable live credentials, Rule bodies, Alarm state, Edge runtime state or secrets.

### 4. Domain releases share a language but keep separate owners

Released artifacts use a common lifecycle and metadata vocabulary:

```text
DRAFT -> VALIDATED -> RELEASED -> RETIRED
identity + revision + schemaVersion + digest + dependencies + createdBy + releasedBy
```

Once RELEASED, an artifact is immutable. Rollback or reassignment creates a new assignment/release decision; it never edits a released revision in place.

The owners remain separate:

- Registry: TemplateRevision / TemplateAssignment;
- Connectivity: TransportProfileRevision / DesiredConfigurationRevision / Credential policy reference;
- Rule Runtime: RuleRevision / RuleBinding;
- Alarm: AlarmPolicyRevision;
- Presentation: DashboardViewDefinition or other first-party presentation revision;
- Edge: signed EdgeRelease;
- Intelligence: ModelDefinition / ModelDeploymentRevision;
- Platform Operations: ProductReleaseManifest, which locks exact domain revisions and coordinates validation, rollout and rollback evidence without becoming their business owner.

### 5. Telemetry truth is split by fact type, never by convenience cache

Telemetry Runtime owns ingest acceptance and the durable current Device Observation Snapshot in PostgreSQL. Business Revision advances only when committed current runtime truth changes.

ClickHouse owns append-only raw/history facts after accepted publication. Valid out-of-order observations remain queryable and never roll current state backward.

Redis is only a rebuildable projection/cache. Redis availability may affect latency or fan-out, but never the correctness or existence of current business truth.

Telemetry Query owns public typed history/aggregation query semantics, not the underlying fact identity. Metric Engine owns versioned Metric definitions, runs and append-only Metric Result facts. A Metric result never silently becomes raw Telemetry or a Point.

Analytics and Dashboard summaries are derived read models with explicit revision, watermark, completeness and quality. They cannot become write authorities for Registry, Telemetry, Alarm or Command.

### 6. Rule Runtime owns execution, not business effects

Rule Runtime owns generic Node definitions, Rule revisions/bindings, executions, durable continuations, generic Rule state, trace/debug evidence and typed Effect Intents.

A Rule node cannot directly:

- mutate Registry;
- write raw Telemetry or Metric facts;
- create/clear/ack an Alarm;
- approve or complete a Command;
- send email/SMS/Slack/REST/MQTT/Kafka/Rabbit/cloud effects;
- read credential material;
- mutate Work Orders or AI findings.

Rule Runtime persists the intended effect and hands it to the authoritative domain. The target domain validates scope, revision, policy and idempotency again and owns the final fact/receipt.

Specialized domain evaluators remain owned by their domain. Alarm duration/repeat/clear state is Alarm state, Metric calculation state is Metric state, and Operations Investigation state is Operations state; they may reuse typed expression, scheduler or work primitives without surrendering ownership to generic Rule Runtime.

### 7. Alarm uses orthogonal facts

Alarm owns versioned Alarm policies, evaluation state and Alarm Incidents. The canonical Incident model separates:

- condition state: ACTIVE / CLEARED;
- acknowledgment fact;
- current and peak severity;
- suppression fact and expiry;
- assignment history;
- immutable timeline;
- Work Order links;
- Notification disposition/projection.

Acknowledgment does not clear a condition. Suppression does not clear or delete it. Work Order completion does not clear it. Notification read/delivery state does not acknowledge or clear it.

One Tenant/Site/Fingerprint has at most one active Incident. Recovery then recurrence creates a new Incident linked by correlation, rather than reopening the historical Incident.

### 8. Notification policy and outbound delivery have different owners

Notification owns Audience definitions, Template revisions, Notification policy/rule revisions, escalation stages, in-app Inbox items and business disposition.

External delivery is owned by the shared Outbound Delivery context:

```text
DeliveryIntent -> DeliveryAttempt -> DeliveryReceipt -> DeadLetter/ReplayApproval
```

Notification submits a durable external-delivery intent referencing its immutable Template/recipient snapshot. The Delivery owner handles CredentialRef, destination policy, retry classification, lease/fence, provider receipt, outcome-unknown and dead-letter behavior. Notification derives its business disposition from those receipts but never owns provider retry internals.

Non-notification outbound integrations use the same Delivery owner. AI/provider calls that require durable external-effect/retry evidence reuse the same Attempt/Receipt and egress-policy pattern; advisory synchronous model inference remains owned by the Intelligence invocation lifecycle.

### 9. Command governance and Edge actuation form a one-way safety chain

Cloud Command owns Command Intent identity, authorization, approval, idempotency, lease/fence, dispatch state and authoritative business outcome.

Connectivity/Transport owns delivery sessions and protocol mechanics, not Command success.

Edge owns local Process Image, Controller/Scheduler/Arbiter execution, effective value selection, local safety/interlock decisions and actuation attempt evidence.

Device/PLC firmware/hardware remains the final hard-real-time safety boundary.

The chain is:

```text
Authorized Cloud Command Intent
  -> approved leased/fenced dispatch
  -> transport delivery
  -> Edge capability + local lease + scheduler + arbiter + interlock
  -> device/PLC actuation
  -> independent reported-state/readback evidence
  -> Cloud Command verification and final outcome
```

Transport ACK, broker ACK, Edge receipt or device ACK alone never means business success. Cloud cannot bypass Edge arbitration. Edge cannot extend an expired Cloud lease or rewrite Cloud approval/governance facts.

### 10. Edge is a projection/execution authority, not a second Cloud Registry

Cloud Registry owns master identity/topology. Edge receives versioned, signed, assignment-scoped projections and reports observed capabilities/state.

For a field, only one side is writable. Edge-originated differences are Observed Facts or Proposals; Cloud acceptance creates a new owner revision.

Edge synchronization uses immutable bootstrap snapshots plus incremental owner revisions, durable DeliveryItems, contiguous ACK cursors, tombstones and quarantine. A failed or incomplete snapshot never mutates active state in place.

Offline operation continues only from the last accepted signed release and within local safety/lease rules. Safety/control/audit evidence has retention priority over low-value diagnostics.

### 11. Identity Provider and IAM are separate authorities

The Identity Provider owns authentication credential lifecycle and authentication assurance. HVAC IAM owns Tenant/Site authorization, Capability grants, Explicit Deny, delegation and policy decisions.

Browser sessions remain BFF-managed; the browser does not own long-lived bearer credentials. High-risk Command and security actions require explicit authentication assurance/step-up evidence.

The target architecture does not require a particular IdP vendor, but the platform must consume a maintained standards-compliant OIDC/OAuth2 boundary. The current minimal identity-service is not automatically the target general-purpose IdP merely because it exists.

### 12. Presentation owns configuration, never business truth

Dashboard/BigScreen/other presentation definitions may own layout, view state, typed datasource references and navigation intents. They do not own Device, Metric, Alarm, Command, Rule or authorization facts.

The primary Site dashboard consumes a coherent SiteDashboardSummary with explicit as-of time, watermarks, completeness, quality and denominator policy. BigScreen consumes the same authoritative summary/projection family.

A presentation action that leads toward control navigates to Command Preview; Command Domain re-authorizes and creates the Intent. Presentation code never directly dispatches control.

### 13. Platform Operations owns reliability mechanisms, not domain facts

Platform Operations owns AvailabilityTier, LimitPolicy, migration/release execution, generic durable Work/Dead Letter conventions, readiness semantics, observability and ProductReleaseManifest.

Phase 1 is `SINGLE_NODE_RECOVERABLE`, not HA. Kafka, actor clustering, shared rate-limit backends and multi-instance partition ownership are introduced only after capacity/availability evidence crosses a documented threshold.

Migration and release never use runtime fallback, dual write or schema-version bypass to pretend compatibility. Service startup fails closed on unsupported schema/product versions.

## Canonical bounded-context seams

| Context | Authoritative writes | May consume/reference | Must never own |
| --- | --- | --- | --- |
| Identity Provider | credentials, authentication assurance | Tenant membership hints as input only | platform authorization |
| IAM | Tenant/Principal/Membership/Capability/Explicit Deny/Delegation | IdP assurance, Registry scope | device/telemetry/control facts |
| Registry | Site/Space/Asset/Device/PhysicalSensor/Point, typed bindings, templates | IAM actor, domain release references | credentials, runtime sessions, alarm/command state |
| Connectivity | integration/transport profiles, CredentialRef lifecycle, sessions, provisioning, desired/reported config, OTA campaign governance | Registry identities, Command intents | Registry identity, Command success |
| Telemetry Runtime | acceptance/quarantine/current Device snapshot/revision | Registry Point/binding revisions | Registry lifecycle, Metric facts |
| Telemetry Query | query/cursor/aggregation contract and read projections | raw/history/current facts | source facts |
| Metric | metric definitions/runs/results/current metric projection | Telemetry/Registry revisions | raw Telemetry, Alarm lifecycle |
| Command | intents/approvals/attempts/leases/fences/outcomes/verification | IAM, Registry command/readback bindings, Edge evidence | Edge arbitration internals |
| Edge Control | channels/process image/controllers/arbiter/local leases/observed state/sync evidence | signed Cloud releases/intents | Cloud approval, Registry master data |
| Rule Runtime | rule revisions/bindings/execution/state/continuation/effect intent | owner facts/snapshots | target-domain final effects |
| Alarm | alarm policies/evaluation state/incidents/ack/suppression/assignment/timeline | Telemetry/Metric/FDD evidence | notification provider delivery |
| Notification | audience/template/policy/escalation/inbox/disposition | Alarm/other trigger facts, Delivery receipts | provider retry/credential internals |
| Outbound Delivery | integration definitions/intents/attempts/receipts/dead letter | owner requests, CredentialRef | originating business state |
| Work Order | work order aggregate/timeline/links | Alarm/Asset references | Alarm recovery truth |
| Intelligence | Investigation/Evidence/Finding, Forecast facts, Optimization recommendations, model deployment/invocation | governed owner reads | direct control/alarm truth |
| Presentation | view definitions and derived presentation projections | owner read models | business truth or authorization |
| Platform Operations | release/migration/limits/readiness/work conventions | owner health/revisions | domain business facts |

## Forbidden paths

The target architecture explicitly forbids:

1. Organization / Area / Equipment compatibility vocabulary in new production contracts or APIs.
2. Free-form Relation graphs as authoritative HVAC topology.
3. Mutable cross-domain God Profiles.
4. Cross-owner direct database/schema writes, even when modules share one process.
5. Browser-side reconstruction of authoritative site/device/alarm/energy truth from partial lists.
6. Redis, transport offsets, queue offsets, caches, actor mailboxes or presentation state as business authority.
7. Rule nodes directly performing cross-domain writes, credential reads or external side effects.
8. Alarm acknowledgment, suppression, notification or Work Order state being used as recovery truth.
9. Cloud Command bypass of Edge safety/arbiter or transport ACK being treated as physical success.
10. Edge and Cloud both mutating the same authoritative field.
11. AI/FDD/Optimization recommendations directly creating physical effects without the normal governed Command path.
12. Secret material in templates, Rule JSON, Notification payloads, Edge release payloads, audit records, logs or browser APIs.
13. Released artifacts being edited in place.
14. Runtime dual-write, fallback read or compatibility layer used to preserve a superseded architecture.
15. Demo/Mock/ThingsBoard runtime fallback inside Real Mode.

## Consequences

- ADR 0001 remains historical evidence for UUIDv7, cursor/RLS and single-owner Registry principles, but its Organization/Equipment public model and fallback route assumptions are superseded.
- ADR 0011 is superseded where it defines Organization/Area/Equipment as canonical and Sensor as a generally first-class measurement identity; Point is canonical and PhysicalSensor is optional.
- ADR 0012 remains accepted for Edge IPO/Process Image/Controller/Scheduler/Arbiter/Driver/Bridge mechanisms, but its Area/Equipment/Sensor terminology is interpreted through this ADR's Tenant/Space/Asset/optional PhysicalSensor model.
- The ten ThingsBoard adjudication documents remain source evidence. Where their local terminology or cross-domain ownership differs, this ADR is the final cross-domain decision.
- Runtime implementation is not completed by this ADR. The implementation order and migration/removal slices belong to #244.

## Evidence

Primary decision inputs:

- `docs/architecture/thingsboard-security-tenancy-adjudication.md`
- `docs/architecture/thingsboard-entity-relation-device-asset-profile-adjudication.md`
- `docs/architecture/thingsboard-connectivity-rpc-ota-adjudication.md`
- `docs/architecture/thingsboard-telemetry-storage-calculated-fields-adjudication.md`
- `docs/architecture/thingsboard-rule-engine-queue-scheduling-debug-adjudication.md`
- `docs/architecture/thingsboard-alarm-notification-disposition-adjudication.md`
- `docs/architecture/thingsboard-dashboard-widget-mobile-adjudication.md`
- `docs/architecture/thingsboard-edge-sync-offline-remote-configuration-adjudication.md`
- `docs/architecture/thingsboard-ai-analytics-integrations-adjudication.md`
- `docs/architecture/thingsboard-operations-platform-deployment-ha-observability-upgrade-adjudication.md`
- `docs/architecture/thingsboard-hvac-module-reverse-audit.md`
- `CONTEXT.md`
- `contracts/registry/s1-registry-model.v1.json`
- ADR 0003, 0006, 0009, 0010, 0011 and 0012.
