# Virtual Central Plant — Production Modbus Seam Source Review

Status: WAYFINDER RESEARCH EVIDENCE
Wayfinder map: `wayfinder: virtual central plant`
Research ticket: `Research production Modbus seam for protocol-level simulation`
Reviewed: 2026-08-28

## Question

Does HVAC_web currently have a production Modbus bridge/driver seam that can connect unchanged to a virtual Modbus/TCP plant, and if not, what is the smallest missing production boundary that must exist before protocol-level simulator work is justified?

## Sources reviewed

Local:

- `libs/edgecontrol/driver.go`
- `libs/edgecontrol/cycle.go`
- `libs/edgecontrol/component.go`
- `tools/eg8200-simulator/internal/simulator/edge_runtime.go`
- `tools/eg8200-simulator/internal/simulator/edge_driver.go`
- `tools/eg8200-simulator/internal/simulator/mqtt_publisher.go`
- `tools/eg8200-simulator/cmd/eg8200-mqtt-publisher/main.go`
- `contracts/architecture/edge-control-plane.v1.json`
- `infra/registry/postgres/init/009-energy-data-foundation.sql`
- `docs/architecture/openems-source-review.md`
- `docs/architecture/openems-architecture-adjudication.md`

Primary upstream reference:

- OpenEMS `BridgeModbusTcpImpl.java`
  - https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.bridge.modbus/src/io/openems/edge/bridge/modbus/BridgeModbusTcpImpl.java
- OpenEMS real Modbus component example (`AbstractGoodWe.defineModbusProtocol`)
  - https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.goodwe/src/io/openems/edge/goodwe/common/AbstractGoodWe.java
- OpenEMS reacting simulator (`SimulatorEssSymmetricReactingImpl`)
  - https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.simulator/src/io/openems/edge/simulator/ess/symmetric/reacting/SimulatorEssSymmetricReactingImpl.java
- OpenEMS Natures / Modbus slave tables
  - https://github.com/OpenEMS/openems/blob/develop/io.openems.edge.ess.api/src/io/openems/edge/ess/api/SymmetricEss.java

Secondary cross-checks:

- ThingsBoard IoT Gateway Modbus connector documentation
  - https://thingsboard.io/docs/iot-gateway/config/modbus/
- ThingsBoard IoT Gateway architecture
  - https://thingsboard.io/docs/iot-gateway/what-is-thingsboard-iot-gateway/
- MyEMS Modbus TCP acquisition architecture / configuration
  - https://github.com/MyEMS/myems
  - https://github.com/MyEMS/myems/discussions/10

## Executive answer

**No. HVAC_web does not currently have a concrete production Modbus/TCP bridge plus physical DeviceAdapter path that can be pointed unchanged at a Virtual Plant.**

What exists today is the correct semantic boundary but not the protocol implementation:

```text
Controller / Scheduler
        ↓
Process Image / Decisions
        ↓
edgecontrol.DeviceAdapter      ← production-facing contract exists
        ↓
[Protocol Bridge / raw mapping] ← concrete Modbus runtime missing
        ↓
physical Modbus/TCP device      ← no production driver in repository
```

The current Virtual Plant connects directly at `DeviceAdapter`:

```text
Plant
  ↓
simulatedDeviceAdapter
  ↓
DeviceHost / Process Image
```

That is a valid in-memory acceptance seam, but it is **not protocol-level acceptance**.

Before a Virtual Modbus slave is justified, one production tracer slice must exist:

```text
Released Device Template / Protocol Mapping
        ↓
production Edge host
        ↓
Modbus/TCP Bridge
        ↓
one production Modbus DeviceAdapter
        ↓
DeviceHost / Process Image / Controllers / MQTT
```

Only then should the Virtual Plant expose the exact Modbus server/register behavior expected by that same production DeviceAdapter.

## 1. The canonical production-facing Device boundary already exists

`edgecontrol.DeviceAdapter` explicitly states that it is shared by physical and simulated drivers. Its contract is already the correct high-level seam:

- `Component()` — component identity/capability declaration;
- `Channels()` — typed semantic Channel contract;
- `Poll(...)` — device input side;
- `Apply(...)` — governed write side.

`DeviceHost` owns registration, Channel ownership, polling and write dispatch. Controllers do not see protocol addresses or register types.

This is aligned with OpenEMS Natures/Channels: protocol-specific raw representation belongs below the semantic Channel contract.

**Decision:** keep `DeviceAdapter`, `ChannelDescriptor`, `CapabilityProfile`, `ProcessImage`, Controller and Scheduler unchanged for physical Modbus devices.

## 2. The Edge cycle already has the correct synchronization seam for a Protocol Bridge

