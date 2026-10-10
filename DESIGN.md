---
version: shadcn-app-v1
name: 泉来禾智慧能源平台
designStatus: selected
selectedDirection: shadcn-application
scope: apps/hvac-web
directionDecision: docs/design-system/shadcn-redesign-2026-09-13.md
architecture: docs/architecture/smart-energy-react-spa-frontend-architecture.md
---

# 泉来禾智慧能源平台 DESIGN.md

## 1. 当前设计方向

2026-09-13 起，全站视觉与页面布局采用 **Shadcn Application System**。

2026-10-10 用户再次确认：采用企业能源 SaaS 的标准做法，坚持 shadcn 风格，高级感来自完成度而不是标新立异；不引入行业主题化视觉（如能效标识、线网图）。完成度标杆是 shadcn 官方 Blocks（dashboard-01 等）与 Vercel 控制台：

- 字体自托管 Geist / Geist Mono（`@fontsource-variable`），中文回退 PingFang SC / Microsoft YaHei；不依赖 Google Fonts 等境外字体服务。
- 浅色为默认场景（办公室桌面 1440–1920），页面底为白色、侧栏为浅灰，inset 布局；页眉为 48px 实底，不用毛玻璃。
- 图表系列色为可区分的五色（蓝、青、紫、品红、石板灰），避开状态色相（青绿信息、琥珀警告、红色危险）；状态色只表达状态。
- 每个路由页面使用 `@/blocks/page-header` 的 `PageHeader` 与 `@/components/layout/Main` 容器；页面内不得再出现 `<main>`。
- 用户菜单位于侧栏底部（dashboard-01 `NavUser` 模式），页眉只放侧栏开关、站点切换、面包屑、搜索、主题与通知。

主要视觉参考：

- `satnaing/shadcn-admin`：应用壳、Sidebar、Header、Command Search、页面密度、后台产品布局与响应式处理。
- 当前 `shadcn/ui`：组件语法、Card / Tabs / Table / Command / Sheet / Dialog / Form 等交互与视觉基线。
- `sadmann7/tablecn`：scan-heavy Data Table / ledger 的筛选、排序、分页、列控制与工具栏 grammar；本项目以 TanStack Table v9-native 实现吸收其 pattern，不维护第二套表格体系。
- `ReUI`、`Kibo UI`、`Dice UI`：shadcn/ui 之上的复杂应用组件与组合来源。ReUI 偏 Frame / advanced Filters / Timeline / Kanban / Gantt / Event Calendar / Tree/Cascader；Kibo UI 偏 Gantt / Calendar / Editor / Dropzone 等功能型组件；Dice UI 偏 Sortable / Kanban / Editable / Selection Toolbar / Tour / Tags Input 等高级交互。它们是 copy-and-own source，不形成第二套 primitive framework。
- `dashboardcn`：聚焦 Dashboard / analytics 的 copy-and-own source，优先用于真实 KPI、Sparkline、普通趋势图、Heatmap、Gauge、Sankey、Timeline 等数据展示组合；不得取代 tablecn operational ledger，也不得替代高密度 HVAC 工程分析中的 ECharts。
- `Shadcnblocks`：应用级 Block / 页面组合候选来源，优先用于 Dashboard、Application Shell、Chart Group、复杂内容区等已经成熟的 shadcn 组合；不得取代官方 shadcn primitive，也不得绕过 tablecn 的 operational ledger 责任。只采用当前账号合法可访问、经过 source review 的条目。

这次是直接重设计，不对上一版 Control Desk、旧 Ant Design / ProComponents 页面或历史截图做视觉兼容。

旧 Control Desk、Ant/ProComponents 设计决策和历史截图包不再保留为设计资料；需要追溯历史时使用 Git 历史，不在当前工作树中保留可误用的视觉入口。

业务事实、安全边界和状态语义仍由 `PRODUCT.md` 决定。

## 2. 权威顺序

发生冲突时按以下顺序裁决：

1. 当前用户明确要求。
2. `PRODUCT.md` 的业务事实、控制安全、数据真实性与工作流。
3. `docs/product/smart-energy-system-page-architecture-v3-research-backed.md` 与 `docs/product/global-navigation-context-interaction-contract-v2.md` 的 Workspace Catalog、Surface placement、页面职责、跨页闭环、capability gating、Inspector / Detail Route 边界，以及“易用性 × 专业性”规则。
4. `docs/product/global-navigation-context-interaction-contract-v1.md` 的导航投影、URL/Search Params 状态所有权、跨页 Context、Inspector/Detail/Dialog/Tab 边界和可访问性交互规则。
5. 匹配的 `docs/product/surface-specifications/*.md` 页面级 Surface Specification；这些 Spec 必须基于 v2 蓝图和 Interaction Contract 生成。v1、旧 `docs/product/surfaces/*.md` 和更早 IA 不再是当前权威。
6. `docs/design-system/shadcn-redesign-2026-09-13.md`。
7. 本 `DESIGN.md`。
8. `docs/design-system/hvac-application-ui-specification.md` 的应用级组件层次、Page Header、Surface archetype、tablecn 与 ReUI / Kibo UI / Dice UI 边界及 UI Definition of Done。
9. `docs/design-system/shadcn-component-contract.md` 的组件选择与组合规则。
10. `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md` 的 tablecn / ReUI 上游采用边界与兼容性证据。
11. `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md` 的 Kibo UI / Dice UI 复杂组件采用边界与 source-first 规则。
12. `docs/architecture/dashboardcn-source-review-2026-09-24.md` 的 Dashboard / analytics 组件采用边界。
13. `docs/architecture/shadcnblocks-source-review-2026-09-22.md` 的 Shadcnblocks Block 采用、许可与 CLI 边界。
14. `docs/design-system/legacy-ui-quarantine.md` 的历史 UI 隔离规则。
15. 当前 shadcn/ui、satnaing/shadcn-admin、sadmann7/tablecn、ReUI、Kibo UI、Dice UI、dashboardcn 与 Shadcnblocks 的成熟模式。
16. 前端架构与状态所有权边界，以及 TanStack / Recharts / ECharts / X6 / G6 的真实能力边界。
17. Impeccable / frontend-design / web-design-guidelines 的审查方法。

