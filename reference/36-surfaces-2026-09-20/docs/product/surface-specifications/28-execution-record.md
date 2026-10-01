# 28 执行记录 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `28 执行记录`  
> **Route intent：** `/sites/:siteId/executions`、`/sites/:siteId/executions/:executionId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Execution`、`Attempt`、`ACK`、`Readback`、`Value Source`、`Correlation`、`Rollback` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有操作日志、旧控制记录、旧策略执行页、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Execution / Attempt / Gateway / Target / Readback / Verification / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **为所有即时控制、自动策略、Override、Rollback、批量控制和受治理的自动化执行提供一个不可含糊、可追溯、可对账的事实 Ledger，让用户能回答“谁在什么时间、基于什么授权、对什么目标提出了什么意图，系统尝试了什么，现场接受了什么，实际读回了什么，最终是否被验证、覆盖、回滚或仍然未知”。**

本 Surface 是 **authoritative control execution ledger + evidence workspace**，不是：

- 通用系统日志；
- HTTP 请求日志；
- BAS raw protocol trace viewer；
- SIEM；
- 25「控制中心」的操作按钮页；
- 26「策略中心」的 Portfolio；
- 27「策略详情」的 Engineering Definition；
- 13「功能验证」的完整 Test Result；
- 一个把所有阶段压成 `Success / Failed` 的操作记录表；
- 一个允许用户从日志页重复发送高风险命令的入口；
- 一个“网络返回 200 就表示现场成功”的页面。

用户打开一条 Execution 后应该能回答：

1. Execution ID 是什么；
2. 来源是人工控制、Strategy、Rollback、Fail-safe 还是其他 owner；
3. Requester / Actor 是谁；
4. Source system / object 是谁；
5. Target 是什么；
6. Intended command / proposed state 是什么；
7. 当时权限/Control Authority/Approval/Interlock context 是什么；
8. 是否通过 authorization；
9. 是否产生了真正 Attempt；
10. Attempt 是否送到 Gateway / Controller；
11. Target 是否 ACK / Reject / Timeout；
12. Readback 是什么；
13. 当前 Value Source / Active Priority 是谁；
14. 是否真正 Verified；
15. 是否被更高优先级 command、manual override 或 strategy supersede；
16. 是否发生 retry；谁决定 retry；
17. Multi-target 执行是否部分成功；
18. 是否进入 Unknown；
19. Unknown 后如何 reconciliation；
20. 是否发生 rollback / override / relinquish；
21. 时间线是否完整；
22. 是否需要进入 13 做 Functional Verification。

---

# 2. 主要用户

## Primary

### 值班操作员

确认刚才的操作到底发生了什么，避免把 ACK 当作现场成功。

### 控制工程师

调查 command priority、Value Source、Readback mismatch、Strategy arbitration、partial execution 与 rollback。

### HVAC / 系统工程师

把 Execution 与真实设备状态、趋势、告警和功能验证联系起来。

## Secondary

- Commissioning Engineer：从 Execution 进入 13 Functional Verification；
- Facility Manager：审查高风险操作和责任链；
- OT Security / Platform Owner：调查 authorization、gateway、runtime、attempt 和异常；
- Auditor：检查 actor、reason、approval、before/after、attempt 与 final outcome；
- Diagnosis / Work / Strategy 用户：把 execution 当作 evidence，而不是人工描述。

---

# 3. 外部最佳实践依据

## 3.1 NIST SP 800-82 Rev.3 — Execution 是 OT 物理变更事实

NIST SP 800-82 Rev.3 将 Building Automation Systems 纳入 OT。OT 系统会通过 monitoring / control 直接影响物理环境，因此控制记录必须同时考虑 performance、reliability 和 safety。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

**本页采用：**

- Execution 不是普通 CRUD audit；
- command lifecycle 不做 optimistic completion；
- request timeout 不自动解释为 target failure；
- high-risk retry 不由浏览器自动发起；
- actor、authorization、target、attempt、result 和 evidence 必须独立；
- immutable audit / reconciliation 是正式产品能力。

## 3.2 BACnet Command Prioritization — ACK 不等于当前值由本次命令控制

BACnet commandable object 使用 `Priority_Array` 和 `Relinquish_Default`。当前 Present_Value 由最高优先级有效 command 决定，因此一个低优先级 write 即使被接受，也可能不成为当前有效值。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-1995-135b.pdf
- https://bacnet.org/wp-content/uploads/sites/4/2022/06/NISTIR-6392.pdf

**本页采用：**

```text
ACK
≠ Active Value
≠ Active Command Source
```

Execution detail 可展示：

```text
Requested Priority
Target ACK
Current Command Priority
Present Value
Value Source
```

但 protocol-specific facts 必须由 protocol/control owner 提供，不能前端猜。

## 3.3 BACnet Value Source — 当前值来源是独立事实

BACnet Value Source mechanism 用于标识当前 Present_Value 的来源 device/object，并与 command prioritization 联动。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-135-2012as.pdf

