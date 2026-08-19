# audit-ledger-service

`audit-ledger-service` is the private S0 append-only security Audit consumer and query seam. It is not browser-facing and contains no HVAC business state.

## Responsibilities

- consume `control.security.session.v1` from the Kafka API;
- decode the versioned Protobuf envelope;
- write Transactional Inbox, Tenant hash head and Audit Record in one PostgreSQL transaction;
- commit broker offsets only after the database transaction commits;
- suppress identical duplicate message IDs;
- reject the same message ID with different Protobuf bytes;
- enforce append-only records and Tenant Row-Level Security;
- serve one private mTLS audit query route.

## Private route

```text
GET /internal/v1/audit/session-events/{messageId}
```

The route requires:

- a trusted Gateway workload certificate;
- a grant signed by that workload key;
- audience `audit-ledger-service`;
- action `audit:read`;
- scope `tenant:<tenant id>`;
- an IAM-authorized `audit.read` capability represented by the constrained `audit:read` delegation; the Ledger does not interpret Role names.

Unknown and cross-Tenant records are both returned as `AUDIT_RECORD_NOT_FOUND`.

## Runtime database identities

The consumer uses `audit_consumer_runtime`. The HTTP query seam uses `audit_query_runtime`. Neither role owns the schema or can write Gateway tables.

## Commands

```bash
npm run build:audit-ledger
npm run test:durable-unit
npm run test:durable-postgres
npm run audit:durable-session
```

See `docs/security/s0-durable-session-audit.md` for the full transaction, delivery, RLS and hash-chain model.
