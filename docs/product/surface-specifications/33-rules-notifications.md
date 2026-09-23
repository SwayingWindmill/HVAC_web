# 33 规则与通知 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `33 规则与通知`  
> **Route intent：** `/settings/rules`、`/settings/rules/:ruleId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`ISA-18.2`、`IEC 62682`、`BACnet`、`CloudEvents` 等标准术语保留为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目旧规则页、旧通知中心、旧告警配置页或旧 Ant/ProComponents 页面。现有实现只能在实施阶段作为真实 Rule / Event / Notification / Permission contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **管理检测规则和通知策略的定义、版本、测试、发布、路由、抑制与审计，而不是在本页处置实时告警。**

33 是 **Rule Governance + Notification Policy Workspace**，不是：

- 09 Alarm Center 的替代；
- 在线告警处置台；
- FDD Root Cause 页面；
- 通用 workflow engine；
- 控制 interlock / safety logic editor；
- “触发条件 + 发短信”两列表单；
- 在 React 中执行规则的前端表达式引擎；
- 一个 `triggered=true` 就代表事件、告警、通知和用户响应全部完成的黑盒系统。

用户离开本页前应该能回答：

1. 这个 Rule 在监测什么事实；
2. Scope 是哪些 Site / System / Asset / Point / Meter；
3. 输入数据需要什么 Unit、Quality、Freshness 与时间对齐条件；
4. Trigger Condition、Evaluation Window、Delay、Hysteresis、Recovery 分别是什么；
5. 条件满足后产生什么 **Domain Output**；
6. 这个输出是不是 Alarm，还是 Event / Alert / Finding / Data Quality Issue；
7. 哪个 Notification Policy 会处理它；
8. 谁会被通知、通过什么渠道、何时升级；
9. 什么情况下只抑制通知而不抹掉事件；
10. Rule / Policy 当前使用哪个 Revision，何时生效；
11. 新 Revision 是否完成 Test / Review / Approval；
12. 历史事件、历史通知是否仍引用当时实际使用的 Revision。

---

# 2. 主要用户

## Primary

- **规则治理人员：**定义规则、版本、测试与发布；
- **能源 / HVAC 工程师：**维护业务阈值、时间窗口、恢复条件；
- **Alarm Philosophy Owner：**治理 alarm-class rule 的 priority / rationale / response contract；
- **平台运维人员：**维护 notification routing、receiver、delivery policy；
- **Site Administrator：**维护 Site scope、maintenance window 与通知接收策略。

## Secondary

- Operator：只读查看 Definition metadata，并从 09 深链进入本页；
- Data Quality Owner：维护 31 相关检测规则；
- FDD / Analytics Owner：消费或维护 detection rule，但 Root Cause 不在本页确认；
- Auditor：追踪 MOC、Rule Revision、Notification delivery 与 suppression 历史。

---

# 3. 外部最佳实践依据

## 3.1 ISA-18.2 / IEC 62682 — Alarm 必须支持及时响应

ISA/IEC Alarm Management 的核心不是“产生红色消息”，而是：

- Alarm 代表异常条件；
- Alarm 需要 operator response；
- 需要 prioritization、documentation、HMI、testing、monitoring、history、audit 和 management of change；
- Alert / Event / Prompt 等非 Alarm 通知必须与 Alarm 区分。

公开资料：

- https://www.isa.org/standards-and-publications/isa-standards/isa-18-series-of-standards
- https://www.isa.org/intech-home/2018/march-april/features/alarm-management-life-cycle
- https://webstore.iec.ch/en/publication/65543

本页采用：

```text
Rule Match ≠ Alarm automatically
Event ≠ Alarm
Alert ≠ Alarm
Alarm Definition ≠ Alarm Occurrence
```

只有 owner-governed Alarm Definition 满足 Alarm Philosophy contract 时，Rule output 才能成为 Alarm Occurrence。

## 3.2 BACnet Event / Notification — Event state、通知与确认不是一个状态

BACnet 的 Event Notification / AcknowledgeAlarm 语义把 Event State、Notification、AckRequired / Acknowledged transition 分开。

本页采用：

```text
Event Transition ≠ Notification Delivery
Notification Delivery ≠ Acknowledgement
```

并且用户在 email / IM 中“已读”不代表 BACnet / Alarm Handling 的 ACK。

## 3.3 Prometheus Alerting / Alertmanager — Evaluation 与 Notification 是两层

Prometheus 官方把 Alerting Rule 与 Alertmanager 分开：

- Rule 负责判断 pending / firing；
- Alertmanager 负责 grouping、deduplication、routing、silencing、inhibition 与 notification；
- `for` / `keep_firing_for` 显式处理持续条件与恢复抖动。

来源：

- https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/
- https://prometheus.io/docs/alerting/latest/alertmanager/

本页采用：

```text
Detection ≠ Distribution
Grouping ≠ Event Merge
Silence ≠ Event Delete
Inhibition ≠ Rule Disable
```

## 3.4 Azure Monitor — Alert Rule 与 Alert Processing Rule 分离

Azure Monitor 官方明确区分：

- Alert Rule：产生 fired alert；
- Alert Processing Rule：对已经 fired 的 alert 应用 routing / suppression；
- planned maintenance 可以抑制通知，但 alert 本身仍然存在。

来源：

- https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-processing-rules
- https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/best-practices-alerts

本页采用：

```text
Notification Suppressed
≠ Rule Disabled
≠ Event Suppressed
≠ Alarm Shelved
```

## 3.5 CloudEvents — Event Identity / Source / Type / Time 必须明确

CloudEvents 提供通用 Event Envelope 思路，强调统一的 event identity / source / type / time / subject 等元数据。

来源：

- https://cloudevents.io/

本页采用 event identity / provenance 原则，但产品不要求 UI 暴露 CloudEvents raw envelope。

## 3.6 AWS EventBridge — Delivery Retry 必须是可观察、有边界的

EventBridge 官方把 target delivery failure、retry policy 和 dead-letter queue 分开处理。

来源：

- https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-rule-retry-policy.html

本页采用：

```text
Notification Created
≠ Delivered

