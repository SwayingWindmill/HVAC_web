# 22 优化方案 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `22 优化方案`  
> **Route intent：** `/sites/:siteId/optimization-plans`、`/sites/:siteId/optimization-plans/:planId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`M&V`、`What-if`、`Rollback` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有优化页、控制台、策略页、AI 建议页、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Opportunity / Change Proposal / Simulation / Approval / Control / Work / Verification / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **把已经选定继续开发的改善机会转化为一个可解释、可评审、可审批、可执行交接、可回滚、可验证的 versioned engineering change proposal。**

本 Surface 是 **engineering change development + approval workspace**，不是：

- 节能机会池；
- Control Center；
- BAS point editor；
- Work Order Detail；
- AI 自动实施建议页；
- 仿真工具本身；
- M&V 结果页；
- 仅填写“当前值/目标值”的轻量表单；
- 一个“Approve & Execute”按钮；
- 绕过现场安全、权限、interlock、commissioning 的捷径。

用户离开本页前应该知道：

1. 当前方案对应哪个 Opportunity / Problem；
2. 当前状态和 proposed change 分别是什么；
3. 影响哪些系统、设备、区域、point、策略、时段和业务；
4. 执行前必须满足哪些 Preconditions；
5. 运行中有哪些 Guardrails / Interlocks；
6. 模拟或 What-if 的方法、假设、结果和不确定性是什么；
7. 预期 Energy / Demand / Cost / Carbon / Comfort / Reliability 影响是什么；
8. 风险和 blast radius 是什么；
9. Test Plan 如何验证功能是否符合要求；
10. Rollback Plan 在什么条件下启用、回到什么 approved state；
11. 谁需要审批当前 revision；
12. 当前 revision 是否 Approved / Needs Revision / Rejected；
13. 允许在哪个 Execution Window 实施；
14. 实施应转 Work Order、Control / Strategy，还是混合编排；
15. 执行后如何进入 13 Functional Verification 和 24 M&V。

---

# 2. 主要用户

## Primary

### 能源 / 优化工程师

把 Opportunity 发展成工程上可执行、可验证的方案。

### HVAC / 控制工程师

定义控制 sequence、setpoint、schedule、staging、interlock、rollback 和 functional test。

### 站点技术负责人 / 审批人

评审风险、影响、证据、模拟、执行窗口和回滚能力，并对特定 revision 做决定。

## Secondary

- Facility Manager：确认站点运行影响和实施窗口；
- Comfort / IAQ owner：确认环境 guardrail；
- Maintenance Planner：把物理改造交给 Work Order；
- Safety / OT Security owner：评审高风险变更；
- Energy Manager：确认 expected benefit 与目标对齐；
- M&V Engineer：确认 measurement boundary 和后续验证计划；
- Management / Sponsor：按组织治理参与高价值/高风险审批。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP Commissioning Process — 调查、实施、再测试是不同阶段

DOE/FEMP 对既有设施 commissioning 的公开流程明确区分：Plan、Investigate、Implement、Hand off and Integrate。Investigation 阶段形成 deficiencies、recommendations、test/monitoring plans 和 savings estimates；Implementation 只对已接受 recommendations 实施，并要求 retest、remonitor、fine-tune 和更新 savings estimates。

来源：

- https://www.energy.gov/cmei/femp/commissioning-process-federal-facilities
- https://www.energy.gov/cmei/femp/articles/2024-commissioning-guidance-energy-savings-performance-contracts

**本页采用：**

```text
Recommendation / Opportunity
≠ Accepted Change

