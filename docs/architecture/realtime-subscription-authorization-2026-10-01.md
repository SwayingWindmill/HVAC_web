# Realtime subscription authorization — 2026-10-01

## Problem

Every realtime subscription was denied in deployed stacks. The Centrifugo subscribe
proxy and the publication relay required per-key `SUBSCRIBE`/`ALLOW` rows in
`telemetry_runtime.iam_scope_projections`, but no component produced them; only a SQL
test fixture did. IAM's telemetry revocation facts (`iam.telemetry_revocation_facts`,
`/internal/v1/telemetry/revocations:poll`) and the runtime's revoke path
(`RealtimeService.Revoke`) existed, but nothing connected them.

## Decision

- **Authorization record.** A subscription row is created only after IAM consumes the
  single-use subscribe (or recovery) grant for the exact principal, session, tenant,
  device, keys and policy revision. The row expires after at most
  `MaximumSubscriptionTTL` (5 min) and is renewed only through another IAM-consumed
  recovery grant. The subscribe proxy and the publication relay authorize from that row
  plus the active Registry device binding. The projection table is dropped
  (`009-realtime-revocation-relay.sql`).
- **Revocation.** The telemetry worker polls IAM revocation facts per tenant with open
  subscriptions, applies each fact to the subscriptions last authorized at or before
  the fact (`updated_at <= occurredAt`, rounded up to IAM's millisecond resolution), and
  unsubscribes their channels through the Centrifugo server API. The per-tenant cursor
  is stored in `telemetry_runtime.iam_revocation_cursors`. A fact never withdraws an
  authorization IAM issued after it, so replaying old facts is safe.

## Reference review — Centrifugo v6.8.1 (`c1246c5472ff9700868ee768e72de2911a3e4f2b`)

Reviewed in the pinned source under `out/centrifugo-source-review/centrifugo-6.8.1`:

- `internal/proxy/subscribe_handler.go`, `internal/proxy/subscribe_http.go`,
  `internal/proxyproto` `SubscribeRequest`: the backend decides permission when the
  client subscribes; the request carries client, user, channel, optional token/data.
- `internal/api/api.go` `Executor.Unsubscribe`: server-side unsubscribe of a user from a
  channel, delivered to connected clients as a server unsubscribe push.

ADOPT: subscribe-time permission in the owner backend, and server-side unsubscribe for
withdrawal. REJECT for now: subscribe-proxy `ExpireAt` with a sub-refresh proxy; the
5-minute subscription row plus the client's grant-backed renewal already bound access.

## Verified

Local stack with the running simulator: seven device subscriptions accepted, delta
publications delivered, and a revocation fact inserted for the principal ended its open
subscriptions within the 2 s relay interval.
