# 接入服务（Connectivity）

一个接入服务进程服务所有租户、站点和网关（ADR 0015）。

```text
站点网关 --MQTT v5 QoS 1，双向 TLS--> Mosquitto --> 接入服务 --双向 TLS--> 遥测
```

## 上行

网关证书的 CN 就是它在登记里的设备 id。网关发到：

```text
hvac/v1/{网关 id}/up/telemetry
hvac/v1/{网关 id}/up/event
```

消息格式见 `contracts/mqtt/gateway-uplink.v2.schema.json`。消息里不带租户和站点。

每条消息，接入服务：

1. 在登记的网关目录（`core_registry.gateway_directory_v1`）里找到这台网关，并确认它有有效的网关证书；
2. 在网关所属的租户里，按网关里的设备名（`gateway_device_source_keys_v1`）找到每台设备，按点位代码（`point_bindings_v1`）找到每个点位；
3. 把找到的租户、站点、设备和点位连同每个值发给遥测。没登记的设备或点位也照发，由遥测隔离并留下记录。

来自未知网关、来自没有有效证书的网关，或者解析不了的消息，写进 `connectivity.uplink_quarantine` 并确认收到。

## 控制指令

`COMMAND_RUNTIME_IN_PROCESS_ENABLED=true` 时，接入服务还通过一条 MQTT 连接（身份 `command-dispatcher`）为所有租户下发并核对控制指令：发到 `hvac/v1/{网关 id}/down/command`，从 `hvac/v1/+/up/reply` 读所有回复；回复属于哪个租户，以登记的网关目录为准。

指令发到哪里，在指令所属的租户里从登记读出。发出之前，下面几种情况直接拒绝：

| 代码 | 什么时候 |
| --- | --- |
| `COMMAND_ROUTE_NOT_FOUND` | 设备不在任何有效网关后面（`gateway_device_source_keys_v1`） |
| `COMMAND_CONTROL_DISABLED` | 指令要写的点位不是这台设备有效的可写点位（`point_bindings_v1`） |
| `COMMAND_GATEWAY_CREDENTIAL_INACTIVE` | 网关没有有效的网关证书 |

发给网关时，设备用网关里的设备名来指定。指令功能打开了却启动不了时，进程直接退出。

## 顺序和积压

每台网关有自己的内存队列与处理线程，第一条消息到达时创建。接入服务默认通过 MQTT v5 共享订阅（`$share/connectivity/hvac/v1/+/up/telemetry` 与 `$share/connectivity/hvac/v1/+/up/event`，由 `CONNECTIVITY_MQTT_SHARE_GROUP` 配置）读取上行消息；每条接收到的消息先按到达顺序追加写入该网关独立的 WAL 磁盘缓冲区（`CONNECTIVITY_OVERFLOW_DIR`，受 `CONNECTIVITY_OVERFLOW_MAX_MESSAGES` 与 `CONNECTIVITY_OVERFLOW_MAX_BYTES` 双上限控制），写盘落袋后立即向 MQTT 发送 PUBACK，同时保持最多 `CONNECTIVITY_QUEUE_CAPACITY` 条热数据在内存中供处理线程零磁盘读取消费，确保单台网关卡死既不阻塞共享读循环，也不会因 broker inflight 窗口堵死其他健康网关。内存与磁盘缓冲区都装满时拒绝新消息、不发 ACK 并主动断开重连（由 MQTT 服务器在持久会话内保留并重传），同时递增 `hvac_mqtt_gateway_overflow_shed_total{gateway_id}`。`hvac_mqtt_gateway_queue_depth{gateway_id}` 显示内存与磁盘待处理总条数，`hvac_mqtt_gateway_overflow_depth{gateway_id}` 显示磁盘溢出条数。处理线程在每批消息成功下发遥测或隔离后推进 WAL 消费游标并在空闲或达阈值时截断回收磁盘空间；临时失败原地重试，所以同一台网关的消息不会乱序。

断线和重连：尚未落盘确认的消息都由 MQTT 服务器在下次连接时重发；已落盘 WAL 的消息在进程带持久 `CONNECTIVITY_OVERFLOW_DIR` 重启后会自动从消费游标恢复并继续按严格先进先出顺序投递。断线时日志记 `mqtt_uplink_connection_lost`，`/health/ready` 立刻变成 503，重新订阅成功后恢复。

## MQTT 服务器

`infra/telemetry/mqtt/acl` 用固定的规则把每台网关限制在 `hvac/v1/{它的 CN}/up/#` 和 `hvac/v1/{它的 CN}/down/#`，所以加网关不用改 MQTT 服务器的配置。

## 配置

环境变量，默认值适用于第一阶段的 Compose 网络：

| 变量 | 默认值 |
| --- | --- |
| `CONNECTIVITY_MQTT_URL` | `tls://mqtt-broker:8883` |
| `CONNECTIVITY_MQTT_CLIENT_ID` | `connectivity` |
| `CONNECTIVITY_MQTT_SHARE_GROUP` | `connectivity`（设为 `off`/`none` 或空串则关闭共享订阅前缀） |
| `CONNECTIVITY_OVERFLOW_MAX_MESSAGES` | `max(QueueCapacity*16, 4096)`（上限 `1048576`） |
| `CONNECTIVITY_OVERFLOW_MAX_BYTES` | `33554432`（32 MiB/网关未消费 WAL 上限，最大 1 GiB） |
| `CONNECTIVITY_OVERFLOW_DIR` | 空（本地默认建并在退出清理临时目录；Phase 1 Compose 挂载 `/var/lib/hvac/connectivity-spool` 持久卷） |
| `CONNECTIVITY_TELEMETRY_URL` | `https://telemetry-runtime-service:8446` |
| `CONNECTIVITY_TLS_CERT` / `CONNECTIVITY_TLS_KEY` | `/run/hvac/pki/mqtt-telemetry-adapter/tls.*` |
| `CONNECTIVITY_CA` | `/run/hvac/pki/ca.crt` |
| `CONNECTIVITY_DATABASE_URL` | 必填 |
| `COMMAND_RUNTIME_IN_PROCESS_ENABLED` | 默认关；`true` 打开控制指令功能 |
| `COMMAND_DISPATCHER_CERT` / `COMMAND_DISPATCHER_KEY` | `/run/hvac/pki/command-dispatcher/tls.*`（MQTT、指令服务、下发安全检查） |
| `COMMAND_VERIFIER_CERT` / `COMMAND_VERIFIER_KEY` | `/run/hvac/pki/command-verifier/tls.*`（指令服务、读回状态） |
| `COMMAND_RUNTIME_CA` | `/run/hvac/pki/ca.crt` |
| `COMMAND_RUNTIME_URL` / `COMMAND_RUNTIME_SERVER_NAME` | `https://command-service:8447` / `command-service` |
| `COMMAND_TELEMETRY_URL` / `COMMAND_TELEMETRY_SERVER_NAME` | `https://telemetry-runtime-service:8446` / `telemetry-runtime-service` |

## 测试

```text
(cd modules/connectivity && go test ./...)
node scripts/run-s1-registry-postgres-tests.mjs   # 含接入服务的数据存取和登记读取接口
```
