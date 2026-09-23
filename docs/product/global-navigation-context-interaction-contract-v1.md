# 智慧能源系统 Global Navigation + Context Interaction Contract v1

> **状态：SUPERSEDED / HISTORICAL**  
> **Superseded by:** `docs/product/global-navigation-context-interaction-contract-v2.md`  
> **日期：2026-09-14**  
> **上游：** `docs/product/smart-energy-system-page-architecture-v2.md`  
> **外部审核：** `docs/product/smart-energy-system-page-architecture-audit-v1.md`  
> **适用范围：** 企业级智慧能源、HVAC 运行、能源绩效、持续改进、控制自动化与平台治理。
>
> 本文件不参考当前项目已有页面、旧菜单、旧路由或旧组件布局。它只定义新的产品级导航、页面关系、上下文传递和交互边界。任何新的 Surface Specification、Route、Sidebar、Inspector、Dialog、Search Params 设计都必须遵守本契约。

---

# 1. 为什么需要单独的导航与上下文契约

智慧能源系统的难点不是“页面多”，而是用户会在多个专业工作区之间连续调查同一个问题：

```text
站点异常
→ 系统运行
→ 设备
→ 趋势
→ 诊断
→ 工单
→ 功能验证
→ 节能机会 / M&V
```

如果每次跳页都丢失 Site、Time、Object、Comparison、Source，用户就需要重复定位，系统会显得“专业但难用”。

因此本契约的目标是：

1. 让导航结构符合真实工作流，而不是后端服务结构。
2. 让用户始终知道“我在哪里、正在看什么、从哪里来、下一步去哪”。
3. 让日常用户看到足够的信息做决定，同时允许专业用户继续深入。
4. 让 URL、Router Search Params、Inspector 与 Detail Route 拥有明确责任。
5. 避免同一对象被重复建页、Drawer/Modal 滥用、菜单过深、筛选状态丢失。

---

# 2. 外部最佳实践依据

## 2.1 DOE / FEMP — EMIS 应围绕行动闭环，而不是模块目录

DOE 对 EMIS 的推荐运营过程是：

```text
Identify and Prioritize
→ Validate, Diagnose, and Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor, Update, and Maintain
```

DOE 同时强调 EMIS 是 human-in-the-loop 工具，必须嵌入组织运营流程，并让改善结果能被验证。

来源：
- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/cmei/femp/articles/best-practices-support-emis-operation-federal-facilities

**本契约采用：** Sidebar 按用户工作域组织；跨页跳转按调查/改进链组织。

## 2.2 ENERGY STAR — Benchmarking 是筛选与优先级工具，不是根因页

ENERGY STAR Portfolio Manager 用于：

- 过去 vs 当前
- 目标 vs 当前
- 同类建筑比较
- weather-normalized EUI
- 识别低效站点
- 确定投资优先级

但官方明确指出 score 是 screening tool，并不能解释“为什么建筑表现差”。

来源：
- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results

**本契约采用：** Benchmarking 下钻到 Site / Energy / Efficiency，而不是在 Benchmark 页面完成所有调查。

## 2.3 ASHRAE Guideline 36 — 运行、FDD 与 Functional Test 是连续工作

Guideline 36-2024 的目标包括：

- HVAC energy efficiency
- control stability
- real-time FDD
- functional tests confirming sequences of operation

来源：
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本契约采用：** System Operations、Diagnosis、Control、Functional Verification 必须能在同一对象/时间上下文中连续跳转。

## 2.4 DOE Commissioning / MBCx — Corrective Action 后必须验证

DOE Commissioning 流程是 Plan → Investigate → Implement → Hand off / Integrate；MBCx 强调持续监测、功能测试、修正、重新验证和长期性能保持。

来源：
- https://www.energy.gov/cmei/femp/commissioning-process-federal-facilities
- https://www.energy.gov/cmei/femp/articles/enhancing-performance-contracts-monitoring-based-commissioning
- https://www.energy.gov/cmei/femp/performance-assurance-planning-utility-energy-service-contracts

**本契约采用：** Work Order 完成后不自动结束调查，必要时必须进入 Functional Verification。

## 2.5 ISA-101 — HMI 必须重视 screen hierarchy、navigation、alarm conventions 与 situational awareness

ISA-101 覆盖：

- menu hierarchies
- screen navigation conventions
- alarming conventions
- graphics/color conventions
- popups
- help screens
- interfaces to historical databases

其目标包括降低操作错误、提高 situational awareness、可靠性和一致性。

来源：
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101
- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards

**本契约采用：** 运行类页面不使用“自由探索式网站导航”，而使用稳定层级、明确当前对象和高一致性操作位置。

## 2.6 ISA-18.2 — Alarm 是生命周期管理，不是一个红色列表

ISA 的公开资料强调 alarm prioritization、documentation、HMI、procedures、testing、monitoring、history、auditing 与 management of change。

