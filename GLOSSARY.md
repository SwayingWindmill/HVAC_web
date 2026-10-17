# Platform Domain Glossary

## Tenant

The top-level data-isolation and ownership boundary. Sites belong directly to one Tenant.

## Site

An operational location within one Tenant. A Site is the primary business scope in which Spaces, Assets, Devices, Points, telemetry and operating decisions are observed.

## Space

A recursive spatial scope below one Site, such as a Building, Floor, Zone, Room, Plant Room or Rooftop. Space expresses where something is installed or observed; it is not an Asset and it does not own Device identity.

## Asset

A maintainable physical business asset. Asset is not interchangeable with a Device. Asset placement in a Space is an effective-dated binding so relocation does not erase installation history.

## Device

An addressable controller, gateway, meter or other independently communicating endpoint with an immutable platform identity. External-system identifiers are mappings, not Device identity.

## Physical Sensor

An optional, independently identifiable physical probe or sensing element that exists only when its own installation, replacement, calibration or traceability lifecycle must be managed. A Physical Sensor is not a canonical data point and is not required between Device and Point.

## Point

The canonical typed data or control point owned by a Device. Device-to-Point is the canonical data-model relationship. A Point may optionally reference a Physical Sensor when a real probe requires independent lifecycle traceability; most Points do not require one.

## Measured Subject

The Site, Space or Asset that a Point describes. A Physical Sensor may carry installation or traceability context, but Point remains the canonical measurement identity used by telemetry and downstream data products.

## Independent Point Observation

One point value sampled at its own observed time and published according to its own schedule. Independent reporting does not require the Sensor to be a separate Device Endpoint.

## Registry Lifecycle

The administrative state of a registered resource, such as `ACTIVE`, `INACTIVE` or `RETIRED`. Registry Lifecycle says whether the resource participates in the managed inventory; it does not say whether a Device is currently connected or producing usable data.

## Presence Applicability

Whether the platform is expected to evaluate Presence for a Device under an explicit policy. It is `APPLICABLE` or `NOT_APPLICABLE` and is separate from Registry Lifecycle. A non-applicable Device has no current Presence or Device Display State; it is not `UNKNOWN`.

## Presence Signal

A trusted observation that a Device or its authoritative upstream source was active at a specific `observedAt` instant.

## Device Presence

The platform's current conclusion about Device reachability from accepted Presence Signals and a versioned Presence Policy. Device Presence is `ONLINE`, `OFFLINE` or `UNKNOWN`.

## Presence Policy

The versioned rules that define accepted Presence Signals, the online window, the offline threshold and required observation coverage for a Device or Device class.

## Last Seen

The greatest `observedAt` among accepted Presence Signals. Last Seen is not the time the platform happened to receive or read an old record.

## Evaluation Availability

Whether the platform can make a trustworthy current Presence and Telemetry evaluation. It is `AVAILABLE` or `UNAVAILABLE`; unavailability is a platform observation failure, not evidence that a Device is offline.

## Telemetry Observation

A value for one Device key, with the time it was sampled and the time it was accepted by the platform.

## Sampled At

The instant represented by a Telemetry Observation according to the trusted source contract.

## Received At

The instant the platform accepted a Telemetry Observation. Received At measures transport and ingest delay; it does not replace Sampled At.

## Evaluated At

The instant at which Presence, Freshness and derived states were calculated.

## Telemetry Freshness

The age classification of the latest accepted Telemetry Observation under a versioned key policy. It is `FRESH`, `STALE` or `MISSING`.

## Telemetry Quality

The V2 quality classification of a Telemetry Observation after validation and governance. It is `GOOD`, `PARTIAL`, `ESTIMATED`, `MANUAL`, `STALE` or `INVALID`. Quality is independent of ingest acceptance; rejection, quarantine, duplicate and out-of-order handling are ingest decisions or evidence, not quality values.

## Required Telemetry Set

The versioned set of keys that must be current and usable for a specific Device view, diagnostic rule or future action. Optional keys do not degrade that consumer's readiness.

## Telemetry Readiness

The aggregate usability of a Required Telemetry Set. It is `CURRENT`, `DEGRADED`, `INCOMPLETE` or `NOT_APPLICABLE`.

## Device Display State

A mutually exclusive user-interface summary derived from Evaluation Availability, Device Presence and Telemetry Readiness. It is `ONLINE`, `OFFLINE`, `STALE`, `UNKNOWN` or `UNAVAILABLE`; it is not an authoritative stored fact.

## Last Known Value

The most recent accepted value retained for historical context. A Last Known Value may be shown with its timestamp when current evaluation is stale or unavailable, but it must never be presented as a current value.

