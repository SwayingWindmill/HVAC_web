# S2 Telemetry observability, capacity and rollout gates

Status: accepted planning baseline

Date: 2026-07-23

Issue: #52

Machine-readable authority: `deploy/s2/release-gates.v1.json`

Primary-source review: `docs/research/s2-observability-capacity-rollout-sources.md`

## Purpose

S2 is not releasable merely because the Snapshot, subscription and recovery contracts compile. A production promotion must prove tenant isolation, live revocation, bounded recovery, observable failure, sufficient capacity, one business writer and deterministic rollback.

This document is the copy-ready acceptance matrix and release checklist for the implementation tickets created by Issue #53. The values are the **Initial Production Release Envelope v1**, not a permanent platform maximum. Increasing any envelope dimension requires a new reviewed contract version and new capacity evidence.

The planned gate does not activate unimplemented S2 routes or workloads.

## Non-negotiable release rules

1. PostgreSQL `telemetry_runtime` remains the authoritative current state.
2. `telemetry-runtime-service` is the only S2 business writer.
3. Centrifugo and dedicated Redis own only bounded transport continuity.
4. Every read, subscription, resubscription, cursor use and checkpoint is authorized from current IAM and Registry facts.
5. No request silently falls back from S2 to Legacy, ThingsBoard or Mock.
6. Shadow comparison is read-only and side-effect-free.
7. A rollout rollback changes the cohort route owner; it is not a per-request fallback.
8. Every transport reset, failed recovery or Business Revision gap requires a fresh authoritative Snapshot.
9. Any non-zero security invariant stops promotion immediately.
10. All promotion and rollback decisions leave a hashed evidence bundle.

## Initial Production Release Envelope v1

| Dimension | Target |
|---|---:|
| Sustained active browser connections | 5,000 |
| Reconnect storm population | 10,000 |
| Reconnect admission rate | 1,000 connections/s |
| Active exact Device/key subscriptions | 50,000 |
| Active history channels | 10,000 |
| Average subscriptions per connection | 10 |
| p95 subscriptions per connection | 25 |
| Hard subscriptions per connection | 100 |
| Peak Device Business Revisions | 2,000/s |
| Average subscribers per publication | 3 |
| Peak publication deliveries | 6,000/s |
| Average encoded publication | 900 bytes |
| p99 encoded publication | 4 KiB |
| Maximum encoded publication | 64 KiB |
| Steady-state run | 60 minutes |
| Short peak | 2× for 15 minutes |
| N+1 proof load | 70% of envelope |
| Minimum measured capacity headroom | 30% |

Derived planning values:

```text
averageOutboundBytesPerSecond
  = 2,000 revisions/s × 3 subscribers × 900 bytes
  = 5,400,000 bytes/s

rawHistoryPayloadUpperBound
  = 10,000 channels × 256 publications × 900 bytes
  = 2,304,000,000 bytes
```

The Redis capacity plan must apply measured serialization, stream/index and replication overhead rather than treating the raw payload value as the provisioned memory requirement.

A release report records both requested and achieved envelope values. It may certify a smaller envelope only by publishing a new lower version before promotion; it cannot mark an under-capacity run as passing.

## Transport and recovery bounds

| Setting | Initial value |
|---|---:|
| Client output queue | 256 KiB |
| History size | 256 publications/channel |
| History TTL | 180 seconds |
| History metadata TTL | 24 hours |
| Maximum publications in one recovery | 256 |
| Recovery Cursor maximum lifetime | 120 seconds |
| Cursor checkpoint interval | 30 seconds |
| Connection token lifetime | 300 seconds |
| Subscription token lifetime | 300 seconds |
| Guaranteed short recovery window | 120 seconds at ≤2 sustained Device revisions/s |

Both `history_size` and `history_ttl` must be enabled. The release config must use dedicated Redis, not Legacy Redis. History loss is tolerated only because the owner Snapshot remains authoritative.