来源：
- https://www.isa.org/intech-home/2018/march-april/features/alarm-management-life-cycle

**本契约采用：** Alarm 页面与 Rule Administration 分离；Active Triage 与 History 分离；ACK/Assign/CLEARED 不混为一个状态。

## 2.7 W3C / USWDS — Breadcrumb、Side Nav、Dialog、Grid/Table 有清晰边界

来源：
- Breadcrumb: https://www.w3.org/WAI/ARIA/apg/patterns/breadcrumb/
- Dialog: https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/
- Grid: https://www.w3.org/WAI/ARIA/apg/patterns/grid/
- Table: https://www.w3.org/WAI/ARIA/apg/patterns/table/
- Side Navigation: https://designsystem.digital.gov/components/side-navigation/
- Combo Box: https://designsystem.digital.gov/components/combo-box/

**本契约采用：**

- Breadcrumb 表示层级，不表示线性流程。
- Side navigation 控制在 1–3 层。
- Modal 只用于必须阻断主页面的短任务/确认。
- 可扫描 Ledger 优先 native table；只有需要强交互键盘导航时才升级 grid。
- 大型 Site/Object 列表使用 searchable combobox，而不是几百项 Select。

## 2.8 Progressive Disclosure — 专业深度不应污染默认视图

Nielsen Norman Group 的 progressive disclosure 原则：默认展示高频、关键操作；高级/低频能力按需展开，降低学习成本和误操作。

来源：
- https://www.nngroup.com/articles/progressive-disclosure/

**本契约采用：** Operational View → Inspector / Advanced → Raw Evidence 三层递进。

---

# 3. 导航总原则

## 3.1 Navigation Projection，而不是固定菜单

完整产品定义 36 个 Surface，但 Sidebar 只是当前用户、当前站点能力和权限的投影。

导航生成条件：

```text
Surface is visible
= Product capability exists
AND deployment/site capability exists
AND principal may discover it
```

Action 是否可用另行判断：

```text
Action is enabled
= Surface visible
AND principal has action permission
AND current object supports capability
AND safety/interlock conditions permit
```

不能把“看得到”与“能执行”混成一个权限判断。

## 3.2 默认只显示高频工作入口

Sidebar 目标不是展示产品全部能力，而是支持高频任务。

默认业务组：

```text
总览
运行
事件与工作
能源与绩效
优化与控制
管理
系统
```

不允许新增一级组只因为后端增加了一个服务。

## 3.3 Side Navigation 最大深度

产品导航最多 3 层：

```text
业务组
→ 主 Surface
→ 少数真正需要直接访问的子 Surface
```

Detail、Inspector、Modal、Tab 不算 Sidebar 层级。

如果设计需要第 4 层菜单，优先重新审查页面职责。

## 3.4 页面标题必须比导航标签更完整

导航用短业务词：

```text
能源
效率
诊断
工单
```

页面标题可完整：

```text
能源分析
系统效率分析
诊断中心
工单中心
```

避免 Sidebar 使用 `Energy Performance Analytics Workspace` 这类术语。

---

# 4. 默认 Sidebar

```text
总览
  站点总览                         [站点用户]
  企业总览                         [portfolio 用户]

运行
  系统运行
  设备
  趋势
  舒适与室内环境                   [capability]

事件与工作
  告警
  诊断
  工单
  功能验证

能源与绩效
  能源
  需求与负荷                       [按产品范围]
  效率
  能源评审                         [能源管理角色]
  账单与成本                       [tariff/billing]
  碳排放                           [carbon]
  分布式能源                       [DER]

优化与控制
  节能机会
  优化方案
  控制中心                         [control]
  策略中心                         [automation]

管理
  站点对标                         [portfolio]
  目标与行动计划
  节能量验证
  报告
  管理评审                         [management]

系统
  数据质量
  配置                             [admin/config]
```

`配置` 进入后再包含：

- 计量与语义模型
- 规则与通知
- 集成管理
- 站点与系统配置
- 用户、权限与审计

这些管理员页面不应与日常运营页面平铺在同一个导航密度中。

---

# 5. 角色默认入口

系统登录后不强制所有用户落在同一 Dashboard。

| 角色 | 默认入口 | 原因 |
|---|---|---|
| 企业能源负责人 | 企业总览 | 先看 portfolio attention |
| 站点能源经理 | 站点总览 | 单站点运营+能源综合判断 |
| HVAC 值班工程师 | 系统运行 | 先看实时运行 |
| 维修负责人 | 工单中心 | 先看责任、SLA、下一动作 |
| 告警值班员 | 告警中心 | Active triage 是核心任务 |
| 控制工程师 | 控制中心 / 策略中心 | 根据职责 |
| 数据工程师 | 数据质量 | 先看数据健康 |
| 管理层 | 企业总览 / 管理评审 | 根据日常 vs 周期评审 |