**本页采用：**

```text
Readback Value
≠ Value Source
```

Execution detail 需要区分：

```text
你请求了什么
当前读回是什么
当前值是谁在控制
```

## 3.4 BACnet Audit Reporting — 一次操作可能有多个来源/目标审计事件

BACnet Addendum 135-2016bi 引入 interoperable Audit Reporting。Operation source 与 operation target 都可以记录 auditable action；consumer 需要把一个动作的多条记录关联起来。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-135-2016bi.pdf
- https://bacnet.org/addenda/

**本页采用：**

```text
Execution
≠ One Log Line
```

一条 execution 可以关联：

```text
Request record
Source-side attempt record
Gateway delivery record
Target-side record
Readback record
Verification record
Rollback / override record
```

Execution owner 负责 correlation，前端不按时间戳相近自行拼接。

## 3.5 DOE OpenBuildingControl — 设计、部署、执行、验证要可连接

DOE/LBNL OpenBuildingControl 将 control design、performance evaluation、implementation 和 verification 连成工程链。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control

**本页采用：**

- Execution 必须能回到 Strategy Version / Control Intent；
- runtime implementation 不能与设计版本脱钩；
- execution evidence 必须能进入 Functional Verification；
- “执行完成”不等于“设计意图验证通过”。

## 3.6 ASHRAE Guideline 36-2024 — sequence implementation 需要 functional test

Guideline 36-2024 同时强调 HVAC efficiency、control stability、real-time FDD，并描述 functional tests 用于确认 sequence implementation。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

```text
Execution Completed
≠ Functional Verification PASS
```

28 提供 execution evidence，13 才是 requirement-driven verification owner。

## 3.7 ISA-101 — HMI 应支持 situational awareness 与低误操作

ISA-101 覆盖 HMI navigation、dynamic elements、security、historical interfaces 与 operator workflow。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101

**本页采用：**

- timeline 必须按业务阶段而不是技术噪声组织；
- abnormal / unknown / partial 优先于绿色 success；
- 状态不能只靠颜色；
- operator 不需要读 protocol packet 才能理解发生了什么。

---

# 4. 产品语言契约

主界面中文优先：

```text
执行记录
执行编号
来源
请求人
目标
意图
授权
执行尝试
网关投递
目标响应
读回值
当前值来源
验证结果
部分执行
状态未知
对账
回滚
覆盖
释放控制
时间线
```

可以保留：

```text
Execution
Attempt
ACK
Readback
Value Source
Correlation ID
Rollback
Override
```

不要默认主界面显示：

```text
HTTP 200
RPC OK
MQTT Delivered
BACnet WriteProperty ACK
Modbus Function Code 06
```

这些属于 Advanced protocol evidence。

---

# 5. Execution Domain Vocabulary

## Execution

一次完整、可追踪的控制执行生命周期。它可以来自人工 control intent、Strategy、Rollback、Fail-safe、approved automation 或其他受治理 owner。

## Execution ID

Execution 的全局/站点唯一 durable identifier。

## Correlation ID

用于关联跨服务、Gateway、Controller、Audit Reporter、Verification 等多条技术/业务事件的 owner-defined correlation identity。

`Correlation ID` 不等于 `Execution ID`；一个 Execution 可能包含多个 correlation / attempt。

## Request

提出“希望系统做什么”的业务意图。

## Authorization

服务端对 Principal、Scope、Permission、Approval、Control Authority policy 等进行的授权决策。

## Attempt

Execution owner 真正尝试向下游执行目标发送 command / mutation 的一次动作。

## Gateway Delivery

Attempt 是否到达指定 Gateway / Runtime / Controller boundary。

## Target Response / ACK

目标 protocol/runtime 对 command attempt 的接受、拒绝或响应事实。

## Readback

来自 authoritative device/control source 的状态/值回读。

## Value Source

当前读回值的有效控制来源（协议/控制 owner 支持时）。

## Verified Result

owner-defined verification contract 对现场行为的确认；不等于 ACK/readback。

## Reconciliation

当 execution 状态未知、跨系统记录不一致或响应丢失时，根据 durable IDs 和 authoritative owners 对齐真实状态。

---

# 6. Mandatory Semantic Separation

```text
Execution ≠ Attempt

Request ≠ Authorization
Authorization ≠ Attempt
Attempt ≠ Gateway Delivery
Gateway Delivery ≠ Target ACK
ACK ≠ Readback
Readback ≠ Verified Result

HTTP 200 ≠ Target ACK
Transport Delivered ≠ Device Executed

ACK ≠ Active Value
Readback Value ≠ Value Source

Requested Priority ≠ Current Command Priority

Execution Completed ≠ Functional Verification PASS
Functional Verification PASS ≠ Verified Savings

Timeout ≠ Failure automatically
Timeout ≠ Success automatically

Retry Attempt ≠ New Execution automatically
New Execution ≠ Retry Attempt automatically

Partial Execution ≠ Success
Partial Execution ≠ Failure of every target

Unknown ≠ Failed
Unknown ≠ Successful

Rollback Requested ≠ Rollback Attempted
Rollback Attempted ≠ Restored
Rollback ACK ≠ Restored State

Override Applied ≠ Base Strategy Changed
Relinquish ≠ Write Default Value

Superseded ≠ Failed
Preempted ≠ Rejected

Audit Event ≠ Execution
Correlation ≠ Causation
```

