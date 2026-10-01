# 19 碳排放 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `19 碳排放（Capability-gated）`  
> **Route intent：** `/sites/:siteId/carbon`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Scope 1 / Scope 2 / Scope 3`、`CO₂e`、`REC/EAC` 等标准术语保留，但必须配合中文业务解释。  
> **设计输入声明：** 本文件不参考当前项目已有 Carbon Dashboard、旧碳排放页、旧 ESG 页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 GHG Inventory / Energy / Fuel / Refrigerant / Emission Factor / Renewable Instrument / Target / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **让用户在明确盘查边界、排放源、活动数据、排放因子、核算方法和数据质量的前提下，准确理解站点当前温室气体排放、排放强度、Scope 2 双重核算结果以及目标进展，并能够追溯每一个排放结果是如何得到的。**

本 Surface 是 **auditable GHG inventory + carbon-performance analysis workspace**，不是：

- `kWh × 一个固定因子` 的计算器；
- ESG 大屏；
- 绿色评分页面；
- 绿证/绿电采购系统；
- 碳交易平台；
- M&V savings 页面；
- 自动把 REC/EAC 解释成物理零排放的页面；
- 把 avoided emissions 混入组织温室气体 inventory 的页面。

用户离开本页前应该知道：

1. 当前 inventory boundary / reporting period 是什么；
2. 当前包含哪些 Scope / source categories；
3. 站点 Scope 1 / Scope 2 排放分别是多少；
4. Scope 2 Location-based 与 Market-based 分别是多少；
5. 每一类排放来自哪些 activity data；
6. 使用了哪个 emission factor / GWP source/version/year；
7. 哪些数据是 Measured / Calculated / Estimated / Allocated；
8. REC/EAC/PPA 等 market instruments 是否满足当前核算方法要求；
9. 排放强度的 denominator 是什么；
10. 与目标相比处于什么状态；
11. 哪些变化来自 activity、factor、boundary 或 instrument，而不是“节能”；
12. 下一步应该进入 Energy、Billing、DER、Opportunity、M&V、Target 或 Data Quality。

---

# 2. 主要用户

## Primary

### 能源 / 可持续发展负责人

维护站点排放 inventory、Scope 2 方法、目标与周期报告。

### 碳核算 / ESG Analyst

检查排放源、factor、instrument、activity-data lineage 和报告一致性。

### 能源工程师

理解电力/燃料消耗变化对排放的影响，并从 Carbon 进入 Energy / Efficiency / Opportunity。

## Secondary

- Facility Manager：了解站点直接燃烧、制冷剂等主要排放源；
- 财务 / 采购：在有 renewable procurement capability 时读取合同工具事实；
- M&V Engineer：消费 project contribution，但不把 inventory variance 当 savings；
- Data Engineer：处理 factor/activity/source quality；
- Management：读取可审计的排放与目标状态。

---

# 3. 外部最佳实践依据

## 3.1 GHG Protocol Corporate Standard — 组织级盘查需要明确 Scope 与边界

GHG Protocol Corporate Standard 为组织级温室气体 inventory 提供核算和报告要求，覆盖 Scope 1 / 2，并与 Scope 3 标准衔接。

来源：

- https://ghgprotocol.org/corporate-standard
- https://ghgprotocol.org/standards-guidance

**本页采用：**

- inventory boundary 必须明确；
- Scope 1 / Scope 2 分开；
- 气体级别与 CO₂e 结果可追溯；
- base year / historical recalculation 必须 versioned/auditable；
- Scope 3 只有 capability 和正式 inventory contract 存在时出现。

## 3.2 GHG Protocol Scope 2 Guidance — Location-based / Market-based 双重核算

GHG Protocol Scope 2 Guidance 专门规范 purchased electricity / steam / heat / cooling 的 Scope 2 排放，并对 market-based contractual instruments 提供质量要求。

来源：

- https://ghgprotocol.org/scope-2-guidance

**本页采用：**

```text
Location-based
≠
Market-based
```

两种方法必须分别显示、分别解释，不能压成一个“Scope 2”。

## 3.3 GHG Protocol — Market-based contractual instruments 需要质量约束

Scope 2 Guidance 对 contractual instruments（例如 REC/EAC 等）设置质量标准，并要求透明披露。

**本页采用：**

- REC/EAC 不是默认 0-emission override；
- instrument 必须有 geography/vintage/quantity/eligibility/ownership 等 owner-defined facts；
- invalid/incomplete instrument 不进入 authoritative market-based result；
- frontend 不自行决定 instrument eligibility。

## 3.4 EPA eGRID — 电力排放因子具有地域、年份和方法属性

EPA eGRID 提供美国电力系统 emissions、emission rates、generation 和 resource mix 数据，并明确区分不同区域/年份的数据。

来源：

- https://www.epa.gov/egrid
- https://www.epa.gov/egrid/technical-resources

**本页采用：**

- electricity factor 必须带 geography / data year / source / version；
- 不能用全国平均因子替代有正式 subregion factor 的结果；
- factor 更新不能静默重写历史 inventory。

## 3.5 EPA GHG Emission Factors Hub — 排放因子需要维护版本和来源

EPA GHG Emission Factors Hub 定期更新 organizational GHG reporting 可用的默认排放因子。

来源：

- https://www.epa.gov/climateleadership/ghg-emission-factors-hub

**本页采用：**

- emission factor 是 versioned reference data；
- factor revision / source year 必须保留；
- 历史 period 使用当时 approved factor context；
- factor unavailable 不回退成 hardcoded constant。

## 3.6 EPA Scope 1 / Scope 2 Inventory Guidance — Scope 1 来源不只是燃气

EPA guidance 对 Scope 1 / 2 inventory 明确区分 stationary combustion、mobile combustion、refrigeration/air-conditioning 等直接排放源，以及 purchased electricity/steam/heat/cooling 等间接排放。

来源：

- https://www.epa.gov/climateleadership/scope-1-and-scope-2-inventory-guidance
- https://www.epa.gov/climateleadership/determine-emissions-sources

**本页采用：**

- Scope 1 可以包含 stationary fuel、mobile fuel、refrigerants 等；
- refrigerant leakage 不从电表推断；
- source categories 保持独立；
- source coverage 由 inventory owner 管理。

## 3.7 ISO 14064-1:2018 — 组织级排放量化、报告与验证需要可管理 inventory

ISO 14064-1:2018 对组织级温室气体排放和移除的量化、报告、inventory 设计、管理与验证提出要求。

来源：

- https://www.iso.org/standard/66453.html

**本页采用：**

- inventory revision、method、evidence、verification status 需要审计；
- scope/boundary changes 不静默发生；
- reporting 和 verification context 不混入纯 chart state。

---

# 4. 产品语言契约

主界面中文优先使用：

```text
温室气体排放
直接排放（Scope 1）
购入能源间接排放（Scope 2）
所在地法（Location-based）
市场法（Market-based）
活动数据
排放因子
排放因子版本
数据性质
排放强度
可再生能源属性凭证
目标进展
核算边界
数据质量
```

避免主界面只有：

```text
Inventory
Factor Set
LB / MB
Instrument Quality
Carbon Target
```

`Scope 1 / Scope 2 / CO₂e / REC / EAC` 等标准缩写可保留，但需有中文上下文。

---

# 5. Domain Vocabulary

## 5.1 GHG Inventory / 温室气体盘查

某一组织/站点在明确 boundary、period、method 下形成的正式温室气体排放 inventory。

不是临时 chart query。

## 5.2 Scope 1 / 直接排放

组织拥有或控制来源产生的直接温室气体排放。

典型站点来源可能包括：

```text
固定燃烧
移动燃烧
制冷剂 / 冷媒泄漏
其他 owner-defined direct sources
```

## 5.3 Scope 2 / 购入能源间接排放

购入或获得的 electricity / steam / heat / cooling 相关间接排放。

## 5.4 Location-based

根据所在地电网/区域平均排放特征核算 Scope 2。

## 5.5 Market-based

根据符合方法要求的合同工具、供应商因子、residual mix 等 market-specific information 核算 Scope 2。

## 5.6 Activity Data / 活动数据

例如：

```text
Electricity kWh
Natural gas therm / m³
Diesel L
Steam MWhth
Refrigerant kg leaked
```

## 5.7 Emission Factor / 排放因子

用于把 activity data 转换为某类 GHG emissions 的 reference factor。

必须有：

```text
source
factor value
unit
geography
applicable source/activity
source year
publication/version
valid/effective period
```

## 5.8 GWP / 全球变暖潜势

将 CH₄、N₂O、HFC 等气体转换为 CO₂e 的换算依据。

GWP 不是 emission factor。

## 5.9 CO₂e

多个温室气体按 approved GWP source 转换后的二氧化碳当量。

## 5.10 Renewable Instrument

例如 REC / EAC / GO 等，用于 market-based Scope 2 核算的合同/环境属性工具。

其存在不等于物理电力来源被改变。

## 5.11 Carbon Intensity / 排放强度

排放量除以正式 denominator，例如：

```text
kgCO₂e/m²
kgCO₂e/occupied-hour
kgCO₂e/unit production
kgCO₂e/kWh delivered service
```

必须明确 denominator。

---

# 6. Mandatory Semantic Separation

以下全部禁止混同：

```text
Scope 1 = Scope 2
Scope 2 Location-based = Market-based
Activity Data = Emissions
Emission Factor = GWP
Measured = Estimated
Grid Factor = Supplier-specific Factor
REC/EAC = Physical zero-carbon electricity
Renewable purchase = zero Location-based emissions
Market-based result = Location-based result overwrite
Avoided Emissions = Inventory Emissions
Offsets = Inventory Reduction automatically
Carbon Target = Verified Reduction
Energy Reduction = Carbon Reduction automatically
Carbon Reduction = Verified Energy Savings
Factor Change = Operational Improvement
Factor Change = Energy Savings
Emission Intensity = Absolute Emissions
Estimated Refrigerant Loss = Measured Leak
```

正确关系：

```text
Inventory Boundary
↓
Source / Scope
↓
Activity Data
↓
Emission Factor
↓
Gas-level Emissions
↓
GWP
↓
CO₂e
↓
Location-based / Market-based result
↓
Intensity / Target / Trend
↓
Opportunity / M&V / Management Review
```

---

# 7. Primary Questions

## Q1 — 当前排放是多少？

显示：

- reporting period；
- Scope 1；
- Scope 2 Location-based；
- Scope 2 Market-based（适用时）；
- total according to selected reporting method；
- coverage / quality。

## Q2 — 排放来自哪里？

按 source category 分解：

```text
购入电力
购入蒸汽/热/冷
天然气/燃油等固定燃烧
移动燃烧
制冷剂泄漏
其他 owner-defined sources
```

## Q3 — Scope 2 为什么有两个结果？

明确显示：

```text
所在地法（Location-based）
市场法（Market-based）
```

以及 factor/instrument provenance。

## Q4 — 这些结果可信么？

显示：

- activity-data source；
- measured/estimated/allocated；
- factor source/version/year；
- instrument eligibility/status；
- missing coverage；
- inventory verification/review state。

## Q5 — 为什么排放变化？

区分：

```text
Activity changed
Emission factor changed
Market instrument changed
Inventory boundary changed
Static/site scope changed
Operational performance changed
```

不自动叫 root cause。

## Q6 — 目标进展怎么样？

显示 linked carbon target / target trajectory / current progress。

Target owner 不在本页创建黑盒目标。

## Q7 — 下一步去哪？

进入：

- 14 能源分析；
- 18 账单成本；
- 20 分布式能源与柔性；
- 21 节能机会；
- 23 目标与行动计划；
- 24 M&V；
- 30 管理评审；
- 31 数据质量。

---

# 8. Capability Gating

本 Surface 仅在站点存在真实 carbon-accounting capability 时出现。

典型 capability：

```text
carbon.inventory.read
carbon.scope1.read
carbon.scope2.read
carbon.market-instruments.read
carbon.factor.read
carbon.target.read
carbon.audit.read
```

如果只有能源数据、没有正式 factor/method owner：

- 14 Energy Analysis 仍存在；
- 19 不显示“临时碳排放”估算页。

如果只有 Scope 2：

- Scope 1 section 不显示；
- 不显示 `Scope 1 = 0`。

---

# 9. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/carbon
```

