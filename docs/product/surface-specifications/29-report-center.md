# 29 报告中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `29 报告中心`  
> **Route intent：** `/sites/:siteId/reports`、`/sites/:siteId/reports/:reportId`、`/sites/:siteId/report-definitions/:definitionId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Report Definition`、`Data Cutoff`、`Snapshot`、`Revision`、`Reissue`、`Artifact`、`Distribution` 等术语仅作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有报表页、旧导出页、旧 Ant/ProComponents 表格或历史设计稿。现有代码只能在实施阶段作为真实 Report Definition / Generation / Snapshot / Artifact / Approval / Distribution contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **把能源、运行、告警、诊断、工单、M&V、碳、成本等业务事实，生成可复现、可追溯、可审批、可分发、可纠正并保留历史的正式报告，而不是每次打开都重新查询当前数据库的“动态打印页”。**

本 Surface 是 **traceable report generation + revision + distribution workspace**，不是：

- Dashboard 打印按钮集合；
- 任意页面截图中心；
- 通用 BI 自助分析器；
- Office 文档编辑器；
- 文件网盘；
- 邮件营销工具；
- 30「管理评审」本身；
- 24「M&V」本身；
- 19「碳排放」inventory 计算 owner；
- 一个“导出 Excel”按钮就算完成的页面；
- 一个会随着底层数据更新而静默改变历史报告的 Live View。

用户应该能够回答：

1. 这是什么报告；
2. 由哪个 Report Definition / Version 生成；
3. 报告覆盖什么 Scope；
4. Reporting Period 是什么；
5. Data Cutoff 是什么；
6. 生成时使用了哪些 source revisions；
7. 数据完整性如何；
8. 哪些内容是 Actual、Estimated、Allocated、Derived；
9. 报告是否需要审批；
10. 当前 Report Revision 是哪个；
11. 是否已经发布/分发；
12. 分发给谁；
13. 哪个 Artifact 是正式版本；
14. 底层数据后来是否发生修正；
15. 是否需要 Correction / Reissue；
16. Reissue 与旧版有什么差异；
17. 谁批准了更正；
18. 历史 Revision 是否仍可访问；
19. 报告中的数字能否 drill back 到权威业务 Surface；
20. 报告是否仍在 retention / audit policy 内。

---

# 2. 主要用户

## Primary

### 能源经理 / Facility Manager

生成月报、季度报、能源绩效报告、目标进展报告，并确保数据与结论可追溯。

### 运营经理

获取运行、告警、工单、策略、功能验证等周期性业务报告。

### 管理评审负责人

准备 30 管理评审所需的正式材料，并保留 review input 的 revision。

## Secondary

- Sustainability / Carbon Manager：生成碳 inventory / carbon performance 报告；
- Finance / Cost Analyst：生成账单与成本报告；
- M&V Engineer：发布 verified savings 报告；
- Commissioning Engineer：发布 functional verification / MBCx 报告；
- Site Manager：接收并确认正式报告；
- Auditor：检查报告 lineage、approval、distribution、correction 和历史 revision；
- Executive / Read-only recipient：查看已批准报告，而不是进入工程配置。

---

# 3. 外部最佳实践依据

## 3.1 ISO 50001:2018 — EnMS 需要系统化测量、文件化与持续改进

ISO 50001:2018 建立能源管理体系框架，并采用 PDCA 持续改进能源绩效。

来源：

- https://www.iso.org/standard/69426.html

**本页采用：**

- 报告应服务于能源绩效管理和决策；
- 报告不是临时截图，而是组织记录体系的一部分；
- Report Definition 与 Generated Report 分开；
- 管理评审材料应可追溯到正式数据与记录。

## 3.2 ISO 50006:2023 — EnPI / EnB 评价必须可维护、可解释

ISO 50006:2023 指导建立、使用和维护 EnPI 与 EnB，以评价和证明能源绩效改善。

来源：

- https://www.iso.org/standard/79367.html

**本页采用：**

- 报告引用 EnPI / EnB 时必须绑定 version；
- 后续 EnPI / EnB revision 不自动改写历史报告；
- performance reporting 必须保留 measurement/monitoring context；
- 报告中的 baseline / normalized metric 不从前端临时重算。

## 3.3 DOE 50001 Ready / eGuide — 管理评审材料与记录应被保留

DOE eGuide 对管理评审要求包括收集适当信息、以适合决策的方式呈现，并维护管理评审记录。管理评审记录应保留 review date、participants、topics/information、decisions/actions 和 assignments。

来源：

- https://www1.eere.energy.gov/manufacturing/eguide/iso_step_5_1.html
- https://www.energy.gov/cmei/ito/50001-ready-program

**本页采用：**

- 29 提供正式报告与管理评审输入；
- 30 管理评审消费已生成/批准的报告 revision；
- 报告 revision、批准、分发和纠正必须留痕；
- “重新生成同一时期报告”不能覆盖历史会议依据。

## 3.4 DOE eGuide — Records 是实施与效果的证据

DOE eGuide 明确将 records 用于证明活动已执行、结果已产生。

来源：

