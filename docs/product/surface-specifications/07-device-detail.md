# 07 设备详情 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `07 设备详情`  
> **Route intent：** `/sites/:siteId/devices/:deviceId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Device Detail、旧 Drawer、旧 Asset Detail、旧卡片堆叠、旧 Ant/ProComponents 页面或旧设计稿。当前代码仅可在实施阶段作为真实 Registry / Semantic Model / Telemetry / Alarm / Diagnosis / Work / Control owner、capability、permission 与 route contract 的候选证据来源。

---

# 1. Primary Job

设备详情的唯一核心任务是：

> **围绕一个明确、稳定的真实设备对象，持续完成“身份确认 → 当前运行判断 → 当前事项理解 → 证据查看 → 关系理解 → 工程数据下钻”，并在需要专业动作时进入对应 durable workflow。**

设备详情是 **single-asset investigation workspace**，不是：

- 设备中心的一行简单放大；
- BAS Object Browser；
- 通用卡片墙；
- 全量 Alarm Center；
- 完整 Diagnosis Center；
- Work Order Center；
- Control Center；
- Strategy Editor；
- CMMS；
- ERP / EAM；
- 网络安全控制台；
- 设备远程“万能控制页”。

用户离开本页前应该已经知道：

1. 这台设备是什么、在哪里、属于哪个系统；
2. 它的 lifecycle / operating / connectivity / freshness / quality 等独立状态是什么；
3. 当前最关键的 4–8 个运行事实是否可信；
4. 当前是否存在 Alarm / Finding / Work / Override / Out-of-Service / Data issue；
5. 最近证据是否支持继续调查；
6. 它与系统、父设备、服务区域、传感器/执行器/计量对象有哪些 authoritative 关系；
7. 专业工程点位、来源和元数据在哪里；
8. 下一步应该进入 Trend、Alarm、Diagnosis、Work、Control、Verification、Data Quality 或 Semantic Model 哪个负责页面。

---

# 2. 主要用户

## Primary

### HVAC 运行 / 调试工程师

围绕单台设备持续查看当前模式、关键过程量、点位证据、关系和最近运行事件。

### 维修 / 维护工程师

确认设备身份、运行状态、当前告警/工单、关键值和整改后的验证入口。

### 诊断工程师

从 Alarm / Finding 进入设备详情，核对设备级事实与 supporting evidence。

## Secondary

- 控制工程师：核对 control authority、setpoint、override、command/readback 摘要后进入 Control / Strategy；
- 能源工程师：核对 power/runtime/关键过程量后进入 Trend / Efficiency；
- 数据工程师：核对 freshness / quality / point coverage / provenance 后进入 Data Quality；
- 资产负责人：查看 identity、vendor/model/serial/lifecycle 等 authoritative metadata。

## 不作为主要目标用户

- 企业管理层：应使用 Portfolio / Site Overview；
- 需要批量筛选设备的人：应使用 Device Center；
- 需要批量修改 Registry 的人：应使用 Semantic Model / Data & Integration 管理页面。

---

# 3. 外部最佳实践依据

## 3.1 ISO 55001:2024 — 设备是稳定资产对象，生命周期、性能、风险和支出需要分离理解

ISO 55001:2024 将资产管理定义为系统化管理资产生命周期，以平衡 performance、risk 与 expenditure，并持续实现组织目标。

来源：

- https://www.iso.org/standard/83054.html

**本页采用：**

- Asset identity / lifecycle 是稳定事实；
- `Decommissioned`、`Out of Service`、`Offline`、`Stopped` 不合并；
- 设备详情可呈现与资产性能和生命周期有关的事实，但不替代 EAM / ERP；
- 资产 metadata、运行数据、维修事实保持 provenance。

## 3.2 NIST SP 800-82 Rev. 3 — OT asset detail 应能准确识别设备、位置、型号、固件和责任角色

NIST SP 800-82 Rev. 3 的 Asset Management 指导强调 OT inventory 的完整和准确，并建议维护 unique identifier、device details、location、vendor/model/serial、software/firmware、asset owner / operations / maintenance 等责任信息。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final
- https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-82r3.pdf

**本页采用：**

- 设备详情必须能确认 authoritative identity；
- vendor / model / serial / firmware 属于 professional metadata；
- operation / maintenance responsibility 可以作为管理事实；
- internal UUID 可以是 machine key，但不能成为用户 primary label；
- firmware / configuration 仅呈现 owner 事实，不在详情页直接执行升级。

## 3.3 DOE Semantic Modeling / ASHRAE 223 — 设备、系统、空间、传感器和执行器关系必须显式建模

DOE 对 semantic modeling 的说明强调：设备、系统、属性、关系、传感器与执行器应该以标准化、可查询方式表达，不能长期依赖 point naming / manual mapping。

来源：

- https://www.energy.gov/cmei/buildings/semantic-modeling-and-interoperability
- https://www.energy.gov/cmei/buildings/ashrae-standard-223p
- https://www.energy.gov/cmei/buildings/control-platforms

**本页采用：**

- Device → System / Parent / Space / Sensor / Actuator / Meter 的关系来自 Semantic Model；
- 不从 device name / point prefix 猜关系；
- Relationships 是设备详情的一等 section；
- 如果没有 authoritative semantic relation，就显示 `未关联 / 未建模`，不画假 graph。

## 3.4 BACnet — Device / Object / Trend / Status 都是不同类型的标准化对象与事实

BACnet 是 building automation 的互操作标准；设备可通过 Device Object、对象列表、Vendor / Model 等属性表达能力，Trend Log 记录带 timestamp 的历史数据和 status information。

来源：

- https://bacnet.org/
- https://bacnet.org/wp-content/uploads/sites/4/2022/06/The-Language-of-BACnet-1.pdf
- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-2001-135b.pdf

**本页采用：**

- device identity、point/object identity、current value、history、status 不是一个字段；
- point 表允许表达 Analog / Binary / Multi-state / Setpoint 等不同语义；
- `Writable` 是对象能力/metadata，不代表当前用户可以安全直接写；
- 趋势记录与当前值分别由 historian / telemetry owner 提供。

## 3.5 DOE EMIS / Operations Support — 设备详情用于 Validate / Investigate，不替代 Diagnosis / Corrective Action / Verification

DOE EMIS 运营方法强调：Identify/Prioritize → Validate/Diagnose/Triage → Corrective Action → Verify Improvement → Monitor/Maintain。

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- 设备详情负责单对象事实核对与持续调查；
- Diagnosis conclusion 由 Diagnosis Center 拥有；
- corrective action 由 Work / Control / Strategy owner 拥有；
- functional verification 由 Verification Surface 拥有；
- 设备详情只提供摘要和 durable deep-link。

## 3.6 DOE OpenBuildingControl — 控制实现需要 specification / execution / verification 分离

DOE OpenBuildingControl 将 control sequence 的设计、部署、测试和 verification 明确分开，并强调 formal verification of correct implementation。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control

**本页采用：**

- 可以展示当前 mode / stage / setpoint / command-readback 摘要；
- 不在 Device Detail 直接做高风险 write；
- `command attempted` ≠ `readback reached` ≠ `function verified`；
- 需要操作时进入 Control / Strategy / Verification。

## 3.7 Siemens Building X Operations Manager — 单设备实时事实应连接 fault、history、work order，但职责分离

公开产品能力将 equipment data、historical charts、fault investigation 和 work-order integration 连接起来。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- Device Detail 提供跨域摘要；
- 完整 fault、trend、work 进入各自专业 Surface；
- 用户从同一设备对象持续调查，不重复寻找对象。

## 3.8 Schneider EcoStruxure Building Operation — 对象、Alarm、Trend 和 Schedule/状态可以互相导航

EcoStruxure Building Operation 将 objects、alarms、trends、schedules 等作为相互连接但独立的 operational views。

来源：

- https://sqa.ecostruxure-building-help.se.com/bms/Topics/show.castle?id=9698&locale=en-US&productversion=2024
- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=7257&locale=en-US&productversion=2024

**本页采用：**

- 设备详情是对象中心；
- Alarm / Trend / Work 通过对象关系下钻；
- 不把其他 workflow 整体嵌入设备详情。

## 3.9 W3C APG — Tabs 只用于真正 peer content；复杂专业信息适合 Disclosure / semantic Table

W3C APG 将 Tabs 定义为互斥显示的一组 peer panels；Disclosure 用于逐步展开低频内容；tabular information 应使用结构化 table semantics。

来源：

- https://www.w3.org/WAI/ARIA/apg/patterns/tabs/
- https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/
- https://www.w3.org/WAI/tutorials/tables/

**本页采用：**

- 不做 `设备 / 告警 / 工单 / 能源` 这种跨 workflow Tabs；
- 默认使用稳定 page sections + in-page section navigation；
- Engineering metadata / provenance 可以 Disclosure；
- Point list 使用 semantic Table，不使用无必要的 ARIA Grid。

---

# 4. Primary Questions

用户进入后按以下顺序回答。

## Q1 — 我正在看哪台设备？

必须清楚显示：

- Site；
- Display Name；
- Equipment Type；
- System / Parent；
- Location；
- lifecycle；
- return-to-source（如果从 Alarm / Diagnosis / Work / Trend 进入）。

## Q2 — 设备现在处于什么状态？

必须分别表达：

- Operating；
- Connectivity / Presence；
- Freshness；
- Quality；
- Alarm；
- Finding；
- Override；
- Out of Service；
- Work / Maintenance；
- Control authority（若有 owner）。

不允许一个 `Health` 覆盖这些事实。

## Q3 — 当前最重要的运行事实是什么？

通过 domain-owned equipment presentation profile 显示约 4–8 个：

- key measured values；
- setpoint；
- mode / stage；
- load / power；
- runtime state；
- command/readback summary；
- efficiency-related fact（若本设备具备）。

## Q4 — 当前有什么需要处理或调查？

通过 authoritative current attention 显示：

- active alarms；
- active findings；
- open / overdue work；
- active override；
- data quality issue；
- verification required。

## Q5 — 最近发生了什么？

通过 limited recent evidence 回答：

- 关键值短历史；
- recent mode / stage changes；
- recent alarm/finding/work/control events；
- source evidence window（如果从调查链进入）。

需要完整时间分析时进入 Trend Analysis。

## Q6 — 这台设备和系统里的其他对象是什么关系？

必须能够理解：

- belongs-to system；
- parent / child equipment；
- served space / zone；
- associated meter；
- sensors / actuators；
- upstream / downstream relation（仅当 semantic owner 明确）。

## Q7 — 工程师如何检查完整点位和来源？

进入 Engineering Points / Metadata section：

- point identity；
- semantic role；
- current value；
- unit；
- freshness；
- quality；
- source；
- object type；
- writable capability metadata（只读呈现）；
- historian availability。

---

# 5. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/devices/:deviceId
```

