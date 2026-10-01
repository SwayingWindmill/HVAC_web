# 智慧能源 React SPA 前端工程骨架设计（评审版）

> 目标：面向智慧能源管理平台，设计一套现代、清晰、可长期演进的 React SPA 前端工程骨架。  
> 原则：**符合最佳实践、不过度防御、不重复造轮子、复杂度随业务增长。**

---

## 1. 结论摘要

前端采用以下核心技术栈：

- React 19
- TypeScript
- Vite
- Tailwind CSS
- shadcn/ui
- TanStack Router
- TanStack Query
- TanStack Table
- React Hook Form
- Zod
- Apache ECharts
- OpenAPI
- WebSocket
- Vitest
- Playwright

整体状态边界：

```text
URL State      → TanStack Router
Server State   → TanStack Query
Form State     → React Hook Form
Local UI State → React useState/useReducer
Shared UI State→ Zustand（确有需要时）
Realtime       → Realtime Layer
```

最重要的工程原则不是“多分几层”，而是：

1. Feature 是主要业务边界。
2. Route 负责 URL，不负责业务。
3. Server State 不进入 Zustand。
4. URL 查询状态进入 Router Search Params。
5. 实时数据采用 Snapshot + Stream。
6. Browser 不理解 MQTT、PLC、IEC 等工业协议。
7. OpenAPI 是 Go 与 TypeScript 的接口契约。
8. 不建立万能 Store、BaseCRUD、Repository、EventBus。
9. 标准已经解决的问题直接用标准。
10. 只有智慧能源领域能力值得重点自研。

---

# 2. 项目定位

该前端不是普通后台模板，而是同时承担：

- 管理后台
- 能源数据分析平台
- 轻量实时 HMI

典型模块：

```text
Dashboard
Site
Asset
Device
Realtime
Energy
PV
Storage
Charging
Alarm
Command
Report
User
Role
Settings
```

因此前端既不能只围绕 CRUD 设计，也不应直接照搬传统 SCADA 前端架构。

目标是：

> 使用现代 Web 技术构建一个能够承载管理、分析和实时监控的 React SPA。

---

# 3. 为什么选择 React SPA

智慧能源管理平台绝大多数页面位于认证之后：

```text
/dashboard
/sites
/assets
/devices
/realtime
/energy
/alarms
/reports
/settings
```

此类页面通常没有 SEO 需求，核心需求是：

- 快速交互
- 大量表格
- 复杂图表
- 高频筛选
- 历史数据查询
- 实时状态更新
- 前后端独立部署

因此采用：

```text
Browser
   │
   ├── REST
   └── WebSocket
   │
   ▼
Go Backend
```

比引入额外 SSR/BFF 层更直接。

---

# 4. 顶层目录

V1 推荐保持克制：

```text
src/
├── routes/
├── features/
├── components/
│   ├── ui/
│   ├── domain/
│   ├── charts/
│   └── layout/
│
├── api/
│   ├── generated/
│   └── client.ts
│
├── realtime/
│   └── client.ts
│
├── auth/
├── lib/
├── app.tsx
└── main.tsx
```

不要一开始就创建：

```text
stores/
hooks/
schemas/
types/
guards/
bootstrap/
services/
repositories/
utils/
```

这些目录并不是禁止存在，而是**等真正出现共享内容后再创建**。

目录应该描述已经存在的复杂度，而不是提前预演未来复杂度。

---

# 5. Feature-Oriented 结构

业务代码以 Feature 为主要边界。

例如：

```text
features/devices/
├── queries.ts
├── schema.ts
├── device-list.tsx
├── device-detail.tsx
└── device-form.tsx
```

如果未来文件增多，再拆：

```text
features/devices/
├── api/
├── components/
├── hooks/
├── schema/
└── ...
```

不要求每个 Feature 一开始就拥有固定子目录。

依赖方向：

```text
routes
  ↓
features
  ↓
domain/shared components
  ↓
ui/lib/infrastructure
```

基础 UI 不应反向依赖业务 Feature。

---

# 6. Router 设计

使用 TanStack Router File-Based Routing。

推荐路由：

