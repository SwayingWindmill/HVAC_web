# 03 站点总览 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `03 站点总览`  
> **Route intent：** `/sites/:siteId/overview`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Overview/Dashboard 页面、旧菜单、旧路由、旧设计稿或旧组件构图。现有代码只可在实施阶段作为真实数据 owner / capability / permission 的候选证据来源。

---

# 1. Primary Job

站点总览的唯一核心任务是：

> **让站点能源经理或综合运营负责人在 30–60 秒内判断“这个站点现在最值得处理什么”，并进入正确的专业工作区继续处理。**

它是 **attention router**，不是：

- 缩小版 BMS；
- KPI Dashboard；
- 所有模块快捷入口墙；
- 故障诊断页；
- 能源分析页；
- 控制台；
- 管理报表页。

用户离开本页时，应该已经知道：

1. 当前站点是否存在必须优先关注的问题；
2. 问题属于运行、告警、工单、能源、效率、数据、舒适还是验证哪个业务域；
3. 为什么这个问题值得关注；
4. 下一步应该进入哪个负责该事实的 Workspace。

---

# 2. 主要用户

## Primary

### 站点能源经理

需要同时理解运行、能源、效率、维护和节能机会，但不需要在首页完成任何专业调查。

### 综合运维负责人 / 值班负责人

需要快速判断当前运行风险、未完成责任和需要升级处理的问题。

## Secondary

- HVAC 工程师：从总览进入系统运行、趋势、设备或诊断。
- 维修负责人：从总览进入工单。
- 企业能源负责人：从 Portfolio / Benchmarking 下钻单站点。
- 管理层：偶尔查看单站点状态，但不以此页替代管理评审。

## 不作为主要目标用户

- 数据工程师：默认入口应是数据质量。
- 控制工程师：默认入口应是控制 / 策略中心。
- 告警值班员：默认入口应是告警中心。

---

# 3. 外部最佳实践依据

## 3.1 DOE / FEMP — Overview 必须帮助 Identify & Prioritize

DOE 对 EMIS 推荐运营过程：

```text
Identify and Prioritize
→ Validate, Diagnose, and Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor, Update, and Maintain
```

DOE 同时强调 EMIS 是 human-in-the-loop 工具；软件本身不能替代团队判断、实施和验证。

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/sites/default/files/2022-02/best-practices-to-support-EMIS-operation-federal-facilities.pdf

**本页采用：**

- 首页优先显示“需要注意什么”，而不是模块数量或数据量；
- 重要项必须带可理解的优先理由；
- 首页负责路由到 Validate / Diagnose / Correct / Verify 的专业页面，不在首页完成整个闭环。

## 3.2 ENERGY STAR — Benchmarking 负责筛选，不负责解释根因

ENERGY STAR 将 Portfolio benchmarking 用于：

- 识别低效建筑；
- 与历史、目标、同类建筑比较；
- 确定投资优先级；
- 验证改善。

同时明确 1–100 Score 是 screening tool，不能解释建筑为什么表现好或差。

来源：

- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results

**本页采用：**

- 从 Benchmarking 进入站点总览时，可保留 benchmark reference 作为来源上下文；
- 站点总览不能继续展示一堆 portfolio ranking；
- 需要解释能耗和效率原因时必须进入 Energy / Efficiency / Trend / Diagnosis。

## 3.3 ISA-101 — 运行首页首先服务 situational awareness

ISA-101 HMI 范围包括 menu hierarchy、screen navigation、graphics/color conventions、alarming conventions、popup conventions、historical interfaces 等，目标包括提高操作员对过程状态的理解和决策效率。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101
- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards

**本页采用：**

- 正常事实保持视觉安静；
- 异常和需要行动的内容拥有视觉优先级；
- 颜色不是唯一状态编码；
- 信息层级稳定，不因为实时数据变化导致版面跳动或模块重排。

## 3.4 ASHRAE Guideline 36 — 运行状态与专业验证要连续但不混在首页

Guideline 36-2024 的目标包括 HVAC energy efficiency、control stability、real-time FDD，以及 functional tests confirming sequences of operation。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- 当前运行模式和系统状态值得在首页摘要；
- sequence、setpoint、interlock、FDD evidence、functional test 不在首页展开；
- 对应专业调查进入 System Operations / Diagnosis / Functional Verification。

## 3.5 Siemens Building X Operations Manager — 汇总状态后继续下钻

公开产品能力强调：

