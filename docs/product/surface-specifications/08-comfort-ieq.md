# 08 舒适与室内环境 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `08 舒适与室内环境（Capability-gated）`  
> **Route intent：** `/sites/:siteId/operations?view=comfort`
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有房间/区域页、旧舒适度卡片、旧空气质量页面、旧设计稿或旧 Ant/ProComponents 页面。当前代码仅可在实施阶段作为真实 Space/Zone Model、Telemetry、Occupancy、Comfort Target、IAQ、Alarm、Diagnosis、Work、Control、Complaint owner、capability、permission 与 route contract 的候选证据来源。
>
> **2026-09-23 Workspace placement：** 08 已合并为“运行” Workspace 的 `空间与环境` peer view，通过 `/sites/:siteId/operations?view=comfort` 进入；旧 `/comfort` runtime route 已删除。

---

# 1. Primary Job

舒适与室内环境页面的唯一核心任务是：

> **让运营人员快速识别哪些已占用或即将占用的空间偏离了明确的热舒适 / 室内环境目标，理解偏离持续了多久、数据是否可信、由哪些 HVAC 系统服务，并在不把单一传感器误当成完整健康结论的前提下继续进入趋势、运行、诊断、工单或优化工作流。**

本页是 **zone-level comfort / IEQ operational analysis workspace**，不是：

- 温度仪表盘；
- “绿色=健康”的 IAQ 大屏；
- 单一 CO₂ 排名页；
- ASHRAE 55 自动合规声明器；
- ASHRAE 62.1 自动合规声明器；
- 医疗健康诊断页面；
- Occupant app；
- 房间控制器；
- HVAC Control Center；
- Energy 页面复制品；
- 黑盒 Comfort Score 排行榜。

用户离开本页前应该已经知道：

1. 哪些 Zone / Space 当前或近期偏离目标；
2. 该偏离发生在 occupied、standby 还是 unoccupied context；
3. 偏离是 thermal、humidity、ventilation indicator、particulate / pollutant，还是 data-quality 问题；
4. 当前 target 的来源、版本和适用条件是什么；
5. 数据是否足以支持“目标偏离”或标准化 comfort / IAQ 评估；
6. 偏离持续多久、影响多少 occupied time；
7. 哪个 HVAC system / equipment 服务该空间；
8. 下一步应该进入 Trend、System Operations、Device、Diagnosis、Work、Data Quality 或 Optimization 哪一个 owner 页面。

---

# 2. 主要用户

## Primary

### 站点能源 / 设施经理

需要确认节能、调度或运行策略没有以牺牲舒适和 IAQ 为代价，并优先处理持续影响已占用空间的问题。

### HVAC 运行工程师

需要从 zone-level deviation 下钻到 AHU / VAV / FCU / terminal / hydronic system 的当前运行与历史证据。

### Commissioning / 诊断工程师

需要把 comfort / IEQ deviation 与 sequence、setpoint、damper、airflow、occupancy、schedule、equipment finding 对齐。

## Secondary

- 优化工程师：把 comfort / IAQ 作为 optimization guardrail；
- 维修负责人：从持续 zone issue 进入 Work Order；
- 数据工程师：处理 stale / bad-quality / missing / semantic-mapping 问题；
- 资产负责人：确认 zone-to-system relationship 是否正确。

## 不作为主要目标用户

- 企业管理层：使用 Portfolio / Site Overview；
- 终端 occupant：本页不是个人舒适控制 app；
- 医疗 / EHS 专家：本页不提供临床或暴露健康结论；
- 控制工程师执行 write：进入 Control Center / Strategy Detail。

---

# 3. 外部最佳实践依据

## 3.1 ASHRAE Standard 55-2023 — Thermal Comfort 不是“室温是否在 22–24°C”

ANSI/ASHRAE Standard 55-2023 明确指出，thermal comfort 由多个环境因素和个人因素共同决定，包括：

- air temperature；
- mean radiant temperature；
- humidity；
- air speed；
- activity / metabolic rate；
- clothing insulation。

标准提供 standard / adaptive 两类评估方法，并要求在适用条件下选择正确方法。

来源：

- https://www.ashrae.org/technical-resources/bookstore/standard-55-thermal-environmental-conditions-for-human-occupancy
- https://www.ashrae.org/technical-resources/ashrae-journal/featured-articles/may-2026-dispelling-thermal-comfort-myths-part-1-foundations-for-applying-ashrae-standard-55-2023

**本页采用：**

- 只有 temperature / RH 时，不宣称“ASHRAE 55 compliant”；
- 这种情况只能表达 configured operational comfort bounds / comfort proxy；
- 只有 required inputs、method、edition、applicability 都由 authoritative owner 提供时，才允许显示 `ASHRAE 55 assessment`；
- air temperature 不等于 operative temperature；
- adaptive comfort 只能在适用条件满足时使用，前端不自行判断。

