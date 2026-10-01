# 智慧能源管理平台 React SPA 前端架构规划

## 1. 前端定位

智慧能源管理平台的前端，不应被简单理解为一个“后台管理模板”。

它实际上同时承担了三类应用的职责：

- 传统管理后台
- 能源数据分析平台
- 轻量级实时 HMI

典型页面包括：

```text
综合驾驶舱
园区 / 站点管理
资产管理
设备管理
实时监控
能源分析
负荷曲线
光伏分析
储能分析
充电设施
告警中心
控制操作
报表中心
用户权限
系统配置

```

因此前端架构既不能只围绕 CRUD 设计，也不能完全按照 SCADA/HMI 的方式设计。

目标应该是：

> 使用现代 Web 技术构建一个能够同时承载管理、分析和实时监控的 React SPA。

---

# 2. 为什么选择 React SPA

本项目采用：

```text
React
+
Vite
+
TypeScript

```

而不是 Next.js。

智慧能源管理平台绝大部分功能都位于认证之后，例如：

```text
/dashboard

/assets

/devices

/realtime

/energy

/alarms

/reports

/settings

```

这些页面没有明显的 SEO 需求。

系统真正需要的是：

```text
快速交互

长生命周期页面

实时数据更新

复杂图表

大量筛选条件

设备状态

历史数据分析

前后端独立部署

```

因此 SPA 的运行模型更加直接：

```text
Browser

   │

   ├── REST
   │
   └── WebSocket

   ↓

Go Backend

```

而不需要：

```text
Browser
   ↓
Next.js Server
   ↓
Go Backend

```

增加额外 Server Layer。

---

# 3. 技术栈

推荐技术栈：

| 领域技术         |                     |
| ------------ | ------------------- |
| Framework    | React 19            |
| Build Tool   | Vite                |
| Language     | TypeScript          |
| CSS          | Tailwind CSS        |
| UI           | shadcn/ui           |
| Router       | TanStack Router     |
| Server State | TanStack Query      |
| Table        | TanStack Table v9 + tablecn ledger grammar |
| Forms        | React Hook Form     |
| Validation   | Zod                 |
| Client State | Zustand             |
| Charts       | shadcn Chart/Recharts + Apache ECharts by capability |
| Date         | date-fns            |
| Realtime     | WebSocket           |
| Testing      | Vitest + Playwright |

整体：

```text
React
│
├── TanStack Router
│
├── TanStack Query
│
├── React Hook Form
│
├── TanStack Table v9
│
├── Zustand
│
├── shadcn Chart / Recharts
│
├── Apache ECharts
│
└── shadcn/ui + approved tablecn + ReUI/Kibo UI/Dice UI copy-and-own sources

```

这些技术解决不同类型的问题，不应互相替代。

---

# 4. 前端核心设计原则

整个前端需要坚持几个原则。

第一：

> Server State 不等于 Client State。

第二：

> URL 是应用状态的一部分。

第三：

> 实时数据和普通 API 数据不是同一种数据。

第四：

> Feature 是代码组织的主要边界。

第五：

> 页面组件不直接操作 HTTP。

第六：

> 图表不负责数据计算。

第七：

> 权限不能只控制菜单是否显示。

第八：

> 不创建“大一统 Store”。

这些原则比具体使用哪个组件库更加重要。

---

# 5. 总体架构

推荐：

```text
┌───────────────────────────────────────┐
│               React App               │
│                                       │
│  Router                               │
│      │                                │
│      ▼                                │
│  Feature Pages                        │
│      │                                │
│      ├──────────────┐                 │
│      ▼              ▼                 │
│ Query Layer     Realtime Layer        │
│      │              │                 │
│      ▼              ▼                 │
│ REST Client      WebSocket            │
│                                       │
│      │                                │
│      ├───── Domain Components         │
│      │                                │
│      └───── UI Components             │
│                                       │
└───────────────────────────────────────┘
             │              │
           REST        WebSocket
             │              │
             └──────┬───────┘
                    ▼
                 Go API

```

前端内部建议分成：

```text
Application

Domain Features

Infrastructure

Shared UI

```

四个逻辑层次。

---

# 6. 项目目录

建议采用 Feature-Oriented Architecture。

