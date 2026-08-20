# OpenEMS Source Review Record

Status: CURRENT IMPLEMENTATION EVIDENCE
Architecture authority: `contracts/architecture/edge-control-plane.v1.json`
Reference repository: `OpenEMS/openems`
Pinned baseline: release `2026.7.0`, commit `2e2792d`

## Rule

This record is required implementation evidence for every `OPENEMS` architecture decision and every Edge-related `MERGE` decision that materially depends on OpenEMS behavior.

The implementation sequence is mandatory:

1. inspect pinned official source;
2. inspect relevant upstream tests and official documentation;
3. record source-level semantics here;
4. classify the mechanism as `ADOPT`, `ADAPT`, or `REJECT`;
5. implement/refactor HVAC Web;
6. add focused local behavior tests.

Architecture prose is not sufficient implementation evidence. Source is reviewed for behavior and lifecycle, not copied verbatim.

## Existing implementation validation queue

All modules below existed locally before completing their pinned-source review and therefore receive no incumbency preference:

| Local module | Current source-validation state | Reference slice |
|---|---|---|
| Channel Runtime | VERIFIED | Channel / AbstractReadChannel / WriteChannel |
| Process Image | VERIFIED | Channel.nextProcessImage / CycleWorker |
| Cycle | VERIFIED | CycleWorker / Edge cycle events |
| Controller Runtime | VERIFIED | Controller / CycleWorker |
| Scheduler | VERIFIED | Scheduler.FixedOrder / CycleWorker |
| Control Arbiter | VERIFIED | controller ordering + write-channel/control constraint implementations |
| Capability Profile Registry | VERIFIED | Nature interfaces + ChannelId/Doc contracts |
| Edge Component Registry | VERIFIED | OpenemsComponent / ComponentManager lifecycle |
| Edge Manifest | VERIFIED | EdgeConfig / Component/Channel serialization |
| Device Driver | VERIFIED | common physical/simulated driver contract + protocol mapping boundary |
| Protocol Bridge | REVIEWING | Bridge.Modbus task/worker implementation; no real protocol driver exists yet |
| Remote Intent Lease | VERIFIED | external write timeout / controller API behavior |
| Edge Timedata | VERIFIED | local latest/history/query authority; Cloud resend worker remains separate |
| Simulator Driver | VERIFIED | Simulator/DataSource acting/reacting implementations |
| MQTT Command Edge Adapter | VERIFIED | S11 governed Edge execution evidence + Cloud independent readback; CycleWorker boundary retained |

A state may change to `VERIFIED` only when the reviewed source files, relevant upstream tests, material differences, local justification for every retained conflict, and focused behavior tests are all recorded below.

## Review 001 — Channel, Process Image, Cycle, Scheduler

Date: 2026-08-17
HVAC decisions: `EDGE-004` through `EDGE-010`, `MODEL-002`, `MODEL-003`

### Upstream source reviewed

- `io.openems.edge.common/src/io/openems/edge/common/channel/Channel.java`
- `io.openems.edge.common/src/io/openems/edge/common/channel/internal/AbstractReadChannel.java`
- `io.openems.edge.common/src/io/openems/edge/common/channel/WriteChannel.java`
- `io.openems.edge.common/src/io/openems/edge/common/channel/value/Value.java`
- `io.openems.edge.common/src/io/openems/edge/common/event/Cycle.java`
- `io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java`
- `io.openems.edge.common/src/io/openems/edge/common/component/Scheduler.java`
- `io.openems.edge.scheduler.fixedorder/src/io/openems/edge/scheduler/fixedorder/SchedulerFixedOrderImpl.java`
- official Edge Architecture documentation for Process Image / Controller execution

The pinned production source is the authoritative evidence for Channel/Cycle lifecycle. The repository has no dedicated `CycleWorker` behavior test at this path; local focused tests therefore protect the adopted phase ordering, immutable image, undefined-value, per-cycle write reset, clock and failure semantics rather than inventing upstream test behavior.

### Source-level findings

#### Channel / Process Image

OpenEMS Channel is not merely a value holder. The source establishes a lifecycle around an active/current value and a next value written by asynchronous workers. At the Process Image boundary, `nextProcessImage()` promotes the pending value to the active value. Channel exposes distinct notification semantics for setting a next value, updating the active value, and changing the active value. This lets asynchronous I/O remain outside Controller execution while Controllers consume one stable image.

