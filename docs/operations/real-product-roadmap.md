# HVAC Web Real Product Roadmap

Status date: 2026-08-01

Tracking map: GitHub issue #166

## Destination

Deliver an authoritative, Site-scoped HVAC operations product in which every visible business fact comes from an owned backend domain or a declared read model. Real pages must never fill gaps with Demo data, browser inference, fabricated alarms, guessed savings, or synthetic operational health.

## Current delivery state

| Sequence | Product area | Status on 2026-08-01 | Current evidence |
| --- | --- | --- | --- |
| 1 | Real Assets | Merged and certified | PR #182 completed the authoritative Site asset workspace and 200-Device browser certification. |
| 2 | Real Dashboard | Merged | PR #165 delivered the authoritative default Site overview with full release gates. |
| 3 | Complete Energy | Merged | PR #167 delivered calendar workspaces, comparisons, drill-down, and protected query lifecycle. |
| 4 | Real Commands | Merged | PR #169 delivered authoritative Site scope, local-only governed Command evidence, and a production-disabled control boundary. |
| 5 | Alarm | P1–P3 merged; P4 implementing | PR #184 merged exact IAM/Gateway list and detail reads through a 1% internal no-fallback canary. Issue #187 adds the formal 1%-to-5% promotion evidence and rollback gate without changing repository traffic; every lifecycle POST remains disabled at 0%. |
| 6 | Work Order | P1 implementing | The S5 contract-only baseline establishes an independent owner, strict list/detail model, 0% no-fallback routes, and read-only FORCE RLS persistence. |
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

Delivered P1/P2 baseline:

- Alarm identity, source, severity, lifecycle, acknowledgement, suppression, assignment, and closure are server-owned.
- Every lifecycle write requires exact signed action and scope, CSRF, expected aggregate version, stable idempotency binding, and durable actor/policy/correlation evidence.
- Telemetry state alone is never displayed as an Alarm unless an Alarm owner publishes it.
- Duplicate and correlated occurrences preserve evidence and lifecycle history.
- Site list, detail, filtering, acknowledgement, assignment, suppression, closure, conflict recovery, and audit evidence are browser-certified locally.
- PostgreSQL certification proves FORCE RLS, explicit runtime-role activation, cross-Organization isolation, restricted column updates, atomic lifecycle persistence, and idempotent replay.

P3 read activation delivered:

- IAM owns `alarm:list` and `alarm:read` decisions with exact Organization/Site/Alarm scope, explicit-deny precedence, policy revision, request ID, trace ID, and durable allow/deny evidence.
- Platform Gateway derives scope from the authenticated Session, calls IAM, signs a short-lived Alarm read context, proxies bounded GET requests over mTLS, and revalidates response scope.
- List and detail routes use a stable 1% internal cohort with no fallback owner; a non-selected Session receives no Alarm route instead of another source of truth.
- Production Web renders only authorized list/detail reads. Local lifecycle mutations are isolated in a development-only lazy component and every public lifecycle POST remains at 0%.
- Browser certification proves public Gateway GET-only traffic, no local seam, no lifecycle controls, no Telemetry inference, Site cache purge, and generic capability denial.

P4 read promotion gate in implementation:

- Issue #187 defines the only adjacent promotion from `S4-R1-internal-read-only` at 1% to `S4-R2-site-canary` at 5%.
- Promotion requires a SHA-bound target-environment attestation, 24-hour hold, minimum cohort and volume, SLO compliance, zero security counters, rollback drill, checksums, in-toto evidence, and two distinct manual approvers.
- Certification itself never edits routing; an accepted bundle authorizes a separate reviewed registry commit, and rollback is a later registry revision affecting future reads only.
- Public lifecycle authorization and rollout, automated suppression expiry, notification delivery, correlation policy, and Work Order linkage remain later independent slices.

### 6. Work Order

Create the human-maintenance execution domain.

P1 contract-only baseline in implementation:

- `work-order-service` owns a strict Site-scoped list/detail contract while both public routes remain disabled at 0%.
- The read model requires one origin source, bounded related references, task/note/attachment summaries, completion evidence, version, timestamps, and a convergent timeline.
- `work_order_runtime` separates current state, sources, timeline, tasks, notes, attachment metadata, and completion evidence behind Organization FORCE RLS and a SELECT-only runtime role.
- Alarm, FDD, Investigation, manual, and external identities may be referenced; Telemetry and browser inference cannot publish Work Order facts.
- IAM/Gateway activation and every lifecycle mutation remain later independently certified slices.

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

Merge the Work Order P1 contract-only authority baseline while retaining both public Work Order reads at 0% and exposing no lifecycle routes. Then certify PostgreSQL role/RLS behavior before any IAM/Gateway read activation. Alarm read promotion and lifecycle rollout remain separately governed.