`deviceId` 是 canonical durable identity，允许内部使用 UUID / canonical key，但用户界面显示 human-readable identity。

推荐 Search Params：

```text
section        // overview | evidence | relationships | points | metadata
point          // selected point reference, only when deep-linking engineering data
source         // semantic source kind, if global context contract uses it
sourceId       // durable source identity when safe / authorized
```

如果 Global Navigation Contract 已有标准 Return-to-Source envelope，优先复用，不新增平行 source 机制。

不把以下写入 URL：

- tooltip；
- temporary disclosure state；
- current stream value；
- expanded row pixel state；
- local hover；
- transient chart cursor。

## 5.1 Section State

设备详情是一个 durable route。

内部 section 可以通过：

- anchor / section navigation；
- `section` search param（需要可分享 deep-link 时）。

不要为每个小 section 再建立独立 route。

---

# 6. Entry Contract

## 6.1 从 Device Center

携带：

- Site；
- Device；
- Device Center source；
- 可返回原 filters / page / selected context。

## 6.2 从 System Operations

携带：

- Site；
- System；
- Device；
- source trail；
- 当前 operational context。

Device Detail 默认显示 current truth，不复制 System Operations 整个 topology。

## 6.3 从 Alarm Center

携带：

- Alarm source；
- Device；
- evidence window；
- Return-to-source。

