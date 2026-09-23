# 20 分布式能源与柔性 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `20 分布式能源与柔性（Capability-gated）`  
> **Route intent：** `/sites/:siteId/der`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`PV`、`BESS`、`EVSE`、`SOC`、`DR`、`V2G` 等行业缩写保留，但必须配合中文业务语义。  
> **设计输入声明：** 本文件不参考当前项目已有 DER Dashboard、微电网页面、旧电气单线图、旧储能/光伏页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 DER Registry / Meter / Grid Connection / Control / Strategy / Forecast / DR / Tariff / Resilience / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **让用户在明确电网连接状态、资源能力、运行约束、能量/功率边界和调度权限的前提下，理解站点现场发电、储能、充电和其他分布式能源资源如何共同影响净负荷、韧性、成本与可用柔性，并把需要执行的动作交给正式 Control / Strategy workflow。**

本 Surface 是 **DER operations + resource flexibility workspace**，不是：

- 一张纯电气单线图；
- 电池 `SOC` 大屏；
- 光伏发电排行榜；
- 直接控制全部 DER 的万能控制台；
- Demand Response 事件收件箱；
- 15「需求、负荷与柔性」的重复页面；
- 微电网保护设置工具；
- 电力市场交易终端；
- 用额定容量推测“可调度能力”的页面。

用户离开本页前应该知道：

1. 当前站点是 Grid-connected、Island-capable 还是 Islanded；
2. 当前 Gross Load、On-site Generation、Grid Import/Export 分别是多少；
3. 有哪些 DER 资源正在运行、受限、不可用或需要关注；
4. BESS 当前 SOC、可用能量、充/放电功率上限和 Reserve 是什么；
5. PV 实际发电、可用发电、限发/削减和预测分别是什么；
6. EVSE/车辆当前连接、充电需求、功率和 departure/mission constraints 是什么；
7. Generator / CHP / Thermal Storage / Heat Pump 等资源是否可用，以及其运行约束；
8. 当前资源级可用柔性、可调度柔性与站点级聚合柔性之间是什么关系；
9. 当前是否存在 DR / price / grid-service event，事件和实际 dispatch 是否一致；
10. 最近 dispatch 的 Intent、Attempt、ACK、Readback、Measured Response、Verified Delivery 分别是什么；
11. 如果进入孤岛模式，哪些 critical loads 能被支持、当前 reserve / survival estimate 是什么；
12. 下一步应该进入 Demand、Control、Strategy、Energy、Billing、Carbon、Opportunity 还是 Data Quality。

---

# 2. 主要用户

## Primary

### 能源 / 微电网运营工程师

理解 DER portfolio 当前状态、能量流、约束、dispatch 和 resilience readiness。

### 控制 / 优化工程师

查看资源能力与 dispatch context，再进入 Control Center / Strategy Detail 执行正式动作。

### 站点能源经理

理解 DER 对 demand、cost、energy、carbon 和 resilience 的贡献。

## Secondary

- Facility Manager：关注关键负荷与断电韧性；
- EV Fleet Manager：关注车辆能量需求和 departure constraints；
- Sustainability Manager：关注 onsite generation 和 carbon context；
- Utility / DR Analyst：关注 event / program / delivery；
- Data Engineer：处理 meter / forecast / DER telemetry / semantic quality。

---

# 3. 外部最佳实践依据

## 3.1 DOE Grid-Interactive Efficient Buildings — DER 与负荷柔性需要协同

DOE/FEMP 对 Grid-Interactive Efficient Buildings（GEB）的公开定义强调：建筑利用智能技术和现场分布式能源资源，在满足 occupant / operational needs 的同时，持续协同 energy cost、grid services 与 demand flexibility。

来源：

- https://www.energy.gov/cmei/femp/grid-interactive-efficient-buildings-federal-agencies
- https://www.energy.gov/cmei/buildings/grid-interactive-efficient-buildings
- https://www.energy.gov/cmei/femp/articles/key-grid-interactive-efficient-building-technologies-federal-and-commercial

**本页采用：**

- DER 不作为孤立设备卡片集合；
- resource operation 必须和 site load / tariff / grid signal / comfort / critical-load constraints 联动；
- flexibility 不是额定功率，而是当前约束下的可用能力；
- 本页以 operational objective / constraint / outcome 为中心，不以纯电气图替代全部工作流。

## 3.2 NREL REopt — DER 需要在同一站点目标与约束下协同优化

NREL REopt 将 PV、Battery、Generator、CHP、Thermal Storage、Electric Vehicles 等技术放在同一 techno-economic decision boundary 下，根据 cost、resilience、emissions、energy goals 和 site constraints 优化 sizing/dispatch。

来源：

- https://www.nrel.gov/reopt/
- https://www.nrel.gov/docs/fy24osti/87770.pdf
- https://www.nrel.gov/docs/fy20osti/76358.pdf

**本页采用：**

- PV / BESS / Generator / Thermal / EV 不各自成为孤岛；
- resource-level status 与 site-level objective 分开；
- resilience、cost、carbon、demand 可以共同成为 context，但本页不自行重算权威经济模型；
- Forecast / Optimization Recommendation / Actual Dispatch / Delivered Result 必须分开。

## 3.3 IEEE 1547 — DER 并网与互操作是正式工程边界