如果一个用户拥有多角色，使用上次合法访问的主工作区；不要每次登录弹“选择角色”。

---

# 6. Global Shell Contract

## 6.1 Shell 永久元素

桌面端 Shell 至少包含：

```text
Sidebar
Top Bar
  Organization / Site Context
  Global Search / Command
  Active Time Context indicator（分析页适用）
  Notifications / Tasks
  Help
  User
Main Content
```

## 6.2 Site Selector

Site 是绝大多数业务页面的一级上下文。

规则：

- Site 数量 ≤ 15：Select 可以接受。
- Site 数量 > 15：必须使用 searchable combobox。
- Portfolio 用户可以切换“全部站点 / 业务组 / 单站点”。
- 从单站点页面切到另一站点时，保留兼容的页面和时间上下文。
- 如果目标站点没有当前 Surface capability，导航到该站点最接近的合法父 Surface，并明确提示 capability 不存在。

禁止：切 Site 后静默展示上一个 Site 的缓存事实。

## 6.3 Global Search / Command Palette

支持搜索：

- Site
- Building / System
- Device
- Alarm
- Diagnosis
- Work Order
- Opportunity
- Strategy

结果必须显示类型和业务位置，例如：

```text
CH-02
冷水机组 · 东京中央冷站 / 冷源系统
```

不能只显示对象名。

搜索结果进入 durable route，不进入临时 Drawer。

---

# 7. URL / Router State Ownership Contract

## 7.1 Path 负责 durable identity

Path 用于“分享后仍应该指向同一个业务对象”的身份。

示例意图：

```text
/sites/:siteId/overview
/sites/:siteId/operations
/sites/:siteId/devices
/sites/:siteId/devices/:deviceId
/sites/:siteId/alarms
/sites/:siteId/diagnostics
/sites/:siteId/work-orders
/sites/:siteId/work-orders/:workOrderId
/sites/:siteId/energy
/sites/:siteId/efficiency
/sites/:siteId/opportunities
/sites/:siteId/strategies/:strategyId
```

内部 UUID 可以作为 route identity，但页面标题、breadcrumb、button 必须显示业务名称。

## 7.2 Search Params 负责可恢复分析上下文

应进入 URL 的状态：

```text
timeStart / timeEnd
compare
baselineVersion
system
assetType
status
severity
owner
sort
page
selectedId（当 Inspector selection 值得分享）
metric
normalization
```

进入 URL 的判断标准：

> 刷新页面、复制链接、在新标签打开以后，如果用户合理期待状态仍然存在，就应该进入 URL。

## 7.3 Local UI State 不进入 URL

以下通常只属于 local state：

- tooltip open
- hover
- accordion 临时展开
- column resize pixel width
- modal 输入到一半但尚未提交
- temporary selection used only for chart highlighting

## 7.4 Saved Views

Ledger / Analysis Workspace 可以保存：

- filters
- sort
- visible columns
- grouping
- time preset（如果语义稳定）

Saved View 是显式用户对象，不等于浏览器 localStorage 隐式记忆。

---

# 8. Cross-Page Context Envelope

所有跨页调查使用统一概念：

```ts
NavigationContext = {
  site?: SiteIdentity
  portfolioScope?: ScopeIdentity
  timeRange?: { start, end, timezone }
  object?: { type, id }
  compare?: { type, reference }
  baselineVersion?: string
  source?: { surface, object?, label? }
  investigation?: { id?, evidenceWindow? }
}
```

这不是要求前端真的创建一个巨型对象；它定义产品语义。

## 8.1 Site Context

除企业/portfolio 页面外，任何业务事实必须有 Site owner。

## 8.2 Time Context

以下跳转默认保留时间窗口：

```text
Energy → Demand → Efficiency → Trend → Diagnosis
System Operations → Trend → Diagnosis
Device Detail → Trend
M&V → Trend / Energy
Functional Verification → Trend
```

如果目标页面不支持原始粒度，可以调整粒度，但不能静默扩大/缩小时间边界。

## 8.3 Object Context

例如 Alarm → Diagnosis：

```text
site
alarmId
asset/device
occurrence window
source=alarm
```

Diagnosis 页面应自动聚焦相关 Investigation/Finding；不能要求用户重新搜索设备。

## 8.4 Compare Context

Energy → Efficiency 时保留：

- actual period
- comparison type
- baseline version
- normalization mode（如果兼容）

如果下游不支持某种 comparison，应明确显示“该比较在本分析中不适用”。

---

# 9. Breadcrumb 与 Return-to-Source

## 9.1 Breadcrumb 只表示 hierarchy

W3C/USWDS 都把 breadcrumb 定义为当前页面在层级结构中的位置。

正确：

```text
东京中央冷站 / 设备 / CH-02
```

错误：

```text
告警 → 诊断 → 工单 → 验证
```