Delivery Failed
→ Bounded Retry
→ Failed / Dead-lettered if exhausted
```

禁止无限重试、失败后伪装 Delivered。

---

# 4. 产品语言契约

主界面中文优先：

```text
规则与通知
规则
通知策略
检测条件
触发条件
恢复条件
持续时间
迟滞
评估窗口
数据质量要求
事件类型
严重性
告警优先级
接收对象
通知渠道
分组
去重
抑制
升级策略
维护窗口
规则版本
策略版本
测试
发布
生效时间
投递记录
审计
```

避免默认页面充满：

```text
Rule Engine
Dispatch Pipeline
Webhook Processor
Event Bus Consumer
```

这些属于 Advanced / Engineering Detail。

---

# 5. Mandatory Semantic Separation

```text
Rule Definition ≠ Rule Evaluation
Rule Revision ≠ Runtime State
Published ≠ Enabled
Enabled ≠ Condition Matched

Threshold ≠ Condition
Condition True ≠ Rule Firing immediately
Evaluation Window ≠ Delay
Delay ≠ Hysteresis
Trigger Condition ≠ Recovery Condition

Rule Match ≠ Domain Event automatically
Domain Event ≠ Alarm automatically
Alarm Definition ≠ Alarm Occurrence
Alarm Occurrence ≠ Notification Attempt

Notification Policy ≠ Notification Attempt
Notification Attempt ≠ Provider Accepted
Provider Accepted ≠ Delivered to Recipient
Delivered ≠ Read
Read ≠ Alarm Acknowledged

Notification Suppressed ≠ Event Suppressed
Notification Suppressed ≠ Alarm Shelved
Rule Disabled ≠ Notification Muted
Inhibition ≠ Rule Disabled
Grouping ≠ Event Merge
Deduplication ≠ Event Deletion

Rule Severity ≠ Alarm Priority automatically
Notification Urgency ≠ Alarm Priority
Escalation ≠ Priority Change

Test Passed ≠ Approved
Approved ≠ Published
Published ≠ Enabled
Rule Changed ≠ Historical Event Rewritten
```

---

# 6. Core Domain Objects

33 至少治理：

```text
Rule Definition
Rule Revision
Rule Scope
Rule Input Contract
Condition
Evaluation Policy
Recovery Policy
Rule Deployment / Activation
Rule Test Run
Domain Output Contract
Notification Policy
Notification Policy Revision
Routing Rule
Recipient Group Reference
Channel Reference
Grouping Policy
Deduplication Policy
Suppression Policy
Inhibition Policy
Escalation Policy
Notification Attempt
Delivery Receipt / Status
Audit Record
```

Runtime Alarm Occurrence 由 09 Alarm domain 持有，不属于 33 owner。

---

# 7. Rule Identity Contract

每个 Rule 必须有稳定 identity：

```text
Rule ID
Display Name
Rule Type
Domain Owner
Scope
Current Published Revision
Lifecycle State
Created By
Created At
```

Rule ID 不使用 display name 作为业务 identity。

Rename 不创建新 Rule。

---

# 8. Rule Revision Contract

Rule 的业务逻辑变化必须形成新 Revision。

至少记录：

```text
Revision ID
Rule ID
Condition
Inputs
Window
Delay
Hysteresis
Recovery
Output Contract
Severity / Priority references
Notification Policy binding
Reason
Author
Reviewer / Approver
Created At
Effective From
Validation Result
Test Result
```

任何影响 detection semantics 的变更不得直接覆盖 published revision。

---

# 9. Rule Lifecycle Contract

建议 Governance State：

```text
Draft
In Review
Approved
Published
Superseded
Retired
```

Runtime Activation 单独表示：

```text
Enabled
Disabled
Scheduled Inactive
Unavailable
Unknown
```

因此：

```text
Published ≠ Enabled
```

一个已发布规则可以暂未启用；一个 Draft 不能成为 production authoritative runtime definition。

---

# 10. Rule Type Contract

规则必须显式定义 output domain，例如：

```text
Alarm Rule
Operational Alert Rule
Data Quality Rule
Diagnostic Detection Rule
Energy Performance Rule
Demand / Flexibility Rule
Reporting / Governance Rule
Integration Health Rule
Other owner-defined rule
```

Rule Type 决定 output contract，不能所有 Rule Match 都生成 Alarm。

---

# 11. Alarm Rule Boundary

只有 Alarm Rule 才允许生成 Alarm Occurrence。

Alarm Rule 必须引用 / 承载 owner-governed Alarm Definition metadata，例如：

```text
Alarm Class
Priority
Cause
Consequence
Required Operator Response
Response Time expectation if owner-defined
Rationalization reference
```

ISA 语义要求“需要 operator response”才能称为 Alarm。

否则应是 Event / Alert / Notification，而不是为了视觉醒目就命名 Alarm。

---

# 12. Alarm Priority ≠ Generic Severity

Generic Rule 可以有：

```text
Severity
Criticality
Business Impact
```

但 Alarm Priority 必须来自 Alarm Philosophy / Rationalization owner。

禁止：

```text
severity = high
→ alarm priority = P1
```

除非 owner contract 明确提供该映射。

---

# 13. Rule Scope Contract

Rule Scope 必须明确：

```text
Portfolio / Site
Building / Space
System
Asset / Device
Point / Meter
Semantic selector if authoritative
Capability requirements
```

Scope 可以是集合，但不能依赖前端字符串 contains 猜对象关系。

32 Semantic Model 提供对象与 relationship authority。

---

# 14. Input Contract

每个 Rule 输入至少说明：

```text
Input ID / Semantic Binding
Quantity
Unit
Data Nature
Expected Cadence
Freshness Requirement
Quality Requirement
Synchronization Requirement
Aggregation / Window Requirement
```

禁止：

```text
unit missing
→ guess from values

