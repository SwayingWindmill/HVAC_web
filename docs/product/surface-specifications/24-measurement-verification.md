# 24 节能量验证 M&V Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `24 节能量验证 M&V`  
> **Route intent：** `/sites/:siteId/mv`、`/sites/:siteId/mv/:mvProjectId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`M&V`、`IPMVP`、`EnPI`、`EnB`、`CV(RMSE)`、`NMBE` 等标准缩写只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有节能报表、旧 M&V 页面、简单基线差值页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 M&V Plan / Baseline / Reporting Period / Model / Adjustment / Savings / Uncertainty / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **在明确测量边界、基线、报告期、调整方法和数据质量的前提下，对实施后的节能量进行可复算、可解释、可审计的验证，并持续区分“预计节能”“运行功能已验证”和“已验证节能量”。**

本 Surface 是 **measurement-and-verification governance + verified-savings evidence workspace**，不是：

- 14「能源分析」的 `Actual - Comparison` 页面；
- 17「能源评审」的 EnPI/EnB 编辑页；
- 22「优化方案」的 expected-benefit 页面；
- 13「功能验证」的替代页；
- 一张 `Baseline - Actual = Savings` 的简单图；
- regression playground；
- AI 自动给“节能量可信度”的页面。

用户离开本页前应该知道：项目验证对象、M&V 方法、Measurement Boundary、Baseline Period、Reporting Period、Baseline Model、Independent Variables、Routine / Non-routine Adjustments、Reporting Actual、Adjusted Baseline、Verified Savings、Model Quality / Validity / Uncertainty、Operational Verification、Result Revision 与 Persistence 状态。

---

# 2. 主要用户

- **M&V 工程师：**维护 M&V Plan、Baseline Model、adjustments、结果、uncertainty 和 audit evidence。
- **能源经理：**消费 verified result，判断目标 / Action Plan 的正式绩效结果。
- **能源分析工程师：**检查边界、基线、变量、数据质量和 calculation lineage。
- **运行/HVAC/控制工程师：**提供 operational verification 与现场变化事实。
- **审计/合同/管理人员：**读取可审计结果，不改写技术事实。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP M&V Guidelines 5.0

FEMP 2024 发布 M&V Guidelines Version 5.0，用于验证和量化 performance-based projects 的 savings。

来源：

- https://www.energy.gov/cmei/femp/articles/mv-guidelines-measurement-and-verification-performance-based-contracts-version-0
- https://www.energy.gov/cmei/femp/measurement-and-verification-federal-energy-savings-performance-contracts

**本页采用：** M&V 是正式 Plan；方法强度应与风险和预期节能规模匹配；结果必须可复核、复算、审计；post-installation 与 performance-period results 保留历史链。

## 3.2 FEMP / IPMVP Options A/B/C/D

```text
Option A — Retrofit Isolation / Key Parameter Measurement
Option B — Retrofit Isolation / All Parameter Measurement
Option C — Whole Facility
Option D — Calibrated Simulation
```

来源：

- https://www.energy.gov/cmei/femp/measurement-and-verification-options-federal-energy-and-water-saving-projects
- https://evo-world.org/en/library/download-protocol-documents-mainmenu-en

**本页采用：** M&V Option 是 Plan 正式属性；不同 Option 保留不同 measurement / estimation / model 语义；前端不猜 Option。

## 3.3 FEMP — Savings 不能直接测量

Energy savings 表示“未发生的能源使用”，不能直接测量。应在可比条件下，将经正式调整的 baseline 与 reporting-period actual 比较。

因此：

```text
Verified Savings
≠ Raw Baseline - Raw Actual
```

## 3.4 ASHRAE Guideline 14-2023

ASHRAE Guideline 14 面向可靠测量 energy、demand、water savings，并覆盖 measured data、data management、model 与 uncertainty。

**本页采用：** model fit 只是质量证据的一部分；measurement/model uncertainty、适用域和数据 provenance 不能隐藏。

## 3.5 ISO 50015:2014

ISO 50015 建立组织及其组成部分能源绩效 M&V 的一般原则与指南；2025 年复审确认继续有效。

来源：https://www.iso.org/standard/60043.html

**本页采用：** M&V Result 是受治理的能源绩效事实；方法、数据、假设、结果和 revision 必须可追溯。

---

# 4. 产品语言契约

主界面中文优先：

```text
节能量验证（M&V）
测量边界
基线期
报告期
基线模型
独立变量
例行调整
非例行调整
报告期实际用能
调整后基线
已验证节能量
不确定性
模型质量
数据覆盖率
运行功能验证
持续性监测
```

可以保留 `M&V`、`IPMVP`、`Option A/B/C/D`、`CV(RMSE)`、`NMBE`、`R²`、`EnPI`、`EnB`。

禁止无方法定义的 `Savings Confidence 92%`、`M&V Health 88`、`Model Score 95`。

---

# 5. M&V Domain Vocabulary

- **M&V Project：**针对一个或一组已实施措施建立的正式验证对象。
- **M&V Plan：**预先定义 scope、Option、boundary、baseline、data、adjustment、calculation、verification、quality 与角色。
- **Measurement Boundary：**节能计算覆盖的物理 / 系统 / meter 边界。
- **Baseline Period：**实施前用于建立参考能源表现的正式期间。
- **Reporting Period：**实施后用于量化节能结果的正式期间。
- **Baseline Model：**把 baseline energy 与 independent variables / parameters 关联的正式方法。
- **Adjusted Baseline：**把 baseline 调整到 reporting-period 可比条件后的参考能源使用。
- **Verified Savings：**在明确 boundary / period / adjustments / validity 下由权威 M&V owner 形成的正式结果。

---

# 6. Mandatory Semantic Separation

```text
Expected Savings ≠ Verified Savings
Operational Verification ≠ M&V
Functional Verification Passed ≠ Verified Savings
Actual Reduction ≠ Savings
Raw Baseline ≠ Adjusted Baseline
Comparison Period ≠ Baseline Period
EnB ≠ M&V Baseline automatically
Routine Adjustment ≠ Non-routine Adjustment
Independent Variable ≠ Static Factor
Model Fit ≠ Model Validity
Model Validity ≠ Savings Certainty
Data Coverage ≠ Data Quality
Option A ≠ Option B ≠ Option C ≠ Option D
Draft Result ≠ Verified Result
Persistence Monitoring ≠ Historical Result Rewrite
Snapback Detected ≠ Previous Period Savings Invalid automatically
```

---

# 7. Primary Questions

默认页面必须回答：哪些项目需要 M&V、当前方法与边界是什么、Baseline / Reporting Period 是什么、Adjusted Baseline 与 Actual 分别是多少、Routine / Non-routine Adjustments 有哪些、数据覆盖与质量如何、模型是否适用、Operational Verification 是否完成、Verified Savings 是多少、不确定性如何、是否存在 persistence loss / snapback。

---

# 8. Information Architecture

```text
Context Header
↓
M&V Portfolio Summary
↓
M&V Project Ledger
                      → M&V Inspector
