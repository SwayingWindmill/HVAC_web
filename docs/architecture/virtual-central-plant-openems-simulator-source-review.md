# Virtual Central Plant — OpenEMS Simulator Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research OpenEMS simulator seams for HVAC adoption`
Reviewed: 2026-08-28
Primary upstream: `OpenEMS/openems` `develop` at `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6` (2026-08-26)

## Question

Which current OpenEMS simulator source patterns should HVAC_web adopt, adapt, or reject for Scenario/Datasource, reacting equipment, cycle timing, command/readback and production-controller reuse, given the project constraints against defensive compatibility and unnecessary framework complexity?

## Primary source reviewed

- OpenEMS simulator README: https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/readme.adoc
- `SimulatorDatasource`: https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/SimulatorDatasource.java
- reacting symmetric ESS: https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java
- reacting ESS test: https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java
- OpenEMS repository architecture and working conventions: https://github.com/OpenEMS/openems and https://github.com/OpenEMS/openems/blob/develop/AGENTS.md
- Existing HVAC pinned source evidence: `docs/architecture/openems-source-review.md`, especially “Review 005 — Simulator acting/reacting lifecycle”.

The current `develop` source was checked rather than assuming the older HVAC pin remains current. The simulator tree had recent maintenance on 2026-08-16 (`6fc76b9e255d7e644d458083ecef6a02c94adc89`, simulator reference-target/JUnit migration), while the core architecture below remains consistent with the existing HVAC source review.

## Source findings

### 1. Datasource is a small input seam, not the physical simulator

`SimulatorDatasource` has a deliberately small contract: available keys, time delta, and typed value/value-series lookup. OpenEMS then provides concrete CSV, direct-value and writable-channel datasources around that contract.

The simulator README describes datasource components as inputs to simulated components. The writable single-channel datasource is explicitly intended for external test input. This supports treating weather/load/occupancy-like signals as exogenous inputs rather than allowing a scenario to prescribe equipment outcomes.

### 2. Acting and reacting are meaningfully different

OpenEMS acting components consume datasource values and expose them as normal Channels. Reacting components instead hold state and change that state in response to production control operations.

For HVAC this distinction is important: weather and building cooling demand may be acting/exogenous; chiller capacity, COP, pump power, condenser temperatures and other equipment results are reacting/endogenous.

### 3. Reacting simulators implement production device capabilities

`SimulatorEssSymmetricReactingImpl` implements the same production Natures used by real components (`ManagedSymmetricEss`, `SymmetricEss`, `StartStoppable`, `ModbusSlave`, etc.). It does not introduce a simulator-only controller contract.

Its `applyPower()` receives the control result through the normal managed-device interface, updates simulated physical state, and writes the resulting state back to the same production Channels. The component also publishes a normal Modbus slave table derived from those same Natures.

This is the strongest OpenEMS pattern for HVAC: the simulated/physical distinction belongs below the production-facing capability/channel boundary.

### 4. Simulation time is obtained from the runtime clock

The reacting ESS integrates energy/SOC using `Instant.now(componentManager.getClock())`, not an independent simulator-private clock. OpenEMS can substitute a simulated clock when running its full Simulator App, but the reacting component itself depends only on the runtime clock abstraction.

This cleanly maps to the already-confirmed HVAC decision: live Virtual Plant runs at 1x wall-clock; a later Historical Replay runner may supply controlled historical time while reusing the same Plant/Scenario behavior.

### 5. Cycle boundaries matter to simulation correctness

OpenEMS is cycle/event driven. Existing HVAC source review already established the adopted order around Process Image, Controllers and Write. The simulator review established that datasource progression occurs after the write phase and that current-cycle telemetry must come from the immutable current Process Image rather than rereading mutable simulated state after output.

The consequence is deliberate one-cycle causality: a command decided/written in Cycle N changes reacting physical state, and normal input sampling makes that state observable in a later Process Image. Simulator-specific immediate readback must not bypass this boundary.

### 6. OpenEMS full Simulator App is broader than the HVAC need

OpenEMS' Simulator App can replace the whole Edge component configuration and supports an accelerated simulated clock (`timeleapPerCycle`). Its own README warns that enabling the app takes control of the complete Edge application and deletes existing component configurations.

That is useful for OpenEMS' full-system simulation use case, but is the wrong lifecycle for HVAC_web's acceptance simulator, which must attach explicitly to the existing production-like stack and must never become a destructive or fallback owner.

### 7. OpenEMS itself favors existing seams over new framework work

The current OpenEMS repository guidance asks contributors to follow nearby utilities/patterns before adding abstractions, run narrow validation, and avoid new frameworks unless explicitly required. This supports adopting simulator semantics without cloning OpenEMS' OSGi/bnd component infrastructure into the Go project.

## Adjudication

### ADOPT

