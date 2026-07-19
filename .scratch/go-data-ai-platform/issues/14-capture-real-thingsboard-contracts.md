# 确定 ThingsBoard 参考契约与生产验证门禁

Type: task
Status: resolved
Blocked by: 03
Part of: ../map.md

## Question

从目标 ThingsBoard 实例和代表性设备 Profile 获取并固化真实数据与控制契约：IntegrationInstance、Tenant、Customer、Asset、Device 及 Relation 关系；遥测 key、类型、单位、频率、质量和样本；Attribute scope；Alarm 类型、级别与生命周期；RPC method、payload、response、timeout 和 ACK；设备能力与版本差异。输出经过脱敏、可用于 TelemetryPoint Schema、Command Schema 和契约测试的证据资产，不允许以 `TBD` 或猜测替代。

## Comments

- 目标实例已确定为 `https://tb.oidcs.com`，Tenant 用户名已提供；密码不得写入仓库、票据、命令历史或证据资产。
- 仓库现有 `integration-thingsboard` 只能证明通用 REST/WebSocket/RPC/Attribute 适配假设：RPC 接受任意 `method + params`，现实现会把 HTTP 超时映射为 TIMEOUT，且代码注释明确承认无法可靠区分设备离线、未执行、ACK 丢失和执行后未响应。这些是迁移风险证据，不是目标设备 Command Schema。
- 已新增只读脱敏采集器 `research/capture-thingsboard-contracts.mjs` 与运行手册 `research/thingsboard-contract-capture-runbook.md`。采集器覆盖实体/Profile/Relation、代表设备遥测 Key/类型/24h 频率、三类 Attribute Scope、Alarm、Dashboard/Rule Chain/Profile 中的 RPC 与单位线索；不会调用 RPC、Attribute 写入、实体写入、删除或设备凭据接口。
- 采集器已通过宿主机 Node `--check` 语法验证。当前会话的工具安全层禁止在命令参数中注入 ThingsBoard 凭据，因此尚未生成真实实例证据；需在本地 PowerShell 使用隐藏密码输入运行采集器。
- 即使只读采集发现 RPC method，也不能据此证明 ACK 和副作用语义。后续若执行真实验证，只能选择厂商明确声明安全且无控制副作用的方法，并经过单独审批。
- 用户决定架构阶段不读取现有绑定设备，改为采用常见 HVAC/ThingsBoard 参考契约继续设计；真实实例与设备契约验证从票据 06/07 的架构前置条件下移为每个 Device Profile 的生产接入硬门禁。

## Answer

架构阶段采用 `research/reference-hvac-thingsboard-contract-baseline.md` 作为参考契约。平台内部只依赖 Canonical TelemetryPoint、Canonical Alarm 和 Canonical Command；ThingsBoard 实际 Key、Attribute、Alarm Type、RPC Method、Payload、单位、采样周期和 ACK 格式全部由版本化 Device Profile Mapping 与 Adapter 转换，不允许业务服务、前端或 AI Tool 硬编码厂商契约。

参考基线覆盖常见环境传感器、AHU、FCU、VAV、冷水机组、冷却塔、水泵、锅炉、换热器和计量设备，定义常见温湿度、空气、水系统、运行状态、阀门/风阀、能耗点位，三类 Attribute Scope，统一 Alarm 严重度与生命周期，以及设定值、模式、速度、位置、启停、复位、时间同步和状态读取等 Canonical Command。

所有映射必须标记为 REFERENCE、INFERRED、VERIFIED、DEPRECATED 或 REJECTED。开发、演示和影子处理可使用 REFERENCE/INFERRED；生产控制只允许 VERIFIED 的 Command Mapping。HTTP 2xx 不默认等价于设备执行成功，超时、断线和无法判断 ACK 的情况统一进入 OUTCOME_UNKNOWN。

真实采集不再阻塞票据 06 和 07，但仍是生产设备接入与控制启用的强制门禁：上线前必须验证外部 Key、类型、单位、缩放、频率、时间质量、Attribute 权威、Alarm 生命周期、RPC 请求/响应、超时、离线、幂等和真实 ACK。未通过门禁的 Profile 只能进入 Quarantine、影子标准化或只读模式，不得执行人工、自动化或 AI 控制。

已生成的只读采集器和运行手册作为后续可选验收工具保留，不要求当前运行。