**当前实现和历史参考图不在视觉权威链中。** 现有实现、旧菜单、旧路由、旧 Ant/ProComponents 页面、旧截图包、历史组件画廊和被 `legacy-ui-quarantine.md` 隔离的资料没有设计上的既得权。当前 36→10 Workspace 收敛有一个已经明确批准的例外：本轮 36 Surface 体系中已经完成并通过 PROMOTED / BROWSER REVIEWED 验收的 shadcn Surface（以 `surface-specifications` 和对应 visual-reframe 记录为准）是新 Workspace 的设计母体，必须继承其页面 archetype、Table/Detail/Inspector 结构、信息层级和状态语义，再叠加后期统一的 tablecn / DataTableBlock / AppShell 规范。更早 Ant / Control Desk 资产仍然只提取业务事实，必须丢弃其视觉结构。

## 3. 技术基线

```text
React 19
Vite
TypeScript
Tailwind CSS v4
shadcn/ui through components/ui
Lucide
TanStack Router
TanStack Query
TanStack Table
React Hook Form
Zod
Recharts through shadcn Chart for ordinary application charts
Apache ECharts
AntV X6 where fixed engineering topology is required
AntV G6 where dynamic relationship graph is required
WebSocket / realtime layer
```

规则：

- shadcn/ui 是 primitive authority；业务代码优先依赖项目自己的 `components/ui`。tablecn 是专业 Data Table / ledger source，ReUI / Kibo UI / Dice UI 是复杂应用组件 source，dashboardcn 是聚焦数据展示与普通 analytics composition 的 source，Shadcnblocks 是应用级 Block / composed-pattern source；全部采用 copy-and-own，不形成第二套 primitive framework。
- 本项目当前 shadcn registry 配置由 `apps/hvac-web/components.json` 决定；当前基线是 `radix-nova`。
- 当前生产项目继续使用 Radix base，不因为 shadcn 对“新项目默认值”的调整而混入 Base UI API；Radix 组合使用 `asChild`，不使用 Base UI `render`。
- ReUI / Dice UI 如同时提供 Radix / Base UI 实现，当前项目只允许采用 Radix flavor；Kibo UI 组件采用前必须核实其实际 primitive/headless dependencies，不能间接混入与当前 `radix-nova` 冲突的 Base UI composition。
- 新增或修改的 Radix primitive 内部统一使用 `radix-ui` 包；Feature 不直接依赖 Radix primitive。
- 应用级页面组合、Surface archetype、Page Header、tablecn 与复杂组件层（ReUI / Kibo UI / Dice UI）选型边界由 `docs/design-system/hvac-application-ui-specification.md` 固定；组件选择、Data Table、Field、Dialog / AlertDialog / Sheet 的职责边界由 `docs/design-system/shadcn-component-contract.md` 固定。
- Lucide 是产品 UI 图标库；图标优先用于导航、明确动作和需要快速扫描的状态，不为普通指标、每一行数据或每个 Card 添加装饰性 SVG。
- 不建立 Ant Design 兼容层。
- 不建立旧 Control Desk 视觉兼容层。
- Ant Design、ProComponents、Ant Design Charts 只允许作为尚未迁移 Surface 的临时历史依赖；最后一个 caller 迁移后删除。

## 4. shadcn/ui 的角色

shadcn/ui 是源码级组件系统，不是黑盒依赖。

优先采用：

- Button
- Badge
- Card / CardHeader / CardTitle / CardDescription / CardAction / CardContent / CardFooter
- Field / FieldLabel / FieldDescription / FieldError / FieldGroup / FieldSet
- Input / Input Group / Textarea / Select / Combobox
- Checkbox / Radio / Switch
- Tabs
- Table primitives + TanStack Table v9
- Command
- Dropdown Menu
- Popover / Tooltip
- Dialog / AlertDialog
- Sheet
- Skeleton
- Separator
- Scroll Area
- Resizable，仅真实需要时

原则：

