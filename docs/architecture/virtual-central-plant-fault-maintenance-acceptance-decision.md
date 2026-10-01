# Virtual Central Plant — First Fault-to-Maintenance Acceptance Scenario Decision

Status: WAYFINDER DECISION
Wayfinder map: `wayfinder: virtual central plant`
Decision ticket: `Decide first Virtual Plant fault-to-maintenance acceptance scenario`
Decided: 2026-08-28

## Decision

The first Virtual Central Plant fault-to-maintenance acceptance scenario is a chilled-water-pump actuator/VFD stuck-high disturbance that produces excessive chilled-water flow and a credible low-delta-T condition through the physical model.

The simulator owns only the physical disturbance. Telemetry, Alarm, FDD and Work Order facts remain owned by their production domains.

## Physical disturbance

Target device: CHWP-01.

Disturbance: the chilled-water-pump actuator is stuck at a high effective frequency even when the governed control target requests a lower value.

The plant must derive the downstream symptoms through physical behavior rather than inserting diagnostic outcomes:

```text
CHWP actuator stuck high
        ↓
actual pump frequency remains high
        ↓
chilled-water flow increases
        ↓
for the same cooling load, return-water temperature approaches supply-water temperature
        ↓
chilled-water delta-T decreases
```

The model must respect the central energy relationship `Q = m_dot × Cp × ΔT`. The simulator must not directly set `deltaT`, Alarm state, FDD status or Work Order state.

A simulator-private fault telemetry key is not part of the acceptance contract. A device fault code may appear only when the real/certified device protocol genuinely exposes that fact.

## FDD decision

Reuse the existing production rule:

- Finding type: `CHILLED_WATER_LOW_DELTA_T`
- Rule revision: `fdd-low-delta-t/v1`
- Minimum delta-T: 5 °C for this acceptance rule revision

Authoritative FDD evidence is the production Telemetry evidence for:

- `btu_meter.supply_water_temperature`
- `btu_meter.return_water_temperature`

The FDD owner computes `return - supply` and emits a Finding only when the rule is satisfied. The simulator does not publish the computed diagnosis.

## Alarm decision

The first Alarm deliberately represents a directly observable operating symptom rather than duplicating the FDD delta-T calculation.

Alarm type:

`CHILLED_WATER_LOW_RETURN_TEMPERATURE_UNDER_LOAD`

Raise condition for the acceptance policy revision:

```text
chiller.run_state == RUNNING
AND chiller.cooling_capacity >= 30% rated capacity
AND btu_meter.return_water_temperature <= 10.5 °C
for 5 minutes
```

Clear condition:

```text
btu_meter.return_water_temperature >= 11.5 °C
OR chiller.run_state != RUNNING
```

The thresholds belong to this explicit Alarm Policy Revision and are not hard-coded as universal product limits. A real Site may use a different policy revision.

The Alarm evaluator must consume production Telemetry-owned facts and evidence. It must not read simulator state directly.

## Minimum acceptance evidence

The scenario requires the following normal production Points:

### BTU Meter

- `supply_water_temperature`
- `return_water_temperature`
- `flow_rate`

### CHWP-01

- `frequency`
- `flow_rate`

### Chiller

- `cooling_capacity`
- `run_state`

These facts demonstrate that:

- the plant is under meaningful cooling load;
- actual pump frequency remains abnormally high;
- chilled-water flow increases;
- return-water temperature decreases relative to the operating condition;
- delta-T genuinely falls below the FDD threshold.

No acceptance-only business fields such as `simulatorFault`, `faultInjected`, `expectedDiagnosis` or `virtualDeltaT` may be added to the production API/frontend contract.

## Work Order provenance

The Work Order is operator-created from the authoritative Alarm Incident.

```text
sourceDomain = ALARM
sourceRef    = alarmId
```

There is no `SIMULATOR` or `FDD` Work Order source domain for this scenario.

The FDD Finding is linked through the formal owner relationship to the same Alarm and Work Order when those links are available. This preserves the distinction between diagnostic evidence and Work Order business provenance.

## Recovery semantics

The full acceptance lifecycle is:

```text
normal operation
    ↓
inject CHWP actuator stuck-high disturbance
    ↓
actual pump frequency / flow increase
    ↓
return-water temperature falls and delta-T decreases
    ↓
Alarm duration condition is satisfied
    ↓
Alarm Incident becomes ACTIVE
    +
FDD Low-Delta-T Finding is produced
    ↓
operator creates ALARM-origin Work Order
    ↓
FDD Finding is formally associated with Alarm / Work Order
    ↓
remove the physical disturbance
    ↓
frequency / flow / temperature recover through plant dynamics
    ↓
Alarm condition becomes CLEARED
    ↓
subsequent FDD evaluation returns CLEAR
```

Recovery of the physical condition does not delete historical facts:

- the FDD Finding remains historical diagnostic evidence;
- the Alarm Incident remains durable with condition state `CLEARED`;
- the Work Order does not automatically complete.

Work Order completion remains an operator/maintenance workflow decision after review of recovery evidence.

The simulator never invokes Alarm clear, Finding resolution or Work Order completion commands. It only changes the physical disturbance.

## Implementation consequence

The implementation tracer must exercise one production vertical slice:

```text
Virtual Plant physical disturbance
    → DeviceAdapter / Process Image
    → MQTT / IoT
    → Telemetry Runtime
    → Alarm evaluator
    → FDD evaluator
    → existing frontend evidence presentation
    → operator-created ALARM-origin Work Order
    → physical recovery
    → Alarm/FDD recovery evidence
```

Missing production bridges identified by the Wayfinder research—Telemetry→Alarm runtime evaluation, Telemetry/History→FDD evaluation, authoritative Work Order source validation and reverse associations—must be implemented at their respective owners rather than bypassed by simulator-specific calls.
