---
date: 2026-11-23
category: 修复
---
- 修复智能运维助手（`operations-agent-service`）资产查询、设备列表、能耗序列与设备实时快照适配器，兼容并严格校验核心台账可选铭牌与水路字段（`ratedPower`、`ratedCapacity`、`tags`、`waterLoop`、`branchCode`）、合成总表能耗诊断字段（`synthesized`、`crossGap`、`crossGapKWh`、`incomplete`、`missingMeters`）及低功耗设备 `SLEEPING` 状态，消除 `TOOL_OWNER_RESPONSE_INVALID` 误报。
