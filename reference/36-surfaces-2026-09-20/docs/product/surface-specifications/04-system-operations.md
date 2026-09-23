# 04 系统运行 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `04 系统运行`  
> **Route intent：** `/sites/:siteId/operations`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 System Operations / Monitor / Realtime 页面、旧设计稿、旧拓扑、旧菜单、旧 Ant/ProComponents 页面或旧组件构图。当前代码只可在实施阶段作为真实 domain owner、capability、permission 和 transport contract 的候选证据来源。

---

# 1. Primary Job

系统运行的唯一核心任务是：

> **让 HVAC 值班人员和工程师在当前站点、当前系统上下文中，快速理解“系统现在怎样运行”，识别不符合预期的运行状态，并在不丢失对象和时间上下文的情况下继续调查。**

它是高频 **operational HMI / engineering workspace**，不是：

- 普通 Dashboard；
- 设备卡片墙；
- 全站点 KPI 汇总页；
- 完整趋势分析页；
- 告警中心；
- FDD / 根因诊断页；
- 策略编辑器；
- 远程控制台；
- 装饰性“大屏拓扑”。

用户离开本页前应该已经知道：

1. 当前正在看哪个 HVAC system / loop；
2. 当前 operating mode / stage / schedule 是否符合预期；
3. 哪些设备正在运行、待机、不可用或未知；
4. 关键过程量是否可信、是否偏离预期；
5. 当前是否有 Alarm / Finding / Work / Override / Data issue；
6. 下一步应该进入 Trend、Device Detail、Alarm、Diagnosis、Control、Strategy 或 Verification 哪一个负责页面。

---

# 2. 主要用户

## Primary

### HVAC 值班工程师

需要高频理解当前系统状态、设备群运行和当前异常，并快速切到对象级调查。

### HVAC 运行工程师 / 调试工程师

需要理解 mode、stage、setpoint、sequence state、override、interlock 与点位证据之间的关系。

## Secondary

- 站点能源经理：从站点总览进入，查看当前低效或异常运行上下文；
- 诊断工程师：从 Alarm / Diagnosis 反向回到当前真实运行状态；
- 控制工程师：查看控制 authority 与实际 readback，然后进入 Control / Strategy；
- 维修人员：查看设备当前运行条件，再进入 Device / Work Order。

## 不作为主要目标用户

- 企业管理层：应使用 Portfolio / Site Overview；
- 数据工程师：默认入口应是 Data Quality；
- 告警值班员：默认入口应是 Alarm Center。

---

# 3. 外部最佳实践依据

## 3.1 ASHRAE Guideline 36-2024 — 系统运行必须围绕真实 sequence / mode / stage

ASHRAE Guideline 36-2024 的目的包括：

- maximize HVAC energy efficiency and performance；
- provide control stability；
- allow real-time fault detection and diagnostics；
- provide functional tests confirming implementation of sequences of operation。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- https://www.ashrae.org/professional-development/all-instructor-led-training/catalog-of-instructor-led-training/guideline-36-best-in-class-hvac-control-sequences

**本页采用：**

- operating mode / stage / schedule / setpoint 是一等运行事实；
- sequence 与 FDD/Verification 必须互相可下钻，但不在主画布混成一个“智能判断”；
- 工程师可以逐步进入 sequence/interlock evidence，但普通值班人员不需要首屏阅读完整控制逻辑。

## 3.2 ISA-101 — HMI 的目标是 situational awareness，而不是视觉丰富度

ISA-101 覆盖：

- menu hierarchy；
- screen navigation conventions；
- graphics/color conventions；
- dynamic elements；
- alarming conventions；
- popup conventions；
- interfaces with historical databases；
- help and configuration conventions。

其目标包括降低操作错误、提高 situational awareness、可靠性和一致性。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101
- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards

**本页采用：**

- 视觉保持 neutral；
- abnormal / action-needed 才获得高视觉权重；
- 层级、selector、Inspector 位置稳定；
- 运行数值更新不能导致页面持续重排；
- 动态元素必须表达真实过程状态，不能为“看起来实时”而动画。

## 3.3 DOE Building Controls — 运行目标同时包括舒适、IAQ、能源和成本

DOE 对 building controls 的说明指出，建筑运行应同时：

- maintain comfortable conditions；
- maintain healthy indoor air quality；
- minimize energy and cost expenditures。

来源：

- https://www.energy.gov/cmei/buildings/about-building-controls
- https://www.energy.gov/cmei/buildings/building-controls

**本页采用：**

- System Operations 不能只围绕“省能”判断好坏；
- 当前运行必须同时尊重 comfort / IAQ / reliability / safety constraint；
- 节能建议与当前运行异常保持不同语义。

## 3.4 DOE EMIS / AFDD — 运行页用于 Validate，不替代 Diagnosis

DOE 将 AFDD 描述为利用规则/算法识别偏离正常/预期运行的设备或系统级 fault，并进一步定位或诊断问题。

来源：

- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

**本页采用：**

