# 智慧能源 Web 页面架构 v3 — 成熟产品基准研究版

> 状态：SELECTED / RESEARCH-BACKED WORKSPACE BLUEPRINT
>
> 日期：2026-09-22
>
> 用途：替代仅凭功能相似度做页面合并的规划方式。本文先研究成熟 BMS / EMIS / FDD / FM 产品及公共标准，再提出本项目的页面架构。未经产品评审，不直接作为路由迁移指令。

## 1. 研究问题

本轮不问“36 个页面能不能减少”，而问：

1. 成熟 BMS 如何组织实时运行、设备、趋势、告警、控制和 Schedule？
2. 成熟 EMIS 如何组织 portfolio、site、energy、demand、cost、carbon、M&V 和 reporting？
3. FDD / maintenance 产品是否把 Alarm、Diagnosis、Task / Work Order 合并？
4. Table / ledger 选中记录后通常是新页面、side pane、drawer，还是 modal？
5. 哪些能力是一级业务域，哪些只是对象上下文能力或高级工具？

## 2. 外部基准

### 2.1 Siemens Building X Operations Manager

官方资料将 Operations Manager 定义为统一的建筑运营应用，能力包括：

- multi-site overview；
- real-time visibility and operation；
- comfort monitoring；
- data history and trend analysis；
- event information and notifications；
- remote control、schedules、alarm management、audit log；
- FDD 与 work-order tracking。

Operations Manager 内部存在 site-specific 的 `Devices`、`Building hierarchy`、`Trend analysis` 等页面，但 Trend 属于 Operations Manager 内部能力，不是与 Operations 平级的独立产品域。

官方迁移文档还显示：用户在 Devices 中选数据点时，设置通过 side-panel 展开；Trend analysis 支持保存定义、最多 10 个点和独立时间范围。

来源：
- https://sid.siemens.com/api/khub/documents/bAG_BJgz2QXQ2vfC5O6rDQ/content
- https://sid.siemens.com/r/A6V14374875/24241637643___en-US_25406326795
- https://sid.siemens.com/r/A6V14374875/24241637643___en-US_25416388619

**对本项目的启示：**
- “趋势”可以是完整、可保存、可深链的工具，但不必成为一级业务域；
- 实时运行、设备上下文、事件、趋势、控制高度互通；
- 即时控制应尽量发生在设备 / 运行上下文，而不是额外制造一个孤立的 Control CRUD 页面。

### 2.2 Schneider EcoStruxure Building Operation WebStation / WorkStation

Schneider 将 WebStation 定义为日常 BMS 工作界面，同一环境中包含：

- dashboards；
- graphics；
- alarms；
- schedules；
- trend logs；
- events；
- user accounts。

更重要的是其语义模型：选择一个 floor / equipment 后，可自动生成与该对象关联的 alarm view、trend chart、schedule view。公开界面也长期采用 system tree + main work area + alarm/event pane 的组合。

来源：
- https://ecostruxure-building-help.se.com/Topics/show.castle?id=8792
- https://sqa.ecostruxure-building-help.se.com/bms/topics/show.castle?id=7939&locale=en-US&productversion=7.1

**启示：**
- 对象是上下文中心，Alarm / Trend / Schedule 是围绕对象展开的视图；
- 不需要把每一种工程视角都提升成 Sidebar 一级页面；
- 高频运营需要在一个 workspace 内快速来回，而不是不断 route hopping。

### 2.3 Honeywell Remote Building Manager

Honeywell RBM 的官方产品和条款资料给出了非常明确的页面组合：

Site Summary Dashboard：
- connectivity；
- schedules & overrides；
- alarms；
- active high alarms；
- point list summary。

Equipment / Device dashboard：
- equipment information；
- equipment summary；
- active alarms；
- trend dashboard；
- point list；
- schedule。

Point List：
- sort / advanced filters；
- point details；
- point-to-trend mapping；
- point write / override / batch write。

另外保留独立 Alarm Console。

来源：
- https://buildings.honeywell.com/us/en/products/by-category/building-management/software/cloud-software/remote-building-manager
- https://ws.buildings.honeywell.com/content/dam/hbtbt/en/documents/other-files/legal-archive/eula/hon-ba-remote-building-manager-offering-specific-terms.pdf

