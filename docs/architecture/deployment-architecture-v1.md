# 部署架构 V1：Backend/UI、集成与 Edge 分层

状态：PROPOSED / SOURCE-ALIGNED  
适用范围：当前 Backend/UI 交付、单节点生产、未来集群演进  
当前生产形态：1 Linux Server + Docker Compose  
当前 Edge 状态：不作为 Backend/UI 交付前置，保留独立演进形态

## 1. 结论

本项目需要同时规划单节点和集群，但二者不是并列的当前部署选项：

- 单节点是当前 Phase 1 的正式生产形态；
- 集群是满足容量、可用性或恢复目标后才进入的未来形态；
- Edge Host 是另一种部署形态，不属于 Backend/UI 集群的一个副本；
- Integration、Observability、Intelligence 是可选部署能力，不应改变 Backend/UI 的核心职责。

当前仓库的 Phase 1 总体基线仍由 deploy/platform/phase1/architecture-baseline.v1.json 和 docs/architecture/phase1-overall-architecture.md 约束。本文件先把部署形态、拓扑和演进关系补齐，不自动覆盖现有机器契约。

## 2. 目标部署层

~~~text
Public Ingress
  └─ Nginx / HTTPS / WSS

Backend/UI Control Plane
  ├─ Energy API / BFF
  ├─ IAM / Identity
  ├─ Registry / Query / Command / Alarm / Work Order
  ├─ Scheduler
  ├─ Telemetry Worker
  ├─ Metric / Energy Processing Worker
  └─ Realtime Transport

Data Plane
  ├─ PostgreSQL: business and configuration authority
  ├─ ClickHouse: history and analytics facts
  ├─ Redis: rebuildable projection and realtime support
  └─ Object Storage: archive, backup, export, artifact

Integration Plane
  ├─ MQTT / HTTP / future protocol adapters
  ├─ Simulator fixture
  └─ External acquisition

Operations Plane
  ├─ Migration / schema preflight
  ├─ Backup / restore
  ├─ Metrics / logs / traces
  ├─ Release evidence
  └─ Host and container limits

Future Edge Host
  ├─ Channel / Process Image / Cycle
  ├─ Controller / Scheduler / Arbiter
  ├─ Safety / Interlock
  ├─ Driver / Protocol Bridge
  └─ Edge Timedata / Store and Forward
~~~

Edge Host 通过未来的 manifest、command intent、reported state、readback evidence seam 接入，不通过 Backend 进程直接访问 OT 设备。

## 3. 网络区

| 网络区 | 当前职责 | 允许进入 | 禁止进入 |
| --- | --- | --- | --- |
| Public | HTTPS、WSS；未来可选 MQTT TLS | Nginx、按形态启用的 MQTT ingress | PostgreSQL、ClickHouse、Redis、内部 module |
| Application | Backend/UI module 间的内部调用 | Energy API、Identity、workers、Realtime | 外部客户端、数据库管理面 |
| Data | 数据库和缓存 | PostgreSQL、ClickHouse、Redis、受限数据 adapter | 浏览器、公开端口 |
| Integration | MQTT、协议 adapter、simulator | Integration module、IoT adapter | UI 直接订阅、业务表直接写入 |
| Observability | metrics、logs、traces | Prometheus、OTel、Loki、Tempo、Grafana | 反向修改业务事实 |
| Management | SSH/VPN、发布、备份、恢复 | 运维操作者和受控工具 | 公网业务流量 |

## 4. 四种部署形态

### 4.1 Backend/UI Development

用途：本地开发、Backend/UI 联调、确定性 simulator fixture。

允许：

- Docker Compose；
- 本地 PostgreSQL、ClickHouse、Redis；
- simulator；
- 可选 MQTT；
- 精简 Observability。

禁止：

- 使用 Production 数据；
- 使用 Production MQTT；
- 把 simulator 伪装成 Edge Host；
- 依赖集群设施才能启动。

### 4.2 Backend/UI Single Node

用途：当前 Phase 1 Staging/Production 基线。

形态：

~~~text
1 Linux Server
└─ Docker Compose
   ├─ Nginx
   ├─ Backend/UI modules
   ├─ PostgreSQL
   ├─ ClickHouse
   ├─ Redis
   ├─ optional Integration
   └─ selected Observability
~~~

属性：

- SINGLE_NODE_RECOVERABLE；
- 不声明数值化 HA SLO；
- PostgreSQL、ClickHouse、Redis、MQTT、Realtime、Observability 都有明确的单点故障说明；
- 依靠 off-server backup、versioned config、restore drill 和可重建 projection 恢复；
- 生产部署不要求 Kubernetes、Kafka、Service Mesh 或多节点数据集群。