## 3.2 ASHRAE Standard 62.1-2025 — Ventilation / IAQ 是系统能力，不是单一传感器阈值

Standard 62.1-2025 规定 mechanical / natural ventilation、filtration、controls、air cleaning、operations & maintenance 等要求，目标是 acceptable IAQ 并降低不利健康影响。

来源：

- https://www.ashrae.org/technical-resources/bookstore/standards-62-1-62-2
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- 不用单个 CO₂ 值宣称 `62.1 compliant / non-compliant`；
- ventilation requirement、occupancy、airflow、DCV、filter / outdoor-air condition 属于不同事实；
- 如果平台拥有正式 62.1 calculation / commissioning owner，页面可以展示该 owner 的 assessment；
- 否则只展示 measured condition 与 configured operational targets。

## 3.3 ASHRAE Indoor CO₂ Position Document 2025 — CO₂ 必须谨慎解释

ASHRAE 2025 Indoor Carbon Dioxide Position Document 专门澄清 CO₂ 与 ventilation / IAQ 的关系；错误地把 CO₂ 当作完整 IAQ 指标在行业中很常见。

来源：

- https://www.ashrae.org/about/position-documents
- https://www.ashrae.org/file%20library/about/position%20documents/pd-on-indoor-carbon-dioxide-english.pdf

**本页采用：**

- CO₂ 默认标签是 `CO₂` 或 `Ventilation indicator`，不是 `Air Quality Score`；
- interpretation 必须考虑 occupancy、space type、outdoor baseline、sensor semantics 和 owner-defined control method；
- 有非 occupant CO₂ source / CO₂ removal mechanism 等特殊条件时，不允许套普通 occupancy-based interpretation；
- CO₂ target / DCV target 必须有 method/source，而不是前端统一写一个 ppm 阈值。

## 3.4 U.S. EPA — 一个 IAQ sensor 不能代表完整 IAQ

EPA 指出低成本/单一污染物监测器只测量其设计检测的污染物或环境因素，不能完整代表 IAQ；CO₂ 可以提供 ventilation 信息，但需要谨慎解释。

来源：

- https://www.epa.gov/indoor-air-quality-iaq/low-cost-air-pollution-monitors-and-indoor-air-quality
- https://www.epa.gov/indoor-air-quality-iaq/can-i-measure-carbon-dioxide-co2-indoors-get-information-ventilation
- https://www.epa.gov/indoor-air-quality-iaq/factsheet-what-indoor-air-quality

**本页采用：**

- 不制造 generic `IAQ 92/100`；
- PM2.5、CO₂、TVOC、CO、humidity、temperature 等保持独立；
- 没有测量的污染物不能被 UI 暗示为“正常”；
- 传感器只证明其测量范围内的事实。

## 3.5 DOE Building Controls — Energy、Comfort、IAQ 必须同时成立

DOE 明确说明 building operation 的目标同时包括：

- comfortable conditions；
- healthy indoor air quality；
- minimized energy / cost。

来源：

- https://www.energy.gov/cmei/buildings/about-building-controls
- https://www.energy.gov/cmei/buildings/about-sensors-and-controls

**本页采用：**

- comfort / IAQ 是 Energy Optimization 的 guardrail，不是附属 KPI；
- 节能策略如果造成 occupied comfort deviation，必须在该页可被识别和追踪；
- 不以 energy reduction 自动证明 optimization success。

## 3.6 DOE Energy Efficiency and IEQ Assessment Guide — IEQ 是多维环境质量

DOE/FEMP 将 IEQ 描述为包括 air quality、thermal comfort、acoustics、lighting 等室内条件，并指出 energy efficiency 与 IEQ 可以存在重叠改进机会。

来源：

- https://www.energy.gov/cmei/femp/articles/energy-efficiency-and-indoor-environmental-quality-assessment-guide
- https://www.energy.gov/cmei/femp/articles/ventilation-assessment-and-action-guide

**本页采用：**

- 当前 HVAC 产品 v1 聚焦 thermal + ventilation/IAQ measurable scope；
- acoustics / lighting 只有真实 capability 接入时才出现；
- 不把未接入维度算作“已达标”。

## 3.7 DOE HVAC Commissioning — Comfort / IAQ 问题需要回到 HVAC operation / verification

DOE 指出 HVAC commissioning 常能发现同时影响 energy、IAQ 和 comfort 的设备 / controls 问题。

来源：

- https://www.energy.gov/cmei/buildings/hvac-commissioning

**本页采用：**

- persistent comfort / IAQ deviation 可以进入 Diagnosis / Work / Functional Verification；
- zone issue 不在本页直接宣称 root cause；
- serving HVAC relation 是调查入口，不是因果结论。

## 3.8 Schneider / Siemens — 成熟产品按 Building / Floor / Room 逐层发现舒适问题

