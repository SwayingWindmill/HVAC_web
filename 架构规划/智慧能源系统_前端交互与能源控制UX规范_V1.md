# 智慧能源系统前端交互与能源控制 UX 规范

> 文档编号：SE-UX-001  
> 版本：V1.0  
> 状态：CURRENT  
> 技术基线：React + Go API + WebSocket/SSE（实时推送）  
> 适用范围：Web 管理端、能源运营端、控制操作端、大屏与管理驾驶舱  
> 上位设计：React 前端信息架构、总体架构设计、后端接口设计、安全与控制架构、告警与规则引擎设计、能源指标体系、计量结算与对账、预测与优化调度设计  
> 核心目标：建立统一、可预测、安全、可解释的前端交互语言，尤其保证能源控制相关操作不会因 UI 设计导致误操作、状态误判或风险隐藏

---

# 1. 设计目标

智慧能源前端不是普通后台系统。

它同时承载：

```text
实时监控
历史分析
告警处置
能源经营
财务结算
设备管理
远程控制
预测
优化调度
```

因此 UX 必须重点解决：

```text
现在系统是什么状态？

数据新不新？

这个数是否可信？

告警是否处理？

设备是否在线？

控制能不能做？

为什么不能做？

执行到哪一步？

算法建议是否安全？

结算结果能不能解释？
```

---

# 2. 核心原则

第一：

```text
状态优先于装饰
```

第二：

```text
用户必须知道数据时间和质量
```

第三：

```text
控制操作必须显式展示风险和最终结果
```

第四：

```text
ACK != VERIFIED
```

第五：

```text
预测、建议、计划、实际
必须视觉和语义分离
```

第六：

```text
财务结算数据必须显示版本与锁定状态
```

第七：

```text
任何高风险动作不得依赖颜色作为唯一提示
```

---

# 3. 前端信息架构

建议一级导航：

```text
首页
实时监控
能源分析
设备
告警
控制
预测与优化
结算
配置
运维
系统管理
```

---

# 4. 首页 Dashboard

首页回答：

```text
当前能源状态怎样？
有没有风险？
今天用了多少？
今天花了多少？
设备是否正常？
```

---

# 5. 首页核心卡片

建议：

```text
Current Load
Grid Power
PV Power
ESS Power
ESS SOC
Today Energy
Today Cost
Open Alarms
Device Online Rate
Data Quality
```

---

# 6. 首页不宜堆太多指标

推荐首页：

```text
8~12 个核心 KPI
```

其他进入二级页面。

---

# 7. KPI Card 标准

每个卡片至少：

```text
名称
数值
单位
状态
更新时间
趋势
```

---

# 8. 数据时间

所有实时值应能看到：

```text
Last Updated
```

例如：

```text
13:21:05
```

---

# 9. Stale 状态

超过：

```text
freshness threshold
```

应明确：

```text
STALE
```

而不是继续显示一个看起来正常的旧值。

---

# 10. 数据质量

数值至少支持：

```text
GOOD
PARTIAL
ESTIMATED
MANUAL
STALE
BAD
```

---

# 11. 质量呈现

推荐：

```text
状态标签
+
Tooltip
+
详细来源
```

不要只改变字体颜色。

---

# 12. 实时监控页面

结构：

```text
Site Selector
↓
Energy Flow
↓
Realtime KPI
↓
Device/Boundary Status
↓
Realtime Trend
```

---

# 13. Energy Flow

实时能流至少：

```text
Grid
PV
ESS
Load
```

---

# 14. 动态方向

Grid：

```text
Import
Export
```

ESS：

```text
Charge
Discharge
```

箭头方向必须随数据变化。

---

# 15. 方向文字

不能只靠箭头。

同时显示：

```text
购电
上网
充电
放电
```

---

# 16. ESS 功率

平台标准：

```text
ESS Power > 0
= 放电

ESS Power < 0
= 充电
```

前端可把绝对值配合状态展示：

```text
放电 300 kW
```

避免用户误解负号。

---

# 17. 能源平衡

可显示：

```text
Balance Error
```

当超过阈值：

```text
展示数据质量提示
```

