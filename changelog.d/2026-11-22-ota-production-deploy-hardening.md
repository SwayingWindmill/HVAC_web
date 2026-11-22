---
date: 2026-11-22
category: 修复
---
- 补齐 Mosquitto ACL 中 `mqtt-telemetry-adapter` 对 `hvac/v1/+/up/ota` 与 `hvac/v1/+/down/ota` 的收发授权，使生产与本地环境启用 OTA 后 MQTT 适配器可正常订阅并下发分片升级指令。
- 在 Phase 1 容器构建脚本与部署脚本中为 `connectivity` 镜像和既有 `connectivity-ota-packages` 卷统一设置 `65532:65532` 属主，避免非 root 进程写入 OTA 安装包目录时触发权限拒绝。
- 为 `edge-gateway` 新增可选的 `mqttCaFile` 与 `mqttServerName` 配置项并在未填写时回退到 `caFile`/`serverName`，支持 HTTPS 公网证书与 MQTT 内部 CA 分离的场景。
