# 09 告警中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `09 告警中心`  
> **Route intent：** `/sites/:siteId/alarms`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Alarm Center、旧告警列表、旧 Drawer、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Alarm Definition / Alarm Occurrence / Alarm State / Handling / Ownership / Suppression / Evidence / Rule / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

告警中心的唯一核心任务是：

> **让操作员在当前异常条件中迅速识别“现在必须关注什么”，理解物理状态、优先级、持续时间与处置责任，完成 ACK / Assign / Shelve 等被授权的告警处理动作，并把需要进一步调查或整改的问题可靠交给 Trend、Diagnosis、Work 或 Device workflow。**

Alarm Center 是 **active-first operator triage workspace**，不是：

- Event Log；
- Notification Inbox；
- FDD / Diagnosis Center；
- Work Order Center；
- Rule Configuration 页面；
- Alarm Philosophy 编辑器；
- generic message center；
- “所有异常都放进去”的红色列表；
- 用前端规则重新计算 alarm condition 的页面。

用户离开本页前应该已经知道：

1. 当前哪些 physical alarm conditions 仍然 active；
2. 哪些报警尚未 ACK；
3. 哪些没有 owner / 责任人；
4. alarm priority / class / consequence 事实是什么；
5. alarm 已持续多久、是否重复、是否处于 flood / standing / chattering context；
6. alarm 是否 Shelved / Suppressed by Design / Out of Service；
7. 哪些需要直接处理，哪些需要 Diagnosis / Work / Verification；
8. 当前 handling action 是否成功被 authoritative Alarm domain 接收。

---

# 2. 主要用户

## Primary

### HVAC / BMS 值班操作员

高频扫描 active alarms、ACK、分派、查看快速证据并决定下一步。

### 站点运行负责人

处理无人负责、持续过久、重复发生、高优先级和 flood 期间的告警。

### 维修协调人员

将需要现场整改的 alarm 交给 Work Order，并追踪责任而不是篡改 physical state。

## Secondary

- 诊断工程师：从 Alarm 进入 Finding / Hypothesis investigation；
- 控制工程师：理解 mode / override / control event 与 Alarm 的时间关系；
- commissioning 工程师：识别 standing / nuisance / recurrent alarms 与 verification 需求；
- alarm administrator：查看 performance / bad actors 后进入 Rule / Philosophy / MOC 管理 Surface。

## 不作为主要目标用户

- 管理层：应使用 Site / Management Review；
- 规则维护人员在本页直接改 threshold / priority / delay：应进入 Rule Administration；
- 需要根因结论的人：应进入 Diagnosis Center。

---

# 3. 外部最佳实践依据

## 3.1 ANSI/ISA-18.2 — Alarm 是需要操作员响应的异常条件

ISA 对 alarm 的核心定义是：以 audible / visible 方式向 operator 指示 equipment malfunction、process deviation 或 abnormal condition，并且**需要响应**。

ISA-18.2 的 lifecycle 覆盖：

- philosophy；
- identification；
- rationalization；
- detailed design；
- implementation；
- operation；
- maintenance；
- monitoring & assessment；
- management of change；
- audit。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards
- https://www.isa.org/intech-home/2018/march-april/features/alarm-management-life-cycle
- https://www.isa.org/intech-home/2016/may-june/departments/isa18-alarm-management-standard-updated

**本页采用：**

- Alarm ≠ Event ≠ Finding ≠ Notification；
- priority、class、cause、consequence、response guidance 必须来自 rationalized owner；
- UI 不重新发明 Alarm semantics；
- HMI 目标是 meaningful、prioritized、actionable，而不是越多越好。

## 3.2 IEC 62682:2022 — 告警系统的主要职责是支持操作员响应异常

IEC 62682:2022 规定基于控制系统/HMI 的 alarm management 原则和流程，并明确 alarm system 主要用于通知 abnormal process conditions / equipment malfunctions 并支持 operator response。

来源：

- https://webstore.iec.ch/en/publication/65543

**本页采用：**

- Active Triage 是主视图；
- operator response / ownership 是一等字段；
- Alarm Center 不接管 Diagnosis / Work 的职责。

## 3.3 ISA-18.2 suppression semantics — Shelved、Suppressed by Design、Out of Service 不同

ISA 公共资料明确区分三类 suppression：

1. **Shelved**：operator 手动、临时抑制；
2. **Suppressed by Design**：系统按设计条件自动抑制；
3. **Out of Service**：报警功能因维护等原因停止服务。

来源：

- https://www.isa.org/intech-home/2017/september-october/features/from-managing-to-optimizing-alarms
- https://www.isa.org/getmedia/55b4210e-6cb2-4de4-89f8-2b5b6b46d954/PAS-Understanding-ISA-18-2.pdf

**本页采用：**

- 不使用单一 `suppressed=true` 覆盖三者；
- operator action 叫 Shelve / Unshelve，而不是泛化 `Suppress`；
- Suppressed by Design 是状态事实，不是人工按钮；
- Out of Service 进入受控 maintenance/admin workflow；
- 所有 suppression 有 provenance / reason / expiry / audit（owner 支持时）。

## 3.4 ISA-101 — Alarm HMI 必须服务 situational awareness

