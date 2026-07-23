# ADR 0004 — S2 public Snapshot, subscription and recovery contract

Status: accepted

Date: 2026-07-23

Issue: #51

## Context

S2 must expose platform-owned Device Presence and latest telemetry to HVAC Web and other authorized callers without reintroducing the Legacy split between REST latest values, a process-local Socket.IO subscription manager and ThingsBoard read-through.

ADR 0002 fixes the semantic dimensions: Presence, Freshness, Quality and Evaluation Availability are orthogonal, and the five-state Device Display State is derived. ADR 0003 fixes ownership: `telemetry-runtime-service` owns the coherent Device Observation Snapshot, per-Device Business Revision, Recovery Cursor, subscription authorization and publication intent; Gateway remains the public Session seam; Centrifugo owns only transport position and bounded recovery.

The public contract must now decide:

- which REST operations exist;
- how Presence and latest values remain coherent;
- how bounded batches represent partial results;
- what a browser may submit;
- how realtime publications advance Business Revision;
- how clients recover without mistaking transport continuity for business continuity;
- how generated clients and Legacy retirement remain compatible.

The machine-readable planned contracts are:

- `contracts/http/s2-telemetry-public.openapi.json`;
- `contracts/events/s2-device-observation-publication.v1.schema.json`.

They are planning locks and do not activate unimplemented production routes.

## Decision

### 1. Publish one coherent Snapshot model, not separate Presence and latest resources

The public read model is `DeviceObservationSnapshot`.

A Snapshot combines, at one Device Business Revision:

- Device, Organization and Site platform IDs;
- `evaluatedAt`;
- Evaluation Availability and reason codes;
- Presence Applicability;
- current and Last Known Presence context;
- `lastSeenAt` and Presence policy revision;
- selected key states;
- per-key `sampledAt`, `receivedAt`, Freshness, Quality, reason codes and policy revision;
- Telemetry Readiness;
- derived Device Display State.

Separate public Presence and latest endpoints are rejected because two calls can observe different Business Revisions and force the browser to invent merge rules. A Presence-only read is the same Snapshot operation with no selected telemetry keys. A latest read is the same operation with an exact key selection.

The single-Device operation is:

```text
GET /api/v1/devices/{deviceId}/observation-snapshot?keys=k1,k2
```

The key query is optional, unique and bounded to 64 keys. Omission or an empty selection returns a Presence-only Snapshot with `values=[]` and Telemetry Readiness `NOT_APPLICABLE`.

The response value array preserves the caller's normalized requested-key order. The service does not return an unbounded “all keys” response.

### 2. Snapshot state versus request failure

A valid Snapshot can explicitly describe non-ideal data. These conditions return HTTP `200`:

| Condition | Contract representation |
|---|---|
| Device is applicable but has no accepted Presence Signal | Presence `UNKNOWN`. |
| Requested configured key has never produced an accepted observation | `TelemetryMissingState` with `freshness=MISSING`. |
| Only rejected candidates exist | `TelemetryMissingState` with `missingReason=ONLY_REJECTED_CANDIDATES`. |
| Latest accepted value exceeded its freshness window | `TelemetryPresentState` with `freshness=STALE`. |
| Latest accepted value is usable with warning | `quality=SUSPECT` plus reason codes. |
| Upstream observation coverage is currently unavailable while owner state remains readable | `evaluationAvailability=UNAVAILABLE`; current Presence is not asserted and retained values are Last Known context. |
| Presence is not applicable | Presence Applicability `NOT_APPLICABLE`, current Presence and Display State are null. |

These are not HTTP failures because the owner successfully answered the question with authoritative state.

HTTP problems are reserved for failure to perform the authorized request:

- malformed key syntax or unknown/unconfigured requested key;
- invalid/tampered Recovery Cursor;
- missing or unauthorized Device;
- batch or subscription limit violation;
- IAM/Gateway/Telemetry Runtime unavailability;
- owner timeout;
- subscription transport bootstrap failure.

A visible Device with no current values is never `204`, `{}`, `null`, zero-filled or silently omitted.

### 3. Exact key semantics

A Telemetry Key Selection is part of authorization and response meaning.

Rules:

1. Keys are canonical case-sensitive names.
2. Duplicate keys are rejected by request validation rather than silently collapsed.
3. A syntactically invalid or unknown/unconfigured key produces `TELEMETRY_KEY_INVALID`.
4. A valid configured key with no accepted value is a successful `MISSING` key state.
5. Optional keys are selected explicitly but do not degrade a consumer's readiness unless the consumer's Required Telemetry Set names them.
6. The owner never adds unrequested keys to a response or publication.
7. A subscription to an empty key set is Presence-only and still receives every Device Business Revision.

