# S0 durable Session and Audit loop

> **Scope: CERTIFICATION_REFERENCE.** This document records the S0 durable-session/audit implementation and its Kafka-compatible compatibility path. It does not make Kafka/Redpanda a Phase 1 Production dependency. The canonical Phase 1 deployment is defined by `deploy/platform/phase1/`, where the compatibility event backbone is optional.

Ticket 03 replaces process-local BFF Session state with a PostgreSQL transaction boundary and proves delivery into a separate append-only Audit Ledger through an at-least-once Kafka-compatible backbone.

## End-to-end topology

```text
Browser
  HTTPS + opaque BFF Session cookie
      |
      v
platform-gateway
  PostgreSQL SERIALIZABLE transaction
    Session state
    Audit Intent
    Transactional Outbox
      |
      v
outbox-relay
  broker acknowledgement required
      |
      v
Redpanda / Kafka API
  control.security.session.v1
      |
      v
audit-ledger-service
  Transactional Inbox
  Organization hash head
  append-only Audit Record
      |
      v
Gateway public Organization-scoped audit query
```

The browser cannot reach PostgreSQL, Redpanda, IAM or Audit Ledger directly. Gateway reaches IAM and Audit Ledger through TLS 1.3 mutual authentication and separate audience-, action- and scope-bound delegation grants.

## Atomic Session mutation

A Session create, logout or administrative revocation uses one PostgreSQL `SERIALIZABLE` transaction. Before commit, the transaction must contain all three records:

1. authoritative Session state;
2. immutable Audit Intent;
3. Protobuf Transactional Outbox envelope.

The BFF Session cookie or successful state-changing response is issued only after that transaction commits. Failure after the state write, after the Audit Intent write, before commit, or during a uniqueness/constraint check rolls the entire transaction back. There is no valid externally visible Session without its Audit Intent and Outbox record.

Provider credentials and the Session CSRF secret remain AES-GCM encrypted in the Session table. The browser Session cookie value is converted to a domain-separated SHA-256 Audit Aggregate ID before it reaches Outbox, Kafka or Audit Ledger. Audit Intent and Outbox records contain only actor, Organization, action, result, policy, correlation, redacted aggregate and hash metadata.

## Versioned event contract

The authority is:

```text
contracts/events/session-audit.v1.proto
```

The compatibility lock is:

```text
contracts/events/session-audit.v1.lock.json
```

`npm run events:check` rejects changes to existing message names, field names, field numbers, field types, package identity or Go package identity. A deliberate incompatible change therefore requires a new versioned message/topic rather than silent wire drift.

`SessionAuditEventV1` carries:

- stable message ID and schema version;
- topic partition key;
- aggregate type, ID and monotonic version;
- occurred and envelope creation timestamps;
- correlation, causation and trace IDs;
- initiating user and executing Workload Identity as separate actors;
- acting Organization;
- action, result and policy revision;
- minimized payload hash and Session state.

The repository-owned encoder emits deterministic Protobuf bytes. Tests reject authorization credentials, provider credentials, cookies and delegation material before serialization.

## At-least-once Outbox Relay

The Relay claims pending Outbox rows with `FOR UPDATE SKIP LOCKED` semantics and a finite claim lease.

- A broker failure releases the claim, records a safe error code and keeps the row pending.
- The Outbox is marked published only after the broker acknowledges the message.
- If the broker acknowledges but the database mark fails, the claim expires and the message can be published again.

That last case is intentional at-least-once behavior. The design does not claim exactly-once delivery.

## Transactional Inbox and duplicate convergence

The Audit consumer fetches a broker message without auto-committing its offset. In one PostgreSQL transaction it:

1. sets the transaction-local Organization RLS context;
2. inserts the Inbox message ID, broker location and Protobuf envelope hash;
3. locks the Organization hash head;
4. appends one Audit Record;
5. advances the Organization hash head.

Only after the database transaction commits does the consumer commit the broker offset.

A duplicate message ID with identical Protobuf bytes is acknowledged without appending another Audit Record. The same message ID with different bytes is rejected as an envelope conflict. Tests repeat delivery before and after consumer restart and force a published Outbox row back to pending; all cases converge to exactly one durable Audit Record.

## Append-only Audit Ledger

Audit Records include complete actor, Organization, action, result, policy, correlation and aggregate data. Each Organization has an independent hash chain:

```text
record_hash = SHA-256(previous_record_hash || 0x00 || protobuf_payload)
```

The first record starts from 64 zero hexadecimal characters. A database trigger rejects `UPDATE` and `DELETE` on Audit Records.

## Organization isolation

Audit tables use forced PostgreSQL Row-Level Security. Runtime code sets `app.organization_id` only for the current transaction.

The public query is:

```text
GET /api/v1/audit/session-events/{messageId}
```

Gateway first validates the durable BFF Session through IAM, then signs a separate Audit delegation constrained to:

- audience `audit-ledger-service`;
- action `audit:read`;
- scope `organization:<current organization>`;
- the current initiating principal, policy revision and Session expiry.

An authenticated caller from another Organization receives the same `AUDIT_RECORD_NOT_FOUND` response as an unknown message ID. Record existence is not disclosed.

## Database runtime identities

Local and CI fixtures create four non-superuser, non-owner runtime roles:

- `gateway_runtime` — Session, Audit Intent and Outbox transaction writes;
- `gateway_relay_runtime` — Outbox claim and delivery status updates only;
- `audit_consumer_runtime` — Inbox, hash head and append-only Audit writes under RLS;
- `audit_query_runtime` — Organization-scoped Audit reads only.

Integration tests prove Gateway cannot write Audit Ledger tables, Relay cannot modify Session state, Audit consumer cannot write Gateway tables, and Audit query cannot write Inbox records.

## Local topology

Docker Desktop or a compatible Docker Engine is required.

```bash
npm run dev:s0-durable
```

The command creates ephemeral test PKI and starts:

- PostgreSQL 16;
- Redpanda with the Kafka API;
- deterministic HTTPS OIDC fixture;
- mTLS IAM;
- mTLS Audit Ledger consumer/query service;
- Outbox Relay;
- Platform Gateway;
- HTTPS HVAC Web.

Open:

```text
https://127.0.0.1:5173/system
```

Local certificates are generated under the operating-system temporary directory and removed on shutdown. Docker volumes are also removed by the isolated test topology.

## Verification

```bash
npm run events:check
npm run contracts:check
npm run test:durable-unit
npm run test:durable-postgres
npm run build:energy-api
npm run build:audit-owner
npm run build:outbox-relay
npm run lint
npm run build
npm run audit:durable-session
```

The real browser audit verifies:

- generated-client Session Audit UI state;
- durable Session creation and public Audit query;
- complete actor and Organization context;
- valid payload and hash-chain values;
- Relay and Audit service restart;
- duplicate Protobuf replay convergence to one record;
- cross-Organization non-disclosure;
- broker outage while the Session transaction still commits;
- pending Outbox visibility during outage;
- recovery and one-record convergence after Redpanda restart;
- direct browser access to private Audit Ledger fails.

Ticket 03 intentionally does not add HVAC business entities, telemetry, commands, schedules or AI state. Later tickets extend the same transaction and event backbone patterns to additional domains.