成熟 building platforms 会让用户从 building / floor level 发现 comfort underperformance，再进入 room / HVAC context。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/comfort-ai/
- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/
- https://www.se.com/us/en/work/software/ecostruxure-building/large-buildings-workplace-management/
- https://www.se.com/ww/en/work/solutions/building-management/room-control/

**本页采用：**

- Zone / Space 是主要扫描单位；
- Building / Floor / Zone context 稳定；
- occupancy 与 environmental condition 一起解释；
- 可以使用 comfort status，但如果采用 score，必须有公开、可追溯的 method owner；
- 本产品 v1 默认不制造黑盒 composite score。

---

# 4. Primary Questions

用户进入后按以下顺序回答。

## Q1 — 当前哪些空间真正值得关注？

优先识别：

- occupied / imminently occupied；
- deviation 持续较久；
- deviation magnitude 较大；
- 多个指标同时异常；
- complaint / work / alarm 已关联；
- data coverage 足够支持判断。

排序必须基于可解释字段，不使用不可见黑盒 `Comfort Risk Score`。

## Q2 — 偏离了什么目标？

必须明确：

- metric；
- observed value；
- target / acceptable band；
- target source；
- target version；
- applicability；
- occupied / unoccupied policy。

## Q3 — 这是“运营目标偏离”还是“标准化舒适/IAQ assessment”？

页面必须明确 label。

不能把：

```text
Temperature target exceeded
```

写成：

```text
ASHRAE 55 non-compliant
```

除非 authoritative assessment owner 确认。

## Q4 — 这个问题影响了多久、什么时候最明显？

回答：

- current duration；
- occupied violation duration；
- today / selected-period violation hours；
- first observed；
- recovered / unresolved；
- trend/evidence window。

## Q5 — 哪个 HVAC system 服务这个 Zone？

显示 authoritative relation：

- AHU；
- terminal/VAV/FCU；
- hydronic loop；
- zone sensor；
- ventilation source；
- relevant equipment。

只表示 `serves / associated with`，不自动表示 root cause。

## Q6 — 下一步去哪？

进入：

- System Operations；
- Trend Analysis；
- Device Detail；
- Diagnosis；
- Work Order；
- Functional Verification；
- Data Quality；
- Optimization Plan。

---

# 5. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/operations?view=comfort
```

推荐 Search Params：

```text
floor
zone
occupancy
metric
status
system
timeStart
timeEnd
view            // workspace owner: comfort
comfortView     // thermal | air-quality
sort
page
size
selected
```

如果页面默认使用运营窗口，例如 `Today`，它必须明确显示并能通过 URL 恢复。

不写入 URL：

- tooltip；
- hover；
- temporary chart cursor；
- disclosure open state；
- transient floor-map pan；
- temporary Sheet animation state。

---

# 6. Capability Gating

本 Surface 不是所有站点都显示。

最小 capability：

```text
Space / Zone model
AND
至少一种 authoritative environmental signal
AND
comfort.read / ieq.read permission
```

可独立启用：

- Thermal capability；
- Occupancy capability；
- CO₂ / ventilation indicator capability；
- PM capability；
- other pollutant capability；
- complaint integration；
- Standard 55 assessment；
- Standard 62.1 assessment；
- spatial/floor-plan geometry。

没有某项 capability 就不展示该列/图层。

禁止用 disabled 空卡片提示“未来可能支持”。

---

# 7. Entry Contract

## 7.1 从 Site Overview

携带 Site；若 Overview 指向 comfort issue，可携带：

- metric / issue type；
- selected zone / floor；
- source trail。

## 7.2 从 System Operations

携带：

- Site；
- serving HVAC system；
- affected zones；
- current operational context。

## 7.3 从 Optimization Plan

携带：

- Site；
- affected zones；
- proposal / strategy source；
- before/after evidence window；
- comfort/IAQ guardrail definition。

该入口尤其重要：用于验证节能优化没有越过环境约束。

## 7.4 从 Alarm / Diagnosis / Work / Verification

携带：

- source object；
- affected zone；
- evidence window；
- return-to-source。

---

# 8. Exit Contract

主要专业出口：

```text
Zone issue
→ Trend Analysis
→ System Operations
→ Device Detail
→ Diagnosis
→ Work Order
→ Functional Verification
→ Data Quality
→ Optimization Plan
```

保留 compatible：

- Site；
- Zone；
- Time Range；
- Metric；
- Source；
- Serving System；
- Evidence Window。

---

# 9. Responsibility Boundary

本页拥有：

- Zone / Space population scan；
- comfort / IEQ operational target deviation；
- occupied violation duration；
- environmental evidence summary；
- target provenance；
- serving-HVAC relationship projection；
- zone-level trend preview；
- complaint summary（有 owner 时）；
- deep-link handoff。

本页不拥有：

- HVAC direct control；
- occupant personal preference profile；
- medical health diagnosis；
- full alarm handling；
- root-cause diagnosis；
- work lifecycle mutation；
- comfort target administration；
- sensor calibration；
- semantic relationship edit；
- Standard 55 / 62.1 formula unless a domain owner explicitly owns it；
- generic IAQ / health score。

---

# 10. Information Architecture

```text
Context Header
  Site · selected period · target policy · timezone