IEEE 1547-2018 是 DER 与电力系统互联和互操作的重要标准，覆盖 performance、operation、interoperability、testing、safety、maintenance 等要求。

来源：

- https://sagroups.ieee.org/scc21/standards/1547rev/
- https://sagroups.ieee.org/scc21/ieee-1547-series/

**本页采用：**

- Technical Capability ≠ Interconnection Permission；
- Grid Connection State 是独立事实；
- grid support / voltage / frequency capability 不由前端猜；
- DER operating mode、interconnection status、protection/authority 状态不可压成一个 Health；
- control/UI 不绕过正式 interconnection/control owner。

## 3.4 DOE Microgrid / IEEE 1547.4 — Grid-connected 与 Islanded 是不同运行状态

DOE Microgrid Systems 明确指出 microgrid 可以在 grid-connected 模式和 islanded 模式运行；IEEE 1547.4 则针对 intentional islands 的设计、运行、分离和重新并网提供工程指导。

来源：

- https://www.energy.gov/oe/microgrid-systems
- https://www.energy.gov/cmei/femp/articles/islanding-microgrid
- https://standards.ieee.org/ieee/1547.4/11292/

**本页采用：**

```text
Grid-connected
≠ Island-capable
≠ Islanded
```

- grid outage 不自动等于 site successfully islanded；
- Islanded 不等于 critical load fully served；
- reconnection / synchronization 属于正式 control/interconnection workflow；
- resilience estimate 不等于 guaranteed survival time。

## 3.5 OpenADR — DR Event 是信号/程序层，不是设备 Dispatch 成功

OpenADR 对 demand response event 信息交换进行标准化，但具体 event signal、reports、targeting、program rules 依 deployment/program 而定。

来源：

- https://www.openadr.org/dr-program-guide
- https://www.openadr.org/faq
- https://www.openadr.org/der_v2g

**本页采用：**

```text
Event Received
≠ Accepted
≠ Strategy Armed
≠ Dispatch Executed
≠ Delivered Response
≠ Settled Result
```

- OpenADR/DR event 进入 20 作为 context；
- event signal 不直接成为设备命令；
- customer/site constraints 与本地 control authority 保持优先级和审计。

## 3.6 DOE/FEMP Managed EV Charging — 充电柔性必须服从车辆运营需求

DOE/FEMP 明确指出 managed EV charging 应协调 EV、building 和 grid 的需求，同时不损害 fleet operational needs。

来源：

- https://www.energy.gov/cmei/femp/managed-ev-charging-federal-fleets
- https://www.energy.gov/cmei/femp/managed-and-bidirectional-charging

**本页采用：**

- EVSE Online ≠ EV Connected；
- EV Connected ≠ Charging；
- Charging Flexibility 必须考虑 departure time / required energy / vehicle availability；
- V2G capability ≠ 当前允许 discharge；
- 不能为了削峰把车辆 mission requirement 当可忽略约束。

---

# 4. 产品语言契约

主界面中文优先使用：

```text
分布式能源与柔性
电网连接状态
站点总负荷
现场发电
电网购电
电网上网
电池储能
荷电状态（SOC）
可用能量
充电功率上限
放电功率上限
备用容量
光伏发电
限发 / 削减
充电负荷
可用柔性
可调度柔性
调度状态
关键负荷
孤岛运行
恢复 / 回弹
```

标准缩写可保留：

```text
PV
BESS
EVSE
SOC
DR
V2G
CHP
```

避免主界面只有：

```text
Grid Mode
Dispatchable Power
Reserve SOC
Curtailment
Delivered Response
Resource Health
```

---

# 5. Domain Vocabulary

## 5.1 DER Resource

受站点管理、影响能源供需或可提供 flexible/grid/resilience value 的现场资源，例如：

```text
PV
BESS
EVSE / EV fleet
Generator
CHP
Thermal Storage
Heat Pump / controllable thermal resource
Other owner-defined DER
```

## 5.2 Gross Facility Load

站点本身端用能负荷，在明确 measurement boundary 下、未被现场发电/储能净额遮蔽的负荷事实。

## 5.3 On-site Generation

现场发电资源在当前 measurement boundary 下的实际输出。

## 5.4 Grid Import / Export

站点与公用电网之间的实际功率/能量交换方向和数量。

## 5.5 Net Grid Demand

由权威 meter / energy-flow owner 提供的净电网需求事实。

Frontend 不自行用多个 telemetry 相减创建权威 Net Grid Demand。

## 5.6 SOC

Battery/EV owner 提供的 State of Charge。

SOC 是百分比状态，不等于可用能量。

## 5.7 Available Energy

在当前 SOC、temperature、reserve、degradation、BMS limit、operating policy 等条件下可实际使用的能量。

## 5.8 Available Power

当前资源在约束下能够提供的 charge/discharge/generation/curtailment 功率能力。

## 5.9 Dispatchable Flexibility

在 Control Authority、permission、interlock、program、asset state、operational constraints 都满足时，可以被正式 dispatch 的能力。

## 5.10 Reserve

为 resilience、battery health、grid service、mission requirement 等目的保留、不应被普通经济调度消耗的能量/功率边界。

## 5.11 Dispatch

对资源发出的正式运行目标/指令及其执行生命周期。

## 5.12 Islanded Mode

站点已与主电网分离并由本地能源资源维持指定电气边界内负荷的运行状态。

---

# 6. Mandatory Semantic Separation

