# ThingsBoard CE Edge、同步、离线与远程配置裁决

Status: `D08_ADJUDICATION_COMPLETE`

Date: 2026-08-18

Issue: #241

References:

- ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`
- ThingsBoard Edge `v4.3.1.1`, commit `04d9daf4a557a13483de2310e35b6493aff751fc`

## 1. 执行结论

本轮不假设 HVAC Web 的现有 Edge 实现正确，也不把 ThingsBoard Edge 当成目标实现。结论来自固定版本 Cloud/Edge 源码、上游测试和迁移、当前官方文档、本地 CodeGraph 反向审查，以及 HVAC 现场控制和安全边界。

核心裁决如下：

- **保留本地 HVAC Edge 控制内核，而不是换成 ThingsBoard 的通用 IoT Edge。** `Channel -> Process Image -> Controller/Scheduler -> Output`、能力约束、现场联锁、命令租约和读回，比 ThingsBoard 的通用实体/Rule Engine 模型更适合设备级 HVAC 闭环。硬实时保护仍由 PLC、设备保护和安全硬件拥有。
- **承认本项目当前没有完整云边同步平面。** 已有 Edge Manifest 只是运行时自描述快照；`FileTimedata` 明确不拥有云端重传游标；MQTT File Queue 只保证已物化 Packet 的传输持久性。三者没有构成 Desired/Observed 配置协调、可恢复全量同步、增量事件日志、Tombstone、远程发布或升级回滚。
- **吸收 ThingsBoard 的同步骨架。** 包括独立 Edge 身份和连接会话、版本握手、Cloud/Edge 双向类型消息、耐久 Cloud/Edge Event 队列、全量 Bootstrap 与增量 Tail 两条路径、显式 ACK、源 Edge 回声抑制、分配驱动投影、连接统计和丰富的实体级集成测试。
- **替换 ThingsBoard 固定版本的可靠性语义。** 其全量 Sync Cursor 只存在内存中，Wire Protocol 只有 `fullSync` 布尔值；上下行普通消息三次失败后可丢弃并推进 Cursor；转换异常可跳过事件后推进整页；高优先级内存队列满后删除最旧事件；Cloud Event 默认一个月 TTL；Timeseries 失败可无限重试造成队头阻塞。这些行为不适合控制配置、告警证据和审计数据。
- **替换 ThingsBoard 的身份和冲突语义。** 固定源码使用 Routing Key + 静态 Secret，默认 gRPC TLS 关闭，认证失败信息包含客户端提交的 Secret，连接配置又会下发 Secret；实体更新会丢弃远端 Version，重名设备可被 Edge 自动改名。目标改为 mTLS Workload Identity、一次性 Bootstrap、独立 Secret Rotation、单一字段所有者、Source Revision、Tombstone 和显式 Conflict，禁止静默改名和最后到达者覆盖。
- **继续使用 MQTT over TLS 作为云边传输。** 不因 ThingsBoard 使用 gRPC 而替换本地传输。采用的是协议行为：类型化 Envelope、双向 ACK、Snapshot/Delta、可恢复 Cursor、版本门禁、大小协商和连接状态，而不是其 Java/gRPC 实现。
- **远程配置采用签名不可变 Release，不采用通用实体双向复制。** Cloud 拥有 Registry、Profile、Policy、Rule、Schedule 和 Release；Edge 只应用已发布、签名、兼容且通过本地安全校验的 Desired Revision，并上报 Observed Revision、Manifest Digest、执行证据和拒绝原因。
- **不保留旧协议兼容层。** ThingsBoard 通过长期 `EdgeVersion` 条件分支和 deprecated Proto 字段兼容旧节点；本项目按仓库规则使用显式 Package/Schema Compatibility Gate、受控升级和整体回退，不长期携带旧路径、双写或 fallback。

本文件完成架构裁决，不表示 P0/P1 缺口已经实现，也不表示当前 EG8200 Simulator 已是可部署的生产 Edge Runtime。

## 2. 范围与跨域边界

本轮逐项覆盖 D08 的 8 项能力：

1. Edge 实体、凭据、Provisioning、连接状态与升级；
2. Device、Asset、Profile、Rule、Dashboard、Entity View 和 OTA Assignment；
3. Edge Event Queue、Cursor 和 Change Delivery；
4. Session、Full/Incremental/Resumable Sync；
5. Cloud/Edge Data、Configuration、Alarm 和 RPC Flow；
6. Offline Operation、Recovery、Priority 和 Backpressure；
7. Edge Local Rule Engine、Notification 和 UI Boundary；
8. Conflict、Delete、Duplicate、Ordering 和版本兼容。

跨域归属如下：

- D02 Registry 拥有 Cloud Master Data、Profile、关系和生命周期；D08 拥有这些权威对象在 Edge 的只读 Projection、Release Manifest 和 Reconciliation。
- D03 拥有设备 Provisioning、Device Credential、RPC 业务语义和 OTA Campaign；D08 拥有 Edge 身份、连接、离线、同步、下载、现场安装门禁和结果证据。
- D04 拥有 Cloud Telemetry/History/Calculated Field；D08 拥有本地 Timedata、Store-and-Forward、Replay Cursor 和 Offline Capacity。
- D05 拥有 Cloud Rule Engine 语义和完整 Rule Engine 裁决；D08 只定义离线本地执行、Rule Release 和现场安全边界。
- D06 拥有 Alarm/Notification 业务生命周期；D08 只同步 Edge Alarm Fact、Evidence 和必要的本地投影。
- D10 拥有 Cloud 部署、平台队列和升级治理；D08 拥有 Edge Release、Edge Fleet 升级和现场回滚。

## 3. 固定证据基线

### 3.1 ThingsBoard Cloud 源码

Cloud 基线固定在 Apache-2.0 的 `thingsboard/thingsboard` tag `v4.3.1.1`。主要入口包括：

- [`edge.proto`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/edge-api/src/main/proto/edge.proto)；
- [`EdgeGrpcSession`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/edge/rpc/EdgeGrpcSession.java)、`PostgresEdgeGrpcSession` 和 `KafkaEdgeGrpcSession`；
- `EdgeSyncCursor`、`DefaultEdgeRequestsService` 和 `sync/fetch`；
- `EdgeEventSourcingListener`、`BaseEdgeProcessor`、`BaseDeviceProcessor` 及各实体 Processor；
- `DefaultTbEdgeConsumerService`、`edge_event` DAO、Queue Offset Attributes 和 Edge Session Cache；
- `DeviceEdgeTest`、`AssetEdgeTest`、`DeviceProfileEdgeTest`、`RuleChainEdgeTest`、`TelemetryEdgeTest`、`AlarmEdgeTest`、`DashboardEdgeTest`、`OtaPackageEdgeTest`、`NotificationEdgeTest`、`QueueEdgeTest`、`CalculatedFieldEdgeTest`、`EdgeGrpcSslTest` 等。

### 3.2 ThingsBoard Edge 源码

Edge 基线固定在 Apache-2.0 的 `thingsboard/thingsboard-edge` tag `v4.3.1.1`。主要入口包括：

- [`edge.proto`](https://github.com/thingsboard/thingsboard-edge/blob/04d9daf4a557a13483de2310e35b6493aff751fc/common/edge-api/src/main/proto/edge.proto)；
- [`tb-edge.yml`](https://github.com/thingsboard/thingsboard-edge/blob/04d9daf4a557a13483de2310e35b6493aff751fc/application/src/main/resources/tb-edge.yml)；
- `BaseGrpcClientManager`、Cloud Manager 和 Session Lifecycle；
- `GrpcCloudEventUplinkSender`、`UplinkMsgMapper`、`PendingUplinkMsgPackHolder`；
- `PostgresCloudEventUplinkProcessingRunner`、`AbstractPostgresCloudEventUplinkBatchDispatcher` 和 `PostgresCloudEventUplinkRetriever`；
- `cloud_event`、`ts_kv_cloud_event` 表、Queue Offset Attributes 和 TTL Worker；
- 与 Cloud 仓库对应的 Edge Entity/Telemetry/Alarm/OTA/Rule 集成测试。

当前官方文档用于辅助说明产品意图：[Architecture Overview](https://thingsboard.io/docs/edge/reference/architecture/)、[gRPC synchronization protocol](https://thingsboard.io/docs/edge/reference/architecture/grpc/)、[Configuration reference](https://thingsboard.io/docs/edge/reference/architecture/configuration/)、[Message queue](https://thingsboard.io/docs/edge/reference/architecture/queue/)、[OTA updates](https://thingsboard.io/docs/edge/user-guide/ota-updates/)。文档当前版本可能已经包含晚于 `v4.3.1.1` 的字段或能力；精确行为以固定源码为准。

### 3.3 HVAC Web 本地源码

本地反向审查先通过 CodeGraph 获取调用路径和源码，主要覆盖：

- `libs/edgecontrol/{channel,component,cycle,controller,intent,timedata}.go`；
- `tools/eg8200-simulator/internal/simulator/{edge_runtime,edge_driver,mqtt_publisher,mqtt_command,mqtt_config}.go`；
- `tools/eg8200-simulator/cmd/eg8200-mqtt-publisher/main.go`；
- `services/mqtt-telemetry-adapter/internal/adapter/runtime.go` 及 Command Runtime；
- `contracts/architecture/edge-control-plane.v1.json`；
- `docs/adr/0012-openems-informed-edge-control-plane.md` 和 `docs/architecture/openems-source-review.md`。

所有本地模块仍按 `UNVERIFIED` 或 `REVIEWING` 对待。局部测试通过只证明被测试行为，不为整个同步架构授予优先权。

## 4. ThingsBoard Edge 解决的问题

ThingsBoard Edge 在现场运行一个接近完整的 ThingsBoard 平台，目标是：

- 现场接入设备并保存 Telemetry/Attributes；
- 在 Cloud 失联时继续本地 Rule Engine、Alarm、Dashboard、RPC 和操作员访问；
- 将 Cloud 分配的 Device、Asset、Profile、Rule Chain、Dashboard、Credential、OTA 等下发到 Edge；
- 将 Edge 产生的 Telemetry、Alarm、Entity Change、RPC Result 等排队上送；
- 恢复连接后按顺序重放积压，并提供 Force Full Sync；
- 用同一套通用 IoT Entity/Rule/UI 模型同时服务 Cloud 和 Edge。

这个问题域比 HVAC Web 当前的“Edge Publisher + Command Executor + 新增控制库”完整得多。它证明了云边同步不能只靠 MQTT Topic 和一个离线 Packet Queue，需要独立的 Session、Projection、Event Log、ACK、Bootstrap、Incremental Tail、状态统计和实体级测试。

它的代价也很明显：Edge 携带完整 Tenant/User/Dashboard/Rule/DAO/UI 平台，资源和安全面更大；通用实体双向传播使所有权、冲突和删除语义更复杂；固定版本部分失败路径选择丢弃并推进 Cursor，适合“尽量同步”的通用 IoT 数据，却不满足受治理 HVAC 配置和安全证据要求。

## 5. ThingsBoard Domain 模型、核心流程与代码结构

### 5.1 参考 Domain 模型

```text
Edge
  -> routingKey + secret + EdgeVersion
  -> Session(connected, syncInProgress, pending packs)
  -> assigned Entity/Profile/Rule/Dashboard/OTA projections
  -> queueStartTs + queueStartSeqId

