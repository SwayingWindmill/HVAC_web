# S5 Work Order contract-only baseline

## Authority boundary

Work Order is an independent authoritative domain. It may reference Alarm, Asset, Equipment, Investigation, manual, or external source identities, but those domains cannot publish or mutate Work Order facts. Telemetry and browser inference are not Work Order sources.

## P1 contract

P1 registers only two public GET routes for Site list and detail. Both routes remain at `S5-R0-contract-only`, carry 0% production traffic, use Gateway ingress, have no fallback owner, and are not discoverable through the Route Ownership Registry.

The read model preserves identity, Site scope, title, description, priority, status, one origin source, related sources, assignee or team, schedule, task counts, note and attachment counts, completion evidence, version, timestamps, and a convergent timeline.

## Persistence

`work_order_runtime` owns current Work Orders, source references, timeline events, tasks, notes, attachment metadata, and completion evidence. Every table has Organization-bound FORCE RLS. The P1 runtime role receives SELECT only; migration authority remains separate.

An origin source is unique per Work Order. Child facts use restricted foreign keys and cannot cascade-delete authoritative evidence.

## Explicit exclusions

P1 does not expose creation, assignment, scheduling, lifecycle, task, note, attachment, or Alarm-link mutations. It does not activate IAM or Gateway proxying, render a Real Web page, send notifications, change Alarm state, run FDD, or execute Commands.

## Next slices

1. Real PostgreSQL certification for role activation, RLS isolation, pagination, and projection convergence.
2. Exact IAM and Gateway list/detail authorization followed by a separately reviewed read canary.
3. Governed creation, assignment, scheduling, lifecycle, tasks, notes, attachment metadata, and explicit Alarm linking.