OpenEMS also keeps short-lived past values and clears callbacks on deactivation. HVAC Web already has durable Edge Timedata, so duplicating durable history inside every Channel would create two history authorities. Short in-memory past-value support is therefore deferred until a Controller needs it; durable history remains Edge Timedata.

Decision:

- `ADOPT`: current/next double buffering;
- `ADOPT`: immutable per-cycle Process Image;
- `ADOPT`: distinct next/update/change Channel events;
- `ADAPT`: canonical HVAC `PointID` remains mandatory on every governed Channel;
- `ADAPT`: durable history stays in Edge Timedata rather than duplicating OpenEMS in-Channel history;
- `DEFER`: dynamic Channel deactivation/removal until dynamic component lifecycle is implemented.

#### Cycle

OpenEMS `CycleWorker` implements an explicit lifecycle rather than a single opaque loop. The reviewed source orders the core phases around Process Image promotion, Controller execution, and protocol writes: before/after Process Image, before/after Controllers, and before/execute/after Write. Read-side asynchronous work is synchronized before Process Image; write-side protocol work is synchronized to the write phase.

Decision:

- `ADOPT`: explicit named Cycle phases and deterministic phase order;
- `ADOPT`: Process Image switch must occur before Controllers;
- `ADOPT`: actuator/protocol write happens only after Controllers finish;
- `ADOPT`: per-cycle execution evidence/duration;
- `ADAPT`: HVAC keeps an explicit fail-closed `Critical` Controller flag. OpenEMS isolates individual Controller failures and continues; that is desirable for ordinary Controllers, but a failed HVAC Safety/Protection Controller must prevent lower-priority actuator output.

#### Scheduler

OpenEMS Scheduler returns Controller IDs in configured order once per Cycle. `SchedulerFixedOrderImpl` preserves configured ordering rather than deriving order from incidental service registration. `CycleWorker` isolates ordinary Controller exceptions and continues executing the schedule while exposing failure state.

Decision:

- `ADOPT`: deterministic configured Controller ordering;
- `ADOPT`: ordinary Controller failure isolation with explicit result evidence;
- `ADAPT`: HVAC currently stores direct Controller objects instead of OSGi component IDs, eliminating an entire class of runtime missing-ID lookup errors;
- `ADOPT`: configured Controller list order is authoritative; numeric priority metadata must not silently reorder that list;
- `ADOPT`: repeated Controller IDs execute once, preserving the first configured position like `LinkedHashSet`;
- `ADAPT`: critical safety failure halts the output path as described above.

### Local implementation consequences

The first source-informed refactor must therefore add:

1. Channel `NEXT_VALUE`, `UPDATE`, and `CHANGE` event subscriptions without invoking callbacks while the Channel Runtime mutex is held;
2. explicit Cycle phases matching the upstream lifecycle around Process Image, Controllers and writes;
3. phase execution evidence in `CycleResult`;
4. focused tests for event semantics and exact phase ordering;
5. retention of the existing HVAC fail-closed critical-controller behavior.

### Implementation evidence — VERIFIED for S10

The stricter source-first rule was applied retroactively on 2026-08-17 and the remaining S10 Channel/Cycle surface was closed on 2026-08-19 against the pinned source above. `VERIFIED` here is limited to the S10 foundation contract; it does not imply S11 command outcome or S12 fleet/release completion.

Material conflicts already found and corrected:

- `libs/edgecontrol/channel.go`: `UPDATE` now occurs on every Process Image cycle after activation rather than only when a fresh asynchronous value arrived; `CHANGE` follows underlying value changes rather than timestamp/quality changes; short runtime history now retains the OpenEMS-equivalent 5m10s window; Channel unregister clears runtime subscriptions and Point mapping;
- `libs/edgecontrol/cycle.go`: explicit `BEFORE_PROCESS_IMAGE`, `AFTER_PROCESS_IMAGE`, `BEFORE_CONTROLLERS`, `AFTER_CONTROLLERS`, `BEFORE_WRITE`, `EXECUTE_WRITE`, `AFTER_WRITE` phases, hook evidence and cycle duration;
- Scheduler now preserves configured order and deduplicates repeated Controller IDs instead of re-sorting by local numeric priority;
- ordinary Controller failures remain isolated; HVAC critical Controller failure still halts before output as an explicit safety adaptation.

Local improvements retained with evidence:

- Channel callbacks are copied under the Runtime lock and invoked after unlock because invoking arbitrary Go callbacks while holding the central Runtime mutex would introduce avoidable deadlock/re-entrancy risk; this changes synchronization technique, not OpenEMS event semantics;
- monotonic `Sample.Sequence` validation is retained because HVAC Edge/Cloud replay and deduplication use explicit transport sequence evidence that OpenEMS Channel values do not model;
- durable history remains in Edge Timedata in addition to the newly restored short Channel runtime history.

Focused verification after these corrections:

- `go test ./libs/edgecontrol/...` — passed;
- Phase 1 Edge Runtime / MQTT Command / generated central-plant targeted tests — passed.

Closure evidence:

- OpenEMS `Value` may be undefined at any time; HVAC represents the same state explicitly as `ChannelSnapshot.HasValue=false` rather than inventing a typed zero value;
- OpenEMS consumes a `WriteChannel` next-write value after retrieval. HVAC adapts this through a fresh `ControlPlan` on every Cycle: per-cycle decisions disappear after write, while an unexpired governed `Intent` must explicitly reassert a persistent setpoint on the next Cycle;
- `DeviceHost.UnregisterAdapter` removes the adapter, Component and owned Channels together, and Channel unregister clears Point mapping/subscriptions; failed driver registration rolls back Channels already registered during that attempt;
- Cycle execution remains measured with Go's monotonic `time.Since`; a regressing logical cycle timestamp is rejected before Process Image promotion, and a rejected device write marks the Cycle halted while still allowing the `AFTER_WRITE` lifecycle hook to run;
- focused tests cover immutable Process Image, configured scheduler order, critical-controller halt, clock regression and rejected-write halt. No generic overrun supervisor is introduced because S10 requires truthful completed-cycle evidence, not a speculative cadence framework.

## Review 002 — Modbus Bridge scheduling and failure isolation

Date: 2026-08-17
HVAC decisions: `DEVICE-002`, `DEVICE-003`, `DEVICE-004`, `EDGE-007`

### Upstream source/tests/docs reviewed

- `io.openems.edge.bridge.modbus/readme.adoc`;
- `io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/api/` task/worker architecture;
- `io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/api/task/` read/write task hierarchy;
- `io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/BridgeModbusTcpImplTest.java`;
- `io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/BridgeModbusSerialImplTest.java`.

### Source-level findings

The bridge is not just a socket wrapper. It owns shared-bus scheduling. Write tasks are synchronized immediately after the cycle write event and are treated as high priority. Read tasks are scheduled as late as practical before the next Process Image boundary. High-priority reads run every cycle; low-priority reads are round-robin. Repeatedly failing components are isolated with increasing read delay/backoff rather than being allowed to consume the whole bus indefinitely, and a component can request communication retry. The bridge also exposes timing/health evidence such as learned cycle delay and insufficient cycle time.

The upstream TCP test exercises a real Modbus slave simulator, the actual bridge, a real Modbus component/protocol mapping, Process Image cycles, and removal/invalidation behavior instead of mocking the whole bridge boundary.

Decision:

- `ADOPT`: shared protocol bridge owns transport connection lifecycle and task scheduling;
- `ADOPT`: write work is aligned to the Edge write phase;
- `ADOPT`: read work completes before the Process Image boundary;
- `ADOPT`: control-critical reads execute every cycle;
- `ADOPT`: lower-value reads use fair round-robin scheduling;
- `ADOPT`: defective-device backoff prevents one device from monopolizing a shared bus;
- `ADOPT`: protocol integration tests must use a real in-process/protocol-level simulator where practical;
- `ADAPT`: HVAC `VERY_HIGH/HIGH` read priorities map to every-cycle control reads; `MEDIUM/LOW` map to background/fair scheduling instead of blindly copying OpenEMS's two-level enum;
- `ADAPT`: implementation will be added together with the first real Modbus Driver/Bridge slice, not as an unused speculative scheduler detached from a physical-device path.

## Review 003 — Nature, Component, Channel Doc and EdgeConfig

Date: 2026-08-17
HVAC decisions: `MODEL-002`, `MODEL-005`, `MODEL-006`, `MODEL-007`, `MODEL-008`, `DEVICE-005`, `DEVICE-006`

### Upstream source reviewed

- `io.openems.edge.common/src/io/openems/edge/common/component/OpenemsComponent.java`;
- `io.openems.edge.common/src/io/openems/edge/common/channel/Doc.java`;
- `io.openems.edge.meter.api/src/io/openems/edge/meter/api/ElectricityMeter.java`;
- `io.openems.common/src/io/openems/common/types/EdgeConfig.java`;
- `io.openems.edge.meter.siemens/src/io/openems/edge/meter/siemens/pac2200/MeterSiemensPac2200Impl.java`;
- `io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/api/ChannelToElementConverter.java` and related Modbus protocol mapping code.