---

# 7. Primary Questions

28 默认必须回答：

1. 这是哪一次 Execution；
2. 从哪里发起；
3. 谁发起；
4. 为什么发起；
5. 目标是什么；
6. Intended command 是什么；
7. 当时 Current State 是什么；
8. authorization decision 是什么；
9. 是否真正 Attempt；
10. 一共几次 Attempt；
11. 每次 Attempt 谁发起、何时、为什么；
12. Gateway 是否投递；
13. Target 是否响应；
14. ACK 是否代表接受还是仅 protocol-level response；
15. Readback 是什么；
16. Value Source / current priority 是什么；
17. 是否符合 Intended state；
18. 是否完成 owner-defined verification；
19. 是否被 override / higher priority / strategy supersede；
20. 是否 partial；
21. 是否 unknown；
22. unknown 后如何 reconciliation；
23. 是否 rollback；
24. timeline 是否完整；
25. 是否需要进入 13 做进一步验证。

---

# 8. Information Architecture

```text
Context Header
↓
Execution Ledger
                             → Execution Inspector
↓
Selected Execution
  Identity / Source / Actor / Reason
  Intended Command
  Authorization Snapshot
  Target Summary
↓
Execution Timeline
  Requested
  Authorized / Rejected
  Attempted
  Gateway Delivery
  Target ACK / Reject / Timeout
  Readback
  Value Source / Priority
  Verification
  Supersede / Override / Rollback
  Reconciliation
↓
Multi-target Result Matrix（若适用）
↓
Related Evidence
  Device / Trend / Alarm / Strategy / Work / Verification
↓
Advanced Protocol / Audit Evidence
```

默认 **Ledger-first + single execution evidence workspace**。

---

