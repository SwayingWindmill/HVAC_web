# S2 realtime transport evaluation

Research ticket: [S2 实时传输方案与 Centrifugo 采用实验](https://github.com/SwayingWindmill/HVAC_web/issues/49)

Evaluation date: 2026-07-23

Evaluated Centrifugo version: `v6.8.1`, digest locked in `pocs/platform-components/versions.lock.json`

## Decision

**Adopt with bounded responsibility.**

Centrifugo `v6.8.1` may enter the S2 design as the browser connection and publication transport only. It may own WebSocket connection handling, channel multiplexing, bounded short-window epoch/offset recovery, client queue enforcement and transport metrics.

It must not own or redefine:

- Organization, Site, Device or key authorization;
- current Presence or telemetry state;
- Snapshot authority;
- business Revision, deduplication or gap meaning;
- IAM permission revocation decisions;
- retained business history;
- source-system mapping;
- fallback, degradation or rollout decisions.

Those responsibilities stay with the platform owner selected by Issue #50. The client consumes an owner Snapshot plus owner-authored business Revisions; Centrifugo history is an optional continuity cache around that contract.

## Why this is not a general Centrifugo adoption

The earlier platform-component POC proved an authenticated connection, one client subscription, live publication, one recovered offline publication and Prometheus metrics. It deliberately did not prove platform Scope authorization, permission revocation, Snapshot continuity, stale-cursor behavior, restart behavior or backpressure.

The supplemental experiment in `pocs/s2-centrifugo/` adds those S2-specific boundaries and keeps the platform owner in the loop. Its passing result authorizes a transport dependency candidate, not a second business owner.

## Options compared

| Option | Advantages | S2 responsibilities that remain project code | Main costs and risks | Decision |
|---|---|---|---|---|
| Platform-owned WebSocket | Lowest external dependency count; full control of protocol and authorization; natural bidirectional subscription changes. | Connection lifecycle, multiplexing, reconnect protocol, retained history, epoch/offset, queue limits, slow-consumer handling, fan-out, cluster routing, metrics and client SDK behavior. | Rebuilds substantial realtime infrastructure around the actual business contract. Correct recovery and multi-replica delivery become project-owned operational systems. | Valid rollback adapter; not the preferred initial transport. |
| Platform-owned SSE | Browser-native one-way stream; simple HTTP authorization and infrastructure compatibility; useful for fixed server-to-client feeds. | Subscription-change HTTP API, reconnect state, Last-Event-ID semantics, fan-out, history, queueing, slow consumers, multi-device multiplexing and metrics. | S2 needs dynamic multi-Device subscriptions and explicit continuity results. Native EventSource is one-way, while emulated bidirectional SSE adds another protocol layer. | Keep as a fallback for simpler fixed feeds, not the initial dynamic telemetry transport. |
| Centrifugo `v6.8.1` | Existing protocol/SDK, WebSocket and emulated transports, channel multiplexing, subscribe proxy, bounded history recovery, queue limits and Prometheus metrics. | Platform authorization, Snapshot, business Revision, revocation orchestration, audit, fallback and business SLOs. | Adds a runtime component and hot-path proxy; broker/retention configuration must be owned; version-specific protocol and metric drift must be gated. | **Adopt with bounded responsibility.** |

Centrifugo's own documentation describes stream history as a bounded cache rather than an authoritative event log and requires a backend fallback when recovery cannot prove continuity. That matches the S2 boundary rather than competing with it.

## Selected responsibility model

### Platform owner

The future S2 owner is the only authority for:

1. authenticated Principal, Organization, Site and Device Scope;
2. exact subscription authorization and key policy;
3. Presence Signals, latest accepted observations and policy revisions;
4. authoritative Snapshot generation;
5. monotonic business Revision and duplicate/out-of-order rules;
6. current permission state and revocation orchestration;
7. audit, trace and business metrics;
8. deciding whether a transport recovery result is usable or a fresh Snapshot is required.

### Centrifugo transport

Centrifugo is permitted to own:

1. browser realtime connection protocol;
2. channel subscription multiplexing;
3. publication fan-out;
4. ephemeral epoch/offset positioning;
5. bounded short-window recovery;
6. bounded client output queues and disconnect behavior;
7. transport-level Prometheus metrics.

Centrifugo presence, map subscriptions, cache recovery state or channel history must not become the S2 Presence/latest source of truth.

### Browser client

The browser owns only local reconciliation state:

- current owner Snapshot Revision;
- current Centrifugo stream position managed by the SDK;
- buffered publications received while Snapshot loads;
- deterministic duplicate and gap handling;
- an immediate Snapshot fallback request when continuity cannot be proven.

## Authorization model

### Connection identity

The platform signs the short-lived Centrifugo connection identity. The browser cannot select its Principal, Organization or Site by changing a channel name or connection payload.

The experiment rejects a forged connection credential before subscription.

### Subscription authorization

Every client-controlled subscription passes through the platform subscribe proxy. The proxy derives the Principal from the authenticated Centrifugo connection and checks the requested internal channel against current platform authorization.

The channel is a routing identifier, not an authorization fact. It uses platform resource IDs only; ThingsBoard IDs remain internal mapping data.

The experiment proves:

- `s2-user` can subscribe to its exact Organization/Site/Device channel;
- the same authenticated user is denied a cross-Site channel with a typed 403 proxy decision;
- the proxy records allowed and denied decisions as owner evidence.

Subscription JWTs are not selected for this dynamic path. Centrifugo permits token-authorized subscriptions, but a valid subscription token can bypass the subscribe proxy decision. S2 needs current owner authorization and explicit revocation, so the subscribe proxy remains the required gate.

### Permission revocation

A proxy decision only protects new subscriptions. Existing delivery stops through an owner-driven sequence:

1. IAM or owner authorization state changes;
2. the owner records the revocation;
3. the owner invokes Centrifugo server `unsubscribe` for the Principal and channel;
4. the client receives an unsubscribe push;
5. any resubscribe attempt is checked by the proxy and denied;
6. later publications are not delivered.

The experiment proves the complete sequence. Centrifugo remains the mechanism that removes the live subscription; the platform remains the authority and audit owner.

## Snapshot and publication continuity

### Initial load algorithm

The selected browser algorithm is:

1. establish the authenticated Centrifugo connection;
2. subscribe to the authorized Device channel and buffer publications;
3. request the authoritative owner Snapshot;
4. install the Snapshot and its business Revision;
5. order buffered publications by transport offset;
6. ignore publications whose business Revision is less than or equal to the installed Revision;
7. apply only the next contiguous business Revision;
8. request a new Snapshot on a business Revision gap.

This order closes the race where an update occurs while the HTTP Snapshot is loading.

The POC deliberately captures Snapshot Revision 1, delays its response, and publishes Revision 2 twice followed by Revision 3. The client finishes at Revision 3, applies `[2,3]` and ignores one duplicate Revision 2.

### Reconnect algorithm

For a reconnect, the Centrifugo SDK returns the last stream `epoch` and `offset` and attempts recovery.

- When recovery succeeds, the client still applies owner business Revision rules to every recovered publication.
- When recovery was attempted but did not succeed, the client discards transport continuity assumptions and fetches a full owner Snapshot.
- Centrifugo transport offsets never replace business Revision.

The current Centrifugo documentation describes `wasRecovering=true` plus `recovered=false` as the fallback signal and states that recovery must not return a partial gap. The raw locked `v6.8.1` protocol observed by this POC omits the `recovered` field when false; the robust rule is therefore `was_recovering === true && recovered !== true`.

### Short-window success

The POC disconnects after Revision 3, publishes Revision 4 while offline and recovers Revision 4 from the retained epoch/offset stream.

### Retention exceeded

The POC retains four messages and publishes six after the saved cursor. Recovery is attempted, does not report success and returns no partial publication list. The client then loads owner Snapshot Revision 11.

This behavior is required: an incomplete recovered suffix cannot be presented as continuous state.

### Service restart

The POC uses Centrifugo's memory engine. Restart changes or loses the stream epoch and cannot recover the previous position. The client detects recovery failure and loads owner Snapshot Revision 11.

Issue #50 must choose the production broker engine and service ownership. A Redis or PostgreSQL broker may preserve more transport history, but owner Snapshot fallback remains mandatory because Centrifugo history is deliberately ephemeral.

## Slow consumers and backpressure

The POC configures a `16,384` byte client queue, pauses one client's network reads and publishes 96 messages with 32 KiB payloads.

The locked `v6.8.1` server disconnects that client with close code `3008`, and `centrifugo_client_num_server_disconnects` increases by one. This proves an enforceable bounded queue and observable slow-consumer failure rather than unbounded server memory growth.

Production values must be selected from actual telemetry payload sizes and rollout targets. The POC queue is intentionally small to force the branch; it is not a recommended production setting.

## Capacity model and evidence

The local experiment opens 32 authenticated clients, subscribes each to one channel and proves all 32 receive one publication. The measured local delivery was 5 ms in the recorded passing run.

This is bounded protocol evidence only; **production scale is not certified**.

The release-gate ticket must declare and test a rollout-specific model using at least:

```text
activeConnections
subscriptionsPerConnection
activeChannels
averageSubscribersPerChannel
peakPublicationsPerSecond
averageEncodedPublicationBytes
recoveryWindowSeconds
historySizePerChannel
slowConsumerFraction
reconnectBurstConnectionsPerSecond
subscribeProxyRequestsPerSecond
```

Derived load includes:

```text
outboundBytesPerSecond = publicationsPerSecond × encodedBytes × subscribersPerPublication
historyPayloadUpperBound = activeHistoryChannels × historySize × encodedBytes
proxyPeak = newSubscriptions + subscriptionRefreshes + reconnectResubscriptions
```

No numeric production target is invented in this decision. Issue #52 must turn the rollout target into a repeatable load test and SLO.

## Observability boundary

### Centrifugo evidence

The locked image exposes:

- client recovery outcome counters;
- subscribe proxy latency histograms;
- server disconnect counters with code labels;
- node client and subscription gauges;
- node broker action counters;
- general process and transport metrics.

The passing run proves recovery, proxy and slow-consumer evidence.

The locked `v6.8.1` image does not expose the dedicated server-unsubscribe metric found in newer documentation. Revocation must therefore be evidenced by the platform authorization event, Centrifugo API result, client unsubscribe push, denied resubscribe and absence of later delivery.

### Platform evidence

The owner must log and trace bounded identifiers for:

```text
requestId
traceId
principalId
organizationId
siteId
deviceId
subscriptionId
channelHash
scopeRevision
snapshotRevision
publicationRevision
recoveryOutcome
revocationReason
```

Tokens, raw credentials and unbounded telemetry values are forbidden log labels.

Centrifugo OSS tracing covers server API request handling; publication-to-Snapshot business correlation remains platform-owned. The public contract and release-gate tickets must define the final trace propagation fields.

## Operations and failure behavior

| Failure | Required behavior |
|---|---|
| Subscribe proxy unavailable | Subscription fails closed; no unverified channel access. |
| Owner Snapshot unavailable | UI reports platform `UNAVAILABLE`; buffered data is not promoted to authoritative current state. |
| Centrifugo unavailable | HTTP Snapshot remains available where the owner is healthy; realtime state degrades explicitly. |
| Recovery window exceeded | No partial replay; fetch owner Snapshot. |
| Epoch changed or broker history lost | Fetch owner Snapshot. |
| Business Revision duplicate | Ignore without applying twice. |
| Business Revision gap | Stop incremental application and fetch owner Snapshot. |
| Permission revoked | Owner unsubscribe, deny resubscribe, stop later publication. |
| Slow consumer | Bounded queue disconnect; reconnect and recover or Snapshot fallback. |
| Subscribe proxy latency increases | Alert on proxy histogram and authorization error rate; do not bypass authorization. |

## Security configuration

The production candidate requires:

- exact version and image digest pinning;
- runtime-managed connection-signing and server-API credentials;
- private network access to the Centrifugo server API and proxy endpoint;
- explicit browser origins;
- no client publish, history, presence or server-API permission;
- subscribe proxy authorization on every client-selected channel;
- short-lived connection identity and current authorization on resubscribe;
- explicit server unsubscribe on permission revocation;
- bounded channel count, publication size, recovery count and output queue;
- platform IDs only in channels and publications;
- negative tests for forged identity, cross-Site discovery, revoked access and API exposure.

The POC creates credentials at runtime and asserts that they do not enter the machine-readable report.

## License and edition boundary

The evaluated Centrifugo repository and locked component record use the `Apache-2.0` license.

This decision relies only on capabilities exercised in the locked OSS image. It does not assume Centrifugo PRO map subscriptions, proprietary brokers, PRO observability or PRO proxy events. Any future move to a different edition or capability requires a separate license and responsibility review.

## Upgrade procedure

A Centrifugo upgrade must:

1. pin the candidate version and immutable image digest;
2. review upstream security and protocol changes since the current version;
3. rerun the static asset gate and full executable POC;
4. compare raw recovery fields and Prometheus metric names, not only current documentation;
5. repeat forged identity, cross-Site, revocation, retention, restart and slow-consumer tests;
6. run the rollout-specific capacity suite from Issue #52;
7. canary the transport while keeping owner Snapshot/Revision unchanged;
8. retain immediate rollback to the previous image or alternate transport adapter.

Protocol and metric drift is already demonstrated by this investigation: current documentation describes explicit `recovered=false` and a dedicated server-unsubscribe counter, while the locked raw `v6.8.1` behavior omits the false field and lacks that metric.

## Rollback

The owner Snapshot and business Revision contract must not depend on Centrifugo types. The transport is behind an adapter boundary.

Rollback is:

1. stop routing new browser connections to Centrifugo;
2. disconnect or expire existing Centrifugo sessions;
3. switch the browser realtime adapter to a platform-owned WebSocket or SSE implementation;
4. force every client to load a fresh owner Snapshot before incremental application;
5. retain the same IAM authorization, publication payload, business Revision and audit contract;
6. remove Centrifugo only after connection and metric evidence reaches zero.

No data migration is required because Centrifugo history is not authoritative.

## Evidence produced

Repository assets:

- `pocs/s2-centrifugo/owner.mjs`
- `pocs/s2-centrifugo/centrifugo.json`
- `pocs/s2-centrifugo/compose.yaml`
- `pocs/s2-centrifugo/README.md`
- `scripts/run-s2-centrifugo-poc.mjs`
- `scripts/check-s2-centrifugo-poc-assets.mjs`

Passing machine-readable evidence is generated at ignored path `out/s2-centrifugo-poc/report.json`.

Recorded passing-run facts:

- forged connection rejected;
- cross-Site subscription denied;
- live revocation stopped delivery;
- Snapshot Revision 1 reconciled through Revision 3 with one duplicate ignored;
- short-gap Revision 4 recovered;
- retention loss and service restart detected with Snapshot Revision 11 fallback;
- 32 local clients all received one publication;
- slow consumer disconnected with code `3008` and metric delta 1;
- runtime secrets were not persisted.

## Follow-on constraints

Issue #50 must decide:

- which service owns Snapshot, policy and business Revision;
- whether Centrifugo runs with memory, Redis or PostgreSQL engine;
- who owns publication and revocation orchestration;
- the deployment and credential boundary.

Issue #51 must define:

- public Snapshot and publication payloads;
- stable business Revision and recovery result representation;
- browser fallback behavior and typed errors.

Issue #52 must define:

- production connection/publication/recovery targets;
- proxy and fan-out load suites;
- SLOs, alerts, canary and rollback gates.

## Primary sources

- Centrifugo stream history and recovery: https://centrifugal.dev/docs/server/history_and_recovery
- Centrifugo proxy events: https://centrifugal.dev/docs/server/proxy
- Centrifugo server API: https://centrifugal.dev/docs/server/server_api
- Centrifugo configuration: https://centrifugal.dev/docs/server/configuration
- Centrifugo observability: https://centrifugal.dev/docs/server/observability
- Centrifugo WebSocket transport: https://centrifugal.dev/docs/transports/websocket
- Centrifugo SSE transport: https://centrifugal.dev/docs/transports/sse
- Centrifugo repository and license: https://github.com/centrifugal/centrifugo
- Centrifugo releases: https://github.com/centrifugal/centrifugo/releases

Documentation pages describe the current v6 line. Exact locked-version behavior in this decision comes from the executed `v6.8.1` image and is intentionally called out where it differs.