后者不是 hierarchy，而是 investigation workflow。

## 9.2 Investigation Trail 单独表示工作流

在 Diagnosis / Work Order / Functional Verification 可显示：

```text
来源
高温差告警
→ 诊断：换热性能偏离
→ 工单：现场复核 CH-02
→ 当前：功能验证
```

这叫 Investigation Trail，不叫 Breadcrumb。

## 9.3 Return to Source

跨页进入时，页面头部提供语义化返回：

```text
← 返回 CH-02 告警
```

而不是只依赖浏览器 Back。

如果 source URL 已失效，则回到当前对象的稳定父页面。

---

# 10. Inspector / Detail / Modal / Tab 的边界

## 10.1 Context Inspector

用于：

- 快速判断
- 保持列表/拓扑/图表上下文
- 读取 5–10 个关键事实
- 提供下一步 deep-link

典型内容：

```text
对象身份
关键状态
最近值
为什么值得关注
1–3 个下一动作
```

Inspector 不承载：

- 长表单
- 完整历史
- 多阶段工作流
- 复杂审批
- 原始点位全集

## 10.2 Durable Detail

当用户需要：

- 持续调查
- 分享链接
- 多 section 信息
- 长历史
- 多个 domain references

必须使用独立 Route。

## 10.3 Modal Dialog

只用于阻断性、短、明确的任务：

- ACK
- Assign
- 高风险控制二次确认
- Approve / Reject
- Create Work Order 的最小表单

Dialog 打开时必须：

- focus 进入 dialog
- focus trap
- Escape / Cancel 明确
- 关闭后焦点回 trigger

不使用 Modal 展示完整设备详情或长分析。

## 10.4 Tabs

Tabs 只用于“同一对象、同一层级、互斥显示的 peer views”。

适合：

```text
Active / Recovered
Actual / Normalized（如果同一分析上下文）
Overview / History（同一对象）
```

不适合：

```text
设备 / 告警 / 工单 / 能源
```

这些是不同 durable jobs，应是 Route。

---

# 11. Ledger / Table Interaction Contract

适用：设备、告警、工单、机会、策略、执行记录。

## 11.1 默认结构

```text
Page title + compact summary
Toolbar
  Search
  Primary filters
  Saved view
  Optional advanced filters
Ledger/Table
Context Inspector（选中时）
```

## 11.2 默认列数量

目标：首屏显示完成扫描任务所需的核心列。

默认避免超过约 7–9 个业务列；额外列进入 Column Settings。

这是产品密度目标，不是技术限制。

## 11.3 Table vs ARIA Grid

优先 native semantic table。

只有在需要：

- spreadsheet-like cell navigation
- inline edit
- row/cell multi-selection
- keyboard arrow navigation

才采用 Grid interaction model，并完整实现 W3C 键盘语义。

## 11.4 Row Click

Row selection 默认更新 Inspector；不得让整行同时有多个不可预测 click target。

建议：

- 单击行 → select / Inspector
- 明确名称链接或“打开详情” → Durable Detail

## 11.5 Filters

默认只展示 3–5 个高频 filters。

其他进入 `更多筛选`。

Filters 必须有：

- active count
- clear all
- 可读 chip/summary

禁止打开页面就展示两排 15 个筛选器。

---

# 12. Analytical Workspace Contract

适用：Benchmarking、Trend、Energy、Demand、Efficiency、M&V。

统一层级：

```text
Context Controls
Headline Facts
Primary Analytical Canvas
Breakdown / Contributors
Events / Evidence
Selected Context / Drill-down
```

## 12.1 Context Controls

时间、比较、范围放在页面上方稳定位置。

改变 filter 不应无提示改变 measurement definition。

## 12.2 Primary Chart

每页只有一个 primary analytical question。

例如 Energy：

> 何时出现显著负荷变化，哪些对象贡献最大？

不是同时堆 8 张同权重图。

## 12.3 Legend / Units

- unit 必须在轴或 metric label 中显式存在。
- Actual / Baseline / Forecast 用视觉+文字双重区分。
- 颜色不能是唯一语义编码。

## 12.4 Drill-down

Chart selection 可以产生一个时间窗口 / contributor context，然后进入：

```text
Trend
Device
Diagnosis
Opportunity
```

下钻不应该清空上游 context。

---

# 13. Portfolio Benchmarking Interaction Contract

ENERGY STAR 的实践表明 Benchmarking 应先用于筛选，再下钻原因。

默认页面：

```text
Scope: Portfolio / Region / Business Unit
Metric: EUI / EnPI / Cost / Emissions
Compare: Peer / Baseline / Target

Distribution / ranking
Site table
Outliers
Trend
```

点击低效站点：

```text
→ Site Overview
→ Energy Analysis
→ Efficiency Analysis
```

禁止在 Ranking 页直接给“根因结论”。