quality missing
→ assume good
```

---

# 15. Input Quality Gate

规则必须声明 bad / stale / missing / suspect input 的处理策略。

合法策略由 owner 明确定义，例如：

```text
Do Not Evaluate
Evaluation = Inconclusive
Emit Data Quality Issue
Use approved estimated input
```

禁止系统通用：

```text
missing → 0
stale → last value
bad quality → good
```

31 Data Quality 是质量事实 owner。

---

# 16. Threshold Contract

Threshold 是 Condition 的参数，不是 Event。

例如：

```text
CHWS Temperature > 8 °C
```

需要同时知道：

```text
Quantity
Unit
Comparison operator
Threshold value
Scope
Revision
```

---

# 17. Condition Contract

Condition 可以是：

```text
Threshold
Range
Rate of Change
State transition
Duration
Logical AND / OR
Sequence
Owner-defined model result
```

复杂表达式必须由 Rule Engine / backend owner 执行。

前端只编辑受约束 DSL / structured definition，不成为 authoritative evaluator。

---

# 18. Evaluation Window Contract

必须区分：

```text
Instantaneous sample
Rolling window
Fixed interval
Calendar window
Event-time window
```

并明确：

```text
Timezone
Event Time basis
Aggregation
Late-data policy
Minimum valid coverage
```

Evaluation Window 不等于 Delay。

---

# 19. Delay / Persistence Contract

为防止瞬时噪声触发，可定义：

```text
On-delay / Persistence
Off-delay / Recovery persistence
```

例如：

```text
Condition true continuously for 5 min
→ Matched
```

不能把 Delay 做成 UI animation delay。

---

# 20. Hysteresis / Deadband Contract

Hysteresis 用于避免边界抖动。

例如：

```text
Trigger: Temperature > 8.0 °C
Recover: Temperature < 7.5 °C
```

必须明确 Trigger 与 Recovery，不能默认：

```text
Recover threshold = Trigger threshold
```

---

# 21. Recovery Contract

Recovery 是一等规则语义，至少定义：

```text
Recovery Condition
Recovery Delay
Recovery Output behavior
Resolved Notification behavior
```

Rule Match 结束不代表 downstream Work / Alarm / Finding 已关闭。

---

# 22. Evaluation State Contract

Rule Runtime 可以表达：

```text
Normal
Pending
Matched
Recovering
Inconclusive
Unavailable
Unknown
```

这些是 **Rule Evaluation State**，不是 Alarm Handling State。

不得映射：

```text
Matched → Alarm Acknowledged = false
Recovered → Alarm Closed = true
```

---

# 23. Schedule Contract

Rule Evaluation schedule 可以定义：

```text
Always
Business Hours
Occupancy Schedule
Seasonal Schedule
One-time Window
Recurring Window
```

Schedule 必须有 timezone / source owner。

Scheduled Inactive 不等于 Disabled。

---

# 24. Maintenance Window Contract

Maintenance Window 可以影响：

```text
Rule Evaluation
Domain Output
Notification Routing
```

但必须明确作用层级。

推荐优先使用 **Notification suppression / processing policy**，而不是为了维护窗口直接 disable Rule，除非 domain owner 明确要求停止评估。

这是为了保留真实事件历史。

---

# 25. Domain Output Contract

Rule Match 后必须声明 output type：

```text
Alarm Occurrence
Operational Event
Alert
Data Quality Issue
Diagnostic Finding Candidate
Energy Deviation Event
Integration Health Event
No persistent output / test-only
```

Output owner 决定 downstream lifecycle。

33 不统一所有业务事件生命周期。

---

# 26. Event Identity Contract

Domain Event 至少需要稳定可追溯：

```text
Event ID
Event Type
Source Rule ID
Source Rule Revision
Subject / Object
Scope
Occurred At
Observed / Evaluated At
Lifecycle Instance ID if applicable
Provenance
```

采用 CloudEvents 类的 identity / source / type / time 思路，但不强制 UI 暴露 raw envelope。

---

# 27. Rule Match ≠ Domain Event Automatically

有些 Rule Match 可能：

- 只更新一个持续 Evaluation Instance；
- 延迟到 persistence 条件满足后才创建 Event；
- 在已有 active occurrence 上更新 evidence；
- 因 suppression policy 仍保留 Event 但不通知。

Event creation semantics 必须由 output owner 定义。

---

# 28. Event Deduplication Contract

重复评估不应该每个周期都创建一个新事件。

Dedup / lifecycle key 必须由 owner 明确，例如：

```text
Rule ID
Rule Revision policy
Subject
Condition fingerprint
Lifecycle instance
```

禁止：

```text
same message text
→ same event
```

---

# 29. Rule Revision and Active Occurrence

发布新 Rule Revision 时，必须明确 active occurrence 如何处理：

```text
Continue under old revision until recovery
Migrate under owner-defined transition
Close old + open new with explicit reason
```

不能静默把历史 active occurrence 的 Rule Revision 改成新版本。

---

# 30. Notification Policy Identity Contract

Notification Policy 是独立版本化对象：

```text
Policy ID
Display Name
Scope
Applicable Event Types
Routing Rules
Grouping
Deduplication
Suppression
Escalation
Receivers
Current Revision
Lifecycle State
Owner
```

Rule Definition 可以绑定 Notification Policy，但两者不是同一个对象。

---

# 31. Notification Policy Revision Contract

影响通知行为的变化必须形成新 Revision：

```text
Recipients
Channels
Routing conditions
Grouping
Repeat interval
Quiet hours
Maintenance suppression
Escalation stages
Templates
```

历史 Notification Attempt 必须引用当时实际使用的 Policy Revision。

---

# 32. Routing Contract

Routing 条件可以基于 authoritative metadata：

```text
Event Type
Alarm Class / Priority
Severity
Site
System
Asset Class
Business Impact
Schedule
Recipient responsibility
```

Routing 不应依赖 message body 字符串匹配作为核心业务规则。

---

# 33. Receiver / Recipient Contract

必须区分：

```text
Recipient Identity
Recipient Group
Channel Endpoint
External Receiver Integration
```

例如：

```text
冷站值班组
→ 企业微信 Channel
→ Receiver integration WX-OPS-01
```

用户身份与权限由 36 owner；外部 connector / credential 由 34 owner。

33 不保存 plaintext secret。

---

# 34. Channel Contract

可用渠道取决于 capability，例如：

```text
In-app
Email
SMS
Voice
Enterprise IM
Webhook
Incident platform
```

不同 Channel 有不同 delivery semantics，不能统一声称“已送达用户”。

---

# 35. Notification Attempt Contract

每次投递尝试至少记录：

```text
Notification ID
Event ID
Policy Revision
Receiver
Channel
Attempt Number
Created At
Attempted At
Provider Response
Delivery State
Failure Reason
Next Retry At
Idempotency Key
```

---

# 36. Delivery State Contract

建议状态：

```text
Queued
Attempting
Provider Accepted
Delivered
Failed
Dead-lettered
Suppressed
Cancelled
Unknown
```

并且：

```text
Provider Accepted ≠ Delivered
Delivered ≠ Read
```

如果 Channel 不支持 delivery receipt，则保持：

```text
Delivery = Unknown / Provider Accepted
```

不能伪造 Delivered。

---

# 37. Read / User Interaction Contract

只有渠道提供权威 read receipt 时才可显示：

```text
Read At
```

`Read` 不等于：

```text
Alarm Acknowledged
Work Accepted
Issue Assigned
```

这些属于各 domain owner。

---

# 38. Notification Acknowledgement Boundary

如果产品支持“收到通知，请确认已知悉”，必须作为独立：

```text
Notification Acknowledgement
```

它不能冒充 09 Alarm Acknowledgement。

---

# 39. Grouping Contract

Grouping 负责把多个事件合成一次通知呈现。

例如：

```text
12 个同类 AHU 通信事件
→ 1 个 grouped notification
```

但底层仍然是 12 个 Event。

```text
Grouping ≠ Event Merge
```

---

# 40. Group Timing Contract

可配置：

```text
Group Wait
Group Interval
Repeat Interval
```

需要权衡及时性与噪声。

对于 Alarm Priority 需要及时响应的场景，Grouping timing 必须符合 alarm philosophy owner 的响应要求。

---

# 41. Notification Deduplication Contract

Deduplication 只减少重复通知。

```text
Deduplicated Notification
≠ Event Deleted
≠ Event Cleared
```

Dedup Key 必须 owner-defined。

---

# 42. Suppression Contract

必须区分至少四类：

```text
Rule Evaluation Suppression
Domain Event / Alarm designed suppression
Notification Suppression
Alarm Shelving / Out of Service
```

它们属于不同 owner / lifecycle。

---

# 43. Notification Suppression Contract

Notification suppression 可以基于：

```text
Maintenance Window
Quiet Hours
Recipient Schedule
Scope
Event metadata
Explicit mute
```

被 suppress 的 Notification 必须可追溯：

```text
Suppressed By Policy
Policy Revision
Reason
Effective Window
```

Event 仍然存在。

---

# 44. Inhibition Contract

Inhibition 表示：当更高层 / 更根本的事件存在时，不再对 dependent events 发送通知。

例如：

```text
BAS Gateway Down active
→ inhibit 50 downstream point communication notifications
```

但 50 个 downstream events 仍可保留用于 evidence / history。

```text
Inhibition ≠ Root Cause Confirmation
```

它只是 notification relevance policy。

---

# 45. Alarm Shelving Boundary

Alarm Shelving / Suppressed by Design / Out of Service 属于 09 Alarm Handling / Alarm Definition lifecycle。

33 可以配置相关 rule / policy definition，但 Live Shelving action 不在本页执行。

避免 maintenance admin 在规则页直接冒充 operator handling state。

---

# 46. Escalation Contract

Escalation Policy 至少定义：

```text
Stage
Trigger condition
Wait duration
Recipient / Group
Channel
Stop condition
Maximum stages
```

例如：

```text
Stage 1 → Site Operator
10 min 无 owner-defined response
Stage 2 → Facility Manager
20 min 仍无 response
Stage 3 → Energy Manager
```

---

# 47. Escalation Trigger Boundary

“无 response”必须引用 authoritative response owner，例如：

```text
Alarm Acknowledgement
Work Assignment
Incident Acceptance
Notification Acknowledgement
```

如果 response owner 不可用：

```text
Response Status = Unknown
```

禁止默认“未响应”。

---

# 48. Delivery Failure Escalation ≠ Business Escalation

必须区分：

```text
Channel delivery failure escalation
Business response escalation
```

例如 email 发送失败可切换 SMS；这与 Alarm 未 ACK 10 分钟升级到主管不是同一逻辑。

---

# 49. Retry Contract

投递失败采用 bounded retry：

```text
Retryable failure
→ backoff
→ retry N times / until time limit
→ Dead-lettered or Failed
```

必须记录 retry history。

禁止无限重试。

---

# 50. Idempotency Contract

Notification delivery 必须支持稳定 idempotency / dedup key，防止 network retry 造成重复短信 / 电话 / webhook side effect。

Idempotency Key 由 delivery owner 生成，前端不拼接随机业务字段冒充。

---

# 51. Dead-letter / Failed Delivery Contract

Retry exhausted 后必须形成可处理事实：

```text
Delivery Failed / Dead-lettered
Receiver
Channel
Last Error
Attempts
First Attempt
Last Attempt
Owner
```

不能：

```text
send failed
→ silently drop
```

---

# 52. Rule Test Contract

Rule Revision 在 Publish 前支持测试：

```text
Static validation
Synthetic input test
Historical replay
Boundary cases
Missing / stale / bad-quality cases
Recovery / hysteresis cases
Notification routing dry-run
```

测试必须 side-effect safe。

---

# 53. Historical Replay Contract

Historical Replay 必须明确：

```text
Data Window
Input Revision / Source
Event-time behavior
Rule Revision
Expected Output
Actual Output
```

测试结果不能写入 production Alarm / Event / Notification ledger。

---

# 54. Notification Dry-run Contract

Dry-run 可以展示：

```text
Would Route To
Would Group By
Would Suppress Because
Would Escalate To
Rendered Template
```

默认不能真正发生产 SMS / Voice / webhook。

真实 test delivery 必须明确标记 `TEST`，并有权限控制。

---

# 55. Test Result Contract

Test Run 至少记录：

```text
Test ID
Rule Revision
Scenario
Dataset / Fixture
Started At
Completed At
Result
Observed Matches
Expected Matches
False / missed cases if owner-defined
Reviewer
```

```text
Test Passed ≠ Approved
```

---

# 56. Publish Contract

Publish 前至少验证：

```text
Rule syntax valid
Inputs resolved
Units valid
Scope resolved
Recovery defined when required
Notification policy resolved when required
Required test passed
Review / approval satisfied
Effective date valid
```

Publish 生成新的 authoritative revision。

---

# 57. Publish ≠ Runtime Activation

发布定义与启用 production runtime 分离。

高风险 Alarm / Notification Policy 可采用：

```text
Approved
→ Published
→ Scheduled Activation
→ Enabled
```

不能 Publish 后无条件立即影响现场。

---

# 58. Version / Effective Date Contract

Rule / Notification Policy 都必须支持：

```text
Revision
Effective From
Effective To
```

Historical Event / Notification 必须引用：

```text
Rule Revision actually evaluated
Policy Revision actually routed
```

不能查询当前最新 revision 后重解释历史。

---

# 59. Change Impact Contract

Rule change 可以影响：

```text
Alarm frequency
Notification volume
Operator workload
Energy / Data Quality issues
Escalation load
Compliance / Audit
```

高影响变更必须显示 change impact / review evidence。

---

# 60. Management of Change Contract

对 Alarm Definition、priority、delay、deadband、suppression logic、routing、cause/consequence/response 等实质修改，必须进入 MOC / review / audit。

09 Alarm Center 已明确这些不允许在 live triage 页面直接修改。

---

# 61. Data Quality Handoff

31 可以产生：

```text
Data Quality Issue
```

33 可以治理其检测 Rule。

但：

```text
Rule Match
≠ Data Quality Issue automatically closed/opened by frontend
```

Issue lifecycle 仍由 31 owner。

---

# 62. Semantic Model Handoff

32 提供：

```text
Canonical Identity
Relationship
Quantity
Unit
Meter / Point semantics
Effective mapping
```

33 Rule Scope / Input Binding 消费这些 authority。

禁止根据 name / prefix 猜 rule binding。

---

# 63. Alarm Center Handoff

09 消费：

```text
Alarm Definition metadata
Rule Revision reference
Cause / Consequence / Response
Priority / Class
```

09 负责：

```text
Occurrence
ACK
Assignment
Shelving
Active / History
```

33 不承担 live alarm handling。

---

# 64. Diagnosis Boundary

Diagnostic Detection Rule 可以产生：

```text
Finding Candidate / Detection Event
```

但：

```text
Rule Match ≠ Confirmed Root Cause
```

10 Diagnosis 才负责 evidence → hypothesis → verification → confirmed root cause。

---

# 65. Control / Safety Boundary

33 不是控制 safety logic editor。

以下不在本页通用 Rule Engine 中治理：

```text
Hard interlock
Safety instrumented function
BACnet direct write sequence
Control guardrail enforcement
Automatic rollback authority
```

这些由 25–28 Control / Strategy / Execution authority 与现场控制 owner 管理。

---

# 66. Integration Boundary

34 Integration 管理：

```text
SMTP / SMS / IM / Webhook connector
Credentials
Auth
Endpoint health
Rate limit / errors
```

33 只绑定 Receiver / Channel Reference。

33 不复制 secret，不成为 connector health owner。

---

# 67. Access Boundary

36 管理：

```text
User identity
Role
Permission
Site scope
Sensitive actions
Audit principal
```

33 可以选择 Recipient Group reference，但不自行维护用户账号。

---

# 68. Notification Template Contract

模板至少可以使用：

```text
Event type
Subject / Object
Site
Severity / Priority
Current state
Occurred at
Rule name
Runbook / deep link
```

模板必须防止把 Unknown 填成 0 / Normal。

Raw payload / secret 不应默认进入消息。

---

# 69. Deep-link Contract

通知应尽量包含可恢复上下文的 deep link，例如：

```text
Site
Object
Occurrence / Event ID
Time window
Source = notification
```

Alarm 通知优先进入 09 Alarm occurrence context，而不是跳到 33 Rule editor。

---

# 70. Rate / Volume Protection Contract

Notification Policy 可以定义：

```text
Grouping
Repeat interval
Per-recipient rate guard
Per-channel guard
Flood protection
```

但 Rate limit 不应该删除 Event。

如果大量消息被抑制 / 延迟，必须可见。

---

# 71. Rule Performance Contract

可以治理 owner-defined metrics：

```text
Evaluation count
Matched count
Events created
Notifications created
Suppressed count
Delivery failures
Repeat / grouped count
```

对于 Alarm Rule，还可 deep-link 09 Alarm Performance，但 33 不重新定义 Alarm KPI。

---

# 72. Rule Health Contract

Rule health 至少区分：

```text
Definition Validity
Input Resolution
Runtime Evaluation Health
Output Delivery Health
```

不能压成：

```text
Rule Health = 87
```

单一黑盒分数。

---

# 73. Permissions

典型权限：

```text
Read Rules
Create Draft Rule
Edit Draft Rule
Run Rule Test
Review Rule
Approve Rule
Publish Rule
Enable / Disable Rule
Read Notification Policy
Edit Notification Policy
Test Notification Delivery
Approve Notification Policy
Publish Notification Policy
Manage Suppression Policy
Read Delivery History
Read Audit
```

高风险 Alarm Rule / external delivery test 需要更高权限。

---

# 74. Audit Contract

至少审计：

```text
Rule create / edit
Revision create
Review / approval
Publish
Enable / disable
Scope change
Threshold change
Delay / hysteresis change
Recovery change
Priority / class change
Notification policy change
Recipient / channel change
Suppression change
Escalation change
Test execution
Production test delivery
Failed delivery handling
```

Audit 记录 who / when / before / after / reason / revision。

---

# 75. AI Assistance Boundary

AI 可以：

- 根据历史数据建议 candidate threshold；
- 建议 hysteresis / delay 候选；
- 总结 notification volume；
- 检查重复规则；
- 生成 test cases；
- 总结 change impact；
- 草拟 notification 文案。

AI 不能：

- 自动把 Event 升级成 Alarm；
- 自动确定 Alarm Priority；
- 自动发布生产 Rule；
- 自动启用 Rule；
- 自动修改 suppression；
- 自动把 delivery failure 当作 delivered；
- 自动修改 live Control interlock。

AI suggestion 必须保持 Candidate 状态。

---

# 76. No Defensive Programming / No Compatibility Design

明确禁止：

```text
rule API error
→ []

