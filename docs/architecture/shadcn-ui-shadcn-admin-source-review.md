# shadcn/ui + shadcn-admin 前端设计体系 Source Review

> 日期：2026-09-11
> 适用范围：`apps/hvac-web`
> 目的：为 `DESIGN.md` 与后续 UI 重构固定可信来源、采用边界和拒绝项。
> 当前方案来源：[`docs/reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md`](../reference/shadcn-admin与shadcn-ui在智慧能源React-SPA中的应用方案.md)。
>
> **Current authority note:** 本 Source Review 继续固定 shadcn/ui、Radix baseline、TanStack、Tailwind 和 shadcn-admin 上游能力/版本证据；shadcn-admin 只作为可访问 Sidebar、Command Search、DataTable、Theme/User Menu 等交互机制的研究样本，不是页面几何或产品视觉模板。当前 AppShell 与 Surface 构图由 `DESIGN.md` 和现行 Surface Specification 决定。

## 1. 已核实来源

### shadcn/ui

- 官方网站：`https://ui.shadcn.com/`
- 官方源码：`https://github.com/shadcn-ui/ui`
- 官方 CLI：`shadcn`
- 2026-09-11 核实时 npm 版本：`shadcn@4.21.0`
- 官方 Vite 安装路径使用 Tailwind CSS v4 与 `@tailwindcss/vite`。
- 官方定位不是传统 npm 组件库，而是“Open Code + Composition + Distribution”的组件代码体系：组件源码进入项目，由项目直接拥有。
- 2026-07 起，shadcn/ui 新项目默认 primitive 为 Base UI；Radix 仍完整支持。两者在 shadcn 组件层保持一致的组合接口。

### shadcn-admin

- 仓库：`https://github.com/satnaing/shadcn-admin`
- 稳定参考：`v2.2.1`
- tag commit：`0217f8c`
- License：MIT
- 技术形态：Vite、TypeScript、TanStack Router、shadcn/ui、Tailwind CSS。
- 可借鉴能力：Sidebar、Header、Main content shell、Command Search、响应式布局、主题、URL 同步 DataTable、批量操作、窄屏导航。
- 仓库 README 明确说明它不是 starter template，因此本项目不把它作为可直接 fork 的产品脚手架。

## 2. ADOPT

### 2.1 shadcn/ui 的 Open Code 模式

采用。`components/ui` 中的组件源码由项目拥有，不再通过第二层 wrapper 隐藏标准 primitive。

原则：

- 标准 Button、Dialog、Sheet、Tabs、Select、Dropdown Menu、Tooltip、Popover、Skeleton、Table、Sidebar 等直接使用 shadcn/ui 组件源码。
- 需要项目风格时直接修改本项目拥有的 primitive 或使用 class/variant 组合，不建立 `AppButton`、`BaseDialog`、`AntButtonAdapter` 之类无业务价值的中间层。
- 领域语义进入 `components/domain`，而不是污染 `components/ui`。

### 2.2 shadcn/ui Composition

采用。组件通过显式组合形成页面，而不是由大型“万能组件”用大量 props 驱动。

例如：

```text
Page
├── Header
├── Toolbar
├── Main
└── Optional Inspector
```

以及：

```text
Card
├── CardHeader
├── CardContent
└── CardFooter
```

只在 Card 真的是独立信息对象时使用 Card；section 不默认等于 Card。

### 2.3 shadcn-admin 的后台应用壳

采用其结构思想：

```text
AppShell
├── Sidebar
├── Header
│   ├── Breadcrumb / Context
│   ├── Global Search / Command
│   ├── Notifications
│   ├── Theme
│   └── User Menu
└── Main
```

采用其“固定壳 + feature 内容”的思路，不复制其具体业务页面。

### 2.4 URL 同步 DataTable

采用。分页、排序、搜索、筛选必须优先进入 TanStack Router Search Params；TanStack Table 只负责 table model，TanStack Query 负责 server data。

### 2.5 响应式后台布局

采用。Desktop 是主要工作台，但 Laptop / Tablet / Mobile Emergency View 都需要明确降级策略；窄屏不是简单把桌面所有 panel 纵向堆叠。

