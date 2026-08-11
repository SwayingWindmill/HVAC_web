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
- Alarm
- Command / Control
- Work Order
- Optimization

浏览器只通过 Nginx 和 Platform Gateway 进入业务 API，不直接访问数据库、MQTT 管理面、ThingsBoard 或内部服务。

## 3. 三级实现映射

当前实现允许把一个逻辑职责拆成多个进程，只要不改变一级架构。例如：

```text
Telemetry
├─ mqtt-telemetry-adapter
├─ thingsboard-telemetry-adapter
├─ telemetry-runtime-service
├─ telemetry-history-projector
├─ telemetry-query-service
└─ analytics-read-model-projector
```

这种拆分属于实现细节。Phase 1 不因为进程数量增加而要求 Kubernetes、Service Mesh 或 Kafka。

## 4. 数据权威

- PostgreSQL：业务数据、配置、Registry、当前遥测状态、控制状态、告警状态等权威状态。
- ClickHouse：历史 Telemetry 和分析读取模型。
- Redis：可重建缓存与实时传输基础设施，不保存唯一业务事实。
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
