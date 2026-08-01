# Real Assets Contract Readiness

Status: accepted research result

Date: 2026-07-30

Historical status note (2026-08-01): This document is the accepted Issue #129 research deliverable. The three additive prerequisites identified below were subsequently delivered through the Real Assets implementation and certification track, culminating in PR #182. Use `docs/operations/real-product-roadmap.md` for current delivery status; retain this file as the contract-boundary decision record.

Issue: #129 — Real Assets 01: 核对公共契约与关键点位配置边界

Parent: #128 — Wayfinder Map: Real Assets 资产运行工作台交付

## Decision summary

The existing platform is **partially ready** for Real Assets.

The following public boundaries are ready to reuse:

- authenticated Principal, Acting Organization and server-authored Registry capabilities;
- authorized Site, Equipment and Device Registry reads;
- coherent Device Observation Snapshot reads;
- bounded ordered Snapshot batch reads;
- exact Device/key realtime bootstrap, contiguous delta application, bounded recovery and revocation handling;
- Real Shell protected Site-scope cancellation and purge coordination;
- S2 telemetry history ingestion and ClickHouse projection.

Three additive prerequisites are required before the complete frozen Real Assets scope can ship:

1. expose a Site-scoped public DeviceBinding collection so the browser can construct Equipment → Device relationships without guessing;
2. expose browser-visible, server-authored Telemetry capabilities through CurrentPrincipal;
3. expose a bounded public Device short-history query backed by the S2 telemetry history owner.

Building and Area are not current Registry resources. The first release therefore collapses those optional levels instead of inventing them. Introducing Building or Area identity is not a Real Assets prerequisite.

## Authority boundary

| Fact | Authoritative owner | Browser treatment |
|---|---|---|
| Principal, Acting Organization, effective capabilities and Session | IAM through Platform Gateway | Read only from CurrentPrincipal. Never derive permissions from roles or browser state. |
| Site, Equipment, Device and DeviceBinding identity | Platform Core Registry | Read only through Platform Gateway Registry routes. |
| Presence, Freshness, Quality, Availability, current values and Business Revision | telemetry-runtime-service | Read as one coherent Device Observation Snapshot. |
| Realtime transport position | Centrifugo adapter | Transport continuity only; never treated as business authority. |
| Realtime Business Revision and Recovery Cursor | telemetry-runtime-service | Apply only contiguous revisions; fallback to a fresh Snapshot on gaps or recovery failure. |
| Historical Device observations | S2 telemetry history projection | Query through a new bounded public Gateway contract; never use a historical last point as current state. |
| Critical-point display and completeness policy | versioned repository catalog, temporarily | Presentation policy only. It does not own observed values, thresholds, Alarm rules, FDD conclusions or control semantics. |
| Real Assets operating projection | HVAC Web | Deterministic UI projection from authorized Registry identity, Snapshot facts and catalog completeness policy. |

## Public operation matrix

