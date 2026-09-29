# 17 能源评审 / 重大用能 / 能源绩效指标 / 能源基线 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `17 能源评审`  
> **Route intent：** `/sites/:siteId/energy-review`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；行业缩写 `SEU / EnPI / EnB` 作为专业辅助，不作为主界面唯一语言。  
> **设计输入声明：** 本文件不参考当前项目已有 Energy Review、基线页、旧能效 KPI 页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Energy Review / SEU / EnPI / EnB / Relevant Variable / Approval / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

能源评审的核心任务是：

> **把能源数据、重大用能、影响变量、能源绩效指标和能源基线组织成一套可审计、可维护、可持续改进的正式能源绩效治理体系。**

本 Surface 是 **formal energy-performance governance workspace**，不是：

- 14「能源分析」换一个标题；
- 一张 ISO 50001 仪表盘；
- 普通同比环比页面；
- M&V savings calculator；
- 目标计划编辑器；
- 节能机会列表；
- “把过去一年设成 baseline” 的快捷按钮；
- 把所有高耗能设备自动标成 SEU 的算法页。

用户离开本页前应该知道：

1. 当前能源评审的 scope / boundary / review period 是什么；
2. 当前有哪些重大用能（SEU），为何被判定为重大；
3. 每个 SEU 的主要相关变量是什么；
4. 用什么能源绩效指标（EnPI）评价绩效；
5. 用什么能源基线（EnB）作为正式参考；
6. 基线是否仍有效，是否需要重新评估；
7. 当前能源绩效相对基线如何变化；
8. 哪些偏离需要进入能源分析、效率分析或诊断；
9. 哪些改进机会需要转为 Opportunity / Objective / Action Plan；
10. 这套方法是谁定义、何时批准、当前 revision 是什么。

---

# 2. 主要用户

## Primary

### 能源经理

维护正式能源评审、SEU、EnPI、EnB，并组织周期性复审。

### 能源工程师

定义指标方法、相关变量、归一化、基线模型，检查数据质量和模型适用性。

### 管理体系负责人

确保能源评审、指标、基线、目标和管理评审之间形成可追溯闭环。

## Secondary

- 站点负责人：阅读当前重大用能与绩效状态；
- HVAC 工程师：理解冷站/AHU 等系统为什么属于 SEU，以及如何评价；
- 数据工程师：处理 meter / variable / baseline data quality；
- M&V 工程师：消费正式 baseline context，但不把本页直接当项目 M&V；
- 管理层：在 Management Review 中消费当前评审结论。

---

# 3. 外部最佳实践依据

## 3.1 ISO 50001:2018 — 能源绩效必须进入持续改进体系

ISO 50001:2018 建立能源管理体系（EnMS）框架，目标是系统性提升 energy performance，包括 energy efficiency、energy use 和 energy consumption，并采用持续改进方法。

来源：

- https://www.iso.org/standard/69426.html
- https://www.iso.org/standard/88430.html

**本页采用：**

- Energy Review 是正式治理工作，不是一次性分析；
- 能源绩效需要明确指标、基线和持续监测；
- 方法、责任、版本和审核历史必须可追溯；
- Energy Review 需要与目标、行动计划、运行控制和管理评审衔接。

## 3.2 ISO 50006:2023 — EnPI / EnB 需要建立、使用和维护

ISO 50006:2023 专门指导如何建立、使用和维护能源绩效指标（EnPI）与能源基线（EnB），并用于能源绩效评价和改进证明。

来源：

- https://www.iso.org/standard/79367.html

**本页采用：**

- EnPI / EnB 是正式对象，不是临时图表参数；
- 指标和基线必须有 method / owner / version / effective period；
- 基线需要维护，不能永久不变；
- normalization 不能覆盖 raw actual；
- 指标比较必须有一致、明确的 evaluation context。

## 3.3 DOE 50001 Ready — Energy Review 是明确的任务链

DOE 50001 Ready 把能源评审相关工作拆为：

```text
Energy Data Collection / Analysis
→ Significant Energy Uses
→ Relevant Variables
→ Energy Performance Indicators
→ Baselines / Objectives / Targets
```

来源：

- https://www.energy.gov/cmei/ito/50001-ready-program
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/50001%20Ready%20Reference%20Design%20Guide%20v0.1.pdf
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/50001_Ready_end_user_2_day_training_overview_0.pdf

