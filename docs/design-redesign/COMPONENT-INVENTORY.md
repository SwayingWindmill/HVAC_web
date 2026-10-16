# 组件清单（商业级 SaaS 升级 · 第一阶段）

审计日期：2026-10-13 · 范围：`apps/hvac-web/src` · 配套文档：[CURRENT-AUDIT.md](CURRENT-AUDIT.md)

“引用文件数”统计的是 `import` 了该文件的源文件数（不含它自己），只作量级参考。“建议”列只是下一阶段的候选处理，**本阶段没有执行**：

- **保留**：行为和形态都合适，只换皮；
- **重设计**：保留职责，重做外观或结构；
- **合并**：和别的组件重复，收敛成一个；
- **删除候选**：零引用或已被替代。

## 1. 应用壳（`components/layout`、`app`）

| 组件 | 文件 | 职责 | 建议 |
| --- | --- | --- | --- |
| ShellChrome | `app/ShellChrome.tsx` | SidebarProvider + 侧栏 + Header + 站点切换确认 / 切换中 / 切换失败三种状态 + Toaster | 保留行为；重设计切换中和失败两页的外观 |
| AppSidebar | `components/layout/AppSidebar.tsx` | 4 组导航，告警 / 工单计数徽标，inset 变体，可折叠为图标 | 重设计（分组、平台入口、高后果入口的区分） |
| app-navigation | `components/layout/app-navigation.ts` | 导航配置（15 项）+ 激活判断 | 重设计 IA，接口保留 |
| ScopeSwitcher | `components/layout/ScopeSwitcher.tsx` | 产品标 + 当前站点；Popover + Command 搜索站点 | 保留行为；重设计形态（平台页的“选择站点”态） |
| NavUser | `components/layout/NavUser.tsx` | 用户菜单：系统管理、退出 | 重设计（平台功能入口） |
| AppHeader | `components/layout/AppHeader.tsx` | 折叠按钮、面包屑、搜索触发（Ctrl K）、主题、通知铃 | 重设计 |
| CommandSearch | `components/layout/CommandSearch.tsx` | 搜页面和设备 | 扩展（告警、工单、报告），重设计 |
| Main | `components/layout/Main.tsx` | 页面容器：`max-w-[1600px] px-6 pt-6 pb-8 gap-6`，`@container/main` | 重设计栅格断点（1280 单列问题） |
| NavigationProgress / SkipToMain | `components/layout/NavigationProgress.tsx` | 顶部加载细线、跳到主内容 | 保留 |
| use-shell-notifications | `components/layout/use-shell-notifications.ts` | 未读通知计数 | 保留 |
| RouteLoading | `app/RouteLoading.tsx` | 启动态 / 路由加载态（目前是无样式文字） | 重设计 |
| RouteSurfaces / RootErrorBoundary / ErrorBoundary | `app/*.tsx`、`components/ErrorBoundary.tsx` | 路由错误、无权限、不可用 | 重设计；补齐未被接住的渲染错误（系统管理崩溃时出现英文默认页） |
| ThemeGate | `app/ThemeGate.tsx` | 浅 / 深 / 跟随系统 | 保留 |

## 2. 应用级 Block（`blocks/`）

| 组件 | 引用文件数 | 用法 | 问题 | 建议 |
| --- | --- | --- | --- | --- |
| `PageHeader` + `PageMeta` | 19 | 每页的 h1、描述行、小标签、右侧操作 | 和面包屑重复；描述行写成了帮助文本；PageMeta 小标签形态单一 | 重设计 |
| `MetricGrid` / `MetricCard` / `MetricValue` | 14 | 13 页首屏的 4~5 格 KPI 条（39 个 Grid 实例，68 张卡） | 模板感的主要来源；底部两行灰字；数字 28px 写死 | 重设计为多种强度（主指标 / 次指标 / 内联事实），不再每页标配 |
| `FactList` / `FactRow` / `FactTile` | 13 | 标签–值列表、网格（113 处 FactRow） | 形态合理；FactTile 的 `className` 作用在值上，容易误用 | 保留，统一字号 |
| `DataTableBlock` | 9 | 页面级表格的外层：标题、操作、控件 | 很多台账没用它（设备、诊断、对标） | 保留，推广 |

## 3. 数据表（`components/data-table`，tablecn 语法）

| 组件 | 说明 | 建议 |
| --- | --- | --- |
| `DataTable` | TanStack Table v9 渲染、空状态、行点击 | 保留，作为唯一的台账表格 |
| `data-table-advanced-toolbar` / `-filter-list` / `-range-filter` / `-sort-list` / `-view-options` / `-column-header` / `-pagination` | 工具栏、筛选、排序、列显示、分页 | 保留；统一高度和样式 |
| `hooks/use-data-table.ts`、`lib/data-table-*.ts`、`lib/parsers.ts` | 状态、URL 序列化 | 保留 |

