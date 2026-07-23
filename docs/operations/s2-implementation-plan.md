# S2 implementation ticket index and delivery order

Status: accepted planning handoff

Date: 2026-07-23

Source map: #46

Planning ticket: #53

Machine-readable authority: `deploy/s2/implementation-plan.v1.json`

## Purpose

The S2 specification is decision-complete. This document hands implementation to short-lived, independently reviewable tracer-bullet tickets without reopening the accepted Presence/Freshness semantics, Telemetry Runtime ownership, public Snapshot/recovery contract or Release Envelope.

The implementation graph is published as GitHub sub-issues #60–#71 under #53 with native blocked-by relationships. The tracker is the live work queue; this document is the repository-owned architectural index and close-condition lock.

## Delivery rules

1. One ticket uses one short-lived branch and one merge to `main`.
2. Every ticket leaves runnable, machine-readable evidence that demonstrates its complete narrow behavior.
3. No long-lived integration branch and no multi-service big-bang merge are allowed.
4. Contract/schema changes use expand-contract compatibility before traffic cutover.
5. `telemetry-runtime-service` remains the only S2 business writer.
6. Gateway, Legacy, ThingsBoard, Redis, Centrifugo, browser caches and shadow comparison never become current-state authority.
7. No request-level fallback to Legacy, ThingsBoard or Mock is permitted.
8. Any transport reset, failed recovery, Business Revision gap or route rollback returns the client to a fresh authoritative Snapshot.
9. A ticket may add an ADR only when it discovers a genuine conflict or needs to change an accepted decision. It may not silently reinterpret ADR 0002, ADR 0003, ADR 0004 or Release Envelope v1.
10. Production traffic is forbidden until the explicit canary/cutover ticket.

## Dependency graph

```text
#60 Contract / ownership / PostgreSQL baseline
  └─ #61 IAM exact Device/key authorization and revocation
       └─ #62 Telemetry Runtime authoritative Snapshot
            ├─ #63 Trusted ingest / ThingsBoard / quarantine / outbox
            └─ #64 Gateway Snapshot and batch routes
                 └──────────────┐
#61 + #63 + #64 ────────────────┴─ #65 Realtime backend
                                      ├─ #66 TelemetryLiveClient adapter
                                      └─ #67 Shadow comparison / cohort routing
#64 + #66 + #67 ──────────────────── #68 HVAC Web real telemetry
#63 + #64 + #65 + #66 + #67 + #68 ─ #69 Security / observability / redaction
#69 ───────────────────────────────── #70 Capacity / failure / release evidence
#70 ───────────────────────────────── #71 Canary / cutover / Legacy retirement
```

The only initial frontier is #60.

Two intentional parallel windows exist:

- after #62, #63 and #64 may proceed independently;
- after #65, #66 and #67 may proceed independently.

No other edge is optional. In particular, realtime cannot precede trusted outbox state and public authorization, and HVAC Web cannot precede both the live adapter and cohort routing.

## Ordered implementation tickets