**本页采用：**

- SEU、Relevant Variable、EnPI、EnB 是相互关联但职责不同的正式对象；
- SEU 需要定期 review/update；
- EnPI 需要 methodology；
- Baseline 需要正式建立并与目标、改进关联；
- 不能把“能耗排行榜”直接等同能源评审。

## 3.4 DOE 50001 Ready — Normalization 要考虑天气、产量、占用等变量

DOE 对 50001 Ready 的更新明确强调，为了更准确跟踪能源绩效改进，需要考虑 weather、production、occupancy 等影响变量。

来源：

- https://www.energy.gov/cmei/ito/articles/50001-ready-navigator-tool-updated-latest-changes-iso-50001-standard

**本页采用：**

- Relevant Variable 是正式绩效方法的一部分；
- weather/occupancy/production 不只是图表 overlay；
- Normalization 必须由正式 model/method owner 提供；
- Raw Actual 与 Normalized Result 并存。

## 3.5 DOE SEP 50001 M&V Protocol — Relevant Variables 与 Static Factors 要分开

DOE/LBNL 的 SEP 50001 M&V 资料明确区分 relevant variables 的常规变化与 static factors 的变化，并用 adjustment/normalization 方法提高 baseline/reporting period 的可比性。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/SEP_50001_MV_Protocol_2019.pdf
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/SEP_50001_MV_Protocol_2019_Guidance_0.pdf

**本页采用：**

- Relevant Variable 与 Static Factor 分开；
- static factor 发生实质变化时，需要触发 baseline/model review，而不是静默继续；
- normalization method 必须 versioned；
- model applicability 不满足时，不能继续显示“基线正常”。

## 3.6 ENERGY STAR Portfolio Manager — Normalized 指标是可比性工具，不是原始事实

ENERGY STAR Portfolio Manager 区分 Site Energy、Source Energy、Weather Normalized Energy 等不同 metrics，说明归一化结果应与实际能耗事实区分。

来源：

- https://portfoliomanager.energystar.gov/pm/glossary
- https://www.energystar.gov/buildings/benchmark

**本页采用：**

```text
Actual
≠ Normalized
```

归一化帮助可比，但不能覆盖真实 consumption。

---

# 4. 产品语言契约

面向中文用户，主界面使用中文业务语言。

推荐：

```text
重大用能（SEU）
能源绩效指标（EnPI）
能源基线（EnB）
相关变量
静态因素
归一化方法
基线有效期
基线健康状态
能源绩效改进
```

禁止主界面只有：

```text
SEU
EnPI
EnB
Relevant Variables
Static Factors
Baseline Health
Normalization
```

标准缩写可在标题、表头或专业详情中保留，但中文名称优先。

---

# 5. Domain Vocabulary

## 5.1 能源评审（Energy Review）

围绕组织/站点 scope 内能源使用、消费和绩效进行正式分析、判断和更新的治理过程。

它不是单次报表。

## 5.2 重大用能（SEU）

由组织依据正式 criteria 判定为具有重大能源影响、显著 consumption 和/或重要 improvement potential 的 energy use。

SEU 不是固定等于 Top N energy consumers。

## 5.3 相关变量（Relevant Variable）

对能源绩效有可测量、常规影响的变量，例如：

```text
天气
产量
占用人数
营业时间
负荷
处理量
入住率
```

## 5.4 静态因素（Static Factor）

正常情况下不频繁变化，但一旦改变可能显著影响能源绩效或模型适用性的条件，例如：

```text
建筑面积
设备容量
生产线配置
重大系统改造
空间用途
工艺结构
```

## 5.5 能源绩效指标（EnPI）

用于量化、跟踪和评价能源绩效的正式指标。

可以是：

```text
kWh/m²
kWh/occupied-hour
kWh/unit production
Plant kW/RT
Normalized annual consumption
Owner-defined regression-based indicator
```

不是所有能源 KPI 都自动是 EnPI。

## 5.6 能源基线（EnB）

正式建立的 reference，用于评价后续 energy performance。

它必须有：

```text
baseline period / model
scope
method
relevant variables
static-factor assumptions
version
effective period
owner
approval
```

## 5.7 归一化（Normalization）

在正式方法下控制/调整 relevant-variable 差异，以提高不同 period 的可比性。