以下全部禁止混同：

```text
Gross Load = Net Grid Import
PV Generation = Site Load Reduction
Battery Discharge = Building Load Reduction
Battery Charge = Site Energy Waste
Grid Export = Negative Load
SOC = Available Energy
Nameplate kW = Available Power
Nameplate kWh = Usable Energy
Available Power = Dispatchable Power
Dispatchable Power = Enrolled DR Capacity
Dispatchable Power = Delivered Response
Forecast = Actual
Schedule = Command
Command = ACK
ACK = Readback
Readback = Delivered Response
Delivered Response = Verified Program Settlement
DR Event Received = Participated
Grid Outage = Islanded Successfully
Island-capable = Islanded
Islanded = Critical Load Fully Served
Interconnection Capability = Export Permission
V2G Capable = Discharge Authorized
PV Output 0 at Night = Fault
Curtailment = Equipment Fault
Battery SOC Low = Battery Fault
```

正确关系：

```text
Site Load / Grid / DER State
↓
Resource Capability + Constraints
↓
Available Flexibility
↓
Control Authority / Program / Strategy
↓
Dispatch Intent
↓
Attempt / ACK
↓
Readback / Measured Response
↓
Verified Delivery
↓
Recovery / Persistence / Settlement context
```

---

# 7. Primary Questions

## Q1 — 站点现在的能量流是什么？

显示：

- Gross Load；
- On-site Generation；
- Storage Charge/Discharge；
- Grid Import/Export；
- data quality / timestamp。

## Q2 — 哪些 DER 正在贡献、受限或异常？

显示：

- resource type；
- operating state；
- connectivity；
- current power；
- current constraint；
- availability；
- active dispatch / strategy；
- issue / next action。

## Q3 — BESS 现在还能做什么？

显示：

- SOC；
- usable/available energy；
- charge/discharge power limits；
- reserve；
- current mode；
- temperature/health constraints when owner provides；
- active dispatch；
- expected recovery。

## Q4 — PV 为什么没满发？

区分：

- irradiance/resource limitation；
- expected available generation；
- actual generation；
- curtailment；
- inverter/asset limitation；
- grid/export constraint；
- outage/fault。

## Q5 — EV Charging 是否影响站点峰值？

显示：

- connected vehicles；
- charging power；
- required energy；
- departure constraints；
- managed charging schedule；
- available charging flexibility。

## Q6 — 当前有多少真正可调度柔性？

区分：

```text
Technical Available
Policy Available
Dispatchable
Enrolled
Committed
Delivered
```

## Q7 — 站点能否进入或维持孤岛运行？

显示：

- island capability；
- current grid mode；
- critical load；
- resource readiness；
- reserve；
- fuel/energy availability；
- owner-provided resilience estimate；
- limiting constraints。

## Q8 — 下一步去哪？

进入：

- 15 Demand / Flexibility；
- Control Center；
- Strategy Detail；
- 14 Energy；
- 18 Billing / Tariff；
- 19 Carbon；
- 21 Opportunity；
- 31 Data Quality。

---

# 8. Capability Gating

本 Surface 只有在站点具有真实 DER capability 时出现。

可能 capability：

```text
der.read
+ at least one of:
  pv.read
  bess.read
  evse.read
  generator.read
  chp.read
  thermal-storage.read
  microgrid.read
```

控制类 capability 独立：

```text
der.dispatch.read
der.dispatch.execute
microgrid.mode.read
microgrid.mode.execute
```

如果站点只有普通 HVAC load flexibility、没有 DER：

- 15 存在；
- 20 不出现空壳菜单。

---

# 9. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/der
```

推荐 Search Params：

```text
view             // overview | resources | flexibility | resilience
resourceType
selectedResource
from
until
mode
constraint
strategy
programEvent
```

视图 capability-gated：

- 没有 microgrid/island capability → 不显示 Resilience/Island-specific view；
- 没有 flexibility capability → 不显示柔性事件工作区；
- 不留 disabled teaser。

---

# 10. Entry / Exit Contract

## Entry

### 从 15 Demand / Flexibility

携带：

- peak/evidence window；
- selected resource/resource group；
- flexibility context；
- source trail。

### 从 Control / Strategy

携带：

- strategy；
- dispatch attempt；
- selected resources；
- evidence window。

### 从 Billing / Tariff

携带：

- TOU / demand-charge period；
- cost context；
- resource scope。

### 从 Carbon

携带：

- onsite generation / import / export context；
- reporting period。

## Exit

```text
DER
→ 15 Demand / Flexibility
→ Control Center
→ Strategy Detail
→ 14 Energy
→ 18 Billing / Tariff
→ 19 Carbon
→ 21 Opportunity
→ 31 Data Quality
```

---

# 11. Responsibility Boundary

本 Surface 拥有：

- DER portfolio operational projection；
- energy-flow projection；
- resource state/availability projection；
- resource capability/constraint projection；
- dispatch history projection；
- flexibility projection；
- grid-mode / island-readiness projection；
- forecast context projection；
- drill-down to control/strategy/demand/data quality。

本 Surface 不拥有：

- protection settings；
- utility interconnection authorization source-of-truth；
- low-level BMS / inverter writes；
- tariff calculation；
- market settlement；
- DR enrollment system；
- battery degradation model creation；
- forecast model creation；
- resilience guarantee；
- frontend-generated power-flow truth。

---

# 12. Information Architecture

```text
DER / Grid Context
  Site · Grid Mode · Connection · Time · Current Strategy

