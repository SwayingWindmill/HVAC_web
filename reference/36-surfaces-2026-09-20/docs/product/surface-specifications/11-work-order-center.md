# 11 工单中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `11 工单中心`  
> **Route intent：** `/sites/:siteId/work-orders`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Work Order 页面、旧任务中心、旧工单 CRUD、旧 Drawer、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Work Order / Work Plan / Asset / Alarm / Diagnosis / Verification / Resource / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

工单中心的唯一核心任务是：

> **把已经确认需要执行的维护、整改或现场工作，转化为“有来源、有优先级、有责任人、有计划、有阻塞原因、有截止/服务承诺、有下一动作、可验证完成”的可执行工作队列。**

Work Order Center 是 **maintenance work management workspace**，不是：

- 普通 Todo List；
- generic CRUD table；
- Alarm Center 的复制品；
- Diagnosis Center 的复制品；
- Technician field execution detail page；
- Asset Registry；
- Planning ERP；
- procurement system；
- direct control console；
- “Completed = Fixed = Verified” 的状态机。

用户离开本页前应该已经知道：

1. 哪些工作最需要现在处理；
2. 哪些 work overdue / SLA at risk；
3. 哪些没有 owner / crew；
4. 哪些因为 material / access / shutdown / approval / condition 被阻塞；
5. 每个 work 的来源是什么；
6. work priority 与源 Alarm/Finding priority 是否不同；
7. 下一动作是什么；
8. 哪些 work 已完成 physical execution 但仍需要 Functional Verification；
9. 哪些 work 可以正式关闭，哪些还不能。

---

# 2. 主要用户

## Primary

### Maintenance Planner / Supervisor

需要查看 backlog、优先级、资源、blocker、due/SLA、计划状态和执行进展。

### Site Operations / Facility Manager

需要确认 alarm/finding 已经转化为责任明确的 corrective action，并追踪 overdue / blocked / verification-required work。

### Maintenance Technician / Crew Lead

在 Center 中找到自己负责的工作，并进入 12 Work Order Detail 完成执行、记录证据和完工信息。

## Secondary

- HVAC engineer：从 Diagnosis/Alarm 创建或跟踪 corrective work；
- Commissioning engineer：关注 completed but verification-required；
- Energy manager：跟踪影响能源绩效的 corrective work；
- Asset manager：观察重复故障、downtime、maintenance history；
- Planner / procurement coordination：查看 material/resource blocking context。

## 不作为主要目标用户

- 操作员只做 Alarm ACK：Alarm Center；
- 工程师做 root-cause investigation：Diagnosis Center；
- 技师填写完整现场执行证据：Work Order Detail；
- 控制工程师发命令：Control Center。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP — EMIS 必须把洞察转化为 Corrective Action，再 Verify Improvement

DOE EMIS Operations Support 明确给出：

```text
Identify & Prioritize
→ Validate, Diagnose & Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor, Update & Maintain
```

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities

**本页采用：**

- Work Order 是 corrective action workflow，不是 investigation workflow；
- Finding / Alarm 可以创建或关联 Work，但不会自动完成；
- Work Completed 后仍可以需要 Verification；
- 工单关闭不代表 energy/comfort/performance outcome 已验证。

## 3.2 Better Buildings — FDD 的价值依赖与 Work Order System 的衔接

Better Buildings 专门强调将 building analytics / FDD recommendation 转换为 effective corrective actions，并与 maintenance work order systems 集成。

来源：

- https://betterbuildingssolutioncenter.energy.gov/webinars/turning-insights-action-bridging-building-data-analytics-and-work-order-systems

**本页采用：**

- Alarm/Finding → Work 是正式 handoff；
- source evidence 必须保留；
- Work queue 不能变成与 analytics 脱节的孤立任务表。

## 3.3 DOE O&M — Maintenance Strategy 不只有 Corrective

DOE/FEMP 将现代 O&M 分为：

- reactive/corrective；
- preventive；
- predictive；
- reliability-centered maintenance。

来源：

- https://www.energy.gov/cmei/femp/operations-and-maintenance-challenges-and-solutions
- https://www.energy.gov/cmei/femp/operations-and-maintenance-federal-facilities

**本页采用：**

- Work Type 与 Source 必须独立；
- 不把所有 work 都标成 fault repair；
- preventive / predictive work 也可进入同一 execution backlog；
- reliability / recurring problem 需要关联历史与 asset context。

## 3.4 ISO 55001:2024 — Work Priority 要服务资产价值、绩效和风险

ISO 55001 要求组织系统化管理资产生命周期，并在 performance、risk、expenditure 间平衡。

来源：

