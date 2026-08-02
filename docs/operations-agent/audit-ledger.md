# Operations Agent Audit Ledger

## Purpose

This runbook defines the governed Operations Audit path. Audit records business facts and
authorization references. It is not a Trace store, Runtime Checkpoint, retry authority or recovery
mechanism.

## Event contract

The only accepted event is `hvac.operations.audit.v1`. It contains:

- a deterministic event identity and schema metadata;
- bounded actor type, actor identity and issuer;
- the executing Operations Agent service and verified SPIFFE identity;
- Organization and Site Scope;
- optional Investigation and Run identities plus Investigation Revision;
- authorization decision identity and policy revision;
- a fixed operation and outcome;
- occurrence time;
- at most 32 unique typed business-record references.

The contract uses exact-key validation. It rejects prompt text, model output or statements, operator
notes or free text, raw Owner payloads, arbitrary attributes, cookies, grants, tokens, secrets,
Checkpoint state, Lease identities, event-stream cursors and unrestricted errors. `action` is a
compatibility alias and must equal `operation`.

## Atomicity

Successful Investigation mutations insert the Audit intent inside the same PostgreSQL transaction as
the aggregate, typed business record and Application Outbox. If any transaction member fails, none of
them commits.

Authorization denial and resource-budget exhaustion have no successful business mutation to join.
They therefore create a deterministic standalone Audit intent. Failure to store that intent never
turns a denial into an allow, resets a budget, starts external work or changes the public result.

## Delivery

`agent_operations.audit_records` is a delivery outbox, not the final Ledger. The delivery worker:

1. claims pending or retryable rows with `FOR UPDATE SKIP LOCKED` and a bounded lease;
2. posts the exact event JSON to `/internal/v1/audit/operations-events`;
3. sets `Idempotency-Key` to the event identity;
4. marks the row delivered after HTTP 204;
5. classifies timeout, unavailable, rejected or invalid responses and schedules bounded exponential
   retry.

Exact retries reuse one event identity. Reuse with different content fails closed. Runtime database
permissions allow updates only to delivery status, attempt, retry, lease, delivery time and bounded
failure-class columns.

## Audit Ledger owner

The private ingest route accepts only the configured Operations Agent mTLS workload. Browser and
caller-supplied authorization, identity, Organization or Site headers are rejected. The event's
executing SPIFFE identity must equal the verified TLS peer.

The Ledger uses the event identity as its inbox identity. Exact duplicates are inert; different
content under the same identity is a conflict. It serializes writes per Organization, hashes the
Operations Investigation aggregate identity and advances the append-only Organization record hash
chain. Multiple Operations facts may be recorded at the same Investigation Revision. Cross-
Organization queries are nondiscoverable and UPDATE/DELETE remain prohibited.

## Trace separation

Operations Audit records intentionally store empty Trace ID and `traceparent` fields. Use hashed
OpenTelemetry correlations for diagnostics and the Audit Ledger for governed facts. Neither is a
substitute for the other.

## Verification

- Operations Agent unit tests validate strict schema, redaction, denial and budget events, exact
  HTTP delivery and failure isolation.
- Operations PostgreSQL tests validate atomic outbox insertion, exact content deduplication, leases,
  retry, reclaim and delivered state.
- Audit Ledger unit tests validate strict decoding, mTLS producer identity, forged-header rejection,
  conflict behavior and error redaction.
- Durable PostgreSQL tests validate exact duplicate handling, same-revision multi-event append,
  Organization hash-chain advancement, aggregate hashing, tenant isolation and append-only storage.
- The `operations-audit-ledger-boundary` deterministic benchmark hard-fails on atomicity,
  idempotency, context, redaction, delivery isolation, append-only, tenant or Trace-separation drift.
