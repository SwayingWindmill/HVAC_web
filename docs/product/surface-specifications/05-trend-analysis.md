# 05 趋势分析 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `05 趋势分析`  
> **Route intent：** `/sites/:siteId/trends`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有趋势页、Monitor trend、历史图表页、旧设计稿、旧 Ant/AntV 图表布局或实现导出的行为。当前代码仅可在实施阶段作为真实 historian owner、capability、permission、route contract 与 transport contract 的候选证据来源。

---

# 1. Primary Job

趋势分析的唯一核心任务是：

> **让运营人员和工程师选择少量有意义且可信的变量，在明确的 evidence window 中观察其时间关系，并把 authoritative Alarm / Finding / Work / Control / Mode / Verification 事件与时序证据对齐，从而继续进入正确的调查或行动页面。**

Trend Analysis 是 **evidence workspace**，不是：

- Dashboard；
- 通用 BI；
- 任意点位绘图器；
- “20 条曲线”工程垃圾场；
- FDD / 根因诊断引擎；
- 告警规则编辑器；
- 控制台；
- M&V 节能量计算器；
- Device Detail 的替代品；
- Data Quality 的替代品。

用户离开本页前应该已经知道：

1. 什么变量在什么时间发生了变化；
2. 哪些变量的变化在时间上可能有关联；
3. 这些数据是否完整、fresh、quality 是否可信；
4. 同一 evidence window 内发生了哪些 authoritative 事件；
5. 当前证据是否足以继续进入 Device、Alarm、Diagnosis、Control、Verification、Energy 或 Efficiency；
6. 当前看到的是 raw、aggregated 还是 comparison data。

趋势可以支持假设，但**不能仅凭曲线同时变化就宣称因果关系**。

---

# 2. 主要用户

## Primary

### HVAC 运行工程师

调查 mode、stage、setpoint、过程量、功率、阀位、流量、温差等随时间的关系。

### HVAC 控制 / 调试工程师

调查 command / readback、stage transition、reset、override、schedule 和 sequence evidence。

### 诊断 / 维修 / commissioning 工程师

调查 intermittent fault、corrective action 前后、retest 与 verification evidence。

## Secondary

- 能源工程师：调查 load profile、runtime、power、efficiency opportunity 的时序证据；
- 告警值班人员：从 Alarm 带 evidence window 进入并理解告警前后发生了什么；
- 数据工程师：从异常曲线进入 Data Quality 进一步确认 historian/quality 问题。

## 不作为主要目标用户

- 企业管理层：应使用 Portfolio / Site Overview；
- 普通维修执行人员：默认使用 Work Order / Device Detail；
- 需要直接写控制的人：进入 Control Center / Strategy Detail。

默认界面保持专业但易读；engineering metadata 通过 progressive disclosure 展开。

---

# 3. 外部最佳实践依据

## 3.1 DOE / FEMP EMIS — Trend 是分析证据，不替代 Diagnosis / M&V / Control

DOE 将 EMIS capability 分成 interval-meter analytics、AFDD、M&V、supervisory control、O&M optimization 等互相连接但职责不同的能力。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

**本页采用：**

- Trend 负责历史时序证据；
- Alarm 由 Alarm owner 提供；
- Finding 由 Diagnosis owner 提供；
- Savings / Baseline 由 Energy / M&V owner 提供；
- Control execution / readback 由 Control owner 提供；
- 前端仅把事实对齐到同一时间轴，不重新定义它们。

## 3.2 DOE Monitoring-Based Commissioning — 时序数据需要与运行事件和 corrective action 对齐

DOE 的 MBCx / EMIS 实践强调持续使用 BAS、meter 和 contextual data 发现运行问题、实施 corrective action，并验证改善是否持续。

来源：

- https://www.energy.gov/cmei/femp/articles/enhancing-performance-contracts-monitoring-based-commissioning
- https://betterbuildingssolutioncenter.energy.gov/smart-energy-analytics-guidance-reports

**本页采用：**

- Work / Verification 事件可以作为 event lane；
- corrective action 前后允许明确 comparison；
- Trend 本身不把“改善”升级成 M&V savings claim；
- 如果需要确认 sequence 是否真的恢复，继续进入 Functional Verification。

