# 30 管理评审 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `30 管理评审`  
> **Route intent：** `/sites/:siteId/management-reviews`、`/sites/:siteId/management-reviews/:reviewId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Management Review`、`EnPI`、`EnB`、`SEU`、`M&V` 等术语仅作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有高管 Dashboard、旧会议纪要页、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Management Review / Decision / Action / Evidence contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **支持管理层基于冻结、可追溯的能源与运营证据，定期评价能源管理体系和能源绩效是否适宜、充分、有效，并形成可执行、可分派、可追踪、可复核的管理决策和行动。**

本 Surface 是 **management review decision workspace + governance record**，不是：

- 高管 KPI Dashboard；
- 29 报告中心的 PDF 预览页；
- 23 目标与行动计划的复制页；
- 21 节能机会审批页；
- 22 优化方案审批页；
- 24 M&V 计算页；
- 11 工单中心；
- 普通会议纪要工具；
- 一个把所有状态压成“绿 / 黄 / 红”的 Executive Summary；
- 一个把“行动完成”直接解释成“能源绩效已改善”的页面。

用户完成一次 Management Review 后，应能回答：

1. 这是哪一次管理评审；
2. 属于完整评审还是部分评审；
3. 评审日期/周期是什么；
4. 谁参与了评审；
5. 当时实际呈现了哪些信息；
6. 使用了哪个 Management Review Pack revision；
7. 上次评审行动完成到什么程度；
8. 能源绩效发生了什么变化；
9. EnPI / EnB 是否仍适用；
10. SEU 是否发生重大变化；
11. 目标是否按计划推进；
12. 已验证节能量是多少；
13. 有哪些重大偏差或风险；
14. 有哪些内部审核发现/纠正措施；
15. 管理层做出了哪些正式 Decision；
16. Decision 产生了哪些 Action；
17. 每个 Action 的 Owner / Due Date / Resource 是什么；
18. 哪些 Action 当前 Blocked；
19. Action 完成后是否需要 Effectiveness Review；
20. 下一次评审何时进行；
21. 评审记录是否完整、可审计；
22. 后续源数据修正是否影响历史评审结论。

---

# 2. 主要用户

## Primary

### Top Management / Site Leadership

基于能源绩效和 EnMS 证据做资源、目标、政策、改进优先级与治理决策。

### Energy Manager / Management Representative

准备评审输入、组织评审、维护正式记录、跟踪管理行动。

### Facility / Operations Manager

说明运营偏差、工单/控制/策略实施状态，并承担部分后续 Action。

## Secondary

- Finance / Procurement：参与资源、投资、采购相关决策；
- Sustainability / Carbon Manager：提供碳/可持续性背景；
- M&V Engineer：提供已验证节能量与持续性证据；
- Internal Auditor：提供 EnMS audit findings；
- Data / Platform Owner：说明数据质量、数据治理和源系统风险；
- Engineering Lead：说明 SEU、优化、重大变更和技术风险；
- Read-only governance/auditor role：审查历史管理评审记录。

---

# 3. 外部最佳实践依据

## 3.1 ISO 50001:2018 — Top Management、目标、数据、评审与持续改善

ISO 50001:2018 为能源管理体系（EnMS）提供系统性框架，采用 PDCA 模型，要求组织建立政策、目标，使用数据理解能源绩效、测量结果、评审体系有效性并持续改善。

来源：

- https://www.iso.org/standard/69426.html
- https://www.iso.org/iso-50001-energy-management.html

**本页采用：**

- Management Review 是 top management governance activity；
- 评审必须围绕能源绩效和 EnMS 是否适宜、充分、有效；
- Review 必须形成 Decision / Action；
- Resource decisions 是正式输出；
- review record 需要可追溯；
- Continual Improvement 不是 KPI 卡片，而是正式决策闭环。

## 3.2 ISO 50004:2020 — 管理体系实施、维护和改进需要结构化治理

ISO 50004:2020 提供实施、维护和改进 ISO 50001 EnMS 的实务指导，支持结构化、战略性的持续改善。

来源：

- https://www.iso.org/standard/74863.html

**本页采用：**

- Management Review 不只评价结果，也要评价 EnMS 本身；
- governance state 与 energy-performance state 分开；
- Decision 需要可执行的 follow-up；
- review input、decision、action、effectiveness 不能混成一个状态。

## 3.3 DOE 50001 Ready / eGuide — Management Review 是“ACT”环节

DOE eGuide 明确指出，Management Review 是 PDCA 的 ACT 阶段，由 top management 对 EnMS 和能源绩效进行定期评价，分析数据、判断行动是否适当，并做出需要的变化和改进决策。

来源：

- https://www1.eere.energy.gov/manufacturing/eguide/iso_step_5_1.html
- https://www.energy.gov/cmei/ito/50001-ready-program

DOE eGuide 还明确要求管理评审输入至少涵盖：

- energy policy；
- objectives / targets / action plans 与状态；
- EnPI 与过去、当前、预测能源绩效，包括 SEU；
- legal/other requirements 的合规评价；
- EnMS audit results；
- corrective / preventive actions 状态；
- recommendations / improvement opportunities；
- previous management review actions。

