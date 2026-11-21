# Mosquitto 网关 CRL 校验与实时踢连插件

`mosquitto_hvac_crl.so` 是面向 Eclipse Mosquitto 2.1.2（Plugin API v5）的轻量 C 插件，用于在 MQTT Broker 入口层直接拦截已吊销网关证书，并在平台吊销单张证书或整台网关时实时断开现有长连接。

## 设计要点

- **双 CA 安全隔离**：`gateway-ca.pem` 挂载了平台内部工作负载 CA 与网关 CA 的联合信任束；插件仅当客户端证书的颁发者名称（Issuer DN）与已验签网关 CRL 的颁发者一致时才校验序列号，不影响 `mqtt-telemetry-adapter` 与 `command-dispatcher` 等内部工作负载连接。
- **签名先验后切（Fail-Safe）**：每次加载或重载 `gateway-ca.crl.pem` 时先按 `cafile` 中的对应 CA 公钥执行 `X509_CRL_verify`，解析失败、空文件、未授权签名或文件意外消失时保留上一份有效 CRL。
- **单证精准踢连与轮换共存**：跟踪活动客户端的 `client_ptr`、`clientid`、`username`、颁发者/序列号及证书 SHA-256 指纹；执行单证吊销时仅踢出匹配指纹的旧证书连接并检查 CRL，同一网关下持有新轮换有效证书的连接保持不断流。
- **双通道触发**：
  1. **即时通道**：`connectivity` 导出更新后的 CRL 并向受 ACL 保护的控制主题 `$CONTROL/hvac-crl/v1` 发布 JSON 指令（`reload` / `reload_and_kick`）；
  2. **文件轮询兜底**：`MOSQ_EVT_TICK` 每秒检查 `crlfile` 的 `stat`（纳秒级 `mtime`、inode 与大小），即使未收到控制报文也会自动重载 CRL 并踢除违规连接。

## 插件配置项（`mosquitto.conf`）

| 选项 | 默认值 | 说明 |
| --- | --- | --- |
| `plugin_opt_cafile` | `/mosquitto/config/gateway-ca.pem` | 客户端 CA 联合信任证书路径 |
| `plugin_opt_crlfile` | `/mosquitto/config/crl/gateway-ca.crl.pem` | `connectivity` 原子写出的网关 CRL PEM 路径 |
| `plugin_opt_control_user` | `mqtt-telemetry-adapter` | 允许操作 `$CONTROL/hvac-crl/v1` 的内部身份用户名 |
| `plugin_opt_poll_seconds` | `1` | CRL 文件变化轮询兜底周期（秒） |