## 3.3 ASHRAE BACnet / Trend Log — 时序证据是 typed object 的 timestamped history

BACnet 标准体系支持 Trend Log 等历史对象，并对 building automation information 进行 typed/object-oriented 表达。

来源：

- https://data.ashrae.org/bacnet/
- https://www.ashrae.org/technical-resources/technical-faqs/question-51-what-is-bacnet

**本页采用：**

- Analog、Binary、Enumerated、Setpoint / Command 不使用同一种视觉语法；
- timestamp、quality、unit 和 object semantics 都是 evidence 的组成部分；
- 不把离散状态平滑成连续模拟曲线。

## 3.4 Schneider EcoStruxure Building Operation — Trend chart 应明确时间范围并与事件/对象持续下钻

成熟 BMS 趋势能力包括 trend log、trend chart、历史范围、live mode 与多趋势比较，并与对象、报警和其他视图相互连接。

来源：

- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=5797&locale=en-US&productversion=2.1
- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=7939&locale=en-US&productversion=7.1

**本页采用：**

- Trend 是 durable investigation surface；
- object / alarm / event 可以继续下钻；
- live 与 historical 是同一证据模型的两种时间模式，而不是两套页面。

## 3.5 Siemens Building X Operations Manager — 实时运行与历史分析连续，但职责分离

公开能力将实时设备可视、historical chart、fault investigation、work order 和 remote control 连接成连续工作流。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- System Operations 可进入 Trend；
- Trend 可返回 Equipment / Alarm / Diagnosis；
- 不在 Trend 页面复制完整实时 HMI 或控制功能。

## 3.6 UK ONS / Government Analysis Function — 避免误导性 dual-axis chart

ONS 的官方数据可视化指南明确建议避免 dual-axis chart，因为不同轴范围和缩放容易制造误导性关系。

来源：

- https://service-manual.ons.gov.uk/data-visualisation/guidance/axes-and-gridlines

**本页采用：**

- 默认禁止双 Y 轴；
- 不同单位使用共享时间轴的 aligned small multiples；
- 同单位、同量纲才优先 overlay；
- 如果未来确有 validated dual-axis use case，必须单独 ADR / product decision，而不是自由配置。

## 3.7 W3C / WCAG — 不能只靠颜色表达系列、质量和状态

W3C WCAG 要求 color 不作为表达信息的唯一视觉手段。

来源：

- https://www.w3.org/WAI/WCAG22/Understanding/use-of-color

**本页采用：**

- series identity 不只靠颜色；
- quality 不只靠红绿；
- exact values 不依赖鼠标 hover；
- 关键 evidence 必须有 structured table / textual summary alternative。

---

# 4. Primary Questions

用户进入后按以下顺序回答。

## Q1 — 我正在调查什么？

必须清楚显示：

- Site；
- source trail（若来自 Alarm / Diagnosis / Work / Verification）；
- current system / device；
- time range；
- Site timezone；
- live / historical intent。

## Q2 — 我选了哪些变量，这些变量是否可信？

必须看到：

- business-readable label；
- unit；
- source / historian；
- quality / freshness；
- sample / aggregation semantics。

## Q3 — 这些变量在时间上怎样变化？

回答：

- change timing；
- range；
- peaks / valleys；
- step transition；
- missing / stale intervals；
- 同单位变量之间的相对关系。

## Q4 — 同一时间还发生了什么？

通过 authoritative event lanes 回答：

- Alarm；
- Finding；
- Mode / Stage；
- Control / Strategy；
- Work；
- Verification。

## Q5 — 下一步该去哪？

进入事实 owner：

- Device Detail；
- System Operations；
- Alarm；
- Diagnosis；
- Work Order；
- Functional Verification；
- Control / Strategy；
- Data Quality；
- Energy / Efficiency。

---

# 5. Route 与 URL State

Canonical route：

```text
/sites/:siteId/trends
```

Durable identity 放 Path；可恢复分析状态放 TanStack Router Search Params。

推荐 Search Params：