管理评审输出至少形成对以下内容的决策/行动：

- energy performance；
- energy policy；
- EnPI；
- objectives；
- targets；
- resources。

并要求正式保留 review date、participants、information/topics、decisions/actions、assignments。

**本页采用：**

- Presented Information 与 Decision 分离；
- Previous Review Actions 是首要输入；
- Review 必须形成 Decision Record；
- Decision 要产生可追踪 Action；
- Review record 需要 immutable historical context；
- partial review 可以存在，但完整覆盖范围必须由 governance owner 管理。

## 3.4 DOE 50001 Ready Reference Design Guide — Internal Audit / Savings / Management Review 分开

50001 Ready 任务体系把 Internal Audit、Calculate Energy Savings、Management Review 分成独立任务。

来源：

- https://betterbuildingssolutioncenter.energy.gov/sites/default/files/attachments/50001%20Ready%20Reference%20Design%20Guide%20v0.1.pdf

**本页采用：**

```text
Internal Audit Finding
≠ Management Decision

Verified Savings
≠ Management Decision

Management Review
= consume evidence + decide
```

## 3.5 ISO 50006:2023 — EnPI / EnB 需要维护与评价

ISO 50006:2023 指导建立、使用和维护 EnPI 与 EnB，以评价和证明能源绩效改善。

来源：

- https://www.iso.org/standard/79367.html

**本页采用：**

- Management Review 输入绑定明确 EnPI / EnB revision；
- baseline / EnPI revision 不静默改写历史 review；
- review 可以产生“需要复审 EnPI/EnB”的 Decision；
- 当前实际绩效与历史 review evidence 分开。

---

# 4. 产品语言契约

主界面中文优先：

```text
管理评审
评审周期
评审范围
评审资料包
上次评审行动
能源绩效
重大用能
目标进展
已验证节能量
重大偏差
运营风险
数据风险
内部审核发现
纠正措施
改进机会
资源需求
管理决策
后续行动
责任人
截止日期
有效性复核
```

保留必要标准缩写：

```text
EnPI
EnB
SEU
M&V
EnMS
```

不要默认主界面显示：

```text
Input Clause
Output Clause
Audit Finding UUID
Evidence Object Hash
Decision Engine State
```

这些进入 Advanced / Audit。

---

# 5. Management Review Domain Vocabulary

## Management Review Definition

定义一个组织如何开展管理评审的治理规范，包括输入范围、参与角色、frequency/cadence、必需 Decision category 和记录要求。

## Management Review Instance

一次具体的管理评审活动。

## Review Pack

29 报告中心生成并冻结的管理评审资料包 revision。

## Presented Information

本次评审实际向管理层呈现的信息与证据。

## Management Decision

管理层在本次评审中正式做出的治理决策。

## Review Action

由 Management Decision 派生、需要明确 Owner 和 Due Date 的后续行动。

## Effectiveness Review

Review Action 完成后，对“行动是否真正解决问题/产生预期改善”的后续评价。

## Previous Review Action

上一次或更早 Management Review 产生、在本次评审中需要复盘的 Action。

---

# 6. Mandatory Semantic Separation

```text
Review Pack ≠ Management Review

Presented Information ≠ Management Decision

Recommendation ≠ Management Decision

Decision ≠ Review Action

Review Action ≠ Work Order

Decision ≠ Action Completed

Action Completed ≠ Effectiveness Confirmed

Action Completed ≠ Energy Performance Improved

Target Progress ≠ Target Achievement

Target At Risk ≠ Target Failed

Expected Savings ≠ Verified Savings

Verified Savings ≠ Management Decision

Internal Audit Finding ≠ Corrective Action

Corrective Action ≠ Management Decision

Corrective Action Closed ≠ Finding Effective Closure automatically

Current Evidence ≠ Historical Review Evidence

Current EnPI Revision ≠ Review EnPI Revision

Current EnB Revision ≠ Review EnB Revision

Review Scheduled ≠ Review Conducted

Review Conducted ≠ Review Completed

Review Completed ≠ All Actions Closed

Review Record ≠ Meeting Notes

Partial Review ≠ Full Review

Management Review Pack ≠ Management Review Decision
```

---

# 7. Primary Questions

30 默认必须回答：

1. 这是哪次 review；
2. review type 是 full 还是 partial；
3. review definition/version 是什么；
4. review date / period；
5. participants；
6. 是否满足组织定义的 quorum / required roles；
7. 使用哪个 Review Pack revision；
8. 哪些 required input 已呈现；
9. 哪些 input 缺失/不完整；
10. previous actions 当前状态；
11. energy performance 是否持续改善；
12. EnPI / EnB 是否仍合适；
13. SEU 是否发生重大变化；
14. target/action plan 是否 on track；
15. verified savings 如何；
16. major deviation / risk 有哪些；
17. internal audit / corrective action 有哪些；
18. top management 做了哪些决定；
19. Decision 是否需要资源；
20. Decision 是否改变 policy / EnPI / target / resource；
21. Decision 派生了哪些 action；
22. action owner / due date；
23. 哪些 action blocked；
24. action completion 是否需要 effectiveness review；
25. 下次 review cadence / trigger。

