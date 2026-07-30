# S2 Ticket 06 — Realtime publication, subscription, and recovery backend

## Scope

Ticket 06 activates the server-side realtime half of the S2 public contract. The browser still obtains all capabilities through Platform Gateway. Telemetry Runtime remains the only owner of active subscriptions, recovery cursor records, publication intent, Device Business Revision, and the decision to use an authoritative Snapshot after recovery uncertainty.

Centrifugo `v6.8.1` and its dedicated Redis are transport components only. They may connect clients, fan out publications, retain a bounded stream, expose epoch/offset positions, and execute server-side unsubscribe. They never authorize a Device/key scope, mint a platform recovery cursor, own a Business Revision, or become a source of telemetry truth.

## Public flow

1. The caller sends only `clientSubscriptionId`, Device ID, exact keys, and an optional prior same-scope recovery cursor to Platform Gateway.
2. Gateway derives the caller from the HttpOnly BFF Session or a verified workload SPIFFE identity. Session POST requests require exact Origin and CSRF validation.
3. Gateway requests an exact IAM grant for `SUBSCRIBE`, `RECOVERY_USE`, or `RECOVERY_CHECKPOINT`.
4. Telemetry Runtime creates all requested subscriptions atomically in PostgreSQL and returns opaque channels plus one short-lived Centrifugo connection token.
5. Centrifugo calls the Telemetry Runtime subscribe proxy for every channel. Runtime rechecks the active subscription, Registry binding, current IAM projection, denial precedence, expiry, principal, Device, and selected keys. A channel name or client token is never authority.
6. Every committed Device Business Revision already has a PostgreSQL publication outbox row. The relay reuses its event ID and exact previous/current revision and publishes a per-subscription delta. If none of the subscription's selected keys changed, `telemetryChanges` is an empty array; Presence and readiness still advance with the Device revision.
7. The browser SDK reports the applied Business Revision and Centrifugo epoch/offset to the checkpoint route. Runtime verifies the active subscription and current revision, then stores and returns a short-lived HMAC-signed, scope-bound cursor.

## Recovery rule

Transport recovery is attempted only when the cursor signature, persisted cursor record, principal, issuer, Session, Organization, Device, exact keys, scope digest, subscription state, expiry, epoch, offset, and IAM authorization all still match. Recovery is accepted only when Centrifugo reports a successful recovery and the resulting Device revisions are continuous.

The adapter must fetch an authoritative Snapshot when any of these conditions occur:

- no successful recovery or an expired/unknown cursor;
- an epoch reset, history overflow, partial recovery, or revision gap;
- a duplicate-only result that cannot establish current continuity;
- a slow-consumer disconnect or transport/node/Redis restart with uncertain continuity;
- IAM, Registry, PostgreSQL, or owner-scope recheck is unavailable;
- revocation occurred before or during reconnect.

Centrifugo history and Redis are never used to reconstruct authority.

## Revocation

IAM calls the private Runtime revocation endpoint over verified mTLS. Runtime first marks matching subscriptions and recovery cursors revoked in PostgreSQL, then calls Centrifugo's server-side unsubscribe API. Subscribe proxy, cursor replay, and recovery bootstrap fail closed immediately after the owner transaction. The release envelope requires zero post-revocation publications and a maximum propagation bound of 10 seconds; Ticket 06 integration evidence exercises immediate unsubscribe and later publication rejection.

## Locked transport bounds

- Centrifugo image: `v6.8.1` at the repository-locked immutable digest.
- Redis image: `7.4.2-alpine` at the repository-locked immutable digest.
- dedicated Redis network and volume; Redis exposes no host port;
- client queue: 262,144 bytes;
- hard subscriptions per connection: 100;
- history and recovery response: 256 publications;
- history TTL: 180 seconds;
- recovery cursor lifetime: at most 120 seconds;
- connection and subscription capability lifetime: at most 300 seconds;
- encoded owner publication: at most 65,536 bytes.

Centrifugo's HTTP subscribe proxy uses its native TLS client configuration with a deployment-issued certificate whose URI SAN is `spiffe://hvac.local/centrifugo`; Runtime verifies that mTLS identity before evaluating the static proxy secret or subscription scope. The Runtime CA, client certificate, private key, API key, token HMAC key, and proxy secret are injected by the deployment environment. The checked-in configuration contains no usable credential.

## Evidence and rollback

Run `npm run s2:realtime-backend`. Evidence is written under `out/s2-ticket-06/` and `out/s2-centrifugo-poc/`.

Rollback disables the bootstrap/checkpoint route revision, expires live connection capabilities, performs server-side unsubscribe, and returns clients to authoritative Snapshot reads. It does not route an individual request to Legacy, Mock, Socket.IO, or ThingsBoard. PostgreSQL migrations are expand-only and remain compatible during rollback.

## Out of scope

- HVAC Web `TelemetryLiveClient` and browser recovery adapter;
- production cohort traffic or route canary;
- using Centrifugo/Redis as data authority;
- retiring Legacy realtime or current-state reads.