| Seq | Ticket | Primary code owner | Blocked by | Complete vertical evidence |
|---:|---|---|---|---|
| 01 | [S2 Ticket 01: 激活 Telemetry contract、ownership 与 PostgreSQL baseline](https://github.com/SwayingWindmill/HVAC_web/issues/60) | Platform architecture / contracts / data foundations | None | Generated clients compile; expand-only PostgreSQL migration and role isolation pass; ownership and clean-CI drift reports pass. |
| 02 | [S2 Ticket 02: IAM 精确 Device/key 授权与撤权](https://github.com/SwayingWindmill/HVAC_web/issues/61) | IAM service | #60 | Exact Device/key decisions, short-lived delegation, negative authorization, revocation and redaction reports pass. |
| 03 | [S2 Ticket 03: Telemetry Runtime 权威 Snapshot 纵向切片](https://github.com/SwayingWindmill/HVAC_web/issues/62) | Telemetry Runtime service | #60, #61 | Internal single/batch Snapshot, PostgreSQL transaction/Revision, semantics and two-Organization isolation pass. |
| 04 | [S2 Ticket 04: ThingsBoard ingest、reconciliation、quarantine 与 outbox](https://github.com/SwayingWindmill/HVAC_web/issues/63) | Telemetry integrations + Telemetry Runtime | #62 | Source acceptance through Snapshot/outbox, dedupe/order, quarantine, reconciliation and failure recovery pass. |
| 05 | [S2 Ticket 05: Gateway Snapshot 与 batch 公共路由](https://github.com/SwayingWindmill/HVAC_web/issues/64) | Platform Gateway + contract generation | #61, #62 | Generated public routes, Session/CSRF, IAM/owner integration, nondiscoverability and no-fallback browser evidence pass. |
| 06 | [S2 Ticket 06: Centrifugo 实时发布、订阅与恢复后端](https://github.com/SwayingWindmill/HVAC_web/issues/65) | Telemetry Runtime + realtime platform | #61, #63, #64 | Outbox-to-publication, subscribe authorization, recovery/fallback, slow consumer, revocation and secret-absence pass. |
| 07 | [S2 Ticket 07: TelemetryLiveClient 与浏览器恢复适配器](https://github.com/SwayingWindmill/HVAC_web/issues/66) | HVAC Web platform client | #64, #65 | Deep-module dependency lock and deterministic browser recovery/revocation state machine pass. |
| 08 | [S2 Ticket 08: Legacy shadow comparison 与 cohort routing](https://github.com/SwayingWindmill/HVAC_web/issues/67) | Platform Gateway + migration/compatibility | #63, #64, #65 | Dark-ingest ownership, side-effect-free diff, deterministic cohort and route rollback pass. |
| 09 | [S2 Ticket 09: HVAC Web real Presence 与 latest telemetry](https://github.com/SwayingWindmill/HVAC_web/issues/68) | HVAC Web feature team | #64, #66, #67 | Production build, real-mode network audit, browser state rendering and revoked-cache purge pass. |
| 10 | [S2 Ticket 10: 安全负向、可观测性与脱敏门禁](https://github.com/SwayingWindmill/HVAC_web/issues/69) | Platform security + observability | #63–#68 as recorded in the tracker | Security-zero, metric cardinality, trace correlation, redaction, alert and exporter-outage reports pass. |
| 11 | [S2 Ticket 11: 容量、故障注入与 release evidence](https://github.com/SwayingWindmill/HVAC_web/issues/70) | Release engineering + SRE | #69 | Clean workflow, production images, SBOM/provenance, Release Envelope capacity/failure, Kind rollback and hashed evidence pass. |
| 12 | [S2 Ticket 12: Canary、100% cutover 与 Legacy current-state retirement](https://github.com/SwayingWindmill/HVAC_web/issues/71) | S2 release owner + Gateway/HVAC Web/Legacy owners | #70 | R1–R8 reports, approvals, rollback drill, seven-day zero-Legacy-current-state observation and final completion attestation pass. |

The issue bodies are authoritative for acceptance criteria and explicit out-of-scope boundaries. This index deliberately keeps only enough detail to navigate and review the graph.

## Ticket execution protocol

For each ticket:

1. Claim the issue before work; skip an assigned ticket.
2. Confirm every native blocker is closed.
3. Start from current `main` on a short-lived branch named for the ticket.
4. Read the linked decisions and the preceding ticket evidence rather than reconstructing semantics from code.
5. Add failing tests or an executable harness before or alongside implementation where practical.
6. Keep the PR inside the issue's declared behavior and explicit out-of-scope list.
7. Run the ticket-specific command plus all affected platform contract, ownership, security and build gates.
8. Publish machine-readable evidence paths in the resolution comment.
9. Merge one green PR with `Closes #<ticket>`; delete the branch and sync `main`.
10. Do not begin a newly unblocked ticket in the same Wayfinder/implementation session.

A ticket is not complete because code exists. It is complete only when the runnable evidence named in its issue passes and the code owner can roll the change back without violating the previous accepted state.

## ADR and contract policy

The implementation must consume the accepted decisions:

- ADR 0002: Presence, Freshness, Quality, Availability and Display State;
- ADR 0003: Telemetry Runtime ownership, PostgreSQL authority and service boundaries;
- ADR 0004: public Snapshot, batch, bootstrap, checkpoint, delta and recovery contract;
- `deploy/s2/release-gates.v1.json`: SLO, capacity, transport bounds, rollout and rollback.

A new ADR is required before any ticket changes:

- the single business owner or transaction boundary;
- Presence/Freshness/Quality semantics;
- Business Revision, Source Position, Transport Position or Recovery Cursor meaning;
- public operation set, DTO meaning, error code or recovery algorithm;
- Centrifugo/Redis responsibility;
- Release Envelope, SLO, zero-security invariant, promotion or rollback threshold;
- Legacy historical compatibility boundary.

An implementation convenience is not sufficient reason to change a decision.

## Evidence ownership by ticket

Evidence is cumulative but not deferred:

- #60 owns contract/schema/ownership baseline evidence.
- #61 owns IAM authorization and revocation evidence.
- #62 owns owner transaction, Snapshot and semantic evidence.
- #63 owns real source acceptance and outbox evidence.
- #64 owns public HTTP and browser Session evidence.
- #65 owns backend live transport/recovery/revocation evidence.
- #66 owns browser live state-machine evidence.
- #67 owns migration comparison/cohort/rollback evidence.
- #68 owns product real-mode browser evidence.
- #69 owns cross-chain security, observability and redaction evidence.
- #70 owns formal capacity, build, rollout-mechanics and release bundle evidence.
- #71 owns actual phase promotion, cutover, retirement and S2 completion evidence.

A later ticket may aggregate earlier evidence but cannot replace a failed earlier acceptance criterion with a final end-to-end test.

## S2 specification map close conditions

The specification map may close when all of the following are true:

- decision issues #47–#53 are closed;
- implementation issues #60–#71 exist as #53 sub-issues;
- all 25 native blocked-by edges match the machine plan;
- #60 is the only initial frontier;
- `npm run s2:implementation-plan:check` passes;
- the map has no remaining Not yet specified fog;
- planned contracts, ownership and release gates remain unactivated until #60 begins implementation.

Closing the map means the route is clear and implementation can start. It does not mean S2 is production-ready.

## Conditions before entering the next slice

No later business slice may rely on S2 until:

- #60–#71 are closed with accepted evidence;
- #71 publishes a passing S2 completion attestation;
- active contract and ownership state match ADR 0002–0004;
- every S2 security-zero invariant equals zero;
- Release Envelope v1 is certified, or a newly reviewed version replaces it;
- Legacy latest, batch and current-state WebSocket have zero production traffic and are retired;
- historical timeseries remains an explicit separate compatibility boundary;
- no known limitation defers tenant security, business correctness, Audit or rollback.

## Risks that must be zero

The final completion report must prove zero:

- ambiguous Snapshot or Business Revision ownership;
- non-owner S2 business writes;
- unauthenticated source ingest affecting S2;
- Registry Lifecycle used as Presence;
- ThingsBoard, Legacy cache or Redis used as current authority;
- Transport Position or Recovery Cursor used as business authority;
- subscription or Cursor use without current authorization;
- post-revocation publication;
- cross-Organization/Site/Device/key disclosure;
- request-level Legacy, ThingsBoard or Mock fallback;
- undetected Business Revision gap;
- raw token, Cursor, channel or telemetry-value leakage;
- unclassified shadow differences;
- missing capacity or rollback evidence;
- remaining Legacy current-state production traffic.

## Planning verification

```bash
npm run s2:implementation-plan:check
npm run s2:rollout-gates:check
npm run s2:ownership:check
npm run s2:public-contract:check
npm run s2:centrifugo:check
```

These commands validate the handoff and decision consistency. They do not execute the future implementation or claim runtime evidence exists.