Operational Summary
  Gross Load
  On-site Generation
  Grid Import / Export
  Storage Power / SOC
  Current Constraint / Attention

Primary Energy Flow Workspace
  authoritative flow facts
  optional relationship/flow diagram when model exists

Resource Ledger
  PV / BESS / EVSE / Generator / Thermal / CHP
                         → Resource Inspector

Flexibility & Dispatch
  Available / Dispatchable
  Event / Strategy / Command lifecycle

Resilience / Island Readiness（capability-gated）
  critical load
  reserve
  resource readiness
  endurance estimate

Professional Detail
  interconnection
  forecast
  constraints
  audit / dispatch history
```

不是一页巨型电气单线图。

---

# 13. Energy Flow Contract

Energy-flow owner 必须提供明确 sign/direction semantics。

至少可表达：

```text
Gross Facility Load
PV Generation
Generator/CHP Generation
Battery Charge
Battery Discharge
EV Charging Load
Grid Import
Grid Export
```

禁止前端自行约定：

```text
positive = import
negative = export
```

而不由数据 contract 明确。

不同 meter boundary 必须可查。

---

# 14. Power Flow Diagram Contract

只有存在 authoritative electrical/semantic relationship model 时，才允许画 Energy Flow / single-line-like diagram。

禁止：

```text
按设备名称猜接线关系
按 telemetry 同时变化猜 power flow
把所有设备都连到 Main Bus
永远流动的装饰动画
发光表示在线
```

如果没有 authoritative topology：

> 使用结构化 Energy Flow Summary + Resource Ledger。

Diagram 是 operational aid，不是电气 protection engineering drawing 的替代品。

---

# 15. Grid Connection / Interconnection Contract

至少区分：

```text
Grid Available
Grid Connection State
Import Permission
Export Permission
Current Import/Export Limit
Interconnection Status
DER Control Authority
```

必须保持：

```text
Grid Available
≠ Connected