- https://www.iso.org/standard/83054.html

**本页采用：**

- Work priority 不是简单复制 Alarm priority；
- priority 由 Work Management owner 基于安全、运行、可靠性、能源、合规、资源等明确规则决定；
- work decision/provenance 需要可审计。

## 3.5 SMRP — Work Management 是 Maintenance & Reliability 的核心专业能力

SMRP Body of Knowledge 将 Work Management 作为 maintenance/reliability 核心 pillar，并提供标准化 metrics/guidelines 支持计划、执行和持续改进。

来源：

- https://smrp.org/learning-resources/smrp-library/body-of-knowledge/
- https://smrp.org/learning-resources/smrp-library/best-practices-metrics-guidelines/

**本页采用：**

- Backlog、schedule/plan readiness、execution、completion、overdue、repeat work 都应是正式 work-management semantics；
- metrics 由 Work Management owner 定义，不在前端发明 benchmark。

## 3.6 IBM Maximo — Waiting Approval / Schedule / Material / Plant Condition / In Progress / Completed 是不同业务状态

Maximo 的成熟 Work Order lifecycle 明确区分：

- waiting approval；
- approved；
- waiting scheduling；
- waiting materials；
- waiting plant condition；
- in progress；
- completed；
- canceled / closed。

来源：

- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=overview-work-order-statuses
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=statuses-material-availability-status-work-orders

**本页采用：**

- `Blocked` 必须有明确 blocker/reason；
- materials / plant condition / approval / schedule 不能都压成 `Pending`；
- Completed 表示 physical work done，不自动代表 verified/closed。

## 3.7 IBM Maximo — Work Planning 需要 Labor / Material / Tool / Asset / Location Readiness

成熟 CMMS 的 work plan 会管理 tasks、labor、labor hours、materials、services、tools、safety plan；planning/scheduling 还要考虑 crew、asset/location availability。

来源：

- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=module-job-plans
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=view-scheduling-work-based-resource-availability

**本页采用：**

- Work Center 可以显示 readiness summary；
- 复杂资源计划进入 Work Detail / Planning owner，不把 Center 做成 ERP；
- `Ready to Execute` 必须由 owner 的 readiness contract 决定。

## 3.8 ASHRAE Commissioning — Corrective Action 后需要验证和文档化

ASHRAE commissioning 强调 verifying and documenting system performance，并在 existing-building commissioning 中包含 investigating、implementing、verifying、documenting performance。

来源：

- https://www.ashrae.org/technical-resources/bookstore/commissioning
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- Work Completed ≠ Functional Verification Passed；
- Work can be complete while Verification state = Required / Failed / Inconclusive；
- 系统真正“恢复”要回到 Alarm/Operations/Verification owner。

---

# 4. Work Domain Vocabulary

必须分清以下对象。

## 4.1 Work Request / Source Issue

来源可以是：

```text
Alarm occurrence
Diagnosis finding / investigation
Operator manual request
Preventive maintenance schedule
Predictive maintenance recommendation
Inspection
Functional verification failure
Energy opportunity implementation
Asset maintenance plan
```

Source 是 provenance，不等于 Work Order 本身。

## 4.2 Work Order

正式可执行工作对象，至少有：

- stable identity；
- problem / work statement；
- work type；
- source；
- affected asset/location；
- priority；
- owner/team；
- lifecycle state；
- due/SLA；
- next action；
- verification requirement；
- audit history。

## 4.3 Work Plan

描述如何执行：

- tasks；
- labor/craft；
- materials；
- tools；
- safety/permit requirements；
- shutdown/plant condition；
- estimated duration/cost。

Center 只显示 readiness summary；完整 plan 在 12 Work Detail。

## 4.4 Work Execution

现场实际执行：

- actual start/end；
- actual labor；
- used material；
- field notes；
- attachments；
- observed condition；
- action performed；
- completion evidence。

属于 12 Work Order Detail。

## 4.5 Verification Requirement

说明完成工作后是否需要：

- functional verification；
- alarm recovery confirmation；
- trend evidence；
- retest；
- M&V / energy verification。