| User behavior | Public owner and operation | Browser capability | Request scope and limits | Authoritative response facts | Errors and cache/revision behavior | Readiness |
|---|---|---|---|---|---|---|
| Enter an authorized Site | Platform Gateway Registry: `GET /api/v1/sites/{siteId}` | `site.read` | UUIDv7 Site path. Site is already validated by Real Shell against the authorized Site collection. | Site identity, owning Organization, timezone, lifecycle and Registry revision. | Unauthorized and absent Sites normalize to `RESOURCE_NOT_FOUND`; Registry responses expose route-policy revision. | Ready. |
| List Equipment | Platform Gateway Registry: `GET /api/v1/sites/{siteId}/equipment` | `equipment.list` | Cursor pagination; `limit` 1–200, default 50. Authorization is re-evaluated for each page. | Equipment identity, type, lifecycle, Site and per-resource Registry revision. | `CURSOR_INVALID`, `REGISTRY_UNAVAILABLE`, `REGISTRY_TIMEOUT`, mapping problems and non-enumerating `RESOURCE_NOT_FOUND`. | Ready. |
| List Devices | Platform Gateway Registry: `GET /api/v1/sites/{siteId}/devices` | `device.list` | Cursor pagination; `limit` 1–200. | Device identity, type, lifecycle, Site and per-resource Registry revision. | Same Registry failure model as Equipment. Server ordering is not a product ordering contract; the UI must apply deterministic ordering after collecting the bounded authorized set. | Ready. |
| Relate Devices to Equipment | Proposed Platform Gateway Registry operation: `GET /api/v1/sites/{siteId}/device-bindings` | Existing `equipment.list` and `device.list`, subject to final IAM decision | Cursor pagination; only bindings whose Site, Equipment and Device are visible under the current scope. | Versioned DeviceBinding identity, Device ID, Equipment ID, role, lifecycle, validity interval and revision. | Must use the Registry public problem model and non-enumerating filtering. | **Missing prerequisite.** The schema exists but no public or internal read route is exposed. |
| Initialize the operating list | Platform Gateway S2: `POST /api/v1/telemetry/observation-snapshots:batchGet` | Proposed browser capability `telemetry.batch.read` | 1–100 Devices per request, 0–64 exact keys per Device and at most 2048 key selections. BFF POST requires CSRF. A 200-Device fixture therefore needs at least two bounded batch requests. | One ordered item for every request. Success contains a coherent per-Device Snapshot and Business Revision; failures are per-item Problem Details. | Whole-request failures for invalid envelope, CSRF, global authorization failure, owner outage or timeout. Successful responses are `private, no-store`; React Query cache is protected application state, not HTTP cache. | Endpoint ready; browser capability projection missing. |
| Open current Device details | Platform Gateway S2: `GET /api/v1/devices/{deviceId}/observation-snapshot?keys=...` | Proposed browser capability `telemetry.snapshot.read` | Exact Device and up to 64 unique configured keys. Organization and Site are not browser-supplied trusted fields. | Presence and exact selected key states at one Business Revision, including Availability, Freshness, Quality, timestamps, policy revisions and derived S2 Display State. | Invalid or unauthorized Device uses `RESOURCE_NOT_FOUND`; invalid key uses `TELEMETRY_KEY_INVALID`; upstream non-ideal data can still be a successful Snapshot. | Endpoint ready; browser capability projection missing. |
| Open live Device details | Platform Gateway S2: `POST /api/v1/telemetry/subscriptions:bootstrap` | Proposed browser capability `telemetry.subscribe` | Exact caller-local subscription ID, Device ID, keys and optional owner-issued cursor. 1–100 subscriptions, 64 keys each and 2048 total selections. BFF POST requires CSRF. | Short-lived WSS endpoint/token, opaque subscription/channel, exact scope and recovery mode. These are transport capabilities, not authorization facts. | Bootstrap is all-or-nothing. Invalid, invisible or revoked target produces no partial capability set. Responses are `private, no-store`. | Ready after capability projection. |
| Persist bounded recovery progress | Platform Gateway S2: `POST /api/v1/telemetry/recovery-cursors:checkpoint` | Adapter-internal authorization actions; not a separate navigation capability | Opaque subscription ID, applied Business Revision and transport position. The feature UI does not construct or interpret cursors. | Owner-signed scope-bound cursor and expiry. | Invalid, expired or mismatched cursor never expands scope and falls back to a fresh Snapshot. | Ready through the existing TelemetryLiveClient. |
| View 1h, 6h or 24h trends | Proposed Platform Gateway bounded Device history query backed by telemetry history | Proposed browser capability `telemetry.history.read` | One visible Device, exact trend-eligible numeric keys, maximum 24-hour range and bounded points per key. | Timestamped accepted values, units and quality, plus data watermark, partial status and a version/revision suitable for cache isolation. | History empty, partial and unavailable remain separate from current-state failure. No direct ClickHouse, Legacy or ThingsBoard browser access. | **Missing prerequisite.** History projection exists, but no browser-facing contract, Gateway route or generated client exists. |

## Registry findings

### Ready facts

The Registry public contract provides:

- visible Organization and Site collections;
- visible Site-level Equipment and Device collections;
- singular Site, Equipment and Device reads;
- UUIDv7 identities;
- per-resource lifecycle and monotonic Registry revision;
- opaque integrity-protected cursor pagination;
- authorization re-evaluation for every page;
- stable public Problem Details and non-enumerating singular reads.

