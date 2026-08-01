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

PostgreSQL certification proves pinned roles, explicit role activation, FORCE RLS on all seven tables, cross-Organization isolation, SELECT-only runtime privilege, deterministic pagination, and malformed projection rejection. Both public routes remain `S5-R0-contract-only` at 0%, with no fallback and no lifecycle routes.

## Explicit exclusions

P2 does not expose creation, assignment, scheduling, lifecycle, task, note, attachment, or Alarm-link mutations. It does not activate IAM or public Gateway proxying, render a Real Web page, send notifications, change Alarm state, run FDD, or execute Commands.

## Next slices

1. Exact IAM and Gateway list/detail authorization followed by a separately reviewed read canary.
2. Governed creation, assignment, scheduling, lifecycle, tasks, notes, attachment metadata, and explicit Alarm linking.
