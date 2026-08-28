# 后端架构 V2.1.2 对齐说明

本文记录当前仓库对 SE-ARCH-004 V2.1.2 的实现状态。机器权威为 `contracts/architecture/backend-architecture.v2.json`；公共 API 机器权威为 `contracts/http/platform-gateway.openapi.yaml`（SE-API-001）。

## 1. 已裁决覆盖

- 项目中的 Organization 平台模型统一替换为 Tenant，不提供 Organization 兼容层或别名。第三方 Provider 自身的原生 organization 术语不属于平台领域模型。
- ThingsBoard 已退出当前项目，不属于 IoT、Telemetry 或 Control 生产路径。
- Point 是 canonical 数据点；Sensor 仅在存在可独立识别、安装、更换、校准、追溯的真实物理探头时出现。
- PostgreSQL 负责业务主数据、元数据与状态机；ClickHouse 负责历史 Telemetry/分析；Redis 仅为可重建 Latest/current 缓存；MQTT 仅为传输；Object Storage 负责归档、备份、Evidence、Dataset、Model Artifact 等对象能力。
- 2026-08-17 用户基于 OpenEMS 对 Edge 架构重新裁决：生产可控 HVAC 必须具备独立 HVAC Edge Control Plane；机器权威为 `contracts/architecture/edge-control-plane.v1.json`，详细裁决为 `docs/architecture/openems-architecture-adjudication.md`。旧“Edge Gateway 只负责协议转发”的表述被正式淘汰。

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

逻辑领域不等于 Deployable Service，不允许按表拆服务。Phase 1 默认业务物理形态已收敛为 `energy-api + telemetry-worker + metric-worker`。Application `scheduler` 与 `maintenance` 是 supporting workload：前者只做跨领域 Job Coordination，后者执行证书到期扫描、Dead Work 处置与 Tenant Retirement 等 operational job；它们都不新增业务 Domain/API authority。`identity-service` 是独立 Identity Infrastructure。`iot-service + mqtt-broker` 作为 `integration` profile 按部署需要启用；Forecast / Optimization / FDD 统一作为 `intelligence` profile 下的 selective intelligence services 按需部署，因此 `PHASE1_PHYSICAL_SERVICE_CONVERGENCE=PASS`。完整运行项分类由 `deploy/platform/phase1/runtime-inventory.v1.json` 冻结。

## 3. SE-API-001

`contracts/http/platform-gateway.openapi.yaml` 是唯一公共 OpenAPI 3.1 机器权威，已包含 V2.1.2 的 21 条 `[CONTRACTED]` method/path。

已完成的运行时收敛包括：

- 登录只使用 `POST /api/v1/auth/login`。
- Command approve 只使用 `/api/v1/commands/{commandId}/approve`，生产解析不保留 `:approve`。
- Alarm 公共子契约只保留 `/api/v1/alarms`、`/api/v1/alarms/{alarmId}`、`/api/v1/alarms/{alarmId}/ack`。
- verified Telemetry workload 使用 Tenant 上下文，不再允许旧 `X-Organization-ID` compatibility exception。
- Operations Agent Registry grant 使用 `X-Operations-Registry-Asset-Grant`；旧 Equipment grant 不再作为运行时输入。

`SE-API-001 V1.2 CURRENT CANDIDATE` 已完成逐路由审查，结果固化在 `contracts/architecture/se-api-001-v1.2-runtime-convergence.json`。Alarm Domain 已由 `SE-DOMAIN-ALARM-001 V1.0` 补齐公开分页、BOLA-safe `alarmId -> Site` 解析以及 ACK 独立事实/幂等语义，因此 3 条 Alarm 路由已同步 OpenAPI 并正式激活；当前严格分类为 `A=3 / B=0 / C=10`。剩余 10 条又进一步对照 `SE-DATA-001 V2.0 CURRENT`、`SE-AI-001 V1.0 CURRENT`、`SE-AI-002 V1.0 CURRENT`、当前 Registry/S2 machine contract 以及 Forecast/Optimization 真实 runtime 重新审查：Registry 仍缺 Create Site、Space Tree、跨 Site Device List、Device Point List 的 endpoint-specific machine projection；Point-centric Latest/History 与现有 Device-centric S2 Snapshot/History contract 语义不同；Forecast 当前公开 API 在详细设计中仍为建议/示例，而 runtime 只有内部 `POST /v1/forecast`；Optimization 虽冻结 Run 核心字段和状态，但公开 Run API 仍是建议级，runtime 只有同步内部 `POST /v1/optimize`，没有 durable public Run resource。故只有这 10 条继续由 Gateway fail-closed：正确 method 返回 `503 CONTRACT_NOT_ACTIVE`，错误 method 返回 405。`SE_API_001_RUNTIME_CONVERGENCE` 保持 PARTIAL，且不通过伪造 public Schema 来清零。

## 4. Device / IoT / Telemetry / Control

新的生产链路明确分成 Cloud Control Plane 与 HVAC Edge Control Plane：

