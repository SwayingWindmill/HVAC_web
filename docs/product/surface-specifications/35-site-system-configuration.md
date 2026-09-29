# 35 站点与系统配置 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-15**  
> **Surface Catalog：** `35 站点与系统配置`  
> **Route intent：** `/settings/sites`、`/settings/sites/:siteId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`IANA Time Zone`、`BACnet Schedule/Calendar`、`ASHRAE 223`、`ISO 50001` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目旧站点设置、旧 Registry、旧组织树、旧系统配置页面或旧 Ant/ProComponents 页面。现有实现只能在实施阶段作为真实 Site / Building / Space / System / Calendar / Timezone / Weather / Capability / Commissioning contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **定义并治理组织、站点、建筑、空间、HVAC 系统、业务日历、时区、天气源、投产状态与站点能力，使全产品拥有稳定、可追溯、可生效期管理的运行上下文。**

35 是 **Site Master Configuration + Operational Context Governance Workspace**，不是：

- 万能 Settings；
- Asset / Device / Point Registry；
- Ontology / semantic triple editor；
- BACnet Schedule 在线控制器；
- 天气数据分析页；
- Commissioning test execution 页；
- Integration credential 页；
- 用户权限页；
- 把所有站点相关字段塞进一个大表单的后台 CRUD。

用户离开本页前应该能回答：

1. 当前 Organization / Portfolio / Site / Building / Space 是什么稳定对象；
2. 站点边界与能源管理 scope / boundary 是什么；
3. 当前站点使用哪个 IANA Time Zone；
4. 当前 Business Calendar / Holiday Calendar 是哪个版本；
5. 日历异常日期如何影响业务周期，但是否会直接影响控制 Schedule；
6. 当前 Weather Source / Station / Provider 是什么；
7. Weather binding 从何时生效；
8. 当前 HVAC System 的业务边界是什么；
9. System 与设备/点位/计量关系由谁管理；
10. 当前 Site / Building / System 处于什么生命周期；
11. 当前 Commissioning State 是什么；
12. 哪些 Product / Site Capabilities 已启用；
13. capability 是否依赖 Integration / Data / Control owner；
14. 某个配置变更会影响哪些历史、分析、规则、报告与控制场景；
15. 当前有效 Revision 和未来生效 Revision 分别是什么。

---

# 2. 主要用户

## Primary

- **Platform / Site Administrator：**维护组织、站点、建筑、空间、系统和运行上下文；
- **Energy Manager：**维护 EnMS scope/boundary、业务日历和天气源上下文；
- **Facility / HVAC Engineer：**维护 HVAC system boundary 与投产状态；
- **Commissioning Manager：**维护 commissioning lifecycle 与 handoff 状态；
- **Product Administrator：**维护 site capability enablement 与 deployment availability。

## Secondary

- Semantic Model Owner：消费 Site/Building/Space/System master identity 并维护 32 relationships；
- Integration Administrator：绑定 34 Source / Connector 到 Site；
- Data Quality Owner：消费 timezone/calendar/weather/config change impact；
- Controls Engineer：消费 site/system context，但不在本页直接控制设备；
- Auditor：查看 revision、effective date、approval、change impact 与退役历史。

---

# 3. 外部最佳实践依据

## 3.1 ISO 50001 / DOE 50001 Ready — Scope 与 Boundary 必须明确

ISO 50001 要求能源管理体系有明确组织范围和边界；DOE 50001 Ready 的 Task 1 也明确要求组织定义、记录并批准 Scope and Boundaries。

来源：

- https://www.iso.org/standard/69426.html
- https://www.energy.gov/cmei/ito/50001-ready-program
- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/Using_50001_Ready_to_Help_Meet_Your_Energy_Goals.pdf

本页采用：

```text
Organization Scope ≠ Site Boundary automatically
Site Boundary ≠ Measurement Boundary
Energy Management Boundary ≠ HVAC System Boundary
```

35 负责站点级主边界和 EnMS scope context；17 / 24 等专业 Surface 使用自己的 EnB / M&V measurement boundary。

## 3.2 ASHRAE 223 — Building / System / Space 需要机器可读语义

ASHRAE 223 旨在标准化建筑与自动化系统的语义信息，使 building data、systems 和 relationships 可以被机器解释。

来源：

- https://www.ashrae.org/news/esociety/ashrae-bacnet-committee-works-with-other-organizations-on-new-standard

本页采用：

- 35 不依赖名称推断系统归属；
- Site / Building / Space / HVAC System 都引用稳定 canonical identity；
- 32 继续负责 semantic relationships / topology；
- 35 不建立第二套对象图。

## 3.3 BACnet Schedule / Calendar — 周计划与例外日历是不同语义

BACnet Schedule Object 明确区分 Weekly Schedule 与 Exception Schedule；Exception Schedule 可以引用 Calendar，并带有效期与优先级。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-2001-135a.pdf

本页采用：

```text
Business Calendar ≠ Control Schedule
Weekly Calendar ≠ Exception Calendar
Calendar Active ≠ Equipment Commanded
```

35 管理业务/站点日历上下文；真正控制设备的 Schedule 由 25–27 Control/Strategy owner 管理。

## 3.4 IANA Time Zone Database — 时区不是固定 UTC Offset

IANA tz database 记录全球 civil-time rules，并持续因政府对 UTC offset / DST 规则的调整而更新。

来源：

- https://www.iana.org/time-zones
- https://www.iana.org/time-zones/tz-link

本页采用：

```text
Timezone ≠ UTC Offset
Timezone ID ≠ Display Abbreviation
```

Site 使用 IANA Zone ID，例如：

```text
Asia/Singapore
America/New_York
Europe/Berlin
```

而不是只保存 `UTC+8`、`EST`、`CST`。

## 3.5 ENERGY STAR — Weather Source 必须具有明确站点/数据来源语义

ENERGY STAR Portfolio Manager 使用明确 weather station / WMO ID 和 GSOD data 来支持 weather-normalized metrics，并根据 property location 选择参考天气站。

来源：

- https://portfoliomanager.energystar.gov/pdf/reference/Climate%20and%20Weather.pdf
- https://portfoliomanager.energystar.gov/pm/degreeDaysCalculator

本页采用：Weather Source / Station / Provider / Effective Period 显式治理；Weather binding 不通过“最近站点”前端临时推断。

## 3.6 DOE Commissioning — 投产/移交是正式生命周期

DOE/FEMP Commissioning Process 使用 Plan → Investigate → Implement → Hand off and Integrate，并要求最终 documentation、system information、actions 与未来 commissioning plan。

来源：

- https://www.energy.gov/cmei/femp/commissioning-process-federal-facilities

本页采用：Commissioning State 是正式治理状态，而不是根据“设备在线”或“某次测试通过”自动推断。

---

# 4. 产品语言契约

主界面中文优先：

```text
站点与系统配置
组织
业务组合
站点
建筑
空间
HVAC 系统
站点边界
能源管理范围
时区
业务日历
节假日日历
天气源
天气站
投产状态
能力
配置版本
生效时间
变更影响
停用
退役
审计
```

可以保留：

```text
IANA Time Zone
WMO ID
BACnet Schedule
Commissioning
ASHRAE 223
ISO 50001
```

---

# 5. Mandatory Semantic Separation

```text
Organization ≠ Portfolio
Portfolio ≠ Site
Site ≠ Building
Building ≠ Space
Space ≠ Thermal Zone automatically