Cloud EdgeEvent
  -> edgeId + type + action + entityId + body + seqId + createdTime
  -> DownlinkMsg pack
  -> DownlinkResponseMsg

Edge CloudEvent / TsKvCloudEvent
  -> type + action + entityId + body + seqId + createdTime
  -> UplinkMsg pack
  -> UplinkResponseMsg

Full Sync
  -> in-memory EdgeSyncCursor(list of fetchers)
  -> pages of DownlinkMsg
  -> SyncCompletedMsg

Incremental Sync
  -> durable event table
  -> start timestamp/sequence stored as attributes
  -> page + ACK/discard
  -> cursor advancement
```

模型优点是把瞬时连接、耐久事件、全量快照和实体处理器分开。弱点是 Cursor 由时间、可循环 `INT seq_id` 和 Attributes 拼接；Full Sync 没有持久 Resume Token 或 Snapshot Revision；同一通用实体可在两端变化，冲突多由处理顺序和本地校验隐式决定。

### 5.2 连接与版本握手

`ConnectRequestMsg` 包含 Routing Key、Secret、`EdgeVersion` 和可选最大入站消息大小。Cloud 查找 Edge 并校验 Secret，返回 Edge/Tenant/Customer、Cloud Type、Routing Key/Secret 和最大消息大小。一个双向 gRPC Stream 承载 Uplink、Downlink、ACK、Sync Request 和 Edge Update。

值得吸收：

- 显式版本和能力握手；
- 最大消息大小协商；
- 连接状态、重连、Keepalive 和统计；
- 单一长连接上的双向类型消息和 ACK。

必须替换：

- 固定源码使用静态共享 Secret，默认 `CLOUD_RPC_SSL_ENABLED=false`；
- Secret 会出现在失败消息中，并在 Edge Configuration 中再次下发；
- 没有把证书身份、配置签名和 Secret Rotation 建成一等协议；
- 版本兼容依赖不断增加的条件分支和 deprecated Proto 字段。

### 5.3 Full Sync

Cloud 构造 `EdgeSyncCursor`，按固定 Fetcher 列表依次发送 Tenant、Queue、Rule Chain、Admin Settings、User、OAuth、Widget、AI、Notification、OTA、Resource，以及分配的 Dashboard、Profile、Device、Asset 和 Entity View。Full Sync 最后发送 `SyncCompletedMsg`。

源码边界：

- Wire Protocol 的 Sync Request 只有可选 `fullSync` 布尔值，没有 Snapshot ID、Page Token、Digest 或 Resume Token；
- `EdgeSyncCursor` 只是内存中的 Fetcher Index，断线后不能从中间恢复；
- 发送 Sync Completed 成功或失败，Cloud 回调都会把 `syncInProgress` 清除；
- Fetcher 失败只记录错误，Session 可能保持在异常状态直至重连；
- 没有发现最终 Manifest Digest、完整性核验或“删除快照中不存在的旧投影”协议；
- Device Profile 在 Full Sync 中再次发送以补软件/硬件字段，说明顺序依赖仍由流程约定承担。

结论：吸收“Bootstrap 全量路径”，替换为固定 Snapshot Revision、分片 Cursor、Digest、Tombstone Set、Resume 和 Commit/Abort。

### 5.4 Incremental Sync 与 Cursor

Cloud 的 `edge_event` 和 Edge 的 `cloud_event`/`ts_kv_cloud_event` 是耐久增量日志。普通路径分页读取，构造消息包，等待 ACK，然后把 `queueStartTs`/`queueStartSeqId` 存为 Attributes。系统用 60 秒 Misordering Compensation 处理 `created_time` 分区与 `seq_id` 排序差异，并处理 Sequence Cycle。

值得吸收：

- 耐久 Event Log 与瞬时连接解耦；
- Cloud->Edge 与 Edge->Cloud 分开；
- General 与 Timeseries 分开处理；
- Cursor 持久化和至少一次重放；
- Source Edge Context 避免事件回声。

固定源码的严重问题：

- 普通 Uplink/Downlink 三次失败后记录 Permanently Failed、丢弃并把 Future 作为“未中断”完成，外层随后推进 Cursor；
- `UplinkMsgMapper` 转换异常会“skipping event”并返回 `null`，页面仍按最后 Cloud Event 推进 Cursor；Cloud Downlink 转换也存在相同的整页推进风险；
- 超过对端最大消息大小的消息直接从 Pending Map 移除，未分片、重写或隔离；
- 非 Telemetry/Attribute Downlink 被 Edge 拒绝后，Cloud 立即从 Pending Map 移除；
- General Event 最多三次，Timeseries 可无限重试；后者可能永久阻塞后续数据；
- Cursor 保存失败只记录错误，造成后续重复；系统依赖下游幂等，但并非每种 Entity 更新都有强幂等键；
- Event 表 `seq_id` 是可循环 `INT`，并依靠时间补偿和 Cycle 检测恢复，不是简单单调 Token；
- Edge/Cloud Event 默认 TTL 一个月，长时间离线可能让未送达数据被清理。

目标必须做到：未成功交付或未进入显式 Quarantine 的 Item 不能推进其提交边界；Quarantine 也必须有可查询 Payload Digest、Reason、Attempt、Operator Disposition 和重新发布流程。

### 5.5 Priority、Backpressure 与 Offline

ThingsBoard 允许 Edge 断网时继续本地平台处理，Cloud Event 累积后重放。Cloud Downlink 还有一个每 Session 的高优先级内存队列。

源码边界：

- 高优先级队列是 `ConcurrentLinkedQueue`，非耐久；超过上限删除最旧事件；检查条件使用 `size > max` 后再 Add，边界还会短暂超过配置值；
- General 优先于 Timeseries 通过等待实现，不是可证明的多级公平调度；
- 默认 Cloud Event TTL 一个月不是容量规划，也不是“永不丢失”；
- 固定重连为 3 秒，没有按 Edge Fleet 抖动、退避上限和服务端 Retry Hint 设计；
- 离线期间 Cloud 对部分普通实体变更选择不保存 Edge Event，依赖后续 Full Sync 修复，但 Full Sync 又没有 Snapshot 清理/完整性协议。

因此只吸收“离线本地运行 + 耐久积压 + 类别隔离 + 恢复重放”，不吸收内存高优先队列、静默删除和无限队头阻塞。

### 5.6 Entity、Credential、Conflict 与 Delete

ThingsBoard 按实体类型提供 Processor，支持 Assignment、Credential、Profile、Relation、Alarm、Dashboard、OTA 等传播；测试广度是其明显优势。

固定源码同时表明：

- `BaseEdgeProcessor.isSaveRequired` 在比较前把远端 `version` 设为 `null`；Version 没有形成跨节点因果冲突检测；
- Device 重名时可在 Edge 生成唯一名称再保存，Cloud/Edge 名称可能分叉；
- Delete 到达后直接删除本地实体，没有统一 Tombstone、Retention、引用协调和延迟删除协议；
- Device Credential 完整值可以被同步到 Edge，扩大高价值 Secret 的复制面；
- Protocol Version 通过长期兼容分支维护，而不是按 Release Package 明确拒绝不兼容组合。

目标不允许通用双向 Last Writer Wins。每个字段必须有唯一 Authority；非 Authority 一端只能提交 Proposal/Observed Fact，不能写同一字段。

## 6. 本地源码级反向审查

### 6.1 可以保留的实现

- `libs/edgecontrol` 已实现强类型 Channel、Point ID 映射、current/next 双缓冲、Process Image、Cycle、Controller/Scheduler、约束组合、Component/Capability Registry 和 Manifest 生成。这些机制直接服务 HVAC 控制，不应被通用 IoT Entity Processor 替换。
- EG8200 Runtime 已把 Cloud Command 变为有 Expiry 的本地 Intent，按 Safety -> Limit -> Intent 顺序仲裁；测试证明限制裁剪、联锁拒绝、Cycle N 写入在 Cycle N+1 才出现在 Telemetry，以及 Lease 到期后释放控制。
- MQTT Edge Publisher 使用 TLS 1.3 双向证书、QoS 1、非 Clean Session、Paho File Queue、指数重连和字节容量上限。这个传输基础比 ThingsBoard 默认关闭 gRPC TLS、静态 Secret 更安全。
- Command Envelope 有 `commandId`、`payloadHash`、`expireAt`、`executionFence` 和 Reply Ledger；重复 Command 返回缓存结果，物理执行前校验过期、映射和 Fence。
- `FileTimedata` 把本地 Latest/History/Range Query 与 Cloud Publish/Resend 分开，按 `LocalPersistencePriority` 过滤并排除 Write-only Channel，保留高分辨率原始 Observation。这比把本地历史塞入传输队列更清楚。
- Edge Manifest 输出 Component、Capability Profile、Channel、Factory/Version、Properties 和 Binding，且测试验证确定性与自描述性。

这些 KEEP 只针对具体语义，不代表模块已经可生产部署。

### 6.2 必须补齐或替换的实现

#### 生产 Edge Runtime 仍缺失

`EdgeControlRuntime` 的实际组装、Plant Adapter、MQTT Publisher、Command Handler 和 Timedata Attachment 仍位于 `tools/eg8200-simulator`。`libs/edgecontrol` 可复用，但仓库中没有独立生产 Edge Daemon、生命周期管理、真实驱动装载、Release Activation 或系统服务打包。

结论：控制库 `KEEP`，Simulator-bound Host `REPLACE` 为生产 Edge Runtime；Simulator 只实现 Driver/Physical Model Adapter。

#### Timedata 不等于 Replay

`FileTimedata` 的源码注释和实现明确不拥有 Cloud Cursor。它提供 History/Latest/QueryRange，但当前没有 Historic Resend Worker、成功 ACK 后的 Recovery Evidence、按 Resend Priority 调度、周期 Full Snapshot、Retention/Quota/Compaction 或磁盘损坏处置闭环。

结论：`DATA-002` 的本地存储边界可保留；`DATA-003/006/007` 不能标为完成。需要独立 Replication Log/Resend Worker，把 Timedata Range Materialization 和 MQTT Packet Durability 连接起来。

#### MQTT File Queue 只是一段传输保证

Publisher 在消息物化后调用 `PublishViaQueue`，Paho File Queue 在断网时保存 Packet，并在目录 `*.packet` 达到配置上限前拒绝新 Publish。这是有价值的 Transport Spool，但：

- Envelope 永远写 `replay=false`；
- 没有 Timedata Range、Source Revision、Delivery Item 或 ACK Cursor；
- 没有按 Safety/Alarm/Command Evidence/Telemetry 分级；
- Queue 满时当前调用失败，Cycle 产生的数据没有随后从 Timedata 补发的闭环；
- Command Reply 直接调用 Client Publish，没有使用同一持久 Outbound Queue。

结论：Paho Queue `KEEP` 为最后一公里 Spool；用它替代 Durable Replication Log 的设计 `REJECT`。

#### Command Ledger 有价值但恢复语义不完整

Command Result 以临时文件 + Rename 保存，能在 Broker Reply 失败和重新投递后避免重复执行。但当前源码没有目录/文件 `fsync` 证据；加载后没有从记录恢复 `maxFenceByDevice`，注释也承认只保留 Command ID 幂等，新物理命令依赖 Cloud 发出新 Fence。Malformed Command 被消费但没有结构化拒绝回复。

结论：保留 Command ID/Payload Hash/Reply Cache Pattern；迁移到耐久 Execution Ledger，原子记录 Fence、Intent、设备写入结果、Readback 和 Reply Delivery，并提供损坏隔离及容量策略。

#### Manifest 不是 Desired/Observed Reconciliation

当前 `Manifest(revision, at)` 接受调用者给出的 Revision 并序列化内存 Registry。没有：

- Cloud Desired Revision 和 Edge Observed Revision；
- Canonical Digest、签名和依赖锁；
- Download/Stage/Validate/Activate/Rollback；
- Tombstone、Drift、Conflict 或 Reconcile Loop；
- SecretRef、Credential Rotation、OTA Campaign/Package；
- 版本不兼容拒绝和升级进度证据。

结论：Manifest 数据结构 `KEEP + EXTEND`；把它描述成远程配置能力 `REJECT`。

#### 配置和路由仍是静态文件/环境变量

MQTT Gateway Config 和 Command Runtime Binding 从本地 JSON 或 Env 载入，Schema 校验和 TLS 要求较好，但没有 Cloud 发布、签名验证、Fleet Cohort、分阶段激活、回滚或 Secret Rotation。Device External ID Mapping 也是静态输入。

结论：本地 Schema Validate `KEEP`；静态文件作为生产配置治理 `REPLACE`。

#### 当前架构契约存在已知过强声明

`contracts/architecture/edge-control-plane.v1.json` 仍将若干已实现代码标为 `MISSING`，同时又把 `DATA-003 Store and Forward / replay` 标成 `IMPLEMENTED`。前者已落后于源码，后者又强于 CodeGraph 和 OpenEMS Source Review 的证据。由于该文件已有并行修改，本轮不覆盖它；后续实现票必须以本裁决和 `openems-source-review.md` 为准，重新生成机器状态，不能继续用旧状态证明完成度。

## 7. 目标 Domain 模型与所有权

### 7.1 目标模型

```text
EdgeNode
  -> EdgeIdentity(mTLS certificate, bootstrap state, rotation revision)
  -> EdgeRuntimeVersion + ProtocolSchemaVersion
  -> ConnectionState + LastSeen + CapacityState