## Site Observation Summary

A count-and-coverage summary of authorized Devices by Device Display State. A Site does not have a single inherited online/offline boolean.

## Telemetry Runtime

The platform domain that accepts source observations and owns current Device runtime truth, including Presence, latest accepted telemetry, policy evaluation and publication intent. Telemetry Runtime is distinct from Registry identity, authorization and transport.

## Device Observation Snapshot

A coherent current evaluation of one Device at one Business Revision. It combines the canonical Presence, Availability and telemetry dimensions without exposing source-system identifiers.

## Business Revision

A monotonic owner-authored revision for one Device Observation Snapshot. It advances only when committed current runtime state changes; source replay, cache refresh and transport retry do not advance it.

## Source Position

The upstream event identity or offset used to detect duplicate, replayed and out-of-order source delivery. Source Position is evidence about ingest order, not a public recovery cursor or Business Revision.

## History Sequence

The number Telemetry assigns to a history batch when it claims it, stored on every history observation the batch writes. Batches become visible one at a time, so History Sequence orders history by visibility, which downstream projections use as a durable cursor. It is not Source Position, Sampled At or Business Revision.

## Plant Efficiency

Cooling energy delivered divided by HVAC electricity over the same Site-local period (shown as COP, 冷站综合能效). Both come from projected meter energy; a period missing either has no Plant Efficiency, never zero.

## Transport Position

The delivery position used by a realtime transport for bounded reconnect recovery. A Transport Position does not establish business ordering or Snapshot authority.

## Recovery Cursor

An opaque, scope-bound request to attempt incremental recovery from a previously applied Business Revision and Transport Position. A Recovery Cursor has no authority of its own and must be reauthorized; failure returns the consumer to an authoritative Device Observation Snapshot.

## Ingest Quarantine

The evidence state for a source candidate that cannot become current runtime truth because its source, mapping or validation is not acceptable. Quarantined candidates never create Devices or replace accepted values.

## Telemetry Key Selection

The exact ordered set of Device keys requested and authorized for one Snapshot or live subscription. The owner never adds unselected keys, and an empty selection means Presence-only observation.

## Subscription Bootstrap

A short-lived setup result that lets a caller establish an exact authorized live subscription. Bootstrap capabilities do not become authorization facts and do not enlarge the caller's Scope.

## Observation Delta

A subscription-scoped transition from one Device Business Revision to the next. It carries the current canonical Device state and only selected key changes; it may contain no key changes while still advancing the Device revision.

## Release Envelope

A versioned, measured upper bound for the workload a release is certified to serve while meeting its security, correctness, latency and resource-headroom gates. A Release Envelope is not a permanent platform limit and cannot be increased without new evidence.

## Shadow Comparison

A side-effect-free comparison of Legacy and S2 read results used only to produce migration evidence. Shadow Comparison cannot write, publish, authorize, repair mappings or influence current state.

## Rollout Cohort

A deterministic set of authorized callers or resources assigned to one route-owner revision during a phased release. A Cohort changes owner only through an explicit promotion or rollback decision; it never chooses a per-request fallback.

## Real Mode

The HVAC Web operating mode in which every displayed business fact comes from an authoritative platform API or an explicitly defined derived read model. Missing, unavailable or unauthorized capabilities remain visibly unavailable; Real Mode never substitutes Mock, Legacy or fabricated values.

## Demo Mode

A separately identified, non-authoritative presentation mode that may use deterministic fixture data for product demonstration. Demo Mode must be visibly distinguishable from Real Mode and its values, actions and state transitions must never be presented as production facts or silently mixed into Real Mode.

## Agent Session

One operator's question to the Operations Agent about one Site and the conversation that follows it. A Session owns its Runs, the finalized messages, Tool Executions and Agent Artifacts; it is a product record independent of the model or the pi runtime that answered it.

## Agent Run

One bounded attempt at answering within an Agent Session: the model and Tool loop from the latest operator message to a terminal outcome. A Session has at most one running Run; an interrupted Run is recorded as failed and the next Run starts from the committed Session, never from runtime state.

## Agent Tool

A server-defined, read-only capability the model may call within a Run, such as reading the Site's energy series. Each call is authorized again by the Gateway for the signed-in operator before the owner answers; there is no Tool that changes equipment or submits commands.

## Semantic Query

An Agent Tool's request for up to four measures of one Cube view, optionally grouped by up to two of its dimensions, per hour, day or month. Only members in the Semantic Catalog can be named; the Site is never a parameter but comes from the Session's grant, and the query service checks the catalog again before it reads.

## Semantic Catalog

