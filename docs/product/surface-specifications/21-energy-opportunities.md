# 21 节能机会 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `21 节能机会`  
> **Route intent：** `/sites/:siteId/opportunities`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`M&V`、`LCC`、`SIR`、`IRR` 等标准缩写只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有节能建议页、AI 节能卡片、旧 Opportunity 页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Opportunity / Evidence / Estimate / Economics / Risk / Workflow / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **把有证据支持的能源绩效改善机会组织成一个可解释、可比较、可排序、可持续推进的机会组合，并明确哪些机会值得继续开发成正式优化方案。**

本 Surface 是 **evidence-backed improvement-opportunity portfolio workspace**，不是：

- AI 自动生成“十大节能建议”的页面；
- 控制动作执行页；
- 设备维修任务列表；
- 22「优化方案」的简化版；
- 23「目标与行动计划」的替代页；
- 24「M&V」的预先节能量结论页；
- 把所有异常都自动转换成节能项目的页面；
- 单纯按 Payback 排序的项目池；
- 黑盒 ROI/AI Score 排行榜。

用户离开本页前应该知道：

1. 当前有哪些值得评审的节能/能源绩效改善机会；
2. 每个机会来自什么证据和业务上下文；
3. 预期收益是什么，能源、需量、成本、碳分别如何表达；
4. 收益估算使用什么 calculation basis 和假设；
5. 当前 estimate / evidence / feasibility confidence 是什么；
6. 机会适用于什么工况、哪些对象和哪些边界；
7. 可能影响哪些舒适、IAQ、安全、可靠性、生产或运维约束；
8. 机会的大致实施成本/复杂度/资源需求是什么；
9. 为什么它当前排在前面或后面；
10. 下一动作是补证据、继续评估、转优化方案、搁置还是归档；
11. 哪些机会已经转入 22「优化方案」；
12. 哪些结果最终需要 13 Functional Verification 和 24 M&V 验证。

---

# 2. 主要用户

## Primary

### 能源经理

维护机会池、组织优先级评审、分配 owner、选择进入方案开发的机会。

### 能源工程师

评估技术可行性、收益估算、适用条件、风险、约束和证据充分性。

### 运行 / HVAC 工程师

把运行异常、诊断 Finding、低效率、控制问题转化为可评审的改善机会。

## Secondary

- 财务/管理人员：读取已有经济性结果与优先级依据；
- 维修负责人：识别机会是否其实是纠正性维修；
- 可持续发展负责人：读取碳收益，但不把碳估算当 inventory verified reduction；
- 控制工程师：消费机会并在 22 中形成正式 change proposal；
- M&V 工程师：提前了解预期收益与 measurement intent；
- 数据工程师：处理 estimate 所需的数据质量与 lineage。

---

# 3. 外部最佳实践依据

## 3.1 DOE / 50001 — 识别并优先排序能源绩效改善机会

DOE AMO eGuide 的 ISO 50001 实施指导明确把 `Identify and prioritize energy opportunities` 作为能源评审和能源规划的重要环节，并指出机会可以来自运行实践、设备/系统改善和先进技术。

来源：

- https://www1.eere.energy.gov/manufacturing/eguide/iso_step_2_6.html
- https://www1.eere.energy.gov/manufacturing/eguide/foundational_step_2_10.html

**本页采用：**

- Opportunity 必须有 evidence/rationale；
- 机会识别和“选择实施”是两个阶段；
- 优先级依据组织 criteria，而不是统一黑盒公式；
- 资源、组织优先级、目标、技术可行性都可以影响是否进入下一阶段。

## 3.2 DOE/FEMP EMIS — Opportunity 需要进入持续 tracking workflow

DOE/FEMP EMIS Operations Support 建议能源管理团队使用 EMIS 识别 improvement opportunities，并按 calculated energy savings、criticality 等定性/定量指标进行排序；还建议使用 issue/opportunity tracking system 贯穿实施周期。

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

**本页采用：**

- Ledger 是默认运营视图；
- 机会不是一次性报告项；
- owner、status、next action、evidence、follow-up 必须可追踪；
- 优先级应可解释。

## 3.3 DOE Better Plants Energy Treasure Hunt — 机会识别、量化、跟踪分阶段

DOE Better Plants 的 Energy Treasure Hunt 方法强调跨职能团队识别并测量节能机会，并提供 Opportunity Sheet、Summary Report 和 Project Implementation Tracker。

来源：

- https://betterbuildingssolutioncenter.energy.gov/better-plants/better-plants-trainings
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/Better%20Plants%20Energy%20Treasure%20Hunt%20Exchange%20Toolkit.pdf
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/Energy%20Treasure%20Hunt%20Fact%20Sheet_0.pdf

**本页采用：**

- Opportunity Sheet 与 Implementation Plan 分离；
- 低/无成本运行改善和资本项目都可以成为 Opportunity；
- Opportunity 需要 evidence、estimated benefit 和 follow-up；
- 发现机会不代表批准实施。

## 3.4 ISO 50001:2018 — Energy opportunity 属于持续改进，不是单项目 KPI

ISO 50001:2018 建立基于 PDCA 的能源管理体系，用于持续改善 energy performance。

来源：

- https://www.iso.org/standard/69426.html

**本页采用：**

- Opportunity 必须与 Energy Review、EnPI/EnB、Objectives、Action Plans 相连；
- 不做脱离治理链的“AI 节能建议”；
- Opportunity 的选择标准可以体现组织优先级与能源绩效目标。

## 3.5 DOE/FEMP Life-Cycle Cost — 经济性不是只有 Payback