**启示：**
- Equipment Detail 应内建 trend / alarm / point / schedule，而不是为这些信息继续跳页面；
- Alarm Console 是高频、跨对象的独立工作任务，值得稳定 workspace；
- Trend 同时存在于设备上下文和更广泛分析，不等于它要占一级 Sidebar。

### 2.4 Johnson Controls Metasys UI

2026 Metasys UI 16.0 的 Equipment Dashboard 包含：

- Trend widget；
- Equipment Activity（alarm + user change）；
- Equipment Data；
- Equipment Relationships；
- Graphics；
- Schedule。

同时系统提供独立的 Custom Trend Viewer，可跨设备选择最多 10 个点。

Alarm Manager 又是独立的高频告警管理工作区。

来源：
- https://docs.johnsoncontrols.com/bas/r/Metasys/en-US/Metasys-UI-Help/16.0/Widgets-dashboards-and-apps/Equipment-dashboard
- https://docs.johnsoncontrols.com/bas/r/Metasys/en-US/Metasys-UI-Help/6.0/Widgets-and-Dashboards/Reporting-Equipment-Trend-widget-Building-Network-Trend-widget-Custom-Trend-viewer-Trend-Study-widget-and-Trend-Study-Manager
- https://docs.johnsoncontrols.com/bas/r/Metasys/en-US/Metasys-UI-Help/8.0/Commanding-and-acting-on-alarms-and-audits/Acting-on-alarms/Alarm-Manager-and-Alarm-Monitor

**启示：**
- 最成熟的趋势架构不是“Trend page 或 no Trend page”二选一，而是：
  1. contextual trend；
  2. advanced cross-object trend studio。
- Equipment 是持久对象上下文；Alarm 是跨对象高频工作上下文。

### 2.5 Siemens Building X Energy Manager

Energy Manager 的公开帮助目录没有把 demand / efficiency / billing / carbon / benchmark 全部拆成十几个一级页面。

核心导航与任务围绕：

- Dashboard / Portfolio overview；
- Site details；
- Energy performance；
- Emissions；
- Reports；
- Configuration；
- Notifications。

Dashboard / Site details 共用：
- time filter；
- location/site filter；
- data type（consumption / costs / CO2）；
- forecast / comparison。

Site details 内进一步包含：
- anomaly detection；
- heatmap；
- weekly consumption；
- load curve；
- multiple meter analysis；
- consumption flow。

Configuration 则集中天气归一化、tariff、emission factor、budget、meter 等配置。

来源：
- https://sid.siemens.com/r/A6V12503191
- https://sid.siemens.com/r/A6V12503191/19445840907_24349853835__en-US_22014708747
- https://sid.siemens.com/r/A6V12503191/19445840907_24349853835__en-US_23552693003

**启示：**
- 能源分析应围绕统一 scope / period / comparison / data type 组织；
- Demand、load curve、meter analysis 往往是同一 Energy workspace 的分析视图；
- Reports、Configuration 是不同的持久任务，值得独立；
- Carbon 可以是 Energy/Sustainability 内的明确视图，不需要散成多个同层页面。

### 2.6 Johnson Controls OpenBlue Enterprise Manager

OpenBlue 将不同工作角色保持区分：

- Sustainability Manager → Energy；
- Asset Manager → Fault Management；
- Service Manager → Work Orders / Service Reports；
- Setup → configuration。

一个官方 energy troubleshooting 案例的实际路径是：

```text
Energy Performance
→ Equipment Performance / Fault
→ Work Order
→ Service completion
→ 返回能源与设备数据验证结果
```

Fault Management 页面中点击 fault 后查看 fault information、fault trend、work-order info、meter/fault relationship。

来源：
- https://docs.johnsoncontrols.com/bas/r/OpenBlue/en-US/OpenBlue-Enterprise-Manager-Implementation-Planning-Guide/4.0/Troubleshooting-energy-issues-with-OBEM
- https://docs.johnsoncontrols.com/bas/r/OpenBlue/en-US/OpenBlue-Enterprise-Manager-User-Guide/6.2/Service-Manager/Service-Manager-Work-Order-tab/Working-with-work-orders
- https://docs.johnsoncontrols.com/bas/r/OpenBlue/en-US/OpenBlue-Enterprise-Manager-Product-Bulletin/6.3/Overview-of-Energy-features/Energy-Fault-Analysis-page

