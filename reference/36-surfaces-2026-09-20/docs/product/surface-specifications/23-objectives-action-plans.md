# 23 目标与行动计划 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `23 目标与行动计划`  
> **Route intent：** `/sites/:siteId/action-plans`、`/sites/:siteId/action-plans/:planId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`EnPI`、`EnB`、`SEU`、`M&V` 等标准缩写只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有目标页、项目看板、任务管理页、旧 KPI Dashboard、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Objective / Target / Action Plan / EnPI / EnB / Opportunity / Optimization Plan / M&V / Management Review / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **把组织批准的能源目标转化成可追踪、可解释、可审计的行动计划，并持续区分“工作做了多少”与“能源绩效真正改善了多少”。**

本 Surface 是 **energy objective governance + action-plan execution + performance-achievement workspace**，不是：

- 普通项目管理看板；
- 通用 Todo / Kanban；
- 21「节能机会」的复制页；
- 22「优化方案」的审批替代页；
- 24「M&V」的节能量计算页；
- 17「能源评审」的图表复制页；
- 直接修改 EnPI / EnB 定义的页面；
- 把所有项目预计节能相加就显示“目标已完成”的页面；
- 把 Task Completion 当成 Energy Performance Improvement 的页面；
- 黑盒“目标健康分”或 AI 自动判定目标是否实现的页面。

用户离开本页前应该知道：

1. 当前组织 / 站点有哪些正式能源 Objective / Target；
2. 每个 Target 关联哪个 EnPI、EnB、Scope、SEU 或其他正式绩效依据；
3. Target 的方向、目标值、目标期、Owner、Due Date 和治理状态是什么；
4. 为实现目标有哪些 Action Plan；
5. 每个 Action Plan 下哪些 Actions / Milestones 已完成、进行中、Blocked 或未开始；
6. 当前有哪些 Opportunities / Optimization Plans / Work / Strategies 对目标提供贡献；
7. 哪些贡献只是 Expected，哪些已 Implemented，哪些已 Functional Verified，哪些已 M&V Verified；
8. 当前 Energy Performance Progress 是多少，依据什么 EnPI / EnB / M&V 结果；
9. 当前 Delivery Progress 是多少，依据什么 Action / Milestone 完成状态；
10. 当前是否 On Track / At Risk / Off Track，依据什么 owner-owned forecast；
11. 主要 Blocker、Dependency、Resource Gap 和需要管理层决策的事项是什么；
12. Target 是否已达到、仍待验证、未达到、延期、取消或被新版替代；
13. Baseline / EnPI revision 变化是否影响目标可比性；
14. 哪些结果应该带入 30「管理评审」；
15. 哪些项目最终必须进入 24 M&V 才能成为 Verified Savings / Verified Performance Improvement。

---

# 2. 主要用户

## Primary

### 能源经理

维护正式 Objective / Target、Action Plan、Owner、Due Date、Progress、Blocker 和管理层汇报上下文。

### 站点能源负责人

把站点能源评审、SEU、EnPI、机会和项目组织成可执行的年度 / 多年度改进计划。

### Energy Management Team

定期审查行动进度、绩效进展、资源和依赖，并准备 Management Review。

## Secondary

- 站点负责人 / Facility Manager：确认资源、运行窗口和责任边界；
- HVAC / 控制工程师：执行关联 Optimization Plan / Strategy / Work；
- 财务 / Sponsor：读取预算、经济性和资源决策，但不改写能源绩效事实；
- M&V Engineer：提供 Verified Result；
- Management：读取 Objective / Target performance 和 unresolved decisions；
- Sustainability / Carbon owner：读取相关目标，但碳核算仍由 19 owner 负责；
- Data / EnPI owner：维护指标、基线和数据可比性。

---

# 3. 外部最佳实践依据

## 3.1 ISO 50001 — Objectives / Targets / Action Plans 是持续改进治理的一部分

ISO 50001:2018 仍是当前有效的能源管理体系国际标准，采用 Plan-Do-Check-Act 持续改进模型，要求组织设定目标、使用数据理解能源绩效、测量结果并持续改进。

来源：

- https://www.iso.org/standard/69426.html
- https://www.iso.org/iso-50001-energy-management.html

**本页采用：**

- Objective / Target 必须是正式治理对象，不是 Dashboard filter；
- Target 必须和能源绩效评价方法关联；
- Action Plan 负责“怎么实现”，Target 负责“要达到什么”；
- 目标执行结果必须回到 Energy Performance Evaluation 和 Management Review。

## 3.2 DOE 50001 Ready — Objectives & Targets、Action Plans、Monitoring、Management Review 是不同任务

DOE 50001 Ready 把能源管理拆成明确任务，其中：

```text
Task 11  EnPI / EnB
Task 12  Objectives and Targets
Task 13  Action Plans for Continual Improvement
Task 20  Monitoring and Measurement of the EnMS
Task 21  Monitoring and Measurement of Energy Performance Improvement
Task 23  Management Review
Task 25  Continual Improvement
```

来源：

- https://www.energy.gov/sites/default/files/2023-09/FEMP-50-Tools-and-Resources-to-Meet-Agency-Goals.pdf
- https://www.energy.gov/cmei/ito/50001-ready-program
- https://www.energy.gov/cmei/ito/energy-management-programs

**本页采用：**

```text
Action Progress
≠
Energy Performance Progress
```

- Actions 做完不自动表示 Target 已实现；
- Target 达成判断必须来自正式 EnPI / EnB / M&V / energy-performance owner；
- Management Review 消费本页结果，但不在本页替代管理层决策。

## 3.3 50001 Ready Task 13 — Action Plan 必须有活动、资源、责任、时间和验证方法

公开的 50001 Ready 企业实施案例说明 Action Plan 应包括：

- activities to achieve the target；
- resources needed；
- time frame；
- responsible person(s)；
- method for verifying project results；
- method for verifying improvement in energy performance。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/tools/GM%2050001%20Ready%20Corporate%20Energy%20System%20Manual%202018%20Update%20Unlinked%20Version.pdf

**本页采用：**

- Action Plan 不是只有 Owner + Due Date；
- Resource / Dependency / Verification Method 是一等结构；
- “项目完成证明”和“能源绩效改善证明”分开。

## 3.4 ISO 50006:2023 — EnPI / EnB 用于评价和证明能源绩效改善

ISO 50006:2023 指导组织建立、使用和维护 EnPI / EnB，并用它们评价能源绩效和证明能源绩效改善。

来源：

- https://www.iso.org/standard/79367.html

**本页采用：**

- Target 的绩效依据必须引用正式 EnPI / EnB version；
- Baseline revision 不得静默改写历史 Target；
- Normalized / Actual / Baseline / Target 值保持不同语义；
- 如果 EnPI / EnB 不再适用，Target 的可比性状态必须明确。

## 3.5 DOE EMIS + EnMS — EMIS 支持识别、实施和验证改进，但组织流程负责治理

DOE/FEMP 强调 EMIS 适合帮助识别、优先排序、实施和验证能源改善，但有效结果依赖配套 EnMS、人员、能源目标、行动计划和管理流程。

来源：

- https://www.energy.gov/cmei/femp/resources-implementing-energy-management-information-systems-federal-facilities
- https://www.energy.gov/cmei/femp/energy-management-information-system-benefits-federal-agencies

**本页采用：**

- 23 是 governance workspace，不是自动优化引擎；
- EMIS 可以提供数据和 verified results，但不替管理层批准 Objective / Target；
- Opportunity / Plan / M&V 都通过引用进入目标治理，而不是复制事实。

## 3.6 Fort Bragg 50001 Ready — 定期评审、跨团队协作和量化目标是持续改善关键

公开案例说明 50001 Ready 帮助站点标准化 energy planning、quantify goals and progress，并通过周期性跨部门 review 维持持续改善。

来源：

- https://betterbuildingssolutioncenter.energy.gov/iso-50001/showcase-projects/fort-bragg-%E2%80%94-50001-ready-facility/printpdf

**本页采用：**

- Review Cadence 是 Action Plan 正式属性；
- Blocker / Resource Decision / Cross-team dependency 必须显式；
- 页面需要为 Management Review 准备清晰的 decision context。

---

# 4. 产品语言契约

主界面中文优先：

```text
目标与行动计划
能源目标
量化目标值
行动计划
执行进度
能源绩效进展
预计达成
已验证结果
负责人
到期日
里程碑
依赖
阻塞事项
资源需求
复审日期
管理层决策
```

可以保留标准缩写：

```text
EnPI
EnB
SEU
M&V
LCC
```

但第一次出现应配中文，例如：

```text
能源绩效指标（EnPI）
能源基线（EnB）
重大用能（SEU）
节能量验证（M&V）
```

不使用满屏：

```text
Objective Health
Target Score
Delivery Confidence
Performance Confidence
Action Completion
Forecast Attainment
```

除非这些术语属于用户所在组织正式采用的治理模型，并有清晰方法定义。

---

# 5. Objective / Target / Action Plan Domain Vocabulary

## 5.1 Objective

高层次希望实现的能源绩效方向，例如：

> 提升中央冷站年度能源绩效。

Objective 可以不是一个单一数字，但必须有明确 Scope、Owner、Period 和关联 Target。

## 5.2 Target

用于判断 Objective 是否实现的量化要求。

例如：

```text
2027 年底前
冷站归一化 EnPI
从 0.68 kW/RT
改善到 ≤ 0.60 kW/RT
```

Target 至少包含：

- scope；
- metric / EnPI reference；
- EnB / reference version；
- direction；
- target value / threshold / range；
- target period / due date；
- owner；
- approval / governance state。

## 5.3 Action Plan

为实现一个或多个 Target 而组织起来的正式执行计划。

包含：

- activities/actions；
- milestones；
- owner / responsible parties；
- time frame；
- resources；
- dependencies；
- blockers；
- linked opportunity / optimization plan / work / strategy；
- project-result verification method；
- energy-performance verification method。

## 5.4 Action

Action Plan 中一个可执行、可分配、可追踪的活动。

例如：

```text
完成 CH-03 传感器校准
发布低负荷单机运行策略 v6
完成功能验证 VR-208
完成 M&V reporting period
```

## 5.5 Milestone

用于跟踪计划阶段完成情况的治理节点，不等于能源绩效结果。

例如：

```text
方案批准
施工完成
策略上线
功能验证通过
M&V 完成
```

## 5.6 Verified Result

来自权威 Energy Performance / M&V / EnPI owner 的验证结果。

它可以支持 Target attainment，但不能由 Action Completion 推断。

---

# 6. Mandatory Semantic Separation

以下语义必须永久分开：

```text
Objective
≠ Target