- 当前运行页展示 authoritative Finding / Alarm 是否存在；
- 不把 telemetry deviation 自动宣称成根因；
- 需要解释为什么时进入 Diagnosis；
- 需要验证时间关系时进入 Trend。

## 3.5 DOE OpenBuildingControl / BOPTEST — 控制 sequence 需要测试和验证

DOE OpenBuildingControl 将高性能控制 sequence 的 simulation、implementation、testing、commissioning 和 verification 放在同一个工程链中；BOPTEST 用标准建筑模型和控制点验证 advanced control strategies。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/boptest-building-operations-testing-framework

**本页采用：**

- 本页展示当前控制 intent / authority / readback 的事实；
- 不把“命令已发送”当成“系统已达到目标”；
- 策略/控制需要进一步验证时进入 Execution / Functional Verification。

## 3.6 ASHRAE BACnet — Alarm / Fault / Override / Out-of-Service 是不同状态维度

BACnet 对 building automation information 采用对象化表达；公开标准资料中，Status Flags 将 `IN_ALARM`、`FAULT`、`OVERRIDDEN`、`OUT_OF_SERVICE` 分离表达。

来源：

- https://www.ashrae.org/technical-resources/technical-faqs/question-51-what-is-bacnet
- https://data.ashrae.org/bacnet/

**本页采用：**

- 不把 Alarm、Fault、Override、Out-of-Service、Connectivity、Freshness、Quality 合并成一个 health badge；
- 如果平台 owner 暴露对应事实，分别展示；
- 没有该事实就不推断。

## 3.7 ISA-18 — Alarm 必须 meaningful / prioritized / actionable

ISA-18 系列强调 alarm prioritization、operator situational awareness、alarm philosophy 和防止 alarm overload。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards

**本页采用：**

- 系统运行只显示与当前系统直接相关、当前需要注意的 Alarm；
- 不把完整 Alarm ledger 搬进本页；
- alarm handling / ACK / assignment 在 Alarm Center 完成。

## 3.8 Siemens Building X Operations Manager — 实时运行、历史趋势、故障与工单连续但分工明确

公开能力包括：

- real-time equipment visibility；
- data-point updates；
- historical charts；
- fault investigation；
- work-order integration；
- remote controls。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- 当前运行、历史趋势、FDD、Work、Control 可连续下钻；
- 但每个 durable job 保持独立 Surface。

## 3.9 Schneider EcoStruxure Building Operation — Graphics 应连接趋势、对象和报警，而不是孤立装饰

EcoStruxure Building Operation 的 Graphics 用于显示 building overview、alarm status、sensor values，并可链接 trend charts / views；WebStation 同时提供 alarms、schedules、trends 和 graphics。

来源：

- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=5788&locale=en-US&productversion=2024
- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=7939&locale=en-US&productversion=7.1

**本页采用：**

- topology/graphic 必须可下钻；
- node 是业务对象入口，不是装饰图形；
- 完整趋势和事件历史属于对应专业页面。

---

# 4. Primary Questions

用户进入后按以下顺序回答。

## Q1 — 我现在看的是哪个系统？

必须清楚显示：

- Site；
- HVAC Domain；
- System / Loop；
- current/live intent；
- Site timezone；
- data freshness / update time。

用户不能因为进入 topology 就失去层级感。

## Q2 — 系统当前处于什么运行模式？

需要看到真实 owner 提供的：

- operating mode；
- occupied / unoccupied / warm-up / cool-down 等适用 schedule state；
- current stage；
- active setpoints；
- lead/lag role（若适用）；
- demand / enable state。

不允许前端从几个点位自行推断 authoritative operating mode。

## Q3 — 哪些设备正在运行，哪些没有？

需要区分：

- commanded / enabled；
- runtime state；
- available / unavailable（如果 owner 定义）；
- connectivity / presence；
- telemetry freshness；
- fault / alarm / override / out-of-service（若存在）；
- unknown。

`Offline ≠ Fault`，`Stopped ≠ Fault`，`No telemetry ≠ Off`。

## Q4 — 当前关键过程量是否合理且可信？

例如按系统类型显示适用事实：

### 冷源

- supply / return chilled-water temperature；
- condenser water temperature；
- flow；
- cooling load；
- current plant power；
- COP / kW/RT（prerequisites valid 时）。

### 冷冻水

- supply / return temperature；
- ΔT；
- differential pressure；
- flow；
- pump speed / state；
- active DP setpoint。

### 冷却水

- condenser supply / return temperature；
- tower approach（输入完整时）；
- flow；
- pump/tower stage；
- fan speed；
- outdoor wet-bulb（有真实气象 owner 时）。

### AHU / 末端

- supply air temperature；
- static pressure；
- damper / valve command/readback；
- occupancy；
- representative zone demand；
- comfort/IAQ violation（能力存在时）。

系统类型不支持的指标不显示空位。

## Q5 — 当前有哪些异常值得继续调查？

只显示与选中 system scope 相关的：

- active important alarms；
- authoritative findings；
- current overrides；
- failed/inconclusive verification；
- data quality incidents；
- current work items that affect operation。

不把 informational event 伪装成 alarm。

## Q6 — 下一步我该去哪？

