# ADR 0002 — S2 Device Presence and telemetry freshness semantics

Status: accepted

Date: 2026-07-23

Issue: #48

## Context

S2 must replace the Legacy boolean `active` and untyped latest-telemetry behavior with platform-owned semantics that are safe for UI, alerting, FDD and future control decisions.

The current Legacy path collapses confirmed inactivity, missing activity attributes and upstream failure into `false`; returns cache-only latest values without source or freshness metadata; and has no Snapshot, Cursor or Revision recovery model. The factual boundary is documented in `docs/research/s2-telemetry-thingsboard-boundary.md`.

The terms `online`, `offline`, `stale`, `unknown` and `unavailable` cannot be one authoritative field because they answer different questions:

- Presence asks whether the Device has been observed recently.
- Freshness asks whether a telemetry value is recent enough for a consumer.
- Quality asks whether an observation is trustworthy.
- Availability asks whether the platform can make a current determination.

S2 therefore needs orthogonal public dimensions and one explicitly derived five-state UI summary.

## Decision

### 1. Registry Lifecycle is independent

Registry Lifecycle remains the administrative state of the platform Device resource. It is never an input to Presence or telemetry Freshness.

Presence Applicability is an explicit S2 fact. A Device may be administratively active while Presence is not yet applicable, and an administratively inactive Device may still have retained runtime evidence. Product workflows may decide whether to display or act on those facts, but they must not infer one from the other.

### 2. Canonical dimensions

S2 uses the following canonical dimensions.

#### Presence Applicability

```text
APPLICABLE
NOT_APPLICABLE
```

`APPLICABLE` means the platform is expected to evaluate Presence for this Device. `NOT_APPLICABLE` means no current Presence or Device Display State is asserted. A non-applicable Device is not `UNKNOWN`; it is reported separately from the five runtime states.

#### Evaluation Availability

```text
AVAILABLE
UNAVAILABLE
```

`AVAILABLE` means the owner has enough healthy dependencies and observation coverage to calculate a trustworthy current result.

`UNAVAILABLE` means the platform cannot currently make that calculation. It is a platform-state conclusion, not Device evidence.

#### Device Presence

```text
ONLINE
OFFLINE
UNKNOWN
```

`ONLINE` and `OFFLINE` are current conclusions from accepted Presence Signals under a versioned Presence Policy. `UNKNOWN` means the evaluation path is available but accepted evidence is insufficient for either conclusion.

Device Presence is not evaluated as current when Evaluation Availability is `UNAVAILABLE`; a last-known Presence may be returned separately as historical context.

#### Telemetry Freshness

```text
FRESH
STALE
MISSING
```

Freshness is calculated per Device key and per consumer policy.

- `FRESH`: an accepted observation exists and its age is within the configured freshness window.
- `STALE`: an accepted observation exists but its age exceeds that window.
- `MISSING`: no accepted observation exists for that key and scope.

#### Telemetry Quality

```text
GOOD
SUSPECT
REJECTED
```

- `GOOD`: the observation passed source, type, unit, range and timestamp validation.
- `SUSPECT`: the observation is retained and may be displayed with a warning, but cannot be treated as fully trustworthy by default.
- `REJECTED`: the observation is retained only as evidence and never replaces the latest accepted value.

Quality reason codes are additive and typed. The initial semantic families are:

```text
SOURCE_UNTRUSTED
TYPE_MISMATCH
UNIT_MISMATCH
OUT_OF_RANGE
CLOCK_AHEAD
CLOCK_BEHIND
SOURCE_LAG_EXCEEDED
DUPLICATE
OUT_OF_ORDER
REPLAYED
```

The later public-contract ticket decides the exact stable code names and wire representation.

#### Telemetry Readiness

A consumer declares a versioned Required Telemetry Set. Its aggregate readiness is:

```text
CURRENT
DEGRADED
INCOMPLETE
NOT_APPLICABLE
```

- `CURRENT`: every required key is `FRESH` and `GOOD`.
- `DEGRADED`: every required key has an accepted value, but at least one is `STALE` or `SUSPECT`.
- `INCOMPLETE`: at least one required key is `MISSING`, or only rejected candidates exist for it.
- `NOT_APPLICABLE`: the consumer has no required telemetry keys for this Device.

Optional keys never degrade readiness.

### 3. Time model

Every current evaluation exposes or internally preserves four distinct instants.

| Instant | Meaning |
|---|---|
| `observedAt` | When a trusted source observed Device activity. |
| `sampledAt` | When the telemetry value represents the physical measurement. |
| `receivedAt` | When the platform accepted the source observation. |
| `evaluatedAt` | When the platform calculated Presence, Freshness and derived state. |