Target
≠ Action Plan

Action Plan
≠ Opportunity

Action Plan
≠ Optimization Plan

Action Complete
≠ Target Achieved

Milestone Complete
≠ Energy Performance Improved

Implementation Complete
≠ Functional Verification Passed

Functional Verification Passed
≠ Verified Savings

Expected Contribution
≠ Implemented Contribution
≠ Verified Contribution

Forecast Achievement
≠ Actual Achievement

Target Value
≠ Current Value

Baseline
≠ Target

Comparison
≠ Baseline

EnPI Improvement
≠ Verified Project Savings automatically

On Track
≠ Achieved

At Risk
≠ Failed

Due Date Passed
≠ Target Failed automatically

Target Achieved
≠ Objective Closed automatically
```

---

# 7. Primary Questions

默认页面必须快速回答：

1. 我们当前有哪些正式能源目标？
2. 哪些目标按计划推进，哪些存在风险？
3. 哪些目标的行动计划落后？
4. 哪些行动已经完成但能源绩效尚未验证？
5. 哪些目标虽然行动进度高，但实际 EnPI / verified result 仍偏离？
6. 哪些目标因 EnB / EnPI / Scope 变化导致可比性需要复审？
7. 哪些 Blocker 需要跨部门或管理层决策？
8. 哪些 Target 预计能够实现，依据是什么？
9. 哪些 Target 已经由权威结果验证实现？
10. 哪些 Objective / Target 即将进入 Management Review？

---

# 8. Information Architecture

```text
Context Header
  Site / Organization
  Energy Review Revision
  Planning Period
  Review Cadence