Implementation
≠ Verification
```

- Proposal 必须经过明确评审/审批；
- Test Plan 在实施前定义；
- 实施后进入 Functional Verification；
- 必要时根据结果继续 fine-tune，而不是执行即关闭。

## 3.2 ASHRAE Commissioning — 变更必须围绕明确要求、验证和文档

ASHRAE Standard 202 / Guideline 0 / Guideline 0.2 / Guideline 1.2 的公开说明都强调 commissioning 是 quality-oriented process，用来规划、评估、调查、实施、验证和记录系统是否满足 Owner’s Project Requirements / Current Facility Requirements。

来源：

- https://www.ashrae.org/technical-resources/bookstore/commissioning
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- https://www.ashrae.org/news/esociety/commissioning-process-for-existing-systems-assemblies-outlined-in-new-guideline
- https://handbook.ashrae.org/Handbooks/A23/IP/A23_Ch44/a23_ch44_ip.aspx

**本页采用：**

- Proposed Change 必须关联明确 performance intent；
- Test Plan 必须在执行前存在；
- verification criteria 不能执行后再临时编；
- implementation documentation、O&M impact、training impact 都可能成为 approval input。

## 3.3 DOE OpenBuildingControl — 控制方案应先可模拟、可评价，再实施和验证

DOE/LBNL OpenBuildingControl 通过 Control Description Language、Modelica 和标准化 sequence library，把 control design、simulation/evaluation、implementation 和 commissioning/verification 连接起来。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control

**本页采用：**

```text
Simulation / What-if
≠ Field Proof
```

- simulation 输入、模型、版本和假设必须可追溯；
- simulation result 可以支持审批，但不能变成现场验证结果；
- control proposal 需要清楚定义 expected sequence / behavior。

## 3.4 NIST SP 800-82 Rev. 3 — OT 变更必须考虑 Safety / Reliability / Performance

NIST 对 Operational Technology 的安全指南明确要求，在 OT 环境中同时考虑 performance、reliability 和 safety，而 BAS/controls 属于 OT 范畴。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

**本页采用：**

- 变更风险不能只看“节能收益”；
- control authority、access、security impact、safety/reliability constraints 是一等事实；
- 高风险变更不得从 Opportunity 直接执行；
- rollback / recovery 必须在实施前规划。

## 3.5 ISA Alarm Lifecycle / Management of Change — 变更需要独立治理

ISA-18 系列明确把 modification / ongoing change management 纳入 alarm lifecycle；不同类别变更可能需要不同审批级别。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards
- https://www.isa.org/intech-home/2018/march-april/features/alarm-management-life-cycle

**本页采用：**

- 如果优化方案会改变 Alarm Definition / priority / suppression / delay / deadband，必须进入相应 Alarm Rule / MOC owner；
- Optimization Plan 不直接改写 Alarm 哲学；
- 变更 classification 可以决定审批要求，但 classification 由治理 owner 定义。

## 3.6 DOE/FEMP Life-Cycle Cost — 方案经济性应使用正式分析，而不是前端拍脑袋

FEMP BLCC / Handbook 135 支持全生命周期经济分析和统一经济假设。

来源：

- https://www.energy.gov/cmei/femp/building-life-cycle-cost-programs

**本页采用：**

- 22 可消费 LCC / NPV / SIR / IRR / Payback；
- economics 有 owner/version/assumptions；
- 前端不自行把 Opportunity estimate 转成 authoritative business case。

---

# 4. 产品语言契约

主界面中文优先：

```text
优化方案
当前状态
拟议变更
影响范围
执行前提
运行护栏
模拟 / 情景分析
预期影响
舒适与室内环境影响
可靠性影响
风险
测试计划
回滚计划
审批
执行窗口
实施方式
变更记录
```

可以保留：

```text
What-if
M&V
Rollback
BAS
OT
```

但不允许主界面满屏：

```text
Current State
Proposed State
Guardrail
Blast Radius
Execution Gate
Rollback Trigger
```

---

# 5. Optimization Plan Domain Vocabulary

## 5.1 Optimization Plan / 优化方案

一个经过工程化开发、可被正式评审和批准的变更提案。

它包含：

```text
Objective
Current State
Proposed Change
Affected Scope
Preconditions
Guardrails
Expected Effect
Simulation / Evidence
Risk
Test Plan
Rollback Plan
Approval
Execution Window
```

## 5.2 Current State

变更前的 authoritative state / configuration / operating context reference。

不是用户临时输入的描述。

## 5.3 Proposed Change

准备实施的工程变更定义。

可能是：

- schedule；
- setpoint / reset logic；
- sequence / staging；
- supervisory strategy；
- equipment retrofit；
- control upgrade；
- DER dispatch strategy；
- meter/data enablement；
- combined physical + controls change。

## 5.4 Preconditions

执行开始前必须成立的条件。

## 5.5 Guardrails

执行过程中不可违反的 operating / safety / comfort / reliability constraints。

## 5.6 Interlock

由 control/safety owner 执行的硬/软约束或 inhibition condition。

Guardrail 不等同于 Interlock。

## 5.7 Simulation / What-if

在明确 model、scenario、inputs 和 assumptions 下对 proposed change 进行预评估。

## 5.8 Test Plan

实施后如何证明系统行为满足 requirement 的验证计划。

## 5.9 Rollback Plan

如果实施结果不可接受，如何回到 approved prior state 或另一安全状态的计划。

Rollback Plan 不是“自动回滚已经配置”的同义词。

## 5.10 Approval Decision

对某个**明确 Plan Revision**做出的治理决定。

## 5.11 Execution Window

允许实施的时段和运行条件，不只是日期字段。

---

# 6. Mandatory Semantic Separation

必须保持：

```text
Opportunity
≠ Optimization Plan

Optimization Plan
≠ Approved Change

Approved
≠ Scheduled

Approved
≠ Executed

Scheduled
≠ Started

Execution Attempt
≠ Execution Success

Execution Success
≠ Functional Verification PASS

Functional Verification PASS
≠ Verified Savings

Simulation
≠ Field Verification

Expected Effect
≠ Observed Effect

Expected Savings
≠ Verified Savings

Current State
≠ Proposed Change

Guardrail
≠ Interlock

Rollback Plan
≠ Automatic Rollback

Rollback Attempt
≠ Restored State

Control ACK
≠ Verified Behavior

Plan Revision
≠ Execution Record

Approval
≠ Permission to bypass Control Authority