---

# 8. Information Architecture

```text
Context Header
↓
Review Identity / Scope / Period
↓
Review Pack / Input Coverage
↓
Previous Review Actions
↓
Energy Performance Summary
  EnPI / EnB
  SEU Changes
  Target Progress
  Verified Savings
↓
Major Deviations / Operational & Data Risks
↓
Internal Audit / Corrective Actions（capability-gated）
↓
Improvement Opportunities / Resource Needs
↓
Management Decisions
↓
Review Actions
↓
Effectiveness / Follow-up
↓
Review Record / Audit
```

默认是 **decision-first governance workspace**，不是 KPI card wall。

---

# 9. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/management-reviews
/sites/:siteId/management-reviews/:reviewId
```

Search Params 可以包括：

```text
status
reviewType
period
owner
decisionStatus
actionStatus
selectedDecision
selectedAction
```

不进入 URL：

```text
local notes draft
hover
unsaved decision text
modal confirmation
```

Review ID 是 durable / shareable。

---

# 10. Capability Gating

Management Review 本身可以作为 Energy Management governance 能力存在。

以下输入按 capability 出现：

```text
Internal Audit Findings
Formal Corrective Action
Compliance Evaluation
Carbon Governance
M&V
Billing / Cost
DER
```

如果某 capability 不存在：

```text
不显示该模块
```

而不是：

```text
0 findings
0 issues
```

---

# 11. Review Definition Contract

Review Definition 至少包含：

```text
Definition ID
Version
Review Type(s)
Required Inputs
Required Roles / Participants
Quorum Policy（若有）
Cadence / Trigger
Required Decision Categories
Record Requirements
Retention Policy
Business Owner
```

Definition 是 versioned governance object。

---

# 12. Review Type Contract

建议支持：

```text
Full Management Review
Partial Management Review
Extraordinary Review
```

Partial Review 必须明确：

```text
Covered Inputs
Not Covered Inputs
Reason
Expected Full-review completion horizon
```

不能把只讨论一个资本项目的会议当作完整年度 Management Review。

---

# 13. Review Cadence Contract

DOE eGuide 明确指出 management review 应在 planned intervals 进行，但频率由组织需要决定；组织出现重大变化、行动计划/资本项目较多时可以增加 review frequency。

因此：

```text
Cadence
= owner-defined governance rule
```

禁止：

```text
hardcode annually
```

页面可以显示：

```text
Next Planned Review
Trigger-based Review Required
```

但 schedule owner 决定真正 cadence。

---

# 14. Review Instance Identity Contract

每次 Review 至少包含：

```text
Review ID
Definition ID / Version
Site / Org Scope
Review Type
Review Period
Review Date / Started At
Governance State
Pack Revision
Chair / Management Owner
Participants
```

---

# 15. Review Lifecycle Contract

建议：

```text
Draft
Preparing
Ready for Review
Scheduled
In Review
Decision Pending
Completed
Completed with Open Actions
Cancelled
Superseded
Archived
```

```text
Scheduled ≠ Conducted
Conducted ≠ Completed
Completed ≠ All Actions Closed
```

---

# 16. Review Pack Contract

Review 默认绑定 29 生成的：

```text
Management Review Pack
Report ID
Revision
Data Cutoff
Source Snapshot
Generated At
```

Review 不能打开时重新查询“当前最新数据”替代 Pack。

---

# 17. Review Pack Immutability Contract

如果：

```text
Review MR-2026-H2
used Pack rev3
```

后来 29 生成：

```text
Pack rev4
```

历史 Review 仍然引用 rev3。

页面可以显示：

```text
Newer Pack Revision Exists
```

但不自动替换。

---

# 18. Review Input Coverage Contract

对 required input 显示：

```text
Presented
Presented with Warning
Not Presented
Not Applicable
Unavailable
```

必须区分：

```text
required input missing
```

和：

```text
not applicable by definition
```

---

# 19. Review Readiness Contract

Review Readiness 不做黑盒分数。

显示明确事实：

```text
Review Pack Ready
Previous Actions Updated
Energy Performance Evidence Ready
EnPI/EnB Review Ready
Target Status Ready
M&V Results Ready
Internal Audit Ready
Corrective Action Status Ready
Resource Requests Ready
```

缺什么直接列什么。

禁止：

```text
Readiness = 87%
```

却无法解释。

---

# 20. Participant Contract

记录：

```text
Name / Role
Organization Role
Review Role
Present / Absent
Decision Authority（若 owner 定义）
```

DOE eGuide 要求 top management 和 management representative 至少参与，并可根据谁需要信息、谁能采取行动、谁能提供资源邀请其他人员。

具体 required roles 由 Definition owner 决定。

---

# 21. Quorum Contract

如果组织定义 quorum / required attendance：

```text
Quorum Satisfied
Quorum Not Satisfied
Unknown
```

由 governance owner 给出。

前端不能通过 participant count 自己猜。

---

# 22. Previous Review Actions Contract

本次 Review 必须首先看到上次 Actions：

```text
Action
Decision Source
Owner
Due Date
Status
Blocker
Completion Evidence
Effectiveness State
```

这不是普通 task list，而是 governance continuity。

---

# 23. Previous Action Status Contract

建议：

```text
Open
In Progress
Blocked
Completed
Cancelled by Decision
Effectiveness Pending
Verified Effective
Verified Ineffective
Inconclusive
```

```text
Completed
≠ Verified Effective
```

---

# 24. Energy Performance Input Contract

Energy Performance Summary 可以消费：

```text
14 Energy Analysis
16 Efficiency
17 Energy Review
23 Objectives / Action Plans
24 M&V
```

但 30 不重新计算。

至少显示：

```text
Current Period
Comparison / EnB Reference
Normalized / Actual distinction
Key EnPI
Verified Savings
Material Deviations
```

---

# 25. EnPI / EnB Review Contract

每个重要 EnPI / EnB 至少显示：

```text
Name
Revision
Status
Current Performance
Applicability
Known Issues
Change Since Previous Review
Review Recommendation
```

管理层可以做 Decision：

```text
Keep
Review Required
Revise
Retire / Replace
```

但真正 EnPI/EnB 修改仍回到 17 owner workflow。

---

# 26. SEU Change Contract

SEU input 至少表达：

```text
Current SEU
Previous SEU status
Significance rationale
Material Change
Relevant Variable / Static Factor Change
Improvement Opportunity
Risk
```

30 不自己重新判定 SEU。

---

# 27. Target Progress Contract

消费 23 的正式目标状态：

```text
Action Progress
Energy Performance Progress
Forecast
Verification State
Blockers
```

明确：

```text
Action Progress
≠ Performance Progress
```

以及：

```text
At Risk
≠ Failed
```

---

# 28. Verified Savings Contract

只消费 24 的 Verified Savings Result。

显示：

```text
M&V Project
Result Revision
Verified Savings
Uncertainty / Quality
Reporting Period
Persistence State
```

禁止：

```text
Expected Savings
→ Verified Savings
```

---

# 29. Major Deviation Contract

Major Deviation 可以来自 owner-defined：

```text
Energy Performance
SEU
Target
M&V Persistence
Billing / Cost
Carbon
Operational Reliability
Data Quality
Compliance
```

Materiality / significance 由对应 owner 决定。

前端不硬编码 `>10% = major`。

---

# 30. Operational Risk Contract

可消费：

```text
Critical / recurring alarms
Diagnosis backlog
Work backlog / overdue
Control / Strategy drift
Failed verification
DER / resilience risk
```

但 30 不重新诊断 root cause。

---

# 31. Data Risk Contract

可消费：

```text
31 Data Quality
32 Semantic / Meter Governance（若存在）
```

重点回答：

```text
哪些管理指标当前不值得完全信任？
影响哪些目标 / M&V / 报告 / 决策？
```

不做一张“Data Health 92”卡片。

---

# 32. Internal Audit Input Contract

Formal EnMS capability 存在时，显示：

```text
Audit Scope
Audit Date
Finding
Severity / Classification（owner-defined）
Owner
Corrective Action
Due
Status
```

```text
Audit Finding
≠ Management Decision
```

管理层可以决定：

```text
Accept corrective plan
Escalate
Allocate resource
Change policy/process
Require follow-up
```

---

# 33. Corrective Action Contract

Corrective Action 至少显示：

```text
Source Finding / Nonconformity
Action
Owner
Due
Status
Evidence
Effectiveness State
```

```text
Corrective Action Completed
≠ Effective
```

是否有效要由 corrective-action / audit owner 验证。

---

# 34. Improvement Opportunities Contract

消费 21 的 Opportunity，不在 30 复制完整机会 Portfolio。

只显示需要管理层决策的：

```text
High-value
Resource-dependent
Cross-functional
Capital-requiring
Strategic
Blocked by decision
```

Recommendation 仍不是 Decision。

---

# 35. Resource Needs Contract

资源需求可以是：

```text
Budget
People / FTE
Training
Metering / Instrumentation
Software / Data
Engineering Support
Capital
Maintenance Window
Procurement
External Specialist
```

必须说明：

```text
Requested Resource
Reason
Linked Risk / Target / Opportunity
Required By
Consequence if Not Approved
```

---

# 36. Management Decision Contract

每个正式 Decision 至少包含：

```text
Decision ID
Review ID
Decision Category
Decision Statement
Decision Maker(s)
Decision Date
Rationale
Evidence References
Conditions
Resource Commitment（若有）
Effective Date（若有）
```

Decision 是 durable governance object。

---

# 37. Decision Category Contract

建议包括：

```text
Energy Performance
Energy Policy
EnPI / EnB
Objective / Target
Resource Allocation
Improvement Initiative
Risk / Corrective Action
Governance / EnMS Change
Review Cadence
Other Approved Category
```

不要只有一个自由文本“会议结论”。

---

# 38. Recommendation vs Decision Contract

```text
Recommendation
= 提案 / 建议

