---
date: 2026-11-22
category: 新增
---
- 设备接入服务新增边端 OTA 升级包存储、Ed25519 签名校验、站点与设备标签筛选、`mTLS` 断点续传（HTTP `Range` `206`）、灰度放量与失败率熔断编排，以及 MQTT `down/ota` / `up/ota` 状态同步。
- 平台网关 `energy-api` 与 OpenAPI 契约新增 `/api/v1/ota/packages` 与 `/api/v1/ota/campaigns` 等读写接口，并同步配置 Nginx 64 MiB 上传与 300 秒网关升级包下载代理超时。