The queue limit is a memory-safety bound, not a delivery guarantee. A client that exceeds it is disconnected, reconnects and either recovers contiguously or reloads a Snapshot.

## Service-level objectives

All percentage objectives use a rolling 30-day window and exclude caller-invalid 4xx responses from availability calculations. Canary promotion additionally requires the phase-specific hold time and minimum sample counts.

### Snapshot HTTP

| Indicator | Objective |
|---|---:|
| Authorized Snapshot availability | 99.9% |
| Single Snapshot p95 | ≤250 ms |
| Single Snapshot p99 | ≤750 ms |
| 100-item batch p95 | ≤1,000 ms |
| 100-item batch p99 | ≤2,000 ms |
| Maximum age asserted as current | 30 seconds |

A Snapshot older than the current-state age bound cannot be silently returned as current. It is returned only with explicit unavailable/Last Known semantics or the request fails with a typed problem.

### Ingest and Presence evaluation

| Indicator | Objective |
|---|---:|
| Platform receipt-to-acceptance p95 | ≤2 s |
| Platform receipt-to-acceptance p99 | ≤5 s |
| Accepted Presence Signal-to-evaluation p95 | ≤3 s |
| Accepted Presence Signal-to-evaluation p99 | ≤10 s |
| Offline policy deadline lateness p95 | ≤15 s |
| Offline policy deadline lateness p99 | ≤60 s |

Source lag (`receivedAt - observedAt`) is a monitored data-quality dimension and is not hidden inside platform ingest latency.

### Publication

| Indicator | Objective |
|---|---:|
| Committed outbox-to-transport p95 | ≤1 s |
| Committed outbox-to-transport p99 | ≤3 s |
| Accepted observation-to-browser p95 | ≤2 s |
| Accepted observation-to-browser p99 | ≤5 s |
| Authorized publications visible within 5 s | ≥99.95% |
| Undetected Business Revision gaps | 0 |

A detected gap is a recovery event; an undetected gap is a release-blocking correctness failure.

### Recovery

| Indicator | Objective |
|---|---:|
| Eligible same-scope transport recoveries successful | ≥99% |
| Recovery or authoritative Snapshot fallback successful | ≥99.9% |
| Recovery-to-live p95 | ≤3 s |
| Recovery-to-live p99 | ≤10 s |
| Snapshot fallback p95 | ≤5 s |
| Snapshot fallback p99 | ≤15 s |

"Eligible" means same current authorization scope, Cursor inside its lifetime, same retained epoch and a fully retained transport gap. Expired or mismatched Cursor attempts do not reduce the eligible recovery numerator; they must still safely fall back.

### Authorization and revocation

| Indicator | Objective |
|---|---:|
| Subscribe decision p95 | ≤100 ms |
| Subscribe decision p99 | ≤300 ms |
| Revocation-to-effective unsubscribe p95 | ≤2 s |
| Revocation-to-effective unsubscribe p99 | ≤5 s |
| Maximum revocation propagation | 10 s |
| Publications after the maximum revocation bound | 0 |

Revocation is a security objective, not an ordinary availability error budget. Any later delivery after the maximum bound blocks promotion and triggers rollback.

### Slow consumers

- Warning: more than 0.1% of active connections disconnected as slow consumers within five minutes.
- Rollback trigger: more than 0.5% within five minutes.
- Healthy-client p99 publication latency may not degrade beyond the publication SLO while 1% of clients stop reading.

## Zero-tolerance security invariants

Every release report must contain the following integer counters and every value must be zero:

- cross-Organization successes;
- cross-Site successes;
- hidden Device disclosures;
- forged scope successes;
- unauthorized key disclosures;
- post-revocation publications;
- Cursor scope expansions;
- Cursor replay authorization bypasses;
- raw connection/subscription token log findings;
- raw Recovery Cursor log findings;
- raw channel log findings;
- telemetry value log findings;
- Legacy or Mock fallbacks;
- non-owner S2 business writes;
- undetected Business Revision gaps.