Decision
= 管理层正式决定
```

例如：

```text
Recommendation
采购冷冻水流量计
```

不能显示：

```text
Approved
```

除非 Decision owner 真正记录批准。

---

# 39. Decision State Contract

建议：

```text
Proposed
Decision Pending
Approved
Approved with Conditions
Rejected
Deferred
Superseded
Withdrawn
```

对于：

```text
Approved with Conditions
```

Conditions 必须成为后续 Action / gate，而不是一行备注。

---

# 40. Decision Revision Contract

Approved Decision 不原地改写。

如果管理层后来改变决定：

```text
Decision D-18
Approved
↓
Decision D-21
Supersedes D-18
```

保留历史。

---

# 41. Decision ≠ Direct Execution

Management Review 可以决定：

```text
批准预算
要求复审 EnPI
批准目标调整方向
要求开发 Optimization Plan
```

但不能直接：

```text
写 BACnet point
发布 Strategy
执行 Override
```

真实执行继续进入 22/25/27 等 owner workflow。

---

# 42. Review Action Contract

Decision 需要后续执行时创建 Review Action：

```text
Action ID
Decision ID
Action
Owner
Due Date
Priority（governance owner）
Dependencies
Required Resource
Status
Blocker
Completion Evidence
Effectiveness Requirement
```

---

# 43. Action State Contract

建议：

```text
Open
Assigned
In Progress
Blocked
Completed
Cancelled by New Decision
Overdue
Effectiveness Pending
Closed
```

状态维度可根据 owner 实现，但必须保留：

```text
Completed ≠ Effective
```

---

# 44. Action Blocker Contract

Blocked Action 至少说明：

```text
Reason
Blocked Since
Blocker Owner
Required Unblock Action
Expected Review / Resolution
Impact
```

不能只显示黄色 `Blocked`。

---

# 45. Action Completion Contract

Action 完成需要：

```text
Completion Date
Completed By
Completion Evidence
Outcome Summary
Follow-up Required
```

Completion 由 action owner authoritative mutation 确认。

---

# 46. Effectiveness Review Contract

对于需要 effectiveness review 的 action：

```text
Not Required
Required
Scheduled
In Review
Effective
Ineffective
Inconclusive
Overdue
```

例如：

```text
Action
更新夜间冷站 schedule policy

