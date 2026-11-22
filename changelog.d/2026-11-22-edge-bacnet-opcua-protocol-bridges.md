---
date: 2026-11-22
category: 新增
---

- 新增纯 Go 南向 **BACnet/IP**（ASHRAE 135 Annex J UDP：设备发现 `Who-Is`/`I-Am`、属性读写、`SubscribeCOV` 异步通知与即时读回核验）与 **OPC-UA**（IEC 62541 二进制 TCP 通道、支持 `None` 及 `Basic256Sha256` `Sign`/`SignAndEncrypt` 实时密钥协商与报文校验、节点读写与订阅断线重连）协议桥及声明式适配器（`libs/edgecontrol`）。
- 扩展云边统一驱动模型 `ValidateSouthboundDriverInput` 与硬实时安全边界（`EDGE-003`）校验，`connectivity` 网关配置影子、`registry` 设备模板激活（支持 `driver` / 设备标签 `bacnet_endpoint` / `bacnet_device_instance` / `opcua_endpoint`）及 `edge-gateway` 守护进程热加载池化连接桥。
- 配套纯 Go 协议级仿真服务端 `VirtualBACnetServer` 与 `VirtualOPCUAServer`（对齐 `SIM-004`），并补齐协议级发现、读写校验、故障隔离与重连回归测试。