用户应该可以直接：

- 看选中对象趋势；
- 打开设备详情；
- 查看相关告警；
- 进入诊断；
- 查看控制 authority / Control Center；
- 查看策略；
- 查看功能验证；
- 进入数据质量。

---

# 5. Non-goals

系统运行明确不负责：

- Portfolio / Site KPI 汇总；
- 完整设备 Registry 浏览；
- 完整 Alarm ledger；
- Alarm ACK / Assign / Suppress；
- 完整 FDD root-cause workspace；
- 长时间范围多变量趋势分析；
- Work Order 生命周期管理；
- Strategy editing；
- 高风险控制确认；
- M&V；
- Utility billing；
- 长期节能机会排序；
- 大屏展示；
- 用一个 health score 总结整个系统。

---

# 6. Entry Points

## 6.1 Sidebar / Role Landing

HVAC 值班工程师可直接以系统运行为默认入口。

## 6.2 从站点总览进入

携带：

```text
site
system/domain（如果用户点击了明确系统域）
source=site-overview
```

## 6.3 从 Alarm / Diagnosis 进入

如果用户需要验证当前运行环境，携带：

```text
site
system / device
source alarm/diagnosis identity
evidence window（如果有）
```

页面默认仍展示 current/live，但可以明确显示“来自某事件/调查”的 source context。

需要历史证据时应跳 Trend，不把 System Operations 变成历史回放工具。

## 6.4 从 Device Detail 进入

携带：

```text
site
device
system
```

如果设备属于某真实 system/loop，系统运行自动聚焦该 scope。

## 6.5 Global Search

搜索 system / equipment 后可以进入系统运行并聚焦对象。

---

# 7. Exit Paths

| 当前任务 | 目标 Surface | 必须携带 |
|---|---|---|
| 查看 30min/24h 时序证据 | 趋势分析 | `site + system/device + points + timeRange` |
| 持续调查单设备 | 设备详情 | `site + device` |
| 处理告警 | 告警中心 | `site + alarm/system/device` |
| 调查 finding/root cause | 诊断中心 | `site + system/device + evidence window + finding/alarm` |
| 查看完整工单 | 工单中心/详情 | `site + workOrderId/object` |
| 查看控制 authority / 执行 | 控制中心 | `site + target object` |
| 查看自动化策略 | 策略详情 | `site + strategyId/system` |
| 验证 sequence / readback | 功能验证 | `site + requirement/system + window` |
| 调查不可信数据 | 数据质量 | `site + point/metric/object + window` |
| 查看舒适影响 | 舒适与室内环境 | `site + zone/system + window` |

---

# 8. Route / URL State Ownership

## Path

```text
/sites/:siteId/operations
```

`siteId` 是 durable site identity。

## Search Params

只保存刷新/分享后用户合理期待保留的运行上下文：

```text
domain        // chilled-water / condenser-water / airside 等产品定义值
system        // durable system/loop identity
selected      // 当前 Inspector 对象，只有 selection 需要可分享时
view          // topology | equipment，仅当两者都是正式 peer view 时
```

是否真的实现 `selected` / `view` 要在 Wireframe 与 Router contract 阶段确认；不提前设计未使用参数。

## 不进入 URL

- hover；
- topology pan/zoom camera；
- inspector accordion 展开；
- transient tooltip；
- live pulse / animation state；
- selected chart cursor。

## Current / Live 是本页默认语义

不通过 query 参数维护一个虚构的 `live=true`。

如果用户需要历史时间范围，进入 Trend。

---

# 9. Screen Hierarchy

系统运行严格使用：

```text
Site
→ HVAC Domain
→ System / Loop
→ Equipment
→ Point / Evidence
```

任何时候必须能看出当前层级。

例如：

```text
东京中央冷站
冷源
一次冷冻水系统
CH-02
```

不是 Breadcrumb 的每一级都必须成为 route；但视觉层级必须明确。

---

# 10. Page Information Architecture

页面固定为 5 个区域：

```text
1. Context Header
2. Domain / System Selector
3. Primary Operating Workspace
4. Context Inspector
5. Supporting Evidence / Current Issues
```

Desktop 主工作区采用 canvas/list + inspector 的稳定双区结构。

---

# 11. Context Header

显示：

- Site business name；
- `系统运行` h1；
- current/live indicator；
- Site timezone；
- snapshot / stream freshness；
- source context（如果从 Alarm/Diagnosis/Device 进入）。

不重复 Global Shell 的大型 Site Selector。

## Live Indicator

必须区分：

- 当前页面与实时服务连接；
- 当前对象 telemetry freshness；
- 当前设备 connectivity。

不能只放一个绿色 `LIVE` 就暗示所有设备数据都新鲜。

---

# 12. Domain / System Selector

## HVAC Domain

常见：

```text
冷源
冷冻水
冷却水
空调风系统
末端
```

只显示站点真实建模存在的 domain。

如果 domain ≤ 5，可使用 segmented/tabs-like selector；如果更多，使用 compact navigation/combobox。

## System / Loop

使用 searchable combobox 或明确 system list。