Display Name ≠ Canonical Identity
Address ≠ Site Identity
Coordinates ≠ Address

Site Boundary ≠ Building Footprint
Site Boundary ≠ Measurement Boundary
Site Boundary ≠ HVAC System Boundary
Site Boundary ≠ EnMS Scope automatically

HVAC System ≠ Equipment
System Boundary ≠ Semantic Relationship Graph
System Boundary ≠ Meter Boundary

Timezone ≠ UTC Offset
Timezone ID ≠ Timezone Abbreviation
Local Time ≠ UTC Timestamp

Business Calendar ≠ Control Schedule
Holiday Calendar ≠ Equipment Override
Calendar Exception ≠ Alarm Suppression

Weather Source ≠ Weather Data
Weather Source Assigned ≠ Weather Data Available
Nearest Weather Station ≠ Approved Weather Source automatically
Weather Source Changed ≠ Historical Weather Rewritten

Site Active ≠ Currently Occupied
Site Active ≠ All Integrations Healthy
Site Active ≠ All Capabilities Available

Commissioning State ≠ Runtime State
Commissioned ≠ Healthy
Commissioning Complete ≠ Functional Verification PASS automatically
Functional Verification PASS ≠ Site Commissioned automatically

Capability Enabled ≠ Integration Healthy
Capability Enabled ≠ Data Available
Capability Enabled ≠ User Authorized
Capability Enabled ≠ Safe to Control

Current Configuration ≠ Historical Configuration
Configuration Published ≠ Effective Now
Future Effective ≠ Active
Site Retired ≠ Historical Data Deleted
```

---

# 6. Core Domain Objects

35 至少治理：

```text
Organization
Portfolio / Business Unit
Site
Site Boundary
Building
Space
Operational HVAC System
Site Timezone
Business Calendar
Holiday / Exception Calendar
Weather Source Binding
Weather Station Reference
Commissioning State Record
Site Capability Configuration
Site Configuration Revision
Effective Period
Change Request / Review
Decommission / Retirement Record
Audit Record
```

32 继续治理：

```text
Canonical semantic relationships
Asset / Device / Point / Meter graph
hasPart / hasLocation / feeds / controls / hosts / hasPoint
Topology
Unit / Quantity
Semantic tags
```

35 不建立 parallel ontology。

---

# 7. Canonical Identity Contract

35 所有主对象必须引用稳定 Canonical Identity。

至少记录：

```text
Canonical ID
Entity Type
Display Name
Lifecycle State
Parent Governance Context
Effective Period
Configuration Revision
Owner
```

禁止：

```text
name = "Building A"
→ identity = Building A
```

Rename 不改变 Canonical ID。

---

# 8. 35 / 32 Authority Boundary

推荐责任：

```text
35
→ Site / Building / Space / Operational System 的行政配置与生命周期
→ timezone / calendar / weather / commissioning / capability