Benchmarking score / normalized metric 必须展示 method / eligibility / coverage。

---

# 14. System Operations Interaction Contract

系统运行是高频 HMI，不是普通 Analytics Dashboard。

## 14.1 Screen hierarchy

```text
Site
→ HVAC System Domain
→ System / Loop
→ Equipment
→ Point / Trend
```

用户必须始终知道当前层级。

## 14.2 Persistent system selector

冷源 / 冷冻水 / 冷却水 / 末端之间切换时：

- 保留 Site
- 保留 selected time intent（current/live）
- 不保留不兼容设备选择

## 14.3 状态编码

高性能 HMI 原则：正常状态保持视觉安静；颜色优先用于 abnormal / action-needed，不让整个画布充满彩色状态块。

## 14.4 Topology

Topology 仅用于真实系统关系。

点击节点：Inspector；打开设备：Detail。

不存在 flow/relationship owner 时禁止动画“水流”暗示真实物理状态。

---

# 15. Alarm Interaction Contract

## 15.1 默认入口

默认 `Active`，不是 History。

## 15.2 页面职责

Alarm Center 负责：

- triage
- awareness
- assignment
- suppression / handling（真实能力）
- evidence handoff

Rule configuration 在 Rule Administration。

## 15.3 Primary sort

默认按业务紧迫度，而不是简单 created_at DESC。

排序可综合：

```text
severity
active duration
unacknowledged
unowned
repeat/escalation
```

具体规则由 Alarm domain contract 定义。

## 15.4 状态分离

单独字段：

- physical condition
- handling/ack
- owner
- suppression

不使用一个 `Status = Open/Closed` 混合表达。

## 15.5 Alarm → Diagnosis

保留：

- alarm
- asset
- occurrence window
- evidence links

Diagnosis 不自动宣称 root cause。

---

# 16. Diagnosis Interaction Contract

默认先看：

```text
问题是什么
已验证事实
影响对象
Finding
下一验证步骤
```

专业层再展开：

```text
Hypotheses
Supporting evidence
Contradicting evidence
Confidence semantics
Rule/model metadata
Raw evidence
```

主要动作只允许：

- 查看趋势
- 查看设备
- 创建/打开工单
- 开始功能验证
- 形成节能机会（满足条件时）

AI 草拟 hypothesis 必须标注来源，不能覆盖 authoritative finding。

---

# 17. Work Order Interaction Contract

## 17.1 Work Center

默认排序关注：

```text
Urgent
Overdue
Unowned
Blocked
Needs Verification
```

Next Action 比内部 workflow state 更重要。

## 17.2 Detail

Work Order Detail 必须显示来源链：

```text
Alarm / Diagnosis / Inspection / Optimization
```

完成时如果问题需要物理验证，必须设置 verification requirement。

`Completed` 与 `Verified Resolved` 是不同事实。

---

# 18. Functional Verification Interaction Contract

DOE/ASHRAE 的 commissioning 实践要求 Functional Test 和结果验证独立存在。

默认 Queue：

- failed
- inconclusive
- due for retest
- newly changed sequence

单个 Verification：

```text
Requirement
Test Conditions
Expected Behavior
Observed Behavior
Evidence
Pass / Fail / Inconclusive
Corrective Action
Retest
History
```

Functional Verification 可以来自：

- Work Order completion
- Strategy rollout
- Optimization Plan
- Commissioning schedule
- periodic recommissioning

---

# 19. Energy / Demand / Efficiency Interaction Contract

## 19.1 Energy

默认回答：用了多少、何时、哪里。

## 19.2 Demand

默认回答：峰值何时出现、谁贡献、是否可削峰/移峰。

## 19.3 Efficiency

默认回答：同等负荷下是否高效、哪个子系统/设备偏离。

三个页面共享：

```text
site
period
comparison
baselineVersion
```

但不要用 Tabs 把三者塞在一个页面，因为它们是不同 durable analytical jobs。

---

# 20. Energy Review Interaction Contract

Energy Review 是正式能源管理 Workspace，不是 Dashboard。

Operational View：

- Significant Energy Uses
- Energy Performance Indicators
- Baseline health
- top deviations
- open opportunities

Advanced：

- SEU definitions
- relevant variables
- EnPI formula
- EnB model/version
- normalization
- effective period
- owner

用户不需要先懂 EnPI/EnB 才能阅读主视图。

---

# 21. Utility Bill / Cost Interaction Contract

只在真实 billing/tariff capability 存在时出现。

页面分两个 peer views：

```text
Bills
Cost Analysis
```

Bills：

- billing period
- actual/estimated
- utility/account
- reconciliation
- anomaly

Cost Analysis：

- tariff
- demand charge
- TOU
- cost breakdown
- contributors

Bill correction 是 durable workflow，不放在 Energy Analysis 的小 Dialog 中。

---