The maximum page size is 200. The first Real Assets certification fixture can therefore request one page for a 200-Device Site, while implementation must still support bounded cursor continuation rather than assuming a single page forever.

### Missing relationship route

`DeviceBinding` is part of the Registry schema and domain model, but it has no public Platform Gateway operation and no Platform Core read endpoint. `Device` itself does not contain `equipmentId`.

Consequences:

- the browser cannot truthfully nest Devices under Equipment;
- matching by code, name, type or array position would invent identity;
- rendering separate Equipment and Device groups, as the older page does, is not the frozen Equipment → Device hierarchy.

The minimal prerequisite is a Site-scoped authorized DeviceBinding collection. A new Building or Area resource is not required for the first release; those optional hierarchy levels remain collapsed.

### Ordering

The contract does not declare business ordering. Real Assets must apply a deterministic presentation order after collecting the authorized set, for example:

1. configured hierarchy order if a later Registry contract supplies one;
2. otherwise normalized display name;
3. code;
4. stable UUID as final tie-breaker.

Cursor order must not be presented as a meaningful operations order.

## Current Snapshot and batch findings

### Coherent state

Presence and latest telemetry must not be read as separate resources. An exact-key `DeviceObservationSnapshot` contains, at one Device Business Revision:

- owning Organization and Site IDs;
- evaluation time and Availability;
- Presence applicability, current state and last-known context;
- aggregate Telemetry Readiness and S2 Display State;
- exact selected key states in request order;
- sampled and received timestamps;
- Freshness, Quality, reason codes and policy revision.

The Real Assets operating projection may use these facts, but it must not replace or reinterpret their ownership.

### Status mapping

The frozen Real Assets projection maps authoritative states as follows:

| Real Assets projection | Required authoritative condition |
|---|---|
| `UNKNOWN` | Snapshot scope/shape cannot be established for an otherwise visible Device, Presence is applicable but unknown, or applicable current-state facts cannot be concluded. A whole service outage remains a page/data-source failure instead of 200 fake UNKNOWN rows. |
| `OFFLINE` | `evaluationAvailability=AVAILABLE`, Presence applicable and current Presence is `OFFLINE`. |
| `ATTENTION` | Presence is online, but any selected critical key is stale, suspect, missing, or unusable; or aggregate readiness is degraded/incomplete. |
| `NORMAL` | Presence online, evaluation available, all applicable critical keys present, fresh and good. |

S2 `displayState` remains visible evidence and a consistency check, but Real Assets uses its own narrower operations vocabulary because it additionally incorporates repository critical-point completeness.

### Valid zero and invalid data

The current public contract has `TelemetryQuality=GOOD|SUSPECT`; it does not have a present-value `INVALID` enum.

The product term “invalid” must therefore map to the owner’s explicit unusable state:

- `state=MISSING` and `missingReason=ONLY_REJECTED_CANDIDATES` means candidates existed but none were accepted;
- this is distinct from `NEVER_OBSERVED` and `POLICY_NOT_CONFIGURED`;
- a present numeric value of `0` remains a valid measured zero and must never be treated as missing;
- `quality=SUSPECT` remains a present value with warning evidence.

A repository profile that does not exist for a Device type is also distinct from telemetry owner `POLICY_NOT_CONFIGURED`.

### Batch planning for 200 Devices

A batch accepts at most 100 Devices. The implementation must:

- split a 200-Device authorized collection into deterministic batches of at most 100;
- preserve one request ID per Device and validate ordered response correspondence;
- keep total selected keys under 2048 for each request;
- represent per-item errors without discarding successful Devices;
- treat global authorization/service failure as a list-level dependency failure;
- cancel all chunks on protected-scope transition and suppress late results.

## Browser capability findings

CurrentPrincipal currently exposes only eight Registry capabilities:

- `organization.list`;
- `organization.read`;
- `site.list`;
- `site.read`;
- `equipment.list`;
- `equipment.read`;
- `device.list`;
- `device.read`.

IAM and Gateway already authorize internal S2 actions:

- `telemetry.snapshot.read`;
- `telemetry.batch.read`;
- `telemetry.subscribe`;
- `telemetry.resubscribe`;
- `telemetry.recovery.use`;
- `telemetry.recovery.checkpoint`.