32
→ canonical semantic relation graph
→ equipment / device / point / meter relationships
→ system topology / meter topology / semantic tags
```

35 写入或引用同一 canonical identity service，不复制第二套 Site ID / Building ID。

如果发现：

```text
Building B incorrectly located under Site A
```

由 35 修正 governance containment。

如果发现：

```text
AHU-03 feeds Zone-07 relationship wrong
```

进入 32。

---

# 9. Organization Contract

Organization 至少有：

```text
Organization ID
Legal / Business Display Name
Status
Owner
Effective Period
External Reference if needed
```

Organization 不等于 Portfolio。

一个 Organization 可以包含多个 Portfolio / Site。

---

# 10. Portfolio Contract

Portfolio / Business Unit 用于业务分组，例如：

```text
华东商业地产
数据中心业务
工业制造事业部
```

Portfolio 是组织治理分组，不代表物理 containment。

禁止：

```text
same portfolio
→ same physical campus
```

---

# 11. Site Contract

Site 至少记录：

```text
Site ID
Display Name
Organization / Portfolio context
Site Type
Lifecycle State
Primary Address reference
Coordinates if owner-provided
IANA Timezone
Business Calendar
Weather Source Binding
Commissioning State
Capability Profile
Current Configuration Revision
Effective From
Owner
```

Site 是产品 Context 的核心 identity。

---

# 12. Site ≠ Building

Site 可以：

- 只有一栋 Building；
- 有多个 Buildings；
- 包含 campus / industrial process / outdoor plant；
- 包含没有传统 Building 的能源系统。

因此：

```text
Site ID ≠ Building ID
```

全产品不得默认“一个 Site 就是一栋楼”。

---

# 13. Site Boundary Contract

Site Boundary 至少有：

```text
Boundary ID
Purpose
Included physical / organizational scope
Excluded scope
Effective Period
Owner
Revision
Evidence / Reference
```

Boundary Purpose 可能是：

```text
Operational Site Boundary
EnMS Scope Boundary
Portfolio Reporting Boundary
Other governed purpose
```

不同 Purpose 不应复用一个模糊 polygon / string。

---

# 14. EnMS Scope / Boundary Contract

如果 Site 参与 ISO 50001 / 50001 Ready，应显式记录：

```text
EnMS Scope
EnMS Boundary
Included buildings/processes
Excluded areas/processes
Approval
Effective From
Revision
```

但：

```text
EnMS Boundary
≠ M&V Measurement Boundary
```

24 仍维护项目级 Measurement Boundary。

---

# 15. Building Contract

Building 至少有：

```text
Building ID
Site ID
Display Name
Building Type
Lifecycle State
Address / geometry reference if available
Gross Area if authoritative
Commissioning State
Effective Period
Owner
```

Gross Area 等数据必须有 owner / unit，不由 UI 从 Space 面积随意相加形成 authoritative value。

---

# 16. Building Lifecycle Contract

建议：

```text
Planned
Under Construction
Commissioning
Operational
Partially Closed
Decommissioning
Retired
```

Lifecycle State 是治理状态，不是 runtime availability。

---

# 17. Space Contract

Space 至少可表达：

```text
Space ID
Building / Site context
Space Type
Floor / Level reference
Area if authoritative
Lifecycle State
Effective Period
Owner
```

Space 可以代表：

```text
Room
Floor Area
Zone-like administrative area
Plant Room
Tenant Area
```

具体 semantic class 由 32 定义。

---

# 18. Space ≠ Thermal Zone

建筑物理 Space 与 HVAC Thermal Zone 不必一一对应。

可能存在：

```text
1 Space → multiple control zones
multiple Spaces → 1 thermal zone
```

禁止：

```text
room name same
→ thermal zone same
```

Thermal/air-side relationships进入 32 semantic model。

---

# 19. Operational HVAC System Contract

35 管理“运营上被视作一个系统”的主配置，例如：

```text
Central Chilled Water Plant
AHU System
Condenser Water System
Heating Hot Water System
VRF System
District Cooling Interface
```

至少记录：

```text
System ID
Site / Building context
System Type
Operational Boundary summary
Lifecycle State
Commissioning State
Capability Profile
Effective Period
Owner
```

---

# 20. System Boundary Contract

System Boundary 回答：

> 在业务运行和治理上，哪些对象属于这个 System 的责任范围？

但成员关系必须引用 32 authoritative semantic relationships / membership model。

35 不通过：

```text
name prefix
BACnet network number
same controller
same floor
```

自动猜 System membership。

---

# 21. System Boundary ≠ Meter Boundary

例如 Central Plant System 可以包含：

```text
Chillers
Pumps
Cooling Towers
Valves
Sensors
```

但 16 Efficiency / 24 M&V 的 meter boundary 可能只覆盖其中部分。

因此：

```text
System Boundary
≠ Measurement Boundary
≠ Meter Hierarchy
```

---

# 22. Site Timezone Contract

Site 必须使用 IANA Zone ID：

```text
Asia/Singapore
Asia/Shanghai
America/New_York
Europe/London
```

至少记录：

```text
Timezone ID
Effective From
Configured By
Revision
```

---

# 23. Timezone ≠ UTC Offset

禁止：

```text
UTC+08:00
```

作为唯一时区配置。

原因：

- DST / legal rule 会变化；
- 同一个 offset 可对应多个不同历史/未来规则；
- abbreviation 如 CST 存在歧义。

显示可以附带当前 offset，但权威配置仍是 IANA Zone ID。

---

# 24. Timezone Change Contract

Timezone 变更是高影响配置变更。

至少评估：

```text
Business Calendar
Report periods
Rule windows
Schedules
Historical rendering
Daily / Monthly aggregation
Tariff periods
M&V periods
Audit timestamps
```

禁止修改 timezone 后把历史 UTC timestamp 改写成新的物理发生时刻。

Historical event time 仍以原始 UTC / source time 保留。

---

# 25. Business Calendar Contract

Business Calendar 用于定义：

```text
Business Day
Weekend
Holiday
Shutdown Day
Special Operating Day
Accounting / Reporting Cutoff if owner-defined
```

至少记录：

```text
Calendar ID
Timezone
Base weekly pattern
Exception dates
Effective Period
Revision
Owner
```

---

# 26. Business Calendar ≠ Control Schedule

Business Calendar 可以被：

```text
Reporting
Benchmarking
Rule context
Occupancy assumptions if explicitly governed
```

消费。

但不能直接认为：

```text
Holiday
→ AHU Off
```

真正的 control Schedule 属于 25–27 Control / Strategy owner。

---

# 27. Weekly Pattern Contract

Calendar 可以定义：

```text
Monday-Friday: Business Day
Saturday: Weekend
Sunday: Weekend
```

但 Weekly Pattern 只是基础规则。

Exception Date 优先级和冲突处理必须明确。

---

# 28. Holiday / Exception Calendar Contract

Exception 至少有：

```text
Date / Range
Exception Type
Reason / Name
Priority / Precedence if needed
Source
Revision
```

例如：

```text
2026-10-01 → Holiday
2026-10-04 → Special Operating Day
```

不能用硬编码 national holiday library silently override Site Calendar。

---

# 29. Calendar Effective Period Contract

Calendar Revision 必须 effective-dated：

```text
CAL-01 rev4
2026-01-01 → 2026-12-31
```

新 revision 不改写已经结算/报告的历史 calendar context。

---

# 30. Calendar Change Impact Contract

Calendar change 可以影响：

```text
Rule evaluation windows
Demand analysis
Occupancy-context analysis
Reporting periods
Forecast assumptions
Strategy schedule references
```

必须显式 impact assessment。

不能：

```text
calendar changed
→ automatically rewrite historical reports
```

---

# 31. Weather Source Binding Contract

每个 Site 可以绑定一个或多个用途明确的 Weather Source：

```text
Operational Weather
Historical Weather
Forecast Weather
Normalization Weather
```

同一个 provider 不自动适用于所有用途。

---

# 32. Weather Source Identity Contract

至少记录：

```text
Weather Binding ID
Purpose
Provider / Dataset
Station / Grid / Location reference
Station ID / WMO ID if applicable
Variables
Cadence
Timezone / Time basis
Quality / availability metadata
Effective Period
Owner
```

---

# 33. Weather Source ≠ Weather Data

配置一个 Weather Source 只说明：

> 系统应该从哪里获取天气。

并不说明：

```text
Data Available
Data Complete
Data Valid
```

34 Integration + 31 Data Quality 继续提供运行事实。

---

# 34. Weather Station Selection Contract

Weather Station / Grid selection 必须由 owner 或明确算法产出并持久化。

可以参考：

```text
latitude / longitude
station coverage
historical completeness
elevation
provider methodology
```

但前端禁止：

```text
nearest station by straight-line distance
→ authoritative station
```

除非 Weather Source owner 明确规定该方法。

---

# 35. Weather Binding Change Contract

如果：

```text
Station A
→ Station B
```

必须记录：

```text
Reason
Effective From
Old / New Source
Impact Assessment
Reviewer if required
```

并明确：

```text
Weather Source Changed
≠ Historical Weather Rewritten
```

历史重新处理必须由下游 owner 形成新 revision。

---

# 36. Forecast Weather Boundary

Forecast Source 与 Historical/Observed Source 必须分开。

```text
Forecast
≠ Observed
```

天气预报不能在事后冒充 measured/observed weather。

---

# 37. Site Lifecycle Contract

建议 Site Governance State：

```text
Draft
Provisioning
Commissioning
Operational
Partially Operational
Decommissioning
Retired
```

State transition 必须审计。

---

# 38. Site Active ≠ Occupied

一个 Site 可以：

```text
Operational
但当前无人占用
```

也可以：

```text
Operational
但周末关闭办公区
```

所以：

```text
Site Lifecycle
≠ Occupancy State
```

Occupancy 属于实时/业务事实 owner。

---

# 39. Building / System Lifecycle Independence

Site 可以 Operational，但某个 Building 仍在 Commissioning。

Site 可以 Operational，但某个 HVAC System 已 Retired。

禁止把 Site 状态强制传播成所有子对象状态。

---

# 40. Commissioning State Contract

Commissioning 至少可表达：

```text
Not Started
Planning
Investigation / Testing
Implementation
Retest / Verification
Handoff Pending
Commissioned
Recommissioning
Decommissioned
Unknown
```

具体状态由 Commissioning owner 管理。

---

# 41. Commissioning State ≠ Runtime State

例如：

```text
System Commissioned
Runtime = Failed
```

完全合法。

或者：

```text
System in Commissioning
Runtime = Running
```

也完全合法。

---

# 42. Commissioned ≠ Healthy

`Commissioned` 说明完成特定 commissioning / handoff 过程。

它不代表：

```text
当前无故障
当前节能
当前舒适
所有传感器健康
```

运行健康由 04 / 07 / 10 / 31 等 owner 提供。

---

# 43. Functional Verification Boundary

13 Functional Verification 负责：

```text
Requirement
Test Definition
Test Run
Observed Evidence
PASS / FAIL / INCONCLUSIVE
```

35 只消费 owner-approved commissioning/handoff state。

```text
One Test PASS
≠ Commissioned automatically
```

同时：

```text
Commissioned
≠ Every individual test currently PASS
```

历史 test 与当前 commissioning governance state 分开。

---

# 44. Commissioning Handoff Contract

从 DOE commissioning handoff 思路，本页至少可以引用：

```text
Final Documentation
Systems Information
Known Open Items
O&M Handoff
Future Recommissioning Plan
Responsible Owner
Handoff Date
```

但详细 test evidence 仍进入 13。

---

# 45. Site Capability Contract

Capability Profile 用于决定某站点具备哪些产品能力，例如：

```text
Realtime Monitoring
Historical Trends
Alarm Management
Diagnostics
Work Orders
Energy Analysis
Billing
Carbon
DER
Control
Strategies
M&V
Reports
Rules
```

Capability 必须有 owner / source，不由前端硬编码。

---

# 46. Capability Enablement Contract

每个 Site Capability 至少记录：

```text
Capability ID
State
Reason
Dependencies
Enabled From
Disabled From if any
Owner
Revision
```

建议状态：

```text
Enabled
Disabled
Provisioning
Degraded
Unavailable
Unknown
```

---

# 47. Capability Enabled ≠ Integration Healthy

例如：

```text
Historical Trends Capability
Enabled