## 5.8 基线健康状态（Baseline Health）

表示当前 EnB/model 是否仍处于有效、适用、数据充分的状态。

不是“基线好不好”的黑盒分数。

---

# 6. Mandatory Semantic Separation

以下全部禁止混同：

```text
Energy Review = Energy Dashboard
SEU = Top 5 energy consumers
SEU = Asset type
EnPI = Any KPI
EnPI = Target
EnPI = Savings
EnB = Previous period
EnB = Budget
EnB = Forecast
EnB = Target
Comparison = EnB
Normalized = Actual
Relevant Variable = Root Cause
Correlation = Relevant Variable automatically
Static Factor = Relevant Variable
Baseline change = overwrite old baseline
Baseline invalid = zero performance
Target achieved = Savings verified
Energy improvement = M&V verified savings
```

正确关系：

```text
Energy Scope / Data
↓
Energy Review
↓
SEU
↓
Relevant Variables / Static Factors
↓
EnPI
↓
EnB / Normalization
↓
Performance Evaluation
↓
Opportunity / Objective / Action Plan
↓
Monitoring / Management Review
```

---

# 7. Primary Questions

## Q1 — 当前评审覆盖什么？

显示：

- Site / organization scope；
- energy sources；
- review period；
- current revision；
- owner；
- approval state；
- next review date。

## Q2 — 哪些是重大用能？

显示：

- SEU name；
- significance rationale；
- energy share/context；
- improvement potential/context；
- owner；
- current performance；
- last reviewed。

## Q3 — 什么因素影响它？

显示：

- relevant variables；
- static factors；
- variable source；
- data coverage；
- relationship/model method。

## Q4 — 用什么指标衡量？

显示：

- EnPI；
- formula/method；
- unit；
- scope；
- current value；
- normalized value if applicable；
- validity。

## Q5 — 基线是否还有效？

显示：

- EnB version；
- baseline period；
- model validity；
- data health；
- relevant-variable range；
- static-factor changes；
- review due state。

## Q6 — 当前绩效是否改善？

显示：

- actual / normalized performance；
- baseline/reference；
- variance；
- improvement direction；
- data/model validity。

不直接叫 verified savings。

## Q7 — 下一步需要做什么？

进入：

- 14 能源分析；
- 16 效率分析；
- 21 节能机会；
- 23 目标与行动计划；
- 24 M&V；
- 30 管理评审；
- 31 数据质量。

---

# 8. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/energy-review
```

推荐 Search Params：

```text
reviewVersion
section          // summary | seu | enpi | baseline | variables | history
selectedSeu
selectedEnpi
selectedBaseline
period
```

Durable object identity 可以进入子路由或 search state，具体由 Router implementation 决定，但不能保留两套竞争路由。

不进入 URL：

- transient tooltip；
- local disclosure state；
- unsaved draft form content；
- hover；
- temporary table width。

---

# 9. Entry Contract

## 从 14 能源分析

携带：

- Site；
- period；
- selected energy scope；
- baselineVersion（若已有）；
- source trail。

14 的 ordinary comparison 不能自动升级成 EnB。

## 从 16 效率分析

携带 selected system / metric / period，用于查看该指标是否属于正式 EnPI。

## 从 Opportunity / Objective

携带对应 SEU / EnPI / EnB context。

## 从 Management Review

直接进入当前 approved Energy Review revision 或 overdue review item。

---

# 10. Exit Contract

主要出口：

```text
能源评审
→ 14 能源分析
→ 16 效率分析
→ 21 节能机会
→ 23 目标与行动计划
→ 24 M&V
→ 30 管理评审
→ 31 数据质量
```

保持：

- Site；
- selected SEU；
- EnPI；
- EnB version；
- review revision；
- relevant period。

---

# 11. Responsibility Boundary

本 Surface 拥有：

- Energy Review revision / lifecycle；
- SEU register；
- significance criteria reference；
- Relevant Variable register；
- Static Factor register；
- EnPI definition / owner / version；
- EnB definition / owner / version；
- normalization method reference；
- baseline health / applicability projection；
- periodic review workflow；
- audit/history；
- links to opportunities/objectives/actions。

本 Surface 不拥有：

- raw meter calculation；
- arbitrary energy analytics；
- M&V project savings；
- utility bill reconciliation；
- control strategy execution；
- carbon factor management；
- ad-hoc dashboard comparisons；
- frontend-generated regression models；
- automatic SEU classification without authoritative criteria。

---

# 12. Information Architecture

```text
评审上下文
  站点 · 当前评审版本 · 状态 · Owner · 最近批准 · 下次复审