显示业务名称和位置，例如：

```text
一次冷冻水系统
B1 冷站 · 主管路 CHW-L1
```

不要显示 UUID。

## 切换规则

切 Domain/System 时：

- 保留 Site；
- 保留 current/live intent；
- 清除不兼容 selected equipment；
- 不保留旧 system 的 Inspector 内容；
- 不显示旧 system snapshot 作为新 system 的 loading placeholder。

---

# 13. Primary Operating Workspace

Primary Workspace 的显示形式取决于真实 semantic/system model capability。

## 13.1 Topology View

只有存在权威 relation owner 时启用。

Topology 表达：

- system/loop；
- equipment nodes；
-真实 upstream/downstream or containment relationships；
- 关键过程变量；
- current running state；
- current important abnormal state。

### 不表达

- 未经建模的水流方向；
- 根据功率猜测的 flow；
- 装饰性 pipe animation；
- 把 telemetry freshness 当 connectivity；
- 把 alarm severity 染满整台设备。

## 13.2 Equipment View

当用户需要扫描设备群时提供 peer view。

推荐 compact operational rows：

```text
设备          运行       连接       关键值             异常
CH-01         运行       在线       312 kW / COP 5.8  —
CH-02         运行       在线       348 kW / COP —    高压告警
CH-03         待机       在线       —                  —
CH-04         未知       数据陈旧   —                  数据问题
```

这里不是完整 Device Center：

- 不提供 Registry 管理；
- 不提供大量资产筛选；
- 只服务当前 system scope 的运行判断。

## 13.3 Topology vs Equipment

如果两者都存在，它们是同一个 system/current context 的 peer views，可以使用 Tabs/segmented view selector。

切换必须保留：

- system；
- selected equipment（如果两边都能表示）；
- inspector context。

---

# 14. Topology Visual Contract

## 14.1 Normal state is quiet

默认 node / line 使用 neutral visual。

颜色只强调：

- critical/high alarm；
- current fault/finding needing attention；
- override；
- out-of-service；
- stale/quality issue（使用独立视觉语义）。

## 14.2 Node anatomy

设备 node 只显示完成运行判断需要的事实：

```text
CH-02
运行
348 kW
COP 5.6
```

必要时一个异常 indicator。

不把 alarm、diagnosis、connectivity、freshness、work order 全做 5 个彩色 badges 塞进 node。

## 14.3 Lines / flow

Line 只表达 authoritative relation。

动态 flow animation 默认不用。

如果真实 owner 提供 flow direction/state，也优先使用箭头、文字、数值等低干扰表达，不使用持续运动动画作为唯一状态。

## 14.4 Pan / Zoom

Topology 可以在自身 canvas 中 pan/zoom；页面主体不产生 horizontal scroll。

提供：

- fit to system；
- reset view；
- keyboard accessible object alternative（Equipment View）。

Topology 不是唯一可访问路径。

---

# 15. Current System Facts

Primary Workspace 周边可以有一个 compact system facts band。

最多显示 4–6 个当前事实，例如：

```text
运行模式        自动制冷
当前负荷        1,260 RT
运行冷机        3 / 4
冷冻水供水温    6.1 °C
ΔT             4.7 K
系统 COP        5.6
```

规则：

- metric 必须有 owner；
- unit 显式；
- timestamp/freshness material 时可追溯；
- 0 是合法值时必须显示 0；
- unavailable 不转成 0；
- 不显示 fake trend arrow；
- 不创建“运行健康分”。

---

# 16. Context Inspector

点击 topology node 或 equipment row 后打开同页 Inspector。

Inspector 用于快速判断，不是设备详情替代品。

## 16.1 Identity

- business name；
- type；
- location/system；
- role（lead/lag / pump group / AHU 等，若 owner 提供）。

## 16.2 Independent current states

独立展示：

- runtime state；
- connectivity / presence；
- telemetry freshness；
- telemetry quality；
- alarm state；
- diagnosis/finding state；
- override/out-of-service（若适用）；
- control authority（若适用）。

禁止合并为 `健康/故障` 单一标签。

## 16.3 Key current values

设备 profile 决定 4–8 个最重要值。

例如 chiller：

- CHWS / CHWR；
- evap ΔT；
- condenser temperatures；
- power；
- load；
- COP/kWRT；
- current setpoint。

不定义一套假通用指标适配所有设备。

## 16.4 Current relationships

只显示当前有效、用户需要知道的：

- active alarms；
- active findings；
- current/open work；
- related verification；
- active strategy/override（若有）。

## 16.5 Inspector actions

允许：

- `打开设备详情`；
- `查看趋势`；
- `查看告警`；
- `进入诊断`；
- `查看控制`；
- `查看策略`；
- `查看数据质量`。

不在 Inspector 里直接完成复杂 mutation。

---

# 17. Sequence / Stage / Setpoint Contract

这是 System Operations 的专业核心之一。

## Operational View

默认只显示：

- current mode；
- current stage；
- active schedule；
- active setpoints；
- lead/lag；
- current override existence。

## Engineering Detail

按需展开：