---

# 18. Realtime Trend

默认时间窗口：

```text
15min
1h
6h
24h
```

---

# 19. 趋势图原则

明确：

```text
Actual
Forecast
Plan
```

三者不能使用同一种线型语义。

---

# 20. Actual

表示：

```text
真实数据
```

---

# 21. Forecast

表示：

```text
预测
```

必须标：

```text
Forecast
```

---

# 22. Plan

表示：

```text
计划/优化调度
```

不能误导成已执行。

---

# 23. 图表 Tooltip

至少：

```text
Time
Value
Unit
Quality
Series Type
```

---

# 24. 时间区间

默认显示：

```text
Site Local Time
```

但后端存储：

```text
UTC
```

---

# 25. 跨时区

用户切换 Site 时：

```text
页面时间自动使用Site Timezone
```

---

# 26. 设备页面

层级：

```text
Site
→ Asset
→ Device
→ Point
```

---

# 27. Device List

字段建议：

```text
Name
Type
Status
Gateway
Last Seen
Model
Firmware
Template Version
Alarm
```

---

# 28. Device Detail

Tabs：

```text
Overview
Points
History
Alarms
Commands
Configuration
Diagnostics
Audit
```

---

# 29. Device Overview

展示：

```text
Device Identity
Online
Last Seen
Firmware
Template
Gateway
Location
```

---

# 30. Point 页面

字段：

```text
Point Code
Name
Value
Unit
Quality
Last Update
Access
```

---

# 31. Point Code

高级用户可查看：

```text
standard point_code
```

普通运营用户主要看：

```text
业务名称
```

---

# 32. Raw Register

厂商寄存器信息：

```text
默认不在普通用户页面展示
```

放入：

```text
Diagnostics / Engineering Mode
```

---

# 33. Point History

提供：

```text
Raw
1min
5min
15min
Hour
```

根据查询跨度自动选择。

---

# 34. Query Guardrail

用户选择：

```text
1000 points × 1 year raw
```

前端应：

```text
阻止 / 提示使用聚合
```

---

# 35. Alarm 页面

结构：

```text
Open
Acknowledged
Resolved
Closed
Suppressed
Shelved
```

---

# 36. Alarm List

字段：

```text
Severity
Status
Name
Source
Started At
Duration
Value
Threshold
Owner
```

---

# 37. Alarm Severity

统一：

```text
CRITICAL
MAJOR
MINOR
WARNING
INFO
```

---

# 38. Alarm Status

不能把：

```text
Severity
```

和：

```text
Status
```

混为一个颜色。

---

# 39. Alarm Detail

必须可查看：

```text
Trigger Condition
Current Value
Rule Version
Timeline
Acknowledgement
Recovery
Notifications
Related Device
```

---

# 40. Alarm ACK

ACK 表示：

```text
有人知道了
```

不等于：

```text
故障恢复
```

---

# 41. Alarm Resolve

必须来自：

```text
Recovery Condition
```

或明确人工流程。

---

# 42. Alarm Suppress

Suppress 操作必须：

```text
Reason
Duration
Scope
```

---

# 43. Alarm Storm

当系统启用 Storm Suppression：

```text
UI仍应显示被合并数量
```

不能完全隐藏事实。

---

# 44. 控制首页

控制相关入口必须：

```text
与普通监控明显区分
```

---

# 45. Control Page

结构：

```text
Control Authority
Control Mode
Target
Current State
Allowed Range
Proposed Command
Preview
Approval
Execution
Readback
Audit
```

---

# 46. Control Authority

页面显式显示：

```text
当前谁拥有控制权
```

例如：

```text
LOCAL
REMOTE_MANUAL
REMOTE_AUTO
LOCKED
```

---

# 47. LOCAL

LOCAL 时：

```text
所有远程控制按钮禁用
```

并说明：

```text
设备处于本地控制模式
```

---

# 48. 控制按钮不可只 Disabled

用户需要知道：

```text
为什么禁用
```

例如：

```text
设备离线
LOCAL模式
权限不足
Safety禁止
维护中
```

---

# 49. Control Preview

所有风险控制操作先：