当前能源评审摘要
  重大用能
  能源绩效指标
  能源基线健康状态
  主要绩效偏离
  待处理节能机会

重大用能（SEU）
  significance rationale
  current performance
  relevant variables
  linked EnPI / EnB

能源绩效指标（EnPI）
  definition
  current / normalized result
  validity
  owner / revision

能源基线（EnB）
  baseline period/model
  health/applicability
  static-factor changes
  effective period

相关变量 / 静态因素
  source
  validity
  range/change
  review impact

评审结论与后续动作
  opportunities
  objectives / targets
  action plans
  management-review handoff

历史与审计
  previous revisions
  approvals
  baseline revisions
  rationale
```

首页不是十几张 KPI 卡片。

---

# 13. Energy Review Lifecycle

建议生命周期：

```text
Draft
↓
Under Review
↓
Approved
↓
Active
↓
Superseded
```

如果组织流程更简单，可以合并 Approved/Active，但 owner contract 必须唯一。

每个 revision 保留：

```text
Review ID
Revision
Scope
Review Period
Prepared by
Reviewed by
Approved by
Approved at
Effective from
Superseded by
Change rationale
```

Frontend 不创建本地-only review status。

---

# 14. Review Cycle Contract

Energy Review 不是一次性对象。

必须支持：

- planned review date；
- last review；
- overdue state；
- material-change-triggered review；
- changed SEU；
- changed variable/model；
- changed baseline；
- changed scope/boundary。

Review trigger 来自 governance owner。

前端不能硬编码“每年一次”。

---

# 15. Scope / Boundary Contract

必须明确：

```text
Organization / Site Scope
Energy Sources
Included Systems / Processes
Excluded Scope
Meter / data boundary
Effective period
```

Scope 修改不能静默发生。

重大 scope change 应：

```text
Create new revision
→ reassess SEU
→ reassess variables
→ reassess EnPI
→ reassess EnB
```

---

# 16. Energy Source / Use Inventory

Energy Review 的输入可以包括：

```text
Electricity
Natural Gas
Steam
Hot Water
Chilled Water
Fuel
On-site generation
Other owner-defined carriers
```

Energy Use 可以按：

- system；
- process；
- building；
- equipment group；
- end-use；
- organizational unit。

但这些关系必须来自正式 data / semantic owner。

---

# 17. SEU Selection Contract

SEU selection 必须由组织的正式 significance criteria 决定。

可能考虑：

```text
Absolute consumption
Share of total
Energy cost
Improvement potential
Operational criticality
Persistence of poor performance
Strategic relevance
Other approved criteria
```

但具体 criteria/weighting 由 governance owner。

禁止前端：

```text
sort by kWh desc
→ top 5 = SEU
```

## SEU rationale

每个 SEU 需要：

```text
Why significant
Criteria applied
Evidence period
Owner
Review date
```

---

# 18. SEU Register

默认表格字段：

```text
重大用能
业务/系统范围
主要能源类型
重大性依据
当前绩效
关联 EnPI
基线状态
负责人
最近复审
下一动作
```

SEU Register 是正式 registry，不是 energy contributor ranking。

---

# 19. SEU Current Performance

每个 SEU 可以显示当前 performance summary：

- current energy use；
- selected EnPI；
- comparison to EnB；
- normalized performance；
- major deviation；
- data validity。

这些结果来自 14/16/analytics owner。

SEU 页面不重复构建第二套 analytics engine。

---

# 20. Relevant Variable Contract

每个 relevant variable 必须有：

```text
Name
Business meaning
Source
Unit
Frequency
Expected operating range
Relationship/model role
Data coverage
Owner
Revision
```

例如：

```text
室外温度
入住人数
营业时长
产量
冷量负荷
处理流量
```

Relevant Variable ≠ causal root cause。

一个变量进入 EnPI/EnB model 前，需要 owner-defined evidence/method。

---

# 21. Static Factor Contract

Static Factor 与 Relevant Variable 分开。

例子：

```text
建筑面积
冷站装机容量
生产线数量
空间用途
设备构成
重大工艺配置
```

显示：

```text
Current value/state
Baseline assumption
Last changed
Change significance
Baseline review impact
```

如果 static factor 发生重大变化：

> `能源基线需要复审`

不能静默继续沿用旧模型。

---

# 22. EnPI Definition Contract

每个能源绩效指标（EnPI）必须有：

```text
中文名称
缩写/technical name
Purpose
Scope
Formula / Method
Numerator
Denominator / variables
Unit
Aggregation period
Normalization method
Validity rules
Owner
Version
Effective period
```

例如：

```text
单位面积用电强度（EnPI）
单位：kWh/m²
```

或者：

```text
冷站单位冷量功率（EnPI）
单位：kW/RT
```

不是所有 KPI 都自动成为 EnPI。

---

# 23. EnPI Validity Contract

每个 EnPI 至少需要状态：

```text
Valid
Insufficient Data
Outside Applicable Range
Method Superseded
Source Unavailable
Needs Review
Unknown
```

不能只存一个 number。

如果 denominator/source 不可用：

> `该能源绩效指标当前不可计算`

不是 0。

---

# 24. EnPI Current Performance

默认显示：

```text
Current Actual
Normalized Result（若适用）
Baseline / Expected
Variance
Direction
Validity
```

例如：

```text
冷站单位冷量功率
当前：0.67 kW/RT
归一后：0.64 kW/RT
能源基线期望：0.60 kW/RT
状态：偏高 · 数据有效
```

不显示：

```text
Savings = 0.07 kW/RT
```

---

# 25. EnB Definition Contract

每个能源基线（EnB）必须有：

```text
Name
Scope
Baseline period
Energy type / EnPI
Method
Relevant variables
Static-factor assumptions
Data sources
Model revision
Validity domain
Effective period
Owner
Approval
```

Baseline 可以是：

- fixed reference period；
- normalized model；
- approved statistical model；
- owner-defined reference method。

前端不限制成一种类型。

---

# 26. Baseline ≠ Comparison

必须保持：

```text
普通比较
Previous period / Previous year
```

和：

```text
正式能源基线（EnB）
```

分开。

如果 EnB unavailable：

> `能源基线暂不可用`

不能 fallback 到 previous period。

---

# 27. Baseline Version / Effective Period

EnB 必须 versioned。

例如：

```text
EnB v3
Baseline period: 2024-01 → 2024-12
Effective: 2025-01-01
Superseded: 2026-04-01
```

新的 baseline revision 不覆盖旧 baseline。

历史 reporting 必须知道当时使用哪个 version。

---

# 28. Baseline Health Contract

Baseline Health 不是黑盒分数。

使用可解释状态：

```text
Valid
Review Due
Data Degraded
Variable Range Drift
Static Factor Changed
Model Not Applicable
Superseded
Unknown
```

并说明原因，例如：

```text
静态因素变化：建筑面积 +18%
→ 需要复审 EnB v3
```

或者：

```text
室外温度连续超出模型训练范围
→ 当前 period 不适用于该基线模型
```

---

# 29. Baseline Adjustment / Re-establishment Boundary

如果组织决定调整或重建 baseline：

必须：

- 新 revision；
- rationale；
- affected EnPI；
- affected period；
- approval；
- link to superseded baseline；
- audit trail。

禁止：

```text
edit baseline date/model
→ silently rewrite historical reports
```

---

# 30. Normalization Contract

Normalization 必须有：

```text
Method
Relevant variables
Model/revision
Applicable range
Reference condition
Data quality rules
Owner
```

可采用：

- weather normalization；
- occupancy adjustment；
- production adjustment；
- regression/model-based normalization；
- approved standard-condition method。

具体方法由 owner。

UI 必须同时保留：

```text
实际值
归一化结果
```

不能只显示 normalized value。

---

# 31. Model Applicability Contract

模型需要明确：

```text
Training/baseline range
Required variables
Data completeness
Valid operating domain
Model revision
```

如果 reporting condition 超出有效域：

> `当前条件超出能源基线模型适用范围`

不能继续算一个看起来精确的 normalized result。

---

# 32. Review Summary

默认运营视图最多突出：

```text
评审状态
重大用能数量
需要复审的基线
主要绩效偏离
待处理节能机会
下次评审日期
```

不是 8–12 个彩色 KPI 卡。

---

# 33. Top Deviation Contract

Top deviation 来自正式 EnPI / EnB evaluation。

每项至少显示：

- SEU；
- EnPI；
- current；
- baseline/expected；
- normalized/raw context；
- validity；
- period；
- next investigation。

Deviation ≠ Finding ≠ Root Cause。

---

# 34. Opportunity Handoff

Energy Review 可以把正式 review finding / gap 交给 Surface 21「节能机会」。

handoff 至少带：

```text
SEU
EnPI
EnB version
Performance gap
Relevant variables
Evidence links
Review revision
```

Opportunity owner 再负责 proposed measure / savings / cost / feasibility。

---

# 35. Objectives / Targets Boundary

本页可以显示：

- linked objective；
- target value；
- target period；
- progress；
- owner。

但正式 Objectives / Action Plans 属于 Surface 23。

必须保持：

```text
EnPI = measurement method
Target = desired future state
```

两者不能混。

---

# 36. M&V Boundary

本页的 EnB 可以成为项目 M&V 的重要上下文，但：

```text
Energy Review baseline
≠ Project M&V plan automatically
```

M&V 还需要：

- measurement boundary；
- project scope；
- reporting period；
- adjustments；
- uncertainty/model quality；
- operational verification；
- savings result。

所以：

```text
EnPI improved 8%
≠ Verified savings 8%
```

---

# 37. Data Quality Contract

每个 SEU / EnPI / EnB 必须能追溯数据质量。

至少区分：

```text
Good
Estimated
Missing
Late
Corrected
Suspect
Insufficient Coverage
Unknown
```

Baseline model 不能把 input quality 隐藏掉。

如果 baseline period 数据质量不足：

> `该基线版本的数据完整性不足，需要复审`

不能继续标 `Valid`。

---

# 38. Data Authority Contract

## Energy consumption / meter facts

Owner：Energy / Meter / Historian domain。

## SEU register

Owner：Energy Management domain。

## Relevant Variables / Static Factors

Owner：Energy Management + authoritative source domains。

## EnPI Definition

Owner：Energy Performance domain。

## EnB Definition / Model

Owner：Energy Performance / Baseline domain。

## Normalization Result

Owner：Baseline / Analytics domain。

## Opportunity

Owner：Opportunity domain。

## Objectives / Targets

Owner：Objectives & Action Plans domain。

Frontend 只做 Energy Review-centered projection 与 authorized governance mutation。

---

# 39. Query / Read Model Contract

推荐：

```text
Energy Review Projection
  + current review revision/status
  + scope/boundary
  + SEU summary
  + EnPI summary
  + EnB health
  + top deviations
  + relevant-variable health
  + open opportunities
  + linked objectives
  + next review