**启示：**
- Energy、Fault、Work Order 不应仅因“流程连续”就被压成一个页面；
- 应保留不同长期任务，但建立明确 drill-through 与 source context；
- Work Order 有生命周期、comments、service report，因此是持久任务。

### 2.7 Clockworks Analytics

Clockworks 的模块结构非常有参考价值：

- Dashboards；
- Diagnostics；
- Tasks；
- Analysis Builder。

Diagnostics 是按影响优先级排列的问题表。
Task 从 Diagnostic 创建，负责 assign / status / action / CMMS work order。
Analysis Builder 是跨点位、任意时间范围的 ad-hoc trend / graph workspace。

来源：
- https://clockworksanalytics.atlassian.net/wiki/spaces/ClockworksAnalyticsUM/overview
- https://clockworksanalytics.atlassian.net/wiki/spaces/ClockworksAnalyticsUM/pages/3519250434
- https://clockworksanalytics.atlassian.net/wiki/spaces/ClockworksAnalyticsUM/pages/3527114753/Tasks
- https://clockworksanalytics.atlassian.net/wiki/spaces/ClockworksAnalyticsUM/pages/32112762

**启示：**
- Diagnostics 与 Tasks / Work Orders 不应强行合并为同一张表；
- Analysis Builder 应作为高级调查工具而不是顶层业务域；
- 从 diagnosis → task 是显式工作流。

### 2.8 Facilio

Facilio 作为成熟 connected FM 平台也保持模块边界：

- Portfolio；
- Asset；
- Energy；
- Maintenance；
- Space；
- Alarms；
- Diagnostics。

Diagnostics 可将严重 alarm 转成 work order；Maintenance 独立拥有完整 Work Order 生命周期、approval、task、attachment、history、close/reopen。

来源：
- https://facilio.com/help/docs/user-guide/introduction/
- https://facilio.com/help/docs/user-guide/facilio-modules/diagnostics/
- https://facilio.com/help/docs/user-guide/facilio-modules/maintenance-management/handling-maintenance/managing-work-orders/

**启示：**
- “告警 / 诊断 → 工作”应该连接，但不是同一对象、同一工作区；
- Work Order 详情适合 durable detail，不适合只放在 Sheet。

### 2.9 DOE / FEMP + ASHRAE

DOE 将 EMIS 定义为能力集合，而不是页面目录：

- centralize / normalize / visualize；
- utility bill management；
- interval meter analytics；
- M&V；
- AFDD；
- supervisory control；
- O&M optimization。

DOE 还明确指出不同建筑可拥有不同 capability stack，不需要所有站点拥有所有高级能力。

ASHRAE Guideline 36 则明确把高性能 HVAC sequence、real-time FDD 和 functional testing 绑定为一个控制与验证闭环。

来源：
- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- https://www.energy.gov/cmei/femp/energy-management-information-system-planning-and-procurement
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**启示：**
- Capability Catalog 不能直接等于页面目录；
- 功能验证必须存在，但不等于一定要成为一级导航；
- capability gating 是成熟 EMIS 的基本架构要求。

---

## 3. 交互模式研究：Table 行点击后到底应该是什么

### 3.1 不采用“所有 Table → modal Sheet”的规则

Microsoft 的 List/Details pattern 对 641px 以上宽度推荐 side-by-side list/details；窄屏才 stacked。

Material Side Sheet 明确区分：
- Standard side sheet：与主内容共存；
- Modal side sheet：带 scrim、阻塞主内容。

Atlassian Drawer 文档明确警告：modal drawer 是 invasive，打开后底层 UI 不可交互；如果任务需要引用背后的内容，应选择新页面或 non-modal 方案。

来源：
- https://learn.microsoft.com/en-us/windows/apps/develop/ui/controls/list-details
- https://github.com/material-components/material-components-android/blob/master/docs/components/SideSheet.md
- https://design-system-docs-bifrost.prod-east.frontend.public.atl-paas.net/components/drawer/usage

### 3.2 本项目的正确模式

桌面高频 Ledger：

```text
Table
+ non-modal Context Inspector
```

用户选下一行时 Inspector 原位更新，不需要关闭。