推荐 Search Params：

```text
from
until
inventoryVersion
scope
method          // location | market | dual
sourceCategory
factorSet
selectedSource
intensityMetric
view            // summary | sources | factors | instruments | history
```

不进入 URL：

- hover；
- transient tooltip；
- local accordion state；
- unsaved note；
- chart cursor。

---

# 10. Entry Contract

## 从 14 Energy Analysis

携带：

- Site；
- period；
- energy carrier；
- meter/source context。

Energy period 不自动变成 inventory reporting period。

## 从 18 Billing

携带 account / commodity / period，用于查看对应 carbon activity source。

## 从 DER

携带 on-site generation / export / procurement context。

## 从 Management Review

进入当前 approved inventory / target variance。

---

# 11. Exit Contract

主要出口：

```text
碳排放
→ 14 能源分析
→ 18 账单、成本与电价
→ 20 分布式能源与柔性
→ 21 节能机会
→ 23 目标与行动计划
→ 24 M&V
→ 30 管理评审
→ 31 数据质量
```

保持：

- Site；
- reporting period；
- inventory version；
- selected scope/source；
- method；
- source trail。

---

# 12. Responsibility Boundary

本 Surface 拥有：

- carbon inventory projection；
- scope/source breakdown；
- location/market method projection；
- factor provenance projection；
- instrument projection；
- emissions/intensity trend；
- inventory quality/coverage；
- target progress projection；
- audit/history projection。