Technically Export-capable
≠ Export Authorized
```

IEEE 1547 / utility-specific interconnection settings 属于 Interconnection owner。

Frontend 不自行判定 export eligibility。

---

# 16. Grid Mode Contract

正式模式至少支持 owner-defined：

```text
Grid-connected
Transitioning to Island
Islanded
Transitioning to Grid
Unavailable / Unknown
```

如果只是普通 behind-the-meter DER、没有 island capability：

不显示 island mode 相关状态。

必须保持：

```text
Grid outage
≠ Islanded
```

如果 Grid unavailable 但 island transition 失败，应明确显示 failure / unavailable，而不是绿色 `Islanded`。

---

# 17. Resource State Dimensions

每个 DER 资源至少把这些维度分开：

```text
Operating State
Connectivity / Presence
Telemetry Freshness
Telemetry Quality
Availability
Constraint
Alarm
Maintenance / Out of Service
Control Authority
Active Strategy / Dispatch
Verification
```

禁止创建单一：

```text
DER Health = 87
```

---

# 18. Resource Ledger Contract

默认核心字段：

```text
资源
类型
运行状态
当前功率
能量状态 / SOC（适用时）
可用能力
当前约束
策略 / 调度
当前事项
更新时间 / 数据质量
```

Ledger 适合资源扫描。

不同 resource type 的专业指标进入 Inspector / detail，不把所有字段强塞主表。

---

# 19. BESS Contract

BESS 至少区分：

```text
SOC
Stored Energy
Usable / Available Energy
Charge Power Limit
Discharge Power Limit
Reserve
Operating Mode
Current Power
Temperature / BMS constraint（若 owner 提供）
State of Health / degradation context（若 owner 提供）
```

必须保持：

```text
SOC
≠ Available Energy
```

例如：

```text
SOC 70%
```

不意味着：

```text
可用能量 = Nameplate kWh × 70%
```

因为还有 reserve、usable window、temperature、degradation、BMS protection 等限制。

---

# 20. BESS Power / Energy Capability Contract

Nameplate：

```text
2 MW / 4 MWh
```

不等于当前：

```text
Available discharge = 2 MW
Available energy = 4 MWh
```

当前 capability 必须来自 resource owner，并带：

```text
Timestamp
Direction
Power limit
Energy limit
Duration / horizon if defined
Constraint reason
```

---

# 21. BESS Reserve Contract

Reserve 可能来自：

- resilience reserve；
- minimum SOC；
- battery warranty / health policy；
- grid service commitment；
- upcoming DR commitment；
- operator hold。

显示：

```text
当前 SOC 62%
Resilience Reserve 30%
经济调度可用范围 30–90%
```

但这些界限由 owner 提供。

前端不硬编码 `20% minimum SOC`。

---

# 22. BESS Charge / Discharge Semantics

必须明确 charge/discharge sign convention。

页面中文显示：

```text
充电 420 kW
放电 650 kW
```

不要让用户通过正负号猜方向。

也不能：

```text
Battery discharge
→ Building Load Reduced
```

更准确是：

> Battery discharge reduces net grid import or supplies local load depending on measurement boundary.

---

# 23. PV Contract

PV 至少区分：

```text
Actual Generation
Available / Expected Generation（若 owner 有）
Forecast
Curtailment
Inverter / Resource Limit
Grid / Export Constraint
Availability
```

夜间 `0 kW` 是正常 operating context，不是 fault。

阴天低输出也不能自动标 performance fault。

---

# 24. PV Curtailment Contract

Curtailment 必须由正式 owner 提供或由明确 approved analytics method产生。

不能：

```text
forecast 500 kW
actual 300 kW
→ curtailment = 200 kW
```

因为差异也可能来自 irradiance、cloud、temperature、sensor/model error、asset outage 等。

真实 curtailment 需要：

```text
Reason
Start / End
Limit / Setpoint
Authority / source
Estimated/Measured lost production if available
```

---

# 25. EVSE / EV Fleet Contract

至少区分：

```text
EVSE Available
EV Connected
Session Active
Charging
Charging Power
Vehicle SOC（有权限/数据时）
Required Energy
Departure / Mission Time
Minimum Departure SOC / Energy
Managed Charging State
V2G Capability
V2G Authorization
```

必须保持：

```text
EVSE Online
≠ EV Connected
≠ Charging
```

---

# 26. Managed EV Charging Contract

Managed charging 的柔性必须考虑：

```text
Arrival
Departure
Required Energy
Minimum SOC
Charger/Feeder Limits
Building Demand Limit
Tariff / price
Grid / DR signal
Vehicle / user constraints
```

前端不能：

```text
connected EV power
→ all flexible
```

也不能为了 site peak objective 破坏 fleet mission requirements。

---

# 27. V2G Contract

必须区分：

```text
V2G hardware capable
Vehicle supports V2G
EVSE supports V2G
Program/interconnection permits V2G
User/fleet authorization
Current dispatchable discharge
```

所以：

```text
V2G capable
≠ discharge now
```

---

# 28. Generator / CHP Contract

Generator / CHP 至少可显示：

```text
Availability
Running State
Electrical Output
Fuel Availability
Start Capability
Minimum / Maximum Power
Minimum Runtime / Start Constraint if owner provides
Maintenance / OOS
Emissions context
CHP Thermal Output / Heat Demand relation（适用时）
```

CHP 不能只按电功率评价，因为其热电耦合是核心事实。

---

# 29. Thermal Storage Contract

Thermal storage 需要区分：

```text
Thermal State / Available Capacity
Charge / Discharge State
Charge / Discharge Rate
Temperature / stratification context where relevant
Current Load Served
Constraints
```

不能强行映射成 battery SOC 语义。

---

# 30. Heat Pump / Controllable Thermal Resource Contract

Heat Pump 作为 DER/flexible resource 时：

- electrical load 是 demand-side fact；
- thermal output / comfort requirement 是 service constraint；
- available flexibility 不能只看 nameplate kW；
- 需要 08 Comfort / 15 Flexibility / 16 Efficiency context。

20 不重建 HVAC engineering diagnosis。

---

# 31. Forecast Contract

至少区分：

```text
Load Forecast
PV Generation Forecast
Price Forecast
Weather Forecast
Resource Availability Forecast
```

每个 forecast 必须有：

```text
Forecast horizon
Generated at
Model/version
Confidence/uncertainty if owner provides
Coverage
```

必须保持：

```text
Forecast
≠ Schedule
≠ Dispatch
≠ Actual
```

---

# 32. Resource Flexibility Contract

每个资源的 flexibility 至少需要：

```text
Direction        // increase load / decrease load / inject / absorb
Available Power
Available Energy / Duration
Start / Ramp Constraint
Lead Time
Availability Window
Recovery / Rebound
Reserve Constraint
Operating Constraint
Control Authority
Timestamp
Method / Revision
```

不能只显示：

```text
Battery Flexibility 1.5 MW
```

---

# 33. Flexibility State Separation

必须区分：

```text
Technical Available
Policy Available
Dispatchable
Enrolled
Committed
Delivered
Settled
```

20 主要负责 resource-level Technical/Dispatchable/Delivered context。

15 负责 site-level demand/flexibility analysis 和聚合语义。

Program settlement 属于 program/market owner。

---

# 34. 15 Demand Flexibility vs 20 DER Boundary

## Surface 15

回答：

> 站点在某个 demand window 中可以如何 Shed / Shift / Modulate？

以 site load shape、peak、rebound、flexibility envelope 为中心。

## Surface 20

回答：

> 哪些 DER 资源现在能提供什么能力、受什么约束、实际 dispatch 了什么？

以 resource state / energy / dispatch / grid context 为中心。

所以：

```text
15 = Site-level flexibility analytics
20 = DER resource operations / orchestration context
```

互相链接，不复制。

---

# 35. DR Event Contract

DR / utility event 至少保持：

```text
Announced
Received
Evaluated
Accepted / Declined
Strategy Armed
Active
Ended
Recovery
Delivery Evaluated
Settled（若 capability）
```

Event 本身不是设备 command。

---

# 36. Dispatch Lifecycle Contract

正式调度链：

```text
Intent
↓
Authorized Command / Strategy
↓
Attempt
↓
ACK / Transport Result
↓
Readback / Resource State
↓
Measured Response
↓
Verified Delivery
```

必须保持：

```text
Intent ≠ Attempt
Attempt ≠ ACK
ACK ≠ Readback
Readback ≠ Measured Response
Measured Response ≠ Verified Delivery
```

---

# 37. Control Boundary

20 可以显示：

- current control authority；
- active strategy；
- command/readback summary；
- dispatch history；
- interlock/constraint summary；
- next professional action。

默认不直接提供：

```text
Battery charge/discharge write
Generator start/stop
PV curtailment setpoint
Microgrid island/reconnect
EVSE power override
Strategy publish
```

这些进入 Control Center / Strategy Detail。

如果未来允许低风险 bounded actions，也必须经过独立安全产品决策，不在 20 v1 默认放行。

---

# 38. Realtime Contract

采用：

```text
Snapshot + Stream
```

Snapshot 提供 authoritative current state。

Stream 只订阅当前：

```text
Site
+ visible resource scope
+ selected resource
```

禁止：

- subscribe all site points then frontend filter；
- stream disconnect → resources offline；
- reconnect → reset selected resource；
- realtime update → reorder ledger continuously；
- stream update → change current grid mode without owner-confirmed state transition semantics。

---

# 39. Resilience / Critical Load Contract

只有 resilience/microgrid capability 存在时显示。

至少包含：

```text
Critical Load Scope
Critical Load Current Demand
Priority / Tier
Available Local Generation
Available Stored Energy
Fuel Availability
Reserve Policy
Grid Mode
Island Readiness
Owner-provided Survival Estimate
Limiting Constraint
```

Critical Load 定义来自 Resilience/Facility owner。

Frontend 不从设备名称推断 `critical`。

---

# 40. Survival / Endurance Estimate Contract

如果 owner 提供 resilience estimate：

必须显示：

```text
Estimate horizon
Assumed load
Resource availability
Starting SOC/fuel
PV/weather assumption
Reserve
Model/version
Generated at
```

必须保持：

```text
Estimated 6.2 h
≠ Guaranteed 6.2 h
```

NREL REopt 的 resilience 分析也说明 outage survival 会受 outage start time、load、renewables、battery starting SOC 等条件影响。

---

# 41. Island Transition Contract

如果支持 intentional island：

状态由 Microgrid Control owner 提供。

可显示：

```text
Grid-connected
Transition requested
Separating
Islanded
Resynchronizing
Reconnected
Failed / Inhibited
```

不能根据 grid meter = 0 猜 `Islanded`。

也不能根据 breaker telemetry 单点推断完整 island-state truth。

---

# 42. Reconnection Boundary

Reconnect to grid 是高风险控制流程。

20 只显示：

- eligibility/readiness；
- current mode；
- synchronization / interlock status summary；
- professional exit。

真正操作进入 Control Center / Microgrid Control workflow。

---

# 43. Safety / Interlock Contract

DER dispatch 必须服从：

```text
Permission
Control Authority
Interconnection Limit
Protection / Interlock
Resource Safety State
Maintenance / OOS
Site Operational Constraint
Comfort / Mission Constraint
Program Constraint
```

“不允许执行”必须有 reason。

前端不能把 blocked action 变成 silent fallback command。

---

# 44. Data Quality Contract

每个关键事实至少区分：

```text
Good
Stale
Suspect
Bad
Estimated
Calculated
Unavailable
Unknown
```

必须特别区分：

```text
SOC stale
≠ SOC 0