- 默认先用 Tailwind + `components/ui` 表达页面，不为每个 Surface 新建大型专用视觉框架。
- **Correctness first, source-first, then reuse.** 现有项目实现没有默认保留权。新增或重做任何通用交互、布局、导航、状态、表单、数据展示或图表组合时，先由 Surface Spec、成熟产品实践、任务语义与可访问性确定“正确的交互模型”，再同时审查 `existing project / shadcn primitive & official blocks / tablecn / approved domain component / ReUI / Kibo UI / Dice UI / dashboardcn / Shadcnblocks / other reviewed mature OSS` 等候选 source，按业务语义、a11y/keyboard、依赖重量、Radix 兼容性、源码质量和维护状态做 ADOPT / ADAPT / REPLACE / REJECT。现有组件如果只是历史遗留、职责错误或弱于成熟 source，应直接替换或删除；不得因为“项目里已经有”而固化错误。只有成熟 source 都无法正确表达任务时，才允许新增 project-owned composition 或 custom component。
- **官方 shadcn Blocks 是应用级组合参考，不只是组件 API 参考。** Overview / Dashboard / Workspace 类页面优先研究当前官方 `dashboard-*`、`sidebar-*`、Data Table、Chart block 的完整构图，再根据能源业务改写；不得只换成 shadcn 组件却保留旧页面骨架。
- **项目内正式区分 Component 与 Block。** `components/ui` 是 shadcn primitive，`components/data-table` 是可复用 DataTable 能力层；`blocks` 是应用级标准组合，只负责稳定的布局与交互 grammar，不吞业务数据、查询、columns 或 feature state。页面级表格的当前标准 Block 是 `@/blocks/data-table/DataTableBlock`，工单页作为 canonical reference。
- 标准 Route Surface 允许并推荐 `Breadcrumb → Page Header → Title / Description / Local Actions → Surface Content`。Breadcrumb 表达层级，Page Header 的 `h1` 表达当前 Surface；全局 App Header 不再重复同一大标题，正文第一张 Card/Section 也不得再次用 route title 充当 section title。
- `Sidebar variant="inset"` 必须与 `SidebarInset` 成套使用；collapsed / mobile / inset spacing 应沿用 shadcn Sidebar 的 data-attribute composition，而不是业务 CSS 另造壳层。
- Card 是有完整语义的 section，不是所有内容的默认外壳；指标卡优先采用 `CardDescription → CardTitle → CardAction`，业务 section 优先采用 `CardTitle + CardDescription + CardAction`，避免自建标题栏。
- 设备、告警、工单等 scan-heavy Surface 默认使用 **tablecn interaction/composition grammar + 项目 TanStack Table v9-native DataTable + shadcn Table primitive**；Feature 自己定义 columns / filters / selection / server state，不建立吞掉所有差异的 mega DataTable。ReUI / Tablecn Data Grid 只在 spreadsheet-style editing、重 virtualization、tree/grid、cell editing 等真正不同的高级 grid 任务中，经 source/API review 后采用。
- **DataTable 只代表页面级数据工作台，不再提供 embedded surface。** DataTable 自己拥有唯一 border/radius，并且必须放在 `DataTableBlock` 中；不得把 DataTable 塞进 Card、Drawer、Sheet、`rounded border` wrapper 或另一层 workspace surface。对象详情、Drawer/Sheet、Dashboard 卡片内部若只是少量只读字段对照，直接使用 shadcn `Table`；用于选择对象的 master-detail 列表使用 shadcn `ItemGroup / Item`；时间顺序事件优先 Timeline；不要为了“统一”把所有结构都继续塞进 DataTable。
- **页面级表格默认一律采用 tablecn / 工单页式 standalone grammar，而不是 Card。** 只要一个表格承担当前页面、Tab 或 workspace 的主要扫描任务，或自身带 Search / Sort / Filter / Columns / Pagination，就按 **DataTableBlock → DataTable（toolbar 作为 DataTable child）→ pagination** 组织，由 DataTable 自己提供唯一 border/radius。Dashboard 中的“证据表”“排行表”“审计表”“资源表”也不因为位于 Dashboard 就自动获得 Card 外壳。小型只读 `Table` 可以存在于 Card/Sheet/Drawer 中，但父 surface 与 Table 之间不得再添加第二层 Card、圆角边框容器或“Table Card”；一个局部信息单元只能有一个完整外框。主台账默认继承共享 DataTable 的 `text-sm` 行密度与 `text-xs` 表头，不允许 Feature 无业务理由整体降成 `text-xs`。
- **Surface 07 保留窄范围 Card/Table 继承例外；Surface 06 已回归标准 DataTableBlock。** 设备列表保留已验收 06 的业务列、概况卡和高密度扫描语义，但主台账必须使用单层 `DataTableBlock → DataTable`，不得再额外套 Card 或重复显示“设备运行台账”标题。07「设备详情」仍保留 `realtime metric Cards + trend Card + parameter/nameplate Cards` 页面骨架，因此仅 07 的参数表允许 Card 承载 DataTable。例外必须在源码用 `@surface-card-table-exception 07` 显式声明，并由 `design:check` 仅对白名单文件放行；不得推广到其它 Workspace。
- **表格只保留一套筛选模型。** 状态、等级、类别、区域、来源、协议、优先级等行级条件统一进入 `Filter`（tablecn advanced filter grammar），不得再在 Filter 上方并列一排 “全部 / 某状态 / 某类别” pills、segmented buttons 或 Tabs 作为第二套筛选入口。`Sort` 负责排序，`Filter` 负责行级条件，`View` 专指列可见性/列显示配置，三者语义不得混用。
- `Tabs` 只用于真正不同的内容视图或数据域，例如 `连接器 / DLQ`、`站点台账 / 电价方案 / 运行日历`、`SEU / 影响变量 / 历史版本`；如果切换后仍是同一批行，只是按 status/type/tier 等字段缩小结果集，就属于 Filter，不得使用 Tabs。
- 图表只用于存在权威 series / category data 的业务事实；没有可信时间序列时不为贴近模板制造趋势。**按数据关系择图，而不是按页面模板固定图型**：完整且类别较少的 part-to-whole 用 Donut/Pie；类别超过约 5 个时优先聚合为 `Top N + 其他`，或改用 horizontal Bar；类别比较、排名和精确差异优先 Bar；时间序列用 Line/Area；相关性/分布才用 Scatter。不完整 composition 不得用饼/环图伪装成完整整体。
- 普通应用级图表优先使用 **shadcn Chart + Recharts v3**，直接复用 shadcn chart tokens、responsive container 与 accessibility layer；高密度 HVAC 时序、多轴、dataZoom、brush、linked cursor、大数据量等工程分析能力使用 Apache ECharts。两者按能力边界选择，不在 Feature 内自造第三套 chart abstraction。
- 等宽 KPI / metric strip 只有在指标确实同级、需要快速横向扫描时才使用；范围、主 KPI、异常状态、过程证据等不同语义不得为了整齐强行等权联排，应按业务对象分组并建立视觉主次。过程量优先按工程对象（如冷冻水、冷却水、冷机、泵组）组织，不把不同含义的参数机械切成 5～6 个等宽单元。
- **形式复用优先，但不能制造错误的可访问性或交互语义。** 如果某个 shadcn 组件在视觉形式上完全适合业务，可以优先复用；只有当其内建 ARIA role、键盘模型、状态机或交互预期会把业务含义表达错误时，才不直接使用该 primitive，而是复用其 visual grammar 或选择更中性的 shadcn 组合。例如 category share 可以复用 Progress 的视觉形式；若直接暴露 `progressbar` 会把“占比”误读成“任务完成度”，则隐藏该视觉 primitive 的辅助语义并由相邻文字完整表达事实，或直接选择更合适的 Chart composition。
- Field 是表单 label / help / error 的统一组合语法；带 icon、结果数、unit、inline action 的搜索/输入使用 Input Group。
- Tabs 用于同一任务的平级视图，不用作跨业务模块导航。
- Dialog 用于短时模态任务；AlertDialog 用于明确的高后果确认；**对象详情统一使用 shadcn Sheet，不再使用会压缩主工作区的 desktop Inspector / split pane。**
- **全站详情交互固定为 `Ledger/List → Sheet quick detail → durable detail Route`。** Desktop 使用右侧 non-modal Sheet、无整页 overlay、不得导致主内容 reflow；窄屏使用同内容的 modal Sheet + overlay。Sheet 只保留快速判断所需事实与少量上下文动作；多 section/tab、大证据集、长表单、附件、工程点位、持续调查或跨会话任务进入 durable Route。
- 分析画布中的证据栏、拓扑摘要、趋势游标详情若本身是当前分析任务的一部分，可以 inline；但“选中一个对象后出现的详情”不得伪装成分析侧栏，统一 Sheet。

## 5. shadcn-admin / tablecn / advanced application component layer 的角色

### 5.1 shadcn-admin

shadcn-admin 是当前主要应用布局与后台产品参考。

深度参考：

- neutral application shell；
- light Sidebar in light mode；
- product/team/site switcher；
- compact grouped navigation；
- compact Header；
- Command Search；
- Theme / notification / user controls；
- Main content density；
- Data Table layout；
- responsive shell behavior；
- Breadcrumb / Page Header / local action 与 content grid 的比例。

不复制其示例业务数据、示例路由命名、示例 Dashboard 指标或不匹配 HVAC 业务的工作流。

### 5.2 tablecn

tablecn 是 scan-heavy operational ledger 的默认交互与 composition 参考。