ISA-101 的 scope 明确包括 menu hierarchy、screen navigation、graphics/color、dynamic elements、alarming conventions、historical database interfaces 和 alarm work methods。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101

**本页采用：**

- 正常信息视觉安静；
- priority/abnormal/action-needed 才获得高视觉权重；
- color 不是唯一编码；
- alarm list 必须支持 rapid scan 与 keyboard operation；
- 不用大量 flashing / pulsing 破坏注意力。

## 3.5 EEMUA 191 Edition 4 — Alarm flood 与 nuisance alarm 本身会伤害操作能力

EEMUA 191 是广泛采用的 alarm-system good-practice guide；Edition 4 明确覆盖 prioritisation、minimising nuisance alarms、avoiding alarm floods、grouping 和 remote sites 等。

来源：

- https://www.eemua.org/products/publications/digital/eemua-publication-191
- https://www.eemua.org/news/good-practice-for-all-aspects-of-industrial-alarm-systems-%E2%80%93-new-edition-of-eemua-191-released
- https://www.eemua.org/news/avoiding-the-high-risk-of-alarm-floods

**本页采用：**

- flood state 是重要 operator context；
- nuisance/chattering/standing alarms 进入 performance improvement workflow；
- 不靠 UI 随意隐藏 lower-priority alarms 来“解决 flood”；
- grouping / suppression 必须由 Alarm domain / philosophy owner 定义。

## 3.6 ISA-TR18.2.5 — Monitoring / Assessment / Audit 是持续告警治理的一部分

ISA-TR18.2.5 公开说明中强调持续跟踪 alarm rates、standing alarms、operator response、flood 等 performance metrics。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards
- https://www.isa.org/getmedia/3b68ebbf-eff7-476a-ad57-7b60072c7e8a/TR_18-2-5_Preview.pdf

**本页采用：**

- Alarm System Performance 是同域 secondary view；
- performance 不挤占 Active Triage 首屏；
- KPI threshold 来自 site alarm philosophy / owner，不在前端硬编码行业示例值。

## 3.7 ISA-TR18.2.8 — 非告警通知不应污染 operator alarm queue

ISA-18 series 将 non-alarm notifications 单独作为 technical report 处理，用于 alerts、prompts、notices 等不属于 operator alarm 的消息。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards

**本页采用：**

- informational notification 不进入 Active Alarm Ledger；
- Finding / recommendation / data warning 保持各自 domain；
- 不把所有事件升级成 alarm。

## 3.8 Schneider EcoStruxure Building Operation — Priority、ACK、cause/action guidance 与 history 是成熟 BMS 的一等能力

EcoStruxure Building Operation 的公开 alarm documentation 支持 priority、acknowledgement、cause/action note、checklist、hide/disable 和 alarm history 等能力。

来源：

- https://sqa.ecostruxure-building-help.se.com/bms/topics/show.castle?id=4316&locale=en-US&productversion=1.9
- https://ecostruxure-building-help.se.com/bms/Topics/show.castle?id=6590&locale=en-US&productversion=7.0
- https://sqa.ecostruxure-building-help.se.com/bms/topics/show.castle?id=5789&locale=en-US&productversion=2024

**本页采用：**

- priority 是 owner fact；
- ACK 是独立 handling action；
- operator guidance 可来自 rationalization metadata；
- hide/disable 类能力必须映射到我们的精确 suppression semantics，而不是照搬 vendor wording。

---

# 4. Alarm Domain Vocabulary

必须保持以下对象和状态分离。

## 4.1 Alarm Definition

稳定的 alarm 配置/规则身份，例如：

```text
CH-02 Low ΔT Alarm
```

包含 owner-defined：

- source；
- class；
- priority；
- condition / threshold reference；
- cause guidance；
- consequence guidance；
- operator response guidance；
- delay / deadband metadata；
- rationalization / revision metadata。

Definition 不等于一次发生。

## 4.2 Alarm Occurrence

一次具体 activation / return-to-normal 生命周期。

```text
Occurrence #A-20260914-0917
activated 09:17
returned to normal 10:04
```

History 以 Occurrence 为主要记录单位。

## 4.3 Physical Condition

建议 owner 至少提供：

```text
ACTIVE
RETURNED_TO_NORMAL / CLEARED
UNKNOWN
```

具体枚举以 Alarm domain contract 为准。

## 4.4 Acknowledgement / Handling

与 physical condition 独立：

```text
UNACKNOWLEDGED
ACKNOWLEDGED
```

某些 alarm philosophy 可能要求 reset-state acknowledgement；UI 按 owner contract 展示，不自行简化。

## 4.5 Ownership

```text
UNASSIGNED
ASSIGNED
```

Owner / team 是 handling responsibility，不是 physical state。

## 4.6 Suppression

```text
NONE
SHELVED
SUPPRESSED_BY_DESIGN
OUT_OF_SERVICE
```

如果 owner 支持更多明确状态，可扩展；不得压成 boolean。

---

# 5. Mandatory Semantics

以下等式全部禁止：

```text
ACK = Cleared
Assign = Cleared
Work Created = Cleared
Work Completed = Cleared
Finding Published = Cleared
Shelved = Cleared
Suppressed by Design = Cleared
Out of Service = Cleared
Offline = Alarm
Fault = Alarm
Finding = Alarm
Event = Alarm
Notification = Alarm
```