EdgeRelease
  -> ReleaseId + immutable digest + signature
  -> RuntimeRevision
  -> ManifestRevision
  -> RegistryProjectionRevision
  -> DriverRevision
  -> RuleRevision
  -> ScheduleRevision
  -> SafetyPolicyRevision
  -> required capabilities + compatibility range

DesiredEdgeState
  -> Cloud-owned published release and assignments

ObservedEdgeState
  -> active/staged/previous revisions
  -> EdgeManifest digest
  -> health, capacity, drift, rejection and rollback evidence

SyncSession
  -> negotiated protocol/capabilities/max payload
  -> BootstrapSnapshot(snapshotRevision, chunkCursor, digest)
  -> IncrementalStream(streamRevision, deliveryCursor)

DeliveryItem
  -> stable idempotencyKey + sourceRevision + ownerDomain
  -> priority + payloadDigest + attempt
  -> PENDING | IN_FLIGHT | ACKED | QUARANTINED

Tombstone
  -> entityId + ownerRevision + deletedAt + retentionUntil

ReconciliationResult
  -> desiredRevision + observedRevision
  -> APPLIED | REJECTED | ROLLED_BACK | DRIFTED
  -> reason + evidenceDigest
```

### 7.2 字段所有权

| 数据 | Authority | Edge 行为 |
|---|---|---|
| Tenant/Site/Space/Asset/Device/Point/Profile | Cloud Registry | 只读 Projection；按 Release/Snapshot 应用 |
| Driver/Channel 实际发现能力 | Edge | 上报 Manifest/Observed Fact；Cloud Reconcile，不反向猜测 |
| Rule/Schedule/Safety Policy Desired Revision | Cloud 发布流程 | 校验后激活；现场硬安全可拒绝 |
| PLC/Device 实时状态、Telemetry、Readback | Edge/设备 | 上报带 Source Position 的 Observation/Evidence |
| Command Governance/Approval/Authoritative Outcome | Cloud Command Domain | Edge 执行 Intent、Fence、Local Outcome 和 Readback |
| Alarm Fact | Edge 或 Cloud Rule Owner | 上报事实；D06 拥有 Cloud Lifecycle/Disposition |
| Device Credential/Certificate | D03 Credential Authority | 通过专用加密轮换流程交付，不进入普通配置 JSON |
| OTA Campaign | Cloud D03 | Edge 校验现场条件、签名、安装、回滚并上报证据 |

同一字段不得在 Cloud 和 Edge 同时可写。需要现场修改时，Edge 提交 Proposal 或 Observed Override；Cloud 接受后产生新 Owner Revision。

## 8. 目标核心流程

### 8.1 Provision 与连接

```text
manufacturing/site bootstrap identity
  -> one-time enrollment token
  -> issue short-lived mTLS Edge certificate
  -> connect with EdgeId + protocol/runtime version + capabilities + max payload
  -> Cloud validates tenant/site assignment and certificate state
  -> negotiate exact supported schema/package
  -> open session and report online state
  -> rotate certificate independently from product config
