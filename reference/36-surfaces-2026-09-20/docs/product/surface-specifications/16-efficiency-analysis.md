# 16 效率分析 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `16 效率分析`  
> **Route intent：** `/sites/:siteId/efficiency`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `14-energy-analysis.md` → `15-demand-load-flexibility.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Efficiency Dashboard、旧 COP 页面、旧冷站能效页、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Thermal Load / Power / Flow / Temperature / Pressure / Equipment Rating / Performance Curve / Data Quality / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

效率分析的唯一核心任务是：

> **在明确 Measurement Boundary、Output Denominator、Operating Context 和数据有效性的前提下，判断 HVAC 系统、子系统或设备是否以合理能效完成当前负荷，并识别在公平可比条件下的效率偏离与改进线索。**

Efficiency Analysis 是 **load-normalized performance analysis workspace**，不是：

- Energy Analysis 的一个 COP Tab；
- 设备额定效率展示页；
- “绿色/红色健康分”页面；
- FDD Root Cause 页面；
- 直接控制页面；
- M&V savings 页面；
- 把任意功率除任意温差就叫效率的计算器；
- 把缺数据变成 0 的 KPI 墙。

用户离开本页前应该知道：

1. 当前分析对象的 Measurement Boundary 是什么；
2. 当前负荷/有用输出如何定义和计算；
3. 当前效率指标是否 Valid / Invalid / Inconclusive / Not Applicable；
4. 当前 COP / kW/RT / transport efficiency 等指标具体包含哪些功率与输出；
5. 当前 operating load / lift / weather / flow / mode 是否允许与历史或其他设备公平比较；
6. 哪个子系统/设备真正偏离了可比期望；
7. ΔT、tower approach、flow、pressure 等 supporting indicators 是否异常；
8. 数据质量是否足够支持判断；
9. 下一步应该进入 Trend、Diagnosis、Device、Opportunity、Control 还是 Data Quality。

---

# 2. 主要用户

## Primary

### HVAC / Plant Engineer

判断冷站、热源、泵、塔、AHU 等是否在当前工况下高效运行。

### Energy Engineer

做 load-normalized comparison、系统分解、part-load analysis 和长期 performance tracking。

### Controls Engineer

查看 staging、setpoint/reset、flow/pressure、tower/chiller interaction 对系统效率的影响。

## Secondary

- Energy Manager：理解低效率来自哪里，而不是只看到高能耗；
- Maintenance engineer：从 outlier 进入设备/工单；
- Diagnosis engineer：把效率偏离作为 Finding/Evidence 的候选输入；
- Optimization engineer：把可验证的效率问题转为 Opportunity；
- Management：读取经过 validity gating 的高层效率事实。

---

# 3. 外部最佳实践依据

## 3.1 AHRI 550/590 与 551/591 — Chiller Efficiency 必须绑定标准工况和 Part-load 方法

AHRI 550/590（I-P）与 551/591（SI）规范 water-chilling packages 的 performance rating，包括 full-load efficiency、IPLV、NPLV 和 operating-map performance。

来源：

- https://www.ahrinet.org/search-standards/ahri-550590-i-p-and-551591-si-performance-rating-water-chilling-and-heat-pump-water-heating-packages
- https://www.ahrinet.org/scholarships-education/education/contractors-and-specifiers/hvacr-equipmentcomponents/liquid-chillers

**本页采用：**

- Rated Full Load / IPLV / NPLV 是 rating/reference，不是 live operating metric；
- field efficiency 需要真实 load/output 与 power input；
- part-load 条件必须进入比较语义；
- 不同 operating conditions 下的 kW/RT 不应直接无条件排名。

## 3.2 DOE/FEMP — Full-load 与 Part-load Efficiency 需要分别理解

DOE/FEMP 对电制冷机的采购效率要求明确区分 full-load optimized 与 part-load optimized applications，并使用 full-load efficiency 与 IPLV 等不同指标。

来源：

- https://www.energy.gov/cmei/femp/purchasing-energy-efficient-electric-chillers
- https://www.energy.gov/cmei/femp/incorporate-minimum-efficiency-requirements-heating-and-cooling-products-federal

**本页采用：**

```text
Rated full-load efficiency
≠ field part-load efficiency
≠ plant efficiency
```

- 现场比较必须带 Load Fraction / operating point；
- 设备额定数据不能替代测量；
- procurement compliance 不等于当前运行高效。

## 3.3 DOE/LBNL Chilled-Water Plant Guidance — Plant Efficiency 是系统结果

LBNL/DOE 的 chilled-water plant guidance 强调 chiller、chilled-water pumps、condenser-water pumps、cooling-tower fans 的交互共同决定 plant performance；part-load staging 也会改变系统效率。

来源：

- https://datacenters.lbl.gov/sites/default/files/EDR_DesignGuidelines_CoolToolsChilledWater.pdf
- https://datacenters.lbl.gov/sites/default/files/Design%20Brief_Chiller%20Efficiency.pdf

**本页采用：**

```text
Chiller kW/RT
≠ Plant kW/RT
```

Plant boundary 如果包含 pumps/towers，就必须把这些功率计入 denominator/numerator contract。

## 3.4 DOE Better Plants — System-level Approach 优先于单设备优化

DOE Better Plants 对 process cooling 明确建议采用 system-level approach，并强调 compressor staging、variable flow、VFD、CHW/CW setpoint reset 与 free cooling 等系统级措施。

来源：

- https://betterbuildingssolutioncenter.energy.gov/better-plants/process-cooling-and-hvac

**本页采用：**

- Efficiency 页面优先看系统分解，不只排设备排行榜；
- staging / pump / tower / reset context 都属于 supporting evidence；
- “单机更省电”不一定等于“全系统更高效”。

## 3.5 DOE Pump/Fan System Assessment — 输配效率必须看系统和工况

DOE Pump Systems / Fan Systems 强调泵风系统应在实际系统要求、part-load、控制方式和 Best Efficiency Point 等条件下评估，不是只看 motor kW。

来源：

- https://www.energy.gov/cmei/ito/pump-systems
- https://www.energy.gov/cmei/ito/fan-systems
- https://betterbuildingssolutioncenter.energy.gov/better-plants/fans

**本页采用：**

- transport efficiency 必须有明确 output denominator；
- VFD speed / flow / pressure / airflow / static pressure context 必须可查看；
- pump/fan power 降低不自动等于系统服务效率提升。

## 3.6 DOE Cooling Guidance — Tower / Condenser Context 会影响 Chiller Efficiency

DOE 的高效冷却指导指出，较低 entering condenser-water temperature、合理 cooling-tower approach、VFD tower/pump operation 和 condenser-water reset 可影响 chilled-water plant energy performance。

来源：

- https://www.energy.gov/sites/default/files/2024-07/best-practice-guide-data-center-design_0.pdf
- https://www.energy.gov/cmei/femp/best-management-practice-10-cooling-tower-management

**本页采用：**

- tower approach 是 supporting performance metric，不是 whole-plant efficiency；
- wet-bulb、load、flow、水质/换热状态等都影响解释；
- 单独一个小 approach 不能证明系统整体最优。

## 3.7 DOE Building Energy Asset / Process Guidance — Variable Flow / Reset 应结合 Typical Operating Conditions

DOE 关于 chilled-water pumping 和 cooling optimization 的资料强调 variable flow、pressure reset、tower fan VFD、condenser pump VFD 等应围绕实际典型工况优化。

来源：

- https://buildingenergyscore.energy.gov/upgrade_guide/UG30_HVAC_Cooling_Chiller.pdf
- https://www.energy.gov/cmei/ito/measur

**本页采用：**

- 设计点不是唯一比较点；
- 低负荷工况不能拿满负荷 benchmark 机械判差；
- 现场效率判断必须允许 performance map / regression / owner-defined expected curve。

---

# 4. Efficiency Domain Vocabulary

## 4.1 Useful Output / Load

效率指标的分母或有用输出，例如：

```text
Cooling load: kWth / RT
Heating load: kWth
Airflow delivered: m³/s or cfm
Water flow delivered: L/s or gpm
Pressure/transport service: owner-defined hydraulic/air-side output
```

必须来自 authoritative load/output owner。

## 4.2 Power Input

效率指标的输入功率，例如：

```text
Chiller compressor kW
Pump kW
Tower fan kW
Plant total kW
AHU fan kW
```

必须明确 boundary。

## 4.3 COP

```text
COP = useful thermal output / energy input rate
```

通常为 dimensionless。

高值通常代表更高效率，但实际定义必须由 metric owner 明确。

## 4.4 kW/RT

```text
kW/RT = electric power input / refrigeration output in tons
```

低值通常代表更高效率。

必须说明是：

```text
Chiller-only kW/RT
Plant kW/RT
Subsystem kW/RT
```

## 4.5 ΔT

例如 chilled-water return minus supply temperature。

ΔT 是 heat-transfer / distribution / load context 指标。

它本身不是 efficiency。

## 4.6 Tower Approach

通常表达 cooling-tower cold/leaving-water temperature 与 ambient entering wet-bulb temperature 的差值，具体 sensor/location/method 由 metric owner 定义。

Tower Approach 是 tower heat-rejection context，不是 whole-plant efficiency。

## 4.7 Load Fraction

当前 load 相对设备/系统参考 capacity 的比例。

必须明确参考 capacity 来源和 revision。

## 4.8 Lift / Thermodynamic Context

例如 chilled-water leaving condition 与 condenser entering/leaving condition 共同形成的 chiller lift context。

用于解释同一机组在不同工况下的效率变化。

## 4.9 Expected Efficiency

由 performance map / approved model / benchmark owner 在给定 load、weather/lift 等 context 下计算的 expected performance。

不是前端临时平均值。

## 4.10 Efficiency Deviation

```text
Observed efficiency - Expected efficiency
```

在 owner-defined comparable context 下的差异。

Deviation ≠ Root Cause。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
High Energy = Low Efficiency
Low Power = High Efficiency
Chiller kW/RT = Plant kW/RT
Rated IPLV/NPLV = Live efficiency
Design efficiency = Current expected efficiency
Low ΔT = Low efficiency automatically
Low Tower Approach = Plant efficient automatically
High COP = Good operation regardless of load
No load = COP 0
Missing load = COP 0
Missing power = COP 0
Estimated load = Measured load
Calculated thermal load = Metered thermal load
Part-load comparison = Full-load comparison
Energy saving = Efficiency improvement
Efficiency deviation = Root Cause
```