```text
src/

├── app/
│   ├── router/
│   ├── providers/
│   ├── layouts/
│   └── bootstrap/
│
├── features/
│   ├── dashboard/
│   ├── assets/
│   ├── devices/
│   ├── realtime/
│   ├── energy/
│   ├── pv/
│   ├── storage/
│   ├── charging/
│   ├── alarms/
│   ├── commands/
│   ├── reports/
│   ├── users/
│   └── settings/
│
├── components/
│   ├── ui/
│   ├── charts/
│   ├── data-table/
│   └── layout/
│
├── api/
│   ├── client/
│   └── generated/
│
├── realtime/
│
├── stores/
│
├── hooks/
│
├── lib/
│
├── schemas/
│
└── types/

```

避免：

```text
components/
pages/
hooks/
services/
utils/

```

逐渐变成几个上千文件的大目录。

---

# 7. Feature 内部结构

每个业务模块独立。

例如：

```text
features/devices/

├── api/
│   ├── queries.ts
│   ├── mutations.ts
│   └── keys.ts
│
├── components/
│   ├── device-table.tsx
│   ├── device-form.tsx
│   ├── device-status.tsx
│   └── device-detail.tsx
│
├── pages/
│   ├── device-list-page.tsx
│   └── device-detail-page.tsx
│
├── schemas/
│   └── device.ts
│
├── hooks/
│
└── types.ts

```

这样未来删除一个 feature 时：

```text
features/devices

```

基本就是完整业务边界。

---

# 8. Router 规划

使用：

```text
TanStack Router

```

建议 URL 结构：

```text
/dashboard

/sites
/sites/:siteId

/assets
/assets/:assetId

/devices
/devices/:deviceId

/realtime

/energy
/energy/overview
/energy/consumption
/energy/load
/energy/cost

/pv

/storage

/charging

/alarms

/commands

/reports

/system/users
/system/roles
/system/settings

```

Router 不只是页面导航工具。

还应该负责：

```text
URL Search State

Route Guard

Route Metadata

Page Title

Breadcrumb

Permission Metadata

```

---

# 9. URL State

对于管理系统：

```text
search
page
pageSize
sort
filters
timeRange
site

```

这些状态应该尽量放到 URL。

例如：

```text
/alarms
?page=2
&severity=critical
&status=active
&site=sg01

```

优势：

```text
浏览器返回正常

页面刷新状态不丢

链接可分享

多标签页行为自然

```

因此不要把：

```text
pagination

filter

search

```

全部放 Zustand。

---

# 10. Server State

统一使用：

```text
TanStack Query

```

所有来自后端 API 的数据，原则上都属于：

```text
Server State

```

例如：

```text
sites

assets

devices

users

alarms

energy summary

reports

tariffs

```

应该：

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

```text
Cache

Loading

Error

Refetch

Retry

Invalidation

Request Deduplication

```

不要再自研一套：

```text
loadingStore

requestCache

apiState

```

---

# 11. Query Key 规范

统一定义 Query Keys。

例如：

```text
['sites']

['site', siteId]

['devices', params]

['device', deviceId]

['telemetry', assetId, metric, range]

['alarms', filters]

```

推荐每个 feature 自己管理：

```text
deviceKeys.all

deviceKeys.lists()

deviceKeys.list(params)

deviceKeys.detail(id)

```

避免全项目出现大量手写：

```text
['device-list-v2']

['all-devices']

['devices-page']

```

最终无法正确 invalidate。

---

# 12. API Client

页面组件禁止直接：

```text
fetch('/api/...')

```

也尽量避免到处：

```text
axios.get(...)

```

推荐统一 API Client：

```text
api/
├── client.ts
├── error.ts
├── auth.ts
└── types.ts

```

负责：

```text
baseURL

Authorization

timeout

error normalization

request ID

401 handling

```

业务 API 再封装成：

```text
deviceApi.list()

deviceApi.get()

deviceApi.create()

deviceApi.update()

```

组件只认识业务函数。

---

# 13. OpenAPI

Go Backend 建议提供：

```text
OpenAPI

```

前端根据 OpenAPI 生成：

```text
request types

response types

client

```

至少生成类型。

不要让前端手动重复定义：

```ts
type Device = {
   ...
}

```

而 Go 又定义：

```go
type Device struct {
   ...
}

```

长期一定产生漂移。