设备详情可以显示 source context，但不把 evidence window 强制变成全页默认 time filter。

`查看完整证据` 应进入 Trend Analysis 并携带该 window。

## 6.4 从 Diagnosis Center

携带：

- Finding source；
- Device；
- evidence window；
- supporting points（若 owner 提供）。

## 6.5 从 Work Order / Verification

携带：

- Work / Verification source；
- Device；
- relevant evidence window；
- source trail。

## 6.6 从 Trend Analysis

携带：

- Device；
- selected point（若适用）；
- source evidence context。

Device Detail 可以直接定位 Engineering Point / Current Fact，但不把 Trend 的所有 chart state 复制进本页。

---

# 7. Exit Contract

设备详情提供 durable professional exits：

- 05 Trend Analysis；
- 04 System Operations；
- 09 Alarm Center；
- 10 Diagnosis Center；
- 11/12 Work Order；
- Functional Verification；
- 25 Control Center；
- Strategy Detail；
- 31 Data Quality；
- Semantic Model / Integration 管理（有权限时）。

保持 compatible：

```text
Site
Device
Source
Evidence Window
Investigation Trail
```

完整 workflow 不在设备详情内嵌完成。

---

# 8. Page Information Architecture

```text
Device Header
  Identity · type · system · location · lifecycle
  return-to-source · last authoritative update

Independent State Strip
  Operating · Connectivity · Freshness · Quality
  Alarm · Finding · Override · OOS · Work · Control Authority

Current Operation
  4–8 key operational facts
  mode / stage / setpoint when applicable

Current Attention
  Alarm / Finding / Work / Data / Verification summaries

Recent Evidence
  small number of domain-owned key series / recent events
  source evidence context
  deep-link to Trend Analysis

Relationships
  system / parent / served space / meters / sensors / actuators

Engineering Points
  searchable semantic point table

Asset Metadata & Provenance
  vendor / model / serial / firmware / source / ownership / timestamps

Professional Exits
```

页面使用**稳定 section hierarchy**，不是几十张同权重 Card。

---

# 9. Device Header Contract

Header 需要立即确认对象身份。

显示：

```text
Display Name
Equipment Type
System
Location
Lifecycle
Business Asset Code (if useful)
```

