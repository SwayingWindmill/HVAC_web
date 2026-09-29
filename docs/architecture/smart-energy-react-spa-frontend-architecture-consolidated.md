# 智慧能源 React SPA 前端架构基线

> 状态：目标架构 / 实施权威
> 日期：2026-09-11
> 适用范围：`apps/hvac-web`

## 1. 目标

HVAC Web 是同时承载管理后台、能源数据分析和轻量实时 HMI 的 React SPA。目标不是搭建“企业级模板”，而是形成一套三到五年后仍容易理解、升级、删除和扩展的前端工程。

工程原则：

- Feature 是主要业务边界。
- Route 负责 URL，不负责业务。
- Server State 不进入 Zustand。
- URL Search Params 是正式应用状态。
- 实时页面采用 Snapshot + Stream。
- Browser 只理解 Asset / Metric / Alarm / Command，不理解 MQTT、Modbus、PLC、IEC 等工业协议。
- OpenAPI 是 Go 与 TypeScript 的 API 契约。
- 不建立万能 Store、Repository、BaseCRUD、BaseFeature、Global Event Bus。
- 标准已经解决的问题直接使用标准；成熟库已经解决的问题直接使用成熟库。
- 新抽象至少需要两个真实当前使用场景证明价值。
- 不做兼容性设计；旧体系迁完即删除，不保留双栈适配层。
- 不做无依据的防御性编程；只在真实信任边界、外部 I/O、安全边界和已发生失败模式上防御。

## 2. 目标技术栈

```text
React 19
Vite
TypeScript
Tailwind CSS
shadcn/ui
TanStack Router
TanStack Query
TanStack Table
React Hook Form
Zod
Apache ECharts
date-fns
WebSocket / current realtime transport
Vitest
Playwright
Lucide
```

X6/G6 不是通用 UI 或统计图库，只在固定工程拓扑和节点—边关系确有业务价值时使用。

Ant Design、ProComponents、Ant Design Charts、React Router 以及围绕它们形成的兼容抽象均为迁移源，不再用于新代码。

## 3. 状态边界

```text
URL State       -> TanStack Router
Server State    -> TanStack Query
Form State      -> React Hook Form
Local UI State  -> useState / useReducer
Shared UI State -> Zustand（只有真实跨组件共享需求时）
Realtime        -> Realtime Layer
```

同一事实只存在一个权威状态来源。

典型 URL State：

```text
page
pageSize
search
sort
filters
site
timeRange
from
to
```

设备、告警、遥测、能源汇总、用户等服务端事实不得镜像到 Zustand。

## 4. 顶层目录：从最小真实结构开始

目标不是一次性创建所有可能目录，而是先保持：

```text
src/
├── routes/
├── features/
├── components/
│   ├── ui/
│   ├── domain/
│   ├── charts/
│   └── layout/
├── api/
│   ├── generated/
│   └── client.ts
├── realtime/
│   └── client.ts
├── auth/
├── lib/
├── app.tsx
└── main.tsx
```

只有共享内容真实出现后，才增加 `stores/`、`hooks/`、`schemas/`、`types/` 等目录。禁止为了“架构完整”预建空层。

## 5. Feature 结构

简单 Feature 保持扁平：

```text
features/devices/
├── queries.ts
├── schema.ts
├── device-list.tsx
├── device-detail.tsx
└── device-form.tsx
```

文件增长后再自然拆为 `api/`、`components/`、`hooks/`、`schema/` 等。

依赖方向：

```text
routes
  -> features
      -> domain/shared components
          -> ui/lib/infrastructure
```

基础 UI 不反向依赖业务 Feature。

## 6. Router

使用 TanStack Router File-Based Routing。Route 文件保持薄，只承担：

- path / search schema
- loader/beforeLoad 的路由级职责
- route metadata
- auth/permission 导航控制
- route error/loading composition
- feature component 挂载

业务查询、mutation、计算和组件组合放在 Feature。

目标路由：

```text
/login
/_authenticated/dashboard
/_authenticated/sites
/_authenticated/assets
/_authenticated/devices
/_authenticated/devices/$deviceId
/_authenticated/realtime
/_authenticated/energy/overview
/_authenticated/energy/consumption
/_authenticated/energy/load
/_authenticated/energy/cost
/_authenticated/pv
/_authenticated/storage
/_authenticated/charging
/_authenticated/alarms
/_authenticated/commands
/_authenticated/reports
/_authenticated/system/users
/_authenticated/system/roles
/_authenticated/system/settings
```

认证直接用父路由 `beforeLoad`；不再建立单独 guards 框架。

旧 URL 不通过兼容 redirect 长期保留；迁移完成后直接删除旧 route。

## 7. Search Params

分页、搜索、排序、筛选、站点和时间范围进入 Router Search Params，并用 Zod 校验。

```text
/devices?page=2&pageSize=50&site=sg01&status=online&q=meter
```

