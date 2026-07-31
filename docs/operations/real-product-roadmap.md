# HVAC Web Real Product Roadmap

Status date: 2026-07-31

Tracking map: GitHub issue #166

## Destination

Deliver an authoritative, Site-scoped HVAC operations product in which every visible business fact comes from an owned backend domain or a declared read model. Real pages must never fill gaps with Demo data, browser inference, fabricated alarms, guessed savings, or synthetic operational health.

## Current delivery state

| Sequence | Product area | Status on 2026-07-31 | Current evidence |
| --- | --- | --- | --- |
| 1 | Real Assets | In progress | Site list, device detail, capability, history, and trend pull requests are active. |
| 2 | Real Dashboard | In review | PR #165 provides the first authoritative Site overview. |
| 3 | Complete Energy | Merged | PR #167 delivered calendar workspaces, comparisons, drill-down, and protected query lifecycle. |
| 4 | Real Commands | In review | PR #169 adds authoritative Site scope and a local-only controlled workbench while production routes remain disabled. |
| 5 | Alarm | Planned | No authoritative Alarm domain may be inferred from telemetry state. |
| 6 | Work Order | Planned | Must be a separate durable domain linked to, but not collapsed into, Alarm. |
| 7 | FDD | Planned | Requires governed evidence from Assets, Telemetry, Energy, Alarm, and Work Order. |
| 8 | Optimization | Planned | Requires FDD and Energy evidence plus explicit safety and approval boundaries. |
| 9 | Cost & Carbon | Planned | Requires stable Energy intervals and versioned tariff and emission-factor authority. |
| 10 | Real BigScreen | Planned | Final read-only aggregation of already authoritative Real modules. |

## Ordered route

### 1. Real Assets

Complete the Site operating list, URL-addressable device detail, exact current state, short history, critical-point trends, realtime recovery, and 200-device browser certification.

Exit criteria:

- Registry owns Site, Equipment, Device, and binding identity.
- Telemetry owns current value, presence, freshness, quality, history, and realtime recovery.
- Capability denial and Site invisibility remain non-leaking.
- Missing, stale, unknown, unavailable, offline, and valid zero remain distinct.
- Real browser certification proves bounded behavior at the supported Site size.

### 2. Real Dashboard

Provide an authoritative overview using only completed read domains. Dashboard cards are projections, not new sources of truth.

Exit criteria:

- Site entry defaults to the Dashboard.
- Device coverage, online state, attention state, and recent Energy are traceable to their owners.
- Partial coverage, source failure, stale data, and unsupported domains are visible.
- Alarm, Work Order, FDD, and Optimization are not fabricated before those domains exist.

### 3. Complete Energy

Turn the initial energy trend into a reproducible analysis workspace.

Scope:

- Calendar-aligned day, week, month, and year workspaces in the Registry Site timezone.
- URL-persisted period, anchor date, and quality policy.
- Current-period versus previous-period comparison without converting missing data to zero.
- Year-to-month and month-or-week-to-day drill-down.
- Returned-bucket evidence table, watermarks, quality summary, and dataset revisions.
- Protected query cancellation and removal on Site, Session, or policy-scope loss.

Exit criteria:

- A copied URL reconstructs the same analysis context.
- DST and Site-timezone boundaries are tested.
- Current and comparison periods are contiguous calendar periods.
- Partial, stale, suspect, empty, failed-baseline, and zero-baseline states are explicit.
- Browser evidence proves Site scope, authority boundary, and absence of sensitive URL fields.

Deferred from this stage:

- Tariffs, monetary savings, carbon factors, weather normalization, forecasting, and device-level allocation without an authoritative allocation model.

### 4. Real Commands

Expose governed command intent and execution evidence, not direct browser-to-device control.

Exit criteria:

- Commands are limited to server-advertised targets, properties, ranges, and policies.
- Intent, approval, dispatch attempt, connector receipt, verification, timeout, rejection, and failure are distinct.
- Idempotency, reason capture, optimistic concurrency, and audit linkage are mandatory.
- Site transition and Session loss purge unsent protected drafts.
- High-risk actions remain unavailable until their approval and rollback contracts exist.

