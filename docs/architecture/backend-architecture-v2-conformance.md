# 后端架构 V2.1.2 对齐说明

本文记录当前仓库对 SE-ARCH-004 V2.1.2 的实现状态。机器权威为 `contracts/architecture/backend-architecture.v2.json`；公共 API 机器权威为 `contracts/http/platform-gateway.openapi.yaml`（SE-API-001）。

## 1. 已裁决覆盖

- 项目中的 Organization 平台模型统一替换为 Tenant，不提供 Organization 兼容层或别名。第三方 Provider 自身的原生 organization 术语不属于平台领域模型。
- ThingsBoard 已退出当前项目，不属于 IoT、Telemetry 或 Control 生产路径。
- Point 是 canonical 数据点；Sensor 仅在存在可独立识别、安装、更换、校准、追溯的真实物理探头时出现。
- PostgreSQL 负责业务主数据、元数据与状态机；ClickHouse 负责历史 Telemetry/分析；Redis 仅为可重建 Latest/current 缓存；MQTT 仅为传输；Object Storage 负责归档、备份、Evidence、Dataset、Model Artifact 等对象能力。

## 2. 逻辑领域与物理部署

V2.1.2 定义 19 个逻辑领域：

1. IAM
2. Tenant/Site
3. Space/Asset
4. Device/Product/Point
5. IoT Runtime
6. Telemetry
7. Energy Topology
8. Metric
9. Energy
10. Tariff
11. Settlement
12. Alarm
13. Control
14. Config
15. Notification
16. Forecast
17. Optimization
18. MLOps Metadata
19. Audit

逻辑领域不等于 Deployable Service，不允许按表拆服务。Phase 1 默认业务物理形态已收敛为 `energy-api + iot-service + telemetry-worker + metric-worker`；Forecast / Optimization 作为 selective intelligence services 按需部署，因此 `PHASE1_PHYSICAL_SERVICE_CONVERGENCE=PASS`。

## 3. SE-API-001

`contracts/http/platform-gateway.openapi.yaml` 是唯一公共 OpenAPI 3.1 机器权威，已包含 V2.1.2 的 21 条 `[CONTRACTED]` method/path。

已完成的运行时收敛包括：

- 登录只使用 `POST /api/v1/auth/login`。
- Command approve 只使用 `/api/v1/commands/{commandId}/approve`，生产解析不保留 `:approve`。
- Alarm 公共子契约只保留 `/api/v1/alarms`、`/api/v1/alarms/{alarmId}`、`/api/v1/alarms/{alarmId}/ack`。
- verified Telemetry workload 使用 Tenant 上下文，不再允许旧 `X-Organization-ID` compatibility exception。
- Operations Agent Registry grant 使用 `X-Operations-Registry-Asset-Grant`；旧 Equipment grant 不再作为运行时输入。

完整 SE-API-001 详细 request/query/domain shape 尚未提供的路由不得由仓库自行发明参数。当前 13 条 shape-pending canonical 路由由 Gateway 明确识别为 contract-only：正确 method 返回 `503 CONTRACT_NOT_ACTIVE`，错误 method 返回 405，二者都使用冻结的 V2.1.2 error envelope，不再依赖旧 Site-scoped handler 或偶然 route-missing 404。Alarm 仍因缺少无 Site path 下 exact Site 授权的冻结规则而不启用真实业务读取/ACK。因此 `SE_API_001_RUNTIME_CONVERGENCE` 仍为 PARTIAL。

## 4. Device / IoT / Telemetry / Control

设备侧生产链路：

```text
Device / Edge Gateway
  -> MQTT TLS
  -> iot-service
  -> telemetry-worker
  -> PostgreSQL authoritative current state
  -> Redis rebuildable Latest/current cache
  -> ClickHouse history / analytics
```

真实控制必须先进入 Control 逻辑域：

```text
Web / Business / Optimization
  -> energy-api / Command Module
  -> iot-service dispatch + verification
  -> MQTT
  -> Edge Gateway
  -> Device
```

Forecast、Optimization、Web 与 Energy 逻辑域不得直接持有 MQTT Publish Authority。

## 5. 事件与跨存储发布

Phase 1 durable business event 使用 PostgreSQL Outbox，采用 at-least-once delivery，并以 `event_id + consumer_name` 保证消费幂等。Kafka 不是 Phase 1 必需组件。

Metric Result、Forecast Result、Optimization Evaluation 使用统一跨存储发布状态：

```text
PostgreSQL PERSISTING
  -> ClickHouse stable result identity
  -> PostgreSQL PERSISTED + durable Outbox
```

stale `PERSISTING` 必须通过 ClickHouse evidence 进行 reconcile。

## 6. 当前两个 PARTIAL

`PHASE1_PHYSICAL_SERVICE_CONVERGENCE` 已完成：默认业务部署已收敛为 `energy-api + iot-service + telemetry-worker + metric-worker`。`energy-api` 合并 Platform Gateway、IAM、Registry Core、Telemetry Query、Audit、Alarm、Work Order、Command，同时保留各逻辑 owner 的 mTLS listener / SPIFFE identity；Session Audit 使用 PostgreSQL Outbox 直投，不依赖 Kafka；`iot-service` 合并 MQTT 上行、Command dispatch/verification；`telemetry-worker` 合并 Telemetry Runtime、history projection、analytics projection；`metric-worker` 执行显式 Scheduled Binding Finalize 与 scoped reconcile。Forecast / Optimization 保持 selective intelligence services。

当前仅剩：

1. `SE_API_001_RUNTIME_CONVERGENCE`：21 条路径已进入唯一机器契约，但缺少的 SE-API-001 详细 shape 不允许自行补造。
2. `OBJECT_STORAGE_PRODUCTION_ROLE`：V2.1.2 已冻结 Object Storage 能力职责，但未冻结具体厂商。Phase 1 已部署 Data Governance + Object Storage Governance PostgreSQL 权威模型，并新增 provider-neutral `deploy/platform/phase1/object-storage.external.v1.json`；生产环境只注入 provider、endpoint reference、credential reference 与 bucket catalog reference。实际生产 endpoint / bucket / credential 尚未 provision，因此仍为 PARTIAL。

Object Storage 厂商不得由仓库自行裁决；仓库不得硬编码产品镜像、生产凭据或以 PostgreSQL BLOB 作为兼容回退。

## 7. 对齐检查

机器基线：

- `contracts/architecture/backend-architecture.v2.json`

静态一致性检查：

```bash
npm run backend:architecture:check
```

该检查只验证 V2.1.2 当前态一致性，不是合并、发布或验收门禁。
