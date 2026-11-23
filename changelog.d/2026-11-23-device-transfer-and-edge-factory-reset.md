---
date: 2026-11-23
category: 新增
---
- 支持物理设备跨站点与跨租户过户（`POST /api/v1/sites/{siteId}/devices/{deviceId}/transfer`）：过户时强制先清理原绑定与点位依赖、退役原逻辑设备并在目标站点生成全新的逻辑设备主键（UUIDv7），严格隔离新旧租户历史遥测、告警与工单数据边界。
- 支持过户后立即吊销原租户接入凭据并向边缘下发出厂安全清除指令（`FACTORY_RESET` / `FACTORY_RESET_ACK`），清理端侧本地 mTLS 证书与私钥、配置影子及离线/时序重传缓存，同时开放网关或直连设备的运维手动清除与记录查询接口（`POST /api/v1/gateways/{gatewayId}/factory-reset`、`GET /api/v1/gateways/{gatewayId}/factory-resets`）。
