# Platform Domain Glossary

## Organization

The top-level business and authorization boundary that owns Sites.

## Site

An operational location within one Organization. A Site is the scope in which Devices, Equipment, telemetry and operating decisions are observed.

## Area

A recursive spatial scope below one Site, such as a Building, Floor, Zone, Room, Plant Room or Rooftop. Area expresses where something is installed or observed; it is not Equipment and it does not own Device identity.

## Equipment

A maintainable physical business asset. Equipment is not interchangeable with a Device. Equipment placement in an Area is a versioned binding so relocation does not erase installation history.

## Device Endpoint

The preferred domain term for a Registry Device when discussing physical integration. A Device Endpoint is an addressable controller, gateway, meter or independently communicating sensor with an immutable platform identity. External-system identifiers are mappings, not Device identity.

## Device

The Registry identity used for a Device Endpoint. Existing APIs retain the shorter name `Device`.

## Sensor

A first-class measurement identity with installation, calibration, replacement and quality lifecycle. A Sensor may report through a controller Device Endpoint or may itself be an independently communicating Device Endpoint.

## Telemetry Point

A typed data channel owned by a reporting Device Endpoint and optionally a Sensor. A Sensor may expose multiple Telemetry Points, and controller-internal points may exist without a separately managed Sensor.

## Measured Subject

The Site, Area or Equipment that a Sensor or Telemetry Point describes. Measured Subject is independent of mounting location and reporting Device Endpoint.

## Calculated Point

A Telemetry Point derived from ordered input point references and a versioned formula. Its value and quality preserve input provenance; it is not a direct sensor observation.

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

The trust classification of a Telemetry Observation after type, unit, range, timestamp and source validation. It is `GOOD`, `SUSPECT` or `REJECTED`.

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

## Operations Investigation

A durable, reviewable body of work in which the platform coordinates authorized evidence gathering, analysis, findings and proposed next actions for an operational question. An Operations Investigation is a business record and remains meaningful independently of any Agent framework or execution checkpoint.

## Operations Investigation Snapshot

A framework-independent persisted representation of one Operations Investigation aggregate, including its Scope, Revision, Agent Run and Lease history, and committed effect journal. It is validated by the Domain when restored and contains no LangGraph, transport or model-provider type.

## Investigation Evidence

A scoped reference to an authoritative fact, snapshot, analytical result or governed knowledge source used by an Operations Investigation. Investigation Evidence preserves provenance, revision, quality and applicability; it does not become the owner of the referenced platform fact.

## Investigation Finding

A reviewable conclusion produced within an Operations Investigation and supported by cited Investigation Evidence. A Finding distinguishes facts, deterministic analysis results, bounded inferences, hypotheses and inability to conclude.

## Proposed Action

A reviewable recommendation produced by an Operations Investigation. A Proposed Action is not an approval, Command Intent, execution attempt or evidence that a physical change occurred.

## Agent Execution Checkpoint

A recoverable record of where an Agent execution is currently paused or which execution steps have completed. An Agent Execution Checkpoint is operational runtime state, not an Operations Investigation, Evidence, Finding, Proposed Action or other business fact.

## Agent Run

One bounded execution attempt that advances an Operations Investigation. Multiple Operations Investigations may have active Agent Runs concurrently, but one Operations Investigation has at most one write-capable Agent Run at a time.

## Agent Run Lease

A short-lived exclusive claim that authorizes one Agent Run to advance the write state of one Operations Investigation. A lease does not grant domain authorization and does not replace Investigation revision checks or idempotency.

## Investigation Revision

A monotonic revision of the Operations Investigation business record used to reject stale concurrent writes. It is distinct from an Agent Execution Checkpoint version, Device Business Revision, Dataset Revision or transport position.

## Investigation Coordinator

The application boundary through which callers start, continue, inspect, cancel or provide input to an Operations Investigation. It coordinates domain rules and execution without exposing an Agent framework, checkpoint or model-provider API.

## Investigation Step

One stable, reviewable unit of intended work within an Operations Investigation. An Investigation Step remains the same logical step across runtime retries and process attempts.