```text
timeStart
timeEnd
timeMode        // absolute | relative | live
series          // canonical point references
compare         // none | previous-period | previous-day | previous-week | custom
compareStart
compareEnd
system
asset
eventTypes      // alarm,finding,work,control,mode,verification
selectedEvent
layout          // overlay | small-multiples
aggregation     // owner-supported explicit aggregation
```

不要因为 UI 有一个 control 就增加 Search Param。

Local-only state：

- tooltip visible；
- transient hover；
- cursor pixel position；
- accordion state；
- legend hover highlight；
- panel drag height；
- unsaved annotation draft。

原则：

> 刷新、复制链接、开新标签后，用户合理期待仍存在的分析状态，才进入 URL。

---

# 6. Entry Contract

## 6.1 从 System Operations

携带：

- Site；
- System；
- selected equipment；
- evidence window；
- domain owner 提供的 recommended points。

禁止自动把该设备的所有 points 全选。

## 6.2 从 Device Detail

携带：

- Site；
- Device；
- selected metric / point（若适用）；
- current evidence window。

## 6.3 从 Alarm Center

携带：

- Alarm source；
- physical condition start / recovery；
- relevant object；
- evidence padding。

Alarm physical-condition marker 必须可见。

ACK 不能被当成 Alarm recovery marker。

## 6.4 从 Diagnosis Center

携带：

- Finding source；
- Finding evidence window；
- supporting point references；
- contradicting evidence（若 owner 提供）。

## 6.5 从 Work Order / Functional Verification

携带：

- before / after evidence window；
- relevant points；
- corrective action / execution event；
- retest / verification event。

## 6.6 从 Energy / Efficiency

携带：

- Site；
- time range；
- object / system；
- metric；
- comparison definition。

不能偷偷替换来源页面的 comparison 语义。

---

# 7. Exit Contract

根据当前 context 提供专业出口：

- System Operations；
- Device Detail；
- Alarm Center；
- Diagnosis Center；
- Work Order；
- Functional Verification；
- Data Quality；
- Energy Analysis；
- Efficiency Analysis；
- Control Center / Strategy Detail（有权限时）。

保持兼容的：

```text
Site
Time Range
Object
Source
Comparison
Evidence Window
```

---

# 8. Responsibility Boundary

Trend Analysis 拥有：

- time-window selection；
- series composition；
- time-series rendering；
- synchronized cursor；
- explicit aggregation presentation；
- authoritative event-lane composition；
- quality / missing-data visualization；
- exact-value inspection；
- historical comparison；
- capability-gated export。

Trend Analysis 不拥有：

- Alarm ACK / Assign；
- Diagnosis conclusion；
- Work Order mutation；
- control write；
- strategy publish；
- M&V savings claim；
- synthetic telemetry；
- domain KPI calculation；
- causal inference。

---

# 9. Information Architecture

```text
Context Header
  Site · source trail · object/system context
  Time range · timezone · live state

Analysis Toolbar
  Series
  Time
  Comparison
  Event tracks
  View
  Export / Save View when supported

Series Summary Strip
  Active series · units · quality/source state · aggregation

Primary Trend Workspace
  Shared Time Axis
  Same-unit Overlay OR aligned Small Multiples
  Event / Evidence Lanes

Cursor / Selection Inspector
  Timestamp
  Exact values
  Units
  Quality/freshness
  Aggregation/source metadata
  Nearby events

Evidence Summary / Data Table
  Selected-window statistics
  Exact records
  Missing/quality coverage

Professional exits
```

Chart 是主要工作区，但不是 evidence 的唯一表达形式。

---

# 10. Series Selection

禁止把全站 points 做成一个平铺 dropdown。

Picker 按层级组织：

```text
Current Context
  Recommended operational points
  Selected object
  Current system

Browse
  System
  Equipment
  Semantic role / measurement type

Search
  Human-readable point name
  Equipment name
  Semantic role
```

新调查默认从 **1–4 个有意义 series** 开始。

如果需要更多：

- 同单位 signal 可在可读范围内 overlay；
- 不同 unit family 使用 aligned small multiples；
- 过多 series 应按 equipment / semantic role 分组，而不是继续堆线。