### Source-level findings

OpenEMS Nature contracts are strongly typed Channel contracts, not a bag of floating-point values. `ElectricityMeter` declares INTEGER and LONG semantic Channels with explicit units and persistence priorities. Real Modbus drivers can use very different raw register/element types and convert them into the stable Nature Channel type. Therefore protocol register type and semantic Edge Channel type are two separate contracts.

Channel `Doc`/`EdgeConfig` self-description includes at least type, access mode, descriptive text, unit, category, persistence priority and enum/state details. An EdgeConfig Component exposes identity, alias, factory/config properties and its Channels. `OpenemsComponent` also has enabled state and dynamic lifecycle. OSGi `servicePid`/DS metadata is an implementation detail of the Java runtime, not an architectural behavior requirement.

Decision:

- `ADOPT`: explicit semantic Channel types `BOOLEAN`, `STRING`, `INTEGER`, `LONG`, `FLOAT`, `DOUBLE`;
- `ADOPT`: protocol raw/register types remain in Driver/Bridge mapping and are converted into semantic Channel types;
- `ADOPT`: Channel self-description includes text/description, unit, category and local/remote persistence policy;
- `ADOPT`: Component self-description includes id, alias, enabled state, factory identity, config properties and owned Channel IDs;
- `ADAPT`: canonical HVAC `PointID`, poll/aggregation/resend priorities remain additional metadata because they serve Cloud identity and store-forward behavior not represented by the basic OpenEMS EdgeConfig Channel;
- `ADAPT`: Phase 1 HVAC engineering quantities such as Hz, kW and °C currently use `DOUBLE`; future real device Drivers must explicitly convert raw `UINT16/FLOAT32/...` protocol values instead of leaking register types into the semantic Channel contract;
- `ADAPT`: `CapabilityProfile` is the Go/runtime equivalent of Nature and remains data/self-description driven because the Cloud Registry must reconcile capabilities; it may not weaken the strongly typed required/optional Channel contract;
- `REJECT`: OSGi-specific `servicePid` and Declarative Services wiring are not added to the Go runtime.

### Local corrections made during review

- removed the former generic `NUMBER/TEXT/BOOL` Edge type set and introduced the six strong semantic types;
- split the ambiguous local `PersistencePriority` into `LocalPersistencePriority` and `RemotePersistencePriority`;
- added Channel `Description`, `Category`, enum options and state-level metadata to the runtime descriptor/manifest surface;
- added Component alias/enabled/factory/config metadata and explicit owned Channel IDs to Edge Manifest;
- defensive cloning now includes Channel options and Component raw configuration properties;
- Component Registry gained explicit unregister support as the first step toward dynamic component lifecycle.

Focused verification after the source-driven changes:

- `go test ./libs/edgecontrol/...` — passed;
- Phase 1 Edge Runtime / MQTT Command / generated central-plant targeted tests — passed.

For S10, Capability Profile, Component Registry and Edge Manifest are now `VERIFIED`: physical and simulated drivers must expose the same profile/channel contract, and `DeviceHost` owns coherent adapter/component/channel registration and removal. A concrete real protocol Driver/Bridge is intentionally still `REVIEWING` under Review 002; protocol mapping is not being declared complete from metadata alone. Enum/state usage may grow with actual HVAC device profiles but is not required to prove the typed foundation.

## Review 004 — Timedata, live persistence and historic resend

Date: 2026-08-17
HVAC decisions: `DATA-002`, `DATA-003`, `DATA-004`, `DATA-005`, `DATA-006`, `DATA-007`

### Upstream source reviewed

- `io.openems.edge.timedata.api/src/io/openems/edge/timedata/api/Timedata.java`;
- `io.openems.common/src/io/openems/common/timedata/CommonTimedataService.java`;
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/TimedataRrd4jImpl.java`;
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/RecordWorker.java`;
- `io.openems.edge.timedata.rrd4j/src/io/openems/edge/timedata/rrd4j/Rrd4jConstants.java`;
- `io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/SendChannelValuesWorker.java`;
- `io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ResendHistoricDataWorker.java`.

### Source-level findings

OpenEMS separates three responsibilities that the first HVAC implementation incorrectly mixed together:

1. local Timedata is a local latest/history/query service and is collected after Process Image using `LocalPersistencePriority`; WRITE_ONLY Channels are excluded;
2. live Edge→Backend publishing is a separate worker using `RemotePersistencePriority`, sends changed values every Cycle and forces a full snapshot at least every five minutes;
3. historic resend is a separate recovery worker. It detects missing timeranges, queries Timedata for the affected time/channel range, sends bounded recovery chunks and only advances successful-resend evidence after transport success.

RRD4j itself records a five-minute local representation and aggregates values from Channel short history. That storage resolution is an OpenEMS implementation choice, not a safe default for HVAC Web because this project's canonical telemetry contract requires original high-resolution observations for Store&Forward and S2 replay.

Decision:

- `ADOPT`: Timedata owns local latest/history/query only; it does not own Cloud resend cursors;
- `ADOPT`: local persistence filtering uses `LocalPersistencePriority` and excludes WRITE_ONLY Channels;
- `ADOPT`: live Cloud publishing and historic resend remain separate services/policies from Timedata;
- `ADOPT`: live policy will send changed values plus a periodic full snapshot, using `RemotePersistencePriority`;
- `ADOPT`: historic resend will query Timedata by time range and Channel set, and successful recovery evidence advances only after send success;
- `ADAPT`: HVAC local Timedata keeps every new canonical observation instead of the OpenEMS RRD4j five-minute rollup because lossless Edge Store&Forward/S2 replay is an explicit product requirement;
- `ADAPT`: the existing MQTT packet queue remains transport durability after a message has been materialized; it does not replace Timedata or historic-query recovery.

### Local corrections made during review

- removed per-priority `Pending/Acknowledge/Acked` resend cursor state from `FileTimedata`;
- replaced per-resend-priority history files with one monotonic local history stream plus latest projection;
- `RecordImage` now filters by local persistence threshold and excludes WRITE_ONLY Channels;
- added time-range + Channel query as the stable boundary for a separate historic resend worker;
- retained high-resolution canonical samples with an explicit HVAC justification;
- Phase 1 local threshold is currently `LOW`, preserving every eligible observed Channel locally.

Focused verification after the source-driven changes:

- `go test ./libs/edgecontrol/...` — passed;
- Phase 1 Edge Runtime / MQTT Command / generated central-plant targeted tests — passed;
- EG8200 MQTT publisher build — passed.

The S10 local Timedata owner seam is `VERIFIED`: local latest/history/range query, local-persistence filtering and WRITE_ONLY exclusion are source-aligned. Live MQTT changed-value/full-snapshot publication and historic resend remain separate transport/recovery workers and are not claimed complete here; `DATA-006`/`DATA-007` therefore remain partial/missing in the machine architecture contract rather than blocking the Timedata module itself.

## Review 005 — Simulator acting/reacting lifecycle

Date: 2026-08-17
HVAC decisions: `SIM-001`, `SIM-002`, `SIM-003`, `SIM-004`

### Upstream source/tests reviewed

