# 05 — End-to-end trace, metrics and operational diagnostics

**What to build:** make an operator able to follow an authenticated browser request through Gateway, IAM, PostgreSQL, Outbox, Kafka, Inbox and Audit Ledger, while receiving safe service metrics, structured logs and actionable runbooks.

**Blocked by:** 03 — Durable Session event to Audit Ledger; 04 — Versioned route and data ownership with controlled Legacy proxy.

**Status:** completed

- [x] W3C Trace Context propagates across public HTTP, internal RPC, Outbox, Kafka and Inbox.
- [x] Correlation and causation IDs connect durable async work even when trace retention expires.
- [x] A single trace demonstrates login/current-principal through resulting Audit ingestion.
- [x] Route owner and route policy revision appear as safe trace attributes.
- [x] Metrics cover traffic, errors, latency, saturation, Outbox age, consumer lag and audit ingestion.
- [x] Metrics do not use session, principal, request, trace or resource IDs as labels.
- [x] Logs use stable error/action codes and redact credentials, cookies, grants and sensitive bodies.
- [x] Health and diagnostics expose no internal addresses, connection strings or tenant data.
- [x] OpenTelemetry Collector outage or backpressure does not block the user request or committed event.
- [x] Dashboards distinguish public API failures, internal identity failures, Legacy proxy failures and async audit lag.
- [x] Alerts have severity, Primary/Secondary Owner and a tested runbook.
- [x] One runbook covers stuck Outbox, one covers audit consumer lag and one covers Legacy proxy timeout.
- [x] Black-box observability tests assert trace continuity and secret absence.