DOE/FEMP Building Life Cycle Cost（BLCC）用于比较能源/水/可再生能源项目的全生命周期经济性，并支持 Net Savings、Savings-to-Investment Ratio、Adjusted IRR、Payback 等指标。

来源：

- https://www.energy.gov/cmei/femp/building-life-cycle-cost-programs

**本页采用：**

- 可以消费 LCC / SIR / IRR / Payback 等专业经济指标；
- 不把 Payback 作为唯一排序标准；
- 财务指标必须有 calculation owner、currency、assumptions、analysis horizon；
- Opportunity 页不在前端临时实现 authoritative finance model。

## 3.6 DOE/FEMP M&V / IPMVP — Expected Savings 不是 Verified Savings

DOE/FEMP M&V Guidelines 和 IPMVP 都强调 savings 不能直接测量，需要在项目实施前后基于 baseline/reporting period、measurement boundary 和 adjustments 进行验证。

来源：

- https://www.energy.gov/cmei/femp/articles/mv-guidelines-measurement-and-verification-performance-based-contracts-version-0
- https://www.energy.gov/sites/default/files/2024-03/femp-mv-5-0-guidelines_DRAFT.pdf
- https://www.evo-world.org/en/822-main-menu/product-a-services/protocols

**本页采用：**

```text
Expected Benefit
≠
Verified Savings
```

- Opportunity 可以包含 estimate；
- estimate 必须说明 method/assumptions/confidence；
- verified savings 只能来自后续 M&V owner。

---

# 4. 产品语言契约

主界面中文优先：

```text
节能机会
机会来源
证据范围
预期收益
估算依据
置信度
适用条件
实施复杂度
预计成本
风险与约束
负责人
下一动作
已转优化方案
```

避免主界面只有：

```text
Opportunity
Evidence Scope
Expected Benefit
Confidence
Applicability
Risk Score
Business Case
```

行业缩写保留时要有上下文：

```text
生命周期成本（LCC）
投资节省比（SIR）
内部收益率（IRR）
测量与验证（M&V）
```

---

# 5. Opportunity Domain Vocabulary

## 5.1 Opportunity / 节能机会

有足够证据表明**值得继续评审**的一项能源绩效改善候选。

它表达：

> “这里可能存在值得开发的改善价值。”

它不表达：

> “这个变更已经批准，可以执行。”

## 5.2 Evidence

支持 Opportunity 存在的事实、Finding、趋势、基线偏离、费用暴露、运行证据、工程观察等。

Evidence 可以来自：

- 14 Energy Analysis；
- 15 Demand / Flexibility；
- 16 Efficiency；
- 17 Energy Review；
- 18 Billing / Tariff；
- 19 Carbon；
- 20 DER；
- 10 Diagnosis；
- 13 Functional Verification；
- Engineer observation / audit；
- approved external assessment。

## 5.3 Expected Benefit

实施该 Opportunity 后**预期**可能获得的结果。

可以包括：

```text
Energy reduction
Demand reduction
Cost reduction
Carbon reduction
Efficiency improvement
Runtime reduction
Maintenance/operability benefit
Resilience benefit
```

不同 benefit 必须分别表达。

## 5.4 Calculation Basis

Expected Benefit 的估算方法、输入、边界、假设、period、source 和 revision。

## 5.5 Confidence

表示某个 estimate/evidence/feasibility 结论在 owner 定义语义下的可信程度。

Confidence 不是自动等于概率。

## 5.6 Applicability

Opportunity 在什么对象、运行模式、时间、工况、约束下成立。

## 5.7 Constraint

影响机会开发/实施的条件，例如：

```text
舒适
IAQ
安全
可靠性
设备寿命
工艺/生产
维护窗口
预算
电网/并网
法规
人员/技能
```

## 5.8 Priority

组织依据明确 criteria 对 Opportunity 的相对处理顺序。

Priority 是治理结果，不是节能量大小的同义词。

---

# 6. Mandatory Semantic Separation

必须保持：

```text
Opportunity
≠ Approved Change

Opportunity
≠ Optimization Plan

Opportunity
≠ Work Order

Opportunity
≠ Diagnosis Finding

Expected Savings
≠ Verified Savings

Expected Cost Savings
≠ Billed Cost Reduction

Expected Carbon Reduction
≠ Inventory Reduction

High Potential
≠ High Priority

High Confidence
≠ Approved

High Confidence
≠ Verified

Estimate
≠ Business Case

Payback
≠ Priority

Low Cost
≠ Low Risk

Evidence
≠ Causality automatically

Finding
≠ Opportunity automatically

Alarm
≠ Opportunity automatically

Efficiency Deviation
≠ Savings automatically

Opportunity Selected for Development
≠ Implementation Approved
```

正式链路：

```text
Evidence / Finding / Analysis
↓
Opportunity
↓
Qualification / Prioritization
↓
Selected for Development
↓
22 Optimization Plan
↓
Approval
↓
Implementation
↓
13 Functional Verification
↓
24 M&V
```

---

# 7. Primary Questions

## Q1 — 现在最值得关注哪些机会？

默认 Ledger 按 owner-defined priority policy 排序，并解释原因。

## Q2 — 为什么这是机会？

必须能够看到：

- Problem / improvement statement；
- Source trail；
- evidence scope；
- supporting facts；
- contradicting/limiting evidence；
- affected objects。

## Q3 — 可能带来多少收益？

显示独立 benefit dimensions：

- energy；
- demand；
- cost；
- carbon；
- operational / maintenance；
- resilience when relevant。

## Q4 — 这个估算有多可靠？

显示：

- estimate method；
- data period；
- key assumptions；
- confidence semantics；
- missing evidence；
- model/applicability issues。

## Q5 — 有什么风险和约束？

显示：

