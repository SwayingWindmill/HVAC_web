# 部署拓扑矩阵 V1

状态：CURRENT / RUNTIME-INVENTORY-ALIGNED  
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
| backend-dev | 本地开发 | 1 开发机 | energy-api、identity、scheduler、workers | simulator / MQTT 可选 | local PostgreSQL/ClickHouse/Redis | core | 否 | CURRENT |
| backend-acceptance | 确定性验收 | 1 测试主机 | 同 backend-dev | simulator acceptance | isolated data | core/logs | 否 | CURRENT |
| single-node | 当前 Staging/Production | 1 Linux Server | 单实例；embedded owners 或 owner-split | optional integration | PostgreSQL/ClickHouse/Redis 单实例 | logs/full | 否，可恢复 | CURRENT TARGET |
| split-state | 数据故障域拆分 | 2+ 主机或托管数据服务 | 仍可单实例 | optional / 独立 host | external PG/CH/Redis，先单 primary | externalized 可选 | 否，可恢复 | POSTGRES+CLICKHOUSE+REDIS PLACEMENT READY |
| application-scale-out | 应用容量/维护窗口扩展 | 2+ application nodes | stateless API 多实例、leased workers | 按站点/连接分区 | external stateful plane | externalized | 应用层部分冗余 | GATED |
| stateful-ha | 整体高可用 | 多节点/托管 | 多实例 | MQTT failover | PG/CH/Redis/MQTT HA 或等价恢复契约 | externalized | 是 | FUTURE |
| edge-host | 现场控制 | 现场 Gateway | 非 Backend/UI replica | local protocol bridge | Edge Timedata | local + cloud | local safety | PARALLEL FUTURE |

## 3. Module 放置矩阵

| Module | backend-dev | single-node | split-state | application-scale-out | stateful-ha | edge-host |
| --- | --- | --- | --- | --- | --- | --- |
| Nginx / UI | same host | same host | application host | LB + UI nodes | redundant ingress | local UI optional |
| Energy API | 1 | 1 | 1 | 2+ stateless nodes | 2+ | 不部署 |
| Identity/IAM | embedded/local | local | application host or external | replicated or managed | HA/managed | 不部署 |
| Scheduler | 1 | 1 | 1 | multiple claim owners only after evidence | multiple claim owners | Edge scheduler is different module |
| Maintenance Worker | 1 | 1 | 1 | multiple leased workers only after evidence | multiple leased workers | 不部署 |
| Telemetry Worker | 1 | 1 | 1 | multiple leased workers | multiple leased workers | Edge runtime separate |
| Metric/Energy Worker | 1 | 1 | 1 | multiple leased workers | multiple leased workers | 不部署 |
| iot-service | simulator adapter optional | integration optional | optional integration host | partitioned by site/connection when needed | redundant/partitioned | protocol bridge on Edge |
| MQTT Broker | optional | optional or external | optional integration host | external/managed | failover/clustered | Edge local protocol may differ |
| PostgreSQL | local | single primary | external single primary first | external primary | primary/standby or managed HA | Edge local state separate |
| ClickHouse | local | single projection | external single node first | external stateful plane | replica or verified rebuild contract | Edge Timedata separate |
| Redis | local | mixed projection + short-lived coordination single | external single first | external stateful plane | HA/split only after authority audit | Edge local cache separate |
| Realtime | local optional | single node | application host | multiple nodes + recovery | redundant | local UI channel optional |
| Observability | core | selected profile | same host or external | externalized | externalized/redundant as required | local diagnostics + cloud |

`embedded-owners` / `owner-split` 是 Runtime Mode，可以叠加在 `single-node` 或后续主机拓扑上，不再作为单独 Topology ID。

目标 `single-node` 默认业务面是 `energy-api + telemetry-worker + metric-worker`，`scheduler + maintenance` 是 supporting workloads，`identity-service` 属于 Identity Infrastructure；`iot-service + MQTT` 属于可选 Integration，Forecast / Optimization / FDD 属于可选 `intelligence`。当前 `runtime-inventory.v1.json` 与 `compose.yaml` 已按这一边界收敛；PostgreSQL、ClickHouse、Redis 的 `local-*` profiles 只表达状态服务 placement，不改变业务角色。

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

## 5. 各阶段准入条件

### 5.1 进入 split-state

不需要先证明“集群能力”。只要出现一个真实的状态外置动因即可：

- PostgreSQL / ClickHouse I/O 或容量开始挤压应用进程；
- 数据备份、恢复或维护需要与应用主机解耦；
- 应用主机损坏不应同时损坏主要数据盘；
- 已经采用托管 PostgreSQL / Redis 等外部服务。

本阶段验证连接、迁移、备份和恢复即可，不做多副本故障矩阵。

### 5.2 进入 application-scale-out

需要有应用侧容量或维护窗口证据：

- 目标设备、点、事件、API、Realtime 连接和 worker backlog；
- 单节点 CPU、内存、网络的实测 headroom；
- Energy API / worker / iot-service 中至少一类出现实际扩展需求。

只有多实例会改变正确性时才验证并发语义：

- 多个 Energy API 节点不会重复 command side effect；
- 多个 worker 通过 PostgreSQL lease / claim 不重复执行；
- Scheduler/outbox 在并发 owner 下不重复；
- current/history projection 不倒退；
- UI realtime 断线后能回到 authoritative snapshot。

### 5.3 进入 stateful-ha

需要明确的可用性目标和对应故障证据：

- PostgreSQL primary failover 和 old-primary fencing；
- MQTT broker failover；
- Redis unavailable / failover；
- ClickHouse replica failover 或经过验证的 rebuild；
- ingress/application rolling upgrade。

没有这些需求和证据时，不因为仓库里存在 Helm、Kustomize 或多节点 YAML 就声明 Cluster Ready。

## 6. 当前 Compose 的调整方向

当前 `deploy/platform/phase1/compose.yaml` 继续作为 Stage 1 `single-node` 的实现来源，下一步只做必要收敛：

- 默认 Compose 保留 Backend/UI + core data；
- 把 MQTT/iot-service 抽为可选 Integration overlay/profile，修正当前 runtime inventory 漂移；
- `owner-split.compose.yaml` 保留为 Runtime Mode overlay，不新增第二套单机基线；
- backup/migration/bootstrap 保持 operations action，不进入常驻业务拓扑；
- intelligence 继续保持可选；
- Stage 2 先通过外部 DSN/endpoint 支持 external data，不提前创建复杂多机编排；
- Stage 3/4 没有真实需求前不新增 Helm/Kustomize 生产资产。

## 7. 来源和裁决

- ThingsBoard：采用“运行角色拆分”和“单机/集群”两条独立轴，并参考其 Standalone -> External DB -> Cluster 的容量驱动思路；不采用 Kafka/actor 作为 Phase 1 前置；
- GitLab Reference Architectures：采用小规模 standalone + backup，以及 Cloud Native/Hybrid 中 stateless workloads 与 external PostgreSQL/Redis/Object Storage 分离的思路；不复制其 GitLab 专用节点数量；
- OpenEMS：采用 Edge、Backend、UI 分离，以及 Edge-facing application 可从同机独立扩出的模式；不采用其开发型 latest 镜像和直接暴露端口作为生产标准；
- MyEMS：采用 acquisition/cleaning/normalization/aggregation 的独立职责，不采用 13 个物理数据库作为当前部署要求。