threshold missing
→ 0

unit missing
→ guess from values

quality unavailable
→ assume Good

freshness unknown
→ assume Fresh

recovery missing
→ trigger condition as recovery

delay missing
→ 0 seconds

hysteresis missing
→ 0

schedule timezone missing
→ browser timezone

scope relationship missing
→ infer from name

rule match
→ alarm occurrence

event severity high
→ alarm priority P1

notification policy missing
→ default admin

recipient missing
→ all admins

channel missing
→ email

notification failed
→ delivered

provider accepted
→ delivered

provider accepted
→ read

delivered
→ read

read
→ alarm acknowledged

notification acknowledged
→ alarm acknowledged

notification suppressed
→ event suppressed

notification suppressed
→ alarm shelved

rule disabled
→ close active alarm

maintenance window
→ delete event

deduplicated
→ delete duplicate event history

grouped
→ merge event identities

inhibition
→ root cause confirmed

new rule revision
→ rewrite old events

new policy revision
→ rewrite old notification records

test passed
→ approved

approved
→ published

published
→ enabled

retry failed
→ retry forever

multiple rule APIs
→ first success wins

legacy threshold parser fallback
legacy notification compatibility adapter
legacy alarm config fallback
```

正式原则：

> **检测、业务事件、Alarm、通知策略、投递尝试、投递结果、用户阅读和业务确认是不同事实。Rule Definition 与 Runtime Evaluation 分离，Rule Revision 与 Historical Occurrence 分离，Notification Suppression 不能抹掉 Event，Delivery 不能冒充 Read / ACK。Unknown 保持 Unknown。**

---

# 77. Information Architecture

```text
Context Header
↓
[规则] [通知策略] [测试] [投递记录] [变更]
↓
Rule Ledger
                         → Rule Inspector