The list of view members a Semantic Query may name, with their Chinese titles and descriptions, generated from the Cube model (`npm run semantic:catalog:generate`). It leaves out the Site, the time dimension and freshness measures; a member the catalog does not list cannot be asked for.

## Agent Artifact

A typed, inspectable result of a Run: an Evidence Reference to an owner fact a Tool returned, a Finding, a Proposal, an Input Request to the operator, the operator's Input Response, or a Limitation. An Artifact cites platform facts but never becomes their owner; a Proposal is not an approval, Command Intent or proof that anything changed.

## Operator-Provided Information

Information explicitly supplied by an authorized operator to an Agent Session. It may guide planning and may be cited as operator testimony, but it is not automatically an authoritative Device, Telemetry, Analytics or Command fact.

## Requested Operational Scope

The exact Tenant, Site, Asset, Device and time boundary that a caller attempted to access but that is not part of the caller's authorized operational Scope. Requested Operational Scope may appear only in authorization decisions and their Evidence; it never authorizes Registry, Telemetry, Analytics or Command reads and must not disclose whether the requested resource exists.

## Workload Principal

An IAM principal that is one of the platform's own unattended workloads, identified by its workload identity rather than by a person. It is authorized by the same Tenant memberships and role bindings as a person and acts only on its own behalf, never through a user session. It is not a tenant-created integration account.
_Avoid_: service account, system user, robot user

## Role

A named set of IAM capabilities. Built-in Roles (`viewer`, `operator`, `site-manager`, `admin`; 只读、运维人员、运维主管、管理员) are shipped by the platform and cannot be edited by a Tenant; Tenant-owned roles exist only for Workload Principals.
_Avoid_: permission group, profile

## Role Assignment

The only authorization fact: one Principal holds one Role in one Tenant, scoped to the whole Tenant (including Sites added later) or to one Site. Access is the union of a Principal's active Role Assignments minus explicit denies (ADR 0022). Stored in `iam.role_bindings`.
_Avoid_: site binding, permission row, grant (a grant is the short-lived signed delegation IAM issues per decision)

## Domain Owner

The single bounded context that is authoritative for creating and changing one class of business fact. Physical co-location with another module never transfers Domain Ownership; other contexts interact through governed commands, queries, owner-authored events or rebuildable projections.

## Template Revision

An immutable released description of stable Asset or Device classification, required capabilities, expected Point shape and references to other domain releases. A Template Revision does not contain live credentials, runtime state, Alarm state, Rule execution state or secrets.

## Release Reference

A stable reference from one released or assigned artifact to the exact immutable revision owned by another domain. A Release Reference coordinates compatibility without transferring ownership or copying the referenced domain configuration into a mutable cross-domain profile.

## Product Release Manifest

An immutable coordination record that locks the exact released revisions intended to operate together for one product rollout. It coordinates validation, deployment, rollback evidence and compatibility, but it does not become the owner of the referenced domain facts.

## Metric

A versioned derived fact computed from authoritative inputs under an explicit definition, unit, quality policy and provenance. A Metric is not a synthetic Device Point and never overwrites raw Telemetry Observations.

## Command Intent

A governed request for a physical or logical control change after authorization and before execution. A Command Intent remains distinct from approval, transport delivery, Edge arbitration, device acknowledgment, readback and verified physical outcome.

## Outcome Reconciliation

A person with control authority over a Device stating whether a Command whose outcome is unknown took effect (APPLIED or NOT_APPLIED). It settles the Command Intent and releases the Device's hold, while its Attempt keeps the unknown outcome as evidence. It is a recorded statement, not a verified physical outcome (ADR 0018).

## Alarm Incident

One durable occurrence of an Alarm condition for a stable fingerprint. Its condition state, acknowledgment, suppression, assignment, Work Order links and Notification disposition are separate facts; recurrence after recovery creates a new Incident rather than reopening the historical occurrence.

## Alarm Condition State

Whether the physical or logical condition represented by an Alarm Incident is currently `ACTIVE` or `CLEARED`. Acknowledgment, suppression, Notification delivery and Work Order completion do not change Alarm Condition State.

## Delivery Intent

A durable request to submit one governed payload to an external destination under an immutable business identity and destination policy. Delivery attempts, provider receipts, retries, dead-letter handling and replay are evidence about delivery; they never become the originating domain's business fact.

## Virtual Central Plant

A non-production-authoritative physical, environmental and field-protocol simulator used for development and acceptance. It may simulate exogenous conditions, equipment behavior, sensors and actuators, but it never authors Telemetry, Alarm, FDD, Forecast, Optimization, Notification or Work Order business facts.

## Virtual Plant Scenario