- `io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/SimulatorDatasource.java`;
- `io.openems.edge.simulator/src/io/openems/edge/simulator/datasource/api/AbstractDatasource.java`;
- `io.openems.edge.simulator/src/io/openems/edge/simulator/meter/grid/acting/SimulatorGridMeterActingImpl.java`;
- `io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java`;
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`;
- simulator test tree for acting/reacting meter, datasource and protocol-level simulator components.

### Source-level findings

OpenEMS separates exogenous scenario data from reacting equipment behavior. A `SimulatorDatasource` supplies typed time-series inputs and advances its record on `AFTER_WRITE`; acting simulators consume the current datasource value before Process Image and publish it as Channel next values. Reacting simulators implement the same production Nature/managed interfaces as physical equipment and change physical state in response to normal write/control operations. Their tests drive the production control Channel contract and use time leaps to verify physical reaction rather than calling a simulator-only command API.

The Cloud/backend data path observes the stable Process Image before the current Cycle's actuator write. Therefore a simulated command written in Cycle N must not leak into Cycle N telemetry; the new state is observable through normal input sampling in a later Cycle.

Decision:

- `ADOPT`: simulated and physical equipment share the same DeviceDriver/Capability/Channel/Controller boundary;
- `ADOPT`: exogenous acting data and reacting physical state are separate concerns;
- `ADOPT`: scenario/datasource progression occurs after the write phase so the next record belongs to the next Process Image;
- `ADOPT`: current-Cycle telemetry is generated from that Cycle's Process Image, never by rereading mutable simulated device state after output;
- `ADOPT`: simulator tests exercise production control inputs and subsequent readback, including time progression;
- `ADAPT`: HVAC central-plant `Plant` remains a reacting multi-equipment thermal model instead of splitting every physical equation into an OSGi simulator component; production DeviceDriver/Capability boundaries already isolate it from Controllers;
- `ADAPT`: a `PlantDatasource` boundary will be introduced together with the first variable weather/load scenario so it is immediately used by the Phase 1 simulator rather than added as an unused abstraction.

### Local corrections made during review

- `EdgeControlCycleResult` now contains a telemetry snapshot derived exclusively from the immutable Process Image;
- EG8200 MQTT publisher publishes that Process Image snapshot instead of `Plant.Snapshot()` after actuator writes;
- typed Process Image values are mapped back to canonical simulator source keys for the existing MeasurementScheduler;
- a focused test proves a frequency command applied in Cycle N is absent from Cycle N telemetry and becomes observable in Cycle N+1.

Focused verification:

- Phase 1 Edge Runtime / MQTT Command / generated central-plant targeted tests — passed after the Process Image telemetry correction.

The S10 simulator/production-control boundary is now `VERIFIED`: simulator and physical drivers share `DeviceAdapter`, Capability Profile and Channel contracts, while Controller/Scheduler/Intent/Telemetry logic stays outside the simulated device model. Broader simulator capabilities remain incomplete: variable exogenous datasource progression, protocol-level Modbus simulation and additional delay/fault/stuck-actuator models stay `PARTIAL` and must be added only with a concrete device/test need.

## Review 006 — Remote override timeout / leased Cloud Intent

Date: 2026-08-17
HVAC decisions: `EDGE-011`, `EDGE-014`

### Upstream source reviewed

- `io.openems.edge.controller.api.common/src/io/openems/edge/controller/api/common/ApiWorker.java`;
- `io.openems.edge.controller.api.modbus/src/io/openems/edge/controller/api/modbus/AbstractModbusApi.java`;
- `io.openems.edge.controller.api.modbus/src/io/openems/edge/controller/api/modbus/readwrite/AbstractModbusReadWriteApi.java`.

### Source-level findings

OpenEMS external API writes are not one-shot setpoint writes. `ApiWorker` keeps the current external write value and reapplies it every Controller Cycle until its timeout fires. A new external write resets the timeout. Timeout clears the active write map/override state and the Modbus ReadWrite API also clears mirrored actual-set Channels. This is the mechanism that prevents a dead external controller from owning the actuator forever.

The first HVAC `IntentStore` had an `ExpiresAt` field but the Phase 1 Edge runtime revoked every Intent immediately after its first successful Driver write. That was a material semantic conflict: the code had expiration metadata without actually holding a leased override.

Decision:

- `ADOPT`: persistent setpoint overrides participate in every Cycle until timeout/expiry;
- `ADOPT`: expiration removes the override so local Controllers regain authority;
- `ADOPT`: external override is expressed through normal writable Channel/Arbiter flow, never a bypass to DeviceDriver;
- `ADAPT`: HVAC uses per-Command absolute `ExpiresAt` rather than one global API-worker timeout because durable Cloud Command identity already provides explicit independent expiry for each governed intent;
- `ADAPT`: numeric/setpoint commands are leased continuous overrides, while current `ACTION` commands (`START`, `STOP`, `RESET_FAULT`) are one-shot operations after their first successful execution. Continuously pulsing reset/start/stop operations would not be a safe semantic translation of a maintained OpenEMS WriteChannel value;
- `ADAPT`: Cloud retains command audit/approval/verification independently from the Edge override lifetime.

### Local corrections made during review

- successful numeric COMMAND execution no longer revokes its Edge Intent;
- numeric Intent stays active and is rewritten by the Intent Controller each Cycle until its `ExpiresAt`;
- failed/rejected/cancelled and ACTION commands still revoke immediately;
- expired active Intent entries are removed from the Intent Store;
- focused simulator test perturbs a leased VFD frequency locally, confirms the lease restores the governed setpoint on the next Cycle, then confirms a post-expiry local value remains untouched.

Focused verification:

- `go test ./libs/edgecontrol/...` — passed;
- Phase 1 Edge Runtime / MQTT Command / generated central-plant targeted tests including lease persistence — passed.

The local S10 lease/timeout mechanism is `VERIFIED`: active numeric intents reassert through the ordinary Controller/Arbiter path each Cycle and disappear at `ExpiresAt`, returning authority to local control. Physical transport delivery, Cloud renewal and authoritative command readback remain S09/S11 concerns; they are not needed to prove that the Edge lease itself expires safely.

## Review 007 — S10 closure: safety freshness, cycle evidence and driver parity

Date: 2026-08-19
HVAC roadmap slice: `S10 edge-control-foundation-source-alignment`

### Upstream source/tests reviewed

- `io.openems.edge.common/src/io/openems/edge/common/channel/Channel.java`;
- `io.openems.edge.common/src/io/openems/edge/common/channel/internal/AbstractReadChannel.java`;
- `io.openems.edge.common/src/io/openems/edge/common/channel/WriteChannel.java`;
- `io.openems.edge.common/src/io/openems/edge/common/channel/value/Value.java`;
- `io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java`;
- `io.openems.edge.common/test/io/openems/edge/common/component/AbstractOpenemsComponentTest.java`;
- `io.openems.edge.bridge.modbus/test/io/openems/edge/bridge/modbus/BridgeModbusTcpImplTest.java`;
- `io.openems.edge.simulator/test/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImplTest.java`.

The pinned Modbus TCP test is `@Disabled` upstream. It is used here only as source evidence for the intended protocol-level integration shape; it is not represented as a passing upstream certification.

### Source-level closure decisions

- `ADOPT`: undefined device values are first-class state. HVAC keeps `ChannelSnapshot.HasValue`/Quality rather than allowing an absent safety input to become a typed zero/default.
- `ADAPT`: OpenEMS `WriteChannel` consumes and resets the pending write value. HVAC achieves the same per-cycle non-stickiness with a new `ControlPlan` each Cycle; a persistent remote setpoint exists only because an unexpired `Intent` explicitly reasserts it through the normal Controller/Arbiter path.
- `ADOPT`: Cycle phase order and actual completed-cycle duration remain explicit. `CycleResult.Duration` measures runtime duration; HVAC additionally rejects a regressing logical Cycle timestamp before Process Image promotion because leases and freshness checks depend on monotonic Edge time.
- `ADAPT`: `DeviceAdapter` is the common production-facing contract for physical and simulated devices. Both expose the same Component, Capability Profiles, typed Channels, input polling and arbitrated output contract. A future physical driver may delegate protocol I/O to a Bridge without changing Controller/Scheduler code.
- `ADOPT`: driver/component/channel lifecycle is coherent. Removing an adapter removes its Component and Channels, and failed registration cleans Channels already registered during that attempt.
- `ADAPT`: safety freshness uses each existing canonical Point `staleAfter`; no new hard-coded timeout is introduced. A stale fault/interlock sample fails closed with `SAFETY_STATE_STALE`.
- `ADAPT`: an output/device rejection is a failed control Cycle (`Halted=true`, `OutputError!=nil`) while `AFTER_WRITE` still runs for lifecycle/evidence cleanup.
- `KEEP REVIEWING`: the real Protocol Bridge implementation. The repository has no concrete real Modbus/BACnet/OPC-UA driver dependency today. Adding an unused bridge scheduler or protocol package only to satisfy S10 would violate the project's no-speculative-infrastructure rule. Review 002 remains binding for the first real physical protocol driver.
- `DEFER`: MQTT governed command outcome/readback to S11 and fleet/sync/signed release/config/OTA to S12.

### Local implementation evidence

- `libs/edgecontrol/driver.go`: common `DeviceAdapter`/`DeviceHost` for `DEVICE_DRIVER` and `SIMULATOR`, coherent unregister and failed-registration cleanup;
- `libs/edgecontrol/component.go`: both physical and simulated device components require Capability Profiles;
- `libs/edgecontrol/cycle.go`: serialized Cycle execution, logical clock regression rejection, truthful duration, failed output halt;
- `tools/eg8200-simulator/internal/simulator/edge_runtime.go`: Point `staleAfter` is applied to fault/interlock evidence; production Controller/Scheduler/Intent/Telemetry path remains outside the simulated Plant;
- focused tests: real/simulator adapter parity, driver lifecycle removal, clock regression, rejected write, stale interlock, existing immutable Process Image/interlock/lease tests.

### Verification

- `go test ./...` in `libs/edgecontrol` — PASS;
- `go test ./...` in `tools/eg8200-simulator` — PASS;
- `cmd.exe /c node scripts/check-s3-target-runtime.mjs` — PASS (`files=18`, native MQTT, OpenEMS-informed Edge, production traffic remains `0`).

## Review 004 — S11 governed MQTT command execution evidence

Date: 2026-08-19
HVAC issue: #270

### Upstream source reviewed

- `io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java` from release `2026.7.0`, commit `2e2792d`;
- the Channel/Process Image/Scheduler/Write sources and tests already pinned in Reviews 001–003 remain the reference for execution ordering.

### Source-level decision

OpenEMS keeps one stable Process Image for controller evaluation and performs writes only after the Scheduler/Controller phase. S11 preserves that boundary: the MQTT adapter does not write Plant state itself; it creates a leased Edge Intent, then the existing Scheduler/Arbiter/Interlock decides the effective value and the normal write phase applies it.

- `ADOPT`: command execution remains inside the normal Process Image -> Controllers -> Write lifecycle;
- `ADAPT`: each governed Cloud command emits durable HVAC execution evidence containing requested, effective/applied, constraints, winning Controller and Cycle number;
- `ADAPT`: a durable `MAY_EXECUTE` record is written before scheduling the Intent. Recovery from that state is `EDGE_OUTCOME_UNKNOWN`, not a second physical execution;
- `ADAPT`: Cloud transport acknowledgement is deliberately weaker than business success; only a later independent S2 readback can produce `SUCCEEDED`;
- `REJECT`: an MQTT callback that writes the simulated/physical Plant directly, or any Edge-local `VERIFIED` status that bypasses independent Cloud readback.

### Focused local evidence

- constrained 55 Hz -> 50 Hz records requested/effective/constraint/winner/cycle and writes only through the Edge Cycle;
- local interlock rejection stays an Edge execution rejection and does not mutate Cloud approval/authorization facts;
- persisted terminal replies survive restart; persisted `MAY_EXECUTE` state never replays the actuator command and recovers the maximum fence;
- transport-only ACK without Edge execution evidence is rejected by Command Service;
- numeric independent readback verifies the Edge applied/effective target while the original Cloud Intent parameter remains immutable.

## Review 005 — S12 signed Desired Edge state and local activation

Date: 2026-08-19
HVAC issue: #279

### Upstream source reviewed

OpenEMS release `2026.7.0`, commit `2e2792d`:

- `io.openems.common/src/io/openems/common/types/EdgeConfig.java`
- `io.openems.common/src/io/openems/common/types/EdgeConfigDiff.java`
- `io.openems.edge.core/src/io/openems/edge/core/componentmanager/EdgeConfigWorker.java`

### Source-level decision

OpenEMS treats the running Edge configuration as a self-describing set of Components/Factories/Channels and exposes explicit created/deleted/updated component diffs. `EdgeConfigWorker` rebuilds its cache from actual configuration events, emits configuration/channel update events and redacts password properties from serialized configuration.

- `ADOPT`: self-describing component/capability/configuration evidence, explicit config diff semantics and event-driven observed configuration updates;
- `ADAPT`: HVAC Cloud Desired state is an immutable signed `EdgeRelease` plus owner-revision Snapshot rather than mutable Edge-local configuration being the Cloud authority;
- `ADAPT`: configuration is first staged, digest/signature/capability/reference/preflight checked, then atomically activated. Health/readback failure records the failed revision and restores the previous signed release instead of mutating release history;
- `ADAPT`: OpenEMS password redaction becomes the stricter HVAC rule that SecretRef/credential identity may be referenced but signing private keys, credential values and recoverable secrets are never members of the Fleet release/snapshot model;
- `REJECT`: direct remote mutation of arbitrary Edge component JSON, Edge-local config as the master copy of Cloud-owned fields, or a rollback that edits an already released revision.

### Focused local evidence

- disk-backed Replica restart leaves the active snapshot untouched while an interrupted newer snapshot remains resumable staging state;
- Cloud-owned change items use one owner domain/revision and Edge-owned observed/control/telemetry/audit domains are rejected from the downlink path;
- signed EdgeRelease activation preserves active/staged/previous identities and automatically rolls back after failed health verification;
- offline pressure evicts diagnostic/normal telemetry before safety/control/alarm/audit evidence.

## Review queue

The next source reviews are performed before their implementation slices:

1. Single/Cluster Controller patterns;
2. EnergyScheduler V2 time-slot/mode optimization.