- https://www1.eere.energy.gov/manufacturing/eguide/iso_50001_energy_management.html
- https://www1.eere.energy.gov/manufacturing/eguide/iso_step_1_1.html

**本页采用：**

- Report Artifact 是正式 record，不是瞬时 UI；
- correction 通过新 revision/reissue 表达；
- 原 artifact 不能被静默覆盖；
- source lineage 与 generated-at context 必须保留。

## 3.5 NIST SP 800-92 — 记录/日志体系需要完整管理过程

NIST SP 800-92 提供 enterprise log management 的建立、维护和 robust process 指导，强调 Audit and Accountability 与记录管理。

来源：

- https://csrc.nist.gov/pubs/sp/800/92/final

**本页采用：**

- generation / approval / distribution / correction 产生 audit；
- report status 不从文件是否存在推断；
- artifact / audit / distribution records 有 retention owner；
- 不通过“最后修改时间”猜正式版本。

## 3.6 GHG Protocol — 报告方法、边界和修订不能被混成实时仪表盘

GHG Protocol corporate reporting framework长期强调 inventory boundary、base-year recalculation、方法一致性和报告原则；其标准修订工作也持续讨论 recalculation、data quality 与 reporting boundary。

来源：

- https://ghgprotocol.org/
- https://ghgprotocol.org/blog/ghg-protocol-newsletter-march-2026

**本页采用：**

- carbon report 绑定 inventory / methodology revision；
- reporting revision 不因 factor/library 更新而静默重算；
- correction/reissue 保留旧版；
- Report Center 不重新实现 carbon accounting。

---

# 4. 产品语言契约

主界面中文优先：

```text
报告中心
报告定义
报告周期
数据截止时间
数据完整性
生成记录
报告版本
审批状态
正式文件
分发状态
接收人
计划任务
更正
重新发布
来源追溯
```

保留必要专业词：

```text
Report Definition
Data Cutoff
Snapshot
Revision
Artifact
Reissue
PDF
XLSX
CSV
```

不要默认满屏：

```text
Dataset ID
Materialized View
Object Storage Key
ETag
Blob Version
Job Queue ID
```

这些进入 Advanced / Audit。

---

# 5. Report Domain Vocabulary

## Report Definition

定义“应该生成什么报告”的 versioned 模板/业务规范。

## Report Generation

使用明确 Report Definition Version、Scope、Period 和 Data Cutoff 执行一次正式生成。

## Report Revision

一份正式 Report Instance 的不可变内容版本。

## Artifact

Report Revision 产生的正式文件，例如 PDF/XLSX；Artifact 是内容载体，不是业务状态本身。

## Reporting Period

报告所覆盖的业务时间范围。

## Data Cutoff

本报告允许纳入数据的截止时点 / snapshot boundary。

## Source Snapshot

生成时冻结或引用的 source revisions / query snapshot / metric revision set。

## Reissue

当正式报告需要纠正、补充或重新发布时创建的新 Report Revision / issuance event。

## Distribution

向明确 recipient/channel 分发某个具体 Report Revision 的动作。

---

# 6. Mandatory Semantic Separation

```text
Report Definition ≠ Generated Report

Report Definition Version
≠ Report Revision

Reporting Period ≠ Data Cutoff
Generated At ≠ Reporting Period End

Live Dashboard ≠ Report Snapshot
Source Data Current State ≠ Report Source Snapshot

Generated ≠ Approved
Approved ≠ Published
Published ≠ Distributed
Distributed ≠ Delivered
Delivered ≠ Read

Artifact Exists ≠ Report Approved
PDF Generated ≠ Report Final

Correction ≠ Overwrite
Reissue ≠ Delete Old Revision
Superseded ≠ Invalid Historical Evidence

Source Updated ≠ Existing Report Updated

Estimated ≠ Actual
Allocated ≠ Measured
Derived ≠ Measured
Missing ≠ Zero

Report Completeness ≠ Data Quality

Report Status ≠ Delivery Status
Report Status ≠ Approval Status

Scheduled ≠ Generated
Generated ≠ Distributed

Download ≠ Distribution

Recipient ≠ Viewer Permission

Management Review Input ≠ Management Review Decision

Report Total ≠ Verified Savings automatically
```

---

# 7. Primary Questions

29 必须让用户回答：

1. 需要生成哪种报告；
2. Report Definition 当前版本是什么；
3. 作用范围是什么；
4. 报告期是什么；
5. 数据截止时间是什么；
6. 是否所有数据都已到齐；
7. 哪些数据仍估算/缺失/待修正；
8. 生成依据的 EnPI/EnB/M&V/Carbon/Tariff revision 是什么；
9. 是否允许生成 Draft；
10. 是否满足 Final/Approval readiness；
11. 谁需要批准；
12. 当前 Report Revision 是什么；
13. 是否已经正式发布；
14. 分发给谁；
15. 哪些人/渠道送达失败；
16. 底层数据后续修正是否影响报告；
17. 是否需要 Correction / Reissue；
18. 新旧 revision 差异是什么；
19. 报告是否被 30 Management Review 引用；
20. 哪个 artifact 才是历史正式版本。

---