Low Risk
≠ No Approval Required automatically
```

正式链路：

```text
21 Opportunity
↓
22 Optimization Plan Draft
↓
Engineering Review
↓
Approval of Revision
↓
Execution Window / Implementation Route
↓
Work / Control / Strategy Execution
↓
13 Functional Verification
↓
24 M&V
↓
Persistence / Management Review
```

---

# 7. Primary Questions

## Q1 — 这项方案到底要改变什么？

必须能看见：

```text
Current State
→ Proposed Change
```

而不是只有“优化冷机控制”。

## Q2 — 为什么要做？

显示：

- linked Opportunity；
- evidence；
- objective；
- expected benefit；
- strategic/SEU/target relation。

## Q3 — 会影响哪些对象和人？

显示 Affected Scope / Blast Radius：

- systems；
- equipment；
- zones；
- points；
- schedules；
- alarms；
- occupants/processes；
- tariffs/DER；
- integrations。

## Q4 — 执行前必须满足什么？

显示 Preconditions、approvals、data readiness、maintenance state、mode、permission、safety/OT conditions。

## Q5 — 执行过程中不能突破什么边界？

显示 Guardrails / Interlocks：

- comfort；
- IAQ；
- temperature/humidity；
- equipment limits；
- minimum ventilation；
- process constraints；
- safety；
- grid/interconnection；
- reserve；
- reliability。

## Q6 — 方案预期有什么效果？

显示 Energy / Demand / Cost / Carbon / Comfort / Reliability / Maintenance effect，来源和 confidence 明确。

## Q7 — 模拟支持这个方案吗？

显示 scenario、model/version、assumptions、coverage、result、uncertainty。

## Q8 — 如果不符合预期怎么办？

显示 rollback trigger、target state、steps、authority、verification、fallback。

## Q9 — 谁批准了当前 revision？

显示 required approvals、decision、actor、time、revision、conditions/comments。

## Q10 — 实施后怎么证明成功？

显示 Test Plan，并进入 13 Functional Verification；Savings 则进入 24 M&V。

---

# 8. Route / URL State Ownership

列表：

```text
/sites/:siteId/optimization-plans
```

Durable Detail：

```text
/sites/:siteId/optimization-plans/:planId
```

推荐 Search Params：

```text
status
owner
riskClass
changeType
approvalState
executionState
sourceOpportunity
q
sort
page
size
section
revision
```

Plan Detail 的 revision 作为显式 context，不靠 local state 猜。

---

# 9. Entry Contract

Primary entry：

```text
21 Opportunity
→ 22 Optimization Plan
```

携带：

```text
Opportunity ID / revision
Opportunity statement
Affected scope
Evidence references
Expected benefit estimate revision
Applicability
Constraints / risks
Dependencies
Economics references
Priority rationale
Owner
```

重要：

```text
Opportunity expected direction
≠ Proposed Change definition
```

22 必须重新工程化设计，而不是 copy title 后就可审批。

也允许由正式：

- Commissioning recommendation；
- approved engineering study；
- regulatory/mandatory change workflow；

创建 Plan，但必须保留 source。

---

# 10. Exit Contract

根据 implementation type：

```text
Optimization Plan
→ 12 Work Order Detail       // physical / maintenance / installation
→ Control Center             // bounded control execution
→ 27 Strategy Detail         // supervisory strategy lifecycle
→ Rule Administration        // alarm/rule change when applicable
→ 13 Functional Verification
→ 24 M&V
→ 23 Objectives / Action Plans
```

没有：

```text
Opportunity → Execute
```

也没有：

```text
Approved Plan → frontend direct point write
```

---

# 11. Responsibility Boundary

本 Surface 拥有：

- Plan identity / revision；
- Objective；
- Current State reference；
- Proposed Change definition；
- Affected Scope；
- Preconditions；
- Guardrails；
- known Interlock references；
- Simulation / What-if references；
- Expected Effect；
- Risk assessment；
- Test Plan；
- Rollback Plan；
- Approval workflow/projection；
- Execution Window；
- Implementation Route；
- plan history/audit。

本 Surface 不拥有：

- raw telemetry truth；
- low-level point-write execution；
- BAS transport ACK；
- work execution；
- alarm physical state；
- safety permit truth；
- interconnection permission truth；
- Functional Verification result；
- M&V verified savings；
- finance ledger；
- simulation engine truth；
- frontend-generated approval。

---

# 12. Information Architecture

```text
方案标题 / Revision / Lifecycle / Approval
↓
Source Opportunity + Objective
↓
Current State ↔ Proposed Change
↓
Affected Scope / Blast Radius
↓
Preconditions
↓
Guardrails / Interlocks
↓
Simulation / What-if
↓
Expected Effects
↓
Risk / Constraints
↓
Test Plan
↓
Rollback Plan
↓
Approval
↓
Execution Window / Implementation Route
↓
History / Audit
```

不是一个 50 字段 form，也不是十几个同权重 Tabs。

---

# 13. Optimization Plan Lifecycle Contract

Conceptual lifecycle：

```text
Draft
↓
Ready for Review
↓
In Review
↓
Approved
↓
Ready for Execution
↓
Handed Off / Scheduled
```

并允许：

```text
Needs Revision
Rejected
Withdrawn
Superseded
Expired
```

精确 enum 由 owner 定义。

重要：**Execution lifecycle 独立于 Plan lifecycle。**

Plan 已 Approved，不意味着已执行。

---

# 14. Plan Identity / Revision Contract

每个 Plan 至少有：

```text
Plan reference
Title
Site
Source Opportunity / source workflow
Revision
Owner
Engineering owner
Risk/change class
Created at
Updated at
Current lifecycle state
Approval state
Superseded relation
```

Approved revision immutable。

修改后：

```text
Plan v4 Approved
↓ material change
Plan v5 Draft / Review Required
```

不能修改 v4 原文后继续显示 `Approved`。

---

# 15. Revision Significance Contract

什么变更需要新 revision / re-approval 由 Governance owner 定义。

通常 material changes 可能包括：

```text
Proposed Change
Affected Scope
Guardrail
Precondition
Risk
Expected Effect method
Test Plan
Rollback Plan
Execution Window class
Implementation Route
```

Frontend 不自己判断：

```text
small diff → approval still valid
```

---

# 16. Objective Contract

Objective 必须可验证、可与 Opportunity 和 Target 对齐。

例如：

```text
降低低负荷时段冷站输入功率，
同时维持 CHWS 温度、关键区域舒适和设备最小运行约束。
```

避免：

```text
节能优化
```

这种无验证意义的 objective。

---

# 17. Current State Contract

Current State 至少引用：

```text
Configuration / Strategy revision
Operating mode
Relevant setpoints / schedules / sequence
Affected equipment/system state
Known constraints
Evidence window
Data quality
Captured at / effective period
```

Current State 应尽量由 owner snapshot/reference 提供。

禁止：

```text
用户自由文本描述
→ authoritative current configuration
```

---

# 18. Proposed Change Contract

Proposed Change 要说明：

```text
What changes
From
To / New behavior
Affected scope
Activation condition
Deactivation condition
Duration / permanence
Dependencies
Implementation mechanism
```

控制类方案还应表达：

```text
sequence logic
mode transition
setpoint/reset behavior
staging
minimum/maximum bounds
failure handling
```

但 22 不直接写 BAS point。

---

# 19. Change Type Contract

可分类：

```text
Schedule
Setpoint / Reset
Sequence / Staging
Supervisory Strategy
Alarm / Rule Change
Physical Retrofit
Control Upgrade
DER / Demand Strategy
Data / Metering Enablement
Mixed Change
```

Type 只用于治理和 routing，不自动决定 risk 或 approval。

---

# 20. Current vs Proposed Diff Contract

核心 UI 应提供 semantic diff：

```text
当前
CHWS reset 6.0–7.0°C

