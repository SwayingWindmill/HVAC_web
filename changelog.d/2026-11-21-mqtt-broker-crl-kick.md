---
date: 2026-11-21
category: 新增
---
- 补齐 MQTT Broker（Eclipse Mosquitto 2.1.2）网关证书吊销列表（CRL）校验与在线连接实时踢连闭环：新增 `mosquitto_hvac_crl.so` 插件支持双 CA 隔离、验签后切换、按证书指纹精准踢连与 1 秒文件轮询兜底；`connectivity` 在启动、吊销、`SharedSession` 重连与每 6 小时刷新时原子写出 CRL 并通过 `$CONTROL/hvac-crl/v1` 触发即时踢连；同步打通本地环境、生产零拉取发布及双 CA 集成测试。