- comfort / IAQ；
- safety；
- reliability；
- maintenance；
- process；
- grid / tariff；
- resource/dependency；
- implementation window。

## Q6 — 为什么它排在这个优先级？

显示 priority criteria contribution / rationale。

不显示不可解释的 `Opportunity Score 87`。

## Q7 — 下一步做什么？

典型动作：

```text
补充证据
继续技术评估
补充经济性分析
指定 Owner
转优化方案
搁置
归档 / 拒绝（有原因）
```

---

# 8. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/opportunities
```

长周期评审需要 durable identity：

```text
/sites/:siteId/opportunities/:opportunityId
```

仍属于 Surface 21 家族，不新增独立 Catalog surface。

推荐 Search Params：

```text
view
q
status
priority
source
benefitType
owner
confidence
applicability
constraint
from
until
sort
page
size
```

不进入 URL：

- hover；
- local accordion；
- unsaved draft；
- temporary column width。

---

# 9. Entry Contract

## 从 10 Diagnosis

携带：

```text
Finding
Affected objects
Verified facts
Hypotheses only if explicitly labelled
Evidence window
Source trail
```

Finding 不自动变成 Opportunity。

## 从 14 Energy Analysis

携带：

```text
Energy scope
Period
Variance window
Contributors
Baseline/comparison context
Evidence links
```

## 从 15 Demand / Flexibility

携带 peak/flexibility context、rebound、tariff exposure 和 resource scope。

## 从 16 Efficiency

携带 metric boundary、load band、expected model、outlier context 和 validity。

## 从 17 Energy Review

携带 SEU、EnPI、EnB version、performance gap、review revision。

## 从 18 Billing

携带 cost/tariff/reconciliation context。

## 从 19 Carbon

携带 inventory method、source、factor context 和 expected carbon opportunity context。

## 从 20 DER

携带 resource constraint、dispatch/flexibility evidence、cost/carbon/resilience context。

---

# 10. Exit Contract

Primary exit：

```text
Opportunity
→ 22 Optimization Plan
```

Supporting exits：

```text
→ 14 Energy Analysis
→ 15 Demand / Flexibility
→ 16 Efficiency
→ 10 Diagnosis
→ 05 Trend Analysis
→ 06/07 Device
→ 18 Billing
→ 19 Carbon
→ 20 DER
→ 31 Data Quality
```

实施后：

```text
22 Optimization
→ Work / Control / Strategy
→ 13 Verification
→ 24 M&V
```

21 不跳过 22 直接执行控制。

---

# 11. Responsibility Boundary

本 Surface 拥有：

- Opportunity identity / lifecycle；
- opportunity statement；
- evidence links / evidence sufficiency projection；
- affected scope；
- expected benefit projection；
- estimate method / revision reference；
- confidence projection；
- applicability；
- constraints / risks；
- preliminary economics projection；
- priority policy/result/rationale；
- owner / next action；
- conversion to Optimization Plan；
- opportunity history/audit。

本 Surface 不拥有：

- authoritative raw analytics；
- diagnosis root cause；
- engineering change design；
- simulation / rollback plan；
- implementation approval；
- control execution；
- work execution；
- verified savings；
- final M&V result；
- carbon inventory reduction；
- finance ledger；
- frontend-generated savings truth。

---

# 12. Information Architecture

```text
机会组合上下文
  Site · Review Scope · Saved View · Priority Policy

Compact Summary
  需评审
  待补证据
  已选开发
  高价值窗口 / 到期事项

Opportunity Toolbar
  Search · Source · Priority · Status · Owner · Benefit · Confidence

Opportunity Ledger
  Opportunity
  Evidence Scope
  Expected Benefit
  Confidence
  Effort / Cost
  Risk / Constraint
  Priority
  Status
  Owner
  Next Action
                                  → Opportunity Inspector

Opportunity Detail
  Opportunity Statement
  Evidence
  Expected Benefit
  Calculation Basis
  Confidence
  Applicability
  Constraints / Risks
  Economics
  Priority Rationale
  History
  [转优化方案]
```

默认不是 Opportunity Card Wall。

---

# 13. Opportunity Lifecycle Contract

推荐 conceptual lifecycle：

```text
New / Needs Review
↓
Qualified
↓
Under Evaluation
↓
Selected for Development
↓
Converted to Optimization Plan
```

并允许：

```text
Needs More Evidence
On Hold
Rejected / Archived
```

精确 enum 由 Opportunity owner 定义。

关键要求：

```text
Selected for Development
≠ Approved for Implementation
```

`Rejected / Archived` 必须有 reason。

---

# 14. Opportunity Identity Contract

至少有：

```text
Opportunity reference
Title
Site / scope
Source type
Created by / source owner
Created at
Current lifecycle state
Owner
Revision
```

主 UI 不以 internal UUID 为主要标识。

---

# 15. Opportunity Statement Contract

Opportunity statement 回答：

> “改善什么、为什么值得改善、预期产生什么价值？”

推荐结构：

```text
Current issue / inefficiency
Affected scope
Proposed improvement direction
Expected outcome
```

但在 21 不需要完整设计 `Proposed Change`。

完整 current/proposed state 属于 22。

---

# 16. Source / Evidence Contract

Opportunity 至少保留：

```text
Source surface / workflow
Source object
Evidence period/window
Evidence references
Data quality
Finding/reference where applicable
Created by
```

Evidence 可以包括：

- trend evidence；
- meter/energy evidence；
- efficiency deviation；
- verified diagnostic facts；
- bill/tariff evidence；
- functional-test failure；
- operator observation；
- approved assessment result。

---

# 17. Evidence Sufficiency Contract

Opportunity owner 可以定义：

```text
Sufficient for Screening
Needs More Evidence
Sufficient for Engineering Development
Insufficient / Inconclusive
```

但不能仅用 evidence count 决定。

例如：

```text
3 screenshots
```

不自动比：

```text
1 validated measurement
```

更可信。

---

# 18. Supporting / Limiting / Contradicting Evidence

Opportunity Detail 需要区分：

```text
Supporting Evidence
Limiting Evidence
Contradicting Evidence
Missing Evidence
```

避免只展示“支持节能”的证据造成 confirmation bias。

例如：

```text
机会：降低夜间冷站基载