正确关系：

```text
Valid Load / Output
+
Valid Input Power
+
Operating Context
+
Measurement Boundary
+
Data Quality
↓
Efficiency Metric
↓
Expected / Comparable Context
↓
Deviation / Outlier
↓
Investigation
```

---

# 6. Primary Questions

## Q1 — 当前系统是否在有效工况下高效运行？

显示：

- current/period efficiency；
- validity；
- measurement boundary；
- load；
- part-load fraction；
- operating mode；
- data quality。

## Q2 — 和什么比较？

支持：

- same asset at comparable load/lift；
- owner-defined expected performance curve；
- peer equipment under comparable conditions；
- previous comparable period；
- approved rated/reference performance。

不默认跨工况直接排名。

## Q3 — 哪个子系统拖累整体效率？

分解：

```text
Chillers
CHW pumps
CW pumps
Cooling towers
Air-side / AHU transport
Other owner-defined auxiliaries
```

## Q4 — 当前偏离是否和 load/lift/flow/temperature/pressure context 一致？

显示 supporting indicators 与 scatter/context。

## Q5 — 指标是否可信？

显示：

- output/load method；
- flow quality；
- temperature quality；
- power quality；
- interval alignment；
- coverage；
- low-load invalidity；
- meter lineage。

## Q6 — 下一步是什么？

