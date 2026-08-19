# Outbound Delivery Service

S15 owns durable external delivery. Business domains submit a `DeliveryIntent`; they do not call a webhook provider directly.

## Durable chain

`IntegrationDefinition revision -> DeliveryIntent -> DeliveryAttempt -> DeliveryReceipt | DeadLetter -> ReplayApproval -> new DeliveryAttempt`

The intent is committed before any external effect. Claiming work also commits a new attempt and lease before the adapter runs. An unfinished attempt whose lease expires is therefore treated conservatively as `MAYBE_SENT`: it becomes `OUTCOME_UNKNOWN` and requires governed replay rather than an automatic retry.

Only an adapter result that proves the request was `NOT_SENT` may enter the bounded automatic retry path. `ACCEPTED_NOT_CONFIRMED` and `MAYBE_SENT` never blind-retry.

## REST/Webhook adapter

The first adapter is intentionally narrow:

- exact destination-host allowlist;
- DNS resolution before connect and dialing only the already-approved resolved addresses;
- private, loopback, link-local, multicast, unspecified, metadata-address and CGNAT destinations rejected;
- redirects rejected;
- no environment HTTP proxy;
- request body <= 1 MiB, response evidence <= 256 KiB, timeout <= 30 s, concurrency <= 32, attempts <= 5;
- `CredentialRef` is persisted; credential material is resolved and injected only at the external-I/O boundary;
- provider response bodies are not persisted, only bounded request identifiers and SHA-256 response digests.

## PostgreSQL

`migrations/001_s15_outbound_delivery.sql` creates the `outbound_delivery` schema and FORCE RLS tenant policies. The runtime sets `app.tenant_id` inside each transaction. Immutable attempts, receipts, dead letters and replay approvals are never rewritten as history correction.

The focused PostgreSQL behavior test runs when `OUTBOUND_DELIVERY_POSTGRES_DSN` is supplied. It protects idempotent intent creation, conservative unknown-outcome handling, governed replay, immutable attempt history and lease-expiry recovery.
