# 03 — Durable Session event to Audit Ledger

**What to build:** make login, logout and revocation durable, auditable state transitions. Gateway atomically persists Session state, Audit Intent and Outbox; an at-least-once relay publishes to the Control Backbone; Audit Ledger consumes through Transactional Inbox so an authorized audit query observes exactly one append-only record.

**Blocked by:** 02 — Authenticated principal loop through Gateway and IAM.

**Status:** completed

- [x] Session state, Audit Intent and Outbox commit atomically in the owning PostgreSQL transaction.
- [x] Pre-commit failure leaves none of the three artifacts accepted.
- [x] Outbox publishes a versioned Protobuf event with stable message ID, aggregate version, actor chain and correlation.
- [x] Delivery is explicitly at-least-once.
- [x] Audit consumer uses Transactional Inbox and duplicate delivery creates no duplicate record.
- [x] Audit preserves initiating principal, executing service, Organization context, action, result and policy revision.
- [x] Audit and Kafka payloads contain no credentials or grants.
- [x] Authorized Organization-scoped audit query can retrieve the event.
- [x] Cross-Organization audit query reveals no existence or content.
- [x] Consumer restart, duplicate publish, replay and broker restart converge to one result.
- [x] Broker outage leaves committed Outbox pending and recoverable.
- [x] Audit Intent persistence failure makes the security mutation fail closed.
- [x] Black-box tests begin with browser/API actions and observe the audit seam.
- [x] Runtime identities cannot write another service's schema.