```

专业详情可单独 query：

```text
SEU Detail
EnPI Definition
EnB Definition / Model Metadata
Review History
```

禁止：

```text
20 SEUs
→ 20 energy queries
→ 20 variable queries
→ 20 baseline queries
→ 20 opportunity queries
```

缺 read model 时修 domain contract，不在前端 fan-out。

---

# 40. Mutation / Governance Contract

可能 mutation：

- create review revision；
- edit draft SEU；
- publish/update EnPI definition；
- propose baseline revision；
- review/approve baseline；
- approve Energy Review；
- supersede review；
- request baseline reassessment；
- create Opportunity / Objective handoff。

所有 mutation：

1. 显式用户动作；
2. 权限校验；
3. authoritative revision/state validation；
4. server-confirmed result；
5. audit trail。

不做 local-only Approved / Valid 状态。

---

# 41. Concurrency / Revision Contract

Energy Review、EnPI、EnB 都是治理对象。

保存前需要 authoritative revision/version。

发生冲突：

```text
refetch authoritative state
→ show changed fields/context
→ user reconfirm
```

不做 silent last-write-wins。

也不在前端自动 merge baseline model。

---

# 42. Loading / Empty / Partial / Error

## No Energy Review

成功返回 empty：

> `当前站点尚未建立正式能源评审。`

可以提供有权限的 `创建能源评审`。

## Review service unavailable

> `能源评审数据暂不可用。`

不能显示 `0 个 SEU / 0 个基线问题`。

## EnPI unavailable

SEU 仍可显示；指标 section 标 unavailable。

不能从普通 KPI 临时选一个冒充 EnPI。

## EnB unavailable

显示：

> `该指标尚未建立可用能源基线。`

不能 fallback previous period。

## Variable unavailable

显示 source unavailable / coverage issue。

不能默认填 average/0。

---

# 43. Permission / Capability Gating

示例：

- `energy-review.read` → 阅读当前评审；
- `energy-review.edit` → 编辑 draft；
- `energy-review.approve` → approve revision；
- `seu.manage` → SEU registry；
- `enpi.manage` → EnPI definition；
- `baseline.manage` → EnB definition/revision；
- `baseline.approve` → approve baseline；
- `opportunity.create` → Opportunity handoff；
- `objective.create` → Objective handoff。

无权限动作不显示。

---

# 44. Visual / UX Contract

默认视觉层级：

```text
当前能源评审状态
↓
重大用能（SEU）
↓
能源绩效指标（EnPI）
↓
能源基线（EnB）健康状态
↓
主要绩效偏离
↓
节能机会 / 目标 / 下一评审
↓
专业方法与审计
```

禁止：

- “ISO 合规 96 分”；
- “Baseline Health 92/100”；
- 全页面英文 acronym；
- 把所有 SEU 做成大卡片墙；
- 用红绿灯代替 validity explanation；
- 把模型参数直接放首屏；
- 默认展示回归系数海洋。

---

# 45. Component Mapping

```text
评审上下文                 → Page header + compact facts
评审状态                   → Badge + text
SEU Register               → shadcn Table + TanStack Table
EnPI Register              → Table + details inspector/route
EnB Register               → Table + health/status facts
相关变量                   → Table / structured list
静态因素                   → structured facts + change status
基线健康                   → explainable status list
绩效趋势                   → dedicated ECharts where needed
方法/版本/审计             → Collapsible / definition list / timeline
批准/发布                   → Dialog / AlertDialog
```

避免 generic CRUD form generator。

---

# 46. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 能源评审 · 中央冷站                                      当前版本 v5 · 已批准 │
│ 评审期：2026-01-01 → 2026-08-31   负责人：能源经理   下次复审：2027-01-15   │
├──────────────────────────────────────────────────────────────────────────────┤
│ 重大用能 3 项   能源绩效指标 5 项   需复审基线 1 项   待处理机会 4 项       │
├──────────────────────────────────────────────────────────────────────────────┤
│ 重大用能（SEU）                                                             │
│ 冷站系统      用电占比 46% · 重大改进潜力 · 负责人：张工                    │
│ 空调末端      用电占比 24% · 长时间运行偏离                                 │
│ 新风系统      用电占比 12% · 通风负荷显著                                   │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ 能源绩效指标（EnPI）                 │ 能源基线（EnB）                       │
│ 冷站单位冷量功率 0.67 kW/RT          │ 冷站基线 v3 · 需要复审                │
│ 期望区间 0.59–0.63 · 偏高            │ 原因：建筑面积 +18%                   │
│                                      │ 有效期：2025-01 → 2026-03            │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 主要绩效偏离                                                                │
│ 冷站单位冷量功率较基线期望高 6.3% · 数据有效 · [能源分析] [效率分析]         │
│ 新风单位面积用电较归一化基线高 8.1% · [查看相关变量]                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 后续动作                                                                    │
│ 4 个节能机会 · 2 个目标进行中 · 1 个基线复审待处理                          │
│ [节能机会] [目标与行动计划] [管理评审]                                      │
└──────────────────────────────────────────────────────────────────────────────┘
```

