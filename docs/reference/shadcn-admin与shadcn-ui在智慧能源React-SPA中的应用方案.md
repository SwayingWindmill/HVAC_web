# `satnaing/shadcn-admin` 与 `shadcn/ui` 在智慧能源 React SPA 中的应用方案

> 适用项目：智慧能源管理平台 React SPA  
> 前端基础：React 19 + Vite + TypeScript + TanStack Router + TanStack Query + shadcn/ui + ECharts  
> 核心原则：**shadcn/ui 是基础组件来源，shadcn-admin 是 UI Shell 和页面模式参考；业务架构由我们自己掌控。**

---

## 1. 两个项目在我们系统中的定位

我们不会把 `satnaing/shadcn-admin` 和 `shadcn/ui` 当成同一层东西。

它们承担不同职责：

```text
shadcn/ui
    │
    │ 基础 UI Primitive / Component Source
    ▼
components/ui
    │
    │ 组合
    ▼
我们的 Domain Components
    │
    │ 组成业务界面
    ▼
Device / Energy / Alarm / Storage / PV ...
```

而：

```text
satnaing/shadcn-admin
    │
    ├── Layout 参考
    ├── Sidebar 参考
    ├── Header 参考
    ├── Command Menu 参考
    ├── Theme 参考
    ├── Table 页面参考
    ├── Settings 页面参考
    └── Responsive / Accessibility 参考
```

因此最终关系是：

```text
                    shadcn/ui
                        │
                 基础组件源码
                        │
                        ▼
                  components/ui
                        │
           ┌────────────┴────────────┐
           │                         │
           ▼                         ▼
   satnaing UI Pattern          我们自己的设计
           │                         │
           └────────────┬────────────┘
                        ▼
                 Domain Components
                        │
                        ▼
                 Energy Features
```

一句话概括：

> **shadcn/ui 是我们的基础组件代码来源；satnaing/shadcn-admin 是高质量参考实现，而不是业务框架。**

---

## 2. 为什么不直接把 shadcn-admin 当完整框架

`satnaing/shadcn-admin` 很适合作为视觉和交互参考，它已经具备：

- Vite
- React
- TypeScript
- TanStack Router
- TanStack Query
- shadcn/ui
- Sidebar
- Command Menu
- Light / Dark
- Responsive
- Accessibility
- RTL
- Table
- 多种后台页面

它与我们的技术方向高度一致。

但它更像一套可复用 Dashboard UI 集合，而不是智慧能源业务框架。对我们的项目来说，仍然必须独立建设：

```text
OpenAPI Client
Auth
Permission
Telemetry
Realtime
Energy Domain
Alarm
Command
Asset
Time / Unit / Quality
```

因此不推荐：

```text
fork shadcn-admin
    ↓
保留全部 Demo
    ↓
在 Demo 上不断增加能源业务
```

几年后很容易变成：

```text
能源业务
+
模板残留页面
+
模板 Mock 数据
+
模板 Auth
+
模板 Store
+
我们的 API
+
我们的 Realtime
```

最终边界混乱。

---

## 3. 推荐采用方式

### 方案 A：推荐

从新的 Vite + shadcn 项目开始：

```text
Fresh Vite Project
        │
        ├── 官方 shadcn/ui
        ├── TanStack Router
        ├── TanStack Query
        └── ECharts
```

然后从 `satnaing/shadcn-admin` 选择性迁移：

```text
Layout
Sidebar
Header
Command Menu
Theme patterns
Responsive patterns
Page composition
Table UX
```

最终：

```text
我们的项目
│
├── 官方 shadcn 基础组件
├── satnaing 的优秀 UI Pattern
└── 我们自己的 Energy Domain
```

这是长期最干净的方案。

### 方案 B：快速启动

如果需要非常快地出第一版 UI，可以：

```text
clone shadcn-admin
        ↓
删除所有无关 Demo Feature
        ↓
保留 UI Shell
        ↓
重建 API/Auth/Query/Realtime
```

最终只留下：

```text
layout
theme
sidebar
command-menu
components/ui
少量通用组件
```

而删除：