↓
Selected M&V Workspace
  M&V Plan
  Measurement Boundary
  Baseline / Model
  Reporting Actual
  Adjustments
  Adjusted Baseline
  Verified Savings
  Quality / Uncertainty
  Operational Verification
  Result Revision
  Persistence / Snapback
↓
13 Functional Verification / 14 Energy / 22 Plan / 23 Target / 30 Review
```

默认是 **Ledger + durable detail**，不是 KPI 卡片墙。

---

# 9. Route / URL State Contract

```text
/sites/:siteId/mv
/sites/:siteId/mv/:mvProjectId
```

Search Params 可包括：`status`、`method`、`owner`、`reportingPeriod`、`sourcePlan`、`persistence`、`sort`、`selectedResult`。

Detail 可保留 `resultRevision`、`reportingPeriod`、`view`。

---

# 10. Entry / Source Contract

M&V Project 可以来自：

```text
22 Optimization Plan
23 Objective / Action Plan
Performance Contract / ECM
Manual M&V Program
Commissioning / Retro-commissioning project
```

进入时保留 source project identity、approved revision、expected benefit reference、implementation date、functional verification references；Expected Benefit 不自动成为 Verified Result。

---

# 11. M&V Project Identity / Revision Contract

至少记录 Project ID/Name、Scope、Owner、M&V Plan Revision、Source Project/Approved Revision、M&V Option/Method、Status、Created/Approved At。

Measurement Boundary、Baseline Period、Model、Variables、Adjustment Method、Reporting Method 的重大修改必须形成 revision / formal change；不能静默修改旧 Plan 后沿用原批准。

---

# 12. M&V Plan Contract

M&V Plan 至少定义：

```text
Project / ECM
Objective
M&V Option
Measurement Boundary
Baseline Period
Reporting Period strategy
Data Sources / Metering
Independent Variables
Fixed Parameters
Routine Adjustment method
Non-routine Adjustment process
Calculation Method
Operational Verification method
Quality / Uncertainty criteria
Missing-data policy
Reporting cadence
Reviewer / Approver
```

禁止项目结束后再挑一个最有利 baseline。

---

# 13. M&V Option Contract

## Option A

关键参数测量，其余参数按批准方法估算 / stipulated。必须明确 measured parameters、estimated/stipulated parameters、frequency、assumptions、risk allocation。

## Option B

相关参数由测量支持。必须保留 meter/sensor lineage、sampling、coverage、quality。

## Option C

Whole Facility。通常使用 whole-facility meter、baseline model、independent variables、reporting data、adjustments 和 model diagnostics。

## Option D

Calibrated Simulation。必须保留 simulation model/version、calibration evidence、inputs、assumptions、boundary、scenario 和 quality；simulation 不能冒充 measured actual。

---

# 14. Measurement Boundary Contract

至少记录 Included systems/assets、Included meters、Excluded loads、Interactive effects treatment、Energy carriers、Boundary references、Effective period、Owner/Revision。

例如中央冷站边界可以包括 chillers、CHW/CW pumps、cooling towers，同时明确 excluded loads。

---

# 15. Measurement Boundary Change Contract

Meter replacement、asset add/remove、system split/merge、boundary load transfer 等必须触发 `Boundary change requires review`。不能把新旧边界数据直接拼接后继续称同一 M&V result。

---

# 16. Baseline Period Contract

Baseline Period 必须有 start/end、coverage、representative operating conditions、independent-variable range、known exclusions、static factors、data quality 和 approved revision。

禁止 universal fallback：`实施前 30 天 = baseline`。

---

# 17. Reporting Period Contract

Reporting Period 必须明确 start/end、completion status、coverage、operating-condition coverage、independent-variable range、operational verification status。可由 Plan 定义 post-installation short-term / monthly / quarterly / annual / custom period；UI 不默认“本月”。

---

# 18. Baseline Model Contract

必须可追溯 Model ID/Version、Method、Dependent Variable、Independent Variables、Functional Form、Fit Period、Exclusions、Fixed Parameters、Quality Metrics、Applicability Domain、Owner/Approved At。

前端不重新拟合 authoritative model。

---

# 19. Independent Variables Contract

可能包括 weather、occupancy、production、operating hours、process throughput、load drivers 等；是否进入模型由 Plan owner 决定。

禁止：相关系数高就自动加入模型；有天气数据就自动 weather normalize。

---

# 20. Fixed / Stipulated Parameters Contract

必须明确 Parameter、Value、Source、Measurement/Estimate/Stipulation、Rationale、Uncertainty/Risk owner、Effective Period；约定参数不能显示成现场实时测量。

---

# 21. Routine Adjustment Contract

Routine Adjustment 处理 reporting period 中正常变化的 independent variables，例如 weather、occupancy、production、operating hours、load；必须来自批准模型 / 方法。

---

# 22. Non-routine Adjustment Contract

用于 baseline 与 reporting period 之间结构性变化，例如 floor area、equipment add/remove、schedule redesign、tenant/process change、major occupancy change、boundary change、capacity change。

每个 adjustment 至少有 change event、effective date、evidence、reason、method、impact、review/approval、revision。

禁止“发现 savings 不够 → 加 unexplained adjustment”。

---

# 23. Routine ≠ Non-routine Adjustment

```text
Routine = model expected normal variation
Non-routine = structural / exceptional change requiring explicit treatment
```

两者在 UI、Audit、Result lineage 中分别展示。

---

# 24. Savings Calculation Contract

常见 avoided-energy-use 方法可以表达：

```text
Adjusted Baseline
− Reporting Period Actual
= Verified Savings
```

但这只是呈现结构；实际 sign convention、carrier、generation/export/water 等由 M&V owner 定义。前端不从 raw series 自算 authoritative savings。

---

# 25. Raw Baseline ≠ Adjusted Baseline

```text
Raw Baseline             1,240 MWh
Routine Adjustment         +72 MWh
Non-routine Adjustment     +13 MWh
Adjusted Baseline         1,325 MWh
Reporting Actual          1,120 MWh
Verified Savings            205 MWh
```

用户必须能看到 adjustment lineage。

---

# 26. Actual Reduction ≠ Savings

Raw baseline 1,240 MWh、Reporting Actual 1,120 MWh 的 raw difference 是 120 MWh；如果 adjusted baseline 是 1,325 MWh，formal savings 可以是 205 MWh。因此 actual reduction 不能被 UI 直接命名为 savings。

---

# 27. Operational Verification Contract

Operational Verification 回答措施 / 设备 / sequence 是否已安装并持续按预期运行，可引用 13 Functional Verification、Work、Control/Strategy、commissioning、inspection。

显示 `Required / Status / Evidence / Last Verified At / Owner`。

---

# 28. Operational Verification ≠ M&V

```text
Functional Verification: PASS
M&V Reporting Period: 40% complete
Verified Savings: Pending
```

完全合法。二者互相链接，但不合并。

---

# 29. Data Source / Meter Lineage Contract

M&V input 必须可追溯 meter/sensor/source、unit、aggregation、timezone、quality、correction history、effective mapping、calibration/replacement context。Meter replacement 不能被前端静默拼成连续历史。

---

# 30. Data Coverage / Quality Contract

Coverage 至少区分 Expected Intervals、Observed Good、Estimated/Corrected、Missing、Suspect/Bad、Late/Revised。

```text
Coverage 100%
≠ Data Quality Good automatically
```

所有 interval 都有值，也可能全部来自估算或错误传感器。

---

# 31. Missing Data / Exclusion Contract

Missing-data treatment 必须来自 M&V Plan / Data owner，可能是 Exclude、Approved Estimate、Redundant Meter Reconstruction、Remeasurement 或 Inconclusive。

前端禁止 `missing → 0`、`last known`、静默 interpolation。

Baseline / Reporting exclusions 必须有 reason、time range、evidence、owner/approval；不能为了改善拟合静默删除“不好看的点”。

---

# 32. Model Quality Contract

Model Quality 可以包含 owner-defined `R²`、`CV(RMSE)`、`NMBE`、residual diagnostics、out-of-sample checks、calibration criteria。

```text
Model Fit Metric
≠ Savings Validity by itself
```

拟合好仍可能边界错误、超出适用域、发生 static-factor change 或存在数据问题。

---

# 33. Model Validity Contract

建议状态：

```text
Valid
Needs Review
Outside Applicability Domain
Static Factor Change
Data Quality Insufficient
Model Superseded
Inconclusive
Unknown
```

前端不能 `R² > 0.8 → Valid`，也不能把固定阈值硬编码成所有项目的 verified criterion。

---

# 34. Uncertainty Contract

如果 method 支持 uncertainty，至少显示 Savings Estimate、Uncertainty/Confidence Interval、Method、Confidence Level、Sources、Revision。

例如：

```text
已验证节能量
205 MWh