### 4. Bounded batch read has item results, not pagination

The batch operation is:

```text
POST /api/v1/telemetry/observation-snapshots:batchGet
```

It accepts 1–100 request items. Each item has a caller-generated `requestId`, one Device ID and 0–64 exact keys. The total key selections are bounded to 2048.

There is no cursor or pagination because the caller already obtains a paginated Device list through the Registry contract and then submits an explicit bounded set.

The response is HTTP `200` when the envelope is syntactically valid and global authorization dependencies are available. It contains exactly one item per input item, in input order:

- `status=OK` plus a Snapshot; or
- `status=ERROR` plus a normal public `ProblemDetails`.

Per-item missing/unauthorized Devices use `RESOURCE_NOT_FOUND`. This reveals no more than the caller's own requested IDs.

The whole request fails instead of returning item results when:

- the JSON body is malformed;
- request IDs are missing or duplicated;
- item/key limits are exceeded;
- the Session or CSRF check fails;
- IAM authorization cannot be evaluated safely;
- the owner service is globally unavailable or times out before a bounded result set can be produced.

HTTP `207` is rejected because it adds a second success envelope with weaker generated-client support. A typed union under a normal `200` is simpler and preserves ordinary Problem Details for global failures.

### 5. Subscription bootstrap is all-or-nothing

The bootstrap operation is:

```text
POST /api/v1/telemetry/subscriptions:bootstrap
```

It accepts 1–100 exact Device/key targets and at most 2048 total key selections.

The browser may submit only:

- a caller-local `clientSubscriptionId`;
- a Registry Device UUIDv7;
- exact selected keys;
- an optional opaque Recovery Cursor issued for that same subscription scope.

The browser may not submit:

- Organization or Site scope;
- Principal, roles, scopes or IAM policy revision;
- channel names;
- connection or subscription tokens;
- ThingsBoard/Legacy IDs;
- arbitrary epoch/offset values in the bootstrap request.

Gateway derives the Principal and acting Organization from the BFF Session, obtains an exact IAM delegation, and forwards only verified context. Telemetry Runtime resolves the Device's current Organization/Site binding and authorizes the exact keys.

Bootstrap is all-or-nothing. If any target is invisible, invalid or no longer authorized, no connection token or subscription descriptor is minted. This prevents partially authorized live sets from being mistaken for a complete requested scope.

A successful response contains:

- a short-lived `wss` endpoint and connection token;
- exact subscription descriptors;
- opaque channel names selected by the platform;
- `SNAPSHOT_THEN_LIVE` or `ATTEMPT_RECOVERY` mode;
- a decoded transport position only when a supplied owner-issued cursor is valid;
- current limits.

The returned token, channel and descriptor are transport capabilities, not authorization facts. Centrifugo still calls the Telemetry Runtime subscribe proxy, which re-evaluates current authorization and exact scope.

Bootstrap responses use `Cache-Control: private, no-store`.

### 6. Recovery Cursor checkpoint is an adapter operation

The owner-issued Recovery Cursor must bind the last applied Business Revision to the matching Centrifugo Transport Position. The feature UI must not construct or interpret that token.

The generated realtime adapter uses:

```text
POST /api/v1/telemetry/recovery-cursors:checkpoint
```

After it has:

1. established the exact authorized subscription;
2. installed an authoritative Snapshot;
3. applied all contiguous buffered publications;
4. obtained the current transport epoch/offset from the transport SDK;

it submits only:

- opaque `subscriptionId`;
- applied Business Revision;
- current Transport Position.

Telemetry Runtime verifies that the Session owns the active subscription, that the Device/key scope still matches, and that the revision/position pair is acceptable. It then signs a scope-bound Recovery Cursor.

The adapter may checkpoint periodically and before planned token renewal rather than after every publication. A cursor that lags behind the latest applied state is safe: recovery may replay duplicates, which are ignored. Cursor lifetime must not exceed the recoverable history policy selected by the rollout ticket.

A caller cannot use a cursor to expand scope. Invalid, tampered, expired or mismatched cursors produce `RECOVERY_CURSOR_INVALID` or a normalized recovery-required outcome and never bypass Snapshot reload.

### 7. Publication is a contiguous subscription-scoped delta