```text
Preview
```

---

# 50. Preview 内容

至少：

```text
Target Device
Current Value
Requested Value
Allowed Range
Control Mode
Online State
Safety Check
Risk Level
Expiry
```

---

# 51. Risk Level

显示：

```text
R1
R2
R3
R4
```

并配文字解释。

---

# 52. R3/R4

需要审批时：

```text
页面明确显示审批状态
```

---

# 53. Step-up Authentication

高风险动作需要：

```text
重新验证
```

UI 应明确：

```text
这是安全确认
```

不是普通登录过期。

---

# 54. Confirmation Dialog

不能只写：

```text
确认执行？
```

应展示：

```text
设备
动作
目标值
当前值
风险
有效期
```

---

# 55. 高风险确认

可要求：

```text
输入原因
```

或：

```text
输入目标设备名称
```

降低误点。

---

# 56. 控制状态机

前端必须完整展示：

```text
CREATED
VALIDATING
APPROVAL_PENDING
APPROVED
SENT
ACKED
EXECUTING
VERIFIED
FAILED
REJECTED
EXPIRED
UNKNOWN
```

---

# 57. SENT

表示：

```text
命令已发送
```

不是成功。

---

# 58. ACKED

表示：

```text
Edge/Device已确认接收
```

仍不是最终成功。

---

# 59. VERIFIED

表示：

```text
Readback确认实际状态符合目标
```

这是成功闭环。

---

# 60. UNKNOWN

如果物理结果无法确认：

```text
必须显示UNKNOWN
```

不能自动显示失败或成功。

---

# 61. UNKNOWN UX

提示：

```text
“设备实际状态尚无法确认，请先查看Readback或现场状态，禁止盲目重试。”
```

---

# 62. Command Retry

UNKNOWN 时：

```text
默认禁用直接Retry
```

---

# 63. Readback Mismatch

明确显示：

```text
Requested
Actual
Tolerance
```

---

# 64. Command Timeline

例如：

```text
13:20:01 Created
13:20:02 Approved
13:20:03 Sent
13:20:04 ACK
13:20:07 Readback 295 kW
13:20:07 Verified
```

---

# 65. Control Audit

用户可查看：

```text
Operator
Source
Reason
Approval
Safety
Command ID
Trace ID
```

---

# 66. Emergency Freeze

控制页面必须有明确：

```text
Automation Freeze Status
```

---

# 67. Kill Switch

仅授权用户显示。

操作后：

```text
明显 Banner
```

例如：

```text
自动调度已暂停
```

---

# 68. 预测页面

结构：

```text
Forecast Target
Actual vs Forecast
Model Version
Forecast Origin
Accuracy
Quality
```

---

# 69. Forecast Target

明确：

```text
Site Load
PV Generation
Net Load
```

---

# 70. Forecast Vintage

用户可查看：

```text
预测生成时间
```

---

# 71. Forecast Quality

展示：

```text
VALID
DEGRADED
FALLBACK
INVALID
```

---

# 72. Fallback

当预测使用 Baseline：

```text
必须告诉用户
```

不能继续装作主模型正常。

---

# 73. Accuracy

展示：

```text
MAE
WAPE
Bias
```

普通用户可显示：

```text
近期平均误差
```

---

# 74. Prediction Warning

预测风险：

```text
必须标注“预测”
```

不能混成真实设备告警。

---

# 75. 优化页面

结构：

```text
Forecast
Tariff
Resource State
Optimization Plan
Expected Benefit
Constraints
Execution
Actual
```

---

# 76. 优化模式

必须明确：

```text
OFF
RECOMMEND
SHADOW
ASSISTED
AUTO_LIMITED
AUTO
```

---

# 77. SHADOW

显示：

```text
“仅模拟，不会发送控制命令”
```

---

# 78. RECOMMEND

显示：

```text
建议方案
```

而不是：

```text
执行计划
```

---

# 79. ASSISTED

必须：

```text
人工Approve
```

---

# 80. AUTO_LIMITED

页面显示：

```text
自动化范围
最大功率
SOC边界
时间窗口
资源范围
```

---