`edgecontrol.Cycle` exposes explicit lifecycle phases including:

- `BEFORE_PROCESS_IMAGE`;
- `EXECUTE_WRITE`;
- `AFTER_WRITE`.

The source comment explicitly says that Driver polling remains asynchronous to Controller execution and that a Protocol Bridge may synchronize read/write work through Cycle hooks.

This mirrors the OpenEMS Modbus Bridge model, where `BridgeModbusTcpImpl` participates in cycle events before the Process Image boundary and during the write phase.

**Decision:** do not invent a second Modbus scheduler/cycle. A concrete Modbus bridge should integrate through the existing Cycle hook model.

## 3. `ComponentProtocolBridge` is currently metadata, not a runtime implementation

`ComponentProtocolBridge` exists as a Component kind and architecture contracts mark Protocol Bridge as `PARTIAL`.

But repository search finds:

- no Modbus bridge interface/implementation;
- no Modbus TCP client dependency in Go modules;
- no read/write register task implementation;
- no connection pooling/retry/backoff implementation;
- no runtime use of Registry `protocol_mappings`;
- no production Modbus DeviceAdapter.

So registering a Component descriptor with `Kind=PROTOCOL_BRIDGE` does not currently create any protocol behavior.

**Finding:** the architecture placeholder must not be confused with a production seam.

## 4. There is no deployable production Edge host using `edgecontrol` yet

Repository-wide search for `NewDeviceHost` shows runtime construction only in:

- edgecontrol tests;
- EG8200 simulator tests;
- `tools/eg8200-simulator/internal/simulator/edge_runtime.go`.

There is no deployment entry under `deploy/` for a generic production Edge host using `edgecontrol`.

The current `eg8200-mqtt-publisher` embeds:

- Plant creation;
- simulator-specific config;
- simulated DeviceAdapters;
- Edge Runtime construction;
- MQTT publisher;
- command handling.

Likewise, `MQTTPublisher` accepts simulator `Config` and `*EdgeControlRuntime` rather than a simulator-independent production host contract.

**Finding:** a real Modbus DeviceAdapter currently has no production runtime executable in which to run.

This does **not** mean a new framework is required. It means the production-neutral bootstrap already demonstrated inside the simulator needs to become a small deployable Edge host before field protocol work can be called production.

## 5. Registry already owns the protocol mapping vocabulary

The Registry schema already provides the correct authoritative configuration model:

```text
device_template_versions
    ↓
point_templates
    ↓
protocol_mappings
    ↓
command_definitions
```

`protocol_mappings` already records:

- protocol;
- protocol address;
- raw data type;
- scale;
- value offset;
- byte order;
- word order;
- function code;
- revision.

This is exactly the kind of mapping a Modbus driver needs between raw registers and canonical Points/Channels.

But current repository search finds no runtime reader/adapter consuming these mappings.

**Decision:** the Virtual Plant must not invent another JSON register table in Scenario config or frontend code. The physical DeviceAdapter and Virtual Modbus endpoint should both be derived from the same released/certified Device Template / Protocol Mapping contract.

The Edge may consume a frozen projection of that released mapping, but Registry remains the authoritative source of template identity and revision.

## 6. OpenEMS source confirms the desired separation

### Bridge

OpenEMS `BridgeModbusTcpImpl` owns:

- TCP master connection lifecycle;
- port/IP configuration;
- reconnect/open behavior;
- transaction creation;
- protocol timeout/retry behavior;
- Edge cycle participation.

The bridge is not a business-domain owner.

### Real driver

OpenEMS real device implementations declare a `ModbusProtocol` containing concrete FC3/FC16 tasks, register addresses, raw word/doubleword types, converters and priorities. Those raw details map into stable semantic Channels.

The important rule is:

```text
raw register type/address
    belongs to Driver/Bridge

semantic Channel / capability
    remains stable above it
```

### Reacting simulator

`SimulatorEssSymmetricReactingImpl` implements the same production Natures as real devices and also implements `ModbusSlave`. Its slave table is composed from the same Nature-level Channel tables. It does not create simulator-specific Controller contracts.

That is the protocol-level acceptance shape HVAC should copy conceptually:

```text
production Modbus master/driver
        ↕ TCP
virtual Modbus slave
        ↕
reacting physical model
```

while Controllers and Cloud remain unchanged.

## 7. ThingsBoard and MyEMS support the same ownership boundary

ThingsBoard IoT Gateway treats Modbus as an Edge connector that polls/writes industrial devices and translates them into the platform device model. Modbus register configuration stays in connector configuration, not Alarm/Rule/UI business facts.

MyEMS similarly deploys `myems-modbus-tcp` as a separate acquisition service. Its datasource/point configuration includes host/port, function code, register offset, register count, byte swapping and raw format, while downstream cleaning/normalization/aggregation remain separate services.

