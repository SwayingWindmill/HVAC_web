# ADR 0022 — Role assignments are the only authorization facts

Status: accepted

Date: 2026-10-14

## Decision

A Principal's access in a Tenant comes from exactly one kind of fact: a **Role Assignment** (`iam.role_bindings`) of a Role (`iam.role_templates`) to the whole Tenant or to one Site. Every IAM decision reads it: Registry, Telemetry, Alarm, Work Order, Command and Analytics. Explicit denies stay as the only subtraction.

- Four Built-in Roles (`viewer`, `operator`, `site-manager`, `admin`) are global rows (`tenant_id IS NULL`) shipped by migration. Tenants cannot edit them. Tenant-owned role templates remain, and today only Workload Principals use them.
- A Site-scoped assignment never grants a Tenant-scoped capability. The `admin` role can only be assigned to the whole Tenant.
- Telemetry is authorized per Site. Device and key scope bindings are gone.
- Commands are authorized by risk ceiling per Site: `command.submit.<risk>` and `command.approve.<risk>`. The decision's maximum risk is the highest ceiling granted on that Site, and the device must belong to it.
- A work order may be assigned to a person who holds `work-order:start` on that Site. Team targets still come from `iam.work_order_ownership_targets`.

The per-domain tables are dropped: `site_bindings`, `telemetry_scope_bindings`, `telemetry_key_bindings`, `alarm_permissions`, `work_order_permissions` and `command_permissions`.

Reference comparison: `docs/architecture/iam-rbac-source-review-2026-10-14.md`. Spec: `docs/specs/iam-roles-and-users.md`.

## Considered Options

- **Keep the per-domain tables and have the admin UI write all of them.** Rejected. One grant would still be six writes, adding a device would still need grants for everyone, and the evaluators would keep six slightly different rule sets.
- **Project role assignments into the per-domain tables.** Rejected. It keeps two sources of truth and needs a projector that can lag behind a revocation.
- **An external authorization engine (OpenFGA, Casbin).** Rejected. See the source review: a two-level Tenant → Site scope does not justify another service or policy language.

## Consequences

- Granting access to a new device or point needs no IAM change.
- Per-device command restrictions and per-key telemetry restrictions are no longer expressible. If a real case needs them, add a narrower scope to the assignment rather than reintroducing per-domain tables.
- Seed and acceptance scripts grant access only through role assignments.
- Telemetry revocation facts now come only from membership, role assignment and grant changes.