# 9. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/executions
/sites/:siteId/executions/:executionId
```

Search Params 可包括：

```text
source
actor
target
status
outcome
unknown
partial
strategyId
commandType
from
to
selectedExecution
```

不进入 URL：

```text
hover
local timeline expansion
protocol detail disclosure
confirmation dialog
```

Execution ID 必须 durable / shareable。

---

# 10. Entry / Source Contract

可从：

```text
25 控制中心
26 策略中心
27 策略详情
13 功能验证
09 告警中心
10 诊断中心
12 工单详情
```

进入。

Deep-link 可以携带：

```text
executionId
attemptId
strategyId
strategyVersion
verificationId
deviceId
timeWindow
```

来源 URL 不改变 Execution truth。

---

# 11. Execution Identity Contract

每条 Execution 至少有：

```text
Execution ID
Site
Source Type
Source ID / Version
Requester / Actor
Created At
Reason
Target Count
Command Type
Risk Class（若 owner 定义）
Current Execution State
Final Outcome（若已最终）
```

`Execution ID` 一旦建立不可复用。

---

# 12. Execution Source Contract

Source Type 可以是：

```text
Manual Control
Strategy
Optimization Deployment
Override
Release / Relinquish
Rollback
Fail-safe
Work / Commissioning Test
External Authorized System
```

必须保留：

```text
Source ID
Source Version / Revision
Source Owner
```

例如：

```text
Strategy STR-18 v7
```

不能只有：

```text
source = automation
```

---

# 13. Requester / Actor Contract

至少区分：

```text
Human Principal
Service Principal
Strategy Runtime
Gateway
Controller
External Authorized System
```

Human request 应可追溯：

```text
User identity
Role / site scope snapshot
Reason
Client / session reference（若 owner 允许）
```

不要将内部账号 secret 暴露给业务用户。

---

# 14. Reason Contract

高风险人工操作应记录 structured reason / operator note。

Reason 不能替代：

```text
Approval
Authorization
Work Order
Strategy source
```

策略执行的 reason 可以是：

```text
Trigger condition
Schedule window
DR event
Fail-safe condition
Rollback trigger
```

且必须引用 authoritative source event。

---

# 15. Intended Command Contract

执行记录必须先表达业务意图：

```text
Target
Current State at Request
Proposed State / Command
Unit / Mode
Priority / Authority request
Expiry（若 override）
Impact Scope
```

而不是先展示 raw protocol payload。

例如：

```text
目标：CHWS Setpoint
请求：6.8°C → 7.2°C
来源：人工控制
Priority：Operator Override
Expiry：60 min
```

---

# 16. Request State Contract

Request 阶段建议：

```text
Created
Submitted
Rejected Before Authorization
Cancelled Before Attempt
```

Request created 不表示任何物理动作发生。

---

# 17. Authorization Contract

Authorization snapshot 至少可以引用：

```text
Principal
Site Scope
Permission
Control Authority Policy
Approval Reference
Execution Window
Interlock / Precondition snapshot reference
Decision
Decision Reason
Evaluated At
Policy Version
```

Authorization 由 backend owner 给出。

UI 不自行重算授权。

---

# 18. Authorization Result Contract

建议：

```text
Authorized
Rejected
Expired
State Unknown
```

如果 authorization service 不可用：

```text
State Unknown / unavailable
```

不能：

```text
authorization unavailable → authorized
```

---

# 19. Attempt Contract

每次真正的下游执行尝试都产生独立 `Attempt ID`。

至少记录：

```text
Attempt ID
Execution ID
Attempt Number
Initiated By
Reason
Started At
Target(s)
Gateway / Runtime
Protocol Adapter
Command Payload Reference
Attempt State
Completed At
```

Attempt history 不覆盖。

---

# 20. Retry Contract

Retry 必须区分：

```text
Retry within same Execution
```

和：

```text
New Execution after reconciliation / operator decision
```

谁可以 retry、什么错误允许 retry、最大次数、delay、idempotency contract 属于 **execution owner**。

浏览器默认不自动 retry 高风险 Attempt。

如果 owner 服务端执行受控 retry，28 必须完整展示每次 Attempt。

---

# 21. Retry Reason Contract

每次 retry 必须说明：

```text
Automatic by execution owner
Manual after reconciliation
Gateway retry policy
Target busy / transient failure
```

不能只显示：

```text
Attempt #2
```

而不知道为什么出现。

---

# 22. Idempotency Contract

Execution owner 需要定义：

```text
Execution ID
Attempt ID
Idempotency Key / Command Identity（若适用）
```

前端不通过：

```text
same target + same value + close timestamp
```

猜 duplicate。

---

# 23. Gateway Delivery Contract

Gateway / Runtime boundary 至少区分：

```text
Not Sent
Queued
Sent
Delivered to Gateway
Gateway Accepted
Gateway Rejected
Gateway Timeout
Gateway State Unknown
```

`Delivered to Gateway` 不表示 target device 已执行。

---

# 24. Target Response Contract

Target response 可以是：

```text
ACK / Accepted
Rejected
Busy
Unsupported
Write Access Denied
Timeout
Protocol Error
Target Unavailable
State Unknown
```

protocol-specific code 可以作为 detail，但主界面先翻译成业务语义。

---

# 25. ACK Contract

ACK 只表示 owner-defined target/protocol acceptance。

```text
ACK
≠ Readback
≠ Active Value
≠ Device Behavior Verified
```

例如 BACnet：

```text
Write ACK
```

只说明 write operation 被接受，不能证明该 priority 成为当前有效 source。

---

# 26. BACnet Priority Evidence Contract

当 target 支持 commandable priority evidence 时，可显示：

```text
Requested Priority
Priority Array reference
Current Command Priority
Relinquish state
Present Value
```

但：

```text
Requested Priority
≠ Current Command Priority
```

如果高优先级 command 存在，当前值可能与本次 request 不同。

---

# 27. Value Source Contract

支持 Value Source 的对象应显示：

```text
Readback Value
Current Value Source
Value Source Device / Object
Current Command Priority
Observed At
```

如果 owner 不支持 Value Source：

```text
Value Source unavailable
```

不能从 actor / last writer 猜。

---

# 28. Readback Contract

Readback 至少记录：

```text
Target
Value / State
Unit / Enum
Observed At
Freshness
Quality
Source
```

Readback 是设备/控制 owner 的 authoritative observation。

不能用 optimistic UI state 当 Readback。

---

# 29. Readback Matching Contract

如果 control owner 定义 expected readback：

```text
Expected
Observed
Tolerance / Matching rule
Result
```

状态可为：

```text
Matched
Mismatch
Pending
Unavailable
Inconclusive
```

前端不自行用 `===` 判断所有控制类型。

---

# 30. Verified Result Contract

Verified Result 是比 Readback 更高一层的 owner-defined behavior confirmation。

可能来自：

```text
Control execution verifier
Commissioning / Functional Verification
Strategy runtime verification
Device owner rule
```

必须记录：

```text
Verification Method
Criteria / Version
Evidence
Result
Verified At
Owner
```

如果完整验证属于 13，则 28 显示引用，不复制结果逻辑。

---

# 31. Execution State Model

建议 Execution 聚合状态：

```text
Requested
Authorization Pending
Rejected
Authorized
Attempting
Awaiting Target Response
Awaiting Readback
Awaiting Verification
Completed
Partially Completed
Failed
Cancelled
Superseded
Rolled Back
State Unknown
Reconciliation Required
```

这些状态只是 summary；底层 timeline 仍保留各阶段事实。

---

# 32. Final Outcome Contract

Final Outcome 与 lifecycle state 分开。

可定义：

```text
Verified Success
Completed Without Full Verification
Rejected
Failed Before Delivery
Target Rejected
Readback Mismatch
Verification Failed
Partially Successful
Superseded
Rolled Back
Cancelled
Inconclusive
Unknown
```

不要只有：

```text
SUCCESS / FAILED
```

---

# 33. Partial Execution Contract

Multi-target / batch command 必须支持 partial outcome。

例如：

```text
Execution EX-1022
4 targets