Target Portfolio Summary
  Active Targets
  At Risk
  Blocked
  Pending Verification
  Verified Achieved

Target Ledger
  Target
  EnPI / Baseline
  Owner
  Due
  Delivery Progress
  Performance Progress
  Forecast
  Blockers
  Verification

                        → Target / Action Plan Inspector

Selected Target Workspace
  Objective / Target Definition
  EnPI / EnB Reference
  Action Plan
  Milestones / Dependencies / Resources
  Linked Opportunities / Optimization Plans
  Expected / Implemented / Verified Contributions
  Progress / Forecast
  Blockers / Decisions Needed
  Verified Results
  Review / Revision / Audit

Professional exits
  17 Energy Review
  21 Opportunity
  22 Optimization Plan
  24 M&V
  30 Management Review
```

默认是 **Target Ledger + selected target workspace**，不是大面积 KPI card wall。

---

# 9. Route / URL State Contract

建议：

```text
/sites/:siteId/action-plans
/sites/:siteId/action-plans/:planId
```

Search Params 可包括：

```text
period
status
owner
objective
seu
verification
sort
selectedTarget
```

不建议把以下短暂状态写 URL：

```text
accordion open
hover
local edit draft
chart tooltip
```

页面从 17 / 21 / 22 / 24 / 30 进入时，应保留：

```text
site
sourceContext
reviewRevision
baselineVersion
selectedTarget / plan
```

---

# 10. Objective Contract

Objective 必须至少包含：

```text
Objective ID
中文名称
业务意图
Scope
Owner
Planning Period
Status
Linked Targets
Source Energy Review Revision
Created / Approved Revision
```

示例：

```text
Objective
提升中央冷站年度能源绩效

Scope
中央冷站

Owner
站点能源经理

周期
2027 年度

来源
能源评审 v6
```

禁止只有：

```text
目标：节能 10%
```

却没有 Scope、Metric、Reference、Period 和 Owner。

---

# 11. Target Definition Contract

每个 Target 必须能回答：

```text
Measure what?
Against what reference?
Within what scope?
By when?
Owned by whom?
How is attainment determined?
```

建议结构：

```text
Target
冷站归一化 EnPI ≤ 0.60 kW/RT

Metric
冷站单位冷量功率（EnPI）v4

Baseline / Reference
能源基线（EnB）v3

Current
0.65 kW/RT

Target
≤ 0.60 kW/RT

Due
2027-12-31

Owner
能源经理
```

Target Definition 不能由前端临时解释一个 KPI。

---

# 12. Target Direction / Threshold Contract

Target 类型可以由 owner 支持：

```text
Decrease to / below
Increase to / above
Maintain within range
Absolute reduction
Intensity improvement
Milestone-based governance target（仅适用明确治理目标）
```

前端不根据数值大小猜 `higher is better` 或 `lower is better`。

例如：

```text
COP
higher may be better

kW/RT
lower may be better
```

必须由 Metric / EnPI definition 提供 direction。

---

# 13. Target Revision Contract

Target 是 versioned governance object。

例如：

```text
Target v1
冷站 EnPI ≤ 0.62
Approved

↓ 正式调整范围 / baseline / due date