本 Surface 不拥有：

- meter ingestion；
- utility bill truth；
- renewable certificate registry；
- factor-source publication；
- GWP standard publication；
- target authoring（属于 Objectives/Action Plans owner）；
- M&V verified savings；
- offset retirement platform；
- frontend-generated factor hierarchy；
- frontend-generated market-based eligibility。

---

# 13. Information Architecture

```text
碳排放上下文
  站点 · Reporting Period · Inventory Revision · Boundary

排放摘要
  Scope 1
  Scope 2 所在地法
  Scope 2 市场法
  数据覆盖 / Inventory 状态

排放来源
  电力 / 热 / 冷 / 燃料 / 制冷剂 / 其他

Scope 2 方法
  Location-based
  Market-based
  Factor / Instrument / Residual context

排放趋势与强度
  Absolute emissions
  Intensity
  Named comparison

目标与偏离
  Target trajectory
  Current variance
  Linked actions

专业详情
  Activity data
  Factor set
  GWP source
  Instrument provenance
  Inventory revision / audit
```

不是 ESG KPI Card Wall。

---

# 14. Inventory Identity / Revision Contract

每个 inventory 至少有：

```text
Inventory ID
Revision
Reporting period
Organizational/site boundary
Operational boundary / included scopes
Methodology
Prepared by
Reviewed / approved by
Effective/reporting status
Superseded relation
```

