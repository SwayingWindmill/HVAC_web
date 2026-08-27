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

## 5. 部署演进：运行模式和主机拓扑必须分开

ThingsBoard 的一个重要经验是：Monolithic / Microservices 是运行角色拆分方式，Single Node / Cluster 是主机拓扑，两者不是同一条轴。本项目也采用这个划分：

~~~text
Runtime Mode
  embedded-owners | owner-split

Host Topology
  single-node | split-state | application-scale-out | stateful-ha

Optional Capability
  integration | intelligence

Observability Tier
  core | logs | full
~~~

`owner-split` 不再等同于“进入集群阶段”。它首先是同一台机器上的进程职责拆分，可用于独立重启、故障隔离和后续水平扩展准备。

### 5.1 Stage 0：Development / Acceptance

- 1 台开发机或测试机；
- Docker Compose；
- simulator / Integration 可选；
- 本地 PostgreSQL / ClickHouse / Redis；
- 不承载 Production 数据，不声明恢复或 HA 能力。

### 5.2 Stage 1：Single Node Recoverable（当前生产目标）

~~~text
1 Linux Server
├─ Nginx / UI
├─ Energy API + supporting workers
├─ Identity
├─ PostgreSQL
├─ ClickHouse
├─ Redis / Realtime
├─ optional Integration: iot-service + MQTT
└─ selected Observability
~~~

特征：

- 当前推荐生产形态；
- `single-lite` 8C/16G 为优先基线，`single-full` 16C/32G 只在完整 traces 或额外负载需要时使用；
- 默认不要求 Kubernetes、Kafka、Service Mesh；
- owner 可 embedded，也可在同机切为 owner-split；
- 数据平面仍有单点，依靠 off-server backup、immutable image、versioned config 和真实 restore drill 恢复；
- 只允许声明 `SINGLE_NODE_RECOVERABLE`，不声明 HA。

### 5.3 Stage 2：Split State / External Data

这是当前规划里需要补上的中间阶段。先把状态和应用故障域拆开，再考虑横向扩应用。

~~~text
Application Host
  Nginx / UI / APIs / workers
        |
        v
Stateful Host or Managed Services
  PostgreSQL / ClickHouse / Redis

optional Integration Host
  MQTT / iot-service
~~~

进入条件可以是：数据库 I/O 已成为主要瓶颈、业务需要独立维护数据库、服务器磁盘容量增长明显，或恢复目标要求应用主机损坏时数据仍然存在。

本阶段仍然可以只有一个 Application 实例和一个 PostgreSQL primary，因此它改善故障域和运维边界，但不自动形成 HA。

推荐迁移顺序：

1. PostgreSQL；
2. backup/object storage；
3. ClickHouse；
4. Redis；
5. MQTT / Integration（仅实际部署需要时）。

### 5.4 Stage 3：Application Scale-out

~~~text
Load Balancer
  ├─ Application Node A
  └─ Application Node B+
          |
          v
External Stateful Plane
~~~

只有以下需求出现时才进入：

- Energy API / realtime CPU 或连接数成为瓶颈；
- 单应用节点维护窗口不可接受；
- worker backlog 需要增加并发 owner；
- Integration 连接量需要独立扩展。

实现约束：

- stateless HTTP/BFF 才直接多实例；
- worker 必须依靠现有 PostgreSQL lease / claim / idempotency；
- Scheduler / Outbox 不能靠“只启动一个副本”的人工约定保证正确性；
- iot-service 按站点、连接或明确分区扩展，不做无状态随机复制；
- UI realtime 断线后仍回到 authoritative snapshot。

如果 Stateful Plane 仍为单 primary，本阶段最多称为 Application redundancy，不称为整体 HA。

### 5.5 Stage 4：Stateful HA / Production Cluster

达到明确可用性目标后才实现：

- PostgreSQL primary/standby 或托管 HA，并具备 fencing；
- Redis Sentinel / managed HA，且继续保持非 authority 边界；
- MQTT failover 和 session/reconnect 语义；
- ClickHouse replica，或有经过验证的重建/恢复方案；
- 外部 Object Storage / backup；
- ingress 和 Observability 不再依赖单一应用主机。

Kubernetes 在这里仍然只是编排实现选择，不是架构阶段本身。优先让 stateless application workloads 进入 Kubernetes/k3s；PostgreSQL、Redis、Object Storage 和通常的 ClickHouse 优先使用外部或专门管理的数据平面，避免为了“全上 K8s”增加运维复杂度。

### 5.6 独立演进线：Edge Fleet

Edge Host 不属于 Stage 0 -> Stage 4 的 Cloud 集群升级链。它可以在 Cloud 仍是 Stage 1 时就独立进入现场部署，也可以在 Cloud 已经 Stage 4 后继续扩展。

Edge 发布单独解决：signed manifest、OTA/灰度、断云运行、store-and-forward、readback、现场回滚和安全联锁。

任何阶段不能只凭 Compose、Helm 或 Pod 能启动就宣布完成；但验证只覆盖该阶段真正新增的故障语义，不建立与风险无关的大型门禁矩阵。

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

## 8. 本项目接下来的部署改造顺序

1. **先收敛当前 Single Node Compose**：`compose.yaml` 只表达 Backend/UI + core data；把 `iot-service + MQTT` 从默认图中抽成可选 Integration overlay/profile。
2. **保留 owner-split，但把它定义成 Runtime Mode**：继续使用现有 `owner-split.compose.yaml`，不再把它等价为多机部署。
3. **保持现有 resource tier**：`single-lite` / `single-full` 已经足够，不继续增加更多单机规格档。
4. **完成 Stage 2 external-data placement 基础**：PostgreSQL、ClickHouse、Redis 均支持独立 `local` / `external` placement；Stage 2 只改变故障域，不宣称 HA，也不增加新的编排系统。
5. **等实际容量数据出现后再实现 Application Scale-out**：首先验证 Energy API、workers、realtime、iot-service 中哪一类真的需要扩。
6. **只有明确 HA 目标后才实现 Stateful HA**：每个数据组件独立选型，不用一套“集群模板”覆盖所有状态服务。
7. **Kubernetes/k3s 不作为当前交付项**：到 Stage 3/4 时再根据节点数量、发布频率、滚动升级和调度需求选择；不提前维护一套与当前生产无关的 Helm/Kustomize 资产。
8. **Edge 保持独立部署线**：不把 Edge simulator、现场 Gateway 和 Backend/UI Compose 混为一套生产拓扑。

