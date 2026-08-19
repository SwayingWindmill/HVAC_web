# ADR 0012 — OpenEMS-informed HVAC Edge Control Plane

Status: accepted, terminology superseded in part by ADR 0013

Date: 2026-08-17

ADR 0013 supersedes Organization/Area/Equipment/Sensor wording with Tenant/Space/Asset/optional PhysicalSensor. The IPO Cycle, Process Image, Controller/Scheduler/Arbiter, Capability Profile, Driver/Bridge, Edge Timedata and source-first implementation decisions remain accepted.

## Context

The existing Phase 1 architecture treated Edge primarily as an OT gateway with local collection, store-and-forward, Cloud failure isolation and replay. That is sufficient for telemetry transport and basic remote commands, but not for a production-grade commercial HVAC control plane.

OpenEMS provides mature architectural mechanisms for on-site EMS control: Input-Process-Output cycles, immutable Process Images, Channel value/nextValue buffering, Controller/Scheduler ordering, device-independent Natures, reusable protocol Bridges, local Timedata/resend policies and simulators that reuse production Controllers/Schedulers.

A project-level adjudication compared 50 architecture concerns without giving incumbency preference to either OpenEMS or HVAC Web. The machine-readable result is `contracts/architecture/edge-control-plane.v1.json`; the detailed reasoning is `docs/architecture/openems-architecture-adjudication.md`.

## Decision

### 1. Introduce a first-class HVAC Edge Control Plane

The target system has two complementary control planes:

```text
Cloud Control Plane
  Tenant / IAM / Registry / Analytics / Alarm / Work Order
  Command Governance / Approval / Audit / Forecast / Optimization
                     │
                     │ MQTT TLS
                     ▼
HVAC Edge Control Plane
  Component Registry / Capability Profiles / Channels
  Process Image / Cycle / Controllers / Scheduler / Arbiter
  Safety / Interlocks / Drivers / Protocol Bridges
  Edge Manifest / Edge Timedata / Store & Forward
                     │
                     ▼
                OT Equipment
```

The Edge Control Plane owns fast local closed-loop execution. Cloud owns durable business governance, authorization, fleet policy, long-horizon intent and analytics.

### 2. Adopt IPO Cycle and Process Image semantics

Edge device communication remains asynchronous. Each governed Channel has a latest incoming value (`nextValue`) and a cycle-visible value (`value`). Only the cycle boundary promotes incoming values into the Process Image. All Controllers in one cycle read the same immutable snapshot.

Default Phase 1 control cycle is 1 second unless a device/capability explicitly requires a different soft-real-time cycle.

### 3. Hard real-time safety remains outside the Edge software runtime

The Edge Control Plane is soft-real-time. PLC/device protection, emergency trip, fire/disaster control and other hard-real-time safety functions remain in dedicated hardware/firmware and cannot be replaced by the Edge runtime.

### 4. Introduce Controller, Scheduler and Control Arbiter

Controllers execute in deterministic priority order. Higher-priority Controllers may establish constraints that later Controllers cannot violate. Cloud and manual commands enter this system as intents rather than bypassing it.

Typical order:

```text
Safety
→ Equipment Protection / Interlock
→ Minimum operating constraints
→ Equipment Controller
→ Cluster / Plant Controller
→ Optimization / Schedule
→ Cloud Command Intent
→ Manual Intent
```

The exact priority policy is explicit configuration, not implicit call order.

### 5. Keep durable Cloud Command governance, change execution semantics

Cloud retains Command identity, IAM authorization, approval, idempotency, audit, dispatch tracking and durable outcome.

A dispatched Command becomes a leased Edge intent. The Edge Arbiter may constrain or reject it. Target Command evidence will distinguish at least:

```text
requestedValue
effectiveValue
constraintReason
winningController
controlCycle
intentExpiresAt
```

Stale remote intents expire and relinquish control to local policy.

### 6. Keep Point as canonical identity and add Edge Channel as runtime object

`Point` remains the durable platform data-point identity and authority boundary.

`Channel` is introduced as the live Edge runtime object carrying value, type, engineering unit, quality, access mode and transport/persistence priorities.

Every governed Channel maps to one canonical Point. Channel address is not a replacement platform identity.

### 7. Introduce Capability Profiles as the controller/driver contract

Capability Profile is the HVAC equivalent of OpenEMS Nature. It declares required/optional Channels, supported commands and limits. Controllers depend on Capability Profiles, never on vendor/product names.

Initial profiles include:

- `VARIABLE_SPEED_PUMP`
- `CHILLER`
- `COOLING_TOWER`
- `ELECTRICITY_METER`
- `WEATHER_STATION`

### 8. Introduce Device Drivers and Protocol Bridges

Vendor/product details belong in Device Drivers. Shared connection lifecycle, retries, pooling, protocol scheduling and serialization belong in reusable Protocol Bridges.

Initial Bridges:

- Modbus TCP
- Modbus RTU
- MQTT

BACnet and OPC UA use the same Bridge abstraction when implemented.

### 9. Introduce Edge Manifest and reconciliation

The Edge runtime exposes a self-describing Edge Manifest containing configured components, drivers, capabilities, channels, controllers, limits and protocol bindings.