这是 Work 的独立 downstream fact。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
Alarm priority = Work priority
Diagnosis severity = Work priority
Assigned = Started
Planned = Ready
Blocked = Cancelled
Due date = SLA
Completed = Closed
Completed = Verified
Work completed = Alarm cleared
Work completed = Diagnosis resolved
Work completed = Root Cause confirmed
Work completed = Savings verified
```

正确关系：

```text
Source issue
→ Work Order
→ Planning / Readiness
→ Execution
→ Work Completed
→ Verification (when required)
→ Final Close / Archive according to owner process
```

---

# 6. Primary Questions

## Q1 — 什么工作现在最需要关注？

优先识别：

- urgent/high-priority；
- overdue；
- SLA at risk/breached；
- unowned；
- blocked；
- verification overdue；
- repeat/callback；
- work linked to active critical alarm/finding。

## Q2 — 这项工作从哪里来？

必须看到 source：

```text
Alarm A-103
Diagnosis D-202
PM Plan PM-CH-02-Quarterly
Manual request
```

Source link 不能丢。

## Q3 — 谁负责，谁在执行？

区分：

- accountable owner/team；
- assigned technician/crew；
- planner/supervisor（若 owner model支持）。

## Q4 — 为什么还没开始/完成？

Blocker 必须明确，例如：

```text
Awaiting approval
Awaiting schedule
Awaiting material
Awaiting access/permit
Awaiting shutdown / plant condition
Awaiting specialist
Awaiting external vendor
Awaiting source clarification
```

不使用模糊 `Pending`。

## Q5 — 什么时候必须完成？

显示：

- target due；
- SLA response target；
- SLA completion target；
- planned start/end；
- actual start；
- overdue / at-risk reason。

## Q6 — 下一动作是什么？

必须有明确 next action：

```text
Assign owner
Approve plan
Schedule outage
Reserve material
Start work
Complete checklist
Upload evidence
Request verification
Review failed verification
```

## Q7 — Work 完成后还需要什么？

显示独立 Verification state：

```text
Not Required
Required
Scheduled
In Progress
Passed
Failed
Inconclusive
Overdue
```

具体枚举由 Verification owner 定义。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/work-orders
```

推荐 Search Params：

```text
view            // active | my-work | blocked | verification | completed
q
workType
priority
state
owner
assignee
sourceType
system
asset
blockedReason
sla
verification
from
until
sort
page
size
selected
```

不进入 URL：

- row hover；
- temporary dialog draft；
- local column width；
- transient note draft；
- temporary tooltip。

---

# 8. Entry Contract

## 8.1 从 Alarm Center

携带：

- Alarm occurrence；
- source asset/system/zone；
- evidence window；
- priority/class context；
- rationalized response guidance；
- return-to-source。

Alarm priority 只作为 source context，不直接写入 Work priority。

## 8.2 从 Diagnosis Center

携带：

- problem statement；
- verified facts；
- published Finding；
- selected Hypothesis（明确仍是 hypothesis）；
- evidence links；
- next verification / corrective recommendation。

## 8.3 从 Device Detail

携带：

- asset identity；
- current issue；
- recent evidence；
- maintenance history context。

## 8.4 从 Functional Verification

如果 verification 失败需要 corrective work，携带：

- requirement/test；
- failed criteria；
- observed evidence；
- retest requirement。

## 8.5 Preventive / Predictive Source

由 maintenance plan / condition-monitoring owner 创建或关联，不伪装成 Alarm/Diagnosis。

---

# 9. Exit Contract

主要出口：

```text
Work Center
→ Work Order Detail
→ Source Alarm
→ Source Diagnosis
→ Device Detail
→ Trend Analysis
→ Functional Verification
→ M&V / Opportunity when applicable
```

Center 默认不在侧栏完成复杂 field execution。

---

# 10. Responsibility Boundary

Work Order Center 拥有：

- work backlog / active ledger；
- work search/filter/sort；
- work type/source projection；
- priority；
- lifecycle state；
- owner/team/assignee summary；
- due / SLA / overdue projection；
- blocker/readiness summary；
- next action；
- verification state；
- limited triage/planning mutations；
- durable link to Work Detail。

Work Order Center 不拥有：

- complete field execution evidence；
- full checklist execution；
- attachment management；
- actual material/labor entry；
- Root Cause confirmation；
- Alarm physical state；
- functional test execution；
- direct equipment control；
- procurement lifecycle；
- vendor invoice/payment；
- inventory master data；
- PM rule administration。

---

# 11. Information Architecture

```text
Context Header
  Site · current scope · timezone

Compact Work Context
  Active · Urgent/Overdue · Unowned · Blocked · Verification Required

Saved / Operational Views
  Active | My Work | Urgent & Overdue | Blocked | Verification | Completed

Work Toolbar
  Search · Priority · State · Owner · Type · More Filters

Work Ledger
  Work
  Source
  Priority
  State
  Owner
  SLA / Due
  Blocker / Readiness
  Next Action
  Verification
  Updated

Selected Work Inspector
  problem/work statement
  source
  asset/location
  state/priority
  ownership
  SLA/due
  readiness/blocker
  verification
  next actions
  source/detail links

Professional exits
  Open Work Detail · Source · Device · Verification
```