不确定性
±24 MWh @ 90% confidence
```

如果未量化则显示 `未量化`，不能显示 0% uncertainty 或黑盒 confidence score。

---

# 35. Interactive Effects / Multiple Carriers Contract

Interactive Effects 的处理必须由 Plan 明确为 Measured / Modeled / Estimated / Excluded with rationale / Not Applicable，不能默认 0。

如果项目涉及 electricity、gas、steam、chilled water、fuel、water，应先分别显示原始 savings；统一换算必须来自正式 conversion / analytics owner。

---

# 36. Cost / Carbon Boundary

```text
Verified Energy Savings ≠ Cost Savings
Verified Energy Savings ≠ Verified Carbon Reduction automatically
```

成本由 18 Billing/Tariff owner 使用正式费率方法；碳由 19 Carbon owner 使用 Scope / Factor / Market method。前端不做 `kWh × average tariff` 或 `kWh × global factor` 的 authoritative 计算。

---

# 37. Result State Contract

建议：

```text
Draft
Calculating
Pending Data
Pending Review
Verified
Verified with Qualification
Inconclusive
Rejected / Needs Revision
Superseded
```

M&V 不是简单 PASS/FAIL。

`Verified with Qualification` 必须有 reason、impact、applicability、reviewer；`Inconclusive` 是合法结果，不能转换成 0 savings 或 expected savings。

---

# 38. Result Revision / Historical Integrity Contract

每个正式 Result 记录 Reporting Period、M&V Plan Revision、Baseline Model Revision、Adjustments Revision、Calculation Revision、Reviewer/Approval。

如果数据修正：

```text
v1 Verified 198 MWh
v2 Verified 205 MWh after corrected meter data
```

旧 v1 保留。必须能回答某次管理评审当时看到的结果是多少、基于哪个模型 / 数据 revision。

---

# 39. Persistence Monitoring / Snapback Contract

如果项目需要持续监测，显示 Persistence Required、Latest Performance Period、Current Savings Trend、Snapback/Degradation、Next Review。

```text
2026 Q1 Verified Savings 205 MWh
2026 Q3 Snapback detected
```

不能把 Q1 历史结果改成 0。Snapback 必须有 reference、period、evidence、threshold/method、impact、next action，不能因“本周用能升高”就前端猜 snapback。

---

# 40. Cross-surface Handoff Contract

如果 persistence / M&V 发现异常：

```text
M&V deviation
→ 13 Functional Verification
→ 10 Diagnosis
→ 11 Work
```

23 只消费 Verified Savings、Reporting Period、Result Status、Persistence Status、Result Revision，不重算 savings。

22 → 24 保留 source plan revision；13 与 24 互链但不合并。

---

# 41. Aggregate Savings Contract

Portfolio 可以显示 owner-approved aggregate verified savings，但必须避免同一 Opportunity → Plan → Project → M&V result 重复计数。

Aggregate owner 应提供 result identity、project lineage、period、overlap/exclusive relation、aggregation eligibility、unit。前端不靠名称去重。

---

# 42. M&V Ledger / Inspector / Detail Contract

Ledger 默认列：

```text
项目
方法 / Option
测量边界
报告期
已验证节能量
结果状态
数据 / 模型状态
持续性
负责人
```

Inspector 只显示 Project、Method、Boundary、Periods、Verified Savings、State、Operational Verification、Quality/Uncertainty、Persistence、Next Action。

Detail 负责 Plan、Boundary、Baseline、Model、Variables、Actual、Adjustments、Adjusted Baseline、Savings、Quality/Uncertainty、Operational Verification、Exclusions、Revisions、Persistence、Audit。

---

# 43. Primary Analytical Visualization

核心图可以是：

```text
Adjusted Baseline
vs
Reporting Actual
```

并显示 reporting period、adjustment markers、excluded periods、model applicability。

完整趋势探索进入 14/05。图表不承担 authoritative savings calculation。

---

# 44. Data Authority Contract

| Fact | Owner |
|---|---|
| M&V Project / Plan | M&V domain |
| Measurement Boundary | M&V / Metering governance |
| Baseline / Model | M&V model owner |
| Independent Variables | M&V plan + source domains |
| Meter / Interval Data | Historian / Metering owner |
| Routine Adjustment | M&V model owner |
| Non-routine Adjustment | M&V governance owner |
| Operational Verification | 13 / Commissioning owner |
| Verified Savings | M&V result owner |
| Cost Savings | Billing / Economics owner |
| Carbon Reduction | Carbon owner |
| Target Contribution | 23 consumes M&V result |

Frontend 只组合和呈现，不重新定义。

---

# 45. Query / Read Model Contract

Ledger 使用服务端 M&V summary read model，不允许：

```text
100 projects
→ 100 model queries
→ 100 baseline queries
→ 100 meter queries
→ 100 verification queries
```

Detail 再加载完整证据。

---

# 46. Realtime / Freshness Contract

M&V 不是 realtime HMI。主要使用 Query、periodic refresh、explicit recalculation/revision event。

Meter stream 可以支持 completeness monitoring，但不能每来一个 sample 就自动改 Verified Savings。

---

# 47. Permission / Audit Contract

典型权限：Read M&V、Create/Edit Draft Plan、Approve Plan Revision、Submit/Review Adjustment、Calculate、Review/Verify/Supersede Result、Read Audit。

Plan revision、boundary、baseline、model、variables、adjustments、exclusions、missing-data treatment、result revision、review decision、persistence finding 都必须审计 who/when/what/before-after/reason/evidence/approval。

---

# 48. AI Assistance Boundary

AI 可以总结 Plan、解释 adjustment lineage、整理 missing evidence、草拟 review note、解释 model-quality metrics、生成 audit-friendly summary 草稿。

AI 不能选择最有利 baseline、自动删 outlier、自动批准 adjustment、把 AI estimate 变成 Verified Savings、把 AI confidence 当 uncertainty、自动 Verify Result 或改写历史。

---

# 49. No Defensive Programming / No Compatibility Design

明确禁止：

```text
M&V API error → 0 savings
baseline unavailable → previous period
baseline model unavailable → raw baseline
adjustment unavailable → assume 0
reporting data missing → 0
missing interval → last value
bad meter quality → last good silently
operational verification unavailable → passed
functional verification passed → savings verified
expected savings → verified savings
actual reduction → verified savings
model R² high → model valid
model fit good → savings verified
coverage 100% → quality good
uncertainty unavailable → 0%
Option A estimate → measured
Option D simulation → measured actual
cost savings → kWh × average tariff
carbon savings → kWh × global factor
new result revision → overwrite old result
snapback detected → rewrite historical savings
same project chain → double-count aggregate savings
multiple M&V APIs → first success wins
frontend regression → authoritative baseline model
old baseline-minus-actual Dashboard fallback
legacy M&V compatibility adapter
```

正式原则：

> **一个 M&V Result 对应一个权威 Plan、Boundary、Period、Model、Adjustment 和 Result Revision。Expected 不是 Verified，Operational Verification 不是 M&V，Actual Reduction 不是 Savings，Model Fit 不是 Model Validity，Persistence 不改写历史，Unknown 保持 Unknown。**

---

# 50. Accessibility / Responsive Contract

- Ledger 使用 semantic table；
- 状态不只靠颜色；
- savings / uncertainty 有文本表达；
- adjustment decomposition 不只靠图形颜色；
- 图表有 data/text alternative；
- Inspector / Detail 支持键盘；
- Inconclusive / Qualified 有文字原因；
- 768px 下仍能查看 project、method/boundary、period、verified savings、quality/state、adjustment summary。

---

# 51. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 节能量验证（M&V）· 中央冷站                              2026 年绩效期     │
│ 来源：优化方案 PLAN-204 v5 · M&V Plan v3                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ 进行中 5   待数据 1   待复核 2   已验证 8   结论不充分 1   持续性异常 1 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 项目                方法     边界       报告期        已验证节能    状态   │
│ 冷站低负荷优化       Option C 冷站总表   2026 Q3       205 MWh       已验证 │
│ AHU 时段优化         Option B AHU-01..08 2026 Q3       41 MWh        待复核 │
│ 照明改造             Option A 3F 照明    2026 H2       —             待数据 │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中：冷站低负荷优化                          │ 快速判断                     │
│ 测量边界：中央冷站总表                        │ Method Option C              │
│ 基线期：2025-01 → 2025-12                     │ 功能验证：通过               │
│ 报告期：2026-07 → 2026-09                     │ 模型有效性：有效             │
│                                               │ 数据覆盖率：99.4%            │
│ 原始基线             1,240 MWh                │ [打开 M&V 详情]              │
│ 例行调整               +72 MWh                │ [查看功能验证]               │
│ 非例行调整             +13 MWh                │ [查看能源分析]               │
│ 调整后基线           1,325 MWh                │ [查看目标与行动计划]         │
│ 报告期实际           1,120 MWh                │                              │
│ ───────────────────────────────               │                              │
│ 已验证节能量           205 MWh                │                              │
│ 不确定性              ±24 MWh @ 90%           │                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 持续性监测：当前正常 · 下次复审 2026-12-31                                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任和信息层级，不是像素规范。

---

# 52. Browser Acceptance Criteria

## M&V Truth

- Expected 与 Verified 分开；
- Option/Method、Boundary、Baseline/Reporting Period 可见；
- Raw / Adjusted Baseline 分开；
- Actual Reduction 不标成 Verified Savings；
- Result 有 revision / owner / state。

## Adjustments

- Routine / Non-routine 分开；
- non-routine adjustment 有 evidence / reason / approval；
- adjustment 不显示成项目节能贡献；
- unavailable 不默认为 0。

## Data / Model

- coverage 与 quality 分开；
- missing 不显示 0；
- model fit 与 model validity 分开；
- 超出适用域时明确提示；
- uncertainty unavailable 不显示 0%。

## Verification

- Operational Verification 与 M&V 分开；
- Functional Verification PASS 不自动产生 Verified Savings；
- Pending Data / Inconclusive 是合法状态。

## Persistence

- snapback 不改写历史 result；
- persistence issue 有 period / evidence / method；
- 历史 revision 可追溯。

## Accessibility / Responsive

- semantic table；
- 状态不只靠颜色；
- exact savings 与 uncertainty 可读；
- 768px 下仍能完成核心 M&V review；
- 无 page-level 横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无前端 `baseline - actual` 权威算法；
- 无前端 regression 作为 authoritative model；
- 无 N+1 project/model/meter queries；
- 无旧 M&V Dashboard compatibility adapter；
- review scenario 无 runtime/network error。

---

# 53. Explicit Non-goals

24 不是：通用能源分析页、EnPI/EnB editor、预计节能计算器、Functional Verification 页面、Tariff cost calculator、Carbon inventory calculator、AI savings estimator、regression playground、financial settlement system 或简单 before-vs-after Dashboard。

---

# 54. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- M&V Plan / Result / Revision 分离；
- Measurement Boundary 明确；
- M&V Option / Method 明确；
- Baseline / Reporting Period 明确；
- Raw / Adjusted Baseline 分离；
- Routine / Non-routine Adjustment 分离；
- Operational Verification / M&V 分离；
- Expected / Verified Savings 分离；
- Model Fit / Model Validity 分离；
- Data Coverage / Data Quality 分离；
- Uncertainty 语义明确；
- Result revision / historical integrity 明确；
- Persistence / Snapback 不改写历史；
- No Defensive Programming 规则明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