进入：

- Trend；
- Device；
- Diagnosis；
- Opportunity；
- Control/Strategy；
- Data Quality。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/efficiency
```

推荐 Search Params：

```text
from
until
periodPreset
system
asset
metric
boundary
comparison
baselineVersion
loadBand
mode
selectedOutlier
selectedSubsystem
view          // overview | scatter | decomposition | distribution
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
- temporary legend highlight；
- disclosure state；
- unsaved analyst note。

---

# 8. Entry Contract

## 从 14 Energy Analysis

携带：

- Site；
- period；
- selected system/end-use；
- comparison；
- variance window（若适用）。

## 从 15 Demand Analysis

携带 selected peak/load window，用于判断高 demand 是否伴随低 efficiency。

## 从 System Operations

携带：

- system；
- equipment；
- operating mode；
- evidence window。

## 从 Device Detail / Diagnosis

携带 object + relevant metric + evidence window。

---

# 9. Exit Contract

主要出口：

```text
Efficiency
→ 05 Trend Analysis
→ 07 Device Detail
→ 10 Diagnosis
→ 21 Savings Opportunity
→ Control Center
→ Strategy Center
→ 31 Data Quality
→ 14 Energy Analysis
→ 15 Demand Analysis
```

保持 Site / Period / Object / Metric / Evidence Window。

---

# 10. Responsibility Boundary

Efficiency Analysis 拥有：

- metric context；
- observed efficiency projection；
- validity state；
- load-normalized comparison；
- subsystem decomposition；
- equipment outlier analysis；
- load-vs-efficiency scatter；
- supporting thermodynamic/hydraulic context；
- expected-performance projection；
- data-quality / method provenance；
- deep-link investigation context。

Efficiency Analysis 不拥有：

- raw meter configuration；
- performance-curve authoring；
- OEM certification；
- BAS control writes；
- root-cause confirmation；
- savings verification；
- frontend thermal-load calculation；
- arbitrary engineering formula builder。

---

# 11. Information Architecture

```text
Context Header
  Site · Period · System · Measurement Boundary

Analysis Controls
  Metric · Comparison · Load Band · Mode · Boundary

Efficiency Summary
  Observed Efficiency
  Validity
  Load / Part-load
  Expected / Comparable Performance
  Data Coverage

Primary Performance Workspace
  Load vs Efficiency Scatter / Time Trend
  Expected performance curve when available
  Selected deviation windows

Subsystem Decomposition
  Chiller
  CHW Pump
  CW Pump
  Tower
  Air-side / Other

Equipment Outliers
  Comparable equipment only
  Load/lift/mode context

Supporting Indicators
  ΔT
  Flow
  CHWS/CHWR
  CWS/CWR
  Tower approach
  Pressure / setpoint
  Staging
  Runtime / mode

Professional Detail
  Metric formula / boundary
  Load method
  Performance model/revision
  Sensor/meter lineage
  Validity rules
  Data quality
```