Grid meter unavailable
≠ Grid import 0

PV forecast unavailable
≠ Forecast 0
```

---

# 45. Data Authority Contract

## Gross Load / Import / Export / Generation

Owner：Meter / Energy Flow domain。

## Resource identity / relationship

Owner：DER Registry / Semantic Model。

## BESS SOC / limit / state

Owner：BESS / EMS / BMS integration domain。

## PV actual / curtailment / forecast

Owner：PV / DER analytics domain。

## EVSE / Vehicle need

Owner：EV Fleet / Charging domain。

## Grid mode / interconnection

Owner：Microgrid / Grid Connection domain。

## DR Event

Owner：DR / Program Integration domain。

## Dispatch / command / readback

Owner：Control Execution domain。

## Verified delivery

Owner：Flexibility / Program Performance domain。

Frontend 只做 DER-centered projection。

---

# 46. Query / Read Model Contract

推荐：

```text
DER Site Projection
  + grid connection/mode
  + gross load/import/export
  + resource summaries
  + current strategies/dispatches
  + available flexibility
  + active constraints
  + attention items
  + resilience summary if applicable
```

Resource Inspector projection：

```text
Identity
Independent states
Current power/energy
Current constraints
Capability
Active dispatch
Recent evidence
Next actions
```

禁止：

```text
50 DER resources
→ 50 telemetry requests
→ 50 availability requests
→ 50 strategy requests
→ 50 constraint requests
```

缺 read model 时修 domain contract，不在前端 fan-out。

---

# 47. Inspector Contract

Resource Inspector 只做快速判断：

```text
资源身份
运行 / 连接 / 数据质量
当前功率
能量状态（适用时）
当前能力
约束
Active strategy / dispatch
Alarm / maintenance
1–3 个下一动作
```

完整控制、历史、设置、保护、长趋势进入 durable owner surface。

---

# 48. Loading / Empty / Partial / Error

## No DER Capability

Surface 不出现。

## DER capability exists, no resources configured

> `当前站点尚未配置可用分布式能源资源。`

## DER service unavailable

> `分布式能源状态暂不可用。`

不能显示资源全为 0。

## Energy flow unavailable

Resource ledger 可继续显示，但：

> `站点能量流暂不可用。`

不能前端相加减重建。

## Grid mode unavailable

显示：

> `电网运行模式暂不可用。`

不能默认 `Grid-connected`。

## SOC unavailable

显示 `SOC 暂不可用`，不能 0%。

---

# 49. Permission / Capability Gating

示例：

```text
der.read
pv.read
bess.read
evse.read
microgrid.read
der.dispatch.read
der.dispatch.execute
microgrid.mode.execute
resilience.read
```

无权限 action 不显示。

UI hiding 不替代 server authorization。

---

# 50. Visual / UX Contract

默认视觉层级：

```text
Grid / Site context
↓
能量流与当前目标
↓
需要关注的资源/约束
↓
DER Resource Ledger
↓
Flexibility / Dispatch
↓
Resilience / Island readiness（适用时）
↓
Professional Detail
```

禁止：

- 满屏动态单线图；
- 设备图标永久动画；
- 一排十几个 SOC / kW 卡；
- 黑盒 `DER Health 91`；
- 把每个 DER 都做成大卡片；
- 用绿色就暗示 renewable/normal；
- 把 grid import/export 正负号留给用户自己猜；
- 把高风险控制按钮混在高频监控区。

---

# 51. Primary Energy Flow Workspace

若 authoritative relationship + meter flow model 存在，可以显示：

```text
           PV
           ↓