- centralized view of connected buildings；
- real-time equipment and event visibility；
- interactive charts drill down to equipment；
- comfort visibility；
- work-order integration；
- fault investigation。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- 站点总览可以同时汇总运行、事件、舒适和工作事实；
- 详细历史、设备级数据和故障调查必须继续下钻。

## 3.6 Schneider EcoStruxure Building Operation — Overview 可组合 energy / alarm / environmental summary

EcoStruxure WebStation 的公开说明中，Dashboard 用于 overview，并可以清晰呈现 basic energy consumption、alarm statistics、environmental summaries；专业功能继续进入 alarm、trend、graphics 等工作区。

来源：

- https://sqa.ecostruxure-building-help.se.com/bms/Topics/show.castle?id=8792&locale=en-US&productversion=7.0

**本页采用：**

- 能源、告警、环境摘要可以共存，但不能扩展成每个领域的完整工具；
- 各摘要必须有明确 deep link。

---

# 4. Primary Questions

用户进入后按以下顺序回答问题：

## Q1 — 现在有什么必须优先处理？

这是页面最高优先级。

用户需要看到：

- 严重 / 高优先级活动告警；
- overdue / unowned / blocked 的重要工单；
- failed / inconclusive 且需要复测的功能验证；
- 对业务判断有实质影响的数据质量问题；
- 已由权威分析 owner 判定的显著能源 / 效率 / 舒适偏差。

## Q2 — HVAC 当前大体如何运行？

只回答宏观运行事实：

- 当前 operating mode；
- 主要系统域是否运行；
- 关键设备群运行数量；
- 当前站点需求 / 功率等一两个适用主事实；
- 是否存在当前运行异常。

不回答 sequence 细节。

## Q3 — 能源和效率是否有值得继续分析的偏差？

用户应能知道：

- 当前 / 本业务日实际能源；
- 当前 demand / power；
- 相对一个**明确命名**比较口径的偏差；
- 当前适用的核心系统效率指标；
- 是否存在明确异常时间段。

## Q4 — 有没有值得进入持续改进流程的节能机会？

首页只显示：

- 证据足够；
- 优先级较高；
- 有明确 expected benefit / risk / next action 的机会。

不在此页展示完整 calculation basis。

## Q5 — 最近发生了哪些真正改变站点判断的重要变化？

例如：

- 重大告警触发 / 恢复；
- 关键工单进入完成 / 待验证；
- 策略发布；
- 控制执行结果；
- 功能验证 Pass / Fail；
- 重要数据源中断 / 恢复。

普通点位数值每次刷新不属于“最近变化”。

---

# 5. Non-goals

站点总览明确不负责：

- 展示完整 HVAC 拓扑；
- 展示所有设备；
- 展示完整告警列表；
- ACK / Assign / Suppress 告警；
- 编辑或完成工单；
- 执行控制；
- 编辑策略；
- 完成根因诊断；
- 展示完整能源分析；
- 展示完整效率模型；
- 配置 baseline / EnPI；
- 展示 raw points；
- 展示内部 ID；
- 做企业级 Benchmarking；
- 通过一个“站点健康分”合并所有状态。

如果某个功能只有通过在首页增加复杂交互才能完成，优先将用户送到负责该任务的专业 Surface。

---

# 6. Entry Points

## 6.1 默认入口

站点能源经理登录后的默认入口。

## 6.2 从 Portfolio / Benchmarking 进入

必须携带：

```text
site
source = portfolio / benchmarking
benchmark period（如适用）
benchmark reference / metric（如适用）
```

站点总览可以显示一个轻量来源提示，但不把 Benchmarking 的排序 UI 带进首页。

## 6.3 Site Selector

从其他 Site Surface 切换站点后，可落在对应站点总览。

## 6.4 Global Search

搜索 Site 后进入站点总览。

## 6.5 不建议入口

告警、设备、工单等对象级通知不应先经过站点总览；应直接进入对象所属 durable workspace。

---

# 7. Exit Paths

| 用户看到的事实 | 目标 Surface | 必须携带 |
|---|---|---|
| 当前系统运行异常 | 系统运行 | `site` + `system`（若已知） |
| 严重活动告警 | 告警中心 | `site` + severity/state filters，或具体 alarm context |
| overdue / unowned 工单 | 工单中心 | `site` + work filters |
| 设备问题 | 设备详情 | `site` + device |
| 需要趋势证据 | 趋势分析 | `site` + object/metric + evidence window |
| 能源偏差 | 能源分析 | `site` + named period + comparison/baseline |
| 效率偏差 | 效率分析 | `site` + period + system + comparison |
| Comfort/IEQ 异常 | 舒适与室内环境 | `site` + zone/system + window |
| 数据可信度问题 | 数据质量 | `site` + impacted metric/source + window |
| 高价值机会 | 节能机会 | `site` + opportunity |
| 待复测 / 功能失败 | 功能验证 | `site` + verification context |
| 控制/策略重要变化 | 执行记录 / 策略详情 | 对应 durable identity |