`lastSeenAt` is the maximum `observedAt` among accepted Presence Signals. It is never synthesized from a cache read or from the time an old record happened to arrive.

A source contract may declare that receipt time is its only authoritative activity time. In that case the accepted Presence Signal records that time basis explicitly; the platform still does not silently rewrite it as a device-sampled time.

The platform preserves both source lag and sample age:

```text
sourceLag = receivedAt - observedAt
sampleAge = evaluatedAt - sampledAt
```

A future timestamp outside the policy's allowed clock skew cannot become the latest current value. A delayed observation may remain historical evidence but cannot replace a newer accepted observation.

### 4. Versioned policies, not a global timeout

Presence and Freshness thresholds are versioned policy data assigned by Device class or Device override. S2 does not hard-code one global number of minutes.

A Presence Policy contains at least:

```text
policyRevision
onlineWithin
offlineAfter
maxFutureClockSkew
maxSourceLag
acceptedSignalTypes
```

A key Freshness Policy contains at least:

```text
policyRevision
key
expectedSampleInterval
freshFor
valueType
unit
qualityRules
```

Policy constraints are:

- `onlineWithin` is positive.
- `offlineAfter` is greater than `onlineWithin`.
- `freshFor` is not shorter than the expected sample interval without an explicit reason.
- every result records the policy revision used for evaluation.
- changing a threshold does not rewrite historical observations; it changes later evaluations.

### 5. Presence evaluation state machine

Presence is evaluated only from accepted Presence Signals and observation coverage.

```text
                          accepted recent signal
             ┌─────────────────────────────────────────┐
             │                                         ▼
          UNKNOWN  ────────────────────────────────>  ONLINE
             ▲                                         │
             │ no evidence / uncertainty window        │ signal age > onlineWithin
             │                                         ▼
             └──────────────────────────────────────  UNKNOWN
                                                       │
                                                       │ signal age >= offlineAfter
                                                       │ and coverage remained available
                                                       ▼
                                                    OFFLINE
                                                       │
                                                       │ accepted recent signal
                                                       └──────────────> ONLINE
```

The deterministic rules at `evaluatedAt` are:

1. If Presence evaluation dependencies or observation coverage are unavailable, Evaluation Availability is `UNAVAILABLE`; no current Presence assertion is emitted.
2. If Presence Applicability is `NOT_APPLICABLE`, no current Presence or Device Display State is emitted. If Applicability is `APPLICABLE` but no valid policy, source binding or accepted Presence Signal exists, Presence is `UNKNOWN`.
3. If `evaluatedAt - lastSeenAt <= onlineWithin`, Presence is `ONLINE`.
4. If the age is greater than `onlineWithin` but less than `offlineAfter`, Presence is `UNKNOWN`.
5. If the age is at least `offlineAfter` and observation coverage was continuously available for the required decision window, Presence is `OFFLINE`.
6. If coverage was interrupted, the result is `UNAVAILABLE`, not `OFFLINE`.
7. A trusted explicit disconnect signal may produce immediate `OFFLINE` only when its source contract guarantees that meaning and the policy names that signal type.
8. Recovery from an availability gap recomputes from current evidence. The platform does not immediately declare `OFFLINE` solely because an old last-seen time predates the outage.

This uncertainty window prevents a late expected sample from becoming a false offline event immediately after the online window expires.

### 6. Telemetry acceptance and replacement

A telemetry candidate is processed in this order:

1. Authenticate and authorize its source and Device mapping.
2. Validate timestamp bounds and source lag.
3. Validate key, value type, unit and configured range.
4. Detect duplicate, replay and out-of-order delivery.
5. Assign Quality and reason codes.
6. Replace the latest accepted point only if the candidate is not `REJECTED` and is newer under the owner revision rule.
7. Advance the owner Revision only for a committed state change.

A new receipt does not prove a new physical sample. Duplicate or replayed observations do not keep a Device online and do not advance `lastSeenAt` unless the source supplies an independently accepted Presence Signal.

### 7. Derived five-state Device Display State

For an `APPLICABLE` Device, the public contract returns the canonical dimensions above. For UI sorting and concise status text it may also return a derived `DeviceDisplayState`:

```text
ONLINE
OFFLINE
STALE
UNKNOWN
UNAVAILABLE
```

The states are mutually exclusive and derived in this precedence order:

1. `UNAVAILABLE` when Evaluation Availability is `UNAVAILABLE`.
2. `OFFLINE` when current Device Presence is `OFFLINE`.
3. `UNKNOWN` when current Device Presence is `UNKNOWN` or Telemetry Readiness is `INCOMPLETE`.
4. `STALE` when Device Presence is `ONLINE` and Telemetry Readiness is `DEGRADED`.
5. `ONLINE` when Device Presence is `ONLINE` and Telemetry Readiness is `CURRENT` or `NOT_APPLICABLE`.

The derivation can be represented as:

| Availability | Presence | Readiness | Display state |
|---|---|---|---|
| `UNAVAILABLE` | any last-known value | any | `UNAVAILABLE` |
| `AVAILABLE` | `OFFLINE` | any | `OFFLINE` |
| `AVAILABLE` | `UNKNOWN` | any | `UNKNOWN` |
| `AVAILABLE` | `ONLINE` | `INCOMPLETE` | `UNKNOWN` |
| `AVAILABLE` | `ONLINE` | `DEGRADED` | `STALE` |
| `AVAILABLE` | `ONLINE` | `CURRENT` | `ONLINE` |
| `AVAILABLE` | `ONLINE` | `NOT_APPLICABLE` | `ONLINE` |

The derived state is not stored as an independent source of truth. Consumers that need safety or diagnostic decisions use the dimensions, policy revision and evidence, not the display summary.

### 8. Value-display rules

| Key condition | Primary UI value | Secondary context | Default machine use |
|---|---|---|---|
| `FRESH + GOOD` | Display normally. | Sample time and source may be shown on demand. | Allowed when the consumer policy includes the key. |
| `FRESH + SUSPECT` | Display with an explicit quality warning. | Quality reason and timestamps. | Not allowed by default. |
| `STALE` | A Last Known Value may be displayed only with a stale label and its sample time. | Age and source availability. | Not allowed as current evidence. |
| `MISSING` | Do not display a numeric current value. | Missing reason and expected key. | Not allowed. |
| only `REJECTED` candidates | Do not display a numeric current value. | Validation reason; an older Last Known Value may be secondary. | Not allowed. |
| Evaluation `UNAVAILABLE` | Do not present any value as current. | Last Known Value may be secondary with age and outage state. | Not allowed. |

A placeholder such as `—` means no trustworthy current value. Zero is a real value and must never be used as a missing-data placeholder.

### 9. Aggregation rules

#### Key to Device

Only the consumer's Required Telemetry Set participates in Telemetry Readiness. A Device may therefore be `CURRENT` for one view and `INCOMPLETE` for a different diagnostic rule.

#### Device to Site

A Site Observation Summary contains counts by Device Display State and explicit coverage, for example:

```text
totalApplicableDevices
nonApplicableDevices
evaluatedDevices
onlineDevices
offlineDevices
staleDevices
unknownDevices
unavailableDevices
evaluationCoverage
currentTelemetryCoverage
```

The summary rules are:

- unauthorized Devices do not enter either numerator or denominator.
- non-applicable Devices are reported separately when needed and do not reduce coverage.
- `UNKNOWN` and `UNAVAILABLE` are never counted as `OFFLINE`.
- Site aggregation does not produce one `siteOnline` boolean.
- percentages are omitted when the denominator is zero rather than invented as zero or one hundred percent.
- Site summaries record `evaluatedAt` and the policy revisions represented.

#### Key-family and Equipment aggregation

Equipment and key-family summaries follow the same count-and-coverage rule. They cannot promote a partial result to healthy merely because a majority of values are fresh.

### 10. Boundary scenarios

#### New Device with a valid mapping but no signal

```text
Evaluation Availability: AVAILABLE
Presence: UNKNOWN
Required telemetry: INCOMPLETE
Display: UNKNOWN
```

No offline alarm is created because the platform has never established an observed baseline.

#### Device recently observed, all required values current

```text
Presence: ONLINE
Readiness: CURRENT
Display: ONLINE
```

#### Device recently observed, one required value aged past its key threshold

```text
Presence: ONLINE
Readiness: DEGRADED
Display: STALE
```

The value may be shown as Last Known with its timestamp. It is not eligible for a new FDD conclusion by default.

#### Device recently observed, required key has never produced an accepted value

```text
Presence: ONLINE
Readiness: INCOMPLETE
Display: UNKNOWN
```

The UI may say “在线 · 数据缺失”; it must not invent a value.

#### Last Seen passed the online window but not the offline threshold

```text
Evaluation Availability: AVAILABLE
Presence: UNKNOWN
Display: UNKNOWN
```

This is the policy uncertainty window.

#### Last Seen passed the offline threshold with continuous observation coverage