Target v2
冷站 EnPI ≤ 0.60
Approved
```

旧 Target revision 必须保留：

- 当时目标；
- 当时 EnPI / EnB reference；
- 当时 due date；
- 当时 owner；
- 调整原因；
- approval / audit。

禁止：

```text
直接把数据库 targetValue 从 0.62 改成 0.60
```

然后历史看起来从未发生过目标调整。

---

# 14. Baseline / EnPI Reference Contract

Target 必须引用正式：

```text
EnPI ID + Version
EnB ID + Version
Reference period / effective period
Normalization method
Metric validity rules
```

17 是 EnPI / EnB governance owner。

23 只消费这些定义，不重新创建 baseline。

如果引用的 EnB 被 superseded：

```text
旧 Target
仍保留原 EnB revision
```

不能自动迁移到最新 EnB。

---

# 15. Baseline Change Impact Contract

如果出现：

```text
EnB revision
Static factor change
Scope change
Metering boundary change
EnPI method revision
```

页面必须显示：

```text
目标可比性需要复审
```

而不是自动：

```text
Target remains On Track
```

治理 owner 决定：

- 继续使用旧基线；
- 正式调整 Target；
- 建立新 Target revision；
- 建立 parallel target；
- 标记历史不可直接比较。

前端不自动重算整个目标历史。

---

# 16. Action Plan Contract

每个 Action Plan 至少包含：

```text
Plan ID / Revision
Linked Objective / Target
Owner
Start / Due
Review Cadence
Actions
Milestones
Resources
Dependencies
Blockers
Expected Contributions
Verification Method
Management Review relevance
```

Action Plan 可以关联一个或多个 Target，但贡献关系必须明确。

---

# 17. Action Contract

Action 不是一个简单 checkbox。

至少支持：

```text
Action
Responsible owner
Due date
Status
Dependency
Resource requirement
Linked implementation object
Completion evidence / reference
Next action
```

状态建议：

```text
Not Started
In Progress
Blocked
Completed
Cancelled
Not Applicable with reason
```

中文界面：

```text
未开始
进行中
已阻塞
已完成
已取消
不适用（有原因）
```

`Completed` 只表示这个 Action owner 确认活动完成。

它不自动改变 Target performance。

---

# 18. Milestone Contract

Milestone 用于 governance progress，不用于替代工程事实。

例如：

```text
机会评审完成
优化方案获批
实施完成
功能验证完成
M&V 完成
管理评审完成
```

Milestone 可以绑定其他 Surface 的真实对象：

```text
Opportunity OP-103
Optimization Plan PLAN-208 v5
Work WO-1032
Verification VR-204
M&V Project MV-22
```

禁止前端自己看某个 Route 有对象就自动判 milestone completed。

---

# 19. Resource Contract

Action Plan 应明确需要什么资源，例如：

```text
Budget
Internal labor
External contractor
Shutdown window
Engineering time
Controls engineer
Metering upgrade
Permit / approval
Training
Procurement
```

Resource 状态与 Action 状态分开。

例如：

```text
Action: Not Started
Budget: Approved
Shutdown Window: Pending
```

页面不能把资源缺失偷偷转换成“进度慢”。

---

# 20. Dependency Contract

Dependency 可以是：

```text
Action dependency
Project dependency
Data dependency
Approval dependency
Procurement dependency
Shutdown / Plant condition
External program / Utility
Baseline / EnPI update
Verification prerequisite
```

依赖必须尽量引用真实 owner/object。

禁止使用大量自由文本：

```text
Waiting for something
```

却没有 blocker owner / next step。

---

# 21. Blocker Contract

Blocked 必须至少回答：

```text
为什么阻塞？
从什么时候开始？
谁拥有解除动作？
需要什么才能解除？
下次 review 什么时候？
对 Target / Due / Forecast 有什么影响？
```

示例：

```text
阻塞原因
等待采购审批

阻塞开始
2026-08-21

负责人
Facility Manager

下一动作
批准 CHW 流量计采购

影响
M&V baseline collection 延后约 3 周
```

禁止：

```text
Status = Blocked
```

但没有原因和 owner。

---

# 22. Delivery Progress Contract

**Delivery Progress** 只反映计划实施进度。

来源可以包括：

```text
Action completion
Milestone completion
Approved project status
Work / Strategy implementation status
```

示例：

```text
行动计划执行进度
72%
```

但页面必须明确它不是：

```text
目标达成率 72%
```

如果组织使用 weighted action progress，权重必须由 Action Plan owner 定义，不由前端平均计算。

---

# 23. Energy Performance Progress Contract

Energy Performance Progress 来自正式能源绩效 owner。

可能基于：

```text
Current EnPI vs Target
Normalized EnPI vs Target
Verified M&V contribution
Approved target-evaluation method
```

页面必须同时显示：

```text
Current
Reference / Baseline
Target
Validity
Period
Method / Owner
```

如果数据不足：

```text
绩效进展：暂不可判断
```

而不是：

```text
0%
```

---

# 24. Delivery Progress ≠ Performance Progress

这是 23 的核心视觉和语义原则。

一个 Target 可以出现：

```text
行动执行进度
90%

能源绩效进展
38%
```

这不是系统错误，而是一个重要管理信号：

> 大部分事情已经做了，但实际能源绩效尚未达到预期。

也可以：

```text
行动执行进度
45%

