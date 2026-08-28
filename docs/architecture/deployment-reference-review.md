# 四参考项目部署源码审查

状态：REVIEWED / SOURCE-ALIGNED  
审查目标：确定单节点、集群、Backend/UI、Integration、Edge 的部署取舍  
本项目现有部署不是裁决依据，固定源码证据优先。

## 1. 审查基线

| 项目 | 固定基线 | 主要部署证据 |
| --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 / c2a52e46c44e308ddee430e7266b8e10eddde9c4 | docs/architecture/thingsboard-source-review.md；官方 Docker、Deployment Scenarios 与 Microservices 文档 |
| GitLab Self-Managed | 2026-08 官方 Reference Architectures | Standalone、Linux package、Cloud Native Hybrid、Cloud Native reference architectures |
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

## 3. GitLab Self-Managed

### 官方部署事实

GitLab 的 Reference Architectures 把部署复杂度和实际负载/可用性需求绑定，而不是把 HA 当成所有生产环境的默认前置：

- 小规模环境明确允许 standalone / non-HA，并依靠自动备份满足恢复目标；
- 更大规模或明确 HA 需求才进入多节点 reference architecture；
- Cloud Native Hybrid 只把适合横向扩展的 stateless workloads 放入 Kubernetes；
- PostgreSQL、Redis、Object Storage 等 stateful services 可以保持在 VM 或托管服务中；
- sizing 依据实际 RPS / workload 监控调整，不以“已经有集群 YAML”作为扩容依据。

