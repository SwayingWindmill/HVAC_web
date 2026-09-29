# 12 工单详情 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `12 工单详情`  
> **Route intent：** `/sites/:siteId/work-orders/:workOrderId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `11-work-order-center.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有 Work Order Detail、旧表单、旧 Drawer、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Work Order / Work Plan / Job Plan / Safety / Actuals / Attachment / Alarm / Diagnosis / Verification / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

工单详情的唯一核心任务是：

> **围绕一张正式 Work Order，安全、可追溯地完成“理解问题 → 准备执行 → 执行工作 → 记录 actuals / evidence → 完成工作 → 移交验证”的全过程，并保留来源、计划、执行与验证之间的事实边界。**

Work Order Detail 是 **single-work execution workspace**，不是：

- 50 字段的大表单；
- Alarm 详情；
- Diagnosis 详情；
- CMMS 全功能镜像；
- Safety compliance engine；
- Inventory / Procurement 页面；
- Payroll / timesheet 系统；
- Functional Verification 页面；
- 一个“点击 Complete 就代表修好了”的任务页。

用户离开本页前应该能明确回答：

1. 为什么要做这项工作；
2. 来源证据是什么；
3. 需要做哪些 tasks；
4. 开始前有哪些 safety / permit / isolation / material / access 前置条件；
5. 谁负责、谁执行；
6. 实际做了什么；
7. 实际用了多少 labor / material / tool / service；
8. 有哪些现场观察、照片、读数或附件作为执行证据；
9. Completion 是否满足 owner 要求；
10. 是否还需要 Functional Verification / Retest / M&V；
11. Work Completed 后，哪些源事实仍然保持独立。

---

# 2. 主要用户

## Primary

### Maintenance Technician / Crew Lead

查看任务、安全前置条件、计划和资料，执行 checklist，记录现场事实、actuals 与完成证据。

### Maintenance Supervisor / Planner

检查 scope、plan readiness、变更、执行进度、blocker、actuals、completion 和 verification handoff。

### Site Operations / Facility Manager

确认 corrective action 被真正执行，且没有把 `Completed` 错当成 Alarm Cleared / Verified。

## Secondary

- HVAC engineer：检查技术执行证据与设备状态；
- Diagnosis engineer：确认 corrective action 是否与 finding/hypothesis 一致；
- Commissioning engineer：接收 verification requirement；
- Energy engineer：在节能措施相关 work 中保留 M&V handoff；
- Safety / permit owner：查看 authoritative permit / isolation state；
- Asset manager：查看维护历史、downtime、failure/remedy context。

---

# 3. 外部最佳实践依据

## 3.1 DOE/FEMP — Corrective Action 之后仍要 Verify Improvement

DOE EMIS Operations Support 的闭环是：

```text
Identify & Prioritize
→ Validate / Diagnose / Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor / Maintain
```

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems
- https://www.energy.gov/cmei/femp/operations-and-maintenance-federal-facilities

**本页采用：**

- Work Detail 负责 corrective-action execution；
- Completion 与 Verification 分开；
- completion evidence 不能冒充 performance verification；
- Work 完成后继续进入 13 Functional Verification（需要时）。

## 3.2 ISO 55001:2024 — Work Execution 属于资产生命周期管理的一部分

ISO 55001:2024 强调通过系统化 asset management 平衡 performance、risk、expenditure，并新增 asset-management decision-making / value 的要求。

来源：

- https://www.iso.org/standard/83054.html
- https://committee.iso.org/sites/tc251/home/projects/published/iso-55001.html

**本页采用：**

- Work 与 Asset identity / lifecycle 保持关联；
- plan / actual / outcome 可追溯；
- 修改 scope / plan 必须保留 revision / audit，而不是静默覆盖历史。

## 3.3 SMRP — Work Management 是独立专业能力

SMRP Body of Knowledge 将 Work Management 列为 maintenance & reliability 五大核心 pillar 之一。

来源：

- https://smrp.org/learning-resources/smrp-library/body-of-knowledge/

**本页采用：**

- planning、scheduling、execution、actuals、completion、history 各自保留语义；
- 现场执行不是 generic todo completion；
- 可持续改进需要真实 work history，而不是只保留最终状态。

## 3.4 IBM Maximo — Work Plan 包含 Tasks / Labor / Materials / Services / Tools

Maximo 公开文档明确：Work Plan 描述完成 Work Order 所需的 tasks、labor、materials、services、tools；套用 Job Plan 后可以针对这张 Work Order 调整 Work Plan，而不会修改原 Job Plan。

来源：

- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=orders-creating-work-plans-work
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=plans-creating-work-using-job
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=module-job-plans

**本页采用：**

```text
Job Plan Template
≠ Work Plan Snapshot
≠ Execution Record
```

- Template 变化不重写已开始工单的历史；
- Work Plan 可以有自己的 revision；
- Execution Record 记录实际执行，而不是计划副本。

## 3.5 IBM Maximo — Actuals 与 Planned Resources 分开

Maximo 明确允许在执行过程中分别记录 actual labor、materials、services、tools。

来源：

- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=orders-reporting-actuals-work
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=overview-actuals-work-orders

**本页采用：**

```text
Planned labor ≠ Actual labor
Planned material ≠ Material used
Planned tool ≠ Tool actually used
Estimated duration ≠ Actual duration
```

前端绝不能在 Complete 时自动把 planned copy 成 actual。

## 3.6 IBM Maximo — Safety Plan 必须与 Hazard / Precaution / Tag-out 关联

Maximo Safety Plan 将工作资产/位置与 hazards、hazardous materials、precautions、tag-out procedures 关联。

来源：

- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=plans-working-safety
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=plans-creating-safety
- https://www.ibm.com/docs/en/masv-and-l/maximo-manage/cd?topic=plans-adding-tag-out-procedures-safety

**本页采用：**

- Safety 是正式 owner 数据，不是自由文本备注；
- Work Detail 展示 applicable hazards / precautions / permit / isolation status；
- 前端不发明安全结论。

## 3.7 OSHA 29 CFR 1910.147 — Hazardous Energy Control 需要既定程序与隔离验证

OSHA 1910.147 适用于 general-industry servicing / maintenance 中可能发生意外 energization / startup / stored-energy release 的场景，并要求 established energy-control procedures；OSHA 对 group LOTO 的说明也允许 work-authorization permit 成为组织既定程序的一部分。

来源：

- https://www.osha.gov/laws-regs/regulations/standardnumber/1910/1910.147
- https://www.osha.gov/control-hazardous-energy/
- https://www.osha.gov/etools/lockout-tagout/hot-topics/group-lockout-tagout/work-authorization-permits

**本页采用：**

- `LOTO required` / `Permit required` 必须来自 Safety / Permit owner；
- “checkbox 已勾选”不代表实际 isolation verified；
- 隔离、许可、恢复服务保留 owner/audit；
- Work Detail 不是法规合规判定器。

**适用性说明：** OSHA 是美国法规参考。跨地区产品应由 Site Safety Policy / jurisdiction owner 决定具体 safety / permit contract，不能全球硬编码 OSHA 流程。

## 3.8 ASHRAE Commissioning — 实施与验证必须记录并可追溯

ASHRAE commissioning 强调 verifying and documenting system performance；existing-building commissioning 需要 investigating、implementing、verifying、documenting。

来源：

- https://www.ashrae.org/technical-resources/bookstore/commissioning
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- completion evidence 与 functional verification evidence 分离；
- expected behavior / test criteria 属于 Verification；
- Work Detail 保留 verification requirement / handoff / latest result。

---

# 4. Work Detail Vocabulary

## 4.1 Source

Work 创建的业务来源，例如：

```text
Alarm occurrence
Diagnosis investigation / finding
Preventive maintenance plan
Predictive recommendation
Manual request
Inspection finding
Verification failure
Optimization plan
```

Source 保留 immutable provenance link。

## 4.2 Problem Statement

说明**为什么需要这项工作**。

必须与执行动作分开。

例如：

```text
Problem:
CH-02 高负荷时冷冻水温差持续偏低，Diagnosis D-202 建议检查旁通阀泄漏。
```

## 4.3 Work Scope

说明**这张工单需要做什么**。

例如：

```text
Inspect bypass valve actuator/readback,
verify physical closure,
repair/replace actuator if failed,
and record post-work state.
```

Problem Statement ≠ Work Scope。

## 4.4 Job Plan Template

可复用的标准作业模板。

## 4.5 Work Plan

绑定本工单的具体计划快照/版本，包括：

- tasks；
- labor；
- materials；
- tools；
- services；
- safety / permit references；
- expected duration；
- execution sequence。

## 4.6 Execution Record

实际发生的：

- task completion；
- observations；
- measurements；
- actual labor/material/tool/service；
- field notes；
- attachments；
- deviations；
- performed actions。

## 4.7 Completion Evidence

用于证明**工单 scope 已完成**的证据。

## 4.8 Verification Evidence

用于证明**设备/系统实际恢复到预期功能**的证据。