# 81. Dispatch Plan Timeline

展示：

```text
Charge
Idle
Discharge
```

以及：

```text
计划SOC
预测Grid
```

---

# 82. Expected Benefit

必须写：

```text
预计收益
```

不能写：

```text
收益
```

---

# 83. Actual Benefit

事后单独展示：

```text
实际收益
```

---

# 84. Expected vs Actual

并排：

```text
Expected Saving
Actual Saving
```

---

# 85. Constraint

用户可展开：

```text
SOC
Power
Demand
Grid Limit
Reserve
Safety
```

---

# 86. Why This Plan

支持解释：

```text
为什么充电？
为什么放电？
为什么没有动作？
```

---

# 87. Optimization Infeasible

如果无解：

```text
明确显示INFEASIBLE
```

例如：

```text
“目标需量3000kW无法满足，可用ESS最大仅200kW。”
```

---

# 88. Plan Superseded

滚动再优化后旧计划：

```text
标记SUPERSEDED
```

不删除。

---

# 89. Execution Deviation

显示：

```text
Plan
vs
Actual
```

---

# 90. Safety Rejection

如果 Safety 拒绝优化命令：

```text
明确显示拒绝原因
```

而不是：

```text
“优化执行失败”
```

---

# 91. 结算页面

结构：

```text
Period
Energy
Demand
Tariff
Charges
Quality
Reconciliation
Snapshot
Revision
```

---

# 92. Settlement Status

统一：

```text
OPEN
CALCULATING
REVIEW
LOCKED
REVISED
```

---

# 93. OPEN

提示：

```text
当前数据仍可能变化
```

---

# 94. LOCKED

明确：

```text
已锁定
```

普通刷新不能改变结果。

---

# 95. REVISED

展示：

```text
Revision 2
```

并可查看：

```text
Revision 1
```

---

# 96. Settlement Lineage

用户可展开：

```text
Meter
Start Reading
End Reading
CT/PT
Tariff Version
Metric Version
```

---

# 97. Estimated / Manual

如果账单含：

```text
ESTIMATED
MANUAL
```

必须高亮提示。

---

# 98. Reconciliation

展示：

```text
Platform
Reference
Difference
Tolerance
Status
```

---

# 99. Correction

人工修正 UI：

```text
Original
Proposed
Reason
Evidence
Impact Preview
Approval
```

---

# 100. 不允许直接编辑结果单元格

不能像普通表格：

```text
双击修改金额
```

所有修正必须：

```text
Correction Workflow
```

---

# 101. 配置中心页面

核心：

```text
Resource
Version
Diff
Validate
Approval
Rollout
Desired/Reported
Rollback
```

---

# 102. Config Diff

必须：

```text
结构化展示差异
```

---

# 103. High-risk Config

例如：

```text
Safety Policy
CT/PT
Template
```

明显标风险。

---

# 104. Desired / Reported

状态：

```text
IN_SYNC
OUT_OF_SYNC
APPLYING
FAILED
```

---

# 105. Rollout

展示：

```text
Canary
Progress
Success
Failed
Paused
```

---

# 106. Config Rollback

操作前显示：

```text
目标版本
影响范围
```

---

# 107. 运维页面

可以集中：

```text
Gateway
Telemetry
MQTT
DB
Backup
Release
```

但普通能源用户：

```text
默认不可见
```

---

# 108. Role-based Navigation

不同角色：

```text
Energy Manager
Operator
Finance
Engineer
SRE
Admin
```

看到不同入口。

---

# 109. 权限不只隐藏按钮

后端仍必须：

```text
Authoritative Authorization
```

前端隐藏只是体验优化。

---

# 110. 多租户

切换 Tenant：

```text
必须明显显示当前Tenant
```

防止误操作。

---

# 111. Site Selector

Site 切换：

```text
保留明确Context
```

---

# 112. 控制操作前 Context

高风险操作再次显示：

```text
Tenant
Site
Device
```

---

# 113. Breadcrumb

建议：

```text
Tenant / Site / Asset / Device
```

---

# 114. 全局状态栏

顶部可显示：

```text
Environment
Tenant
Site
Realtime Connection
User
```