官方参考：[Reference architectures](https://docs.gitlab.com/administration/reference_architectures/)、[Cloud Native reference architecture](https://docs.gitlab.com/administration/reference_architectures/cloud_native/)

### ADOPT

- 小规模先 standalone + backup/recovery；
- 将 application scale-out 和 stateful HA 分阶段；
- Kubernetes 优先承载 stateless workloads，stateful plane 独立管理；
- 扩容由实际资源/吞吐证据驱动。

### ADAPT

- 本项目在 Single Node 与 Application Scale-out 之间加入 `split-state`；
- PostgreSQL 优先外置，其次 ClickHouse、Redis，再按需要外置 MQTT/Integration；
- 当前单机资源档继续使用 `single-lite` / `single-full`，不建立大量尺寸模板。

### REJECT

- 不复制 GitLab 的节点数量和 GitLab 专用组件；
- 不因为未来可能需要 HA 就提前维护 Kubernetes 集群；
- 不把所有 stateful component 强行部署到 Kubernetes。

## 4. OpenEMS

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

## 5. MyEMS

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

## 6. Backend/UI 发布单元补充裁决

这次本机部署暴露了一个独立于拓扑的问题：旧 Web bundle 仍要求 `capabilitySetVersion=7`，而当前 Gateway/contract 已是 `11`。三套能源/IoT 参考源码都支持 UI 独立部署，但没有把“任意旧 UI + 任意新 Backend”当作正常发布方式：ThingsBoard 根 Maven reactor 直接包含 `ui-ngx`，认证态由当前 JWT 和用户加载流程共同建立；OpenEMS release workflow 从同一 checkout 构建 Backend JAR 与 UI artifact，并在一个 Release 中发布；MyEMS Web README 要求先生成 production build，再将该静态产物部署到代理 `/api` 的 Web Server。

官方源码参考：[ThingsBoard root reactor](https://github.com/thingsboard/thingsboard/blob/master/pom.xml)、[ThingsBoard AuthService](https://github.com/thingsboard/thingsboard/blob/master/ui-ngx/src/app/core/auth/auth.service.ts)、[OpenEMS release workflow](https://github.com/OpenEMS/openems/blob/develop/.github/workflows/release.yml)、[OpenEMS UI modes](https://github.com/OpenEMS/openems/blob/develop/ui/README.md)、[MyEMS Web deployment](https://github.com/MyEMS/myems/blob/master/myems-web/README.md)

因此本项目裁决为：

- 保持 Web 对 Principal/Session contract 的严格校验，不接受旧 capability contract 的兼容 fallback；
- 本地 WSL 源码部署使用 `--source-deploy`，从同一 checkout 重建 Web 与第一方 runtime；
- Web build identity 使用当前 Git revision；
- `index.html` / SPA fallback 使用 `no-cache`，hash asset 使用 immutable 长缓存，避免入口缓存把旧 bundle 留在新 Backend 前面；
- Staging/Production 仍使用已审批的 immutable image digest，不在目标主机临时源码构建。

## 7. Registry 授权 revision authority 源码复核

这次真实登录验收还暴露了 `GET /api/v1/sites` 的独立 `REGISTRY_UNAVAILABLE`：Gateway 的父 delegation 使用本地静态 `IDENTITY_POLICY_REVISION=registry-read:1`，而 PostgreSQL IAM 根据当前 Registry policy 与 Tenant authorization revision 生成 `registry-read:1/iam:1`。旧 Gateway 把这两个不同层次的 revision 强制要求相等，因此在 IAM 已允许并签发 Registry grant 后、请求进入 Core 前错误返回 503。

本次按源码而不是既有本地实现做复核，固定以下上游版本与文件：

- ThingsBoard `c8e780baa70b3b6a01754d5c578c36f3814a780d`：[`ui-ngx/src/app/core/auth/auth.service.ts`](https://github.com/thingsboard/thingsboard/blob/c8e780baa70b3b6a01754d5c578c36f3814a780d/ui-ngx/src/app/core/auth/auth.service.ts)；认证态由当前 token 校验和服务端用户加载共同建立，不把客户端静态配置当成当前授权事实。
- OpenEMS `a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6`：[`RestHandler.java`](https://github.com/OpenEMS/openems/blob/a7efc1c1eacd05f7a0f8eb43f962564ccf66ead6/io.openems.edge.controller.api.rest/src/io/openems/edge/controller/api/rest/RestHandler.java)；请求先通过服务端 `UserService` 得到当前 User，再把该身份交给后续资源处理。
- MyEMS `51972b1bb807e47c86feca443b53a560d985adcc`：[`database/README.md`](https://github.com/MyEMS/myems/blob/51972b1bb807e47c86feca443b53a560d985adcc/database/README.md)；用户、Privilege、Session 与 API key 都由服务端用户数据库持有，当前权限事实不由 Web 端复制维护。

本项目对这一问题的裁决为：

- **ADOPT**：当前授权事实由真正拥有该授权域的服务端 owner 决定；Registry 的当前 policy revision 以 IAM decision / IAM-signed Registry grant 为准。
- **ADAPT**：Gateway 继续严格校验 Tenant、Subject、Principal、Action、allow reason、Site scope 以及 grant 的结构边界；Core 继续验 IAM 签名，并通过 IAM grant-status 检查当前 policy revision 和 revocation。
- **REJECT**：不再要求 IAM Registry decision 的动态 revision 与 Gateway 父 delegation 的静态 revision 字符串相等，也不通过增加 fallback 或兼容分支掩盖 revision ownership 混淆。

## 8. 对 HVAC_web 的最终裁决

| 议题 | 裁决 |
| --- | --- |
| 当前生产 | Stage 1 Single Node Recoverable + Docker Compose |
| 当前 Backend/UI | 独立于 Edge Host，Integration 可选 |
| 当前数据 | PostgreSQL/ClickHouse/Redis 单节点；off-server backup；authority/rebuildability 分开 |
| 下一部署阶段 | Stage 2 Split State：PostgreSQL / ClickHouse / Redis placement 已可独立外置；下一步按真实容量证据进入 Stage 3 Application Scale-out |
| Runtime Mode | embedded-owners / owner-split 与主机拓扑解耦 |
| 未来应用扩容 | Stage 3 先扩 stateless Backend/UI 和 leased workers；Integration 按连接/站点分区 |
| 未来整体 HA | Stage 4 PostgreSQL、MQTT、ClickHouse、Redis 分别以可用性目标和故障证据准入 |
| 未来 Edge | 单独 Host、单独 Release/Manifest/Recovery 文档 |
| Kubernetes/k3s | 只是 Stage 3/4 的可选编排，不是阶段本身，也不是当前前置 |
| Kafka | 只有现有 PostgreSQL durable backbone 出现实测瓶颈时再评估 |

## 9. 尚未确认的内容

- ThingsBoard CE 固定 tag 的完整生产集群容量数据；
- OpenEMS 官方生产 HA/升级/备份 runbook；
- MyEMS 社区版当前完整容器编排和生产恢复证据；
- HVAC_web 集群所需的实际容量、连接数、事件量和查询量。

这些内容在没有源码、官方文档或实测证据前，不进入当前部署结论。