页面不得同时维护第二份对应 `useState`。

## 8. TanStack Query

所有后端数据原则上都是 Server State：

```text
REST/OpenAPI -> TanStack Query -> Feature Component
```

Query Key 表示数据身份，而不是页面或组件身份：

```text
['devices', 'list', params]
['devices', 'detail', id]
['alarms', 'list', params]
['telemetry', 'history', params]
```

简单 Feature 直接导出 `queryOptions`；只有复杂度出现后才引入 `deviceKeys` 等 Key Factory。

`staleTime` 由数据生命周期决定，不使用一个全局万能值。

Mutation 默认以后端事实为准；设备控制、Alarm Ack、关键配置等不做“先显示成功”的乐观更新。

## 9. API / OpenAPI

Go Backend 发布 OpenAPI，生成：

- request types
- response types
- schema types
- typed client

API DTO 不在前端重复手写。前端只为真实 View Model / Domain Presentation Model 定义额外类型。

API Client 保持薄，只负责真实基础设施职责：

- base URL
- authentication / credentials
- AbortSignal
- Problem Details parsing
- 必要 request metadata

页面和 Feature Component 不直接 `fetch()`。

推荐错误契约为 RFC 9457 Problem Details；前端不再发明第二套通用错误协议。

API Client 不负责 Toast、Router Navigation、Permission、Query Cache 或业务逻辑。

## 10. Realtime

业务组件禁止 `new WebSocket()`。

统一 realtime layer，业务 API 最小化：

```ts
interface RealtimeClient {
  connect(): void
  disconnect(): void
  subscribe(subscription: Subscription, listener: MessageListener): () => void
}
```

实时页面使用：

```text
REST Snapshot
    +
Realtime Stream
```

默认尽量共享一个应用级实时连接，但“单连接”不是教条；只有 endpoint、QoS 或安全策略真实不同才拆连接。

连接可以延迟到首次订阅；只有全局告警/通知/设备状态确实需要时才登录即连接。

基础状态：

```text
idle
connecting
connected
reconnecting
disconnected
```

重连后恢复真实订阅并刷新 snapshot。平台连接状态与设备连接状态必须分开建模。

不提前实现 Ring Buffer、reference counting、复杂 replay、QoS、ACK framework。若当前后端协议已经有 recovery cursor / capability 等机制，只保留能对应到真实后端契约、安全边界或已发生故障的部分；不因为“已有代码”保留额外复杂度。

## 11. Browser 协议边界

Browser 只认识业务语义：

```text
Asset
Metric
Alarm
Command
Quality
Timestamp
```

Browser 不认识：

```text
MQTT Topic
Modbus Address
PLC Register
IEC 61850 Logical Node
```

工业协议转换属于 Backend / Edge。

## 12. UI / Design System

shadcn/ui 提供标准 primitives。项目不重新实现 Button、Dialog、Dropdown、Tabs、Select 等通用控件。

项目值得维护的是能源 Domain Components：

```text
MetricValue
TelemetryQuality
DeviceStatus
AlarmSeverity
CommandStatus
SiteSelector
AssetSelector
TimeRangePicker
TimeSeriesChart
RealtimeConnectionIndicator
```

设计系统服务于 PRODUCT.md 和批准参考图，不反过来要求所有业务长成同一种 Card。

Lucide 是唯一业务图标体系。

## 13. Table

使用 TanStack Table + UI primitives。

V1 只解决：

- server pagination
- server sorting
- server filtering
- column visibility
- row selection
- loading
- empty state

Table 状态与 URL 联动：

```text
filter/sort/page change
 -> Router Search Params
 -> Query Key
 -> TanStack Query fetch
```

不建立 100 props 的 SuperTable，不提前实现 Pivot、Excel Editing、复杂 Grouping、Tree Grid Engine。

## 14. Form

使用 React Hook Form + Zod。

```text
Zod Schema -> Form -> Mutation -> Query Invalidation
```

简单表单逻辑留在 Feature；有真实复用后才提取 hook。

## 15. Charts

全平台统计图统一 Apache ECharts。

第一批只需要真正通用的：

```text
TimeSeriesChart
EnergyBarChart
MetricCard / MetricValue
```

只有交互、布局或业务语义真实不同才增加 `PowerCurveChart`、`SOCChart` 等新组件。

浏览器不做大型能源聚合；后端负责 aggregation/downsampling，前端主要做 presentation transform。

## 16. 时间、时区、单位和数据质量

这些是能源 UI 一等公民。

时间：

```text
transport/storage -> UTC
business display/range -> Site Timezone
```

Today / Yesterday / Daily Energy / Peak-Off-Peak / Settlement 均按 Site Timezone。

单位统一 formatter：

```text
formatPower
formatEnergy
formatVoltage
formatTemperature
formatPercentage
```

Telemetry 显式表达：

```text
value
unit
quality
updatedAt
```

Quality 至少：