主界面全部中文；`SEU / EnPI / EnB` 保留作为行业缩写。

---

# 47. Accessibility

必须：

- 状态不只依赖颜色；
- SEU / EnPI / EnB 表格使用 semantic headers；
- 基线健康原因文本可读；
- 图表有 text/table alternative；
- acronym 首次出现提供中文解释；
- approval dialog keyboard 可操作；
- history/timeline 对读屏有顺序语义；
- around 768px 不依赖 hover。

---

# 48. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- 当前评审版本/状态；
- SEU 摘要；
- EnPI 当前状态；
- EnB health；
- 至少一个主要绩效偏离；
- 下一步行动。

## 1024–1439 px

- SEU / EnPI / EnB 上下排列；
- 专业 metadata 下沉；
- 表格保持扫描效率。

## Around 768 px

仍必须能：

- 查看评审状态；
- 查看重大用能；
- 查看指标与基线状态；
- 查看 review due / health reason；
- 进入能源分析 / 机会 / 目标 / 管理评审。

表格自身允许横向 scroll；页面整体不横向溢出。

---

# 49. No Defensive Programming / No Compatibility Design

明确禁止：

```text
review API error → 0 SEU
review unavailable → show “no review issues”
energy contributor top 5 → auto SEU
SEU missing criteria → sort by kWh and guess
EnPI unavailable → pick any KPI
EnPI formula missing → frontend invent formula
EnB unavailable → previous period fallback
EnB invalid → use old value silently
baseline API error → use last cached baseline as current
normalized unavailable → show actual as normalized
relevant variable missing → use average/0
occupancy missing → assume scheduled occupancy
static factor changed → keep baseline valid silently
model outside domain → extrapolate anyway
baseline revision → overwrite historical baseline
review revision → overwrite approved review
approved review changed → mutate in place
comparison → call it baseline
baseline variance → call it savings
EnPI improvement → call it verified savings
multiple baseline APIs → first success wins
one SEU → one energy/variable/baseline/opportunity request N+1
old Energy Review page adapter
old baseline page fallback
frontend-created SEU significance
frontend-created EnPI validity
frontend-created baseline health
```