# 8. Information Architecture

```text
Context Header
↓
Report Definition / Report Type Selector
↓
生成条件
  Scope
  Reporting Period
  Data Cutoff
  Completeness / Data Quality
↓
Report Ledger
                        → Report Inspector
↓
Selected Report
  Identity / Revision / Status
  Source Snapshot / Lineage
  Completeness
  Approval
  Artifact
  Distribution
↓
Correction / Reissue
↓
Schedule / Recipients
↓
Audit / History
```

默认是 **Ledger-first + report revision detail**，不是文件缩略图墙。

---

# 9. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/reports
/sites/:siteId/reports/:reportId
/sites/:siteId/report-definitions/:definitionId
```

Search Params 可以包括：

```text
type
status
period
scope
approval
revision
distribution
recipient
scheduled
```

不进入 URL：

```text
unsaved definition edit
local validation errors
modal confirmation
hover
```

Report ID / Revision 是 durable shareable context。

---

# 10. Capability Gating

某些 report type 只在对应 capability 存在时出现：

```text
Energy Performance
Demand
Efficiency
Billing / Cost
Carbon
DER
M&V
Alarm Performance
Work Management
Functional Verification
Management Review Pack
```

例如没有 Billing capability：

```text
不显示 Utility Cost Report
```

而不是显示一个永远为空的 disabled report type。

---

# 11. Report Type Contract

每个 Report Type 必须有明确业务 owner，不使用“万能模板”承载所有业务规则。

建议包括：

```text
能源月报
能源绩效报告
SEU / EnPI / EnB 报告
账单与成本报告
碳排放报告
M&V 节能量验证报告
告警绩效报告
运行与工单报告
功能验证报告
管理评审资料包
```

通用 layout 可以复用，业务 semantics 不能用 GenericReportConfig 代替。

---

# 12. Report Definition Contract

Report Definition 至少包含：

```text
Definition ID
中文名称
Report Type
Definition Version
Business Owner
Allowed Scope
Default Period Rule
Required Sections
Required Source Domains
Completeness Requirements
Approval Requirement
Allowed Artifact Formats
Distribution Policy
Retention Policy Reference
```

Definition 是 versioned object。

---

# 13. Definition Version Contract

修改以下内容通常产生新 Version：

```text
Section semantics
Metric definition
Required source
Calculation source
Completeness threshold owner contract
Approval requirement
Distribution requirement
Report wording with governance meaning
```

纯展示细节是否产生新 version，由 Report Governance owner 决定。

历史报告继续绑定原 Definition Version。

---

# 14. Report Instance Contract

每个 Report Instance 至少绑定：

```text
Report ID
Definition ID / Version
Site / Scope
Reporting Period
Current Revision
Governance State
Created By
Created At
```

一个 Report Instance 可以有多个 immutable revisions。

---

# 15. Reporting Period Contract

Reporting Period 是业务时间范围，例如：

```text
2026-08-01 00:00
→ 2026-08-31 23:59:59
Timezone: Asia/Shanghai
```

必须明确：

```text
Timezone
Calendar semantics
Inclusive/exclusive boundary
```

不能依浏览器 locale 猜月界限。

---

# 16. Data Cutoff Contract

Data Cutoff 表示：

> 生成这个 revision 时，允许进入报告的源数据/修订最晚截止到什么时候。

例如：

```text
Reporting Period End
2026-08-31 23:59

Data Cutoff
2026-09-05 18:00
```

这允许 late-arriving bill / meter corrections 在 cutoff 前进入。

```text
Reporting Period End
≠ Data Cutoff
```

---

# 17. Snapshot vs Live Contract

正式报告必须是：

```text
Snapshot / Frozen Reference Set
```

而不是：

```text
每次打开重新调用当前 API
```

生成后底层业务数据更新：

```text
Existing Revision remains unchanged
```

如果需要纳入更新：

```text
Create New Revision / Reissue
```

---

# 18. Source Snapshot Contract

Source Snapshot 至少需要可追溯：

```text
Source Domain
Source Object / Query
Revision / Version / Snapshot ID
As-of / Cutoff
Quality / Completeness
Owner
```

例如：

```text
EnPI v4
EnB v3
M&V Result MV-12 rev2
Carbon Inventory rev5
Tariff v8
Bill B-202608 rev2
```

不能只存最终数字而失去 lineage。

---

# 19. Source Lineage Contract

报告中的关键数值 / 表格 /结论需要能 drill back 到：

```text
14 Energy Analysis
15 Demand
16 Efficiency
17 Energy Review
18 Billing
19 Carbon
24 M&V
09 Alarm
11 Work Orders
13 Functional Verification
```

但 drill-back 打开的是权威业务 Surface，不是 report 内复制一个编辑器。

---

# 20. Completeness Contract

Report Completeness 必须由 Report owner 定义。

至少可以表达：

```text
Complete
Complete with Estimated Data
Incomplete
Blocked
Unknown
```

还应能说明：

```text
missing sources
late sources
estimated sections
coverage
unresolved data issue
```

不能只有一个 `Completeness 92%` 黑盒分数。

---

# 21. Completeness vs Data Quality

```text
Completeness
= 所需数据是否齐全

