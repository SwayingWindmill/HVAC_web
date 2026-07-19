# 现有契约与 Go 迁移约束盘点

日期：2026-07-19

## 结论

当前仓库已经形成了一组可以作为 Go 迁移行为基线的能力，但尚未形成一份真正统一、稳定的公开契约。

应当保留的是用户可见业务能力、Site 隔离、命令幂等、REST 初始快照加实时增量、调度执行记录以及 EnergyAgent 的 Investigation 状态语义。需要演进的是 `customerId`/ThingsBoard Customer 中心模型、响应包、认证提供方、遥测查询、Socket.IO 协议、数据表和 AI 数据来源。应直接废弃的是演示 ID、前端容错补丁、请求内 ThingsBoard 全量同步、未保护的摄取端点、浏览器跨设备聚合和通用双向数据覆盖。

Go 平台不能通过逐文件翻译 NestJS 完成迁移。必须先建立兼容网关和反腐层，把旧 `tbCustomerId/customerId/buildingId` 映射到新 `Organization/Site/Equipment/Device/TelemetryPoint` 模型。

## 1. 当前运行拓扑

| 组件 | 当前契约 | 证据 | 迁移判断 |
|---|---|---|---|
| HVAC Web | Vite 开发端口 5173 | `apps/hvac-web/vite.config.ts:35-46` | 保留开发体验，不是业务契约 |
| NestJS 后端 | 全局前缀 `/api/v1`；代码默认端口 3001 | `hvac-backend/src/main.ts:21-49` | 保留 `/api/v1` 兼容入口；端口不冻结 |
| Vite 业务代理 | `/api/v1` 和 `/ws` 指向 `localhost:3000` | `apps/hvac-web/vite.config.ts:25-46` | 当前与 NestJS 默认端口冲突，必须统一 |
| Copilot Runtime | `/api/v1/copilotkit`，默认端口 3001 | `runtimes/copilot-runtime/server.mjs:6-38` | 路径可继续由统一 Gateway 暴露；端口不冻结 |
| EnergyAgent | 默认 `http://127.0.0.1:8123`，graph ID `sample_agent` | `runtimes/copilot-runtime/server.mjs:8-27` | 部署地址和 graph ID 属配置，不是平台业务契约 |

当前 NestJS 和 Copilot Runtime 都默认使用 3001，而前端假定业务后端在 3000。Go 平台上线前必须先确定统一本地及生产路由拓扑。

## 2. HTTP 横切契约

### 已存在行为

- 所有 NestJS 路由带 `/api/v1` 前缀。
- 请求启用白名单转换，未知字段会被拒绝。
- JWT、Role、Scope Guard 是全局 Guard。
- SiteContext 是全局 Interceptor，从 `X-Site-Id`、Token Site、Active Site 和 legacy Customer 中解析当前 Site。
- 所有请求经过 Trace ID middleware。
- `/health` 和 `/ready` 是公开端点。

证据：

- `hvac-backend/src/main.ts:18-45`
- `hvac-backend/src/app.module.ts:31-82`
- `hvac-backend/src/site/interceptors/site-context.interceptor.ts:24-100`
- `hvac-backend/src/app.controller.ts:12-33`

### 响应包不一致

`ResponseDto` 宣称所有接口返回：

```json
{
  "code": 200,
  "message": "Success",
  "data": {},
  "traceId": "...",
  "timestamp": 0
}
```

但多数 Controller 手写 `{code: 0, message: "success", data}`；Telemetry 又使用 `code: 200`；认证 Session 还把 `accessToken` 和 `user` 重复放在顶层。前端因此实现了容错 `unwrap`，接受 `code=0`、`code=200` 或裸数据。

证据：

- `hvac-backend/src/common/dto/response.dto.ts:4-68`
- `hvac-backend/src/iam/iam.controller.ts:87-134`
- `hvac-backend/src/telemetry/telemetry.controller.ts:47-53`
- `apps/hvac-web/src/api/http.ts:15-24`

**迁移约束：** Go 新契约必须统一 HTTP 状态码、业务错误码、`traceId` 和错误结构。旧响应包只能由兼容层暂时输出，不能进入新服务内部接口。

## 3. 现有公开 REST 能力

### 健康与认证

