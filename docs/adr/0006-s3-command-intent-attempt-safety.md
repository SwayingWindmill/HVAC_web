# ADR 0006 — S3 governed command intent, attempt and execution-fence boundary

Status: accepted

Date: 2026-07-26

## Context

S3 introduces the first platform capability that may change physical Device state. A control request cannot be treated as an ordinary HTTP mutation: a timeout or process failure may happen after the provider accepted the request, so blind retry can duplicate a physical side effect.

S2 remains the owner of current Device Presence and telemetry Snapshot. Historical telemetry is not a control-precondition source.

## Decision

### Service ownership

`command-service` owns Command Intent, governance state, idempotency, Capability revision, current-state evidence references, device command sequence, transitions, audit intent and dispatch outbox.

`command-dispatcher` is a restricted execution worker. It prepares and resolves Attempts, leases and execution fences, but cannot create user intent, change authorization or invent a Capability.

`thingsboard-connector-control` adapts the provider protocol and classifies provider outcomes. It cannot authorize a request or independently declare business success.

The browser and Gateway never call ThingsBoard directly.

### Intent and Attempt are separate

Intent states begin with:

```text
SUBMITTED -> VALIDATING -> QUEUED -> DISPATCHING
SUCCEEDED | FAILED | REJECTED | CANCELLED | EXPIRED | OUTCOME_UNKNOWN
```

Attempt states begin with:

```text
PREPARED
REQUEST_COMMITTED
ACKNOWLEDGED
VERIFIED
NOT_SENT
FAILED
OUTCOME_UNKNOWN
```

Every transition records its previous state, next state, reason, actor, causation and evidence reference. Historical uncertainty is never deleted.

### Durable acceptance and idempotency

Creating a Command returns `202 Accepted` only after the Command transaction commits. It means the Intent was accepted for governance, not that the Device changed state.

The idempotency key is scoped to `organization_id + device_id`. The same key and canonical payload returns the original Command. The same key with a different payload is rejected.

### Canonical Capability only

Arbitrary provider method names and payloads are forbidden. The first tracer-bullet Capability is `SET_TEMPERATURE_SETPOINT` with:

- canonical unit Celsius;
- absolute range 16–30 °C;
- maximum change from current accepted temperature 3 °C;
- risk `LOW`;
- approval `NONE` for Synthetic-only execution;
- retry policy `PRE_SEND_ONLY`;
- no production provider mapping.

### Required S2 evidence

The initial Capability may reach `QUEUED` only when current S2 evidence says:

```text
Evaluation Availability = AVAILABLE
Device Presence = ONLINE
Telemetry Readiness = CURRENT
required key Quality = GOOD
Business Revision > 0
```

The Command records the S2 Business Revision used for validation. Historical queries, stale caches and Last Known context cannot satisfy the precondition.

### Ordering, lease and fence

Every accepted state-changing Intent receives a monotonic `device_command_sequence` per Device.

Before Connector execution, Dispatcher preparation persists the Attempt, payload hash, lease owner, lease expiry and monotonic execution fence, then moves the Intent to `DISPATCHING`.

An old fence is always rejected. A worker restart never turns an uncertain Attempt back into `QUEUED` merely because the worker disappeared.

### Retry and OUTCOME_UNKNOWN

The Connector classifies the execution boundary as:

```text
PRE_SEND_REJECTED
REQUEST_COMMITTED
ACKNOWLEDGED
```

Only `PRE_SEND_REJECTED` proves that no request was written and may safely return the Intent to `QUEUED` with a higher fence on the next Attempt.

`REQUEST_COMMITTED` without a provable result becomes `OUTCOME_UNKNOWN` and is not automatically retried.

`ACKNOWLEDGED` becomes `SUCCEEDED` only when the Capability verification also passes. Otherwise it becomes `OUTCOME_UNKNOWN`.

### Persistence and activation

PostgreSQL Schema `command_runtime` is the planned authority. Its first tables are capability profiles, intents, attempts, transitions, idempotency, device control state, dispatch outbox and audit intents.

The first planned public operations are:

```text
POST /api/v1/commands
GET  /api/v1/commands/{commandId}
```

Gateway remains the public Session, CSRF and IAM seam. Organization, Site and Principal are derived from verified context, not accepted from the browser body.

Route ownership remains `expand-baseline` with rollout disabled. Ticket 01 permits only a Synthetic Connector and never contacts a production Device or provider endpoint.

### Forbidden paths

```text
browser -> ThingsBoard
browser -> arbitrary provider method/params
Gateway -> ThingsBoard control
AI or Automation -> Connector
historical telemetry -> control precondition