辅助事实：

- Registry source / authoritative update time；
- source trail；
- capability-gated action links。

Header 不显示：

- UUID 作为主标题；
- 10 个 badge；
- 20 个 metadata field；
- 大量实时数字。

实时事实进入 State / Current Operation。

---

# 10. Independent State Strip

必须保持与 06 Device Center 相同的状态语义。

推荐顺序：

```text
Operating
Connectivity
Freshness
Quality
Alarm
Finding
Override
Out of Service
Work
Control Authority
```

只显示 owner 实际支持的维度。

## 10.1 状态语义不可互相推断

```text
Offline != Fault
Stopped != Fault
Stale != Offline
Bad Quality != Alarm
Alarm != Finding
Work Open != Device Fault
Override != Out of Service
Out of Service != Decommissioned
Command Attempt != Readback
Readback != Verification
```

## 10.2 Visual Weight

正常事实保持 neutral。

只有：

- abnormal；
- action-required；
- unavailable；

获得额外视觉权重。

禁止整页因为一个 minor state 变红。

---

# 11. Current Operation

Current Operation 是设备详情首个主要业务 section。

显示约 **4–8 个 domain-relevant current facts**。

来源必须是：

> Equipment presentation profile / semantic metadata / domain-owned contract

而不是前端 if/else 猜测。

## 11.1 Examples

Chiller（示例，不是硬编码规则）：

```text
Operating mode
Load %
Power kW
CHWS
CHWR
ΔT
Current stage
Setpoint
```

Pump：

```text
Run state
Speed / Hz
Power
Flow
Differential pressure
Setpoint
```

AHU：

```text
Mode
Supply air temperature
SAT setpoint
Fan state / speed
Static pressure
Static pressure setpoint
Outdoor air damper
```

## 11.2 Value Contract

每个 current value 必须知道：

- value；
- unit；
- observation timestamp；
- freshness；
- quality。

真实 `0` 显示 `0`。

Missing / invalid 不转 `0`。

Stale value 不能只显示数值而隐藏 stale。

---

# 12. Mode / Stage / Setpoint / Sequence Summary

对于 control-rich equipment，Current Operation 中允许显示：

- operating mode；
- current stage；
- active schedule / enable state；
- active setpoints；
- lead / lag role；
- sequence state summary；
- active interlock summary（若 owner 提供）。

专业 detail 可以 Disclosure 展开：

- current sequence state；
- transition reason；
- reset logic summary；
- interlock status；
- enable / disable reason。

完整控制逻辑、策略编辑和 function test 不在这里完成。

---

# 13. Control Boundary

设备详情默认**只读**。

允许显示：

- control authority；
- current setpoint；
- active override；
- last command attempt summary；
- readback；
- active strategy；
- verification state。

明确禁止直接在普通详情 section 提供：

```text
Start
Stop
Setpoint write
Override
Release override
Strategy publish
High-risk command
```

这些进入 Control Center / Strategy Detail。

## 13.1 Writable Point != Authorized Action

Engineering Point 表中的 `Writable` 只能表示 source/object capability metadata。

它不能直接生成 editable input。

用户是否能控制，还需要：

- permission；
- control authority；
- safety / interlock；
- command workflow；
- audit；
- verification。

---

# 14. Current Attention

设备详情只显示与当前 device 直接关联、当前需要知道的事项摘要。

## 14.1 Alarm

来自 Alarm domain：

- active count；
- highest severity；
- concise active items；
- recovery state。

不在本页 ACK / Assign / Suppress。

## 14.2 Finding / Diagnosis

来自 Diagnosis owner：

- active / recent Finding；
- publication time；
- confidence（仅在 owner 定义其语义时）；
- evidence-limited state。

Finding 不是 proven root cause。

## 14.3 Work

来自 Work owner：

- open work；
- owner；
- due / overdue；
- next action。

不在本页完成完整 lifecycle mutation。

## 14.4 Data Issue

来自 Data Quality owner：

- stale point coverage；
- bad / suspect points；
- integration issue；
- semantic metadata issue。

## 14.5 Verification

如果 corrective action / control strategy 需要 Functional Verification：

- `待验证` / `验证中` / `已验证` / `不通过`；
- latest verification run；
- durable link。

---

# 15. Recent Evidence

Device Detail 可以提供有限的 recent evidence，但不能取代 Trend Analysis。

## 15.1 Purpose

回答：

> “这台设备最近有没有明显变化，是否值得进入完整趋势调查？”

## 15.2 Series Count

默认 **1–3 个 domain-owned primary evidence series**。

不能把所有 points 绘制成 chart。

## 15.3 Time Window

窗口必须明确标识。

可能是：

- 产品定义的 recent window；
- source Alarm / Diagnosis / Work 带入的 evidence window。

不能无提示重置 source evidence context。

## 15.4 Representation