These actions are not projected into the browser’s EffectiveAuthorization contract. Therefore the browser cannot safely decide whether Assets current-state, live or history controls should be visible or disabled before attempting the operation.

The minimal public projection should add high-level user-facing capabilities:

- `telemetry.snapshot.read`;
- `telemetry.batch.read`;
- `telemetry.subscribe`;
- `telemetry.history.read`.

`telemetry.resubscribe`, `telemetry.recovery.use` and `telemetry.recovery.checkpoint` remain adapter operations implied by an authorized live session; they need not become separate navigation decisions.

The capability contract must be versioned or expanded with a coordinated compatibility plan because the generated client uses a strict enum and currently caps the list at eight entries.

Recommended Real Assets checks:

| Surface | Required browser capabilities |
|---|---|
| Site route and hierarchy | `site.read`, `equipment.list`, `device.list` |
| Singular Device identity | `device.read` |
| Operating list current facts | `telemetry.batch.read` |
| Current detail refresh | `telemetry.snapshot.read` |
| Live detail | `telemetry.subscribe` |
| Short trends | `telemetry.history.read` |

These checks improve navigation and honest UI states. Gateway, IAM and owning services still perform exact Organization, Site, Device and key authorization for every call.

## Realtime findings

The existing TelemetryLiveClient is suitable for Real Assets detail reuse. It already provides:

- exact normalized Device/key targets;
- all-or-nothing bootstrap validation;
- CSRF acquisition through CurrentPrincipal;
- secure WSS/loopback endpoint validation;
- Snapshot-before-live behavior;
- strict scope validation for Snapshot and publications;
- contiguous Business Revision application;
- duplicate suppression;
- revision-gap, protocol and recovery fallback to an authoritative Snapshot;
- bounded publication buffering;
- periodic owner-issued Recovery Cursor checkpointing;
- token renewal without scope expansion;
- revocation purge;
- exact session close and unsubscribe.

Real Assets must register the live session as a protected `realtime` resource and close it before query-cache, selection and temporary-state purge.

The list must not open one subscription per Device or key. At most, a later explicitly budgeted low-cardinality Presence subscription may be introduced; it is not required for the first list slice. The current accepted strategy is Snapshot/Batch for the list and exact realtime only for the selected Device.

## Short-history findings

The repository now has an S2 telemetry history pipeline:

- accepted and rejected source observations are projected through a PostgreSQL outbox;
- ClickHouse stores raw observations with Organization, Site, Device, key, unit, sample time, receive time, acceptance status, Quality and reasons;
- accepted numeric observations also feed an hourly aggregate projection;
- history architecture checks and integration evidence exist.

This is sufficient storage groundwork, but it is not yet a product query boundary. There is no:

- public short-history OpenAPI operation;
- Platform Gateway route owner;
- IAM `telemetry.history.read` browser capability;
- typed internal query operation for Device key series;
- generated HVAC Web history client;
- public limits, partial-data, watermark or error semantics.

The old Legacy timeseries controller is not an acceptable Real Assets browser contract because it:

- is not registered as a Platform Gateway public route;
- uses a Legacy envelope and millisecond parameters;
- does not expose the frozen S2 scope, quality, revision and partial-data semantics;
- must not become a direct browser or ThingsBoard read-through path.

The additive history prerequisite should reuse the S2 ClickHouse history projection and expose a bounded product query. Minimum contract properties:

- one authorized Device per query;
- exact numeric trend-eligible keys only;
- inclusive start and exclusive end with a maximum 24-hour range;
- bounded maximum points per key and explicit actual aggregation;
- Site and Organization derived and verified by the server;
- per-point timestamp, numeric value, unit and Quality;
- no synthetic missing buckets or zero filling;
- metadata containing data watermark, partial status and a stable query/dataset revision suitable for cache isolation;
- separate empty, partial, unavailable, timeout, authorization-unavailable and not-visible outcomes;
- historical values never satisfy a current-state precondition.

## Versioned critical-point catalog

The existing `centralPlantTelemetry` module is useful prior art but is not sufficient as the frozen catalog because:

- it has no schema version or catalog revision;
- it has no `critical` or `trendEligible` declaration;
- unknown Device types silently fall back to a GENERIC profile;
- it mixes full detail keys and highlight keys without an explicit completeness contract.

