# 确定粗粒度服务边界与故障域

Type: grilling
Status: resolved
Blocked by: 01, 02, 03
Part of: ../map.md

## Question

根据目标运行包络、领域模型和现有契约，Go 平台应拆成哪些独立部署单元？需要明确控制平面、遥测数据平面、命令控制平面、计算平面和 AI 平面的边界，哪些能力必须留在同一事务和发布单元，哪些能力必须独立扩缩容、隔离故障、独立发布，并为每个服务指定数据所有权和允许的同步依赖。

## Comments

- 总体采用五个平面、约 14 个粗粒度部署单元：platform-gateway、identity-access-service、platform-core-service、thingsboard-connector、telemetry-ingest、telemetry-processor、telemetry-query-service、realtime-gateway、command-service、command-dispatcher、automation-service、analytics-worker、ai-platform-service、agent-worker。
- 业务核心保持模块化服务，不按 Organization、Portfolio、Project、Site、Building、Equipment 等低吞吐领域逐个拆分；遥测摄取/处理/查询、实时连接、命令状态机/执行、批量计算、AI Worker 与 ThingsBoard 集成因吞吐、SLO、资源和故障模式不同而独立部署。
- 所有 ThingsBoard 南向访问必须经过 thingsboard-connector；Agent Worker 只能通过受审计 Tool API 访问平台能力；automation-service 只能通过 command-service 创建命令，不能绕过命令治理直接调用 ThingsBoard。
- Go 采用单仓库多二进制，各服务独立镜像、配置、健康检查、扩缩容和发布版本；Python EnergyAgent 与 Node Copilot Runtime 保留独立运行时，不强行改写为 Go。
- 首版允许多个服务共享同一 HA PostgreSQL 集群，但必须使用独立数据库或 Schema、独立账户和严格数据所有权；禁止跨服务外键、SQL JOIN、直接写表和分布式事务。跨服务一致性采用 API、Outbox/Inbox、事件和 Saga。
- 必须同事务提交的内容包括业务状态变更、版本/幂等/Transition 记录及对应 Outbox；telemetry-processor 等高频消费者通过事件驱动本地投影或配置快照获取 Registry 数据，不逐条同步查询 Core。
- Gateway 后默认只同步调用一个领域服务，禁止事务内远程调用、同步调用环和大规模 fan-out；命令执行、Registry 传播、遥测处理、调度、报表与 AI 等状态传播均通过事件或任务队列完成。
- telemetry-ingest、telemetry-processor、telemetry-query、realtime-gateway、command-service、command-dispatcher、analytics-worker 等服务维护带 source_version、effective_at、last_event_id 和 staleness 的本地投影，不能依赖无版本缓存。
- IAM/Core 短时不可用时，普通读取和已批准执行可按有效快照有限降级；新高风险命令、审批、权限修改和未知设备写入必须 fail closed。Connector、查询、分析或 Agent 故障不得级联阻塞遥测摄取和命令状态机。
- 生产环境所有服务均为独立 Deployment，但按命令控制、遥测写入、在线读取、批处理/AI 划分资源隔离池；命令关键路径具有保留资源和更高调度优先级，批计算与 AI 不得挤占控制面资源。
- thingsboard-connector 使用同一代码库但以 control/data 两种角色部署：control 负责 RPC、命令 ACK 与高优先级属性读取，data 负责 Registry、Telemetry、Alarm 与普通 Attribute，同角色使用独立队列、消费者组、连接池和 Worker Pool。开发环境只允许主机级共置，不允许合并服务边界、数据库账户或生产二进制。
- 审计采用独立 audit-ledger-service，领域服务通过本地事务 Outbox 追加高优先级审计事件；通知规则归 automation-service，但 notification-worker 独立部署和排队。文件对象存储为共享基础设施，元数据按 Core、Analytics、AI、Audit 领域分别拥有；首版不建立通用文件服务或跨域搜索服务。

## Answer

