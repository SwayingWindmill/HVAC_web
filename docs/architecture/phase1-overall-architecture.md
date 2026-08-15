# Phase 1 总体架构基线

本文件把 `架构规划/智慧能源系统部署与运维架构设计.md` 作为总体与部署架构来源。Phase 1 的实现、部署和验收不得因为已有内部组件更复杂而反向扩大一级架构。

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
                                           Edge Gateway
                                                │
                                           OT Protocol
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

metric-worker
├─ 显式 Binding allowlist
├─ Site timezone + Binding granularity 周期 Finalize
├─ Scheduled run 幂等去重
└─ stale PERSISTING 跨存储 reconcile
```

因此 Phase 1 核心物理形态已收敛为 `energy-api + iot-service + telemetry-worker + metric-worker`。不再单独部署 `platform-gateway`、`iam-service`、`platform-core-service`、`telemetry-query-service`、`audit-ledger-service`、`alarm-service`、`work-order-service`、`command-service`、`mqtt-telemetry-adapter`、`command-dispatcher`、`command-verifier`、`telemetry-history-projector`、`analytics-read-model-projector`；原 `telemetry-runtime-service` 仅保留逻辑 DNS alias。Forecast / Optimization 仍按 V2.1.2 的 selective intelligence services 定位按需部署，不要求成为默认常驻基础进程。Phase 1 不引入 Kubernetes、Service Mesh 或 Kafka 作为必需依赖。

## 4. 数据权威

- PostgreSQL：业务主数据、配置、Registry、控制状态、告警状态等权威业务状态。
- ClickHouse：历史 Telemetry、聚合和分析读取模型。
- Redis：Latest/current 可重建缓存与实时传输基础设施，不保存唯一业务事实。
- Object Storage：Archive / Backup / Evidence / Dataset / Model Artifact 等大对象与冷数据；Phase 1 只冻结 provider-neutral 外部依赖 contract，具体产品由部署环境选择，治理元数据由 PostgreSQL `object_storage_buckets` / `archive_manifests` / `backup_manifests` 权威维护。
- MQTT：Edge ↔ Cloud 传输。
- Edge 本地磁盘：断网期间 Store & Forward；Cloud 恢复后 Replay。

## 5. Cloud / Edge 边界

```text
Cloud
  │ MQTT TLS
  ▼
Edge Gateway
  │ Modbus / BACnet / OPC UA / vendor protocol
  ▼
OT Device
```

Cloud 不直接访问 PLC、BMS、PCS 或其他现场设备。Cloud 故障不得阻止 Edge 继续采集、缓存和执行本地安全策略。

## 6. Phase 1 部署边界

Phase 1 canonical deployment：

```text
Linux Server(s)
+
Docker Compose
```

允许一台服务器，也允许少量服务器按 Application / Data / MQTT 职责拆分。

以下能力不作为 Phase 1 前置：

- Kubernetes
- Service Mesh
- Auto Scaling
- PostgreSQL Replica HA
- ClickHouse Cluster
- MQTT Cluster
- Kafka/Redpanda 作为平台必需 backbone
- Multi Region
- GitOps Controller

这些资产如果已经存在，只能作为未来演进、认证实验或局部测试资产，不能成为 Phase 1 部署成功的必要条件。

## 7. 环境

必须有独立：

```text
Development
Testing
Staging
Production
```

四套环境分别隔离 Database、Redis、MQTT、Storage、Secrets、Domain 和 Configuration。Development/Testing 不得指向 Production 数据库或 MQTT。

## 8. 验收来源

机器可读基线：

- `deploy/platform/phase1/architecture-baseline.v1.json`
- `deploy/platform/phase1/alignment-matrix.v1.json`

静态检查入口：

```bash
npm run architecture:phase1:check
```