# 22. Opportunity → Optimization → Control Contract

## 22.1 Opportunity

只表示值得评审的改善机会。

## 22.2 Optimization Plan

表示工程方案：

- proposed change
- constraints
- comfort/reliability impact
- expected benefit
- simulation
- rollback
- approval

## 22.3 Control / Strategy

只有计划已审批且满足权限、安全和 capability 时才能生成控制执行。

页面之间必须显式显示状态转变，而不是一键从“发现机会”直接“执行”。

---

# 23. Control / Strategy Interaction Contract

高风险控制动作必须使用 Modal confirmation，并包含：

- Target
- Current state
- Proposed state
- Impact scope
- Preconditions / interlocks
- Expiry（override）
- Confirmation

提交后不要显示“控制成功”，而应进入：

```text
Requested
→ Sent/Attempted
→ ACK
→ Readback
→ Verified
```

用户可从 Control Center 打开 Execution Record。

---

# 24. M&V Interaction Contract

DOE FEMP M&V 与 IPMVP 原则决定：Verified Savings 不能只用 `baseline - actual` 的简单差值表达。

默认运营视图：

- project
- reporting period
- verified savings
- M&V status
- persistence/snapback indicator

Advanced：

- measurement boundary
- baseline period
- reporting period
- model
- independent variables
- routine adjustments
- non-routine adjustments
- exclusions
- uncertainty/model quality
- operational verification evidence

M&V 与 Functional Verification 必须互相链接，但不能合并成一页。

---

# 25. Data Quality / Semantic Model Interaction Contract

数据问题必须能回答：

```text
哪里坏了？
什么时候开始？
影响什么？
谁负责？
修复以后哪些分析要重新计算？
```

Data Quality deep-link 应携带：

- source object
- metric/point/meter
- time window
- impacted calculation

Semantic Model 编辑属于管理员/数据工程师权限；普通用户可以查看 lineage，但不能看到复杂配置表单。

---

# 26. Capability Gating 与权限交互

## 26.1 不显示 vs Disabled

产品能力不存在：**不显示**。

用户能发现但无操作权限：页面可见，Action disabled/hidden 依据安全策略，并提供权限解释。

对象当前条件不允许执行：Action disabled，并显示业务原因：

```text
设备离线，无法远程控制
等待数据恢复后可执行
当前策略审批中
```

不能只写“按钮不可用”。

---

# 27. Loading / Empty / Error / Partial Contract

所有页面至少区分：

- Loading
- Ready with data
- Ready empty
- Partial
- Stale
- Suspect
- Owner unavailable
- Not integrated
- Not authorized
- Request failed

示例：

错误：

```text
暂无数据
```

正确：

```text
能耗数据已更新，但基线服务暂不可用。
实际用电仍可查看；相对基线偏差暂不计算。
```

Partial state 必须局部降级，不把整个页面变 Error。

---

# 28. Narrow / Tablet Contract

智慧能源系统主要是桌面工作应用，但窄屏必须可完成核心任务。

规则：

- Sidebar → overlay/collapsible navigation。
- Inspector → full-width Sheet 或 inline section。
- Table 保留核心 3–5 列，其余进入 row detail。
- Primary chart 可横向压缩，但不能让单位/legend 不可读。
- 高风险 control 不因为移动端而简化确认信息。
- 不强求大型 topology 在手机上完整操作；提供 list/facts alternative。

---

# 29. Keyboard / Accessibility Contract

## 29.1 Global

- 页面有唯一主要 heading。
- Navigation 有 landmark。
- Breadcrumb 有 aria-label。
- 当前 breadcrumb 使用 `aria-current=page`。
- Focus visible。

## 29.2 Dialog

遵循 W3C modal dialog pattern：

- focus enters dialog
- Tab trapped
- Escape closes（除非安全流程明确禁止）
- close/cancel visible
- close 后 focus 返回 trigger

## 29.3 Data table

普通可扫描表格使用 native table semantics。

只有真正 interactive data grid 才使用 grid，并实现 Arrow/Home/End 等键盘模式。

## 29.4 Command Search

Combobox 必须支持 keyboard selection，不能依赖鼠标 hover。

---

# 30. 页面设计前的 Interaction Checklist

任何 Surface Specification 必须回答：

1. Primary Job 是什么？
2. Primary Entry 从哪里来？
3. Primary Exit 去哪里？
4. 必须保留哪些 Context？
5. 哪些状态进入 Path？
6. 哪些状态进入 Search Params？
7. 哪些状态只属于 Local UI？
8. 是否需要 Inspector？
9. 是否需要 Durable Detail？
10. 哪些动作必须用 Dialog？
11. 默认 Operational View 展示什么？
12. Engineering Detail 如何渐进展开？
13. capability 不存在时页面如何消失？
14. permission 不足时怎么解释？
15. Loading / Partial / Stale / Error 怎么区分？
16. Narrow layout 的主要任务是否仍可完成？
17. Keyboard / screen reader 是否能完成核心任务？