**表格的实际分布（不一致的根源）**

| 做法 | 页面 |
| --- | --- |
| `DataTable` | 告警、能耗分析明细、通知、集成管理、系统管理（审计、资产、规则、导入导出） |
| shadcn `Table` 手写 | 设备台账、实时快照、负荷预测逐时、站点对标排名、诊断发现、设备 / 工单 Sheet 内 |
| 裸 `<table>` | 月度报告 |

建议：台账类（设备、诊断发现）迁到 `DataTable`；Card / Sheet 内的小型只读表保留 `Table`，但统一表头、行高和数值对齐。

## 4. 图表

| 组件 | 文件 | 引用 | 说明 | 建议 |
| --- | --- | --- | --- | --- |
| ChartContainer 等 | `components/ui/chart.tsx` | 9 | shadcn Recharts 容器、主题色、Tooltip、Legend | 保留，重做皮肤 |
| ComposedChart | `components/ui/composed-chart.tsx` | 1 | 通用折 / 面积 / 柱组合，默认 `monotone` 曲线 | 重设计：默认改为线性或阶梯；连续时间轴 |
| MetricTabsChartCard | `components/metric-tabs-chart-card.tsx` | 2 | 页签式 KPI + 主图（总览、能耗分析） | 重设计：分类 X 轴压缩缺失区间（P0） |
| HeatmapChartCard / HeatmapChart | `components/heatmap-chart-card.tsx`、`ui/heatmap-chart.tsx` | 1 / 1 | 7×24 逐时热力图 | 重设计：宽屏比例、深色空格 |
| Sparkline | `components/ui/sparkline.tsx` | 1 | 迷你趋势 | 保留 |
| BarList | `components/ui/bar-list.tsx` | 1 | 横向排行条 | 合并：分项排行、对标排名、告警设备排行用统一的排行组件 |
| PlantDiagram | `features/operations/realtime/components/PlantDiagram.tsx` | 页面专用 | 冷站两环路示意（HTML 卡 + SVG 管段 + 流动动画） | 保留工程规则，重设计外观 |
| 桑基（自绘） | `features/breakdown/components/BreakdownWorkspace.tsx` | 页面专用 | 能流 | 保留，统一配色 |
| 分时电价条 / 电价时段轴 | `features/tariff/components/TouCostCard.tsx` | 2 页 | 峰平谷占比和 24h 时段 | 重设计：峰时段别用警告色 |
| 约束滑轨 / 阈值轨 | `OptimizationWorkspace`、`DiagnosticsWorkspace` 内联 | 页面专用 | 当前值、建议值、阈值在范围中的位置 | 抽成一个共享的“范围标尺”组件 |
| KpiCard、DistributionBar、RadialGauge | `components/ui/` | 0 | 从 dashboardcn 拷入，未使用 | 删除候选 |

## 5. 状态、徽标与反馈

| 组件 | 文件 | 引用 | 颜色来源 | 建议 |
| --- | --- | --- | --- | --- |
| StatusBadge | `components/common/StatusBadge.tsx`（另有 `components/status-badge.tsx` 转出） | 7 + 6 | **硬编码** emerald / amber / sky / red；alarm 默认 `animate-ping` | 合并进 `Status` |
| Status / StatusIndicator / StatusLabel | `components/ui/status.tsx` | 6 | token（success / warning / information / destructive） | 保留，作为唯一状态徽标 |
| SeverityBadge | `features/alarms/alarm-presentation.tsx` | 告警、总览 | `--severity-*` | 合并为 Status 的告警变体 |
| PriorityBadge / WorkOrderStatusBadge | `features/work-orders/work-order-presentation.tsx` | 工单、总览 | 硬编码 red / orange / sky | 合并 |
| FindingSeverityBadge | `features/diagnostics/finding-presentation.tsx` | 诊断、总览 | 硬编码 | 合并 |
| MeasureStatusBadge | `features/savings/components/measure-presentation.tsx` | 节能 | 硬编码 | 合并 |
| LiveIndicator | `features/operations/realtime/device-presentation.tsx` | 多页 | PageMeta 圆点 | 保留，重设计（“实时推送 / 快照”） |
| ReadingValue / ReadingMetric / KeyReadings | 同上 | 实时、总览、设备 | 过期置灰 | 保留语义 |
| Badge | `components/ui/badge.tsx` | 21 | | 保留，收敛用途 |
| DeltaBadge | `components/ui/delta-badge.tsx` | 6 | 增减方向，可反转 | 保留 |
| Alert | `components/ui/alert.tsx` | 16 | | 保留 |
| Empty | `components/ui/empty.tsx` | 11（36 处） | 圆角图标框 + 一行字 | 重设计，区分“没有事项”“不可用”“未接入” |
| Skeleton / Spinner | `ui/skeleton.tsx`、`ui/spinner.tsx` | 22 / 6 | | 保留，补齐页面级骨架 |
| Toaster（sonner） | `ui/sonner.tsx` | 1（7 处调用） | `richColors` | 保留 |
| Progress | `ui/progress.tsx` | 0 | | 删除候选 |