不是 12 张 KPI 卡片。

---

# 12. Metric Definition Contract

任何 efficiency metric 都必须拥有：

```text
Metric name
Definition
Numerator
Denominator / Useful Output
Measurement boundary
Units
Aggregation window
Validity prerequisites
Owner
Revision
```

示例：

```text
Plant kW/RT
Numerator:
  Chiller kW + CHW pump kW + CW pump kW + tower fan kW
Denominator:
  Authoritative plant cooling load in RT
```

如果 boundary 只含 chiller compressor：

必须命名：

```text
Chiller kW/RT
```

不能仍叫 Plant kW/RT。

---

# 13. Thermal Load Contract

Cooling/heating load 可以来自：

```text
Thermal energy meter
Validated flow × enthalpy/temperature method
Authoritative analytics model
OEM/plant controller load output
```

必须标记 provenance。

## Calculated Load

如果通过 flow + ΔT 推导：

owner 必须负责：

- fluid properties；
- glycol/concentration if applicable；
- sensor locations；
- calibration；
- time alignment；
- sign convention；
- unit conversion；
- quality propagation。

Frontend 不自己算。

---

# 14. COP Contract

COP 只有在：

- useful thermal output valid；
- input power valid；
- boundary明确；
- load高于 owner-defined minimum validity threshold；
- interval alignment valid；

时才显示数值。

否则显示：

```text
Not Applicable
Insufficient Data
Invalid Operating Condition
```

禁止：

```text
load missing → COP 0
power missing → COP 0
load near zero → huge COP shown as real efficiency
```

---

# 15. kW/RT Contract

必须区分：

```text
Chiller kW/RT
Plant kW/RT
Subsystem normalized kW/RT
```

### Chiller kW/RT

仅 chiller input power / chiller refrigeration output。

### Plant kW/RT

包含 owner-defined plant auxiliaries。

必须在 UI 中可查看 included components。

`0.55 kW/RT` 如果没有 boundary，不应显示为可信 KPI。

---

# 16. Rated Performance vs Field Performance

Rated metrics 可包括：

```text
Full-load rating
IPLV
NPLV
OEM operating map
```

Field metrics 是实际运行数据。

必须视觉标记：

```text
Rated Reference
Observed Field Performance
Expected Field Performance
```

禁止：

```text
Observed current kW/RT
vs
IPLV
→ direct pass/fail
```

除非 metric owner 明确说明该比较适用。

---

# 17. Part-load Context

效率比较必须显示：

```text
Load Fraction
Number of active machines
Staging state
Speed/loading
Operating mode
```

例如两台 chillers：

```text
CH-01 at 75% load
CH-02 at 28% load
```

不能只按 kW/RT 排名而忽略 loading。

---

# 18. Lift / Temperature Context

Chiller performance 受 evaporator/condenser temperature conditions 影响。

专业层可显示：

- CHWS leaving temperature；
- condenser-water entering temperature；
- condenser-water leaving temperature；
- ambient wet-bulb；
- owner-defined lift metric。

比较不同时间/设备时，应优先使用 load + lift comparable context。

前端不自行构造 thermodynamic lift formula，除非 owner已定义。

---

# 19. Expected Performance Contract

Expected efficiency 可来自：

```text
OEM operating map
Approved regression/model
Commissioning baseline
Validated peer curve
Owner-defined benchmark
```

必须显示：

- model/method；
- revision；
- required variables；
- validity domain；
- confidence/uncertainty semantics if owner provides。

如果 operating point 超出 model domain：

> `Expected performance model not applicable at this operating point`

不能外推后冒充可信 expected value。

---

# 20. Load vs Efficiency Scatter

这是本页核心专业视图之一。

X-axis：

```text
Load / Load Fraction
```

Y-axis：

```text
COP
or
kW/RT
or owner-defined efficiency metric
```

可编码：

- mode；
- equipment；
- lift band；
- date/window。

默认不同时编码五六个视觉维度。

Expected curve/region 只有 owner存在时显示。

Scatter outlier ≠ fault。

---

# 21. Time Trend Contract

时间趋势适合回答：

> 什么时候效率开始偏离？

默认：

- efficiency metric；
- load context；
- validity gaps；
- important mode/staging events。

不同单位仍遵守 05 small-multiple 规则。

完整时序调查进入 Trend Analysis。

---

# 22. Subsystem Decomposition Contract

Plant/System Efficiency 页面应能回答：

> 总输入功率都花在哪里？

例如：

