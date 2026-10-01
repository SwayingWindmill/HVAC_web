# Virtual Central Plant CHWP stuck-high source review

Status: VERIFIED for GitHub Issue #334
Date: 2026-08-28

## Scope

Issue #334 adds the first physical acceptance disturbance to the reacting Plant: CHWP-01 VFD actuator stuck-high. A governed lower frequency target must remain accepted while actual CHWP frequency stays physically high; higher flow and lower chilled-water delta-T must then emerge from Plant physics and recover dynamically after the disturbance is removed.

This change does not add Alarm, FDD, Work Order, expected-diagnosis, synthetic-delta-T, protocol, or simulator-specific business fields.

## OpenEMS

Pinned checkpoint: OpenEMS commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6` (2026-08-26), the #331 implementation checkpoint.

Official source/tests re-read immediately before implementation:

- `io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java`
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`
- existing implementation-time Review 005 in `docs/architecture/openems-source-review.md`

Observed behavior:

- reacting simulated equipment accepts normal production control inputs and changes physical state through the same equipment-facing contract;
- elapsed time is explicit and controlled, and tests advance time before asserting the resulting physical state;
- a control input is not treated as proof that the requested physical result has already happened;
- simulator physical state remains below higher-level telemetry/business ownership.

Decision:

- **ADOPT** production control target versus later physical-readback causality;
- **ADOPT** controlled elapsed-time progression in disturbance onset and recovery tests;
- **ADAPT** the reacting-equipment pattern to CHWP actuator effectiveness: while the dedicated stuck-high disturbance is active and CHWP is requested to run, the physical VFD output is driven toward the model's nominal high frequency even when the accepted governed setpoint is lower;
- **ADAPT** the existing HVAC water-side relation `Q ≈ 1.163 × flow(m³/h) × ΔT(°C)` so increased actual pump speed raises flow and reduces delta-T naturally at the same cooling load;
- **REJECT** OpenEMS OSGi/component/factory machinery and ESS-specific state equations.

## ThingsBoard CE

Pinned baseline: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Relevant implementation-time source evidence remains `docs/architecture/thingsboard-source-review.md`, especially S06 Telemetry Current authority (`BaseTimeseriesService.java`, `SqlLatestInsertTsRepository.java`, and `SqlTimeseriesLatestDaoTest.java`). Those sources establish authoritative observed-state handling but do not provide a competing physical reacting-equipment disturbance model.

Decision:

- **ADAPT** the established authority boundary: ThingsBoard-style telemetry/latest state may observe the resulting device values, but it does not author the stuck-high physical condition;
- **REJECT** representing the disturbance as a telemetry-business flag, synthetic diagnostic value, or direct Alarm/FDD outcome.

## MyEMS

Pinned baseline: MyEMS `v6.7.0`, commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

The reviewed official source/module split in `docs/architecture/myems-source-review.md` separates Modbus acquisition from central cleaning/normalization/aggregation and other business processing. It does not provide a stronger reacting-equipment disturbance model for this seam.

Decision:

- **ADAPT** the acquisition/business-processing separation: the physical disturbance stays below MQTT/Telemetry/Energy/FDD owners and only changes device observations;
- **REJECT** injecting low-delta-T, diagnosis, Alarm, FDD, or maintenance facts from simulator state.

## Canonical observation check

The existing released central-plant Point contract already contains the production evidence required by #334:

- BTU meter: `supplyWaterTemperatureC`, `returnWaterTemperatureC`, `flowRateM3h`;
- CHWP: `frequencyHz`, `flowRateM3h`;
- Chiller: `coolingCapacityKw`, `runState`.

The implementation therefore adds no acceptance-only Point and no disturbance field to the Plant snapshot. Focused tests verify both the physical onset/recovery behavior and that every required observation is backed by an existing non-command canonical Point.

## Local consequence

The smallest owner-correct implementation is one CHWP-specific disturbance state in `Plant`, not a generic fault framework and not a Scenario schema change. `SET_FREQUENCY` continues to accept/store the governed target. When the disturbance is active, the physical CHWP actual frequency reacts toward 50 Hz; flow follows the existing pump affinity relation, and BTU delta-T follows the existing water-side energy balance. Removing the disturbance restores the original governed target through the normal pump time constant.
