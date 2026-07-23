# Platform Domain Glossary

## Organization

The top-level business and authorization boundary that owns Sites.

## Site

An operational location within one Organization. A Site is the scope in which Devices, Equipment, telemetry and operating decisions are observed.

## Equipment

A maintainable business asset. Equipment is not interchangeable with a Device.

## Device

An addressable IoT endpoint with an immutable platform identity. External-system identifiers are mappings, not Device identity.

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