```text
Chillers          68%
CHW Pumps         11%
CW Pumps           8%
Tower Fans         7%
Other              6%
```

但必须基于真实 measurement/aggregation boundary。

Decomposition 是 power/input breakdown，不自动等于“损失占比”。

---

# 23. Equipment Comparison Contract

设备横向比较必须满足 owner-defined comparability。

至少考虑：

- same equipment type；
- same metric boundary；
- similar load band；
- similar lift/weather conditions；
- same operating mode；
- sufficient quality/coverage。

否则显示：

```text
Not directly comparable
```

不要强行排名。

---

# 24. Outlier Contract

Outlier 可以是：

- statistically outside peer band；
- above expected kW/RT；
- below expected COP；
- persistent degradation；
- unusual input-power split。

必须显示 owner-defined method。

Outlier ≠ Finding ≠ Root Cause。

进入 Diagnosis 后才进入正式调查。

---

# 25. ΔT Contract

Chilled-water ΔT 必须明确：

```text
CHWR - CHWS
```

或 owner-defined orientation。

需要：

- both temperatures valid；
- sensors correctly mapped；
- timestamps aligned；
- system flowing / applicable state。

`Low ΔT` 可以提示：

- excess flow；
- coil/valve behavior；
- bypass；
- low load；
- sensor issue；
- other causes。

但页面不能直接宣布 Root Cause。

---

# 26. Tower Approach Contract

Tower Approach 只有在：

- tower/condenser-water loop active；
- leaving/cold-water temperature valid；
- wet-bulb valid；
- sensor/site mapping valid；
- comparable airflow/water-flow context；

时有效。

显示：

```text
Approach
Wet-bulb
Leaving condenser-water temp
Tower fan state/speed
CW flow when available
```

Approach 较低可能改善 chiller conditions，但可能增加 tower fan/pump energy。

因此：

```text
Lower Approach
≠ Lower Whole-Plant Energy automatically
```

---

# 27. Pump / Hydronic Transport Efficiency

Hydronic transport 指标必须 owner-defined。

可以包括：

```text
Pump kW / thermal load
Pump kW / flow
Wire-to-water efficiency
Hydraulic efficiency
Differential-pressure performance
```

不同指标不能混写。

必须显示：

- active pumps；
- flow；
- differential pressure；
- speed；
- valve/bypass context；
- required service/load。

---

# 28. Air-side Transport Efficiency

Air-side 可包括：

```text
Fan power / airflow
Specific Fan Power
Fan-system efficiency
Static-pressure context
```

具体定义由 owner。

显示：

- fan kW；
- airflow；
- static pressure；
- speed；
- duct/terminal demand context；
- reset/setpoint context。

低 fan power 如果同时没提供足够 airflow，不叫高效。

---

# 29. Transport Efficiency Boundary

必须坚持：

```text
Less transport power
≠ Better transport efficiency
```

只有在 required flow/airflow/service 得到满足时才有意义。

例如：

```text
Fan kW ↓ 20%
Airflow ↓ 35%
```

不能宣称 efficiency improvement，除非 metric owner的 output-normalized calculation支持。

---

# 30. Staging Efficiency Contract

多机系统要显示：

- active count；
- individual load；
- total load；
- total plant kW；
- plant kW/RT；
- staging transition；
- expected staging region if owner defined。

Staging event 可以成为 variance context。

页面不能自己改 staging；控制进入 Control/Strategy。

---

# 31. Setpoint / Reset Context

可以显示：

- CHWS setpoint；
- DP setpoint；
- condenser-water setpoint；
- SAT reset；
- static-pressure reset；
- strategy revision。

这些是 efficiency context，不自动成为 Root Cause。

---

# 32. Weather / Ambient Context

对 water-cooled plant：

- wet-bulb；
- dry-bulb；
- humidity。

对 air-cooled equipment：

- outdoor dry-bulb / entering-air condition。

Weather 是 comparability/context input。

Weather unavailable 时不偷偷使用 last-known weather。

---

# 33. Mode / Occupancy Context

设备/系统处在不同 mode 时不可盲目比较：

```text
Occupied cooling
Unoccupied setback
Warm-up
Free cooling
Economizer
Heat recovery
Defrost
Standby
```

只有 owner明确 mode mapping 才使用。

Unknown mode 保持 Unknown。

---

# 34. Validity Contract

每个 metric 至少支持：

```text
VALID
INVALID_DATA
INVALID_OPERATING_CONDITION
INSUFFICIENT_LOAD
NOT_APPLICABLE
INCONCLUSIVE
UNKNOWN
```

具体枚举可以由 owner定义，但 UI 不能只用 nullable number。

## 示例

```text
COP: —
Reason: Cooling load below valid threshold
```

比：

```text
COP: 0.00
```

正确得多。

---

# 35. Low-load / Near-zero Denominator Contract

所有 ratio metric 都必须有 denominator validity rule。