### 4.3 Backend/UI Cluster

用途：未来多实例、组件冗余或跨节点容量。

集群不是把当前 Compose 复制到多台机器。它必须同时解决：

- Public ingress 的负载均衡；
- stateless Backend/UI module 的多实例；
- worker 的 claim/lease/dedup；
- PostgreSQL 的主备、故障切换和 fencing；
- ClickHouse 的副本或可验证重建；
- Redis authority boundary 后的高可用；
- MQTT session、订阅和 Edge reconnect；
- Realtime transport 的多节点恢复；
- 外部化 Observability 和发布/备份路径。

集群进入实现前，必须有容量或可用性证据。没有证据时，单节点更容易保持 locality 和可验证恢复。

### 4.4 Future Edge Host

用途：现场 Gateway 上的 OpenEMS-informed 控制面。

Edge Host 不属于 Backend/UI Cluster：

- Edge 有本地 Process Image、Cycle、Controller、Arbiter 和 Safety；
- Cloud 只下发受治理的 Command Intent；
- Edge 在断云时继续本地控制和缓存；
- Cloud 不直接访问 PLC、BMS、PCS 或现场设备；
- Edge 的生产部署、升级、签名 manifest、回滚和现场恢复另立部署文档。

## 5. 单节点到集群的演进阶段

| 阶段 | 形态 | 主要变化 | 当前状态 |
| --- | --- | --- | --- |
| Stage 0 | Single Node Compose | energy-api 嵌入 owner；单实例数据平面 | CURRENT |
| Stage 1 | Single Node Owner Split | owner 以独立 module 运行；仍是单机 | IMPLEMENTED / DRILL REQUIRED |
| Stage 2 | Component Scale-out | Backend/UI 多实例、worker 多 owner、PostgreSQL lease/dedup | DESIGNED / NOT CERTIFIED |
| Stage 3 | Data-plane Redundancy | PostgreSQL standby、MQTT failover、Redis HA、ClickHouse replica/rebuild | GATED |
| Stage 4 | Edge Fleet | Edge release、manifest、现场同步、灰度、回滚 | FUTURE |

任何阶段不能只凭 Compose 能启动就宣布完成，必须绑定该阶段的 exit evidence。

## 6. 部署模块的接口原则

部署架构中的每个 Module 都必须有清晰 Interface：

- 启动依赖；
- 健康与就绪语义；
- 数据 authority；
- 输入/输出协议；
- 配置和 Secret 来源；
- 资源预算；
- 失败时的降级和恢复方式；
- 升级/回滚约束。

不要让环境变量成为隐藏拓扑的唯一 Interface。Topology、Environment、Resource Tier、Observability Tier 应分别可审查。

## 7. 明确取舍

### ADOPT

- ThingsBoard 的单节点 → owner split → 多实例的拓扑演进；
- OpenEMS 的 Edge、Backend、UI 独立部署形态；
- MyEMS 的 acquisition、cleaning、normalization、aggregation 处理 module 分离；
- 当前项目已有的单机恢复、资源 tier、immutable image、migration preflight。

### ADAPT

- 当前一个 Phase 1 Compose 适配为 Backend/UI base + optional Integration + optional Intelligence + Observability；
- owner-split 适配为明确的 Stage 1 deployment shape；
- 集群适配为 PostgreSQL durable lease/outbox，而不是直接采用 Kafka/actor runtime；
- MyEMS 多数据库经验适配为逻辑数据集分离，不直接复制物理数据库数量。

### REJECT

- 当前阶段把 Kubernetes、Kafka、Service Mesh 或 HA 数据库变成生产前置；
- 把 Edge Host 当作 Backend/UI 集群副本；
- 把 simulator 作为生产 Edge 部署；
- 通过同一份 Compose 和大量隐式变量长期表达所有环境和拓扑；
- 以“所有容器都 running”替代业务恢复或集群故障演练。

## 8. 本项目需要修改的部署方向

1. 把 Backend/UI Base 从 MQTT/Edge Integration 中抽出来；
2. 将 Integration 明确为可选 deployment shape；
3. 将 owner-split 从隐式 overlay 提升为带准入条件的 Stage 1；
4. 将 Single Node 和 Cluster 的资源、网络、数据恢复和发布契约分别写清楚；
5. 将 Edge recovery 语义从当前 Backend/UI 最小部署中降为条件性未来能力；
6. 保留当前单服务器基线，但删除对集群能力已经完成的暗示。