- sequence state；
- stage transition condition；
- reset logic current input/output；
- interlock state；
- enable/disable reason；
- current control authority；
- command/readback relationship。

## 不允许

- 把复杂 sequence logic 全铺在主画布；
- UI 根据 telemetry 点自行复制 controller state machine；
- command 已发出就显示 target state achieved；
- 读取不到 sequence owner 时根据设备数量猜 current stage。

---

# 18. Control Boundary

System Operations 默认 **read-oriented**。

## 可以显示

- current control authority；
- active setpoint；
- override exists；
- last execution status summary；
- strategy currently governing target。

## 不在本页直接执行

- start/stop；
- setpoint write；
- override；
- strategy publish；
- high-risk command。

这些进入 Control Center / Strategy Detail。

理由：

- 本页主任务是理解当前运行；
- control 需要更完整 safety/interlock/impact context；
- 避免用户在快速扫描时误触高后果动作。

如果未来产品明确要求 direct low-risk command，需要单独更新本 Surface Contract，而不是在实现中偷偷加入按钮。

---

# 19. Current Issues / Supporting Evidence

Primary Workspace 下方或侧下区域展示当前 system scope 的少量相关证据。

建议 3 类：

## Current alarms

最多显示高相关的 3–5 条；点击进入 Alarm Center。

## Findings / Diagnosis

显示：

- finding title；
- source type；
- affected object；
- current state；
- confidence（仅 owner 真正提供且语义明确）。

点击进入 Diagnosis。

## Work / Verification

显示影响当前系统的：

- open urgent work；
- completed-needs-verification；
- failed/inconclusive verification。

点击进入 Work / Verification。

不要做完整 side-panel ledger。

---

# 20. Trend Preview

System Operations 可以在 Inspector 或 supporting evidence 中显示一个**短时趋势预览**，但不是 Trend Analysis 替代品。

允许：

- 1–3 个当前关键点；
- 最近 30–60 分钟；
- 明确 unit；
- current value marker；
- data gap / stale indication。

不允许：

- 任意多点选择；
- 多轴工程分析；
- baseline overlay；
- export；
- 长时间范围；
- event correlation tooling。

这些进入 Trend Analysis。

---

# 21. Data Authority Contract

System Operations 不成为新的 domain owner。

| 事实 | Authoritative owner | 本页可做 | 本页不可做 |
|---|---|---|---|
| Site/System identity | Registry / semantic model | 显示层级和关系 | 前端猜设备归属 |
| Physical/functional relation | Semantic/system model | topology | 从命名规则拼 topology |
| Runtime mode/stage | Operations/control runtime | 显示 current mode/stage | 从零散点位推断 authoritative mode |
| Schedule/setpoint | Schedule/control owner | 显示当前生效值 | 从历史命令猜当前值 |
| Telemetry | Telemetry owner | 显示 current values | stale → zero |
| Connectivity | Presence/connectivity owner | 独立显示 | platform SSE disconnect → device offline |
| Data quality | Telemetry/data-quality owner | quality/freshness | quality issue → fault |
| Alarm | Alarm owner | 当前摘要/deep link | telemetry threshold → self-created alarm |
| Finding | Diagnosis owner | 当前 finding | finding → root cause without evidence |
| Work | Work-order owner | current work | infer responsibility from alarm |
| Control execution | Command/control owner | request/ack/readback summary | ACK → target achieved |
| Strategy | Strategy owner | active strategy reference | active strategy → verified outcome |
| Functional verification | Verification owner | result/deep link | Work completed → verified |

---

# 22. State Semantics

至少保持以下维度独立：

```text
Runtime
Connectivity / Presence
Telemetry Freshness
Telemetry Quality
Alarm
Finding / Diagnosis
Override
Out of Service
Maintenance / Work
Control / Command
Verification
```

示例：

```text
CH-04
运行：停止
连接：在线
遥测：新鲜
告警：无活动告警
维护：计划维护
控制：自动
```

这是合法组合。

不要变成：

```text
CH-04 正常
```

除非产品有被正式定义的权威综合状态 owner。

---

# 23. Loading / Partial / Stale / Error

## 23.1 Initial loading

Context Header / selector 已知时先显示；Primary Workspace 使用稳定 skeleton。

不要先渲染旧 system data 再闪成新 system。

## 23.2 Topology owner unavailable

如果 semantic model owner 失败：

```text
系统关系暂不可用
当前设备运行事实仍可查看（若设备/telemetry owner 正常）
```

这不是自动 fallback 到另一套 topology source。

## 23.3 Telemetry partial

某些点缺失时：

- 设备身份仍显示；
- 缺失 metric 显示 unavailable；
- 依赖 metric 的 COP/ΔT 等不计算；
- 不把 entire system 标记 failure。

## 23.4 Stale

显示最后可信时间。

例如：

```text
CH-02 功率更新至 10:31:14，已超过新鲜度要求
```

## 23.5 Stream disconnected

只表达 realtime channel 状态：

```text
实时更新中断，正在显示 10:31:14 的最新快照
```

不能把所有设备标 offline。

## 23.6 No permission