```

禁止在日志、通知、API Error 或普通配置包中返回 Secret。TLS 和服务端身份验证在生产不可关闭。

### 8.2 可恢复 Bootstrap

```text
Cloud freezes Published DesiredEdgeState at SnapshotRevision
  -> Edge requests snapshot (new or resume token)
  -> Cloud sends typed chunks with chunk index/digest
  -> Edge writes staging projection, never mutates active state in place
  -> validate references, capability compatibility and local safety
  -> receive explicit tombstone/completeness set
  -> compare final snapshot digest
  -> atomically activate projection revision
  -> ACK committed SnapshotRevision + Observed Manifest
  -> Cloud starts incremental stream strictly after SnapshotRevision
```

断线后从已验证 Chunk Resume；Snapshot 失败保留旧 Active Revision。`SyncCompleted` 不能替代完整性证明。

### 8.3 Incremental Delivery

```text
owner transaction
  -> append typed ChangeEvent with source revision/idempotency key
  -> select by Edge assignment and owner domain
  -> durable DeliveryItem
  -> send bounded batch
  -> Edge validate + apply idempotently
  -> ACK applied owner revision
  -> advance contiguous committed cursor
```

单个坏 Item 进入 Quarantine，不阻断不相关 Ordering Key，也不被静默跳过。只有 ACK 或显式、可审计的 Operator Disposition 才能越过该 Item。

### 8.4 Offline 与恢复

```text
Cloud disconnected
  -> keep last ACTIVE signed release
  -> continue local controller/rule/schedule within lease and safety policy
  -> never extend expired Cloud command lease implicitly
  -> store telemetry/alarm/control/audit evidence by priority and retention policy
  -> shed low-value diagnostics before safety/control evidence
  -> expose local degraded/queue/disk state