平台采用五个平面和粗粒度微服务，不把现有 NestJS 模块逐个翻译为微服务。首版确定 16 个逻辑服务/Worker：platform-gateway、identity-access-service、platform-core-service、thingsboard-connector、telemetry-ingest、telemetry-processor、telemetry-query-service、realtime-gateway、command-service、command-dispatcher、automation-service、analytics-worker、ai-platform-service、agent-worker、audit-ledger-service、notification-worker；Node Copilot Runtime 继续作为 AI 协议桥独立运行。thingsboard-connector 虽为一个代码和领域边界，但生产按 control/data 两个角色独立部署。

业务核心 Organization、Portfolio、Project、Site、空间层级、HVAC System、Equipment、Device、ExternalBinding 和 TelemetryPoint 归 platform-core-service，在同一模块化服务和事务边界内演进。IAM、遥测摄取/处理/查询、实时连接、命令状态机/执行、自动化、批量分析、AI 状态、审计及通知因 SLO、资源类型、扩缩容和故障模式不同而独立部署。

Go 采用单仓库多二进制。每个服务拥有独立镜像、配置、健康检查、数据库账户、连接池、扩缩容策略和发布版本。首版可以共享一个多可用区 PostgreSQL 集群，但必须使用独立数据库或 Schema，并禁止跨服务外键、SQL JOIN、直接写表和分布式事务。业务状态、版本/幂等/Transition 和 Outbox 必须在数据所有者本地事务中提交；跨服务一致性使用版本化 API、Outbox/Inbox、事件和 Saga。

Gateway 后默认只允许一个领域同步调用；领域服务禁止事务内远程调用、调用环和大规模同步 fan-out。命令执行、Registry 与映射传播、遥测处理、调度、报表和 AI 均异步推进。高频及关键服务通过带 source_version、effective_at、last_event_id 和 staleness 的本地投影工作，不逐请求依赖 Core 或 IAM。

生产环境按故障域划分资源池：命令控制池、遥测写入池、在线读取池、批处理/AI 池。命令关键路径具有保留资源和高调度优先级；分析和 AI 不得挤占命令或摄取资源。开发环境可以主机级共置，但不能合并生产边界、数据库所有权或形成仅在单体进程内可用的调用路径。

服务等级确定为：command-service、command-dispatcher、thingsboard-connector-control 和 telemetry-ingest/持久化事件骨干为 99.99% 关键级；Gateway、IAM、Core、Telemetry Query、Realtime、Connector Data 和 Automation 为 99.95% 在线级；Telemetry Processor、Audit Ledger 和 Notification 可短时积压但不得丢事件；Analytics、AI Platform、Agent Worker 和 Copilot Runtime 为 99.9% 异步计算级，不得影响控制、摄取和在线查询。

唯一数据所有者如下：IAM 拥有 Principal、Membership、RoleBinding、SiteBinding 和 Policy；Core 拥有平台业务主数据与外部绑定；Connector 拥有 ThingsBoard Token、同步游标、南向运行状态和隔离记录；遥测事件骨干拥有平台已确认接收的原始事件边界，遥测数据平面拥有标准化遥测、聚合和最新状态；Command Service 拥有命令业务状态，Dispatcher 仅拥有执行租约、Attempt 和 Inbox；Automation 拥有 Schedule/Rule/Strategy；AI Platform 拥有 Investigation/Finding/Recommendation/Review；Audit Ledger 拥有不可变合规审计；分析域拥有任务、计算版本和报表元数据；Agent Worker 仅拥有可清理临时 Checkpoint。

禁止依赖包括：Gateway 或 Agent 直接访问业务数据库，Agent/Automation 直接访问 ThingsBoard，Command Service 同步依赖 Telemetry Query 或 Agent，Dispatcher 实时查询 IAM，以及任何服务访问其他服务数据库。所有南向访问必须经过 Connector；Automation 只能通过 Command Service 创建命令；Agent 只能通过受审计 Tool API 使用平台数据与控制能力。

HTTP、Tool API 和事件 Schema 必须版本化，消费者至少兼容当前和前一版本。数据库 migration 只能由数据所有者执行，发布采用扩展契约优先、消费者升级、最后移除旧字段的顺序；禁止共享 Go struct 包迫使多服务同步上线。各服务必须能独立构建、部署、扩容和回滚。