Completed
Yes

Effectiveness
Pending 30-day review
```

不能一完成 action 就显示“改进已实现”。

---

# 47. Action Handoff Contract

Review Action 可以 handoff 到：

```text
23 Objectives / Action Plans
21 Opportunity
22 Optimization Plan
11 Work Order
31 Data Quality
Training / Procurement owner
```

handoff 必须保留：

```text
Decision ID
Action ID
Reason
Owner
Due
Evidence
```

---

# 48. Management Review Record Contract

正式 Review Record 至少包含：

```text
Review ID
Date
Participants
Topics / Inputs Presented
Pack Revision
Decisions
Actions
Assignments
Review Conclusion
Next Review
```

与 DOE eGuide 对 review record 的要求一致。

---

# 49. Review Record ≠ Meeting Notes

Meeting Notes 可以存在，但：

```text
Notes
≠ Presented Information
≠ Decision
≠ Action
```

正式记录必须使用结构化 Decision / Action objects。

---

# 50. Review Conclusion Contract

Review Conclusion 可以回答：

```text
EnMS Suitability
EnMS Adequacy
EnMS Effectiveness
Energy Performance Continual Improvement
```

建议状态：

```text
Confirmed
Needs Improvement
Not Demonstrated
Inconclusive
Not Evaluated
```

这些判断由 management-review owner 给出。

前端不从 KPI 自动生成。

---

# 51. Continual Improvement Contract

```text
More Actions Completed
≠ Continual Improvement Demonstrated
```

Continual improvement evidence 应来自：

```text
Energy Performance
Verified Savings
Target Results
Corrective Action Effectiveness
Governance Effectiveness
```

而不是 task completion 数量。

---

# 52. Current Evidence vs Historical Evidence

Review Detail 应区分：

```text
Evidence Used in Review
```

和：

```text
Current Updated Evidence
```

如果当前数据已经变化，可以显示：

```text
Current evidence differs from review snapshot
```

但不能替换历史 input。

---

# 53. Source Correction Impact Contract

如果 29 / 24 / 17 等源数据在 review 后更正：

```text
Source Revision Changed
↓
Impact Assessment
↓
No Impact / Review Follow-up Required / Extraordinary Review Required / Unknown
```

由 governance owner 决定。

不能自动重写管理 Decision。

---

# 54. Extraordinary Review Contract

以下情形可以触发额外 review（owner-defined）：

```text
Major Energy Performance Deviation
Major SEU Change
Major Regulatory Change
Major Data Correction
Material M&V Revision
Major Incident
Major Organizational Change
```

是否触发 Extraordinary Review 由 governance policy 决定，前端不硬编码。

---

# 55. Review Frequency / Full Coverage Contract

Partial reviews 可以分期完成。

如果组织要求在某 horizon 内覆盖完整 EnMS，系统应能展示：

```text
Inputs Reviewed This Cycle
Inputs Still Due
Full Coverage Due Date
```

但 horizon 由 owner 定义，不默认 12 个月。

---

# 56. Compliance Input Contract

若启用 formal compliance capability，可以呈现：

```text
Legal / Other Requirement
Evaluation Status
Material Change
Open Nonconformance
Required Decision
```

30 不自己判断法律合规。

---

# 57. Policy Review Contract

Review 可以产生：

```text
Keep Energy Policy
Revise Policy
Policy Review Required
```

但真正 Policy 内容和发布由 policy owner 管理。

Management Review Decision 只记录治理结论与 handoff。

---

# 58. Objective / Target Change Contract

Review 可以决定：

```text
Keep target
Revise target
Add target
Retire / supersede target
```

但 23 是 Objective / Target 的正式 owner。

30 不直接原地改 23 数据。

---

# 59. EnPI / EnB Change Contract

同理，Decision 可以：

```text
Require EnPI review
Approve direction to revise EnB
Request new relevant-variable model
```

但真正 revision 在 17。

---

# 60. Resource Decision Contract

资源 Decision 至少说明：

```text
Resource Type
Amount / Scope
Purpose
Owner
Availability Date
Conditions
Linked Action / Target / Risk
```

例如：

```text
批准预算 ¥180,000
用于冷站流量计升级
条件：Q4 采购并纳入 M&V instrumentation plan
```

---

# 61. Resource Decision ≠ Spend Executed

```text
Budget Approved
≠ Purchase Order Issued
≠ Resource Available
```

采购/财务执行属于对应 owner。

---

# 62. Investment Decision Boundary

Review 可以做：

```text
Approve for development
Approve budget envelope
Defer
Reject
Request business case
```

但复杂投资审批可以进入专门 finance/procurement workflow。

30 不做通用 ERP。

---

# 63. Risk Decision Contract

对于 Operational / Data / EnMS risk，Decision 可以：

```text
Accept
Mitigate
Escalate
Transfer
Request more evidence
```

但风险模型由 risk owner 定义，前端不生成黑盒 Risk Score。

---

# 64. Review Agenda Contract

建议 Review Agenda 围绕 Decision Need 组织，而不是固定 30 页 PPT：

```text
Previous Actions
Performance
Targets
SEU / EnPI / EnB
Verified Savings
Major Deviations / Risks
Audit / Corrective Actions
Opportunities
Resources
Decisions Required
```

允许 owner 自定义顺序，但 required inputs 不能因此丢失。

---

# 65. Decision Needed Queue Contract

页面可以优先显示：

```text
Resource Approval Required
Target Revision Required
Baseline Review Required
Corrective Action Escalation
Opportunity Decision
Extraordinary Review Required
```

这比 6 张漂亮 KPI 更适合管理评审。

---

# 66. Default Prioritization Contract

默认首屏优先：

```text
Previous Review Actions Overdue / Blocked
Decision Required
Material Performance Deviation
Target At Risk
Verified Savings / Persistence Issue
Critical Data Risk
Open Audit / Corrective Action
Resource Constraint
```

而不是：

```text
Energy Consumption
Cost
Carbon
Savings
```

四个高管卡片。

---

# 67. Review Inspector Contract

快速显示：

```text
Review ID
Type
Period
Date
Pack Revision
Input Coverage
Participants
Decision Count
Open Actions
Blocked Actions
Review Conclusion
```

然后进入完整 Review Detail。

---

# 68. Review Detail Contract

完整页面包含：

```text
Identity
Pack / Input Coverage
Previous Actions
Energy Performance
EnPI / EnB / SEU
Targets
Verified Savings
Major Deviations / Risks
Audit / Corrective Actions
Opportunities / Resources
Decisions
Review Actions
Effectiveness
Review Record / Audit
```

不做一个 PDF iframe + 会议纪要 textarea。

---

# 69. Decision Workspace Contract

每个 Decision detail 显示：

```text
Decision Need
Evidence Presented
Recommendation
Alternatives（若 owner 提供）
Risk / Impact
Decision
Conditions
Resource
Owner
Follow-up Action
```

Recommendation 与 Decision 视觉必须不同。

---

# 70. Alternative Options Contract

如果管理层需要在多个方案间选择：

```text
Option A
Option B
Defer
Request More Evidence
```

这些 options 由业务 owner 提供。

30 不自动生成“最优选项”。

---

# 71. Decision Audit Contract

至少审计：

```text
Decision proposed
Decision updated before approval
Decision made
Decision superseded
Condition changed
Action assigned
Action completed
Effectiveness reviewed
```

每条包括：

```text
Who
When
Before / After
Reason
Evidence reference
```

---

# 72. Review Audit Contract

至少审计：

```text
Review created
Pack revision attached
Participants confirmed
Review started
Input presented
Decision recorded
Review completed
Review record corrected/reissued（若允许）
Archived
```

历史 record 不 silent overwrite。

---

# 73. Review Record Correction Contract

如果 review record 存在录入错误：

```text
Record Correction
→ new record revision / amendment
```

保留：

```text
Original Record
Correction Reason
Corrected By
Corrected At
```

不能覆盖历史 Decision 文字而无痕。

---

# 74. Decision Correction vs Decision Change

```text
Typo correction
≠ Management changes its decision
```

Decision change 应产生 superseding Decision。

文书 correction 产生 amendment/audit。

---

# 75. Live Action Status vs Frozen Review Record

这条必须明确：

```text
Review Evidence / Decision
= frozen historical record

