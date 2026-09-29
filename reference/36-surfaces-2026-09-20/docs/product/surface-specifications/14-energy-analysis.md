# 14 能源分析 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `14 能源分析`  
> **Route intent：** `/sites/:siteId/energy`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Energy Dashboard、旧能耗页、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Meter / Interval Data / Energy Type / Baseline / Normalization / Allocation / Data Quality / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

能源分析的唯一核心任务是：

> **让能源经理和工程师在一个明确的计量边界、时间范围和能源类型下，解释“用了多少、什么时候用、用在哪里、为什么变化”，并能区分真实计量、比较、基线、归一化、分项贡献与数据质量。**

Energy Analysis 是 **meter-centered analytical workspace**，不是：

- 通用 BI Dashboard；
- Utility Bill 页面；
- Demand Response 页面；
- Efficiency 分析页；
- Energy Review / ISO 50001 管理页；
- M&V savings page；
- FDD / Root Cause 页面；
- “随便拖十几个图”的 chart builder；
- 把功率趋势在浏览器里积分后冒充正式能耗的工具。

用户离开本页前应该知道：

1. 当前 period / timezone / energy type 是什么；
2. 当前 Actual Consumption 是多少；
3. 当前显示的是 measured、derived、allocated 还是 estimated；
4. 与哪个明确命名的 comparison 比较；
5. 差异发生在什么时间窗口；
6. 哪些 meter / end-use / system 是主要贡献者；
7. contributor coverage 是否完整、是否有 unallocated remainder；
8. weather / occupancy / schedule 等 context 是否可能解释变化；
9. 当前数据是否有 missing / estimated / stale / quality 问题；
10. 是否需要继续进入 Demand、Efficiency、Device、Diagnosis、Opportunity 或 Data Quality。

---

# 2. 主要用户

## Primary

### Site Energy Manager

需要快速理解站点实际能源使用、period-over-period 变化、主要贡献和异常时间窗口。

### Energy Engineer

需要深入查看 meter hierarchy、submeter contribution、normalization、baseline version 和数据质量。

### Facility / HVAC Engineer

需要从 energy deviation 进入 System / Device / Trend / Diagnosis，理解运行侧可能发生了什么。

## Secondary

- Management：读取简洁、可信的实际能耗与变化结论；
- Sustainability / Carbon user：使用本页 energy facts 作为 Carbon owner 的输入；
- M&V engineer：从本页进入正式 M&V，但不在本页验证 savings；
- Data engineer：排查 meter lineage / missing / estimated data；
- Utility / cost analyst：进入 Bills / Cost / Tariff。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP EMIS — Interval Meter Analytics 是独立核心能力

DOE/FEMP 明确把 EMIS capabilities 分为 utility bill management、interval meter analytics、M&V、AFDD、supervisory control、O&M optimization 等。Interval meter analytics 使用小时或更细粒度数据分析 daily/weekly profiles、base load、scheduling、peak demand，并可以结合 weather 和 submeter/system trends 理解 usage changes。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- https://www.energy.gov/cmei/femp/energy-management-information-systems-federal-facilities

**本页采用：**

- Energy Analysis 独立于 Bills、M&V、FDD；
- load profile 是主分析画布；
- interval data 的粒度/coverage/aggregation 必须显式；
- “why changed” 通过可解释 context / contributor / variance window 支持调查，不由前端制造因果结论。

## 3.2 DOE / LBNL Energy Information Handbook — Load Profile 用于理解 Schedule、Base Load、Peak

Energy Information Handbook 把 load profiling 作为基础能源分析方法，要求 interval meter data，并用 daily/weekly profile 理解时间分布和运行模式。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/energy-information-handbook.pdf

**本页采用：**

- Primary Load Profile 是第一分析画布；
- 昼夜、工作日/周末、occupied/unoccupied 差异应该可分析；
- profile 只能展示真实 interval/aggregation semantics；
- 数据缺口不能在前端悄悄填平后再计算结论。

## 3.3 DOE Metering Best Practices — Metering Hierarchy 决定诊断粒度

DOE 的 Metering Best Practices 给出从 whole-building meter 到 panel/sub-panel、circuit、end-use 的计量层级。更细粒度 submetering 可以提高识别 end-use / system contribution 的能力。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/tools/MeteringBestPractices.pdf
- https://www.energy.gov/cmei/femp/metering-federal-buildings

**本页采用：**

- contributor 必须来自真实 meter/submeter hierarchy 或 owner-defined allocation；
- whole-building meter 不足以支持 equipment-level attribution 时，不在 UI 里猜设备贡献；
- meter lineage、boundary、coverage 是专业层一等事实。

## 3.4 DOE Building Energy Data — Energy Data 必须与 Assets / Activities / Relationships 结合