能源绩效已经接近目标
```

表示部分高影响措施已产生效果。

两条进度永远不能合成一个黑盒 `Overall Progress`。

---

# 25. Expected / Implemented / Verified Contribution Contract

每个关联项目对 Target 的贡献至少区分：

```text
Expected Contribution
Implemented Contribution
Functional Verified
M&V Verified Contribution
```

例如：

```text
低负荷单机运行
Expected: 120 MWh/y
Implemented: Yes
Functional Verification: Passed
M&V Verified: Pending
```

不能显示：

```text
Savings = 120 MWh/y
```

直到权威 M&V / performance owner 验证。

---

# 26. Contribution Double-counting Contract

目标页必须防止在汇总显示上重复计算同一效果。

例如：

```text
Opportunity OP-101
→ Optimization Plan PLAN-204
→ Strategy STR-18
→ M&V MV-12
```

这些是同一改进链条，不是四份独立节能贡献。

Contribution owner 应提供：

```text
contribution lineage
overlap / exclusive relation
verified contribution identity
```

前端不能：

```text
Expected Opportunity
+ Expected Plan
+ Verified M&V
```

全部相加。

---

# 27. Forecast Contract

Forecast 回答：

> 按当前已知实施进度、已验证绩效和 owner-approved assumptions，这个 Target 到 Due Date 预计能否实现？

正式状态可以是：

```text
预计达成
存在风险
预计无法达成
暂无法预测
```

英文辅助可以是：

```text
On Track
At Risk
Off Track
Unknown
```

但 Forecast 必须来自 Planning / Forecast owner，并携带：

```text
Generated At
Method / Version
Assumptions
Expected Contributions included
Verified Contributions included
Uncertainty / Confidence semantics
```

前端不能简单：

```text
sum(expected savings) >= gap
→ On Track
```

---

# 28. Forecast ≠ Achievement

正式语义：

```text
预计达成
≠ 已达成

预计无法达成
≠ 已失败
```

Forecast 是当前条件下的前瞻判断。

Achievement 是 reporting/target period 下权威绩效判断。

---

# 29. Target Status Contract

建议分离：

## Governance Lifecycle

```text
Draft
Approved
Active
Superseded
Cancelled
Closed
```

## Performance State

```text
Not Yet Evaluated
On Track
At Risk
Off Track
Pending Verification
Verified Achieved
Verified Not Achieved
Inconclusive
```

不要用一个：

```text
status = GREEN / YELLOW / RED
```

把治理和绩效压在一起。

---

# 30. Target Achievement Contract

Target 只有在其正式 evaluation method 满足时，才能进入权威 achievement state。

例如：

```text
Target
Normalized EnPI ≤ 0.60 kW/RT

Evaluation Period
2027 年度

Result
0.59 kW/RT

Validity
Valid

State
Verified Achieved
```

如果年度尚未结束：

```text
Current = 0.59
```

也不一定代表目标已最终实现。

页面应显示：

```text
当前达到目标区间
最终目标期尚未结束
```

而不是提前宣布：

```text
目标已实现
```

---

# 31. Pending Verification Contract

当行动完成、当前指标看似达到目标，但正式 period / M&V / review 尚未结束时，使用：

```text
待验证
```

例如：

```text
行动计划执行：100%
当前 EnPI：0.59
Target：≤0.60
状态：待验证
原因：年度评价期尚未结束
```

这是合法且重要的状态。

---

# 32. Objective Achievement Contract

一个 Objective 可以关联多个 Targets。

Objective 是否完成由 Objective owner / governance method 决定。

前端禁止：

```text
all targets verified achieved
→ auto close objective
```

因为可能还涉及：

```text
scope change
management decision
policy requirement
new target cycle
continuation requirement
```

---

# 33. Opportunity / Optimization Plan Relationship

23 可以显示：

```text
Linked Opportunity
Linked Optimization Plan
```

但不会复制 21 / 22 的完整内容。

Action Plan 可以引用：

```text
Opportunity OP-101
Optimization Plan PLAN-204 v5
```

并显示：

```text
Expected Contribution
Implementation Status
Verification Status
```

专业调查进入 owner Route。

---

# 34. Work / Strategy / Control Relationship

23 可以显示行动实施对象：

```text
Work Order
Strategy
Control Execution
Procurement
Training
Metering Upgrade
```

但不会在本页直接：

```text
Start / Stop
Publish Strategy
Write Setpoint
Complete Work
Approve Permit
```

Action Plan 是治理与追踪，不是执行控制台。

---

# 35. Verification Method Contract

每个高价值 Action Plan 应明确两种不同验证：

```text
Project Result Verification
Energy Performance Verification
```

例如：

```text
项目结果验证
功能验证 VR-208 通过

