# 盘点现有契约与迁移约束

Type: research
Status: resolved
Blocked by: none
Part of: ../map.md

## Question

当前 NestJS 后端、HVAC Web、ThingsBoard 和 EnergyAgent 之间已经存在什么可观察契约与数据约束？需要盘点公开 API、WebSocket、认证与 Site Context、数据库表与 migrations、ThingsBoard 字段映射、命令和调度行为、前端依赖、AI 状态契约和现有测试，并区分必须兼容、可以演进和应直接废弃的部分。

## Assets

- [现有契约与 Go 迁移约束盘点](../research/current-contract-and-migration-inventory.md)

## Answer

当前系统已经形成可用的**行为基线**，但没有形成统一、稳定、可直接逐字段复制的协议基线。Go 平台必须保留 Site 范围校验与审计、设备列表和遥测查询、REST 初始快照加实时增量、命令幂等及状态跟踪、调度运行历史，以及 EnergyAgent 的 Investigation、AnalysisRun、Evidence、Finding、Recommendation 和 Review 语义。

`customerId/tbCustomerId/buildingId`、不一致响应包、两套时序查询、Socket.IO 当前 payload、旧角色、旧表结构和 Agent 状态字段可以通过统一 Gateway 与 Legacy Anti-Corruption Layer 演进；演示 ID、读请求内全量同步 ThingsBoard、浏览器跨设备聚合、未保护摄取端点、WebSocket 越权缺口、进程内跨服务事件总线、`default` ThingsBoard token namespace、级联删除命令历史和 Agent Mock 数据应直接清退。

迁移期间必须保持统一 `/api/v1` 入口、每张数据库表单一写入 owner、命令链路禁止双写、只读链路先做影子比对，并先提取语言无关契约测试。真实 ThingsBoard 遥测 key、单位、Attribute、Alarm 和 RPC 仍大量为 `TBD`，因此新增 [获取真实 ThingsBoard 数据与控制契约](14-capture-real-thingsboard-contracts.md)，并将其设为遥测与命令架构决策的前置条件。