窄屏：

```text
Table
→ shadcn Sheet
```

复杂持久对象：

```text
Inspector
→ Open full detail
→ Durable Route
```

因此实现规则应为：

| 情况 | 默认交互 |
| --- | --- |
| 高频逐行扫描、比对 | Desktop split inspector |
| 768px 左右及以下 | Sheet |
| 小型阻塞式创建 / ACK / Assign | Dialog |
| 不可逆 / 高风险确认 | AlertDialog |
| 长生命周期、多步骤、多证据对象 | Route |
| 跨对象高级趋势调查 | Secondary durable analysis route |
| 行内次要动作 | Dropdown menu |

## 4. 基于研究重新定义导航层级

成熟产品反复出现四个层级：

```text
Business Domain
→ Workspace
→ Contextual View / Tool
→ Durable Object Detail
```

本项目此前的问题，是把后面三个层级大量提升成了 Sidebar 页面。

### 4.1 36 Surface → 10 Workspace 的设计继承规则

36 个已完成并通过评审的 Surface 不只是 capability inventory，也是新 10 Workspace 的 **设计母体库**。页面收敛首先改变 IA、导航层级和职责归属，不默认推翻已经成熟的页面形态。

实施时必须遵守：

- 每个新 Workspace 先明确它继承哪些旧 Surface，并逐项记录保留的页面 archetype、信息层级、Table / Detail / Inspector 语法、状态表达和专业动作；
- 已经 PROMOTED / BROWSER REVIEWED 的 Surface，其主工作区结构默认继承；不能因为多个 Surface 合并到一个 Workspace，就静默重新设计它的核心 Table、Detail 或调查流程；
- 后续全站统一形成的设计系统改进可以叠加在旧 Surface 母体上，例如 `DataTableBlock + tablecn / TanStack`、统一 Toolbar、standalone Table、shadcn tokens 和新的 AppShell；这属于组件/交互语法升级，不等于推翻原业务页面结构；
- 合并后的次级能力可以降为 Tab、contextual view、Inspector 或 durable detail，但其原有成熟信息层级和业务语义仍应被继承，而不是重新拼装成 generic Dashboard；
- 如果新 Workspace 确实需要改变某个已验收 Surface 的核心形态，必须单独说明“为什么旧形态在新职责下不再成立”，并重新做显式视觉与交互评审；
- 这里的“继承旧 Surface”特指本轮 36 Surface 体系中已完成、已评审的 shadcn 页面资产。被 `legacy-ui-quarantine` 隔离的更早 Ant / ProComponents / Control Desk 页面、旧截图和旧 CSS 仍然不是设计依据。

因此后续每个 Workspace 都应形成：

```text
36 Surface 已验收设计
        +
后期统一组件 / Block 规范
        +
新的 10 Workspace 职责边界
        ↓
合并后的 Workspace
```

而不是：

```text
36 Surface 只抽业务字段
        ↓
从零重新画一个 Workspace
```

## 5. 建议的新一级导航

### 5.1 总览

**Primary job：** 决定现在最值得关注什么。

Portfolio context：
- portfolio overview；
- site benchmarking；
- major risks / opportunities。

Site context：
- site attention overview；
- current operation / alarms / work / energy deviations。

**吸收：** 01、02、03。

说明：
- 02 Benchmarking 归 portfolio context，不再错误地作为单站点一级 Surface。
- Portfolio 与 Site 使用同一个“总览”入口，根据当前 scope 显示对应视图。

### 5.2 运行

**Primary job：** 理解 HVAC 当前如何运行，并在对象上下文中调查实时与历史行为。

内部视图：
- 系统；
- 空间 / Comfort / IEQ；
- contextual trends；
- schedules / overrides（有能力时）。

**主要吸收：** 04、08，以及 05 的 contextual trend 能力。

不把高级趋势分析删除；高级 Trend Studio 改为 secondary route：

`/sites/:siteId/operations/trends`

它从运行、设备、告警、诊断、工单等处以“在趋势中调查”进入，并携带 points/time/sourceContext。

### 5.3 设备

**Primary job：** 查找、筛选和理解设备 / 点位 / 关系。