Compact Summary
  Occupied zones
  Zones outside thermal target
  Zones with IAQ/ventilation attention
  Data coverage / unavailable domains

Primary View Controls
  Attention | Thermal | Air Quality
  Floor / System / Occupancy / More Filters

Zone Attention Ledger
  Zone
  Occupancy
  Thermal status
  Humidity
  CO₂ / IAQ dimensions
  Violation duration
  Serving HVAC
  Data quality

Optional Spatial View
  only with authoritative geometry

Selected Zone Inspector
  identity
  occupancy
  exact targets
  current measurements
  violation timeline
  serving system/equipment
  current issues
  professional exits

Evidence Workspace
  zone trend
  occupancy context
  HVAC events / target changes

Complaints / Related Work
  capability-gated
```

默认是 **Attention-first Zone Ledger**，不是 KPI Card Wall。

---

# 11. Target Semantics

这是本页最重要的专业契约之一。

## 11.1 Operational Target

站点可有明确的 operational bounds，例如：

```text
Occupied temperature target: 22–25 °C
Occupied RH operational band: 30–60%
Zone CO₂ operational threshold: owner-defined
```

这些 target 必须有：

- owner；
- version；
- effective time；
- occupancy mode；
- applicable space type；
- unit；
- source policy / engineering basis。

UI label：

> `运营目标`

不要叫：

> `ASHRAE 合规范围`

除非 owner 真正定义了该 assessment。

## 11.2 Standard 55 Assessment

只有在 owner 提供：

- edition；
- method（standard / adaptive）；
- required measured / assumed inputs；
- applicability determination；
- calculation result；
- documentation/provenance；

才显示：

> `ASHRAE 55-2023 assessment`

缺 required input 时必须显示：

> `无法完成标准化热舒适评估`

而不是自动回退成温度阈值“合规”。

## 11.3 Standard 62.1 Assessment

只有 ventilation / IAQ owner 提供正式 assessment 时显示。

CO₂ measurement 本身不能被 UI 升级成 62.1 compliance。

---

# 12. Thermal Comfort Contract

## 12.1 Default operational facts

页面默认可显示：

- air temperature；
- relative humidity / dew point（owner 支持时）；
- target band；
- deviation magnitude；
- duration；
- occupancy；
- trend；
- serving HVAC。

## 12.2 Advanced comfort assessment

只有 owner 支持时，可显示：

- operative temperature；
- mean radiant temperature；
- air speed；
- metabolic rate assumption；
- clothing assumption；
- PMV/PPD 或 owner-defined Standard 55 result；
- adaptive comfort result；
- local discomfort assessment。

UI 必须同时显示 method / assumptions。

## 12.3 Forbidden simplification

禁止：

```text
22–24°C = comfortable
temperature in range = ASHRAE compliant
RH in range = comfortable
sensor missing = comfortable
```

---

# 13. IAQ Contract

IAQ 不使用单一 generic state。

按 available measured dimensions 独立表达：

```text
CO₂
PM2.5
PM10
TVOC
CO
O₃
Humidity / moisture-related indicator
Other site-specific pollutant
```

只有真实 sensor / analytics owner 存在时才出现。

## 13.1 Measurement != health diagnosis

页面可以表达：

> `PM2.5 超出站点目标`

不能表达：

> `该房间不健康`

除非有明确、合法、专业 owner 定义该 health classification。

## 13.2 Outdoor context

需要时可显示：

- outdoor pollutant context；
- outdoor CO₂ baseline；
- outdoor humidity / temperature；

但不能因为 outdoor data unavailable 就把 indoor sensor 判为 normal / abnormal。

---

# 14. CO₂ Contract

CO₂ 必须单独处理。

页面显示：

```text
CO₂ 980 ppm
Occupancy 12
Outdoor CO₂ 430 ppm (if authoritative)
Ventilation target/source
```

可用 label：

- `CO₂`
- `Ventilation indicator`
- `DCV input`（如果 owner 明确）

禁止默认 label：

- `IAQ Score`
- `Fresh Air Quality`
- `62.1 Compliance`

## 14.1 Occupancy Dependency

CO₂ interpretation 与 occupancy 密切相关。

Occupancy unavailable 时：

> `occupancy context unavailable`

不能继续按满员 / 空房假设解释。

## 14.2 Special conditions

如果 owner 标记：

- non-human CO₂ source；
- CO₂ removal mechanism；
- sensor calibration issue；

普通 DCV / occupancy interpretation 必须关闭或明确标记不适用。

---

# 15. Occupancy Contract

Occupancy 可能来自：

- schedule；
- badge/access aggregate；
- people count；
- presence sensor；
- room booking；
- BAS occupied mode；

这些不是同一种事实。

必须区分：

```text
Scheduled occupied
Observed occupied
Occupied standby
Unoccupied
Unknown
```

如果只是 schedule，不要显示成“当前有 18 人”。

## 15.1 Privacy

默认展示运营所需的：

- occupancy state；
- aggregate count；
- utilization context。

不展示个人身份、轨迹或个人舒适偏好，除非另有合法授权和产品需求。

---

# 16. Violation Semantics

## 16.1 Current deviation

一个 zone 的当前偏离至少包含：

```text
Metric
Observed value
Target
Magnitude
Started at
Duration
Occupancy context
Quality
```

## 16.2 Violation Hours

`Violation hours` 必须说明口径：

```text
Occupied violation hours
All-hours violation
Scheduled occupied violation
```

默认优先 `Occupied violation hours`，但 safety / humidity / moisture 等 owner 可以定义 unoccupied 仍重要的 rule。

## 16.3 No hidden composite score

默认排序建议基于显式字段：

1. occupied / upcoming occupied；
2. severity / magnitude；
3. duration；
4. repeat；
5. complaint/work relation；
6. data confidence。

不使用不可解释的黑盒 score。

---

# 17. Compact Summary

Header 下只允许少量 operational summary，例如：

```text
Occupied zones: 84
Thermal attention: 7
IAQ attention: 3
Data unavailable: 4 zones
```

规则：

- 每个数字有 owner；
- unavailable 不能显示 0；
- thermal / IAQ 不合成一个 `Healthy Spaces %`；
- Summary 只辅助 scope，不变成 Dashboard。

---

# 18. Primary View Modes

推荐 peer views：

```text
Attention
Thermal
Air Quality
```

它们共享：

- Site；
- Floor；
- Zone；
- time range；
- occupancy filter；
- selected Zone。

## Attention

综合显示需要行动的 zone，但不计算隐藏 score。

## Thermal

强调 temperature / humidity / comfort-target evidence。

## Air Quality

强调 CO₂ / PM / other available IAQ dimensions。

如果站点没有 IAQ capability，不显示 `Air Quality` view。

---

# 19. Zone Ledger

默认是高密度、可扫描 Zone Table。

推荐核心列：

| 列 | 内容 |
|---|---|
| Zone | 名称 + floor/location |
| Occupancy | occupied state / aggregate count |
| Thermal | current thermal target status |
| Temperature | current value + deviation |
| Humidity | RH / dew point when available |
| Air Quality | concise measured dimensions |
| Duration | current / occupied violation duration |
| Serving HVAC | authoritative system/equipment |
| Data | freshness / quality |

默认保持约 7–9 个业务列。

## 19.1 Row semantics

单击行：

> select Zone → Inspector

Zone name / `打开空间`：

如果未来有 durable Space Detail，再进入；v1 没有该 Surface 时保持在 Inspector + professional exits。

---

# 20. Spatial / Floor View Policy

Floor map / zone heatmap 只有在以下条件满足时允许：

- authoritative floor/space geometry；
- stable zone identity；
- zone-to-sensor mapping；
- accessible non-map alternative。

禁止：

- 根据 zone name 拼平面图；
- 用固定矩形模拟真实空间；
- 没 geometry 也画装饰性“楼层热力图”；
- 颜色作为唯一状态编码。

没有 authoritative geometry 时，Zone Ledger 就是正式 primary view。

---

# 21. Selected Zone Inspector

Inspector 用于快速理解一个 zone，保持 Ledger / Map context。

## Identity

```text
Zone name
Floor / area
Space type
Serving system
```

## Occupancy

```text
Observed / scheduled occupancy state
Aggregate count when available
Source
```

## Thermal

```text
Temperature
Target band
Deviation
Humidity / dew point
Assessment type
```

## IAQ

只显示 available dimensions：

```text
CO₂
PM2.5
TVOC
...
```

每个 metric 有：

- target；
- source；
- quality；
- observation time。

## Duration

显示：

- current violation duration；
- selected-period occupied violation hours；
- recurrence count（若 owner 提供）。

## Serving HVAC

显示 authoritative：

- AHU / terminal / VAV / FCU；
- relevant devices；
- active mode / setpoint summary when owner supports。

## Professional exits

突出 1–3 个：

- 查看趋势；
- 打开系统运行；
- 打开诊断 / 工单。

Inspector 不承载完整 Trend / Diagnosis / Control。

---

# 22. Evidence Workspace

Selected Zone 下方或 Inspector deep section 可提供 evidence preview。

默认系列：

```text
Temperature
Target band
Occupancy
CO₂ / PM / humidity as relevant
```

如果同时有多个单位，遵循 05 Trend Analysis：

- same-unit overlay；
- different unit family → aligned small multiples；
- no default dual-Y-axis。

## Event lanes

可以叠加：

- occupied/unoccupied transition；
- target / schedule change；
- serving HVAC mode change；
- relevant alarm/finding/work/control event。

事件来自各自 owner。

`查看完整趋势` 进入 05，并保留 Zone / time / series / source context。

---

# 23. HVAC Contribution / Serving-System Contract

页面可以显示：

```text
Zone Z-1203
served by AHU-03
terminal VAV-3-17
```

可以显示同时发生的事实：

```text
Zone temp high
VAV damper 100%
AHU SAT 16.5°C
```

但不能直接宣称：

> `AHU-03 是根因`

根因属于 Diagnosis。

## 23.1 Missing relation

如果 serving relation 未建模：

> `服务系统未关联`

不能根据 zone name / point path 猜。

---

# 24. Complaint Correlation

只有有真实 complaint / workplace integration 时显示。

可以展示：

- complaint count；
- category；
- time；
- zone；
- resolution state；

默认使用 aggregate / operationally relevant information。

不要把个人姓名、联系方式、自由文本隐私信息直接铺在运营页。

## 24.1 Complaint != sensor truth

Complaint 是 occupant evidence。

```text
No complaint != comfortable
Complaint != sensor fault
```

二者都保留各自 provenance。

---

# 25. Target Administration Boundary

本页只读展示 target 和 provenance。

Target administration 属于：

- Site Configuration；
- Strategy / Schedule；
- Space/Comfort policy administration；

具体由后续产品 owner 定义。

本页不提供 inline：

```text
Edit target
Change setpoint
Change occupancy schedule
Override ventilation
```

避免把 investigation 与 control 混在一起。

---

# 26. Data Authority Contract

## Zone / Floor / Space Type

Owner：Space / Semantic Model。

## Serving HVAC relation

Owner：Semantic Model / Registry relation。

## Temperature / Humidity / Pollutants

Owner：Telemetry Snapshot / historian。

## Comfort operational target

Owner：Comfort Target / Site Policy domain。

## Standard 55 assessment

Owner：Comfort Assessment domain。

## Ventilation / 62.1 assessment

Owner：Ventilation / IAQ assessment domain。

## Occupancy

Owner：Occupancy domain；source type 必须明确。

## Alarm

Owner：Alarm domain。

## Finding

Owner：Diagnosis domain。

## Work

Owner：Work Order domain。

## Complaints

Owner：Workplace / complaint integration。

Frontend 只做 zone-centered projection。

---

# 27. Query / Read Model Contract

禁止每个 zone 逐个请求。

推荐：

```text
Zone Population Query
  + Batch Environmental Snapshot
  + Batch Occupancy Snapshot
  + Target Projection
  + Batch Serving-System Relation
  + Domain Attention Summary
