# 15 需求、负荷与柔性分析 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `15 需求、负荷与柔性分析`  
> **Route intent：** `/sites/:siteId/demand`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `14-energy-analysis.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Demand Dashboard、旧负荷页、旧削峰页面、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Demand / Meter / Tariff / DR Program / Flexibility / DER / Control / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

需求、负荷与柔性分析的唯一核心任务是：

> **让用户理解“什么时候出现高需求、峰值由谁贡献、这种峰值是否预期、能否在不破坏运行约束的前提下削峰/移峰，以及哪些柔性能力是真实可用、已注册、可调度或已执行的”。**

本 Surface 是 **demand-and-flexibility analytical workspace**，不是：

- Energy Analysis 的 Demand Tab；
- Utility Bill 页面；
- Control Center；
- DER control console；
- DR program administration；
- 电力市场交易平台；
- “看见峰值就自动削峰”的控制页；
- 用一个 AI Flex Score 代替工程事实的 Dashboard。

用户离开本页前应该知道：

1. 当前 demand 的定义、interval、unit 和 measurement boundary；
2. 当前 period 的 peak demand 何时出现；
3. 这个 peak 是 site peak、on-peak demand、billing demand、coincident/system peak 还是其他 owner-defined demand；
4. 负荷曲线是怎样形成的；
5. 哪些 meter / end-use / system 对 peak 贡献最大；
6. 这个 peak 是否落在 schedule / occupied / weather / process context 下；
7. 当前是否存在 demand-charge exposure；
8. 哪些 flexibility capability 真实存在；
9. Available / Dispatchable / Enrolled / Committed / Delivered flexibility 分别是多少；
10. 继续应该进入 Tariff、DER、Control、Strategy、Opportunity 还是 Data Quality。

---

# 2. 主要用户

## Primary

### Energy Manager

理解 peak、load shape、demand cost exposure 与 demand flexibility opportunity。

### Energy / Controls Engineer

分析 HVAC、storage、EV、flexible process 等负荷对峰值的贡献，并评估受约束的可移峰/可削峰能力。

### Facility Manager

确认峰值是否与运营计划一致，以及削峰策略是否会影响 comfort / IAQ / critical operation。

## Secondary

- Utility / tariff analyst：分析 billing demand / ratchet / TOU exposure；
- DER operator：从本页进入 DER / storage / EV flexibility；
- Controls engineer：从分析进入 Control Center / Strategy；
- Sustainability manager：理解 grid-aligned load shifting，但不把其自动等同 emissions reduction；
- Management：阅读 peak 和已验证 flexibility facts。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP Grid-Interactive Efficient Buildings — Demand Flexibility 不是单纯降耗

DOE/FEMP 把 Grid-Interactive Efficient Building 描述为利用 smart technologies 和 onsite DER 提供 demand flexibility，同时协同优化 energy cost、grid services 和 occupant needs/preferences 的建筑。

来源：

- https://www.energy.gov/cmei/femp/grid-interactive-efficient-buildings-federal-agencies
- https://www.energy.gov/cmei/femp/articles/key-grid-interactive-efficient-building-technologies-federal-and-commercial

**本页采用：**

- flexibility 必须与 occupant / operational constraints 一起表达；
- 可用柔性不是“所有可关负荷的总和”；
- demand flexibility 是 capability / service，不自动等于 savings；
- onsite storage / PV / EV 可以参与 flexibility，但必须保留 source semantics。

## 3.2 DOE GEB Framework — Efficiency / Shed / Shift / Modulate 分开

DOE GEB 技术资料将建筑 demand flexibility 典型模式区分为：

```text
Efficiency
Load Shed
Load Shift
Modulate
```

来源：

- https://www.energy.gov/cmei/articles/does-national-roadmap-grid-interactive-efficient-buildings
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/grid-interactive-efficient-buildings-report.pdf

**本页采用：**

- `Shed` 与 `Shift` 不合并；
- `Efficiency` 是持续减少能耗，不等于事件型 flexibility；
- `Modulate` 是更快时间尺度的动态响应，不应被普通 load-shift UI 冒充；
- 每种 capability 需要独立 duration / notice / recovery / comfort / control constraints。

## 3.3 FERC — Demand Response 有正式定义

FERC 定义 Demand Response 为：用户相对于正常消费模式，因电价变化或激励支付，在高市场价格或系统可靠性受威胁时改变电力使用。

来源：

- https://ferc.gov/power-sales-and-markets/demand-response/reports-demand-response-and-advanced-metering
- https://ferc.gov/electric/industry-activity/demand-response/national-assessment-and-action-plan-demand-response

**本页采用：**

- DR 必须有 baseline/normal-consumption context、program/event/signal；
- `Demand Reduction` 不一定就是 enrolled DR performance；
- 自主 site peak shaving 与 utility/ISO DR 分开；
- event participation / settlement 属于 program owner，不由前端猜。

## 3.4 FERC Demand / Peak Load Definitions — Demand 是功率，不是能量

FERC Glossary 将 Demand 定义为特定时刻/期间的电力消费功率，通常以 kW/MW 表达；Peak Demand 是某时刻或某平均窗口的最大 power requirement。

来源：

- https://www.ferc.gov/glossary-0

**本页采用：**

```text
kW / MW = demand / power
kWh / MWh = energy
```

- interval demand 的 averaging/window semantics 必须明确；
- peak 必须带 interval/window/timezone；
- 不能从 Energy Total 直接推算 billing peak。

## 3.5 DOE/FEMP EMIS — Load Profile 支持 Peak Management

DOE/FEMP EMIS 资料明确指出 daily load profiles 可用于降低 peak demand 和 demand charges，并区分 utility-initiated 与 building-initiated demand reduction。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- load profile / heatmap / peak windows 是核心分析工具；
- `utility-initiated` 与 `building-initiated` demand management 分开；
- 分析与控制执行分离。

## 3.6 DOE/FEMP Utility Rate Guidance — Billing Demand 有多种计算方式

FEMP utility-rate guidance 明确区分：

- non-coincident demand charge；
- on-peak demand charge；
- demand ratchet/look-back；
- system-peak / coincident demand charge。

来源：

- https://www.energy.gov/cmei/femp/evaluating-your-utility-rate-options

**本页采用：**

- `Site Peak` 不能默认叫 `Billing Demand`；
- billing exposure 只有真实 tariff/rate owner 存在时才显示；
- ratchet / coincident peak 必须保留正式 tariff semantics。

## 3.7 DOE/FEMP DR / Time-Variable Pricing — DR / Price Signal 是不同 program mechanics

FEMP 说明 DR 通常是短期、voluntary 的用电降低，常由 grid reliability 或 high wholesale prices 触发；Time-variable pricing 则包括 TOU、real-time pricing、day-ahead pricing 等不同机制。

来源：

- https://www.energy.gov/cmei/femp/demand-response-and-time-variable-pricing-programs

**本页采用：**

- DR event、price signal、TOU tariff 是不同对象；
- price signal 不等于 mandatory dispatch；
- event notification 不等于执行成功；
- strategy response / delivered reduction 需由 Control / DR program owner确认。

## 3.8 NREL GEB — Demand Flexibility 是跨时间尺度调整 Load Profile 的能力

NREL GEB 技术报告把 demand flexibility 描述为 DER/loads 在不同时间尺度调整 building load profile 的能力；HVAC 等 space conditioning 对 peak 和 occupant comfort 都高度相关。

来源：

- https://www.nrel.gov/docs/fy20osti/75470.pdf
- https://www.nrel.gov/docs/fy20osti/75478.pdf

**本页采用：**

- flexibility 需要 time scale；
- HVAC flexibility 必须带 comfort / IAQ / operational guardrails；
- `available kW` 必须绑定 duration / notice / recovery / rebound；
- flexibility 不只是一个瞬时数字。

## 3.9 OpenADR — DR Event / Signal 与 Facility Control 分离

OpenADR 标准化 utility/ISO/aggregator 与 energy-management/control systems 之间的 DR / price / reliability signal exchange，但 program signal 具体含义和参与规则依 program 而定。

来源：

- https://www.openadr.org/about-us
- https://www.openadr.org/dr-program-guide
- https://www.openadr.org/openadr-3-0

**本页采用：**

- DR Event 是 program/signal object；
- signal receipt ≠ accepted participation；
- accepted participation ≠ control execution；
- control execution ≠ delivered demand response；
- settlement/performance result由 DR/market owner。

---

# 4. Domain Vocabulary

## 4.1 Demand / Load

某个 interval/window 下的 power requirement，例如：

```text
812 kW average over 15 min
```

必须有：

- value；
- unit；
- interval/window；
- timestamp；
- meter boundary；
- source/provenance。

## 4.2 Site Peak Demand

选定 period 内 site demand series 的最大值。

不是自动的 Billing Demand。

## 4.3 Billing Demand

由 tariff/rate owner 根据正式规则计算的 billable demand。

可能包含：

- on-peak window；
- demand ratchet；
- coincident/system peak；
- minimum billing demand；
- other tariff rules。

## 4.4 Load Shape

Demand 随时间的 profile。

## 4.5 Load Duration Curve

把 demand observations 按大小排序后展示“多少时间处于某需求水平以上/附近”。

它丢失 clock-time 顺序，因此不能替代 timeline/load profile。

## 4.6 Peak Contributor

在明确 peak interval 和 measurement boundary 下，对 peak demand 的贡献。

Contributor 必须来自：

- synchronized submeters；
- owner-defined allocation；
- authoritative demand decomposition。

## 4.7 Load Shed

在一段时间内降低需求，并通常存在 recovery / rebound。

## 4.8 Load Shift

把部分 energy consumption 从一个时间窗口转移到另一个窗口。

`Shift` 不意味着 total energy 必然减少。

## 4.9 Peak Shaving

以降低 local/site peak 或 billing exposure 为目标的 demand management。

它可以通过：

- load shed；
- load shift；
- storage discharge；
- onsite generation；
- mixed strategy。

这些 source 必须分开。

## 4.10 Demand Response Event

由 utility/ISO/RTO/aggregator/program owner 发布的正式 event/signal。

## 4.11 Flexibility Capability

某 asset/system 在特定 constraints 下可调整 net demand 的能力。

它不是一个永久固定的设备属性。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
Energy = Demand
Site Peak = Billing Demand
Site Peak = Grid Coincident Peak
Peak Demand = Demand Charge
Load Shed = Load Shift
Load Shift = Energy Savings
Peak Shaving = Demand Response
Available Flexibility = Dispatchable Flexibility
Available Flexibility = Enrolled Capacity
Enrolled Capacity = Committed Capacity
Committed Capacity = Delivered Response
DR Event = Control Command
DR Event Received = Participated
Participated = Delivered Reduction
Price Signal = Mandatory Dispatch
Battery Discharge = Building Load Reduction
PV Generation = Load Shed
Comfort Headroom = Flexibility Capacity
No alarm = Safe to shed
Strategy simulated = Strategy deployable
```