不建立：

```text
new Energy Review unavailable
→ fallback old dashboard
```

原则：

> **One review → one authoritative revision. SEU is selected by approved criteria, not ranking convenience. EnPI and EnB are governed methods, not chart settings. A baseline is not a comparison. Normalized is not actual. Historical revisions are immutable. Unknown stays unknown.**

---

# 50. Browser Acceptance Criteria

## 产品语言

- 页面主标题、区块、按钮、状态中文优先；
- `SEU / EnPI / EnB` 作为缩写保留，但有中文名称；
- 不出现大面积 `Baseline Health / Relevant Variables / Expected / Review Due` 英文主文案。

## Review

- 当前 review revision/status/owner/effective context 可见；
- Approved revision 不被原地修改；
- next review / overdue 可见；
- material change 可以触发 review-needed state。

## SEU

- SEU 有 significance rationale；
- SEU 不等于 top consumer；
- criteria/source/review date 可查；
- SEU change 有 revision/audit。

## EnPI

- 中文名称 / unit / method / validity 可见；
- EnPI 与 Target 分开；
- invalid / unavailable 不显示 0；
- Normalized 与 Actual 分开。

## EnB

- baseline version/period/method/effective period 可见；
- Comparison ≠ EnB；
- invalid/unavailable 不 fallback previous period；
- static-factor change 能触发 review；
- old baseline revision 保留。