```text
OT Device
  -> Device Driver / Protocol Bridge
  -> Edge Channel.nextValue
  -> Process Image / Cycle
  -> Edge Timedata / publication policy
  -> MQTT TLS
  -> iot-service
  -> telemetry-worker
  -> PostgreSQL authoritative business/current state
  -> Redis rebuildable Latest/current cache
  -> ClickHouse history / analytics
```

真实控制必须先进入 Cloud Control Domain，再作为 governed leased intent 进入 Edge 本地裁决：

```text
Web / Business / Optimization
  -> energy-api / Command Module
  -> IAM / approval / audit / idempotency
  -> iot-service dispatch
  -> MQTT
  -> Edge Command Intent Controller
  -> Safety / Interlock / Equipment / Cluster Controllers
  -> Scheduler / Control Arbiter
  -> effectiveValue
  -> Device Driver / Protocol Bridge
  -> Device
  -> Edge readback
  -> Cloud durable verification
```

`Command Dispatcher` 不再等同于最终 actuator decision；最终有效值由 Edge Control Arbiter 决定。Forecast、Optimization、Web 与 Energy 逻辑域不得直接持有 MQTT Publish Authority，也不得绕过 Edge Controller/Safety/Interlock 直接写现场设备。

`Point` 仍是 Cloud canonical 数据点，Edge 新增运行时 `Channel`；二者通过显式 Point↔Channel 映射关联。Controller 面向 `Capability Profile`，厂家/型号差异进入 `Device Driver`，连接/轮询/重试进入 `Protocol Bridge`。

## 5. 事件与跨存储发布

Phase 1 durable business event 使用 PostgreSQL Outbox，采用 at-least-once delivery，并以 `event_id + consumer_name` 保证消费幂等。Kafka 不是 Phase 1 必需组件。

Metric Result、Forecast Result、Optimization Evaluation 使用统一跨存储发布状态：

```text
PostgreSQL PERSISTING
  -> ClickHouse stable result identity
  -> PostgreSQL PERSISTED + durable Outbox
```

stale `PERSISTING` 必须通过 ClickHouse evidence 进行 reconcile。

## 6. 当前三个 PARTIAL

`PHASE1_PHYSICAL_SERVICE_CONVERGENCE` 已完成：默认业务部署已收敛为 `energy-api + telemetry-worker + metric-worker`。`energy-api` 合并 Platform Gateway、IAM、Registry Core、Telemetry Query、Audit、Alarm、Work Order、Command，同时保留各逻辑 owner 的 mTLS listener / SPIFFE identity；Session Audit 使用 PostgreSQL Outbox 直投，不依赖 Kafka；`telemetry-worker` 合并 Telemetry Runtime、history projection、analytics projection；`metric-worker` 只执行由 Application Scheduler 协调产生的 `METRIC_*` durable Job 与 scoped reconcile，不再拥有 Schedule scan authority。独立 `scheduler` 只负责跨领域 Job Coordination；`maintenance` 执行跨领域 operational maintenance job 并保持独立最小权限，两者均为 supporting workload，不计作新的业务 deployable。`identity-service` 继续作为独立 Identity Infrastructure。`iot-service` 合并 MQTT 上行、Command dispatch/verification，并与 `mqtt-broker` 一起作为可选 `integration` profile 启用。Forecast / Optimization / FDD 保持 `intelligence` profile 下的 selective intelligence services。机器分类以 `deploy/platform/phase1/runtime-inventory.v1.json` 为准。

当前仅剩：

1. `SE_API_001_RUNTIME_CONVERGENCE`：21 条路径已进入唯一机器契约，但缺少的 SE-API-001 详细 shape 不允许自行补造。
2. `OBJECT_STORAGE_PRODUCTION_ROLE`：V2.1.2 已冻结 Object Storage 能力职责，但未冻结具体厂商。Phase 1 已部署 Data Governance + Object Storage Governance PostgreSQL 权威模型，并新增 provider-neutral `deploy/platform/phase1/object-storage.external.v1.json`；生产环境只注入 provider、endpoint reference、credential reference 与 bucket catalog reference。实际生产 endpoint / bucket / credential 尚未 provision，因此仍为 PARTIAL。
3. `EDGE_CONTROL_PLANE_TARGET`：OpenEMS 对照裁决已把 Process Image、IPO Cycle、Controller/Scheduler/Arbiter、Capability Profile、Device Driver、Protocol Bridge、Edge Manifest、Edge Timedata、leased remote intent 纳入目标；当前 EG8200/MQTT/store-forward/Command safety 只能算基础能力，因此 IoT Runtime 与 Control 对新的目标均为 PARTIAL。

Object Storage 厂商不得由仓库自行裁决；仓库不得硬编码产品镜像、生产凭据或以 PostgreSQL BLOB 作为兼容回退。

## 7. 对齐检查

机器基线：

- `contracts/architecture/backend-architecture.v2.json`

静态一致性检查：

```bash
npm run backend:architecture:check
```

该检查只验证 V2.1.2 当前态一致性，不是合并、发布或验收门禁。