默认是 **Ledger-first work management workspace**，不是 KPI Card Wall。

---

# 12. Operational Views

这些 view 是 saved/scoped views，不一定都必须用 Tabs；实现时可由全局导航/URL contract决定。

## Active

默认主视图：所有尚未 final close/cancel 的 work。

## My Work

当前 user/team 的 assigned/owned work。

## Urgent & Overdue

聚焦：

- top priority；
- overdue；
- SLA breached / at risk；
- source criticality high。

## Blocked

只显示有明确 blocker 的 work，并按 blocker type 分组/筛选。

## Verification

显示：

- completed work requiring verification；
- verification failed/inconclusive；
- verification overdue。

## Completed

只用于近期完成/历史回看；不与 Active backlog 混在默认首屏。

---

# 13. Work Type Contract

Work Type 与 Source 分离。

建议 domain owner 至少可以表达：

```text
Corrective
Preventive
Predictive / Condition-based
Inspection
Commissioning / Verification corrective work
Improvement / Modification
Emergency
```

Source 另行表达：

```text
Alarm
Diagnosis
PM schedule
Condition monitoring
Manual request
Verification failure
Opportunity / project
```

禁止：

```text
Alarm source → work type = Emergency
Diagnosis source → work type = Corrective
```

这由 Work Management owner 决定。

---

# 14. Work Priority Contract

Work priority 是 Work domain 的正式字段。

可以考虑：

- safety / compliance；
- operational criticality；
- asset criticality；
- occupant/comfort impact；
- energy/cost impact；
- source alarm/finding context；
- risk of deferral；
- business deadline。

UI 只展示 authoritative result / rationale。

## 14.1 Alarm Priority != Work Priority

例如：

```text
Alarm P1
→ immediate operator response required

Work Priority P2
→ permanent repair can be scheduled in next approved shutdown
```

也可能相反。

前端不能复制值。

---

# 15. Lifecycle State Contract

具体状态枚举由 Work domain owner 定义，但产品必须保持业务阶段清晰。

建议 semantic groups：

```text
Draft / Triage
Awaiting Approval
Planned / Awaiting Schedule
Ready
In Progress
Blocked
Completed
Cancelled
Closed / Archived
```

Owner 可有更细状态，例如：

```text
Awaiting Material
Awaiting Plant Condition
Awaiting Access
Awaiting Vendor
```

## 15.1 Completed vs Closed

`Completed`：physical work / assigned scope 已执行完成。

`Closed`：owner workflow 的最终 administrative closure。

如果 owner 没有 Close 概念，就不要前端自造。

## 15.2 Blocked

Blocked 必须同时有：

- blocker type；
- blocker owner；
- since；
- next unblock action；
- expected resolution / review time（若 owner supports）。

禁止只显示 `Blocked` 红 badge。

---

# 16. Planning / Readiness Contract

Center 可以显示 concise readiness：

```text
Plan ready
Crew ready
Material ready
Tool ready
Access/permit ready
Plant condition ready
Safety requirements ready
```

但不复制完整 planning module。

## 16.1 Ready to Execute

只有 owner 判断 required prerequisites satisfied 才显示 `Ready`。

Frontend 不自己做：

```text
if owner && dueDate then ready
```

## 16.2 Material

Material readiness 可以是：

```text
Not Required
None Available
Partial
Complete
Unknown
```

具体语义由 Inventory/Work owner 定义。

---

# 17. Owner / Assignee Contract

至少区分：

```text
Accountable owner/team
Assigned technician/crew
```

如果 domain 还有 planner / supervisor，则显示专业层字段。

## Unowned

owner 明确为空才显示 `Unowned`。

Owner service unavailable 不能显示成 `Unowned`。

## Assignment

Assign/Reassign 可以在 Center 使用 compact Dialog；复杂 crew planning 进入 Detail/Planning owner。

---

# 18. SLA / Due Contract

不能把所有日期都叫 SLA。

可能存在：

```text
Reported at
Response SLA
Assignment SLA
Target start
Planned start
Target completion / due
Completion SLA
Actual start
Actual completion
Verification due
```

页面根据 owner capability 展示。

## SLA state

建议：

```text
On Track
At Risk
Breached
Paused (only if owner explicitly supports)
Not Applicable
Unknown
```

Pause 必须来自 owner workflow，前端不能因为 Blocked 就自行暂停 SLA。

---

# 19. Default Sort / Prioritization

默认排序服务 actionability，而不是简单 `created_at DESC`。

推荐逻辑：

```text
urgent / critical work
→ SLA breached / overdue
→ unowned
→ blocked requiring intervention
→ verification required/failed
→ due soon
→ owner-defined priority
→ oldest meaningful age
```