采用：

- filter / sort / pagination / visibility / selection grammar；
- table toolbar 与 dense border-first presentation；
- 可分享状态与 server-side table workflow 的成熟 pattern。

项目仍使用自己的 TanStack Table v9-native DataTable；上游 tablecn 当前 v9 兼容性必须按 `docs/architecture/shadcn-tablecn-reui-source-review-2026-09-20.md` 处理，不能直接复制未核验实现。

### 5.3 ReUI

ReUI 是复杂应用组件层的一部分。

优先考虑：

- Frame；
- advanced Filters；
- Timeline / Kanban / Gantt / Event Calendar；
- Tree / Cascader / Sortable / Stepper；
- base shadcn blocks 不足以覆盖的成熟 composed application pattern。

规则：

- primitive-specific 组件只采用 Radix flavor；
- 普通 table/ledger 不绕过 tablecn 项目层；
- ReUI Data Grid 只用于 spreadsheet、重 virtualization、tree/grid 等真正不同的高级 grid 任务，并且必须先核实选定源码的 TanStack Table v9 API；
- 不复制 ReUI 示例业务，不把 ReUI 页面模板当产品 IA。

### 5.4 Kibo UI

Kibo UI 是复杂应用组件层的功能型 component / block source，重点减少高功能组件的重复实现。

优先考虑：

- Gantt / scheduling；
- Calendar；
- Editor / rich text；
- Dropzone / file workflow；
- 复杂内容展示与 application blocks；
- 其他明确建立在 shadcn token / CSS-variable 体系之上的成熟功能组件。

规则：

- Kibo Table 不取代项目 tablecn operational ledger；
- 引入前检查实际 headless library / primitive dependencies；
- 不直接把 Kibo Dashboard/block 当 HVAC Surface 模板；
- 只引入当前任务真正需要的组件，不整套搬入。

### 5.5 Dice UI

Dice UI 是复杂应用组件层的高级交互 source。

优先考虑：

- Sortable / drag-and-drop；
- Kanban；
- Editable；
- Selection Toolbar / Action Bar；
- Tour；
- Tags Input / Mention / Listbox；
- File Upload / Media / Cropper；
- 其他 shadcn/ui 未覆盖的 accessibility-heavy interaction。

规则：

- 当前项目只采用 Dice UI 的 Radix source path；
- 不通过 Dice UI 重新引入一套 Dialog / Select / Button primitive family；
- 与 ReUI/Kibo 重叠时按 task semantics、a11y/keyboard、dependency weight、Radix compatibility 与 source quality 选一个实现。

### 5.6 dashboardcn

dashboardcn 是聚焦 Dashboard / analytics 的数据展示与组合 source，不是新的页面模板体系。

优先考虑：

- authoritative KPI / Metric Value / Sparkline；
- 普通 Trend / Bar / Composed Chart；
- Heatmap；
- 有真实范围/阈值语义的 Gauge / Segmented Meter；
- 有真实流量或分配关系的 Sankey；
- 告警、控制、验证、审计等时间顺序证据的 Timeline。

规则：

- dashboardcn Data Table 不取代 tablecn + TanStack Table v9 operational ledger；
- Recharts/shadcn Chart 只承担普通应用级分析，高密度、多轴、dataZoom/brush 等 HVAC 工程分析继续使用 ECharts；
- KPI 的“上升/下降好坏”、单位、精度和 locale 必须按 HVAC 业务语义适配，不继承示例默认值；
- 不把页面做成无业务依据的“几张 KPI Card + 几张图”模板；采用前遵守 `docs/architecture/dashboardcn-source-review-2026-09-24.md`。

### 5.7 复杂组件层共同规则

`ReUI + Kibo UI + Dice UI` 是同一层的候选 source，不是三个必须同时存在的 runtime framework；dashboardcn 是相邻的 analytics composition source。

同一 capability 只保留一个项目实现。高级交互类 production adoption 必须遵守 `docs/architecture/kibo-diceui-advanced-components-source-review-2026-09-20.md`；analytics composition 必须遵守 `docs/architecture/dashboardcn-source-review-2026-09-24.md` 与既有 source-first 规则，记录精确 tag/commit、源码、测试与 ADOPT/ADAPT/REJECT 结论。

我们学习和拥有的是成熟 Pattern / Source，而不是并行维护多个视觉世界。

## 6. 全局视觉语言

### 6.1 性格

目标是现代、克制、精密、可信的智慧能源 Web 应用。

不是：

- 大屏/HMI 风格后台；
- 永久深色工业控制台；
- 装饰性渐变与阴影堆叠；
- 大量彩色 KPI 卡；
- 每个区块都用大圆角 Card；
- AI 生成感很强的“模块拼贴”。

### 6.2 颜色

全站采用 **Zinc 极简中性外壳 + 官方 shadcn chart 精准色彩调色板** 双层策略：

1. **框架中性色（Zinc 主题）**：
Light mode 与 Dark mode 严格对标 shadcn 经典 Zinc 调色体系，外壳保持克制、安静、高对比度，为内容与数据让出视觉焦点。

Light mode：
```text
background        #FAFAFA
foreground        #18181B
card              #FFFFFF
secondary/muted   #F4F4F5
muted text        #71717A
border            #E4E4E7
primary           #18181B
ring              #A1A1AA
```

Dark mode：
```text
background        #09090B
card/sidebar      #18181B
secondary/muted   #27272A
foreground        #FAFAFA
muted text        #A1A1AA
border            #27272A
primary           #FAFAFA
```

2. **图表官方配色规范（Chart Color Palette - shadcn 经典科技蓝体系）**：
数据可视化图表严格采用 shadcn 官方图表规范（`https://ui.shadcn.com/charts/bar#charts`）展示的经典科技蓝体系，具有层级鲜明、沉稳科技、高明度梯度的 5 阶蓝色系色谱，杜绝随手指定纯色或无序色盘：

```css
/* Light Mode 图表变量（对标 shadcn 官方图表示例色谱） */
--chart-1: #2b7fff;  /* Vibrant Azure / 鲜亮科技蓝 - 实际运行负荷主曲线 / 核心用能分项 */
--chart-2: #8ec5ff;  /* Sky Blue / 浅天蓝 - 能耗基准对比虚线 / 次级分项 */
--chart-3: #155dfc;  /* Royal Blue / 皇家蓝 - 关键动力设备负荷 */
--chart-4: #1447e6;  /* Cobalt Blue / 深钴蓝 - 输配管网分项 */
--chart-5: #193cb8;  /* Deep Navy / 沉稳藏青 - 其他末端辅助分项 */
```
Dark Mode 映射至深色背景下的高辨识度蓝色阶（`#3b82f6`、`#93c5fd`、`#60a5fa`、`#2563eb`、`#1d4ed8`）。所有业务图表（AreaChart、Donut、Bar 等）一律通过 `var(--chart-1)` ~ `var(--chart-5)` 或 `--color-<name>` 访问，严禁在业务组件内随意硬编码孤立色值。