↓
Rule Detail / Revision
↓
Inputs / Scope
↓
Condition / Window / Delay / Hysteresis / Recovery
↓
Domain Output
↓
Notification Policy
↓
Routing / Grouping / Dedup / Suppression / Escalation
↓
Test / Historical Replay
↓
Review / Publish / Activation
↓
Delivery History
↓
Audit
```

---

# 78. Route / URL State Contract

```text
/settings/rules
/settings/rules/:ruleId
```

Search Params 可包括：

```text
view
siteId
ruleType
state
activation
severity
outputType
notificationPolicy
channel
recipientGroup
suppression
revision
q
```

Rule identity 进入 Path；筛选 / view 进入 Search Params。

---

# 79. Capability Gating

33 可见条件：

```text
Product Rules capability exists
AND deployment supports rule governance
AND principal may discover rules
```

通知策略能力可以进一步 gating：

```text
Notification capability exists
AND one or more channel capabilities exist
```

没有 SMS capability 时不显示假的 SMS toggle。

---

# 80. Rule Ledger Contract

默认列：

```text
规则
类型
Scope
输出
当前版本
治理状态
运行状态
通知策略
最近测试
最近变更
```

可选专业列：

```text
Delay
Hysteresis
Recovery
Evaluation Window
Input Quality Gate
```

---

# 81. Rule Inspector Contract

快速 Inspector 只显示：

```text
Identity
Rule Type
Scope
Inputs
Trigger summary
Recovery summary
Output type
Current revision
Published / Enabled state
Notification policy
Last test
Last change
```

编辑进入 Durable Detail Route。

---

# 82. Rule Detail Layout

推荐：

```text
Overview
Inputs & Scope
Condition
Recovery
Output
Notification
Tests
Versions
Audit
```

不是 12 层 accordion。

---

# 83. Notification Policy Workspace Contract

Notification Policy 默认显示：

```text
Applicable event types
Routes
Receivers
Channels
Grouping
Dedup
Suppression
Escalation
Version / effective period
Test status
```

不能只显示“通知开启/关闭”。

---

# 84. Delivery History Contract

Delivery History 是运维诊断用途，不是用户 Inbox。

默认列：

```text
Notification
Event
Policy Revision
Receiver
Channel
State
Attempts
Last Attempt
Failure reason
```

可以从 failed delivery deep-link 到 34 Integration Health。

---

# 85. Test Workspace Contract

Test 页面至少支持：

```text
Choose Draft Revision
Choose Test Scenario / Historical Window
Run Evaluation
Inspect Pending / Match / Recovery
Inspect Would-route notification
Compare Expected vs Actual
Save Test Evidence
```

默认无 production side effect。

---

# 86. Empty / Unknown / Error States

## Empty

无 Rule 是合法 Empty。

## Unknown

例如 receiver delivery receipt 不支持。

## Partial

Rule metadata 可读，但 runtime health owner 暂不可用。

## Error

Rule service / notification service 请求失败明确显示。

禁止：

```text
error → empty
unknown delivery → failed
unknown delivery → delivered
```

---

# 87. Accessibility / Responsive Contract

- Rule state 不只靠颜色；
- Enabled / Published / Matched 三种状态必须有文本；
- Priority / Severity 不只靠颜色；
- Routing tree 有 textual list alternative；
- Condition builder 支持键盘；
- Dialog focus 遵守全局契约；
- 768px 下仍能完成 rule lookup、查看 condition、查看 notification route、run test 和 review history。

---

# 88. Wireframe Intent

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ 规则与通知                                         中央园区 · 生产环境     │
├────────────────────────────────────────────────────────────────────────────┤
│ [规则] [通知策略] [测试] [投递记录] [变更]                               │
├────────────────────────────────────────────────────────────────────────────┤
│ 规则                         类型        输出         版本      状态        │
│ 冷冻水供水温度高             Alarm       Alarm        rev7      已启用      │
│ 冷量计数据陈旧               DataQuality Issue        rev4      已启用      │
│ 夜间基载异常                 Energy      Event        rev6      已启用      │
│ BAS Gateway 通信中断         Integration Event       rev3      已启用      │
├───────────────────────────────────────────────┬────────────────────────────┤
│ 冷冻水供水温度高                             │ 快速检查                   │
│ Scope: 中央冷站 / CHWS Header                 │ Rule R-102                  │
│ Input: CHWS Temp · °C · Good Quality required │ Type Alarm                  │
│ Trigger: > 8.0 °C for 5 min                   │ Revision rev7               │
│ Recover: < 7.5 °C for 3 min                   │ Published / Enabled         │
│ Output: Alarm Definition A-21                 │ Policy NP-03 rev5           │
│                                               │ Last Test Passed            │
├────────────────────────────────────────────────────────────────────────────┤
│ 通知策略 NP-03 · 冷站高优先级                                                  │
│ P1/P2 → 值班组 · 企业微信 + SMS                                              │
│ Group wait: 0 · Repeat: 15 min                                               │
│ Maintenance: 只抑制通知，不删除 Alarm occurrence                            │
│ Escalation: 10 min 未 ACK → Facility Manager                                 │
├────────────────────────────────────────────────────────────────────────────┤
│ 待发布变更 rev8                                                              │
│ Trigger 8.0 → 8.2 °C · Hysteresis 保持 0.5 °C                              │
│ Historical Replay: Passed · 30 days                                          │
│ Impact: Alarm volume -18% · required review                                  │
│ [查看测试] [提交审批]                                                        │
└────────────────────────────────────────────────────────────────────────────┘
```

