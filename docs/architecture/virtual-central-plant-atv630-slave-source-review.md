# Virtual Central Plant ATV630 Modbus Slave Source Review

Date: 2026-08-28

Local issue: #338

OpenEMS checkpoint: `develop` commit `df53f1670ed9b1a782c6c215082a375d5dd4b55e`.

Standing comparison baselines: ThingsBoard IoT Gateway `3.8.3` / `7f7e0bf061bf92c2feb12b5098620f118dce364b`; MyEMS `v6.7.0` / `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

## Sources re-checked

OpenEMS:

- `io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java`;
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`;
- `io.openems.edge.common/src/io/openems/edge/common/modbusslave/ModbusSlave.java`;
- `io.openems.edge.common/src/io/openems/edge/common/modbusslave/ModbusSlaveTable.java` and `ModbusSlaveNatureTable.java`;
- `io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/ModbusSlaveSimulator.java`.

The current reacting simulator implements the same production Natures it simulates and also implements `ModbusSlave`; it does not create a second business model for its slave surface. The Modbus test simulator starts an actual TCP slave/process image and exposes explicit registers to a real master connection.

ThingsBoard IoT Gateway:

- `tests/integration/data/modbus/modbus_server.py` at `3.8.3`;
- the Modbus connector and black-box/integration tests already reviewed for #336/#337.

The ThingsBoard integration fixture starts a real TCP server with explicit unit/register process images so the gateway is exercised through the protocol boundary. It is test infrastructure rather than a business-state owner.

MyEMS:

- `myems-modbus-tcp/test.py` and `README.md` at `v6.7.0`;
- the acquisition source already reviewed for #336/#337.

MyEMS provides a master/acquisition boundary and verifies real TCP reachability plus Modbus reads. It does not provide a reacting device-side simulator pattern to adopt for this ticket.

Implementation dependency:

- `github.com/simonvetter/modbus` `v1.6.4` (MIT), already selected and pinned by #336. #338 uses its production client in real-socket tests. Its server implementation was inspected but not used because its `RequestHandler` collapses FC6 and FC16 writes into the same `HoldingRegistersRequest`, which would make the Virtual device broader than the #337 release candidate.

## ADOPT / ADAPT / REJECT

- **ADOPT**: one reacting physical state owner is shared by the simulator and protocol endpoint. The Virtual ATV630 reads CHWP state from the existing `Plant` and sends physical actuator requests through `Plant.ApplyCommand`; it does not mirror frequency, flow or fault state in a second simulator model.
- **ADOPT**: the device endpoint is a real Modbus/TCP server. Tests connect through a real localhost socket, and the acceptance compose exposes TCP port `1502` only in the explicit `simulator-acceptance` profile.
- **ADOPT**: the Virtual device exposes exactly the #337 release-candidate addresses: ETA `3201`, RFR `3202`, LFT `7121`, CMD `8501`, LFR `8502`. The addresses and scaling are read from `edgecontrol.ATV630ProtocolReleaseCandidate()` rather than duplicated in Scenario or frontend configuration.
- **ADOPT**: ETA is the CiA402/DriveCom protocol state, RFR is later physical CHWP frequency readback, and LFT retains the last numeric drive fault after reset as Schneider specifies. The #337 adapter decides whether that retained LFT is a current semantic `faultCode` only when ETA indicates an active fault. CMD advances the CiA402 state/physical start-stop-reset path and LFR updates the Plant frequency setpoint. Command writes do not mutate the current physical readback.
- **ADAPT**: OpenEMS expresses slave tables through Java Natures/OSGi components. HVAC uses one narrow Go `VirtualATV630Server` around the existing `Plant`; no component framework is copied.
- **ADAPT**: the first device-side endpoint implements only the minimal Modbus/TCP framing and function surface needed by the exact candidate: FC3 holding-register reads and FC6 single-register writes. This small standard-library endpoint is intentionally narrower than `simonvetter/modbus` server mode because that library erases the distinction between FC6 and single-register FC16 before the handler. FC16 is explicitly rejected with Modbus Illegal Function, while the mature `simonvetter/modbus` client remains the real-socket test master.
- **ADAPT**: ThingsBoard's static process-image test server is useful evidence for real TCP black-box testing, but HVAC binds register reads/writes to a reacting Plant so later reads represent device behavior rather than fixture mutation.
- **REJECT**: simulator-only registers, generic register configuration, address aliases/base offsets, alternate ATV630 profiles, automatic mapping detection, Telemetry/Alarm/FDD/Work Order knowledge, direct downstream business writes and production fallback to the Virtual endpoint.
- **REJECT**: implementing CHWP stuck-high inside the protocol server. #334 is the Plant-layer owner and is now merged on `main` (`d302239`, merge `4f48360`). #338 remains disturbance-agnostic: its real-TCP tracer lowers LFR while the Plant-level stuck-high disturbance keeps actual CHWP frequency/flow high, and RFR exposes that later physical state without any protocol-specific disturbance branch; after the disturbance is removed, the same RFR dynamically recovers toward the governed LFR.

## Local consequence

`tools/eg8200-simulator/internal/simulator/virtual_atv630.go` owns only the Virtual device's protocol projection and CiA402 transition state. `eg8200-mqtt-publisher` starts it against the same `Plant` on `EG8200_ATV630_MODBUS_ADDR` (default `:1502`). `deploy/acceptance/phase1-simulator.compose.yaml` exposes that port only under the existing simulator acceptance profile.

Focused evidence is `TestVirtualATV630ExposesOnlyReleaseCandidateRegistersOverRealTCP` (including explicit FC16 rejection), `TestVirtualATV630WritesDrivePlantAndLaterReadsPhysicalState`, `TestVirtualATV630StuckHighRemainsPhysicalAndAppearsOnlyInLaterRFR`, and `TestVirtualATV630FaultReadbackAndResetUsePlantState`. Full production Bridge + production ATV630 DeviceAdapter conformance and immutable Registry release remain #339 scope.