DOE BTO 明确指出，仅仅测量 meter energy 并不足以驱动改进；energy use 还需要与 building assets / activities 关联，并以可复用的数据标准和关系模型组织。

来源：

- https://www.energy.gov/cmei/buildings/building-energy-data
- https://www.energy.gov/cmei/buildings/semantic-modeling-and-interoperability

**本页采用：**

- meter → building/system/end-use 的关系来自 Registry/Semantic Model；
- 不根据 meter name / equipment name 猜 lineage；
- contributor drilldown 依赖真实 relationship，不做字符串匹配。

## 3.5 ENERGY STAR Portfolio Manager — Benchmarking / Weather Normalization 用于可比性

ENERGY STAR Portfolio Manager 支持与过去时期、national median、peer buildings 等比较，并提供 Site/Source EUI 和 weather-normalized metrics。其 weather normalization 是为了让不同天气条件下的时间比较更合理。

来源：

- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/understand-metrics/what-eui
- https://portfoliomanager.energystar.gov/pm/glossary

**本页采用：**

- comparison 必须命名；
- normalized value 必须与 raw actual 并存；
- weather-normalized ≠ measured actual；
- benchmarking / normalized metric 不自动解释 root cause。

## 3.6 ISO 50006:2023 — EnPI / EnB 是正式能源绩效方法

ISO 50006:2023 提供建立、使用、维护 EnPI 与 EnB 的指导，并用于测量、监控和展示能源绩效改进。

来源：

- https://www.iso.org/standard/79367.html

**本页采用：**

- baseline / normalization / EnPI 必须是 owner-defined、versioned 的正式对象；
- Energy Analysis 可以消费 baseline/normalized projection，但不在本页定义正式 EnB/EnPI；
- 正式 Energy Review / SEU / EnPI / EnB 管理属于 Surface 17。

## 3.7 IPMVP Core Concepts — Difference 不等于 Savings

IPMVP 明确指出 savings 本身不能直接测量，而是通过基线期与报告期 measured energy 的一致比较，并结合适当 adjustments 来确定；measurement boundary 也是 M&V 的核心。

来源：

- https://evo-world.org/en/products-services-mainmenu-en/protocols/ipmvp
- https://evo-world.org/en/library/download-protocol-documents-mainmenu-en

**本页采用：**

```text
Actual difference
≠ Verified savings
```

- 本页可以显示 baseline comparison / variance；
- 不能把 difference 直接叫 savings；
- 正式 savings、adjustment、uncertainty、measurement boundary verification 属于 Surface 24 M&V。

---

# 4. Energy Analysis Vocabulary

## 4.1 Energy Quantity

一定时间范围内的能源量，例如：

```text
Electricity: 124,560 kWh
Natural gas: 8,430 therm
Steam: 1,220 MMBtu
Chilled water: owner-defined thermal energy quantity
```

Energy 是累计量。

## 4.2 Demand / Power

某一 interval 或瞬时/平均窗口的功率/需求，例如：

```text
kW
MW
kVA
```

Demand/Power 不是 Energy。

## 4.3 Actual

来自 authoritative meter / historian / energy aggregation owner 的实际记录或正式 derived value。

Actual 仍可能标记为：

```text
Measured
Estimated
Derived
Adjusted by source system
```

这些 provenance 必须保留。

## 4.4 Comparison

用于回答“当前比什么”的明确比较窗口/对象。

例如：

```text
Previous period
Same period last year
Previous comparable weekday set
Custom period
Portfolio peer / reference (when supported)
```

Comparison 不是 Baseline。

## 4.5 Baseline

由 Energy Review / M&V / analytics owner 发布的、具有 method/version/effective period 的参考模型或参考期。

Baseline 不由本页临时生成。

## 4.6 Normalized Value

使用 weather、occupancy、production、floor area 或其他 relevant variable 对 energy performance 进行 owner-defined adjustment / denominator transformation 后的值。

Normalized ≠ Actual。

## 4.7 Contributor

在明确 measurement boundary 下，对 total energy 的组成贡献。

Contributor 可以是：

```text
Metered submeter
Derived end-use
Allocated tenant/department/system
Owner-defined category
```

必须标注 provenance。

## 4.8 Variance Window

Actual 与 named comparison / baseline 之间差异最明显的时间窗口。