Reconnect
  -> authenticate and negotiate version
  -> upload critical evidence first with fair scheduling
  -> compare Desired/Observed revision
  -> resume delta if cursor retained, otherwise bootstrap snapshot
  -> reconcile drift/tombstone
  -> declare converged only after both directions commit
```

磁盘不足必须有明确水位：Normal、Pressure、Critical、Read-only Safety。不得用“删除最旧高优先级事件”恢复空间。

### 8.5 远程配置发布与回滚

```text
Draft
  -> schema/reference/capability/safety validation
  -> immutable artifact + dependency lock + digest
  -> sign and publish to cohort
  -> Edge download to staging
  -> verify signature/hash/schema/runtime compatibility
  -> local dry-run and device/driver preflight
  -> activate at safe cycle boundary
  -> health/readback verification window
  -> ACTIVE or automatic rollback to previous signed revision
  -> Cloud records evidence and fleet status
```

Secret 只通过 SecretRef 和专用 Rotation Channel 交付。配置包中不包含私钥、长期 Token 或明文 Device Secret。

### 8.6 OTA

D03 创建和批准 Campaign；D08 在 Edge 侧执行：

```text
signed package + compatible device/profile + campaign window
  -> Edge stage and verify
  -> check plant mode, redundancy, minimum running equipment and rollback capacity
  -> transfer to device
  -> monitor reported state/readback
  -> success evidence or rollback/quarantine