Cloud Registry remains master-data authority. Edge Manifest is reconciled with Registry; it does not replace Tenant/Site/Equipment/Device/Sensor/Point lifecycle governance.

### 10. Upgrade Store & Forward into Edge Timedata

The existing persistent MQTT queue remains useful but is not sufficient as the complete local data model. Edge Timedata owns local latest/history, aggregation metadata and resend cursors. Persistence, aggregation and resend priorities are explicit Channel/Point policies.

### 11. Simulator and real devices share production interfaces

Simulation replaces Driver/Protocol/physical behavior, not Controller/Scheduler/Command/Telemetry business logic.

The simulator target includes reacting physical behavior, ramp rates, startup/shutdown delays, minimum run/stop times, faults, stale data, sensor noise, communication timeout, stuck actuators and rejected writes.

### 12. Introduce Single and Cluster Controller separation

Single-equipment Controllers own equipment state machines and local sequences. Cluster/Plant Controllers allocate load or operating modes across multiple Single Controllers.

Examples:

- `ChillerPlantController`
- `ChilledWaterPumpClusterController`
- `CoolingWaterPumpClusterController`
- `CoolingTowerClusterController`

### 13. Keep the existing Cloud domains where they are stronger

The following remain target Cloud authorities after explicit comparison:

- Tenant/Site/Role/Policy IAM and PostgreSQL RLS;
- Area/Equipment/Device/Sensor/Point lifecycle and historical bindings;
- S2 + ClickHouse + PostgreSQL Cloud telemetry/analytics split;
- Alarm, Work Order and durable Audit domains;
- MQTT TLS as canonical Edge-Cloud transport.

### 14. Do not make OpenEMS Java/OSGi/Backend/UI a target dependency

The architectural mechanisms are adopted because they are stronger. The OpenEMS runtime technology is not adopted by default. A bounded A/B PoC may compare direct OpenEMS Edge with an equivalent HVAC Edge implementation, but implementation language/runtime must be justified independently.

### 15. Source-first implementation is mandatory

The OpenEMS reference baseline for this ADR is pinned to official release `2026.7.0`, commit `2e2792d`. `develop` may be consulted for later changes, but it does not silently change this architecture baseline.

For every `OPENEMS` decision and every `MERGE` decision whose Edge behavior materially depends on OpenEMS, implementation or refactoring must follow this order:

1. read the relevant official OpenEMS source at the pinned baseline;
2. read the relevant upstream tests and official documentation;
3. record observed runtime semantics and lifecycle behavior in `docs/architecture/openems-source-review.md`;
4. classify each relevant mechanism as `ADOPT`, `ADAPT`, or `REJECT` for HVAC Web;
5. only then implement or refactor the local code;
6. add local behavior tests that exercise the adopted/adapted semantics.

This requirement is retroactive. Edge modules that were implemented before their source review are `UNVERIFIED`, regardless of whether local tests pass. Existing HVAC Web code receives no incumbency preference. When a material conflict is found, the pinned OpenEMS behavior is the default outcome; retaining a conflicting local behavior requires an explicit, evidence-backed justification showing that the difference is required by HVAC domain constraints or is measurably safer, simpler, or more maintainable. Without such justification, the local module must be refactored to the reference behavior.

Architecture prose or diagrams alone are not sufficient evidence for implementation. Source review is behavioral, not a license to copy upstream source verbatim. Project-specific mechanisms that are demonstrably stronger—such as durable Cloud Command governance or fail-closed HVAC safety behavior—remain only when that difference is explicitly documented and reviewed.

## Consequences

- Existing `IoT Runtime` and `Control` architecture conformance becomes `PARTIAL` until the Edge foundation exists.
- EG8200 evolves from a publisher/command executor into, or is replaced by, a first-class HVAC Edge Control Plane runtime.
- `iot-service` remains a Cloud-side IoT integration process; it is not the Edge Controller runtime.
- Cloud Command APIs remain stable at the governance boundary, but downstream execution gains local intent arbitration and lease semantics.
- New real-device work should start at Capability Profile + Driver + Bridge, not vendor-specific end-to-end service code.
- Phase 1 production-grade controllable Edge cannot be claimed solely because MQTT telemetry and command round-trips work.

## Required Phase 1 Edge foundation

Before claiming the Edge control architecture complete, implement:

1. Channel Runtime;
2. Process Image;
3. Cycle;
4. Controller Contract;
5. Scheduler / Control Arbiter;
6. Capability Profile Registry;
7. Device Driver abstraction;
8. Protocol Bridge abstraction;
9. Local Safety / Interlock Controllers;
10. leased Cloud Command Intent adapter;
11. Edge Manifest;
12. Edge Timedata + priority resend;
13. real/simulated Driver parity.

## References

- `contracts/architecture/edge-control-plane.v1.json`
- `docs/architecture/openems-architecture-adjudication.md`
- OpenEMS Edge Architecture: <https://openems.github.io/openems.io/openems/latest/edge/architecture.html>
- OpenEMS Configuration: <https://openems.github.io/openems.io/openems/latest/edge/configuration.html>
- OpenEMS Device implementation: <https://openems.github.io/openems.io/openems/latest/edge/implement.html>
- OpenEMS Simulation: <https://openems.github.io/openems.io/openems/latest/simulation/realtime.html>