拟议
CHWS reset 6.0–8.0°C，
仅在低负荷且关键区域满足条件时允许上移
```

不要显示 raw JSON diff 作为默认业务界面。

专业层可以查看 owner-native diff。

---

# 21. Affected Scope Contract

Affected Scope 至少可表达：

```text
System
Equipment
Zone
Point group
Sequence / Strategy
Schedule
Alarm / Rule
Meter boundary
DER resource
Occupant/process group
Integration / downstream consumer
```

范围必须来自 Registry / Semantic Model / owner selection。

不能按名字猜。

---

# 22. Blast Radius Contract

Blast Radius 回答：

> 如果这个变更表现不符合预期，最大可能影响谁/什么？

可包括：

```text
Single equipment
System
Multiple systems
Zone group
Whole site
Critical process
Life-safety-adjacent workflow
Grid / DER scope
```

Blast Radius 不是 Risk Score。

必须显示 reason / affected dependency。

---

# 23. Dependency Contract

Plan 可依赖：

- Work Order；
- sensor/calibration；
- software/control upgrade；
- permit；
- shutdown window；
- data quality fix；
- tariff/program activation；
- another Plan；
- vendor support；
- training/documentation。

每个 dependency 至少有：

```text
Dependency
State
Owner
Required before what gate
```

未满足 dependency 不能静默 Ready for Execution。

---

# 24. Preconditions Contract

Precondition 是执行**开始前**必须成立的条件。

例如：

```text
站点处于正常冷却模式
CH-03 不在维护
关键传感器质量 Good
无 active override
目标区域已占用/未占用符合方案要求
控制权限可用
计划窗口已批准
```

Precondition 必须有 owner 和 evaluation semantics。

---

# 25. Preconditions ≠ Guardrails

必须保持：

```text
Precondition
= can we start?

Guardrail
= can we continue safely/acceptably?
```

例如：

```text
Precondition:
Outdoor air > 24°C

Guardrail:
关键区域温度不得超过 25.5°C
```

不能混成一个 checklist。

---

# 26. Guardrail Contract

Guardrail 至少可以覆盖：

```text
Comfort
IAQ / ventilation
Humidity
Freeze / condensation protection
Equipment operating limits
Minimum / maximum flow
Pressure
Process / production quality
Critical load
Battery reserve
Grid export/import limit
Reliability
Safety
```

每个 guardrail 至少有：

```text
Metric / condition
Allowed bound/state
Source owner
Evaluation period
Action when violated
```

---

# 27. Guardrail ≠ Hard-coded Frontend Threshold

例如：

```text
Zone temp < 26°C
```

只有在 Comfort/Operations owner 正式定义后才能成为 Guardrail。

Frontend 不从设计常识写死全站阈值。

---

# 28. Interlock Contract

Interlock 是 control/safety owner 的正式阻断逻辑或保护条件。

22 只引用：

```text
Interlock ID / name
State
Owner
Reason
Effect
```

不重新实现 interlock。

例如：

```text
CHW low-flow interlock
```

不能被一个 Optimization approval 绕过。

---

# 29. Comfort / IAQ Impact Contract

涉及 HVAC 的方案必须明确：

```text
Expected comfort effect
Expected IAQ effect
Known sensitive zones
Relevant guardrails
Evidence / simulation
Fallback
```

如果无法评估：

> `舒适影响尚未完成评估。`

不能默认 `No Impact`。

---

# 30. Reliability / Equipment Impact Contract

至少考虑：

```text
Starts/stops
Cycling
Runtime distribution
Minimum on/off time
Valve/damper hunting
Pump/fan operating range
Chiller lift/load envelope
Battery cycle/degradation context
Generator runtime/fuel
```

不能因为 energy model 结果好，就把 equipment impact 默认安全。

---

# 31. Safety / OT Security Impact Contract

对于高风险 control/OT change：

显示：

```text
Safety review required?
OT security review required?
Access/change authority
Remote/local control impact
Fail-safe behavior
Recovery path
```

NIST OT 语义决定 safety/reliability/performance 是独立评审维度。

---

# 32. Simulation / What-if Contract

Simulation 结果至少有：

```text
Scenario ID / revision
Model / version
Input dataset / period
Current-state calibration context
Proposed change revision
Assumptions
Exclusions
Outputs
Uncertainty / confidence
Generated at
Owner
```

没有这些 lineage 的“模拟节能 18%”不能成为正式 approval evidence。

---

# 33. Simulation Validity Contract

至少明确：

```text
Applicable
Outside calibration range
Insufficient input data
Model mismatch
Scenario incomplete
Superseded
Unknown
```

如果模型超出适用范围：

> `当前情景超出模型验证范围。`

不能仍显示高精度结果。

---

# 34. Simulation ≠ Verification

必须保持：

```text
Simulation PASS / favorable
≠ Functional Verification PASS
```

Simulation 回答：

> 在模型和假设下，方案预计如何表现？

Functional Verification 回答：

> 实施后真实系统是否按要求工作？

---

# 35. Scenario Comparison Contract

可以比较：

```text
Current / Baseline Scenario
Scenario A
Scenario B
Scenario C
```

但每个 scenario 必须绑定同一或可解释的：

- boundary；
- input period；
- model version；
- assumptions。

不同基础条件不能假装直接可比。

---

# 36. Expected Effect Contract

必须分开：

```text
Energy
Demand
Cost
Carbon
Comfort / IAQ
Reliability
Maintenance / Operability
Resilience
Other service outcome
```

不能一个：

```text
Impact Score = 92
```

---

# 37. Expected Energy / Demand Effect

应引用 21 estimate 或 22 updated engineering estimate：

```text
Amount / range
Unit
Period
Boundary
Method / model
Revision
Confidence
```

22 可以更新 estimate，但必须产生新 revision/lineage。

---

# 38. Expected Cost Effect

Cost 结果必须来自 tariff/economics owner。

至少显示：

```text
Currency
Tariff / model revision
Period
Range/scenario
Capex/Opex if applicable
LCC/NPV/SIR/IRR/Payback when available
```

不能：

```text
Expected kWh × average rate
→ authoritative savings
```

除非 owner 方法就是如此。

---

# 39. Expected Carbon Effect

必须保留：

```text
Method
Factor context
Location/Market basis
Inventory/project boundary
Period
Estimate nature
```

Expected carbon effect 不自动改写 19 Carbon inventory。

---

# 40. Expected Effect ≠ Acceptance Criteria

Expected benefit 是“预计得到什么价值”。

Test acceptance criteria 是“功能是否按要求工作”。

二者不能混。

例如：

```text
Expected Benefit:
年节电 140 MWh