Supporting
• 夜间 00:00–05:00 平均 420 kW
• 占用为零，多个 AHU 已关闭

Limiting
- 实验室区域存在 24h 冷却需求

Missing
- 冷站最小稳定负荷验证
```

---

# 19. Evidence Immutability / Reference Contract

Opportunity 不复制并修改原始事实。

应引用：

```text
Alarm occurrence
Finding revision
Trend evidence window
EnPI / EnB version
Bill revision
Carbon inventory revision
DER dispatch record
```

源事实后续修正时：

- 保留原引用；
- 标记 source revision / superseded；
- 触发 Opportunity review if material；
- 不静默改写历史 estimate。

---

# 20. Affected Scope Contract

至少可表达：

```text
System
Equipment
Zone
Meter / Energy boundary
Strategy
Schedule
Tariff / Account
DER Resource
Process / Business area
```

Affected scope 必须来自 authoritative semantic/registry relation 或用户显式选择。

前端不通过名字猜范围。

---

# 21. Opportunity Type Contract

可以采用 owner-defined categories，例如：

```text
Operational / Scheduling
Control Optimization
Maintenance-related Improvement
Retrofit / Equipment
Setpoint / Sequence Improvement
Demand / Tariff
DER / Storage
Behavior / Process
Energy Procurement
Data / Measurement Enablement
Other
```

Type 用于组织，不自动决定 priority、approval 或 savings method。

---

# 22. Corrective Work vs Energy Opportunity Boundary

如果问题本质是：

```text
设备故障
安全问题
必须恢复的设计功能
强制维修
```

优先进入 Work / Diagnosis / Verification。

不能为了形成节能项目，把必要维修包装成 Opportunity。

如果维修之外存在额外 improvement potential，可以建立独立 Opportunity 并链接 Work/Finding。

---

# 23. Expected Benefit Dimensions

必须分开：

```text
Energy
Demand
Cost
Carbon
Efficiency / Performance
Maintenance / Operability
Resilience
Other owner-defined
```

不允许一个没有单位和语义的：

```text
Benefit = 82
```

---

# 24. Energy Benefit Contract

Energy estimate 至少带：

```text
Energy carrier
Amount / range
Unit
Period / annualization basis
Measurement boundary
Calculation method
Assumptions
Source data
Estimate revision
Confidence
```

例如：

```text
预计节电
120–150 MWh/年
```

比：

```text
预计节电 137.42 MWh/年
```

更诚实，如果输入只支持范围估计。

---

# 25. Demand Benefit Contract

Demand benefit 至少需要：

```text
Expected kW reduction / shift
Applicable peak window
Duration
Rebound/recovery context
Tariff relevance if applicable
Method
```

必须保持：

```text
Peak reduction
≠ Demand-charge savings automatically
```

成本效果属于 tariff/cost calculation owner。

---

# 26. Cost Benefit Contract

Expected cost benefit 至少需要：

```text
Currency
Estimate period
Energy/demand/tariff assumptions
Rate/tariff version if used
Owner/calculation model
Range / scenario
Confidence
```

禁止：

```text
energy estimate × current average $/kWh
→ authoritative cost savings
```

除非正式 cost analytics owner就是这样定义。

---

# 27. Carbon Benefit Contract

Expected carbon benefit 至少需要：

```text
Inventory / project boundary
Method
Factor context
Reporting period
Location/market basis if Scope 2 relevant
Estimate nature
```

必须保持：

```text
Expected carbon benefit
≠ Inventory reduction automatically
```

19 Carbon owner决定 inventory accounting。

---

# 28. Non-Energy Benefit Contract

可以记录：

- improved reliability；
- reduced maintenance burden；
- comfort improvement；
- process stability；
- resilience；
- deferred capital；
- compliance benefit。

但必须标记 benefit type，不能折算成虚假 kWh。

---

# 29. Calculation Basis Contract

Expected Benefit 的每个 calculation 至少保留：

```text
Method name
Method version
Inputs
Input sources
Boundary
Period
Assumptions
Exclusions
Normalization if applicable
Uncertainty / range
Owner
Calculated at
```

前端展示结果，不拥有算法真相。

---

# 30. Estimate Revision Contract

Estimate 必须 versioned。

例如：

```text
Estimate v1
120–180 MWh/yr
粗筛选

Estimate v2
138–152 MWh/yr
增加 6 周负荷数据后
```

v2 不覆盖 v1 历史。

Priority decision 应能追溯当时使用哪个 estimate revision。

---

# 31. Confidence Contract

不要只放一个不解释的：

```text
Confidence 87%
```

建议至少区分：

```text
Evidence Confidence
Estimate Confidence
Feasibility Confidence
```

如果 owner 采用 qualitative levels：

```text
Low / Medium / High
```

必须给 definition。

如果 owner 使用概率/score，必须说明 calibration/meaning。

---

# 32. Confidence ≠ Verification

必须保持：

```text
High Estimate Confidence
≠ Verified Savings