The realtime data publication is `DEVICE_OBSERVATION_DELTA` schema version 1.

It contains:

- stable event ID;
- opaque subscription ID;
- Registry Device ID;
- `previousRevision` and `revision`;
- evaluation and publication timestamps;
- current Evaluation Availability;
- full Presence state for this revision;
- current Telemetry Readiness and Display State for the selected key set;
- only selected key states changed by this revision.

A publication represents exactly one transition:

```text
revision = previousRevision + 1
```

Every active subscription for that Device receives every Device Business Revision, even when only an unselected key changed. In that case `telemetryChanges=[]`. This small revision-only publication prevents an apparent gap at the next selected-key change without leaking an unrequested key.

Full-Snapshot publications are rejected because they multiply payload size for high-frequency keys and repeat unchanged values. Key-only publications without every Device revision are rejected because a subscriber cannot distinguish an omitted irrelevant revision from a lost publication.

Transport Position and Recovery Cursor are deliberately absent from the business publication. The Centrifugo SDK owns epoch/offset; the checkpoint operation mints the owner-issued cursor.

Publication delivery is at-least-once. Relay retry reuses the same event ID and Business Revision.

### 8. Initial subscribe and Snapshot algorithm

The generated realtime adapter implements this sequence for each exact subscription:

1. Call bootstrap without a cursor for a new subscription.
2. Connect and subscribe using the returned descriptor.
3. Buffer data publications immediately; do not apply them to an uninitialized business state.
4. Load the authoritative single or batch Snapshot for the same Device/key selections.
5. Set `appliedRevision` to the Snapshot Business Revision.
6. Process buffered publications in transport delivery order:
   - if `revision <= appliedRevision`, ignore the duplicate/old publication;
   - if `previousRevision == appliedRevision`, apply the delta and set `appliedRevision=revision`;
   - otherwise stop treating the local state as current and reload a Snapshot.
7. After the buffer is reconciled, expose the state as live/current.
8. Checkpoint the applied revision and current transport position asynchronously.

The adapter must not expose a transient state assembled from a new publication and an older Snapshot.

### 9. Reconnect and recovery algorithm

On a short connection interruption within the same transport session, the Centrifugo SDK may perform normal epoch/offset recovery.

On token renewal, page restoration or a new bootstrap, the adapter submits its most recent Recovery Cursor for the same exact Device/key selection.

For `ATTEMPT_RECOVERY`:

1. connect and subscribe with the decoded transport position;
2. buffer recovered/live publications;
3. if the transport reports successful recovery, apply the normal contiguous-revision algorithm from the cursor's Business Revision;
4. if any publication has a gap, the transport reports unsuccessful recovery, the epoch changed, history expired, or the cursor is rejected, discard incremental assumptions and use the initial subscribe-and-Snapshot algorithm;
5. checkpoint again after reaching a contiguous current state.

Transport recovery success is necessary but not sufficient. A Business Revision gap always forces Snapshot reload.

### 10. Duplicate, gap, out-of-order and invalid publication behavior

The adapter rules are deterministic:

| Publication condition | Client behavior |
|---|---|
| `revision <= appliedRevision` | Ignore as duplicate or old. |
| `previousRevision == appliedRevision` and `revision=previousRevision+1` | Apply. |
| `previousRevision > appliedRevision` | Gap: mark not current and reload Snapshot. |
| `previousRevision < appliedRevision < revision` | Invalid overlap: reload Snapshot and record protocol evidence. |
| Device or subscription ID differs from descriptor | Reject publication, close that subscription and record security evidence. |
| Key change is outside selected keys | Reject publication and record a contract violation. |
| Schema version or kind unsupported | Stop applying live data and load/retain Snapshot as explicitly non-live; never interpret by guesswork. |

The client does not sort arbitrary publications to conceal a gap. Per-channel transport ordering is an interface assumption and is tested by rollout gates.

### 11. Revocation and resource invisibility

IAM owns revocation facts; Telemetry Runtime owns their live-subscription effect.

When authorization is revoked:

- Telemetry Runtime invokes server unsubscribe;
- resubscribe and cursor reuse are denied;
- the adapter removes the affected live state from browser caches and feature stores;
- the UI does not retain unauthorized Last Known telemetry as a fallback;
- subsequent single and batch reads return nondiscoverable `RESOURCE_NOT_FOUND` for that Device/scope.

Missing and unauthorized Devices are indistinguishable at the public seam. Negative tests must assert identical status, code and response shape, without comparing unstable detail text.

