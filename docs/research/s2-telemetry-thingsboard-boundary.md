# S2 Telemetry / ThingsBoard current-boundary research

Research ticket: [S2 现有 Telemetry 与 ThingsBoard 边界盘点](https://github.com/SwayingWindmill/HVAC_web/issues/47)

Research date: 2026-07-23

## Executive conclusion

The current telemetry path is a Legacy facade around ThingsBoard, not a platform-owned S2 boundary.

HTTP reads have one strong reusable property: the requested Device is resolved inside the authenticated Site before ThingsBoard is called, so a missing Device and an out-of-Site Device are both nondiscoverable. The live path does not preserve that model. A connected Socket.IO client can submit a Device ID and key list without Site, scope, or Device authorization, and the server source explicitly leaves the authorization check as a TODO.

Latest telemetry also has no single writer. The same Redis key can be written by the public ThingsBoard webhook path, the demand-driven ThingsBoard WebSocket path, and REST read-through backfill. These paths have no shared ingest revision, deduplication key, monotonic write rule, received timestamp, quality status, or source priority.

HVAC Web real mode is not contract-compatible with the Legacy backend today. Batch latest expects an array while the backend returns an object keyed by request ID; timeseries sends `range` to an endpoint that expects explicit boundaries; and frontend values are numeric although ThingsBoard values may also be strings, booleans, or objects. S2 therefore cannot be a routing-only cutover.

## Current topology

```text
HVAC Web
├── REST /api/v1/telemetry/*
│   └── Legacy SiteContext + telemetry.read scope
│       └── requested Device ID + current Site -> local Device ID + TB Device ID
│           ├── Redis latest:{siteId}:{localDeviceId}:{key}
│           └── ThingsBoard REST through the Legacy integration credential
│
└── Socket.IO /ws/telemetry
    └── Legacy session verification only
        └── process-local SubscriptionManager
            └── local Device ID -> TB Device ID, without Site
                └── ThingsBoard WebSocket
                    └── cache write + browser publication

ThingsBoard Rule Chain / Webhook
└── public POST /api/v1/ingest/tb/telemetry
    └── global lookup by TB Device ID
        └── same cache write + browser publication
```

Primary sources:

- API prefix: `hvac-backend/src/main.ts:53`.
- Module wiring: `hvac-backend/src/telemetry/telemetry.module.ts:18-37`.
- REST latest path: `hvac-backend/src/telemetry/telemetry.service.ts:70-171`.
- Demand-driven live path: `hvac-backend/src/telemetry/telemetry-ws.service.ts:22-85`.
- Webhook path: `hvac-backend/src/telemetry/tb-ingest.controller.ts:7-35`.
- Common cache-and-push path: `hvac-backend/src/telemetry/telemetry-ingest.service.ts:18-40`.

## Existing interfaces

### HTTP reads

| Interface | Current behavior | Data shape | Source |
|---|---|---|---|
| `GET /api/v1/telemetry/devices/:id/latest` | Requires keys, current Site, and `telemetry.read`. | `{ [key]: { ts, value } }` in the Legacy envelope. | `hvac-backend/src/telemetry/telemetry.controller.ts:29-53` |
| `POST /api/v1/telemetry/latest/batch` | Resolves every requested Device inside the current Site, then reads cache and TB. | `{ [requestedId]: { [key]: { ts, value } } }`. | `hvac-backend/src/telemetry/telemetry.controller.ts:118-137`; `hvac-backend/src/telemetry/telemetry.service.ts:122-171` |
| `GET /api/v1/telemetry/devices/:id/timeseries` | Accepts keys, start/end, limit and aggregation; defaults to one hour, 100 points, no aggregation, ascending. | `{ [key]: Array<{ ts, value }> }`. | `hvac-backend/src/telemetry/telemetry.controller.ts:56-115` |
| `GET /api/v1/devices/:id/telemetry` | Compatibility route accepting `1h`, `6h`, or `24h`. | Nested range metadata and `telemetry`. | `hvac-backend/src/telemetry/device-telemetry.controller.ts:8-53` |

Controller tests preserve resolved Site context rather than the legacy customer field, the object-keyed batch response, and the compatibility route. Source: `hvac-backend/src/telemetry/telemetry.controller.spec.ts:47-166`.

### Ingest

`POST /api/v1/ingest/tb/telemetry` accepts a TB Device ID, one timestamp, and a value map. It is marked public and has no application-level source identity, signature, replay protection, or allowlist. The source comment says external isolation or request authentication is required, but that protection is not implemented in this controller. Source: `hvac-backend/src/telemetry/tb-ingest.controller.ts:7-35`.

The public attributes endpoint is a placeholder that returns success with “Not implemented yet.” Source: `hvac-backend/src/telemetry/tb-ingest.controller.ts:38-52`.

### Live subscription

Socket.IO uses `/ws/telemetry`. Connection and message guards verify the Legacy session but do not resolve Site context, check `telemetry.read`, or authorize the requested Device. Sources:

- `hvac-backend/src/telemetry/telemetry.gateway.ts:18-70`.
- `hvac-backend/src/telemetry/guards/ws-jwt.guard.ts:9-41`.
- The authorization TODO is at `hvac-backend/src/telemetry/telemetry.gateway.ts:60-61`.

Client messages contain only an optional command ID, Device ID, and key list. Publications contain only Device ID, key, value, and sample timestamp. Sources:

- `hvac-backend/src/telemetry/dto/ws-subscribe.dto.ts:3-25`.
- `hvac-backend/src/telemetry/telemetry.gateway.ts:88-104`.

There is no Snapshot, Cursor, Revision, received timestamp, quality, source, retained window, gap indication, or replay contract.

## Identity and authorization boundaries

### HTTP Site context

`SiteContextInterceptor` resolves Site from the authenticated context, explicit selection, active Site, IAM bindings, and legacy fallback, and records success or denial through the audit logger. Source: `hvac-backend/src/site/interceptors/site-context.interceptor.ts:24-100`.

The policy prevents an authenticated Site from being overridden, rejects an explicit unbound Site, selects an active or sole Site when valid, and requires explicit selection when multiple Sites remain. Source: `hvac-backend/src/site/site-context/site-context.policy.ts:9-76`.

`@CurrentSite()` reads the resolved context instead of trusting the raw legacy customer field. Source: `hvac-backend/src/site/decorators/current-site.decorator.ts:7-11`.

### Device mapping and REST nondiscoverability

`DeviceService.requireInSite` accepts either local Device ID or TB Device ID while including Site in the database predicate. Missing and out-of-Site Devices both become not found. Source: `hvac-backend/src/registry/services/device.service.ts:57-84`.

The test asserts that Site is included for both accepted identifier forms. Source: `hvac-backend/src/registry/services/device.service.spec.ts:168-189`.

This behavior should be preserved by S2, although the implementation must consume the platform Registry owner rather than keep Legacy tables authoritative.

### Live authorization gap

The live path stores a subscription after session verification and then resolves the TB ID using a local Device lookup with no Site argument. Sources:

- `hvac-backend/src/telemetry/telemetry.gateway.ts:56-70`.
- `hvac-backend/src/telemetry/telemetry-ws.service.ts:80-85`.
- `hvac-backend/src/registry/services/device.service.ts:168-174`.

An authenticated user who obtains another Site’s local Device ID can therefore request that subscription. The live path does not reuse HTTP nondiscoverability.

### ThingsBoard credential boundary

The integration service documents a per-Customer strategy, but the telemetry facade methods used by current latest, timeseries, activity, and TB WebSocket paths use the shared default integration context. Sources:

- Strategy description: `hvac-backend/src/integration-thingsboard/thingsboard.service.ts:19-68`.
- Latest and timeseries: `hvac-backend/src/integration-thingsboard/thingsboard.service.ts:164-208`.
- Activity read: `hvac-backend/src/integration-thingsboard/adapters/thingsboard-device-activity-reader.service.ts:11-32`.
- TB WebSocket: `hvac-backend/src/integration-thingsboard/clients/tb-ws.client.ts:53-76`.

The local Registry Site check is the effective tenant boundary for REST telemetry; the ThingsBoard integration credential does not independently constrain each Site in these paths.

## Data and identifier semantics

Legacy Device context contains Site ID, local Device ID, and TB Device ID. Source: `hvac-backend/src/registry/services/device.service.ts:14-41`.

REST latest, batch, and timeseries accept local or TB IDs. Cache keys use the resolved local ID; ThingsBoard calls use the TB ID; batch responses preserve the caller’s requested IDs. Sources:

- `hvac-backend/src/registry/services/device.service.ts:61-84`.
- `hvac-backend/src/telemetry/telemetry.service.ts:131-171`.
- `hvac-backend/src/telemetry/telemetry.service.spec.ts:206-231`.

Live subscription resolution is narrower and expects the local Device ID. Source: `hvac-backend/src/registry/services/device.service.ts:168-174`.

ThingsBoard values support `string | number | boolean | object`. Source: `hvac-backend/src/integration-thingsboard/dto/tb-telemetry.dto.ts:7-42`.

HVAC Web types every point and push value as numeric. Source: `apps/hvac-web/src/api/types.ts:1-22`.

S2 must define a scalar union with key metadata or deliberately limit an interface to numeric keys.

## Latest read and cache behavior

### Single latest

The sequence is:

1. Resolve Device inside Site.
2. Read requested keys from Redis.
3. Copy cache hits into the result.
4. Read all requested keys from ThingsBoard.
5. Accept a TB value only when its sample timestamp is strictly newer.
6. Backfill changed values to Redis.
7. If TB fails, return cache-only or empty data with HTTP success.

Source: `hvac-backend/src/telemetry/telemetry.service.ts:70-117`.

Tests preserve newer-value merge, partial cache fallback, and silent TB failure. Source: `hvac-backend/src/telemetry/telemetry.service.spec.ts:53-141`.

### Batch latest

Every requested Device is resolved before cache reads. One missing or out-of-Site Device rejects the whole batch. TB failures are caught per resolved Device, leaving cache-only or empty data without per-item error metadata. Source: `hvac-backend/src/telemetry/telemetry.service.ts:122-171`.

### Redis representation

Keys are `latest:{siteId}:{localDeviceId}:{key}`. Source: `hvac-backend/src/common/constants/redis-key.constants.ts:1-11`.

Values are JSON `{ ts, value }`. No expiry is configured. Source: `hvac-backend/src/telemetry/telemetry-cache.service.ts:96-116`.

If a single cached value is not valid JSON, the single-Device path assigns the current time to the raw value. The batch path drops parse failures instead. Source: `hvac-backend/src/telemetry/telemetry-cache.service.ts:24-44,76-93`.

S2 must not invent freshness timestamps for malformed data.

## Timeseries boundary

Timeseries is not stored in the Legacy platform cache. It is read directly from ThingsBoard after Site-scoped Device resolution. The facade defaults to ascending order and applies a limit. Source: `hvac-backend/src/telemetry/telemetry.service.ts:174-198`.

There is no cache fallback or typed degraded response. Historical timeseries can remain a separate compatibility boundary if S2 explicitly limits its ownership to Presence, latest, live publication, and short-window recovery.

## Writers and ordering

### Writer A: public webhook

Webhook data is mapped from TB Device ID to local Device and Site, then written to Redis and published. Sources:

- `hvac-backend/src/telemetry/tb-ingest.controller.ts:24-35`.
- `hvac-backend/src/telemetry/telemetry.service.ts:200-218`.
- `hvac-backend/src/telemetry/telemetry-ingest.service.ts:24-40`.

### Writer B: demand-driven TB WebSocket

The first browser subscriber causes the process to subscribe the matching TB Device. Incoming latest tuples are written to Redis and republished. The last browser unsubscribe removes the TB subscription. Source: `hvac-backend/src/telemetry/telemetry-ws.service.ts:22-85`.

The TB client reconnects and resubscribes, but requests latest telemetry only and has no cursor, replay, or gap reconciliation. Source: `hvac-backend/src/integration-thingsboard/clients/tb-ws.client.ts:53-131,158-230`.

### Writer C: REST read-through

Latest and batch reads may write newer TB values directly into the same Redis keys. This path bypasses the common ingest service, so connected browsers are not notified of a correction. Source: `hvac-backend/src/telemetry/telemetry.service.ts:98-107,158-164`.

### Consequences

The common ingest method unconditionally writes and publishes every received point. Source: `hvac-backend/src/telemetry/telemetry-ingest.service.ts:24-40`.

Therefore duplicate webhook and TB WebSocket delivery can write and publish twice, delayed older events can overwrite newer cache data, and REST can later correct Redis without correcting connected clients. Existing tests verify routing, not deduplication, monotonicity, or source competition. Sources:

- `hvac-backend/src/telemetry/telemetry-ws.service.spec.ts:59-88`.
- `hvac-backend/src/telemetry/telemetry.service.spec.ts:280-320`.

## Subscription-state behavior

`SubscriptionManager` is process-local memory with Socket-to-Device/key and Device-to-Socket indexes. Source: `hvac-backend/src/telemetry/subscription.manager.ts:8-21`.

It does not provide a multi-replica routing or authority model.

There is also an unsubscribe mismatch:

- HVAC Web emits one subscribe and unsubscribe message per key;
- the server merges keys on subscribe;
- one server unsubscribe removes the entire Device subscription.

Sources:

- `apps/hvac-web/src/api/telemetry.ts:44-50`.
- `hvac-backend/src/telemetry/subscription.manager.ts:65-85`.

When one key’s reference count reaches zero while another key remains active, the browser can remove the whole server subscription.

## Existing Presence behavior

Legacy Device list/detail exposes a boolean `active` through `DeviceActivityReader`. Source: `hvac-backend/src/registry/services/device.service.ts:100-159`.

The ThingsBoard adapter reads server attribute `active`; its Site argument is unused, and any read failure becomes `false`. Source: `hvac-backend/src/integration-thingsboard/adapters/thingsboard-device-activity-reader.service.ts:11-32`.

At DeviceService level, an adapter error also becomes an empty result and then `false`. Source: `hvac-backend/src/registry/services/device.service.ts:187-205`.

This collapses confirmed inactive, missing attribute, upstream failure, and authorization/transport failure into one value. It must not become the S2 Presence contract.

The S1 Registry UI correctly keeps lifecycle separate and displays “S2 尚未提供” instead of inventing online state. Source: `apps/hvac-web/src/pages/Assets/RealAssets.tsx:254-317`.

## HVAC Web real-mode contract gaps

### Batch shape

HVAC Web declares batch latest as `DeviceSnapshot[]` and later calls `.find(...)`. Source: `apps/hvac-web/src/api/rest.ts:17-18,92-110`; `apps/hvac-web/src/api/types.ts:12-15`.

The backend returns an object keyed by requested Device ID. Source: `hvac-backend/src/telemetry/telemetry.service.ts:122-171`.

A real batch response is not consumable by `useTelemetryLive` as written.

### Timeseries parameters

HVAC Web sends `keys` and `range` to the modern timeseries endpoint. Source: `apps/hvac-web/src/api/rest.ts:12-15`.

That endpoint accepts explicit boundaries, limit, and aggregation and otherwise defaults to one hour. Source: `hvac-backend/src/telemetry/telemetry.controller.ts:56-109`.

The separate compatibility route accepts `range`, but HVAC Web does not call it. Real-mode `6h`, `24h`, day, week, and month requests therefore do not preserve mock behavior.

### Browser context and recovery

REST sends the UI building ID as `X-Site-Id` and the Legacy session credential from browser storage. Sources:

- `apps/hvac-web/src/api/auth.ts:4-14`.
- `apps/hvac-web/src/api/http.ts:5-13`.

Socket.IO sends the session credential through the handshake query and sends no Site context. Source: `apps/hvac-web/src/api/telemetry.ts:22-30`.

The browser keeps the last arrival per Device/key in one animation frame without comparing sample timestamps. On reconnect it only resubscribes; it does not obtain a Snapshot or replay from a Cursor. Source: `apps/hvac-web/src/api/telemetry.ts:122-128,174-210`.

## Failure matrix

| Condition | Current behavior | S2 requirement exposed |
|---|---|---|
| Unknown or cross-Site Device on REST | Site-qualified lookup returns not found. | Preserve nondiscoverability. |
| Unknown Device in batch | Whole batch rejects during pre-resolution. | Decide atomic versus typed partial semantics. |
| Redis read failure | Logged, treated as cache miss, TB attempted. | Keep fallback but report source/degradation. |
| Redis write failure | Logged and swallowed. | Expose cache health and durability limits. |
| Malformed single cache value | Current timestamp is invented. | Quarantine; never invent freshness. |
| TB latest failure | Success with cache-only or empty object. | Return source, age, completeness, and upstream status. |
| TB batch failure for one Device | Cache-only/empty item without error metadata. | Add per-item status. |
| TB timeseries failure | Error propagates. | Define typed compatibility error. |
| Activity read failure | `active=false`. | Distinguish offline, unknown, and unavailable. |
| Forged or replayed webhook | Accepted if Device ID resolves. | Authenticate source and prevent replay. |
| Delayed point | Overwrites latest and publishes. | Enforce monotonic revision/sample policy. |
| Browser reconnect | Resubscribe only; gaps unknown. | Snapshot plus Cursor/Revision recovery. |
| Permission revoked after connect | Existing subscription remains. | Reauthorize or terminate active subscriptions. |
| Multiple Legacy replicas | Subscription state and fan-out are local. | Define shared routing or an explicit single-replica limit. |

## Current authority and ownership

ThingsBoard is the external source of samples and the `active` attribute. Legacy `hvac-backend` is the current facade and cache/push implementation owner. Redis is not authoritative: it has no expiry, multiple writers, swallowed write failures, and no revision/source metadata. Historical timeseries authority remains ThingsBoard.

The platform ownership registry names Registry, IAM, Gateway, audit, and migration resources but no Presence, latest telemetry, telemetry event family, or subscription projection. Source: `contracts/ownership/data-ownership.v1.json:1-40`.

S2 must establish explicit ownership before implementation. Routing Legacy telemetry through Gateway alone would not create a platform-owned data boundary.

## Observability baseline and gaps

Current evidence is primarily logs for subscriptions, cache errors, TB connection state, and HTTP Site resolution. Sources:

- `hvac-backend/src/telemetry/telemetry.gateway.ts:35-60`.
- `hvac-backend/src/telemetry/telemetry-cache.service.ts:41-43,89-91,114-116`.
- `hvac-backend/src/integration-thingsboard/clients/tb-ws.client.ts:53-108`.
- `hvac-backend/src/site/interceptors/site-context.interceptor.ts:49-87`.

No telemetry-specific metrics implementation was found for ingest lag, sample age, cache age, publication gaps, active subscriptions, authorization denial, replay, slow consumers, or recovery success. There is no revision or trace key that connects a TB sample, Redis write, browser publication, and later REST snapshot.

## Reusable assets

1. Explicit local Device to TB Device mapping.
2. Site-qualified REST nondiscoverability behavior.
3. Site-context policy concepts and HTTP audit evidence.
4. Key-grouped latest/timeseries DTO vocabulary, after adding value typing and freshness metadata.
5. Initial REST snapshot plus one shared live client pattern.
6. Reference-counted browser subscriptions, after protocol correction.
7. Backend-only ThingsBoard adapter boundary; browsers do not contact TB directly.
8. S1 UI discipline separating Registry lifecycle from online state and avoiding real-to-Mock fallback.

## Legacy responsibilities S2 must replace or contain

1. Session-only live subscription authorization.
2. Public, replayable webhook ingest.
3. Three independent latest-cache writers.
4. Shared default ThingsBoard integration context as an implicit dependency.
5. Boolean `active` with failure-to-false behavior.
6. Process-local subscription authority.
7. Reconnect without replay or gap detection.
8. Untyped cache-only/partial latest responses.
9. Frontend tolerant contracts and batch/range/value mismatches.
10. Public acceptance of both local and TB IDs; S2 public APIs should use platform Registry Device IDs only.

## Non-negotiable behavior for S2

1. The browser never contacts ThingsBoard directly.
2. A caller cannot learn whether a Device exists outside its authorized Organization/Site.
3. Registry lifecycle is never interpreted as online state.
4. Real-mode failure never substitutes Mock telemetry.
5. Organization, Site, executing service, and initiating principal come from authenticated platform context, not client-submitted identity fields.
6. Sample timestamps are preserved; malformed data never receives invented freshness.
7. Historical timeseries remains separate unless S2 explicitly adopts it.
8. Recovery never applies an older publication over a newer Snapshot/revision.
9. Permission revocation stops future publications without requiring browser logout.
10. Every Legacy fallback has comparison, rollback, and retirement conditions.

## Security gaps that must close before production cutover

1. Per-subscription Organization/Site/Device/key authorization is absent.
2. Active subscriptions are not revoked when IAM permissions change.
3. TB ingest lacks source authentication and replay protection.
4. The current browser live handshake carries a long-lived Legacy credential in request metadata.
5. Cross-Site live negative tests are absent.
6. Latest writes are not monotonic or deduplicated.
7. There is no recovery-gap detection after disconnect.
8. The shared TB integration context cannot serve as tenant authorization.
9. Subscription state is process-local with no documented replica safety boundary.
10. Malformed Redis data can receive an invented current timestamp.

## Decisions now made possible

This research clears the factual prerequisites for the existing S2 tickets:

- Presence semantics must replace boolean `active` and distinguish stale, unknown, unavailable, missing keys, sample time, receipt time, and platform outage.
- Ownership must select one owner for Presence/latest/revision and explicitly leave or adopt historical timeseries.
- The transport experiment must prove platform authorization, revocation, recovery, multi-replica behavior, and slow-consumer handling, not merely successful publication.
- The public contract must use platform Device IDs, typed partial/degraded responses, and contract-generated browser types.
- Rollout gates must cover cross-Site subscription, forged/replayed ingest, stale overwrite, duplicate source delivery, permission revocation, recovery gaps, and Legacy comparison.

## Verification performed

The findings were cross-checked against the current source and existing telemetry, DeviceService, Site-context, controller, and WebSocket tests. No production behavior was changed by this research.