正确表达：

```text
Physical condition
+ handling / acknowledgement
+ ownership
+ suppression
+ related investigation/work
```

是并行事实。

---

# 6. Primary Questions

## Q1 — 现在有哪些 abnormal conditions 仍然需要操作员关注？

默认 Active View 只突出当前 active / action-required occurrences。

## Q2 — 哪一个更紧急？

依据 rationalized facts：

- priority；
- alarm class；
- consequence / response time（若 owner 提供）；
- active duration；
- acknowledgement；
- ownership；
- recurrence / escalation context。

不使用不可解释的黑盒 `Risk Score`。

## Q3 — 谁已经看到、谁负责？

必须同时看见：

- acknowledgement；
- acknowledged by / at；
- owner/team；
- assigned at。

## Q4 — 这是一次新 alarm、长期 standing，还是重复/抖动问题？

来自 Alarm domain / performance analytics：

- first active time；
- duration；
- recurrence count；
- standing flag；
- chattering/fleeting classification；
- flood context。

前端不自行用简单次数阈值分类。

## Q5 — 这个 alarm 为什么存在、应该怎么响应？

若 rationalization metadata 存在，Inspector 展示：

- condition；
- probable cause guidance；
- consequence；
- operator response；
- allowed response time；
- class / priority basis。

这些是 Definition guidance，不是当前 occurrence 的 root-cause conclusion。

## Q6 — 是否需要继续调查或整改？

进入：

- Trend；
- Device Detail；
- Diagnosis；
- Work Order；
- Functional Verification。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/alarms
```

推荐 Search Params：

```text
view            // active | history | suppressed | performance
q
priority
class
physical
ack
owner
suppression
sourceType
system
asset
from
to
sort
page
size
selected
```

规则：

- `active` 默认不要求 from/to；
- `history` / `performance` 使用明确时间窗口；
- `selected` 用于 shareable Inspector context；
- filter/sort/page 必须可恢复。

不进入 URL：

- dialog open animation；
- temporary ACK form draft；
- row hover；
- transient tooltip；
- local column-resize pixels。

---

# 8. Entry Contract

## 8.1 从 Site Overview

可以携带：

- Site；
- priority / attention filter；
- source trail。

## 8.2 从 System Operations

携带：

- Site；
- System / Device；
- current operational context。

## 8.3 从 Device Detail

携带：

- Device；
- selected alarm occurrence / definition（若有）；
- return-to-source。

## 8.4 从 Comfort / IEQ

携带：

- Zone；
- related alarm；
- serving system；
- evidence window。

## 8.5 从 Diagnosis / Work / Verification

携带 source Alarm identity，并支持明确 `← 返回调查来源`。

---

# 9. Exit Contract

主要出口：

```text
Alarm
→ Trend Analysis
→ Device Detail
→ System Operations
→ Diagnosis Center
→ Work Order
→ Functional Verification
```

Admin capability 下可进入：

```text
Alarm Definition / Rule Administration
Alarm Philosophy / Management-of-Change
```

如果这些 Surface 尚未定义，先使用明确的 future owner link，不在 Alarm Center 内联编辑规则。

保持：

- Site；
- Alarm occurrence；
- Asset；
- Evidence window；
- Source trail。

---

# 10. Responsibility Boundary

Alarm Center 拥有：

- Active triage；
- Alarm occurrence scan；
- ACK；
- Assign / ownership；
- Shelve / Unshelve（capability-gated）；
- handling audit；
- suppression visibility；
- occurrence history；
- alarm-performance summary；
- evidence handoff。

Alarm Center 不拥有：

- alarm threshold/rule editing；
- priority rationalization；
- root-cause conclusion；
- corrective work execution；
- control command；
- equipment lifecycle；
- generic notification center；
- frontend-derived Alarm condition。

---

# 11. Information Architecture

```text
Context Header
  Site · realtime state · site timezone

Compact Alarm Load Context
  Active · Unacknowledged · Unassigned · Shelved/OOS · Flood state

Peer Views
  Active | History | Suppressed | Performance

Active Triage Toolbar
  Search · Priority · Ack · Owner · System · More Filters

Alarm Triage Ledger
  Priority
  Alarm / Source
  Physical condition
  ACK
  Owner
  Duration
  Repeat/context
  Suppression
  Last transition

Selected Alarm Inspector
  occurrence truth
  rationalized guidance
  evidence preview
  handling actions
  investigation/work links
  history/provenance

Action Dialogs
  ACK
  Assign
  Shelve / Unshelve