Variance Window 是“哪里不同”，不是“为什么”的最终因果结论。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
Energy = Demand
Actual = Normalized
Actual = Baseline
Comparison = Baseline
Baseline = Target
Baseline = Forecast
Difference = Savings
Variance = Root Cause
Estimated = Measured
Allocated = Metered
Derived = Measured
Submeter sum = Parent meter automatically
Meter name = End-use semantic relation
Power trend integration in browser = authoritative energy
Missing interval = 0 energy
Unknown energy type = electricity
Site Energy = Source Energy
EUI = Energy
Weather normalized = actual measured consumption
```

正确关系：

```text
Actual measured / derived facts
↓
Named comparison
↓
Variance windows
↓
Contributor + operational context
↓
Deeper investigation
```

正式 Baseline / EnPI / M&V 是独立 owner workflow。

---

# 6. Primary Questions

## Q1 — 用了多少？

显示：

- period；
- energy type；
- actual consumption；
- unit；
- source/provenance；
- coverage / estimated-data state。

## Q2 — 什么时候用？

显示：

- primary interval load profile；
- daily/weekly pattern；
- occupied/unoccupied or schedule context when available；
- variance windows；
- notable interval peaks as context。

## Q3 — 用在哪里？

显示 ranked contributors：

- building/system/end-use/meter；
- measured / allocated / derived label；
- amount；
- share of covered total；
- unallocated/remainder；
- coverage。

## Q4 — 为什么变化？

本页只提供 explainable context：

- contributor change；
- schedule/occupancy context；
- weather context；
- operational event link；
- major system/device change；
- data-quality change。

不能把 correlation 自动写成 cause。

## Q5 — 数据可靠吗？

显示：

- missing intervals；
- estimated intervals；
- meter coverage；
- stale/late data；
- lineage gaps；
- reconciliation issues；
- source revision。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/energy
```

推荐 Search Params：

```text
from
until
periodPreset
energyType
meterScope
comparison
compareFrom
compareUntil
baselineVersion
normalization
normalizeBy
groupBy
selectedContributor
selectedVariance
resolution
```

共享到 Demand / Efficiency 的核心 context：

```text
site
period
comparison
baselineVersion
```

不进入 URL：

- chart cursor；
- transient tooltip；
- accordion open state；
- local table width；
- unsaved annotation。

---

# 8. Entry Contract

## Site Overview

携带：

- Site；
- period；
- selected energy context；
- comparison（若 overview 有明确命名比较）。

## Portfolio / Benchmarking

携带：

- Site；
- benchmark period；
- selected metric；
- benchmark source/context。

Benchmark context 不自动变成本页 Baseline。

## Device / System / Diagnosis

如果从设备或系统进入，只有该对象存在 authoritative meter/end-use relation 时才预选 energy scope。

禁止根据 asset name 猜 meter。

## Energy Review / Opportunity

携带：

- relevant EnPI / baseline version；
- period；
- measurement scope；
- source trail。

---

# 9. Exit Contract

主要出口：

```text
Energy Analysis
→ 15 Demand / Load / Flexibility
→ 16 Efficiency
→ 17 Energy Review / SEU / EnPI / Baseline
→ 07 Device Detail
→ 05 Trend Analysis
→ 10 Diagnosis
→ 18 Bills / Cost / Tariff
→ 21 Savings Opportunity
→ 24 M&V
→ 31 Data Quality
```

保持 compatible：

- Site；
- Period；
- Comparison；
- Baseline Version；
- Selected contributor / meter scope when semantically valid。

---

# 10. Responsibility Boundary

Energy Analysis 拥有：

- energy analysis context；
- actual energy projection；
- named comparison；
- primary load profile；
- contributor breakdown；
- variance-window identification/projection；
- meter lineage drilldown；
- data-quality projection；
- normalization selection（消费 owner-defined method）；
- contextual overlays；
- export of analytical evidence。

Energy Analysis 不拥有：

- meter master configuration；
- utility bill validation/payment；
- tariff calculation；
- formal EnB / EnPI definition；
- savings verification；
- carbon factors；
- root-cause diagnosis；
- frontend energy integration from raw power samples；
- arbitrary allocation formulas created in the chart UI。

---

# 11. Information Architecture

```text
Context Header
  Site · Period · Timezone · Energy Type · Meter Scope

Analysis Controls
  Period · Energy Type · Comparison · Normalization · Grouping

Headline Energy Facts
  Actual Consumption
  Named Comparison Difference
  Demand context (concise)
  Data Coverage / Quality

Primary Load Profile
  Actual
  Named Comparison / Baseline when explicitly selected
  Context overlays / variance windows

Ranked Contributors
  Meter/System/End-use
  Measured/Allocated/Derived provenance
  Amount / Share / Coverage

Variance Windows
  when / how much different
  linked context

Professional Detail
  Meter Lineage
  Aggregation / Resolution
  Normalization Method
  Baseline Version
  Data Quality / Estimated Data

Professional Exits
  Demand · Efficiency · Device · Diagnosis · Opportunity · Data Quality
```