所有跳转必须保持站点上下文；能量/效率/趋势类跳转还必须保留分析时间和比较口径。

---

# 8. Route / URL State Ownership

## Path

```text
/sites/:siteId/overview
```

`siteId` 是本页唯一必须的 durable route identity。

## Search Params

默认**不设计复杂 Search Params**。

理由：站点总览是“现在的注意力路由页”，不是分析 Workspace。

允许的 future-safe 参数只有在真实用例出现后再引入，例如明确的 `source`/`focus` deep-link；不预先创建一组没人使用的 query params。

## 不进入 URL

- section collapse；
- hover；
- auto-refresh state；
- 当前动画/图表 hover；
- 本页临时视觉选择。

## 不保留旧路由兼容

本文件定义 greenfield route intent，不要求为了历史 `/dashboard` 或旧 Overview 路由设计兼容层。

---

# 9. 页面信息层级

页面采用 6 个区域，按业务优先级排列。

```text
1. Page Context
2. Site Facts Strip
3. Priority Attention + Current Operation
4. Energy & Performance Evidence
5. Savings / Comfort / Data follow-up
6. Recent Meaningful Changes
```

## 9.1 Page Context

内容：

- Site business name；
- `站点总览` h1；
- Site timezone；
- 当前数据更新时间 / realtime status（如果真实 owner 提供）；
- Refresh（仅在确有手动 refresh 语义时）。

Site Selector 属于 Global Shell，不在页面正文重复一个大型站点选择器。

## 9.2 Site Facts Strip

使用**一个连续 facts band**，不是四张独立 KPI Card。

最多 4 个 headline facts：

1. **重要活动告警**
2. **需要关注的工作**
3. **当前站点需求 / 功率**（若真实能源 capability 存在）
4. **关键数据可信度**（若存在权威 data-quality/readiness owner）

可按站点 capability 替换第 3/4 项，但 headline 数量不增长。

Facts 必须是 authoritative facts，不显示装饰性 trend arrows。

### “需要关注的工作”定义

不是所有工单数量。

只包含类似：

- overdue；
- urgent/high priority；
- unowned 且超过业务阈值；
- blocked 且影响关键任务；
- completed but requires verification（若该域定义为需关注）。

这些条件必须来自 Work Order domain contract，不在 UI 临时创造状态。

## 9.3 Priority Attention

这是页面视觉和任务主导区域。

### 允许进入的领域

- Alarm
- Work Order
- Functional Verification
- Energy / Efficiency deviation
- Comfort/IEQ violation
- Data Quality incident

### 默认不把 Savings Opportunity 混进紧急队列

节能机会是持续改进任务，紧迫性语义和运行风险不同，应单独呈现。

### 每条 Attention Item 必须回答

```text
发生了什么
影响哪个业务对象 / 系统
为什么现在值得关注
发生 / 持续多久
当前责任（如果有）
下一步去哪
```

推荐行结构：

```text
[类型 / 严重性]  业务标题
影响 / 优先理由                              时间
对象 · 责任/状态                             查看告警 / 打开工单 / 查看分析
```

### 优先排序

首页不创建黑盒“AI 综合优先分”。

排序必须：

- 可解释；
- 基于 domain owner 已定义事实；
- 业务风险优先于视觉新鲜度。

推荐排序原则：

1. safety / critical domain events；
2. critical/high active alarms with duration/ack/owner urgency；
3. urgent/overdue/blocked work；
4. failed/inconclusive required verification；
5. significant verified operational/energy/comfort deviation；
6. data incidents that make important decisions unreliable。

同一层级的具体排序由对应 domain contract 决定，不在 Overview 前端复制业务逻辑。

### 数量

默认显示 4–6 条。

超过时使用 `查看全部` 进入对应专业工作区，不在首页做分页。

## 9.4 Current Operation

目标：提供 situational awareness，不做完整 System Operations。

推荐按系统域呈现 compact rows，例如：

```text
冷源        制冷运行 · 3/4 台运行     1 项重要异常
冷冻水      自动压差控制              运行
冷却水      2 泵 / 3 塔运行           正常
末端        主要区域供冷              2 个区域需关注
```

每行最多提供：

- system/domain name；
- current operating mode；
- running/available population（只有真实定义时）；
- 一个最关键工程事实；
- 当前重要异常数量。