## 6. 表单与输入

| 组件 | 引用 | 说明 | 建议 |
| --- | --- | --- | --- |
| Button | 42 | 主按钮青绿；尺寸 sm 用得最多 | 保留，重做变体（危险操作、次级） |
| ButtonGroup | 1 | 控制页“启动 / 停止 / 复位” | 保留 |
| Input / InputGroup / Textarea / Label / Field | 15 / 3 / 10 / 5 / 1 | Field 只有 1 处用，其他表单手搭 label | 统一用 Field |
| Select | 13 | 各页高度、宽度不一 | 统一尺寸 |
| Checkbox / Calendar / Kbd | 1 / 1 / 1 | | 保留 |
| Toggle / ToggleGroup | 1 | 告警视图切换（带计数） | 合并为统一的“视图切换” |
| PeriodTabs | 4 | 期间切换（今日 / 近 7 天 / 本月 / 本年） | 合并为统一的“视图切换” |
| Tabs | 5 | 系统管理（下划线）、工单 / 通知（胶囊） | 合并：页面内容切换和数据视图切换分开 |
| Faceted / Sortable | 2 / 2 | 筛选面、拖拽排序（系统管理） | 保留 |
| Switch / RadioGroup / Slider | 0 | | 删除候选（或在设置页需要时再用） |

## 7. 浮层与容器

| 组件 | 引用 | 用法 | 建议 |
| --- | --- | --- | --- |
| Sheet | 8 | 对象详情：设备、告警、工单、诊断、节能措施、通知；右侧 480px 非模态 | 重设计宽度和内部分组；保留 `?inspect=` |
| Dialog | 9 | 新建工单、登记措施、措施时间、站点切换确认 | 保留，重做表单排版 |
| AlertDialog | 2 | 控制确认、凭据吊销 | 重设计：危险样式、影响说明、风险等级 |
| Popover / DropdownMenu / Tooltip / Command | 5 / 3 / 5 / 6 | | 保留 |
| HoverCard / Accordion / Resizable | 0 | | 删除候选 |
| Card | 25 文件（152 处） | 几乎所有内容块 | 重设计：减少卡片，分清“区块”和“卡片” |
| Item / ItemGroup | 2 | 总览列表卡 | 保留 |
| Timeline | 2 | 告警详情、控制指令记录 | 保留 |
| Separator / ScrollArea / Avatar / Breadcrumb / Sidebar | 5 / 1 / 1 / 1 / 5 | | 保留 |

## 8. 页面级 Workspace（`features/`）

