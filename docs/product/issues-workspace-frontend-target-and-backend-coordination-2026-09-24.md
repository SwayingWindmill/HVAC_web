# Issues Workspace 前端目标态与后端配合要求

日期：2026-09-24  
状态：SELECTED / FRONTEND TARGET  
范围：/sites/$siteId/issues  
边界：本文只定义前端产品目标与后端配合要求，不授权或实施任何后端修改。

## 1. 产品定位

“告警与诊断”不是两个平级页面，也不是一张 Alarm 日志表。正式工作模型是：

~~~text
Issue Operations
├─ 问题处置：现在需要处理什么
└─ 告警分析：为什么告警多、哪里最慢、哪些问题反复发生

selected Alarm / Issue
↓
OperationalDetailSheet
├─ Alarm facts
├─ Published Finding
├─ Impact
├─ Hypotheses
├─ Evidence
├─ Root Cause Decision
├─ Verification
├─ Related Issues
├─ Lifecycle / handling timeline
└─ Work-order / trend / device / data-quality exits
~~~

Alarm 是异常事件入口；Finding 是规则或模型发布的诊断结果；Hypothesis 是调查中的可证伪解释；Root Cause 是经过证据闭环后明确确认的结论。四者不得合并成一个“AI 诊断结果”。

## 2. 参考产品与行业方向

### 2.1 Alarm Operations

参考 ThingsBoard Alarm Table / lifecycle、Grafana alert history、SigNoz Triggered Alerts 以及 ISA-18 系列。成熟模式共同强调：物理状态、确认、责任、筛选、处置和审计分离；Alarm Table 是工作队列，不只是日志；Alarm Performance 需要持续监测 alarm rate、standing alarms、operator response 和 alarm flood。

对应前端决策：

- “问题处置”与“告警分析”是两种任务模式，可以使用 Tabs；
- “当前活动 / 全部记录 / 已搁置”只是同一 Ledger 的范围筛选，不作为 Tabs；
- Physical condition、ACK、assignee、suppression、diagnosis 分列，不合并成黑盒 status；
- Alarm Performance 必须回答操作负荷与规则质量问题，而不是只有数量趋势。

### 2.2 HVAC / FDD

参考 Clockworks Analytics 与 Siemens Building X。成熟 FDD 的方向是从“发现异常”转向“解释、排序、验证、修复并证明结果”。

正式调查链：

~~~text
Finding
↓
Hypotheses
↓
evidence supports / contradicts
↓
next verification
↓
Root Cause Decision
↓
work / correction
↓
verification
~~~

前端禁止把 Finding confidence 显示为“根因概率”。

### 2.3 智慧能源管理方向

DOE Grid-interactive Efficient Buildings / EMIS 的方向是把 Efficiency、Connected Data、Smart Controls、Demand Flexibility、Energy Cost、Occupant Needs 与 Grid Interaction 共同考虑。

因此 Issue 的“影响”不能只用 severity。目标态需要同时表达：

- avoidable energy；
- avoidable cost；
- comfort impact；
- reliability risk；
- 后续可扩展 demand / grid-event risk 与 carbon impact。

这些值必须来自后端权威分析或明确测算模型，前端不得按告警次数或 severity 自行推导。

## 3. 页面信息架构

### 3.1 顶层任务模式

只保留：

~~~text
[问题处置] [告警分析]
~~~

问题处置面向实时/近期 issue queue，目标是确认、建立责任、调查并闭环。告警分析面向站点级分析，目标是发现 alarm-system 与 maintenance process 的系统性问题。

### 3.2 问题处置

布局：

~~~text
Site context / refresh
↓
FactStrip
  活动告警 | 未确认 | 未指派 | 已搁置
↓
DataTable toolbar
  范围 | Search | Severity | ACK | Owner | Source | View | Export
↓
Issue Ledger
↓
row selection
↓
OperationalDetailSheet
~~~

范围只有当前活动、全部记录、已搁置。范围是 Filter，不是 peer Tabs。

Ledger 默认字段：

- 等级；
- 告警 / 来源对象；
- 物理状态；
- 确认；
- 诊断；
- 负责人；
- 持续；
- 重复；
- 搁置；
- 最近变化。

后续可以加入 authoritative business impact，但低频字段应由 View 控制，避免把 Ledger 变成无限横向扩张的 spreadsheet。

## 4. Investigation Sheet

Sheet 是快速调查上下文，不是第二个页面模板。

### 4.1 当前问题

必须显示 severity、physical condition、ACK、diagnosis state、suppression、title/device/location、first occurred、duration、repeat、owner 与 current summary。

### 4.2 Published Finding

必须保留 finding type、evaluation window、evidence references、rule/model revision、result score/confidence 和 quality blocker。

### 4.3 Impact

前端目标字段：

- avoidableEnergyKwh；
- avoidableCost + currency；
- comfortImpactHours；
- reliabilityRisk = LOW / MEDIUM / HIGH / CRITICAL；
- estimatedFrom / estimatedTo。

Impact 用于“先处理什么”，不替代 Alarm Severity。

### 4.4 Hypothesis

前端目标字段：

- id / title；
- status = CANDIDATE / SUPPORTED / WEAKENED / REJECTED / CONFIRMED；
- rationale；
- supportingEvidenceCount；
- contradictingEvidenceCount；
- nextVerification。

Hypothesis 必须可证伪，同时显示支持证据和反证，并且能落到下一验证动作；不允许只有“AI 建议”。

### 4.5 Root Cause

前端目标字段：

- status = UNCONFIRMED / CONFIRMED；
- title / rationale；
- evidenceIds；
- confirmedAt / confirmedBy。

只有明确 CONFIRMED 决策才允许 UI 显示“根因已确认”。

