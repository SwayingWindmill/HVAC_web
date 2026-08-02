# S5 Work Order governed authority baseline

## Authority boundary

Work Order is an independent authoritative domain. It may reference Alarm, Asset, Equipment, Investigation, manual, or external source identities, but those domains cannot publish or mutate Work Order facts. Telemetry and browser inference are not Work Order sources.

## P1 contract

P1 registers only two public GET routes for Site list and detail. Both routes remain at `S5-R0-contract-only`, carry 0% production traffic, use Gateway ingress, have no fallback owner, and are not discoverable through the Route Ownership Registry.

The read model preserves identity, Site scope, title, description, priority, status, one origin source, related sources, assignee or team, schedule, task counts, note and attachment counts, completion evidence, version, timestamps, and a convergent timeline.

## Persistence

`work_order_runtime` owns current Work Orders, source references, timeline events, tasks, notes, attachment metadata, and completion evidence. Every table has Organization-bound FORCE RLS. The P1 runtime role receives SELECT only; migration authority remains separate.

An origin source is unique per Work Order. Child facts use restricted foreign keys and cannot cascade-delete authoritative evidence.

## P2 authoritative PostgreSQL reads

Work Order Service exposes internal-only Site list and detail GET handlers backed by `work_order_runtime`. The Store requires the `s5_work_order_service` login, explicitly activates the NOLOGIN `s5_work_order_runtime` role, starts read-only transactions, and binds every query to the exact Organization through the FORCE RLS session setting.

List reads support bounded status, priority, assignee, and opaque cursor filters with deterministic `updated_at DESC, work_order_id ASC` ordering. Detail reads require exact Organization, Site, and Work Order identity. Stored sources, timeline, tasks, notes, attachments, and completion evidence are reconstructed and validated as one projection; inconsistent summaries fail closed.

The internal HTTP boundary accepts only a short-lived signed Gateway context with one exact `work-order:list` or `work-order:read` action and exact Organization, Site, and Work Order scopes. Forged headers, expanded scopes, wrong actions, stale contexts, cross-Site reads, and cross-scope Store responses are rejected before authority data is returned.

P2 PostgreSQL certification proved pinned roles, explicit role activation, FORCE RLS on all seven tables, cross-Organization isolation, SELECT-only runtime privilege, deterministic pagination, and malformed projection rejection. Both public routes remained `S5-R0-contract-only` at 0% through P2, with no fallback and no lifecycle routes.

## P3 exact IAM and Gateway read canary

IAM owns `work-order:list` and `work-order:read` decisions. List authorization is exact to Organization and Site; detail authorization is exact to Organization, Site, and Work Order. Explicit deny wins, and every allow or deny preserves principal, policy revision, request ID, trace ID, scope, reason, and occurrence time in durable PostgreSQL audit evidence.

Platform Gateway derives Organization from the authenticated Session and Site or Work Order identity from the route. It rejects caller identity and authority headers, calls IAM, signs a short-lived `X-Work-Order-Read-Context`, proxies only bounded GET reads over the workload-authenticated backend client, and validates every returned projection against the requested scope and filter.

The two public GET routes advance together to `S5-R1-internal-read-only` with one stable 1% Organization/principal cohort, no fallback owner, no shadow owner, and no alternate source. A non-selected Session receives route absence rather than a Work Order from another domain. Browser certification proves stable selection, authorization denial without retained data, public Gateway GET-only traffic, cross-Site nondiscovery, Session-loss purge, and zero lifecycle writes.

## P4 governed creation and assignment

IAM owns exact `work-order:create` and `work-order:assign` decisions. Create is exact to Organization and Site; assignment is exact to Organization, Site, and Work Order. Requested Principal and Team ownership targets are part of the authorization tuple, must be explicitly declared for that Site, and remain subject to explicit-deny precedence. Clear or unassigned semantics are represented by the absence of both targets rather than an invented identity.

Platform Gateway accepts only the reviewed create collection POST and assignment action POST. It derives Organization, Site, Principal, and Work Order scope from the authenticated Session and route, requires Origin-bound CSRF and a bounded `Idempotency-Key`, rejects browser authority headers, signs a short-lived `X-Work-Order-Write-Context`, and validates the returned projection against the requested identity, ownership, and version change.

Work Order Service owns Work Order ID, initial `OPEN` status, timestamps, actor, version, and append-only timeline evidence. Create accepts exactly one typed authoritative origin source, bounded text and priority, an optional bounded due window, and optional declared assignee or team ownership. Assignment changes only assignee and team, preserves lifecycle status, requires the expected version, and increments version exactly once.

The existing `s5_work_order_service` login and `s5_work_order_runtime` role remain SELECT-only. Mutations use a separate `s5_work_order_mutation_service` login that explicitly activates `s5_work_order_writer`; writer UPDATE privilege is restricted to assignee, team, version, and updated time. Current projection, source, timeline, idempotency record, and mutation audit commit atomically under Organization FORCE RLS. Exact retries return the original committed response after restart, while key reuse with another payload and stale or future versions fail closed.

The two mutation routes use one stable `S5-R1-internal-create-assign` 1% Organization/principal cohort with no fallback and no shadow owner. Browser certification proves authorized create and assignment, exact replay, stale-version conflict, authorization denial without retained data, non-selected route absence, cross-Site nondiscovery, Session-loss purge, public Gateway-only POST traffic, and absence of unreviewed lifecycle routes.

## P5 governed lifecycle graph