没有回答完，不进入 Wireframe。

---

# 31. 推荐 Route Intent

这只是 Greenfield route intent，不是旧路由兼容要求。

```text
/portfolio/overview
/portfolio/benchmarking

/sites/:siteId/overview
/sites/:siteId/operations
/sites/:siteId/trends
/sites/:siteId/devices
/sites/:siteId/devices/:deviceId
/sites/:siteId/comfort

/sites/:siteId/alarms
/sites/:siteId/diagnostics
/sites/:siteId/work-orders
/sites/:siteId/work-orders/:workOrderId
/sites/:siteId/verifications
/sites/:siteId/verifications/:verificationId

/sites/:siteId/energy
/sites/:siteId/demand
/sites/:siteId/efficiency
/sites/:siteId/energy-review
/sites/:siteId/billing
/sites/:siteId/carbon
/sites/:siteId/der

/sites/:siteId/opportunities
/sites/:siteId/optimization-plans
/sites/:siteId/optimization-plans/:planId
/sites/:siteId/action-plans
/sites/:siteId/action-plans/:planId
/sites/:siteId/mv
/sites/:siteId/mv/:mvProjectId

/sites/:siteId/control
/sites/:siteId/strategies
/sites/:siteId/strategies/:strategyId
/sites/:siteId/strategies/:strategyId/versions/:version
/sites/:siteId/executions
/sites/:siteId/executions/:executionId

/sites/:siteId/reports
/sites/:siteId/reports/:reportId
/sites/:siteId/report-definitions/:definitionId
/sites/:siteId/management-reviews
/sites/:siteId/management-reviews/:reviewId

/sites/:siteId/data-quality
/sites/:siteId/data-quality/issues/:issueId
/sites/:siteId/model
/sites/:siteId/model/:entityId
/settings/rules
/settings/rules/:ruleId
/settings/integrations
/settings/integrations/:integrationId
/settings/sites
/settings/sites/:siteId
/settings/access
/settings/access/principals/:principalId
/settings/access/roles/:roleId
/settings/access/audit/:auditEventId
```

是否采用这些 path 需要在 Router 实施前根据最终 domain identity 再确认，但页面层级和 context 责任不应改变。

---

# 32. 关键跨页跳转矩阵

| From | To | 必须携带 |
|---|---|---|
| 企业总览 | Benchmarking | portfolio scope + metric + period |
| Benchmarking | 站点总览 | site + benchmark period/reference |
| 站点总览 | 系统运行 | site + selected system（可选） |
| 系统运行 | 趋势 | site + device/system + points + evidence window |
| 系统运行 | 设备详情 | site + device |
| 告警 | 诊断 | site + alarm + device + occurrence window |
| 诊断 | 趋势 | evidence window + point/device context |
| 诊断 | 工单 | source diagnosis + affected objects + evidence summary |
| 工单详情 | 功能验证 | source work order + requirement + affected objects |
| 功能验证 | 趋势 | test window + points |
| 能源 | 需求负荷 | site + period + comparison |
| 能源 | 效率 | site + period + comparison + baseline |
| 效率 | 设备详情 | site + equipment + analysis window |
| 效率 | 节能机会 | evidence + period + affected system |
| 节能机会 | 优化方案 | opportunity + evidence + expected benefit + constraints |
| 优化方案 | 策略详情 | approved proposal + guardrails + scope |
| 策略详情 | 执行记录 | strategy + version + rollout window |
| 执行记录 | 功能验证 | execution + target + readback window |
| 优化方案 | M&V | plan + measurement boundary + expected savings |
| 功能验证 | M&V | operational verification evidence |
| 数据质量 | 计量/语义模型 | affected point/meter/object + issue |

---

# 33. Source Context 示例

用户从 Alarm 深入：

```text
Alarm: CH-02 冷冻水温差高
  ↓ 调查
Diagnosis: 换热性能偏离
  ↓ 建立现场任务
Work Order: 复核 CH-02 蒸发器
  ↓ 完成
Functional Verification: CH-02 ΔT / staging verification
```

整个链路顶部可以显示：

```text
来源：CH-02 冷冻水温差高告警
```

并保留一个 Investigation Timeline，而不是用 breadcrumb 模拟流程。

---

# 34. 用户易用性与内容设计目标

这些是产品内部验收目标，不是通用行业定律：

- 高频 Surface 应在一级 Sidebar 2 次交互内可达。
- 进入页面后 10 秒内可识别 Primary Job 和最重要事实。
- 日常动作默认不要求打开 Advanced 区域。
- 同一调查链内不重复选择 Site / Device / Time。
- 绝大多数 Ledger 扫描任务不需要横向滚动即可完成核心判断。
- 专业术语第一次出现时提供业务化解释。
- 所有高风险控制动作都能看到影响对象和当前状态。