点击系统域进入 **System Operations**。

### 禁止

- 在 Overview 画完整 X6 拓扑；
- 装饰性水流动画；
- 展示 20+ 点位；
- 用“健康 92%”替代 runtime/connectivity/alarm/data 等真实维度。

## 9.5 Energy & Performance Evidence

这是首页唯一建议出现的主要趋势可视化区域。

### 默认回答

> 当前 / 本业务日能源表现是否偏离一个明确比较口径？

### 推荐内容

左侧 / 主区：

- site-local current day / selected operating period actual load profile；
- 一个 named comparison，例如：
  - 同类工作日基线；
  - 上一可比日；
  - approved baseline version。

右侧 / supporting facts：

- 当前 power / demand；
- site-local-day energy；
- applicable system COP / kW/RT 等 1–2 个核心效率事实；
- deviation amount / percentage（只有定义完整时）。

### 强制语义

- `Actual`、`Baseline`、`Forecast` 文字显式区分；
- 首页默认最多一个 comparison；
- 如果 baseline owner 不可用，Actual 仍然显示，但明确“基线暂不可用”，不退回其它比较口径；
- efficiency prerequisite 缺失时显示 `数据不足`，不显示 `0`；
- 业务日按 Site Timezone。

### Drill-down

- 图表 / `查看能源分析` → Energy Analysis；
- efficiency fact → Efficiency Analysis；
- 选定异常时段后（如果未来支持选择）→ 保留 time range 下钻。

## 9.6 Savings Opportunities

单独的持续改进区域，不与 Priority Attention 混排。

默认最多 3 条。

每条显示：

- opportunity title；
- affected scope；
- expected benefit（只有权威 calculation owner）；
- risk；
- review state；
- next action。

证据不足的 candidate 不允许通过视觉做成“推荐立即执行”。

`Expected Savings` 不得写成 `已节省`。

## 9.7 Comfort / IEQ

只有 Capability 存在时出现。

首页优先显示可行动的事实，例如：

- `3 个区域温度超出目标`；
- `CO₂ 目标超标 2 个区域`；
- `过去 2h 有 4 个区域持续偏离`。

不要求统一 Comfort Score；如果某站点已有权威 score，必须同时让用户能看到其定义或来源。

无 IAQ capability 时不显示空卡片。

## 9.8 Data Trust

数据可信度不是“平台技术状态”卡片。

用户只需要看到会影响当前业务判断的数据问题：

- critical meter stale；
- key flow sensor invalid；
- data gap affects COP；
- baseline calculation paused because input missing。

点击进入 Data Quality，并带上 impacted metric/source/time window。

## 9.9 Recent Meaningful Changes

只显示会改变用户判断的权威业务事件。

推荐最多 5–8 条：

```text
09:32  CH-02 高压告警恢复
09:18  冷源策略 v14 发布
08:55  WO-1842 完成，等待功能验证
08:20  冷冻水流量计恢复正常
```

### 不允许

- 把每个 telemetry refresh 当 activity；
- 将当前 snapshot 倒推成“发生过的事件”；
- 如果没有 authoritative event/audit owner 就拼接一个虚假的 recent feed。

---

# 10. Desktop Information Architecture

推荐 12-column desktop grid，但信息层级优先于具体列宽。

```text
┌──────────────────────────────────────────────────────────────────────┐
│ Site / 站点总览                                  更新 / 时区 / 状态 │
├──────────────────────────────────────────────────────────────────────┤
│ 重要活动告警 │ 需要关注工作 │ 当前需求/功率 │ 关键数据可信度       │
├─────────────────────────────────────┬────────────────────────────────┤
│ Priority Attention                  │ Current Operation              │
│                                     │                                │
│ 4–6 条可行动事项                    │ 冷源 / 输配 / 末端 compact rows│
│                                     │                                │
├─────────────────────────────────────┴────────────────────────────────┤
│ Energy & Performance Evidence                                        │
│ 主负荷曲线 + named comparison                  supporting efficiency │
├─────────────────────────────────────┬────────────────────────────────┤
│ Savings Opportunities               │ Comfort / Data Trust*         │
├─────────────────────────────────────┴────────────────────────────────┤
│ Recent Meaningful Changes                                             │
└──────────────────────────────────────────────────────────────────────┘

* only when capability / attention exists
```

## First viewport requirement

在典型 1440–1720px 桌面宽度下，首屏至少必须完整回答：

1. 当前 Site；
2. 是否有重要告警 / 工作；
3. 最优先的 3 条以上 Attention；
4. HVAC 当前宏观运行状态。