Approved inventory revision 不在原地修改。

新的 factor/boundary/method revision 需要形成新的 inventory result/revision 或正式 recalculation record。

---

# 15. Inventory Boundary Contract

必须明确：

```text
Site / organization scope
Included operations/assets
Excluded sources
Scope coverage
Energy carriers
Reporting period
Consolidation approach if applicable
```

Frontend 不根据设备列表自动决定 inventory boundary。

---

# 16. Scope 1 Contract

Scope 1 source categories 可包括：

```text
固定燃烧
移动燃烧
制冷剂 / 冷媒泄漏
工艺排放（如业务适用）
其他 owner-defined direct sources
```

每个 source 至少有：

```text
Activity data
Unit
Activity source
Measured/estimated state
Emission factor
Gas-level result
GWP source
CO₂e result
Quality
```

---

# 17. Stationary Combustion Contract

固定燃烧 activity 可来自：

- fuel meter；
- utility bill；
- fuel delivery / inventory owner；
- owner-approved estimate。

必须明确：

```text
Fuel type
Quantity
Unit
HHV/LHV basis if relevant
Factor source/version
CO₂ / CH₄ / N₂O treatment
```

不能：

```text
Natural Gas Cost
→ frontend estimate volume
→ authoritative emissions
```

除非正式 owner 提供转换。

---

# 18. Refrigerant / Fugitive Emissions Contract

制冷剂排放至少需要：

```text
Refrigerant type
Quantity emitted / loss estimate
Method
Source event / inventory
GWP source/version
CO₂e
Evidence / maintenance relation
```

允许方法由 owner 定义，例如 material balance / service-event based。

禁止：

```text
refrigerant alarm
→ assume leaked kg
```

也不能：

```text
equipment offline
→ refrigerant emissions = 0
```

---

# 19. Scope 2 Activity Contract

Scope 2 可包含：

```text
Purchased electricity
Purchased steam
Purchased heat
Purchased cooling
```

Activity data 必须来自正式 meter/bill/energy owner。

```text
Metered
Estimated
Allocated
Corrected
Unknown
```

保持独立。

---

# 20. Location-based Contract

Location-based Scope 2 至少需要：

```text
Activity quantity
Grid/region geography
Emission factor
Factor year
Factor source
Factor version
Applicable period
Coverage
```

如果正式 subregion/region factor 不可用：

由 carbon method owner 决定 fallback hierarchy。

Frontend 不自行：

```text
subregion missing
→ use US national average
```

---

# 21. Market-based Contract

Market-based result 至少需要：

```text
Activity quantity
Supplier/product/instrument context
Instrument quantity
Instrument type
Vintage
Geography/deliverability context when required
Retirement/ownership status when owner supports
Emission factor / residual mix
Method/version
Coverage
```

Frontend 不自行判断 instrument 符合 GHG Protocol quality criteria。

---

# 22. Dual Reporting Contract

如果 method/region 要求或组织选择双重报告：

页面必须并列显示：

```text
Scope 2 所在地法
Scope 2 市场法
```

不能：

```text
market-based available
→ hide location-based
```

也不能把两者相加形成一个 Scope 2 total。

---

# 23. Renewable Instrument Contract

可能包括：

```text
REC
EAC
Guarantee of Origin
Supplier-specific product
PPA / contract instrument
Other program-specific instrument
```

必须有 owner-provided：

```text
Instrument ID/reference
Type
Quantity
Unit
Vintage
Geography
Supplier/program
Retirement/ownership status
Eligibility/status
Effective reporting period
```

主界面显示中文业务语义，专业详情保留原始 instrument 类型。