```text
tasks
apps
chats
demo users
Clerk demos
mock dashboard
mock auth
无关 error demo
```

这种方式启动更快。

但完成初始化之后，应把仓库视为自己的产品代码，而不是长期持续 merge `satnaing/main`。

> UI Template 更新和业务系统更新节奏完全不同。

---

## 4. 项目初始化

如果采用推荐的方案 A，可以使用当前 shadcn CLI 初始化 Vite 项目，并选择 Radix primitive：

```bash
pnpm dlx shadcn@latest init -t vite -b radix
```

选择 Radix 的主要原因是 `satnaing/shadcn-admin` 当前大量基于 Radix/shadcn 组件模式，保持 primitive 一致可以降低迁移 Layout 和交互 Pattern 时的差异。

然后按需增加组件：

```bash
pnpm dlx shadcn@latest add button
pnpm dlx shadcn@latest add card
pnpm dlx shadcn@latest add dialog
pnpm dlx shadcn@latest add dropdown-menu
pnpm dlx shadcn@latest add sidebar
pnpm dlx shadcn@latest add command
pnpm dlx shadcn@latest add table
pnpm dlx shadcn@latest add select
pnpm dlx shadcn@latest add tabs
pnpm dlx shadcn@latest add tooltip
pnpm dlx shadcn@latest add sheet
pnpm dlx shadcn@latest add sonner
```

原则：

> **按需添加，不要一开始把 shadcn 全部组件安装进来。**

---

## 5. 不直接复制 satnaing 的 `components.json`

`satnaing/shadcn-admin` 当前有自己的：

```text
components.json
```

包括：

```text
style
rsc
tailwind css path
base color
CSS variables
aliases
icon library
```

这些配置值得参考，但不要直接复制整个文件。

正确流程：

```text
当前 shadcn CLI init
       ↓
生成当前版本 components.json
       ↓
确认 aliases
       ↓
确认 CSS variables
       ↓
迁移我们的 theme tokens
```

原因是 shadcn CLI、preset、primitive 和 schema 都在持续演进。

我们的原则是：

> **以当前官方 CLI 生成结果为基础，以 satnaing 的视觉实现为参考。**

---

## 6. shadcn/ui 的定位：源码，不是传统组件依赖

shadcn/ui 和传统的：

```text
npm install some-ui-library
```

思路不同。

运行：

```bash
pnpm dlx shadcn@latest add button
```

之后得到的是类似：

```text
src/components/ui/button.tsx
```

这份代码属于我们的项目。

因此：

```text
components/ui
```

可以理解为：

> **Vendorized UI Source Code**

意味着我们：

- 可以修改
- 可以审查
- 可以做无障碍调整
- 可以做样式调整
- 可以升级
- 不依赖黑盒 runtime UI package

但同时也意味着：

> 修改越多，未来同步官方组件更新越困难。

因此必须制定组件治理规则。

---

## 7. `components/ui` 的治理规则

把：

```text
src/components/ui/
```

定义为：

> **基础 UI Primitive 层。**

这里可以存在：

```text
button.tsx
dialog.tsx
table.tsx
sidebar.tsx
select.tsx
tabs.tsx
tooltip.tsx
sheet.tsx
command.tsx
```

但不允许出现：

```text
device-status.tsx
power-card.tsx
alarm-dialog.tsx
storage-soc.tsx
```

这些属于能源业务层。

边界：

```text
components/ui
    ↓
通用视觉行为

components/domain
    ↓
能源领域语义

features
    ↓
具体业务
```

---

## 8. 基础 UI 尽量少改

例如官方：

```text
Button
Dialog
Select
Table
Tabs
Sheet
```

原则上：

> 能通过组合解决，就不要修改 Primitive。

错误方式：

```text
修改 Button
加入 permission
加入 loading API
加入 alarm logic
加入 device logic
```

正确方式：

```tsx
<Can permission="command.execute">
  <Button>Execute</Button>
</Can>
```

或者：

```tsx
<CommandButton pending={pending}>
  Execute
</CommandButton>
```

`CommandButton` 属于：