These counters must be produced by blocking assertions, not entered by an operator.

## Negative-security acceptance matrix

| Test | Required result |
|---|---|
| Cross-Organization single Snapshot | `404 RESOURCE_NOT_FOUND`, shape equal to a missing Device. |
| Cross-Site batch item | Batch stays ordered; item is `RESOURCE_NOT_FOUND`. |
| Forged browser identity/scope headers | Request rejected; no scope expansion and no header becomes trusted context. |
| Hidden Device in all-or-nothing bootstrap | `404 RESOURCE_NOT_FOUND`; no token or partial descriptor minted. |
| Client-selected forged channel | Subscribe proxy denies; channel name has no authority. |
| IAM revocation during live delivery | Server unsubscribe, denied resubscribe, no publication after 10 s maximum. |
| Cursor replay by another Principal | `400 RECOVERY_CURSOR_INVALID`; no resource detail. |
| Cursor replay for another Device/key set | `400 RECOVERY_CURSOR_INVALID`; Snapshot fallback only after normal authorization. |
| Unknown versus unauthorized key on visible Device | Same `400 TELEMETRY_KEY_INVALID` status/code/shape and generic detail. |
| Any key request against hidden Device | Device invisibility is evaluated first; `404 RESOURCE_NOT_FOUND`. |

Negative tests compare status, stable code, schema and bounded timing. They do not compare unstable human detail text.

## Metrics and alerting contract

The implementation must expose at least these platform metric families:

```text
s2_telemetry_ingest_acceptance_duration_seconds
s2_telemetry_source_lag_seconds
s2_telemetry_presence_evaluation_duration_seconds
s2_telemetry_offline_deadline_lateness_seconds
s2_telemetry_snapshot_request_duration_seconds
s2_telemetry_snapshot_age_seconds
s2_telemetry_publication_lag_seconds
s2_telemetry_publication_revision_gap_total
s2_telemetry_recovery_attempts_total
s2_telemetry_snapshot_fallback_total
s2_telemetry_subscribe_decision_duration_seconds
s2_telemetry_revocation_propagation_seconds
s2_telemetry_outbox_oldest_unpublished_timestamp_seconds
s2_telemetry_quarantine_candidates_total
s2_telemetry_upstream_errors_total
s2_telemetry_active_subscriptions
s2_telemetry_publication_bytes_total
```

Centrifugo metrics are consumed for active connections/subscriptions, recovery outcomes, proxy duration, broker actions, queue-driven disconnects and process pressure. The locked image's raw metric set is captured in every release report; documentation alone is not accepted as proof.

Allowed metric labels are bounded dimensions such as:

```text
service
operation
outcome
reason_family
source_type
transport
phase
cohort
schema_version
```

Forbidden metric labels include Principal, Organization, Site, Device, subscription, channel, Cursor, Business Revision, telemetry key/value and token. Metrics-cardinality evidence must list every family, label name, observed unique values and estimated series count.

Required alerts include:

- fast and slow Snapshot availability/error-budget burn;
- Snapshot latency and age;
- ingest/outbox/publication lag;
- Presence evaluation and offline-deadline lateness;
- revision gaps and Snapshot fallback spikes;
- recovery failure ratio;
- subscribe proxy latency/denial anomalies;
- revocation propagation breach;
- slow-consumer disconnect ratio;
- Redis memory, failover and broker errors;
- PostgreSQL pool pressure and owner unavailability;
- upstream observation outage;
- metric/trace exporter outage.

Every alert records severity, primary owner, secondary owner and runbook path. Security-zero alerts page immediately and do not wait for a burn-rate window.

## Trace, log and audit correlation

The correlation chain is:

```text
browser request
  requestId + traceId
Gateway admission and IAM delegation
  requestId + traceId + bounded HMAC references
Telemetry Runtime Snapshot/subscribe/checkpoint
  requestId + traceId + subscriptionRef + Business Revision
outbox publication
  traceId + eventId + subscriptionRef + previous/current Revision
Centrifugo API/transport
  traceId where supported + channelHash + transport outcome
client application/recovery evidence
  requestId/traceId + subscriptionRef + applied Revision + recovery outcome
audit ledger
  raw authorized platform IDs + action/result + trace/request IDs
```

Logs and spans may contain:

- trace/span/request IDs;
- operation, outcome and reason family;
- HMAC-derived `organizationRef`, `siteRef`, `deviceRef`, `subscriptionRef`;
- `channelHash` and `cursorFingerprint`;
- Business Revision numbers;
- recovery outcome, rollout phase and cohort.

They must not contain connection/subscription tokens, raw Cursor, raw channel, telemetry value, Authorization header, Session cookie, CSRF token or source credential.

The reference method is environment-keyed HMAC-SHA-256 truncated to 16 bytes. It supports local correlation without exposing raw IDs across telemetry backends. Key rotation changes references and is recorded as observability metadata.

S2 propagates W3C Trace Context. Tenant, Device, subscription, Cursor and telemetry data are not placed in OpenTelemetry Baggage.

Raw platform IDs are present only in the access-controlled Audit Ledger where the business audit contract requires them. Audit retention and access controls are separate from ordinary operational logs.

## Capacity and failure suite

The implementation ticket must provide one command that executes or orchestrates all scenarios and writes machine-readable reports.

### Steady state

Run the full envelope for 60 minutes. Pass only when all SLOs hold and:

- service CPU ≤60%;
- service memory ≤70%;
- Redis memory ≤65%;
- network utilization ≤60%;
- PostgreSQL pool utilization ≤70%;
- no process restart, OOM kill or unbounded queue growth occurs.

### Two-times short peak

Run 2× the declared envelope for 15 minutes. Passing does not require normal latency SLOs, but requires:

- zero security invariant failures;
- no business state corruption or non-owner write;
- no OOM, crash loop or unbounded queue;
- explicit overload responses or bounded slow-consumer disconnects;
- recovery/Snapshot fallback remains correct.

### Reconnect storm

Reconnect 10,000 clients at 1,000/s with 80% eligible for recovery and 20% forced to Snapshot fallback. The run must prove:

- subscribe proxy remains fail-closed;
- recovery and fallback objectives hold;
- PostgreSQL and IAM do not collapse under synchronized fallback;
- clients do not expose buffered publications before authoritative initialization.

### Slow consumers

Pause reads for 1% of clients under full publication load. Their queues must remain bounded and they must disconnect. Healthy clients must remain inside the publication SLO.

### Retention overflow and epoch reset

Expire 5% of cursors beyond history or reset the transport epoch. No partial suffix may be promoted as current. Every affected client reloads a Snapshot and reports a typed recovery outcome.

### N+1 and dependency failover

At 70% envelope:

- terminate one Centrifugo node;
- perform Redis failover or restart;
- perform PostgreSQL failover;
- interrupt IAM subscribe decisions;
- remove upstream observation coverage for 15 minutes.

Required outcomes:

- transport loss never changes business ownership;
- Redis loss may reduce recovery success but not Snapshot correctness;
- PostgreSQL loss is explicit `UNAVAILABLE`, never cache authority;
- IAM failure denies new/resubscriptions;
- upstream outage produces `UNAVAILABLE`, not false `OFFLINE`;
- committed outbox work is neither lost nor duplicated in business effect.

### Revocation storm

Apply 100 revocations/s for 60 seconds while publications continue. All affected subscriptions meet the revocation objective and have zero later delivery after the maximum bound.

## Shadow comparison

Shadow comparison is allowed only after dark ingest is stable.

It may read Legacy and S2 results and record a diff. It may not:

- write either system;
- publish data;
- create subscriptions;
- alter authorization;
- repair mappings;
- change route ownership;
- feed a comparison result back into current state.

Promotion requires:

- zero Registry mapping mismatch;
- zero missing/extra Device mismatch for the selected cohort;
- at least 99.9% agreement for overlapping accepted latest values under type-specific tolerances;
- at least 99.5% sampled timestamp agreement within one expected sample interval;
- every semantic difference classified, especially Legacy `active` versus S2 Presence/Availability semantics.

A classified expected semantic difference is not "fixed" by changing S2 to match Legacy.

## Rollout phases and single-writer rule

| Phase | S2 traffic | Minimum hold | Writer | Reader and rollback |
|---|---:|---:|---|---|
| R0 Contract only | 0% | none | No implementation | Legacy remains active. |
| R1 Dark ingest | 0% | 24 h | Telemetry Runtime writes only S2 schema | Legacy serves current routes. |
| R2 Shadow compare | 0% | 24 h | Telemetry Runtime writes only S2 schema | Legacy serves; comparison has no effects. |
| R3 Internal canary | 1% | 2 h and 10k Snapshot requests/1k subscriptions | Telemetry Runtime | Cohort route owner; rollback by route revision. |
| R4 External canary | 5% | 4 h and 50k requests/5k subscriptions/100 recovery attempts | Telemetry Runtime | Cohort route owner; no in-request fallback. |
| R5 Ramp | 25% | 8 h | Telemetry Runtime | Route-revision rollback only. |
| R6 Ramp | 50% | 12 h | Telemetry Runtime | Route-revision rollback only. |
| R7 Primary | 100% | 24 h | Telemetry Runtime | S2 serves; Legacy remains rollback target during compatibility window. |
| R8 Legacy current-state retired | 100% | 7 days before removal approval | Telemetry Runtime | No Legacy current-state fallback. Historical boundary remains separate. |

The phrase "single writer" means only Telemetry Runtime can create S2 Presence/latest/Snapshot/Revision/outbox state in every implemented phase. A Legacy system may continue writing its own isolated compatibility store until retirement, but there is no cross-write, reverse synchronization or shared Redis/database state.

## Promotion gates

A phase can advance only when:

- every zero-tolerance counter is zero;
- every required evidence file exists and validates;
- SLO burn rate for the phase is ≤1× budget;
- no unclassified shadow difference remains;
- public contract and ownership drift are zero;
- measured capacity headroom is at least 30%;
- the phase hold time and sample minimums are satisfied;
- primary and secondary owners approve promotion.

Promotion is manual. Automation may recommend or block; it does not self-promote production traffic.

## Rollback triggers

Immediate rollback:

- any security-zero invariant becomes non-zero;
- Business Revision corruption or non-owner write;
- wrong Device/key publication;
- token, Cursor, channel or telemetry-value leakage;
- Snapshot authority bypass;
- unclassified cross-tenant difference.

Rollback after a sustained breach:

- Snapshot 5xx >1% for five minutes;
- single Snapshot p99 >1.5 s for ten minutes;
- publication p99 >5 s for ten minutes;
- recovery-or-Snapshot failure >0.5% for five minutes;
- slow-consumer disconnect ratio >0.5% for five minutes;
- Redis memory >80%, service CPU >80% or capacity headroom <20% for ten minutes.

The on-call decision must begin within five minutes and the cohort route rollback must complete within fifteen minutes.

Rollback procedure:

1. Stop phase promotion and freeze the rollout registry revision.
2. Change the affected cohort route owner to the last accepted revision.
3. Disconnect or expire S2 live sessions.
4. Require a fresh authoritative Snapshot before any incremental state is presented.
5. Keep expand-only database migrations in place during the compatibility window; do not perform emergency down-migrations.
6. Verify zero S2 traffic for the rolled-back cohort and no new S2 publications reach it.
7. Preserve failed-version traces, metrics, audit and configuration hashes.
8. Publish the rollback report and obtain approval before retry.