Professional exits
```

默认是 **Active-first Triage Ledger**，不是 Dashboard Card Wall。

---

# 12. Peer Views

## 12.1 Active

默认入口。

只服务当前 operator triage。

包含：

- current active occurrences；
- active but acknowledged；
- active unacknowledged；
- active assigned/unassigned；
- returned-to-normal but still action-required occurrence only when Alarm philosophy/ack semantics requires it。

不把历史 recovery 大量混入默认列表。

## 12.2 History

按 Occurrence 查看：

- activation；
- acknowledgement；
- assignment；
- shelving；
- return-to-normal；
- related work / finding；
- operator actions。

时间范围必须明确。

## 12.3 Suppressed

按精确语义分组：

```text
Shelved
Suppressed by Design
Out of Service
```

不能混成一列 `Disabled`。

## 12.4 Performance

面向 alarm-system continuous improvement：

- alarm rate；
- flood periods；
- standing alarms；
- chattering/fleeting alarms；
- frequent bad actors；
- priority distribution；
- acknowledgement/response metrics；
- suppression inventory；
- alarm-system performance vs site philosophy target。

Performance 不承担当前 emergency triage。

---

# 13. Compact Alarm Load Context

Header 下仅显示少量 operational context，例如：

```text
Active 18
Unacknowledged 4
Unassigned 3
Shelved 2
Alarm flood: No
```

规则：

- 每个数字由 Alarm owner 提供；
- owner unavailable 不显示 `0`；
- flood threshold 来源于 Alarm Philosophy / performance owner；
- 不用一个 `Alarm Health 87%` 合并这些事实；
- 不铺 8 张 KPI cards。

---

# 14. Active Triage Ledger

推荐核心列：

| 列 | 语义 |
|---|---|
| Priority | rationalized alarm priority |
| Alarm | human-readable message / definition |
| Source | system / asset / zone |
| Physical | active / returned-to-normal |
| ACK | unack / ack + actor/time |
| Owner | assignee/team |
| Duration | physical condition duration |
| Repeat | owner-provided recurrence context |
| Suppression | none / shelved / SBD / OOS |
| Transition | latest authoritative transition |

默认保持约 8–10 个业务列。

## 14.1 Priority vs Severity

如果 domain 同时提供：

- `priority`；
- `consequence severity`；

必须分开。

Priority 是 operator response ordering fact；severity 可能是 consequence dimension。

不得将颜色或数值位置自行解释成另一者。

## 14.2 Source

Source 显示 human-readable：

```text
CH-02 · 冷冻水系统
Z-1203 · 12F East
```

internal alarmId / UUID 不作为主视觉。

---

# 15. Default Sort / Prioritization

默认排序必须可解释，并服从 site Alarm Philosophy。

推荐逻辑：

```text
rationalized priority
→ physical active before returned-to-normal handling residue
→ unacknowledged before acknowledged within same priority
→ unassigned before assigned within same priority/context
→ older/long-standing or escalation according to owner policy
```

如果 domain owner 提供明确 triage order，可直接使用，但 UI 应解释排序依据。

禁止：

- hidden AI risk score；
- `created_at DESC` 作为唯一默认排序；
- 前端按颜色判断 priority；
- 把 duration 自动等同 severity。

Realtime 更新默认不持续重排行到让操作员失去当前 row；需要 owner-defined refresh / stable-sort behavior。

---

# 16. Physical Condition Contract

Physical condition 是最重要的事实之一。

## Active

当前 abnormal condition 仍存在。

显示：

- activated at；
- current duration；
- latest source state；
- quality/provenance（若 owner提供）。

## Returned to Normal / Cleared

物理条件已经恢复。

显示：

- recovery time；
- active duration；
- acknowledgement / work 状态仍独立。

## Unknown

owner 无法判断时显示 Unknown。

绝不能把：

- telemetry unavailable；
- source offline；
- query failed；

自动翻译成 Cleared。

---

# 17. ACK Contract

ACK 表示：

> **操作员已确认看到 / 接受该 alarm handling responsibility 的一个步骤。**

ACK 不表示：

- condition resolved；
- root cause known；
- work done；
- device healthy。

ACK mutation 最少需要：

- occurrence identity；
- authoritative action permission；
- server-confirmed result；
- actor / time audit。

如果 Alarm Philosophy 要求：

- comment；
- checklist；
- dual acknowledgement；

由 owner contract 决定 UI 字段。

前端不自行强制一套万能表单，也不省略 owner 要求字段。

---

# 18. Assign / Ownership Contract

Assign 的作用是建立责任。

支持：

- assign user/team；
- reassign；
- unassign（若 owner允许）；
- owner history。

Assign 不改变 physical condition。

推荐 Dialog 只包含：

```text
Current owner
New owner/team
Optional note if owner contract supports
```

复杂 maintenance scheduling 进入 Work Order，不在 Assign dialog 中复制。

---

# 19. Shelving Contract

Shelve 是 operator 临时 suppression。

只有 capability + permission + Alarm Philosophy 允许时显示。

Shelve action 至少需要 owner contract 中明确的：

- alarm occurrence/definition target；
- reason；
- duration / expiry；
- actor；
- timestamp。

UI 必须显示：

```text
Shelved by
Shelved at
Reason
Expires at / duration
```

禁止：

- `Suppress forever` 默认选项；
- 没 expiry/reason 的无痕隐藏；
- Shelved 后显示 Cleared。

Unshelve 是明确 action。

---

# 20. Suppressed by Design Contract

Suppressed by Design 是 automation / state-based design 的结果。

Alarm Center 只显示：

- suppression reason / state basis（若 owner提供）；
- since；
- rule / definition link；
- current applicability。

它不是普通 operator mutation。

不要提供 `Unsuppress` 按钮绕开 designed logic。

需要修改逻辑时进入 Rule / Alarm Administration + MOC。

---

# 21. Out of Service Contract

Out of Service 表示 alarm functionality 因 maintenance / administrative reason 不在服务。

Alarm Center 可以显示：

- reason；
- owner；
- start；
- planned return；
- related Work Order；
- audit。

把 alarm 放 OOS / 恢复服务应进入受控 maintenance/admin workflow。

不把 OOS 当普通一键隐藏。

---

# 22. Selected Alarm Inspector

Inspector 保持 Ledger context，负责快速判断，不是新的 Alarm Detail 巨页。

## Identity

```text
Alarm name
Priority / Class
Source asset/system/zone
Occurrence state
```

## Physical Timeline

```text
Activated
Acknowledged
Assigned
Shelved/Unshelved
Returned to normal
```

只显示真实发生的 transitions。

## Current Handling

- ACK；
- owner；
- suppression；
- related Work / Finding。

## Rationalized Guidance

如果 Definition owner 提供：

```text
Condition
Cause guidance
Consequence
Operator response
Response time
Alarm class
Revision
```

必须标注这些是**设计/理性化指导**，不是本次 occurrence 的已验证 root cause。

## Evidence Preview

显示少量：

- source value；
- threshold / condition reference；
- 1–3 relevant trend series；
- evidence window；
- related mode/control events。

完整分析进入 Trend。

## Professional exits

突出：

- 查看趋势；
- 查看设备；
- 进入诊断；
- 创建/打开工单。

---

# 23. Evidence Window Contract

Alarm occurrence 必须拥有明确 evidence time semantics。

推荐：

```text
pre-condition window
activation timestamp
active interval
recovery timestamp
post-recovery window
```

具体 padding 由 Alarm / investigation owner 定义，不在 UI 硬编码一个全局分钟数。

从 Alarm → Trend / Diagnosis 时保留：

- occurrence id；
- asset；
- evidence window；
- source series；
- source trail。

---

# 24. Repeat / Standing / Chattering / Fleeting

这些不是前端通过简单字符串/次数自己算的标签。

Owner 可以提供：

- recurrence count；
- standing alarm flag；
- chattering flag；
- fleeting flag；
- bad-actor rank；
- detection method / policy version。

UI 负责表达，不重新分类。

## 24.1 Standing Alarm

指长期保持 active 的 alarm occurrence / condition。

不要与 telemetry `stale` 混淆。

## 24.2 Chattering / Fleeting

用于识别 nuisance behavior。

不要把多个独立 occurrence 静默合并成一个假的 continuous alarm。

---

# 25. Alarm Flood Contract

Flood 是 operator workload context，不只是漂亮 KPI。

如果 Alarm performance owner 判断进入 flood：

- Header 显示明确 `Alarm Flood` context；
- 保持 high-priority alarms 可扫描；
- 可以提供 owner-defined grouping / suppression result；
- 不在前端自行删除/隐藏 lower priority occurrences；
- 不把 related alarms 通过文本相似度合并。

Flood 结束后保留 occurrence history 和 performance period。

## 25.1 No hardcoded universal threshold

ISA / EEMUA 公开资料存在行业参考指标，但实际 product UI 的 threshold 必须来自：

```text
Site Alarm Philosophy
+ Alarm Performance Owner
```

前端不硬编码某个 `10 alarms / 10 min` 就宣称所有站点进入 flood。

---

# 26. Alarm Grouping / Correlation

允许 grouping 的前提：

- Alarm domain 明确定义 parent/child / common-cause / campaign / group；
- 或 Diagnosis domain 提供明确 related evidence relation。

UI 可以显示：

```text
1 primary + 8 related alarms
```

但必须允许展开每个真实 occurrence。

禁止：

- 根据 message 文本相似度自动 merge；
- 根据同一设备就认定 common cause；
- 根据同一时间出现就宣称 root cause；
- 前端去重导致真实 occurrence 消失。

---

# 27. Alarm → Diagnosis Contract

进入 Diagnosis 时携带：

```text
Alarm occurrence
Asset/system/zone
Activation/recovery window
Relevant source series
Rationalized cause/consequence guidance (as guidance)
```

Diagnosis 页面再区分：

- verified facts；
- published finding；
- hypothesis；
- root cause（如果最终验证）。

Alarm Center 不把 rationalized `possible cause` 显示成已验证 Root Cause。

---

# 28. Alarm → Work Contract

只有需要 corrective action 时创建/链接 Work。

最小 handoff：

```text
Alarm occurrence
Source object
Problem summary
Priority / class context
Evidence link
Current owner context
```

Work 创建后：

```text
Alarm physical condition remains independent.
```

Work Completed 也不自动把 Alarm 物理状态改成 Cleared。

---

# 29. Alarm → Functional Verification

适用于：

- corrective work completed but physical/sequence recovery needs confirmation；
- recurrent alarm requires retest；
- control/strategy change needs post-change verification。

Verification result 与 Alarm recovery 仍是两个事实。

例如：

```text
Alarm returned to normal
but functional verification failed
```

在产品语义上完全允许。

---

# 30. History View

History 以 Occurrence 为中心，不是 raw event dump。

推荐列：

- occurrence；
- alarm name；
- priority；
- source；
- activated at；
- returned at；
- active duration；
- ack by / at；
- owner；
- suppression periods；
- linked Finding/Work。

支持明确时间范围和 server-side pagination/filtering。

Raw transition log 可以在 Inspector professional disclosure 下查看。

---

# 31. Performance View

目标：

> **发现 alarm system 自身的问题，指导 rationalization / rule improvement / MOC。**

不是当前事件处理。

可包含：

## Alarm Load

- average alarm rate；
- peak alarm rate；
- flood periods；
- operator position / site scope（owner supports）。

## Bad Actors

- most frequent definitions；
- repeated alarms；
- chattering/fleeting alarms；
- standing alarms。

## Priority Quality

- priority distribution；
- unrationalized / unclassified alarms（如果 owner supports）；
- stale rule metadata / missing response guidance。

## Handling Performance

- acknowledgement time；
- assignment time；
- unresolved duration；
- shelved overdue；
- OOS overdue。

## Governance exits

- Rule Administration；
- Alarm Philosophy / Rationalization；
- MOC / Audit。

所有 target/limit 由 alarm philosophy owner 提供。

---

# 32. Rule Administration Boundary

Alarm Center 只读显示 Definition metadata 和 deep-link。

不在 Active Triage 直接编辑：

```text
threshold
priority
delay
deadband
alarm class
suppression logic
notification routing
cause/consequence/response text
```

这些属于 Alarm Definition / Rule Administration，并需要对应 change-control / audit。

避免 operator 在处置 live alarm 时误改规则。

---

# 33. Data Authority Contract

## Alarm Definition

Owner：Alarm Definition / Rule domain。

## Alarm Occurrence / Physical State

Owner：Alarm Runtime domain。

## Acknowledgement

Owner：Alarm Handling domain。

## Assignment / Ownership

Owner：Alarm Handling / responsibility domain。

## Shelving

Owner：Alarm Handling domain。

## Suppressed by Design

Owner：Alarm Runtime / Rule domain。

## Out of Service

Owner：Alarm Maintenance / Administration domain。

## Alarm Class / Priority / Guidance

Owner：Rationalization / Definition domain。

## Evidence source values

Owner：Telemetry / Historian / Operations domains。

## Finding

Owner：Diagnosis domain。

## Work

Owner：Work Order domain。

## Performance metrics

Owner：Alarm Performance / analytics domain。

Frontend 只做 triage/read-model projection 与授权 mutation invocation。

---

# 34. Query / Read Model Contract

Active Triage 需要适合 operator scan 的 read model。

推荐：

```text
Active Alarm Projection
  + definition summary
  + physical state
  + ack/owner
  + suppression
  + source identity
  + repeat/performance flags
  + linked finding/work summary