无权限不等于空数据。

例如用户看不到 Control Authority，直接隐藏相关 section/action，或按 discovery policy 显示权限说明。

---

# 24. No Defensive Programming / No Compatibility Design

实施本页明确禁止：

- `value || 0`；
- `try/catch` 后返回空数组掩盖 owner failure；
- topology API 失败后根据 device name 拼关系；
- mode owner 缺失后根据泵/冷机数量猜 operating mode；
- realtime disconnect 后把所有设备标 offline；
- alarm 请求失败后用 telemetry threshold 临时生成 alarm；
- COP 缺 flow/load 时返回 0；
- command ACK 后直接显示“执行成功”；
- 新 `/operations` 与旧 AI Operations / Monitor 同时保留两套产品语义；
- 为旧 Monitor route 做 adapter；
- 两套 realtime transport “谁先成功用谁”；
- 给所有设备类型硬塞同一组 key metrics；
- 为“以后可能需要”增加几十个 system flags / query params；
- 一个失败 branch 同时访问多个 owner 试图“尽量显示点东西”。

正确方式：

- 一个事实一个 owner；
- domain capability 存在才显示；
- topology 只来自 semantic relation owner；
- current values 只来自 telemetry owner；
- mode/stage 只来自 operations/control owner；
- unavailable 就明确 unavailable；
- 不兼容旧实现就直接替换旧 Surface。

---

# 25. Desktop Information Architecture

建议桌面主布局：

```text
┌─────────────────────────────────────────────────────────────────────────┐
│ Site / 系统运行                 Current · 更新时间 · 来源上下文           │
├─────────────────────────────────────────────────────────────────────────┤
│ Domain selector     System selector                  Topology | Equipment │
├──────────────────────────────────────────────┬──────────────────────────┤
│                                              │ Context Inspector         │
│ Primary Operating Workspace                  │                          │
│                                              │ Identity                 │
│ Topology / Equipment View                    │ Independent states        │
│                                              │ Key current values        │
│                                              │ Related issues            │
│                                              │ Deep links                │
├──────────────────────────────────────────────┴──────────────────────────┤
│ System Facts / Short Trend / Current Issues                             │
└─────────────────────────────────────────────────────────────────────────┘
```

## Desktop first viewport

1440–1720px 下必须在首屏看到：

- Site + System scope；
- current mode/stage；
- Primary Workspace 大部分；
- Inspector（选中对象时）；
- 关键异常入口。

不要让 6 张 KPI Card 把 topology/list 挤到首屏以下。

---

# 26. Inspector Geometry

Desktop：

- Inspector 是稳定 side panel；
- 建议宽度约 320–400px，根据内容验证；
- 不 overlay 主 workspace；
- selection 改变时保持 panel 位置不跳动。

Narrow：

- 转为 full-width Sheet 或 inline detail section；
- 关闭后焦点返回选中 node/row；
- Equipment View 必须仍可使用。

最终具体宽度在 Browser Review 中决定，不提前写死像素 contract。

---

# 27. Narrow / Tablet Layout

## 1024–1279px

- System selector 保留；
- topology/list 主区缩小；
- Inspector 可以更窄但不压到不可读。

## 768–1023px

推荐：

```text
Context
Selector
Primary Workspace
Selected Object summary
Current Issues / short trend
```

Inspector 变 Sheet 或 inline section。

## <768px

系统运行不是手机优先页面，但核心任务仍可完成：

- default to Equipment View；
- topology 作为可选 canvas；
- system selector 使用 combobox；
- 关键 current states 可读；
- 所有主要 deep link 可访问。

不要强迫用户在手机上操作巨大 topology。

---

# 28. Interaction Model

## Node / Row selection

单击：

- selection；
- 更新 Inspector；
- 不直接导航。

明确的 `打开设备详情` 才进入 durable route。

## Double click

不把 double-click 作为唯一核心操作；触摸与可访问性差。

## Context menu

默认不依赖右键菜单完成核心工作。

## Keyboard

Equipment View 必须完整支持 keyboard。

Topology 如使用 Canvas/X6：

- 至少提供可聚焦的对象选择机制，或；
- 提供等价 Equipment View。

Canvas 不可成为唯一无障碍入口。

---

# 29. Visual Hierarchy / High-Performance HMI Rules

## 29.1 正常状态视觉安静

正常值不使用大面积绿底。

推荐：

- neutral foreground/background；
- subtle state text/icon；
- semantic color 留给 abnormal / action-needed。

## 29.2 不做“每种设备一个颜色”

CHWP、CWP、Chiller、Tower 可以用 icon/label 区分，不依赖彩虹配色。

## 29.3 Alarm color

只在当前 active alarm / serious abnormal 状态中使用语义色。

不能因为设备历史上有 alarm 就永久红色。

## 29.4 Data quality

Stale / suspect 使用自己的视觉语言，不复用 alarm 红色。

## 29.5 Motion

仅在 motion 本身是业务信息时使用。

禁止：

- 永久旋转风机 icon；
- 流动虚线装饰；
- pulsing glow 表示“在线”；
- 无意义实时数字跳动动画。