属于 Functional Verification owner。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
Problem Statement = Work Scope
Job Plan = Work Plan
Work Plan = Execution Record
Planned = Actual
Checklist checked = physical fact verified
Field note = verified evidence
Photo uploaded = proof of success
Work Completed = Closed
Work Completed = Alarm Cleared
Work Completed = Diagnosis Resolved
Work Completed = Root Cause Confirmed
Work Completed = Functional Verification Passed
Work Completed = Savings Verified
Safety checklist complete = LOTO verified
Permit reference exists = Permit active
Technician-reported cause = authoritative Root Cause
```

正确结构：

```text
Source / Problem
↓
Approved Work Scope
↓
Work Plan + Safety Preconditions
↓
Execution Record + Actuals
↓
Completion Evidence
↓
Work Completed
↓
Functional Verification / M&V（需要时）
```

---

# 6. Primary Questions

## Q1 — 为什么做？

显示 Source、Problem Statement、source evidence 和 affected asset/location。

## Q2 — 要做什么？

显示 approved Work Scope、task list、plan revision 和 change history。

## Q3 — 开始前是否具备执行条件？

检查 authoritative：

- approval；
- crew/skill；
- material/tool；
- safety plan；
- permit；
- isolation/LOTO；
- access；
- plant condition / shutdown；
- required documentation。

## Q4 — 现场实际发生了什么？

记录：

- start/stop；
- executed tasks；
- observations；
- measurements；
- action performed；
- deviations；
- actual resources。

## Q5 — 什么证据证明 scope 完成？

Completion criteria 必须有 owner-defined requirement。

## Q6 — 完成后是否还要验证？

显示 Verification Requirement，并 handoff 至 13。

## Q7 — 是否需要 follow-up work？

执行中发现新问题时，创建 related/follow-up work，不扩大当前 scope 直到不可审计。

---

# 7. Route / URL State Ownership

Canonical route：

```text
/sites/:siteId/work-orders/:workOrderId
```

Work identity 属于 Path。

建议 Search Params 仅用于可分享的 Detail context：

```text
section        // overview | plan | safety | execution | actuals | evidence | timeline
source         // source-return context when needed
returnTo       // normalized source intent, not arbitrary unsafe URL
```

不进入 URL：

- unsaved field note；
- upload progress；
- local checkbox hover；
- dialog open state；
- temporary timer state unless authoritative owner supports durable timer session。

---

# 8. Entry Contract

## 8.1 从 Work Order Center

携带：

- Site；
- Work identity；
- list/filter return context；
- selected source context（若有）。

返回时应回到原 ledger scope，不只是 `/work-orders` 顶部。

## 8.2 从 Alarm

显示来源链：

```text
Alarm A-103
→ Work WO-1032
```

ACK/physical state 保持 Alarm owner 事实。

## 8.3 从 Diagnosis

显示：

```text
Diagnosis D-202
Finding
Selected Hypothesis (if included, still hypothesis)
Verified Facts
Evidence Window
```

不要把 Hypothesis 改写成“故障原因”。

## 8.4 从 Verification Failure

显示：

- failed requirement；
- expected behavior；
- observed behavior；
- evidence；
- retest requirement。

## 8.5 从 Preventive / Predictive Owner

显示对应 maintenance-plan / condition-monitoring source，不伪装成 Alarm。

---

# 9. Exit Contract

主要出口：

```text
Work Detail
→ Source Alarm
→ Source Diagnosis
→ Device Detail
→ Trend Analysis
→ Functional Verification
→ Data Quality
→ Follow-up Work
→ M&V / Opportunity when applicable
```

所有 deep-link 保留 Work identity / source / evidence context。

---

# 10. Responsibility Boundary

Work Detail 拥有：

- work identity / scope projection；
- source chain；
- plan snapshot / revision；
- task/checklist execution；
- field observations / notes；
- attachment linking；
- actual labor/material/tool/service projection；
- execution evidence；
- completion workflow；
- follow-up work creation/link；
- verification handoff；
- work timeline/audit projection。

Work Detail 不拥有：

- Alarm physical state；
- Root Cause confirmation；
- Functional Verification result；
- Safety regulation interpretation；
- Permit authority；
- LOTO truth unless safety owner provides it；
- inventory master；
- purchasing/payment；
- payroll；
- M&V savings claim；
- direct HVAC control。

---

# 11. Information Architecture

```text
Work Header / Action Bar
  Work code · State · Priority · Owner · Due/SLA · Verification

Source & Problem
  Source chain
  Problem statement
  Affected asset/location
  Source evidence

Work Scope & Plan
  Scope
  Plan revision
  Tasks/checklist
  Planned labor/material/tools/services

Safety & Preconditions
  Hazards
  Precautions
  Permit
  LOTO/isolation
  Access / shutdown / plant condition

Execution Workspace
  Start/stop/status
  Task execution
  Field observations
  Measurements
  Deviations
  Attachments

Actuals
  Labor
  Materials
  Tools
  Services
  Downtime / meter readings when supported

Completion
  Completion criteria
  Completion summary
  Completion evidence
  Follow-up items

Verification Handoff
  Required?
  Verification requirement
  Latest result
  Retest / next action

Timeline & Audit
  meaningful work events
