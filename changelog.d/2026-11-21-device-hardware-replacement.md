---
date: 2026-11-21
category: 新增
---
- 支持逻辑设备现场换机换表并保持历史用能、趋势曲线、点位与告警连续（#913）：新增 `device-hardware-replacements.sql` 迁移与 `POST /api/v1/sites/{siteId}/devices/{deviceId}/replace-hardware` 接口，事务性轮换设备外部序列号、通信地址与 `gateway_device_source_keys` 映射并写入审计记录；Web「系统管理 / 设备与点位登记」设备列表新增「更换硬件」操作弹窗。