推荐：

```text
Go API Schema
      ↓
OpenAPI
      ↓
TypeScript Generated Types

```

但是生成层只作为：

```text
Infrastructure

```

不要让整个页面直接依赖自动生成 client 的内部结构。

---

# 14. Form 架构

普通业务表单采用：

```text
React Hook Form
+
Zod

```

例如：

```text
新增设备

编辑资产

配置告警规则

修改费率

```

结构：

```text
Zod Schema
   ↓
Form
   ↓
Mutation
   ↓
invalidate Query

```

例如：

```text
DeviceSchema

DeviceForm

useCreateDevice()

useUpdateDevice()

```

表单 schema 尽可能放 feature 内。

---

# 15. Table 架构

大量业务页面都会依赖 Table：

```text
设备列表

资产列表

用户列表

告警列表

命令历史

通信状态

报表记录

```

统一维护一套 ordinary operational ledger grammar：

```text
Project DataTable
=
tablecn interaction/composition grammar
+
TanStack Table v9
+
shadcn Table primitives
```

ReUI、Kibo UI、Dice UI 作为 shadcn/ui 之上的 advanced application-component source layer 参与：ReUI 偏 workspace/composition 与高级 Data Grid，Kibo UI 偏 Gantt/Editor/Dropzone 等高功能组件，Dice UI 偏 Sortable/Kanban/Editable/Tour 等高级交互。普通 operational ledger 仍由 tablecn + TanStack Table v9 统一；重叠能力必须选一个项目实现，不能并行维护。

包含：

```text
pagination

sorting

filter

column visibility

row selection

loading

empty state

```

但是不要创建一个：

```text
SuperTable

```

有 100 个 props。

应该采用：

```text
基础 Table Primitive
+
Feature-specific columns

```

例如：

```text
DeviceTable

AlarmTable

CommandTable

```

共享行为，业务列独立。

---

# 16. Zustand 的使用边界

Zustand 只负责真正的客户端状态。

例如：

```text
sidebar collapsed

dashboard edit mode

selected map layer

temporary workspace

UI preference

```

不负责：

```text
devices

users

alarms

telemetry

energy summary

```

这些属于 Server State。

整个项目最好维持：

```text
少量小 Store

```

而不是：

```text
useAppStore()

```

管理整个系统。

---

# 17. 实时数据层

实时数据不能简单设计成：

```text
WebSocket message
   ↓
setState()

```

建议建设单独：

```text
Realtime Client

```

目录：

```text
realtime/

├── client.ts
├── protocol.ts
├── subscriptions.ts
├── reconnect.ts
└── hooks/

```

统一负责：

```text
connect

disconnect

reconnect

subscribe

unsubscribe

heartbeat

authentication

message routing

```

---

# 18. 实时订阅模型

用户打开：

```text
/storage/ESS-01

```

才订阅：

```text
ESS-01.power

ESS-01.soc

ESS-01.status

ESS-01.temperature

```

离开页面：

```text
unsubscribe

```

不要进入系统以后：

```text
subscribe *

```

订阅整个园区所有遥测。

---

# 19. WebSocket 连接数量

原则上：

> 一个 Browser Session 一个 WebSocket。

不要：

```text
Dashboard 一个

Alarm 一个

Device 一个

Chart 一个

```

建立多个连接。

在前端建立：

```text
RealtimeClient
       │
       ├── dashboard
       ├── alarms
       ├── device
       └── storage

```

内部 multiplex subscription。

---

# 20. 实时状态和 Query Cache

实时消息到达后有两种处理方式。

对于：

```text
device status

latest metric

```

可以更新：

```text
Query Cache

```

例如：

```text
WebSocket
   ↓
queryClient.setQueryData()

```

这样普通 REST 和实时数据仍共享一个状态模型。

但对于：

```text
高频曲线点

```

不建议每个数据点都修改巨大的 Query Cache。

可以使用专用：

```text
ring buffer

realtime series state

```

避免 Query Cache 高频重建。

---

# 21. 历史数据与实时数据分离

一张曲线建议分成：

```text
Historical Data

       +

Live Tail

```

例如：

```text
历史曲线
REST 查询最近 1h

实时尾部
WebSocket 每秒追加

```

形成：