```

默认使用 **stable single-work page + section navigation**，不是十几个业务 Tab 的碎片化表单。

---

# 12. Work Header Contract

Header 应始终可见核心事实：

```text
WO-1032 · 检查 CH-02 旁通阀
State: In Progress
Priority: P1
Owner: HVAC Team A
Assignee: Li Wei
Due: Today 16:00
Verification: Required
```

Header action 根据状态/权限动态显示 1–3 个主要动作，例如：

- Start Work；
- Pause/Block（owner workflow）；
- Complete Work；
- Assign/Reassign；
- Request Verification。

不要把所有动作堆到顶部。

---

# 13. Problem Statement & Source Evidence

这是 Detail 第一屏必须看到的内容。

至少显示：

```text
Problem Statement
Source type
Source business id
Affected asset/location
Source timestamp/window
Evidence links
Source owner/state
```

## 13.1 Source Evidence

可以包括：

- Alarm occurrence；
- Diagnosis Finding；
- Trend window；
- inspection result；
- photo/report；
- preventive plan trigger；
- verification failure。

Work Detail 不复制完整 source 页面，只提供 concise evidence + durable deep-link。

## 13.2 Source later changes

如果 Alarm clears / Diagnosis resolves：

- source link仍保留；
- Work 不自动 Complete/Cancel；
- source 状态更新为当前事实。

---

# 14. Work Scope Contract

Scope 必须明确、可执行、可验收。

建议结构：

```text
Objective
In scope
Out of scope
Affected asset/location
Expected completion condition
```

## Scope Change

Work 开始后如果 scope 变化：

- 由 authorized user修改；
- 记录 reason；
- 产生 revision/audit；
- 必要时重新评估 safety/material/time/verification；
- 不静默覆盖原 scope。

发现新的独立问题时优先创建 Follow-up Work，而不是无限扩大当前工单。

---

# 15. Job Plan Template vs Work Plan

## Job Plan Template

组织的标准作业模板。

## Work Plan Snapshot

此 Work Order 实际采用的计划版本。

必须记录：

- template identity/revision（若有）；
- applied at；
- work-plan revision；
- modifications from template；
- approved by / reason（owner支持时）。

## Mandatory Rule

Template 后续更新不能改变已执行/正在执行 Work 的历史计划。

---

# 16. Task / Checklist Contract

Task 是可执行步骤，不是 generic checkbox list。

每项可以包含：

```text
Sequence
Task statement
Responsible role
Required/optional
Expected input/result
Measurement field when applicable
Evidence requirement
Safety dependency
Status
Completed by / at
```

## 16.1 Task status

owner 可定义：

```text
Not Started
In Progress
Completed
Skipped with reason
Blocked
Not Applicable with reason
```

禁止只有：

```text
true / false
```

## 16.2 Skip / N/A

如果允许 Skip / Not Applicable：

- 必须有 owner policy；
- 需要 reason；
- 必要时 supervisor review；
- 不把未执行 silently 标 Completed。

## 16.3 Measurement Task

如果 task 要记录 meter/measurement：

- unit 显式；
- timestamp；
- entered by / source；
- quality/provenance；
- expected range如果有 owner。

不要把输入框的值自动当 Telemetry fact。

---

# 17. Safety & Preconditions Contract

Safety 区域展示 authoritative prerequisites，不做前端合规推理。

可能包括：

```text
Safety Plan
Hazards
Precautions
PPE requirement
Permit requirement/state
LOTO / isolation requirement/state
Access authorization
Shutdown / plant condition
Confined-space / hot-work / electrical permit references
```

只有对应 capability/owner 存在时显示。

## 17.1 Safety Readiness

`Safety Ready` 只能由 Safety/Work owner 明确提供。

前端禁止：

```text
all visible checkboxes checked → Safety Ready
permit document uploaded → Permit Active
LOTO task checked → Isolation Verified
```

## 17.2 Jurisdiction

Product 只表达 site policy / jurisdiction-owned requirement。

不能硬编码：

```text
Every work order must use OSHA LOTO
```

因为不同地区、不同工作类型适用要求不同。

---

# 18. Permit Contract

Permit 是正式对象/状态，而不是附件名称。

如果 permit owner 存在，显示：

```text
Permit type
Permit id
Status
Issued by
Issued at
Valid window
Conditions
Expired/cancelled state
```

Work Detail 只调用受控 issue/close workflow（如果产品有该 capability），不靠 local UI 自造 Permit Approved。

---

# 19. LOTO / Isolation Contract

需要 hazardous-energy control 时，至少区分：

```text
Required
Not Required
Prepared
Applied
Verified
Released / Restored
Unknown
```

具体状态由 Safety/Isolation owner 定义。

## Critical separation

```text
LOTO planned
≠ LOTO applied
≠ Isolation verified
≠ Safe to work
≠ Equipment ready to return to service
```

Work Detail 不从设备 telemetry/offline 状态推断 isolation。

---

# 20. Execution State Contract

执行状态属于 Work owner。

常见阶段：

```text
Ready
In Progress
Paused
Blocked
Completed
```

如果 `Paused` 存在，必须有 owner semantics，例如：

- break；
- shift end；
- temporary operational interruption。

`Paused` 不自动等于 Blocked。

---

# 21. Start Work Contract

Start Work 前 owner 可以要求：

- approval complete；
- assigned technician/crew；
- safety prerequisites；
- permit state；
- materials/tools ready；
- plant condition ready。

前端只提交 authoritative transition。

失败就显示真实 reason。

禁止本地先切 `In Progress` 再等待服务器“补上”。

---

# 22. Field Observations / Notes

Field Note 是现场叙述，不自动升级成 Fact / Root Cause。

每条 note 至少有：

- author；
- timestamp；
- note type；
- related task/asset（可选）；
- attachment links。

推荐类型：

```text
Observation
Action Taken
Issue Found
Handover Note
Supervisor Note
```

## 22.1 Cause / Remedy Reporting

如果 CMMS 支持 failure/problem/cause/remedy codes：

- 展示为 Technician/CMMS-reported maintenance classification；
- 不自动覆盖 Diagnosis Root Cause；
- 如果未来要升级为 authoritative root cause，必须走 Diagnosis/Verification owner workflow。

---

# 23. Attachments Contract

附件不是无结构文件桶。

每个 attachment 至少保留：

- filename/type；
- uploaded by；
- uploaded at；
- relation：source / task / observation / completion / safety；
- description；
- security/access classification（owner支持时）。

## 23.1 Evidence semantics

```text
Photo uploaded
≠ Completion proven
PDF attached
≠ Permit valid
Screenshot attached
≠ Trend verified
```

只有 Completion / Verification owner 明确引用该 attachment 时，它才成为对应 evidence set 的一部分。

---

# 24. Planned vs Actual Contract

页面必须清楚对照，但绝不相互覆盖。

```text
Planned Labor      vs Actual Labor
Planned Material   vs Actual Material
Planned Tools      vs Actual Tools
Planned Services   vs Actual Services
Planned Duration   vs Actual Duration
Estimated Cost     vs Actual Cost
```

actual 不存在时显示 `Not Reported / Unknown`，不是 0。

---

# 25. Labor Actuals

如果 owner 支持，可记录：

```text
Technician/Craft/Crew
Regular hours
Overtime hours
Start/end or transaction time
Task relation
Labor rate/cost (permission-gated)
```

Work Detail 不是 payroll source-of-truth。

不能通过浏览器打开时长自动计算“实际工时”。

---

# 26. Material Actuals

显示/记录 owner-supported：

- item/material；
- quantity used；
- unit；
- issue/return；
- source/store；
- task relation；
- cost（权限允许时）。

Planned material 未实际使用时不能自动计入 Actual。

---

# 27. Tool / Service Actuals

Tool / service 同样区分 Planned vs Actual。

只有真实 transaction/entry 才算 actual。

不因为 Work Plan 中列出某工具就认定已使用。

---

# 28. Asset Downtime / Meter Readings

如果 Work owner/CMMS 支持，可以记录：

- downtime start/end；
- downtime category；
- meter reading；
- condition reading。

这些仍然是 Work/CMMS record。

如果同一值也属于 Operational Telemetry，二者必须保留 source/provenance，不能互相覆盖。

---

# 29. Execution Evidence Contract

Execution Evidence 用于回答：

> **现场到底执行了什么？**

可以包含：

- completed task records；
- before/after photos；
- measurements；
- replacement part serial；
- torque/setting value；
- technician observation；
- vendor report；
- test instrument reading；
- work logs。

它不自动回答：

> 系统是否恢复正常？

后者属于 Functional Verification。

---

# 30. Completion Criteria Contract

每张 Work 的 Completion 不能只靠一个通用 `Done` checkbox。

owner 可以要求：

```text
Required tasks completed
Required actuals entered
Required completion summary
Required evidence attached
Outstanding issues classified
Follow-up work created if needed
Safety/permit closure handoff completed
Verification requirement selected/derived by owner
```

不同 Work Type 可以有不同 completion requirement，但由 owner contract 定义，不在前端堆设备类型 if/else。

---

# 31. Completion Summary

Completion Summary 应结构化回答：

```text
What was found?
What was done?
What changed?
What remains unresolved?
Was scope changed?
Is follow-up required?
Is verification required?
```

允许 narrative，但不应只有一个自由文本大框。

---

# 32. Complete Work Mutation Contract

Complete 操作：

1. 用户明确触发；
2. UI 展示 missing required completion items；
3. 用户补齐 owner-required fields；
4. 提交 authoritative mutation；
5. server 验证；
6. 成功后显示 Completed by/at；
7. 按 owner结果生成/关联 verification requirement；
8. 失败显示真实错误，不 local complete。

## Mandatory

Completion mutation **绝不能**顺带修改：

- Alarm physical state；
- Diagnosis Root Cause；
- Verification result；
- Savings result。

---

# 33. Verification Handoff Contract

Work Header 和 Completion 区域都必须显示：

```text
Verification: Not Required / Required / Scheduled / In Progress / Passed / Failed / Inconclusive / Overdue
```

具体枚举由 Verification owner。

如果 Required，handoff 至：

```text
/sites/:siteId/verifications/...
```

并携带：

- Work identity；
- source issue；
- affected system/device；
- expected restored behavior；
- execution summary；
- changed settings/components；
- relevant points；
- suggested evidence window；
- completion timestamp。

---

# 34. Completion Evidence vs Verification Evidence

必须视觉分开。

## Completion Evidence

证明：

> 工作 scope 已按要求执行。

## Verification Evidence

证明：

> 设备/system/sequence 在实际运行条件下达到预期行为。

示例：

```text
Completion Evidence:
Valve actuator replaced, wiring restored, stroke test performed locally.