High Feasibility Confidence
≠ Approved Change
```

Confidence 仅描述当前 evaluation quality。

---

# 33. Applicability Contract

每个 Opportunity 可以声明：

```text
Applicable objects
Operating modes
Load/weather/occupancy range
Season / schedule
Required capabilities
Required data
Required control authority
Effective period
Exclusions
```

例如：

```text
仅适用于：
工作日 18:00–06:00
无实验室高负荷模式
冷站负荷 < 35%
```

不能把局部工况机会推广成全年 savings。

---

# 34. Constraint Contract

Constraints 至少分类：

```text
Comfort / IAQ
Safety
Reliability
Process / Production
Maintenance
Control / Interlock
Grid / Interconnection
Tariff / Program
Budget / Procurement
People / Skill
Schedule / Shutdown Window
Regulatory
```

Constraint 是 Opportunity screening 一等事实。

---

# 35. Guardrail Boundary

21 可以显示 known guardrails 和 impact concern，但不设计具体 guardrail implementation。

详细：

```text
Current State
Proposed Change
Precondition
Guardrail
Rollback
```

属于 22 Optimization Plan。

---

# 36. Risk Contract

Risk 不做单一黑盒分数。

至少可区分：

```text
Safety Risk
Comfort / IAQ Risk
Reliability Risk
Operational Risk
Implementation Risk
Savings Risk
Financial Risk
Schedule Risk
Data / Measurement Risk
```

每个 risk 至少有：

```text
Level
Reason
Owner/source
Mitigation needed
```

---

# 37. Cost / Effort Contract

Opportunity 可以有 screening-level：

```text
No/Low Cost
Opex
Capex
Engineering Effort
Operational Effort
Shutdown Need
Lead Time
```

但这仍不是正式 implementation plan。

成本可表达：

```text
Range
Class / Rough Order of Magnitude
Owner
Currency
Estimate date
Basis
```

不强迫假精确值。

---

# 38. Economic Evaluation Contract

如有正式 economics owner，可以显示：

```text
Simple Payback
Life-Cycle Cost (LCC)
Net Savings
Savings-to-Investment Ratio (SIR)
IRR
NPV
```

必须带：

```text
Analysis horizon
Discount assumptions
Currency
Cost basis
Savings basis
Owner/model
Revision
```

21 不自行计算 authoritative economics。

---

# 39. Payback Boundary

必须保持：

```text
Shortest Payback
≠ Highest Priority
```

原因可能包括：

- safety/reliability risk；
- strategic target；
- shutdown opportunity；
- critical energy use；
- prerequisite/dependency；
- data uncertainty；
- large absolute benefit；
- regulatory need。

---

# 40. Priority Policy Contract

Priority 由 Opportunity Governance owner 提供。

可以考虑：

```text
Expected energy benefit
Expected demand/cost/carbon benefit
Confidence
Implementation cost
Complexity
Risk
Urgency / opportunity window
Strategic alignment
SEU / EnPI relevance
Dependencies
Resource availability
```

但 UI 不自己加权。

---

# 41. Explainable Priority Contract

如果显示：

```text
优先级：高
```

用户必须能看到 rationale，例如：

```text
高预期节能潜力
低实施复杂度
证据充分
与 2027 EnPI 目标直接相关
仅本季度停机窗口可实施
```

而不是：

```text
AI Opportunity Score 91
```

---

# 42. Portfolio Ranking Contract

默认排序可以采用 owner policy，例如：

```text
Priority
→ Next-action urgency
→ Evidence readiness
→ Opportunity window
→ Owner-defined secondary sort
```

不能默认只按：

```text
Expected kWh desc
```

或者：

```text
Payback asc
```

---

# 43. Saved Views / Operational Views

推荐 saved views，而不是把生命周期强行做成很多 Tabs：

```text
需评审
待补证据
高优先级
低/无成本
已选开发
待 Owner
受阻 / On Hold
已转优化方案
```

它们是 query presets，不一定是 domain peer views。

---

# 44. Opportunity Ledger Contract

默认 8–10 个核心业务列：

```text
机会
来源 / 证据范围
预期收益
置信度
成本 / 复杂度
主要风险 / 约束
优先级
状态
Owner
下一动作
```

不要放：

- internal UUID；
- raw model id；
- ingestion source；
- internal trace id；
- 20 个 technical fields。

---

# 45. Opportunity Inspector Contract

Inspector 只用于快速 triage：

```text
Opportunity statement
Source/evidence
Expected benefit
Confidence
Applicability
Top risks/constraints
Priority rationale
Owner
Next action
```

不放：

- 完整 calculation workbook；
- 完整经济分析；
- 工程变更设计；
- Simulation；
- Approval；
- Rollback plan。

深度评审进入 durable Opportunity Detail。

---

# 46. Opportunity Detail Contract

Durable detail 包含：

```text
Opportunity Identity
↓
Why this matters
↓
Evidence
  Supporting
  Limiting
  Contradicting
  Missing
↓
Expected Benefits
↓
Calculation Basis / Estimate Revision
↓
Confidence
↓
Applicability
↓
Constraints / Risks
↓
Economics
↓
Priority Rationale
↓
Owner / Next Action
↓
History
↓
[转优化方案]
```

---

# 47. Duplicate / Related Opportunity Contract

不能基于：

```text
same title
same asset
same source
same timestamp
text similarity
```

自动 merge Opportunities。

允许：

```text
Related Opportunities
Potential Duplicate - Needs Review
Parent / Child if owner defines
Alternative Measures
Mutually Exclusive Measures
```

Merge 是显式 governance action，并保留 history/source relations。

---

# 48. Alternative / Mutually Exclusive Measures

一个问题可能有多个 Opportunity：

```text
Opportunity A
优化冷机启停排序