首屏不做 8–12 张同权重 KPI 卡片。

---

# 12. Context Header Contract

Header 必须始终明确：

```text
Site
Period
Timezone
Energy Type
Measurement Scope
```

例如：

```text
Phoenix Central Plant
2026-09-01 → 2026-09-14
Site timezone UTC-07:00
Electricity
Main + authorized submeters
```

用户不能在图表中迷失当前 measurement boundary。

---

# 13. Period / Calendar Contract

## Absolute period

明确起止时间与 site timezone。

## Presets

例如：

```text
Today
Yesterday
Last 7 days
Month to date
Previous month
Year to date
```

Preset 展开后必须对应明确 absolute range。

## Calendar alignment

Comparison 必须说明：

- same elapsed duration；
- same calendar dates；
- same weekdays；
- same billing period；
- owner-defined comparable period。

`Month-to-date vs previous full month` 默认禁止，因为时间长度不一致，除非用户明确选择并页面清楚说明。

---

# 14. Timezone / Interval Contract

Energy intervals 以 authoritative timestamp/timezone 处理。

必须处理：

- site local timezone；
- daylight saving transitions；
- interval ending vs interval beginning semantics；
- meter interval duration；
- late-arriving/corrected intervals。

Frontend 不自行假设：

```text
one record = exactly 15 minutes
```

如果 owner 没有提供 interval semantics，就不能精确积分/重采样。

---

# 15. Energy Type / Carrier Contract

能源类型必须来自 owner taxonomy，例如：

```text
Electricity
Natural Gas
Steam
Hot Water
Chilled Water / Thermal Energy
Fuel Oil
Other owner-defined carriers
```

## Cross-carrier total

不同 carrier 只有在 owner 提供统一 conversion methodology 时，才允许展示：

```text
Total Energy
```

并必须显示：

- conversion basis；
- site/source basis；
- unit；
- factor version。

禁止前端把 `kWh + therm` 直接相加。

---

# 16. Site Energy / Source Energy Contract

如果 capability 存在，必须明确：

```text
Site Energy
Source Energy
```

两者不混写。

Source Energy conversion 需要 owner-defined factor/method/version。

ENERGY STAR Score / Source EUI 等 benchmark metric 属于相应 metric owner，本页只消费，不自行复刻算法。

---

# 17. Actual Consumption Contract

Actual Consumption 必须有：

```text
Value
Unit
Period
Measurement scope
Source
Provenance class
Coverage
Revision / last updated
```

Provenance class 可包括：

```text
Measured
Estimated by source
Derived by analytics owner
Allocated
```

`Actual` 表示当前分析所采用的正式事实，不意味着每个 interval 都一定是 physical meter measured。

Estimated fraction 必须可查看。

---

# 18. Power-to-Energy Derivation Boundary

禁止浏览器：

```text
fetch power samples
→ trapezoid integrate
→ call result authoritative kWh
```

如果系统允许从 power derived energy：

必须由 historian/energy analytics owner 提供：

- method；
- interval semantics；
- missing-data handling；
- quality rule；
- unit conversion；
- revision/provenance。

UI 标记 `Derived`。

---

# 19. Primary Load Profile

Primary chart 回答：

> **能量/负荷随时间怎样变化？**

根据 owner 数据类型可显示：

```text
Interval Energy (kWh/interval)
Average Demand / Power (kW)
```

必须明确 y-axis 语义。

不要把 interval kWh 曲线标签写成 kW。

## Default comparison

最多一个明确命名 comparison 叠加在主图。

更多 breakdown 放到 contributor / small multiples，而不是叠 8 条线。

---

# 20. Named Comparison Contract

Comparison 必须明确命名，例如：

```text
Actual vs Previous 14 days
Actual vs Same period last year
Actual vs Previous comparable weekdays
Actual vs Custom period
```

同时显示：

- current period；
- comparison period；
- alignment rule；
- timezone；
- data coverage。

禁止：

```text
comparison API unavailable
→ silently use previous period
```

---

# 21. Baseline Contract

Baseline 只能来自 authoritative baseline owner。

显示：

```text
Baseline name
Version
Owner
Effective period
Model/method summary
Relevant variables
Last validation state
```

如果 baseline unavailable：

> `基线暂不可用`

不能自动改为 previous period。

## Baseline difference

可以显示：

```text
Actual - Baseline Expected
```

但默认名称是：

```text
Variance / Avoided-use candidate context
```

而不是：

```text
Savings
```

正式 savings 属于 M&V。

---

# 22. Normalization Contract

Normalization 用于提高不同 operating condition 下的可比性。

可包括：

- weather；
- occupancy；
- production/output；
- floor area / EUI；
- operating hours；
- owner-defined relevant variables。

必须显示：