Energy & Performance 至少应该在首屏底部开始出现或紧接首屏，不要求为了“首屏塞满”压缩 Priority Attention。

---

# 11. Narrow / Tablet Layout

窄屏不是把 Desktop 两列缩小。

顺序固定：

```text
Page Context
Facts Strip（2×2 或水平可读布局；不做横向页面滚动）
Priority Attention
Current Operation
Energy & Performance
Savings Opportunities
Comfort / Data Trust
Recent Changes
```

## 768–1023px

- Facts 可 2×2；
- Priority 和 Current Operation 纵向堆叠；
- Energy 图表保持单位、legend 和 comparison 可读；
- 不隐藏 Priority reason / next action。

## <768px

该产品不是手机优先，但核心判断必须可完成：

- headline facts 转为紧凑 rows；
- Priority item 保留 title / impact / time / next action；
- Current Operation 变系统列表；
- 能源图可简化 ticks，不删除单位和 Actual/Comparison 语义；
- 不把所有 section 变成独立大 Card。

---

# 12. Interaction Model

## 12.1 Overview 不使用 Context Inspector

本页本身已经是轻量 attention router。

Priority item 应直接进入负责该事实的专业 Workspace；不再增加一层 Overview Inspector。

原因：

- 减少“首页 → Inspector → 详情”的无意义中间层；
- 专业页面本身已经拥有 Inspector / Detail 机制；
- 避免在 Overview 复制 domain 信息。

## 12.2 Overview 不执行业务 Mutation

本页不做：

- Alarm ACK；
- Assign；
- Work Order state change；
- Control；
- Strategy approval；
- Opportunity approval。

所有 mutation 在拥有完整上下文和权限边界的专业页面完成。

## 12.3 Refresh

如果使用 Snapshot + Stream：

- Stream 更新当前事实；
- 手动 Refresh 只在确有同步/重取意义时出现；
- realtime platform connection 状态不能映射成 device online/offline。

实时更新不得重新排序用户正在阅读的列表到无法跟踪；对于 Priority Attention 的实时排序变化，应采用稳定的 presentation policy，而不是每个事件到来就强行跳行。

---

# 13. Data Authority Contract

Overview 不拥有任何新的业务真相。

| 区域 | Authoritative owner | Overview 可以做什么 | Overview 不可以做什么 |
|---|---|---|---|
| Site identity/timezone | Site / Registry owner | 显示名称、时区 | 猜测时区 |
| Current operation | Operations/System runtime owner | 摘要 mode / population / key facts | 从零散 telemetry 自行推断完整 mode |
| Alarms | Alarm owner | 聚合 active critical/high facts | 自行把 stale/offline 当 alarm |
| Work | Work Order owner | 聚合 urgent/overdue/unowned | 自造 SLA 状态 |
| Energy | Energy analytics owner | 显示 actual / named compare | 前端自行算权威 baseline |
| Efficiency | Efficiency analytics owner | 显示已验证 metric | prerequisite 缺失时显示 0 |
| Comfort/IEQ | Comfort/IEQ owner | 显示真实 violation / metric | 无 sensor 时生成 score |
| Opportunities | Opportunity owner | 排序展示已存在机会 | 把 AI 建议转为 approved opportunity |
| Functional verification | Verification owner | 显示 failed/inconclusive attention | 用 Work Complete 代替 verification |
| Data quality | Data quality owner | 显示 impacted business data issue | 用 catch/fallback 隐藏 owner failure |
| Recent changes | Event/Audit/domain event owner | 展示权威事件 | snapshot 差异伪造事件历史 |

---

# 14. Priority Attention Data Contract

建议规范一个**presentation projection**，不是新的 domain entity。

概念字段：

```ts
SiteAttentionItem = {
  sourceType
  sourceIdentity
  title
  scopeLabel
  priorityReason
  occurredAt / since
  responsibility?
  impactLabel?
  destination
}
```

规则：

- `sourceIdentity` 继续由 domain owner 拥有；
- projection 不复制 alarm/work/diagnosis lifecycle；
- `priorityReason` 必须由真实事实组成；
- 不持久化一个新的 Overview lifecycle；
- 不创建需要和各 domain 双向同步的新 `overview_issue` 表。

这是为了避免 Overview 成为第二个业务 owner。

---

# 15. Capability Gating

## Always applicable

只要 Site 本身有效：

- Page Context；
- Current Operation（如果运行平台是产品基础能力）；
- Alarm / Work attention（对应能力存在时）。

## Capability-gated sections