正确关系：

```text
Demand Facts
↓
Peak / Load Shape Analysis
↓
Contributor / Cost / Context
↓
Flexibility Capability
↓
Program / Strategy / Control Eligibility
↓
Dispatch / Execution
↓
Delivered Result / Verification
```

---

# 6. Primary Questions

## Q1 — 峰值何时出现？

显示：

- peak value；
- interval/window；
- timestamp；
- period；
- meter boundary；
- site timezone；
- comparison peak。

## Q2 — 这个峰值是什么性质？

区分：

```text
Site Period Peak
On-Peak Tariff Peak
Billing Demand
Coincident / System Peak Exposure
DR Event Peak
```

只有对应 owner capability 存在时显示。

## Q3 — 谁贡献了峰值？

显示 synchronized peak contributors、coverage 和 remainder。

不是 period energy contributors。

## Q4 — Peak 是预期的吗？

关联：

- schedule；
- occupancy；
- weather；
- operating mode；
- process/load event；
- DER state；
- known shutdown/startup；
- strategy/event context。

## Q5 — 能不能削峰/移峰？

只有真实 Flexibility owner 存在时显示 capability：

- shed；
- shift；
- storage；
- EV charge management；
- thermal storage；
- load modulation。

## Q6 — 柔性能持续多久、需要多久准备？