```text
REST history
      │
      ▼
──────────────●
              ●
               ●  ← WebSocket

```

而不是 WebSocket 自己维护整整一天历史。

---

# 22. 图表能力分层

智慧能源平台固定两层图表能力边界：

```text
普通应用级图表
→ shadcn Chart + Recharts

高密度 HVAC / engineering analytics
→ Apache ECharts
```

shadcn Chart + Recharts 负责简单 Bar、Line/Area、Donut/Pie、小型分类比较和普通 Dashboard 图表，直接复用 shadcn chart tokens、responsive container 与 accessibility layer。

Apache ECharts 负责高密度时序、多 Y Axis、dataZoom、brush、linked cursor、大数据量与设备/过程工程分析。

禁止再引入 Chart.js、Highcharts 或第三套 chart abstraction，也禁止同一 Feature 为同一个问题并行维护 Recharts 与 ECharts 两种实现。

需要建立自己的：

```text
Energy Chart Layer

```

例如：

```text
TimeSeriesChart

EnergyBarChart

StackedEnergyChart

PowerCurveChart

SOCChart

LoadProfileChart

EnergyFlowChart

```

但是：

> 自己封装业务 Chart，不能重新封装整个 ECharts。

---

# 23. 时间序列 Chart

统一解决：

```text
zoom

tooltip

legend

loading

no-data

quality

unit

timezone

aggregation

```

每个页面不重复实现。

例如：

```tsx
<TimeSeriesChart
  series={series}
  unit="kW"
  timezone="Asia/Singapore"
/>

```

内部才使用 ECharts。

---

# 24. 不让图表负责业务计算

禁止：

```text
Browser 收到 100 万数据
        ↓
ECharts
        ↓
自己 aggregate

```

应该：

```text
Backend
  ↓
aggregation
  ↓
API
  ↓
1000~5000 points
  ↓
ECharts

```

浏览器只进行：

```text
presentation-level calculation

```

例如：

```text
tooltip

percentage

label

```

而不是大型能源计算。

---

# 25. 时间范围组件

能源系统使用频率极高。

建议统一：

```text
TimeRangePicker

```

支持：

```text
Last 15m

Last 1h

Today

Yesterday

Last 7d

This Month

Custom

```

URL：

```text
?from=
&to=

```

或者：

```text
?range=today

```

这是整个应用的基础组件，不要每个 Dashboard 自己做一套。

---

# 26. 时间和时区

能源系统必须明确：

```text
UTC Storage

Local Display

```

前端必须知道：

```text
Site Timezone

```

例如：

```text
Asia/Singapore

Asia/Shanghai

Europe/Berlin

```

不要直接使用用户浏览器：

```text
new Date()

```

隐式决定能源数据日期。

能源结算中：

```text
今天

昨天

峰谷时间

月度

```

应该按照：

> Site Timezone

而不是开发者电脑时区。

---

# 27. 单位体系

能源平台一定会遇到：

```text
W

kW

MW

Wh

kWh

MWh

V

A

Hz

%

°C

```

不要在页面中：

```tsx
{value} + ' kW'

```

到处拼字符串。

建立统一：

```text
unit formatter

```

例如：

```text
formatPower()

formatEnergy()

formatVoltage()

formatPercentage()

```

支持自动：

```text
980 W

→

0.98 kW

```

或者：

```text
1200000 W

→

1.2 MW

```

显示策略统一。

---

# 28. Data Quality UI

Telemetry 不只有：

```text
value

```

还有：

```text
quality
timestamp

```

UI 要表达：

```text
GOOD

STALE

BAD

UNCERTAIN

```

例如：

```text
120.5 kW

Updated 5 sec ago

```

如果 stale：

```text
120.5 kW

Last update 22 min ago
STALE

```

而不是继续像正常数据一样显示。

---

# 29. Device State

不要简单：

```text
online = true / false

```

可以统一 UI State：

```text
ONLINE

OFFLINE

DEGRADED

UNKNOWN

MAINTENANCE

```

然后：

```text
<DeviceStatus />

```

全平台统一表达。

---

# 30. Alarm UX

Alarm 页面不是普通 Table。

至少需要区分：

```text
Active

Acknowledged

Cleared

```

以及：

```text
Severity

Source

First Occurred

Last Occurred

Acknowledged By

```

对于 Critical Alarm：