```text
components/domain
```

而不是修改：

```text
components/ui/button.tsx
```

---

## 9. 什么时候可以修改 shadcn Primitive

以下情况可以修改。

### 9.1 项目级 Design Token

例如：

```text
radius
focus ring
spacing
font
```

### 9.2 Accessibility 修复

例如：

```text
ARIA
keyboard interaction
focus behavior
```

### 9.3 项目全局行为

例如所有 Dialog 的统一尺寸约定。

### 9.4 已验证的真实需求

必须确实是基础层的问题，而不是某个业务 Feature 的问题。

每次修改应满足：

```text
有明确原因
+
小范围修改
+
Git 可追踪
```

---

## 10. satnaing 已修改组件的处理

`satnaing/shadcn-admin` 自己已经标记了一些修改过的 shadcn 组件。

一般修改：

```text
scroll-area
sonner
separator
```

RTL 调整：

```text
alert-dialog
calendar
command
dialog
dropdown-menu
select
table
sheet
sidebar
switch
```

我们的处理不是：

```text
把这些文件全部复制过来
```

而是逐个判断。

例如初期不支持 RTL：

```text
RTL 修改
→ 不迁移
```

如果需要 satnaing Sidebar：

```text
官方 sidebar
+
迁移 satnaing AppSidebar composition
```

优先这样做。

只有当 satnaing 的底层修改确实解决了我们真实遇到的问题，再迁移 patch。

---

## 11. 真正从 shadcn-admin 复用什么

建议优先参考或迁移以下内容。

### Layout

包括：

```text
App Shell
Sidebar
Header
Main Content
Mobile Sidebar
Responsive layout
```

这是 satnaing 最值得借鉴的部分之一。

### Sidebar Composition

satnaing 已经把 Sidebar 数据抽成：

```text
navGroups
items
teams
user
```

我们的项目可以延续“配置驱动导航”的思想。

但业务数据替换为：

```text
Overview
Sites
Assets
Devices
Energy
PV
Storage
Charging
Alarms
Commands
Reports
System
```

例如：

```ts
export const navigation = [
  {
    title: 'Monitoring',
    items: [
      {
        title: 'Overview',
        to: '/dashboard',
        icon: LayoutDashboard,
        permission: 'dashboard.read',
      },
      {
        title: 'Devices',
        to: '/devices',
        icon: Cpu,
        permission: 'device.read',
      },
    ],
  },
]
```

这比页面里手写 Sidebar JSX 更适合我们的系统。

---

## 12. 不照搬 satnaing Sidebar Data

satnaing 的 Sidebar 中包含：

```text
Tasks
Apps
Chats
Users
Clerk
Demo Auth
Error Pages
Settings Demo
```

这些只是 Template 内容。

智慧能源系统应该重新设计信息架构。

推荐：

```text
Overview

Monitoring
├── Realtime
├── Sites
├── Assets
└── Devices

Energy
├── Overview
├── Consumption
├── Load
└── Cost

DER
├── PV
├── Storage
└── Charging

Operations
├── Alarms
├── Commands
└── Reports

System
├── Users
├── Roles
└── Settings
```

Sidebar 应该反映产品的信息架构，而不是模板页面列表。

---

## 13. Navigation 加入权限

在 satnaing 配置驱动导航的基础上增加：

```text
permission
```

例如：

```ts
{
  title: 'Commands',
  to: '/commands',
  icon: Terminal,
  permission: 'command.read',
}
```

然后：

```text
navigation config
      ↓
permission filter
      ↓
Sidebar
```

但注意：

> Sidebar 隐藏不是安全控制，Go Backend 仍然必须执行真正权限检查。

---

## 14. Command Menu 值得借鉴

satnaing 的全局 Command Menu 会直接读取导航数据。

这个模式非常适合智慧能源平台。

可以扩展为：

```text
Ctrl / Cmd + K
```

第一阶段搜索：

```text
页面
功能入口
```

以后再逐步增加：

```text
Search Device ESS-01
Search Site SG-01
Search Asset Transformer-02
```

不要一开始把 Command Menu 做成万能搜索平台。