```

或正式 page-level read model，但必须保留 provenance。

明确禁止：

```text
150 zones
→ 150 temperature requests
→ 150 occupancy requests
→ 150 alarm requests
→ 150 relationship requests
```

如果 owner 没有 batch/projection，优先修 owner contract，不在前端堆并发 workaround。

---

# 28. Snapshot / Stream Realtime Contract

实时语义：

```text
Snapshot = selected scope authoritative current state
Stream = authorized incremental measurements/events
```

订阅范围：

- current Site；
- current page / filter scope；
- selected Zone；
- relevant metric set。

Realtime update：

- 原位更新 values；
- 不抢 focus；
- 不关闭 Inspector；
- 不每个 tick 重排行；
- 不把 stream disconnect 解释成 sensor bad / zone safe；
- 不重置 evidence chart zoom。

Reconnect：

- 重新获取 authoritative Snapshot；
- 再恢复 Stream。

---

# 29. Loading / Empty / Partial / Error

## No Zone Model

> `当前站点尚未建立空间 / Zone 模型。`

有配置权限时提供 Semantic Model 管理入口。

## No Environmental Capability

该 Surface 不应该出现在导航中。

## No result after filters

> `没有空间符合当前筛选条件。`

提供 Clear Filters。

## Telemetry unavailable

Zone identity / target 仍可显示；measurement 显示 unavailable。

不能显示成 `Comfortable`。

## Occupancy unavailable

显示 `Occupancy unknown`。

Occupied violation hours 不得计算为 0。

## Comfort target unavailable

显示 measurement，但：

> `未配置舒适目标`

不能自行使用 22–24°C。

## IAQ sensor unavailable

只标该 metric unavailable。

不能暗示其他 IAQ dimensions normal。

## Serving relation unavailable

> `服务系统关系暂不可用`

不能猜。

## Standard assessment unavailable

运营 target view 可以继续；但不显示 Standard 55/62.1 compliance。

---

# 30. Permission / Capability

示例：

- 无 Occupancy read → 隐藏 occupancy-specific columns / counts；
- 无 IAQ capability → 不显示 Air Quality view；
- 无 Complaint permission → 不显示 complaints；
- 无 engineering metadata → 隐藏 assessment method / raw sensor metadata；
- 无 Work read → 不显示 work summary；
- 无 Diagnosis read → 不显示 finding；
- 无 Optimization read → 不显示 optimization exit。

没有权限的能力默认不做 disabled teaser。

---

# 31. Sorting / Prioritization

默认排序需要业务可解释。

推荐 Attention view：

```text
occupied first
→ higher deviation severity
→ longer duration
→ unresolved first
→ zone name
```

具体 severity 由 target owner 定义。

禁止：

- hidden ML priority score；
- generic health score；
- 根据 UI 颜色排序。

实时更新默认不持续重排行。

如果动态排序需要刷新，使用 explicit refresh / query update。

---

# 32. No Defensive Programming / No Compatibility Design

明确禁止：

```text
missing temperature → 0°C
missing occupancy → unoccupied
missing target → hardcode 22–24°C
CO₂ < 1000 ppm → IAQ healthy
CO₂ > threshold → ASHRAE 62.1 non-compliant
only temp/RH → ASHRAE 55 compliant
missing IAQ sensor → IAQ normal
stream disconnect → zone safe
bad-quality measurement → use last good silently
serving relation missing → infer AHU from zone name
complaint absent → comfortable
complaint exists → sensor fault
multiple target APIs → first success wins
one request per zone
old comfort-card page fallback
old room-dashboard compatibility adapter
universal ComfortScore abstraction owning semantics
```

不建立“新评估失败就降级成旧 Comfort Score”的兼容链。

正确原则：

> **One measurement → one owner. One target → one explicit policy. A proxy stays a proxy. A standard assessment is only a standard assessment when required inputs and method are authoritative. Unknown stays unknown.**

---

# 33. Component Mapping

```text
Page header                 → application layout
Time / view controls        → Select / Popover / Button group
Primary filters             → Select / Popover
Zone ledger                 → shadcn Table + TanStack Table
Status / deviation          → Badge + text + numeric difference
Active filters              → removable chips / Badge
Zone Inspector              → responsive aside / Sheet
Metric definition           → definition list / semantic text
Evidence chart              → dedicated ECharts feature component
Section disclosure          → Collapsible / Disclosure
Complaint summary           → semantic list
Professional exits          → Button / Link
Pagination                  → accessible pagination
```

避免：

- 每个 Zone 一个 Card；
- 巨型红绿热力卡片墙；
- 所有指标合成一个圆环 score；
- nested Card；
- 色彩作为唯一“舒适/不舒适”信号。

---

# 34. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 舒适与室内环境 · 凤凰中央冷站             今日 00:00–现在 · UTC+08:00       │
│ Target policy: Occupied Comfort v4                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ Occupied 84   Thermal attention 7   IAQ attention 3   Data unavailable 4    │
├──────────────────────────────────────────────────────────────────────────────┤
│ [Attention] [Thermal] [Air Quality]   [楼层: 全部] [Occupancy: Occupied]    │
│ [系统: 全部] [更多筛选 2]                                                  │
├──────────────────────────────────────────────────┬───────────────────────────┤
│ Zone       Occupancy Thermal Temp/RH  IAQ  Dur.  │ Z-1203 · 12F East        │
│ 12F East   18 people  High   27.1°C  CO₂  46m  │ Occupied · 18 people      │
│ 11F Conf A Occupied   OK     23.8°C  CO₂  —    │                           │
│ 10F West   Unknown    —      —       PM   —    │ Temperature 27.1°C        │
│ ...                                              │ Target 22–25°C            │
│                                                  │ +2.1°C · 46 min           │
│                                                  │ RH 58%                    │
│                                                  │ CO₂ 1080 ppm              │
│                                                  │                           │
│                                                  │ Assessment: Operational   │
│                                                  │ comfort target            │
│                                                  │ Not ASHRAE 55 assessment  │
│                                                  │                           │
│                                                  │ Served by AHU-03           │
│                                                  │ VAV-3-17                  │
│                                                  │                           │
│                                                  │ [查看趋势] [系统运行]      │
│                                                  │ [进入诊断]                 │
├──────────────────────────────────────────────────┴───────────────────────────┤
│ Evidence · Z-1203                                                           │
│ Temperature / Target  ───────────────────────────────────────────────────── │
│ CO₂                 ─────────────────────────────────────────────────────── │
│ Occupancy            ──■■■■■■■■■■■■■■──────────────────────────────────── │
│ 09:12 occupied · 09:18 temp > target · 09:31 VAV damper 100%              │
│                                                       [查看完整趋势]         │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责、密度和语义，不是 pixel specification。

---

# 35. Accessibility

必须：

- Zone Ledger 使用 semantic table；
- comfort / IAQ status 不只靠红绿；
- target 与 observed value 有文本；
- evidence chart 有 accessible description / structured data alternative；
- floor/spatial view 不是唯一导航方式；
- keyboard 可完成 filter / row select / Inspector / deep-link；
- Inspector / Sheet 遵循 focus management；
- realtime update 不抢 focus；
- dynamic sensor values 不高频 announce；
- unit 与 metric label 始终可读。

如果使用 map/heatmap，必须有等价 Zone Table。

---

# 36. Responsive Behavior

## 1440–1720 px

首屏看到：

- context / target policy；
- compact summary；
- primary view / filters；
- Zone Ledger；
- selected Zone Inspector。

Ledger 是主要扫描区。

## 1024–1439 px

- filters 可 wrap；
- Inspector 可变窄；
- secondary IAQ columns 进入 compact summary / Inspector。

## Around 768 px

必须仍能：

- 找到 zone；
- 查看 occupancy；
- 查看 thermal / IAQ deviation；
- 理解 target；
- 打开 Inspector；
- 进入 Trend / System Operations / Diagnosis。

策略：

- Zone Table 自己成为 accessible horizontal scroll region；
- Inspector 使用 Sheet；
- evidence small multiples stack；
- 不强制转成卡片墙；
- 不依赖 hover。

---

# 37. Browser Acceptance Criteria

## Target Truth

- target source / version 可获得；
- missing target 不硬编码默认范围；
- operational target 与 Standard 55/62.1 assessment 文字区分；
- 只有 temperature/RH 时不会显示 `ASHRAE 55 compliant`；
- CO₂ 不会显示 generic IAQ score / 62.1 compliance。

## Zone / Occupancy

- Zone identity 来自 Space/Semantic Model；
- scheduled occupancy 与 observed occupancy 区分；
- occupancy unavailable 不显示 unoccupied；
- Occupied violation hours 在 occupancy unavailable 时不显示 0。

## Thermal

- real zero / negative temperature（若有效）按真实值显示；
- missing 不转 0；
- stale / bad-quality 明确；
- temperature target deviation 与 full comfort assessment 分开；
- adaptive/standard method 只有 owner 提供时出现。

## IAQ

- CO₂ / PM / TVOC 等维度独立；
- 未测量维度不暗示 normal；
- CO₂ interpretation 显示 occupancy / target context；
- bad sensor quality 不继续给 confident status；
- no generic health score。

## Duration / Attention

- current deviation duration 正确；
- occupied violation duration 定义明确；
- default prioritization 可解释；
- realtime tick 不持续重排行。

## Serving HVAC

- relation 来自 Semantic Model；
- relation unavailable 不根据命名猜；
- `served by AHU-03` 不显示成 `root cause AHU-03`；
- System Operations / Device Detail deep-link 保留 Zone context。

## Evidence

- selected Zone evidence 有明确 time range；
- missing data 是 gap；
- occupancy / target change / HVAC event 对齐；
- full Trend deep-link 保留 series/time/source；
- no default dual-Y-axis。

## Error / Partial Availability

- Zone model missing / telemetry unavailable / target missing / occupancy unavailable / relation unavailable 分开；
- unavailable 不能显示成 0 / healthy / comfortable；
- partial domain failure 不阻塞其他可信事实。

## Responsive

1440–1720 px：

- Header / Summary / Ledger / Inspector 是单一工作区；
- 无 card wall；
- 无 page-level horizontal overflow。

Around 768 px：

- filter / table / Inspector / deep links 可操作；
- table scroll 在自身 region；
- evidence 可读；
- 无 hover-only interaction；
- 无 page-level horizontal overflow。

## Accessibility

- status 不只靠颜色；
- table keyboard / screen-reader structure 正确；
- map 非唯一表示；
- chart 有 text/data alternative；
- realtime 不抢 focus。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 comfort dashboard compatibility adapter；
- 无 hardcoded universal target；
- 无 frontend-generated ASHRAE compliance；
- 无 generic Comfort / IAQ Health score；
- 无 one-zone-one-request N+1；
- review scenario 无 runtime/network error。

---

# 38. Explicit Non-Goals

本页不是：

- medical diagnosis；
- occupational health compliance engine；
- WELL certification engine；
- automatic ASHRAE compliance report；
- room thermostat control UI；
- personal occupant app；
- lighting/acoustic platform unless capability exists；
- full HVAC trend workspace；
- full Diagnosis Center；
- direct Control Center；
- semantic geometry editor。

---

# 39. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Space / Zone identity owner 明确；
- comfort operational target owner 明确；
- Standard 55 assessment 与 simple temperature target 的语义边界已接受；
- CO₂ / IAQ non-composite policy 已接受；
- Occupancy source semantics 已定义；
- serving-HVAC relation owner 明确；
- environmental snapshot / historian 支持 batch projection；
- Zone Ledger + Inspector 是默认主结构；
- floor/spatial view 仅在 authoritative geometry 存在时启用；
- direct control 与 target administration 不在本页；
- 旧舒适/IAQ页面没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