```text
GOOD
BAD
UNCERTAIN
STALE
```

状态不能只依赖颜色；必须有文字或图标+文字。

## 17. Device / Alarm / Command 状态

设备状态是后端事实，不简单等价为 UI bool；至少区分在线、离线、降级、未知、维护等真实业务状态。

Alarm 至少区分：

```text
ACTIVE
ACKNOWLEDGED
CLEARED
```

Command 是独立于普通 CRUD 的安全工作流，展示：

```text
Asset
Current State
Requested Action
Parameters
Impact
PENDING -> SENT -> ACKED -> SUCCEEDED / FAILED
```

ACK 与最终状态回读分开；结果未知不能伪装成成功或确认失败。

## 18. Permission

前端权限只服务 UX：

```text
<Can permission="alarm.ack" />
usePermission('alarm.ack')
```

Browser 不实现复杂 RBAC/ABAC/Policy DSL。真正授权由 Go Backend 执行。

菜单、route navigation guard 和 backend authorization 语义保持一致，但 backend 是最终 authority。

## 19. Error / Loading

V1 使用 Root Error Boundary + Route Error Boundary；只有确有独立失败价值的高价值 Widget 再增加局部 boundary。

错误 UX 区分：

```text
Page Error
Form Error
Mutation Error
Realtime Error
```

Realtime disconnected 是稳定状态，不做持续 Toast。

Loading：

```text
first load -> Skeleton
background refetch -> subtle indicator
table -> table loading
chart -> chart skeleton
mutation -> button pending
```

## 20. Authentication / Runtime Config

认证按部署模型选择最简单正确方案。同域 SPA + Go API 优先 Secure HttpOnly Cookie；SPA 作为 OAuth public client 时使用 Authorization Code + PKCE。

Runtime Config 不是默认必须能力；只有同一构建产物需要跨环境复用时引入 `/config.json` 类运行时配置。

## 21. Testing

Vitest 保护纯逻辑和明确 contract：

```text
unit conversion
time calculation
Zod schema
query param mapper
domain formatter
```

Component test 只覆盖复杂关键交互：Command Dialog、Alarm Ack、复杂 Form、TimeRangePicker、Permission UI。

Playwright 覆盖高价值主流程：Login、Device List/Edit、Realtime Device、Historical Curve、Alarm Ack、Command Execute、Permission Denied。

不追求 100% coverage，不为实现细节和兼容路径保留测试。

## 22. 明确禁止的抽象

V1 不建立：

```text
BaseRepository
AbstractApiService
BaseCRUDPage
BaseFeature
Global Event Bus
Universal Store
Universal Form Engine
Universal Table Engine
```

前端不要机械复制 Java/后端分层。

## 23. Code Splitting

依赖 TanStack Router + Vite 做 route/feature code splitting。大型可选模块 ECharts、Map、Calendar、Editor 按实际 route/feature 延迟加载。

不为每页机械手写一层无意义 lazy wrapper。

## 24. 当前仓库迁移策略

迁移不是“双栈架构”。同一 surface 迁移时直接改为最终实现，旧实现随后删除。

顺序：

```text
0. Architecture authority / governance
1. App shell + providers + target UI primitives
2. Device Management vertical slice
3. Realtime Device Monitoring vertical slice
4. Alarm Center vertical slice
5. Energy / Dashboard / Command
6. Remaining system/report/operations features
7. Delete Ant Design / ProComponents / Ant Design Charts
8. Delete obsolete compatibility code and migration-only scripts/tests
```

当前已经正确的资产优先保留并重新归属：React 19、Vite、TypeScript、TanStack Router、TanStack Query、Zod、OpenAPI generated clients、真实 backend contracts、真实 telemetry authorization/recovery requirements。

当前需要重点重构的资产：Ant/Pro UI、Ant Design Charts、巨型 shared UI pattern 层、Feature ownership 混杂、散落 Query Keys、直接 fetch、URL/Store 重复状态、历史 migration/certification 脚本。

## 25. Vertical Slice 验证顺序

### Device Management

验证：

```text
Route -> Search Params -> Query -> Go API -> DataTable -> Detail -> Form -> Mutation -> Permission -> Realtime Status
```

### Realtime Device Monitoring

验证：

```text
REST Snapshot + Realtime Stream + Telemetry Quality + Unit Formatting + Realtime Chart
```

### Alarm Center

验证：

```text
Server Table + Filtering + Mutation + Permission + Realtime Event + Alarm Lifecycle
```

三条完成后，CRUD、Realtime、Domain Workflow 三种核心模式即被真实业务验证。

## 26. 最终判断标准

每次新增代码先判断它属于哪类事实和哪一层；如果无法明确归属，先收紧边界而不是增加中间层。

最终工程不是“用了很多新技术”，而是：

> 只获取当前页面真正需要的数据，只订阅用户正在看的实时数据；标准问题交给成熟生态，研发复杂度只花在能源领域本身。