结构：
- Device ledger 作为完整宽度主工作区；
- Quick Preview 只显示 identity、独立当前状态、少量关键值、当前事项和“打开完整详情”；
- desktop Preview 为 non-modal overlay，不通过 splitter 压缩 Ledger；窄屏使用 modal Sheet；
- point list、relationships、recent evidence、alarm / diagnosis / work 摘要以及 schedule / control context 进入 durable Device Detail；
- 高级时序调查继续进入 secondary Trend Studio，不在 Preview 或 Device Detail 中复制完整趋势工作区。

**吸收：** 06。
**保留 hidden durable detail：** 07。

**设计继承规则：**
- 06 / 07 在 `docs/product/visual-reframes/06-08-visual-redesign-2026-09-17.md` 中已经完成 PROMOTED 视觉验收；36→10 不把这两张 Surface 当作仅有业务字段的“需求池”，而把其已验收的页面结构、信息层级和交互语法作为设备 Workspace 的设计母体；
- Device Ledger 保留 06 已验收的八列语义：`设备 / 对象与位置 / 运行 / 连接 / 数据 / 关键值 / 当前事项 / 更新`；
- 表格外观与工具栏叠加 36 Surface 后期统一的 `DataTableBlock + tablecn / TanStack` 规范：standalone table、Search / Sort / Filter / Columns / Pagination，不回退到 Card 套 Table；
- durable Device Detail 保留 07 已验收的 `Independent State Strip → 当前运行 → 当前注意事项 → 最近观测 + 工程点位 / 关系 + 设备资料 + 专业出口` 主次结构；
- 只有新 10-Workspace 的职责归属、跨工作区出口和导航层级可以在合并时调整；若要改变 06 / 07 的核心 Table 或 Detail 形态，必须重新做显式视觉评审，不能以“合并”为理由静默重设计。

设备详情 route 仅用于长时间工程调查，不是每次 row click 的默认目标。

### 5.4 告警与诊断

**Primary job：** 从 authoritative condition 进入 evidence-backed diagnosis。

内部保持两个明确视图：
- 告警；
- 诊断。

**吸收：** 09、10。

**设计继承规则：**
- 09 / 10 继续作为这个 Workspace 的设计母体，而不是只保留字段和业务能力。合并只改变一级导航与共享上下文，不把两个 Surface 重做成同一种通用 Dashboard；
- `告警` View 保留 09 的 active-first operator triage：告警负荷事实 → Active / History / Shelved / Performance 子视图 → standalone ledger → desktop 固定详情区 / narrow Sheet。ACK、Assign、Shelve 等处置事实与 physical condition 继续并行；
- `诊断` View 保留 10 的 evidence-led investigation：finding queue → selected investigation → Verified Facts → Published Finding → Hypothesis / evidence → Next Verification → Work / Verification handoff。Finding、Hypothesis 与 confirmed Root Cause 不得在合并后被压平；
- 两个 View 共享 Site / object / source trail，但不共享状态机；跨 View 跳转通过 `view=alarms|diagnostics` 与显式 Alarm/Finding identity 完成；
- 36→10 不允许为了“视觉统一”把 09 的告警台账和 10 的调查工作台改成相同的卡片墙，也不允许再次出现 table 外层 Card + 重复标题。standalone `DataTableBlock`、shadcn Tabs/Sheet/Dialog 和必要的 detail section 是共同语法，业务层级仍由原 Surface 决定。

为什么可合并成一个一级 workspace：
- 两者共享 site / object / time / severity / evidence context；
- 用户频繁从 alarm 进入 diagnosis；
- 但数据模型、状态和术语必须继续完全分离。

为什么不与 Work Order 合并：
- Clockworks、OpenBlue、Facilio 都把 diagnosis/fault 与 task/work lifecycle 分开；
- ACK / cleared / finding / root cause / work completed 是不同事实。

### 5.5 工单与验证

**Primary job：** 管理执行责任，并确认修正后系统是否真的恢复预期。

内部视图：
- 工单；
- 功能验证 / commissioning queue。

**吸收：** 11、13。
**保留 hidden durable detail：** 12。

Functional Verification 不再占 Sidebar 一个入口，但必须作为正式工作视图存在。

### 5.6 能源与绩效

**Primary job：** 解释能源、需量、效率、成本和排放，并发现偏差。