Telemetry Integration
Degraded
```

这时能力仍存在，但页面可能显示 Partial / Stale。

不能因为 Integration degraded 就把 capability 配置自动删除。

---

# 48. Capability Enabled ≠ Data Available

```text
M&V Capability Enabled
```

不代表：

```text
当前已有 M&V Project
当前数据完整
```

Empty / Not Ready 是独立状态。

---

# 49. Capability Enabled ≠ Permission

Global navigation 使用：

```text
Product capability exists
AND deployment/site capability exists
AND principal may discover it
```

因此：

```text
Site capability enabled
≠ User can see
≠ User can act
```

36 负责 Principal / Permission。

---

# 50. Capability Enabled ≠ Safe to Control

即使：

```text
Control capability = Enabled
```

仍不代表用户现在可以执行命令。

25 还要检查：

```text
Permission
Control Authority
Precondition
Interlock
Current State
```

---

# 51. Capability Dependency Contract

Capability 可以声明正式 dependencies，例如：

```text
Energy Analysis
→ meter semantic model
→ telemetry/history integration

Control
→ control gateway
→ authorization
→ execution owner
```

Dependency state 由对应 owner 提供。

35 不自己猜 dependency healthy。

---

# 52. Capability Provisioning Contract

启用新能力可以是 workflow：

```text
Requested
Validated
Dependencies Ready
Provisioning
Enabled
```

高风险 capability 如 Control / External Notification 可以需要 review / approval。

不能点击 toggle 立即假设所有依赖都已存在。

---

# 53. Configuration Revision Contract

站点配置必须 versioned。

一个 Site Configuration Revision 至少绑定：

```text
Site identity
Organization / Portfolio context
Building / Space governance refs
System configs
Timezone
Calendar refs
Weather bindings
Commissioning refs
Capability profile
Effective From
Author
Reason
Review / Approval if required
```

已生效 Revision 不原地修改。

---

# 54. Current vs Future Configuration Contract

允许：

```text
Current Revision rev8
Effective now

