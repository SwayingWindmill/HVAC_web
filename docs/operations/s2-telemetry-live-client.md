# S2 Ticket 07 — TelemetryLiveClient and browser recovery adapter

## Boundary

`apps/hvac-web/src/platform/telemetry-live/index.ts` is the only feature-facing entrypoint. Feature code receives normalized `initializing`, `snapshot`, `live`, `unavailable`, and `revoked` states whose payload is the generated `DeviceObservationSnapshot`. It never receives a Centrifugo channel, connection capability, subscription ID, Transport Position, or Recovery Cursor.

Only `centrifugo-transport.ts` imports the Centrifuge SDK. The state machine, browser storage, public types, and orchestration layer depend on generated platform contracts and internal transport interfaces. This prevents feature pages from becoming coupled to transport protocol details.

## Snapshot-first algorithm

A new exact Device/key subscription uses the frozen Snapshot-first sequence:

1. call Gateway bootstrap with only `clientSubscriptionId`, Device, exact keys, and an optional same-scope Cursor;
2. create the transport subscription so publications can be buffered immediately;
3. fetch an authoritative Snapshot for exactly the same Device/key scope;
4. install the Snapshot and set its Business Revision as the applied revision;
5. process buffered publications in delivery order;
6. ignore duplicate or older revisions, apply only `previousRevision == appliedRevision` and `revision == previousRevision + 1`, and reload Snapshot on every gap or ambiguity.

The buffer is bounded to 256 publications. Overflow never produces partial live state; it returns to Snapshot authority.

## Reconnect and recovery

A stored Cursor is accepted only for the exact same `clientSubscriptionId`, Device, ordered key list, unexpired cursor record, and stored generated Snapshot. Transport recovery is necessary but insufficient: the adapter also requires the same epoch, a valid transport position, strict publication schema/scope, and continuous Business Revision.

History overflow, epoch reset, recovery failure, missing position, out-of-order transport offsets, unknown schema, invalid revision relation, or an unselected key all fail closed. Scope violations revoke the local subscription; continuity uncertainty reloads the authoritative Snapshot.

Connection capability renewal is owned by the adapter. It checkpoints currently applied Business Revision plus the SDK position, persists only the matching Snapshot/Cursor result, and re-bootstraps the same subscription set. A renewal that changes Device, keys, subscription ID, or channel is rejected.

## Browser Last Known data

`BrowserRecoveryStore` uses `sessionStorage` with an in-memory fallback. It persists only:

- local client subscription ID;
- exact Device and ordered keys;
- generated Snapshot;
- opaque Recovery Cursor and expiry;
- save timestamp.

It never persists channel, connection capability, subscription capability, subscription ID, epoch, offset, or raw transport events. The store validates exact scope and expiry before every use.

Revocation, wrong-scope publication, Organization switch, and logout call purge paths that remove the browser Last Known record. Unauthorized state is never retained as a fallback.

## Failure semantics

- Snapshot read unavailable: normalized `unavailable` with the last authorized Snapshot, if still authorized.
- Transport disconnect or slow consumer: normalized `snapshot/reconnecting`; failed recovery returns through a new authoritative Snapshot.
- Revocation or `RESOURCE_NOT_FOUND`: normalized `revoked`, no Snapshot, and browser cache deletion.
- Wrong Device/subscription/key: fail closed and purge that scope.
- Unknown schema or revision relation: stop applying live data and reload Snapshot.

There is no Legacy, Mock, Socket.IO, or ThingsBoard request fallback in this module.

## Evidence

Run:

```bash
npm run s2:live-client:check
npm run s2:live-client:browser
```

The real-browser CDP harness exercises multiple exact subscriptions, publication-before-Snapshot buffering, duplicate and contiguous revisions, gap fallback, successful reconnect recovery, recovery failure and epoch reset, slow-consumer disconnect, checkpoint and page restore, capability renewal, revocation, wrong-scope publication, and Organization switch/logout purge.

Evidence is written to:

- `out/s2-telemetry-live-client/live-client.json`
- `out/s2-telemetry-live-client/browser-live-client.json`

## Out of scope

Ticket 07 does not migrate room, Device, topology, or asset pages and does not remove the existing Legacy/Socket.IO client. Those feature migrations and cohort controls remain later S2 Tickets.