- Energy & Performance：energy analytics capability；
- Efficiency：efficiency prerequisites / analytics capability；
- Comfort/IEQ：comfort/IAQ capability；
- Savings Opportunities：opportunity capability；
- Data Trust：data quality capability；
- Verification Attention：functional verification capability。

不存在 capability 时：**不显示该 section，也不放 disabled placeholder。**

Capability 存在但当前暂时不可用时：显示该 section 的 truthful unavailable state。

---

# 16. Permission Boundary

本页主要是 read-only。

最低能力：

```text
site.read
```

各 section 是否可见取决于对应 read permission / discovery policy，例如：

- alarm list/read；
- work-order list/read；
- energy read；
- efficiency read；
- opportunity read；
- data-quality read。

## 不允许的权限推断

- 看不到某 domain ≠ 该 domain 没数据；
- 无权限 ≠ 空列表；
- 首页不能通过请求失败判断“用户无权限”；权限由 Principal / Route capability contract 明确提供。

如果某 section 不允许发现，直接不显示；如果允许发现但禁止具体查看，按全局 permission policy 提供业务化解释。

---

# 17. Loading / Empty / Partial / Stale / Error

遵循全局 10-state contract，但针对 Overview 明确以下规则。

## 17.1 Initial loading

Page Context 能先确定时可以先渲染；各 section 使用自身 skeleton。

不要求为了等待所有 owner 返回而整页 blank。

## 17.2 Ready empty

示例：

```text
当前没有需要优先处理的运行事项。
```

前提是所有参与 Priority Attention 的必要 owner 均成功返回。

不能在 Work Order 仍 loading 时显示 `0 条工单`。

## 17.3 Partial

示例：

```text
运行与告警数据已更新。
工单数据暂不可用，因此优先处理列表可能不完整。
```

局部 section 继续可用。

## 17.4 Stale

Stale 必须显示最后可信时间，例如：

```text
能源数据更新至 09:32，已超过站点新鲜度要求。
```

不把 stale 值画成普通 current value。

## 17.5 Not integrated

Capability 本来就没接入：section 不出现。

不要显示：

```text
碳排放 0
水耗 0
舒适度 --
```

## 17.6 Request failed

只在 owner 请求失败时显示失败，不做跨 owner fallback。

---

# 18. No Defensive Programming / No Compatibility Design

实施本页时明确禁止：

- `value || 0` 把未知变零；
- 捕获 owner 错误后静默返回空数组；
- Alarm 请求失败后用 telemetry 猜 alarm；
- Energy baseline 失败后自动换上一周期而不告诉用户；
- Comfort capability 不存在时造一个 placeholder score；
- Opportunity owner 不可用时用 AI 文本生成假的机会；
- 为了复用旧 UI 保留 `/dashboard` 和 `/overview` 两套产品语义；
- 建立旧 Dashboard component adapter；
- 对一个事实同时查询两套接口并“谁先成功用谁”；
- 为“也许以后用”预设大量 query params、fallback branch 或 feature flag；
- 在前端复制 Alarm / Work / Energy domain state machine。

正确方式：

- 一个事实一个 owner；
- capability 明确才渲染；
- owner 不可用就显示 owner unavailable；
- 数据不足就显示数据不足；
- 旧实现如果与本 spec 冲突，直接重做当前 Surface。

---

# 19. Visual Hierarchy Contract

## 页面视觉优先级

```text
1. Priority Attention
2. Current Operation
3. Energy & Performance
4. Savings / Comfort / Data follow-up
5. Recent Changes
```

## 色彩

- 正常运行内容采用 neutral；
- semantic color 只用于异常等级、验证失败、data issue 等真实语义；
- 不把每个系统域分配一个饱和品牌色；
- 重要性同时用文字、排序、icon/label 表达，不只靠颜色。

## Cards

允许 Card 作为真正独立 semantic section。

禁止：

- 4–8 个 KPI 大卡组成首屏；
- 每一个 Priority item 一张 Card；
- Card 内再嵌套 Card 制造层级；
- shadow / gradient 制造“智慧感”。

Facts Strip 使用连续边框/分隔结构即可。

---

# 20. Component Mapping

在进入 Wireframe / 实现时，优先映射到当前 shadcn contract：

| 页面责任 | 推荐模式 |
|---|---|
| Page Context | semantic header + compact actions |
| Facts Strip | semantic grid + Separator；必要时小型 `Card size=sm` 仅作为整个 band 外壳 |
| Priority Attention | semantic list / compact rows；不使用 mega DataTable |
| Current Operation | compact list / table-like rows |
| Energy chart | ECharts |
| Supporting facts | typography + Separator / concise sections |
| Opportunity rows | compact list |
| Recent Changes | ordered/unordered event list |
| Section deep link | Button `variant=ghost/link` 或明确文本链接 |
| Tooltip | 只解释术语/单位，不承载关键事实 |
| Skeleton | section-scoped loading |