每个 capability 必须有：

- kW range；
- duration；
- notice/lead time；
- recovery/rebound；
- availability window；
- constraints；
- confidence/provenance。

## Q7 — 当前是否有 DR / price / tariff context？

显示 authoritative event / program / price / tariff information，不把它直接变成控制命令。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/demand
```

推荐 Search Params：

```text
from
until
periodPreset
meterScope
interval
comparison
compareFrom
compareUntil
peakType
selectedPeak
contributorGroup
view            // profile | duration | heatmap | flexibility
flexMode        // all | shed | shift | storage | modulation
program
selectedEvent
selectedResource
```

共享 context：

```text
site
period
comparison
baselineVersion
```

不进入 URL：

- chart cursor；
- hover；
- temporary selection rectangle；
- disclosure state；
- unsaved scenario draft。

---

# 8. Entry Contract

## 从 14 Energy Analysis

携带：

- Site；
- period；
- electricity/meter scope；
- comparison；
- selected variance window（若有）。

## 从 Bills / Cost / Tariff

携带：

- billing period；
- tariff/rate；
- demand component；
- billing demand / ratchet context；
- source account。

## 从 Site Overview / Operations

携带：

- Site；
- time window；
- selected system；
- source trail。

## 从 DER / Flexibility

携带：

- selected DER/resource；
- capability；
- availability window；
- event/program context。

---

# 9. Exit Contract

主要出口：

```text
Demand Analysis
→ 14 Energy Analysis
→ 16 Efficiency
→ 18 Bills / Cost / Tariff
→ 20 DER & Flexibility
→ 21 Savings Opportunity
→ Control Center
→ Strategy Center
→ Trend Analysis
→ Data Quality
```

如果 DR capability 存在：

```text
Demand Analysis
→ DR Program/Event Detail owner
→ Strategy / Control execution
→ Verification
```

保持 period / selected peak / resource / event context。

---

# 10. Responsibility Boundary

本 Surface 拥有：

- demand analysis context；
- peak identification/projection；
- load profile；
- heatmap / load duration curve；
- peak contributor breakdown；
- billing-demand context projection；
- schedule/weather/operation context；
- flexibility capability projection；
- DR event/program context projection；
- delivered-response evidence links。

本 Surface 不拥有：

- tariff/rate calculation engine；
- utility billing settlement；
- DR enrollment administration；
- DR settlement/M&V；
- DER dispatch；
- HVAC control writes；
- strategy authoring；
- comfort guardrail definition；
- flexibility model generation；
- frontend-created dispatch capacity。

---

# 11. Information Architecture

```text
Context Header
  Site · Period · Timezone · Electricity Scope · Interval