---

# 89. Browser Acceptance Criteria

## Rule Semantics

- Threshold / Condition / Window / Delay / Hysteresis / Recovery 明确分开；
- Rule Definition / Evaluation 分开；
- Published / Enabled / Matched 分开；
- Missing / stale / bad quality 不被补零或默认 good；
- rule scope 不通过名称推断。

## Domain Output

- Rule Match 不自动生成 Alarm；
- Alarm Rule 必须有 Alarm Definition / priority / response reference；
- Alert / Event / Data Quality / Diagnostic output 可区分；
- Rule Severity 不自动映射 Alarm Priority。

## Notification

- Rule 与 Notification Policy 分开；
- Attempt / Provider Accepted / Delivered / Read 分开；
- Channel 不支持 receipt 时保持 Unknown；
- Read 不修改 Alarm ACK；
- notification ack 不修改 Alarm ACK。

## Suppression / Noise

- Grouping 不合并 Event identity；
- Dedup 不删除 Event；
- Notification suppression 不删除 Event；
- Inhibition 不等于 Root Cause；
- Alarm shelving 不在本页 live 执行；
- maintenance policy 明确作用层级。

## Version / Test

- Rule / Notification Policy 都 versioned；
- historical event / attempt 保留原 revision；
- Test Passed 不自动 Publish；
- Publish 不自动 Enable；
- historical replay 默认无 production side effect。