## 不使用

- Tabs 作为业务模块切换；
- Sheet 作为本页默认详情；
- Dialog 做分析；
- X6 完整拓扑；
- TanStack Table 只是为了排 4 条 Priority；
- Carousel；
- auto-rotating dashboard panels。

---

# 21. Energy & Efficiency Metric Rules

首页只能使用已定义且用户能理解的指标。

## Energy

必须显示：

- metric name；
- unit；
- aggregation period；
- named comparison；
- data freshness/quality when material。

## Efficiency

例如 COP / kW/RT / ΔT：

- prerequisite 必须满足；
- denominator / calculation definition 由专业页面可追溯；
- 首页只显示少量最适用指标；
- 不跨不兼容设备/工况做误导排名。

## 禁止

- 自创综合 Efficiency Score；
- `+12%` 不说明“相对什么”；
- Forecast 和 Actual 同样样式；
- 没 tariff 数据显示 cost savings；
- Expected savings 与 Verified savings 混用。

---

# 22. Recent Change Semantics

Recent Changes 是事件摘要，不是审计日志替代品。

每条至少拥有：

```text
business event
business object
occurred time
result/state
source route
```

如事件属于 control：

```text
策略 v14 发布
```

不等于：

```text
设备已达到目标状态
```

如需判断真实执行，进入 Execution Record / Functional Verification。

---

# 23. Accessibility

## Structure

- 一个 `h1`：站点总览；
- 主要 section 使用层级正确的 heading；
- 页面 `main` landmark 明确；
- Sidebar / nav 由 Global Shell 负责。

## Links and actions

- Priority item 的 primary destination 可键盘访问；
- 链接文本描述目标，如 `查看告警`，不是多个相同 `详情`；
- 不让整块大区域只能通过 pointer click。

## Charts

能源图表必须同时提供可读摘要，例如：

```text
今日截至 10:00 用电 1.8 MWh，相对已批准工作日基线高 9%。
```

不能要求 screen reader 用户读取 Canvas 才知道主要结论。

## Live updates

- realtime refresh 不抢焦点；
- 不在每个 telemetry tick 上触发 screen-reader announcement；
- 用户正在操作的 focus target 不因排序刷新消失。

## Color

状态不能只通过红/绿区别。

---

# 24. Performance / Realtime Behavior

站点总览不是高频 raw telemetry wall。

推荐：

- Snapshot 形成完整初始状态；
- Stream 只更新真正需要实时的运行 / alarm / work / data status；
- Energy 聚合按其真实更新周期更新；
- 不因为一个 section 高频更新导致整页 rerender / skeleton；
- 每个 owner 的 cache / invalidation 按其业务语义处理。

不创建全局 polling loop 去轮询所有 domain。

---

# 25. Example Business Copy

推荐：

```text
优先处理
3 项事项需要关注
```

```text
CH-02 高压告警持续 18 分钟
严重 · 未确认 · 冷源系统
```

```text
冷源今日能耗较工作日基线高 11%
主要偏差发生在 08:20–09:40
```

```text
关键数据不完整
冷冻水流量数据中断，系统 COP 暂不计算
```

不推荐：

```text
系统健康度 83
AI 风险指数 72%
综合节能指数 91
设备正常率 96%  // 若分母和状态语义不清楚
```

---

# 26. Empty / Good State

“没有问题”也要有清晰信息，而不是空白 Dashboard。

示例：

```text
当前没有需要优先处理的运行事项
最近一次检查：10:02
```

下方 Current Operation 与 Energy & Performance 仍正常存在。

不要为了避免空态而制造“建议优化”或随机 insight。

---

# 27. Browser Acceptance Criteria

实现后必须使用真实浏览器、真实或固定认证数据进行视觉/交互验收。

## Desktop 1440–1720px

必须满足：

- 首屏能识别 Site、当前更新时间和 Priority Attention；
- Facts Strip 是一个连续 summary band，不是 4 张独立大 Card；
- Priority Attention 是页面最强视觉区域；
- 至少 3 条 priority item 可在首屏扫描（有数据时）；
- Current Operation 同屏可见；
- Energy section 在首屏末端或紧接首屏；
- 无水平页面滚动；
- 正常状态不使用大面积高饱和色；
- 不展示 UUID / trace / revision 等内部字段；
- 不出现 Ant Design DOM/视觉兼容层于新 Surface；
- 不出现 fake health score；
- 不出现 `0` 代替 unavailable。