```text
Raw Actual
Normalized Metric
Method
Variables
Version
Applicability
```

禁止 normalized metric 覆盖 raw actual。

## Example

```text
Actual electricity: 124,560 kWh
Weather-normalized: 118,900 kWh-equivalent context
```

具体 label/unit 由 owner method 定义。

---

# 23. EUI / Intensity Contract

只有 denominator owner 可信时才显示 intensity。

例如：

```text
kWh/m²
kBtu/ft²
kWh/occupied-hour
kWh/unit-production
```

必须显示 denominator definition / period。

禁止：

```text
area missing → use 1
occupancy missing → use scheduled occupancy silently
```

Portfolio benchmarking 属于独立 Benchmarking Surface，本页不把 EUI 排名变成主任务。

---

# 24. Ranked Contributors Contract

默认显示 5–10 个主要 contributor，而不是无限饼图。

每个 contributor 必须有：

```text
Name
Scope
Amount
Share
Provenance
Coverage
Relation to total
```

## Provenance

清楚标记：

```text
Measured
Allocated
Derived
Estimated
```

## Remainder

如果 contributors 不覆盖 total：

```text
Unallocated / Unmetered / Remainder
```

必须显式存在。

不能把已覆盖部分重新归一到 100% 而隐藏缺口。

---

# 25. Contributor Boundary / Double-counting Contract

同一 breakdown 中不能同时把 parent meter 与其 child submeters 当作可相加 peers。

例如：

```text
Building Main = 1,000 kWh
Chiller Plant Submeter = 400 kWh
Lighting Submeter = 250 kWh
```

若 Main 已包含 children：

不能显示：

```text
Total = 1,650 kWh
```

Meter hierarchy owner 必须提供：

- parent/child relation；
- inclusive/exclusive semantics；
- overlap；
- virtual meter formula；
- effective period。

---

# 26. Meter Lineage Contract

专业层必须允许回答：

> 这个数字从哪里来？

Lineage 至少包括：

```text
Meter / Virtual Meter identity
Measurement boundary
Energy type
Parent/child relation
Physical / virtual
Aggregation owner
Source system
Effective period
Quality / estimate state
```

默认用 structured lineage / hierarchy；只有真实 relation model 且图能提升理解时才画 topology graph。

禁止根据 meter naming convention 构造 lineage。

---

# 27. Allocation Contract

某些 contributor 不是 physical meter，而是 allocation。

例如：

- tenant allocation；
- department allocation；
- modeled end-use；
- shared-system allocation。

必须显示：

```text
Allocation method
Input meters
Driver / coefficient
Version
Effective period
Owner
Coverage
```

`Allocated` 不得显示成 `Measured`。

---

# 28. Reconciliation Contract

当 parent meter 与 child breakdown 不一致时，可显示：

```text
Parent total
Covered child total
Remainder
Difference / reconciliation state
```

差异可能来自：

- unmetered loads；
- losses；
- timing misalignment；
- estimated intervals；
- virtual meter method；
- data gaps。

Frontend 不自动把 remainder 平摊到 children。

---

# 29. Variance Window Contract

系统可以识别并排序：

```text
largest positive variance windows
largest negative variance windows
persistent off-hours excess
step changes
```

但 variance 算法必须由 analytics owner 明确。

每个 window 显示：

- from / until；
- actual；
- comparison/baseline；
- delta；
- coverage；
- related context；
- deep-link Trend/Operations/Diagnosis。

Variance Window 不是 Finding/Root Cause。

---

# 30. Contextual Explanation Contract

为了帮助解释“为什么变化”，允许关联：

```text
Weather
Occupancy
Operating hours / schedule
System operating mode
Major equipment runtime
Strategy deployment
Work / shutdown event
Known site event
```

前端只能表达：

```text
Coincident / Associated context
```

不能表达：

```text
Cause confirmed
```

除非 Diagnosis / analytics owner 正式提供 causal finding。

---

# 31. Weather Context

Weather 可显示：

- outdoor dry-bulb；
- degree days；
- humidity；
- owner-defined weather indices。

必须显示 weather source / station / period when available。

Weather overlay 是 context，不等于 weather normalization model。

---

# 32. Occupancy / Schedule Context

Occupancy facts可能是：

```text
Measured occupancy
Scheduled occupancy
Operating schedule
Unknown
```

必须区分。

```text
Occupancy unknown
≠ Unoccupied
```

Energy Analysis 可以显示 off-hours usage，但只有 schedule/occupancy owner 明确时才能标 `Off-hours`。

---

# 33. Demand Boundary

14 可以显示 concise demand context：

```text
Peak demand in selected period
Peak timestamp
```

但以下属于 15 Demand / Load / Flexibility：