---

## 15. Theme 可以复用思想，不复制全部 Provider

satnaing 已经有：

```text
ThemeProvider
FontProvider
DirectionProvider
```

对于我们的 V1：

```text
ThemeProvider
```

值得保留。

但：

```text
FontProvider
DirectionProvider
```

是否需要应该由实际需求决定。

如果：

```text
只支持一种字体
不支持 RTL
```

就不要为了保持模板一致而保留额外 Provider。

原则：

> Provider 只为真实全局能力存在。

---

## 16. shadcn Theme 采用 Semantic Tokens

使用 shadcn 推荐的 CSS Variables。

例如：

```text
background
foreground
card
popover
primary
secondary
muted
accent
destructive
border
ring
```

业务组件应该优先使用：

```text
bg-background
text-foreground
border-border
```

而不是在各页面硬编码：

```text
bg-white
text-gray-950
dark:bg-zinc-950
```

这样 Light / Dark Mode 的维护成本最低。

---

## 17. 能源状态颜色不要滥用 shadcn Base Tokens

能源领域会出现：

```text
ONLINE
OFFLINE

GOOD
UNCERTAIN
BAD
STALE

INFO
WARNING
CRITICAL

CHARGING
DISCHARGING
IDLE
```

不要试图全部塞进：

```text
primary
secondary
destructive
```

应该建立能源领域状态映射。

例如：

```ts
const qualityVariant = {
  GOOD: 'good',
  UNCERTAIN: 'warning',
  BAD: 'critical',
  STALE: 'muted',
}
```

然后：

```tsx
<TelemetryQuality quality="GOOD" />
```

这样形成：

```text
shadcn Theme
→ 产品基础视觉

Energy Domain Theme
→ 业务状态语义
```

两层不会混淆。

---

## 18. 建设自己的 Domain Components

在：

```text
src/components/domain/
```

第一批建议：

```text
metric-value.tsx
telemetry-quality.tsx
device-status.tsx
alarm-severity.tsx
command-status.tsx
site-selector.tsx
asset-selector.tsx
time-range-picker.tsx
realtime-connection.tsx
```

例如：

```tsx
<MetricValue
  value={823.4}
  unit="kW"
  quality="GOOD"
  timestamp={timestamp}
/>
```

这个组件内部组合：

```text
Typography
Badge
Tooltip
```

底层来自 shadcn。

形成：

```text
shadcn Primitive
       ↓
Energy Domain Component
       ↓
Feature
```

---

## 19. Table 的应用方式

shadcn 提供：

```text
Table Primitive
```

TanStack Table 提供：

```text
Table Behavior
```

我们组合成：

```text
DataTable
```

然后 Feature 自己定义：

```text
DeviceTable
AlarmTable
CommandTable
```

层级：

```text
shadcn Table
     ↓
Generic DataTable
     ↓
Device Table
```

不要建立一个有几十个 props 的：

```text
SuperAdminTable
```

---

## 20. 可以参考 satnaing 的 Table UX，而不是 Mock Data

satnaing 的 Users / Tasks 等 Feature 可以用于参考：

```text
Toolbar
Column actions
Filter
Pagination
Selection
Dialog
Responsive layout
```

但：

```text
mock users
mock tasks
demo schema
demo filters
```

不应该进入正式项目。

我们自己的 Device Table 数据来源应该是：

```text
URL Search Params
       ↓
TanStack Query
       ↓
Go API
```

而不是：

```text
import mockData
```

---

## 21. Form 的应用方式

基础组件：

```text
Input
Select
Checkbox
Switch
Textarea
Dialog
Form Field
```

来自 shadcn。

业务逻辑：

```text
React Hook Form
+
Zod
```

Feature：

```text
DeviceForm
AlarmRuleForm
TariffForm
```

例如：

```text
shadcn Input
      ↓
RHF Field
      ↓
DeviceForm
```

不要修改：

```text
components/ui/input.tsx
```

让它知道：

```text
deviceCode
assetId
alarmRule
```

---

## 22. Dialog / Sheet 使用约定

推荐：