Grid ↔ Site Bus ↔ Building Load
           ↕
          BESS
           ↓
         EVSE
```

但必须显示真实值、方向、timestamp、quality。

如果关系不完整，降级为 structured flow facts，不画假 topology。

---

# 52. Chart Contract

## Net Load / DER Contribution

显示：

- Gross Load；
- Net Grid Import；
- PV；
- BESS charge/discharge；
- selected DER groups。

不同单位遵循 aligned-small-multiples。

## BESS

可以显示：

- SOC；
- charge/discharge power；
- reserve threshold；
- dispatch events。

SOC 与 kW 不默认双 Y 轴。

## PV

Actual / Available / Forecast 可以比较，但明确标注来源和语义。

## Dispatch

用 event lane 表示 Intent / ACK / Measured / Verified milestones。

---

# 53. ECharts Boundary

ECharts 可以负责：

- power/energy trend；
- SOC trend；
- dispatch/event visualization；
- forecast vs actual；
- contribution breakdown。

ECharts 不负责：

- power-flow inference；
- SOC calculation；
- available energy calculation；
- curtailment inference；
- dispatch eligibility；
- resilience estimate；
- grid-mode truth；
- interconnection permission；
- DR delivery calculation。

---

# 54. Component Mapping

```text
Context Header             → normal app layout
Mode / connection status   → Badge + text + reason
Energy Flow                → dedicated feature component
Resource Ledger            → shadcn Table + TanStack Table
Resource Inspector         → Sheet / side inspector
Resource type filters      → Select / Popover / Command
Flexibility facts          → semantic table / compact facts
Dispatch timeline          → Timeline / event list
Resilience detail          → structured facts + chart
High-risk action exit      → Button → Control/Strategy route
```

不建立 generic DER card framework。

---

# 55. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 分布式能源与柔性 · 中央园区                 电网状态：并网运行              │
│ 当前策略：需量控制 v6                      站点时区 UTC+08:00               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 站点总负荷 2.84 MW   现场发电 0.96 MW   电网购电 1.52 MW   储能放电 0.36 MW│
│ 数据更新时间 15:42:18 · 关键量测有效                                      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 能量流                                                                       │
│ 光伏 0.96 MW  ──→  站点负荷 2.84 MW  ←── 电网购电 1.52 MW                  │
│                         ↑                                                    │
│                    储能放电 0.36 MW                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ 资源                     当前状态        当前功率      当前能力 / 约束       │
│ 屋顶光伏 PV-01           发电中          0.96 MW       限发：无              │
│ 电池储能 BESS-01         放电中          0.36 MW       SOC 62% · 可放 1.2 MW │
│ EV 充电集群              受控充电        0.28 MW       可下调 0.14 MW        │
│ 备用发电机 GEN-01        待机            0 kW          可用 · 非当前调度     │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ 当前柔性                             │ 储能 BESS-01                          │
│ 可调度下调负荷 0.14 MW               │ SOC 62%                               │
│ 可调度供电 1.20 MW / 1.8 MWh         │ 韧性备用 30%                          │
│ 当前 DR 事件：无                     │ 充电上限 0.8 MW · 放电上限 1.2 MW     │
│ [需求分析] [策略中心]                │ [趋势] [设备] [控制中心]              │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 韧性状态（Microgrid capability）                                             │
│ 孤岛能力：可用   关键负荷：0.92 MW   估算支撑：4.6 h · 非保证值             │
│ 限制：按当前天气/储能/燃料假设                       [查看韧性详情]           │
└──────────────────────────────────────────────────────────────────────────────┘
```

主界面中文；PV/BESS/EVSE/SOC 等标准缩写作为辅助。

---

# 56. Accessibility

必须：

- Import / Export 不只靠箭头颜色；
- Charge / Discharge 有文字方向；
- Grid-connected / Islanded 有文本；
- chart 有 exact table/text alternative；
- SOC / reserve / constraints 可读屏；
- realtime 不抢 focus；
- event/dispatch milestone keyboard 可访问；
- around 768px 不依赖 hover；
- power-flow diagram 不是唯一信息载体。

