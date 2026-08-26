# Phase 1 总体架构基线

本文件以 `SE-ARCH-DEPLOY-001 V1.0 CURRENT`《智慧能源系统总体部署架构设计 V1（单服务器基线）》作为当前总体部署权威，并接受用户对 V2.1.2 的后续架构裁决。Edge 控制面的当前机器权威为 `contracts/architecture/edge-control-plane.v1.json`，ADR 为 `docs/adr/0012-openems-informed-edge-control-plane.md`。若旧“Edge Gateway”表述与该裁决冲突，以新的 HVAC Edge Control Plane 为准；单服务器 Cloud 部署边界不因此扩大。

## 1. 一级总体架构

```text
                         User
                          │
                    HTTPS / WSS
                          │
                          ▼
                    Nginx / Gateway
                          │
                 ┌────────┴────────┐
                 │                 │
                 ▼                 ▼
              React          Central Platform
                                   │
                     ┌─────────────┼─────────────┐
                     │             │             │
                     ▼             ▼             ▼
                 Business       IoT/Data      Control
                 Services       Services      Services
                     │             │             │
                     └─────────────┼─────────────┘
                                   ▼
                            Data / Message
                     ┌────────┬───────┬────────┐
                     ▼        ▼       ▼        ▼
                 PostgreSQL ClickHouse Redis  MQTT
                                                │
                                                ▼
                                     HVAC Edge Control Plane
                                                │
                                      OT Protocol / Driver
                                                │
                                                ▼
                                             Devices
```

旁路能力贯穿全部层级：

```text
Security
Monitoring
Logging
Tracing
Backup
Deployment
Audit
```

一级架构只表达职责，不暴露 S0/S1/S2/S3/S4、Projector、Adapter、Centrifugo 等工程实施细节。

## 2. 二级逻辑服务架构

Central Platform 按业务职责包含：

- Identity / Authorization
- Registry / Device Management
- Telemetry
- Energy / Analytics
- Metric
- Alarm
- Command / Control
- Work Order
- Forecast
- Optimization

浏览器只通过 Nginx 和 Platform Gateway 进入业务 API，不直接访问数据库、MQTT 管理面或内部服务。

## 3. 三级实现映射

V2.1.2 要求逻辑领域与物理 Deployable 解耦，并在 Phase 1 避免把内部职责继续拆成独立进程。当前 Phase 1 已形成真实的 `energy-api` 与 `iot-service`，并进一步收敛 Telemetry Worker 内部职责：

```text
energy-api
├─ Platform Gateway / BFF
├─ Realtime Module        # 浏览器唯一 /realtime/ 入口；Centrifugo 仅为内部 transport 实现细节
├─ IAM Module             # 保留独立 IAM mTLS listener / SPIFFE identity
├─ Registry Core Module   # 保留独立 Core mTLS listener / SPIFFE identity
├─ Telemetry Query Module # 保留独立 Query mTLS listener / SPIFFE identity
├─ Audit Module           # Session Audit 使用 PostgreSQL Outbox 直投，不依赖 Kafka
├─ Alarm Module           # 保留独立 Alarm mTLS listener / SPIFFE identity
├─ Work Order Module      # 保留独立 Work Order mTLS listener / SPIFFE identity
└─ Command Module         # 保留独立 Command mTLS listener / SPIFFE identity

iot-service
├─ MQTT telemetry / state / event / heartbeat receive
├─ Command dispatch worker
└─ reported-state verification worker
   # Dispatcher / Verifier 仍加载各自的 workload credential

telemetry-worker
├─ Telemetry HTTP / ingest / latest / realtime
├─ history projection
└─ analytics energy projection
   # 不再单独部署 telemetry-history-projector / analytics-read-model-projector

scheduler
├─ Schedule Definition scan / next_fire_at coordination
├─ Job Instance creation + dedup / misfire / retry coordination
└─ expired Lease recovery
   # PostgreSQL 是 Schedule / Job / Attempt 权威；Scheduler 不执行业务公式或设备控制

maintenance-worker
├─ credential expiry scan
├─ dead-work disposition
└─ Tenant retirement coordination execution
   # 这是跨领域 operational worker，不是新的业务 Domain；不并入 Scheduler 或在线 energy-api 进程

metric-worker
├─ Claim METRIC_* Job via FOR UPDATE SKIP LOCKED
├─ Lease / Attempt / Timeout / cooperative cancel
├─ Metric business execution + Scheduled run 幂等去重
└─ stale PERSISTING 跨存储 reconcile
   # 不再拥有 Schedule scan / next_fire_at authority
```