---

# 24. Renewable Instrument ≠ Physical Power Contract

必须保持：

```text
Electricity consumed from grid
≠ Renewable instrument purchased
```

市场法的排放结果可以受 instrument 影响，但：

```text
REC/EAC purchase
≠ Site physically consumed zero-carbon electricity
```

Location-based result不因 instrument 自动变 0。

---

# 25. Residual Mix / Supplier Factor Contract

如果 market-based method 使用：

```text
Supplier-specific factor
Residual mix
Contract-specific factor
```

必须显示：

```text
Source
Geography
Year/version
Eligibility
Coverage
```

如果 factor unavailable：

显示 unavailable / incomplete。

不能从 location-based factor 静默复制。

---

# 26. Emission Factor Contract

每个 factor 至少需要：

```text
Factor ID/reference
Source/publisher
Factor value
Unit
Applicable activity/source
Geography
Source/data year
Publication/version
Effective period
Gas coverage
Method
```

Factor update 不能静默改写历史 inventory。

---

# 27. GWP Contract

GWP source/version 必须明确。

例如：

```text
GWP source
Assessment/report version
Gas
Factor
Effective inventory method
```

禁止：

```text
HFC kg × hardcoded GWP constant in UI
```

GWP 与 emission factor 是两个不同 owner/reference 层。

---

# 28. Gas-level Result Contract

当 source/method 支持时，专业层可追溯：

```text
CO₂
CH₄
N₂O
HFCs
other covered gases
↓
GWP conversion
↓
CO₂e
```

主界面默认可以展示 CO₂e，但 gas-level lineage 应可查。

---

# 29. Activity Data Quality Contract

至少区分：

```text
Measured
Calculated
Estimated
Allocated
Corrected
Missing
Suspect
Unknown
```

Estimated 不等于 Invalid，但必须显式。

Missing 不等于 0。

---

# 30. Factor Quality / Applicability Contract

Factor 需要 owner-defined applicability：

```text
Applicable
Outside geography
Outside period
Superseded
Missing
Unknown
```

如果 factor outside applicability：

> `当前排放因子不适用于该活动数据。`

而不是继续计算一个精确 CO₂e。

---

# 31. Emissions Calculation Boundary

计算应由 Carbon Accounting / Analytics owner 完成。

Frontend 不自己：

```text
activity × factor × GWP
```

生成 authoritative inventory truth。

前端可以展示公式和输入，但不拥有方法语义。

---

# 32. Source Breakdown Contract

排放来源可按：

```text
Scope
Energy carrier
Source category
System/process
Building/site area
Owner-defined category
```

但 aggregation 必须避免 overlap/double count。

例如 purchased electricity 的 location-based 与 market-based 是同一 activity 的两种方法结果，不是两个可相加 source。

---

# 33. Double-counting Contract

禁止：

```text
Location-based Scope 2
+
Market-based Scope 2
→ Total Scope 2
```

禁止：

```text
Building main electricity
+
submeter electricity
→ total activity
```

如果 submeter 是 main meter 的子集，activity lineage owner 必须说明 inclusive/exclusive relationship。

---

# 34. Carbon Intensity Contract

每个 intensity 必须有：

```text
Numerator emissions
Denominator
Unit
Boundary
Period
Normalization if any
Owner
Validity
```

例如：

```text
kgCO₂e/m²
kgCO₂e/occupied-hour
kgCO₂e/unit production
```

禁止：

```text
area missing
→ divide by 1
```

也不能跨不同 denominator 直接排名。

---

# 35. Absolute vs Intensity Contract

必须保持：

```text
Absolute Emissions
≠ Emission Intensity
```

可能出现：

```text
Absolute emissions ↑
Intensity ↓
```

因为产量/入住/面积等 denominator 变化。

页面需要同时给 context，不下假结论。

---

# 36. Emissions Trend Contract

趋势可显示：

- Scope 1；
- Scope 2 location-based；
- Scope 2 market-based；
- selected source/category；
- target trajectory；
- factor revision markers；
- boundary revision markers。

不同方法不默认叠成一根线。

---

# 37. Change Attribution Contract

排放变化至少区分：

```text
Activity change
Factor change
Instrument change
Boundary change
Method change
Source correction
Operational/efficiency change
```

页面可以显示“变化贡献/上下文”，但：

```text
Change Attribution
≠ Root Cause
```

真正 causal finding 进入 Diagnosis / Opportunity owner。

---

# 38. Emission Factor Change Contract

例如：

```text
Electricity usage unchanged
Grid factor ↓ 8%
Location-based emissions ↓ 8%
```

不能显示成：

```text
Energy efficiency improved 8%
```

也不能叫：

```text
Verified project savings
```

---

# 39. Target Contract