能源绩效验证
M&V MV-22 / EnPI v4 年度评价
```

这直接对应 50001 Ready Task 13 的公开实践。

两者不能合并成一个：

```text
Verified = true
```

---

# 36. M&V Boundary

23 消费 24 的 verified result，但不自行计算 savings。

23 可以显示：

```text
M&V 状态
Reporting Period
Verified Savings
Persistence status
```

但不能在本页：

```text
Actual - Baseline = Savings
```

或：

```text
Expected - Current = Verified Contribution
```

---

# 37. Energy Review Boundary

17 是：

```text
SEU
EnPI
EnB
Energy Review governance
```

23 是：

```text
Objective
Target
Action Plan
Progress
Performance Achievement
```

17 可以提出目标需求或更新 EnPI / EnB。

23 不回写 Energy Review 方法定义。

---

# 38. Management Review Handoff Contract

30「管理评审」应从 23 获取：

```text
Objectives / Targets
Verified achievement state
Performance trends
At-risk / off-track targets
Blocked actions
Overdue decisions
Resource gaps
Unresolved dependencies
Significant changes
Previous management actions
Requested decisions
```

Handoff 不只是一个 PDF。

页面必须能明确标记：

```text
需要管理层决策
```

例如：

```text
追加预算 300k
批准停机窗口
调整 Target due date
批准 target revision
决定暂停项目
批准新增资源
```

---

# 39. Management Decision Contract

管理层 Decision 可以影响：

```text
Resource allocation
Target revision approval
Due date revision
Scope revision
Program continuation
Priority
Risk acceptance
```

但不能直接修改：

```text
Measured EnPI
Verified M&V result
Historical action completion
Physical operating facts
```

Governance decision 和 physical / performance fact 分离。

---

# 40. Review Cadence Contract

Action Plan / Target 应有 review cadence，例如：

```text
Monthly
Quarterly
At milestone
Before Management Review
```

由治理 owner 定义。

页面可以显示：

```text
上次评审
2026-09-01

下次评审
2026-10-01
```

不能因为用户一个月没打开页面就说：

```text
Target overdue
```

除非 owner 的 review schedule 明确超期。

---

# 41. Portfolio Summary Contract

页面顶部只使用少量、明确的治理事实，例如：

```text
进行中目标 8
存在风险 2
已阻塞 1
待验证 3
已验证达成 4
```

不做：

```text
Target Health 87
Energy Program Score 92
```

除非未来存在真正批准的方法 owner。

---

# 42. Target Ledger Contract

默认列建议：

```text
目标
EnPI / 基线
负责人
截止日期
执行进度
绩效进展
预计达成
阻塞事项
验证状态
```

可选列：

```text
SEU
Objective
Review Date
Expected Contribution
Verified Contribution
Budget / Resource Status
```

不要默认塞入：

```text
created_by
updated_by
internal UUID
API source
model raw id
```

---

# 43. Target Inspector Contract

Ledger 选中目标后 Inspector 只显示快速判断内容：

```text
Objective / Target
Owner / Due
EnPI / EnB
Delivery Progress
Performance Progress
Forecast
Top Blocker
Next Review
1–3 Next Actions
```

以及专业入口：

```text
打开行动计划
查看能源评审
查看优化方案
查看 M&V
进入管理评审上下文
```

不把完整 action list / audit / M&V details 塞进 Inspector。

---

# 44. Action Plan Detail Contract

Durable Detail Route 负责持续治理：

```text
Objective / Target Definition
Metric / Baseline Reference
Action Plan
Milestones
Resources
Dependencies
Blockers
Linked Initiatives
Progress
Forecast
Verified Results
Review Notes
Decisions / Audit
```

这页可以支持 section navigation，但不做几十个 Tab。

---

# 45. Action Plan Progress Visualization

建议同时展示：

```text
执行进度
能源绩效进展
```

例如：

```text
执行进度
72%