```text
颜色
声音
弹窗

```

都需要谨慎。

原则：

> 告警应该突出异常，而不是让整个页面长期红色闪烁。

避免“报警疲劳”。

---

# 31. Command UX

设备控制必须与普通编辑明显区分。

例如：

```text
修改设备名称

和

关闭 PCS

```

不是同一种操作。

普通 CRUD：

```text
Save

```

控制操作：

```text
Control Dialog

```

需要显示：

```text
Asset

Current State

Requested Action

Parameters

Risk / Impact

```

然后：

```text
Confirm

```

控制发出后：

```text
Pending

Sent

Acknowledged

Succeeded / Failed

```

完整展示生命周期。

---

# 32. 权限系统

前端权限建议：

```text
Role
+
Permission
+
Resource Scope

```

UI 层拥有：

```text
<Can permission="alarm.ack">

```

或者：

```text
usePermission()

```

但是必须记住：

> 前端权限仅用于 UX。

真正授权必须发生在 Go Backend。

不能：

```text
button hidden
=
security

```

---

# 33. Navigation 权限

菜单根据：

```text
permission

```

动态显示。

例如：

```text
Energy Analyst

Dashboard
Energy
Report

看不到：

Device Control
System Settings

```

但路由本身仍要做 guard：

```text
/menu

+
route guard

+
backend auth

```

三层保持一致。

---

# 34. Layout

推荐标准布局：

```text
┌──────────────────────────────────────────┐
│ Top Header                               │
├───────────┬──────────────────────────────┤
│           │                              │
│ Sidebar   │ Main Content                 │
│           │                              │
│           │                              │
│           │                              │
└───────────┴──────────────────────────────┘

```

支持：

```text
desktop sidebar

collapsed sidebar

mobile drawer

```

不要为每个业务页面创建不同 Layout。

---

# 35. 页面结构

一个业务页面统一：

```text
PageHeader

Toolbar

Content

Feedback

```

例如：

```text
Devices

[Site ▼] [Status ▼] [Search...]     [+ Device]

────────────────────────────────────────────

Device Table

────────────────────────────────────────────

```

页面布局一致性比花哨动画更重要。

---

# 36. Dashboard

Dashboard 不应该成为：

```text
几十个 Widget 全塞首页

```

建议分级：

```text
Executive Dashboard

Site Dashboard

Asset Dashboard

Energy Analysis

```

首页只回答几个核心问题：

```text
现在能源使用如何？

设备是否正常？

有没有严重告警？

今天和昨天相比怎么样？

PV / ESS 当前状态如何？

```

不要试图把所有业务都展示在一个页面。

---

# 37. Responsive

智慧能源平台虽然主要使用 Desktop，但仍要支持：

```text
Laptop

Tablet

Mobile Emergency View

```

移动端主要目标：

```text
查看状态

查看告警

简单确认

快速设备查询

```

不是把复杂：

```text
30列能源表格

```

强行塞到手机里。

---

# 38. Error Handling

统一错误体系。

API Error：

```text
Unauthorized

Forbidden

Validation Error

Conflict

Server Error

Network Error

```

前端统一转成：

```text
AppError

```

页面不要到处：

```text
toast(error.message)

```

应该区分：

```text
Page Error

Form Error

Toast Error

Realtime Error

```

---

# 39. Loading UX

不要全平台只有一个：

```text
Spinner

```

不同情况：

```text
首次加载
→ Skeleton

小范围刷新
→ subtle indicator

Table
→ table loading

Chart
→ chart skeleton

Mutation
→ button pending

```

TanStack Query background refetch 时，不应该把整个 Dashboard 清空重画。

---

# 40. Offline 与断线

前端至少识别：

```text
API offline

WebSocket disconnected

Device offline

```

这三个完全不同。

例如：

```text
Platform connection lost

```

不能显示成：

```text
All devices offline

```

所以：

```text
Application Connectivity

和

Asset Connectivity

```

必须分开建模。

---

# 41. WebSocket 重连

统一策略：

```text
Connected

Connecting

Disconnected

Reconnecting

```

采用：

```text
exponential backoff

```

恢复连接后：

```text
重新认证

重新订阅

重新获取 snapshot

```

不能假设错过的数据会自动回来。

---

# 42. 数据 Snapshot