因此 Phase 1 的默认业务 deployable 固定为 `energy-api + iot-service + telemetry-worker + metric-worker`。长期运行的 supporting workload 另有 `scheduler + maintenance`：`scheduler` 只负责 Application Job Coordination，`maintenance` 执行证书到期扫描、Dead Work 处置和 Tenant Retirement 等跨领域 operational job，两者都不形成新的业务 Domain/API authority。`identity-service` 是独立 Identity Infrastructure，也不计入四个默认业务 deployable。数据库迁移、Schema Preflight、Identity Bootstrap/Admin/Reconcile、PostgreSQL/ClickHouse Backup 都是 one-shot operator action，不计入长期运行拓扑。不再单独部署 `platform-gateway`、`iam-service`、`platform-core-service`、`telemetry-query-service`、`audit-ledger-service`、`alarm-service`、`work-order-service`、`command-service`、`mqtt-telemetry-adapter`、`command-dispatcher`、`command-verifier`、`telemetry-history-projector`、`analytics-read-model-projector`；原 `telemetry-runtime-service` 仅保留逻辑 DNS alias。Forecast / Optimization / FDD 统一归入可选 `intelligence` profile，不要求成为默认常驻基础进程。完整分类由 `deploy/platform/phase1/runtime-inventory.v1.json` 作为机器合同约束。Phase 1 不引入 Kubernetes、Service Mesh 或 Kafka 作为必需依赖。

`iot-service` 是 Cloud 侧 IoT 集成进程，不等于新的 HVAC Edge Control Plane。生产 Edge 运行时位于现场 Gateway；Development 可以把 Simulator/Edge Runtime 与 Cloud Compose 放在同一台开发机，但不能据此改变生产职责边界。

## 4. 数据权威

- PostgreSQL：业务主数据、配置、Registry、控制状态、告警状态等权威业务状态。
- ClickHouse：历史 Telemetry、聚合和分析读取模型。
- Redis：Latest/current 可重建缓存与实时传输基础设施，不保存唯一业务事实。
- Object Storage：Archive / Backup / Evidence / Dataset / Model Artifact 等大对象与冷数据；Phase 1 只冻结 provider-neutral 外部依赖 contract，具体产品由部署环境选择，治理元数据由 PostgreSQL `object_storage_buckets` / `archive_manifests` / `backup_manifests` 权威维护。
- MQTT：Edge ↔ Cloud 传输。
- Edge Timedata：现场 latest/history、恢复游标、优先级 Replay 和必要的本地聚合；它不替代 Cloud 的 Point/Telemetry/Analytics 权威。
- Edge 本地磁盘：Edge Timedata 与 Store & Forward 的持久化介质；Cloud 恢复后按 resend priority Replay。

## 5. Cloud / Edge 边界

```text
Cloud Control Plane
  ├─ Tenant / IAM / RLS
  ├─ Registry / Equipment / Device / Sensor / Point
  ├─ Command Governance / Approval / Audit
  ├─ S2 / Analytics / Alarm / Work Order
  └─ Forecast / Optimization / desired intent
          │
          │ MQTT TLS
          ▼
HVAC Edge Control Plane
  ├─ Edge Component Registry
  ├─ Capability Profile Registry
  ├─ Channel Runtime
  ├─ Process Image
  ├─ Cycle
  ├─ Controller Runtime
  ├─ Scheduler / Control Arbiter
  ├─ Local Safety / Interlock Controllers
  ├─ Equipment / Cluster Controllers
  ├─ Device Drivers
  ├─ Protocol Bridges
  ├─ Edge Manifest
  ├─ Edge Timedata
  └─ Store & Forward / Replay
          │
          ▼
Modbus / BACnet / OPC UA / MQTT / vendor protocol
          │
          ▼
OT Device
```