```text
Dialog
→ 短表单 / 确认

Sheet
→ 详情 / 较复杂编辑

Full Page
→ 高复杂度 Workflow
```

例如：

```text
确认 Alarm Ack
→ Dialog

编辑 Device 基础属性
→ Sheet

ESS 策略配置
→ Full Page
```

避免所有业务都塞进 Modal。

---

## 23. Dashboard 不直接复制 satnaing

satnaing 的 Dashboard 可以参考：

```text
Card spacing
Grid
Header
Responsive
Chart container
```

但是能源 Dashboard 的信息结构必须重建。

例如：

```text
Current Demand
Today Energy
PV Generation
ESS SOC
Active Critical Alarms
Carbon / Cost
Load Curve
```

真正复用的是：

> Dashboard Layout Pattern

而不是 Dashboard 内容。

---

## 24. Chart 不沿用 Recharts 作为主方案

`satnaing/shadcn-admin` 当前使用 Recharts。

智慧能源系统主图表统一采用：

```text
Apache ECharts
```

因为我们更关注：

```text
长时间序列
多曲线
Zoom
大量数据点
能源分析
实时曲线
```

所以：

```text
satnaing Chart Card Layout
        ↓
可以参考

satnaing Recharts implementation
        ↓
不作为标准
```

自己的：

```text
components/charts/time-series-chart.tsx
```

内部使用 ECharts。

---

## 25. TanStack Router 可以沿用 satnaing 的方向

satnaing 当前已经使用：

```text
TanStack Router
+
generated route tree
```

这和我们的规划一致。

可以参考：

```text
Route organization
Layout route
Error route
Search Params
Router Provider
```

但是业务 Route 应重新组织为：

```text
_authenticated/
├── dashboard
├── devices
├── energy
├── alarms
└── ...
```

生成的：

```text
routeTree.gen.ts
```

属于 Router 生成文件，不要手动修改。

---

## 26. TanStack Query 只参考 Provider，不照搬全局策略

satnaing 当前在入口初始化：

```text
QueryClient
QueryCache
retry
global mutation error
401 handling
500 handling
```

这个结构值得参考：

```text
QueryClientProvider
+
Router Context
```

但具体策略不要直接复制。

尤其不建议全局：

```text
500
→ navigate /500
```

智慧能源 Dashboard 中一个 Widget 请求失败，不应该让整个系统跳到错误页。

我们的策略应该更细：

```text
401
→ Session/Auth Handling

403
→ Feature / Route Permission UX

5xx
→ Query / Page / Widget Error

Mutation Error
→ 就地反馈
```

因此：

> 复用初始化结构，不复制 Demo Error Policy。

---

## 27. 不直接使用 satnaing 的 Auth Store

satnaing 当前有：

```text
useAuthStore
```

以及 Clerk 示例。

我们的认证由实际部署模式决定。

推荐优先考虑：

```text
Go API
+
OIDC Provider
+
Secure HttpOnly Cookie
```

必要时才采用：

```text
SPA OAuth Client
+
Authorization Code + PKCE
```

因此：

```text
satnaing auth UI
→ 可以参考

satnaing auth state implementation
→ 不直接继承
```

---

## 28. 不直接继承 satnaing 的 Zustand 使用方式

`satnaing/shadcn-admin` 当前项目存在：

```text
src/stores
```

我们的项目不因为模板有 Store 就必须拥有 Store。

规则仍然是：

```text
Server State
→ TanStack Query

URL State
→ TanStack Router

Form State
→ RHF

Local UI
→ React

Shared Client State
→ Zustand（确有需求时）
```

模板技术选择不应该推翻我们的状态边界。

---

## 29. shadcn Component 添加流程

团队增加组件时统一：

```bash
pnpm dlx shadcn@latest add <component>
```

例如：

```bash
pnpm dlx shadcn@latest add calendar
```

添加之后：

```text
1. 查看新增依赖
2. 查看 components/ui 的 Git Diff
3. 运行 typecheck
4. 运行 lint
5. 运行相关测试
```

然后提交。

不要：

```text
CLI add
↓
不看 diff
↓
直接 merge
```