- peak interval investigation；
- load duration curve；
- demand charge exposure；
- peak contributors；
- DR；
- load shifting；
- peak shaving；
- flexible load。

不要把 15 做成 14 的一个 Tab。

---

# 34. Efficiency Boundary

14 回答 consumption / usage distribution。

16 Efficiency 回答：

> 同样负荷下系统是否高效？

因此：

```text
High energy use
≠ Low efficiency
```

高能耗可能只是高负荷、长运行时间或天气更极端。

COP/kWRT/ΔT 不在 14 主视图混入。

---

# 35. Energy Review / Baseline Boundary

17 Energy Review 管理：

- SEU；
- EnPI；
- EnB；
- relevant variables；
- normalization method；
- baseline governance/version/effective period。

14 只消费这些正式对象用于分析。

不能在 14 的 chart control 里“随手创建新基线”。

---

# 36. Bills / Cost Boundary

18 Bills / Cost / Tariff 管理：

- billing statement；
- utility account；
- actual/estimated bill；
- reconciliation；
- tariff；
- demand charge；
- cost allocation。

14 默认分析 physical energy facts。

如果显示 cost context，也必须来自 Cost/Tariff owner，不在前端 `kWh × 单价` 估算后当 authoritative cost。

---

# 37. M&V Boundary

24 M&V 管理：

- measurement boundary；
- baseline/reporting periods；
- adjustments；
- model fit / uncertainty；
- operational verification；
- verified savings；
- persistence。

14 可以提供 source data / period / baseline context，但：

```text
Actual 100 MWh
Baseline 120 MWh
Difference 20 MWh
```

不能自动显示：

```text
Savings = 20 MWh
```

---

# 38. Data Quality Contract

能源数据至少区分：

```text
Measured good
Estimated
Missing
Late / incomplete
Corrected / revised
Suspect / bad quality
Unknown
```

## Missing

不显示 0。

## Estimated

必须可见 estimated flag / fraction。

## Revised

如果 meter owner 后续纠正 interval，analysis 要反映 current revision；export/report 需要 provenance。

## Coverage

推荐显示：

```text
Interval coverage 98.7%
Estimated 1.1%
Missing 0.2%
Last updated 11:45
```

不制造 generic `Data Health 92`。

---

# 39. Missing-data / Gap-filling Boundary

某些正式 analytics owner 可能有 gap-filling method。

如果存在，必须显示：

- method；
- affected intervals；
- estimate flag；
- revision；
- impact on aggregation。

Frontend 禁止：

```text
missing → 0
missing → previous interval
missing → linear interpolate
```

除非这些正是 authoritative owner 的 method，并作为 estimated data 明确返回。

---

# 40. Aggregation / Resolution Contract

可支持：

```text
15-minute
Hourly
Daily
Weekly
Monthly
```

具体可用 resolution 由 owner 数据决定。

必须区分 aggregation function：

```text
Sum energy
Average demand
Max demand
Min/Max context
```

不能：

```text
average kWh intervals → monthly energy
sum kW samples → energy
```

聚合语义由 energy/historian owner 定义。

---

# 41. Late-arriving / Corrected Data Contract

Interval energy 经常存在 late posting / correction。

因此本页默认是 query-driven analytical surface，不要求像 04 Operations 那样持续高频 Stream。

Current-day view 可以周期性 refresh / event invalidation。

如果 owner 支持 correction event：

- refetch affected period；
- preserve user zoom/selection；
- mark revised interval if useful；
- do not append duplicate sample。

`No latest interval yet` 不等于 zero use。

---

# 42. Query / Read Model Contract

推荐 Energy Analysis read model：

```text
Energy Analysis Projection
  + period/context
  + actual summary
  + comparison summary
  + primary interval profile
  + ranked contributors
  + variance windows
  + data coverage
  + meter-lineage summary
  + normalization/baseline metadata
```

大 contributor table / lineage 可独立分页。

禁止：

```text
100 meters
→ 100 interval queries
→ 100 registry queries
→ 100 lineage queries
```

前端不建立并发 fan-out workaround。

---

# 43. Loading / Empty / Partial / Error

## No data in period

owner 成功返回 empty：

> `该时间范围内没有能源数据。`

不是 0 kWh。

## Meter service unavailable

> `能源数据暂不可用。`

不能显示 0 Consumption。

## Comparison unavailable

Actual 保留；comparison 显示 unavailable。

不能偷偷换 previous period。

## Baseline unavailable

显示：

> `基线暂不可用。`

Actual/normal comparison仍可用。

## Contributor data unavailable

Total 仍可显示；contributors 显示 unavailable。

不能把 total 按设备额定功率分配。

## Weather unavailable

Energy facts继续显示；weather context unavailable。