能源绩效进展
当前 0.65 kW/RT
目标 ≤0.60
基线 0.68
```

比单一：

```text
Overall progress 72%
```

更符合真实治理。

---

# 46. Progress Calculation Boundary

前端禁止默认：

```text
completed actions / total actions
```

就称为“Action Plan progress”。

原因：

- Actions 可能权重不同；
- Milestones 可能比 task 数更重要；
- 某些 Action 是 optional；
- 有些 Action 是 governance gate。

Progress Method 必须来自 Action Plan owner：

```text
Equal weighted
Weighted by owner
Milestone based
Schedule based
Custom approved method
```

并能解释 method/version。

---

# 47. Overdue Contract

需要区分：

```text
Action Overdue
Milestone Overdue
Review Overdue
Target Due Date Passed
Verification Overdue
```

不能统一成一个红色：

```text
Overdue
```

Target Due Date 过了但 evaluation data 不完整时，可能是：

```text
Due passed / Result pending
```

而不是自动：

```text
Failed
```

---

# 48. Forecast / Scenario Boundary

23 只消费正式 forecast。

复杂 What-if / engineering scenario 属于 22。

23 不做：

```text
拖一个滑块
→ 自动修改项目节能
→ 自动改变目标 forecast
```

除非未来有正式 Planning Forecast service。

---

# 49. Budget / Economics Boundary

可以显示治理上下文：

```text
Approved Budget
Committed
Spent
Forecast Cost
Economic Analysis reference
```

但财务数值必须来自 Finance / Project Economics owner。

不能：

```text
expected savings × tariff
→ project ROI
```

然后作为 target truth。

---

# 50. Data Authority Contract

| Fact | Owner |
|---|---|
| Objective / Target | EnMS / Goal Governance |
| Action Plan / Action / Milestone | Action Plan owner |
| EnPI / EnB | 17 Energy Review / Energy Performance owner |
| Opportunity | 21 Opportunity domain |
| Optimization Plan | 22 Optimization domain |
| Work status | Work Order domain |
| Strategy / execution | Strategy / Control domain |
| Functional Verification | 13 Verification domain |
| Verified Savings | 24 M&V owner |
| Carbon target/result | Carbon owner when applicable |
| Budget / economics | Finance / Economics owner |
| Management decision | Management Review / Governance owner |

Frontend 只组合这些事实，不重新定义它们。

---

# 51. Query / Read Model Contract

Target Ledger 应由服务端提供适合治理扫描的 read model。

例如：

```text
Target summary
Owner
Due
EnPI reference
Current performance summary
Delivery progress
Performance progress
Forecast
Top blocker
Verification summary
```

禁止 N+1：

```text
100 targets
→ 100 EnPI queries
→ 100 M&V queries
→ 100 Opportunity queries
→ 100 Action queries
```

Detail 页面再按需要加载完整关系。

---

# 52. Realtime / Freshness Contract

23 不是高频 realtime HMI。

大多数事实适合：

```text
Query + explicit refresh / normal cache
```

如果 EnPI / project status 有更新，可使用 Query invalidation。

不要：

```text
每秒刷新 action progress
```

或因为 realtime stream disconnect 就把所有目标标记为 Unknown。

每个 Performance Result 应保留自己的 `asOf / period / validity`。

---

# 53. Permission Contract

典型权限：

```text
Read Objectives
Manage Draft Target
Approve Target Revision
Manage Action Plan
Assign Action Owner
Edit Resource Request
Submit Management Decision
Read M&V Result
Read Financial Data
```

权限不只是 UI 隐藏。

未经权限不能修改 Target / Plan authoritative state。

---

# 54. Revision / Audit Contract

以下变化必须进入 audit：

```text
Target value
Due date
Scope
EnPI reference
EnB reference
Owner
Action Plan revision
Progress method
Forecast method
Resource decision
Target lifecycle state
Management decision
```

Audit 至少包含：

```text
who
when
what changed
before / after
reason
approval reference when required
```

不能只保留 `updatedAt`。

---

# 55. Historical Integrity Contract

新的 Target / Plan revision 不得改写旧历史。

例如：

```text
2026 Target v1
Due 2026-12-31
Target 5% improvement

2026-09 formally revised
Target v2
Due 2027-03-31
Target 7% improvement
```

历史报表必须仍能回答：

> 2026-08 当时批准的目标是什么？

---

# 56. AI Assistance Boundary

AI 可以：

```text
总结目标状态
整理 blocker
草拟 review note
指出目标与行动计划之间的缺口
提示哪些 Action 缺 owner / due / verification method
解释 EnPI / EnB reference
整理 Management Review briefing
```

AI 不能：

```text
自动设 Target
自动修改 Target Value
AI forecast → authoritative Forecast
AI guess → Verified Progress
AI 判断 → Target Achieved
AI 自动关闭 Action
AI 自动批准 Revision
AI 自动改 Management Decision
```

AI output 必须保持 assistant / draft 身份。

---

# 57. No Defensive Programming / No Compatibility Design

明确禁止：

```text
objectives API error → []

target current value missing → 0

target progress missing → 0%

verification unavailable → achieved

M&V unavailable → expected = verified

EnPI unavailable → use arbitrary KPI

EnB unavailable → previous period

baseline superseded → silently use latest

baseline changed → rewrite historical target

action completed → target achieved

all actions completed → objective closed

work complete → action verified

functional verification pass → savings verified

expected contribution → verified contribution

sum expected opportunities → forecast achieved

same contribution chain → double count savings

missing blocker owner → treat as no blocker

missing forecast → On Track

past due → Failed

all targets achieved → auto-close objective

multiple goal APIs → first success wins

old KPI Dashboard fallback

old generic project-management board fallback