因为 shadcn 的设计就是组件源码进入我们的仓库。

---

## 30. shadcn 升级策略

### 未修改 Primitive

一般可以较放心通过当前 CLI 更新/重新添加，并审查 Diff。

### 已修改 Primitive

必须：

```text
官方新版本
      ↓
Git Diff
      ↓
手工 merge
      ↓
Test
```

尤其 satnaing 自己已经提醒：修改过的组件不能盲目被 CLI 覆盖。

我们的项目同样应该执行这个纪律。

---

## 31. 建立轻量修改记录

可以维护：

```text
docs/ui-overrides.md
```

例如：

```md
# UI Overrides

## sidebar.tsx

Reason:
- Support our collapsed navigation behavior.

Changed:
- Added compact icon alignment.

## table.tsx

Reason:
- None.
- Keep upstream compatible.
```

目的不是写文档 KPI，而是让半年后的开发者知道：

> 为什么这个 shadcn 文件和官方不一样。

---

## 32. V1 不建设内部 shadcn Registry

shadcn 当前已经支持：

```text
Custom Registry
GitHub Registry
Namespaced Registry
```

长期如果我们有多个产品：

```text
Energy Admin
Edge Admin
Operations Portal
Customer Portal
```

内部 Registry 会有价值。

例如：

```text
@energy/metric-value
@energy/device-status
@energy/time-range
@energy/data-table
```

但 V1 不需要。

只有当：

```text
第二个独立前端项目
```

真的需要复用这些 Domain Components 时，再建设内部 Registry。

> 一个项目里的 `components/domain` 不需要为了“平台化”提前变成 Registry。

---

## 33. 如果未来建设内部 Registry

推荐分成：

```text
@company/ui
@energy/domain
```

例如：

```text
@company/ui
├── page-header
├── app-shell
└── data-table

@energy/domain
├── metric-value
├── device-status
├── telemetry-quality
└── time-range-picker
```

不要把：

```text
DevicePage
AlarmPage
StoragePage
```

这种完整业务 Feature 随意变成公共组件。

共享稳定设计能力，不共享所有业务代码。

---

## 34. 推荐最终目录

结合 shadcn-admin 与已经确定的 SPA 骨架：

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
│   ├── ui/                # shadcn
│   ├── domain/            # Energy domain UI
│   ├── charts/            # ECharts wrappers
│   └── layout/            # satnaing-inspired shell
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
│   ├── units.ts
│   └── utils.ts
│
├── styles/
│   └── index.css
│
├── app.tsx
└── main.tsx
```

其中：

```text
components/ui
```

主要来自 shadcn。

```text
components/layout
```

大量借鉴 satnaing。

```text
components/domain
```

完全由我们的能源产品定义。

```text
features
```

完全由我们的业务定义。

---

## 35. 哪些 satnaing 文件值得优先研究

优先级最高：

```text
src/components/layout/
src/components/command-menu.tsx
src/context/theme-provider*
src/styles/
src/routes/
src/features/users/
```

研究重点不是机械复制文件，而是理解：

```text
组件怎么组合
布局怎么响应式
Sidebar 怎么配置驱动
Feature 怎么组织
Route 怎么保持薄
Table UX 怎么构建
```

---

## 36. 哪些 satnaing 内容建议删除或忽略

如果从其仓库快速启动，建议尽快删除：

```text
Clerk Demo
Tasks Demo
Apps Demo
Chats Demo
Mock Users
Mock Dashboard Data
Demo Auth Pages
无关 Error Demo
Brand Assets
示例 Team Data
```

以及和能源系统无关的业务 Store。

UI Shell 应该尽快从 Demo 中剥离。

---

## 37. 第一阶段应该复用到什么程度

第一阶段目标不是：

> “把 shadcn-admin 改成能源系统。”

而是：

> “借 shadcn-admin 快速得到一个成熟的 Application Shell。”

第一阶段完成：

```text
App Shell
Sidebar
Header
Mobile Navigation
Theme
Command Menu
Page Header
Table Layout
Dialog / Sheet pattern
```

然后停止迁移。

后续 Energy Feature 全部按照我们的工程规范开发。

---

## 38. 第一阶段页面

建议只做：

```text
/dashboard
/devices
/devices/:id
/alarms
/energy/overview
```

不要一开始迁移十几个 satnaing 页面。

因为页面越多，Demo Debt 越多。

---

## 39. Device 页面应用示例

最终 Device List：

```text
satnaing
→ App Layout / Toolbar Pattern