Recommended series 只能来自 explicit metadata / domain-owned recommendation contract，不根据 point name 猜。

每个 series 必须可查看：

- business-readable label；
- equipment / system context；
- semantic role（若 known）；
- unit；
- historian / source；
- quality availability；
- sample / log semantics（若 available）。

---

# 11. Units / Axes / Composition

## 11.1 X 轴

Time 永远是主要 horizontal axis。

## 11.2 Same Unit Family

允许 overlay。

例如：

```text
CHWS vs CHWR temperature
Static pressure vs static-pressure setpoint
CH-01 kW vs CH-02 kW
```

## 11.3 Different Unit Families

默认使用 aligned small multiples：

```text
Temperature (°C)
Valve position (%)
Power (kW)
Flow (m³/h)
```

共享：

- time range；
- synchronized cursor；
- synchronized zoom；
- event markers。

## 11.4 Dual Y-Axis Policy

**默认不使用双 Y 轴。**

如果未来存在经过验证的专业 use case，必须新增明确 product decision / ADR。

不能把 dual axis 作为用户自由配置项。

## 11.5 Axis Label

单位必须在 panel / axis 上明确出现：

```text
Temperature (°C)
Power (kW)
Differential pressure (kPa)
Valve position (%)
```

不能只在 tooltip 里显示 unit。

## 11.6 Scaling

禁止：

- hidden log scale；
- silent normalization；
- 为制造视觉相关性任意调整多个 panel scale。

同 quantity 的 small multiples 默认使用 consistent scale，除非明确标识不同 scale。

Setpoint / target / threshold 必须文字标识，不能仅靠颜色。

---

# 12. Signal Type Rendering

## Analog

按 owner 记录的 sample 画线。

不制造不存在的 sample。

## Binary

使用 step / digital / state-lane representation。

禁止 smooth interpolation。

## Enumerated

使用 discrete state band / step lane，并显示 human-readable state label。

## Command / Setpoint

如果 source semantics 是 discrete write / held value，使用 step representation。

Command 与 Readback 保持不同 series / state。

---

# 13. Time Model

## 13.1 Timezone

默认采用 Site operational timezone，并在 header 明确显示。

禁止混用：

- site-local；
- browser-local；
- UTC。

## 13.2 Window Modes

```text
Absolute evidence window
Relative historical window
Live-follow window
```

## 13.3 Evidence Preservation

从 Alarm / Diagnosis / Work / Verification 进入时保留来源 evidence window。

不能重置成 `Today`。

## 13.4 Zoom

Zoom 修改 visible analytical window。

需要作为 durable investigation range 的时间范围才更新 URL；drag 中的 pixel state 不进入 URL。

## 13.5 Live Follow

Live follow 必须显式。

如果用户 pan / zoom 离开 live edge：

> **自动 pause follow。**

Realtime 更新不能：

- 移动 cursor；
- 重置 zoom；
- 重排 legend；
- steal focus；
- 重建整个 chart workspace。

---

# 14. Sampling / Aggregation / Downsampling

禁止 silent aggregation。

必须显示当前 resolution，例如：

```text
Raw samples
1-minute samples
15-minute mean
15-minute min / max / mean
Hourly total
```

Aggregation 属于 historian / time-series owner 或明确 analytics owner。

不能前端抓一个月高频数据后静默 average。

长时间范围降采样时，若 operational extremes 有意义，就不能把 peak 完全平均掉。

Derived / aggregated series 必须显示 method：

```text
mean
sum
delta
min/max envelope
rate
normalized value
```

---

# 15. Missing / Stale / Bad Quality

## Missing

形成可见 **gap**。

禁止跨缺测段连接成连续事实。

## Stale

在 series state、cursor 和 table 明确表示。

不能转成 `0` 或“当前值”。

## Bad / Suspect

必须与 good sample 可区分。

不能仅用颜色；可同时使用：

- marker shape；
- line treatment；
- text state；
- quality lane。

## Invalid / NaN

绝不能转换为 0。

## Coverage

如果 owner 提供，可明确显示：