## Delivery Reliability

- retry 有边界；
- failed attempt 可见；
- exhausted retry 可进入 Failed / Dead-lettered；
- provider accepted 不冒充 delivered；
- idempotency contract 明确。

## Governance

- substantive changes 有 review / audit；
- alarm definition changes 可追溯；
- notification recipient / channel changes 可追溯；
- no compatibility fallback；
- review scenario 无 runtime / network error。

---

# 90. Explicit Non-goals

33 不是：

- Alarm Triage Center；
- operator ACK console；
- universal event workflow engine；
- SIEM；
- generic low-code automation platform；
- control interlock editor；
- safety PLC editor；
- full contact directory；
- integration credential vault；
- frontend runtime rule evaluator。

---

# 91. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Rule Definition / Evaluation 分离；
- Rule Revision / Runtime State 分离；
- Threshold / Condition / Window / Delay / Hysteresis / Recovery 分离；
- Rule Match / Event / Alarm 分离；
- Alarm / Alert / Event / Data Quality output 分离；
- Rule Severity / Alarm Priority 分离；
- Rule / Notification Policy 分离；
- Attempt / Accepted / Delivered / Read / ACK 分离；
- Grouping / Dedup / Suppression / Inhibition 分离；
- Alarm Shelving 与 Notification Suppression 分离；
- Escalation response owner 明确；
- bounded retry / dead-letter 明确；
- Rule / Policy Revision + Effective Date 明确；
- Historical integrity 明确；
- Test / Replay / Dry-run 明确；
- MOC / Review / Approval / Publish / Enable 明确；
- 31 / 32 / 09 / 10 / 34 / 36 责任边界明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