### 4.6 Verification

前端目标字段：

- id / title / method；
- status = PENDING / IN_PROGRESS / PASSED / FAILED；
- owner / dueAt。

这是 diagnosis 到 closed-loop operations 的关键连接。

### 4.7 Related Issues

目标关系：SAME_INCIDENT、SAME_EQUIPMENT、UPSTREAM、DOWNSTREAM、RECURRING_PATTERN。目的是避免操作员逐条孤立处理同一系统故障产生的 Alarm Flood。

## 5. 告警分析

支持 7d / 30d / 90d 周期。

第一层回答响应效率：

- total triggered；
- active at period end；
- acknowledgement rate；
- diagnosis coverage；
- average acknowledge time；
- average resolution time。

第二层回答 alarm quality：

- repeat issue rate；
- Alarm Flood windows；
- standing/stale alarms（后端补齐后加入）；
- chattering / nuisance alarms（后端补齐后加入）；
- severity distribution（后端补齐后加入）。

第三层回答 HVAC / Energy impact：

- avoidable energy；
- avoidable cost；
- comfort impact；
- reliability impact。

第四层是分析视图：

- weekday × time Heatmap；
- Top contributing equipment；
- recurring issue classes；
- 后续可增加 rule bad actors / source distribution / Pareto。

## 6. 前端当前目标合同

前端已经定义以下读取目标：

~~~text
GET /api/v1/sites/{siteId}/issues/{alarmId}/investigation

GET /api/v1/sites/{siteId}/issues/performance
    ?period=7d|30d|90d
~~~

当前生产后端若返回 404 / 501：

- Investigation 保留 Finding + fallback next-verification；
- Performance 显示明确“数据尚未接入”；
- 不使用前端推算制造生产事实。

Frontend Review fixture 只用于设计和浏览器验收，不得解释为真实站点数据。

## 7. 后端配合要求

本节是需求说明，不实施后端代码。

### 7.1 Investigation projection

后端需要提供面向 UI 的 issue investigation projection，聚合但不夺取各 owner 的事实所有权。至少提供 alarmId、investigation status、impact、hypotheses、rootCause decision、verification steps、related issues、updatedAt，并建议保留来源标识与 revision。

### 7.2 Performance aggregation

每项指标必须由后端固定统计语义。例如：

~~~text
MTTA = PUBLISH occurredAt → first ACKNOWLEDGE occurredAt
MTTR = PUBLISH occurredAt → CLEAR occurredAt
~~~

后端还需要定义：

- 一个 Alarm occurrence 与 repeated trigger 的计数边界；
- Flood window 阈值、窗口长度与 scope；
- Standing / stale 定义；
- Chattering / nuisance 判断；
- Recurring Pattern 的关联方法；
- diagnosis coverage 的 denominator；
- impact 的模型、费率版本、估算窗口与 provenance。

这些定义不能散落在前端。

### 7.3 Bulk handling

成熟 Alarm Table 的目标交互：

~~~text
row selection
↓
bottom ActionBar
  批量确认
  批量指派
  批量搁置
~~~

后端后续应提供真正的 bulk command，而不是前端 N 次循环调用单条 mutation。建议能力：

~~~text
POST /api/v1/sites/{siteId}/alarms/bulk/acknowledge
POST /api/v1/sites/{siteId}/alarms/bulk/assign
POST /api/v1/sites/{siteId}/alarms/bulk/suppress
~~~

每次 bulk operation 应返回逐项 result，并支持 partial failure、idempotency、authorization 和 audit。前端当前不伪造 bulk success；批量 ActionBar 在 bulk contract 接入时启用。

## 8. Registry / source decisions

ADOPT：

- dashboardcn Timeline → Alarm lifecycle / handling chronology；
- OperationalDetailSheet → quick investigation container，前提是继续满足 desktop non-modal/no-reflow 与 narrow modal；
- tablecn + project DataTable → Issue Ledger。

ADAPT：

- dashboardcn Heatmap Chart → Alarm weekday × time density；
- dashboardcn Bar List → Top contributors / recurring issues。

Heatmap 与 Bar List 不直接 shadcn add，因为 dry-run 会覆盖项目已有 Tooltip / format 并加入不必要依赖；只复制成熟 interaction grammar，并适配现有 primitives、tokens 与 localization。

REVIEW NEXT：

- Dice UI Action Bar → bulk selection actions；
- tablecn advanced filter grammar → 在 server filter ownership 不改变的情况下进一步收敛 Filter UI。

## 9. Acceptance rules

- Alarm 与 Diagnosis 不成为两个 peer Workspace tabs；
- active/history/suppressed 不作为 Tabs；
- Issue Ledger 不包在 Card 中；
- desktop Detail Sheet 打开时 Ledger 不 reflow；
- Published Finding 不等同于 Root Cause；
- Hypothesis 同时允许 supporting 与 contradicting evidence；
- Impact 不从 frontend severity / occurrence count 推导；
- Performance 使用真实 aggregation contract；
- Heatmap / BarList 必须来自 authoritative performance data；
- narrow Sheet 无页面级横向溢出；
- 后端未接入目标 contract 时显示明确 unavailable state，不伪造数据。

## 10. 参考方向

- ThingsBoard Alarm Management / Alarm Table
- Grafana Alerting state and history
- SigNoz Triggered Alerts history
- ISA-18 Series / ISA-TR18.2.5 alarm performance monitoring
- Siemens Building X Operations Manager / Rules
- Clockworks Analytics FDD workflow and impact prioritization
- U.S. DOE Grid-interactive Efficient Buildings / EMIS capabilities

这些来源用于产品与交互校准；最终领域语义仍以 HVAC Surface Spec 与正式 owner contract 为准。