```text
Coverage 97.8%
3 missing intervals
2 suspect samples
latest sample 11:42:00
```

禁止制造 generic “data health score”。

---

# 16. Event / Evidence Lanes

Event lanes 与所有 trend panel 共用时间轴。

## Alarm

只使用 Alarm owner 的 authoritative 事件：

- activated；
- severity transition（如果 authoritative）；
- recovered。

ACK 不是 physical recovery。

## Finding / Diagnosis

根据 owner contract 显示：

- evidence start / end；
- finding detected / published；
- verification / retest event（若属于该 owner）。

## Mode / Stage

- mode transition；
- stage transition；
- lead/lag transition。

## Control / Strategy

如果 owner 提供，保持不同语义：

```text
Intent
Attempt
Execution result
Readback
Verified
```

不能压成一个“控制成功” marker。

## Work

只显示 meaningful lifecycle event：

- work started；
- corrective action；
- completed；
- verification required / completed。

不展示每条评论、字段修改。

点击 event 后显示 concise facts + durable owner route。

---

# 17. Comparison

Comparison 必须回答一个**明确命名的问题**。

例如：

```text
current window vs previous day
current window vs previous week
before corrective action vs after
current operation vs selected historical window
```

Comparison line / panel 必须明确标识。

禁止 unlabeled ghost line。

必须说明 alignment semantics：

- absolute timestamp；
- elapsed time；
- wall-clock time。

Trend Analysis 不自行宣称 baseline equivalence。

如果 comparison 使用 formal baseline，baseline owner 必须来自 Energy / M&V contract。

---

# 18. Cursor / Exact Values

Synchronized cursor 是主要工程交互。

选中一个 timestamp 后显示：

```text
series label
value
unit
actual sample timestamp
quality
aggregation / resolution
```

同时显示附近 authoritative events。

如果不同 series 的实际 sample timestamp 不同，就分别显示。

Nearest sample 不得伪装成 interpolation。

---

# 19. Evidence Summary / Data Table

对于 valid numeric series，selected window 可显示：

```text
min
max
mean / total（仅在语义有效时）
latest
coverage
```

Binary / enumerated state 不计算无意义的 mean。

必须提供 structured evidence table：

- timestamp；
- series；
- value；
- unit；
- quality；
- relevant events。

同时提供 concise textual chart summary，例如：

> 4 个变量，08:00–12:00；CHWS 有 1 个缺测区间；09:17 告警激活。

这是 evidence summary，不是 AI causal conclusion。

---

# 20. Progressive Professional Disclosure

默认 operational layer：

```text
human-readable name
unit
selected value
quality
time range
aggregation
```

Engineering detail 可展开：

```text
historian source
semantic role
sample / log interval
raw point identifier
quality code
aggregation definition
source timezone / timestamp metadata
```

Internal ID 不能作为 primary label。

---

# 21. Toolbar

Primary：

```text
Time Range
Series
Compare
Events
View
```

Secondary：

```text
Export
Save View   // 仅当 persistence owner 已存在
Reset View
```

禁止把图表当成设计器，不能提供：

- RGB color picker；
- arbitrary line width；
- arbitrary axis count；
- smoothing；
- gradient；
- animation speed。

---

# 22. Export

Capability-gated export 必须保留 provenance：

```text
site
series identity
window
timezone
unit
sample timestamp
quality
aggregation / resolution
comparison definition
export timestamp
```

不能把 visual downsample 结果叫成 raw evidence。

如果同时支持 raw / aggregated export，必须明确命名。

---

# 23. Empty / Loading / Partial / Error

## No Series

> 选择 1–4 个关键变量开始分析。

## No Data

> 该时间范围内没有记录。

不能画 0。

## One Series Unavailable

如果 owner contract 支持 partial result：

- valid series 继续显示；
- unavailable series 明确显示 unavailable。

不能静默删除失败 series。

## Historian Unavailable

明确显示 historian unavailable。

不能用 current telemetry 冒充 historical data。

## Event Owner Unavailable

Trend data 可以继续，但 event lane 明确例如：

> 告警事件暂不可用

