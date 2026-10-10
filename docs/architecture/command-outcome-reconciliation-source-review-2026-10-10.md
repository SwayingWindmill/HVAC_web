# Command outcome reconciliation source review — 2026-10-10

Scope: #444. An `OUTCOME_UNKNOWN` command held every later command for its Device and froze its control group with no way out. Decision: [ADR 0018](../adr/0018-operator-reconciliation-of-unknown-command-outcomes.md).

## Pinned sources

### ThingsBoard v4.4 (`6d46786579c8b29caf5102f95ddb133674bed68b`)

- [`RpcStatus.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java): TIMEOUT is in flight; SUCCESSFUL, FAILED and EXPIRED are terminal and immutable.
- [`DeviceActorMessageProcessor.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/actors/device/DeviceActorMessageProcessor.java): a pending RPC's timeout is scheduled at its `expirationTime`, and an expired RPC is closed EXPIRED or FAILED; nothing holds later RPCs for the device.

**ADOPT:** terminal statuses stay immutable, and settling records a transition instead of rewriting history. **REJECT:** sending the next RPC while the previous one's effect is unknown, which ADR 0006 exists to prevent.

### OpenEMS 2026.9.0 (`14f0dedf5fe279845281bafaf0e48ea0ab51333a`)

- [`WriteChannel.java`](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/src/io/openems/edge/common/channel/WriteChannel.java): a controller sets the next write value each cycle and it is consumed and reset; there is no per-command lifecycle.

**REJECT** as a model for this decision: with continuous setpoints there is no single command whose outcome can be unknown. The lease-bound Edge intents of ADR 0012 already follow this model at the Edge.

### MyEMS v6.9.0 (`b360f5bb4c2be4fd15854057963531b2e8d1bc0a`)

- [`myems-api/core/command.py`](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/command.py) `CommandSend.on_put`: publishes to MQTT and returns success without readback.

**REJECT:** success without evidence; there is no unknown state to settle.

## What was adopted

A person with the Device's control authority states APPLIED or NOT_APPLIED; the Intent records a PRINCIPAL transition to SUCCEEDED or FAILED with an audit intent, the Attempt keeps OUTCOME_UNKNOWN, and the control group is unfrozen. Later matching readback does not settle it automatically, and STOP does not bypass the hold (deferred).
