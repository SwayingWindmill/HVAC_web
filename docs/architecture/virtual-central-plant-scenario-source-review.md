# Virtual Central Plant Scenario source review

Status: VERIFIED for GitHub Issue #332
Date: 2026-08-28

This record ties the #332 implementation to the pinned upstream source reviews required by `AGENTS.md`. The Scenario contract itself is fixed by #327: explicit `STATIC` / `SCENARIO`, exact inputs `ambientDryBulbC`, `ambientWetBulbC`, `coolingLoadKw`, stepwise progression from offset `0s`, no interpolation/looping, and no `loadFraction` compatibility alias.

## OpenEMS

Pinned baseline: release `2026.7.0`, commit `2e2792d`.

Official source/tests reviewed for the simulator seam:

- `io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/SimulatorDatasource.java`
- `io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/AbstractDatasource.java`
- `io.openems.edge.simulator/src/io/openems/edge/simulator/meter/grid/acting/SimulatorGridMeterActingImpl.java`
- `io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java`
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`

The detailed source evidence is already recorded in `docs/architecture/openems-source-review.md`, Review 005.

Decision for #332:

- **ADOPT** separation between exogenous datasource inputs and reacting equipment state.
- **ADOPT** scenario/cycle progression as an input concern rather than equipment-owned business state.
- **ADOPT** production control/readback boundaries for simulated equipment instead of a simulator-only business API.
- **ADAPT** the datasource seam to the HVAC v1 typed `STATIC` / `SCENARIO` document and the multi-equipment `Plant` model.
- **REJECT** OSGi/component-framework infrastructure, ESS-specific domain modeling, plugin/factory machinery, or framework copying that is unnecessary for this contract.

## ThingsBoard CE

Pinned baseline: `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`.

Official source/tests reviewed for Current/Latest authority:

- `dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java`
- `dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/latest/sql/SqlLatestInsertTsRepository.java`
- `dao/src/test/java/org/thingsboard/server/dao/sqlts/SqlTimeseriesLatestDaoTest.java`

The detailed source evidence is recorded in `docs/architecture/thingsboard-source-review.md`, S06.

Decision for #332:

- **ADAPT** the upstream persisted Latest/Current ownership principle: platform Telemetry Runtime owns authoritative current observation state and its Business Revision.
- **REJECT** simulator-local counters being exposed as Telemetry Business Revision or canonical `*.business_revision` Points.
- **REJECT** preserving those simulator fields as aliases after the authority migration.

## MyEMS

Pinned baseline: `v6.7.0`, commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

Official source/module surfaces reviewed for acquisition versus central ownership:

- `myems-modbus-tcp`
- `myems-cleaning`
- `myems-normalization`
- `myems-aggregation`
- `database` historical/latest design

The detailed source evidence is recorded in `docs/architecture/myems-source-review.md`.

Decision for #332:

- **ADOPT** separation of protocol/acquisition behavior from centrally owned historical/current processing facts.
- **ADAPT** the acquisition seam to the current MQTT/simulator DeviceAdapter path.
- **REJECT** promoting simulator/acquisition-local state into canonical Telemetry truth or business revision ownership.

## Local consequence

#332 therefore performs one authoritative migration: the simulator consumes only the canonical Scenario exogenous inputs, `loadFraction` is removed rather than aliased, simulator-local revision counters are removed from command results and device telemetry, the four central-plant `*.business_revision` Points are deleted, and frontend central-plant profiles stop presenting those simulator values as business facts. Platform `DeviceObservationSnapshot.businessRevision` remains untouched because it belongs to the Telemetry Runtime authority boundary.