3. **业务语义色**：
业务语义色独立且仅用于真实业务状态：
- success：正常/已完成/满足；
- warning：需要关注；
- destructive：明确严重异常或危险动作；
- information：信息性业务状态；
- unknown：未知、不完整、不可用。

语义色只用于真实业务状态，不作为页面品牌大面积铺色。

### 6.3 Typography

优先使用系统中文字体栈。

建议层级：

- Page title：24px / 600；
- Section title：14px / 600；
- Card title：14px / 600；
- Body：13–14px；
- Label / supporting text：11–12px；
- Summary metric：22–28px / 600；
- Table：12–13px；
- 变化数值使用 `tabular-nums`。

不把 uppercase English eyebrow 当全站视觉语言。

### 6.4 产品语言 / 专业术语

面向中文用户的业务界面默认 **中文优先**。专业性通过正确的业务语义、单位、证据和工程上下文体现，不通过大面积英文体现。

规则：

- 页面标题、区块标题、按钮、筛选器、状态、说明、空状态和下一步动作默认使用中文；
- 行业通用缩写和单位可保留，例如 `COP`、`kW/RT`、`EnPI`、`EnB`、`SEU`、`PM2.5`；
- 首次或关键位置使用“中文业务名称 + 缩写”，例如“能源绩效指标（EnPI）”“能源基线（EnB）”“重大用能（SEU）”；
- 英文标准原文、模型字段名、算法名和底层 technical enum 只进入工程详情、方法说明、帮助文本或审计信息；
- 不允许为了显得专业，把中文可清楚表达的 `Load / Boundary / Expected / Valid / Contributor / Evidence` 等直接作为主界面文案；
- 对确无稳定中文译法的标准术语，保留英文并给出简短中文解释；
- 同一页面术语必须一致，不在“冷站 / Plant”“负荷 / Load”“有效 / VALID”之间随机切换。

原则：

> **中文负责可理解性，标准缩写负责专业精度；用户不应先翻译界面，再理解业务。**

### 6.5 用户可理解性 / Content Design

页面视觉和内容必须共同服务用户完成当前任务，而不是展示系统拥有多少字段。

默认视图优先回答：

```text
我现在看的是谁 / 什么范围？
当前最重要的事实是什么？
为什么值得注意？
这个结论值得信吗？
下一步应该做什么？
```

规则：

- 区块标题优先使用用户问题和业务名称，例如“需要关注”“当前运行”“已验证结果”“下一步”，而不是 `Projection / Read Model / Payload`；
- 异常、失败、不可用状态必须尽量同时提供“事实 + 原因 + 下一步”，不能只显示一个红色 Badge；
- 空状态必须区分“当前没有事项”和“数据/服务不可用”；
- 默认层只展示完成主要任务需要的信息，内部 ID、raw enum、trace/correlation、schema/model hash 进入 Advanced / Audit；
- 设计原则、实现理由和数据建模约束不得直接写进业务界面，例如“一个事实一个 owner”“不做推断”“这些状态必须独立”“不合并成健康分”等只属于规范、代码和审计说明；
- `owner / projection / read model / authority / schema / transport / payload` 等内部架构词默认禁止出现在普通用户文案中；只有面向专业工程/审计任务且该术语本身就是业务对象时例外；
- 缺失能力或缺失数据在界面上只表达用户需要知道的结果和可采取动作，例如“运行模式暂不可用”，不要解释“因为某 owner 尚未提供”；
- Primary Action 使用具体动词 + 对象，例如“创建工单”“查看能源分析”“释放临时覆盖”，避免“处理 / 操作 / 确定 / 执行”这类含糊按钮；
- 产品文案遵循 `docs/product/content-design.md`。标题优先写用户正在处理的业务对象，而不是 UI 容器名称；不要因为内容采用 Table 就写成“XX 台账 / XX 列表 / XX 明细 / XX 工作台”。
- **灰色 supporting copy 默认不出现。** 只有时间范围、数据范围、方法/标准、当前状态、重要限制或异常条件等会改变用户理解的信息才保留；“查看 / 管理 / 支持 / 用于……”这类解释可见 UI 功能的说明默认删除。
- Page title 已经准确命名当前对象时，正文主表允许无二级标题，直接进入 Search / Filter / Sort / Columns / Table，禁止“设备 → 设备台账”“工单 → 工单台账”这类语义重复。
- KPI label 使用最短且不丢失含义的业务名称，例如“当前功率 / 系统 COP / 在线率 / 最大需量 / 可调负荷”，避免报告式长标题和行政化名词堆叠。
- 状态使用文字 + 图标/形状/标签表达，颜色只作为冗余编码；
- 实时刷新不能抢 focus、重置筛选/分页、关闭用户正在操作的浮层或让列表持续跳动；
- 默认 Ledger 在目标桌面宽度下必须无需横向滚动即可完成核心判断，低频列进入 Column Settings / Detail；
- hover 不作为获取关键业务信息的唯一方式；
- 图表必须存在标题、单位、时间范围、数据质量语义，并提供可访问的文字或表格替代；
- Wireframe 与实现以 WCAG 2.2 AA 方向作为 accessibility baseline。

易用性不能通过合并专业事实获得。仍必须保持：

```text
Offline ≠ Fault
Stale ≠ Offline
ACK ≠ Cleared
Expected ≠ Verified
Approved ≠ Executed
Delivered ≠ Read
```

> **用户友好 = 更快理解正确事实并知道下一步，而不是把复杂业务压成一个模糊分数。**

### 6.6 Spacing / radius / elevation

- 主要间距：4 / 8 / 12 / 16 / 24 / 32。
- 普通页面遵循 shadcn-admin 的紧凑主区：默认约 `px-4 py-6`，需要更宽呼吸感时 desktop 可提升到 `px-6`；不再把固定 24px 当所有页面的硬规则。
- compact control：28–32px 高。
- Card 默认 radius 跟随 shadcn 当前组件，不再为业务页面任意放大。
- 默认无装饰性大阴影；边框和层级优先。

## 7. Application Shell