```

History / Performance 使用独立 owner queries。

禁止：

```text
500 alarm rows
→ 500 definition requests
→ 500 device requests
→ 500 owner requests
→ 500 work requests
```

不要在前端用 N+1 拼 triage list。

如果 domain 缺少 projection，应修后端/read model，不用前端并发 workaround 长期顶住。

---

# 35. Snapshot / Stream Realtime Contract

Active Alarm View 使用：

```text
Snapshot = 当前 scope 的 authoritative alarm state
Stream = alarm transitions / handling updates / owner updates
```

Stream event 示例：

- activated；
- returned to normal；
- acknowledged；
- assigned；
- shelved / unshelved；
- suppression changed；
- priority/class revision effective（owner-defined）。

实时更新要求：

- 不抢 focus；
- 不关闭 Inspector；
- 不把正在 ACK 的 row 移走；
- 不每个 event 无控制重排行；
- selected occurrence 即使 recovered 也保留到用户完成当前阅读；
- stream disconnect 不解释为 alarms cleared。

Reconnect：

- 重新取 authoritative Snapshot；
- reconcile occurrence identities；
- 再恢复 Stream；
- 不用 local cache 冒充 current physical truth。

---

# 36. Mutation Contract

ACK / Assign / Shelve 等 mutation：

1. 用户明确触发；
2. UI 收集 owner-required fields；
3. 提交 authoritative mutation；
4. 显示 pending；
5. server/domain 确认成功后更新 handling state；
6. failure 显示具体失败，不伪造成功；
7. audit 来自 owner。

不做：

- local-only ACK；
- frontend-generated owner assignment；
- mutation failed 但 UI 先显示成功；
- physical state optimistic clear。

---

# 37. Loading / Empty / Partial / Error

## No Active Alarms

如果 owner 成功返回 empty：

> `当前没有活动告警。`

可以显示最近 recovery / history 入口。

## Alarm Runtime Unavailable

> `当前告警状态暂不可用。`

不能显示 `0 active`。

## Definition Metadata Unavailable

Occurrence 仍可以显示 runtime facts，但 Definition guidance 显示 unavailable。

不能用 message 文本猜 priority/class。

## Handling Service Unavailable

Alarm 仍可查看，但 ACK / Assign / Shelve action 显示不可用状态与明确原因。

不能 local fallback。

## Asset Registry Unavailable

Alarm occurrence 仍保留 source identity；human-readable asset projection unavailable。

不能丢掉 alarm。

## Performance Unavailable

Active Triage 不应被阻塞；Performance view 明确 unavailable。

---

# 38. Permission / Capability Gating

示例：

- `alarm.read` → Active / History；
- `alarm.ack` → ACK；
- `alarm.assign` → Assign；
- `alarm.shelve` → Shelve；
- `alarm.performance.read` → Performance；
- `alarm.definition.read` → Definition guidance；
- `alarm.rule.manage` → Rule Admin deep-link；
- `work.create` → Create Work；
- `diagnosis.read` → Diagnosis deep-link。

没有权限的 mutation 默认不显示。

如果 audit policy 要求显示“存在但无权限”，由全局权限 UX contract 决定，不在本页自创 disabled teaser。

---

# 39. Search / Filter Contract

Primary filters：

```text
Search
Priority
Acknowledgement
Owner
System / Source
```

Secondary filters：

- class；
- suppression；
- repeat/standing；
- asset type；
- alarm definition；
- physical state（History/edge cases）。

默认不把 15 个 filters 全部铺开。

Search 优先 human-readable：

- alarm message；
- asset/device name；
- system；
- zone；
- business code。

internal UUID 不是默认搜索视觉对象。

---

# 40. Alarm Actions UX

## ACK

短、明确、低输入量时使用 Dialog / Alert Dialog。

## Assign

使用 compact Dialog / Combobox。

## Shelve

需要 reason + duration/expiry 时使用 Dialog，清楚显示风险与恢复时间。

## Create Work

只做最小 handoff；复杂表单进入 Work Order durable route。

## Diagnosis

直接进入 durable route，不使用巨型 modal。

---

# 41. HMI Visual Contract

遵循 ISA-101 的 situational-awareness 思路。

## Normal / handled

低视觉噪声。

## High priority / unack / flood

获得适度高权重，但不制造整屏红色。

## Color

颜色不是唯一编码。

Priority 同时使用：

- text；
- badge/label；
- icon/shape（需要时）；
- color 辅助。

## Flashing

Web UI 默认不使用持续闪烁行/背景。

若未来 safety/alarm philosophy 明确要求 audible/flashing annunciation，需要独立 HMI/safety review，并遵循无障碍/光敏安全要求。

---

# 42. Accessibility

必须：

- Ledger 使用 semantic table；
- priority / physical / ACK / suppression 不只靠颜色；
- row selection 与 actions keyboard 可达；
- Dialog focus 管理正确；
- ACK/Assign/Shelve action 有明确 accessible name；
- realtime transition 不抢 focus；
- 新 alarm arrival 不导致 screen-reader 高频噪音；
- critical annunciation 如果需要 live region，必须由 HMI policy 精确定义；
- Inspector 在 narrow viewport 使用 accessible Sheet；
- Duration / timestamp 有可读文本；
- audible cue 不是唯一告警信号。

---

# 43. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- Site/context；
- compact load context；
- Active Triage controls；
- Ledger 主要列；
- selected Alarm Inspector（选择后）。

## 1024–1439 px

- 次要列可以隐藏到 Inspector；
- toolbar wrap；
- Ledger 保持扫描优先。

## Around 768 px

核心任务必须仍可完成：

- 看 active priority；
- 看 physical / ACK / owner；
- ACK；
- Assign；
- Shelve（若有权限）；
- 打开 Inspector；
- 进入 Trend / Diagnosis / Work。

策略：

- table 在自身 labeled horizontal scroll region；
- Inspector / actions 用 Sheet/Dialog；
- 不转成每个 alarm 一张巨型 card；
- 不依赖 hover。

---

# 44. No Defensive Programming / No Compatibility Design

明确禁止：

```text
alarm API error → []
owner unavailable → unassigned
ACK API error → local ACK success
physical state unknown → cleared
telemetry offline → create alarm
telemetry spike → create alarm
finding exists → create alarm
work completed → clear alarm
shelved → clear alarm
suppressed by design → clear alarm
OOS → clear alarm
missing priority → infer from color/message
missing asset → discard alarm
repeat count missing → compute by message-text grouping
common cause missing → merge by timestamp
performance owner missing → hardcode EEMUA/ISA threshold
stream disconnect → 0 alarms
multiple alarm APIs → first success wins
old Alarm Center adapter
old Close/Reopen lifecycle compatibility
frontend-generated Close action
```

不保留旧 `Close / Reopen` 语义兼容层。

不把 ACK / Work Complete 映射成 physical clear。

不做“新 Alarm Runtime 不可用就回旧 alarm endpoint”的 fallback chain。

原则：

> **One alarm occurrence → one authoritative lifecycle. Physical truth, acknowledgement, ownership and suppression remain independent. Unknown stays unknown. Handling does not rewrite physics.**

---

# 45. Component Mapping

```text
Page header                  → application layout
Peer views                   → Tabs (true peer alarm-domain views)
Compact load context         → compact semantic facts, not card wall
Toolbar search               → InputGroup
Filters                      → Select / Popover / Command
Alarm ledger                 → shadcn Table + TanStack Table
Priority/state               → Badge + text + semantic icon
Alarm Inspector              → responsive aside / Sheet
ACK                           → AlertDialog / Dialog
Assign                        → Dialog + Combobox
Shelve                        → Dialog + duration/reason Fields
Guidance                      → semantic sections / Collapsible
Evidence preview              → dedicated ECharts feature
History timeline              → semantic ordered list
Performance charts            → dedicated ECharts, owner-defined metrics
Pagination                    → accessible pagination
```

避免：

- KPI card wall；
- row 内塞 8 个彩色 badge；
- 每行一个闪烁动画；
- nested Drawer；
- giant all-in-one alarm detail modal。

---

# 46. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 告警中心 · Phoenix Central Plant                         Live · UTC-07:00     │
├──────────────────────────────────────────────────────────────────────────────┤
│ Active 18   Unack 4   Unassigned 3   Shelved 2   Flood: No                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ [Active] [History] [Suppressed] [Performance]                               │
│ [Search alarms…] [Priority] [Ack] [Owner] [System] [More 2]                │
├──────────────────────────────────────────────────┬───────────────────────────┤
│ Pri Alarm / Source    Physical ACK  Owner  Dur. │ CH-02 Low ΔT             │
│ P1  CH-02 Low ΔT      Active   —    Li    46m  │ Priority P1               │
│     CH-02 · CHW System                         │ Active · 46 min           │
│ P2  AHU-03 SAT High    Active   Ack  Zhang 18m │ Unacknowledged            │
│ P2  VAV-17 Flow Low    Active   Ack  —      8m │ Owner: Li                 │
│ ...                                              │                           │
│                                                  │ Guidance                  │
│                                                  │ Condition: ΔT < limit     │
│                                                  │ Consequence: ...          │
│                                                  │ Response: verify flow...  │
│                                                  │                           │
│                                                  │ Evidence 08:45–10:00      │
│                                                  │ ΔT ───────────────────    │
│                                                  │ Flow ─────────────────    │
│                                                  │                           │
│                                                  │ [ACK] [Assign] [Shelve]   │
│                                                  │ [Trend] [Diagnose] [Work] │
└──────────────────────────────────────────────────┴───────────────────────────┘
```