- `GET /api/v1/health`
- `GET /api/v1/ready`
- `POST /api/v1/auth/login/wechat`
- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/forgot-password`
- `POST /api/v1/auth/reset-password`
- `GET/PATCH /api/v1/auth/me`
- `POST/GET /api/v1/auth/sites`
- `GET/PUT /api/v1/auth/me/active-site`
- `DELETE /api/v1/auth/sites/:tbCustomerId`
- 管理员 Site Identity Mapping CRUD 与 Logto Organization provision

证据：

- `hvac-backend/src/iam/iam.controller.ts:24-125`
- `hvac-backend/src/iam/site-identity-mapping.controller.ts:12-47`

**判断：** 登录后的主体、Site 选择和权限语义需要保留；本地用户名密码、微信密码逻辑、直接输入 `tbCustomerId` 绑定 Site、`active_tb_customer_id` 均应迁移到外部 IAM 加平台 SiteBinding 模型。

### Registry 与设备状态

- `GET /api/v1/assets/tree`
- `GET /api/v1/devices`
- `GET /api/v1/devices/:id`
- `GET /api/v1/devices/status/batch`
- `GET /api/v1/devices/:id/state`
- `PUT /api/v1/devices/:id/desired`

`GET /devices` 支持 `sync=true`，并在本地无设备时于读请求内自动从 ThingsBoard 全量同步。

证据：`hvac-backend/src/registry/controllers/registry.controller.ts:22-202`。

**判断：** 设备列表、详情、批量状态和 desired/reported 业务能力应保留。请求内同步 ThingsBoard 应废弃，改为异步 Registry 同步和显式同步状态。

### Telemetry

主接口：

- `GET /api/v1/telemetry/devices/:id/latest?keys=`
- `GET /api/v1/telemetry/devices/:id/timeseries?keys=&startTs=&endTs=&limit=&agg=`
- `POST /api/v1/telemetry/latest/batch`

兼容接口：

- `GET /api/v1/devices/:id/telemetry?keys=&range=1h|6h|24h`

主接口默认一小时、100 点、ASC；兼容接口把 `range` 转成时间戳并带默认原始 key。

证据：

- `hvac-backend/src/telemetry/telemetry.controller.ts:24-138`
- `hvac-backend/src/telemetry/device-telemetry.controller.ts:8-54`

**判断：** latest、batch、timeseries 能力必须保留。只保留一套平台契约，使用显式 `startAt/endAt/granularity/aggregation/limit`；`range` 只可存在于前端 adapter，不作为后端核心协议。

### Command

- `POST /api/v1/devices/:id/commands`
- `GET /api/v1/commands/:id`

当前命令保存 `requestId`，数据库唯一键为 `(customer_id, device_id, request_id)`；状态机只有 `PENDING → SENT → ACKED_SUCCESS/ACKED_FAILED/TIMEOUT`。

证据：

- `hvac-backend/src/command/command.controller.ts:21-54`
- `hvac-backend/src/command/entities/command.entity.ts:19-84`
- `hvac-backend/src/command/enums/command-status.enum.ts:5-50`

**判断：** 创建命令、幂等请求、状态查询和设备 ACK 语义必须保留。现有状态名和字段可演进为包含校验、审批、排队、执行、取消、冲突、重试和超时的完整状态机。

### Scheduler

现有能力包括创建、查询、更新、删除、启停、绑定目标、查看运行记录和手动触发。

证据：`hvac-backend/src/scheduler/controllers/schedule.controller.ts:41-275`。

**判断：** 用户可见调度能力、时区、运行记录和目标绑定需要保留；进程内 Cron 注册不是可迁移契约，必须替换为高可用调度执行模型。

### 内部与南向入口

- `POST /api/v1/internal/commands`
- `POST /api/v1/ingest/tb/telemetry`
- `POST /api/v1/ingest/tb/attributes`

TB 摄取端点当前使用 `@Public()`，注释要求依赖网络隔离或预共享密钥，但代码未实现；attributes 仅返回成功，实际未处理。内部命令以 `customerId` 和 `createdBy="system-internal"` 调用普通命令服务。

证据：

- `hvac-backend/src/telemetry/tb-ingest.controller.ts:7-52`
- `hvac-backend/src/internal/internal.controller.ts:34-74`

**判断：** 这些 URL 和 DTO 不应冻结。Go 平台应建立带服务身份、签名、防重放、版本和幂等键的正式内部及 ThingsBoard 接入契约。

## 4. WebSocket 契约

当前使用 Socket.IO：

- path：`/ws/telemetry`
- 客户端事件：`subscribe`、`unsubscribe`
- 服务端事件：`subscribe_ack`、`unsubscribe_ack`、`telemetry`
- telemetry payload：`{deviceId, key, value, ts}`
- JWT 通过握手 token 或 Authorization Header 获取

服务端订阅代码明确留有 TODO，尚未验证用户是否有权读取指定设备。前端会逐 key 发送订阅，在重连后重订阅，并用 `requestAnimationFrame` 合并刷新。

证据：

- `hvac-backend/src/telemetry/telemetry.gateway.ts:18-115`
- `apps/hvac-web/src/api/telemetry.ts:16-59`
- `apps/hvac-web/src/api/telemetry.ts:101-224`

**判断：** “初始 REST 快照 + 实时增量 + 断线重连重订阅”是必须保留的用户体验契约。Socket.IO、逐 key 订阅和当前 payload 可通过兼容适配器演进；新协议必须加入 Organization/Site 授权、订阅上限、sequence/cursor、快照版本和断线恢复语义。

## 5. 前端真实依赖与临时补丁

### 已真实依赖

- REST base `/api/v1`
- WebSocket path `/ws/telemetry`
- Bearer Token
- `X-Site-Id`
- latest map：`Record<key, {ts,value}>`
- timeseries map：`Record<key, Array<{ts,value}>>`
- batch：`[{deviceId, latest}]`

证据：

- `apps/hvac-web/src/api/config.ts:1-8`
- `apps/hvac-web/src/api/http.ts:1-13`
- `apps/hvac-web/src/api/types.ts:1-22`
- `apps/hvac-web/src/api/rest.ts:9-18`

### 临时实现

- 默认 `VITE_API_MODE` 为 mock。
- Token 直接从 localStorage `hvac_token` 读取，没有完整登录、刷新和失效流程。
- `buildingId` 被当作 Site ID，默认值是演示 ID `b1`。
- timeseries 前端发送 `range`，但后端主接口读取 `startTs/endTs`，因此真实模式会落入后端默认一小时。
- 建筑总能耗在浏览器中逐设备查询并按数组索引相加。
- 前端角色 `demo/ops/rd` 只是页面可见性，不是后端授权真相。

证据：

- `apps/hvac-web/src/api/config.ts:1-5`
- `apps/hvac-web/src/api/auth.ts:4-14`
- `apps/hvac-web/src/store/ui.ts:35-60`
- `apps/hvac-web/src/api/rest.ts:12-15`
- `apps/hvac-web/src/api/rest.ts:50-84`

**判断：** 页面能力、实时刷新和 TypeScript 领域形状可作为迁移验收基础；演示 ID、容错响应、浏览器聚合和前端角色授权必须废弃。

## 6. 数据库与身份迁移约束

初始 migration 已建立：

- schedules、schedule_bindings、schedule_runs
- rules、rule_bindings
- assets、devices
- diagnostic_rules、fault_events
- strategies、strategy_bindings
- users、user_site_bindings
- audit_logs、commands

核心问题：

- 多数站点范围使用 `customer_id`，类型还在 `uuid` 与 `varchar(36)` 之间不一致。
- assets/devices 是 ThingsBoard Asset/Device 的本地镜像，包含 `tb_asset_id`、`tb_device_id`。
- users 同时包含本地 password、微信 openid、ThingsBoard user/customer、active TB Customer。
- Logto migration 又加入 `logto_subject`、`auth_provider` 和 Logto Organization 到 TB Customer 的一对一映射。
- 命令外键对 Device 使用 `ON DELETE CASCADE`，与新架构要求保留历史审计冲突。

证据：

- `hvac-backend/src/migrations/1780490000000-InitSchema.ts:12-303`
- `hvac-backend/src/migrations/1780490000000-InitSchema.ts:306-331`
- `hvac-backend/src/migrations/1780500000000-AddLogtoIdentityMapping.ts:6-31`

**迁移约束：**

1. 旧 `customer_id/tb_customer_id` 必须通过迁移表映射到新 `organization_id/site_id/integration_instance_id`。
2. 旧 assets/devices 不能直接成为新 Equipment/Device；需建立 Equipment、IoT Device 和 ExternalBinding。
3. 旧 users 的外部身份映射可保留，密码与本地认证字段应逐步退役。
4. 命令、审计和历史记录迁移后不得因资产删除级联消失。
5. 迁移期间不得让 NestJS 和 Go 同时写同一业务表；每张表必须有单一 owner。

## 7. ThingsBoard 集成现状

现有 Adapter 已覆盖：

- 按 Customer 隔离和刷新 TB Token
- Customer/User provisioning
- Customer 下 Device/Asset 列表及关系树
- latest/timeseries
- two-way RPC
- shared/server/client attributes

证据：`hvac-backend/src/integration-thingsboard/thingsboard.service.ts:19-303`。

但当前实现仍大量使用 `customerId`，部分读取直接使用 token namespace `default`，不满足已经决定的多个 IntegrationInstance 和显式 ExternalBinding 模型。

ThingsBoard 字段映射文档明确规定前端消费业务 DTO，而不是 TB 原始结构；同时承认大量 telemetry key、运行状态、Alarm、能耗算法和 RPC payload 尚未确认。

证据：`hvac-backend/docs/THINGSBOARD_FIELD_MAPPING.md:3-14`、`:41-69`、`:72-122`、`:154-163`。

**迁移约束：** 在定义 TelemetryPoint Schema 和 Command Schema 前，必须从目标 ThingsBoard 环境获取真实设备 Profile、key、单位、Attribute、Alarm 和 RPC 样本。文档中的 `TBD` 不能被实现者自行猜测。

## 8. EnergyAgent 与 Copilot 契约

### Runtime 边界

Copilot Runtime 将 `/api/v1/copilotkit` 映射到一个 LangGraph Agent，启用 A2UI，并单独提供 `/health`。

证据：`runtimes/copilot-runtime/server.mjs:21-82`。

### 已形成的 AI 状态语义

EnergyAgent 已定义：

- AnalysisRequest、ResolvedScope、WorkflowStatus、WorkflowError
- Investigation、AnalysisRun、Finding、Recommendation、Review
- Result Surface
- active run、过期运行丢弃、用户 mutation
- Evidence reference 和最多 3 个 Finding/Recommendation 的输出约束

证据：

- `agents/energy-agent/src/workflow/contracts.py:17-152`
- `agents/energy-agent/src/investigation/state.py:13-94`
- `agents/energy-agent/src/investigation/state.py:101-295`

### 当前演示约束

- scope 是 `building_id`，演示值为 A/B/C。
- 业务时区被固定为 `Asia/Shanghai`。
- 数据是进程内生成的 60 天、15 分钟确定性 Mock。
- 前端 AI Context 使用 `buildingId=b1`、页面路由和本地运维 Mock 状态。

证据：

- `agents/energy-agent/src/energy/data.py:1-22`
- `agents/energy-agent/src/energy/data.py:95-105`
- `agents/energy-agent/src/energy/data.py:190-221`
- `agents/energy-agent/src/investigation/state.py:318-353`
- `apps/hvac-web/src/ai/context.ts:10-33`
- `apps/hvac-web/src/ai/context.ts:43-53`

**判断：** Investigation 生命周期、Finding/Recommendation、Evidence 和可取消/失效运行语义应保留。A/B/C、`b1`、固定时区、Agent 内部数据集以及 Agent 对长期业务状态的所有权应废弃；Agent 后续必须通过 Go AI Tool API 获取授权后的真实数据。

## 9. 验证状态

2026-07-19 执行结果：

- HVAC Web `npm run build`：通过。存在大 chunk 警告，但不阻塞契约迁移。
- NestJS `npm test -- --runInBand`：34 套件中 33 通过；183 测试中 173 通过。唯一失败文件为 `registry.controller.spec.ts` 的 10 个测试，失败原因是测试模块无法解析新增 `LogtoAuthService`，不是测试断言已经证明业务错误。
- EnergyAgent `uv run pytest -q`：无法启动 `pytest`，因为 `pyproject.toml` 没有声明 pytest；当前测试文件存在，但测试入口不可重复执行。

证据：

- `hvac-backend/package.json:8-26`
- `agents/energy-agent/pyproject.toml:1-18`
- 本次 Wayfinder 会话命令输出。

**迁移约束：** 在建立 Go 对照测试前，先修复 NestJS Registry 测试依赖并为 EnergyAgent 固化测试依赖和统一命令。

## 10. 契约分类

### 必须兼容的业务行为

1. JWT 后的主体身份、Scope 权限和 Site 范围校验。
2. 前端通过统一 `/api/v1` 入口访问，不感知内部服务拓扑。
3. Site/设备越权必须拒绝并可审计。
4. 设备列表、详情、最新值、批量最新值、历史曲线。
5. REST 初始快照加实时增量、断线重连与重订阅。
6. 命令请求幂等、状态查询、设备 ACK/失败/超时记录。
7. 调度 CRUD、启停、目标绑定和运行历史。
8. Trace ID、健康检查、可诊断错误。
9. Investigation、Analysis Run、Evidence、Finding、Recommendation 和用户 Review 状态语义。
10. ThingsBoard 是设备接入、原始遥测、属性和 RPC 通道；业务 DTO 由平台生成。

### 可通过兼容层演进

1. `/api/v1` 下的具体资源路径和 DTO。
2. `X-Site-Id`：可暂时保留 Header，但值必须从旧 TB Customer/Building ID 迁移为平台 Site ID。
3. Socket.IO：可保留兼容端点，目标协议增加 cursor、sequence、租户授权和订阅配额。
4. `customerId`、`tbCustomerId` 和 Project facade：统一迁移为 Organization/Site/ExternalBinding。
5. 用户角色和 Scope 名称：保持能力等价，重构为范围化 RoleBinding。
6. 命令状态和 Scheduler 内部实现。
7. ThingsBoard Token 策略：从 Customer/default namespace 迁移到 IntegrationInstance。
8. EnergyAgent 的状态字段：保留语义，但使用平台 UUID、Site scope 和 Go 持久化。

### 应直接废弃

1. 默认演示 ID：`b1`、Building A/B/C。
2. 前端把 `buildingId` 直接作为 `X-Site-Id`。
3. 本地用户名密码成为平台身份权威。
4. 用户直接输入 `tbCustomerId` 绑定 Site。
5. `users.active_tb_customer_id` 作为 Active Site 权威。
6. `GET /devices?sync=true` 和读取时自动全量同步 TB。
7. 两套 timeseries 协议和后端 `range` 核心契约。
8. 前端容忍多种响应包和裸数据。
9. 浏览器逐设备查询并做建筑级聚合。
10. 未签名、未防重放的公开 TB ingest。
11. WebSocket 订阅跳过设备权限验证。
12. 内部命令使用普通用户模型、`customerId` 和伪造 `system-internal` actor。
13. 跨服务使用进程内 EventEmitter/InMemoryEventBus。
14. 单一 `default` ThingsBoard token namespace。
15. 命令和历史数据因 Device 删除而级联删除。
16. Agent 内部 Mock 数据作为生产分析来源。

## 11. Go 迁移必须遵守的执行约束

1. **统一入口先行。** Go Gateway 首先接管 `/api/v1`，已迁移路由进入 Go，未迁移路由代理 NestJS。
2. **建立 Legacy Anti-Corruption Layer。** 明确转换 legacy Customer/Site/Device/response/role/telemetry DTO，不允许旧字段进入新领域模型。
3. **只读链路先影子验证。** Registry、latest、timeseries 可双读比对，但只向一个调用方返回权威结果。
4. **命令禁止双写。** 同一命令只能由一个 Command owner 下发；通过 Site/设备范围灰度切换，不向 NestJS 和 Go 同时发送 RPC。
5. **数据库单一 owner。** 迁移期禁止两个运行时写同一张业务表；通过 Outbox、事件、迁移作业或只读兼容视图同步。
6. **先建立契约测试。** 从当前前端数据形状、Site 越权测试、命令幂等、调度和 Agent 状态测试中提取语言无关的黑盒测试。
7. **先修复测试基线。** 修复 Registry Controller 测试 DI，并为 Agent 增加可重复测试命令。
8. **先获取真实 TB 契约。** 未确认 key、单位、Alarm、Attribute 和 RPC 不得进入 Go 固定 Schema。
9. **审计历史不可丢。** 迁移过程中保留旧用户、旧 Customer、旧设备和外部 ID 的映射快照。
10. **兼容层有退役条件。** 每个 legacy endpoint、字段和表都要记录调用量、迁移目标、关闭条件和最终日期。

## 12. 新暴露的前置工作

当前 `THINGSBOARD_FIELD_MAPPING.md` 的关键内容仍是 `TBD`。遥测和命令架构票不能仅依赖代码推测真实设备协议，因此新增 Wayfinder 前置票：**获取真实 ThingsBoard 数据与控制契约**。