当 thermal load 接近零时：

- 不显示无限大 COP；
- 不显示异常巨大 kW/RT；
- 不强行进入 peer ranking；
- 显示 `Insufficient Load` 或 owner状态。

Validity threshold由 metric owner。

---

# 36. Sensor / Meter Alignment Contract

效率通常需要多源数据：

```text
Power
Flow
Supply temp
Return temp
Wet-bulb
Pressure
State
```

需要 owner 负责：

- timestamp alignment；
- aggregation window；
- latency；
- quality propagation；
- clock drift；
- missing interval；
- sensor revision。

Frontend 不做 `nearest sample wins` 来计算正式效率。

---

# 37. Data Quality Contract

至少区分：

```text
Good
Estimated
Missing
Stale
Suspect
Bad
Corrected
Unknown
```

Efficiency metric quality 必须由其 input quality传播或 owner计算。

不能：

```text
flow bad
+ temperatures good
→ COP good
```

---

# 38. Efficiency Period Aggregation Contract

必须区分：

```text
Instantaneous / interval efficiency
Load-weighted period efficiency
Energy-weighted period efficiency
Simple arithmetic mean
```

不能默认把 interval COP 简单平均成 monthly COP。

Period efficiency method由 owner定义。

如果 owner没有 period aggregate，UI 不自己算。

---

# 39. Comparison Contract

允许：

```text
Current vs comparable previous period
Current vs same load band historical
Current vs expected curve
Peer asset at similar operating point
Before vs after corrective action
```

必须显示 comparison context。

不允许：

```text
CH-01 30% load
vs
CH-02 85% load
→ winner ranking
```

除非 expected/model normalization解决了 comparability。

---

# 40. Efficiency vs Energy Boundary

14 Energy Analysis 回答：

> 用了多少 Energy？

16 Efficiency 回答：

> 为完成当前 useful output，用得是否合理？

所以：

```text
High Energy
≠ Low Efficiency
```

也可能：

```text
High Energy
+ High Efficiency
```

只是因为负荷很高。

---

# 41. Efficiency vs Demand Boundary

15 Demand Analysis 回答：

> Peak / load shape / flexibility 是什么？

16 Efficiency 回答：

> 在那个 load 下系统有多高效？

Peak period 可以 deep-link 到 Efficiency with same window。

Demand reduction 不自动等于 efficiency improvement。

---

# 42. Efficiency vs Diagnosis Boundary

Efficiency 偏离可以产生：

```text
Investigation candidate
```

但不能直接产生：

```text
Confirmed fault / Root Cause
```

Diagnosis owner负责 Finding / Hypothesis / Root Cause。

---

# 43. Efficiency vs Opportunity Boundary

Efficiency gap 可以成为 Opportunity 的证据，但 Opportunity 需要：

- proposed measure；
- engineering rationale；
- expected impact；
- constraints；
- confidence；
- cost/savings evaluation。

16 不自动把所有 outlier 转成 savings。

---

# 44. Efficiency vs M&V Boundary

Observed efficiency improvement：

```text
0.78 → 0.62 kW/RT
```

不等于：

```text
Verified energy savings
```

M&V 仍需 baseline/reporting period/adjustments/boundary。

---

# 45. Data Authority Contract

## Thermal load

Owner：Thermal Meter / Plant Analytics。

## Electric power

Owner：Meter / Telemetry / Historian。

## Flow / temperature / pressure

Owner：Telemetry / Historian / Data Quality。

## Equipment rating / performance map

Owner：Asset Engineering / OEM Data domain。

## Expected performance model

Owner：Efficiency Analytics / Commissioning domain。

## Mode / staging / setpoint

Owner：Operations / Control domain。

## Efficiency metric / validity

Owner：Efficiency Analytics domain。

## Findings

Owner：Diagnosis domain。

Frontend 只做 efficiency-centered projection。

---

# 46. Query / Read Model Contract

推荐：

```text
Efficiency Analysis Projection
  + metric definition/boundary
  + current/period efficiency
  + validity
  + load/part-load context
  + expected performance
  + load-vs-efficiency series
  + subsystem power decomposition
  + equipment comparable set
  + supporting indicators
  + data quality / lineage
```

禁止：

```text
30 assets
→ 30 power queries
→ 30 flow queries
→ 60 temp queries
→ 30 rating queries
```

缺 read model / batch contract 时修 domain，不在前端 fan-out。

---

# 47. Loading / Empty / Partial / Error

## No applicable load

> `当前工况不适用于该效率指标。`

不是 `COP 0`。

## Thermal load unavailable

Power仍可显示作为 context，但 efficiency 不显示数值。

> `效率暂不可计算：负荷数据不可用。`

## Power unavailable

同理，不显示效率。

## Supporting indicator unavailable

主 metric 如果 owner确认仍有效可保留；missing supporting indicator 明确 unavailable。