Future Revision rev9
Effective 2026-10-01
```

必须明确：

```text
Published ≠ Effective Now
```

未来版本不能提前影响当前计算。

---

# 55. Effective Date Contract

以下变化至少需要 Effective Date：

```text
Timezone
Calendar
Weather binding
Site boundary
Building / Space lifecycle
System boundary
Capability enablement
Commissioning state if historical reporting depends on it
```

Effective Date 不默认 `now()`。

---

# 56. Historical Configuration Contract

下游历史分析应能回答：

> 在那个时间点，Site 使用哪个 timezone / calendar / weather source / configuration revision？

所以：

```text
Current Configuration
≠ Historical Configuration
```

历史查询不能用最新配置重解释所有过去事实。

---

# 57. Configuration Change Impact Contract

每次实质变更必须评估影响：

```text
Navigation / Capability
Energy Analysis
Demand Analysis
Tariff / Billing periods
M&V
Rules
Reports
Management Review
Integrations
Semantic Model
Control / Strategy
Data Quality
```

Impact 不是自动 recomputation。

---

# 58. Timezone Change Impact

Timezone 变更特别需要评估：

```text
Daily boundary
Billing day
Rule windows
Trend rendering
Schedule interpretation
Report period
DST transitions
Audit display
```

禁止：

```text
timezone changed
→ rewrite event timestamps
```

---

# 59. Calendar Change Impact

Calendar 变更可以触发：

```text
Rule / Report / Forecast review
```

但：

```text
Calendar changed
≠ Historical Report Automatically Reissued
```

29 仍负责 Report Correction / Reissue。

---

# 60. Weather Change Impact

Weather binding change可能影响：

```text
Weather-normalized energy
EnPI / EnB models
Forecast
M&V models
Comfort context
```

由对应 owner 决定是否 recompute / new revision。

---

# 61. System Boundary Change Impact

如果 HVAC System 成员发生变化：

```text
System Efficiency
Alarm grouping
Diagnosis scope
Work scope
Strategy scope
Meter / M&V boundaries
```

都可能需要 review。

成员关系的具体变更与 validation 进入 32。

---

# 62. Site Split / Merge Contract

如果发生：

```text
Site A
→ Site A1 + Site A2
```

或者：

```text
Site A + Site B
→ Site C
```

必须创建明确的新 identity / lineage / effective date。

禁止通过 rename / parentId 修改让历史 Site identity 消失。

---

# 63. Building / Space Move Contract

空间或 Building governance context 调整必须：

```text
Old relationship effective-to
New relationship effective-from
Reason
Owner
Revision
```

不能覆盖旧 parent 后让历史位置关系消失。

---

# 64. Retirement Contract

Site / Building / System Retired 时：

```text
new operations disabled
new configuration changes limited
historical records retained
reports / audit still resolvable
```

明确：

```text
Retired ≠ Deleted
```

---

# 65. Delete Contract

已经产生业务历史的 Site / Building / System 默认不得物理删除。

如果 Draft 从未投入使用且 owner 允许删除，可以走显式 governance action。

禁止通用 CRUD：

```text
DELETE /site/:id
```

作为所有状态下的默认动作。

---

# 66. Address Contract

Address 是 Site / Building 的描述性地理事实。

至少区分：

```text
Postal Address
Coordinates
Timezone
Weather Source
```

不能：

```text
Address → infer timezone silently
Coordinates → infer weather station silently
```

可以提供 candidate suggestion，但必须明确确认并保存 authoritative value。

---

# 67. Coordinates Contract

Coordinates 至少记录：

```text
Latitude
Longitude
Source
Accuracy / confidence if available
Effective Period
```

前端不通过 geocoding silently overwrite owner-provided coordinates。

---

# 68. Geometry Boundary

复杂 site/building geometry 如果有 authoritative GIS/BIM owner，可引用其 geometry record。

35 不成为 GIS editor。

32 topology 也不等于 geometry。

---

# 69. Integration Handoff

34 提供：

```text
Integration
Source System
Connector Health
Capability
Mapping
```

35 可以把 Integration 绑定到 Site context。

但：

```text
Integration Bound to Site
≠ Integration Healthy
```

也不能在 35 保存 credential secret。

---

# 70. Data Quality Handoff

31 消费：

```text
Timezone
Calendar
Weather binding
Site / System effective config
```

作为数据解释上下文。

发现配置相关问题时可以产生 Data Quality / Governance Issue，但 31 不直接改 35 配置。

---

# 71. Rules Handoff

33 Rule 可以引用：

```text
Site Timezone
Business Calendar
Site Scope
System Scope
```

Rule Revision 必须记录实际引用的配置 revision / semantic binding。

日历修改不自动重写旧 Rule evaluation history。

---

# 72. Reporting Handoff

29 Report source snapshot 应能记录：

```text
Site Configuration Revision
Timezone
Calendar Revision
Weather Source Revision if relevant
```

之后站点配置变化不能静默改变旧报告。

---

# 73. Management Review Handoff

30 Management Review 可以看到：

```text
Site / System scope changes
Commissioning changes
Capability changes
Major configuration risks
```

但 30 Decision 不能直接修改 35 production configuration；必须形成配置 change workflow。

---

# 74. Control / Strategy Boundary

35 提供：

```text
Site / System context
Timezone
Calendar reference
Capability state
Commissioning state
```

但不直接维护：

```text
Setpoint
Schedule command
Strategy logic
Interlock
Override
Command
```

这些属于 25–28。

---

# 75. Business Calendar vs Strategy Schedule

Strategy 可以引用 Business Calendar 作为输入，但必须拥有自己的 versioned activation logic。

禁止：

```text
Business Holiday
→ Automatically Disable Strategy
```

除非 Strategy Version 明确这样定义。

---

# 76. Permission Contract

典型权限：

```text
Read Site Configuration
Create Draft Site
Edit Draft Configuration
Manage Building / Space Governance
Manage System Configuration
Manage Timezone
Manage Calendar
Manage Weather Binding
Manage Commissioning State
Manage Site Capabilities
Review Configuration Change
Approve High-impact Change
Publish Configuration
Retire Site / System
Read Audit
```

高影响变更可以要求 edit / approve 分离。

---

# 77. Audit Contract

至少审计：

```text
Organization / Portfolio assignment
Site create / rename / lifecycle
Site boundary change
Building / Space lifecycle
System boundary change
Timezone change
Calendar / holiday change
Weather source change
Commissioning state change
Capability enable / disable
Effective date change
Publish
Retire
Site split / merge
```

审计保留：

```text
Who
When
Before
After
Reason
Revision
Effective Date
Approval / Evidence
```

---

# 78. AI Assistance Boundary

AI 可以：

- 建议 missing configuration checklist；
- 识别 calendar / timezone inconsistency；
- 建议 candidate weather station；
- 总结 configuration impact；
- 检查 Site / Building / System 命名重复；
- 草拟 change review summary。

AI 不能：

- 自动更改 timezone；
- 自动选择并发布 weather station；
- 自动重构 Site / Building identity；
- 自动修改 system boundary；
- 自动启用 Control capability；
- 自动把 Commissioning 状态改成 Commissioned；
- 自动覆盖历史配置；
- 自动物理删除 Retired Site。

AI suggestion 必须保持 Candidate / Draft。

---

# 79. No Defensive Programming / No Compatibility Design

明确禁止：

```text
site API error
→ []