```text
/
├── /login
└── /_authenticated
    ├── /dashboard
    ├── /sites
    ├── /assets
    ├── /devices
    │   └── /$deviceId
    ├── /realtime
    ├── /energy
    │   ├── /overview
    │   ├── /consumption
    │   ├── /load
    │   └── /cost
    ├── /pv
    ├── /storage
    ├── /charging
    ├── /alarms
    ├── /commands
    ├── /reports
    └── /system
        ├── /users
        ├── /roles
        └── /settings
```

Route 文件保持薄：

```tsx
export const Route = createFileRoute('/_authenticated/devices/$deviceId')({
  component: DeviceDetail,
})
```

业务逻辑放到：

```text
features/devices/device-detail.tsx
```

不再额外引入一层 `pages/` 进行无意义转发。

---

# 7. Authenticated Route

不单独维护 `guards/` 层。

直接使用 Router 提供的路由前置能力：

```tsx
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: ({ context }) => {
    if (!context.auth.user) {
      throw redirect({ to: '/login' })
    }
  },
})
```

这样：

```text
_authenticated/*
```

自然继承认证保护。

需要注意：

> 前端 Route Guard 只是 UX 和导航控制，真正授权必须由 Go Backend 完成。

---

# 8. Search Params 即正式状态

管理后台的这些状态应进入 URL：

```text
page
pageSize
search
sort
filter
site
status
timeRange
```

例如：

```text
/devices?page=2&pageSize=50&site=sg01&status=online&q=meter
```

推荐结合 Zod 做校验：

```ts
const deviceSearchSchema = z.object({
  page: z.coerce.number().default(1),
  pageSize: z.coerce.number().default(20),
  q: z.string().optional(),
  site: z.string().optional(),
  status: z.enum(['online', 'offline', 'degraded']).optional(),
})
```

优势：

- 刷新不丢
- 浏览器返回正常
- 链接可分享
- 多标签页行为自然

不要再额外维护第二份：

```ts
const [page, setPage] = useState(...)
```

---

# 9. Server State

所有来自后端的数据原则上进入 TanStack Query：

```text
sites
assets
devices
users
alarms
energy summary
reports
tariffs
historical telemetry
```

流程：

```text
REST API
   ↓
TanStack Query
   ↓
React Component
```

而不是：

```text
API
 ↓
Zustand
 ↓
Component
```

TanStack Query 负责：

- Cache
- Loading
- Error
- Refetch
- Retry
- Request Deduplication
- Invalidation

不要再自研：

```text
request cache
global loading store
server data store
```

---

# 10. Zustand 的边界

Zustand 不作为默认架构支柱。

优先：

```text
Local UI State
→ useState / useReducer

URL State
→ TanStack Router

Server State
→ TanStack Query
```

只有真正出现跨多个无关组件共享的客户端状态时，再使用 Zustand。

适合：

```text
dashboard edit mode
selected workspace
map layer state
global UI preference
```

不适合：

```text
devices
users
alarms
telemetry
energy summary
```

---

# 11. OpenAPI 契约

Go Backend 应发布 OpenAPI。

推荐链路：

```text
Go Backend
   ↓
openapi.json
   ↓
TypeScript Generator
   ↓
src/api/generated/
```

至少生成：

- Request Types
- Response Types
- API Schema Types
- Typed Client

避免：

Go：

```go
type Device struct {
    ID   string
    Name string
}
```

前端又重复：

```ts
interface Device {
  id: string
  name: string
}
```

长期必然产生漂移。

原则：

> API DTO 由 OpenAPI 统一生成；前端只定义真正的 View Model。

---

# 12. API Client

API Client 应该保持很薄。

职责：

```text
Base URL
Authentication
credentials
AbortSignal
Problem Details parsing
```

如果 OpenAPI generator 已经生成 Fetch Client，就不要再手写一套 Axios 风格框架。

建议：

```text
Generated Client
      ↓
Thin customFetch
      ↓
Feature queryOptions
```

API Client 不负责：

```text
Toast
Router Navigation
Permission
Query Cache
Business Logic
```

---

# 13. API Error

推荐后端统一采用 RFC 9457 Problem Details，而不是自创错误协议。

基本结构：

```json
{
  "type": "https://example.com/problems/device-not-found",
  "title": "Device not found",
  "status": 404,
  "detail": "Device ESS-01 does not exist",
  "instance": "/devices/ESS-01"
}
```

必要时通过扩展字段增加：

```text
code
requestId
validationErrors
```

