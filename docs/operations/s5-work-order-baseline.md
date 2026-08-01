# S5 Work Order contract-only baseline

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

## Explicit exclusions

P4 does not expose start, block, resume, complete, cancel, reopen, task, note, attachment, completion-evidence, or Alarm-link mutations. It does not infer Work Orders from Telemetry or browser state, change Alarm status, run FDD, execute Commands, or admit arbitrary JSON source payloads. Every unreviewed Work Order write route remains absent.

## Next slices

1. Governed lifecycle transitions with explicit legal graph, reason, version, idempotency, actor, policy, and completion-evidence requirements.
2. Ordered tasks, notes, attachment metadata, and explicit Alarm link/unlink as separately reviewed write slices.