### 12. Stable Problem Details

The planned telemetry-specific stable codes are:

| Code | Typical status | Retryable | Meaning |
|---|---:|---:|---|
| `RESOURCE_NOT_FOUND` | 404 | no | Device does not exist or is not visible for the requested action. |
| `TELEMETRY_REQUEST_INVALID` | 400 | no | Body/query shape, duplicate request ID or cross-field constraint is invalid. |
| `TELEMETRY_KEY_INVALID` | 400 | no | Key syntax or configured-key validation failed. |
| `RECOVERY_CURSOR_INVALID` | 400 | no | Cursor is malformed, tampered, expired or scope-mismatched. |
| `TELEMETRY_BATCH_LIMIT_EXCEEDED` | 413 | no | Device/key batch bound exceeded. |
| `SUBSCRIPTION_LIMIT_EXCEEDED` | 429 or 413 | conditional | Per-Principal/Session or request subscription bound exceeded. |
| `TELEMETRY_UNAVAILABLE` | 503 | yes | Gateway/owner/storage cannot perform the request. |
| `TELEMETRY_TIMEOUT` | 504 | yes | Bounded owner request exceeded its deadline. |
| `SUBSCRIPTION_UNAVAILABLE` | 503 | yes | Bootstrap or transport coordination cannot be completed. |
| `TELEMETRY_AUTHORIZATION_UNAVAILABLE` | 503 | yes | Current IAM authorization cannot be safely evaluated. |

`fieldErrors` identifies body/query fields for validly authenticated callers, but must not disclose hidden Organization, Site, Device or mapping facts.

Stale, missing, suspect and upstream observation unavailability are not Problem Details when authoritative Snapshot state can be returned.

### 13. Authentication and authorization propagation

Browser callers use the opaque HttpOnly BFF Session. JavaScript never supplies a bearer token. All POST operations require the Session-bound CSRF token.

Service callers use verified workload identity and an internal Gateway/IAM delegation; the eventual active OpenAPI may express browser and workload security alternatives, but public DTOs remain identical.

The Gateway-to-owner delegation binds at least:

- initiating Principal and actor chain;
- acting Organization derived from Session;
- action;
- exact Device IDs and key selections or a hash thereof;
- IAM policy revision;
- audience and expiration;
- route and request correlation.

Telemetry Runtime independently verifies the signed grant, current Device binding, exact keys and route/action. Client-supplied headers or fields never enlarge the grant.

### 14. Client generation and module seam

The planned OpenAPI is not merged into the active Gateway generator until implementation begins.

The implementation ticket must bump the platform contract generator and produce:

- Go Gateway server/request/response interfaces for the four operations;
- TypeScript HTTP DTOs and functions for HVAC Web;
- shared TypeScript publication types from the JSON Schema;
- one `TelemetryLiveClient` adapter that hides Centrifugo tokens, channels, subscribe proxy events, transport position and cursor checkpointing.

Feature code may use only the generated HTTP functions and the `TelemetryLiveClient` interface. It must not import the Centrifugo SDK directly, parse channels, inspect connection tokens or handwrite duplicate Snapshot/publication DTOs.

The HTTP interface is deep: four actions hide IAM delegation, mapping, Snapshot coherence, batching, transport capabilities and cursor signing. The realtime adapter is also deep: callers observe normalized Snapshot/live/unavailable/revoked states rather than Centrifugo-specific callbacks.

### 15. Versioning and compatibility

HTTP paths remain under `/api/v1`; every Snapshot, batch, bootstrap, cursor response and publication includes `schemaVersion=1` where applicable.

Compatibility rules:

- removing or changing a required field is breaking;
- changing field meaning, Business Revision rules, key ordering, batch ordering, canonical enums or Problem code semantics is breaking;
- changing a numeric/string/boolean/JSON value to another type without a new key policy revision is invalid data, not contract evolution;
- additive optional fields are permitted only after generated clients are proven to ignore unknown fields safely;
- new canonical enum values require a new schema version because exhaustive clients must not guess their meaning;
- breaking HTTP changes use a new path/media/schema version with an explicit coexistence window;
- breaking publication changes use a new `schemaVersion` and publication kind/channel contract.

The contract SHA and generator version are embedded in generated files. Drift checks must fail when checked-in clients do not match the active contract.

### 16. Legacy compatibility and retirement

The following Legacy current-state paths are replaced by S2 and eventually retired:

```text
GET  /api/v1/telemetry/devices/{deviceId}/latest
POST /api/v1/telemetry/latest/batch
Socket.IO /ws/telemetry
```

The following historical routes remain a separately owned compatibility boundary:

```text
GET /api/v1/telemetry/devices/{deviceId}/timeseries
GET /api/v1/devices/{deviceId}/telemetry
```

They do not update S2 current state and are not fallback sources for the new Snapshot routes.

Current-state Legacy retirement requires all of the following:

1. the S2 routes are implemented from the planned contract and registered to `telemetry-runtime-service`;
2. generated Go/TypeScript contract drift checks pass;
3. HVAC Web real mode uses only the generated S2 Snapshot client and `TelemetryLiveClient`;
4. no production browser traffic uses Legacy latest/batch or `/ws/telemetry`;
5. authorization negative tests prove missing/unauthorized nondiscoverability for single, batch, bootstrap and cursor operations;
6. disconnect, duplicate, revision-gap, cursor-expiry, transport-reset and live-revocation tests pass;
7. Issue #52 rollout gates accept Snapshot latency, publication lag, recovery rate, capacity and rollback evidence;
8. route ownership reaches S2 primary with no real-to-Legacy or real-to-Mock fallback;
9. source integrations no longer depend on the unauthenticated Legacy ingest path for S2 current state.

Shadow comparison may record side-effect-free parity evidence. It cannot write, publish, repair or authorize either implementation.

### 17. HVAC Web real-mode requirements

The contract supports HVAC Web without Mock fallback:

- Registry pages obtain visible Device IDs through S1;
- pages batch Snapshot reads for the visible page or selected Device set;
- Presence-only views select no keys;
- metric cards select exact required keys;
- realtime updates use the live adapter and the same key state types;
- Snapshot failure, live transport failure and upstream observation unavailability are rendered as different states;
- stale or Last Known values retain original `sampledAt` and are never stamped with render/request time;
- unauthorized or revoked state is removed rather than replaced with Mock data.

## Consequences

### Positive

- REST and realtime use one coherent business state and one revision rule.
- Presence-only and latest reads do not drift across endpoints.
- Batch partial results are explicit and generated-client friendly.
- Browser DTOs cannot claim identity, Site scope, channels or transport positions.
- Every Device revision remains detectable without leaking unselected keys.
- Transport recovery remains replaceable and subordinate to Snapshot authority.
- Real mode can express missing, stale, unavailable and revoked states without Mock fallback.

### Costs

- The generated realtime adapter must implement buffering, gap detection, Snapshot reload and cursor checkpointing.
- Every active Device subscription receives a small publication for every Device revision, including empty key deltas; Issue #52 must validate capacity.
- A fourth adapter-only HTTP operation is required to mint owner-issued Recovery Cursors safely.
- Bootstrap is all-or-nothing, so callers must correct one invalid target before receiving any live capability.

### Deferred

Issue #52 selects concrete timeouts, token/cursor lifetimes, Redis history retention, publication payload/connection capacity, SLOs, canary gates and rollback thresholds.

Issue #53 splits implementation into ordered tracer-bullet tickets without reopening this wire contract.

## Architecture Decision Trace

| Issue #51 requirement | Decision section | Machine-readable asset |
|---|---|---|
| Presence/latest paths and types | Sections 1–3 | OpenAPI Snapshot operation and schemas |
| Batch boundary and partial results | Section 4 | OpenAPI batch operation, limits and result union |
| Subscription bootstrap | Section 5 | OpenAPI bootstrap operation and descriptors |
| Snapshot/Cursor/Revision/timestamps/quality/freshness | Sections 1–3, 6–7 | OpenAPI schemas and publication JSON Schema |
| Gateway/IAM propagation and forbidden input | Sections 5 and 13 | `x-client-forbidden-fields` and closed request DTOs |
| Empty/key/device/cursor/stale/unavailable/timeout/partial semantics | Sections 2, 4 and 12 | Problem code lock plus Snapshot key-state unions |
| Disconnect/replay/gap client algorithm | Sections 8–10 | Publication invariants and ADR algorithm |
| Generated clients | Section 14 | Planned operation IDs and event schema |
| Compatibility/Legacy retirement | Sections 15–16 | `x-legacy-retirement` and ADR gates |

Changing the operation set, coherent Snapshot boundary, batch ordering, all-or-nothing bootstrap, Recovery Cursor checkpoint model, publication revision sequence, stable Problem codes, client forbidden fields or Legacy retirement boundary requires a new ADR and compatible contract revision.