Action Status
= live follow-up state
```

因此同一个页面可以看到：

```text
Review completed 2026-09-10
Decision frozen
Action WO-302 current state = In Progress
```

这不是历史改写。

---

# 76. Follow-up Contract

每个 Review Action 可以在下一次 Management Review 自动出现在：

```text
Previous Review Actions
```

但是否属于 next review input 由 governance owner 定义。

---

# 77. Effectiveness Handoff Contract

不同 Action 的 effectiveness owner 不同：

```text
Functional control change → 13 Functional Verification
Savings → 24 M&V
Target progress → 23 Objectives
Data fix → 31 Data Quality
Corrective action → Audit/Corrective owner
```

30 只汇总结果，不重复实现验证方法。

---

# 78. Management Review Conclusion vs Action Status

Review 本身可以 Completed with Open Actions。

```text
Review Completed
Open Actions = 5
```

是合法组合。

不要求所有 Action 关闭后才能完成 Review。

---

# 79. Query / Read Model Contract

Management Review Ledger 使用服务端 summary read model。

禁止：

```text
50 reviews
→ 50 pack queries
→ 50 participant queries
→ 50 decision queries
→ 50 action queries
```

Summary 至少包含：

```text
identity
period
type
pack revision
input coverage
decision count
open/blocked action count
review conclusion
```

Detail 再 lazy-load heavy evidence。

---

# 80. Snapshot + Follow-up Stream Contract

Review evidence 本身不是实时流。

但 open actions / follow-up 可以使用：

```text
Snapshot
+
Action update stream / query refresh
```

Action stream disconnect：

```text
≠ Review record unavailable
≠ Action failed
```

---

# 81. Security Boundary Contract

Backend 需要验证：

```text
Principal
Site / Org Scope
Review read permission
Review prepare permission
Decision permission
Resource-decision permission
Action assignment permission
Sensitive input permission
Audit permission
```

前端按钮隐藏不是 security boundary。

---

# 82. Sensitive Information Contract

Review 可能包含：

```text
成本 / Budget
Personnel
Internal Audit
Compliance
OT Risk
Procurement
Vendor / Contract
```

按角色显示。

不将敏感内部追踪字段默认暴露给普通运营用户。

---

# 83. AI Assistance Boundary

AI 可以：

```text
基于 frozen review pack 草拟 executive summary
总结 major deviations
整理 previous actions
提示 decision gaps
整理 conflicting evidence
草拟 decision wording
草拟 action summary
比较两次 management review
```

AI 不能：

```text
自动做 Management Decision
自动批准预算
自动修改 target / EnPI / EnB
自动关闭 corrective action
自动把 action complete 判成 effective
自动把 forecast 判成 achievement
自动隐藏 missing input
自动重写 historical review evidence
```

AI 输出默认是 Draft / Review Required。

---

# 84. Accessibility Contract

- Review Ledger 使用 semantic table；
- Decision / Action 状态不只靠颜色；
- keyboard 可浏览 review、decision、action；
- frozen evidence 与 live action status 有清晰文本标签；
- status updates 不抢 focus；
- 768px 下保留 Review、Pack、Decision、Open Actions、Conclusion；
- 无 page-level 横向 overflow。

---

# 85. Browser Acceptance Criteria

## Review Integrity

- Review Pack revision 固定；
- historical input 不随 current source 更新；
- source correction 不自动重写 Decision；
- Review Record amendment 可审计；
- Partial Review 不冒充 Full Review。

## Inputs

- Previous Review Actions 可见；
- Energy Performance / EnPI / EnB / SEU / Targets / M&V 分开；
- Internal Audit / Corrective Actions capability-gated；
- Missing input 不显示“正常”。

## Decisions

- Recommendation 与 Decision 分开；
- Decision 有 actor/date/evidence/rationale；
- Approved with Conditions 有真实 conditions；
- Decision 不直接执行 Control / Strategy mutation。

## Actions

- Decision 与 Action 分开；
- Owner / Due / Blocker 明确；
- Completed 不等于 Effective；
- Effectiveness owner 可追溯；
- previous actions 在后续 review 可复盘。

## Performance

- Action completion 不称 performance improvement；
- Target At Risk 不称 Failed；
- Expected Savings 不称 Verified；
- EnPI / EnB revision 绑定明确。

## Resource Governance

- Resource Requested / Approved / Available 分开；
- Budget Approved 不称 Purchase Executed；
- resource decision 可追溯到风险/目标/机会。

## Accessibility / Responsive

- semantic table / headings；
- 状态不只靠颜色；
- keyboard 可操作；
- 768px 核心事实可见；
- 无全局横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无前端重算 EnPI / savings / audit status；
- 无 old Executive Dashboard fallback；
- 无 legacy management-review compatibility adapter；
- 无 N+1 review ledger queries；
- review scenario 无 runtime/network error。

---

# 86. No Defensive Programming / No Compatibility Design

明确禁止：

```text
review API error → []

