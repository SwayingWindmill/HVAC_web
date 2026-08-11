# S0 observability operations

> **Scope: CERTIFICATION_REFERENCE.** The Kafka/Redpanda signals and runbooks below apply only to the retained S0 compatibility/certification path. Redpanda is not required by the canonical Phase 1 deployment; the default Phase 1 platform under `deploy/platform/phase1/` keeps Kafka/Redpanda outside the required runtime.

## Signals and boundaries

The S0 platform emits W3C Trace Context across public HTTP, private mTLS HTTP, PostgreSQL-backed Outbox, Kafka headers, Inbox processing, and Audit Ledger writes. Correlation and causation identifiers are persisted in the event envelope so incident analysis does not depend on trace-backend retention.

Diagnostic listeners bind to loopback by default and expose only `/health/live`, `/health/ready`, `/metrics`, and `/diagnostics`. They never return credentials, connection strings, workload certificates, delegation grants, Session cookies, Principal identifiers, Organization identifiers, or raw error text.

Metric labels are restricted to bounded operational dimensions such as service, route template, method, result, topic, and partition. Session, Principal, request, trace, span, message, resource, Organization, and tenant identifiers are rejected by the metrics registry.

## Outbox stuck

**Alert:** `S0OutboxStuck`  
**Primary Owner:** `platform-runtime`  
**Secondary Owner:** `data-platform`  
**Severity:** `page`

1. Confirm `s0_outbox_oldest_age_seconds` is increasing and compare it with `s0_outbox_publish_total{result="error"}`.
2. Check Redpanda availability and broker authentication without printing broker credentials.
3. Inspect `outbox_publish_failed` structured logs using `error_code=BROKER_PUBLISH_FAILED`; do not search for payload bytes or grants.
4. Verify the relay diagnostics endpoint is ready and that `failedExports` is not being mistaken for a business outage.
5. Restore broker service or network reachability. Do not delete Outbox rows or manually advance aggregate versions.
6. Confirm the oldest age returns below 30 seconds and the same messages converge through Inbox deduplication.

A drill is successful when the broker is stopped long enough to trigger the alert, public Gateway requests remain available, and queued records publish exactly once logically after broker recovery.

## Audit ingestion lag

**Alerts:** `S0AuditIngestionFailing`, `S0AuditIngestionLatencyHigh`  
**Primary Owner:** `security-platform`  
**Secondary Owner:** `platform-runtime` for failures, `data-platform` for sustained latency  
**Severity:** `page` for failures, `ticket` for sustained latency

1. Compare `s0_audit_ingestion_total` by result and `s0_audit_ingestion_latency_seconds` p95.
2. Confirm the Audit consumer process, PostgreSQL consumer role, and Redpanda group are healthy.
3. Use `s0_audit_consumer_offset` per bounded topic/partition to determine whether one partition is stalled.
4. Inspect stable codes `AUDIT_TRANSACTION_FAILED` and `OFFSET_COMMIT_FAILED`; raw SQL, DSNs, payloads, and grants must remain redacted.
5. Restore the failing dependency. Never bypass the Inbox transaction or mutate append-only Audit records.
6. Query the original message through the Gateway and verify one ledger record exists with the original correlation, causation, and trace identifiers.

## Collector unavailable

**Alert:** `S0TelemetryExportFailures`  
**Primary Owner:** `platform-runtime`  
**Secondary Owner:** `developer-experience`  
**Severity:** `ticket`

1. Confirm public and private business endpoints remain available; telemetry export is explicitly non-blocking.
2. Check `/diagnostics` for increasing `droppedSpans` or `failedExports` without exposing endpoint addresses or credentials.
3. Restore the pinned Collector service and verify export counters stop increasing.
4. Do not enlarge queues without a memory-impact review. The queue is bounded so a telemetry outage cannot become a platform outage.

A drill is successful when the Collector is stopped during login/current-principal traffic, request latency and success remain within the normal test envelope, and export failures are visible after the fact.

## Secret-absence verification

Seed test-only markers in a browser Cookie, Authorization header, CSRF header, delegation grant fixture, database URL, and event payload fixture. Search captured OTLP JSON and structured logs for those exact markers. The test passes only when every marker is absent while trace IDs, route owner/revision, service names, stable error codes, and correlation/causation identifiers remain queryable.