页面进入时：

```text
REST
 ↓
Current Snapshot

```

然后：

```text
WebSocket
 ↓
Incremental Updates

```

这比：

```text
等 WebSocket 慢慢把当前状态发完整

```

可靠很多。

模式：

```text
Snapshot + Stream

```

应该作为实时页面的基础模式。

---

# 43. 性能规划

重点关注：

```text
大量 Table Row

大量图表

高频数据

大 Dashboard

长期运行

```

不要过早：

```text
memo everywhere

useMemo everywhere

```

优先控制：

```text
数据量

render boundary

subscription scope

```

最大的性能优化通常不是 React API，而是：

> 不要把不需要的数据发到 Browser。

---

# 44. Table Virtualization

普通：

```text
20

50

100

```

行分页不需要 virtualization。

只有真正出现：

```text
数千行本地数据

```

再增加 virtual table。

不要为了“高性能”让所有 Table 从第一天增加复杂度。

---

# 45. Code Splitting

按照 Route / Feature 进行。

例如：

```text
/storage

/charging

/reports

```

分别 lazy load。

不要一次加载：

```text
ECharts

Calendar

Map

Editor

全部 Feature

```

首屏需要的 JS 应尽可能少。

---

# 46. Web Worker

V1 默认不使用。

只有：

```text
非常重的本地计算

大型 CSV parsing

复杂数据转换

```

明显阻塞 UI 时再增加。

能源计算原则上尽量后端完成。

---

# 47. 测试策略

测试分三层。

## Unit

Vitest：

```text
formatter

schema

utility

domain helper

```

不要追求：

```text
100% coverage

```

---

## Component

重点测试：

```text
Alarm interaction

Command Dialog

Complex Form

Permission UI

```

---

## E2E

Playwright 覆盖关键业务流：

```text
Login

Device CRUD

Alarm Ack

Historical Query

Command Execution

Permission Restriction

```

E2E 数量不需要特别大，但必须覆盖：

> 高价值路径。

---

# 48. Mock

开发阶段可以使用：

```text
MSW

```

模拟 API。

这样：

```text
Frontend

```

和：

```text
Backend

```

可以并行开发。

但是 mock response 应基于同一套：

```text
OpenAPI Types

```

避免 Mock 和真实后端完全不同。

---

# 49. Storybook

V1 可选。

如果团队：

```text
多人开发

有专职设计

有大量 Domain Components

```

Storybook 很有价值。

如果只有：

```text
2~3 个前端

```

则不是上线前必须项。

不要为了“Design System”而先花一个月搭 Storybook。

---

# 50. Design System 边界

直接使用：

```text
shadcn/ui

```

作为基础。

自己建设的应该是：

```text
能源 Domain Component

```

例如：

```text
PowerValue

EnergyValue

MetricValue

DeviceStatus

AlarmSeverity

TelemetryQuality

AssetSelector

SiteSelector

TimeRangePicker

TimeSeriesChart

```

不要重新自己做：

```text
Button

Dialog

Dropdown

Tabs

```

这就是“不重复造轮子”。

---

# 51. 国际化

如果平台未来可能走海外项目，建议从 V1 避免：

```tsx
<div>设备状态</div>

```

直接散落。

可以早期就引入成熟 i18n library。

但如果已经明确：

```text
未来几年只有中文

```

不必为了国际化做复杂 Translation Management Platform。

至少保证：

```text
Date

Number

Unit

Timezone

```

没有写死中国场景。

---

# 52. Theme

支持：

```text
Light

Dark

```

即可。

能源大屏可能额外有：

```text
Dark Dashboard

```

但是：

> 不建议 V1 做 10 套颜色 Theme。

这类 Theme 对真正能源业务价值很低。

---

# 53. Map

如果项目存在：

```text
多个园区

站点 GIS

充电站

光伏站

```

建议独立：

```text
Map Feature

```

不要让 Map library 成为整个应用的基础依赖。

需要时再引入：

```text
MapLibre / Mapbox 等

```

按业务和授权要求选择。

---

# 54. 能源拓扑

如果需要：

```text
一次接线图

能源流

配电拓扑

```

不要直接尝试做：

```text
通用 SCADA Graphics Engine

```

V1 可以：

```text
SVG
+
React

```

或者成熟 graph library。