```text
Evaluation Availability: AVAILABLE
Presence: OFFLINE
Display: OFFLINE
```

Stored telemetry may also be stale, but the concise display state remains `OFFLINE`.

#### Upstream observation path failed before the offline threshold

```text
Evaluation Availability: UNAVAILABLE
Current Presence: not asserted
Last-known Presence: ONLINE or UNKNOWN, with its evaluation time
Display: UNAVAILABLE
```

The outage cannot be converted into a Device offline event.

#### Platform recovers after a long observation gap

The owner first emits `UNAVAILABLE` until it has restored sufficient current evidence or completed a new decision window. It does not retroactively label every Device offline from old timestamps.

#### Device clock is ahead of the platform

The telemetry candidate is `REJECTED` with a clock reason when it exceeds policy tolerance. An independent trusted Presence Signal may still establish `ONLINE`; the invalid value does not become current telemetry.

#### Delayed or replayed telemetry arrives after a newer accepted point

The candidate remains evidence with an ordering/replay reason. It does not replace latest state, advance Revision or refresh Presence.

#### One Device key is optional for the current view

Its stale or missing state is visible in key detail but does not degrade that view's Telemetry Readiness.

### 11. Consumer invariants

#### UI

- Registry Lifecycle and Device Display State are displayed as separate facts.
- Normal values require `FRESH + GOOD` for that view.
- stale, suspect, missing and unavailable states include text, not color alone.
- Last Known Values always include their sample time or age.
- the UI never turns an unavailable platform path into an offline Device count.

#### Alerts and FDD

- Equipment-fault and diagnostic rules consume only the key states allowed by their versioned Required Telemetry Set and quality policy.
- stale, missing, rejected or unavailable data creates a data-quality or observation-gap condition, not an equipment fault by default.
- a rule that intentionally supports stale or suspect input must declare that behavior and include the evidence state in its result.
- recomputation records the policy and telemetry revisions used.

#### Future control

Presence is necessary but not sufficient for control. A future control precondition must separately require:

- Evaluation Availability `AVAILABLE`;
- Device Presence `ONLINE`;
- the command-specific Required Telemetry Set `CURRENT`;
- required values `GOOD`;
- current authorization, capability, safety and approval checks.

`OFFLINE`, `STALE`, `UNKNOWN` and `UNAVAILABLE` display states block control by default. No control path may rely only on the derived Display State.

### 12. Stable invariants

1. Registry Lifecycle never implies Presence.
2. Platform unavailability never implies Device offline.
3. `lastSeenAt` comes from accepted source observation time, not cache-read or delayed receipt time.
4. `sampledAt`, `receivedAt` and `evaluatedAt` remain distinct.
5. an older or rejected observation never replaces a newer accepted latest value.
6. missing data is not zero, false or an empty string.
7. stale or Last Known data is never presented as current.
8. optional keys do not degrade a consumer's readiness.
9. Site summaries report counts and coverage, not a synthetic online boolean.
10. UI summaries are derived; FDD, alerts and future control consume canonical dimensions and evidence.

## Consequences

The ownership ticket must select one owner for Presence Signals, accepted latest observations, policy revision and monotonic Revision. The transport experiment must preserve these semantics across disconnect, replay and permission revocation. The public-contract ticket must expose the canonical dimensions, timestamps, policy revision, reason codes and derived display state without exposing ThingsBoard identifiers.

Existing Legacy `active` cannot be mapped directly to `ONLINE` or `OFFLINE`. During migration it can only be treated as one candidate Presence Signal with declared source semantics and comparison evidence.

The model is intentionally stricter than the current UI. Some existing mock rows that display “online” from a recent-looking string will become `UNKNOWN` until a valid Presence Policy and accepted signal exist.

Changing the canonical states, their derivation precedence, the meaning of Last Seen, or the separation between availability and offline requires a new ADR and compatible contract revision.

## Architecture Decision Trace

| Issue #48 criterion | Decision location |
|---|---|
| `online`, `offline`, `stale`, `unknown`, `unavailable` and mutual exclusion | Canonical dimensions and derived Device Display State |
| Last Seen and time semantics | Time model |
| clock skew and source delay | Time model, policy and acceptance rules |
| data quality and missing keys | Telemetry Quality, Freshness and Readiness |
| Site, Device and key aggregation | Aggregation rules |
| upstream/platform failure versus Device offline | Evaluation Availability and Presence state machine |
| UI, FDD, alert and future-control invariants | Consumer invariants |
| edge cases | Boundary scenarios |
| canonical terminology | Root `CONTEXT.md` |