## Step Identity

The stable identity of one Investigation Step used to correlate execution, Evidence and idempotent effects. Step Identity is not the identity of a worker attempt or graph-node invocation.

## Independent Read Plan

A bounded set of governed, read-only Owner queries proposed for one Agent Run. Queries within one declared batch may execute concurrently because they do not modify Investigation business state; every resulting Evidence, Finding or Proposed Action still requires a separate serialized Coordinator write.

## Idempotency Key

The stable identity of one intended business effect or governed external submission across retries. An Investigation Step may produce multiple effects with different Idempotency Keys; reusing one Idempotency Key for a different Step or effect is a conflict.

## Tool Execution Receipt

A bounded record that an Agent Run invoked a governed tool for a specific Investigation Step. It preserves tool identity, Scope, authorization and input/output digests, outcome and correlation references without becoming the owner of the returned platform fact.

## Operator-Provided Information

Information explicitly supplied by an authorized operator to an Operations Investigation. It may guide planning and may be cited as operator testimony, but it is not automatically an authoritative Device, Telemetry, Analytics or Command fact.

## Agent Runtime Revision

The immutable revision of the Agent execution graph and runtime policy assigned to an Agent Run. An active run never changes Agent Runtime Revision mid-execution; restarting under another revision creates a new Agent Run from authoritative Investigation state.

## Operations Agent Benchmark Scenario

A versioned, framework-independent fixture that defines one operational question, its authorized Scope, authoritative input facts, Ground Truth, Evidence requirements, planning and execution constraints, tool policy, data-quality conditions and acceptance rules. A Benchmark Scenario specifies observable business behavior; it does not prescribe a prompt, model, graph implementation or checkpoint format.

## Benchmark Blocker Criterion

A deterministic acceptance invariant whose failure invalidates an Operations Agent Benchmark Scenario result regardless of any scored quality or usefulness. Authorization, tenant isolation and physical-action safety are always blocker concerns and cannot be offset by a higher aggregate score.

## Benchmark Scored Criterion

A non-blocking evaluation dimension used to compare acceptable results after every Benchmark Blocker Criterion has passed. Scored Criteria may assess qualities such as evidence completeness or operational usefulness, but they never weaken authorization, ownership or safety invariants.

## Required-Next Evidence

A structured Benchmark Evidence requirement that is not present in the current scenario input and must be obtained from its named authoritative owner before a blocked conclusion may be attempted. Required-Next Evidence identifies a governed next data product or fact; it does not authorize a tool, claim that an endpoint already exists or permit the Agent to fill the gap with inference.

## Requested Operational Scope

The exact Organization, Site, Equipment, Device and time boundary that a caller attempted to access but that is not part of the caller's authorized operational Scope. Requested Operational Scope may appear only in authorization decisions and their Evidence; it never authorizes Registry, Telemetry, Analytics or Command reads and must not disclose whether the requested resource exists.

## Benchmark Deterministic Blocker Sample

A small framework-independent candidate result used to prove that one Benchmark Blocker Criterion accepts the required behavior and rejects a known violation with a stable failure code. A Deterministic Blocker Sample is test evidence for benchmark semantics, not a model transcript or Agent checkpoint.

## Action Lifecycle Expectation

A Benchmark Scenario declaration that distinguishes a Proposed Action, formal approval, Command Intent and physical execution result. It states which lifecycle artifacts may exist at the current Investigation stage and prevents a recommendation from being represented as approval, command submission or physical success.

## Benchmark Blocker Profile

The repository-owned deterministic evaluator registered for one Operations Agent Benchmark Scenario. It runs only after the scenario contract passes, returns stable dimensioned failure codes, and must cover the scenario's authorization, ownership, data-quality, diagnostic and action-safety invariants. A scenario without a Blocker Profile fails closed.

## Operations Agent Benchmark Report

The versioned machine-readable result produced by the repository Benchmark Runner. It records discovery status, scenario and contract versions, ordered structure and blocker phases, scored criteria that remain unevaluated or blocked, and stable failure codes by scenario and dimension. A passing score can never replace or offset a failed blocker phase.