## Expected model unavailable

Observed efficiency仍可显示；comparison unavailable。

不能 fallback 到 rated IPLV/NPLV。

## Data quality unavailable

不能默认标 Good。

---

# 48. Permission / Capability Gating

示例：

- `efficiency.read` → main analysis；
- `efficiency.engineering.read` → formula/boundary/model detail；
- `asset.rating.read` → OEM/rated reference；
- `trend.read` → evidence deep-link；
- `diagnosis.read/create` → investigation handoff；
- `opportunity.create` → Opportunity handoff；
- `control.read` → operating context；
- `control.execute` → only Control Center。

---

# 49. Export Contract

Export 保留：

```text
Site
Period/timezone
System/asset
Metric definition
Boundary
Observed value
Validity
Load/output
Input power
Part-load/lift context
Expected/model revision
Supporting indicators
Data quality
Source/provenance
Export timestamp
```

不能只导出：

```text
CH-01, 0.62
```

而丢失 kW/RT / boundary / load context。

---

# 50. Visual / UX Contract

默认视觉层级：

```text
Efficiency + Validity + Load
↓
Load-vs-Efficiency / Time Performance
↓
Subsystem Decomposition
↓
Comparable Equipment Outliers
↓
Supporting Indicators
↓
Method / Boundary / Quality
```

禁止：

- Efficiency Health 92；
- 全站设备红绿排行榜；
- `COP 0` 表示无数据；
- 大型 gauge；
- 只显示效率、不显示 load；
- 默认双 Y 轴堆所有变量；
- 把 rated value 画成“必须达到”的红线而不说明工况。

---

# 51. Chart Contract

## Load vs Efficiency Scatter

核心专业视图。

## Time Performance

识别 degradation / mode transition / corrective-action window。

## Subsystem Input Breakdown

优先 horizontal bars / stacked bars，清楚标 boundary。

## Comparable Asset Plot

只展示 comparability-valid set。

## Supporting Indicators

不同单位使用 aligned small multiples。

---

# 52. ECharts Boundary

ECharts 可以负责：

- scatter；
- time trend；
- expected region/curve；
- subsystem bars；
- selected outlier highlight；
- synchronized cursor；
- zoom。

ECharts 不负责：

- COP/kWRT calculation；
- thermal load calculation；
- validity；
- equipment comparability；
- performance model；
- rated-performance interpretation；
- Root Cause inference。

---

# 53. Component Mapping

```text
Context controls             → Select / Popover / Calendar
Metric selector              → Select
Boundary indicator           → compact semantic facts
Validity                     → Badge + explanatory text
Primary scatter/trend        → dedicated ECharts
Subsystem decomposition      → bars + Table
Equipment outliers           → TanStack Table
Supporting indicators        → semantic facts / small charts
Method/model detail          → Collapsible / definition list
Data quality                 → status + deep-link
Export                       → Dropdown Menu
```

避免 universal engineering metric builder。

---

# 54. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 效率分析 · 中央冷站                                          站点时区 UTC-07:00 │
│ [9月1–14日] [冷站 kW/RT] [全部负荷区间] [期望曲线 v4]                      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 冷站 0.67 kW/RT · 有效   负荷 1,420 RT（71%）   期望 0.59–0.63             │
│ 计量边界：冷水机组 + 冷冻水泵 + 冷却水泵 + 冷却塔   数据覆盖率 98.9%      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 负荷与效率                                                                  │
│ kW/RT                                                                        │
│ 0.9       • 偏离点                                                          │
│ 0.8    •                                                                    │
│ 0.7           ● 当前                                                        │
│ 0.6  ───────── 期望区间 ─────────────────────                               │
│ 0.5                                                                         │
│       20%      40%      60%      80%      100% 负荷                        │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ 输入功率构成                         │ 辅助工程指标                          │
│ 冷水机组      68%                    │ 冷冻水 ΔT       4.2°C                 │
│ 冷冻水泵      11%                    │ 冷却塔逼近温差   4.8°C                 │
│ 冷却水泵       8%                    │ 室外湿球温度    25.1°C                 │
│ 冷却塔风机     7%                    │ 运行冷机台数       3                   │
│ 其他           6%                    │ 冷冻水压差       96 kPa                │
├──────────────────────────────────────┴───────────────────────────────────────┤
│ 可比工况偏离设备                                                            │
│ CH-03 · 0.74 kW/RT · 68%负荷 · 高于期望区间                               │
│ [趋势] [设备] [诊断] [节能机会] [数据质量]                                 │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责、工况和证据层级，不是 pixel specification。

---

# 55. Accessibility

必须：

- efficiency value 同时有 unit / direction semantics；
- validity 不只靠颜色；
- scatter 有 table/text alternative；
- expected region 不只靠颜色；
- exact points keyboard 可达；
- outlier list 使用 semantic Table；
- ratio metric invalid reason 可由读屏获得；
- charts 不依赖 hover；
- mobile 保留 method/validity information。