Demand Summary
  Site Peak
  Peak Timestamp
  Billing Demand Context (capability-gated)
  Comparison
  Data Coverage

Primary Load Workspace
  Load Profile
  Peak Windows
  Schedule / Weather / Event Context

Peer Analytical Views
  Profile | Load Duration | Heatmap | Flexibility

Peak Contributors
  demand at selected peak interval
  measured/allocated/derived provenance
  remainder/coverage

Flexibility Workspace (capability-gated)
  Available resources
  Shed / Shift / Storage / Modulate
  kW · duration · notice · recovery · constraints
  Enrolled / committed / available / delivered semantics

DR / Tariff Context (capability-gated)
  Event / price / tariff windows
  program status
  delivered-response link

Professional Detail
  demand calculation
  meter lineage
  tariff method
  flexibility model/revision
  data quality
```

不是 KPI Card Wall。

---

# 12. Demand Definition Contract

页面必须明确 demand 的计算语义：

```text
Average kW over 15-minute interval
Rolling 15-minute kW
30-minute block demand
Instantaneous power (if explicitly supported)
```

不能只显示：

```text
Demand: 812 kW
```

而隐藏 interval semantics。

## Interval ownership

来自 meter/tariff/demand analytics owner。

Frontend 不自行把 telemetry 重采样后创造 billing demand。

---

# 13. Peak Demand Contract

Peak 至少有：

```text
Value
Unit
Timestamp
Interval/window
Measurement scope
Peak type
Source
Quality/coverage
```

Peak type 例如：

```text
Site period peak
On-peak period peak
Billing demand
Coincident peak exposure
DR event peak
```

不同 peak types 不能复用同一个 badge `Peak` 而不说明含义。

---

# 14. Site Peak vs Billing Demand

## Site Peak

当前 analysis scope 的最大 demand observation。

## Billing Demand

由 tariff owner 根据正式 rate rule 得出的 billable demand。

可能出现：

```text
Site peak: 812 kW
Billing demand: 905 kW
```

如果 ratchet/look-back 生效，这是合理的。

也可能：

```text
Site peak: 812 kW
On-peak billing demand: 731 kW
```

Frontend 不自行解释收费规则。

---

# 15. Coincident / System Peak Contract

如果 utility/grid owner 提供 coincident peak exposure：

显示：

```text
Program / tariff
Candidate/system peak window
Site demand during window
Contribution basis
Status / settlement state
```

禁止：

```text
site max timestamp = utility system peak
```

System peak 是外部 grid fact。

---

# 16. Load Profile Contract

Primary load profile 显示 demand vs time。

支持：

- Actual；
- 一个 named comparison；
- tariff/event windows；
- selected peak marker；
- schedule/occupancy context；
- weather/context small multiples。

规则继承 05：

- missing → gap；
- stale/estimated explicit；
- no default dual-Y-axis；
- exact values available；
- timezone visible。

---

# 17. Heatmap Contract

Heatmap 用于长时间段识别：

- recurring peak hours；
- overnight/base load；
- weekday/weekend pattern；
- seasonal pattern；
- event clustering。

Heatmap 必须：

- 有可读 legend/unit；
- 不只靠颜色；
- 允许 exact values/table alternative；
- missing 与 low load 视觉不同。

Heatmap 不是 floor heatmap。

---

# 18. Load Duration Curve Contract

LDC 将 demand 按 magnitude 排序。

回答：

> 站点有多少时间处在高需求区间？

显示：

- demand；
- exceedance duration/percentage；
- threshold；
- optional comparison。

不回答：

> 峰值具体发生在几点？

因此必须提供回到 chronological profile 的 deep-link/selection。

---

# 19. Peak Contributor Contract

Peak contributor 是**在 selected peak interval 同时发生的 demand contribution**。

不是：

```text
monthly energy share
```

每个 contributor 显示：

```text
Name
Demand kW
Share of covered peak
Provenance
Meter alignment
Coverage
```

## Synchronization

不同 meter interval 必须由 owner 对齐。

Frontend 不做：

```text
nearest timestamp join
→ call it peak attribution
```

除非 analytics owner 明确采用该方法并返回 provenance。

---

# 20. Contributor Coverage / Remainder

如果 parent peak = 812 kW，已知 contributors 总计 640 kW：

必须显示：

```text
Covered: 640 kW
Remainder / Unmetered: 172 kW
Coverage: 78.8%
```

禁止将已知 contributors 重新归一化为 100%。

---

# 21. Scheduled vs Unexpected Peak

页面可以在 authoritative schedule/occupancy/process context 存在时分类：

```text
Scheduled / expected context
Unexpected / outside schedule
Unknown
```

`Unexpected` 的含义必须是：

> 与 owner-defined operating schedule/expected load context 不一致

不是：

> root cause 已知。

如果 schedule unavailable：

> `Peak context unknown`

不能自动当 unexpected。

---

# 22. Demand Charge Context

只有真实 tariff capability 存在时显示。

可以展示：

```text
Current billing-demand basis
On-peak window
Current billing demand
Ratchet reference
Demand charge rate/context
Projected exposure (if tariff owner supports)
```

Cost calculation来自 Surface 18 / tariff owner。

本页不做：

```text
peak kW × generic $/kW
```

---

# 23. Flexibility Capability Model

Flexibility 必须是 owner-defined capability，不能由前端根据设备状态猜。

每个 resource/capability 至少有：

```text
Resource
Mode: Shed / Shift / Storage / Modulate
Up/Down direction
Available kW range
Duration
Notice / lead time
Availability window
Recovery / rebound
Energy budget if relevant
Comfort / IAQ constraints
Operational constraints
Control authority
Confidence / method
Revision / timestamp
```

---

# 24. Available / Dispatchable / Enrolled / Committed / Delivered

必须独立：

## Available Flexibility

当前条件下理论/工程可用能力。

## Dispatchable Flexibility

当前 control/authorization/interlock 条件下可以实际发起的能力。

## Enrolled Capacity

在某 DR/program 中正式注册的容量。

## Committed Capacity

针对某 event/program 已接受/承诺的容量。

## Delivered Response

事件期间实际交付并由 owner 计算/验证的 response。

不能显示成单一：

```text
Flexibility = 500 kW
```

---

# 25. Flexibility Availability Is Time-Varying

柔性随以下因素变化：

- HVAC load；
- thermal state；
- occupancy；
- weather；
- storage SOC；
- EV connection/departure；
- process schedule；
- maintenance/outage；
- comfort/IAQ guardrails；
- control authority；
- recent dispatch/recovery state。

所以：

```text
AHU-03 flexible capacity = 80 kW
```

必须带 timestamp / window / method。

不能作为永久资产属性展示。

---

# 26. Shed Contract

Shed 需要至少表达：

```text
Expected demand reduction
Maximum duration
Minimum notice
Recovery profile
Rebound risk
Constraints
```

Shed 可能降低 event-window energy，但不自动等于长期 energy savings。

---

# 27. Shift Contract

Shift 必须同时表达：

```text
From window
To window
Shiftable energy
Demand impact
Recovery / precharge
Net-energy effect if known
```

例如 thermal pre-cooling：

```text
pre-cool earlier
↓
reduce afternoon cooling demand
```

总 energy 可能：

- decrease；
- stay similar；
- increase。

因此：

```text
Load Shift ≠ Energy Savings
```

---

# 28. Storage Flexibility Contract

Battery / thermal storage 应区分：

```text
Building native load
Storage charge
Storage discharge
Grid import/export
Net site demand
```

Battery discharge 导致 net demand 下降，不等于 building end-use load 下降。

必须保留：

- SOC；
- available power；
- available energy；
- efficiency；
- reserve constraint；
- max duration；
- charge/recovery needs。

完整 DER operation 在 Surface 20。

---

# 29. DER / PV Boundary

PV generation 可以降低 net grid demand，但：

```text
PV generation ≠ Load Shed
```

如果本页显示：

```text
Gross Building Load
PV Generation
Storage
Net Grid Demand
```

必须由 meter/DER owner提供真实 decomposition。

不能通过 `main meter - PV` 猜 gross load，除非这就是 authoritative model。

---

# 30. HVAC Flexibility Guardrails

HVAC flexibility 必须尊重：

- occupied thermal comfort；
- IAQ / ventilation；
- humidity；
- equipment limits；
- minimum ventilation；
- freeze/condensation protection；
- process/critical-zone requirements；
- sequence/interlock；
- maintenance constraints。

来自：

- Comfort/IEQ owner；
- Operations owner；
- Control/Strategy owner；
- Safety/asset constraint owner。

禁止：

```text
zone temperature inside band
→ all HVAC load is flexible
```

---

# 31. Rebound / Recovery Contract

柔性事件结束后的 rebound 是一等事实。

显示：

```text
Expected recovery profile
Observed rebound
Recovery duration
Post-event peak
Net energy shift
```

如果策略削峰 200 kW，但 30 分钟后造成 260 kW rebound，不能只展示 event-window success。

---

# 32. DR Program Contract

如果 Site 注册了 Demand Response program，页面可以显示：

```text
Program
Enrollment state
Committed capacity
Notification requirements
Event types
Measurement/settlement owner
Control pathway
```

Program rules来自 DR/utility owner。

前端不根据 OpenADR signal 自己解释 financial settlement。

---

# 33. DR Event Lifecycle

建议保持：

```text
Event Announced
↓
Received
↓
Eligibility / Site Decision
↓
Accepted / Declined (if program supports)
↓
Strategy Armed
↓
Dispatch / Event Active
↓
Execution
↓
Event End
↓
Recovery
↓
Delivered Response / Program Verification
```

每个阶段 owner 不一定相同。

强制：

```text
Event received ≠ accepted
Accepted ≠ executed
Executed ≠ delivered target
Delivered ≠ settled payment
```

---

# 34. Price Signal Contract

Price signal 可以来自：

- TOU；
- real-time price；
- day-ahead price；
- critical-peak pricing；
- owner-defined tariff signal。

必须显示：

- source；
- effective window；
- unit；
- tariff/program；
- published/revised timestamp。

Price signal 是 decision input，不自动触发 control。

---

# 35. Strategy / Control Boundary

本页可以：

- 分析可削峰资源；
- 显示 active strategy；
- 显示 current control authority；
- 显示 last dispatch summary；
- 进入 Strategy/Control。

默认不直接执行：

- Start/Stop；
- Setpoint Write；
- Battery dispatch；
- DR strategy publish；
- Override。

高风险 control 进入 Control Center / Strategy Detail。

---

# 36. Flexibility Scenario Boundary

如果未来支持 scenario analysis，可比较：

```text
No action
Shed 150 kW for 2 h
Shift 300 kWh to 10:00–12:00
Battery discharge 200 kW
Mixed strategy
```

但 scenario 必须来自 simulation/optimization owner。

Frontend 不做：

```text
sum all listed flexible kW
→ call guaranteed dispatch capacity
```

---

# 37. Delivered Response / Performance

Delivered demand response 需要正式 baseline/performance method。

可能包含：

- event baseline；
- adjustment；
- actual demand；
- delivered reduction；
- event interval；
- settlement methodology；
- quality/exception。

由 DR/M&V/program owner。

本页只消费结果。

不能简单：

```text
pre-event demand 900 kW
current 700 kW
→ delivered response = 200 kW
```

除非 program owner 的正式方法就是如此并提供结果。

---

# 38. Flexibility vs M&V

Demand flexibility performance 与 energy-project M&V 分开。

DR event verification 可能回答：

> 事件期间减少了多少 kW？

Surface 24 M&V 回答：

> 节能措施在 reporting period 节省了多少 energy/cost？

不要合并。

---

# 39. Data Quality Contract

Demand analysis 至少区分：

```text
Good
Estimated
Missing
Late
Corrected
Suspect
Unknown
```

Peak 对数据质量特别敏感。

如果 peak interval 缺失：

> `Peak may be understated — interval coverage incomplete`

不能仍然宣称：

> `True monthly peak = 812 kW`

除非 owner明确如此。

---

# 40. Peak Completeness Contract

显示：

```text
Peak confidence / completeness status
Period coverage
Missing intervals
Late intervals
Meter correction status
```

不是黑盒 score。

例如：

```text
Peak observed: 812 kW
Coverage: 96.4%
Peak completeness: Incomplete period
```

---

# 41. Time / Interval Alignment

必须明确：

- site timezone；
- DST；
- interval start/end semantics；
- rolling/block demand；
- utility billing interval；
- meter clock alignment；
- event window alignment。

不同 meters 的 contributor analysis 必须由 analytics owner对齐。

---

# 42. Comparison Contract

可以比较：

```text
Current vs previous comparable period
Current vs same weekdays
Current vs same period last year
Peak day vs typical day
DR event vs comparable non-event day (analytics context only)
```

Comparison 不自动成为 DR baseline。

DR baseline必须来自 program owner。

---

# 43. Data Authority Contract

## Demand / meter facts

Owner：Meter / Historian / Demand Analytics。

## Billing demand / tariff windows

Owner：Tariff / Billing domain。

## Schedule / occupancy

Owner：Operations / Occupancy domain。

## Weather

Owner：Weather context provider。

## Peak contributor

Owner：Demand analytics / meter decomposition。

## Flexibility capability

Owner：Flexibility / DER / Strategy analytics domain。

## Comfort / IAQ constraints

Owner：Comfort/IEQ domain。

## DR Program/Event

Owner：DR / Utility / Program integration。

## Control execution

Owner：Control domain。

## Delivered response / settlement

Owner：DR Performance / M&V / Utility integration。

Frontend 只做 demand-centered projection。

---

# 44. Query / Read Model Contract

推荐：

```text
Demand Analysis Projection
  + demand summary
  + selected peak
  + chronological profile
  + heatmap/load-duration aggregates
  + peak contributors
  + tariff/billing context
  + flexibility summary
  + DR event context
  + data coverage
