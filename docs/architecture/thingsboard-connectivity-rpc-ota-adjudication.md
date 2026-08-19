# ThingsBoard CE 接入协议、网关、RPC 与 OTA 裁决

状态：`D03_ADJUDICATION_COMPLETE`

审查票：[审查接入协议、网关、RPC 与 OTA](https://github.com/SwayingWindmill/HVAC_web/issues/234)

本文只裁决 ThingsBoard CE v4.3.1.1 的设备接入协议、Transport、设备凭据、Provisioning、Claiming、Gateway 子设备会话、Attributes、Presence、RPC 和 OTA。本文不把 ThingsBoard 或 HVAC Web 预设为正确实现，也不授权在本审查票内改变运行时产品行为。

裁决词汇：

- `ADOPT`：行为和边界可直接成为目标设计；
- `ADAPT`：吸收模式，但按 HVAC Domain、安全和现有数据权威重做；
- `KEEP`：本地实现有明确场景或安全证据，应保留；
- `REPLACE`：本地或上游行为存在实质冲突，应由目标行为替换；
- `REJECT`：明确不进入本项目；
- `DEFER`：有潜在价值，但当前没有设备清单或产品证据支持实施。

## 1. 执行结论

HVAC Web 不应重新引入 ThingsBoard 运行时，也不应为了“协议数量多”复制一套通用 IoT 平台。固定源码反向审查后的客观结论是：

1. **保留本地 Command 安全内核。** 本地 Command Intent、IAM/审批、幂等、单设备 Fence、只允许发送前重试、`OUTCOME_UNKNOWN` 和独立 reported-state 验证，明显强于 ThingsBoard 通用 RPC 的“请求/响应”语义，并与 HVAC Edge Control Arbiter、Lease/Expiry 和安全联锁要求一致。
2. **吸收 ThingsBoard 的统一 Transport 边界。** MQTT、HTTP、CoAP、LwM2M、SNMP 不应各自直连业务域；协议适配器应统一进入 transport-neutral Session、Ingress、Attribute 和 Command Port。当前 MQTT Adapter 直接调用 Telemetry Runtime，且 Command Worker 由 MQTT Telemetry Adapter 进程装配，职责和故障域仍需拆清。
3. **吸收 Gateway 子设备会话生命周期，但拒绝隐式创建设备。** ThingsBoard 的每子设备 Session、Connect/Disconnect、异步订阅、限流、错误映射值得采用；按设备名自动创建 Device/Profile 不符合本项目 Registry 权威和现场资产绑定要求。
4. **建立安全 Provisioning/凭据生命周期。** 当前项目只有静态 Gateway Scope 与 Device Binding，没有产品级 Enrollment、Credential Rotation、Revocation 和审计管理面。ThingsBoard 的 Provision 状态和 Profile 策略值得参考，但共享 Profile Secret、明文 MQTT Password、URL Access Token、自动创建设备均不能照搬。
5. **OTA 是真实缺口，但 ThingsBoard OTA 只能作为产品流程参考。** Package、Device/Profile Assignment、批次和状态机值得吸收；仅校验 Checksum、由设备自报 `VERIFIED/UPDATED`，且缺少源码可见的签名信任、Anti-rollback、Attestation 和 Last-known-good 语义，不足以成为 HVAC 生产升级安全边界。
6. **CoAP/LwM2M/SNMP/Sparkplug 暂缓实现。** 当前仓库没有这些协议的运行时实现，Phase 1 Edge 明确优先 Modbus TCP/RTU/MQTT。先冻结可插拔协议契约；只有真实设备清单、厂商协议和运维成本证据出现后再实施。

当前 D03 不能宣称完成：本地 MQTT/Command 主路径已有扎实的幂等和证据边界，但仍存在无限重试导致队头阻塞、Connector 重启/多实例相关状态只在内存中、静态 Binding 缺少生命周期、Edge Control Cycle 测试失败，以及 OTA/Provisioning/多协议完全缺失等实质差距。

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

上游行为以固定提交的源码、测试、DDL 和配置为准；官方文档用于解释公开产品入口，不覆盖源码事实。

主要上游源码入口：

- [TransportService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/transport/transport-api/src/main/java/org/thingsboard/server/common/transport/TransportService.java) 与 [DefaultTransportService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/transport/transport-api/src/main/java/org/thingsboard/server/common/transport/service/DefaultTransportService.java)；
- [AbstractGatewaySessionHandler](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/transport/mqtt/src/main/java/org/thingsboard/server/transport/mqtt/session/AbstractGatewaySessionHandler.java) 与 [DefaultTransportApiService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/transport/DefaultTransportApiService.java)；
- [DeviceCredentials](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/security/DeviceCredentials.java)、[BasicMqttCredentials](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/device/credentials/BasicMqttCredentials.java)；
- [DeviceProvisionServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/device/DeviceProvisionServiceImpl.java) 与 [ClaimDevicesServiceImpl](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/device/ClaimDevicesServiceImpl.java)；
- [RpcV2Controller](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/RpcV2Controller.java)、[DefaultTbCoreDeviceRpcService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/rpc/DefaultTbCoreDeviceRpcService.java) 与 [RpcEntity](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/model/sql/RpcEntity.java)；
- [OtaPackageController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/OtaPackageController.java)、[DefaultOtaPackageStateService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/ota/DefaultOtaPackageStateService.java)、[BaseOtaPackageService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/ota/BaseOtaPackageService.java)；
- 官方说明：[Command and control](https://thingsboard.io/docs/user-guide/command-and-control/)、[OTA updates](https://thingsboard.io/docs/user-guide/ota-updates/)、[Provisioning](https://thingsboard.io/docs/reference/http-api/provisioning/)、[Claiming](https://thingsboard.io/docs/user-guide/claiming/)、[Gateway API](https://thingsboard.io/docs/paas/reference/gateway-api/overview/)。

主要本地证据：

- `services/mqtt-telemetry-adapter/internal/adapter/runtime.go`, `processor.go`, `envelope.go`, `gateway_messages.go`, `runtime_client.go`；
- `services/telemetry-runtime-service/internal/telemetry/ingest_store.go`, `source_server.go`, `command_verifier_server.go`；
- `services/command-service/pkg/commandservice/service.go`, `connector_evidence.go`, `verification.go`；
- `services/command-dispatcher/pkg/mqttconnector/connector.go`；
- `contracts/ownership/s2-telemetry-ownership.v1.json`, `s3-command-ownership.v1.json`；
- `docs/architecture/phase1-overall-architecture.md`, `backend-architecture-v2-conformance.md`；
- `contracts/architecture/edge-control-plane.v1.json` 与 `libs/edgecontrol`。

## 3. 参考项目功能与它解决的问题

### 3.1 Transport 与协议接入

ThingsBoard 用一个公共 Transport API 把 MQTT、HTTP、CoAP、LwM2M、SNMP 的协议差异转换为统一内部消息，集中处理认证、Session、Telemetry、Attributes、RPC、限流和 Rule Engine 投递。它解决的是“多个现场协议如何共享同一设备域和处理链”的问题。

其固定版本覆盖：

- MQTT 3.1/3.1.1/5、QoS 0/1、Gateway 和 Sparkplug B；
- HTTP 设备 API；
- CoAP JSON/Protobuf、DTLS/X.509、Observe 与低功耗会话；
- LwM2M Object/Instance/Resource、Read/Write/Execute/Observe；
- SNMP v1/v2c/v3 的轮询、GET/SET、Telemetry/Attribute/RPC 映射。

ThingsBoard 的价值不是协议列表本身，而是协议适配器不拥有 Device 业务事实。该模式与本项目“Protocol Bridge 负责连接、重试、轮询和协议任务调度；Point 是 Cloud 权威，Channel 是 Edge 运行时值”一致。

### 3.2 Gateway 与子设备

ThingsBoard 允许一个已认证 Gateway 代理多个子设备，并为每个子设备建立独立 Session，处理 Connect/Disconnect、Telemetry、Attributes、RPC、Claim 和限流。它解决的是“资源受限或非 IP 设备如何经边缘网关接入”的问题。

但其 `GetOrCreateDeviceFromGatewayRequestMsg` 路径按 `deviceName/deviceType` 查找并可创建 Device，缺少本项目要求的预注册 Asset/Device、精确 Tenant/Site/Gateway Binding 和安装证据。该行为对通用 IoT 上手方便，对 HVAC 现场资产治理过于宽松。

### 3.3 Provisioning、凭据与 Claiming

ThingsBoard 的 Device Profile 支持禁用、允许新建设备、仅允许预注册设备和 X.509 Chain 四类 Provision 策略；Provision 成功后写一次性状态并返回设备凭据。Claiming 则让设备在 TTL 窗口内被 Customer 领取。

这些功能解决“设备首次获得身份”和“设备所有权转移”两个不同问题。固定源码也暴露出不能照搬的边界：

- Profile Provision Key/Secret 是共享秘密；
- MQTT Basic Password 被序列化进 `credentialsValue`，认证时取出后直接比较；
- `DeviceCredentials.toString()` 包含 `credentialsValue`，存在日志泄露面；
- HTTP Device API 把 Access Token 放在 URL Path；
- Gateway 和部分 Provision 策略允许自动创建设备；
- Claim Secret 可同时为空，且可配置默认全局允许 Claim。

### 3.4 Attributes 与 Presence

ThingsBoard 区分 Client、Shared、Server Attributes，支持设备获取服务端配置和订阅 Shared Attribute 更新；Transport Session 与 Activity 共同驱动连接状态。它解决“云端期望配置如何同步到设备”和“在线状态如何传播”的问题。

本项目目前能接收 state/heartbeat 证据，并由 Telemetry Runtime 基于新鲜度计算 Presence；设备主动上报的负面 Offline 不直接覆盖 Cloud 状态。这个边界更稳健，因为断线设备通常无法可靠报告自身断线。但项目还没有完整的版本化 Desired/Reported Configuration、每子设备 Session Registry 和配置同步回执。

### 3.5 RPC

ThingsBoard RPC 提供 one-way/two-way、轻量内存请求和持久数据库请求。持久 RPC 有 Expiration、Retries、状态查询和重连后投递，状态包括 `QUEUED/SENT/DELIVERED/SUCCESSFUL/TIMEOUT/EXPIRED/FAILED/DELETED`。它解决的是通用“服务端调用设备方法并跟踪响应”。

官方文档明确说明 one-way HTTP 200 只表示请求已发送，不能证明设备处理或执行成功。持久 RPC 能改善离线等待和运维可查询性，但没有 HVAC Command 所需的风险等级、审批、设备能力白名单、单调 Fence、发送前/发送后不确定性区分、Edge Arbiter 约束和独立权威读回。

### 3.6 OTA

ThingsBoard OTA 对 Firmware/Software Package 建模，支持 Package 元数据、Checksum、Device/Profile Assignment、按批遍历设备和 `QUEUED -> INITIATED -> DOWNLOADING -> DOWNLOADED -> VERIFIED -> UPDATING -> UPDATED/FAILED` 状态。它解决的是“发布什么版本、分配给谁、设备报告到哪一步”。

固定源码支持 MD5、SHA-256/384/512、CRC32、Murmur Checksum；未发现 Artifact Signature、Signing Public Key、Trust Root、Anti-rollback、Secure-boot Compatibility、Attestation 或 Last-known-good Rollback 的实现。`INITIATED` 之后的状态主要由设备报告，因此这是发布协调和可视化流程，不等于可信升级证明。

## 4. Domain 模型裁决

### 4.1 ThingsBoard 参考模型

```text
DeviceProfile
  ├─ TransportConfiguration
  ├─ ProvisionConfiguration
  └─ FirmwareId / SoftwareId

Device
  ├─ DeviceCredentials
  ├─ Gateway/Session
  ├─ Client/Shared/Server Attributes
  ├─ PersistentRpc
  └─ FirmwareId / SoftwareId

OtaPackage
  └─ type + title + version + tag + url/file + checksum
```

该模型覆盖面完整，但把 Transport、Provision、配置、RPC 和 OTA 都围绕通用 Device/Profile 聚合，容易让 Profile 变成过大的跨域配置容器。

### 4.2 HVAC Web 目标模型

```text
Registry Authority
  Tenant -> Site -> Asset -> Device -> Point
                        └─ DeviceBinding [integration, external identity, validity]

IoT Runtime
  IntegrationInstance
  TransportProfile
  CredentialRef
  Enrollment
  GatewaySession -> ChildSession
  IngressEnvelope -> IngressReceipt

Control Authority
  CommandIntent -> CommandAttempt -> DispatchEvidence -> VerificationEvidence

Configuration
  DesiredConfiguration [revision, expiresAt]
  ReportedConfiguration [revision, observedAt, evidence]

OTA Authority
  Artifact -> Release -> DeploymentCampaign -> DeviceUpdateAttempt
```

目标模型必须保持以下不变量：

- Transport Session 不能创建 Registry Device/Point；
- `CredentialRef` 只保存 Secret Manager 引用、Hash 或 Certificate Fingerprint，不保存可恢复明文；
- Enrollment 只能绑定一个预注册 Device、Site 和允许的 Gateway/Hardware Identity，并且一次性、短 TTL、可审计；
- Shared/Server Attribute 不能成为无版本的第二套业务配置；它只是 Desired Configuration 的 Transport Projection；
- Command Transport ACK 不能把 Command 标为业务成功；
- OTA Device Status 是证据输入，不是最终可信事实。

## 5. 核心流程与映射

### 5.1 Telemetry/State 接入

```text
Protocol Adapter
  -> authenticate Integration/Credential
  -> resolve exact Tenant/Site/Gateway/Device Binding
  -> decode bounded, versioned envelope
  -> normalize source position and event time
  -> common Ingress Port
  -> Telemetry Runtime transaction
  -> dedup / quarantine / out-of-order decision
  -> PostgreSQL evidence + Outbox
  -> ClickHouse history / Redis latest / Presence
```

本地已经实现严格 Topic Scope、1 MiB Body、未知字段拒绝、每 Envelope/Device/Point 上限、重复 Point 拒绝、Source Position、原子 Dedup/Quarantine/Outbox、乱序历史保留且不回退 Latest。这些是 `KEEP`。

需要从 ThingsBoard 吸收的是协议无关入口、统一 Session/Auth/Quota 和协议错误映射。不能让每个新协议复制一套 Telemetry 业务判定。

### 5.2 Enrollment 与凭据轮换

```text
Admin pre-registers Device + Binding
  -> issue one-time Enrollment Challenge
  -> device proves hardware/certificate identity
  -> atomically consume Enrollment
  -> issue/activate CredentialRef
  -> establish Session
  -> rotate/revoke with overlap policy and audit
```

默认不允许 self-chosen Device Name、Gateway 自动创建 Registry 记录或共享 Profile Secret 批量创建设备。证书链接入必须校验证书用途、信任链、吊销和预期硬件绑定，不能只从 CN 正则提取名称后创建设备。

### 5.3 Gateway 子设备会话

```text
Gateway Session authenticated
  -> child connect request
  -> resolve active GatewayChildBinding
  -> create in-memory session projection from durable binding
  -> apply per-gateway and per-child quotas
  -> route telemetry/attributes/command replies by immutable child identity
  -> disconnect/expiry removes session, not Registry identity
```

Gateway 断线必须使其子 Session 进入明确状态；Gateway 重连后应重建 Session Projection。一个 Gateway Credential 被攻破的爆炸半径必须限制在绑定 Site/Child 集合内。

### 5.4 Command

```text
Command Intent
  -> authorization / capability / risk / approval / idempotency
  -> durable Attempt + lease + monotonic fence
  -> connector prepares evidence
  -> publish through transport
  -> ACK is transport evidence only
  -> Edge Arbiter computes effectiveValue
  -> independent reported-state verification
  -> SUCCEEDED or OUTCOME_UNKNOWN
```

ThingsBoard 的持久 RPC 可贡献 `expiresAt`、查询、重连待发和终态清理模式，但不能改变该状态机。对请求已提交但结果不可证明的 Attempt，继续禁止盲目重试。

### 5.5 OTA

```text
Artifact ingest
  -> verify signature, hash, provenance and compatibility
  -> immutable Release
  -> Campaign selects explicit cohort
  -> canary / wave rollout with concurrency limit
  -> Edge downloads and verifies before activation
  -> reboot/health/telemetry attestation
  -> promote, pause or rollback to last-known-good
  -> durable audit and evidence retention
```

OTA 与 Command 共用身份、Tenant/Site Scope、审批、Audit 和 Edge 安全边界，但不应伪装成一个长时 RPC。Artifact、Campaign、DeviceUpdateAttempt 是独立 Domain；大文件进入 Object Storage，PostgreSQL 只保存治理元数据、引用、状态和证据索引。

## 6. 源码级本地反向审查

| 本地模块 | 已证实行为 | 客观裁决 |
| --- | --- | --- |
| MQTT Telemetry Runtime | TLS 1.3 mTLS、QoS 1、Persistent Session、Manual ACK、有限队列、四个精确 Topic | `KEEP` 基线 |
| MQTT Envelope | 严格 Schema/Scope/大小/数量/时间/Sequence/非有限数校验 | `KEEP` |
| Adapter Retry | Permanent Error ACK+Drop；Transient Error 在单 Worker 内无限退避重试 | `REPLACE`：一个持续下游故障会阻塞后续消息，需 Retry Budget、可观测 Parking/DLQ 和背压策略 |
| Telemetry Runtime | Serializable 事务、Partition/Event Lock、Dedup、Quarantine、Outbox、Latest/Presence 原子裁决 | `KEEP` |
| Presence | 正向 Activity 作为证据；设备负面 Offline 不强制覆盖 Cloud，Freshness 决定失联 | `KEEP`，补 Session Registry |
| Gateway Scope | 配置文件中静态 Tenant/Site/Gateway Scope | `ADAPT`：固定站点可用，但缺少注册、轮换、吊销、有效期和管理面 |
| Command Service | Intent/Attempt、审批、幂等 Payload Hash、Lease、Fence、只发送前重试、`OUTCOME_UNKNOWN` | `KEEP` |
| Command Verification | Transport ACK 后独立读取 S2 reported state；只有权威状态匹配才成功 | `KEEP` |
| MQTT Command Connector | mTLS/QoS 1/Persistent Session、静态 Device Binding、准备后发送、旧 Fence 拒绝 | `ADAPT`：核心边界正确，但 Waiter、Result Cache 和最大 Fence 在内存中，重启/多实例恢复证据不足 |
| 进程装配 | MQTT Telemetry Adapter 同时装配 Command Worker | `REPLACE` 生命周期耦合：可留在 Phase 1 `iot-service` deployable，但必须是独立模块、队列、健康状态和故障域 |
| CoAP/LwM2M/SNMP/Sparkplug | 运行时代码静态搜索无实现 | `DEFER`，不得宣称支持 |
| Provisioning/Claiming | 无产品级运行时实现 | Provisioning `P1 ADAPT`；Claiming `REJECT/DEFER` |
| OTA | 无 Package/Release/Campaign/Device Update 运行时实现 | `P1 ADAPT`，是否进入 Phase 1 由产品规范裁决 |
| Edge Control Cycle | 新增 `libs/edgecontrol`，但 Cycle 顺序测试出现 `duration=0s` | `待验证`；在修复并通过前不得作为生产控制完成证据 |

## 7. 最终能力裁决矩阵

| ThingsBoard 参考能力 | 裁决 | 映射到 HVAC Web |
| --- | --- | --- |
| Common Transport Service | `ADOPT` | 建 transport-neutral Ingress/Session/Attribute/Command Port，协议适配器只负责协议语义 |
| MQTT Direct Device | `KEEP/ADAPT` | 保留 mTLS、QoS 1、Persistent Session、严格 Envelope；补 Credential Lifecycle、Quota、Retry Budget |
| MQTT Gateway API | `ADAPT` | 每子设备 Session、Connect/Disconnect、批量和限流；只允许预注册 Binding |
| Gateway 自动创建设备/Profile | `REJECT` | Registry 是唯一身份权威，未知映射进入 Quarantine |
| MQTT Basic 明文 Password | `REJECT` | Secret Manager 引用或不可逆 Hash；日志永不输出 Secret |
| HTTP URL Access Token | `REJECT` | 不作为生产 Field Device 接入；若有 HTTP Adapter，使用 mTLS/短时签名和 Header，不把 Secret 放 URL |
| Device Profile Provision Policy | `ADAPT` | Profile 只表达允许策略；Enrollment 必须一机一凭据、预绑定、一次性、短 TTL |
| X.509 CN 自动创建设备 | `REJECT` | Certificate Identity 必须与预注册 Hardware/Device Binding 匹配 |
| Device Claiming | `REJECT/DEFER` | 当前 B2B HVAC 没有匿名领取/客户转移需求；未来若有则建立独立转移 Saga |
| Client/Shared/Server Attributes | `ADAPT` | Desired/Reported Configuration + Revision + Expiry + ACK；Attributes 仅作传输投影 |
| Activity/Session Presence | `ADAPT` | 保留 Freshness Authority，新增 Gateway/Child Session Registry 和重连恢复 |
| Lightweight RPC | `REJECT` 用于控制 | 可用于无风险诊断且必须白名单；不能绕过 Command Domain |
| Persistent RPC | `ADAPT` | 吸收 Expiry、离线待发、查询、删除和重连模式，不吸收成功语义 |
| Arbitrary Method/Params RPC | `REJECT` | 保持 Capability Profile 与版本化 Payload |
| OTA Package 与 Assignment | `ADAPT` | Artifact/Release + Device/Profile-compatible Targeting |
| OTA 批次和状态机 | `ADAPT` | Campaign/Canary/Wave/Pause/Rollback；设备上报只作为证据 |
| Checksum-only OTA Trust | `REPLACE` | 签名 Manifest、SHA-256 最低线、Trust Root、Anti-rollback、Provenance、Attestation |
| CoAP | `DEFER` | 只有低功耗/UDP 设备清单出现后实现同一 Transport Port |
| LwM2M | `DEFER` | 只有运营商终端/Object Model 需求出现后采用成熟库，不自研协议栈 |
| SNMP | `DEFER` | 只有网络/电力设备明确要求后实现；优先 v3 AuthPriv，拒绝默认 v1/v2c 写控制 |
| Sparkplug B | `DEFER` | 只有现有 Sparkplug 现场生态和 Birth/Death 语义需求出现后实现 |

## 8. 异常与边界处理

### 8.1 Ingress

- Malformed、未知 Schema Major、Scope 冲突、超限和永久 Mapping 错误：负向 Receipt，记录有限脱敏证据，按协议返回明确 Reason；不无限重试，不创建设备。
- 下游暂时不可用：有限 Retry Budget；超限后进入可观测 Parking/DLQ，继续服务其他 Partition/Device，避免单 Worker 队头阻塞。
- Duplicate：幂等 Receipt，不重复更新 Latest、Presence 或 Business Revision。
- Out-of-order：保留历史事实，不回退 Latest。
- Queue Saturation：产生指标、告警和明确 NACK/断流策略；不能静默丢弃或无限占住 ACK。

### 8.2 Session 与 Gateway

- Gateway 重连、Session Expiry、重复 Child Connect 必须幂等；Session Projection 可重建。
- Gateway Credential Rotation 应允许明确的短 Overlap，过期凭据立即拒绝；Revocation 必须使关联 Session 失效。
- Gateway 只能代理 Durable Binding 允许的 Child；跨 Site/未知 Child 进入 Quarantine。
- 每 Gateway、每 Child、每消息和每数据点均需限流，防止一个 Gateway 影响全站点。

### 8.3 Command/RPC

- Publish 前失败可安全释放 Lease 并重试；Broker 接受请求后结果不可证明时进入 `OUTCOME_UNKNOWN`。
- Late Reply、Duplicate Reply、错误 Attempt/Fence/Payload Hash 一律不能推进状态。
- Connector 重启后必须从耐久 Attempt/Evidence 恢复，或由单 Active Owner Lease 明确保证旧实例不能接收/处理 Reply。
- Transport ACK、Device ACK、Edge `VERIFIED` 均不是 Cloud 业务成功；只有独立 reported-state 证据可完成 Command。

### 8.4 OTA

- 离线设备保持 Pending/Expired，不因 Campaign 完成而伪造成功。
- 下载完成但签名失败、版本回退、硬件不兼容、健康检查失败必须阻止激活或触发回滚。
- Device 自报 `UPDATED` 与实际启动版本/健康证据冲突时进入 Inconclusive/Failed，不覆盖证据。
- Campaign 必须支持并发上限、失败阈值、自动暂停、人工恢复和不可变 Target Snapshot。
- Artifact 删除必须受活跃 Release/Campaign 和审计保留约束，不能破坏历史证据。

## 9. 值得吸收的 Pattern

1. 一个 Common Transport Boundary 服务多个协议，而不是复制业务逻辑。
2. Protocol-specific Adapter + Canonical Internal Message + Domain Service 的三层结构。
3. Gateway 维护每子设备 Session，并在 Gateway Disconnect 时一致清理。
4. Provisioning 作为显式一次性状态转换，而不是第一次上报即创建 Registry 身份。
5. Persistent RPC 的 Expiration、Offline Queue、Status Query 和 Reconnect Delivery。
6. Attributes 的 Server-to-device Subscription 模式，但映射为版本化 Desired Configuration。
7. OTA Package、Assignment 和按页/批处理设备的发布协调模式。
8. Transport、Gateway、Message、Data Point 多层配额以及协议级错误码测试。
9. 针对 MQTT v3/v5、Gateway、CoAP、LwM2M、SNMP、Provisioning、Claiming、RPC、OTA 的端到端 Transport 测试结构。

## 10. 不适合本项目的部分

- 明文或可恢复形式保存 MQTT Password、Access Token，并在对象字符串中暴露 Credentials。
- 把 Access Token 放 URL Path，扩大代理日志、Tracing、历史和监控泄露面。
- 一个 Profile Secret 允许大批设备自创建，或仅凭 X.509 CN 推导并创建设备身份。
- Gateway 按名称隐式创建 Child Device/Profile，绕过现场资产安装、Site Scope 和 Registry 审批。
- Secret 可为空、默认全局允许的 Claiming；当前产品没有消费者式“认领设备”模型。
- 任意 Method/Params 的通用 RPC 直接进入现场控制。
- 以发送、送达或设备 ACK 代表 HVAC 控制成功。
- 只用 Checksum 和设备自报状态构成 OTA 信任链。
- 因 ThingsBoard 支持某协议就在本项目实现该协议；没有现场需求的协议只增加攻击面和运维成本。
- 把 Device Profile 扩张成跨 Transport、控制安全、业务配置、OTA 和身份的万能聚合。

## 11. 与项目规范的对齐

| 项目要求 | 当前证据 | 裁决 |
| --- | --- | --- |
| MQTT 仅为 Edge ↔ Cloud Transport | S2/S3 Ownership 均标记 `transport-only` | 保留；RPC/Attribute/OTA 不得让 MQTT 成为业务权威 |
| Point 是 Cloud canonical identity | S2 要求 Adapter 不拥有 Device/Point Identity | 拒绝 Gateway/Provision 自动创建设备和 Point |
| Protocol Bridge 隔离厂家与协议 I/O | Phase 1 架构定义 Driver/Bridge/Channel 边界 | Adopt Common Transport 思路，但 Cloud Adapter 与 Edge Bridge 分域 |
| Cloud Command 是 governed leased intent | S3 有审批、Lease、Fence、独立读回 | 保留，不用通用 RPC 替换 |
| Cloud 不能绕过 Edge Safety/Interlock | Edge Control Plane 明确 Arbiter 决定 `effectiveValue` | Transport 只交付 Intent，不直接持有最终 Actuator Decision |
| Cloud 失联后 Edge 继续安全运行 | Edge 本地 Controller/Schedule/Timedata | Session/RPC/OTA 不能阻塞 IPO Cycle；协议 I/O 异步 |
| Simulator 与真实设备路径同构 | 只允许 Driver/Protocol/Physical Model 不同 | 现有 Edge Cycle 测试失败，当前仅 `PARTIAL` |
| Object Storage 保存大对象 | 现有基线冻结 provider-neutral Object Storage | OTA Artifact 使用 Object Storage；PostgreSQL 不存大包兼容回退 |
| Phase 1 优先 Modbus TCP/RTU/MQTT | Phase 1 Bridge 清单明确 | CoAP/LwM2M/SNMP/Sparkplug `DEFER` |

## 12. 实施优先级与验收门槛

本节冻结后续实现顺序，本审查票不修改运行时。

### P0：现有 MQTT/Command 可信闭环

1. 将 MQTT Telemetry Ingress 与 Command Worker 拆成同一 `iot-service` 内独立模块、队列、健康状态和故障域；任一侧故障不得拖垮另一侧。
2. 为 Telemetry Adapter 增加有限 Retry Budget、按 Partition/Device 隔离、可观测 Parking/DLQ、Queue Saturation 策略和恢复工具，消除单消息无限重试导致的队头阻塞。
3. 让 MQTT Command Reply Correlation、Attempt Result 和 Fence 在 Connector 重启/多实例切换后可从 Command Authority 恢复；或实现明确、可证明的 Single Active Connector Ownership Lease。
4. 建立 `CredentialRef`、Rotation、Revocation、Expiry、Session Invalidation 和 Audit 契约；任何日志和数据库业务表不得保存/打印设备明文 Secret。
5. 修复 `libs/edgecontrol` Cycle Duration/Clock 证据失败，并补重启、Stale Input、Write Rejected、Interlock 和 Command Lease Expiry 测试。

验收门槛：持续下游错误不能阻塞无关设备；Connector 在 Publish 前后各故障点重启后不重复执行；旧 Fence 永远不能推进；Transport/Edge ACK 永远不能单独完成 Command；Edge Cycle 全部直接测试通过。

### P1：Transport、Provisioning、Gateway 与 OTA 基础

1. 定义 transport-neutral `SessionPort`、`IngressPort`、`DesiredConfigPort` 和 `CommandTransportPort`；先由 MQTT 迁移并保持端到端工作，再增加其他协议。
2. 建立 Durable `IntegrationInstance`、`TransportProfile`、`DeviceBinding` 和 `GatewayChildBinding`；静态配置只作为部署输入，不再是唯一管理面。
3. 实现 Pre-provisioned Enrollment：一次性、短 TTL、Hash-at-rest、精确 Device/Site/Gateway/Hardware Scope、原子消费、失败审计；默认禁止自动创建 Registry 对象。
4. 建立 Gateway/Child Session Registry、每层配额、重连恢复、Credential Rotation/Revocation 和 Blast-radius 测试。
5. 建立版本化 Desired/Reported Configuration；支持 Revision、Expiry、订阅、ACK、冲突与过期处理。
6. 若产品规范确认 OTA 进入当前阶段，实现 Signed Artifact/Release/Campaign/DeviceUpdateAttempt 最小纵切：Object Storage、签名 Manifest、SHA-256、Compatibility、Canary/Wave/Pause、Last-known-good 与独立健康证据。

验收门槛：未知 Device/Gateway 不会被任何 Transport 自动创建；Enrollment 只能成功一次；Credential 吊销能终止 Session；Desired Configuration 可证明已应用的 Revision；OTA 没有有效签名或 Anti-rollback 证据时不能激活。

### P2：按现场需求扩展协议

1. 根据实际 BOM、厂商协议矩阵、网络拓扑和设备数量选择 CoAP、LwM2M、SNMP 或 Sparkplug，不以平台对标数量作为需求。
2. 每增加一个协议，复用同一 Domain Port、Credential Lifecycle、Quota、Quarantine、Presence、Command 和审计语义。
3. 优先复用维护良好的协议库，并固定版本/Commit；禁止自研完整 LwM2M、SNMP 或 Sparkplug 协议栈。
4. Claiming 只有在出现真实多客户所有权转移场景后才立项，并使用显式 Tenant Admin、一次性高熵 Secret Hash、短 TTL、Device/Site 归属校验和耐久审计。

验收门槛：每个宣称支持的协议必须有真实 Runtime Wiring、认证失败、重连、重复/乱序、限流、离线 Command 和跨 Tenant/跨 Site 拒绝的端到端测试；只有 Schema 或示例代码不算实现。

## 13. 文档与源码冲突

1. ThingsBoard 的产品文档常把 RPC `SUCCESSFUL` 或 OTA `VERIFIED/UPDATED` 作为用户可见状态；固定源码和官方 RPC 说明同时表明，one-way 200 只代表发送，OTA 的后半段状态主要来自设备上报。本文不把这些状态提升为 HVAC 业务真相。
2. 本地 `backend-architecture-v2-conformance.md` 把 `iot-service` 定义为 MQTT 上行、Command Dispatch/Verification 的 Phase 1 合并 Deployable；源码当前仍由 `mqtt-telemetry-adapter` 进程装配 Command Runtime。本文不要求重新拆出更多 Deployable，但要求在同一 Deployable 内分离模块、队列、Health 和故障域。
3. 本地 `s3-command-ownership.v1.json` 仍列出已淘汰的 `:approve` 路由，而 `backend-architecture-v2-conformance.md` 声明生产只接受 `/approve`。这是现有契约漂移，不由 RPC 参考实现解决，后续实现票必须删除旧路径而不是加兼容层。
4. Phase 1 架构已声明“仅 MQTT Telemetry、MQTT Command 和 Persistent Queue 不足以证明生产级 Edge 完成”；因此本轮不得用已有 MQTT 主路径掩盖 Edge Control Cycle 的失败测试。

## 14. 本轮最终裁决

- 本地 Telemetry Dedup/Quarantine/Out-of-order、Command Governance/Fence/Lease/`OUTCOME_UNKNOWN`/独立读回获得 `KEEP`。
- ThingsBoard 的 Common Transport、每子设备 Session、Transport Quota、Provision 状态、Persistent RPC 运维语义、Attributes Subscription 和 OTA Campaign 思路获得 `ADOPT/ADAPT`。
- ThingsBoard 的明文 Credentials、URL Token、共享 Secret 自动创建、空 Secret Claim、Gateway 隐式创建设备、任意 RPC 控制和 Checksum-only OTA Trust 获得 `REJECT/REPLACE`。
- 本地无限 Transient Retry、Connector 内存状态、静态 Binding/Scope、进程内生命周期耦合、缺少 Credential Lifecycle/Provisioning/OTA/Session Registry，以及 Edge Cycle 失败测试，均是必须承认的差距。
- CoAP、LwM2M、SNMP 和 Sparkplug 仅冻结 Adapter Boundary，按真实现场需求 `DEFER`。

该裁决完成 D03 对比，不完成整个 ThingsBoard 全功能审查；其余审查域与最终跨域反向审查仍按总路线图继续。

## 15. 本轮验证结果

执行日期：2026-08-17。

| 验证 | 结果 |
| --- | --- |
| CodeGraph 对 MQTT Adapter、Telemetry Runtime、Command Service/Dispatcher 的源码级调用链审查 | 完成 |
| 静态搜索 CoAP/LwM2M/SNMP/Sparkplug/OTA/Provisioning 运行时实现 | 无匹配；不得宣称支持 |
| `go test ./...` in `services/command-service` | 通过 |
| `go test ./...` in `services/command-dispatcher` | `pkg/commanddispatcher` 通过；`pkg/mqttconnector` 因 `proxy.golang.org` 下载 `paho.golang v0.23.0` 超时而未验证 |
| `go test ./...` in `services/mqtt-telemetry-adapter` | 因同一 Paho 依赖下载超时而未验证，不判为代码通过或失败 |
| `go test ./...` in `libs/edgecontrol` | 失败：`TestCyclePhasesFollowProcessControllersWriteOrder` 得到 `phases=7 duration=0s` |

依赖下载超时属于环境阻断，不能作为代码缺陷，也不能算通过。`edgecontrol` 是真实测试失败；在修复前，当前 Edge Control 实现继续标记为“待验证”。