不能根据 spike 猜 alarm。

---

# 24. Permission / Capability

例如：

- 无 Alarm Read → 不显示 Alarm lane；
- 无 control history → 不显示 Control lane；
- 无 engineering point permission → 隐藏 raw metadata；
- 无 export permission → 不显示 Export action。

没有权限的高级能力默认不作为 disabled teaser 展示，除非产品明确要求 discoverability。

---

# 25. Responsive Behavior

## 1440–1720 px

首屏应看到：

- investigation context；
- time range / timezone；
- selected series；
- primary trend workspace；
- event lanes；
- cursor/evidence affordance。

Chart 获得主要视觉面积。

## 1024–1439 px

- toolbar 可 wrap；
- Series Picker 可用 Popover / Command；
- Evidence Summary 可下移。

## Around 768 px

核心任务必须仍可完成：

- choose time；
- choose series；
- inspect trend；
- inspect exact values；
- follow event links。

Small multiples 纵向堆叠。

Series management 可进入 Sheet。

禁止 hover-only interaction。

---

# 26. Accessibility

必须：

- color 不是唯一 series identifier；
- color 不是唯一 quality / state cue；
- controls 和 event markers 可 keyboard 操作；
- exact value 不依赖 hover；
- primary chart 有 accessible name / description；
- essential evidence 有 structured table / text alternative；
- realtime update 不移动 focus；
- 无 decorative continuous animation。

---

# 27. Realtime Contract

Historical Trend Analysis 使用 Snapshot / query semantics。

Live mode 增加 Stream：

```text
Snapshot = authoritative current history window
Stream = authorized new evidence
```

Stream disconnect 只意味着：

> live updates unavailable

不意味着 device offline。

Reconnect：

- 从 historian owner reconcile；
- 不 duplicate sample；
- 如果 follow paused，不 reset viewport。

---

# 28. Data Authority

- Historical value → historian / time-series owner；
- Alarm → Alarm domain；
- Finding → Diagnosis domain；
- Work event → Work Order domain；
- Control / Strategy event → Control execution / Strategy owner；
- Mode / Stage / Sequence → Operations / Control state owner。

Frontend 负责时间对齐，不负责重新解释事实。

---

# 29. No Defensive Programming / No Compatibility Architecture

明确禁止：

```text
value || 0
NaN → 0
missing sample → previous value
historian error → current telemetry fallback
alarm API error → infer alarm from spike
metadata error → infer unit from point name
dual axis merely because units differ
silent browser-side averaging
old trend-page adapter
Ant chart compatibility wrapper
generic ECharts abstraction owning domain semantics
multiple historical APIs where first success wins
```

不要保留旧 trend page。

不要同时维护两套 chart grammar。

不要做 `BaseTrendPage` / `UniversalChartConfig` 去拥有业务语义。

原则：

> **One fact → one owner. One analysis question → one deliberate representation. Missing evidence stays missing.**

---

# 30. ECharts Boundary

ECharts 可以负责：

- line / step / bar rendering；
- synchronized axis pointer；
- zoom；
- event marker；
- small multiples；
- pointer / tooltip presentation。

ECharts 不能负责：

- unit inference；
- semantic inference；
- Alarm generation；
- quality judgment；
- aggregation rules；
- baseline calculation；
- diagnosis logic。

在 option builder 前先准备 semantic chart-ready data。

---

# 31. shadcn / Component Mapping

```text
Context/header             → normal application layout
Time range                 → Popover / calendar-time composition
Series picker              → Command + searchable Popover / Sheet
Quick time presets         → Button / Toggle-style composition
Comparison                 → Select / Popover
Event filters              → Dropdown Menu / Checkbox composition
Series state               → Badge + text
Chart workspace            → dedicated ECharts feature component
Cursor evidence            → semantic evidence panel
Data table                 → shadcn Table + TanStack Table when needed
Engineering metadata       → Collapsible / disclosure
Export                     → Dropdown Menu
Mobile series management   → Sheet
```

不要给每个 subsection 再套一层 decorative Card。

---