这样避免团队重新发明一套 HTTP 错误格式。

---

# 14. Query 组织方式

简单 Feature 直接：

```ts
export const deviceListQuery = (params: DeviceParams) =>
  queryOptions({
    queryKey: ['devices', 'list', params],
    queryFn: () => api.listDevices(params),
    staleTime: 30_000,
  })
```

组件：

```tsx
const query = useQuery(deviceListQuery(search))
```

复杂 Feature 再引入：

```text
deviceKeys
alarmKeys
telemetryKeys
```

Query Key Factory 不是强制模板。

原则：

> Query Key 必须规范，Query Key Factory 按复杂度引入。

---

# 15. Query Key 规则

推荐：

```text
Resource

Resource + list

Resource + list + params

Resource + detail + id
```

例如：

```text
['devices', 'list', params]
['devices', 'detail', id]

['alarms', 'list', params]

['telemetry', 'history', params]
```

Query Key 表示：

> 数据身份。

不应该包含：

```text
Component Name
Page Name
UI State
```

---

# 16. staleTime

不要全局使用一个值。

不同数据生命周期不同：

```text
Site List              5 min
Asset Metadata         5~30 min
Device Config          1~5 min
Dashboard Summary      30 sec
Active Alarm           10~30 sec
Historical Telemetry   较长
```

由 Feature 自己决定。

避免：

```ts
staleTime: 0
```

导致频繁重复请求。

也避免：

```ts
staleTime: Infinity
```

让所有数据永久不更新。

---

# 17. Mutation

标准做法：

```ts
const mutation = useMutation({
  mutationFn: api.createDevice,

  onSuccess: async () => {
    await queryClient.invalidateQueries({
      queryKey: ['devices'],
    })
  },
})
```

简单 Mutation 可以直接放在 Feature Component 中。

当多个地方复用时，再提取：

```text
useCreateDevice
useUpdateDevice
```

不要为了分层机械创建 Hook。

---

# 18. 乐观更新

适合：

```text
UI preference
简单标签
非关键开关
```

谨慎：

```text
设备控制
Alarm Ack
能源参数
关键配置
```

尤其：

```text
PCS Start
Breaker Open
Command Execute
```

必须以后端真实状态为准。

不要为了 UI 看起来更快提前显示成功。

---

# 19. 实时数据核心模式

所有实时页面统一采用：

```text
Snapshot + Stream
```

流程：

```text
页面进入
   ↓
REST Snapshot
   ↓
展示当前状态
   ↓
WebSocket Subscribe
   ↓
Incremental Updates
```

WebSocket 不负责恢复完整历史，也不负责单独构建页面初始状态。

---

# 20. Realtime Layer

业务组件禁止：

```ts
new WebSocket(...)
```

统一：

```text
realtime/client.ts
```

V1 只提供最小能力：

```ts
interface RealtimeClient {
  connect(): void
  disconnect(): void

  subscribe(
    subscription: Subscription,
    listener: MessageListener,
  ): () => void
}
```

第一版只实现：

```text
connect
disconnect
subscribe
unsubscribe
reconnect
```

不要一开始实现：

```text
reference counting
resume token
QoS
ack protocol
delivery guarantee
replay
complex event bus
```

这些属于真实需求出现后的演进项。

---

# 21. WebSocket 协议

保持简单：

```json
{
  "version": 1,
  "type": "subscribe",
  "payload": {
    "assetId": "ESS-01",
    "metrics": ["power", "soc"]
  }
}
```

Telemetry：

```json
{
  "version": 1,
  "type": "telemetry",
  "payload": {
    "assetId": "ESS-01",
    "metric": "power",
    "value": 823.4,
    "unit": "kW",
    "quality": "GOOD",
    "timestamp": "2026-09-11T06:00:00Z"
  }
}
```

Browser 只认识：

```text
Asset
Metric
Alarm
Command
```

Browser 不应该理解：

```text
MQTT Topic
Modbus Address
IEC 61850 Logical Node
PLC Register
```

这些属于后端/Edge。

---

# 22. WebSocket 生命周期

默认策略：

> 尽量共享一个应用级实时连接。

但这不是硬规则。

如果未来：

```text
Alarm Stream
High-Frequency Telemetry
```

存在不同 Endpoint、QoS 或安全策略，也可以拆连接。