Data Quality
= 数据是否可信/有效
```

例如：

```text
Coverage 100%
但 meter quality suspect
```

可以是：

```text
Complete
Data Quality Issue
```

两者不能合并。

---

# 22. Estimated / Actual Contract

报告必须保留上游数据性质：

```text
Actual
Measured
Estimated
Allocated
Derived
Corrected
Missing
Unknown
```

不能因为报告需要“好看”而把 Estimated 统一隐藏。

---

# 23. Missing Data Contract

```text
Missing ≠ 0
```

如果关键 source 缺失：

- Draft 是否允许生成由 Report Definition 决定；
- Final 是否允许审批由 governance rule 决定；
- 页面必须显示影响 section；
- 不允许前端临时补零/last value/previous period。

---

# 24. Generation Request Contract

生成动作至少包含：

```text
Definition ID / Version
Scope
Reporting Period
Data Cutoff
Requested Formats
Requested By
Reason / Schedule Reference
```

生成请求产生 durable `Generation ID`。

---

# 25. Generation Lifecycle Contract

建议：

```text
Requested
Queued
Collecting Sources
Validating Sources
Rendering
Artifact Generation
Completed
Completed with Warnings
Blocked
Failed
Cancelled
State Unknown
```

Generation Completed 不等于 Approved / Published。

---

# 26. Generation Failure Contract

失败需要明确：

```text
Source unavailable
Source incomplete
Definition invalid
Render failure
Artifact storage failure
Permission / scope issue
Unknown
```

不能：

```text
any error → empty report
```

也不能“谁成功用谁”从多套 source fallback。

---

# 27. No Automatic High-level Retry

如果生成动作已产生 partial artifacts / source snapshot：

```text
Generation timeout
≠ automatically start another independent generation
```

应先按 `Generation ID` 查询权威状态。

纯 rendering owner 可以有内部 retry，但需要 idempotent / owner-defined policy。

---

# 28. Report Revision Contract

每个 Report Revision 是 immutable record：

```text
Report ID
Revision
Definition Version
Source Snapshot Set
Reporting Period
Data Cutoff
Generated At
Generated By
Content Hash / Artifact Hash reference
Approval State
Publication State
Supersedes（若有）
Reason
```

Approved / Published revision 不允许原地改内容。

---

# 29. Draft Revision Contract

Draft 可以：

```text
review
annotate
request correction
```

如果允许人工 narrative，必须明确：

```text
Data-derived sections
Manual commentary sections
```

人工文字不能覆盖/修改权威计算事实。

---

# 30. Final / Official Contract

正式报告不使用模糊 `Final=true` 即完事。

至少区分：

```text
Draft
Ready for Review
In Review
Approved
Published
Superseded
Withdrawn
```

是否需要 Approval 由 Report Definition 决定。

---

# 31. Approval Contract

Approval 绑定：

```text
Report ID
Revision
Content / Artifact Hash
Definition Version
Approver
Decision
Conditions（若支持）
Approved At
```

Approval 不绑定一个会变化的“latest report”。

---

# 32. Approval ≠ Publish

```text
Approved
≠ Published
```

Approval 表示内容治理通过。

Publish 表示该 revision 成为正式可分发版本。

这两个生命周期动作分别审计。

---

# 33. Artifact Contract

一个 Report Revision 可以产生：

```text
PDF
XLSX
CSV appendix
Machine-readable JSON（若业务需要）
```

Artifact 必须绑定同一 Report Revision。

不同格式不能使用不同 source snapshot 重新计算。

---

# 34. Artifact Integrity Contract

至少保留：

```text
Artifact ID
Report ID / Revision
Format
Generated At
Hash / Integrity Reference
Size
Retention State
```

Artifact hash / integrity 由 storage/report owner 管理。

前端不自己计算并作为审计真相。

---

# 35. Artifact ≠ Report State

```text
PDF exists
≠ Approved