Wireframe 只表达职责、密度和交互层级，不是 pixel specification。

---

# 47. Browser Acceptance Criteria

## Physical Truth

- Active 与 Returned-to-Normal 明确分开；
- owner unavailable 不显示 Cleared；
- telemetry/stream failure 不自动 clear；
- ACK / Assign / Shelve / Work Complete 均不改变 physical state；
- real recovery 由 Alarm Runtime owner 提供。

## Handling

- ACK 成功后显示 actor/time；
- ACK failure 不假成功；
- Assign 与 ACK 独立；
- Shelve 显示 reason/expiry/actor；
- Suppressed by Design 没有 operator Unsuppress；
- OOS 与 Shelved 分开。

## Priority / Triage

- priority 来自 Definition/Rationalization owner；
- missing priority 不猜；
- default sort 可解释；
- duration 不等于 priority；
- realtime arrival 不让当前 selected row 不断跳动。

## Flood / Repeat

- flood state 来自 performance owner / philosophy；
- frontend 不硬编码行业示例阈值；
- standing/chattering/fleeting 由 owner 分类；
- no text-similarity merging；
- 每个真实 occurrence 可被追溯。

## Inspector

- occurrence / definition 分开；
- rationalized cause guidance 不显示为 verified root cause；
- evidence window 明确；
- Trend / Device / Diagnosis / Work deep-link 保留 context。