# 32. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 趋势分析 · CH-02 / 冷冻水系统                 站点时区 UTC+08:00          │
│ 来源：冷冻水温差告警                                                    │
├──────────────────────────────────────────────────────────────────────────────┤
│ [08:00–12:00] [变量 4] [对比: 无] [事件: 告警/模式/控制] [视图: 分面]   │
├──────────────────────────────────────────────────────────────────────────────┤
│ CHWS 7.1°C   CHWR 12.4°C   阀位 68%   功率 504 kW                       │
│ Raw / 1 min · coverage 99.1% · 1 missing interval                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ Temperature (°C)                                                           │
│ ────────────────────────────────────────────────────────────────────────── │
│                                                                            │
│ Valve position (%)                                                         │
│ ────────────────────────────────────────────────────────────────────────── │
│                                                                            │
│ Power (kW)                                                                 │
│ ────────────────────────────────────────────────────────────────────────── │
│                                                                            │
│ Events                                                                     │
│ 09:17 Alarm ▲   09:31 Stage 2   09:42 Command   10:05 Work started       │
├──────────────────────────────────────────────────────────────────────────────┤
│ 游标 09:42:18                                                             │
│ CHWS 7.1°C GOOD · CHWR 12.4°C GOOD · Valve 68% · Power 504 kW             │
│ [查看命令执行] [打开告警] [进入设备详情]                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ Evidence table / selected-window statistics / quality coverage             │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任边界，不是 pixel specification。

---

# 33. Browser Acceptance Criteria

## Context

- Alarm entry 保留 alarm evidence window；
- Diagnosis entry 保留 finding evidence 和 supporting series；
- Operations entry 保留 system / object context；
- Site timezone 可见；
- durable URL 能恢复 time range 和 selected series。

## Series / Units

- 默认调查 1–4 个 series 可读；
- same-unit series 可 overlay；
- different-unit series 使用 aligned small multiples；
- 无默认 dual-Y-axis；
- 每个 panel 有单位；
- discrete state 不做 smooth interpolation。

## Data Truth

- real zero 显示 `0`；
- missing 不是 `0`；
- missing interval 是 visible gap；
- stale / bad / suspect quality 明确；
- NaN / invalid 不转 0；
- aggregation / resolution 可见；
- raw vs aggregated export 不混淆。

## Events

- Alarm marker 来自 Alarm owner；
- ACK 不表示 physical recovery；
- Control intent / execution / readback 保持不同语义；
- event link 保留 evidence time context；
- event owner unavailable 时不制造 marker。

## Interaction

- synchronized cursor 跨 panel 工作；
- zoom 后 panels / event lanes 保持对齐；
- live updates 不 reset zoom / focus；
- 离开 live edge 时暂停 follow；
- exact values 不依赖 hover。

## Accessibility

- series identity 不只靠颜色；
- chart 有 accessible description；
- structured data alternative 存在；
- keyboard 能选择 series / time / events / exact evidence。

## Responsive

1440–1720 px：

- context、toolbar、chart、event lane 是一个连贯 workspace；
- chart 获得主要视觉面积；
- 无 page-level horizontal overflow。

Around 768 px：

- 能选择 time / series；
- 能检查 trend / exact values；
- 能打开 event evidence；
- small multiples stack；
- 无 hover-only dependency；
- 无 page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / AntV Charts runtime DOM；
- 无旧 trend compatibility adapter；
- 无 universal chart abstraction 拥有 domain semantics；
- review scenario 无 runtime / network errors；
- internal ID 不作为 primary label。

---

# 34. Explicit Non-Goals

Trend Analysis 不是：

- all-points oscilloscope；
- chart designer；
- M&V savings calculator；
- diagnosis engine；
- alarm-rule engine；
- control command surface；
- generic BI dashboard；
- Device Detail 的替代；
- Data Quality 的替代。

---

# 35. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- single-axis / aligned-small-multiples policy 已接受；
- historian contract 能提供 unit、timestamp、quality 和 aggregation metadata；
- event owner contract 能提供 authoritative timeline event；
- source navigation 能保留 evidence context；
- non-visual evidence representation 已纳入；
- 旧 trend / chart implementation 无设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
