# Virtual Central Plant — First Certified Modbus Device Template Acceptance Slice

Status: WAYFINDER DECISION
Wayfinder map: `wayfinder: virtual central plant`
Decision ticket: `Decide first certified Modbus Device Template acceptance slice`
Decided: 2026-08-28

## Decision

The first production Modbus/TCP tracer for protocol-level Virtual Central Plant acceptance is a Schneider Electric Altivar Process ATV630 VFD used as the communicating Device Endpoint for CHWP-01.

The physical pump remains the maintainable Asset. The ATV630 is the independently communicating Device whose Points may describe the pump as their Measured Subject.

The same released Device Template / Protocol Mapping revision must be used unchanged by:

- the production Modbus Bridge and ATV630 DeviceAdapter against real hardware; and
- the Virtual ATV630 Modbus/TCP slave against the reacting Virtual Plant.

There is no simulator-only register table and no compatibility alias layer.

## Product and source specification

Manufacturer: Schneider Electric
Product range: Altivar Process ATV630
Protocol: Embedded Ethernet / Modbus TCP

Pinned source specifications for the first template revision:

- `EAV64327`, Embedded Ethernet Manual, version 03
- `EAV64332`, Communication Parameters, version 4.6, published 2026-05-01

These references are part of the template release provenance. A later specification change does not silently mutate a released template; it requires a new template revision when the effective protocol contract changes.

## Device/Asset boundary

```text
CHWP-01
= Asset: chilled-water pump

ATV630-CHWP-01
= Device: independently communicating VFD

ATV630 Device Points
→ may use CHWP-01 as Measured Subject
```

The frontend mock entry that names a KSB pump is not protocol authority and must not be used as a vendor-register source.

## Control profile

The first production driver supports one explicit control profile only:

```text
CiA402 / DriveCom
command and frequency reference not separated
via Embedded Ethernet / Modbus TCP
```

The implementation must not also support I/O Profile in v1 and must not auto-detect or fallback between profiles.

Production Controllers continue to use semantic capabilities:

```text
START
STOP
RESET_FAULT
SET_FREQUENCY
```

The ATV630 DeviceAdapter owns the translation from these governed Decisions into the required CiA402 command-word sequence and legal device-state transitions.

Controller code never manipulates raw Modbus register addresses or CiA402 command bits.

## Minimal v1 raw protocol mapping

The first Device Template intentionally freezes only the raw variables required to prove read, governed write and independent readback.

```text
3201  ETA  RO  WORD  drive status
3202  RFR  RO  INT   actual motor frequency
7121  LFT  RO  ENUM  current/last detected fault code
8501  CMD  RW  WORD  CiA402 command word
8502  LFR  RW  INT   frequency reference
```

The exact datatype, scale, unit, byte/word order and Modbus function-code metadata are recorded in Registry `protocol_mappings` from the pinned Schneider specification rather than in Simulator configuration.

## Semantic Channel mapping

The production DeviceAdapter projects the raw protocol contract into the existing `VARIABLE_SPEED_PUMP` capability profile.

```text
ETA
  → runState
  → fault-active state

LFT + ETA fault-active state
  → faultCode

RFR
  → frequency

CMD
  ← START
  ← STOP
  ← RESET_FAULT

LFR
  ← SET_FREQUENCY
```

### Fault semantics

`LFT` must not be exposed blindly as the current `faultCode` because it may retain the most recently detected fault after the active fault condition has cleared.

The DeviceAdapter therefore exposes a current fault code only when the current drive state indicates an active fault:

```text
if ETA indicates active fault:
    faultCode = decode(LFT)
else:
    faultCode = empty
```

Historical fault diagnosis remains separate from the current semantic Channel.

## Deliberately excluded from v1

The first template does not add protocol mappings merely because ATV630 exposes them.

Excluded from the first tracer:

- pump flow rate: owned by the physical flow measurement path, not inferred as an authoritative VFD fact;
- optional power telemetry;
- current, torque and thermal diagnostics;
- PID configuration;
- the broad ATV parameter catalogue;
- simulator-only fault or control registers;
- alternate I/O Profile mappings;
- BACnet or other protocol variants.

A second concrete requirement may justify extending the template in a later immutable revision.

## RELEASED versus Vendor Template Certification

`RELEASED` and hardware certification are separate concepts.

### RELEASED Template Revision

A Registry Device Template revision may be released after its authoritative protocol contract is reviewed and proven against the production Modbus Bridge/DeviceAdapter and the Virtual ATV630 slave.

`RELEASED` means:

> this immutable protocol contract is approved for platform use by the production driver.

Release requires at least:

- pinned official source specifications;
- explicit Point/Channel and protocol mappings;
- reviewed datatype, address, scale, unit and access semantics;
- production Modbus read/write implementation using those mappings;
- protocol-level acceptance against the Virtual ATV630 slave over real TCP;
- governed write followed by independent readback through the normal Process Image cycle.

### Vendor Template Certification

Vendor Template Certification is separate acceptance evidence that the same released revision has been validated against real ATV630 hardware.

Hardware certification validates, at minimum:

```text
Address
DataType
Endian / word order
Scale
Unit
Access / function code
Command semantics
Readback semantics
```

Absence of physical hardware must not be disguised as certification. It also must not force the Virtual Plant to invent a second register contract.

When lab hardware becomes available, certification evidence attaches to the immutable released revision.

If real hardware proves that a released mapping is wrong:

```text
released v1 remains immutable
        ↓
correct v2 is released
        ↓
assignment migrates explicitly
```

No address aliases, dual-register probing or fallback behavior are added to preserve the incorrect revision.

## Protocol-level acceptance shape

The required tracer is:

```text
Virtual Central Plant physics
        ↕
Virtual ATV630 Modbus/TCP Slave
        ↕ real TCP
Production Modbus/TCP Bridge
        ↕
Production ATV630 DeviceAdapter
        ↕
DeviceHost / Process Image
        ↕
Production Controller / Scheduler
        ↓
normal MQTT / Telemetry / business owners
```

The production Bridge and DeviceAdapter are unchanged when the Virtual slave is replaced by real ATV630 hardware.

## Implementation consequence

The protocol-level implementation sequence is therefore constrained to:

1. provide a production-neutral Edge host for the existing `edgecontrol` runtime;
2. implement one concrete Modbus/TCP Bridge integrated with existing Cycle phases;
3. define/release the ATV630 Device Template revision from the pinned Schneider documents;
4. implement one production ATV630 DeviceAdapter using only that released mapping;
5. implement the Virtual ATV630 Modbus/TCP slave from the same mapping and reacting CHWP physical model;
6. prove poll → Process Image → Controller → governed write → real TCP → physical reaction → independent readback;
7. later validate the same released revision against real ATV630 hardware and record Vendor Template Certification evidence.

No generic multi-protocol framework is required for this tracer.