## Empty / Partial / Error

- successful empty 才显示 `当前没有活动告警`；
- Alarm Runtime unavailable 不显示 0；
- Definition unavailable 不猜 priority/class；
- Handling unavailable 不 local fallback；
- Registry unavailable 不丢 alarm occurrence。

## Performance

- Active Triage 不被 performance query 阻塞；
- threshold/target 来自 Alarm Philosophy owner；
- performance bad actors 可进入 Definition/Admin workflow；
- 不把 performance KPI 变成 live alarm state。

## Responsive

1440–1720 px：

- Active Ledger + Inspector 形成单一 operator workspace；
- 无 card wall；
- 无 page-level horizontal overflow。

Around 768 px：

- priority / physical / ACK / owner 可查看；
- ACK/Assign/Shelve 可操作；
- Inspector/Deep links 可达；
- table scroll 在自身 region；
- 无 hover-only interaction。

## Accessibility

- color 非唯一编码；
- semantic table；
- keyboard actions；
- Dialog focus 正确；
- realtime 不抢 focus；
- 无持续 flashing；
- audible cue 非唯一信号。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 Alarm Center adapter；
- 无 Close / Reopen compatibility lifecycle；
- 无 frontend-derived alarm condition；
- 无 generic `suppressed` boolean 覆盖三类 suppression；
- 无 one-row-one-request N+1；
- review scenario 无 runtime/network error。

---

# 48. Explicit Non-Goals

本页不是：

- Alarm Rule Editor；
- Alarm Philosophy Editor；
- notification inbox；
- diagnosis engine；
- root-cause workspace；
- work execution page；
- control console；
- raw event historian；
- safety instrumented system replacement；
- generic CMMS；
- AI-generated alarm classifier。

---

# 49. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Alarm Definition 与 Occurrence identity 已分离；
- physical condition 与 ACK / owner / suppression 已分离；
- Shelved / Suppressed by Design / Out of Service semantics 已接受；
- Priority / Class owner 明确；
- Active Triage 是默认 view；
- History / Suppressed / Performance 是 peer views；
- Alarm Performance owner / Philosophy target contract 明确；
- ACK / Assign / Shelve mutation contract 明确；
- Alarm → Trend / Diagnosis / Work context handoff 明确；
- Rule Administration 不在本页内联；
- 旧 Close/Reopen / 旧 Alarm Center 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