PDF downloaded
≠ Distributed
```

Artifact 只是 revision 的载体。

---

# 36. Report Ledger Contract

默认列：

```text
报告
范围
报告周期
Revision
数据截止时间
完整性
审批状态
发布状态
分发状态
生成时间
```

必要时显示：

```text
Correction / Reissue
Warnings
```

---

# 37. Default Prioritization

默认优先暴露：

```text
Blocked generation
Incomplete final report
Correction required
Approval overdue
Reissue required
Distribution failure
Scheduled report overdue
Recently published
```

而不是纯 `generatedAt DESC`。

---

# 38. Report Inspector Contract

快速显示：

```text
Report ID
Definition / Version
Scope
Reporting Period
Data Cutoff
Revision
Completeness
Data Quality warnings
Approval
Artifact
Distribution
Correction/Reissue state
```

然后进入完整 detail。

---

# 39. Report Detail Contract

完整 Detail 包括：

```text
Identity
Revision
Source Snapshot / Lineage
Section Summary
Completeness
Quality Warnings
Approval
Artifacts
Distribution
Correction / Reissue
History / Audit
```

不做一个只显示 PDF iframe 的页面。

---

# 40. Drill-back Contract

报告中关键指标可带：

```text
View Source
```

例如：

```text
Verified Savings 205 MWh
→ 24 M&V MV-12 rev2
```

或：

```text
Scope 2 Market-based
→ 19 Carbon Inventory rev5
```

Drill-back 不能让报告直接修改 source。

---

# 41. Schedule Contract

周期报告支持：

```text
Schedule ID
Definition / Version policy
Scope
Period rule
Cutoff rule
Generation time
Timezone
Recipient set
Approval workflow
```

例如：

```text
每月第 5 个工作日 09:00
生成上月能源月报
Data Cutoff = generation time - 1h
```

“第 5 个工作日”必须由正式 calendar owner 解析，不由前端硬编码。

---

# 42. Schedule Definition Version Policy

必须明确 scheduled report 使用：

```text
Pinned Definition Version
```

或：

```text
Current Approved Definition Version
```

不能模糊。

如果 Definition 升级，Schedule owner 决定后续周期是否切换。

历史报告不受影响。

---

# 43. Scheduled ≠ Generated

```text
Schedule due
≠ report generated
```

状态应区分：

```text
Scheduled
Generation Requested
Generated
Blocked
Missed
Cancelled
```

不能因为时间到了就显示“报告已生成”。

---

# 44. Recipient Contract

Recipient 可以是：

```text
User
Role / Group
Approved External Recipient
Distribution List
System Integration
```

每个 recipient 必须有权限/数据敏感性检查。

不能把一个 Site report 发给无 site scope 的 recipient。

---

# 45. Distribution Contract

每次分发至少绑定：

```text
Distribution ID
Report ID / Revision
Artifact ID
Recipient
Channel
Requested At
Sent At
Delivery State
Owner
```

Distribution 只针对明确 Revision。

---

# 46. Distribution State Contract

建议：

```text
Pending
Sent
Delivered（渠道支持时）
Bounced / Failed
Cancelled
Unknown
```

如果渠道不支持 read receipt：

```text
Read State unavailable
```

不能 `Sent → Read`。

---

# 47. Download ≠ Distribution

用户手动下载：

```text
Download Event
```

不等于：

```text
Official Distribution
```

正式分发必须走 Distribution owner，以便 audit recipient/revision/channel。

---

# 48. External Distribution Boundary

对外部 recipient：

- 检查数据分级；
- 检查 site/customer boundary；
- 检查 artifact format；
- 检查 approval state；
- 检查 external-sharing policy。

前端隐藏按钮不是 security boundary。

---

# 49. Correction Trigger Contract

以下情况可能触发 correction review：

```text
Source data corrected after cutoff
Bill rebill
Meter correction
Carbon factor correction
M&V result revision
EnPI/EnB governance correction
Report narrative error
Recipient metadata error
```

是否需要 reissue 由 Report Governance owner 决定。

---

# 50. Source Correction Impact Contract

如果源数据在报告发布后变化：

```text
Source Revision Changed
↓
Impact Assessment
↓
No Impact / Material Impact / Unknown
↓
Correction Decision
```

不能直接重新生成并覆盖。

---

# 51. Materiality Contract

是否“重大影响 / 需要更正”由对应 Report / Compliance owner 定义。

前端不硬编码：

```text
variance > 2% → reissue
```

不同 report type 可以有不同 materiality policy。

---

# 52. Correction Contract

Correction 必须记录：

```text
Reason
Affected Revision
Affected Sections
Source Changes
Requested By
Reviewed By
Decision
```

Correction 可以：

```text
No Reissue Required
Reissue Required
Withdraw Required
```

---

# 53. Reissue Contract

Reissue 产生新 Revision：

```text
Report RPT-202608 rev1
Published
↓ correction
Report RPT-202608 rev2
Reissued
```

rev1 保留，并标：

```text
Superseded by rev2
```

不能删除 rev1。

---

# 54. Reissue Diff Contract

Reissue 必须可解释：

```text
哪些 section 变了
哪些指标变了
为什么变
Source revision 从什么变成什么
```

例如：

```text
电费总额
$81,694 → $79,880
原因：Utility corrected bill rev2
```

---

# 55. Reissue Distribution Contract

Reissued revision 是否自动重新分发，由 Distribution policy 明确定义。

默认不在前端静默重发。

应显示：

```text
Previous recipients
Reissue distribution required
```

然后由 owner workflow 执行。

---

# 56. Withdrawal Contract

如果报告需要撤回：

```text
Withdrawn
```

仍保留 revision 和 withdrawal reason。

不能 physically delete 作为业务撤回方式。

---

# 57. Management Review Pack Contract

管理评审资料包可以引用：

```text
17 Energy Review
23 Objectives / Action Plans
24 M&V
09 Alarm Performance
11 Work Orders
31 Data Quality
32 Semantic / Meter governance（若适用）
```

29 负责生成 pack artifact；
30 负责 review/decision。

```text
Management Review Pack
≠ Management Review Decision
```

---

# 58. Management Review Snapshot Integrity

如果 30 管理评审在 2026-09-10 使用：

```text
Management Pack rev3
```

后续 29 生成 rev4：

```text
rev3 remains the evidence used in that review
```

不能让 review history 自动改成 rev4。

---

# 59. Live Preview Contract

允许在生成前显示：

```text
Preview
```

但必须标识：

```text
Live Preview / Not a Generated Report
```

Preview 不能拥有正式 Report Revision / Approval / Distribution semantics。

---

# 60. Preview ≠ Generated Report

```text
Preview looks complete
≠ generated record
```

真正报告必须经过 Generation owner 创建 source snapshot 与 revision。

---

# 61. Report Section Contract

每个 section 应知道：

```text
Section ID
Business Meaning
Source Domain
Source Revision
Completeness
Data Quality
Rendered At
```

Report Detail 可以显示这些 lineage，但主阅读视图不需要暴露所有内部字段。

---

# 62. Narrative Contract

报告可以包含人工 narrative：

```text
本期重点
管理说明
异常解释
下一步行动
```

必须区分：

```text
System-generated facts
Human-authored commentary
```

人工文字不能静默修改权威数字。

---

# 63. AI Assistance Boundary

AI 可以：

```text
根据正式 source facts 草拟摘要
总结变化
生成 executive summary draft
提示 source inconsistency
提示缺失说明
帮助比较 revision diff
```

AI 不能：

```text
自行补缺失数据
自行生成 verified savings
自行修改 carbon inventory
自行批准报告
自行分发外部 recipient
把估算改成 actual
把 source conflict 隐藏
```

AI narrative 必须明确 Draft / Review required。

---

# 64. Localization / Units Contract

报告必须明确：

```text
Locale
Timezone
Currency
Units
Decimal / rounding policy
```

这些由 Report Definition / business owner 决定。

前端不依浏览器 locale 随机改变正式 artifact 中的小数、日期和币种格式。

---

# 65. Rounding Contract

正式报告中的 rounding 由 metric/report owner 定义。

不能：

```text
UI 2 decimals
PDF 1 decimal
Excel raw
```

造成正式数字不一致而没有说明。

必要时 Excel 可以附 raw precision，但 displayed result / total 的方法要明确。

---

# 66. Timezone / DST Contract

Reporting Period 与 interval summaries 必须绑定 site/report timezone。

DST、跨时区和 billing period semantics 由 source/report owner处理。

前端不能按本地浏览器 timezone 重算正式 monthly total。

---

# 67. File Naming Contract

Artifact 文件名可以是用户友好的，例如：

```text
中央园区_能源月报_2026-08_rev2.pdf
```

但业务身份仍由：

```text
Report ID + Revision + Artifact ID
```

决定。

文件名不是身份主键。

---

# 68. Retention Contract

Retention 由 Report/Audit governance owner 定义，例如：

```text
Report revision
Artifact
Distribution record
Approval record
Source snapshot metadata
```

可以有不同 retention policy。

UI 不自行删除 old reports。

---

# 69. Archive Contract

```text
Archived
≠ Deleted
```

Archived report 仍保留：

```text
identity
revision
artifact（若 retention 允许）
approval
distribution
audit
```

---

# 70. Security Boundary Contract

Backend 每次关键操作验证：

```text
Principal
Site Scope
Report Type
Definition permission
Source-data permission
Generate permission
Approve permission
Publish permission
External distribution permission
Recipient scope
```

“能看 Dashboard”不自动意味着“能生成或分发正式报告”。

---

# 71. Sensitive Content Contract

某些 report section 可能包含：

```text
人员姓名
成本
合同
告警/安全信息
OT identifiers
Carbon / compliance data
```

不同 artifact / recipient 可以受到 data classification policy 约束。

不能只依赖前端隐藏列。

---

# 72. Audit Contract

至少审计：

```text
Definition created/changed
Generation requested
Generation completed/failed
Revision created
Approval requested/decided
Published
Artifact generated
Distributed
Distribution failed
Correction opened/decided
Reissued
Withdrawn
Archived
```

每条包括：

```text
Who
When
Report / Revision
Before / After
Reason
Reference
```

---

# 73. Query / Read Model Contract

Ledger 使用服务端 report summary read model。

禁止：

```text
100 reports
→ 100 approval queries
→ 100 recipient queries
→ 100 artifact queries
→ 100 completeness queries
```

Summary 至少包含：

```text
identity
period
revision
completeness
approval
publication
distribution
warnings
```

Source lineage/detail 再按需 lazy-load。

---

# 74. Generation Backend Boundary

页面不在浏览器里组装“正式 PDF 数据集”。

正式流程：

```text
UI
→ Report Generation API
→ Source Snapshot / Report Engine
→ Artifact Store
```

前端可以做 Preview，但正式 report truth 由 backend owner 产生。

---

# 75. No Frontend Recalculation Contract

禁止：

```text
fetch raw meters
→ frontend sum
→ put into official report
```

也禁止：

```text
fetch M&V raw data
→ frontend calculate verified savings
```

报告只消费对应 domain owner 的正式结果。

---

# 76. Concurrency Contract

若两人同时基于 rev1 发起 correction：

- correction owner 处理 revision conflict；
- 不 silent last-write-wins；
- 需要明确当前 authoritative revision；
- 新 reissue 基于明确 parent revision。

---

# 77. Schedule Concurrency Contract

同一 schedule period 不应因 worker race 创建两个独立“正式报告”而无人知道。

Duplicate prevention / idempotency 属于 Report Generation owner。

前端不根据：

```text
same month + same report name
```

自行删除“重复报告”。

---

# 78. Report Correction vs Source Correction

```text
Source correction
≠ Report correction automatically
```

Source 修订后需要影响评估。

Report correction 可能还包括：

```text
Narrative typo
Recipient metadata
Artifact formatting
```

二者 relation 必须明确。

---

# 79. Report Revision vs Definition Version

```text
Definition v4
→ Aug Report rev1
→ Aug Report rev2 (source correction)
```

rev2 可以仍然使用 Definition v4。

如果 Definition 变成 v5：

```text
Sep Report may use v5
```

不要把 Report Revision 和 Definition Version 混成一个 `version` 字段。

---

# 80. Generated Data vs Published Data

允许：

```text
Generated Draft
```

但正式对外/管理使用通常是：

```text
Published Revision
```

页面必须让用户看出自己正在阅读 Draft 还是正式版本。

---

# 81. Data Freshness Disclosure

即使 snapshot 完整，也应能说明：

```text
Data Cutoff
Latest source arrival
Late data known / unknown
```

不能因为报告已经生成，就暗示后续没有更晚的数据。

---

# 82. Artifact Preview Contract

PDF/XLSX preview 是辅助。

主业务事实仍由 Report Detail 表达：

```text
revision
source lineage
approval
distribution
correction
```

不能让用户只有一个 PDF iframe，无从判断正式性。

---

# 83. Distribution Schedule Contract

可以定义：

```text
Generate first
Require approval
Then distribute
```

或：

```text
Generate and auto-distribute approved report type
```

具体流程由 Definition policy 决定。

前端不默认“生成即发送”。

---

# 84. Delivery Failure Contract

Distribution failure：

```text
Report Published
Distribution Failed
```

是合法组合。

不能把 delivery failure 改写成 report generation failure。

---

# 85. Recipient Change Contract

Recipient set 变更不应改写已分发 revision 历史。

例如：

```text
rev2 distributed to A/B on Sep 5
```

Sep 10 recipient list 加 C：

- 旧 distribution history 不变；
- 是否补发 C 产生新 Distribution event。

---

# 86. Report Comparison Contract

可以比较：

```text
Current report vs previous period
rev1 vs rev2
```

但必须明确比较类型。

```text
Period Comparison
≠ Revision Diff
```

Revision Diff 解释同一报告为什么更正；Period Comparison 解释业务变化。

---

# 87. Revision Diff Contract

Revision Diff 优先显示：

```text
Changed metrics
Changed sections
Changed source revisions
Changed commentary
Changed artifact metadata
Reason
```

不是只做 binary file diff。

---

# 88. Report Status Model

建议治理状态：

```text
Draft
Ready for Review
In Review
Approved
Published
Superseded
Withdrawn
Archived
```

独立维度：

```text
Generation State
Completeness State
Approval State
Publication State
Distribution State
Correction State
```

不要压成一个万能 enum。

---

# 89. Report Completeness Gate

Report Definition 可以要求：

```text
critical sections complete
required data quality accepted
required owner results final
```

如果不满足：

```text
Blocked from Final / Approval
```

是否允许 Draft generation 是独立配置。

---

# 90. Official Report Label Contract

只有满足 owner policy 的 revision 才显示：

```text
正式报告
```

Draft/Preview 不使用类似视觉，以免用户误转发。

---

# 91. Browser Acceptance Criteria

## Historical Integrity

- 已生成 revision 不因 live data 更新而改变；
- source correction 通过 impact + correction/reissue；
- reissue 保留旧 revision；
- management review 引用固定 revision；
- Definition Version 与 Report Revision 分离。

## Period / Snapshot

- Reporting Period 与 Data Cutoff 同时可见；
- timezone 明确；
- source snapshot 可追溯；
- live preview 明确不是正式报告。

## Completeness / Quality

- Missing 不显示 0；
- Estimated 不冒充 Actual；
- Completeness 与 Data Quality 分开；
- blocked final 有明确原因。

## Approval / Publication

- Generated 不等于 Approved；
- Approved 不等于 Published；
- Published 不等于 Distributed；
- approval 绑定 revision/hash；
- Published revision 不原地修改。

## Artifact

- Artifact 与 Report Revision 绑定；
- PDF exists 不等于 Approved；
- 不同格式使用同一 source snapshot；
- artifact integrity reference 可追溯。

## Distribution

- recipient / revision / channel 可追溯；
- Sent / Delivered / Read 分开；
- download 不等于 official distribution；
- external distribution 受 backend permission/data-classification 控制。

## Correction / Reissue

- materiality 不由前端硬编码；
- revision diff 可解释；
- correction 不覆盖历史；
- reissue 不静默重发。

## Accessibility / Responsive

- Ledger semantic table；
- status 不只靠颜色；
- keyboard 可完成查看/筛选/审批入口；
- artifact preview 有文本/结构化替代；
- 768px 下保留 Report、Period、Revision、Completeness、Approval、Distribution；
- 无 page-level 横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无前端 authoritative report calculation；
- 无 old export page compatibility adapter；
- 无“latest live data”重算历史 report；
- 无 N+1 report ledger queries；
- review scenario 无 runtime/network error。

---

# 92. No Defensive Programming / No Compatibility Design

明确禁止：

```text
report API error → []