### 2.6 Tailwind CSS v4

采用。使用语义 CSS variables + Tailwind utilities，避免重新建立大型页面级 CSS 框架。

## 3. ADAPT

### 3.1 Primitive backend

shadcn-admin 当前稳定版以 Radix 为主要 primitive 来源；批准的项目应用方案也明确选择 Radix 以降低迁移 Layout 与交互模式时的差异。

本项目决定：

- 新 shadcn/ui 基座以 **Radix 生成结果**为目标。
- 业务代码只依赖 `@/components/ui/*`，不直接依赖 Radix 的业务 API。
- `shadcn-admin` 仅作为 composition/layout/reference；不要求其底层 primitive 与本项目一致。
- 不建设 Radix/Base UI 兼容层，也不同时维护两套 primitive。

### 3.2 shadcn-admin 的视觉密度

shadcn-admin 是通用后台参考；HVAC Web 是 Operate 产品，需要更高的信息密度、更克制的状态色、更稳定的扫描路径。

因此：

- 保留其 shell、sidebar、header、command、table、responsive 思路。
- KPI、遥测、设备、告警、控制等模块按 HVAC 业务密度重做。
- 不直接复制通用 SaaS Dashboard 的卡片数量和视觉层级。

### 3.3 shadcn/ui Card

Card 作为离散对象容器，而不是页面布局单位。大多数 section、toolbar、fact grid 可以直接使用 semantic HTML + border/background/spacing。

### 3.4 shadcn/ui Chart

shadcn/ui Chart 进入正式能力边界：普通应用级 Bar、Line/Area、Donut/Pie 与小型分类比较使用 shadcn Chart + Recharts，以直接复用其 tokens、responsive container 与 accessibility layer；高密度 HVAC 时序、多轴、dataZoom、brush、linked cursor、大数据量等工程分析继续使用 Apache ECharts。

两者按任务能力边界选择；同一 Feature 不为相同问题并行维护两套实现，也不引入第三套 chart abstraction。

## 4. REJECT

以下内容不进入目标架构：

- Ant Design / ProComponents 作为新 UI 权威。
- Ant Design Charts。
- Ant/shadcn 双栈长期共存。
- 为迁移保留的 Adapter / Compatibility Layer。
- `BaseCRUD`、`BaseFeature`、`BaseRepository`、`AbstractApiService`。
- 万能 Store / Event Bus。
- 100+ props 的 SuperTable / UniversalForm。
- 把 shadcn-admin 当作 starter 并整体 fork 其业务结构。
- 为了像模板而复制 Dashboard 卡片墙。
- 页面组件直接 `fetch()`。
- 将 Server State 镜像进 Zustand。

## 5. 对 HVAC Web 的最终落点

目标实现：

```text
React 19 + Vite + TypeScript
Tailwind CSS v4
shadcn/ui (Radix primitive baseline)
Lucide
TanStack Router
TanStack Query
TanStack Table v9
React Hook Form + Zod
shadcn Chart + Recharts（普通应用级图表）
Apache ECharts（工程分析）
date-fns
Current realtime transport behind RealtimeClient
Vitest + Playwright
```

设计来源优先级：

```text
1. 当前用户明确要求
2. PRODUCT.md 与真实业务/安全语义
3. 已批准业务参考图
4. 智慧能源 React SPA 架构基线
5. DESIGN.md
6. shadcn/ui 官方组件与 composition
7. shadcn-admin 稳定参考
8. Impeccable / frontend-design / web-design-guidelines
9. 现有实现
```

## 6. 实施约束

- 每个 surface 直接迁移到最终体系；完成后删除对应 Ant 实现。
- `components/ui` 只保存通用 primitives。
- `components/domain` 保存稳定能源语义。
- feature-specific 组件留在 `features/<feature>`。
- Route 保持薄。
- 目录按真实复杂度生长，不提前创建空层。
- 新抽象至少需要两个真实当前使用场景。
- 不做兼容性设计，不做无真实失败模式依据的防御性编程。