具体规则由 Work Management owner 定义。

禁止：

- hidden AI work score；
- 前端自己把 Alarm severity 变成 Work rank；
- duration 自动等同 priority；
- realtime update 持续重排行导致 row 跳动。

---

# 20. Work Ledger

推荐核心列：

| 列 | 内容 |
|---|---|
| Work | readable code + summary |
| Source | Alarm / Diagnosis / PM / Manual |
| Asset/Location | affected object |
| Priority | Work priority |
| State | current lifecycle state |
| Owner | accountable team/person |
| SLA / Due | concise time commitment |
| Blocker / Readiness | actionable blocker |
| Next Action | next required step |
| Verification | requirement/result |

默认保持约 8–10 个业务列。

## Row interaction

单击行：

> select → Inspector

Work code / Open：

> `/sites/:siteId/work-orders/:workOrderId`

---

# 21. Selected Work Inspector

Inspector 用于 backlog triage，不执行整张工单。

## Identity

```text
Work code
Work type
Priority
State
Asset/location
```

## Source

```text
Source type
Alarm/Finding/PM identity
Problem statement
Evidence link
```

## Ownership

```text
Owner/team
Assignee/crew
Planner/supervisor when supported
```

## Commitment

```text
SLA state
Due
Planned start
Actual start
```

## Blocker / Readiness

显示 blocker 和下一 unblock action。

## Verification

```text
Required / Not required
Verification state
Verification due
Latest result
```

## Actions

只保留 1–3 个高价值动作：

- Assign / Reassign；
- Open Work Detail；
- Open Source；
- Open Verification。

不要把完整 checklist / attachment / labor form 塞进 Inspector。

---

# 22. Work Creation Contract

Work 可以从：

- Alarm；
- Diagnosis；
- Manual Request；
- PM/Predictive owner；
- Verification failure；
- Opportunity/Plan。

最小创建字段由 owner 定义，通常包括：

```text
Problem / work statement
Affected asset/location
Source
Work type
Priority
Owner/team
Due/SLA policy
Verification requirement
```

## 22.1 No Fake Auto-Creation

如果系统支持 rule-generated work，必须由 Work domain/automation owner 创建。

Frontend 不做：

```text
finding appears → silently POST work
alarm P1 → auto create work in browser
```

---

# 23. Duplicate / Related Work Contract

可以显示 authoritative：

- parent/child work；
- duplicate relation；
- follow-up work；
- callback/rework；
- recurring PM series。

禁止：

- 根据标题相似度自动 merge；
- 同一 asset 的两个 work 自动当 duplicate；
- 同一 alarm 的多次 occurrence 自动合并；
- 前端删除重复 work identity。

---

# 24. Blocked Contract

Blocked 是需要管理动作的正式状态/condition。

至少显示：

```text
Blocked reason
Blocked since
Blocker owner
Required unblock action
Review / expected date
```

典型 blocker：

- Approval；
- Material；
- Access / Permit；
- Plant Condition / Shutdown；
- Vendor；
- Specialist skill；
- Safety prerequisite；
- Source clarification；
- External dependency。

不使用 generic `Waiting` 代替所有原因。

---

# 25. Verification Contract

Verification 是 Center 必须显式展示的一等字段。

## Not Required

Work owner / source workflow 明确不需要独立 functional verification。

## Required

work 完成后必须验证。

## Passed / Failed / Inconclusive

结果来自 Verification domain。

## Overdue

verification due 已超期。

### 强制语义

```text
Work Completed
+
Verification Failed
```

是完全合法的状态组合。

同样：

```text
Alarm Cleared
+
Verification Failed
```

也可能成立。

---

# 26. Work Completion Contract

Center 可以显示 Completed，但复杂 completion mutation/证据进入 12 Work Detail。

Work completion 至少应有：

- completed by；
- completed at；
- completion summary；
- actual execution evidence availability；
- unresolved follow-up；
- verification requirement。

禁止仅靠：

```text
checkbox = done
```

把 work 完成。

---

# 27. Close / Archive Contract

Final Close 是否存在取决于 Work owner。

如果存在，通常要求：

- work completed；
- required fields complete；
- verification policy satisfied或有明确 exception；
- administrative review complete。

Frontend 不自造 Close 状态。

更不能：

```text
close work → clear alarm
```

---

# 28. Source → Work Context Integrity

Source 必须永久可追溯。

例如：

```text
Work WO-1032
Source: Diagnosis D-202
Finding: CH-02 low ΔT under high load
Alarm: A-103
```

即使 source later resolved，Work 仍保留 source provenance。