These references reinforce the same decision:

**protocol acquisition belongs below the platform/business model and must not create a Virtual-Plant-specific Cloud path.**

## 8. Smallest justified production Modbus tracer slice

Do not implement a generic multi-protocol framework first.

The smallest useful slice is:

1. **Production-neutral Edge host**
   - initialize existing `Runtime`, `ComponentRegistry`, `DeviceHost`, `IntentStore`, Scheduler, Cycle and Timedata;
   - use existing MQTT/cloud command transport semantics;
   - contain no Plant/Scenario dependency.

2. **One concrete Modbus/TCP bridge**
   - TCP connection lifecycle;
   - bounded timeout/retry;
   - read work synchronized before Process Image;
   - write work synchronized with execute-write;
   - enough priority/fair scheduling for the first real DeviceAdapter only.

3. **One production Modbus DeviceAdapter**
   - binds one released Device Template version;
   - resolves its canonical Point Templates / Protocol Mappings;
   - converts raw register values to existing semantic Channel types;
   - maps Decisions back to Modbus writes;
   - exposes the same Capability Profile used by Controllers.

4. **Then one Virtual Modbus/TCP slave**
   - exposes exactly the register behavior required by that released Device Template;
   - reads physical state from the shared reacting Plant;
   - applies Modbus writes to the physical model;
   - has no Alarm/FDD/Telemetry/Work Order knowledge.

5. **Protocol-level acceptance**

```text
Virtual Plant physics
       ↕
Virtual Modbus/TCP Slave
       ↕ real TCP
production Modbus Bridge
       ↕
production DeviceAdapter
       ↕
DeviceHost / Process Image
       ↕
production Controller / Scheduler
       ↓
normal MQTT / Telemetry / business owners
```

The production bridge and DeviceAdapter must be used unchanged between real hardware and Virtual Plant tests.

## 9. What should not be built

Reject the following:

- Simulator-only Modbus register schema duplicated from Registry;
- a `VirtualModbusDeviceAdapter` that bypasses the production Modbus client;
- Controller branches checking whether a device is simulated;
- Telemetry/Alarm/FDD/Work Order protocol-specific fields;
- browser configuration of register addresses;
- a generic `ProtocolBridge<TProtocol>` abstraction before a second protocol exists;
- simultaneous BACnet/OPC-UA framework work;
- a full OpenEMS task/component framework copied into Go;
- an acceptance-only fake vendor driver that cannot be used against real hardware;
- preserving both prototype `sourceKey` register semantics and a new certified mapping as compatibility modes.

## 10. MQTT simulator adapter transition

The existing in-memory Simulator DeviceAdapter + MQTT path remains valuable for the first end-to-end business acceptance because it exercises production Edge Controller/Telemetry/Cloud seams cheaply.

It should not be deleted merely because Modbus is added.

Instead the roles become explicit:

```text
In-memory DeviceAdapter path
    = fast physics/controller/business acceptance

Modbus/TCP path
    = protocol/driver/production-edge acceptance
```

Once the first production Modbus slice is operational, protocol-level acceptance is the stronger seam for that certified device profile. The MQTT simulator adapter is not a production fallback and never substitutes for a failed Modbus device in Real Mode.

## 11. Remaining HITL decision

The architecture question is now clear, but one product/acceptance choice must not be guessed:

> Which first **released/certified Modbus Device Template** should be the tracer device for protocol-level Virtual Plant acceptance?

A useful first tracer should ideally exercise both reads and governed writes, so a variable-speed pump/VFD-class device is stronger than a read-only meter for validating the full Edge write phase. However no vendor register map should be invented from generic assumptions or demo frontend metadata.

The selected device must have an actual protocol specification or an approved internal Device Template/Protocol Mapping before implementation.

This decision should graduate to a separate Wayfinder grilling ticket.

## Final decision

- **KEEP** existing `DeviceAdapter`, Channel, Capability, Process Image, Scheduler and Cycle contracts.
- **ADOPT** OpenEMS separation of Bridge transport/task ownership from Driver register mapping and semantic Channels.
- **ADAPT** the mapping source to HVAC Registry released Device Template / `protocol_mappings` rather than a simulator-local table.
- **REQUIRE** a small production-neutral Edge host plus one real Modbus/TCP Bridge/DeviceAdapter tracer before Virtual Modbus implementation.
- **ADD LATER** a Virtual Modbus slave that implements the exact same certified mapping and talks to the production driver over real TCP.
- **REJECT** simulator-specific business APIs, duplicated register schemas, compatibility fallbacks, generic multi-protocol frameworks and protocol implementation without a real production device slice.