---

# 115. Production 环境

生产环境建议：

```text
明显标识
```

降低误把 Test 当 Prod。

---

# 116. 实时连接状态

WebSocket/SSE：

```text
CONNECTED
RECONNECTING
DISCONNECTED
```

---

# 117. 实时断开

断开时：

```text
页面继续显示最后数据
但必须明确STALE风险
```

---

# 118. 自动刷新

不能：

```text
刷新时重置用户筛选
```

---

# 119. Loading

区分：

```text
Initial Loading
Background Refresh
```

避免每次刷新整页闪烁。

---

# 120. Error State

错误页面必须：

```text
说明影响
提供Request ID
```

---

# 121. Request ID

错误详情：

```text
request_id
trace_id
```

方便 Runbook 排查。

---

# 122. Empty State

区分：

```text
No Data
No Permission
No Device
Filtered Empty
```

---

# 123. No Data

不能直接显示：

```text
0
```

---

# 124. Zero

0 是合法能源数据。

因此：

```text
No Data ≠ 0
```

---

# 125. Number Formatting

功率：

```text
kW / MW
```

按量级自适应。

---

# 126. Energy

```text
kWh / MWh
```

---

# 127. Money

财务：

```text
Decimal
```

并显示：

```text
Currency
```

---

# 128. Percentage

明确：

```text
0~100%
```

不要同时混用：

```text
0.75
75
```

---

# 129. Precision

同类指标统一小数位。

例如：

```text
Power 1 decimal
SOC 1 decimal
Money 2 decimals
```

具体按业务定义。

---

# 130. Unit

单位应来自：

```text
Metric / Point Contract
```

不能前端硬编码。

---

# 131. 国际化

建议：

```text
UI文案可i18n
```

但：

```text
point_code
metric_code
```

保持标准编码。

---

# 132. 可访问性

至少：

```text
Keyboard
Focus
Contrast
ARIA
```

---

# 133. Color

颜色不作为唯一语义。

例如 Alarm：

```text
红色 + CRITICAL文字
```

---

# 134. 深色模式

如支持大屏/控制室：

```text
保证图表对比度
```

---

# 135. 大屏

大屏特点：

```text
只读
高信息密度
远距离可读
```

---

# 136. 大屏禁止控制

推荐：

```text
默认不提供真实控制按钮
```

---

# 137. Mobile

如果未来支持移动端：

```text
高风险控制默认限制
```

可只提供：

```text
查看 / ACK
```

---

# 138. 表格规范

支持：

```text
Filter
Sort
Column Config
Export
Pagination
```

---

# 139. 大数据表

必须：

```text
server-side pagination
```

---

# 140. Filter

关键筛选条件：

```text
URL State
```

方便分享和回溯。

---

# 141. URL Context

例如：

```text
site
device
time range
```

可保留。

---

# 142. Export

导出需要：

```text
Time Range
Timezone
Metric Version
Generated At
```

---

# 143. 财务导出

额外：

```text
Settlement Revision
```

---

# 144. Chart Downsampling

大时间范围：

```text
后端聚合
```

不要前端下载百万点再抽样。

---

# 145. Realtime Push

推荐只推：

```text
用户当前可见/订阅数据
```

避免全量点位推送浏览器。

---

# 146. Subscription

前端：

```text
Subscribe Site/Device/Point
```

切页后：

```text
及时Unsubscribe
```

---

# 147. Browser Performance

避免：

```text
每个Point一个独立状态订阅
```

应：

```text
Batch / Store
```

---

# 148. React 状态分层

建议：

```text
Server State
UI State
Realtime State
Form State
```

分离。

---

# 149. Server State

来自：

```text
REST API
```

---

# 150. Realtime State

来自：

```text
WebSocket/SSE
```

---

# 151. UI State

例如：

```text
Drawer
Selected Tab
Filter
```

---

# 152. Form State

例如：

```text
Control Request
Config Edit
Correction Request
```

---

# 153. 乐观更新

控制、结算、配置：

```text
禁止使用误导性的Optimistic Success
```

---

# 154. Control Submission

点击后：

```text
显示Pending
```