IAM owns exact `work-order:plan`, `work-order:start`, `work-order:block`, `work-order:resume`, `work-order:complete`, `work-order:cancel`, and `work-order:reopen` decisions. Every action is exact to Organization, Site, Principal, Work Order, and the hashed idempotency-key scope; explicit deny wins and no browser-supplied identity or authority is accepted.

The fixed graph is `OPEN -> OPEN` for plan, `OPEN -> IN_PROGRESS` for start, `IN_PROGRESS -> BLOCKED` for block, `BLOCKED -> IN_PROGRESS` for resume, `IN_PROGRESS -> COMPLETED` for complete, `OPEN | IN_PROGRESS | BLOCKED -> CANCELLED` for cancel, and `COMPLETED | CANCELLED -> OPEN` for reopen. Start requires an assignee or team. Plan replaces only the bounded future schedule window. Complete requires non-empty typed evidence plus a task summary with zero incomplete or blocked tasks. Reopen never deletes prior completion evidence.

Platform Gateway exposes exactly those seven POST action routes under a separate stable 1% lifecycle cohort. It derives all scope from Session and route, requires Origin-bound CSRF and a bounded `Idempotency-Key`, includes the shared `key:<sha256>` scope in the short-lived write context, performs an internal write-context-protected precondition read, forwards only the fixed action, and rejects downstream Organization, Site, Work Order, title, description, priority, source, ownership, task, note, attachment, schedule, evidence, status, version, or timeline drift.

Work Order Service applies one server-owned transition per successful request, increments version exactly once, appends exactly one timeline event, and preserves title, description, priority, source, ownership, task, note, and attachment projections. Illegal graphs, stale versions, future or invalid schedules, missing evidence, duplicate evidence, cross-Site access, and malformed requests make no state change.

Lifecycle persistence uses the isolated `s5_work_order_mutation_service` login and `s5_work_order_writer` role. Current status or plan, timeline, newly appended completion evidence, idempotency snapshot, and mutation audit commit in one FORCE RLS transaction. Completion evidence records the completing Work Order version internally. Exact retries return the original committed snapshot after restart; payload reuse and concurrent stale versions fail closed.

The seven lifecycle routes share `S5-R1-internal-lifecycle`, cohort `s5-work-order-lifecycle-v1`, and salt `s5-work-order-lifecycle-canary-v1`, with no fallback and no shadow owner. Browser certification proves the legal graph, exact retry, illegal transition, stale version, missing evidence, authorization cleanup, non-selected route absence, cross-Site nondiscovery, Session-loss purge, public Gateway-only POST traffic, and absence of collaboration routes.

## P6 governed ordered task checklist

IAM owns exact `work-order:task:list`, `work-order:task:append`, `work-order:task:status`, and `work-order:task:reorder` decisions. List, append, and reorder bind Organization, Site, Principal, and Work Order; status additionally binds the exact Task identity. Explicit deny wins, and durable decision evidence records the Task identity when one is present.

Platform Gateway exposes only the reviewed collection GET/POST, `tasks/{taskId}:status` POST, and `tasks:reorder` POST routes. It derives all authority from Session and route, requires Origin-bound CSRF and a bounded `Idempotency-Key` for writes, signs exact read or write context, and validates the complete returned checklist, summary, Work Order version, Task version, requested status, and exact reordered identity sequence.

Work Order Service owns UUIDv7 Task identity, append-only title, zero-based position, `OPEN | BLOCKED | COMPLETED` state, Task version, timestamps, and Work Order task-summary convergence. Task writes are allowed only while the Work Order is `OPEN`, `IN_PROGRESS`, or `BLOCKED`. Status changes require both expected Work Order and Task versions. Reorder accepts exactly one full changed permutation: duplicates, omissions, additions, invalid identities, and no-op orderings fail closed.

Append, status, and reorder share one durable `TASK` idempotency domain. An exact retry returns the original committed checklist after restart. Reuse across task actions or with another payload returns `IDEMPOTENCY_CONFLICT`. Task rows, Work Order summary and version, timeline evidence, idempotency snapshot, and mutation audit commit in one FORCE RLS transaction.

The writer role receives INSERT on `work_order_task` and UPDATE only for position, status, version, and updated time. It receives no DELETE privilege and no title-update privilege. Reorder uses a collision-free intermediate position range inside the transaction before applying the exact requested order.

The four task operations use `S5-R1-internal-task-checklist`, cohort `s5-work-order-task-v1`, and salt `s5-work-order-task-canary-v1` at 1%, with no fallback and no shadow owner. Browser certification proves ordered list and append, exact snapshot replay, dual-version conflict, unified task idempotency, exact full permutation, summary convergence, authorization cleanup, non-selected route absence, cross-Site nondiscovery, Session-loss purge, public Gateway-only traffic, and absence of delete and title-edit routes.

## Explicit exclusions

P6 does not expose Task deletion or title editing, note, attachment, collaboration, notification, escalation, SLA automation, Alarm link/unlink, Work Order title, description, priority, source, assignment, or Site changes through task endpoints. It does not infer Work Orders from Telemetry or browser state, change Alarm status, run FDD, execute Commands, or admit arbitrary JSON evidence. Historical timeline, Task history, and completion evidence cannot be purged or rewritten.

## Next slices

1. Notes and attachment metadata as separate append-only write slices with their own authority, versioning, idempotency, and evidence rules.
2. Explicit Alarm link/unlink, Task title editing or deletion, collaboration, notifications, and SLA automation only through separately reviewed slices.