Carbon 页面可以消费：

```text
Target name
Target type
Scope/method
Base year
Target year
Target value / trajectory
Owner
Status
```

正式 target authoring 属于 Surface 23 Objectives & Action Plans。

---

# 40. Target vs Inventory Contract

必须区分：

```text
Inventory Result
Target
Forecast
Scenario
```

Target 不能覆盖 actual。

Forecast 不能显示成 inventory actual。

---

# 41. Base Year Contract

如果组织使用 carbon base year：

必须保留：

```text
Base year
Boundary
Method
Factor context
Recalculation policy
Revision
```

结构变化 / boundary change 需要由 owner决定是否 recalculation。

Frontend 不自己重算历史。

---

# 42. Project Contribution Contract

Carbon 页面可以显示来自 project/M&V owner 的：

```text
Verified project contribution
Reporting period
Boundary
Method
Status
```

但必须保持：

```text
Project Contribution
≠ Inventory subtraction automatically
```

是否体现在 organization inventory 由 carbon-accounting methodology 决定。

---

# 43. Avoided Emissions Boundary

Avoided emissions / enabled emissions reduction 如果产品未来支持，必须单独展示。

不能：

```text
Inventory emissions
-
Avoided emissions
=
Net Scope 1/2 inventory
```

除非正式标准/method owner明确允许。

默认 inventory 与 avoided/emissions benefit 分开。

---

# 44. Offsets Boundary

如果未来支持 offsets：

- 单独 capability；
- 单独 instrument/status；
- 不自动改写 gross Scope 1/2 inventory；
- retirement/eligibility 由 owner。

当前 Surface 不把 carbon offset 作为默认功能。

---

# 45. On-site Generation Contract

On-site PV / CHP / battery 等需要区分：

```text
Gross load
On-site generation
Grid import
Grid export
Fuel input
Renewable attribute ownership
```

例如：

```text
PV generated onsite
```

并不自动表示 renewable attribute 仍归 Site 所有；如果属性已出售，market-based accounting 语义可能不同。

这些事实由 DER / instrument owner提供。

---

# 46. Purchased Renewable Energy Contract

可以显示：

```text
Purchased renewable quantity
Instrument type
Coverage of purchased electricity
Applicable reporting period
Eligibility/status
```

但不显示：

```text
100% Renewable
```

除非 owner提供正式定义和 coverage。

---

# 47. Data Authority Contract

## Energy / fuel activity

Owner：Energy / Meter / Billing domain。

## Refrigerant events / inventory

Owner：Maintenance / Refrigerant Management domain。

## Emission factors

Owner：Carbon Factor domain。

## GWP

Owner：Carbon Method / Standard Reference domain。

## Renewable instruments

Owner：Renewable Procurement / Instrument Registry domain。

## Inventory results

Owner：Carbon Accounting domain。

## Target

Owner：Objectives / Sustainability Target domain。

## Verified project contribution

Owner：M&V / Project Performance domain。

Frontend 只做 carbon-centered projection。

---

# 48. Query / Read Model Contract

推荐：

```text
Carbon Inventory Projection
  + inventory revision/boundary
  + Scope 1 summary
  + Scope 2 location result
  + Scope 2 market result
  + source breakdown
  + intensity
  + target progress
  + data/factor/instrument quality
```

专业详情：

```text
Source Calculation Detail
Factor Detail
Instrument Detail
Inventory Revision History
```

禁止：

```text
50 sources
→ 50 activity requests
→ 50 factor requests
→ 50 GWP requests
→ 50 instrument requests
```

缺 projection/batch contract 时修 domain。

---

# 49. Loading / Empty / Partial / Error

## No Carbon Capability

整个 Surface 不出现。

不是显示一个空白“碳排放”菜单。

## Capability exists, inventory not configured

> `当前站点尚未建立可用温室气体盘查。`

## Inventory service unavailable

> `碳排放数据暂不可用。`

不能显示 `0 tCO₂e`。

## Scope 1 unavailable

不显示 `Scope 1 = 0`。

## Market-based unavailable

Location-based 可正常显示，同时：

> `市场法核算暂不可用。`

不能把 Location-based 复制过去。

## Factor unavailable

对应 emissions result 标 unavailable / incomplete。

不能 hardcode global factor。

## Instrument unavailable

不能默认 market-based = location-based 或 0。

---

# 50. Permission / Capability Gating

示例：

```text
carbon.read
carbon.scope1.read
carbon.scope2.read
carbon.factor.read
carbon.instrument.read
carbon.audit.read
carbon.export
carbon.target.read
```

无权限 action 不显示。

UI hiding 不替代 server authorization。

---

# 51. Export Contract

Export 至少保留：