- 同 unit 可 overlay；
- 不同 unit 优先 aligned small multiples；
- missing data 显示 gap；
- quality / aggregation semantics 保持与 05 Trend Analysis 一致。

## 15.5 Exit

`查看完整趋势` → `/sites/:siteId/trends`，带 device / series / evidence window。

---

# 16. Recent Meaningful Events

可以在 Recent Evidence 下方显示少量 object-related events：

- Alarm activate/recover；
- Finding published；
- Work started/completed；
- mode/stage transition；
- control attempt/readback；
- verification result；
- metadata/config change（若 authoritative）。

不要显示：

- 每个 stream tick；
- 每个 tooltip；
- 每个无业务意义字段更新；
- 所有评论日志。

每个 event 链回 owner Surface。

---

# 17. Relationships

Relationships 是设备详情的一等专业 section。

## 17.1 Default Representation

默认使用**结构化关系列表 / grouped relation rows**，不是 graph-first。

例如：

```text
Belongs to system
  冷冻水系统

Parent equipment
  CH-Plant-01

Serves
  B1–B6 chilled-water loop

Associated meters
  CH-02 Power Meter

Sensors
  CHWS Temp Sensor
  CHWR Temp Sensor

Actuators
  CH-02 Enable
  CH-02 CHWS Setpoint
```

## 17.2 Graph Policy

只有 semantic owner 提供真实关系，而且 graph 能明显改善理解时，才允许 optional relationship graph。

禁止：

- 根据名称拼 graph；
- 根据共同 point 前缀猜 parent；
- 根据功率/流量猜 upstream/downstream；
- 用装饰性连线制造系统关系。

## 17.3 Relationship Exit

相关对象名称可进入对应 Device Detail / System Operations / Semantic Model。

---

# 18. Engineering Points

这是专业层，不是默认首屏主体。

## 18.1 Point Table

使用 semantic Table。

推荐列：

| 列 | 内容 |
|---|---|
| Point | Human-readable point name |
| Role | Semantic role / measurement type |
| Current value | value + unit |
| Freshness | Fresh / Stale / Unknown |
| Quality | Good / Suspect / Bad / Unknown |
| Source | protocol / integration / owner |
| History | historian available / unavailable |

Optional engineering columns：

- object type；
- raw source identifier；
- writable capability；
- sample interval；
- last observation timestamp；
- engineering range；
- point lifecycle；
- semantic tags / class。

## 18.2 Point Search / Filter

允许：

- search human-readable point name；
- semantic role filter；
- point type filter；
- stale / quality filter；
- history availability filter。

不要默认展示 12 个 filter。

## 18.3 Point Selection

选中 point 可以显示 compact Point Inspector：

- full label；
- current value；
- unit；
- timestamp；
- quality；
- source；
- semantic role；
- history availability；
- `查看趋势`。

Point Inspector 不提供直接 write control。

---

# 19. Point Truth Semantics

## Current value

来自 Telemetry Snapshot owner。

## Freshness

来自 Telemetry / Data Quality policy。

## Quality

来自 owner quality semantics。

## History

来自 Historian capability。

## Writable

来自 integration / object metadata。

`Writable = true` 不代表：

- 当前用户有权限；
- 当前设备允许控制；
- 写入是安全的；
- interlock 满足；
- 操作不需要审批。

---

# 20. Asset Metadata

Metadata section 使用 progressive disclosure。

## Default metadata

- Asset Code；
- Vendor；
- Model；
- Serial；
- Firmware / Software version；
- Commissioned date（若 owner 提供）；
- Lifecycle；
- Owner / Maintainer；
- Registry source。

## Advanced metadata

- internal canonical id；
- integration source；
- protocol address；
- semantic class；
- schema version；
- last metadata update；
- provenance。

Internal IDs 和 protocol addresses 只能在 engineering context 下显示，不能成为业务主视觉。

---

# 21. Provenance Contract

设备详情必须帮助专业人员回答：

> “这个事实从哪里来，什么时候观测/更新？”

对关键事实可按需展开：

```text
Fact owner
Source system
Observation timestamp
Ingest timestamp (if needed)
Quality
Semantic mapping version (if relevant)
```

不要默认把所有 provenance 铺满首屏。

---

# 22. Audit / Change Boundary

设备详情可以显示 concise recent changes，例如：

- Registry metadata changed；
- Firmware/version changed；
- semantic relationship changed；
- control authority changed；
- lifecycle changed。

完整审计记录属于 Users / Permissions / Audit 或对应 owner 的 Execution / Audit Surface。

不要在设备详情复制整套 audit log。

---

# 23. Section Navigation

推荐稳定 section navigation：

```text
Overview
Evidence
Relationships
Engineering Points
Metadata
```

实现可以是：

- sticky in-page nav；
- anchor nav；
- responsive dropdown / section picker。

## 23.1 Tabs Policy