review pack unavailable
→ use latest dashboard data

pack revision unavailable
→ use latest pack

previous actions unavailable
→ 0 open actions

EnPI unavailable
→ show previous EnPI

EnB unavailable
→ previous period

M&V unavailable
→ expected savings

internal audit unavailable
→ 0 findings

corrective action unavailable
→ all closed

participant unavailable
→ assume present

quorum unavailable
→ satisfied

missing input
→ normal

missing risk
→ low risk

action completed
→ effective

action completed
→ performance improved

all actions completed
→ review successful

target at risk
→ target failed

forecast on track
→ target achieved

verified savings exists
→ management approved initiative

recommendation
→ decision

decision approved
→ control / strategy executed

budget approved
→ spend executed

source correction
→ rewrite review history

new pack revision
→ replace historical review pack

partial review
→ mark full review complete

same topic + same date
→ merge review decisions

AI summary
→ official decision record

multiple review APIs
→ first success wins

old Executive Dashboard fallback
legacy ManagementReview adapter
```

正式原则：

> **一次 Management Review 对应一个冻结、可追溯的 evidence context 和一个明确的治理记录。Presented Information、Recommendation、Decision、Action、Completion、Effectiveness 是不同事实。历史评审证据不能被当前数据静默改写，Decision 不能绕过业务 owner 直接执行，Unknown 保持 Unknown。**

---

# 87. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 管理评审 · 2026 H2 · 中央园区                             状态：进行中     │
│ Review Pack：MRP-2026-H2 rev3 · Data Cutoff 2026-09-05 18:00              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 上次评审行动   2 逾期   1 阻塞   6 已完成   3 待有效性复核                  │
├──────────────────────────────────────────────────────────────────────────────┤
│ 本次评审输入                                                                  │
│ 能源绩效       已呈现   EnPI v4 / EnB v3                                   │
│ 目标进展       已呈现   2 项存在风险                                        │
│ 已验证节能量   已呈现   205 MWh · MV-12 rev2                               │
│ SEU 变化       已呈现   冷站 SEU 需复审                                     │
│ 数据风险       已呈现   冷量计数据质量下降                                  │
│ 内部审核       已呈现   2 项 open findings                                  │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 需要管理层决策                                │ 决策详情                     │
│ 1. 冷量计升级预算                              │ 建议：批准 ¥180,000          │
│ 2. 2027 冷站 EnPI target revision              │ 证据：Data Quality + M&V      │
│ 3. 夜间低负荷策略扩大部署                      │ 风险：关键流量数据仍不稳定    │
│                                                │ [批准] [有条件批准] [延期]    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 管理决策                                                                     │
│ D-18  批准冷量计升级预算 ¥180,000 · Owner: Facility Manager · Due 10/30   │
│ D-19  要求 Energy Team 复审 EnPI/EnB · Due 10/15                           │
├──────────────────────────────────────────────────────────────────────────────┤
│ 后续行动                                                                     │
│ A-31  流量计采购     In Progress   Due 10/30                               │
│ A-32  EnPI/EnB 复审  Open          Due 10/15                               │
│ A-33  数据质量验证   Blocked       等待安装窗口                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ 评审结论                                                                     │
│ EnMS 适宜性：确认 · 充分性：需改进 · 有效性：确认                           │
│ 能源绩效持续改善：已证明，但数据质量风险需处理                               │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责与信息层级，不是像素规范。

---

# 88. Explicit Non-goals

30 不是：

- CEO Dashboard；
- Board BI；
- 会议纪要 App；
- 项目管理器；
- 工单系统；
- M&V 计算器；
- EnPI/EnB 编辑器；
- Strategy/Control 执行器；
- Internal Audit 工具；
- 财务/ERP 审批系统；
- 通用风险管理平台。

---

# 89. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Review Definition / Type / Cadence 明确；
- Review Pack / frozen evidence 明确；
- Input Coverage 明确；
- Previous Review Actions 明确；
- Energy Performance / EnPI / EnB / SEU / Target / M&V 输入明确；
- Internal Audit / Corrective Actions capability-gated；
- Recommendation / Decision 分离；
- Decision / Action 分离；
- Completed / Effective 分离；
- Resource Decision 明确；
- Review Record / Amendment / Audit 明确；
- Current Evidence / Historical Evidence 分离；
- Partial / Full Review 分离；
- Security / Sensitive Data 边界明确；
- AI boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
