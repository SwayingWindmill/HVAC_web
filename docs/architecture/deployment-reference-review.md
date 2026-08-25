# 三参考项目部署源码审查

状态：REVIEWED / SOURCE-ALIGNED  
审查目标：确定单节点、集群、Backend/UI、Integration、Edge 的部署取舍  
本项目现有部署不是裁决依据，固定源码证据优先。

## 1. 审查基线

| 项目 | 固定基线 | 主要部署证据 |
| --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 / c2a52e46c44e308ddee430e7266b8e10eddde9c4 | docs/architecture/thingsboard-source-review.md；官方 Docker 与集群部署文档 |
| OpenEMS | 2026.7.0 / 2e2792d | E:/Code/openems/docker-compose.yml；E:/Code/openems/tools/docker/backend/docker-compose.yml；E:/Code/openems/tools/docker/edge/docker-compose.yml；E:/Code/openems/tools/docker/backend-edge/docker-compose.yml |
| MyEMS | v6.7.0 / be6e6ce | README、database/README.md、myems-api、myems-modbus-tcp、myems-cleaning、myems-normalization、myems-aggregation、myems-admin、myems-web |

## 2. ThingsBoard

### 源码/官方部署事实

- 官方 Docker 形态存在单节点 Compose；
- 应用节点、PostgreSQL 和消息队列是独立部署角色；
- queue type 可在 in-memory、Kafka 等形态间切换；
- 集群部署进一步拆分 web、transport、rule、executor 等运行角色；
- Edge 通过独立连接进入 Cloud，而不是把 Edge 当作 Cloud 应用节点的普通副本。

官方参考：[Docker installation](https://thingsboard.io/docs/installation/docker/)、[Docker Compose cluster setup](https://thingsboard.io/docs/installation/docker-compose-setup/)、[Architecture](https://thingsboard.io/docs/reference/architecture/)

### ADOPT

- 单节点到多节点有明确 topology stage；
- 应用运行角色和 durable queue/data role 分开；
- 集群准入依赖吞吐、故障和扩展证据；
- Edge 是独立运行形态。

### ADAPT

- 本项目使用 PostgreSQL Outbox、Job Lease、Idempotency 作为当前 durable backbone；
- 未来集群才评估 Kafka/其他消息平台；
- energy-api embedded owners 到 owner-split 是本项目的 Stage 0/1。

### REJECT

- 不把 Kafka、actor runtime 或 ZooKeeper 作为 Phase 1 前置；
- 不复制通用 IoT transport 的全部角色；
- 不把 ThingsBoard Cluster 的部署数量当作 HVAC 的目标数量。

## 3. OpenEMS

### 源码事实

OpenEMS 源码仓库存在清晰的部署组合：

- 根目录 Compose：Edge + UI；
- Backend Compose：Backend + UI + InfluxDB；
- Edge Compose：Edge + UI；
- Backend-Edge Compose：Backend-Edge 独立形态；
- UI Compose：UI 作为独立静态/代理模块。

本地证据：

- [OpenEMS root docker-compose.yml](E:/Code/openems/docker-compose.yml)
- [OpenEMS Backend Compose](E:/Code/openems/tools/docker/backend/docker-compose.yml)
- [OpenEMS Edge Compose](E:/Code/openems/tools/docker/edge/docker-compose.yml)
- [OpenEMS Backend-Edge Compose](E:/Code/openems/tools/docker/backend-edge/docker-compose.yml)

官方架构参考：[OpenEMS Backend Architecture](https://openems.github.io/openems.io/openems/latest/backend/architecture.html)、[OpenEMS Edge Architecture](https://openems.github.io/openems.io/openems/latest/edge/architecture.html)

### ADOPT

- Edge、Backend、UI 具有独立部署形态；
- Edge 本地运行时和 Backend 数据/管理面不混为一个部署角色；
- Edge 的本地数据与 Cloud 的 Backend Timedata 具有不同职责。

### ADAPT

- 本项目当前先实现 Backend/UI Base；
- Integration/simulator 作为可选形态；
- Edge Host 未来独立部署；
- OpenEMS 的 Edge manifest、Channel 和 control seam 不改变当前 Cloud 部署数量。

### REJECT

- 不采用 latest image tag 作为生产发布策略；
- 不复制直接暴露 Apache Felix、WebSocket 管理端口的开发型 Compose；
- 不把 OpenEMS Java/OSGi runtime 作为 HVAC Backend 依赖。

## 4. MyEMS

### 源码事实

MyEMS 的官方数据库设计把部署和处理职责拆成：

~~~text
myems-modbus-tcp
  ↓
myems_historical_db
  ↓
myems-cleaning
  ↓
myems-normalization
  ↓
myems-aggregation
  ↓
energy / billing / carbon databases
  ↓
myems-api
  ↓
myems-web / myems-admin
~~~

官方设计还列出 13 个逻辑数据库，并明确 acquisition、cleaning、normalization、aggregation、API、Admin/Web UI 的独立职责。[MyEMS database design](https://github.com/MyEMS/myems/blob/master/database/README.md)

### ADOPT

- Acquisition 与能源处理分开；
- Cleaning、Normalization、Aggregation 作为不同处理职责；
- Admin UI 和用户 Web UI 具有不同部署任务；
- 数据库/数据集按用途、写入模式和保留周期区分。

### ADAPT

- 本项目先使用 PostgreSQL、ClickHouse、Redis、Object Storage 的逻辑数据集；
- 当前可用 MQTT/simulator adapter 替代真实 Modbus acquisition；
- 处理 module 可以先在当前 Go workers 中落地，不立即增加生产进程数量；
- 集群形态由实际吞吐和恢复目标驱动。

### REJECT

- 不直接复制 13 个物理数据库；
- 不把 MyEMS 的安装脚本和默认密码/默认暴露端口当作生产安全标准；
- 不把定时汇总模块当作 Edge 实时控制模块。

## 5. 对 HVAC_web 的最终裁决

| 议题 | 裁决 |
| --- | --- |
| 当前生产 | Single Node + Docker Compose |
| 当前 Backend/UI | 独立于 Edge Host，Integration 可选 |
| 当前数据 | PostgreSQL/ClickHouse/Redis 保持单节点，但 authority/rebuildability 必须分开 |
| 当前集群 | 只做文档、契约和准入条件，不做默认部署 |
| 未来应用集群 | 先扩 stateless Backend/UI 和 leased workers |
| 未来数据集群 | PostgreSQL、MQTT、ClickHouse、Redis 分别以故障证据准入 |
| 未来 Edge | 单独 Host、单独 Release/Manifest/Recovery 文档 |
| Kafka/Kubernetes | Future Stage，不是 Phase 1 前置 |

## 6. 尚未确认的内容

- ThingsBoard CE 固定 tag 的完整生产集群容量数据；
- OpenEMS 官方生产 HA/升级/备份 runbook；
- MyEMS 社区版当前完整容器编排和生产恢复证据；
- HVAC_web 集群所需的实际容量、连接数、事件量和查询量。

这些内容在没有源码、官方文档或实测证据前，不进入当前部署结论。