Verification Evidence:
Under occupied/high-load operation, command/readback tracks correctly and CHW ΔT returns to expected range.
```

两者不能合并成一个 `Evidence` 文件区。

---

# 35. Follow-up Work Contract

执行过程中发现超出 scope 的新问题时：

```text
Create Follow-up Work
```

携带：

- parent work；
- observed issue；
- affected object；
- evidence；
- recommended priority/type；
- reason for separate work。

Parent/child relation由 Work owner。

禁止：

- silently expand scope；
- duplicate current work by copy/paste without relation；
- follow-up completion 自动改 parent state。

---

# 36. Timeline & Audit Contract

Timeline 只记录有业务意义的事件：

- created；
- approved；
- assigned/reassigned；
- plan revision；
- safety/permit readiness transition；
- started/paused/blocked/resumed；
- task completion；
- significant field observation；
- actual transaction summary；
- completion；
- verification requested/result；
- follow-up work linked；
- final close。

不记录：

- page view；
- hover；
- autosave heartbeat；
- chart cursor。

Audit 来源于 domain owners，不由前端自制“历史”。

---

# 37. Data Authority Contract

## Work identity / lifecycle / scope

Owner：Work Order / CMMS domain。

## Job Plan Template

Owner：Maintenance Planning domain。

## Work Plan Snapshot / Revision

Owner：Work Planning domain。

## Tasks / Checklist execution

Owner：Work Execution domain。

## Safety Plan / Hazards / Precautions

Owner：Safety / CMMS domain。

## Permit

Owner：Permit-to-Work / Safety domain。

## LOTO / Isolation

Owner：Energy Isolation / Safety domain。

## Labor / Material / Tool / Service Actuals

Owner：CMMS / Inventory / Labor transaction domains。

## Attachments

Owner：Document / Work domain。

## Source Alarm

Owner：Alarm domain。

## Source Diagnosis

Owner：Diagnosis domain。

## Verification

Owner：Functional Verification domain。

## Asset / Location

Owner：Registry / Semantic Model。

Frontend 只做 work-centered projection 与授权 mutation invocation。

---

# 38. Query / Read Model Contract

推荐 detail read model：

```text
Work Detail Projection
  + identity/scope
  + source summary
  + asset/location
  + owner/assignee
  + work plan revision
  + tasks
  + safety/preconditions summary
  + actuals summary
  + completion requirement
  + verification summary
  + related work