共享全局上下文：
- scope；
- period；
- media / energy type；
- comparison；
- normalization；
- baseline；
- data quality。

内部视图建议：
- 概览；
- 用能与需量；
- 系统能效；
- 成本与账单；
- 碳；
- 能源绩效方法 / EnPI / baseline。

**吸收：** 14、15、16、17、18、19。

这些不再是六个 Sidebar 页面。

其中 Billing / Carbon 依据 capability 显示。

20 DER / Flexibility 不默认塞在此处：如果实际部署具有 DER、storage、grid dispatch，则提升为“能源与绩效”中的 Flexibility view 或“自动化”中的 dispatch view，具体由真实控制能力决定。

### 5.7 改进

**Primary job：** 把 evidence-backed opportunity 变成受控改进，并验证收益。

内部对象 / views：
- Opportunities；
- Improvement projects / optimization plans；
- Objectives & action plans；
- M&V；
- management review inputs。

**吸收：** 21、22、23、24、30。

重要：这是一个业务闭环 workspace，不代表这些对象合成一种状态机。

应继续严格分离：
`Opportunity ≠ Proposal ≠ Approved Change ≠ Completed Work ≠ Verified Savings`

复杂 Optimization Plan、M&V Project 使用 durable detail route。

### 5.8 自动化

**Primary job：** 管理长期自动控制策略及其可审计执行。

内部视图：
- Strategies；
- Schedules / policies；
- Executions。

**吸收：** 26、27、28。

25“即时控制”不再默认做独立一级页面。
成熟 BMS 普遍把即时 command / setpoint / override 放在设备、系统或 graphics 上下文。

因此：
- 即时控制入口放在运行 / 设备 Inspector；
- 高风险动作经过 preflight + confirmation；
- 策略编辑、仿真、审批、版本、rollout 保留 durable Strategy Detail；
- execution ledger 保留为自动化 workspace 的 audit view。

只有当项目未来明确拥有一个 NOC 式跨系统批量控制岗位，才重新评估独立 Control Center。

### 5.9 报告

**Primary job：** 生成、计划、分发和追溯正式报告。

**保留独立：** 29。

Siemens Energy Manager、OpenBlue 等成熟平台都把 Report generation/scheduling 作为独立持久任务，而不只是各分析页的导出按钮。

### 5.10 设置

**Primary job：** 管理系统结构、数据、规则、集成和授权。

local navigation：
- 数据质量；
- 计量与语义模型；
- 规则与通知；
- 集成；
- 站点与系统；
- 用户 / 权限 / 审计。

**吸收：** 31–36。

它们是重要能力，但不应六个都与日常运营页面在主 Sidebar 平级。

Data Quality 同时必须从所有业务数据状态 deep-link 进入对应 issue。

---

## 6. 最终一级导航建议

```text
总览
运行
设备
告警与诊断
工单与验证
能源与绩效
改进
自动化
报告
设置
```

共 10 个一级入口。

不是为了追求“10”这个数字，而是这些边界在 Siemens / Schneider / Honeywell / Johnson Controls / Clockworks / Facilio 中都能找到对应的成熟任务边界。

## 7. Secondary routes，不进入 Sidebar

建议保留：

- Advanced Trend Studio；
- Device Detail；
- Work Order Detail；
- Optimization Project Detail；
- M&V Project Detail；
- Strategy Detail；
- Report Definition editor；
- Integration mapping / commissioning editor（复杂时）。

这些 route 允许 deep-link、bookmark、browser history 和跨会话继续工作，但不占一级导航。

## 8. 36 Surface 的初步裁决