A non-business configuration that supplies explicit exogenous physical inputs to a Virtual Central Plant over time. A Virtual Plant Scenario is not Registry identity, Telemetry, runtime state or another business-domain fact.

## Historical Replay

A controlled historical runner that reuses authoritative physical/scenario logic to produce past-time observations for Telemetry-owned history. Historical Replay preserves historical Sampled At and truthful current Received At, and it never mutates current latest telemetry, Presence or Business Revision.

## Historical Replay Dataset

A stable non-business provenance scope for one deterministic Historical Replay sequence across retries or runner restarts. Its identity exists only to preserve replay ordering and idempotency; it is not Registry, Telemetry current state, Forecast, Optimization or another business-domain fact.

## Vendor Template Certification

Acceptance evidence that one immutable released Device Template revision has been validated against real vendor hardware and its authoritative protocol specification. Certification verifies the released mapping and behavior; it is not a mutable Template status and does not replace the released revision itself.

## Gateway

A Device that holds a Gateway Credential and carries telemetry, events and commands for the Devices behind it. A Gateway's Tenant and Site come from Registry, never from what the Gateway sends.
_Avoid_: integration instance, edge node, connector

## Gateway Credential

The certificate the platform issues to one Gateway; its subject is the Gateway's Device identity. A Gateway without an active credential may still connect, but none of its messages are accepted and no commands are sent to it.

## Enrollment Code

A one-time, short-lived code an operator issues for one registered Gateway, which the Gateway exchanges once for its first Gateway Credential.
_Avoid_: provisioning key, activation token

## Device Source Key

The name a Gateway uses in its messages for one Device behind it. It is a Registry mapping scoped to that Gateway, not Device identity.
_Avoid_: external device id, child id

## Site Profile

What the Registry records about a Site beyond its identity, to compare it with others: its building floor area (建筑面积, m²), which energy per square metre is taken on (GB/T 51161), and its GB 50176 thermal design zone (气候区). Both are optional; a Site without a floor area is not ranked on energy per square metre.
_Avoid_: site metadata, site attributes

## Cooling Degree Days

CDD26 (冷却度日): over a period, the sum of each day's mean outdoor dry-bulb temperature above 26 °C, read from the Site's own weather station. Benchmarking divides electricity per square metre by it (单位度日用电, kWh/m²·℃·d) to compare Sites in different weather, over the days that have both electricity and a mean temperature. A Site without a weather station has none.
_Avoid_: weather normalization, climate-adjusted energy

## Sub-meter

A meter on the feed into one piece of equipment (分表), as opposed to the Site's total meter. Its energy is already inside the Site total: a Meter Binding on an edge from a non-grid node into a device's load node has accounting scope BRANCH, every other one SITE, and a Site's total counts only SITE energy. What the total meter reads beyond the sub-meters is 未分项 (unallocated), shown as it is even when negative.
_Avoid_: branch meter, child meter

## Energy Conservation Measure

One change to how a Site's plant runs or what it is made of, registered so its savings can be verified (ADR 0021). It moves from planned to implemented to ended. Measures report at the same time only on different machines' sub-meters (see Measurement Boundary).
_Avoid_: project, ECM record

## Measurement Boundary

Where a measure's electricity is measured (测量边界): the plant's total meter (IPMVP Option C) or one machine's Sub-meter (Option B). It is chosen at registration and never changes. Two measures may report over the same period only on different machines; a measure on the total meter reports alone, since the total holds every sub-meter.
_Avoid_: scope, measurement point

## Baseline

The line fitted over a period before a measure was implemented that predicts the hourly electricity on its Measurement Boundary from the cooling the plant delivered. It is frozen at implementation and trusted only when it passes ASHRAE Guideline 14; it can be refitted on another period until the first Verified Day is published.

## Adjusted Baseline

What the Baseline predicts the plant would have used under the conditions actually seen after implementation. Avoided energy is the adjusted baseline minus the electricity actually used.
_Avoid_: expected consumption, reference consumption

## Report Snapshot

A Site's closed month as its energy report shows it, taken once by report-service after the month ends and never changed. Taking it again keeps a new version; the report shows the latest. Demand, alarms and work orders are not in it and are worked out live beside it.
_Avoid_: archived report, frozen report

## Rule Revision

One immutable version of a diagnosis rule FDD holds for a Tenant (诊断规则版本): for chilled-water low delta-T, the threshold and the window of minutes it is read over. An evaluation uses the revision in effect at its time and its finding cites it; changing the threshold publishes a new revision.
_Avoid_: rule config, threshold setting

## Verified Day

One Site-local day of a measure's reporting period, verified once against its Baseline and never changed afterwards. Hours missing data count against its coverage; they are not filled in.