连接可以延迟到第一次出现实时订阅时再建立。

只有当：

```text
Header 始终显示实时告警
全局 Device Status
全局通知
```

等功能存在时，才需要登录后立即建立应用级连接。

---

# 23. Reconnect

基础状态：

```text
idle
connecting
connected
reconnecting
disconnected
```

采用简单 exponential backoff。

重连成功后：

```text
restore subscriptions
+
refresh snapshot
```

因为断线期间错过的数据不能假设 WebSocket 会自动重放。

---

# 24. 平台连接与设备连接分离

必须区分：

```text
PlatformConnectionState
```

和：

```text
DeviceConnectionState
```

例如：

```text
WebSocket disconnected
```

只代表 Browser 与平台实时通道断开。

不代表所有设备：

```text
OFFLINE
```

设备在线状态应以后端判断为准。

---

# 25. 高频实时数据

V1 不提前实现复杂 Ring Buffer。

对于普通智慧能源界面：

```text
1 sec
5 sec
10 sec
```

刷新通常足够。

简单实现先满足需求。

只有真实测出：

```text
GC pressure
render pressure
memory growth
```

以后，再引入固定长度 Ring Buffer。

---

# 26. ECharts 规划

全平台统一：

```text
Apache ECharts
```

不要同时引入：

```text
Recharts
Chart.js
Highcharts
ECharts
```

第一批只封装：

```text
TimeSeriesChart
EnergyBarChart
MetricCard
```

不要一开始创建：

```text
PowerCurveChart
SOCChart
TemperatureChart
FrequencyChart
```

如果行为相同，只需要：

```tsx
<TimeSeriesChart unit="kW" />
```

真正交互、布局、语义不同再新建组件。

---

# 27. 图表计算边界

Browser 不负责大型能源计算。

禁止：

```text
几百万点
   ↓
Browser
   ↓
ECharts aggregate
```

应该：

```text
Backend
   ↓
Aggregation / Downsampling
   ↓
1000~5000 display points
   ↓
ECharts
```

前端只负责：

```text
tooltip
legend
label
presentation transform
```

---

# 28. Domain Components

这是值得从 V1 就建设的一层。

第一批：

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

例如：

```tsx
<MetricValue
  value={823.42}
  unit="kW"
  quality="GOOD"
  timestamp={timestamp}
/>
```

能源语义：

```text
Value
Unit
Precision
Quality
Timestamp
```

应该集中处理，而不是散落页面。

---

# 29. 单位

不要：

```tsx
{value} + ' kW'
```

到处拼字符串。

建立统一格式函数：

```text
formatPower
formatEnergy
formatVoltage
formatTemperature
formatPercentage
```

例如：

```text
980 W
→ 0.98 kW

1,200,000 W
→ 1.2 MW
```

统一显示策略。

---

# 30. 时间与时区

后端 API：

```text
ISO 8601
UTC
```

前端：

```text
transport/storage
→ UTC

display/business range
→ Site Timezone
```

智慧能源中的：

```text
Today
Yesterday
Daily Energy
Peak/Off-Peak
Monthly Settlement
```

必须按照 Site Timezone，而不是开发者浏览器时区。

---

# 31. Telemetry Quality

Telemetry UI 必须认识：

```text
GOOD
BAD
UNCERTAIN
STALE
```

例如：

```text
823.4 kW
Updated 5 sec ago
```

和：

```text
823.4 kW
STALE
Last update 22 min ago
```

必须有明显区别。

不能只显示数值。

---

# 32. Accessibility

能源系统的重要状态不能只依赖颜色。

不要只显示：

```text
绿色
黄色
红色
```

应该同时提供：

```text
Online
Warning
Critical
```

或者图标 + 文本。

尤其：

```text
Alarm Severity
Telemetry Quality
Device Status
Command Status
```

属于业务核心信息。

基础 Accessibility 尽量依赖 shadcn/Radix/Base UI，而不是自研。

---

# 33. DataTable

第一版只做：

```text
Server Pagination
Server Sorting
Server Filtering
Column Visibility
Loading
Empty State
Row Selection
```

不要一开始做：

```text
Pivot
Formula
Excel Editing
Tree Grid Engine
Complex Grouping
```

Table 状态与 URL 联动：