不要把 source status 同步覆盖 work state。

---

# 29. Preventive / Predictive Work

Preventive / predictive work 可以与 corrective backlog 共存，但必须保留来源类型。

## Preventive

通常来自：

- time-based schedule；
- usage/meter trigger；
- maintenance plan。

## Predictive / Condition-based

通常来自：

- condition monitoring；
- predictive recommendation；
- remaining-life / degradation model。

这些推荐不等于 Alarm，也不等于 Diagnosis Root Cause。

---

# 30. Work Metrics / Management Context

Center 可以有少量 compact work context：

```text
Active
Overdue
Unowned
Blocked
Verification required
```

更深入的 management metrics 可以包括：

- backlog age；
- schedule compliance；
- response/completion time；
- rework/callback；
- repeat work；
- planned vs unplanned work；
- preventive vs corrective mix；
- downtime；
- work completion trend。

但：

- metric 定义来自 Work Management owner；
- benchmark 来自 organization/SMRP governance；
- 不把这些铺成默认首屏 Card Wall。

---

# 31. Bulk Actions Policy

默认不提供危险的 broad bulk mutation。

可以考虑：

- bulk assign same owner/team；
- bulk tag/category（如果 owner supports）；
- export selected；

但必须：

- compatible work states；
- explicit confirmation；
- owner authorization；
- per-item result feedback。

禁止默认 bulk：

- Complete；
- Close；
- Cancel；
- Priority change；
- Verification pass。

除非 domain 有明确受控 workflow。

---

# 32. Mutation Contract

Center 允许的 mutation 仅限 owner-defined backlog management，例如：

- Assign / Reassign；
- Change priority；
- Set/adjust due（权限允许）；
- Change state to planned/ready（owner workflow）；
- Mark blocker / unblock（owner workflow）；
- Open/Link Verification；
- Cancel（受控）。

所有 mutation：

1. explicit user action；
2. authoritative endpoint；
3. server validation；
4. server-confirmed result；
5. audit trail。

不做 local-only success。

---

# 33. Data Authority Contract

## Work identity / lifecycle

Owner：Work Order domain / CMMS integration。

## Work type / priority

Owner：Work Management domain。

## Owner / assignee

Owner：Work Responsibility domain / CMMS。

## SLA / due

Owner：Work Management / service policy。

## Material readiness

Owner：Inventory / CMMS projection。

## Plant condition / access readiness

Owner：Work Planning / Operations domain。

## Asset / location

Owner：Registry / Semantic Model。

## Alarm source

Owner：Alarm domain。

## Finding source

Owner：Diagnosis domain。

## Verification state

Owner：Functional Verification domain。

## Labor / cost actuals

Owner：CMMS / Work Detail domain。

Frontend 只做 work-centered projection 与授权 mutation invocation。

---

# 34. Query / Read Model Contract

Work Center 需要专门的 batch/read model，不做 N+1。

推荐：

```text
Work Ledger Projection
  + source summary
  + asset/location summary
  + owner/assignee
  + SLA/due
  + blocker/readiness
  + verification summary
```

History/metrics/Planning 可用独立 owner queries。

明确禁止：

```text
300 works
→ 300 source requests
→ 300 asset requests
→ 300 owner requests
→ 300 verification requests
→ 300 material requests
```

如果缺 projection，修 domain/read model，不在前端做并发 workaround。

---

# 35. Realtime / Refresh Contract

Work Center 不需要高频 telemetry stream。

可以使用 event-driven/cache invalidation 更新：

- new work；
- assignment；
- state change；
- blocker change；
- SLA breach；
- completion；
- verification result。

要求：

- 不抢 focus；
- 不关闭 Inspector；
- selected work 状态变化后仍保持 context；
- 不每个 update 自动重排行到用户丢失 row；
- connection failure 不把 owner/state 变 Unknown/Completed。

重新连接后 refetch authoritative Work Snapshot。

---

# 36. Loading / Empty / Partial / Error

## Successful Empty

> `当前没有符合条件的工单。`

如果 Active truly empty，可提示 Completed/History 入口。

## Work service unavailable

> `工单数据暂不可用。`

不能显示 `0 active`。

## Source unavailable

Work 仍显示；Source summary 显示 unavailable。

不能把 Source 删除。

## Owner directory unavailable

已有 owner identity 仍可保留；assign action unavailable。

不能显示 `Unowned`。

## Verification unavailable

Work state 仍显示；verification 显示 unavailable。

不能把它当 Not Required/Passed。

## Material service unavailable

显示 readiness unknown；不能显示 Ready。

---

# 37. Permission / Capability Gating

示例：