| ID | 当前 Surface | v3 归属 | 裁决 |
| --- | --- | --- | --- |
| 01 | 企业总览 | 总览 / Portfolio | MERGE_VIEW |
| 02 | 站点对标 | 总览 / Portfolio Benchmark | MERGE_VIEW + FIX_SCOPE |
| 03 | 站点总览 | 总览 / Site | MERGE_VIEW |
| 04 | 系统运行 | 运行 | KEEP_WORKSPACE_CORE |
| 05 | 趋势分析 | 运行 / Advanced Trend Studio | SECONDARY_ROUTE |
| 06 | 设备 | 设备 | KEEP_WORKSPACE |
| 07 | 设备详情 | 设备 | HIDDEN_DURABLE_ROUTE |
| 08 | 舒适与室内环境 | 运行 | MERGE_VIEW |
| 09 | 告警 | 告警与诊断 | MERGE_VIEW |
| 10 | 诊断 | 告警与诊断 | MERGE_VIEW |
| 11 | 工单 | 工单与验证 | MERGE_VIEW |
| 12 | 工单详情 | 工单与验证 | HIDDEN_DURABLE_ROUTE |
| 13 | 功能验证 | 工单与验证 | MERGE_VIEW |
| 14 | 能源分析 | 能源与绩效 | MERGE_VIEW |
| 15 | 需量与负荷 | 能源与绩效 | MERGE_VIEW |
| 16 | 能效 | 能源与绩效 | MERGE_VIEW |
| 17 | 能源评审 | 能源与绩效 | MERGE_VIEW |
| 18 | 账单成本电价 | 能源与绩效 | CAPABILITY_VIEW |
| 19 | 碳排放 | 能源与绩效 | CAPABILITY_VIEW |
| 20 | DER / Flexibility | Energy 或 Automation | CAPABILITY_VIEW / DEPLOYMENT_DECISION |
| 21 | 节能机会 | 改进 | MERGE_VIEW |
| 22 | 优化方案 | 改进 | MERGE_VIEW + DURABLE_DETAIL |
| 23 | 目标与行动计划 | 改进 | MERGE_VIEW |
| 24 | M&V | 改进 | MERGE_VIEW + DURABLE_DETAIL |
| 25 | 控制 | 运行 / 设备 contextual command | REMOVE_PRIMARY_ROUTE |
| 26 | 策略 | 自动化 | MERGE_VIEW |
| 27 | 策略详情 | 自动化 | HIDDEN_DURABLE_ROUTE |
| 28 | 执行记录 | 自动化 | MERGE_VIEW |
| 29 | 报告 | 报告 | KEEP_WORKSPACE |
| 30 | 管理评审 | 改进 | MERGE_VIEW |
| 31 | 数据质量 | 设置 / Data Health | SETTINGS_CHILD |
| 32 | 计量与语义模型 | 设置 / Data Model | SETTINGS_CHILD |
| 33 | 规则与通知 | 设置 / Rules | SETTINGS_CHILD |
| 34 | 集成管理 | 设置 / Integrations | SETTINGS_CHILD |
| 35 | 站点与系统配置 | 设置 / Site Config | SETTINGS_CHILD |
| 36 | 用户权限审计 | 设置 / Access | SETTINGS_CHILD |

## 9. URL 原则

合并页面不等于把状态藏进本地 React state。

重要视图继续 URL-addressable：

```text
/sites/:siteId/operations?view=systems
/sites/:siteId/operations?view=comfort

/sites/:siteId/issues?view=alarms
/sites/:siteId/issues?view=diagnostics

/sites/:siteId/work?view=orders
/sites/:siteId/work?view=verification

/sites/:siteId/performance?view=energy
/sites/:siteId/performance?view=demand
/sites/:siteId/performance?view=efficiency
/sites/:siteId/performance?view=billing
/sites/:siteId/performance?view=carbon

/sites/:siteId/improvements?view=opportunities
/sites/:siteId/improvements?view=projects
/sites/:siteId/improvements?view=mv

/sites/:siteId/automation?view=strategies
/sites/:siteId/automation?view=executions
```

Advanced Trend Studio：

```text
/sites/:siteId/operations/trends
?points=...
&from=...
&to=...
&source=alarm:...
```

## 10. 下一步实施前必须先做的工作

1. 不直接删除 route。
2. 先把 `Surface Catalog` 拆成：
   - Capability catalog；
   - Workspace catalog；
   - Route catalog；
   - Navigation projection。
3. 建立统一 interaction contract：
   - INLINE；
   - SPLIT_INSPECTOR；
   - SHEET_NARROW；
   - DIALOG；
   - DURABLE_ROUTE。
4. 给所有 Ledger 标注 desktop inspector 行为。
5. 建立 context handoff：
   - site；
   - time range；
   - object；
   - evidence/source；
   - baseline/comparison。
6. 经过 browser prototype 验证后，再一次性删除旧导航和旧 route；不做长期 alias/compatibility architecture。