不能把天气归一化 metric 冒充 available。

---

# 44. Permission / Capability Gating

示例：

- `energy.read` → main analysis；
- `energy.meter-lineage.read` → meter detail；
- `energy.normalization.read` → normalized metrics；
- `energy.baseline.read` → baseline comparison；
- `energy.export` → export；
- `cost.read` → cost context；
- `mv.read` → M&V deep-link。

无 capability 的 controls 不显示。

---

# 45. Export Contract

Export 必须保留：

```text
Site
Period / timezone
Energy type
Meter scope
Value / unit
Interval resolution
Provenance
Estimated flag / quality
Comparison definition
Baseline version
Normalization method
Export timestamp
```

如果 export 是 aggregated data，明确写 aggregated。

不能把 downsampled chart data 标成 raw meter data。

---

# 46. Saved Analysis Views

可支持保存：

- period preset；
- energy type；
- comparison；
- grouping；
- meter scope；
- normalization；
- visible contributors。

不保存：

- hover；
- cursor；
- temporary chart zoom unless product explicitly支持 durable investigation window。

---

# 47. Visual / UX Contract

## Visual hierarchy

```text
Actual + named comparison
↓
Primary Load Profile
↓
Contributors
↓
Variance Windows / Context
↓
Professional lineage/quality detail
```

## Chart discipline

主图只回答一个问题。

不要：

- 6 个同权重折线图；
- Pie chart 展示 30 个 end-use；
- gauge 显示“Energy Health”；
- flashy gradients；
- animated flow；
- 默认双 Y 轴。

## Difference semantics

增长/下降要同时显示 absolute + percentage（当 denominator有效）。

例如：

```text
+8.2 MWh (+6.4%) vs same period last year
```

Comparison denominator 为 0 / invalid 时，不显示无意义百分比。

---

# 48. ECharts Boundary

ECharts 可以负责：

- load profile；
- comparison overlay；
- variance shading；
- contributor bar chart；
- weather/context small multiples；
- synchronized cursor；
- zoom。

ECharts 不负责：

- unit conversion business truth；
- meter hierarchy；
- baseline calculation；
- normalization method；
- gap filling；
- energy integration；
- allocation；
- savings calculation。

---

# 49. Component Mapping

```text
Context controls              → Select / Popover / Calendar composition
Energy type                   → Select / segmented choice when few
Comparison                    → Select / Popover
Normalization                 → Select + details disclosure
Headline facts                → compact semantic facts, not KPI wall
Primary chart                 → dedicated ECharts feature
Contributor breakdown         → ranked bars + semantic Table
Variance windows              → Table/List
Meter lineage                 → structured Table/Tree-like semantic list when appropriate
Data quality                  → status + details
Professional metadata         → Collapsible / definition list
Export                        → Dropdown Menu
```

避免 universal chart builder / generic dashboard card framework。

---

# 50. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 能源分析 · Phoenix Central Plant                              UTC-07:00      │
│ [Sep 1–14] [Electricity] [Main Site] [vs Same Period LY] [Raw Actual]       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Actual 124.6 MWh     +8.2 MWh (+6.4%)     Peak 812 kW     Coverage 98.7%    │
│ Comparison: 2025-09-01 → 2025-09-14 · same calendar alignment              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Primary Load Profile                                                         │
│ Actual      ───────────────────────────────────────────────────────────────  │
│ Comparison  ───────────────────────────────────────────────────────────────  │
│              [variance window]                                               │
├──────────────────────────────────────────┬───────────────────────────────────┤
│ Ranked Contributors                      │ Variance Windows                  │
│ Chiller Plant   46.2 MWh   Measured      │ Sep 06 13:00–18:00  +2.1 MWh     │
│ AHUs            27.8 MWh   Allocated     │ Sep 09 00:00–05:00  +1.3 MWh     │
│ Lighting        18.4 MWh   Measured      │ Sep 12 14:00–17:00  +0.8 MWh     │
│ Other/Remainder 32.2 MWh                 │                                   │
├──────────────────────────────────────────┴───────────────────────────────────┤
│ Context: hotter weather on Sep 06 · AHU schedule extended on Sep 09          │
│ [需求分析] [效率分析] [趋势] [诊断] [数据质量]                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责和分析层级，不是 pixel specification。

---

# 51. Accessibility

必须：

- chart 有 text/data alternative；
- comparison 不只靠颜色；
- contributor chart 同时有 Table；
- exact values keyboard 可达；
- period/comparison controls 有明确 accessible label；
- measured/estimated/allocated 有文本标签；
- missing/quality 不只靠颜色；
- table 使用 semantic headers；
- chart cursor 不是唯一读取方式；
- around 768px 无 hover-only interaction。

