# Operations Agent observability

## Purpose

This runbook defines the diagnostic-only telemetry boundary for the Operations Agent. It does not define business authority, retry state or recovery state.

## Correlation

Platform Gateway accepts valid W3C traceparent and tracestate values and creates a child CLIENT span for the authorized Operations upstream call. Operations Agent Service creates a SERVER span and continues child spans through authorization, Runtime planning, Runtime Steps, logical Tools, fixed Owner calls, resource checks, business commits, model synthesis, terminal transitions and event-stream recovery.

Investigation, Run, Step and request identities are transformed into stable SHA-256 correlation values before export. Query traces by those hashed values when following work across restart, Checkpoint recovery or reconnect. Never add raw identities or Last-Event-ID values to span attributes or metric labels.

## Allowed telemetry

Allowed attributes are fixed operation, outcome, logical Tool, fixed Owner, recovery mode and reason, resource-budget dimension, duration, retry count, record count, payload bytes, token counts, duplicate, restart, partial and terminal booleans.

Allowed metric labels are fixed operation, outcome, logical Tool, Owner, recovery mode, recovery reason and budget dimension. Changes to these catalogs require contract and benchmark updates.

## Forbidden telemetry

Do not export prompt text, completion text, operator text or notes, Owner payloads, model statements, arbitrary error messages, cookies, CSRF values, bearer tokens, delegation grants, secrets, raw request or Investigation identities, Run or Step identities, event-stream cursors or Checkpoint state.

## Failure behavior

Exporters are asynchronous and bounded. Failed exports and dropped spans are diagnostic counters only. Do not retry business work because telemetry export failed. Do not fail an HTTP response, Investigation transaction, resource check, Outbox write or Audit write because telemetry is unavailable.

## Verification

The service telemetry tests prove W3C lineage, restart-stable hashing, redaction, low-cardinality rejection, bounded queues, exporter isolation and separation from business records. Gateway tests prove authorized child propagation, reconnect correlation, recovery metrics and exporter isolation. The operations-telemetry-boundary deterministic scenario hard-fails when any of these controls is removed.