### 5. Alarm

Create an authoritative Alarm domain for durable operational exceptions.

Exit criteria:

- Alarm identity, source, severity, lifecycle, acknowledgement, suppression, assignment, and closure are server-owned.
- Telemetry state alone is never displayed as an Alarm unless an Alarm owner publishes it.
- Duplicate and correlated occurrences preserve evidence and lifecycle history.
- Site list, detail, filtering, acknowledgement, and audit evidence are browser-certified.

### 6. Work Order

Create the human-maintenance execution domain.

Exit criteria:

- Work Order identity, status, priority, assignee, due date, tasks, notes, attachments, and completion evidence are durable.
- Alarm-to-Work-Order linkage is explicit and many-to-many capable where required.
- Creation, assignment, scheduling, execution, verification, cancellation, and reopen transitions are governed.
- Work Orders remain meaningful when their originating Alarm is later cleared.

### 7. FDD

Deliver fault detection and diagnostics as governed findings with evidence.

Exit criteria:

- Every Finding records rule or model version, evaluation window, input evidence, confidence, and quality blockers.
- Detection, diagnosis, recommendation, dismissal, confirmation, and recurrence remain distinct.
- Missing or suspect evidence blocks or qualifies conclusions instead of being silently imputed.
- Findings link to Assets, Energy intervals, Alarms, and Work Orders without taking ownership of them.

### 8. Optimization

Provide bounded recommendations and approved optimization actions.

Exit criteria:

- Baseline, objective, constraints, candidate action, expected impact, uncertainty, and verification plan are explicit.
- Recommendations do not become Commands without human or policy approval.
- Comfort, equipment safety, command limits, and rollback constraints are enforced server-side.
- Claimed impact is verified against authoritative post-action Energy and operating evidence.

### 9. Cost & Carbon

Add financial and environmental interpretation after Energy is stable.

Exit criteria:

- Tariffs and emission factors have owner, geography, effective interval, currency or unit, revision, and provenance.
- Cost and carbon calculations reference exact Energy dataset revisions and factor revisions.
- Missing tariff periods, demand charges, tiering, taxes, and uncertain factors are visible.
- Avoided cost and avoided carbon are not shown without a declared baseline and methodology.

### 10. Real BigScreen

Deliver the final read-only operating display.

Exit criteria:

- BigScreen consumes the same authorized read models and state semantics as standard Real pages.
- No BigScreen-only business truth or Demo fallback exists.
- Layout handles unavailable modules, partial data, stale watermarks, and reduced motion.
- Site identity, last update, data authority, and degraded state remain visible at display distance.

## Cross-cutting release gates

Every module must satisfy all of these before it is called Real:

1. **Authority:** every business field names or traces to its owning service or read model.
2. **Scope:** Organization and Site come from authenticated Principal and validated Registry context, never browser authority headers.
3. **Capability:** navigation and direct URL behavior agree; denial is generic and non-leaking.
4. **State semantics:** loading, empty, partial, stale, suspect, unavailable, forbidden, unknown, and valid zero stay distinct.
5. **Protected lifecycle:** old requests are aborted and protected cache, subscriptions, and drafts are purged on Site, Session, logout, revocation, or material policy change.
6. **No Demo contamination:** the Real dependency graph and bundle exclude Mock and Demo business modules.
7. **Evidence:** unit, contract, integration, browser, accessibility, build-graph, and bundle gates produce reviewable evidence.
8. **Performance:** reads are bounded, pagination is explicit, and supported Site-size certification is recorded.
9. **Auditability:** commands and human or automated state transitions preserve actor, reason, correlation, result, and revision evidence.
10. **Honest incompleteness:** an unavailable domain is shown as unavailable or not integrated, never approximated by another domain.

## Parallel tracks

The Operations Agent and Platform/System Management are parallel programs rather than steps inside this product sequence. They may advance concurrently, but they must consume the same authoritative contracts and cannot bypass the release gates above.

## Immediate next action

Finish the first Real Commands slice: authoritative Organization/Site projections, exact scope rejection, local-only submit and approval evidence, and protected lifecycle certification. Production Command routes remain disabled at 0% until a separate activation and formal certification decision.