---

# 30. Component Mapping

| 页面责任 | 推荐实现模式 |
|---|---|
| Page Context | semantic header |
| Domain selector | compact Tabs/segmented control，仅少量固定 peer domains |
| System selector | searchable Combobox |
| View switch | Tabs / Toggle Group，仅 Topology/Equipment peer views |
| Topology | X6，仅真实 relation model |
| Equipment view | semantic table/compact rows；复杂交互才 TanStack Table |
| Inspector | contextual side panel / responsive Sheet |
| State labels | Badge / icon + text，克制使用 |
| System facts | compact semantic grid + Separator |
| Short trend | ECharts |
| Current issues | compact semantic list |
| Deep links | Button/link |
| Loading | section-scoped Skeleton |
| Help | Tooltip/Popover 解释术语，不承载关键事实 |

## 不使用

- Carousel；
- masonry cards；
- KPI card wall；
- nested Card wall；
- animated SVG flow decoration；
- Modal 展示完整设备详情；
- Tabs 跨 Alarm/Diagnosis/Work/Control 业务域。

---

# 31. Realtime Contract

遵循 `Snapshot + Stream`。

## Snapshot

负责：

- 初始完整 scope；
- 当前 mode/stage；
- equipment population；
- current values；
- current related facts。

## Stream

只增量更新：

- 当前 scope 中需要实时的 point/state；
- relevant alarms/findings/work/control events。

## Scope

只订阅当前 active Site/System 与必要 selected object。

不要为了简单而订阅整个站点所有 telemetry 再在客户端过滤。

## Presentation stability

实时更新：

- 更新 value；
- 不抢 focus；
- 不自动重置 pan/zoom；
- 不因为 metric 更新重新创建 nodes；
- 不频繁改变 equipment row order。

---

# 32. Performance Contract

- Topology semantic graph 与 telemetry stream 解耦；
- relation model 不因每个 telemetry tick 重建；
- visible scope 才订阅高频 stream；
- large system node count 应按真实系统层级分 scope，而不是无限 canvas；
- ECharts short trend 仅加载选中对象必要序列；
- 不做 site-wide raw point dump；
- 不创建全局 event bus 复制 Query/stream state。

---

# 33. Business Copy

推荐：

```text
冷源系统
自动制冷 · 3 台冷机运行
```

```text
冷冻水压差控制
目标 62 kPa · 当前 59 kPa
```

```text
CH-02
运行 · 在线
功率 348 kW
```

```text
数据陈旧
CH-04 功率更新于 10:31:14
```

```text
当前有人工覆盖
冷冻水压差设定值由值班员临时覆盖
```

不推荐：

```text
系统健康度 87
AI 运行指数 93%
设备正常率 96%
智能节能模式已优化
```

除非这些是权威、定义完整、可追溯的正式产品指标。

---

# 34. Empty / Normal State

正常运行不应该变成一个空页面。

示例：

```text
当前未发现需要关注的运行异常
```

同时仍显示：

- mode；
- stage；
- equipment；
- current values；
- setpoints；
- data freshness。

不要为了“页面有内容”随机生成 insight。

---

# 35. Accessibility

## Structure

- 唯一 `h1`：系统运行；
- selector 有明确 label；
- view switch 有当前状态；
- Inspector heading 层级正确。

## Equipment View

提供 semantic table/list，可键盘完成：

- 浏览；
- selection；
- 打开详情；
- 打开趋势。

## Topology

Canvas/X6 不能成为唯一信息来源。

重要 node facts 必须在 Inspector / Equipment View 可访问。

## Live update

- 不在每个 telemetry tick 上 screen-reader announce；
- critical state change 可以由 Alarm/Notification domain 处理 announcement；
- current focus 不被 stream update 移除。

## Color

- runtime / alarm / stale / override 都有文字或 icon + label；
- 不能只用色相区别。

---

# 36. Browser Acceptance Criteria

实现后必须使用真实浏览器、真实或固定认证数据验证。

## Desktop 1440–1720px

必须满足：

- Site/System scope 一眼可见；
- operating mode/stage 在首屏；
- Primary Workspace 是视觉主体；
- Inspector 选中对象后同屏可见；
- normal state 视觉安静；
- abnormal state 可定位；
- no KPI card wall；
- no page-level horizontal scroll；
- no UUID/trace/revision 主显示；
- no fake topology animation；
- no Ant Design DOM/compat layer 于新 Surface；
- current values 单位清楚；
- selected object 不因 stream update 丢失。

## 1024px

- selector 可用；
- Primary Workspace 保持主导；
- Inspector 不遮挡关键设备信息；
- topology/list 可以完成 selection。

## 768px

- 无页面级横向溢出；
- Equipment View 可完成核心任务；
- Inspector 转 responsive Sheet/inline；
- deep links 保留；
- key state labels 可读。

## Truthfulness scenarios

至少测试：

