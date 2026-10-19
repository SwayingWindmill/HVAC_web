# 指令结果确认调研（2026-10-10）

问题：#444。一条「结果不明」（`OUTCOME_UNKNOWN`）的指令会挡住这台设备后面所有的指令，并冻结它的控制组，而且没有办法解除。决定见 [ADR 0018](../adr/0018-operator-reconciliation-of-unknown-command-outcomes.md)。

## 看过的源码（固定版本）

### ThingsBoard v4.4（`6d46786579c8b29caf5102f95ddb133674bed68b`）

- [`RpcStatus.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java)：超时（TIMEOUT）算还在进行；成功、失败、过期（SUCCESSFUL、FAILED、EXPIRED）算结束，之后不能再改。
- [`DeviceActorMessageProcessor.java`](https://github.com/thingsboard/thingsboard/blob/6d46786579c8b29caf5102f95ddb133674bed68b/application/src/main/java/org/thingsboard/server/actors/device/DeviceActorMessageProcessor.java)：每条待发的远程调用到 `expirationTime` 就超时，过期的记为过期或失败；不会挡住这台设备后面的调用。

**采用**：结束的状态不能改；确认结果时记一次新的状态变化，不改写历史。**不采用**：上一条的效果还不知道就发下一条，这正是 ADR 0006 要防止的。

### OpenEMS 2026.9.0（`14f0dedf5fe279845281bafaf0e48ea0ab51333a`）

- [`WriteChannel.java`](https://github.com/OpenEMS/openems/blob/14f0dedf5fe279845281bafaf0e48ea0ab51333a/io.openems.edge.common/src/io/openems/edge/common/channel/WriteChannel.java)：控制器每个周期设一次要写的值，写完就清掉；没有「一条指令」的生命周期。

**不作为参照**：设定值是连续写的，就没有哪一条指令会「结果不明」。现场端按租约执行的意图（ADR 0012）已经是这种做法。

### MyEMS v6.9.0（`b360f5bb4c2be4fd15854057963531b2e8d1bc0a`）

- [`myems-api/core/command.py`](https://github.com/MyEMS/myems/blob/b360f5bb4c2be4fd15854057963531b2e8d1bc0a/myems-api/core/command.py) 的 `CommandSend.on_put`：发到 MQTT 就返回成功，不读回状态。

**不采用**：没有证据就算成功，也就没有「结果不明」需要确认。

## 最后采用的做法

对这台设备有控制权限的人说明「已执行」或「没执行」；指令记一次由人做的状态变化，变成成功或失败，并写审计；那次发送尝试仍保留「结果不明」；控制组解冻。之后上报的状态对上了也不自动确认；「停止」不绕过冻结（以后再说）。