site timezone missing
→ browser timezone

site timezone missing
→ UTC

timezone
→ fixed UTC offset only

address present
→ infer timezone automatically

coordinates present
→ pick nearest weather station automatically

weather source unavailable
→ silently use another provider

weather data unavailable
→ previous weather

business calendar missing
→ Monday-Friday default

holiday calendar missing
→ public holiday library automatically

holiday
→ equipment off

calendar changed
→ rewrite historical reports

site active
→ occupied

site active
→ all integrations healthy

site operational
→ all buildings operational

commissioning complete
→ all tests PASS

functional verification PASS
→ commissioned

commissioned
→ healthy

capability enabled
→ integration healthy

capability enabled
→ data available

capability enabled
→ user authorized

control capability enabled
→ safe to control

system membership missing
→ infer from name

space name matches zone
→ same thermal zone

system boundary
→ meter boundary

new configuration revision
→ overwrite previous revision

published future revision
→ effective immediately

site retired
→ delete historical data

site merge
→ rewrite old site IDs

multiple configuration APIs
→ first success wins

legacy site settings fallback
legacy registry compatibility adapter
```

正式原则：

> **组织、站点、建筑、空间、系统、时区、日历、天气、投产和能力是不同配置事实。Timezone 不是 UTC Offset，Business Calendar 不是 Control Schedule，Commissioning State 不是 Runtime State，Capability Enabled 不是 Permission / Data / Control Authority。所有实质配置必须 versioned、effective-dated、可审计；历史配置不被当前配置静默改写；Unknown 保持 Unknown。**

---

# 80. Information Architecture

```text
Context Header
↓
[组织与站点] [建筑与空间] [系统] [日历与时区] [天气] [能力] [变更]
↓
Site Ledger
                          → Site Inspector