---

# 52. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- period / energy type / comparison；
- actual consumption；
- data coverage；
- primary load profile；
- 主要 contributor；
- 至少一个 variance/context entry。

## 1024–1439 px

- contributor 与 variance 上下排列；
- filters wrap；
- main chart 仍是主要区域。

## Around 768 px

仍必须能：

- 切 period；
- 切 energy type；
- 查看 actual/comparison；
- 阅读 load profile；
- 查看 contributors/coverage；
- 打开 variance / professional exits。

Contributor Table 自身允许 horizontal scroll；页面整体不横向溢出。

---

# 53. No Defensive Programming / No Compatibility Design

明确禁止：

```text
energy API error → 0 kWh
missing interval → 0
missing interval → previous value
comparison unavailable → previous period fallback
baseline unavailable → previous period fallback
weather unavailable → use last weather silently
occupancy unavailable → unoccupied
meter lineage missing → infer from meter name
asset relation missing → infer from equipment name
power samples → frontend integrate authoritative energy
unknown energy type → electricity
unknown unit → kWh
parent + child submeters → sum all
contributors incomplete → renormalize visible rows to 100%
remainder → distribute proportionally in frontend
allocated → measured
estimated → measured
normalized → actual
baseline difference → savings
cost missing → kWh × hardcoded tariff
source-energy factor missing → use default global factor
EUI area missing → divide by 1
one meter → one interval/registry/lineage request N+1
multiple energy APIs → first success wins
old Energy Dashboard compatibility adapter
old chart page fallback
frontend-created baseline
frontend-created normalization
frontend-created allocation
```

不建立：

```text
new Energy Analysis unavailable
→ fallback old dashboard
```

原则：

> **One energy fact → one authoritative measurement/analytics owner. Measured, estimated, allocated and normalized stay distinct. A comparison is not a baseline. A difference is not savings. Missing energy stays missing.**

---

# 54. Browser Acceptance Criteria

## Context / Time

- Site/period/timezone/energy type 始终可见；
- comparison 有明确名称与 period；
- month-to-date 不偷偷与 full previous month 比；
- interval semantics / resolution 可查。

## Actual / Units

- Actual 有 unit/source/provenance；
- real zero 显示 0；
- missing 不显示 0；
- estimated/derived/allocated 有标记；
- Energy 与 Demand 单位不混。

## Comparison / Baseline

- Comparison ≠ Baseline；
- baseline version 可见；
- baseline unavailable 不 fallback；
- normalized metric 不覆盖 raw actual；
- variance 不叫 savings。

## Contributors

- contributor provenance 可见；
- measured / allocated / derived 分开；
- unallocated remainder 可见；
- parent/child meter 不 double-count；
- contributors unavailable 不按额定功率猜。

## Data Quality

- coverage / estimated / missing 可查看；
- late/corrected data 能刷新；
- no generic data-health score；
- gap-filling 只有 owner-defined 才使用。

## Causality

- variance window 不显示 root cause；
- weather/occupancy/operation 只作为 context，除非 diagnosis owner 提供 finding；
- high energy use 不显示 low efficiency；
- no hidden AI cause score。

## Boundaries

- Demand deep-link 到 15；
- Efficiency deep-link 到 16；
- Baseline governance deep-link 到 17；
- M&V deep-link 到 24；
- Data Quality deep-link 到 31。

## Responsive / Accessibility

- 1440–1720 px 是 coherent analytical workspace；
- around 768 px 核心任务完整；
- chart 有 data alternative；
- no color-only comparison/quality；
- no hover-only exact values；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old Energy Dashboard compatibility adapter；
- 无 frontend-generated baseline/normalization/allocation/savings；
- 无 power-to-energy browser integration；
- 无 N+1 meter analytics calls；
- review scenario 无 runtime/network error。

---

# 55. Explicit Non-Goals

本页不是：

- Utility Billing system；
- tariff engine；
- demand-response control page；
- efficiency/COP page；
- ISO 50001 Energy Review administration；
- M&V savings calculator；
- carbon accounting page；
- AFDD/root-cause diagnosis page；
- meter configuration editor；
- generic BI/chart builder。

---

# 56. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Actual / Comparison / Baseline / Normalized 语义已分离；
- Energy / Demand 语义已分离；
- measured / estimated / allocated / derived provenance 明确；
- meter hierarchy / lineage owner contract 明确；
- contributor coverage / remainder / double-counting 规则明确；
- data-quality / missing / corrected data contract 明确；
- normalization/baseline 从 owner 消费，不在本页创建；
- Difference ≠ Savings 已接受；
- Energy / Demand / Efficiency / Review / M&V 职责边界明确；
- old Energy Dashboard / chart pages 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