Functional Criterion:
低负荷时 CHWS reset 能按 zone demand 稳定执行，
且关键区域温度保持 guardrail 内
```

---

# 41. Risk Contract

风险至少分类：

```text
Safety
Comfort / IAQ
Reliability
Equipment
Operational
Cyber / OT
Implementation
Schedule
Financial
Savings / Performance
Regulatory / Interconnection
Rollback / Recovery
```

每个 risk 至少：

```text
Level
Reason
Affected scope
Mitigation
Owner
Residual risk if assessed
```

不使用不可解释的单一 `Risk 72/100`。

---

# 42. Change / Risk Classification Contract

组织可以定义：

```text
Standard / Low-risk change
Normal change
High-risk change
Emergency change
```

但 classification 由 Change Governance owner 管理。

它可以决定：

- required reviewers；
- approval quorum；
- execution restrictions；
- test/rollback depth；
- OT/Safety review。

Frontend 不自己按 changed point count 判风险等级。

---

# 43. Test Plan Contract

Test Plan 至少包含：

```text
Requirement
Test Definition reference / draft
Preconditions
Test Conditions
Expected Behavior
Observed variables / points
Acceptance Criteria
Evidence required
Owner
Execution timing
Failure / inconclusive handling
```

13 Functional Verification 是 authoritative execution/results owner。

22 定义“准备怎么验”。

---

# 44. Test Plan Must Exist Before Execution

对于要求 Functional Verification 的变更：

```text
Approved for execution
```

前必须有足够 Test Plan。

不能：

```text
先改
→ 看起来没问题
→ 再写测试标准
```

这会造成 post-hoc acceptance criteria。

---

# 45. Verification Requirement Contract

每个 Plan 明确：

```text
Functional Verification
Required / Not Required / Owner Decision Pending
```

如果 Not Required：

必须有 owner + rationale。

前端不能根据 changeType 自动宣布不需要验证。

---

# 46. M&V Planning Boundary

如果 expected savings 需要正式 M&V，22 可以引用：

```text
Candidate measurement boundary
Baseline reference
Required meters
Reporting period intent
Adjustment variables
M&V owner
```

但正式 M&V Plan / savings result 属于 24。

---

# 47. Rollback Plan Contract

Rollback Plan 至少有：

```text
Rollback target state / revision
Trigger conditions
Decision authority
Execution method
Dependencies
Expected duration
Operational impact
Data/config backup reference
Post-rollback verification
Fallback if rollback fails
```

不能只写：

```text
Rollback: restore previous settings
```

---

# 48. Rollback Target Contract

Rollback 应回到明确的 approved prior state，例如：

```text
Strategy v12
BAS config package 2026-08-18
Schedule revision r7
```

而不是：

```text
previous
```

因为“previous”在并发变更环境中可能已经变化。

---

# 49. Rollback Trigger Contract

可能触发条件：

```text
Guardrail violation
Critical alarm
Functional test failure
Unexpected oscillation
Equipment protection event
Operator abort
Data/telemetry loss when safe operation cannot be assured
Performance degradation beyond owner-defined bound
```

Trigger 必须 owner-defined。

Frontend 不写：

```text
if temp > 26 → auto rollback
```

除非 Control/Strategy owner 明确提供。

---

# 50. Rollback Plan ≠ Automatic Rollback

必须保持：

```text
Rollback Plan exists
≠ Auto rollback configured
```

自动 rollback 是 Control/Strategy execution capability，需要独立：

```text
Authority
Interlock
Test
Permission
Audit
```

---

# 51. Rollback Execution Contract

Rollback 也有生命周期：

```text
Requested
→ Attempted
→ ACK
→ Readback
→ Restored State
→ Functional Verification / check
```

不能：

```text
rollback command sent
→ Plan restored
```

---

# 52. Approval Contract

Approval 是对特定 revision 的正式 decision。

每个 required approval 至少有：

```text
Role / authority
Required / optional
Decision
Actor
Timestamp
Plan revision
Conditions / comments
Expiry if policy uses
```

Decision 可包括：

```text
Pending
Approved
Approved with Conditions
Changes Requested
Rejected
Expired / Superseded
```

精确 enum 由 owner 定义。

---

# 53. Approval Matrix Contract

Approval requirement 可以基于：

```text
Change class
Risk
Blast radius
Control authority
Safety impact
Comfort/IAQ impact
Capital cost
Regulatory / interconnection impact
Cyber/OT impact
Business process impact
```

但 matrix 由 Governance owner 提供。

Frontend 不自己推导。

---

# 54. Approval Revision Binding

必须保持：

```text
Approval(plan v4)
≠ Approval(plan v5)
```

如果 v5 是 material revision，v4 approval 不能自动继承。

页面要明确：

> `该方案已修改，需要重新审批。`

---

# 55. Conditional Approval Contract

如果审批带条件：

```text
Approved with Conditions
```

必须显示：

```text
Condition
Owner
Due / gate
Satisfied state
Evidence
```

条件未满足：

```text
Ready for Execution = false
```

不是静默忽略。

---

# 56. Approval ≠ Authorization Bypass

即使 Plan 已审批：

- Control permission 仍需存在；
- Safety permit 仍由 Safety owner；
- interlock 仍有效；
- Work authorization 仍由 Work/Safety workflow；
- grid/interconnection permission 仍由其 owner。

Approval 不是“超级权限”。

---

# 57. Execution Window Contract

Execution Window 至少有：

```text
Start / End
Timezone
Allowed operating mode
Occupancy / production condition
Maintenance / shutdown coordination
Weather/load condition if relevant
Required personnel / vendor
Rollback time allowance
Verification time allowance
```

不是只有：

```text
Execute at 10 PM
```

---

# 58. Window Validity Contract

如果执行条件不成立：

```text
Window missed / invalid
```

不允许默认顺延到下一晚。

是否 reschedule 由 owner workflow 决定。

---

# 59. Implementation Route Contract

按方案类型显式选择 owner workflow：

## Physical change

```text
22 Plan
→ Work Order
```

## Control / sequence change

```text
22 Plan
→ Strategy Detail / Control Center
```

## Alarm / rule change

```text
22 Plan
→ Rule Administration / MOC
```

## Mixed change

建立明确 dependencies / order：

```text
Work
→ Control Publish
→ Functional Verification
```

而不是一个前端按钮同时调用多个 domain API。

---

# 60. Control / Strategy Handoff Contract

对于 control plan，handoff 至少携带：

```text
Approved Plan ID / revision
Target scope
Proposed strategy/config revision
Preconditions
Guardrails
Interlock references
Execution window
Rollback target
Test Plan reference
Approval evidence
```

Control execution 仍遵守：

```text
Intent
→ Attempt
→ ACK
→ Readback
→ Verified
```

---

# 61. Work Order Handoff Contract

物理改造至少携带：

```text
Approved Plan
Scope
Required work
Equipment / location
Safety / permit context
Dependencies
Completion evidence requirement
Test / verification handoff
Rollback / restoration context if applicable
```

Work Completed 仍不等于 Plan success。

---

# 62. Alarm / Rule Change Boundary

如果 Proposed Change 包含：

```text
Alarm threshold
Priority
Delay
Deadband
Suppression logic
Notification routing
```

22 只定义 change intent / rationale。

真正修改必须进入 Alarm Definition / Rule owner + MOC。

不能从 Optimization Plan 直接 patch alarm runtime。

---

# 63. Simulation / Execution Drift Contract

如果实际 implementation 与 Approved Plan 不一致：

必须显示：

```text
Implementation Deviation
```

例如：

```text
Approved:
SAT reset 12–16°C