↓
Site Detail
  Identity / Lifecycle
  Scope / Boundary
  Buildings / Spaces
  Operational Systems
  Timezone
  Calendar
  Weather
  Commissioning
  Capability Profile
  Versions / Effective Date
  Change Impact
  Audit
↓
32 Semantic Model / 34 Integration / 31 Data Quality / 36 Access
```

默认是 Ledger + Durable Detail，不做卡片墙。

---

# 81. Route / URL State Contract

```text
/settings/sites
/settings/sites/:siteId
```

Search Params 可包括：

```text
view
organization
portfolio
siteType
lifecycle
commissioning
capability
weatherState
calendarState
q
revision
```

Site identity 进入 Path；view/filter/revision 进入 Search Params。

---

# 82. Capability Gating

35 可见条件：

```text
Site Configuration capability exists
AND principal may discover it
```

具体 action：

```text
principal permission
AND configuration lifecycle permits
AND required owner service available
```

没有 Calendar capability 时不显示伪造 calendar editor。

---

# 83. Site Ledger Contract

默认列：

```text
站点
组织 / Portfolio
类型
生命周期
时区
日历
天气源
投产状态
能力状态
当前版本
Owner
```

异常优先显示：

```text
Timezone Missing
Weather Binding Needs Review
Commissioning Pending
Capability Dependency Degraded
Future Revision Pending
```

---

# 84. Site Inspector Contract

快速 Inspector 显示：

```text
Site Identity
Organization / Portfolio
Lifecycle
Timezone
Calendar Revision
Weather Source
Commissioning State
Capability Summary
Current Revision
Future Effective Revision
Owner
```

复杂编辑进入 Durable Detail。

---

# 85. Site Detail Layout

推荐：

```text
概览
范围与边界
建筑与空间
HVAC 系统
时区与日历
天气源
投产状态
站点能力
版本与变更
审计
```

避免几十个平级 Settings Tabs。

---

# 86. Configuration Diff Contract

版本评审应显示业务语义 diff，例如：

```text
rev8 → rev9

Timezone
Asia/Shanghai → Asia/Singapore

Business Calendar
CAL-03 rev4 → rev5

Weather Source
GSOD Station A → Station B