等待服务端状态。

---

# 155. Config Save

Draft 可乐观保存。

Release：

```text
必须等待后端确认
```

---

# 156. Idempotency

前端重复点击：

```text
不能创建重复Command
```

按钮提交后：

```text
立即防重复
```

并依赖后端 Idempotency。

---

# 157. Double Click

高风险按钮：

```text
点击一次即锁定
```

直到得到明确状态。

---

# 158. Refresh 后状态

用户刷新页面：

```text
必须能从后端恢复Command真实状态
```

不能丢失执行过程。

---

# 159. Notification

全局 Toast 只适合：

```text
轻量成功/失败
```

---

# 160. 高风险结果

不能只用：

```text
3秒Toast
```

必须：

```text
Persistent Status / Timeline
```

---

# 161. Critical Banner

系统级问题：

```text
Remote Control Frozen
Telemetry Degraded
Backup Failure
```

可使用：

```text
全局Banner
```

---

# 162. Banner Scope

明确：

```text
Global
Tenant
Site
```

---

# 163. Maintenance Mode

显示：

```text
维护窗口
影响功能
预计恢复
```

---

# 164. Degraded Mode

可以显示：

```text
Analytics Degraded
History Degraded
Realtime Degraded
Control Frozen
```

---

# 165. Frontend Telemetry

前端自身监控：

```text
Page Load
API Error
JS Error
WebSocket Disconnect
```

---

# 166. 前端日志

不得写：

```text
Access Token
Password
Secret
```

---

# 167. Audit Context

高风险操作传：

```text
reason
client context
```

但最终审计由后端生成。

---

# 168. UX 与 Runbook

错误页面可提供：

```text
Runbook Reference
```

给 SRE。

普通用户不暴露复杂运维命令。

---

# 169. Control Error Codes

前端必须根据标准错误码映射：

```text
PERMISSION_DENIED
DEVICE_OFFLINE
LOCAL_MODE
OUT_OF_RANGE
SAFETY_REJECTED
COMMAND_EXPIRED
READBACK_MISMATCH
```

---

# 170. 不要只显示“操作失败”

应：

```text
明确可行动原因
```

---

# 171. Safety Rejected

例如：

```text
“当前SOC 18%，低于允许放电下限20%，命令未执行。”
```

---

# 172. Device Offline

显示：

```text
Last Seen
```

帮助判断。

---

# 173. Out of Range

显示：

```text
Requested
Allowed Min
Allowed Max
```

---

# 174. Approval Pending

显示：

```text
Approver
Requested At
Expiry
```

---

# 175. Approval Expired

必须重新提交。

---

# 176. 控制 Source

命令来源：

```text
MANUAL
OPTIMIZATION
SCHEDULE
EMERGENCY
```

前端可区分。

---

# 177. Optimization Command

显示：

```text
Plan ID
Interval
Reason
```

---

# 178. Manual Takeover

当人工接管：

```text
自动调度状态明显变更
```

---

# 179. Control Conflict

多个来源冲突：

```text
UI显示最终Authority
```

而不是多个按钮同时可用。

---

# 180. Design Token

建议统一：

```text
Spacing
Radius
Typography
Status Semantic
```

---

# 181. 状态语义 Token

至少：

```text
success
warning
danger
info
neutral
disabled
```

---

# 182. 业务状态不直接绑定颜色名

代码使用：

```text
status-critical
```

不要：

```text
red-status
```

---

# 183. Component Library

建议沉淀：

```text
MetricCard
QualityBadge
DeviceStatus
AlarmBadge
TimeRangePicker
EnergyFlow
TrendChart
CommandPreview
CommandTimeline
ApprovalPanel
VersionBadge
DiffViewer
```

---

# 184. CommandPreview 组件

必须统一：

```text
Target
Current
Requested
Allowed
Risk
Safety
```

所有控制页面共用。

---

# 185. QualityBadge

统一：

```text
GOOD
ESTIMATED
MANUAL
PARTIAL
STALE
BAD
```

---

# 186. VersionBadge

用于：

```text
Config
Template
Tariff
Metric
Model
Settlement
```