AHU-01  Verified
AHU-02  ACK but readback pending
AHU-03  Rejected by interlock
AHU-04  Gateway timeout
```

聚合状态：

```text
Partially Completed
```

不能：

```text
3/4 success
→ Success
```

也不能一项失败就说所有目标失败。

---

# 34. Multi-target Result Matrix Contract

至少列：

```text
Target
Requested
Authorization
Attempt
ACK
Readback
Value Source
Verification
Final Target Outcome
```

每个 target 可以拥有独立 Attempt / Retry / Reconciliation。

---

# 35. Atomic vs Non-atomic Contract

如果 execution owner 支持 atomic transaction，需要明确：

```text
Atomic
```

否则默认不要暗示 all-or-nothing。

对于非原子 multi-target execution，应明确：

```text
Partial change possible
```

前端不能因为一个 API request 包含多个 target 就假定事务原子性。

---

# 36. Execution Unknown Contract

以下情况可能进入 Unknown：

```text
Request sent, response lost
Gateway accepted, target response unavailable
Controller reconnect before final response
Audit streams inconsistent
Readback unavailable after attempt
```

Unknown 是正式状态。

```text
Unknown ≠ Failed
Unknown ≠ Successful
```

---

# 37. Reconciliation Contract

Reconciliation 可以使用：

```text
Execution ID
Attempt ID
Gateway operation ID
Target audit record
Protocol transaction reference
Readback
Value Source
Runtime execution record
```

最终生成：

```text
Reconciled Outcome
Reconciled At
Evidence
Owner
```

前端不通过“当前值看起来一样”自行宣布成功。

---

# 38. Correlation Contract

跨系统事件关联由 Execution / Audit owner 提供明确 identity / relation：

```text
Execution ID
Correlation ID
Attempt ID
Gateway operation ID
Target audit ID
Verification ID
```

不能：

```text
timestamps within 2 seconds
+ same target
→ same execution
```

---

# 39. Correlation ≠ Causation

即使某 telemetry 变化紧随 command：

```text
command at 10:00:00
value changed at 10:00:02
```

也不能由 UI 自动宣布：

```text
command caused change
```

除非 control/verification owner 明确关联。

---

# 40. Strategy Arbitration Evidence Contract

Strategy execution 应可引用：

```text
Strategy ID / Version
Trigger snapshot
Eligibility
Arbitration group
Candidate strategies
Winner / loser
Priority / rule
Control Authority result
```

28 不重新计算 arbitration，只显示 owner evidence。

---

# 41. Superseded / Preempted Contract

执行可能被：

```text
Higher Priority Command
Manual Override
Newer Strategy Output
Fail-safe
Local Control
```

supersede / preempt。

这不一定是 Failure。

例如：

```text
ACK = Yes
Readback initially matched
2 min later manual override takes control
```

Execution 可以是：

```text
Completed → Superseded
```

需要保留两段事实。

---

# 42. Override Contract

Override execution 至少记录：

```text
Override Target
Value / Mode
Priority / Source
Actor
Reason
Start
Expiry
ACK
Readback
Current Value Source
End / Expire / Release event
```

Override Apply 和 Release 是不同 execution / lifecycle relation。

---

# 43. Relinquish Contract

释放控制必须记录：

```text
Target
Priority / Source being relinquished
Attempt
ACK
Post-relinquish Present Value
Post-relinquish Value Source
```

不能写成：

```text
Reset to Normal
```

因为 post-relinquish 可能由另一个 priority / schedule / strategy 接管。

---

# 44. Rollback Contract

Rollback execution 至少关联：

```text
Original Execution / Deployment
Rollback Reason
Rollback Target
Rollback Intent
Attempt(s)
ACK
Readback / Runtime State
Verification
```

```text
Rollback ACK
≠ Restored State
```

---

# 45. Fail-safe Execution Contract

Fail-safe 触发也必须产生 execution/evidence：

```text
Trigger condition
Strategy / Control Version
Fail-safe action
Targets
Attempt
Readback
Final source
Verification
```

不能把 fail-safe 当隐式后台行为完全不留痕。

---

# 46. Timeline Contract

Timeline 按业务阶段组织：

```text
10:02:11 Requested
10:02:11 Authorization Passed
10:02:12 Attempt #1
10:02:12 Gateway Accepted
10:02:13 Target ACK
10:02:14 Readback 7.2°C
10:02:14 Value Source = Operator Override
10:04:20 Verification Passed
```

技术 packet trace 进入 Advanced。

---

# 47. Immutable Timeline Contract

已发生 timeline event 不允许前端覆盖或重写。

如果后续 reconciliation 改变解释：

```text
10:02 Target State Unknown
10:10 Reconciled: target accepted at 10:02:13
```

保留原 Unknown event，并增加 reconciliation event。

不能把历史改成“当时一直知道成功”。

---

# 48. Timestamp Contract

每个事件应尽可能明确：

```text
Occurred At
Recorded At
Source Clock
Timezone / UTC
Clock Quality（若 owner 支持）
```

跨系统 timeline owner 负责 clock alignment。

前端不按本地浏览器时间修正 OT event ordering。

---

# 49. Out-of-order Event Contract

如果晚到 audit/readback event 补入：

- 根据 authoritative occurredAt 排序；
- 标记 late arrival（若相关）；
- 不丢弃；
- 不因为 UI 已显示 final 就拒绝更新 evidence。

但 final outcome revision 必须可追踪。

---

# 50. Audit Contract

至少记录：

```text
Who / What Actor
When
Source
Target
Intent
Authorization
Attempt
Before / After（可得时）
Reason
Approval reference
Execution ID
Attempt ID
Result
```

BACnet Audit Reporting 的 source-side / target-side records 可以作为 protocol evidence，但产品层不要求用户理解 Audit Reporter object。

---

# 51. Audit Source / Target Contract

正式区分：

```text
Operation Source
Operation Target
Logger
Viewer
```

这与 BACnet audit architecture 一致。

同一个 action 可以有 source-side 和 target-side record；Execution owner 负责关联。

---

# 52. Audit Record ≠ Business Execution

一个 Execution 可以对应多条 audit record。

一条 audit record 也可能只是：

```text
attempted unauthorized change
```

而没有成功 Execution。

因此 28 不直接把 audit table 当 execution ledger。

---

# 53. Unauthorized / Rejected Attempt Contract

授权失败也应保留受控记录：

```text
Execution / Request ID
Actor
Target
Requested Action
Authorization Result = Rejected
Reason category
Timestamp
```

敏感 security detail 只对授权角色显示。

无 Attempt 时明确显示：

```text
未向现场发送命令
```

---

# 54. Device / Trend Evidence Contract

28 可以关联：

```text
Device Detail
Trend Analysis
Alarm
Diagnosis
Work
Functional Verification
```

Trend deep-link 应保留：

```text
Target
Execution timestamp
Evidence window
Relevant points
```

28 不自己实现完整趋势分析。

---

# 55. Alarm Relationship Contract

Execution 之后发生 Alarm：

```text
Execution → Alarm
```

只有 owner 提供 relation 时才显示关联。

不能仅因时间接近就说“本次控制导致告警”。

---

# 56. Diagnosis Relationship Contract

Diagnosis 可以引用 Execution 作为 evidence。

但：

```text
Execution failed
≠ Root Cause
```

可能还有 target、gateway、interlock、data、device、strategy 等多种原因。

---

# 57. Functional Verification Boundary

28 回答：

> 这次 execution 各阶段发生了什么？

13 回答：

> 系统行为是否满足明确 requirement？

因此：

```text
Readback matched
≠ Functional Verification PASS
```

完整测试结果进入 13。

---

# 58. M&V Boundary

Execution 可以成为 24 M&V 的 implementation / operational evidence。

但：

```text
Execution Verified
≠ Savings Verified
```

28 不显示权威 savings result。

---

# 59. Ledger Contract

默认 Execution Ledger 列：

```text
时间
执行编号
来源
Actor
目标
意图
Execution State
ACK / Readback
Final Outcome
验证
```

必要时可增加：

```text
Strategy Version
Risk Class
Partial / Unknown indicator
```

不默认放 protocol code 列。

---

# 60. Default Prioritization Contract

默认排序优先：

```text
State Unknown / Reconciliation Required
Partial Execution
Verification Failed
Readback Mismatch
Target Rejected
High-risk recent control
Recent normal executions
```

而不是纯 `createdAt DESC`。

但最终排序 policy 可以由 Execution owner / product policy 定义。

---

# 61. Execution Inspector Contract

Inspector 快速显示：

```text
Execution ID
Source / Actor
Target(s)
Intent
Authorization
Attempt count
ACK summary
Readback summary
Value Source
Final Outcome
Verification
Next action
```

然后进入完整 Execution detail。

---

# 62. Execution Detail Contract

完整页面包含：

```text
Identity / Source
Intent
Authorization Snapshot
Targets
Timeline
Attempt Evidence
Gateway Evidence
Target Evidence
Readback / Value Source
Verification
Supersede / Override / Rollback
Related Evidence
Advanced Audit / Protocol
```

不做一个 100 字段 JSON dump。

---

# 63. Advanced Protocol Evidence Boundary

高级层可以展示：

```text
Protocol
Gateway
Object / Property reference
Priority
Transaction / Invoke ID
Error class / code
Raw response reference
```

但：

- 默认折叠；
- 需要专业权限；
- 不暴露 secrets/credentials；
- 不允许从该层直接重放命令。

---

# 64. No Replay Command From Log

明确禁止：

```text
[Retry]
[Replay]
```

直接出现在旧 Execution 详情上执行原命令。

如果用户要重新执行，必须回到 25 / owner workflow 创建新的受治理 intent，或由 execution owner 在明确 reconciliation/retry policy 下产生 Attempt。

---

# 65. Snapshot + Event Stream Contract

Execution Detail 使用：

```text
Snapshot
+
Execution Event Stream
```

Snapshot 提供当前聚合状态；Stream 增量追加：

```text
Authorization
Attempt
Gateway
ACK
Readback
Verification
Reconciliation
Supersede / Rollback
```

Stream 不删除历史 event。

---

# 66. Stream Disconnect Contract

```text
Execution stream disconnected
≠ Execution Failed
```

显示：

```text
执行状态实时更新暂不可用
最后同步：20:14:23
```

恢复后通过 Snapshot + event reconciliation 补齐。

---

# 67. Reconnect Contract

重新连接时：

- 先 fetch authoritative snapshot；
- 根据 event cursor / sequence 补 events；
- 去重由 owner identity 决定；
- 不按 display text 去重；
- 不重新发 command。

---

# 68. Event Ordering Contract

Execution owner 提供：

```text
Event ID
Sequence / revision（若支持）
Occurred At
Recorded At
```

前端不只按 arrival time 判断业务顺序。

---

# 69. Query / Read Model Contract

Ledger 使用服务端 execution summary read model。

禁止：

```text
100 executions
→ 100 actor queries
→ 100 target queries
→ 100 verification queries
→ 100 strategy queries
```

summary 至少包含：

```text
identity
source
actor
target summary
intent summary
state
ack/readback summary
outcome
verification
partial/unknown
```

Detail heavy protocol evidence 再 lazy-load。

---

# 70. Retention / Archive Contract

Execution history retention 由 Audit / OT governance owner 决定。

UI 不自行删除 old executions。

Archived 不等于 erased。

如果某类 raw protocol evidence retention 较短，产品要明确：

```text
Business Execution retained
Raw Protocol Evidence expired per policy
```

---

# 71. Security Boundary Contract

客户端仅做 affordance。

Execution read 权限需要 backend 验证：

```text
Principal
Site Scope
Target Scope
Execution Risk / sensitivity
Audit permission
Protocol-detail permission
```

28 默认是只读事实页，不提供绕过 owner workflow 的 mutation。

---

# 72. Privacy / Sensitive Audit Contract

Execution 可能包含：

```text
User identity
Reason
Network/controller identifiers
Security rejection details
```

应按角色最小化展示。

业务用户不需要看到 credential、token、secret、certificate private data。

---

# 73. AI Assistance Boundary

AI 可以：

```text
总结 Execution timeline
解释 ACK 与 Readback 差异
解释为什么 Execution partial
解释 Value Source / Priority evidence
整理 unknown reconciliation evidence
总结 rollback / override chain
草拟 incident note
```

AI 不能：

```text
自动 retry 命令
自动宣布 unknown = failed/success
自动把 telemetry 相关性写成因果
自动把 ACK 升级成 verified
自动改写 immutable timeline
自动创建 rollback
自动隐藏 failed attempts
```

---

# 74. No Defensive Programming / No Compatibility Design

明确禁止：

```text
execution API error → []

