# Virtual Central Plant reacting-physics source review

Status: VERIFIED for GitHub Issue #333
Date: 2026-08-28

## Scope

Issue #333 deepens only the reacting central-plant physical model after #332 canonicalized Scenario ownership. It does not add the #334 stuck-high disturbance, a protocol implementation, a new business owner, or a generic simulator framework.

The implementation must satisfy the #331/#333 contract that `coolingLoadKw` is absolute plant demand, water-side temperatures/flow/cooling capacity remain physically related, equipment changes over controlled time, and governed writes become later physical readback rather than an immediate mutation of the current Process Image.

## OpenEMS

Pinned implementation checkpoint: OpenEMS commit `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6` from 2026-08-26, as fixed by #331 for the Virtual Central Plant implementation review.

Official source/tests re-read immediately before implementation:

- `io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java`
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`
- `io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/SimulatorDatasource.java`

Observed behavior:

- reacting equipment implements the normal production equipment/control interfaces rather than a separate business-control path;
- a control input changes physical state through the reacting component, and elapsed time is taken from an injected/controlled clock;
- the upstream reacting test applies production control inputs, advances the test clock with explicit time leaps, and verifies subsequent state rather than asserting an instantaneous simulator-only mutation;
- exogenous datasource values remain a separate concern from reacting equipment state.

Decision for #333:

- **ADOPT** controlled elapsed-time evolution as the basis for reacting physical state;
- **ADOPT** command-target versus subsequent physical-readback causality;
- **ADOPT** production DeviceAdapter/Channel/Controller boundaries already established by the earlier OpenEMS review;
- **ADAPT** OpenEMS's reacting-equipment pattern to one concrete HVAC multi-equipment `Plant` rather than ESS state-of-charge equations;
- **ADAPT** the #331 HVAC energy relationship to chilled water using `Q ≈ 1.163 × flow(m³/h) × ΔT(°C)`, so BTU-meter flow, delta-T and cooling capacity describe the same physical state;
- **ADAPT** pump affinity behavior, cooling-tower approach and chiller capacity to fixed v1 time responses inside the Plant model, without adding configuration/plugin abstractions before another use case requires them;
- **REJECT** OpenEMS OSGi/component factories, ESS-specific storage equations and framework infrastructure.

The earlier architectural evidence remains in `docs/architecture/openems-source-review.md`, Review 005; this record captures the implementation-time re-check required by `AGENTS.md`.

## ThingsBoard CE

Pinned project baseline remains ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

No ThingsBoard reacting-equipment mechanism is adopted in #333. The relevant established ThingsBoard boundary is still Telemetry/Current ownership recorded in `docs/architecture/thingsboard-source-review.md`; #333 remains below that boundary and emits device observations only through the existing Edge/MQTT path.

Decision:

- **ADAPT** the established platform authority boundary: the physical simulator may produce device-level observations but does not author Telemetry business truth;
- **REJECT** using generic telemetry generation, browser fixtures or platform business state as a substitute for reacting plant physics.

## MyEMS

Pinned project baseline remains MyEMS `v6.7.0`, commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

The applicable MyEMS source review separates acquisition from central historical/energy processing; it does not provide a reacting central-plant equipment model that should replace the OpenEMS-informed Plant seam. See `docs/architecture/myems-source-review.md`.

Decision:

- **ADAPT** acquisition/processing separation: simulator physical state stays below MQTT/Telemetry/Energy owners;
- **REJECT** turning simulator calculations into canonical Energy, Alarm, FDD or other downstream business facts.

## Local consequence

The #333 implementation therefore keeps one concrete Plant and deepens it only where the current acceptance requires it: pump and cooling-tower commands set targets while actual speed ramps with elapsed time; chiller cooling capacity follows absolute `coolingLoadKw` subject to existing availability/load-limit constraints; chilled-water supply/return temperatures are derived from actual cooling capacity and flow; cooling-tower water temperature reacts to wet-bulb, fan state and heat rejection; and tests use explicit controlled time to prove load response, command/readback delay and recovery.