```text
User changes filter
      ↓
Router Search Params change
      ↓
Query Key change
      ↓
TanStack Query fetch
```

---

# 34. Permission

权限保持轻量。

后端返回：

```json
{
  "permissions": [
    "alarm.read",
    "alarm.ack",
    "device.read",
    "telemetry.read"
  ]
}
```

前端：

```tsx
<Can permission="alarm.ack">
  <AcknowledgeButton />
</Can>
```

或者：

```ts
const canAck = usePermission('alarm.ack')
```

不要在 Browser 建：

```text
RBAC Engine
ABAC Engine
Policy DSL
复杂授权计算器
```

复杂授权由后端负责。

前端权限只用于 UX。

---

# 35. Command UX

设备控制不是普通 CRUD。

控制页面需要显示：

```text
Asset
Current State
Requested Action
Parameters
Impact
```

然后明确 Confirm。

发送后展示真实生命周期：

```text
PENDING
SENT
ACKED
SUCCEEDED
FAILED
```

不要发请求后立即显示：

```text
Success
```

---

# 36. Alarm UX

告警至少区分：

```text
ACTIVE
ACKNOWLEDGED
CLEARED
```

需要展示：

```text
Severity
Source
First Occurred
Last Occurred
Acknowledged By
```

Critical Alarm 可以突出，但不要让整个产品长期红色闪烁。

避免 Alarm Fatigue。

---

# 37. Error Boundary

V1 使用：

```text
Root Error Boundary
+
Route Error Boundary
```

就够。

只有 Dashboard 中真正需要独立失败的高价值 Widget 再单独增加 Error Boundary。

不要每个 Card 都包一层。

---

# 38. Error UX

区分：

```text
Page Error
Form Error
Mutation Error
Realtime Error
```

不要所有错误统一：

```text
toast(error.message)
```

例如：

```text
Realtime disconnected
```

应该作为稳定连接状态展示，而不是不断弹 Toast。

---

# 39. Loading UX

根据场景：

```text
First Load
→ Skeleton

Background Refetch
→ subtle indicator

Table
→ table loading

Chart
→ chart skeleton

Mutation
→ button pending
```

后台刷新时不要清空整个 Dashboard。

---

# 40. Runtime Config

不作为 V1 强制能力。

如果 CI/CD 每个环境单独构建：

```text
import.meta.env
```

足够。

只有明确需要：

> 同一 Docker Image 部署多个环境

时，再增加：

```text
/config.json
```

Runtime Config 是部署策略，不是前端架构必需项。

---

# 41. Authentication

优先根据部署模型选择。

如果 SPA 与 Go API 同域：

```text
Browser
 ↓
Secure HttpOnly Cookie
 ↓
Go
 ↓
OIDC Provider
```

通常最简单。

如果 SPA 本身作为 OAuth Public Client：

```text
Authorization Code + PKCE
```

不要把长期 Session Token 随意存入 localStorage。

不默认引入：

```text
BFF
DPoP
mTLS
Token Mediator
```

除非实际威胁模型需要。

---

# 42. Testing

测试保持务实。

## Unit

Vitest：

```text
Unit Conversion
Time Calculation
Zod Schema
Query Param Mapper
Domain Formatter
```

## Component

只覆盖复杂关键交互：

```text
Command Dialog
Alarm Ack
Complex Form
TimeRangePicker
Permission UI
```

## E2E

Playwright 第一批：

```text
Login
Device List/Edit
Realtime Device Page
Historical Curve
Alarm Ack
Command Execute
Permission Denied
```

不追求 100% Coverage。

优先保护关键业务路径。

---

# 43. 不自研的能力

明确不自研：

```text
UI Library
Router
Query Cache
Form Engine
Table Engine
Chart Engine
Date Library
OAuth/OIDC
HTTP Framework
WebSocket QoS
Frontend RBAC Engine
```

成熟生态已经能解决这些问题。

真正值得自己做：

```text
能源 UX
设备交互
能源图表
实时监控
告警体验
控制流程
单位/质量/时间语义
能源分析
```

---

# 44. 明确不做的抽象

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

尤其不要把 Java Backend 的分层思维机械复制到 React。

前端保持：

```text
OpenAPI Client
+
queryOptions
+
Feature Components
```

已经足够。

---

# 45. Code Splitting