| 页面 | 主文件 | 行数 | 依赖的共享组件 | 页面专用组件 |
| --- | --- | --- | --- | --- |
| 总览 | `overview/OverviewWorkspace.tsx` | 524 | PageHeader、MetricTabsChartCard、HeatmapChartCard、Card、Item、Empty | LivePlant、SavingsCard、ListCard |
| 实时运行 | `operations/realtime/components/RealtimeWorkspace.tsx` | 266 | MetricGrid、FactList、Table | PlantDiagram、device-presentation |
| 设备 | `operations/systems-devices/components/SystemsDevicesWorkspace.tsx` / `DeviceInspector.tsx` | 267 / 223 | MetricGrid、Table、Select、Sheet、ComposedChart | 分组表行 |
| 能耗分析 | `energy-analysis/consumption/components/ConsumptionWorkspace.tsx` | 255 | MetricTabsChartCard、FactList、DataTable、PeriodTabs | TouCostCard |
| 分项能耗 | `breakdown/components/BreakdownWorkspace.tsx` | 280 | MetricGrid、PeriodTabs、Chart | 桑基、排行 |
| 用电负荷 | `load/components/LoadWorkspace.tsx` + `LoadCurve`、`ForecastHours`、`DailyPeaks`、`DemandCharge` | 280 | 指标条、Chart、Tabs、Table | 今天与明天一条轴、预测区间、电价高峰底色、本月最高参考线、最高日标红 |
| 优化建议 | `optimization/components/OptimizationWorkspace.tsx` | 352 | MetricGrid、FactList、Card | 建议主卡、约束滑轨 |
| 节能项目 | `savings/components/*` | 171 + 299 + 148 + 72 | Empty、Dialog、Sheet、Chart | MeasureSheet、RegisterMeasureDialog、MeasureTimeDialog |
| 站点对标 | `benchmark/components/BenchmarkWorkspace.tsx` | 223 | MetricGrid、PeriodTabs、Table、Chart | 排名条 |
| 月度报告 | `reports/components/ReportWorkspace.tsx` | 321 | Select、Chart | A4 报告版式（编号章节） |
| 告警 | `alarms/components/AlarmsWorkspace.tsx` / `AlarmInspector.tsx` | 492 / 217 | MetricGrid、DataTable、ToggleGroup、Chart、Sheet、Timeline | 14 天堆叠柱、设备排行 |
| 故障诊断 | `diagnostics/components/DiagnosticsWorkspace.tsx` | 326 | MetricGrid、Table、Sheet | 阈值轨 |
| 工单 | `work-orders/components/WorkCenterWorkspace.tsx` / `WorkOrderInspector.tsx` / `CreateWorkOrderDialog.tsx` | 193 / 218 / 97 | MetricGrid、DataTable、Sheet、Dialog | |
| 控制指令 | `control/components/ControlWorkspace.tsx` / `CommandConfirmation.tsx` | 402 / 87 | MetricGrid、Card、ButtonGroup、AlertDialog、Timeline | 设定步进输入 |
| 通知中心 | `notifications/NotificationCenter.tsx` | 257 | DataTable、Tabs、Sheet | |
| 集成管理 | `integrations/IntegrationsWorkspace.tsx`、`connectivity/GatewayCredentialPanel.tsx` | 90 / 54 | DataTable、Select、AlertDialog | |
| 系统管理 | `system/SystemManagement.tsx` + `registry-admin/*`、`rule-management/*`、`audit/*`、`design-system/*` | 248 + 约 1300 + 685 + 58 + 265 | MetricGrid、Tabs、DataTable、表单 | 资产工作台、规则编辑、设计系统展示页 |

## 9. 重复与收敛候选（汇总）

| 主题 | 现状 | 收敛方向 |
| --- | --- | --- |
| 状态徽标 | `StatusBadge`（硬编码）、`Status`（token）、5 个领域 Badge 各自配色 | 一个 `Status` + 语义变体表（运行 / 连接 / 告警严重度 / 优先级 / 流程状态） |
| 指标展示 | `MetricCard`、`MetricValue`（blocks）和 `ui/metric-value`（两个同名组件）、`KpiCard`（未用）、MetricTabsChartCard 里的页签指标、总览 `FactTile text-lg`、报告页自绘指标 | 一个数值组件（数值 + 单位 + 过期 / 缺失），几种强度的指标容器 |
| 表格 | DataTable / Table / 裸 table 三种 | 台账用 DataTable，嵌入用 Table，统一样式 |
| 视图 / 期间切换 | PeriodTabs、ToggleGroup、Tabs（胶囊）、Tabs（下划线） | 两种：数据视图切换（分段控件），内容切换（下划线 Tabs） |
| 排行条 | BarList、分项排行、对标排名条、告警设备排行 | 一个排行组件 |
| 范围标尺 | 优化约束滑轨、诊断阈值轨 | 一个 RangeGauge |
| 空状态 | Empty 36 处，形态一致但语义不分 | Empty 分三种语义（无事项 / 不可用 / 未接入） |
| 工具函数 | `cn` 来自 npm 包 `cn`（27 个注册表组件）和 `@/lib/utils` | 统一用 `@/lib/utils` |
| 原生色阶 | 11 个文件约 90 处 Tailwind 色阶类 | 全部改用 token |
| 任意字号 | `text-[11px]` ×14、`[1.75rem]` ×4、`[0.8rem]` ×4、`[10.4px]` ×2、`[10px]` ×1 | 收敛到字号阶梯 |
| 任意高度 | 图表高度有 220 / 260 / 280 / 300 / 340 / 360 / 372 / 420 / 520 / 560 / 640 / 900px | 2~3 档图表高度 |

## 10. 零引用（删除候选）

- `components/ui/`：`kpi-card`、`distribution-bar`、`radial-gauge`、`progress`、`hover-card`、`accordion`、`radio-group`、`resizable`、`slider`、`switch`。
- `package.json` 依赖：`echarts`、`@antv/g6`、`@antv/x6`、`@antv/x6-react-shape`、`zustand`、`date-fns`（源码中引用为 0；删除前要确认构建脚本和测试没有用到）。