```text
Site / Inventory boundary
Reporting period
Inventory revision
Scope/source
Activity value/unit
Activity data nature
Emission factor source/value/unit/year/version
GWP source/version
Gas-level emissions if available
CO₂e
Location/market method
Instrument reference/status when applicable
Data quality
Target context
Export timestamp
```

不能只导出：

```text
Site A, 1234 tCO2e
```

而丢失 factor/method lineage。

---

# 52. Visual / UX Contract

默认视觉层级：

```text
盘查上下文
↓
Scope 1 / Scope 2 双重结果
↓
排放来源
↓
排放趋势 / 强度
↓
目标进展
↓
Factor / Instrument / Data Quality
↓
Audit / Revision
```

禁止：

- “碳健康分 92”；
- 绿色渐变大屏；
- 只显示一个“总碳排”；
- 把 market-based 覆盖 location-based；
- 把 REC/EAC 显示成物理零排放；
- 把 factor revision 隐藏；
- 把 Estimated 隐藏在 tooltip；
- 用树叶图标替代 method/quality。

---

# 53. Chart Contract

## Emissions Trend

显示 absolute emissions + one named comparison/target where appropriate。

## Scope / Source Breakdown

优先 stacked/ranked bars + exact table。

## Location vs Market

优先并列事实 / small multiples，不把两者相加。

## Intensity

显示 denominator 和 unit。

## Factor / Boundary Change Marker

在趋势中明确标记 factor/boundary revision。

不同单位遵守 05 aligned-small-multiples 原则。

---

# 54. ECharts Boundary

ECharts 可以负责：

- emissions trend；
- source breakdown；
- intensity trend；
- target trajectory；
- factor/boundary markers；
- comparison。

ECharts 不负责：

- emission calculation；
- factor selection；
- GWP selection；
- instrument eligibility；
- Scope classification；
- inventory boundary；
- target validation；
- avoided-emissions accounting。

---

# 55. Component Mapping

```text
盘查上下文                   → Page Header + compact facts
Scope 摘要                   → semantic facts
方法切换/展示                → segmented/peer comparison, not hidden switch
来源分解                     → ECharts + Table
排放趋势                     → dedicated ECharts
Factor / Instrument Detail   → Table / Collapsible / definition list
数据质量                     → status + reason
目标进展                     → compact trajectory + text
Inventory history            → Timeline / Table
Export                       → Dropdown Menu
```

不使用 generic ESG dashboard framework。

---