legacy goal-status adapter
```

正式原则：

> **一个 Objective / Target 对应一个权威治理 lifecycle。行动完成度与能源绩效改善度保持分离；Forecast 不是 Achievement，Expected 不是 Verified，Baseline revision 不改写历史，Unknown 保持 Unknown。**

---

# 58. Accessibility

- Target Ledger 使用 semantic table；
- 状态不只靠颜色；
- `At Risk / Blocked / Pending Verification` 使用文字和图标冗余表达；
- progress bar 必须有可读文字和值；
- 如果进度未知，不显示空的 0% progress bar；
- Inspector / Detail 可键盘访问；
- Review / Revision Dialog 遵守 focus management；
- timeline / dependency 不能只靠视觉连线表达；
- deadline / overdue 不能只靠红色。

---

# 59. Responsive Contract

## 1440–1720 px

首屏应看到：

```text
Planning Context
Target Portfolio Summary
Target Ledger
Selected Target / Inspector
```

## 1024–1439 px

- Ledger 保留核心列；
- 次要字段进入 Column Settings / Inspector；
- Summary 压缩为一行事实；
- Detail section 垂直布局。

## 约 768 px

核心任务仍可完成：

```text
查看 Target
查看 owner / due
查看 Delivery Progress
查看 Performance Progress
查看 blocker
进入 action plan detail
进入 M&V / Energy Review / Management Review
```

不在窄屏转换成完全不同的“目标卡片产品”。

---

# 60. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 目标与行动计划 · 中央冷站                                  2027 能源计划   │
│ 来源：能源评审 v6 · 月度复审 · 下次评审 2026-10-01                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 进行中目标 6   存在风险 2   已阻塞 1   待验证 2   已验证达成 3            │
├──────────────────────────────────────────────────────────────────────────────┤
│ [状态] [负责人] [SEU] [验证状态] [复审期]                        [筛选]    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标                    EnPI/基线     负责人   截止    执行    绩效    状态 │
│ 冷站 EnPI ≤0.60          v4 / EnB v3   李工     12/31   72%    44%    有风险│
│ 夜间基载下降 12%         v3 / EnB v2   王工     11/30   90%    —      待验证│
│ 峰值需量下降 8%          v2 / EnB v2   陈工     12/15   55%    61%    按计划│
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中目标：冷站 EnPI ≤0.60                    │ 快速判断                     │
│                                               │ Owner 李工                   │
│ Current 0.65 kW/RT                            │ Due 2027-12-31               │
│ Baseline 0.68                                 │ Forecast 存在风险            │
│ Target ≤0.60                                  │ Top blocker 流量计采购       │
│                                               │                              │
│ 行动执行进度 72%                              │ [打开行动计划]               │
│ ███████████████░░░░░                          │ [查看能源评审]               │
│                                               │ [查看优化方案]               │
│ 能源绩效进展 44%                              │ [查看 M&V]                   │
│ 当前 0.65 → 目标 0.60                         │                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 行动计划                                                                    │
│ ✓ 流量计方案确认         完成                                                 │
│ ! 流量计采购             阻塞 · 等待预算审批                                 │
│ → 低负荷策略 v5          已批准 · 待执行                                      │
│ ○ 功能验证 VR-208        等待实施                                              │
│ ○ M&V MV-22              计划中                                                │
├──────────────────────────────────────────────────────────────────────────────┤
│ 贡献                                                                      │
│ PLAN-204 预计 120–150 MWh/y · 已批准 · 尚未验证                            │
│ PLAN-211 预计 70–90 MWh/y  · 已实施 · 功能验证通过 · M&V 待完成             │
├──────────────────────────────────────────────────────────────────────────────┤
│ 需要管理层决策：批准 CHW 流量计预算，否则 M&V baseline collection 延后 3 周 │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任和信息层级，不是像素规范。

---

# 61. Browser Acceptance Criteria

## Objective / Target Truth

- Objective 与 Target 分开；
- Target 显示 Scope、Metric、Reference、Due、Owner；
- Target 引用明确的 EnPI / EnB revision；
- Baseline / EnPI 不可用时不会 fallback 到上一期；
- Target revision 不覆盖旧 revision；
- Baseline change 会触发可比性提示，而不是静默重算。

## Progress

- Delivery Progress 与 Performance Progress 同时可见且文字区分；
- Delivery 100% 不自动显示 Target Achieved；
- Performance unavailable 不显示 0%；
- 进度方法可解释；
- progress bar 不只靠颜色。

## Contributions

- Expected / Implemented / Functional Verified / M&V Verified 分离；
- 同一 Opportunity → Plan → M&V 链不会重复汇总；
- Expected 不能显示成 Verified Savings；
- 未验证 contribution 明确标记。

## Forecast

- Forecast 与 Achievement 分离；
- Forecast 有 owner/method/asOf；
- missing forecast 显示不可用，不 fallback 到 On Track；
- At Risk 不等于 Failed。

## Action Plan

- Blocked action 必须有 blocker reason / owner / next action；
- Resource / Dependency 明确；
- Action Complete 不改变 EnPI / target state；
- Review cadence 和 next review 清晰。

## Verification

- Project Result Verification 与 Energy Performance Verification 分离；
- Functional Verification Pass 不等于 Verified Savings；
- M&V result 来自 24 owner；
- Pending Verification 是合法状态。

## Management Review

- 可清晰看到需要 management decision 的 blocker/resource/target revision；
- handoff 保留 target/reference/revision context；
- management decision 不改写 measured performance facts。

## Accessibility / Responsive

- semantic table；
- 键盘可选择 target / action；
- 768px 下仍能完成核心 target review；
- 无 page-level 横向溢出；
- 状态不依赖颜色。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 KPI Dashboard / generic project board adapter；
- 无前端自算 authoritative target progress；
- 无 N+1 target/enpi/mv queries；
- review scenario 无 runtime/network error；
- 内部 UUID 不作为主标签。

---

# 62. Explicit Non-goals

23 不是：

- 通用项目管理系统；
- 财务预算系统；
- 21 Opportunity Portfolio；
- 22 Engineering Change Approval；
- 24 M&V Calculator；
- EnPI / EnB Editor；
- 自动控制页面；
- AI 项目经理；
- 一张“目标健康分” Dashboard；
- 把所有 Action 完成率加权后叫 Energy Performance 的系统。

---

# 63. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Objective / Target / Action Plan 语义分开；
- Target 绑定正式 EnPI / EnB / reference revision；
- Delivery Progress 与 Energy Performance Progress 分离；
- Expected / Implemented / Verified contribution 分离；
- Contribution lineage 能避免 double counting；
- Forecast 与 Achievement 分离；
- Blocker / Resource / Dependency 可治理；
- Verification Method 明确；
- Management Review handoff 明确；
- revision / historical integrity 明确；
- No Defensive Programming 规则明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