## Required local, build and implementation commands

The planning baseline is reproducible with existing commands:

```bash
npm run s2:rollout-gates:check
npm run s2:ownership:check
npm run s2:public-contract:check
npm run s2:centrifugo:check
npm run contracts:check
npm run lint
npm run build
```

Issue #53 must assign implementation tickets that add these repeatable production and integration commands:

```bash
npm run build:telemetry-runtime
npm run build:telemetry-runtime-migrator
npm run s2:security-negative
npm run s2:postgres-integration
npm run s2:transport-integration
npm run s2:capacity
npm run audit:s2-browser
npm run audit:s2-kind-rollout
npm run s2:release-evidence
```

The formal clean-runner workflow is planned as:

```text
.github/workflows/s2-telemetry-release.yml
```

It must contain blocking jobs for contracts/static checks, security negatives, PostgreSQL integration, transport integration, capacity/failure injection, browser real mode, production images, Kind rollout/rollback and final release evidence. The workflow uploads one `s2-telemetry-release-evidence` artifact.

## Required evidence bundle

The implementation release bundle must contain at least:

```text
out/s2-release-evidence/workflow-jobs.json
out/s2-release-evidence/security-negative-report.json
out/s2-release-evidence/postgres-integration-report.json
out/s2-release-evidence/transport-integration-report.json
out/s2-release-evidence/capacity-report.json
out/s2-release-evidence/reconnect-storm-report.json
out/s2-release-evidence/slow-consumer-report.json
out/s2-release-evidence/revocation-report.json
out/s2-release-evidence/browser-report.json
out/s2-release-evidence/kind-rollout-report.json
out/s2-release-evidence/rollback-report.json
out/s2-release-evidence/metric-cardinality-report.json
out/s2-release-evidence/log-redaction-report.json
out/s2-release-evidence/trace-correlation-report.json
out/s2-release-evidence/shadow-comparison-report.json
out/s2-release-evidence/alert-rule-validation-report.json
out/s2-release-evidence/production-image-report.json
out/s2-release-evidence/sbom-provenance-report.json
out/s2-release-evidence/release-evidence.intoto.json
out/s2-release-evidence/SHA256SUMS
```

Every report records repository SHA, immutable image digests, release-envelope version, environment, start/end times, fixture seed, achieved load, SLO samples, zero-invariant counters and command exit status.

## Release checklist

- [ ] Immutable images and component versions are pinned.
- [ ] Ownership and public-contract gates pass.
- [ ] PostgreSQL migrations are expand-only and previous-version compatible.
- [ ] Dedicated Redis and Centrifugo N+1 behavior are proven.
- [ ] All negative security tests pass.
- [ ] Every zero-tolerance counter equals zero.
- [ ] Steady-state, 2×, reconnect, slow-consumer and failure scenarios pass.
- [ ] SLO recording and alert rules validate against fixtures.
- [ ] Metrics cardinality is inside the approved budget.
- [ ] Logs, traces and evidence bundle contain no forbidden data.
- [ ] HVAC Web real mode has no Mock or Legacy fallback.
- [ ] Shadow comparison has no side effects.
- [ ] Canary hold time and sample counts are satisfied.
- [ ] Rollback drill completes within fifteen minutes.
- [ ] Rollback and transport reset require a fresh Snapshot.
- [ ] Runbooks list primary and secondary owners.
- [ ] Evidence bundle hashes verify.
- [ ] Manual promotion approval is recorded.

## Definition of done for Issue #52

Issue #52 is complete when the machine-readable gate, this operations specification, source review, domain terms and static checker are merged. It does not claim the production chain has passed these gates; implementation tickets must create the commands and runtime evidence.

Issue #53 may copy the matrices, commands, evidence paths, phase boundaries and rollback criteria directly into ordered tracer-bullet implementation tickets without reopening SLO or capacity decisions.
