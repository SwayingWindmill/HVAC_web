# 明确 `hvac-backend` 的非生产参考边界与依赖清理

Type: grilling
Status: resolved
Blocked by: 03, 04, 06, 07, 08, 09, 10, 11
Part of: ../map.md
Supersedes: 原 NestJS → Go Strangler 生产迁移假设
Decision: `docs/adr/0005-hvac-backend-non-production-reference.md`

## Question

在 `hvac-backend` 未成为生产系统、权威数据源或控制Owner的前提下，Go平台应如何使用该项目作为行为参考，同时防止它进入生产架构、发布、回滚与灾备链路？

## Comments

- `hvac-backend`仅为非生产行为参考和可选Fixture，不是Go服务边界、领域模型、数据库Schema、DTO、缓存、事件或ThingsBoard集成模式的架构来源。
- 生产公开入口只有`platform-gateway`。活跃Route Ownership Registry只允许Go Owner，不得选择`legacy-hvac-backend`，也不得配置Legacy fallback。
- S1 Registry从首次生产启用起由`platform-core-service`拥有。历史S1迁移阶段JSON可保留作回归Fixture，但不代表当前生产流程。
- S2 Presence/latest/Snapshot/live由`telemetry-runtime-service`唯一拥有。生产请求不得回退NestJS、旧Redis或旧Socket.IO；历史查询必须由Go Query边界实现或明确声明不可用。
- `legacy-migration-service`、Telemetry Shadow Comparator、旧退役报告和兼容Fixture均分类为历史/非生产资产。保留或删除由独立清理任务决定，不得成为Release Gate。
- 生产部署、备份、Restore和区域灾备不得要求`LEGACY_URL`、NestJS数据库、Redis键、ServiceAccount、证书、Secret或ThingsBoard Token。
- Go服务不得读取NestJS表或Redis作为权威事实，也不得通过NestJS发起设备控制、调度或AI工具调用。
- 允许使用语言无关Golden Fixture比较旧行为与新契约，但差异结果只用于测试，不得动态改变生产路由、修复权威状态或创建副作用。
- Command和Scheduler仍坚持唯一副作用Owner、Generation、Lease和Fence。回滚目标是上一Go版本或关闭能力，不是NestJS。
- 不再要求Legacy快照回填、CDC尾差、Legacy流量归零观察或Legacy数据库写权切换。若未来出现新的真实生产旧系统，必须通过新的ADR重新定义迁移源和切换规则。
- 依赖清理包括：活跃路由去Legacy Owner/fallback；生产环境不注入Legacy配置；前端不依赖旧DTO/Header/Socket.IO；历史查询不隐式调用NestJS；无用DNS、Ingress、凭据和权限按审计流程撤销。
- 删除历史Fixture或`hvac-backend`目录本身不是架构完成标准。完成标准是生产运行、恢复和开发新能力均不依赖它。

## Answer

Go平台采用独立建设路线，不执行NestJS到Go的生产Strangler迁移。`hvac-backend`只保留为非生产行为参考；生产Route/Data Owner、身份、遥测、命令、调度、AI、回滚与灾备全部由Go平台定义并承担。

S7改为`Production Cohort Rollout & Operational Hardening`：按P0–P6逐步扩大Go能力的生产Cohort，并验证容量、故障注入、Restore、Kill Switch、Owner/Command Fence、未来请求回滚和正式运维接管。生产回滚只回到上一Go阶段或关闭能力。