1. **Separate exogenous datasource from reacting physical state.** Scenario inputs provide causes; equipment outputs are calculated state.
2. **Use production-facing contracts for simulated and physical equipment.** Controllers, Scheduler, Process Image, command governance, telemetry and business services must not branch on simulator mode.
3. **Keep datasource seam small.** It supplies typed external inputs and time progression; it is not a plugin framework or business-rule engine.
4. **Preserve explicit Cycle causality.** Read/Process Image → Controllers → governed write → reacting state update; the next input sample exposes new state.
5. **Advance scenario data at a defined cycle boundary.** The next scenario record belongs to the next Process Image, avoiding mid-cycle mutation.
6. **Use one runtime/injected clock for physical integration.** Live and Replay may use different runners/clocks without duplicating Plant physics.
7. **Test through production contracts and subsequent readback.** Simulator tests should drive the same command/channel/device boundary that real equipment uses.
8. **Allow external dynamic scenario input only through the datasource boundary.** A future live disturbance/input API may update datasource inputs; it must not update Alarm/FDD/Work Order or equipment-result Channels directly.

### ADAPT

1. **OpenEMS Nature/Channel → HVAC Capability Profile/Device/Point/Process Image.** Keep the established HVAC canonical Registry identities and `edgecontrol` contracts rather than copying Java interfaces.
2. **Per-component reacting simulator → coupled Central Plant model.** Chiller, CHWP, CWP and cooling tower share hydraulic/thermal state, so the physical equations may remain one Plant model while each equipment endpoint exposes its normal Device/Point contract.
3. **Channel-address datasource keys → canonical typed Plant inputs.** Prefer explicit fields/identities for required exogenous inputs. Do not make arbitrary string aliases part of the runtime contract.
4. **OpenEMS accelerated Simulator App → separate Historical Replay runner.** Reuse Scenario/Plant behavior, but keep live runtime at 1x wall-clock and keep replay timestamps explicit.
5. **OpenEMS datasource catalogue → minimum HVAC scenario surface.** Start with the concrete profile form needed by the first operating-day/fault acceptance scenario; add CSV/dynamic input only when a real use case needs it.
6. **OpenEMS ModbusSlave evidence → later protocol-level acceptance.** The source proves a reacting simulator can expose the same protocol-facing semantic contract, but the exact HVAC virtual Modbus shape remains dependent on the production Modbus seam research.

### REJECT

1. **Copying OSGi/bnd/component factory infrastructure.** It solves OpenEMS runtime composition, not a current HVAC_web problem.
2. **A destructive full-stack Simulator App lifecycle.** Virtual Plant must be an explicit acceptance/development attachment, not an owner that deletes/replaces running platform configuration.
3. **Simulator-specific Controller/Scheduler/Telemetry/business paths.** There must be no simulator Alarm, FDD, Forecast, Optimization or Work Order truth path.
4. **Accelerated time inside the live simulator.** It would make freshness, alarms, FDD windows, work-order timestamps and frontend “current” semantics ambiguous. Historical Replay is the separate mechanism.
5. **Silent missing-input defaults for required HVAC scenario inputs.** OpenEMS' writable single-channel datasource defaults to zero when unset; HVAC required weather/load inputs should instead be explicit and validated, because zero is a plausible but materially different physical value.
6. **Compatibility/alias lookup for scenario keys.** Existing OpenEMS CSV datasource behavior has historically accepted less-specific channel keys as fallback. HVAC must have one canonical key/typed field per input and reject invalid config instead of guessing.
7. **Using acting data to prescribe endogenous equipment outcomes.** Scenario must not directly set chiller COP/power/cooling capacity, alarm state or FDD result; those are consequences of reacting physics and production business owners.
8. **A broad datasource plugin hierarchy before concrete need.** One small seam plus the first required implementations is enough.

## Consequences for the Virtual Central Plant map

The OpenEMS side of the architecture is now clear:

```text
Scenario / external conditions
        |
        v
small typed Datasource seam
        |
        v
coupled reacting Central Plant
        |
        v
normal Device/Point/Capability + Process Image contract
        |
        v
production Controller / Scheduler / Command / Telemetry / business owners
```

This research does **not** decide the exact HVAC datasource/config API yet. That decision still requires the current EG8200/Edge authority-seam research so the contract is derived from both the upstream pattern and the local production boundary rather than from the prototype's current shape.

Likewise, this research does not start Virtual Modbus work. OpenEMS validates the architectural idea of exposing reacting simulation through normal protocol/device contracts; the exact HVAC Modbus boundary remains blocked on discovering whether a production driver/bridge seam exists locally.

## Decision

Use OpenEMS as a **behavioral architecture reference**, not a framework template:

- adopt Datasource vs Reacting separation, shared production contracts, cycle causality and runtime clock semantics;
- adapt those patterns to HVAC's coupled multi-equipment Plant and canonical Registry/Edge model;
- reject OSGi replication, destructive simulator ownership, ambiguous compatibility keys/defaults, simulator-only business paths and live accelerated time.

This is sufficient to resolve `Research OpenEMS simulator seams for HVAC adoption`. The next local-seam research remains necessary before the Wayfinder can graduate the exact Scenario contract from fog.