```

大数据 sections 可独立分页：

- timeline；
- attachments；
- labor transactions；
- material transactions。

禁止：

```text
20 tasks → 20 API calls
10 materials → 10 inventory calls
12 attachments → 12 metadata calls
1 page → 1 source + 1 asset + 1 user + 1 verification per row N+1
```

如果 owner 缺 projection/batch，修 domain contract，不在前端建立并发 workaround。

---

# 39. Mutation Concurrency Contract

Work Detail 是多人协作对象，mutation 必须支持 authoritative revision/version semantics。

如果用户提交时 work 已被他人修改：

- 显示 conflict；
- 展示当前 authoritative state；
- 让用户重新确认 mutation；
- 不 silent last-write-wins（除非 owner contract明确）。

不要写复杂前端 merge engine 来“智能融合”两个 Work Plan。

---

# 40. Offline / Mobile Policy

如果未来 technician app 支持 offline execution，必须有独立 offline sync contract，明确：

- cached scope；
- locally captured evidence；
- mutation queue；
- conflict resolution；
- attachment sync；
- timestamp/source；
- safety-critical actions是否允许 offline。

当前 Web Detail **不默认实现 speculative offline fallback**。

不要为了“以后可能 mobile”在当前页面预埋双数据层。

---

# 41. Loading / Empty / Partial / Error

## Work Not Found

> `未找到该工单。`

## Unauthorized

> `你无权查看该工单。`

不能混成 Not Found（除非安全策略明确要求）。

## Work Service Unavailable

> `工单详情暂不可用。`

不能显示旧缓存为当前 truth 而不标 stale。

## Source Unavailable

Work 仍显示，Source 标 unavailable。

## Safety Service Unavailable

显示 Safety status unavailable，并阻止 owner-required Start/Complete action；不能显示 Ready。

## Actuals Service Unavailable

现有 plan / work可看；actuals unavailable。

不能显示 actual = planned。

## Verification Unavailable

显示 `Verification status unavailable`，不能显示 Not Required/Passed。

---

# 42. Permission / Capability Gating

示例：

- `work.read` → Detail；
- `work.execute` → task/status execution；
- `work.plan.manage` → plan revision；
- `work.actuals.report` → labor/material/tool/service actuals；
- `work.complete` → Complete；
- `work.followup.create` → Follow-up；
- `safety.read` → safety summary；
- `permit.read/manage` → permit workflow；
- `verification.read/create` → verification handoff；
- `work.cost.read` → cost actuals。

权限 gating 不代替 server authorization。

---

# 43. Editing / Autosave Policy

避免“整个页面一个 Save 按钮”。

推荐：

- scoped mutation；
- 每个 section 独立明确保存；
- status transition 使用确认 Dialog；
- notes 可 draft + explicit save；
- checklist item 可按 owner语义逐项提交；
- Plan 结构性修改以 revision save。

不要对 safety / completion / lifecycle transition 做无提示 autosave。

---

# 44. Visual / UX Contract

视觉顺序必须反映现场执行工作流：

```text
Why / What
↓
Ready / Safe
↓
Execute
↓
Record Actuals / Evidence
↓
Complete
↓
Verify
```

## 不做 50 字段大表单

通过 sections + progressive disclosure 组织。

## 不做 Card Wall

关键 sections 使用语义容器、表格、列表和 disclosure。

## Safety visual hierarchy

Safety/permit/isolation 缺失时应清晰阻断，但不使用满屏红色制造恐慌。

## Action hierarchy

当前阶段只突出 1–2 个主要动作。

例如 In Progress 时主要是：

```text
Complete Work
Block / Pause
```

而不是同时显示 Start/Approve/Cancel/Close/Verify/Archive 等十个按钮。

---

# 45. Component Mapping

```text
Page header / action bar       → application layout + Button
Source chain                   → Breadcrumb-like investigation source trail, not hierarchy breadcrumb
Problem / Scope                → semantic text / definition list
Section navigation             → sticky local nav / anchor nav
Task checklist                 → semantic task list + Checkbox only when checkbox semantics are real
Plan resources                 → shadcn Table
Safety / Preconditions         → semantic status list / Alert only for actionable blockers
Permit / LOTO                  → structured status section
Field notes                    → Textarea + timeline/list
Attachments                    → upload/list feature component
Measurements                   → Field/Input with unit
Actuals                        → TanStack Table / forms per transaction owner
Completion                     → structured checklist + Dialog
Verification handoff           → semantic status + Link/Button
Timeline                       → ordered event list
Collapsible engineering detail → Collapsible
```

避免：

- Nested Drawer；
- 每个字段一个 Card；
- generic Form Generator；
- inline edit everything；
- giant Tabs with 12 unrelated workflows；
- optimistic safety/completion transitions。

---

# 46. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ WO-1032 · 检查 CH-02 旁通阀           In Progress · P1 · Due 16:00         │
│ Owner HVAC Team A · Assignee Li Wei                Verification Required    │
│                                                     [Block] [Complete Work]  │
├──────────────────────────────────────────────────────────────────────────────┤
│ 来源：Diagnosis D-202 ← CH-02 Low ΔT Alarm                                 │
│ Problem: 高负荷时冷冻水温差持续偏低                                        │
│ Finding: ΔT below expected range at load > 60%                              │
│                                                    [打开诊断] [查看趋势]     │
├──────────────────────────────────────────────────────────────────────────────┤
│ [概览] [计划] [安全] [执行] [Actuals] [完成] [时间线]                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Work Scope                                                                   │
│ 检查旁通阀执行器/反馈，确认物理关闭，必要时维修/更换，记录结果。              │
│ Plan v3 · based on Job Plan VALVE-INSPECT v5                                │
├──────────────────────────────────────────────────────────────────────────────┤
│ Safety / Preconditions                                                       │
│ Safety Plan SP-12   Permit: Active   LOTO: Verified   Material: Ready       │
│ Hazard: electrical / stored pressure                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ Tasks                                                                         │
│ ✓ Verify permit & isolation                 Li Wei · 13:05                   │
│ ✓ Inspect actuator/linkage                  Observation attached              │
│ ◉ Verify valve physical closure             [record measurement]             │
│ ○ Repair/replace if failed                  conditional                       │
│ ○ Record post-work state                                                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ Execution Notes / Evidence                                                    │
│ 13:32 Actuator linkage loose. Tightened and retested.                        │
│ [photo_1328.jpg] [stroke-test.csv]                                           │
├──────────────────────────────────────────────────────────────────────────────┤
│ Actuals                                                                       │
│ Labor 1.6 h   Material: none   Tool: actuator tester                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ Completion                                                                    │
│ Required evidence 3/4 · Follow-up: none · Verification: Required             │
│                                                     [Complete Work]           │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责、信息层级和执行顺序，不是 pixel specification。

---

# 47. Accessibility

必须：

- section navigation keyboard 可用；
- task status 不只靠颜色/checkbox；
- required / blocked / completed 有文本；
- Safety/Permit/LOTO 状态有可读标签；
- Table 有 semantic headers；
- Dialog focus management 正确；
- upload 进度/错误可被读屏获取但不过度 announce；
- measurement input 有 unit、label、error；
- timeline 语义正确；
- mobile 不依赖 hover；
- action disabled 时有明确 reason（仅当 action 应显示但当前状态不允许）。

---

# 48. Responsive Behavior

## 1440–1720 px

首屏应看到：

- Work identity / state / owner / due / verification；
- source/problem；
- scope；
- safety/precondition summary；
- 当前 execution stage。

## 1024–1439 px

- local section nav 可 sticky；
- resources tables 允许 section-level scroll；
- secondary metadata 下沉。

## Around 768 px

必须仍能：

- 理解 problem/scope；
- 查看 safety/permit/LOTO；
- 执行 checklist；
- 添加 field note / measurement / attachment；
- 查看 actuals；
- 完成 work；
- 打开 verification/source。

策略：

- section nav 可变为 compact selector；
- tables 在自身 scroll region；
- action bar sticky但不遮挡内容；
- attachment/photo workflow touch-friendly；
- 无 page-level horizontal overflow。

---

# 49. No Defensive Programming / No Compatibility Design

明确禁止：

```text
work detail API error → show cached work as current without stale label
source unavailable → delete source chain
job plan missing → infer tasks from work title
safety service unavailable → safety ready
permit unavailable → permit not required
LOTO unavailable → isolation verified
asset offline → isolation verified
all checkboxes true → work completed locally
planned labor → actual labor
planned material → actual material
planned tool → actual tool
missing actual → 0
photo uploaded → completion evidence accepted automatically
technician note says root cause → confirm Diagnosis root cause
work complete → alarm clear
work complete → diagnosis resolved
work complete → verification passed
verification unavailable → not required
scope changed → overwrite original without revision
job plan updated → mutate active work plan silently
multiple CMMS APIs → first success wins
one task/material/attachment → one API N+1
old WorkOrderForm compatibility adapter
old Drawer detail fallback
old open/closed status compatibility
frontend-generated Safety Ready
frontend-generated Permit Approved
frontend-generated LOTO Verified
frontend-generated Completed
```

不建立：

```text
new Work Detail unavailable
→ fallback old CRUD form
```

不为了未来 offline/mobile 预埋双数据源或 speculative sync layer。

原则：

> **One work order → one authoritative execution record. Plan is not actual. A checkbox is not physical proof. Safety truth belongs to the safety owner. Completion is not verification. Unknown stays unknown.**

---

# 50. Browser Acceptance Criteria

## Source / Scope

- Source chain 始终可追溯；
- Problem Statement 与 Work Scope 分开；
- Hypothesis 不显示成 Root Cause；
- scope change 有 revision/reason/audit；
- follow-up issue 不 silent expand scope。

## Plan

- Job Plan Template 与 Work Plan revision 分开；
- template 更新不改变 active Work 历史；
- tasks/resources 来自 authoritative plan owner；
- plan unavailable 不根据 title 猜 checklist。

## Safety

- applicable hazards/precautions 可见；
- permit state 来自 Permit owner；
- LOTO/isolation state 来自 Safety owner；
- service unavailable 不显示 Ready/Verified；
- checkbox 不产生 safety truth；
- jurisdiction/site-policy provenance 可获得。

## Execution

- Start/Status mutation server-confirmed；
- task skip/N/A 有 reason；
- measurement 有 unit/time/source；
- field note 与 verified evidence 分开；
- attachment 有 author/time/relation。

## Actuals

- planned 与 actual 同屏可比较但不互相覆盖；
- missing actual 不显示 0；
- labor/material/tool/service transaction 有 provenance；
- browser open duration 不自动变 labor actual。

## Completion

- Complete 前显示 missing required fields；
- completion result server-confirmed；
- Completed by/at 可见；
- Completion Evidence 与 Verification Evidence 分开；
- Work Completed 不自动改 Alarm/Diagnosis/Verification。

## Verification

- requirement state 来自 owner；
- Required 可进入 Functional Verification；
- verification unavailable 不显示 Passed/Not Required；
- latest result 与 Work state 并列而非覆盖。

## Partial / Error

- Work Not Found / Unauthorized / Unavailable 分开；
- source/safety/actuals/verification partial failure 不伪造事实；
- stale/cache 有明确标识；
- Unknown 保持 Unknown。

## Responsive

1440–1720 px：

- 单工作执行路径清晰；
- 不出现 50 字段首屏；
- 无 card wall；
- 无 page-level horizontal overflow。

Around 768 px：

- scope/safety/tasks/notes/attachments/actuals/completion/verification 均可操作；
- table scroll 在自身 region；
- 无 hover-only interaction。

## Accessibility

- keyboard section nav/actions；
- semantic task/table/timeline；
- status 不只靠颜色；
- form label/unit/error 完整；
- Dialog/Sheet focus 正确。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 WorkOrderForm / Drawer compatibility adapter；
- 无 old open/closed lifecycle fallback；
- 无 frontend-generated safety/permit/isolation/completion truth；
- 无 planned→actual fallback；
- 无 N+1 task/resource projection；
- review scenario 无 runtime/network error。

---

# 51. Explicit Non-Goals

本页不是：

- Alarm lifecycle editor；
- Diagnosis root-cause workspace；
- Safety compliance certification engine；
- Permit master-data administration；
- lockout/tagout procedure authoring system；
- Inventory/Procurement ERP；
- payroll/timekeeping system；
- functional-test execution page；
- M&V savings page；
- direct equipment control page；
- generic document repository。

---

# 52. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Problem Statement / Work Scope / Source Evidence 已分离；
- Job Plan Template / Work Plan / Execution Record 已分离；
- Safety / Permit / LOTO owner contract 明确；
- Planned vs Actual 语义已接受；
- Task/Checklist execution contract 明确；
- Completion Evidence vs Verification Evidence 已分离；
- Completion mutation 与 source/verification 状态独立；
- Functional Verification handoff 明确；
- Work Detail 与 Work Center / Verification / Diagnosis 职责边界明确；
- read model/batch contract 明确；
- old WorkOrderForm / Drawer / CRUD detail 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
