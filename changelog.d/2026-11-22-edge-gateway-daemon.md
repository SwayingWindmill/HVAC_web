---
date: 2026-11-22
category: 新增
---
- 新增面向 Linux 边缘网关的独立守护进程 `cmd/edge-gateway` 与 ARMv7/`systemd`/容器部署模板，集成 `libs/edgecontrol`、共享串口总线 Modbus RTU/TCP（预留 BACnet 桥接扩展点）、分级磁盘断点续传队列、配置影子热更新、14 天证书自动续签、受控反向隧道客户端与支持断点续传/SHA-256/Ed25519 校验及启动自检自动回滚的 A/B OTA 代理；同时修复 `modules/connectivity/pkg/tunnel` 收到 `FrameOpenErr` 时 `Stream.resolveOpen` 递归调用 `sync.Once` 导致的会话读循环死锁。