Cloud 不直接访问 PLC、BMS、PCS 或其他现场设备。Cloud 故障不得阻止 Edge 继续采集、缓存、执行本地 Controller、维护本地 Schedule 或执行本地安全策略。

### 5.1 Edge 是软实时控制面，不是硬实时安全系统

HVAC Edge Control Plane 默认控制周期为 `1s`，设备或 Capability 可以显式配置更适合的软实时周期。PLC、设备保护、急停、消防、灾害保护等硬实时功能必须保留在专用硬件/固件中，不能由 Edge 软件替代。

### 5.2 Input → Process → Output 与 Process Image

Edge 必须采用明确的 IPO Cycle：

```text
Input
  asynchronous Device/Protocol reads -> Channel.nextValue
  ↓
Switch Process Image
  Channel.nextValue -> Channel.value
  ↓
Process
  Controllers read one immutable Process Image
  Scheduler / Arbiter applies constraints in deterministic priority order
  ↓
Output
  effective values -> Device Driver -> Protocol Bridge -> device
```

一个 Cycle 内所有 Controller 必须看到同一份不可变数据。协议 I/O 不允许阻塞 Controller 运行。

### 5.3 Point 与 Channel 的边界

```text
Point   = Cloud durable identity / authority / history / provenance
Channel = Edge runtime value / quality / access / priority
```

每个受治理 Channel 映射一个 canonical Point；Edge Channel 地址不得成为新的平台主键。

### 5.4 Capability Profile / Driver / Bridge

Controller 不依赖厂家名或寄存器地址，而依赖 Capability Profile。第一批 Profile：

```text
VARIABLE_SPEED_PUMP
CHILLER
COOLING_TOWER
ELECTRICITY_METER
WEATHER_STATION
```

厂家/型号差异进入 Device Driver；连接、重试、轮询、协议任务调度进入 Protocol Bridge。第一批 Bridge：

```text
Modbus TCP
Modbus RTU
MQTT
```

BACnet / OPC UA 后续复用同一抽象。

### 5.5 Cloud Command 是 governed intent

Cloud 继续拥有 Command identity、IAM、审批、幂等、Audit 和 durable outcome，但 Dispatcher 不再拥有最终 actuator value。Edge Control Arbiter 对 Intent 应用安全、联锁、设备限制和更高优先级 Controller 后生成 `effectiveValue`。

目标 Command evidence 至少包含：

```text
requestedValue
effectiveValue
constraintReason
winningController
controlCycle
intentExpiresAt
```

远程 Intent 必须有 Lease/Expiry。Cloud 失联或 Intent 过期后，不允许最后一次远程设定值永久黏住；控制权回到本地策略。

### 5.6 Local Timedata / publication priority

Edge 不只维护“发送失败文件队列”。Channel/Point 应支持：

```text
pollPriority
persistencePriority
aggregationPriority
resendPriority
```

实时变化优先发布，同时周期发送完整快照用于收敛。恢复 Replay 按优先级先发送故障、状态和控制证据，再发送低价值诊断数据。

### 5.7 Simulator 与真实设备同构

Simulator 与真实设备只能在 Driver/Protocol/physical model 边界不同。生产 Controller、Scheduler、Command Intent、Telemetry 和 Verification 路径必须共用。

Simulator 目标行为至少包括：

```text
startup / shutdown delay
ramp rate
minimum run / stop time
fault / reset
communication timeout
stale value
sensor noise
stuck actuator
write rejected
interlock
```

### 5.8 Single / Cluster Controller

单设备 Controller 管单机状态机、sequence 和局部约束；Cluster/Plant Controller 负责多设备负荷分配与组合策略。Phase 2 目标包括：

```text
ChillerPlantController
ChilledWaterPumpClusterController
CoolingWaterPumpClusterController
CoolingTowerClusterController
```

## 6. Phase 1 Edge 最小完成定义