1. valid zero telemetry 显示 `0`；
2. stale telemetry 不显示成 current；
3. realtime stream down 不把 device 标 offline；
4. device offline 不自动标 fault；
5. alarm active 但 connectivity online 的组合可正确显示；
6. point quality bad 时 dependent COP/ΔT 不计算；
7. topology relation owner unavailable 时不拼 fake topology；
8. mode owner unavailable 时不猜 mode；
9. command ACK 不显示 target verified；
10. override 与 alarm 独立显示；
11. no permission 不显示 empty；
12. domain capability absent 不显示 placeholder。

## Navigation scenarios

- Site Overview → Operations 保留 site/system；
- Operations → Trend 带 selected object + relevant points；
- Operations → Device Detail 带 device；
- Operations → Diagnosis 带 system/device/source context；
- Operations → Control 不丢 target；
- Operations → Verification 不丢 requirement/system context。

---

# 37. Wireframe Gate

本 Surface 已回答 Interaction Contract 的 17 个问题：

1. **Primary Job：** 理解 HVAC 当前真实运行，并把异常送入正确调查路径。
2. **Primary Entry：** role landing / Site Overview / Alarm / Diagnosis / Device / Search。
3. **Primary Exit：** Trend / Device / Alarm / Diagnosis / Work / Control / Strategy / Verification / Data Quality。
4. **Context：** Site + Domain + System + optional selected object；current/live 为默认语义。
5. **Path：** `/sites/:siteId/operations`。
6. **Search Params：** domain/system/selected/view，仅真实使用时引入。
7. **Local UI：** pan/zoom/hover/accordion/chart cursor。
8. **Inspector：** 需要，用于对象快速判断。
9. **Durable Detail：** Device Detail / Diagnosis / Trend / Strategy 等负责。
10. **Dialog：** 本页无高风险 mutation，因此默认不需要业务 Dialog。
11. **Operational View：** selector + current mode/stage + topology/equipment + current values + current issues。
12. **Engineering Detail：** sequence/interlock/control authority/point evidence 渐进展开。
13. **Capability absent：** 对应 domain/view/metric 不显示。
14. **Permission：** Principal capability 决定 section/action，不从请求错误推断。
15. **State：** Loading / Partial / Stale / Owner unavailable / Stream disconnected 分离。
16. **Narrow：** Equipment View 成为可靠主路径，Topology 可选。
17. **Accessibility：** semantic list/table alternative、keyboard selection、no color-only、no live focus theft。

**结论：READY FOR WIREFRAME。**

---

# 38. Implementation Discipline

进入实现时：

1. 先确认 semantic model、runtime mode/stage、telemetry、alarm、finding、control 的真实 owner；
2. Route 文件保持薄；
3. Query/stream owner 保持单一；
4. current server truth 不复制进第二份 global client store；
5. Snapshot + Stream 按 active scope 实现；
6. topology graph 与 telemetry update 解耦；
7. 不迁移旧 Monitor / Realtime / AI Operations 页面构图；
8. 不建立旧 route adapter 作为产品设计的一部分；
9. 不增加“以防 API 出错”的第二数据源；
10. 每个 branch 必须对应已知业务状态，不写 hypothetical fallback；
11. 首轮实现完成后必须真实 Chrome visual review，再调整 canvas density / inspector width / node anatomy；
12. 没有 authoritative relation model 时，宁可先实现 Equipment View，也不画 fake topology。

---

# 39. 本 Surface 的参考资料

## Government / standards — primary

- ASHRAE Guideline 36-2024 — High Performance Sequences of Operation for HVAC Systems  
  https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- ASHRAE Guideline 36 training summary  
  https://www.ashrae.org/professional-development/all-instructor-led-training/catalog-of-instructor-led-training/guideline-36-best-in-class-hvac-control-sequences
- ISA-101 Human-Machine Interfaces  
  https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101
- ISA-101 Series  
  https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- ISA-18 Alarm Management Series  
  https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards
- U.S. DOE — About Building Controls  
  https://www.energy.gov/cmei/buildings/about-building-controls
- U.S. DOE — Building Controls  
  https://www.energy.gov/cmei/buildings/building-controls
- U.S. DOE FEMP — EMIS Capabilities  
  https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- U.S. DOE FEMP — EMIS Operations Support  
  https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- U.S. DOE — OpenBuildingControl  
  https://www.energy.gov/cmei/buildings/openbuildingcontrol
- U.S. DOE — BOPTEST  
  https://www.energy.gov/cmei/buildings/boptest-building-operations-testing-framework
- ASHRAE BACnet overview / resources  
  https://www.ashrae.org/technical-resources/technical-faqs/question-51-what-is-bacnet  
  https://data.ashrae.org/bacnet/

## Mature product evidence — secondary

- Siemens Building X Operations Manager  
  https://www.siemens.com/en-us/products/building-x/applications/operations-manager/
- Schneider EcoStruxure Building Operation — Graphics  
  https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=5788&locale=en-US&productversion=2024
- Schneider EcoStruxure Building Operation — WebStation Overview  
  https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=7939&locale=en-US&productversion=7.1

商业产品仅用于验证成熟工作流和 HMI 能力组合，不作为视觉模板，不复制其菜单、Dashboard 或品牌样式。