默认不使用以下 Tabs：

```text
设备 | 告警 | 工单 | 能源 | 控制
```

因为这些是不同 durable jobs。

如果未来使用 Tabs，只允许同对象、同层级 peer views，例如：

```text
Current Points | Historical Point Coverage
```

并遵循 W3C Tabs keyboard contract。

---

# 24. Data Authority Contract

## Identity / Type / Location / Lifecycle / Metadata

Owner：Registry / Asset Model。

## Relationships / Semantic Role

Owner：Semantic Model。

## Operating / Mode / Stage / Sequence

Owner：Operations / Control state contract。

## Connectivity / Presence

Owner：Realtime presence / connectivity domain。

## Current Values

Owner：Telemetry Snapshot。

## Freshness / Quality

Owner：Telemetry / Data Quality。

## Historical Evidence

Owner：Historian / time-series domain。

## Alarm

Owner：Alarm domain。

## Finding

Owner：Diagnosis domain。

## Work

Owner：Work Order domain。

## Control / Override / Authority / Readback

Owner：Control domain。

## Verification

Owner：Functional Verification domain。

Frontend 只做对象中心投影，不成为这些事实的新 owner。

---

# 25. Query / Read Model Contract

设备详情不能通过 ad hoc N+1 拼装大量点位。

推荐：

```text
Device Identity Query
  + Batch Current Snapshot for configured key facts
  + Domain Summary Queries
  + Paginated / filtered Engineering Point Query
  + Relationship Query
```

或经过正式架构定义的 single-device read model，但必须保留 provenance 和 domain ownership。

禁止：

```text
1 device
→ 1 request per point
→ 1 request per alarm
→ 1 request per finding
→ 1 request per work item
```

Engineering Points 可以独立加载，不阻塞首屏 current operation。

---

# 26. Snapshot / Stream Realtime Contract

设备详情实时语义：

```text
Snapshot = 当前设备的权威当前事实
Stream = 当前设备已授权事实的增量更新
```

订阅范围：

- current Device；
- configured key current facts；
- selected Engineering Point（若需要）；
- relevant domain event summary（owner 支持时）。

不要订阅全站全部 telemetry。

Realtime update：

- 原位更新 current facts；
- 不重排 section；
- 不抢 focus；
- 不关闭 Disclosure；
- 不改变 selected point；
- 不把 stream disconnect 当 device offline；
- 不重置 Evidence chart zoom。

Reconnect：

- 重新取 authoritative Snapshot；
- 再恢复 Stream；
- 不用旧 stream cache 冒充 current truth。

---

# 27. Loading Strategy

页面分层加载：

## Critical first

- identity；
- lifecycle；
- independent states；
- key current facts；
- current attention summaries。

## Secondary

- recent evidence；
- relationships。

## Professional / deferred

- full Engineering Points；
- advanced metadata；
- provenance detail。

这不是 defensive fallback，而是按任务优先级拆分 owner queries。

不要为了等待 full point list 阻塞设备详情首屏。

---

# 28. Empty / Not Found / Unauthorized / Unavailable

必须严格区分。

## 28.1 Device Not Found

Registry owner 成功返回“对象不存在”：

> 未找到该设备。

提供返回 Device Center。

## 28.2 Device Unauthorized

权限系统确认不可访问：

> 你没有查看该设备的权限。

不要伪装成 Not Found，除非安全策略明确要求资源存在性保密。

## 28.3 Registry Unavailable

> 设备身份信息暂不可用。

不能用 telemetry 中的 deviceId 临时造一个设备详情。

## 28.4 Telemetry Unavailable

Identity / metadata 仍可显示；Current Operation 显示：

> 当前遥测暂不可用。

不能把 values 显示为 0 / Stopped / Offline。

## 28.5 No Historical Capability

Recent Evidence 不显示图表，明确：

> 当前设备未提供历史数据能力。

不要用 current samples 拼 fake history。

## 28.6 Relationship Unavailable

> 关系模型暂不可用。

不能根据名称猜。

## 28.7 Alarm / Diagnosis / Work owner unavailable

各自 section 局部显示 unavailable。

不能把 unavailable 显示成 `0` / `无事项`。

---

# 29. Permission / Capability Gating

示例：

- 无 historian → 无 Recent Evidence / Trend deep-link；
- 无 Alarm read → 不显示 Alarm summary；
- 无 Diagnosis read → 不显示 Finding；
- 无 Work read → 不显示 Work；
- 无 engineering metadata → 隐藏 raw point / protocol / firmware 等专业数据；
- 无 Control capability → 不显示 Control authority deep-link；
- 无 Audit permission → 不显示 advanced audit/provenance；
- 无 Semantic Model capability → Relationships 只显示 Registry 明确关系。

未经授权的能力默认不做 disabled teaser。

---

# 30. No Defensive Programming / No Compatibility Design

