---
date: 2026-11-22
category: 改进
---

- 边缘网关 `cmd/edge-gateway` 与 `libs/edgecontrol` 新增 Modbus 线圈与离散量输入功能码（`FC01` / `FC02` / `FC05` / `FC15`）、共享串口 `45ms` 默认转向间隔与控制写优先于周期读的 RS-485 总线锁。
- 网关运行时接入 `FileTimedata` 与模拟量 `DeadbandFilter`，实现 `VERY_HIGH -> LOW` 四级优先级断网续传与 `replay: true` 重放标记、`DeterministicReplicationUUIDv7` 幂等键及 `LWT` / `GATEWAY_CONNECTED` / `GATEWAY_DISCONNECTED`（含 `sessionId`）生命周期事件上报。
- 修复 `edge-gateway` `emitRuleEvents` 边缘规则事件载荷对齐 `#950`：移除重复 `"EDGE_RULE_"` 事件名前缀，统一 `sourceType: "EDGE_RULE"`，按 `occurredAt` 设置事件时间并序列化完整 `EdgeRuleEvent`（保留 `eventId`、`ruleId`、`severity`、`triggeredActions`）。