## 34.1 默认视图必须回答用户问题，而不是暴露数据模型

区块标题优先使用：

```text
需要关注
当前运行
为什么异常
下一步
数据是否可信
已验证结果
```

避免把以下内部/工程名直接作为普通用户的首要导航：

```text
Projection
Read Model
Payload
Revision Hash
Correlation
Capability Matrix
```

这些信息进入 Advanced / Audit / Evidence。

## 34.2 异常内容必须同时给“事实 + 原因 + 下一步”

禁止只有：

```text
异常
失败
不可用
按钮不可用
```

推荐：

```text
基线服务暂不可用
实际用电仍可查看；相对基线偏差暂不计算。
[查看数据状态]
```

或者：

```text
当前无法远程控制
原因：设备处于 Local 模式
下一步：确认现场控制模式后重试
```

## 34.3 空状态必须区分“没有事情”和“没有数据”

```text
当前没有活动告警
```

与：

```text
告警服务暂不可用，当前告警状态未知
```

必须是两种不同 UI。

同理：

```text
0
≠ 缺失
≠ 未接入
≠ 未计算
≠ 无权限
```

## 34.4 默认只展示完成当前任务需要的信息

遵循三层渐进披露：

```text
Operational View
→ Professional Detail
→ Raw Evidence / Audit
```

默认层优先展示：

```text
对象
状态
影响
时间
业务原因
责任
下一步
```

以下信息默认不抢占主界面：

```text
UUID
trace / correlation ID
raw enum
schema name
model hash
internal revision ID
protocol address
```

## 34.5 动作名称必须具体

优先：

```text
查看能源分析
创建工单
提交审批
释放临时覆盖
确认将 CHWS 设定值调整到 7.2°C
```

避免：

```text
详情
处理
操作
确定
执行
```

当动作存在不可逆或高风险影响时，按钮文本必须包含动作对象或目标状态。

## 34.6 默认表格列服务“扫描判断”

默认列只保留完成主要扫描所需信息；低频工程字段进入 Column Settings / Detail。

任何 Ledger 在目标桌面宽度下，应能不依赖横向滚动完成核心判断；如果列持续增加，优先重新审查页面职责，而不是继续压缩字号。

## 34.7 实时更新不能破坏用户正在做的事情

实时刷新必须：

- 不抢 focus；
- 不关闭用户正在使用的 Popover / Dialog / Inspector；
- 不重置筛选、分页、展开状态；
- 不因 stream tick 频繁重排行；
- 不把用户正在阅读的证据自动滚走；
- 重要状态变化通过 WCAG-compatible status message 或明确视觉更新表达。

## 34.8 状态不能只靠颜色

Alarm、Risk、Quality、Verification、Execution 等状态至少使用：

```text
文字
图标 / 形状 / 标签
必要时颜色
```

颜色只作为冗余编码，不是唯一编码。这与 ISA-101 的 situational-awareness 目标和 WCAG 2.2 可理解/可操作要求一致。

## 34.9 专业性不等于英文密度

主界面中文优先；行业标准缩写用于保持精度，例如：

```text
能源绩效指标（EnPI）
能源基线（EnB）
节能量验证（M&V）
室内空气质量（IAQ）
```

不允许通过大面积英文、底层字段名或协议名营造“专业感”。

## 34.10 用户友好不等于隐藏专业事实

专业事实不能为了“简单”被合并成错误状态。例如：

```text
Offline ≠ Fault
Stale ≠ Offline
ACK ≠ Cleared
Expected ≠ Verified
Delivered ≠ Read
Approved ≠ Executed
```

易用性的目标是**更容易理解正确事实**，不是减少事实精度。

## 34.11 Accessibility baseline

Wireframe 和实现至少按 WCAG 2.2 AA 方向验收：

- 核心任务可键盘完成；
- Focus visible 且不会被 sticky UI 完全遮挡；
- 状态变化可由 assistive technology 感知；
- heading / label 描述真实目的；
- Navigation 保持一致；
- 图表有文本或表格等价内容；
- hover 不是获取关键信息的唯一方式。

---

# 35. 最终结论

新的智慧能源系统导航不能被理解成“36 个页面如何排菜单”。

真正的产品结构是：

```text
Surface Catalog
+ Role / Capability Projection
+ Stable Global Shell
+ Durable Route Identity
+ Restorable Analysis Context
+ Inspector → Detail Progressive Disclosure
+ Explicit Investigation Trail
+ Safe Action Dialogs
+ Evidence-preserving Cross-page Navigation
```

用户看到的是简洁、稳定、业务化的入口；专业深度通过同一上下文逐层展开。

这套导航与上下文规则与 `smart-energy-system-page-architecture-v2.md` 一起构成后续所有页面设计的上游产品契约。