The first Real Assets implementation should introduce a repository-owned catalog with the following logical schema:

```text
Catalog
  schemaVersion: 1
  catalogRevision: stable string
  profiles:
    canonical Device type
      title
      aliases[]
      points[]
        key
        label
        order
        critical
        showInList
        showInDetail
        trendEligible
        defaultUnit?
        precision?
```

Rules:

1. Device-type normalization and aliases are deterministic and versioned.
2. Unknown Device types resolve to `unconfigured`, not GENERIC.
3. Unknown types remain visible with Registry identity and Presence-only Snapshot evidence.
4. Observed unit wins. `defaultUnit` is only a display fallback when the owner returns no unit.
5. A critical point affects completeness only for a configured profile.
6. `trendEligible` is limited to numeric points appropriate for short history.
7. Invalid or stale catalog keys must fail visibly as profile/contract drift; they must not be silently deleted or replaced.
8. The catalog owns no threshold, alarm severity, FDD rule, optimization rule, safety class or command mapping.
9. A future platform metadata migration must preserve catalog revision and behavior tests before removing the repository catalog.

The catalog revision belongs in current and history query keys.

## Cache, revision and lifecycle rules

### Registry

Registry query keys include:

- Session/protected-scope generation;
- Acting Organization;
- Site;
- resource collection or singular resource;
- cursor/page parameters.

Resource revisions and `X-Route-Policy-Revision` are retained as evidence. A route-policy change triggers protected cache purge.

### Current Snapshot and batch

Query keys include:

- Session/protected-scope generation;
- Acting Organization;
- Site;
- operation (`batch` or `single`);
- ordered Device IDs;
- exact ordered keys per Device;
- critical-point catalog revision.

Each Snapshot carries its own Device Business Revision. A batch has no global Business Revision. The client must validate response order and scope before committing data.

HTTP responses are `private, no-store`. Any React Query retention is an in-memory protected resource and must be purged on Site, Session, Principal or policy transition.

### Short history

The new query key must include:

- Session/protected-scope generation;
- Acting Organization;
- Site;
- Device;
- exact keys;
- from/to range or preset;
- requested aggregation and maximum points;
- Site timezone used for display;
- critical-point catalog revision;
- returned dataset/query revision where the client stores revision-specific entries.

### Cancellation and late results

The generated Registry and S2 clients accept `AbortSignal`. Real Assets must combine that capability with the Real Shell protected-scope generation:

1. obtain a protected request token;
2. pass its signal to every request in the operation;
3. validate response scope and contract shape;
4. commit through the token so a late response cannot write after a generation change;
5. register query-cache, selection and realtime resources for ordered purge.

## Reuse and non-reuse decisions

### Reuse

- generated Platform Gateway Registry client;
- generated S2 telemetry client, wrapped with runtime contract validation;
- Registry pagination and public error presentation patterns;
- `parseSnapshot`, realtime publication validation and TelemetryLiveClient;
- Real Shell `ProtectedScopeCoordinator`;
- Real route SiteContext and authenticated Principal;
- existing Registry, S2 Snapshot, live recovery and browser audit fixtures as test prior art.

### Do not reuse as authority

The older `pages/Assets/RealAssets.tsx` cannot be mounted as the new Site-scoped Real Assets implementation because it:

- discovers and selects Organization and Site in browser state;
- uses the legacy application shell and query-parameter detail model;
- renders Equipment and Devices as unrelated sibling groups;
- depends on a presence helper capped at one 100-Device batch;
- opens live telemetry through the older page lifecycle rather than the Real Shell protected scope;
- contains product behavior that predates the frozen attention-first and URL route decisions.

Individual presentational and test patterns may be extracted only after their authority assumptions are removed.

The current `telemetry-current.ts` is also not the complete list data module. It is useful prior art for CSRF, exact scope checks, item-order validation and live opening, but its presence helper:

- accepts at most 100 Devices;
- requests no keys;
- polls every 30 seconds;
- does not implement the frozen critical-point completeness projection.

## Public failure mapping