definition unavailable
→ use previous definition silently

reporting period missing
→ current month

data cutoff missing
→ now

missing source
→ 0

missing source
→ previous period

estimated
→ actual

incomplete
→ complete

quality unknown
→ good

source revision unavailable
→ current source

published report open
→ rerun live query

source corrected
→ overwrite report

reissue
→ delete old revision

generated
→ approved

approved
→ published

published
→ distributed

sent
→ delivered

delivered
→ read

artifact exists
→ final report

PDF generated
→ official

manual download
→ distribution success

correction variance > hardcoded %
→ auto reissue

schedule due
→ generated

generation timeout
→ start second generation blindly

recipient added
→ rewrite historical distribution

same report name + period
→ duplicate, delete one

frontend meter sum
→ official report metric

frontend baseline - actual
→ verified savings report

AI summary
→ approved narrative

multiple report APIs
→ first success wins

old Export Center fallback
legacy Report compatibility adapter
```

正式原则：

> **一个正式 Report Revision 对应一个明确 Definition Version、Reporting Period、Data Cutoff 和 Source Snapshot。Generated、Approved、Published、Distributed 是不同阶段。底层数据更新不能静默改写历史报告；Correction 通过新 Revision / Reissue 表达；Missing 保持 Missing，Unknown 保持 Unknown。**

---

# 93. Accessibility Contract

- Report Ledger 使用 semantic table；
- Revision/Approval/Distribution 状态有文字；
- PDF preview 不能是唯一阅读方式；
- keyboard 可选择 report、打开 inspector、查看 revision history；
- correction diff 不只靠红绿；
- live status update 不抢 focus；
- 768px 下保持核心治理事实；
- 无 page-level 横向 overflow。

---

# 94. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 报告中心 · 中央园区                                      [生成报告]        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 需更正 1   待审批 2   分发失败 1   本月已发布 8                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ 报告                周期          Rev  完整性      审批      分发            │
│ 能源月报            2026-08       2    完整        已批准    已发送          │
│ 碳排放月报          2026-08       1    有估算      待审批    —               │
│ M&V 节能量报告      2026-Q3       1    不完整      阻塞      —               │
│ 告警绩效月报        2026-08       1    完整        不要求    失败 1          │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中：能源月报 · 2026-08 · rev2             │ 快速判断                     │
│ Definition：能源月报 v5                      │ 报告周期  08/01–08/31       │
│ Data Cutoff：2026-09-05 18:00               │ 数据完整性 完整              │
│ Generated：2026-09-05 18:14                  │ 审批 已批准                  │
│ Source：EnPI v4 / EnB v3 / Billing rev2      │ 发布 已发布                  │
│ Reissue reason：Utility corrected bill       │ 分发 8/8 已发送              │
├──────────────────────────────────────────────────────────────────────────────┤
│ Revision Diff                                                                 │
│ rev1 → rev2                                                                  │
│ 电费总额：$81,694 → $79,880                                                   │
│ 来源：Bill B-202608 rev1 → rev2                                              │
│ 原因：Utility corrected bill                                                 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 正式文件                                                                     │
│ [查看 PDF] [下载 XLSX] [查看来源追溯]                                        │
├──────────────────────────────────────────────────────────────────────────────┤
│ 分发                                                                         │
│ 能源经理     Sent     09/05 18:22                                            │
│ 园区经理     Sent     09/05 18:22                                            │
│ 管理评审组   Sent     09/05 18:23                                            │
├──────────────────────────────────────────────────────────────────────────────┤
│ 历史：rev1 已被 rev2 取代，但仍可审计                                         │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任和信息层级，不是像素规范。

---

# 95. Explicit Non-goals

29 不是：

- 通用 BI designer；
- Excel replacement；
- Dashboard screenshot service；
- live dashboard；
- carbon calculation engine；
- M&V calculation engine；
- billing engine；
- management review decision page；
- document file manager；
- marketing email system；
- arbitrary SQL report builder。

---

# 96. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Report Definition / Version 明确；
- Report Instance / Revision 明确；
- Reporting Period / Data Cutoff 分离；
- Snapshot vs Live 明确；
- Source Snapshot / Lineage 明确；
- Completeness / Data Quality 分离；
- Estimated / Actual / Missing 明确；
- Generation lifecycle 明确；
- Approval / Publish 分离；
- Artifact contract 明确；
- Schedule / Recipient / Distribution 明确；
- Correction / Reissue / Withdrawal 明确；
- Management Review pack boundary 明确；
- Historical Integrity 明确；
- Retention / Security / Audit 明确；
- AI boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