等真正需要：

```text
在线编辑

复杂拓扑

大量动态图元

```

再考虑专门图形引擎。

---

# 55. 导出

Export 应该主要由 Backend 负责。

例如：

```text
大量 Excel

CSV

PDF Report

```

不要浏览器下载 500 万条记录再：

```text
xlsx.generate()

```

前端：

```text
提交 Export Job
       ↓
查看 Progress
       ↓
下载 File

```

更合理。

少量当前 Table 导出可以直接客户端完成。

---

# 56. 大文件上传

例如：

```text
设备导入

配置导入

```

采用：

```text
Upload
→ Backend Validation
→ Preview
→ Confirm

```

而不是：

```text
Excel
→ Browser parse
→ 直接创建几千设备

```

批处理规则留给 Backend。

---

# 57. Audit UI

重要操作应允许查看：

```text
Who

When

What

Before

After

```

例如：

```text
Alarm Rule Changed

Device Config Changed

Command Executed

Permission Changed

```

Audit 是 Backend 的事实数据。

前端只负责展示。

---

# 58. 不应该自研的前端能力

明确不做：

```text
自研 UI library

自研 Router

自研 Query Cache

自研 Form Engine

自研 Chart Engine

自研 Table Engine

自研 Date Library

自研 WebSocket Protocol Framework

```

使用成熟生态。

我们真正值得开发的是：

```text
能源 UX

能源图表

设备页面

告警体验

实时监控

控制体验

能源分析

```

---

# 59. 第一阶段

第一阶段目标：

> 把应用骨架做正确。

完成：

```text
Vite

React

Router

Query

shadcn

Auth

Layout

API Client

Error Handling

Permission

WebSocket Client

```

以及基础组件：

```text
DataTable

PageHeader

MetricValue

DeviceStatus

TimeRangePicker

TimeSeriesChart

```

---

# 60. 第二阶段

建设能源核心 Feature：

```text
Dashboard

Asset

Device

Realtime

Energy

Alarm

```

重点打通：

```text
REST Snapshot
+
WebSocket Stream
+
Historical Query

```

三种数据模式。

---

# 61. 第三阶段

增加：

```text
PV

ESS

Charging

Command

Reports

Tariff

Audit

```

形成完整能源管理能力。

---

# 62. 后续阶段

真正遇到业务需求再增加：

```text
GIS

Topology Editor

Digital Twin Visualization

Advanced Analytics

AI Assistant

Prediction

Optimization

```

这些不应阻碍 V1 上线。

---

# 63. 最终前端架构

最终收敛：

```text
                    Browser

                       │
                       ▼

                React + Vite

                       │

        ┌──────────────┼───────────────┐
        │              │               │
        ▼              ▼               ▼

 TanStack Router  TanStack Query    WebSocket

        │              │               │
        │              │               │
        └──────────────┼───────────────┘
                       │

                   Features

                       │

      ┌────────────────┼────────────────┐
      │                │                │
      ▼                ▼                ▼

   shadcn          DataTable          ECharts

      │                                 │
      └─────────────── UI ──────────────┘

                       │

                    Go API

```

状态体系：

```text
URL State
→ TanStack Router

Server State
→ TanStack Query

Form State
→ React Hook Form

Client UI State
→ Zustand

Realtime State
→ Realtime Layer

```

这五类状态必须保持边界清晰。

---

# 64. 最终评价

智慧能源平台前端不需要追求最复杂的 React 架构。

真正重要的是：

```text
业务边界清晰

状态边界清晰

实时链路清晰

时间语义清晰

权限边界清晰

数据质量可见

历史与实时分离

后端负责计算

前端负责交互

```

技术上保持：

```text
React
Vite
TanStack
shadcn
ECharts
RHF
Zod
Zustand

```

已经足够支撑一个相当大型的能源平台。

不要因为未来可能有：

```text
几十万设备

```

就提前把 Browser 做成一个复杂分布式系统。

前端最重要的性能原则始终是：

> **只获取当前页面真正需要的数据，只订阅当前用户真正正在看的实时数据。**

最终我们希望得到的不是：

> “一个用了很多新技术的 React 项目”。

而是：

> **一个研发团队在三到五年以后依然敢修改、敢升级、敢增加新业务的智慧能源前端工程。**