依赖 TanStack Router + Vite 提供的路由级自动 Code Splitting。

不需要每个页面都手写：

```tsx
lazy(() => import(...))
```

大型可选模块：

```text
ECharts
Map
Calendar
Editor
```

按路由/Feature 延迟加载。

---

# 46. 第一阶段实施

第一阶段不是先做漂亮 Dashboard。

先完成基础设施：

```text
React + Vite

TanStack Router

TanStack Query

shadcn

Auth

OpenAPI client

Layout

Error handling

Realtime client
```

再完成少量 Domain Components：

```text
MetricValue
DeviceStatus
TelemetryQuality
TimeRangePicker
TimeSeriesChart
```

---

# 47. 第一个 Vertical Slice

建议：

> Device Management

打通：

```text
Route
 ↓
Search Params
 ↓
Query
 ↓
Go API
 ↓
Table
 ↓
Detail
 ↓
Form
 ↓
Mutation
 ↓
Permission
 ↓
Realtime Status
```

这个 Feature 可以验证大部分工程设计。

---

# 48. 第二个 Vertical Slice

建议：

> Realtime Device Monitoring

验证：

```text
REST Snapshot

+

WebSocket Stream

+

Telemetry Quality

+

Unit Formatting

+

Realtime Chart
```

---

# 49. 第三个 Vertical Slice

建议：

> Alarm Center

验证：

```text
Server Table
Filtering
Mutation
Permission
Realtime Event
Alarm State
```

三个 Vertical Slice 做完后：

```text
CRUD
Realtime
Domain Workflow
```

三种核心模式基本都已验证。

---

# 50. V1 最终工程骨架

最终推荐：

```text
src/
├── routes/
│   ├── __root.tsx
│   ├── login.tsx
│   └── _authenticated/
│       ├── dashboard.tsx
│       ├── devices/
│       ├── energy/
│       └── alarms.tsx
│
├── features/
│   ├── dashboard/
│   ├── devices/
│   ├── energy/
│   └── alarms/
│
├── components/
│   ├── ui/
│   ├── domain/
│   ├── charts/
│   └── layout/
│
├── api/
│   ├── generated/
│   └── client.ts
│
├── realtime/
│   └── client.ts
│
├── auth/
│
├── lib/
│   ├── time.ts
│   └── units.ts
│
├── app.tsx
└── main.tsx
```

当业务增长时再自然增加：

```text
features/pv
features/storage
features/charging
features/reports
```

而不是一开始建立大量空目录。

---

# 51. 最终架构原则

最终团队应长期坚持：

1. Feature 是业务边界。
2. Route 负责 URL，不负责业务。
3. Server State 优先进入 TanStack Query。
4. URL State 优先进入 TanStack Router。
5. Local State 优先使用 React 自身能力。
6. Zustand 按实际共享状态需求引入。
7. Realtime 只建立最小基础设施。
8. 实时页面采用 Snapshot + Stream。
9. OpenAPI 是 Go 和 TypeScript 的 API 契约。
10. Browser 不理解 MQTT 和工业协议。
11. ECharts 只做显示，后端负责大规模数据计算。
12. Quality、Timestamp、Unit 是 Telemetry UI 一等公民。
13. Permission 主要解决 UX，Backend 才负责真正 Security。
14. 不提前建设 Repository、BaseCRUD、EventBus 等通用框架。
15. 新增抽象前，至少有两个真实使用场景证明它值得存在。
16. 性能优化以真实 profiling 为依据，而不是提前猜测。
17. Accessibility 不依赖颜色单独表达能源状态。
18. 技术复杂度应跟随业务复杂度增长。

---

# 52. 最终评价

本方案追求的不是：

> 一个看起来非常“企业级”的 React 工程。

而是：

> 一个小到中型研发团队可以快速开始，并在三到五年后依然容易理解、修改、升级和扩展的智慧能源前端工程。

架构提前明确：

```text
Router
Query
Realtime
API Contract
Domain Components
Feature Boundaries
```

但具体抽象按需求增长：

```text
Query Key Factory
Zustand
Ring Buffer
Reference Counting
Runtime Config
Advanced Error Layer
Complex Permission
```

最后坚持一条原则：

> **标准已经解决的问题直接用标准，成熟库已经解决的问题直接用成熟库；研发投入应该集中在能源领域本身。**