---

# 56. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- metric / boundary；
- observed efficiency；
- validity；
- load/part-load；
- primary performance chart；
- subsystem decomposition；
- supporting indicators / outlier。

## 1024–1439 px

- decomposition 与 indicators 上下排列；
- scatter 保持主视觉区域；
- method metadata 下沉。

## Around 768 px

仍必须能：

- 选择 metric/system；
- 看 efficiency + validity + load；
- 看 performance trend/scatter；
- 看 subsystem/outlier；
- 打开 Trend/Diagnosis/Data Quality。

无 page-level horizontal overflow；Table 自身 scroll。

---

# 57. No Defensive Programming / No Compatibility Design

明确禁止：

```text
efficiency API error → COP 0
missing thermal load → COP 0
missing power → COP 0
near-zero load → huge COP/kWRT displayed
bad flow sensor → use last-good silently
bad temperature sensor → still mark metric GOOD
missing load method → calculate in frontend
power samples + flow/temp → frontend build authoritative COP
rated IPLV → current expected efficiency fallback
expected model unavailable → use design efficiency silently
load fraction missing → assume 100%
capacity missing → use nameplate string parser
mode unavailable → assume normal cooling
weather unavailable → use last weather
low ΔT → root cause “bypass”
low tower approach → declare plant efficient
high energy → low efficiency
low power → high efficiency
chiller kWRT → label Plant kWRT
subsystem missing power → treat as 0 contribution
peer comparison with different load/lift → rank anyway
invalid metric → coerce to zero
multiple efficiency APIs → first success wins
one asset → one power/load/rating/quality request N+1
old COP Dashboard adapter
old efficiency page fallback
frontend-generated expected curve
frontend-generated validity
frontend-generated root cause
```

不建立：

```text
new Efficiency Analysis unavailable
→ fallback old efficiency dashboard
```

原则：

> **One efficiency metric → one authoritative definition and boundary. Useful output, input power, operating context and validity are inseparable. Rated performance is not live performance. High energy is not low efficiency. Invalid is not zero. Unknown stays unknown.**

---

# 58. Browser Acceptance Criteria

## Metric semantics

- metric name/unit/boundary 可见；
- Chiller vs Plant kW/RT 分开；
- COP direction semantics 清楚；
- rated vs observed vs expected 分开。

## Validity

- missing/near-zero load 不显示 0；
- invalid operating state有 reason；
- bad quality传播到 metric；
- Not Applicable / Insufficient Data 与 zero 分开。

## Comparability

- load fraction 可见；
- lift/weather/mode context 可查看；
- incomparable assets不强制排名；
- expected model超出 domain 不外推冒充。

## System boundary

- Plant metric列出 included components；
- subsystem power missing 不当 0；
- decomposition总和与 boundary一致；
- chiller-only metric不冒充 plant metric。

## Supporting indicators

- ΔT不是 efficiency；
- Tower Approach不是 whole-plant efficiency；
- pump/fan transport metric有 output denominator；
- staging/setpoint只是 context，除非 Diagnosis owner有 finding。

## Data quality

- flow/temp/power quality 可追溯；
- alignment/aggregation method 可查；
- no last-good silent fallback；
- no frontend-derived authoritative thermal load。

## Boundaries

- Energy → 14；
- Demand → 15；
- Diagnosis → 10；
- Opportunity → 21；
- control writes → Control Center；
- M&V 独立。

## Responsive / Accessibility

- 1440–1720px 是 coherent efficiency workspace；
- around 768px 核心任务完整；
- no color-only validity/outlier；
- exact values不依赖hover；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 old COP/Efficiency Dashboard compatibility adapter；
- 无 frontend-generated COP/load/expected curve/validity；
- 无 N+1 asset-performance queries；
- 无 invalid→0 fallback；
- review scenario 无 runtime/network error。

---

# 59. Explicit Non-Goals

本页不是：

- chiller certification tool；
- OEM selection tool；
- generic thermodynamics calculator；
- root-cause diagnosis page；
- direct control page；
- M&V savings page；
- equipment procurement compliance page；
- arbitrary formula builder；
- Energy/Demand dashboard replacement。

---

# 60. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- useful output / input power / boundary contract 已明确；
- COP / kWRT validity contract 已接受；
- Chiller / Plant boundary 已分离；
- Rated / Observed / Expected performance 已分离；
- part-load / lift / mode comparability 已明确；
- ΔT / Tower Approach / transport metrics 不再冒充 overall efficiency；
- low-load / missing-data validity 已明确；
- subsystem decomposition / equipment outlier contract 已明确；
- Efficiency / Energy / Demand / Diagnosis / M&V 边界明确；
- old COP/Efficiency Dashboard 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