| Condition | Contract state | Real Assets presentation |
|---|---|---|
| Site has no Registry Devices | Successful empty Device collection | “当前 Site 尚未登记设备.” |
| Device exists, key never observed | Snapshot `MISSING/NEVER_OBSERVED` | Key has never been observed; Device may require attention if the point is critical. |
| Candidates existed but none accepted | Snapshot `MISSING/ONLY_REJECTED_CANDIDATES` | Invalid/unusable current observation, distinct from never observed. |
| Telemetry policy absent | Snapshot `MISSING/POLICY_NOT_CONFIGURED` | Owner telemetry policy is not configured; distinct from missing repository profile. |
| Value is old | Present state with `freshness=STALE` | Show the retained value, sampled time and stale status; do not erase or replace with zero. |
| Value is usable with warning | Present state with `quality=SUSPECT` | Show value and warning reasons. |
| Current owner coverage unavailable | Successful Snapshot with `evaluationAvailability=UNAVAILABLE` | Preserve last-known context with explicit unavailable evidence; do not assert current Presence. |
| One batch Device is invisible | Per-item `RESOURCE_NOT_FOUND` | Non-enumerating row-level not-visible/error handling for the requested authorized list inconsistency. |
| Registry dependency fails | Registry Problem Details | Keep separate from telemetry failure; no Demo fallback. |
| Current telemetry dependency fails globally | S2 Problem Details | Keep Registry identities visible where already established, with current-state dependency unavailable. |
| Realtime transport fails | TelemetryLive state unavailable with last authoritative Snapshot if present | Mark transport degraded; do not label retained Snapshot as live. |
| History query fails | Future history Problem Details | Keep current identity and Snapshot available; retry history independently. |

## Existing verification evidence

The following checks passed during this readiness review:

```text
npm run contracts:check
npm run s1:registry:check
npm run s2:contracts:check
npm run s2:public-contract:check
npm run s2:live-client:check
npm run s2:history:check
```

Observed evidence:

- generated Platform Gateway contract has no drift;
- S1 Registry baseline verifies eight routes, six resources, cursor integrity and ownership/RLS assets;
- generated S2 public contract has no drift;
- coherent Snapshot, bounded batch, exact subscription scope and Snapshot-authoritative recovery checks pass;
- TelemetryLiveClient architecture check passes;
- S2 ClickHouse telemetry history architecture check passes.

These checks prove the stated foundations. They do not prove the three missing public prerequisites.

## Additive prerequisites

### P1 — Public Site DeviceBinding collection

Tracked by [Real Assets P1: 公开 Site DeviceBinding 集合](https://github.com/SwayingWindmill/HVAC_web/issues/135).

Deliver an authorized, cursor-paginated Site-scoped DeviceBinding read through Platform Gateway and Platform Core, including generated clients, ownership registration, non-enumeration tests and browser scope evidence.

This prerequisite blocks the hierarchical list slice.

### P2 — Browser-visible Telemetry capabilities

Tracked by [Real Assets P2: 投影浏览器 Telemetry Capability](https://github.com/SwayingWindmill/HVAC_web/issues/136).

Expand/version CurrentPrincipal EffectiveAuthorization to expose `telemetry.snapshot.read`, `telemetry.batch.read`, `telemetry.subscribe` and `telemetry.history.read`, while retaining server-side exact authorization for every request and adapter operation.

This prerequisite blocks the operating list, current detail, live detail and history controls.

### P3 — Bounded public Device short-history query

Tracked by [Real Assets P3: 公开有界设备短历史查询](https://github.com/SwayingWindmill/HVAC_web/issues/137).

Expose a Platform Gateway product query backed by the S2 ClickHouse history projection, with exact Device/key authorization, maximum 24-hour range, bounded point count, Quality, watermark, partial state, revision and generated HVAC Web client.

This prerequisite blocks the short-trend slice.

## Final readiness statement

After P1 and P2 are complete, `Real Assets 02: 交付 Site 设备运行列表` can implement the frozen hierarchy and current operating list without reopening product decisions.

After P3 is complete, `Real Assets 04: 增加设备关键点位短趋势` can implement 1h, 6h and 24h trends without Legacy or direct ClickHouse access.

The existing S2 Snapshot and TelemetryLiveClient boundaries are sufficient for current Device detail and exact realtime recovery. No additional realtime protocol decision is required.