```

ThingsBoard 的“Cloud 创建 Package、同步到 Edge、Edge 分配给 Device/Profile、状态 Attribute 跟踪”可吸收；只依赖 Checksum、设备自报状态或无现场冗余门禁不可接受。

## 9. 全能力裁决矩阵

| # | 能力 | ThingsBoard 判断 | HVAC Web 当前判断 | 裁决 | 目标行为 |
|---|---|---|---|---|---|
| 1 | Edge Entity、Credential、Provisioning、Connectivity、Upgrade | 身份/会话/版本/在线状态完整；静态 Secret、默认无 TLS、Secret 泄漏和轮换不足 | mTLS/MQTT 更安全，但无 Enrollment、Fleet Identity、Rotation、Edge Release | `KEEP local security + ADOPT session + REPLACE identity lifecycle` | 一次性 Enrollment、短期 mTLS、版本握手、Fleet 状态、签名 Edge Release、独立轮换 |
| 2 | Device/Asset/Profile/Rule/Dashboard/View/OTA Assignment | 类型和测试覆盖广，Cloud 分配可自动传播 | Registry/控制模型更适合 HVAC，但没有 Edge Projection 发布 | `ADAPT` | 只复制现场所需、Cloud-owned 的版本化 Projection；不复制完整通用平台或本地 CRUD Authority |
| 3 | Edge Event Queue、Cursor、Change Delivery | 耐久双向日志和 ACK 值得吸收；丢弃/跳过后推进、循环 Seq/时间补偿风险高 | Paho Packet Queue 有持久性，但没有领域 Delivery Log/Cursor/DLQ | `ADOPT pattern + REPLACE semantics` | 类型化 DeliveryItem、单调 Source Revision、连续 ACK Cursor、Quarantine、无静默丢失 |
| 4 | Session、Full/Incremental/Resumable Sync | Full + Delta 存在；Full Cursor 只在内存，Wire 无 Resume/Digest | 无完整 Snapshot/Delta 协议 | `ADAPT + REPLACE` | 在 MQTT 上实现 Snapshot Revision、Chunk Resume、Digest、Tombstone、Commit 和 Delta Tail |
| 5 | Data/Configuration/Alarm/RPC Flow | 覆盖全面，但通用双向 Entity Flow 模糊 Authority | Telemetry/Command 安全语义较强，Config/Alarm Edge Projection 缺失 | `KEEP local domain + ADAPT typed flow` | 按 Owner Domain 拆流；Command/Alarm 保留既有权威，Edge 只产生 Intent Outcome/Evidence |
| 6 | Offline、Recovery、Priority、Backpressure | 本地平台可离线；Event 积压与重放成熟；高优先队列丢旧、General/TS 策略和 TTL 不安全 | 本地闭环和 bounded Packet Queue 有基础；缺 Historic Resend、容量策略和公平优先级 | `KEEP control + ADOPT offline log + REPLACE queue policy` | Last-known-safe Release、分级耐久队列、公平调度、磁盘水位、Range Resend、无无限 HOL |
| 7 | Local Rule Engine、Notification、UI Boundary | 完整本地 Rule/UI/Alarm，可离线操作；资源和 Authority 面过大 | 本地 Controller 强，Rule/Notification/UI Edge 仍缺 | `ADAPT + DEFER domain detail` | D05 实现受版本治理的 Edge Rule Runtime；D06 管生命周期；Edge UI 仅现场维护/应急，不拥有 Cloud 业务真值 |
| 8 | Conflict、Delete、Duplicate、Ordering、Compatibility | 有 Source Edge 抑制和实体处理器；Version 被清空、重名改名、删除直接执行、长期兼容层不足 | Source Position/Fence 较强，但 Config Projection 无冲突模型 | `REPLACE` | 单一字段 Authority、Owner Revision、Tombstone、幂等键、Ordering Key、显式 Conflict、无兼容层 |

## 10. 值得吸收的 Pattern

1. **Durable event before transport**：业务变更先进入耐久事件/Delivery Log，再由连接会话发送。
2. **Bootstrap plus tail**：首次/失配走完整快照，正常运行只追增量；两者在同一 Source Revision 上衔接。
3. **Typed bidirectional ACK**：每个方向都有消息 ID、类型、结果和错误，不把 TCP/MQTT 连接成功当业务应用成功。
4. **Assignment-driven projection**：只向 Edge 投影与 Tenant/Site/Edge Assignment 相关的对象。
5. **Source-origin suppression**：Edge 上报的事实再次生成 Cloud Event 时避免回送给同一 Edge。
6. **Version/capability handshake**：建立连接前声明协议版本、Runtime 能力和消息大小。
7. **Separate general and telemetry work**：配置/控制/证据与批量 Telemetry 使用不同队列和 SLO。
8. **Edge online/stats model**：连接、积压、失败、永久问题和版本形成 Fleet 运维状态。
9. **Entity-level integration matrix**：每种 Projection 必须测试 Create/Update/Delete/Assign/Unassign/Restart/Offline/Reconnect。
10. **Cloud-owned OTA package propagation**：Package 不在 Edge 任意创建；Edge 负责验证、现场门禁、安装和证据。

## 11. 不适合本项目的部分

- 在 EG8200 上部署完整 Tenant/User/Dashboard/REST/Web UI/通用 Entity 平台。
- 用 ThingsBoard Java、Actor、gRPC 或 PostgreSQL 表结构作为运行时依赖，只因为参考项目使用它们。
- 静态 Routing Key/Secret 认证、默认关闭 TLS、错误信息返回 Secret、普通配置同步 Credential 完整值。
- Full Sync 只有 `fullSync=true` 和最终 `SyncCompleted`，没有固定 Snapshot、Digest、Resume 和 Tombstone。
- 三次失败、转换异常或消息过大后丢弃并推进 Cursor。
- Timeseries 无限重试导致队头永久阻塞，或高优先级队列满后删除最旧事件。
- 用一个月 TTL 代替 Offline Capacity/Retention/SLO；未送达安全证据不得被定时清理。
- 将远端 Version 清空后按对象 Equality/到达顺序保存，或为了绕过唯一约束在 Edge 自动改名。
- Cloud/Edge 对同一通用实体字段双向可写。
- 用长期 deprecated Proto 字段和运行时条件分支维持所有旧 Edge 版本。
- 让 Edge UI、Local Alarm 或 Local Rule Runtime成为 Cloud Alarm Disposition、IAM、Registry 或 Audit 的第二权威。

## 12. 映射到本项目实施

### 12.1 P0：先修正架构契约和不可接受缺口

1. 新增 `EdgeNode/EdgeIdentity/EdgeRelease/DesiredEdgeState/ObservedEdgeState` 合同和 Owner Matrix。
2. 建立 MQTT 云边 Replication Protocol v1：Handshake、Snapshot Request/Chunk/Commit、Change Batch/ACK、Quarantine、Heartbeat/Capacity。
3. 把 `Edge Manifest` 扩展为 Canonical Digest、Observed Revision、Runtime/Driver/Rule/Safety Revision 和 Drift，不再接受任意字符串充当可信 Revision。
4. 建立独立于 Paho Packet Queue 的 Durable Delivery Log、Cursor 和 Quarantine；明确 Packet Queue 仅为 Transport Spool。
5. 实现 Timedata Historic Resend Worker：按时间范围/Channel Materialize，ACK 后推进 Recovery Evidence；补 Changed-value + Periodic Full Snapshot。
6. 将 EG8200 Simulator 中的生产组装迁出为独立 Edge Runtime；Simulator 只保留 Driver/Physical Model。
7. 把 Command Execution Ledger 升级为可崩溃恢复的 Fence/Intent/Readback/Reply Ledger；Command Reply 进入持久 Outbound Delivery。
8. 修正 `edge-control-plane.v1.json` 的过期状态，禁止 `DATA-003 IMPLEMENTED` 等未经源码证据支持的声明。

### 12.2 P1：远程配置、Fleet 与离线闭环

1. 实现签名 Edge Release：Draft/Validate/Diff/Publish/Cohort/Stage/Activate/Verify/Rollback。
2. 实现一次性 Enrollment、mTLS Certificate Rotation、Revocation 和到期运维事件；Secret 不进入 Release。
3. 实现 Registry/Profile/Rule/Schedule/Driver Projection Snapshot + Delta，包含 Tombstone 和 Reference Validation。
4. 建立多级 Offline Queue：`SAFETY_EVIDENCE`、`COMMAND_RESULT`、`ALARM_FACT`、`CONFIG_RESULT`、`TELEMETRY_CRITICAL`、`TELEMETRY_NORMAL`、`DIAGNOSTIC`；每级独立容量、保留和公平权重。
5. 实现磁盘水位和 Degradation Policy；Critical 时保留安全闭环和高价值证据，停止低价值采样/聚合，不静默删除。
6. 建立 Edge Fleet View：Desired/Observed、版本、最后连接、积压 Age/Bytes、Quarantine、Drift、证书和升级状态。
7. 扩充故障测试：断网、长离线、Broker Session 丢失、重复/乱序 ACK、坏 Chunk、消息过大、磁盘满、进程崩溃、回滚、证书吊销。

### 12.3 P2：现场 Rule、OTA 与高级管理

1. D05 Rule Engine 发布 Edge-compatible Rule Package；Edge 在资源/安全 Sandbox 内运行，Cloud 保留版本与治理。
2. D03 OTA Campaign 与 Edge 安装执行打通；支持签名、兼容、分批、现场冗余门禁、A/B 或可证明回滚。
3. 增加只读/维护型 Local UI：连接诊断、Active/Previous Release、设备健康、队列/磁盘和受审计的 Emergency Procedure；不复制完整 Cloud 管理台。
4. 只有容量和可用性证据要求时再做 Edge Cluster；单 EG8200 不引入 ThingsBoard 的完整集群栈。

## 13. 异常和边界处理

| 场景 | 必须行为 |
|---|---|
| Snapshot 中途断线 | 保留旧 Active；按 Snapshot Revision + Chunk Cursor Resume，不混用新旧 Chunk |
| Snapshot 完成但 Digest 不一致 | 拒绝 Commit，保留 Staging Evidence，重新拉取或 Quarantine |
| Delta 重复 | 按 Delivery ID + Owner Revision 幂等返回原 ACK |
| Delta 乱序 | 同 Ordering Key 缓冲/拒绝并请求缺口；不同 Key 可继续 |
| 单个 Payload 转换失败 | 进入 Quarantine；不得从 Batch 静默过滤后推进 Cursor |
| Payload 超过协商上限 | 分片或按类型重物化；无法处理则 Quarantine，不丢弃 |
| ACK 丢失 | 重发安全；接收端返回已应用 Revision |
| Cursor 持久化失败 | 不提交下一批；重放允许，但不能产生重复物理控制 |
| Event 达到 Retention | 未 ACK 的高价值 Item 不自动删除；触发容量事件和人工处置 |
| Offline Queue 满 | 按显式等级降采样/聚合低价值数据；安全/命令/告警证据禁止静默删除 |
| Cloud Command Lease 到期 | 停止参与本地仲裁；不得因离线自动续期 |
| 新配置违反本地安全/能力 | Edge 拒绝激活并上报结构化理由；Cloud 不强制覆盖 |
| 激活后健康失败 | 自动回滚到 Previous Signed Revision，记录前后版本和证据 |
| Delete 与本地运行冲突 | 先 Tombstone/停用，释放 Driver/Controller 后完成清理；保留审计和引用证据 |
| 同名/唯一约束冲突 | 显式 Conflict；禁止 Edge 自动改名形成分叉 |
| Certificate 吊销/过期 | 停止 Cloud Session；保持 Last-known-safe 本地控制，禁止新远程命令/配置 |
| 协议/Runtime 不兼容 | 连接可进入 Upgrade-required/Read-only 状态，但不得运行旧路径 fallback |

## 14. 差距过大项

以下差距不能用“当前只是 Phase 1”直接豁免：

1. 生产 Edge Runtime 尚未从 Simulator Host 中独立出来。
2. 不存在完整 Full Snapshot + Incremental Tail + Resume + Digest + Tombstone 协议。
3. `FileTimedata`、Paho File Queue 和 Cloud Telemetry Ingest 之间没有 Historic Resend/Cursor 闭环。
4. Edge Manifest 没有 Desired/Observed、签名、激活、回滚和 Drift 语义。
5. 没有 Edge Enrollment、Certificate Rotation、Revocation 和 Fleet Identity Lifecycle。
6. 没有签名远程配置 Release、Cohort、Stage、Safe-cycle Activation 和自动回滚。
7. 没有 Device/Profile/Rule/Schedule Projection 的发布与 Reconciliation。
8. 没有 OTA Edge 执行、现场冗余门禁、回滚和证据。
9. Command Ledger 不能完整恢复 Device Fence，Reply 也没有统一进入持久 Outbound Delivery。
10. 没有 Offline Priority/Quota/Retention/Backpressure 的端到端故障认证。
11. 没有 Quarantine/Operator Disposition，当前成功路径无法证明坏消息不会静默丢失。
12. 机器架构契约与当前源码状态互相漂移，不能作为完成度证据。

## 15. 实施门槛

### Sync Correctness Gate

- Snapshot Revision 固定、Chunk 可恢复、最终 Digest 一致；
- Snapshot 与 Delta 在同一 Source Revision 连续衔接；
- Create/Update/Delete/Assign/Unassign/Restart/Offline/Reconnect 全矩阵通过；
- 无任何转换异常、过大消息或三次失败后静默推进 Cursor 的路径；
- Duplicate、Out-of-order、ACK Loss 和 Cursor Store Failure 测试通过。

### Offline Safety Gate

- 断网不影响本地 Controller/Scheduler/Safety；
- Cloud Intent 到期后释放，不能粘住输出；
- 磁盘水位、降采样和队列优先级有可证明行为；
- Safety/Command/Alarm/Audit Evidence 在目标最长离线时长内不丢失；
- 恢复后积压 Age/Bytes 在声明 SLO 内收敛，且正常实时流不被无限饿死。

### Edge Release Gate

- Artifact 不可变、有 Digest/Signature/Dependency Lock 且无 Secret；
- Runtime/Schema/Driver/Rule/Safety 兼容性在下载和启动时双重校验；
- Staging 不改变 Active；激活在安全 Cycle Boundary；
- 失败自动回滚 Previous Signed Revision；
- Desired/Observed/Drift/Reject/Rollback 全部可查询和审计。

### Identity Gate

- 一次性 Enrollment Token 短期、单用、绑定 Tenant/Site/Edge；
- mTLS Certificate 可轮换、吊销和到期告警；
- 日志、Metric、Trace、Error、Notification 和配置包均不含 Secret；
- Credential 与普通配置发布分离；被吊销 Edge 不能接收新命令或发布配置。

### Production Edge Gate

- Runtime 与 Simulator 分离，真实 Driver/Protocol Bridge 通过端到端测试；
- 进程重启、主机重启、Broker 断线、磁盘压力和配置回滚测试通过；
- Local Timedata、Replication Log、Packet Spool 和 Command Ledger 的容量/损坏/恢复策略明确；
- 本地 UI/Rule/Alarm 不成为 Cloud IAM、Registry、Disposition 和 Audit 的第二权威。

## 16. 最终裁决

ThingsBoard Edge 在“完整现场 IoT 平台、实体传播和云边同步覆盖”上明显领先当前 HVAC Web。若忽略它的 Session、耐久事件、Bootstrap/Delta、Assignment、ACK、离线积压和实体测试，本项目会继续把局部 MQTT 成功误报成完整 Edge 能力。

但 ThingsBoard 固定版本并不是可靠性或安全性的上限。三次失败后丢弃、转换异常跳过、内存高优先队列删旧、Timeseries 无限队头阻塞、Full Sync 不可恢复、共享 Secret 和弱冲突语义，都不应进入受治理 HVAC 控制平台。

目标不是复制 ThingsBoard Edge，而是把三类优势组合起来：

- 用本地 HVAC Edge Control Plane 负责现场闭环、能力、联锁、租约和读回；
- 用 ThingsBoard 证明过的同步分层建立 Session、Bootstrap、Incremental Event Log、ACK、Assignment 和 Fleet 状态；
- 用本项目更严格的 mTLS、Owner Revision、Quarantine、签名 Release、Tombstone、回滚和 Evidence 语义替换参考实现的薄弱部分。

## 17. 本轮验证结果

- D08 的 8 项能力均已逐项分类为 `KEEP`、`ADOPT`、`ADAPT`、`REPLACE`、`REJECT` 或 `DEFER`。
- Cloud 与 Edge 仓库均固定到 `v4.3.1.1` 的精确 commit；未用当前文档覆盖固定源码差异。
- 已审查 Protocol、Session、Full Sync Cursor、Incremental Queue/Cursor、上下行 Sender/Mapper、Entity Processor、TTL、Priority、Credential、OTA 和实体级测试入口。
- 已通过 CodeGraph 反向审查本地 Channel/Cycle/Manifest/Timedata/MQTT Queue/Command Ledger/Simulator Host 和 Cloud Adapter 调用路径。
- 已明确记录本地优于 ThingsBoard、ThingsBoard 优于本地及双方都必须替换的行为。
- 本轮只完成源码级架构裁决；没有声称 P0/P1 缺口已实现或生产认证。