明确禁止：

```text
value || 0
missing current value → 0
missing operating state → stopped
stream disconnected → offline
alarm owner failed → 0 alarms
finding owner failed → no findings
work owner failed → no work
relationship missing → parse device name
point role missing → infer from suffix
historian missing → build history from current cache
last historical value → current value
writable point → render editable input
multiple APIs → first success wins
full point list → one API per point
old DeviceDrawer adapter
old /devices detail route compatibility
old card-based detail page fallback
universal DeviceDetailSection abstraction owning domain semantics
```

不要保留新旧设备详情双路由。

不要做“新 owner 不可用就调用旧 owner”的兼容链。

原则：

> **One device → one canonical identity. One fact → one owner. Current is current. Historical is historical. Writable is not automatically controllable. Unknown stays unknown.**

---

# 31. Component Mapping

```text
Device header                → application layout + semantic text
State strip                  → compact Badge/text composition
Section navigation           → in-page nav / responsive Select
Current operation            → definition-list / compact metric layout
Current attention            → semantic list / linked rows
Recent evidence              → dedicated ECharts feature component
Relationship list            → semantic grouped list / tree-like read-only view
Engineering point table      → shadcn Table + TanStack Table
Point search/filter          → InputGroup + Select/Popover
Point inspector              → contextual panel / Sheet when narrow
Metadata                     → definition list + Disclosure
Provenance                   → Disclosure / semantic detail list
Professional exits           → Button / Link
```

避免：

- 每个 fact 都单独 Card；
- nested Card inside Card；
- 4×4 KPI tile grid；
- 复杂 Tab maze；
- generic `UniversalDeviceDetail` 拥有业务语义。

---

# 32. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ CH-02  离心式冷机                                      ← 返回冷冻水温差告警 │
│ 冷冻水系统 · B1 冷机房 · Active                                         │
│ 最后权威快照 11:42:18                                                     │
├──────────────────────────────────────────────────────────────────────────────┤
│ Running | Online | Fresh | Good | Alarm 1 | Finding 1 | Override — | Work 1│
├──────────────────────────────────────────────────────────────────────────────┤
│ [Overview] [Evidence] [Relationships] [Engineering Points] [Metadata]       │
├──────────────────────────────────────────────────────────────────────────────┤
│ 当前运行                                                                      │
│ Mode Auto       Load 73%       Power 487 kW       Stage 2                  │
│ CHWS 7.0°C      CHWR 12.2°C    ΔT 5.2K           SP 6.8°C                  │
│                                                                              │
│ 当前事项                                                                      │
│ 高优先级告警：冷冻水温差偏高                         [打开告警]             │
│ Finding：换热效率偏离                                [打开诊断]             │
│ 工单 WO-128：现场复核中                              [打开工单]             │
├──────────────────────────────────────────────────────────────────────────────┤
│ Recent Evidence · 来源窗口 08:45–10:00                                      │
│ Temperature (°C)  ────────────────────────────────────────────────────────  │
│ Power (kW)        ────────────────────────────────────────────────────────  │
│ 09:17 Alarm ▲  09:31 Stage 2  09:42 Command                               │
│                                                        [查看完整趋势]        │
├──────────────────────────────────────────────────────────────────────────────┤
│ Relationships                                                                 │
│ Belongs to: 冷冻水系统                                                       │
│ Meter: CH-02 Power Meter                                                     │
│ Sensors: CHWS / CHWR / Flow                                                  │
│ Actuators: Enable / CHWS Setpoint                                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ Engineering Points                    [搜索点位] [Role] [Quality]             │
│ Point           Role          Value      Freshness  Quality  History          │
│ CHWS Temp       supply temp   7.0 °C     Fresh      Good     Yes              │
│ CHWR Temp       return temp   12.2 °C    Fresh      Good     Yes              │
│ CHWS SP         setpoint      6.8 °C     Fresh      Good     Yes              │
│ Enable Cmd      command       ON         Fresh      Good     Yes              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Metadata / Provenance  [展开]                                                 │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责和信息层级，不是 pixel specification。

---

# 33. Responsive Behavior

## 1440–1720 px

首屏应看到：

- Device identity；
- independent state strip；
- Current Operation；
- Current Attention；
- Recent Evidence 的开始或主要摘要。

首屏不能被 metadata 卡片挤占。

## 1024–1439 px

- section nav 保持稳定；
- current facts 可以 2–3 列；
- recent evidence 可纵向分面；
- relationships / point table 在后续 section。

## Around 768 px

必须仍能完成：

- 确认 device identity；
- 理解 independent states；
- 查看 key current facts；
- 查看 current attention；
- 进入 Trend / Alarm / Diagnosis / Work；
- 搜索 engineering point；
- 查看 metadata。

策略：