Opportunity B
更换 CH-03

Opportunity C
增加冷量储能
```

它们可能竞争或互斥。

21 应保留关系，而不是提前合并成一个“最佳方案”。

真正方案比较进入 22。

---

# 49. Dependency Contract

Opportunity 可以依赖：

- Meter fix；
- data quality improvement；
- maintenance work；
- controls upgrade；
- shutdown window；
- procurement；
- another Opportunity / Optimization Plan。

显示：

```text
Dependency
Status
Owner
Impact on next action
```

依赖未满足不能静默标 Ready。

---

# 50. Opportunity Window / Expiry Contract

有些机会受时间窗口影响：

```text
planned shutdown
season
tariff change
capital planning cycle
lease/event schedule
DR enrollment deadline
```

可以显示：

```text
Opportunity Window
Valid until
Review by
```

但不把所有 Opportunity 强行设 expiry。

---

# 51. Owner / Responsibility Contract

至少分开：

```text
Opportunity Owner
Technical Reviewer
Business Sponsor（如有）
Next-action Owner
```

Owner ≠ Approver。

正式审批 belongs to 22/23/organizational workflow。

---

# 52. Next Action Contract

每个 active Opportunity 默认必须有明确 next action，例如：

```text
验证夜间负荷边界
补充 4 周趋势
获取预算报价
确认停机窗口
进行 controls feasibility review
转优化方案
等待设备维修完成
```

禁止只有：

```text
Status = In Progress
```

却没有下一动作。

---

# 53. Convert to Optimization Plan Contract

这是 21 最重要 mutation 之一。

Conversion 至少携带：

```text
Opportunity ID / revision
Problem / opportunity statement
Affected scope
Evidence references
Expected benefit + estimate revision
Applicability
Constraints / risks
Known dependencies
Economics references
Priority rationale
Owner
```

22 再建立：

```text
Objective
Current State
Proposed Change
Preconditions
Guardrails
Simulation
Test Plan
Rollback
Approval
Execution Window
```

---

# 54. Opportunity → Optimization Semantic Boundary

必须保持：

```text
Opportunity
= 值得进一步开发的改善候选

Optimization Plan
= 可评审、可审批、可回滚的工程变更方案
```

所以：

```text
[转优化方案]
```

不是：

```text
[实施]
```

---

# 55. Objectives / Action Plan Boundary

23「目标与行动计划」回答：

> 我们正式承诺实现什么目标，以及通过哪些 action plans 推进？

21 回答：

> 当前有哪些可能帮助改善能源绩效的候选机会？

必须保持：

```text
Opportunity
≠ Objective
≠ Action Plan
```

Opportunity 可以关联一个或多个目标。

---

# 56. M&V Boundary

Expected Benefit 可以是：

```text
预计节电 150 MWh/年
```

但不能显示：

```text
已节电 150 MWh/年
```

直到 24 M&V 完成正式 verification。

FEMP/IPMVP 的 baseline/reporting/adjustment 方法属于 M&V owner。

---

# 57. M&V Readiness Context

对于潜在大型 Opportunity，可以在 21 提前记录：

```text
Potential Measurement Boundary
Candidate Baseline Data
Required Metering
Expected Verification Difficulty
```

但这只是 `M&V planning context`，不是正式 M&V Plan。

---

# 58. Functional Verification Boundary

如果 Opportunity 最终涉及 sequence/control/repair：

实施后可能要求 13 Functional Verification。

必须保持：

```text
Functional Verification PASS
≠ Verified Savings
```

以及：

```text
Opportunity estimate
≠ Functional test result
```

---

# 59. Carbon Boundary

Expected Carbon Benefit 只能消费 Carbon method/factor context。

不能：

```text
Expected Energy Saving × hardcoded factor
→ authoritative carbon reduction
```

19 Carbon owner负责 inventory semantics。

---

# 60. Data Quality Contract

Opportunity 本身需要知道关键输入质量：

```text
Good
Estimated
Partial
Missing
Suspect
Stale
Corrected
Unknown
```

Estimate 不能隐藏：

```text
40% input data estimated
```

这种重要限制。

---

# 61. Data Authority Contract

## Evidence facts

Owner：对应 source domain。

## Finding

Owner：Diagnosis domain。

## Energy / Demand / Efficiency analytics

Owner：14/15/16 对应 analytics domain。

## EnPI / EnB

Owner：Energy Review / Performance domain。

## Cost / Tariff

Owner：Billing / Tariff domain。

## Carbon

Owner：Carbon Accounting domain。

## DER flexibility

Owner：DER / Flexibility domain。

## Opportunity lifecycle / priority

Owner：Opportunity Management domain。

## Economics

Owner：Financial/Economic Analysis domain。

## Verified Savings

Owner：M&V domain。

Frontend 不成为任何这些事实的替代 owner。

---

# 62. Query / Read Model Contract

推荐：

```text
Opportunity Portfolio Projection
  + identity/title
  + source/evidence summary
  + expected benefit summary
  + confidence summary
  + cost/effort summary
  + risk/constraint summary
  + priority/rationale summary
  + lifecycle state
  + owner
  + next action
  + optimization-plan link
