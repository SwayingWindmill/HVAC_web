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
| 5 | Alarm | P1–P4 merged | PR #188 added the formal read-promotion evidence gate without changing the exact 1% internal no-fallback read canary; every lifecycle POST remains disabled at 0%. |
| 6 | Work Order | P1–P4 merged; P5 certifying | PR #217 delivered governed create and assignment. Issue #220 adds only the fixed plan/start/block/resume/complete/cancel/reopen graph under a third independent 1% internal cohort, with exact idempotency, optimistic concurrency, typed completion evidence, and all collaboration/link writes absent. |
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

P1 contract-only baseline delivered:

- `work-order-service` owns a strict Site-scoped list/detail contract while both public routes remain disabled at 0%.
- The read model requires one origin source, bounded related references, task/note/attachment summaries, completion evidence, version, timestamps, and a convergent timeline.
- `work_order_runtime` separates current state, sources, timeline, tasks, notes, attachment metadata, and completion evidence behind Organization FORCE RLS and a SELECT-only runtime role.
- Alarm, FDD, Investigation, manual, and external identities may be referenced; Telemetry and browser inference cannot publish Work Order facts.
- IAM/Gateway activation and every lifecycle mutation remain later independently certified slices.

P2 authoritative PostgreSQL reads delivered:

- Work Order Service activates a SELECT-only runtime role inside read-only transactions and binds every query to Organization FORCE RLS.
- Internal Site list/detail GETs require an exact short-lived signed Gateway context; forged headers, wrong action, scope expansion, stale context, and cross-Site access fail closed.
- Status, priority, assignee, and opaque cursor filters are deterministic and bounded.
- Sources, timeline, task/note/attachment counts, and completion evidence must reconstruct one valid projection; malformed stored state is unavailable rather than partially rendered.
- Public Work Order routes remained disabled at 0% through P2, with no fallback and no lifecycle routes.

P3 exact IAM/Gateway read canary delivered:

- IAM owns exact `work-order:list` and `work-order:read` decisions and durable allow/deny audit evidence.
- Gateway derives scope from the authenticated Session and route, signs a short-lived Work Order read context, proxies bounded GETs, and rejects cross-scope responses.
- Both public GET routes use one stable 1% internal cohort with no fallback or shadow; non-selected Sessions receive route absence.
- Browser and Gateway evidence prove GET-only public traffic, stable selection, authorization denial without retained data, cross-Site nondiscovery, Session-loss purge, and zero lifecycle writes.

P4 governed creation and assignment merged:

- IAM authorizes exact create or assignment scope and validates every requested Principal or Team target against Site-scoped declared ownership facts; explicit deny wins.
- Gateway exposes only collection create and `:assign`, requires CSRF and idempotency, signs exact short-lived write context, and rejects browser authority headers.
- Work Order Service owns ID, initial state, timestamps, version, actor, and timeline; assignment preserves status and increments version once.

P5 governed lifecycle graph in certification:

- IAM authorizes exact plan, start, block, resume, complete, cancel, or reopen action scope; explicit deny wins.
- Gateway exposes exactly seven action POST routes, binds Origin CSRF and hashed idempotency-key scope into the server-to-server write context, and rejects downstream transition drift.
- Work Order Service enforces one fixed graph: plan remains `OPEN`; start enters `IN_PROGRESS`; block/resume toggle execution; complete requires typed evidence and converged tasks; cancel closes non-terminal work; reopen returns terminal work to `OPEN` without deleting evidence.
- The isolated writer commits current status or plan, timeline, completion evidence with completion version, idempotency snapshot, and mutation audit atomically under FORCE RLS.
- Lifecycle routes use a separate stable 1% no-fallback/no-shadow cohort. Read and create/assign cohorts remain independently governed.
- Browser certification covers the legal graph, exact replay, illegal and stale transitions, missing evidence, denial cleanup, cross-Site and Session boundaries, and collaboration-route absence.
- A separate mutation login activates a least-privilege writer role. Projection, source, timeline, idempotency, and audit evidence commit atomically under FORCE RLS.
- Both writes share one stable 1% no-fallback/no-shadow cohort. Browser evidence proves exact retry, stale conflict, denial cleanup, cross-Site nondiscovery, Session purge, and absence of every other lifecycle route.

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

Complete and merge Work Order P5 governed lifecycle while keeping tasks, notes, attachments, collaboration, notifications, SLA automation, title/source/priority/assignment changes, and Alarm link/unlink absent. The next Work Order slice must be separately reviewed; Alarm lifecycle rollout remains independently governed.