# 56. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 碳排放 · 中央冷站                     盘查期：2026-01-01 → 2026-08-31       │
│ Inventory v4 · 已批准                  核算边界：站点 Scope 1 + Scope 2      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 直接排放（Scope 1）        486 tCO₂e       数据覆盖率 99.1%                 │
│ 购入能源间接排放（Scope 2）                                                │
│   所在地法 Location-based  3,820 tCO₂e                                    │
│   市场法 Market-based      2,940 tCO₂e                                    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 排放来源                                                                     │
│ 购入电力              3,820 tCO₂e（所在地法）                               │
│ 天然气固定燃烧          338 tCO₂e                                            │
│ 制冷剂泄漏              148 tCO₂e                                            │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ Scope 2 方法                         │ 目标进展                              │
│ 所在地法：EPA eGRID 2025 / 区域因子  │ 2030 目标：较基准年 -35%              │
│ 市场法：REC/EAC 覆盖 42%             │ 当前轨迹：落后目标 4.2%               │
│ 剩余电量：Residual Mix v2025         │ [目标与行动计划]                      │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 需要关注                                                                     │
│ 制冷剂排放数据 2 项为估算 · 市场法 instrument 3 项将在 30 天内到期          │
│ [能源分析] [分布式能源] [节能机会] [数据质量] [管理评审]                    │
└──────────────────────────────────────────────────────────────────────────────┘
```

主界面中文；标准 Scope / CO₂e / REC/EAC 名称作为专业辅助。

---

# 57. Accessibility

必须：

- Scope/method 不只靠颜色；
- Location-based / Market-based 有文本标签；
- source breakdown 有 table alternative；
- factor/instrument validity reason 可读；
- exact chart values keyboard 可达；
- CO₂e unit 可被读屏正确理解；
- Estimated/Measured 不只靠颜色；
- around 768px 不依赖 hover。

---

# 58. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- inventory period/revision；
- Scope 1；
- Scope 2 location；
- Scope 2 market（适用时）；
- main source breakdown；
- factor/instrument context；
- target / data-quality attention。

## 1024–1439 px

- source breakdown 与 method detail 上下排列；
- factor metadata 下沉；
- 保持 location/market 并列语义。

## Around 768 px

仍必须能：

- 查看 Scope 结果；
- 查看来源；
- 区分 location/market；
- 查看 factor/instrument provenance；
- 查看目标进展；
- 进入 Energy / Opportunity / Data Quality。

无 page-level horizontal overflow。

---

# 59. No Defensive Programming / No Compatibility Design

明确禁止：

```text
carbon API error → 0 tCO₂e
missing Scope 1 → 0 Scope 1
missing Scope 2 market → copy location result
missing factor → hardcoded global factor
missing subregion factor → national factor silently
factor outside valid year → use anyway
new factor version → rewrite historical inventory
activity missing → 0
estimated → measured
bill usage missing → meter fallback silently
meter missing → bill usage fallback silently
refrigerant alarm → assume leak amount
refrigerant event missing → 0 leakage
REC/EAC exists → market emissions 0
REC/EAC exists → location emissions 0
instrument missing retirement/eligibility → assume valid
market-based unavailable → location-based as market-based
supplier factor missing → location factor silently
residual mix missing → grid average silently
Location-based + Market-based → total Scope 2
submeters + parent meter → double-count activity
factor change → call it operational improvement
factor change → call it savings
energy reduction → carbon reduction without factor context
carbon variance → verified project reduction
avoided emissions → subtract from inventory
carbon offset → rewrite gross Scope 1/2
intensity denominator missing → divide by 1
target missing → infer from previous year
multiple carbon APIs → first success wins
one source → one activity/factor/GWP/instrument request N+1
old Carbon Dashboard adapter
old ESG page fallback
frontend-generated emission calculation
frontend-generated factor selection
frontend-generated market instrument eligibility
frontend-generated inventory total
```

不建立：

```text
new Carbon unavailable
→ fallback old carbon dashboard
```

原则：

> **One inventory result → one authoritative method and revision. Activity data, emission factor, GWP and market instrument remain separate facts. Location-based is not market-based. Renewable instruments do not rewrite physical electricity. Factor changes are not operational savings. Missing emissions stay missing.**

---

# 60. Browser Acceptance Criteria

## Capability

- 无 carbon-accounting capability 时，不显示空壳 Carbon Surface；
- capability 存在但 inventory 尚未配置，与 service unavailable 分开。

## Inventory

- reporting period / revision / boundary 可见；
- approved revision 不被原地修改；
- historical factor/method context 可追溯。

## Scope

- Scope 1 / Scope 2 分开；
- 未覆盖 Scope 不显示 0；
- source category 可追溯。

## Scope 2

- Location-based 与 Market-based 分开；
- 两种结果不相加；
- market unavailable 不复制 location；
- instrument coverage/status 可查。

## Factors

- source/year/version/geography 可见；
- factor missing/outside applicability 不继续权威计算；
- factor update 不重写历史。

## Activity

- measured / calculated / estimated / allocated 分开；
- missing ≠ 0；
- parent/submeter 不 double-count。

## Intensity

- denominator/unit/boundary 可见；
- absolute 与 intensity 分开；
- denominator unavailable 不计算。

## Targets / Projects

- target / actual / forecast 分开；
- project contribution ≠ inventory subtraction automatically；
- avoided emissions 不混入 Scope 1/2 gross inventory。

## Boundaries

- Energy → 14；
- Billing → 18；
- DER → 20；
- Opportunity → 21；
- Objectives → 23；
- M&V → 24；
- Management Review → 30；
- Data Quality → 31。

## Responsive / Accessibility

- 1440–1720px 是 coherent carbon workspace；
- around 768px 核心任务完整；
- no color-only method/quality；
- no hover-only critical values；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old Carbon/ESG compatibility adapter；
- 无 frontend-generated factor/GWP/instrument eligibility/inventory truth；
- 无 N+1 source calculation fan-out；
- 无 missing→0；
- review scenario 无 runtime/network error。

---

# 61. Explicit Non-Goals

本页不是：

- carbon trading platform；
- offset marketplace；
- REC/EAC registry；
- ESG disclosure suite；
- Scope 3 full value-chain platform；
- arbitrary lifecycle assessment tool；
- M&V savings page；
- renewable procurement system；
- generic sustainability score dashboard。

---

# 62. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Carbon capability gating 已明确；
- inventory boundary/revision 已明确；
- Scope 1 / Scope 2 已分离；
- Scope 2 Location-based / Market-based 已分离；
- activity / factor / GWP 已分离；
- factor source/year/version/geography 已明确；
- renewable instrument eligibility/provenance 已明确；
- instrument ≠ physical electricity 已明确；
- actual/estimated/missing 已明确；
- absolute/intensity 已分离；
- target / forecast / actual 已分离；
- project contribution / avoided emissions / inventory 已分离；
- 中文优先产品语言已落实；
- no defensive fallback contract 已接受；
- old Carbon / ESG Dashboard 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
