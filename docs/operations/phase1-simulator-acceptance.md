# Phase 1 Simulator Acceptance

## Purpose

This acceptance profile is the software-and-simulator gate used before real field meters are available. It implements the Phase 1 subset of the test and acceptance design without claiming hardware or formal endurance evidence that has not been executed.

The source design is `智慧能源系统测试与验收体系设计`.

## Status model

Every mapped requirement ends in one of these states:

- `PASS` — an automated software/simulator gate produced passing evidence.
- `FAIL` — a required software gate failed or its required evidence is missing.
- `DEFERRED_HARDWARE` — requires a real meter, protocol documentation, hardware lab, or field comparison.
- `DEFERRED_FORMAL` — requires a formal staging time window, wall-clock load attestation, Linux browser/UAT environment, or production acceptance process.

A deferred requirement is never rewritten as `PASS`.

## Commands

Static RTM and asset validation:

```bash
npm run acceptance:phase1:check
```

Database backup/destroy/restore drill:

```bash
npm run acceptance:phase1:restore
```

Full simulator acceptance:

```bash
npm run acceptance:phase1
```

A single gate can be executed while developing the suite:

```bash
node scripts/run-phase1-simulator-acceptance.mjs --profile=simulator --gate=GATE-MQTT-E2E
```

A single-gate run is reported as `partial` and is not Simulator Acceptance evidence.

## Evidence

The main evidence bundle is written under:

```text
out/phase1-acceptance/
```

Key files:

```text
traceability-static.json
restore-report.json
acceptance-report.json
acceptance-summary.md
```

The acceptance report also references evidence produced by existing domain gates, including the Phase 1 data-architecture baseline, MQTT/S2 integration, S2 PostgreSQL, Analytics/ClickHouse/Cube, Command certification, and S2 capacity preflight.

`GATE-DATA-ARCHITECTURE` runs `npm run data:phase1:check`. It prevents regression of the implemented Asset/Device/Point/Time Series/Energy Topology/Metric Definition foundation and requires Command, Alarm, and Work Order tenant-scoped persistence to retain both `tenant_id` and Organization→Tenant binding/RLS invariants. This gate does not mark the entire data architecture complete; remaining `PARTIAL`/`MISSING` items stay visible in `contracts/data/phase1-data-architecture.v1.json`.

## Hard rules

The Simulator profile is eligible only when every `SOFTWARE_REQUIRED` requirement passes. The following remain zero-tolerance:

```text
P0 defects
P1 defects
critical security issues
critical safety issues
unauthorized control successes
duplicate command side effects
cross-scope telemetry successes
restore failures
```

This runner proves automated gate status; defect counters not represented by an automated domain gate must still be supplied by the release/PRR process before production approval.

## Edge failure evidence

`GATE-MQTT-E2E` is a runtime failure-injection gate, not only a connectivity check. It proves the current central-plant Point set over a real Mosquitto broker and additionally executes:

```text
Broker stop
→ Edge readiness becomes unavailable
→ S2 stops receiving new MQTT observations
→ persistent .packet queue grows
→ EG8200 Publisher process is stopped
→ Broker returns
→ Publisher process restarts with the same queue/state directory
→ queued observations are delivered
→ queue returns to zero
→ persisted per-Point sequence continues monotonically
```

Point count is derived from the checked-in central-plant configuration rather than hard-coded in the gate. The test still requires exactly seven expected Device identities.

The sequence state is persisted before new measurements are admitted to the MQTT queue. A crash may therefore leave a sequence gap, but a restarted Publisher must not reuse an earlier Source Position offset.

## Capacity limitation

`GATE-CAPACITY-PREFLIGHT` intentionally invokes the existing S2 configuration preflight. Its evidence explicitly states that it is not a measured 60-minute steady-state or 15-minute 2x peak run.

Therefore Simulator Acceptance must not be presented as proof of:

```text
24h / 72h endurance
10k / 20k values/s Meter acceptance
formal reconnect storm capacity
production hardware capacity
```

Those remain `DEFERRED_FORMAL` until an approved wall-clock attestation exists.

## Hardware limitation

Until a real meter and vendor protocol material are available, these remain `DEFERRED_HARDWARE`:

```text
physical Modbus connection
register address/data type/endian/scale certification
field value/unit/direction comparison
real device disconnect/reconnect behavior
hardware lab and small-site rollout acceptance
```

The simulator path is designed so these items can later be added without changing the Requirement -> Gate -> Evidence model.

## Go / No-Go interpretation

`simulatorAcceptanceEligible=true` means the current software and simulator scope has passed its automated blocking gates.

It does **not** mean production go-live is approved. Production still requires hardware acceptance, formal performance/endurance, browser/UAT where applicable, defect review, and PRR approval.