```

Flexibility detail 可单独 query。

禁止：

```text
100 assets
→ 100 telemetry requests
→ 100 flexibility requests
→ 100 comfort requests
→ 100 control authority requests
```

缺 projection 时修 domain contract，不在前端 fan-out。

---

# 45. Realtime / Refresh Contract

Demand 页面主要是 query-driven analytics。

Current-day / active DR event 可以使用 Snapshot + Stream/event invalidation。

要求：

- stream disconnect ≠ demand 0；
- stream disconnect ≠ event ended；
- new peak 不抢 focus；
- 不自动重排 selected contributors；
- active event state authoritative；
- reconnect refetch authoritative snapshot。

---

# 46. Loading / Empty / Partial / Error

## No demand data

> `该时间范围内没有需求数据。`

不是 0 kW。

## Meter unavailable

> `需求数据暂不可用。`

## Tariff unavailable

Demand analytics仍可用；billing-demand/demand-charge context unavailable。

不能把 site peak 当 billing demand。

## Flexibility unavailable

Demand analytics仍可用；flexibility workspace unavailable。

不能把设备额定功率当 flexibility。

## DR integration unavailable

Demand/flexibility facts仍显示；event/program unavailable。

不能显示 `No Active Event`。

## Comfort owner unavailable

HVAC flexibility的 affected constraints unknown；不能显示 `Comfort Safe`。

---

# 47. Permission / Capability Gating

示例：

- `demand.read` → main analysis；
- `tariff.read` → billing-demand context；
- `flexibility.read` → flexibility workspace；
- `dr.read` → program/event；
- `der.read` → DER contribution；
- `control.read` → authority/strategy context；
- `control.execute` → 仅 Control Center action；
- `demand.export` → export。

无 capability 不显示对应 surface controls。

---

# 48. Export Contract

Export 保存：

```text
Site
Period / timezone
Demand definition
Interval/window
Meter scope
Peak type
Demand values
Quality/estimated flags
Selected peak
Contributor provenance
Tariff context/version
Flexibility model/revision
DR event/program context
Export timestamp
```

不能把 chart downsample data 标成 raw billing interval data。

---

# 49. Visual / UX Contract

默认视觉层级：

```text
Peak & demand context
↓
Load Profile
↓
Peak Contributors
↓
Load Duration / Heatmap
↓
Flexibility
↓
Tariff / DR Context
```

禁止：

- 一个“Demand Health Score”；
- 一个“Flexibility 82/100”；
- 20 个资源卡片墙；
- 全页面红色突出 peak；
- 把 price / demand / SOC / temperature 全塞双 Y 轴；
- 用动画水流/电流表现负荷。

---

# 50. Chart Contract

## Load Profile

Line/area；time x-axis。

## Heatmap

Long-period day/hour pattern。

## Load Duration Curve

Sorted demand vs exceedance duration。

## Peak Contributor

Ranked horizontal bars + Table。

## Flexibility

适合使用：

- availability timeline；
- capacity band；
- duration/lead-time table；
- resource matrix。

不要默认用 gauge。

---

# 51. ECharts Boundary

ECharts 可以负责：

- demand profile；
- heatmap；
- load duration curve；
- contributor bars；
- availability bands；
- event windows；
- synchronized cursor；
- zoom。

ECharts 不负责：

- billing demand calculation；
- tariff logic；
- flexibility capacity calculation；
- DR baseline；
- comfort constraint；
- control dispatch；
- delivered-response verification。

---

# 52. Component Mapping

```text
Context controls              → Select / Popover / Calendar
Demand definition             → compact semantic metadata
Peak summary                  → compact facts
Main profile                  → dedicated ECharts
View switch                   → peer-view Tabs only for Profile/Duration/Heatmap/Flexibility
Peak contributors             → ranked bars + Table
Flexibility resources         → semantic Table / Matrix
Tariff/DR event               → structured status sections
Professional metadata         → Collapsible / definition list
Export                        → Dropdown Menu
```

避免 generic dashboard framework。

---

# 53. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 需求、负荷与柔性 · Phoenix Central Plant                     UTC-07:00      │
│ [Sep 1–14] [Main Electric] [15-min demand] [vs Same Period LY]              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Site Peak 812 kW · Sep 06 15:15   Billing Demand 905 kW*   Coverage 99.2% │
│ *Ratchet tariff owner · not equal site peak                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ [Load Profile] [Duration] [Heatmap] [Flexibility]                           │
│                                                                              │
│ 900 kW                  ▲ 812 kW                                             │
│       ────────────╮    ╭─╯    ╭───────                                      │
│                   ╰────╯      ╰──────                                        │
│     DR event window        On-peak tariff                                    │
├──────────────────────────────────────────┬───────────────────────────────────┤
│ Peak Contributors                        │ Peak Context                      │
│ Chiller Plant       310 kW Measured      │ Occupied · 41°C OAT              │
│ AHUs                186 kW Allocated     │ Chillers 3/3                     │
│ EV Charging          92 kW Measured      │ EV scheduled charging            │
│ Other/Remainder     224 kW               │                                  │
├──────────────────────────────────────────┴───────────────────────────────────┤
│ Flexibility Available Now                                                   │
│ HVAC Shed   95 kW · 60 min · 15 min notice · comfort constrained           │
│ Battery    180 kW · 90 min · SOC 68% · reserve constraint                  │
│ EV Shift    70 kW · until 18:00 · departure constraints                    │
│                                            [DER] [Strategy] [Control]        │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 表达职责和分析关系，不是 pixel specification。

---

# 54. Accessibility

必须：

- demand/peak 不只靠图形；
- load profile 有 data/table alternative；
- heatmap 有非颜色替代；
- exact peak values keyboard可达；
- flexibility mode/state 有文本；
- DR/tariff windows 不只靠背景色；
- tables 有 semantic headers；
- no hover-only interaction；
- charts 有 accessible summary；
- live event update 不抢 focus。

---

# 55. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- demand definition；
- site peak；
- billing context（若有）；
- load profile；
- selected peak contributors；
- flexibility summary（若 capability存在）。

## 1024–1439 px

- contributors / context 上下布局；
- flexibility table 下沉；
- profile 保持主要视觉区域。

## Around 768 px

仍必须能：

- 切 period；
- 看 peak/time；
- 看 profile；
- 看 contributor；
- 看 tariff/DR context；
- 看 flexibility kW/duration/constraints；
- 进入 DER/Strategy/Control。

无 page-level horizontal overflow；table 自身可 scroll。

---

# 56. No Defensive Programming / No Compatibility Design

明确禁止：

```text
demand API error → 0 kW
missing interval → 0
missing peak interval → declare true peak anyway
site peak → billing demand
site peak → coincident grid peak
billing demand unavailable → use site peak
rate unavailable → kW × hardcoded demand rate
peak contributor unavailable → use equipment rated power
meter alignment missing → nearest timestamp join in frontend
contributors incomplete → normalize visible to 100%
schedule unavailable → unexpected peak
occupancy unavailable → unoccupied
comfort unavailable → safe to shed
no alarm → safe to shed
asset online → dispatchable
available flexibility → enrolled DR capacity
available flexibility → guaranteed dispatch
accepted DR event → executed
executed strategy → delivered response
pre-event minus event demand → authoritative DR performance
battery discharge → building load reduction
PV generation → load shed
load shift → energy savings
strategy simulation → deployable strategy
price signal → automatic control command
DR event received → participated
stream disconnect → event ended
multiple demand APIs → first success wins
one resource → one telemetry/comfort/control/flexibility request N+1
old Demand Dashboard adapter
old peak-shaving page fallback
frontend-generated flexibility capacity
frontend-generated billing demand
frontend-generated DR baseline
```

不建立：

```text
new Demand Analysis unavailable
→ fallback old demand dashboard
```

原则：

> **One demand fact → one authoritative meter/analytics owner. Peak type is explicit. Flexibility is constrained and time-varying. Available is not dispatchable. Dispatch is not delivery. Load shift is not savings. Unknown stays unknown.**

---

# 57. Browser Acceptance Criteria

## Demand semantics

- Demand unit/window 可见；
- Energy 与 Demand 不混；
- real zero显示0；
- missing不显示0；
- interval/block/rolling semantics 可查。

## Peak

- peak type 可见；
- site peak 与 billing demand 不混；
- billing demand只有 tariff owner 时出现；
- coincident peak来自 grid/tariff owner；
- coverage incomplete 时不夸大 peak certainty。

## Contributors

- contributor 是 peak-interval demand contribution；
- provenance visible；
- remainder visible；
- no parent/child double counting；
- unavailable 不按额定功率猜。

## Flexibility

- mode / kW / duration / notice / recovery / constraints 可见；
- availability timestamp/window 可见；
- Available / Dispatchable / Enrolled / Committed / Delivered 分开；
- comfort/IAQ/operational constraints 可查；
- no black-box Flex Score。

## DR / Price

- event/program/source 可见；
- received / accepted / executed / delivered 分开；
- price signal 不自动控制；
- DR baseline/performance来自 program owner。

## Boundaries

- tariff/cost deep-link → 18；
- DER deep-link → 20；
- control writes → Control Center；
- strategy authoring → Strategy Center；
- energy total → 14；
- efficiency → 16。

## Partial/Error

- tariff unavailable 不冒充 billing demand；
- flexibility unavailable 不冒充 zero flexibility；
- DR integration unavailable 不显示 `No Active Event`；
- comfort unavailable 不显示 safe-to-shed；
- Unknown 保持 Unknown。

## Responsive / Accessibility

- 1440–1720px 是 coherent demand workspace；
- around 768px 核心任务完整；
- no color-only heatmap/event semantics；
- no hover-only exact values；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old Demand Dashboard compatibility adapter；
- 无 frontend-generated billing demand/flexibility/DR baseline；
- 无 N+1 flexibility/resource calls；
- 无自动 control shortcut；
- review scenario 无 runtime/network error。

---

# 58. Explicit Non-Goals

本页不是：

- Utility Billing engine；
- tariff authoring tool；
- DR enrollment/settlement system；
- DER dispatch console；
- HVAC direct-control surface；
- strategy editor；
- M&V savings page；
- energy-total dashboard；
- generic grid-market trading system；
- automatic demand-control agent UI。

---

# 59. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Energy / Demand 已分离；
- Site Peak / Billing Demand / Coincident Peak 已分离；
- load profile / heatmap / duration curve 职责明确；
- peak contributor 是 interval demand contribution，不是 energy share；
- demand charge context 只消费 tariff owner；
- Shed / Shift / Storage / Modulate 语义已分离；
- Available / Dispatchable / Enrolled / Committed / Delivered 已分离；
- comfort/IAQ/operational constraints 明确；
- DR event lifecycle 与 control execution 分离；
- Demand / DER / Control / Tariff / M&V 边界明确；
- old Demand Dashboard / peak-shaving pages 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