shadcn
→ Button / Input / Select / Table / Dropdown

TanStack Router
→ Search Params

TanStack Query
→ Device Server State

TanStack Table
→ Table Behavior

Go API
→ Device Data
```

形成：

```text
                 DeviceList
                     │
      ┌──────────────┼───────────────┐
      │              │               │
      ▼              ▼               ▼
 shadcn UI      TanStack Table   TanStack Query
      │                              │
      │                              ▼
      │                            Go API
      │
      ▼
 satnaing-inspired layout
```

这就是我们希望的依赖方式。

---

## 40. Device Detail 页面应用示例

```text
App Shell
    │
    ▼
Device Detail
    │
    ├── Device Header
    │     └── shadcn Card / Badge
    │
    ├── Current Metrics
    │     └── MetricValue
    │
    ├── Device Status
    │     └── DeviceStatus
    │
    ├── Historical Chart
    │     └── ECharts
    │
    └── Config
          └── RHF + shadcn Form
```

数据采用：

```text
REST Snapshot
+
WebSocket Stream
```

这是 satnaing 本身没有提供、必须由我们增加的能源系统能力。

---

## 41. Alarm 页面应用示例

```text
shadcn
├── Table
├── Badge
├── Dialog
├── Button
└── Dropdown

我们的 Domain
├── AlarmSeverity
├── AlarmStatus
└── AlarmAckDialog

TanStack
├── Router Filters
├── Query
└── Table

Go API
└── Alarm lifecycle
```

这样基础 UI 与 Alarm Domain 完全分离。

---

## 42. Dashboard 应用示例

参考 satnaing：

```text
Grid
Card
Spacing
Responsive
Header
```

自己实现：

```text
Current Demand
Energy Today
PV Today
ESS SOC
Critical Alarm Count
Load Curve
```

图表：

```text
ECharts
```

数据：

```text
TanStack Query
```

实时部分：

```text
WebSocket
```

不要让 Dashboard 直接依赖 satnaing Mock Data。

---

## 43. 项目 Theme 建议

V1 建议：

```text
Light
Dark
```

够了。

不要因为 shadcn 很容易换主题，就投入时间建设很多主题。

智慧能源产品的设计价值更多来自：

```text
信息密度
状态清晰度
图表可读性
告警可见性
实时可信度
```

而不是主题数量。

---

## 44. 色彩体系建议

shadcn 基础 Token：

```text
background
foreground
primary
secondary
muted
destructive
border
```

能源 Domain 再定义状态语义：

```text
status-online
status-offline

quality-good
quality-uncertain
quality-bad
quality-stale

alarm-info
alarm-warning
alarm-critical
```

但不要建立几十个全局 CSS Token。

只有真正跨业务复用的状态才进入 Domain Token。

---

## 45. Accessibility

satnaing 本身很重视：

```text
Responsive
Accessibility
```

这是值得继承的方向。

尤其能源平台不要只靠颜色表达状态。

例如 Critical 不应该只有“红色”，而应该同时有：

```text
图标
+
文本
+
颜色
```

同样适用于：

```text
Telemetry Quality
Device Status
Command Status
```

---

## 46. 对 shadcn 的核心使用原则

采用：

> **Composition over Modification**

即：

```text
shadcn Button
+
shadcn Tooltip
+
我们的 Permission
=
Domain Command Button
```

而不是：

```text
修改 Button.tsx
让 Button 自己处理 Permission
```

这样未来 shadcn 更新时成本最低。

---

## 47. 对 satnaing 的核心使用原则

采用：

> **Pattern over Dependency**

即学习和迁移它的模式，而不是让业务长期依赖它的 Demo 架构。

可以复制：

```text
App Sidebar composition
Page Header structure
Responsive shell
Command menu pattern
```

复制以后代码就是我们的。

不要等待模板作者决定我们的产品架构。

---

## 48. 版本升级策略

整个 UI 更新分三类。

### shadcn 官方组件

```text
CLI / Registry
   ↓