Implemented:
SAT reset 12–15°C
```

不能仍把 approved simulation result 当 actual implemented scenario 的依据。

可能要求：

- review；
- new plan revision；
- re-simulation；
- re-approval；
- updated Test Plan。

由 governance owner决定。

---

# 64. Execution Record Boundary

Plan Detail 可以显示 execution summary：

```text
Execution ID
Route
Started / ended
Outcome
Implemented revision
Deviation
Rollback state
Verification state
```

但 authoritative execution log 属于 Work / Control / Strategy owner。

22 不复制成第二套 lifecycle。

---

# 65. Outcome Contract

方案之后可能看到：

```text
Not Executed
Execution In Progress
Executed as Approved
Executed with Deviation
Rolled Back
Execution Failed
Verification Pending
Verification Passed / Failed / Inconclusive
M&V Pending / Result Available
```

这些是不同 owner 的 projection，不压成一个 `Success`。

---

# 66. Plan Success Boundary

不能定义：

```text
Plan Success = Control ACK
```

也不能：

```text
Plan Success = Work Completed
```

更专业的链路：

```text
Implemented
↓
Function Verified
↓
Performance Monitored
↓
Savings Verified where required
```

---

# 67. Data Authority Contract

## Opportunity / expected benefit source

Owner：Opportunity domain。

## Current configuration / state

Owner：Registry / Control / Strategy / Operations domain。

## Equipment / relationship scope

Owner：Registry / Semantic Model。

## Preconditions / operating context

Owner：对应 Operations / Safety / Control / Data domains。

## Comfort / IAQ guardrails

Owner：Comfort / Facility / Control policy domain。

## Safety / permit

Owner：Safety domain。

## Interlocks

Owner：Control / Safety domain。

## Simulation result

Owner：Simulation / Engineering Analysis domain。

## Economics

Owner：Financial/Economic Analysis domain。

## Plan / approval lifecycle

Owner：Optimization / Change Governance domain。

## Execution

Owner：Work / Control / Strategy domain。

## Functional Verification

Owner：Verification domain。

## Verified Savings

Owner：M&V domain。

Frontend 不成为上述任何事实的替代 owner。

---

# 68. Query / Read Model Contract

列表推荐：

```text
Optimization Plan Portfolio Projection
  identity/title
  source opportunity
  revision
  change type
  affected scope summary
  risk/change class
  approval state
  execution readiness
  owner
  next action
```

详情推荐：

```text
Optimization Plan Detail Projection
  objective
  current/proposed semantic diff
  affected scope
  preconditions
  guardrails/interlocks
  simulation summary
  expected effects
  risks
  test plan
  rollback plan
  approvals
  execution window
  handoff/execution summary
  audit history
```

禁止：

```text
20 affected assets
→ 20 current-state calls
→ 20 guardrail calls
→ 20 approval calls
→ 20 telemetry calls
```

缺 projection/batch read model 时修 domain。

---

# 69. Mutation Contract

可能 mutation：

- create from Opportunity；
- revise Objective / Proposed Change；
- update affected scope；
- attach simulation；
- update expected effect；
- add/update guardrails/preconditions；
- add risk；
- define Test Plan；
- define Rollback Plan；
- submit for review；
- request changes；
- approve/reject；
- schedule execution window；
- handoff to implementation route；
- withdraw/supersede。

所有 mutation：

1. 显式用户动作；
2. 权限 / role；
3. revision check；
4. server-authoritative decision；
5. audit trail；
6. no fake local success。

---

# 70. Concurrency / Revision Contract

多人可同时参与 engineering / review / approval。

要求：

```text
revision/version
optimistic conflict detection
refetch authoritative state
explicit reconfirm
```

禁止 silent last-write-wins。

也不做前端自动 merge：

- Proposed Change；
- Test Plan；
- Rollback Plan；
- approval comments。

---

# 71. AI Assistance Boundary

AI 可以：

- 总结 Opportunity/evidence；
- 草拟 Current/Proposed change prose；
- 提醒遗漏 Preconditions；
- 提醒可能 Guardrails / Risks；
- 汇总 simulation result；
- 草拟 Test Plan；
- 草拟 Rollback checklist；
- 比较 revisions；
- 解释 approval blockers。

AI 输出必须标来源/provenance。

AI 不能：

```text
AI proposal
→ Approved Change

AI says safe
→ safety review passed

AI predicted savings
→ authoritative expected effect

AI simulation summary
→ field verification

AI suggests rollback trigger
→ active interlock

AI approval recommendation
→ approval decision