## Variables

- Relevant Variable 与 Static Factor 分开；
- source / data quality / applicability 可查；
- correlation 不显示为 root cause。

## Boundaries

- Energy analytics → 14；
- Efficiency → 16；
- Opportunity → 21；
- Objective / Action Plan → 23；
- M&V → 24；
- Management Review → 30；
- Data Quality → 31。

## Responsive / Accessibility

- 1440–1720px 是 coherent governance workspace；
- around 768px 核心任务完整；
- no color-only validity/health；
- no hover-only critical information；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 Energy Review / baseline compatibility adapter；
- 无 frontend-generated SEU / EnPI / EnB / normalization / baseline health；
- 无 N+1 SEU data fan-out；
- review scenario 无 runtime/network error。

---

# 51. Explicit Non-Goals

本页不是：

- ISO certification audit tool；
- generic Energy Dashboard；
- M&V project savings page；
- Utility Billing page；
- target/action execution workspace；
- arbitrary regression workbench；
- meter configuration editor；
- root-cause diagnosis page；
- compliance score dashboard。

---

# 52. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- 中文优先产品语言规则已接受；
- Energy Review revision/lifecycle 已明确；
- SEU selection/rationale contract 已明确；
- Relevant Variable / Static Factor 已分离；
- EnPI definition/validity/version 已明确；
- EnB definition/version/effective period 已明确；
- baseline health/review trigger 已明确；
- Actual / Normalized / Baseline / Comparison 已分离；
- review / opportunity / objective / M&V / management-review 边界已明确；
- no defensive fallback contract 已接受；
- old Energy Review / baseline pages 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