- section nav 变成紧凑 select / sticky menu；
- current facts 单列/双列；
- Engineering Points 使用 labelled horizontal scroll region；
- Point Inspector 使用 Sheet；
- 不把页面改造成完全不同的 Card Wall。

---

# 34. Accessibility

必须：

- 页面唯一 H1 为 human-readable device name；
- state 不只靠颜色；
- section nav 可 keyboard 操作并有 current indication；
- current facts 使用语义文本 / definition list；
- Engineering Point 使用 semantic table；
- point table header / row relationships 可被 assistive tech 理解；
- Recent Evidence 有 accessible description / data alternative；
- Disclosure 使用 button + `aria-expanded`；
- realtime update 不移动 focus；
- current value update 不自动 announce 高频噪音；
- actions 有明确可理解 label，而不是图标-only mystery button。

如果未来使用 Tabs，必须遵循 W3C APG tabs keyboard contract。

---

# 35. Browser Acceptance Criteria

## Identity

- Device route 使用 canonical device identity；
- human-readable Display Name 是主标题；
- internal UUID 不作为 primary label；
- Offline / telemetry unavailable 时 device identity 不消失；
- Decommissioned 与 Offline 分开。

## Independent States

必须验证：

```text
Operating != Connectivity
Connectivity != Freshness
Freshness != Quality
Alarm != Finding
Override != OutOfService
OutOfService != Lifecycle
Work != Physical State
Command != Readback
Readback != Verification
```

页面不存在 generic Health 替代这些事实。

## Current Operation

- 4–8 个 key facts 来自 domain presentation contract；
- real zero 显示 0；
- missing 不显示 0；
- stale / bad quality 明确；
- unit / timestamp 可获得；
- mode / stage / setpoint 只在 owner 支持时显示。

## Current Attention

- Alarm / Finding / Work / Data / Verification 来自各自 owner；
- owner unavailable 不显示 `0 / none`；
- Finding 不标成 proven root cause；
- ACK 不等于 physical recovery；
- Work completed 不自动等于 physical verification completed。

## Evidence

- recent evidence 只显示少量 key series；
- missing data 是 gap；
- source evidence window 能保留；
- `查看完整趋势` 正确进入 Trend Analysis；
- Device Detail 不变成 full trend workspace。

## Relationships

- relationships 来自 Semantic Model / Registry explicit relation；
- 没有 relation 时不猜；
- 无假 topology；
- related object deep-link 正确。

## Engineering Points

- point table 使用 semantic table；
- point role / value / unit / freshness / quality / source 明确；
- selected point 可以进入 Trend；
- writable point 不出现 inline write input；
- 大量 points 不采用一 point 一 API。

## Control Boundary

- 无直接 Start / Stop / Setpoint / Override mutation；
- control authority / readback 可以只读显示；
- control action 进入 Control / Strategy durable workflow；
- readback 不被标成 verified。

## Loading / Error Truth

- Not Found / Unauthorized / Unavailable 语义分开；
- Registry unavailable 不用 telemetry 临时造设备；
- telemetry unavailable 仍可显示 identity；
- historian unavailable 不造 fake history；
- relationship unavailable 不解析名称猜关系。

## Responsive

1440–1720 px：

- identity/state/current operation/current attention 形成明确首屏 hierarchy；
- 无 card wall；
- 无 page-level horizontal overflow。

Around 768 px：

- 核心 current facts 和 attention 可读；
- point table 在自身 scroll region 内；
- section nav 可达；
- deep links 可操作；
- 无 page-level horizontal overflow。

## Accessibility

- H1 正确；
- state 不只靠颜色；
- section nav / disclosures / point table keyboard 可用；
- charts 有 text/data alternative；
- realtime 不抢 focus。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 DeviceDrawer compatibility adapter；
- 无 `/devices/:deviceId` 双产品 route；
- 无 generic Health score；
- 无 direct point write；
- 无 N+1 point fetch；
- internal IDs 不进入业务主视觉；
- review scenario 无 runtime / network error。

---

# 36. Explicit Non-Goals

设备详情不是：

- EAM / ERP；
- CMMS replacement；
- full Alarm Center；
- full Diagnosis Center；
- full Work Order page；
- trend oscilloscope；
- semantic relationship editor；
- point configuration editor；
- BAS programming tool；
- firmware upgrade console；
- control command console；
- universal audit log。

---

# 37. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Device identity owner 明确；
- independent state semantics 与 06 Device Center 一致；
- key fact presentation profile 有 domain owner；
- Alarm / Finding / Work / Verification summary owner 明确；
- historian 支持 recent evidence 或明确 capability gating；
- Semantic Model / Registry 能提供真实 relationships；
- Engineering Point query 支持 pagination / search / batch current state；
- control boundary 已接受：详情页只读，不做直接 point write；
- Not Found / Unauthorized / Unavailable 语义已接受；
- 旧 Device Detail / Drawer / `/devices/:deviceId` 无设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