在 ADR 0012 生效后，以下能力全部属于 Phase 1 Edge Foundation：

1. Channel Runtime；
2. Process Image；
3. Cycle；
4. Controller Contract；
5. Scheduler / Control Arbiter；
6. Capability Profile Registry；
7. Device Driver；
8. Protocol Bridge；
9. Local Safety / Interlock Controller；
10. leased Cloud Command Intent adapter；
11. Edge Manifest；
12. Edge Timedata + priority resend；
13. Simulator/real Driver parity。

仅有 MQTT telemetry、MQTT command 和 persistent queue 不再足以声明生产级可控 Edge 完成。当前实现因此对新的 Edge Control Plane 目标是 `PARTIAL`。

## 7. Phase 1 部署边界

Phase 1 canonical deployment 是 **1 Linux Server + Docker Compose**。

Phase 1 canonical Cloud deployment：

```text
1 Linux Server
+
Docker Compose
```

当前正式 Cloud 基线只接受单服务器部署。Application / IoT / Telemetry / Metric / Data / MQTT / Observability 仍保持逻辑与进程边界；多服务器只作为未来拆分边界，不属于当前 Phase 1 Cloud 部署模式。

现场 HVAC Edge Control Plane 是独立 OT/Edge 边界，不计入“1 Linux Server Cloud”数量。Development/Testing 可以把它作为 Compose profile 同机模拟，Production 不允许因此把 Cloud 进程直接部署到 PLC/BMS 网络。

以下能力不作为 Phase 1 Cloud 前置：

- Kubernetes 不作为 Phase 1 前置
- Service Mesh
- Auto Scaling
- PostgreSQL Replica HA
- ClickHouse Cluster
- MQTT Cluster
- Kafka/Redpanda 作为平台必需 backbone
- Multi Region
- GitOps Controller

这些资产如果已经存在，只能作为未来演进、认证实验或局部测试资产，不能成为 Phase 1 Cloud 部署成功的必要条件。

## 8. 环境

必须有独立：

```text
Development
Testing
Staging
Production
```

四套环境分别隔离 Database、Redis、MQTT、Storage、Secrets、Domain 和 Configuration。Development/Testing 不得指向 Production 数据库或 MQTT。

## 9. 恢复目标

恢复目标以 `SE-OPS-009 V1.0 CURRENT CANDIDATE` 为权威，并由 `deploy/platform/phase1/recovery/recovery-targets.v1.json` 机器化。当前核心目标为 PostgreSQL `RPO≤5min/RTO≤2h`、Control `RPO≤5min/RTO≤1h`、Telemetry Cloud `RTO≤4h`、Metric `RPO≤30min/RTO≤4h`、Whole Server Replacement `RTO≤4h`。

Whole Server 的 4 小时目标只有在 Cold Standby/可及时替换硬件、External Backup、Versioned Config 和 Recovery Runbook 同时成立时才可声明。单服务器不承诺 Zero Downtime、Automatic Failover、Database RPO=0 或 99.99% Availability。

目标定义不等于达标证明。RTO 从故障被确认影响服务开始，到 Business Service Restored + 关键业务验证通过结束；Container Running 不是 RTO End。真实生产达标必须通过 timestamped Restore Drill 记录 Actual RPO/RTO。

Phase 1 可用性档位固定为 `SINGLE_NODE_RECOVERABLE`，机器契约位于 `deploy/platform/phase1/availability-tier.v1.json`。观测栈按 `observability-core` / `observability-logs` / `observability-full` 三档 profile 选择，Forecast / Optimization / FDD 通过 `intelligence` profile 按需启用；资源档位位于 `deploy/platform/phase1/deployment-tiers.v1.json`。

## 10. 验收来源

机器可读基线：

- `deploy/platform/phase1/architecture-baseline.v1.json`
- `contracts/architecture/edge-control-plane.v1.json`
- `deploy/platform/phase1/alignment-matrix.v1.json`
- `deploy/platform/phase1/recovery/recovery-targets.v1.json`

静态检查入口：

```bash
npm run architecture:phase1:check
```