```text
ApplicationShell
├── Sidebar
│   ├── Product + Site switcher
│   ├── Primary navigation
│   ├── Grouped navigation
│   └── Collapse
├── Header
│   ├── Current route identity
│   ├── Realtime status
│   ├── Command Search
│   ├── Theme
│   ├── Notifications
│   └── Account
└── Main
    └── Route-owned Surface
```

### Sidebar

- expanded：约 240px；
- collapsed：约 48px；
- Light theme 使用浅色 Sidebar；Dark theme 跟随 dark token；
- Site selector 放在 Sidebar 顶部 product/site switcher；
- active item 使用轻量背景，不使用永久高饱和色块或左侧工业指示条；
- group label 安静、紧凑；
- 不把设备树、楼层树等业务 scope 塞进全局 Sidebar。

### Header

- 高度约 56px；
- sticky；
- 只保留全局能力；
- page-specific KPI、天气、站点大标题不进入 Header；
- Search 使用 Command；
- Theme、Notification、Account 保持紧凑。

### Main

- 普通页面：默认 `px-4 py-6`，desktop 按信息密度可用 `px-6`；
- 常规 Surface 由页面自身决定 max-width，通常 1400–1600px；
- 工程 Canvas 可用更宽 fluid content；
- 页面决定自己的纵向滚动，不建立旧固定工作台兼容规则。

## 8. 页面通用语法

### Breadcrumb + Page Header（页面头部标准语法）

标准 Route Surface 采用：

```text
Breadcrumb

Left: Page Title (h1 24px semibold)       Right: Local Actions
      Description / scope / freshness            Secondary status when useful
```

Breadcrumb 与 Page Header 是不同职责，因此可以同时存在：

- **Breadcrumb**：表达导航层级与当前对象路径；
- **Page Header / H1**：表达当前 Surface 的唯一主标题；
- **Supporting line**：只在能补充 scope、freshness、任务说明时存在；
- **Actions**：只放当前 Surface 的关键本地动作。

#### 1. 规范构图（Canonical Page Header Layout）
- Breadcrumb 紧凑置于 H1 上方，不放入 Card；
- `h1` 页面主标题使用 `text-xl font-semibold tracking-tight sm:text-2xl text-foreground`；
- supporting line 使用 `text-sm text-muted-foreground` 或更紧凑的 `text-xs` 元信息；
- 右侧动作优先使用紧凑 Button / Dropdown / period control，不堆满状态徽章；
- Page Header 保持扁平语义结构：统一使用 `@/blocks/page-header` 的 `PageHeader`（`header` + `h1` + supporting line + actions，无 Card、边框或背景），不建设万能 boxed `PageHeader` component。

#### 2. 严禁的反模式（Anti-patterns & Lessons Learned）
- **禁止 route title 向下重复**：Page Header 已表达“工单中心”时，第一张 Card/Section 应写“待处理工作”“SLA 风险”等真实 section task，而不是再次写“工单中心”；
- **禁止 App Header 再做第二个大标题**：全局 Header 负责全局能力，不与 route-owned H1 竞争；
- **严禁左侧空洞失衡**：不能使用单纯的 `justify-end` 将所有元素推至右侧；
- **严禁过度胶囊化**：只读站点、时区、更新时间优先使用纯文本，不伪装成控件。

### Summary Cards (KPI Stat Cards 标准语法与经验教训)

Summary Card（如 4 联顶层 KPI 卡片）是用户进入系统第一眼建立全局感知的关键层，必须严格遵守 `satnaing/shadcn-admin` 官方成熟形态与克制原则：

#### 1. 规范的三行式结构（Canonical 3-Row Anatomy）
每个标准 Stat Card 容器高度严格一致，由 3 行构成：
- **行 1 (CardHeader)**：
  - `CardTitle` 使用 `text-sm font-medium text-muted-foreground`；
  - 右侧仅放置极简轻量裸图标（`size-4 text-muted-foreground`），与标题在同一水平基线上；
  - 严禁为图标外包灰色底座（如 `grid size-8 rounded-md bg-muted`），保持通透无遮挡。
- **行 2 (Hero Value)**：
  - 核心数值使用 `text-2xl font-bold tracking-tight tabular-nums`；
  - 辅助单位（如 `kW`、`COP`、`项`、`%`）统一使用紧凑的 `ml-1 text-sm font-normal text-muted-foreground`，严禁单位与数值脱节，严禁将 `%` 连在粗体主数值内破坏整排卡片的统一字重律动。
- **行 3 (Subtitle / Supporting Context)**：
  - 辅助事实与基准使用 `text-xs text-muted-foreground`；
  - 单行展现，多事实间使用中置点 ` · ` 优雅分隔（例如：`环比提升 +6.1% · 综合节能率 8.6%` 或 `在线可用 125 / 175 台 · 状态未知 25 台`）；
  - 必须保持单行高度与基线对齐，严禁内部折行造成相邻卡片高度脱节。

#### 2. 严禁的反模式（Anti-patterns & Lessons Learned）
- **禁止卡片内做微型仪表盘（Mini-Dashboard Inception）**：卡片宽度通常仅 200~300px，绝不能在卡片内部塞入粗进度条、纵向两行小标题、断行多列统计标签（如“25 离线 25 延迟 25 未知”）。复杂多维数据应下沉至下一级的分析图表或矩阵表格。
- **禁止视觉重量失衡与碎片化**：单张卡片内若既有数值、又有 Badge、又有进度条、又有两行小字，会使得整排 4 张卡片严重变形割裂（如早期迭代中第 3 张卡片在 200px 内塞入 6 项元素造成严重崩坏）。
- **禁止大面积高饱和彩色卡片**：卡片背景一律采用干净的白底（Dark mode 下为深灰），禁止使用大色块底色；色彩只允许出现在图表与明确的业务告警 Badge 上。
- **只展示权威事实**：不伪造趋势，不展示无依据的平滑曲线或衍生虚假比例；严格保证 4 联卡片在任何桌面视口下高度对称、基线严格统一。

### Ledger / Data Table

设备、告警、工单优先使用：

```text
Breadcrumb + Page Header
Search + Filters + View/column controls
Data Table
Pagination / server result state
```

Table 负责快速扫描、排序、比较和批量选择。卡片视图只有真实使用价值时才保留。

### Analysis

```text
Breadcrumb + Page Header + period/context controls
Primary quantitative chart
Supporting comparison/evidence Cards
Drill-down table
```