Control Capability
Disabled → Provisioning
```

而不是 JSON diff 为主视图。

---

# 87. Calendar Workspace Contract

Calendar 编辑至少可视化：

```text
Weekly Pattern
Holiday / Exception Days
Effective Period
Timezone
Conflicts
Referenced By
```

保存前验证 overlapping / invalid exceptions。

---

# 88. Weather Workspace Contract

至少显示：

```text
Purpose
Provider
Station / Grid
Station ID
Location
Variables
Cadence
Coverage metadata
Integration State
Data Quality link
Effective Period
Revision
```

不能只显示：

```text
Weather = Singapore
```

---

# 89. Commissioning Workspace Contract

至少显示：

```text
Object / Scope
Commissioning State
Owner
Started At
Handoff Target
Open Items
Functional Verification references
Final Documentation reference
Recommissioning plan
History
```

不在本页执行 test run。

---

# 90. Capability Workspace Contract

至少显示：

```text
Capability
Configured State
Dependencies
Dependency Status
Effective From
Owner
Reason
```

UI 不使用一排无上下文 toggle。

高风险 capability activation 必须显示依赖与 review。

---

# 91. Empty / Partial / Unknown / Error States

## Empty

新 Organization 没有 Site 是合法 Empty。

## Partial

Site config 可读，但 Weather / Integration owner 暂不可用。

## Unknown

例如 Commissioning owner 没有提供状态。

## Error

Configuration service request failed 明确显示 Request Failed。

禁止：

```text
error → no sites
unknown commissioning → not commissioned
unknown weather → default station
```

---

# 92. Accessibility / Responsive Contract

- Lifecycle / Commissioning / Capability 不只靠颜色；
- 时区必须显示完整 IANA ID；
- Calendar exceptions 有 textual list；
- Weather station 不只依赖地图；
- Configuration Diff 可由 screen reader 理解；
- 768px 下仍能完成 Site lookup、查看 timezone/calendar/weather/commissioning/capability 和 revision；
- 高影响变更确认不因窄屏而省略 impact / effective date。

---

# 93. Wireframe Intent

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ 站点与系统配置                                         生产环境            │
├────────────────────────────────────────────────────────────────────────────┤
│ [组织与站点] [建筑与空间] [系统] [日历与时区] [天气] [能力] [变更]         │
├────────────────────────────────────────────────────────────────────────────┤
│ 站点                 生命周期   时区            日历       投产      版本  │
│ 新加坡中央园区       运行中     Asia/Singapore CAL-03 v5  已投产    rev8  │
│ 上海研发中心         运行中     Asia/Shanghai  CAL-02 v4  已投产    rev6  │
│ 深圳二期             投产中     Asia/Shanghai  CAL-05 v1  调试中    rev2  │
├───────────────────────────────────────────────┬────────────────────────────┤
│ 新加坡中央园区                              │ 快速检查                   │
│ Scope: Campus SG-01                         │ Current rev8               │
│ Timezone: Asia/Singapore                    │ Future rev9 · 2026-10-01  │
│ Calendar: CAL-03 rev5                       │ Commissioned               │
│ Weather: GSOD / WMO 48698                   │ Capabilities: 18 Enabled   │
│ Buildings: 4 · Operational Systems: 12      │ 1 Provisioning            │
├────────────────────────────────────────────────────────────────────────────┤
│ 待生效变更 rev9                                                             │
│ Weather Source Station A → Station B                                        │
│ Effective: 2026-10-01                                                       │
│ Impact: EnPI / Forecast / M&V models require review                         │
│ [查看变更影响] [审批]                                                       │
└────────────────────────────────────────────────────────────────────────────┘
```

---

# 94. Browser Acceptance Criteria

## Identity / Hierarchy

- Organization / Portfolio / Site / Building / Space 分开；
- Site / Building 使用稳定 identity；
- Rename 不改变 identity；
- Space 不自动等于 Thermal Zone；
- 35 不建立第二套 semantic graph。

## Scope / Boundary

- Site Boundary / EnMS Scope / System Boundary / Measurement Boundary 分开；
- System membership 不通过名称猜；
- System Boundary 不冒充 Meter Boundary。

## Time / Calendar

- Site 使用 IANA Time Zone；
- Timezone 不以固定 UTC offset 代替；
- Business Calendar / Control Schedule 分开；
- Holiday 不直接控制设备；
- Calendar revision / effective date 可追溯。

## Weather

- Weather Source / Weather Data 分开；
- Station / Provider / Purpose / Effective Period 可见；
- nearest station 不自动成为 authoritative；
- source change 不改写历史。

## Commissioning

- Commissioning / Runtime / Health 分开；
- One Test PASS 不自动 Commissioned；
- Commissioned 不显示 Healthy；
- Handoff / open items 可追溯。

## Capability

- Enabled / Dependency / Permission / Data Availability / Control Safety 分开；
- Capability 不使用无上下文 toggle；
- Control capability enabled 不冒充可执行控制。

## Revision / History

- Config revision immutable；
- Future revision 不提前生效；
- Historical config 可解析；
- Site retire 不删除历史；
- split / merge 保留 identity lineage。

## Implementation Integrity

- 无 browser timezone fallback；
- 无 silent weather provider fallback；
- 无 name-based system inference；
- 无 legacy site settings compatibility adapter；
- review scenario 无 runtime / network error。

---

# 95. Explicit Non-goals

35 不是：

- Asset / Device / Point registry；
- Semantic ontology editor；
- GIS / BIM editor；
- BACnet live schedule editor；
- Weather analytics；
- Functional Verification executor；
- User / RBAC admin；
- Integration credential manager；
- Control Strategy editor；
- Generic organization HR directory；
- Universal settings page。

---

# 96. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Organization / Portfolio / Site / Building / Space 分离；
- 35 / 32 authority boundary 明确；
- Site / Building / System lifecycle 明确；
- Site / EnMS / System / Measurement Boundary 分离；
- IANA Time Zone 明确；
- Timezone / UTC Offset 分离；
- Business Calendar / Control Schedule 分离；
- Calendar Revision / Effective Date 明确；
- Weather Source / Weather Data 分离；
- Weather Binding versioned；
- Commissioning / Runtime / Health 分离；
- Functional Verification / Commissioning 分离；
- Capability / Integration / Permission / Control Authority 分离；
- Configuration Revision / Effective Date / Change Impact 明确；
- Current / Future / Historical Configuration 分离；
- Retired / Deleted 分离；
- 31 / 32 / 34 / 25–30 / 36 责任边界明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