---

# 57. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- Grid mode；
- Gross Load / Import / Generation；
- primary energy flow；
- resource ledger 前几行；
- current flexibility / constraint；
- selected resource inspector 或等价上下文。

## 1024–1439 px

- energy flow 与 resource ledger 上下排列；
- Inspector 可变 Sheet；
- resilience 下沉。

## Around 768 px

仍必须能：

- 看 Grid mode；
- 看主要能量流事实；
- 找到 DER resource；
- 看 SOC / current power / constraint；
- 看 available/dispatchable flexibility；
- 打开 Control / Strategy / Demand。

Power-flow diagram 不成为移动端唯一入口。

---

# 58. No Defensive Programming / No Compatibility Design

明确禁止：

```text
DER API error → 0 resources
energy-flow unavailable → frontend sum/subtract telemetry
missing grid mode → assume grid-connected
grid meter 0 → islanded
breaker state → infer complete island state
grid outage → island success
resource offline → availability 0 without owner semantics
SOC unavailable → 0%
SOC × nameplate kWh → authoritative available energy
nameplate kW → current available power
battery discharge → building load reduction
battery charge → energy waste
PV actual < forecast → curtailment
PV output 0 at night → fault
forecast unavailable → 0
forecast → schedule
schedule → command
command ACK → delivered response
readback → verified delivery
DR event received → participation
DR accepted → executed
resource online → dispatchable
V2G capable → discharge allowed
EV connected power → all flexible
comfort/mission constraint unavailable → safe to curtail
export technically possible → export authorized
interconnection unavailable → assume unrestricted export
critical load missing → infer from equipment names
resilience estimate missing → battery duration arithmetic in frontend
estimated survival → guaranteed survival
one resource → one telemetry/constraint/strategy request N+1
multiple DER APIs → first success wins
old DER Dashboard adapter
old microgrid page fallback
frontend-generated curtailment
frontend-generated dispatch eligibility
frontend-generated grid-mode truth
```

不建立：

```text
new DER unavailable
→ fallback old dashboard
```

原则：

> **One DER fact → one authoritative owner. Nameplate is not current capability. SOC is not usable energy. Grid-connected is not islanded. Available is not dispatchable. Dispatch is not delivery. Forecast is not actual. Unknown stays unknown.**

---

# 59. Browser Acceptance Criteria

## Capability

- 无 DER capability 时不显示空壳页面；
- 无 microgrid capability 时不显示 island-specific UI；
- 无 control permission 时无写操作入口。

## Energy Flow

- Gross Load / Generation / Import / Export 语义可查；
- direction 不靠正负号猜；
- no authoritative topology 时不画假 flow graph；
- missing flow 不前端重建。

## BESS

- SOC / available energy / power limit / reserve 分开；
- SOC unavailable 不显示 0；
- nameplate 不冒充 available；
- charge/discharge direction 明确。

## PV

- actual / forecast / available / curtailment 分开；
- 夜间 0 不显示 fault；
- curtailment 不由 forecast gap 推断。

## EVSE

- EVSE available / EV connected / charging 分开；
- departure/required energy 约束可见；
- V2G capability 与 authorization 分开。

## Dispatch

- Intent / Attempt / ACK / Readback / Measured / Verified 分开；
- DR Event 不冒充 dispatch；
- active strategy / control authority 可见；
- high-risk control 进入 owner workflow。

## Resilience

- Grid-connected / Islanded 分开；
- outage 不冒充 island success；
- critical load/assumptions/estimate provenance 可查；
- survival estimate 明确非保证值。

## Boundaries

- Site flexibility analytics → 15；
- Control execution → Control Center；
- Strategy lifecycle → Strategy Detail；
- Energy → 14；
- Billing/Tariff → 18；
- Carbon → 19；
- Opportunity → 21；
- Data Quality → 31。

## Responsive / Accessibility

- 1440–1720px 是 coherent DER workspace；
- around 768px 核心任务完整；
- no color-only flow/state；
- no hover-only critical values；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 DER/Microgrid compatibility adapter；
- 无 frontend-generated grid mode / available power / curtailment / dispatchability；
- 无 N+1 resource fan-out；
- 无 unknown→0；
- review scenario 无 runtime/network error。

---

# 60. Explicit Non-Goals

本页不是：

- protection relay configuration tool；
- SCADA electrical engineering console；
- power-market bidding platform；
- DR settlement engine；
- inverter commissioning tool；
- BMS parameter editor；
- battery degradation modeling studio；
- tariff optimizer；
- resilience certification tool；
- generic electrical one-line viewer。

---

# 61. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- DER capability gating 已明确；
- Gross Load / Generation / Import / Export 已分离；
- authoritative energy-flow/topology boundary 已明确；
- Grid-connected / Island-capable / Islanded 已分离；
- interconnection capability / permission 已分离；
- BESS SOC / available energy / available power / reserve 已分离；
- PV actual / forecast / curtailment 已分离；
- EVSE / EV / charging / mobility constraint 已分离；
- resource flexibility / dispatchability / delivery 已分离；
- DR Event / Dispatch lifecycle 已分离；
- Control / Strategy boundary 已明确；
- Resilience / critical-load / survival estimate 已明确；
- 中文优先产品语言已落实；
- no defensive fallback contract 已接受；
- old DER / Microgrid Dashboard 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