```

Detail 再 query：

```text
Opportunity Evidence
Estimate Revisions
Economics
Constraints
History
Related Opportunities
```

禁止：

```text
100 opportunities
→ 100 evidence requests
→ 100 savings requests
→ 100 economics requests
→ 100 owner requests
```

缺 projection 时修 domain/read model，不在前端 fan-out。

---

# 63. Mutation Contract

允许的 mutation 可以包括：

- create opportunity from selected evidence；
- qualify / mark needs evidence；
- assign owner；
- add/revise estimate；
- add constraint/risk；
- update priority through owner workflow；
- set next action；
- hold / resume；
- reject/archive with rationale；
- relate/merge if authorized；
- convert to Optimization Plan。

所有 mutation：

1. 显式用户动作；
2. 权限校验；
3. authoritative revision check；
4. server-confirmed result；
5. audit trail。

---

# 64. Concurrency / Revision Contract

Opportunity、Estimate、Priority Evaluation 都是多人协作对象。

要求：

```text
revision/version
conflict detection
refetch authoritative state
user reconfirm
```

禁止 silent last-write-wins。

也不做前端自动 merge engineering estimates。

---

# 65. AI Assistance Boundary

AI 可以：

- 汇总 evidence；
- 草拟 Opportunity statement；
- 提醒缺失 evidence；
- 草拟 calculation assumptions；
- 解释 estimate method；
- 提出候选 next action；
- 帮助整理 risks/constraints。

AI 输出必须有来源/provenance。

AI 不能：

```text
AI guess → Expected Savings authoritative
AI confidence → engineering confidence
AI recommendation → Approved Opportunity
AI recommendation → Optimization Plan approved
AI recommendation → Control execution
```

---

# 66. Loading / Empty / Partial / Error

## No opportunities

成功返回 empty：

> `当前范围内暂无已登记节能机会。`

可提供：

- 从 Energy Review 开始；
- 从 Energy/Efficiency 分析发现机会；
- 有权限时创建 Opportunity。

## Service unavailable

> `节能机会数据暂不可用。`

不能显示 `0 个机会`。

## Benefit unavailable

机会仍存在：

> `预期收益尚未完成估算。`

不能显示 0 kWh。

## Economics unavailable

显示：

> `经济性分析尚未完成。`

不能显示 Payback = 0。

## Confidence unavailable

显示 Unknown / Not Assessed。

不能默认 Medium。

---

# 67. Permission Contract

示例：

```text
opportunity.read
opportunity.create
opportunity.edit
opportunity.assign
opportunity.evaluate
opportunity.prioritize
opportunity.archive
opportunity.convert-to-plan
economics.read
```

无权限动作不显示。

UI hiding 不替代 server authorization。

---

# 68. Visual / UX Contract

默认视觉层级：

```text
Opportunity Portfolio Context
↓
Needs Attention / Saved View
↓
Opportunity Ledger
↓
Selected Opportunity Inspector
↓
Evidence / Benefit / Confidence / Risk
↓
Priority Rationale / Next Action
↓
Professional Detail
```

禁止：

- 满屏“节能建议卡”；
- 绿色越多代表越好；
- Opportunity Score 0–100；
- AI 推荐占据首屏；
- Payback 单指标排序统治页面；
- 把 Expected Savings 做成已实现收益；
- 用一个综合气泡图替代 Ledger。

---

# 69. Portfolio Visualization Contract

可选 portfolio visualization：

```text
Expected Benefit vs Effort
Expected Benefit vs Confidence
Risk vs Benefit
```

前提：

- 两轴语义和单位明确；
- unknown 不被 0 代替；
- economics/confidence owner 已提供可比指标；
- 图只是辅助，Ledger 仍是主工作区。

不同单位机会不能强行映射为一个单一“收益轴”。

---

# 70. Chart / ECharts Boundary

ECharts 可负责：

- portfolio distribution；
- benefit range；
- opportunity trend；
- optional benefit-vs-effort plot。

ECharts 不负责：

- savings calculation；
- confidence calculation；
- priority calculation；
- economics calculation；
- risk scoring；
- opportunity qualification；
- approval。

---

# 71. Component Mapping

```text
Page Context              → standard page header
Saved Views               → Tabs only if true peer views, otherwise buttons/select
Filter / Search           → Search + Popover + Select
Opportunity Ledger        → shadcn Table + TanStack Table
Opportunity Inspector     → Sheet / side inspector
Durable Detail            → Route-owned page
Evidence                  → structured list + deep links
Benefit                   → compact semantic facts
Confidence                → Badge + explanation
Risk / Constraints        → structured list
Estimate History          → Table / Timeline
Convert to Plan           → Dialog confirmation + route handoff
```

不建立 universal OpportunityCard / generic CRUD engine。

---

# 72. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 节能机会 · 中央冷站                                  优先级规则：Energy v4    │
│ [需评审 6] [待补证据 3] [已选开发 4] [低/无成本 5]                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ 搜索机会…  [来源] [预期收益] [置信度] [Owner] [状态]                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 机会                  预期收益         置信度    复杂度   优先级  下一动作     │
│ 优化夜间冷站最小负荷   120–150 MWh/年   中-高      低       高    补趋势证据    │
│ 优化冷机启停排序       85–110 MWh/年    高         中       高    转优化方案    │
│ 降低夏季需量峰值       140–180 kW       中         中       中    确认回弹      │
│ 冷却塔控制优化         40–65 MWh/年     中         低       中    评估湿球条件  │
├──────────────────────────────────────────┬───────────────────────────────────┤
│ 选中机会：优化夜间冷站最小负荷           │ 为什么优先                        │
│ 来源：能源分析 + 趋势证据                 │ · 节能潜力高                       │
│ 适用：工作日 18:00–06:00                  │ · 实施复杂度低                     │
│ 证据：6 周夜间基载持续偏高                 │ · 与 SEU 冷站直接相关               │
│ 限制：实验室区域存在 24h 负荷              │ · 仍需确认最小稳定负荷              │
│ 估算：v2 · 120–150 MWh/年                  │                                   │
│ [查看详情] [补充证据] [转优化方案]          │ 下一动作：验证最小稳定负荷          │
└──────────────────────────────────────────┴───────────────────────────────────┘
```