## Narrow 768px

必须满足：

- 无页面级横向溢出；
- Priority Attention 排在 Current Operation 前；
- Facts Strip 仍可快速理解；
- Priority reason / time / next action 不丢失；
- Energy Actual / Comparison / unit 仍可区分；
- capability-gated section 不以空卡占位。

## Interaction

- 所有 Priority deep links 进入正确 durable Surface；
- Site context 保留；
- Energy/Efficiency deep link 保留 named comparison；
- no business mutation from Overview；
- keyboard 可以访问所有主要 deep link；
- live update 不抢焦点。

## Truthfulness

在测试场景中至少验证：

- Work Order pending 时不显示 `0`；
- baseline unavailable 时 Actual 仍显示且 comparison 明确 unavailable；
- valid zero telemetry 仍显示 `0`；
- stale 不显示成 current；
- no permission 不显示成 empty；
- capability absent 不显示 placeholder；
- one owner failure 只影响对应 section，并有 Partial 说明。

---

# 28. Wireframe Gate

本 Surface 在进入 Wireframe 前已回答 Interaction Contract 的 17 个问题：

1. **Primary Job：** 30–60 秒确定站点最值得处理什么并正确分流。
2. **Primary Entry：** site-manager landing / Portfolio / Benchmarking / Site search。
3. **Primary Exit：** Operations / Alarm / Work / Energy / Efficiency / Opportunity / Data Quality / Verification。
4. **Context：** Site 必须保留；分析 deep link 带 period/comparison。
5. **Path：** `/sites/:siteId/overview`。
6. **Search Params：** 默认无复杂参数；只在真实 deep-link 用例出现后增加。
7. **Local UI：** hover / collapse / realtime presentation。
8. **Inspector：** 不需要。
9. **Durable Detail：** 由目标专业 Surface 负责。
10. **Dialog：** 不需要业务 Dialog。
11. **Operational View：** Priority + Current Operation + Energy/Performance。
12. **Engineering Detail：** 不在 Overview 展开，通过专业页面 progressive disclosure。
13. **Capability absent：** section 不显示。
14. **Permission：** 由 Principal capability 明确决定，不从请求失败推断。
15. **State：** section-scoped Loading / Partial / Stale / Unavailable / Error。
16. **Narrow：** 保持 priority-first 单列任务顺序。
17. **Accessibility：** semantic headings/links、chart summary、no color-only、live update no focus theft。

**结论：READY FOR WIREFRAME。**

---

# 29. Implementation Discipline

进入代码实现时：

1. 先确认每个区块的真实 owner/capability；
2. 没有 owner 的内容不实现成 mock production fact；
3. route 文件保持薄；
4. server truth 留在 TanStack Query，不复制到第二个 client store；
5. realtime 遵循 Snapshot + Stream；
6. Overview 只做 projection/composition，不成为新的业务 owner；
7. 不迁移旧 Dashboard 布局；
8. 不建立兼容 adapter；
9. 不增加“以防万一”的 fallback API；
10. 每一个 defensive branch 都必须有明确已知 failure mode，否则不写；
11. 完成后先 browser review，再调整布局，不凭代码想象视觉效果。

---

# 30. 本 Surface 的参考资料

## Government / standards — primary

- U.S. DOE FEMP — EMIS Operations Support  
  https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- U.S. DOE FEMP — Best Practices to Support EMIS Operation at Federal Facilities  
  https://www.energy.gov/sites/default/files/2022-02/best-practices-to-support-EMIS-operation-federal-facilities.pdf
- ENERGY STAR — Benchmark Your Building  
  https://www.energystar.gov/buildings/benchmark
- ENERGY STAR — Analyze Benchmarking Results  
  https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results
- ASHRAE Guideline 36-2024 purpose/scope  
  https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- ISA-101 Human-Machine Interfaces  
  https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101
- ISA-101 Series  
  https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards

## Mature product evidence — secondary

- Siemens Building X Operations Manager  
  https://www.siemens.com/en-us/products/building-x/applications/operations-manager/
- Schneider EcoStruxure Building Operation WebStation  
  https://sqa.ecostruxure-building-help.se.com/bms/Topics/show.castle?id=8792&locale=en-US&productversion=7.0

这些商业产品用于验证成熟工作流和信息组合方式，不作为视觉模板，不复制其菜单、Dashboard 构图或业务命名。
