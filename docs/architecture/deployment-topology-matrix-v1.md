# 部署拓扑矩阵 V1

状态：PROPOSED / SOURCE-ALIGNED  
关联基线：docs/architecture/deployment-architecture-v1.md

## 1. 拓扑不是环境

Environment 说明数据、Secret、域名和发布隔离；Topology 说明运行哪些 Module、哪些网络区和哪些数据角色；Resource Tier 说明容量；Observability Tier 说明信号。

四者必须独立选择：

~~~text
Environment
  × Topology
  × Resource Tier
  × Observability Tier
~~~

不能因为某个环境叫 Production，就自动推导出 Cluster；也不能因为选择了 single-full，就把 Edge 或 Intelligence 当成核心链路。

## 2. 拓扑总矩阵

| Topology ID | 用途 | 节点形态 | Backend/UI | Integration | Data | Observability | HA | 状态 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| backend-dev | 本地开发 | 1 开发机 | energy-api、identity、scheduler、workers | simulator，可选 MQTT | local PostgreSQL/ClickHouse/Redis | core | 否 | 应优先固化 |
| backend-acceptance | 确定性验收 | 1 测试主机 | 同 backend-dev | simulator acceptance | isolated data | core/logs | 否 | 当前已有资产 |
| single-node | Phase 1 Staging/Production | 1 Linux Server | Compose 单实例 | optional integration | PostgreSQL/ClickHouse/Redis 单实例 | logs/full | 否，可恢复 | CURRENT |
| single-node-owner-split | Stage 1 验证 | 1 Linux Server | owner 独立 Module | optional integration | 同 single-node | full | 否，可恢复 | DRILL REQUIRED |
| cluster-application | 未来应用扩容 | 多节点 | Backend/UI 多实例、workers 多 owner | external/clustered adapter | 数据仍需 HA 契约 | externalized | 部分 | GATED |
| cluster-data | 未来数据高可用 | 多节点/托管 | Backend/UI 多实例 | MQTT failover | PG/CH/Redis/MQTT HA | externalized | 是 | FUTURE |
| edge-host | 现场控制 | 现场 Gateway | 非 Backend/UI replica | local protocol bridge | Edge Timedata | local + cloud | local safety | FUTURE |

## 3. Module 放置矩阵

| Module | backend-dev | single-node | owner-split | cluster-application | edge-host |
| --- | --- | --- | --- | --- | --- |
| Nginx / UI | same host | same host | same host | ingress + UI nodes | local UI optional |
| Energy API | 1 | 1 | 1 gateway | 2+ stateless nodes | 不部署 |
| Identity/IAM | embedded/local | embedded/local | independent owner | replicated or managed | 不部署 |
| Scheduler | 1 | 1 | 1 | multiple claim owners | Edge scheduler is different module |
| Telemetry Worker | 1 | 1 | 1 | multiple leased workers | Edge runtime separate |
| Metric/Energy Worker | 1 | 1 | 1 | multiple leased workers | 不部署 |
| iot-service | simulator adapter optional | integration optional | integration optional | external/replicated | protocol bridge on Edge |
| MQTT Broker | optional | optional or external | optional or external | clustered/managed | Edge local protocol may differ |
| PostgreSQL | local | single primary | single primary | HA primary/standby | Edge local state separate |
| ClickHouse | local | single projection | single projection | replica or rebuild contract | Edge Timedata separate |
| Redis | local | rebuildable single | rebuildable single | HA only after authority audit | Edge local cache separate |
| Realtime | local optional | single node | single node | multiple nodes + recovery | local UI channel optional |
| Observability | core | selected profile | full for drill | externalized | local diagnostics + cloud |

## 4. Backend/UI 最小形态

backend-dev 和 single-node 必须可以在不启动 Edge Host 的情况下完成：

~~~text
Registry
  ↓
Simulator / Integration Adapter
  ↓
Raw + Current + History
  ↓
Energy Processing
  ↓
Backend Query
  ↓
UI
~~~

如果没有真实 Edge，输入可以来自 simulator；但 simulator 只能替换 Integration Adapter，不能替换 Backend、Energy Processing、Command Governance 或 UI Query module。

## 5. 集群准入条件

cluster-application 或 cluster-data 只有在以下证据齐全后才能进入实现：

### 容量证据

- 目标设备、点、事件、历史写入和查询量；
- 单节点 CPU、内存、磁盘和网络的实测 headroom；
- 处理延迟、实时延迟、队列 backlog 和恢复时间；
- ClickHouse 写入与查询压力；
- PostgreSQL transaction/lock/connection 指标；
- Redis 使用模式和失败时影响。

### 正确性证据

- 多个 Energy API 节点不会重复 command side effect；
- 多个 worker 通过 PostgreSQL lease 只能有一个有效 owner；
- Scheduler/outbox 在并发 owner 下不重复；
- current/history projection 不倒退；
- command intent 的 idempotency、fence 和 outcome 不重复；
- UI realtime 断线后能回到 authoritative snapshot。

### 故障证据

- 单个 Backend node kill；
- 单个 worker kill；
- PostgreSQL primary failover；
- MQTT broker failover；
- Redis unavailable；
- ClickHouse unavailable/rebuild；
- rolling upgrade；
- old primary fencing。

没有这些证据时，不允许用“集群部署文件已存在”声明 Cluster Ready。

## 6. 当前 Compose 的调整方向

当前 deploy/platform/phase1/compose.yaml 可以继续作为 single-node 的实现来源，但后续应：

- 把 backend-dev 和 single-node 的差异放入 topology contract；
- 把 MQTT/iot-service 放入 Integration profile；
- 把 owner-split 标记为 Stage 1，不作为默认生产拓扑；
- 将 backup/migration/bootstrap 作为 operations action，不混入常驻业务拓扑；
- 将 intelligence 继续保持可选；
- 将 future cluster 只写在演进契约和准入矩阵，不提前制造未验证的 Compose。

## 7. 来源和裁决

- ThingsBoard：采用 topology role switching 和单节点/集群分层，不采用 Kafka/actor 作为 Phase 1 前置；
- OpenEMS：采用 Edge、Backend、UI 分离，不采用其开发型 latest 镜像和直接暴露端口作为生产标准；
- MyEMS：采用 acquisition/cleaning/normalization/aggregation 的独立职责，不采用 13 个物理数据库作为当前部署要求。