量化图表按能力边界收敛：普通应用级 Pie/Donut、Bar、基础 Line/Area 优先使用 shadcn Chart + Recharts；高密度时序、多轴、dataZoom、brush、linked cursor 与大数据工程分析使用 Apache ECharts。图型必须由数据关系和用户判断任务决定，不以“页面模板里有什么图”为依据；禁止同一 Feature 为相同问题并行维护多套图表实现。

### Detail

- Row → durable Route：复杂、可分享、可恢复的持续调查；
- Row → Quick Preview：高频连续比较，只显示做“是否继续调查”判断所需的少量事实；
- 设备类 Ledger 的桌面 Quick Preview 默认保持主表完整宽度，采用 non-modal overlay；窄屏使用 modal Sheet；
- Sheet primitive 可以承载 Quick Preview，但产品层仍称 Quick Preview / 快速查看，不把完整详情塞进侧栏；
- Dialog：确认或小表单。

不做 `Card → Drawer → Splitter → Full Route` 的固定兼容路径，也不为了展示 Quick Preview 给 Ledger 增加可拖动 splitter。

## 9. Dashboard → Shadcn Overview

Dashboard 不是旧 Control Desk 的固定 Operational Home 构图。

当前 baseline：

```text
Breadcrumb + Page Header + actions
4 concise metric Cards
Run posture / device evidence           Priority handling
Optimization opportunity                Data confidence         Continue workflows
```

Dashboard 必须回答：

- 当前有什么值得注意；
- 核心运行事实是什么；
- 哪些告警/设备事项优先；
- 数据是否足够可信；
- 下一步进入哪个工作流。

禁止 opaque health score。

## 10. HVAC / Realtime → Shadcn Engineering Workspace

HVAC 是工程工作区，但周边 UI 服从 shadcn 语法。

```text
Breadcrumb + Page Header + local actions
Compact summary Cards
Line Tabs: topology / anomaly / energy evidence
Workspace Card
├── X6 engineering topology, or
├── measured energy evidence
└── selected object → right-side Detail Sheet
Supporting Evidence Cards
```

规则：

- X6 只负责真实固定工程拓扑；
- anomaly 保持空间记忆；
- energy flow 不伪造流量/分配；
- 设备/对象选择后的详情使用 Sheet 展示业务事实，不泄漏 raw enum / UUID / trace；
- 旧 Control Desk CSS 不作为视觉权威。

## 11. Device Center → Data Table First

默认视图是 Ledger / TanStack Data Table。

```text
Breadcrumb + Page Header
Search / filters / column controls
Device Data Table
Right-side Detail Sheet (no ledger reflow)
```

主要列优先：

- 设备名称与类型；
- 位置；
- 运行；
- 连接；
- 遥测新鲜度 / 质量；
- 关键当前值；
- 告警；
- 最近更新时间；
- actions。

Card wall 不再作为默认。

## 12. Alarm Center → Triage Data Table

```text
Breadcrumb + Page Header
Small severity/queue summary
Filters
Alarm Data Table
Investigation detail / route
```

用户必须快速判断：严重度、对象、时间、状态、当前证据、责任和下一动作。

## 13. Work Orders

```text
Breadcrumb + Page Header
Compact queue summary
Filters
Work Order Data Table
Durable detail surface
```

显式展示 Owner、Priority、SLA、Source、Progress、Blocker、Next action。

## 14. Energy Analytics

```text
Breadcrumb + Page Header
Period / scope controls
Primary capability-appropriate analysis
Comparison / evidence Cards
Drill-down Table
```

Year / Month / Week / Day 仍保持 URL 可恢复语义。

## 15. Control / Strategy / FDD

按决策链设计：

```text
Current fact
→ diagnosis / candidate
→ evidence
→ impact / safety
→ approve / execute
→ verify
```

危险操作继续遵守 `PRODUCT.md` 控制安全规则。

## 16. Truth rules

无论视觉如何重设计：

- Offline != Fault；
- Missing != Zero；
- Unknown != Healthy；
- ACK != achieved target；
- Timeout != confirmed failure；
- Runtime / Connectivity / Freshness / Quality / Diagnosis / Alarm / Maintenance / Control 保持独立；
- 用户界面显示业务事实，内部 UUID / trace / revision / raw technical enum 只留在数据、URL、请求和日志层，除非任务确实需要。

## 17. Responsive

- desktop：完整 Sidebar + Header + multi-column content；
- tablet：内容 grid 逐步降为两列/一列；
- mobile：Sidebar overlay，Header 只保留关键 controls，Card/Table 改变结构而不是强行缩放；
- Table 在小屏根据任务隐藏次要列或进入 row detail，不做不可读的横向压缩。

## 18. Migration policy

直接迁移，不做视觉兼容层。

顺序：

1. AppShell；
2. Dashboard；
3. HVAC / Realtime；
4. Device Center；
5. Alarm Center；
6. Work Orders；
7. Energy Analytics；
8. Control / Strategy / FDD；
9. System / Registry；
10. 删除旧 shared UI / CSS，最后删除 Ant Design / ProComponents / Ant Design Charts。

每个 Surface 完成标准：

- 业务事实与权限语义正确；
- 使用新 shadcn application grammar；
- 无旧视觉兼容 JSX/CSS；
- TypeScript / relevant tests / build 通过；
- 使用 WSL + Windows Chrome/Chromium 做真实浏览器视觉验收。

## 19. 旧参考资料的地位

以下资料只用于恢复业务语义、信息需求或历史上下文，不是新的视觉 source of truth：

- 旧 HVAC 01–08 设计稿；
- 旧 Device Center 图片；
- 旧 Alarm Center 图片；
- Ant Design Pro / ProComponents 示例；
- Control Desk v1 截图和 CSS；
- 当前尚未迁移的产品页面。

需要新的视觉判断时，优先对照当前 shadcn/ui、shadcn-admin 和本设计文档。

## 20. 视觉审查反面模式与经验教训（Visual Review Anti-patterns & Practical Lessons）

在 2026-09 视觉大重构与真实浏览器审查中，沉淀出以下 6 大常见视觉反面模式（Anti-patterns）与实战精进法则：

### 20.1 首屏三重回声（The Triple Echo）
- **反面模式**：全局 App Header、Page Header、第一张 Card/Section 连续重复相同 route title，或 Breadcrumb、H1、ContextBar、CardTitle 都机械重复同一站点/对象名称。
- **根因分析**：没有区分导航层级、页面身份、业务上下文和 section task 的职责。
- **精进法则**：
  - **App Header** 负责 Sidebar trigger、全局搜索、通知、主题、账户和真正全局的系统状态；
  - **Breadcrumb** 负责层级路径；
  - **Page Header / H1** 负责当前 Surface 的唯一页面主标题；
  - **Supporting context** 负责 scope / freshness / 时间范围等必要上下文，不再重复 H1；
  - **CardTitle / Section Title** 必须表达该区块的业务功能与任务语义（例如“运行概况”“关键过程量”“待处理工作”），严禁再次使用 route title 作为 section 标题。

