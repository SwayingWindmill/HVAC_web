# outbox-relay

`outbox-relay` publishes committed Gateway Outbox rows to the Kafka-compatible Control Backbone. It is a separate runtime process and does not own Session or Audit Ledger state.

## Delivery semantics

The Relay claims one pending row using `FOR UPDATE SKIP LOCKED` and a finite lease.

- Broker acknowledgement is required before `published_at` is recorded.
- Publish failure keeps the row pending and records only a stable error code.
- A failure after broker acknowledgement but before the database mark can produce a duplicate publication.
- Duplicate convergence is provided downstream by the Audit Transactional Inbox.

This is explicit at-least-once delivery. The service does not claim exactly-once messaging.

## Kafka record

- topic: `control.security.session.v1`
- key: `bff-session:<sha256 audit aggregate id>`; the browser Session cookie value is never published
- value: deterministic `SessionAuditEventV1` Protobuf bytes
- headers: message ID, schema version, aggregate version, Organization and content type

The Relay validates the stored Protobuf envelope hash before publishing.

## Runtime database identity

`gateway_relay_runtime` can select and update Outbox delivery fields only. It cannot modify Session state, Audit Intent or Audit Ledger tables.

## Commands

```bash
npm run build:outbox-relay
npm run test:durable-unit
npm run audit:durable-session
```

See `docs/security/s0-durable-session-audit.md` for failure and recovery tests.