AI plan
→ direct control execution
```

---

# 72. Loading / Empty / Partial / Error

## No plans

成功 empty：

> `当前范围内暂无优化方案。`

可进入 21 Opportunity。

## Service unavailable

> `优化方案数据暂不可用。`

不能显示 `0 个方案`。

## Simulation unavailable

Plan 仍存在：

> `模拟结果暂不可用。`

不能显示 `Simulation Passed`。

## Approval service unavailable

显示：

> `审批状态暂不可用。`

不能默认 Pending / Approved。

## Current-state owner unavailable

显示 unavailable。

不能用 Plan 中旧 snapshot 冒充 current live state；历史 snapshot 可以明确标“方案编制时状态”。

---

# 73. Permission Contract

示例：

```text
optimization-plan.read
optimization-plan.create
optimization-plan.edit
optimization-plan.submit
optimization-plan.review
optimization-plan.approve
optimization-plan.schedule
optimization-plan.handoff
simulation.read
risk.read
```

Approval permission 和 execution permission 分开。

UI hiding 不替代 server authorization。

---

# 74. Visual / UX Contract

默认视觉层级：

```text
Plan Header / Revision / Approval
↓
Objective
↓
Current ↔ Proposed
↓
Impact Scope
↓
Preconditions / Guardrails
↓
Simulation / Expected Effects
↓
Risk
↓
Test + Rollback
↓
Approval
↓
Execution Window / Handoff
```

禁止：

- 一页巨大审批表；
- 满屏绿色“收益卡”；
- `Approve & Execute`；
- Simulation result 占据绝对权威地位；
- 只显示 expected savings，不显示 comfort/reliability；
- rollback 只写一句 `restore previous`；
- Approval 用一个总 `Approved` 掩盖多个 required roles；
- control button 混在方案编辑区。

---

# 75. Plan List Contract

列表核心字段建议：

```text
方案
来源机会
Revision
变更类型
影响范围
风险/变更等级
审批状态
执行准备度
负责人
下一动作
```

Expected Benefit 可作为次级 summary，不让它独占排序逻辑。

---

# 76. Plan Detail Navigation Contract

长页面使用 section navigation：

```text
概览
变更定义
影响与护栏
模拟与预期效果
风险
测试与回滚
审批与执行
历史
```

这不是 Peer Tabs，不应假装每个 section 是独立页面。

---

# 77. Current / Proposed Primary Workspace

首个专业核心区必须能回答：

```text
当前怎么运行？
要改成什么？
为什么？
影响谁？
```

例如：

```text
当前
冷机台数由固定 2 台最小运行规则约束

拟议
在总冷量负荷 < 35% 且满足最小流量/压差时允许单机运行，
并由负荷 + 效率模型选择优先机组
```

而不是：

```text
Optimization Strategy v7
```

---

# 78. Approval Workspace Contract

审批区应让 reviewer 一屏看到：

```text
Revision
Objective
Semantic diff
Expected effects
Top risks
Guardrails
Simulation status
Test Plan readiness
Rollback readiness
Execution route/window
Required approvals
```

审批不是只看 PDF attachment。

---

# 79. Review Comment / Decision Contract

Review comment 必须绑定：

```text
Plan revision
Section / subject
Actor
Timestamp
Resolution state if workflow supports
```

不能把 reviewer comment 作为普通聊天消息丢失上下文。

---

# 80. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 优化方案 · 冷站低负荷单机运行                         Revision v5 · 待审批    │
│ 来源机会：OP-0214 · 预计节电 138–152 MWh/年 · 负责人：能源工程师             │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标                                                                         │
│ 低负荷时减少冷机并联运行，同时维持供水温度、最小流量和关键区域舒适            │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ 当前状态                             │ 拟议变更                              │
│ · 最少 2 台冷机运行                  │ · 负荷 <35% 时允许单机运行             │
│ · 固定启停排序                       │ · 按负荷 + 效率选择优先机组            │
│ · CHWS reset 6.0–7.0°C               │ · CHWS reset 6.0–8.0°C                 │
│ [查看配置来源]                       │ [查看语义差异]                         │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 影响范围：冷站 · CH-01~03 · CHW 泵组 · 关键区域 12 个 · Strategy v12        │
│ 变更等级：正常变更 · Blast Radius：冷站系统                                 │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ 执行前提                             │ 运行护栏                              │
│ ✓ 关键传感器有效                     │ CHWS 6.0–8.0°C                         │
│ ✓ CH-03 非维护状态                   │ 关键区域温度 ≤ owner-defined limit     │
│ ! 最小稳定流量验证待完成             │ 最小流量 / 压差 interlock 保持有效     │
├──────────────────────────────────────┼───────────────────────────────────────┤
│ 模拟 / 情景分析                      │ 预期影响                              │
│ Model v8 · 8 周数据                  │ 节电 138–152 MWh/年                    │
│ Scenario B：有利 · 适用范围有效      │ 峰值影响：无显著变化                   │
│ 不确定性：中                         │ 舒适：预计无显著影响 · 待现场验证      │
├──────────────────────────────────────┼───────────────────────────────────────┤
│ 测试计划                             │ 回滚计划                              │
│ · 低负荷工况功能测试                 │ Target：Strategy v12                   │
│ · CHWS / flow / zone evidence         │ Trigger：护栏违反 / 振荡 / test fail  │
│ · 结果：13 功能验证                  │ 回滚后需再次确认系统状态               │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 审批                                                                         │
│ 控制负责人      已批准 v5                                                   │
│ 设施负责人      待审批                                                       │
│ OT / 安全       不要求 · Governance policy v3                               │
│                                                                              │
│ 执行窗口：2026-09-18 22:00–23:30 · Asia/Shanghai · 需预留 30 min 回滚         │
│ [请求修改] [批准当前 Revision]                         [进入策略中心实施]    │
└──────────────────────────────────────────────────────────────────────────────┘
```

主界面始终先回答：**现在怎样、要改什么、影响什么、怎么保证边界、怎么验证、失败怎么恢复、谁批准。**

审批动作只针对当前 revision；“进入策略中心实施”只有在所有 execution gates 满足后才出现，并且只是 handoff，不在本页直接执行控制。

---

# 81. Accessibility

必须：

