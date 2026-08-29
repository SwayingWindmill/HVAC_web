# Review 015 — Deployed ATV630 protocol acceptance source review

Issue: #347
Parent: #331

## Scope

Prove the production-neutral Edge host, production Modbus/TCP Bridge, production ATV630 DeviceAdapter and Virtual ATV630 slave as separately running WSL/Docker components over a real TCP socket. The acceptance environment must bind the immutable released ATV630 template revision and must not introduce an in-process or protocol fallback.

## Reference checkpoints

### OpenEMS

Rechecked current `develop` at `caf4f971…`, focusing on the Modbus TCP bridge/process-image/write-cycle boundary already used by #336/#339.

- **ADOPT**: the production Edge process owns the Modbus TCP bridge/client connection to an external device process; input synchronization occurs before control decisions and writes occur after decisions.
- **ADAPT**: retain the same causality without OSGi/component runtime machinery; the Go Edge composition root wires `Host -> ModbusTCPBridge -> ATV630DeviceAdapter` explicitly.
- **REJECT**: simulator-owned in-memory state inside the production Edge process, automatic protocol/profile fallback, and any acceptance shortcut that bypasses a real TCP socket.

### ThingsBoard Gateway 3.8.3

Rechecked the previously pinned Modbus integration fixture pattern at commit `7f7e0bf061bf92c2feb12b5098620f118dce364b`.

- **ADOPT**: black-box deployment testing against a separately running Modbus endpoint and observable connection failure.
- **ADAPT**: use the existing HVAC MQTT/Telemetry publisher and canonical Point contract rather than the ThingsBoard connector/config model.
- **REJECT**: introducing a generic connector framework, dynamic register aliases, or fallback mappings for this ticket.

### MyEMS 6.7.0

Rechecked the previously pinned Modbus TCP acquisition boundary at commit `be6e6ce8ddeac57afb04bddb9621501fb555cab0`.

- **ADOPT**: external Modbus/TCP acquisition remains a transport boundary whose failures are visible.
- **ADAPT**: preserve HVAC Edge process-image/control semantics rather than MyEMS polling architecture.
- **REJECT**: treating acquisition success as sufficient proof of governed command causality or immutable template release.

## Implementation decisions

1. The Virtual ATV630 process exclusively owns `Plant`, physical tick progression, stuck-high disturbance and the Modbus/TCP slave.
2. The deployed ATV630 Edge process has no `Plant` dependency. It owns the production Host, production Modbus/TCP Bridge, production ATV630 DeviceAdapter, command Intent lifecycle, Process Image and MQTT publisher.
3. Canonical Registry Point codes are not renamed to vendor channel names. DeviceAdapter channels are bound to canonical Points through stable `PointID -> Channel address` binding.
4. A single governed START Intent remains active while the ATV630 adapter returns `IN_PROGRESS` through ETA-confirmed CiA402 transitions `6 -> 7 -> 15`; it completes only after later ETA reports operation-enabled.
5. The acceptance Registry bootstrap uses the existing `ReleaseTemplate` owner and writes only the returned immutable TemplateRevision identity into runtime config. The released payload is the exact #337 `ATV630ProtocolReleaseCandidate()` with Schneider references `EAV64327 v03` and `EAV64332 v4.6 (2026-05-01)`, and it explicitly does not claim hardware certification.
6. The production Phase 1 compose remains unchanged. All Virtual-device deployment wiring is confined to the explicit `atv630-protocol-acceptance` profile and an acceptance-only workspace Dockerfile.

## WSL deployment evidence

Observed on 2026-08-29 in Docker Desktop WSL acceptance:

- `atv630-template-release` exited `0` and produced immutable TemplateRevision `01a04b91-beb1-7a77-a7b5-e5597364ee34`.
- `virtual-atv630` and `atv630-edge` ran as separate containers on the `hvac-phase1-local_mqtt` network.
- Edge diagnostics reported `modbusReady=true`, `mqttReady=true`, template key `schneider.atv630.cia402-modbus-tcp`, and the immutable revision above.
- `SET_FREQUENCY 42 Hz` returned production Edge result `APPLIED`; later independent Modbus readback moved from `50.0 Hz` to `47.1 Hz`, proving physical reaction rather than immediate in-memory substitution.
- `STOP` returned `APPLIED`; later readback reported `runState=STOPPED` with physical coast-down (`31.9 Hz`).
- One `START` command returned final `APPLIED` after the ETA-confirmed multi-cycle sequence; later readback reported `runState=RUNNING`.
- Injected fault `16` in the Virtual process was later read through production Modbus as `runState=FAULT`, `faultCode=16`, `frequencyHz=0`; `RESET_FAULT` returned `APPLIED` and later readback cleared current fault.
- With CHWP stuck-high enabled while STOPPED, independent later RFR increased from `12.4 Hz` to `33.5 Hz`; the disturbance remained a Plant physical effect and the protocol endpoint stayed generic.
- Stopping the Virtual ATV630 container left the Edge process running but changed diagnostics to `modbusReady=false` while `mqttReady=true`; no alternate adapter/register/profile appeared. Restarting the Virtual endpoint restored `modbusReady=true` automatically.

## Release/certification boundary

This review proves deployed protocol-level conformance of the production Bridge/DeviceAdapter against the Virtual ATV630 over real TCP and binds that path to the immutable Registry revision. It does **not** establish Schneider real-hardware Vendor Template Certification.