Git Diff
   ↓
Review
   ↓
Test
```

### satnaing UI Pattern

不做自动升级。

需要新能力时：

```text
查看 upstream 实现
   ↓
人工判断
   ↓
选择性 port
```

### 我们自己的 Domain Components

完全由项目自己维护：

```text
MetricValue
DeviceStatus
TelemetryQuality
AlarmSeverity
...
```

这是长期产品资产。

---

## 49. 不建议的做法

避免以下方式：

1. 整体 fork satnaing 后长期 merge upstream。
2. 直接复制 satnaing 的 `components.json`。
3. 一次性安装全部 shadcn 组件。
4. 随意修改 `components/ui`。
5. 把能源业务写进 shadcn Primitive。
6. 把 satnaing Demo Store 当正式业务架构。
7. 让 Mock Data 长期存在到生产阶段。
8. 同时维护 Recharts + ECharts 两套图表体系。
9. 为单个前端项目提前建设内部 Component Registry。

---

## 50. 推荐实施顺序

### Phase 1：UI Foundation

创建：

```text
Vite
React
shadcn
TanStack Router
TanStack Query
```

完成：

```text
components/ui
styles
theme
```

### Phase 2：迁移 App Shell

从 satnaing 参考或迁移：

```text
Sidebar
Header
Layout
Mobile Navigation
Command Menu
Theme
```

替换所有 Demo Navigation。

### Phase 3：Domain UI

创建：

```text
MetricValue
TelemetryQuality
DeviceStatus
AlarmSeverity
TimeRangePicker
```

### Phase 4：Device Vertical Slice

打通：

```text
Route
Search Params
OpenAPI
Query
Table
Form
Realtime Status
```

### Phase 5：Alarm Vertical Slice

打通：

```text
Filter
Table
Ack
Permission
Realtime Alarm
```

### Phase 6：Energy

增加：

```text
ECharts
Historical Telemetry
Time Range
Aggregation
```

---

## 51. 最终技术关系

```text
                     shadcn/ui
                         │
                         ▼
                 components/ui
                         │
          ┌──────────────┴──────────────┐
          │                             │
          ▼                             ▼
 satnaing-inspired layout       Energy domain UI
          │                             │
          └──────────────┬──────────────┘
                         ▼
                      Features
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
       Router          Query         Realtime
          │              │              │
          └──────────────┼──────────────┘
                         ▼
                       Go API
```

其中：

```text
shadcn/ui
```

解决基础交互组件。

```text
satnaing/shadcn-admin
```

解决成熟 Admin Application 的视觉和交互参考。

```text
我们的代码
```

解决智慧能源领域本身。

这三个职责必须始终保持分离。

---

## 52. 最终决策

### shadcn/ui

作为：

> **官方基础 UI Component Source。**

通过当前 CLI 按需增加组件。

### satnaing/shadcn-admin

作为：

> **UI Shell、布局和页面交互模式的参考实现。**

优先借鉴：

```text
Sidebar
Layout
Theme
Command Menu
Responsive Pattern
Table UX
```

但不把：

```text
Auth
Mock Data
Demo Feature
Store
Dashboard Business
```

当成正式业务基础设施。

### 智慧能源前端

自己建设：

```text
Asset
Device
Telemetry
Energy
Alarm
Command
PV
Storage
Charging
Report
```

以及真正具有产品价值的：

```text
MetricValue
TelemetryQuality
DeviceStatus
Energy Chart
Realtime UX
Alarm UX
Command UX
```

最终希望得到的不是：

> “一个改过的 shadcn-admin”。

而是：

> **一个使用 shadcn/ui 作为基础设计语言、吸收 shadcn-admin 成熟后台交互经验、但拥有独立智慧能源领域架构的长期产品。**