- Current / Proposed 不只靠颜色；
- semantic diff 可读屏；
- risk/approval 不只靠红绿；
- required approval 有文字；
- simulation chart 有 table/text alternative；
- guardrail bounds 可读取；
- dialog focus trap/return 正确；
- around 768px 不依赖 hover；
- 高风险 action 有明确 label 和确认上下文。

---

# 82. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- Plan identity/revision；
- approval state；
- objective；
- current/proposed summary；
- affected scope；
- top preconditions/guardrails；
- top risk；
- next action。

## 1024–1439 px

- Current / Proposed 可上下堆叠；
- sticky section nav 简化；
- simulation/detail 下沉。

## Around 768 px

仍必须能：

- 看 plan revision；
- 看 Current / Proposed；
- 看 affected scope；
- 看 preconditions/guardrails；
- 看 top risks；
- 看 Test / Rollback readiness；
- 看 Approval；
- 进入 owner execution workflow。

页面整体不横向溢出。

---

# 83. No Defensive Programming / No Compatibility Design

明确禁止：

```text
plan API error → []
current state unavailable → use plan snapshot as live state
opportunity missing → infer source from title
scope missing → infer from device names
preconditions unavailable → assume satisfied
guardrails unavailable → assume none
interlock unavailable → assume clear
comfort assessment unavailable → no impact
IAQ assessment unavailable → no impact
reliability assessment unavailable → low risk
simulation unavailable → passed
simulation favorable → approved
simulation favorable → verified
model outside applicability → use result anyway
expected effect missing → 0 kWh
cost effect missing → $0
carbon effect missing → 0 tCO₂e
risk missing → low
change class missing → standard change
approval unavailable → pending
approval service error → preserve local approved state
new plan revision → inherit previous approval automatically
approved plan → execution authorized everywhere
approved plan → bypass safety permit
approved plan → bypass interlock
approved plan → direct point write from frontend
execution scheduled → executed
control ACK → plan success
work completed → plan success
functional verification pass → verified savings
rollback plan exists → auto rollback active
rollback command ACK → state restored
rollback target missing → use previous value
execution window missed → silently execute next night
implementation differs from approved plan → ignore drift
source opportunity updated → silently rewrite approved plan
same plan title → merge plans
multiple optimization APIs → first success wins
one plan → one request per affected asset N+1
old Optimization Dashboard adapter
old AI Optimize page fallback
old direct-control shortcut
frontend-generated approval
frontend-generated risk class
frontend-generated guardrail
frontend-generated simulation pass/fail
```

不建立：

```text
new Optimization Plan unavailable
→ fallback old optimization/control page
```

原则：

> **One optimization plan → one authoritative revisioned change proposal. Opportunity is not a plan. Approval binds to a revision. Simulation is not proof. Guardrails are not guessed. Approval does not bypass authority or interlocks. Execution is not verification. Rollback is a controlled execution path, not a magic undo. Unknown stays unknown.**

---

# 84. Browser Acceptance Criteria

## Revision

- current revision 显式；
- approved revision immutable；
- material change 形成 new revision；
- previous approval 不静默继承。

## Current / Proposed

- semantic diff 可读；
- current-state source/time 可追溯；
- proposed change 足够明确可评审；
- raw JSON 不是默认 UI。

## Scope / Risk

- affected scope/blast radius 可见；
- dependency 可见；
- risk category/reason/mitigation 可见；
- missing risk ≠ low risk。

## Preconditions / Guardrails

- 二者分开；
- owner/source 可查；
- unknown 不显示 satisfied；
- interlock 不可被 approval 绕过。

## Simulation

- model/version/input/assumptions 可追溯；
- applicability 可见；
- Simulation ≠ Verification；
- unavailable 不显示 passed。

## Test / Rollback

- required test plan 可见；
- verification requirement 明确；
- rollback target/trigger/method/authority 可见；
- rollback plan ≠ auto rollback。

## Approval

- approval 绑定 revision；
- required approvers/decisions/conditions 可见；
- conditional approval gate 可见；
- approval 不等于 execution permission。

## Execution

- execution window/timezone/conditions 可见；
- implementation route 明确；
- physical/control/rule change 进入各自 owner；
- execution drift 可见；
- no direct generic multi-domain execution button。

## Verification / M&V

- Execution ≠ Functional Verification；
- Functional Verification ≠ Verified Savings；
- 13 / 24 handoff 明确。

## Responsive / Accessibility

- 1440–1720px 是 coherent engineering change workspace；
- around 768px 核心 review/task 完整；
- no color-only diff/risk/approval；
- no hover-only critical information；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old Optimization/AI Control compatibility adapter；
- 无 frontend-generated approval/risk/guardrail/simulation truth；
- 无 N+1 affected-scope fan-out；
- 无 unknown→safe/approved；
- review scenario 无 runtime/network error。

---

# 85. Explicit Non-Goals

本页不是：

- PLC/BAS programming IDE；
- protection relay tool；
- low-level point-write console；
- generic project management system；
- Work Order execution；
- M&V report；
- simulation engine；
- OT security administration console；
- approval-only workflow product；
- AI autonomous optimizer。

---

# 86. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Opportunity / Plan / Approval / Execution / Verification 已分离；
- Plan revision / approval binding 已明确；
- Current State / Proposed Change 已明确；
- Affected Scope / Blast Radius 已明确；
- Preconditions / Guardrails / Interlocks 已分离；
- Simulation / What-if lineage 与 applicability 已明确；
- Simulation ≠ Functional Verification 已明确；
- Expected Effect dimensions 已分离；
- Comfort / IAQ / Reliability / Safety / OT impacts 已明确；
- Risk / Change Classification boundary 已明确；
- Test Plan / Functional Verification handoff 已明确；
- Rollback Plan / target / trigger / execution lifecycle 已明确；
- Approval matrix / conditions / revision binding 已明确；
- Execution Window / Implementation Route 已明确；
- Work / Control / Strategy / Rule boundaries 已明确；
- Execution Drift 已明确；
- M&V boundary 已明确；
- AI 只辅助、不成为审批或工程 truth 已明确；
- 中文优先产品语言已落实；
- no defensive fallback contract 已接受；
- old Optimization / AI Control 页面没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