---

# 187. DiffViewer

用于：

```text
Config
Safety Policy
Template
Tariff
```

---

# 188. Empty/Error/Loading 组件

统一避免各模块自行实现不同体验。

---

# 189. 测试

UX 测试至少：

```text
Component
Accessibility
Interaction
E2E
Permission
Control Negative
Realtime Disconnect
```

---

# 190. Control E2E

必须：

```text
Preview
Submit
Pending
ACK
Readback
Verified
```

完整覆盖。

---

# 191. Negative UX Test

至少：

```text
LOCAL
Offline
No Permission
Out of Range
Safety Reject
Expired
Unknown
Readback Mismatch
```

---

# 192. Realtime Disconnect Test

断开 WebSocket：

```text
页面显示Reconnecting
数据变STALE
```

恢复：

```text
自动同步
```

---

# 193. Permission Test

无权限用户：

```text
不能通过URL直接进入控制功能
```

且后端拒绝。

---

# 194. Settlement UX Test

LOCKED 后：

```text
Late Data到达
页面结果不静默变化
```

Revision 出现：

```text
明确提示
```

---

# 195. Optimization UX Test

SHADOW：

```text
绝不出现“已执行”
```

---

# 196. Visual Regression

核心页面：

```text
Dashboard
Alarm
Control
Settlement
Optimization
```

建议做视觉回归。

---

# 197. 第一阶段 P0

```text
Global Layout
Site Context
Dashboard
Device
Point
History
Alarm
Control Preview
Command Timeline
Realtime State
Quality
```

---

# 198. P1

```text
Settlement
Topology
Forecast
Optimization
Config Diff
Approval
```

---

# 199. P2

```text
Advanced Explainability
Mobile
Personalized Dashboard
Large-screen Builder
```

---

# 200. 第一阶段 Production Gate

至少：

```text
Site Context明确

Realtime更新时间显示

STALE明确

No Data不显示0

Quality可见

Control Mode可见

Preview完整

R3/R4风险可见

ACK与VERIFIED分离

UNKNOWN不允许盲目Retry

Readback Mismatch可见

Command Timeline完整

WebSocket断开可见

Settlement Lock/Revision可见

Forecast/Fallback可见

Shadow不误导为执行

权限和后端一致
```

---

# 201. 控制 UX Gate

真实远程控制上线前：

```text
所有Negative UX Case PASS
```

---

# 202. 可用性验收

现场操作人员应能够：

```text
10秒内判断Site是否正常

30秒内找到离线设备

1分钟内找到告警来源

明确判断控制是否执行成功

明确区分预测、计划和实际
```

具体时间目标可按项目验证。

---

# 203. 前端状态总模型

```text
Data State
+
Quality State
+
Connectivity State
+
Business State
+
Control State
```

不能只靠：

```text
一个online字段
```

---

# 204. 最终页面语言

平台前端统一回答：

```text
是什么？
现在是多少？
数据是什么时候的？
可信度怎么样？
为什么这样？
下一步能做什么？
操作是否真的生效？
```

---

# 205. 最终冻结原则

第一：

```text
No Data ≠ 0
```

第二：

```text
Old Data必须显示STALE
```

第三：

```text
ACK ≠ VERIFIED
```

第四：

```text
Forecast ≠ Actual
```

第五：

```text
Plan ≠ Executed
```

第六：

```text
Expected Saving ≠ Actual Saving
```

第七：

```text
Disabled Control必须解释为什么
```

第八：

```text
UNKNOWN Command不得诱导盲目Retry
```

第九：

```text
财务/结算修改必须显示Revision
```

第十：

```text
任何高风险控制
都必须让用户明确知道
“正在控制谁、控制什么、允许范围是什么、风险是什么、最后是否真的生效”
```

最终目标不是：

```text
“页面做得漂亮”
```

而是：

```text
在数据异常、设备离线、告警爆发、控制失败、预测降级、
结算修订和自动优化等复杂情况下，
用户仍然能正确理解系统真实状态，
并做出安全、可追溯、低误操作风险的决策。
```

这才是智慧能源系统前端 UX 的核心标准。
