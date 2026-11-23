---
date: 2026-11-23
category: 新增
---
- 新增站级批量群控指令接口（`POST /api/v1/sites/{siteId}/command-batches`、`GET` 列表与详情、`POST .../cancel`），支持同站最多 100 个目标点位独立校验与鉴权，高风险批量控制保持一步式 TOTP 确认与人工冷却锁约束。
- 新增 `QUEUE_IF_OFFLINE` 离线排队投递策略与活跃设备即时唤醒：设定值/模拟量调节指令可为离线或 `SLEEPING` 设备排队并延迟重试，上行遥测、上线或非休眠事件到达时主动提前唤醒派发；停机、启停机序列及紧急安全指令强制保持 `FAIL_FAST`。
- 补齐云边端到端 `ttlSeconds`（60 秒至 24 小时）与 `expiresAt` 到期防线：云端入队前、派发前与边缘执行器/模拟器均拦截超期与超时指令（`COMMAND_TTL_EXPIRED` / `TTL_EXCEEDS_MAX`），避免设备重新上线后执行过时控制动作。