missing execution state → failed
missing execution state → success

HTTP 200 → ACK
HTTP 200 → control success

transport delivered → target executed

ACK → readback
ACK → verified

readback match → functional verification passed

value source unavailable
→ assume requester is active source

priority unavailable
→ assume requested priority active

request timeout
→ failed
request timeout
→ auto retry

unknown
→ failed
unknown
→ success

one target failed
→ entire multi-target failed

3/4 targets success
→ overall success

same target + same value + nearby timestamp
→ duplicate execution

same timestamp
→ correlate audit events

telemetry changed after command
→ command caused it

reconciliation unavailable
→ use current value as proof

rollback ACK
→ restored

relinquish
→ write default value

strategy execution success
→ strategy verified

execution verified
→ savings verified

stream disconnect
→ execution failed

old Operation Log fallback
legacy Control History compatibility adapter
raw protocol log as primary UX
```

正式原则：

> **一个 Execution 对应一个权威、可追溯的生命周期。Request、Authorization、Attempt、Delivery、ACK、Readback、Value Source 和 Verification 是不同事实。Unknown 不是 Failure，Partial 不是 Success，Retry 必须有 owner，Timeline 不可篡改，Correlation 不等于 Causation。**

---

# 75. Accessibility Contract

- Execution Ledger 使用 semantic table；
- State / outcome 不只靠颜色；
- Timeline 使用语义化列表；
- keyboard 可选择 execution、展开 stage、打开 evidence；
- protocol detail 默认折叠且有明确 heading；
- stream updates 不抢 focus；
- 新 event 不频繁 live-announce；
- partial/unknown 有文字；
- 768px 下仍能查看 Execution ID、Source、Target、State、ACK/Readback、Outcome；
- 无 page-level 横向 overflow。

---

# 76. Browser Acceptance Criteria

## Execution Truth

- Request / Authorization / Attempt 分开；
- Attempt / Delivery / ACK 分开；
- ACK / Readback / Verification 分开；
- timeout 不自动算 Failed；
- unknown 是正式状态。

## BACnet / Control Evidence

- Requested Priority 与 Current Command Priority 分开；
- Value Source unavailable 不猜；
- Readback 不冒充 Value Source；
- relinquish 不写成“恢复默认值”。

## Multi-target

- 每个 target 独立 outcome；
- partial 可见；
- 非 atomic 不假装 transaction；
- 一个 target 失败不改写其他 target 事实。

## Retry / Reconciliation

- 浏览器不自动 retry；
- owner retry 每次产生 Attempt；
- unknown 通过 durable ID reconciliation；
- reconciliation 不覆盖原历史 event。

## Correlation / Audit

- Execution 与 Audit Event 分开；
- source-side / target-side records 可关联；
- 不按 timestamp heuristic 合并；
- timeline immutable。

## Verification

- readback match 不称 Functional Verification PASS；
- 13 是完整 requirement verification owner；
- execution verified 不称 savings verified。

## Accessibility / Responsive

- semantic table；
- timeline 可键盘浏览；
- status 不只靠颜色；
- 768px 保留核心事实；
- 无全局横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无旧 Operation Log 兼容层；
- 无 log-page command replay；
- 无前端自动 retry 高风险 command；
- 无前端 correlation heuristic；
- 无 N+1 actor/target/verification 查询；
- review scenario 无 runtime/network error。

---

# 77. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 执行记录 · 中央冷站                                 近 24 小时 · 全部来源 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 状态未知 1   部分执行 2   验证失败 1   读回不一致 2   最近执行 46          │
├──────────────────────────────────────────────────────────────────────────────┤
│ 时间      执行编号     来源       目标              状态          结果       │
│ 20:14:22  EX-2048     人工控制   CHWS SP           等待验证      读回匹配   │
│ 20:02:11  EX-2047     STR-18 v7  CH-01..04         部分完成      3/4 已确认  │
│ 19:51:03  EX-2046     Override   AHU-03 SAT SP     状态未知      对账中     │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中：EX-2048                                 │ 快速判断                     │
│ 来源：人工控制 · operator.zhang              │ Target：CHWS Setpoint        │
│ Reason：低负荷调试                            │ Request：6.8 → 7.2°C         │
│ Authorization：通过                          │ Priority：Operator Override  │
│ Attempts：1                                  │ ACK：已接受                  │
│ Readback：7.2°C @ 20:14:24                  │ Value Source：Operator       │
│ Verification：待完成                         │                              │
├──────────────────────────────────────────────────────────────────────────────┤
│ 时间线                                                                         │
│ 20:14:22.118  Requested      7.2°C · expiry 60 min                           │
│ 20:14:22.231  Authorized     Control policy v12 · interlocks clear           │
│ 20:14:22.410  Attempt #1     Gateway GW-02                                  │
│ 20:14:22.552  Gateway        Accepted                                        │
│ 20:14:22.731  Target ACK     BACnet command accepted                         │
│ 20:14:24.008  Readback       7.2°C · GOOD                                   │
│ 20:14:24.020  Value Source   Operator override · current priority owner      │
│ 20:14:24.100  Verification   Pending                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ [打开设备] [趋势证据] [功能验证] [查看高级审计证据]                           │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任和信息层级，不是像素规范。

---

# 78. Explicit Non-goals

28 不是：

- raw packet sniffer；
- SIEM；
- generic application log viewer；
- 直接命令重放工具；
- 控制按钮页；
- Strategy authoring；
- Functional Verification result authoring；
- 告警中心；
- M&V；
- “Success/Fail” 两状态日志。

---

# 79. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Execution / Attempt / Correlation identity 明确；
- Request / Authorization / Attempt 分离；
- Gateway / Target / ACK 分离；
- Readback / Value Source / Priority 分离；
- Verification boundary 明确；
- Multi-target / Partial Execution 明确；
- Atomic / Non-atomic 边界明确；
- Unknown / Reconciliation 明确；
- Retry owner / No Automatic Browser Retry 明确；
- Strategy Arbitration evidence 明确；
- Override / Relinquish / Rollback 明确；
- Immutable Timeline 明确；
- Audit source/target/correlation 明确；
- Functional Verification / M&V 边界明确；
- Security / privacy / audit 边界明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