### 20.2 蔓延的横向滚动条（The Creeping Scrollbar）
- **反面模式**：在标准桌面视口（1440px～1672px）下，设备中心或告警中心的台账表格外层或内部容器出现横向滚动条，内容被无故截断或需拖动查看。
- **根因分析**：
  - 给 Table 随意设置 `min-w-[1080px]` 等固定最小宽度；
  - 单元格文字未加 `truncate`，遇到复合标题或多标签时撑破列宽；
  - 容器层多重包裹 `overflow-x-auto`，导致滚动条层叠蔓延。
- **精进法则**：
  - **目标桌面视口下，核心台账必须实现零横向滚动**；
  - 采用 `table-fixed w-full`，并在 `TableHeader` 中为每一列显式分配百分比宽度（总和严格为 100%），如：等级 7%、告警标题 28%、状态 8%、确认 8%、负责人 9%、持续 10%、重复 6%、搁置 11%、最近变化 13%；
  - 主文案列（告警标题、设备名称、关联空间）强制配合 `min-w-0 pr-2` 与 `truncate`；详情 Sheet 覆盖在右侧而不改变台账宽度，核心 Ledger 不再为常驻 Split Inspector 让出列宽。

### 20.3 机械 KPI 卡片平铺（Equal-weight KPI Card Wall）
- **反面模式**：把系统运行做成 5～6 张独立 KPI Card 的通用 Dashboard，再把真正的过程状态、设备群和对象调查压到第二屏。
- **根因分析**：把运行工作区误当成统计首页，而不是 ISA-101 风格的 situational-awareness workspace。
- **精进法则**：
  - **运行 Workspace 不使用 KPI Card Wall**。站点、时区、更新时间、功率、COP、设备群、告警和数据延迟采用紧凑 `FactStrip` / context facts 表达，不制造一排独立 Card；
  - 视觉主角必须是 **当前过程事实 + 设备群**；选中对象后用右侧 Detail Sheet 补充详情，不能常驻压缩主工作区；
  - `FactStrip` 只做快速扫读，不承载“正常/优秀/自动优化”等前端结论；异常才获得 warning / critical 视觉权重；
  - Site Overview 可以按其独立 Surface 规范使用少量 Metric Card，但不能把该 Dashboard 语法复制到 Operations、Devices、Alarms 等任务型 Workspace。

### 20.4 低数据量断崖留白（The Wasteland Layout）
- **反面模式**：当站点告警较少（仅 1～2 条）、或者外层 Card 固定高度过大（如固定 400px）而内部图表自身半径过小时，卡片内部出现大面积惨白断层，产生“系统卡死或数据未加载完”的劣质感。
- **根因分析**：图表几何参数（如甜甜圈图内径外径）使用了小卡片的固定像素，在宽屏自适应容器下未进行比例伸展。
- **精进法则**：
  - 甜甜圈图与饼图必须根据卡片高度适配饱满度（如 innerRadius: 68, outerRadius: 100~110），消除周围不合理的巨大留白；
  - 维持垂直视觉张力（Vertical Balance），低数据量或无选中态时，使用结构完备的 `Empty` 组件（配合 `EmptyMedia` 图标与引导文案），杜绝孤立悬空的文本框。

### 20.5 灰字未知网格与真实工业数据语义（Industrial Data Realism）
- **反面模式**：面对设备离线或数据源中断，表格中直接出现一排单调无解释的灰色“未知”或破折号“—”，甚至在某些异常下误用绿色 Badge 标为正常。
- **根因分析**：混淆了物理状态与数据质量，缺乏对工业数据不完备性的认知。
- **精进法则**：
  - 坚决执行真实性原则：$Offline \neq Fault$、$Missing \neq Zero$、$Unknown \neq Healthy$；
  - 运行状态（Running/Stopped）、连接状态（Online/Offline）、数据新鲜度（Fresh/Stale）和数据质量（Good/Suspect）必须**四维严格独立解耦**；
  - 针对异常或未知，必须提供具体业务归因（例如“通信离线”“点位配置需核查”“数据不完整”），并在详情 Sheet 中提供诊断、工单或完整详情 Route 等闭环行动入口。

### 20.6 色彩克制与视觉噪声控制（Noise & Palette Discipline）
- **反面模式**：图表使用全黑柱条、或者页面堆砌大面积高饱和度的彩色卡片，导致操作员产生视觉疲劳，对真正的异常失去敏锐度。
- **根因分析**：混淆了面向大众消费者的“炫酷大屏”与面向专业值班人员的“高可靠工作台”。
- **精进法则**：
  - 常态维持中性克制（Zinc neutral/muted）；
  - 彩色仅保留给**需要值班人员立即干预的异常与偏离**（Critical/Major/Minor 语义色）；
  - 统计图表使用品牌与主题色系的低刺激度阶梯色，并提供平滑过渡与 Hover 状态，保持工业级沉稳感。

## 21. 总览看板（2026-10-10 当前方向）

2026-09-30 的「基线 → 实际 → 节省」首页依赖基线、节能量与电价 owner，三者均未接入，真实构建只剩「未接入」。2026-10-10 起首页按 shadcn dashboard-01 构图，只展示站点已有的真实事实：

- 四张指标卡（`CardDescription → CardTitle → CardAction`，footer 两行说明）：冷站实时功率（附瞬时制冷量）、今日空调用电（附供冷量）、今日冷站综合能效（附实时 COP）、主要设备运行（冷机、水泵、冷却塔；离线优先于运行状态）。卡片为纯白底，不加渐变。
- 全宽用电与供冷面积图，期间（今日 / 近 7 天 / 本月 / 本年）写入 URL；窄卡片用 Select 代替 ToggleGroup。
- 未结告警与待办工单两张列表卡，按告警严重度、工单优先级排序各取前 5 条，链接到对应页面的 `?inspect=` 详情；查询与排序复用告警、工单 owner 的共享 query options。
- 节能量、节能率与节约费用以一条虚线框明确「未接入」，接入基线与电价 owner 后再回到首页。
- 过期或质量降级的读数置灰并说明，设备台账未就绪时不显示 0 台。

`docs/design-system/dashboard-layout-specification.md` 与 `03-site-overview.md` 的 2026-09-30 构图仅作背景。
