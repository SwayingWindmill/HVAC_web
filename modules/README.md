# Domain modules

`modules/` contains logical domain owners and the data assets that belong to those domains. A module is a source and authority boundary, not a statement that the domain runs as its own Phase 1 process or container.

Current migrated modules:

- `audit/` — Audit ledger, transactional inbox, query/server package and the explicit `audit-owner` owner-split entrypoint.
- `alarm/` — Alarm runtime, rule/lifecycle implementation, PostgreSQL migrations/testdata and the explicit `alarm-owner` owner-split entrypoint.
- `workorder/` — Work Order runtime/lifecycle implementation, PostgreSQL migrations/testdata and the explicit `work-order-owner` owner-split entrypoint.
- `registry/` — Tenant/Site, Space/Asset and Device/Product/Point registry ownership, private read/write boundary and the explicit `registry-owner` owner-split entrypoint.
- `iam/` — Principal/capability authorization, tenant/site policy, delegation and reconciliation ownership with the explicit `iam-owner` owner-split entrypoint.
- `command/` — Cloud command intent/governance/approval authority, durable command migrations, IoT dispatch/reported-state verification and MQTT execution packages, plus the explicit `command-owner` owner-split entrypoint. Phase 1 executes the IoT-side packages through `cmd/iot-service`.
- `telemetry/` — Telemetry ingest/current-state/history/query ownership, generated Telemetry API, bounded analytics query adapters, the `telemetry-query-owner` owner-split entrypoint and history projector. Default runtime executes through `cmd/telemetry-worker`.
- `metric/` — Metric version/binding/calculation/result/publication and durable Metric job execution. Default runtime executes through `cmd/metric-worker`.
- `energy/` — Energy Processing ownership: MeterBinding resolution, canonical Counter Delta to Energy Fact projection, rebuild/correction evidence and the explicit `energy-projector` build entrypoint. Default Phase 1 execution is composed into `cmd/telemetry-worker`.
- `scheduler/` — Durable schedule scanning, claim coordination and scheduler statistics. Default runtime executes through `cmd/scheduler`.
- `maintenance/` — Operational maintenance jobs such as certificate expiry scans, dead-job disposition and tenant retirement. Default runtime executes through `cmd/maintenance-worker`; `maintenance-admin` remains an explicit admin entrypoint inside the same module.
- `iot/` — MQTT ingress, connectivity/Edge Fleet state and IoT protocol execution used by the canonical `cmd/iot-service` composition root. Telemetry and Command authority remain in their own modules.

## Rules

- Default Phase 1 process topology remains defined by `deploy/platform/phase1/runtime-inventory.v1.json` and canonical root `cmd/` entrypoints.
- Domain code, domain persistence migrations and domain-owned test fixtures move together. Do not split one authority between `modules/<domain>` and a historical `services/*-service` directory.
- `modules/*/cmd/*-owner` exists only when the owner-split topology needs the same domain code as an explicit process for failure-domain verification. It is not an additional default deployable.
- Cross-domain consumers import a module's public package. They must not reach into another module's `internal/` packages.
- Shared technical/security primitives remain under `libs/`; do not turn `modules/` into a generic utility directory.
- RC-04 removes historical `services/*` module namespaces directly. Do not add forwarding modules, compatibility imports, duplicate migrations, or fallback execution paths.