- `work.read` → Work Center；
- `work.assign` → Assign；
- `work.priority.manage` → Priority mutation；
- `work.plan.manage` → planning actions；
- `work.complete` → Detail completion workflow；
- `work.cancel` → Cancel；
- `work.metrics.read` → management metrics；
- `verification.read` → verification summary；
- `verification.create` → request/open verification。

没有权限的高风险 action 默认不显示。

---

# 38. Search / Filter Contract

Primary filters：

```text
Search
Priority
State
Owner
Work Type
```

Secondary：

- Source Type；
- Asset/System；
- Blocked Reason；
- SLA；
- Verification；
- Due range；
- Assignee/Crew；
- PM/Predictive source。

Search human-readable：

- work code；
- summary；
- asset/location；
- source business code；
- owner/team。

内部 UUID 不做主视觉。

---

# 39. Saved Views

可支持 server/user-owned Saved Views，例如：

```text
My overdue work
Cooling plant blocked by material
Verification required this week
Unowned corrective work
Planned shutdown work
```

Saved View 保存：

- filters；
- sort；
- columns；
- scope。

不保存 transient hover/Inspector width。

---

# 40. Visual / UX Contract

页面视觉必须服务 backlog management。

## Visual hierarchy

优先：

```text
Action-needed
Overdue / SLA breached
Unowned
Blocked
Verification required
```

正常 planned work 保持安静。

## Color

颜色不是唯一编码。

## Density

高密度 Ledger 优先，不做每个 Work 一个 Card。

## No fake urgency

不要让所有 work 都红色；priority/overdue/blocker 分开表示。

---

# 41. Accessibility

必须：

- semantic table；
- priority/state/SLA/blocker 不只靠颜色；
- keyboard 可完成 filter/row select/open/assign；
- Inspector/Sheet focus management 正确；
- Dialog 有 accessible labels；
- due/SLA 用文本可读；
- update 不抢 focus；
- table horizontal scroll 有 labeled region；
- compact icons 都有文字/accessible name。

---

# 42. Responsive Behavior

## 1440–1720 px

首屏看到：

- site/context；
- compact work context；
- filters/views；
- Work Ledger；
- selected Work Inspector。

## 1024–1439 px

- secondary columns 下沉 Inspector；
- filters wrap；
- Ledger 保持主视图。

## Around 768 px

仍必须能：

- 找工单；
- 看 priority/state/owner/due；
- 看 blocker/next action；
- 看 verification；
- assign（若有权限）；
- open Work Detail/source/verification。

策略：

- table 自身 horizontal scroll；
- Inspector 使用 Sheet；
- 不切换成完全不同的 card wall；
- 不依赖 hover。

---

# 43. No Defensive Programming / No Compatibility Design

明确禁止：

```text
work API error → []
owner service unavailable → unowned
verification unavailable → not required
verification unavailable → passed
material service unavailable → ready
missing due → no SLA breach
alarm priority → copy into work priority
finding severity → copy into work priority
blocked → pause SLA automatically
work complete → alarm clear
work complete → diagnosis resolved
work complete → root cause confirmed
work complete → verification passed
work complete → savings verified
source unavailable → drop source link
asset unavailable → drop work
one work → one source/asset/owner/verification/material request
multiple CMMS endpoints → first success wins
old Task Center adapter
old CRUD WorkOrder page fallback
old Close semantics compatibility
frontend-generated Ready state
frontend-generated SLA state
frontend-generated Verification state
```

不建立：

```text
new Work domain unavailable
→ fallback legacy task list
```

不保留旧 `status=open/closed` 兼容层覆盖新的 lifecycle。

原则：

> **One work order → one authoritative lifecycle. Source, execution, completion and verification remain distinct. A blocker has a reason. Completed is not verified. Unknown stays unknown.**

---

# 44. Component Mapping

```text
Page header                  → application layout
Compact work context         → compact semantic facts
Operational/Saved views      → Tabs or saved-view selector when truly peer/scoped
Search                       → InputGroup
Filters                      → Select / Popover / Command
Work ledger                  → shadcn Table + TanStack Table
Priority/state               → Badge + text
SLA/due                      → text + semantic status
Blocker/readiness            → text + status, no generic health score
Work Inspector               → responsive aside / Sheet
Assign/Reassign              → Dialog + Combobox
Priority/Due mutation        → compact Dialog/Fields
Verification summary         → semantic section
Pagination                   → accessible pagination
```

避免：

- Card Wall；
- giant Drawer 复制 Work Detail；
- row 内十几个彩色 badge；
- inline editable spreadsheet 默认模式；
- 所有 mutation 都塞 row action menu。

---