主界面中文；`SEU / M&V / LCC / SIR / IRR` 只在专业上下文保留缩写。

---

# 73. Accessibility

必须：

- Priority/Confidence 不只靠颜色；
- range 有可读单位；
- table 使用 semantic headers；
- evidence links keyboard 可达；
- inspector focus 正确；
- priority rationale 有文字；
- chart 有 table alternative；
- around 768px 不依赖 hover。

---

# 74. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- saved view / attention context；
- Opportunity Ledger；
- expected benefit；
- confidence；
- priority；
- next action；
- selected inspector。

## 1024–1439 px

- Inspector 变 Sheet；
- 次要 economics columns 下沉；
- Ledger 保留核心字段。

## Around 768 px

仍必须能：

- 找到 Opportunity；
- 看 evidence/source；
- 看 expected benefit；
- 看 confidence/priority；
- 看 next action；
- 打开 detail；
- 转 Optimization Plan（有权限时）。

Table 可内部横向滚动；页面整体不横向溢出。

---

# 75. No Defensive Programming / No Compatibility Design

明确禁止：

```text
opportunity API error → []
benefit unavailable → 0 kWh
cost unavailable → $0
confidence unavailable → Medium
risk unavailable → Low
priority unavailable → Normal
evidence missing → assume sufficient
finding exists → auto create opportunity
alarm exists → auto create opportunity
efficiency deviation → savings estimate
energy variance → savings estimate
baseline difference → expected savings automatically
expected savings → verified savings
expected cost savings → billed savings
expected carbon reduction → inventory reduction
high savings → high priority
short payback → high priority
low cost → low risk
AI suggestion → authoritative opportunity
AI confidence → estimate confidence
same title / same asset → merge opportunities
related opportunities → deduplicate by text
missing affected scope → infer from names
missing tariff → use average $/kWh
missing carbon factor → hardcoded factor
missing economics → frontend calculate payback
missing priority policy → sort by savings desc
selected for development → approved
convert to plan → execute control
rejected → delete history
estimate revision → overwrite old estimate
source fact revised → silently rewrite estimate
multiple opportunity APIs → first success wins
one opportunity → one evidence/economics/owner request N+1
old Savings Recommendation adapter
old AI Opportunity page fallback
frontend-generated verified savings
frontend-generated priority score
```

不建立：

```text
new Opportunity unavailable
→ fallback old energy-savings cards
```

原则：

> **One opportunity → one authoritative lifecycle. Evidence, expected benefit, confidence, priority and approval remain separate facts. Opportunity is not an approved change. Expected is not verified. High potential is not automatically high priority. Unknown stays unknown.**

---

# 76. Browser Acceptance Criteria

## Portfolio

- 默认是 Ledger-first；
- saved view/filter 不改变 domain truth；
- priority 可解释；
- no black-box score。

## Evidence

- source/evidence window 可追溯；
- supporting/limiting/contradicting/missing evidence 可表达；
- source revision 可追溯；
- evidence unavailable 不变成 `sufficient`。

## Benefit

- Energy/Demand/Cost/Carbon 分开；
- range/unit/period/boundary 可见；
- Estimate revision 可查；
- Expected ≠ Verified。

## Confidence

- confidence meaning 可解释；
- evidence/estimate/feasibility confidence 不强行合并；
- missing confidence ≠ Medium。

## Priority

- criteria/rationale 可见；
- High Potential ≠ High Priority；
- Payback 不成为唯一 priority owner。

## Risk / Constraints

- comfort/IAQ/safety/reliability/process constraints 可表达；
- low cost 不冒充 low risk；
- dependency 未满足不显示 Ready。

## Workflow

- Opportunity lifecycle 可追踪；
- Selected for Development ≠ Approved；
- Convert to Plan 进入 22；
- 没有一键执行控制；
- reject/archive 保留 history/rationale。

## Boundaries

- Energy/Demand/Efficiency → 14/15/16；
- Energy Review → 17；
- Billing → 18；
- Carbon → 19；
- DER → 20；
- Optimization Plan → 22；
- Objectives/Actions → 23；
- Verified Savings → 24；
- Data Quality → 31。

## Responsive / Accessibility

- 1440–1720px 是 coherent opportunity portfolio；
- around 768px 核心任务完整；
- no color-only priority/confidence；
- no hover-only critical information；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 Savings/AI Recommendation compatibility adapter；
- 无 frontend-generated savings/priority/economics truth；
- 无 N+1 portfolio fan-out；
- 无 unknown→0；
- review scenario 无 runtime/network error。

---

# 77. Explicit Non-Goals

本页不是：

- engineering design studio；
- control center；
- project approval board；
- work-order system；
- M&V savings report；
- financial investment committee system；
- AI recommendation feed；
- generic idea-management platform；
- automatic project optimizer。

---

# 78. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Opportunity / Optimization Plan / Approved Change 已分离；
- Evidence/source trail 已明确；
- Supporting / Limiting / Contradicting / Missing Evidence 已明确；
- Expected Benefit dimensions 已分离；
- Calculation Basis / Estimate Revision 已明确；
- Confidence semantics 已明确；
- Applicability / Constraints / Risks 已明确；
- screening cost / economics boundary 已明确；
- explainable Priority policy 已明确；
- Lifecycle / Owner / Next Action 已明确；
- Convert to Optimization Plan handoff 已明确；
- Expected Savings ≠ Verified Savings 已明确；
- Opportunity ≠ Objective / Action Plan 已明确；
- 中文优先产品语言已落实；
- no defensive fallback contract 已接受；
- old Savings Recommendation / AI Opportunity 页面没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