# 45. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 工单中心 · Phoenix Central Plant                              UTC-07:00      │
├──────────────────────────────────────────────────────────────────────────────┤
│ Active 42   Urgent/Overdue 7   Unowned 3   Blocked 8   Verify 5            │
├──────────────────────────────────────────────────────────────────────────────┤
│ [Active] [My Work] [Urgent & Overdue] [Blocked] [Verification]             │
│ [Search…] [Priority] [State] [Owner] [Type] [More 3]                       │
├──────────────────────────────────────────────────┬───────────────────────────┤
│ Work      Pri State      Owner   Due    Verify  │ WO-1032 · CH-02           │
│ WO-1032   P1  Blocked    HVAC A  2h     Req.    │ Corrective                 │
│  CH-02 low ΔT · Source Diagnosis D-202           │ Priority P1               │
│ WO-1038   P2  In Prog    Li      Today  —       │ Blocked: Awaiting shutdown│
│ WO-1041   P2  Ready      Team B  Tue    Req.    │ Since 09:20               │
│ ...                                              │ Next: approve outage      │
│                                                  │                           │
│                                                  │ Owner: HVAC Team A        │
│                                                  │ SLA: At risk · 2h         │
│                                                  │ Verification: Required    │
│                                                  │                           │
│                                                  │ [Assign] [Open Detail]    │
│                                                  │ [Source] [Verification]   │
└──────────────────────────────────────────────────┴───────────────────────────┘
```

Wireframe 只表达职责、密度和操作层级，不是 pixel specification。

---

# 46. Browser Acceptance Criteria

## Lifecycle Truth

- Work state 来自 authoritative Work owner；
- Assigned 不显示为 In Progress；
- Blocked 有 reason；
- Completed 与 Closed 分开（若 owner支持）；
- Completed 不自动变 Verified。

## Source Integrity

- Alarm/Diagnosis/PM source 可追溯；
- source state变化不覆盖 Work state；
- missing source service 不删除 Work；
- Alarm priority 不自动复制到 Work priority。

## Owner / Assignment

- owner unavailable 不显示 Unowned；
- assign mutation server-confirmed 后才成功；
- owner/team 与 assignee/crew 分开；
- reassignment 有 audit。

## SLA / Due

- SLA 与 due/date labels 明确；
- blocked 不自动暂停 SLA；
- missing SLA 显示 Not Applicable/Unknown，不能假 On Track；
- overdue/at-risk 由 owner policy计算。

## Blocker / Readiness

- blocker 有 type/owner/since/next action；
- material unavailable 与 plant-condition unavailable 分开；
- readiness unavailable 不显示 Ready；
- no generic readiness score。

## Verification

- Required / Not Required 来自 owner；
- verification unavailable 不显示 Passed；
- Completed + Failed Verification 可同时存在；
- functional verification deep-link 保留 Work/source/evidence context。

## Empty / Error

- successful empty 才显示 0 work；
- Work service unavailable 不显示 empty；
- owner/verification/material partial failure 不阻塞其他可信字段；
- Unknown 保持 Unknown。

## Responsive

1440–1720 px：

- Work Ledger + Inspector 构成单一 work management workspace；
- 无 Card Wall；
- 无 page-level horizontal overflow。

Around 768 px：

- priority/state/owner/due/blocker/verification 可查；
- Inspector/Detail/source actions 可达；
- table scroll 在自身 region；
- 无 hover-only interaction。

## Accessibility

- semantic table；
- status 不只靠颜色；
- keyboard filters/actions；
- Dialog focus 正确；
- timestamps/SLA 可读。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 Task Center / Work CRUD compatibility adapter；
- 无 old open/closed lifecycle fallback；
- 无 frontend-generated SLA/readiness/verification state；
- 无 one-row-one-request N+1；
- review scenario 无 runtime/network error。

---

# 47. Explicit Non-Goals

本页不是：

- full technician execution form；
- inventory/procurement system；
- payroll/timekeeping system；
- alarm triage；
- diagnosis/root-cause workspace；
- functional-test execution page；
- asset registry；
- direct control center；
- generic project management board；
- Kanban-first task app。

---

# 48. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Work Source 与 Work identity 分离；
- Work Type / Priority / State / Owner / SLA / Verification owner 明确；
- Alarm priority 与 Work priority 的边界已接受；
- Completed ≠ Verified 已接受；
- Blocked reason/readiness contract 明确；
- Work Center 与 Work Detail 职责边界明确；
- Alarm/Diagnosis → Work context handoff 明确；
- Work → Functional Verification handoff 明确；
- server-side ledger projection/batch contract 明确；
- old Task Center / CRUD Work page 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
