# 25 控制中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `25 控制中心`  
> **Route intent：** `/sites/:siteId/control`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`ACK`、`Readback`、`Interlock`、`Override`、`BACnet Priority Array` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有命令页、旧 Commands 页面、点位写入表单、旧 Ant/ProComponents 控制台或历史设计稿。现有代码只能在实施阶段作为真实 Target / Control Authority / Permission / Interlock / Command / Readback / Verification / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **在当前授权范围内，帮助操作员理解“谁正在控制、现在是什么状态、哪些即时控制可执行、执行会影响什么、执行后到底发生了什么”，并以可审计、不可误导的方式完成一次安全控制交互。**

本 Surface 是 **high-risk operational control workspace**，不是：

- BAS 原始点位编辑器；
- 任意 `writable=true` 属性的通用写入工具；
- 26「策略中心」；
- 27「策略详情 / 仿真 / 审批」；
- 22「优化方案」的执行捷径；
- 设备详情里的“万能控制 Tab”；
- 前端自己解释 BACnet 优先级、interlock 或控制权限的页面；
- 一个“点击按钮 → Toast 控制成功”的页面；
- life-safety / fire / smoke control 的通用替代 HMI；
- 允许绕过现场保护、联锁或设备本地控制的后门。

用户离开本页前应该知道：

1. 当前控制对象 / Scope 是什么；
2. 当前 Mode、Setpoint、Schedule、Override 和 Control Authority Source 是什么；
3. 当前哪些命令可用，哪些不可用，原因是什么；
4. 当前是否存在更高优先级命令、保护逻辑、interlock 或 local/manual source；
5. 即将执行的 Target / Current State / Proposed State 分别是什么；
6. 执行前 Preconditions 是否满足；
7. 影响范围和潜在风险是什么；
8. Override 是否有 Priority / Source / Expiry / Reason；
9. Command 当前处于 Requested、Attempted、ACK、Readback、Verified 或其他状态；
10. ACK 是否真正成为 active command；
11. Readback 是否与 Requested 一致；
12. 是否需要 Functional Verification；
13. 如果执行失败、超时或状态未知，下一步应该做什么；
14. 谁在何时执行、批准、释放或覆盖了该控制；
15. 哪些操作必须转入 27 策略详情、28 执行记录或 13 功能验证。

---

# 2. 主要用户

## Primary

### HVAC / 控制操作员

在经过授权的范围内进行即时 setpoint、mode、start/stop、temporary override 等操作，并持续观察 readback 与实际行为。

### 值班工程师

快速判断当前 control authority、override、schedule、strategy、interlock 和 command execution 状态。

### 控制工程师

分析 command priority、value source、readback、override conflict，并从即时控制进入策略/执行/验证工作流。

## Secondary

- Facility Manager：读取站点级控制状态、Active Override 和重大控制事件；
- Energy / Optimization Engineer：从 22 Approved Plan 进入授权执行上下文，但不绕过 control owner；
- Commissioning Engineer：从 execution 进入 13 Functional Verification；
- OT Security / Safety Owner：提供 control policy / authorization / safety gate，不由前端替代；
- Audit / Compliance：读取不可变执行记录和 approval lineage。

---

# 3. 外部最佳实践依据

## 3.1 NIST SP 800-82 Rev. 3 — OT 控制必须同时考虑 Performance / Reliability / Safety

NIST SP 800-82 Rev.3 明确把 Building Automation Systems 纳入 Operational Technology，并要求 OT security 在设计和运行时考虑系统的 performance、reliability 与 safety 特性。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final
- https://www.nist.gov/publications/guide-operational-technology-ot-security

**本页采用：**

- 控制写入不是普通 SaaS mutation；
- UI 权限不是安全边界；
- command 必须由服务端权威重新验证 target / permission / authority / interlock / policy；
- 高风险控制不能使用 optimistic success；
- 不允许“网络失败 → 自动重试命令”的普通 Web 模式；
- 失联、超时、未知执行结果必须进入 reconciliation，而不是猜成功/失败。

## 3.2 DOE OpenBuildingControl — 控制设计、实现、测试和验证必须形成连续链

DOE/LBNL OpenBuildingControl 把 control sequence specification、simulation、implementation、commissioning / verification 连接起来，用来减少控制逻辑翻译中的人工错误和性能差距。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control

**本页采用：**

```text
Approved Control Intent
≠ Execution

Execution
≠ Verified Behavior
```

- 25 负责即时控制执行与观察；
- 27 负责 durable strategy / control logic；
- 13 负责功能验证；
- 控制中心不重新定义 approved strategy logic。

## 3.3 ISA-101 — HMI 必须支持 Situational Awareness 并减少 Operator Error

ISA-101 系列强调 HMI 的安全性、可用性、navigation、graphics/color、dynamic elements、security methods 和 operator performance。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101

**本页采用：**

- 当前状态、控制来源、异常状态和下一动作必须比装饰性视觉更突出；
- 高风险动作采用清晰、有限、上下文充分的确认；
- 颜色不是唯一状态通道；
- 控制动作不与普通导航按钮混淆；
- Confirmation 应说明“会发生什么”，而不是只有“确定吗？”。

## 3.4 BACnet Command Prioritization — Commandable 不等于直接覆盖当前值

BACnet commandable properties 使用 Priority Array。真正 active 的是当前最高优先级的非空命令；Priority Array 与 Relinquish Default 共同决定 commandable Present Value 的控制行为。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-1995-135b.pdf
- https://bacnet.org/wp-content/uploads/sites/4/2022/08/135-2008-Errata-2010-10-25.pdf
- https://bacnet.org/addenda/

**本页采用：**

```text
Write Accepted
≠ Command Active

ACK
≠ Active Control Source
```

- 当前控制来源和 active priority 必须由 owner/protocol contract 提供；
- UI 不自己假设 priority number；
- 同一对象可能同时存在多个 priority command；
- relinquish 是释放某个 priority/source 的语义，不是“写回默认值”。

## 3.5 BACnet Value Source / Command Source — 需要知道是谁在控制

BACnet 的 value-source 机制允许 commandable object 暴露当前 Present Value 的 command source；现代控制系统中，operator、schedule、strategy、life-safety / critical process 等都可能成为 value source。

来源：

- https://bacnet.org/wp-content/uploads/sites/4/2022/08/Add-135-2012as.pdf

**本页采用：**

- `Control Authority Source` 是一等事实；
- 如果 owner 能提供 `Value Source`，UI 应显示；
- 如果 owner不能提供 source，显示 `未知`，不通过时间戳/请求历史猜 source。

## 3.6 Schneider EcoStruxure — Forced Value / Priority 误用可能造成意外设备运行

Schneider 的公开 Building Operation 文档明确说明，BACnet command priority 解决多个控制来源之间的冲突，并警告不正确的 priority/forced-value 设置可能造成 unintended equipment operation。

来源：

- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=14260&locale=en-US&productversion=3.3
- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=14227&locale=en-US&productversion=7.1

**本页采用：**

- Raw force / priority configuration 不是普通 operator control；
- control surface 只暴露经过产品/站点治理批准的 command intents；
- manual override 必须显式显示 active source / priority / expiry / effect；
- release override 不等于 set current value to normal。

## 3.7 Siemens Building X — 远程 Setpoint Control 必须与历史/运行上下文联动

Siemens Building X Operations Manager 的公开产品说明支持远程 command setpoints，并同时提供实时状态、历史数据和 operational issue context。

来源：

- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/

**本页采用：**

- Command 之前必须能看到 current context；
- Command 之后必须进入 readback / evidence / execution record；
- 控制不是从上下文中孤立出来的“写值工具”。

---

# 4. 产品语言契约

主界面中文优先：

```text
控制中心
当前模式
当前设定值
当前控制来源
当前计划
临时覆盖
可执行控制
执行前检查
执行条件
联锁
影响范围
执行原因
覆盖到期时间
释放覆盖
发送请求
执行记录
当前反馈值
验证状态
```

可以保留：

```text
ACK
Readback
Override
Interlock
BACnet
Priority
Value Source
```

禁止默认主界面满屏：

```text
Control Authority
Commandable
Priority Array
Relinquish Default
Present Value
Command Execution State
```

这些专业术语进入辅助标签、Tooltip、工程详情或执行记录。

---

# 5. Control Domain Vocabulary

## Control Target

经过明确业务/安全治理后可接受控制意图的对象，可以是：

```text
设备
系统
回路
设定值
运行模式
临时覆盖入口
预定义控制动作
```

`Control Target` 不是任意 Registry Point。

## Control Intent

用户希望系统实现的业务控制意图，例如：

```text
将 CHWS 设定值临时调整到 7.2°C
将 AHU-03 切换到 Occupied Override 60 分钟
请求 CH-02 停机
释放当前 operator override
```

不是：

```text
Write BACnet AO-312 Present_Value = 7.2
```

## Control Authority

当前控制域是否允许本系统/用户对该 target 发起控制，以及当前真正控制该对象的 source / mode / priority 上下文。

## Permission

当前用户身份是否具有请求某类控制的授权。

## Interlock

保护设备、人员或工艺的约束逻辑。Interlock 是系统事实，不是前端自己推导的 UI 条件。

## Precondition

当前请求执行前必须满足的业务/工程条件，例如：

```text
设备处于 Remote
目标系统处于 Cooling Mode
无 maintenance lock
关键 sensor 有效
特定 approval 已完成
```

## Guardrail

控制实施过程中必须持续维持的运行边界。Guardrail 与 Interlock 不同；它可能触发停止、回滚、告警或人工处置，但语义由 owner 定义。

## Override

临时覆盖较低优先级自动控制源的正式控制对象，必须有 source、priority、reason、actor、start、expiry 或明确 persistent policy。

## Readback

设备 / controller / owner 返回的当前控制状态或值，不等于物理行为已经满足业务要求。

## Verified Behavior

依据 13 Functional Verification 或 owner-defined verification rule 确认系统实际行为满足预期。

---

# 6. Mandatory Semantic Separation

```text
Permission ≠ Control Authority

Writable ≠ Command Available
Writable ≠ Safe to Control

Online ≠ Safe to Control
Online ≠ Remote Control Available

Approved Plan ≠ Control Authority
Approval ≠ Interlock Bypass

Precondition ≠ Interlock
Interlock ≠ Guardrail

Requested ≠ Attempted
Attempted ≠ ACK
ACK ≠ Readback
Readback ≠ Verified Behavior

Write Accepted ≠ Active Command Source
ACK ≠ Active Priority

Override ≠ Permanent Configuration
Override Expired ≠ Expected State Restored automatically

Release Override ≠ Write Normal Value
Relinquish ≠ Setpoint Write

Transport Timeout ≠ Command Failed automatically
Stream Disconnect ≠ Device Offline

Control Execution ≠ Functional Verification
Functional Verification ≠ Energy Savings Verification
```

---

# 7. Primary Questions

默认页面必须回答：

1. 当前 Scope / Target 是什么；
2. 当前 Mode / Setpoint / Schedule / Strategy 是什么；
3. 当前 Control Authority Source 是什么；
4. 是否存在 Active Override；
5. 当前 command availability 是什么；
6. 如果不可执行，具体原因是什么；
7. Proposed command 会改变什么；
8. 当前值 / proposed 值 / 单位 / 差值是什么；
9. Interlock / Precondition / Guardrail 状态是什么；
10. 是否存在更高优先级 command source；
11. 是否需要 approval；
12. command 的 expiry / duration 是什么；
13. 当前 execution lifecycle 走到哪一步；
14. readback 是否匹配；
15. verified behavior 是否完成；
16. active override 应何时释放 / 审查；
17. 是否需要转 13 Functional Verification 或 28 Execution Record。

---

# 8. Information Architecture

```text
Context Header
  Site / System / Target Scope
↓
Control State Summary
  Current Mode
  Active Setpoints
  Current Schedule
  Control Authority Source
  Active Overrides
  Command Availability
↓
Target / Control Selector
↓
Primary Control Workspace
  Current State
  Available Intent
  Proposed State
  Preconditions
  Interlocks
  Impact Scope
↓
Confirmation / Execute
↓
Execution Status
  Requested
  Attempted
  ACK
  Readback
  Verified
↓
Active Overrides / Temporary Controls
↓
Recent Execution Summary
↓
07 Device / 27 Strategy / 28 Execution / 13 Verification
```

页面本身不做大面积 KPI Dashboard。

---

# 9. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/control
/sites/:siteId/control/executions/:executionId
```

Search Params 可以包括：

```text
system
asset
target
controlType
overrideState
executionState
selectedExecution
```

不进入 URL：

```text
Confirmation Dialog open
未提交的输入值
本地 hover
按钮 loading
临时 validation message
```

从 04/07/22/27 进入时保留兼容的 site/system/object context。

---

# 10. Entry / Source Contract

Control Center 可以从：

```text
04 系统运行
07 设备详情
10 诊断中心
22 优化方案
27 策略详情
28 执行记录
13 功能验证
```

进入。

来源上下文可以携带：

```text
site
system
asset
target
approvedPlanRevision
strategyRevision
reason/source trail
verification context
```

来源页面不能把前端 URL 参数当成 control approval。

例如：

```text
?target=CH-02&action=stop
```

只代表导航意图，不代表有权执行 Stop。

---

# 11. Control Capability Contract

页面只展示 owner 明确提供的 control capabilities，例如：

```text
Setpoint Change
Mode Change
Start / Stop
Temporary Override
Release Override
Schedule Exception（如果 owner 明确支持）
Approved One-shot Procedure
```

不能：

```text
Registry point writable = true
→ 自动生成输入框
```

也不能根据 point datatype 自动生成控制动作。

Control Capability 至少包括：

```text
Control Intent ID
Target
Action Type
Allowed Value / Range / Enum
Unit
Control Authority Requirements
Permission Requirement
Preflight Requirement
Approval Requirement
Interlock Contract
Expiry Requirement
Verification Requirement
Risk Class
Owner
Version
```

---

# 12. Command Availability Contract

每个控制动作必须由权威 owner 给出 command availability，而不是前端拼逻辑。

建议语义：

```text
AVAILABLE
AVAILABLE_WITH_CONFIRMATION
REQUIRES_APPROVAL
BLOCKED_PRECONDITION
BLOCKED_INTERLOCK
BLOCKED_AUTHORITY
BLOCKED_MAINTENANCE
BLOCKED_DATA_QUALITY
UNAVAILABLE_TARGET_STATE
UNSUPPORTED
UNKNOWN
```

页面必须显示原因。

例如：

```text
停止 CH-02
当前不可执行
原因：CH-02 正作为唯一可用冷机承担关键负荷
来源：Plant Control Authority
```

而不是一个灰色 disabled button，没有解释。

---

# 13. Permission Contract

```text
User Permission
≠ Command Availability
```

用户有 `control.write` 权限，也可能因为：

```text
当前设备 Local Mode
Interlock Active
Target Out of Service
Maintenance Lock
Higher-level Strategy Authority
Approval Pending
```

而不能执行。

反过来，Target technically commandable，也不表示当前用户有权请求。

Client-side permission 只用于 UI affordance；服务端必须在提交时重新授权。

---

# 14. Control Authority Contract

至少区分：

```text
当前控制来源
当前 active priority / source（若协议支持）
当前 operating mode
Local / Remote state
Schedule / Strategy source
Manual Override source
Safety / Critical source
Maintenance / Out-of-service condition
```

Control Authority 可以显示：

```text
来源：Strategy STR-18 v6
优先级：站点定义 Manual / Strategy priority
当前模式：Remote / Automatic
```

禁止前端根据最近一个 execution 推断：

```text
last command = user
→ current source = user
```

---

# 15. BACnet Priority / Source Contract

如果 Target 使用 BACnet commandable semantics：

- owner/protocol adapter 提供 Priority Array / active priority / value source 的业务投影；
- 前端不直接让普通 operator 输入 1–16；
- priority number 与业务名称由站点/owner 定义；
- 不把 Schneider 或任何厂商默认 priority mapping 当成全局真理；
- life-safety / critical equipment higher-priority commands 必须保持独立。

页面可以显示：

```text
当前控制来源
自动策略 · Priority 10

更高优先级占用
Critical equipment control · Priority 5
```

如果 owner 只提供 source class，不提供 BACnet number，就只显示 source class。

---

# 16. Active Command Conflict Contract

命令被接收后，可能因为更高优先级 command source 未释放而不成为 active command。

合法状态示例：

```text
Requested: 7.2°C
ACK: Yes
Stored Priority: 10
Active Priority: 5
Active Value: 6.8°C
Result: Accepted but not controlling
```

不能显示：

```text
控制成功
```

这类 conflict 必须提供下一专业入口：

```text
查看当前控制来源
打开执行记录
查看策略
```

而不是提供 `Force Higher Priority` 通用按钮。

---

# 17. Current State Contract

确认控制前至少展示：

```text
Current Value / State
Unit
Timestamp
Freshness
Quality
Operating Mode
Control Source
Relevant Override
Relevant Alarm / Finding（如果 owner 提供）
```

```text
Stale / Unknown Current State
≠ Current State 0
```

Current snapshot stale 时，command availability 由 owner 决定，不由前端自己写：

```text
if stale then disabled
```

或：

```text
if stale then still submit
```

---

# 18. Proposed State Contract

Proposed State 必须使用业务可理解单位 / enum，例如：

```text
冷冻水供水设定值
当前 6.5°C
拟调整到 7.2°C
变化 +0.7°C
```

而不是：

```text
AO-17
6.5 → 7.2
```

Owner 提供：

```text
allowed range
step
unit
enum
precision
semantic meaning
```

前端不自己硬编码合理区间。

---

# 19. Preconditions Contract

Precondition 是请求执行前的 readiness 条件。

例如：

```text
Remote Mode
No Maintenance Lock
Plant in Cooling Mode
Required Sensors Valid
Approval Complete
Required Schedule Window Open
```

每一项必须有：

```text
State
Reason
Owner
Last Evaluated At
```

```text
Unknown
≠ Passed
```

---

# 20. Interlock Contract

Interlock 是保护逻辑 / hard constraint。

例如：

```text
Low CHW Flow Protection
Freeze Protection
Minimum Run Time
Minimum Off Time
High Pressure Protection
Life Safety Condition
Generator / Grid Interlock
```

Control Center：

- 可以显示 interlock active / clear / unknown；
- 可以解释 owner-provided reason；
- 不能提供通用 `Bypass Interlock`；
- approval 不能绕过 interlock；
- Optimization Plan approval 不能绕过 interlock；
- admin role 也不能被前端解释为 interlock bypass authority。

任何真正的 interlock bypass 都必须属于独立、受治理、安全 owner 明确批准的 workflow；本 Surface 默认不承载。

---

# 21. Guardrail Contract

Guardrail 是执行期间持续监测的运行边界，例如：

```text
Zone temperature limit
IAQ / minimum ventilation
CHWS bounds
Static pressure bounds
Equipment loading
Vibration / temperature
Grid import limit
Battery reserve
```

Guardrail 状态可以是：

```text
Normal
Approaching Limit
Violated
Unknown
```

Control Center 只消费 owner 状态。

`Guardrail Violated` 后采取什么行为（报警、回滚、停止策略、人工决定）由 approved control contract / strategy owner 定义。

---

# 22. Impact Scope Contract

Confirmation 前必须让用户理解影响对象：

```text
Direct Target
Affected Equipment
Affected Zones
Affected Systems
Estimated Operational Impact（若 owner 有）
Current Occupancy / Critical Context（若 relevant owner 有）
```

Impact Scope 只能来自 semantic / control model owner。

禁止：

```text
根据设备名
根据拓扑位置
根据前端组件树
```

猜 blast radius。

---

# 23. Control Reason Contract

高风险和 override 操作必须有 reason。

Reason 可以是：

```text
Alarm response
Diagnosis investigation
Approved optimization execution
Maintenance support
Commissioning test
Temporary comfort correction
Emergency procedure（仅 owner 明确支持）
Other with note
```

Reason 是 audit fact，不是权限来源。

```text
Reason = Emergency
≠ Authorization granted
```

---

# 24. Confirmation Contract

高风险控制使用 Modal confirmation。

Confirmation 必须至少显示：

```text
Target
Current State
Proposed State
Unit / Delta
Control Source
Impact Scope
Preconditions
Interlocks
Guardrails relevant to action
Override Priority / Expiry（若适用）
Approval State（若适用）
Reason
```

确认按钮使用动作名称：

```text
确认将 CHWS 设定值调整到 7.2°C
```

而不是：

```text
确定
```

所有控制都用红色 destructive 样式是错误的；只有真正 destructive / emergency-risk action 才使用相应语义。

---

# 25. Confirmation Freshness Contract

如果 owner 使用 preflight / eligibility token：

```text
Preflight Result
Revision / Token
Evaluated At
Expires At
```

用户确认时服务端必须重新验证或验证 token。

前端不能：

```text
10 分钟前 interlock clear
→ 现在仍假设 clear
```

如果 preflight 已过期：

```text
重新检查执行条件
```

而不是继续发送旧请求。

---

# 26. Command Lifecycle Contract

正式生命周期：

```text
Intent / Requested
↓
Validated / Accepted for Execution
↓
Sent / Attempted
↓
ACK（若协议 / owner 支持）
↓
Readback Observed
↓
Verified Behavior（需要时）
```

同时存在合法分支：

```text
Rejected
Blocked
Cancelled
Expired
Attempt Failed
ACK Timeout
Readback Timeout
Readback Mismatch
Verification Failed
Execution State Unknown
Superseded
Rolled Back
```

不要压成：

```text
Pending / Success / Failed
```

---

# 27. Requested / Attempted Contract

`Requested` 表示权威 control owner 已接收用户的控制请求。

`Attempted` 表示执行层已尝试向 downstream control system / device 发起动作。

如果请求在 authorization / preflight 阶段被拒绝：

```text
Requested
→ Rejected before attempt
```

不应显示 `Command Failed`，因为它根本没有被发送到现场。

---

# 28. ACK Contract

`ACK` 只代表协议 / downstream owner 定义的 acknowledgement。

可能是：

```text
Controller accepted write
Gateway accepted command
Device acknowledged request
```

必须显示 ACK 的具体语义 / owner。

```text
ACK
≠ Physical response
≠ Active priority
≠ Verified behavior
```

---

# 29. Readback Contract

Readback 必须说明：

```text
Readback Value
Unit
Timestamp
Quality
Source
Match / Mismatch rule owner
```

例如：

```text
Requested 7.2°C
Readback 7.2°C
```

也不意味着整个系统已达到 intended effect。

对于 Start/Stop：

```text
Command Start
Readback Command State = On
Motor Status = Running
```

也可能是两个不同事实。

---

# 30. Verified Behavior Contract

某些低风险 setpoint write 可以由 owner 定义 `Readback sufficient`。

复杂控制必须进入 13 Functional Verification，例如：

```text
Stage transition
Lead/Lag change
Reset sequence
Optimization rollout
Control loop behavior
Interlock response
```

25 不自行定义 pass/fail criteria。

---

# 31. Execution State Unknown Contract

这是 OT 控制必须有的一等状态。

例如：

```text
Request sent
HTTP response lost
```

前端不能判断：

```text
Failed
```

也不能自动重发。

正确流程：

```text
Execution State Unknown
↓
Query by command / execution ID
↓
Reconcile authoritative execution record
```

用户界面显示：

> 执行结果暂无法确认。系统正在核对执行记录，请勿重复发送。

---

# 32. No Automatic Retry Contract

控制 mutation 默认不做自动 retry。

特别禁止：

```text
network timeout
→ retry command 3 times
```

因为第一次请求可能已经在设备侧执行。

如果 transport owner 实现 idempotency / deduplication，它属于 authoritative execution layer；前端仍应通过 execution identity 对账，而不是盲目重试。

---

# 33. Idempotency / Duplicate Submission Contract

一次用户确认产生一个明确 request / execution identity。

UI：

- 提交后禁止重复点击产生新 request；
- 页面刷新后通过 execution ID 恢复状态；
- 如果用户明确再次执行同一个 intent，应创建新的 execution，并显示与前一次的关系。

前端不通过：

```text
same target + same value + 30 sec
```

猜 duplicate。

---

# 34. Optimistic UI Prohibition

控制动作禁止：

```text
click Stop
→ UI 立刻显示 Stopped
```

正确是：

```text
Requested Stop
↓
Attempted
↓
ACK
↓
Readback / Runtime state
```

Current State 只由 authoritative snapshot/stream 更新。

---

# 35. Override Contract

Operator Override 至少包含：

```text
Target
Override Value / Mode
Priority / Source Class
Actor
Reason
Start At
Expiry / Duration
Current Active State
Superseded State
Release Permission
```

默认 temporary override 应有明确 expiry。

如果站点政策允许 persistent override：

- 必须明确标为 `持续覆盖`；
- 需要 elevated authorization / review；
- 不允许把空 expiry 偷偷解释成永久；
- 不能把 persistent override 当成策略配置替代品。

---

# 36. Override Expiry Contract

Expiry 到时意味着：

```text
当前 override source 应被 relinquish / expire
```

但不意味着：

```text
设备一定恢复到某个“正常值”
```

因为：

```text
Schedule
Strategy
Higher Priority Command
Relinquish Default
Local Control
```

可能接管。

所以 expiry 后必须重新观察 active source / readback。

---

# 37. Release / Relinquish Contract

释放 Override 是正式 command intent：

```text
Release current operator override
```

不是：

```text
Write 0
Write Auto
Write Default Value
```

如果 owner 使用 BACnet relinquish semantics，释放对应 priority/source 后，下一 active command 由 Priority Array / owner 决定。

页面应在确认中显示：

> 释放后将由当前下一级有效控制源接管；预计接管源：Schedule / Strategy（若 owner 可确认）。

如果接管源未知，显示 `未知`，不猜。

---

# 38. Active Override Review Contract

Control Center 首屏应让 operator 快速看到：

```text
Active Overrides
Expiring Soon
Persistent Overrides
Overrides without expected readback
Overrides superseded by higher priority
```

这是 shift handover 的重要事实。

不要把 Active Override 藏在 Engineering Detail。

---

# 39. Schedule Contract

25 显示：

```text
当前 Schedule
当前 schedule state
Next Transition
Schedule source
Temporary exception（若适用）
```

持久 schedule 逻辑编辑属于 26/27 或 Schedule owner。

Control Center 不提供任意 calendar editor 来修改 durable automation logic。

---

# 40. Mode Contract

必须区分：

```text
Operating Mode
Control Mode
Local / Remote Mode
Occupied / Unoccupied
Maintenance / Out-of-Service
Strategy Mode
```

不能都压成：

```text
Mode = Auto
```

Mode owner 必须定义其语义。

---

# 41. Start / Stop Contract

Start / Stop 是高风险 control intent，不是普通 Toggle。

确认至少展示：

```text
Target
Current Runtime State
Current Command State
Control Source
Preconditions
Interlocks
Minimum On/Off constraint（若 applicable）
Affected System / Redundancy Context
Reason
```

不能用一个无 confirmation 的 Switch 控件直接启动大型 HVAC / DER 设备。

---

# 42. Setpoint Contract

Setpoint Control 必须有：

```text
Semantic Name
Current Value
Proposed Value
Unit
Allowed Range
Step / Precision
Current Source
Expiry（如果是临时）
Affected system context
```

前端禁止：

```text
HTML number input
min=0
max=999
```

作为所有 setpoint 的通用实现。

---

# 43. Multi-state / Mode Change Contract

Mode enum 必须来自 owner，例如：

```text
Automatic
Occupied
Unoccupied
Maintenance
Standby
```

UI 不根据整数 `0/1/2/3` 自己映射。

改变 mode 可能改变多个 control paths，必须显示 impact scope 和 authority transition。

---

# 44. Bulk Control Contract

默认不提供任意表格 multi-select：

```text
选 20 台设备
→ 全部 Stop
```

批量控制只能来自：

- 明确建模的 Group Control Capability；
- Approved Strategy / Procedure；
- owner-defined scope；
- 明确 aggregate preflight / impact / audit。

需要复杂多步骤联动时，优先进入 27 Strategy Detail，而不是在 25 拼一组请求。

---

# 45. Emergency / Life Safety Boundary

25 默认不承载 fire / smoke / life-safety certified control responsibilities。

如果未来产品需要这些能力：

- 必须有独立产品/法规/认证范围；
- 独立 authority / HMI / audit contract；
- 不从普通 HVAC control capability 自动继承。

禁止：

```text
Admin
→ life-safety bypass
```

---

# 46. Approval Contract

某些高风险 control intent 可以要求：

```text
single approval
dual approval
change-window approval
pre-approved procedure
```

Approval owner 提供：

```text
Required
Approver Role
Status
Approved Scope
Approved Value / Range
Valid Window
Revision / Token
```

```text
Approval Granted
≠ Command Executed
≠ Interlock Clear
```

---

# 47. Execution Window Contract

如果 control intent 只能在 approved window 执行：

```text
Window Start
Window End
Timezone
Owner
```

窗口过期后：

```text
Expired
→ re-evaluate / re-approve
```

绝不能：

```text
missed window
→ automatically execute next night
```

---

# 48. Rollback / Recovery Boundary

25 可以发起 owner-defined rollback/recovery intent，例如：

```text
Restore approved strategy revision
Release temporary override
Return to previous approved setpoint set
```

但：

```text
Rollback Intent
≠ Rollback Completed

Rollback ACK
≠ Restored State
```

Rollback 同样走完整 command lifecycle。

如果 automatic rollback 存在，它必须来自 approved Strategy / Control owner；前端不自己生成。

---

# 49. Superseded / Preempted Command Contract

一个已经 Verified 的 command，之后可能被新 schedule / strategy / higher priority override 接管。

历史 execution 仍然保持：

```text
Execution EX-103
Verified at 14:07
```

当前状态可以显示：

```text
14:32 被 Strategy STR-12 supersede
```

不能把旧 execution 改成 `Failed`。

---

# 50. Control vs Strategy Boundary

## 25 控制中心

适合：

```text
即时 control intent
临时 override
单次 setpoint / mode change
一次 start/stop
release override
查看 command execution state
```

## 26/27 策略

负责：

```text
长期自动化逻辑
Schedule / Trigger
Multi-object orchestration
Priority / Conflict policy
Fail-safe
Simulation
Version
Approval
Publish / Rollback strategy
```

如果用户需要“每天 18:00 自动把 CHWS reset 改成 7.5°C”，那是 Strategy，不是每天在 25 手动执行。

---

# 51. Control vs Device Detail Boundary

07 Device Detail 可以显示：

```text
Control Authority
Command Availability
Active Override
Last Execution Summary
```

但真正发送高风险 command 进入 25。

`Writable Point` 在 07 中仍然只是工程能力事实，不代表 25 自动提供控制。

---

# 52. Control vs Optimization Plan Boundary

22 Approved Plan 可以提供：

```text
approved change
scope
preconditions
guardrails
execution window
rollback plan
```

进入 25 后仍需：

```text
Current Authority
Current Interlock
Current Preflight
Permission
Execution request
```

```text
Plan Approved
≠ Execute Now
```

---

# 53. Control vs Functional Verification Boundary

Control Center 负责：

```text
Intent → Attempt → ACK → Readback
```

13 负责：

```text
Requirement
Expected Behavior
Observed Behavior
Evidence
Pass / Fail / Inconclusive
```

复杂控制执行后，25 显示：

```text
需要功能验证
```

并进入 13。

---

# 54. Execution Record Boundary

28「执行记录」是 durable execution evidence Surface。

25 只显示最近执行摘要：

```text
Target
Requested State
Lifecycle State
Readback
Verified State
Actor
Time
```

完整 protocol / retry / approval / readback / timeline / audit 进入 28。

---

# 55. Snapshot + Stream Contract

Control Center 使用：

```text
Snapshot
+
authorized Stream
```

Snapshot 提供：

```text
Current State
Control Authority
Overrides
Command Availability
```

Stream 更新：

```text
current value
mode
source
execution status
readback
interlock / precondition state
```

Realtime update 不能：

```text
重置用户正在输入的 proposed value
关闭 confirmation dialog
替用户点击 confirm
抢 focus
自动重新排序 target list
```

如果关键 precondition 在 confirmation 期间改变，提交时由 server/preflight 再验证。

---

# 56. Stream Disconnect Contract

```text
Stream disconnected
≠ Device offline
≠ Command failed
≠ Current value = last value as truth
```

页面显示：

```text
实时更新暂不可用
最后确认状态：14:22:18
```

高风险 action 是否仍允许提交由 control owner/preflight 决定。

---

# 57. Error / Unknown State Contract

必须明确区分：

```text
Unauthorized
Target Not Found
Control Capability Unavailable
Control Authority Blocked
Precondition Blocked
Interlock Active
Current State Stale
Current State Unknown
Execution Rejected
Attempt Failed
ACK Timeout
Readback Timeout
Readback Mismatch
Verification Failed
Execution State Unknown
Control Service Unavailable
```

禁止一个：

```text
操作失败，请重试
```

覆盖所有状态。

---

# 58. Reconciliation Contract

当 execution state unknown：

```text
Execution ID
↓
Execution owner
↓
Current lifecycle state
↓
Current target state / readback
```

如果最终发现 request 已执行：

```text
Unknown → Reconciled / Attempted / ACK / Readback
```

如果确认未执行：

```text
Unknown → Reconciled / Not Attempted
```

只有此后用户才可以明确发起新的 request。

---

# 59. Audit Contract

每个控制执行至少记录：

```text
Execution ID
Site / Target
Control Intent
Requested Value / State
Previous Observed State
Actor / Principal
Reason
Source Surface
Approval Reference
Preflight / Authority Revision
Interlock / Precondition snapshot reference
Requested At
Attempted At
ACK At
Readback At
Verified At
Failure / Rejection reason
Override priority/source/expiry
Release event
Superseded / rollback relation
```

审计记录不可由前端删除或覆盖。

---

# 60. Query / Read Model Contract

Control Center 使用 owner-defined summary/read model。

禁止：

```text
50 targets
→ 50 current-state requests
→ 50 authority requests
→ 50 interlock requests
→ 50 permission requests
→ 50 execution requests
```

Target List 应由服务端投影提供：

```text
identity
current state summary
control source
active override
availability
reason
freshness
```

选中 target 后再加载完整 control detail / preflight。

---

# 61. Mutation / Command API Contract

页面组件不能直接调用 raw device protocol / `fetch` 写点。

业务路径：

```text
UI
→ Control feature mutation
→ Platform Control API
→ Authoritative Execution Owner
→ Gateway / BAS / Device
```

不允许 React component 直接写：

```text
POST /bacnet/write
MQTT publish
Modbus write register
```

---

# 62. No Generic Repository / Universal Command Abstraction

禁止：

```text
BaseCommand<T>
UniversalPointWriter
GenericControlForm
AnyWritableObjectEditor
```

拥有 domain semantics 的 control intent 应有明确 contract。

可以共享：

```text
Confirmation shell
Execution-status presentation
Reason input
Audit timeline primitives
```

但不让通用组件决定 control meaning。

---

# 63. UI Composition Contract

建议组件：

```text
Page Header
Compact status facts
Scope / Target selector
State strip
Control workspace
Preflight facts
Confirmation Dialog
Active Override ledger
Recent Execution ledger
Execution status timeline
```

shadcn mapping：

```text
Button
Dialog / AlertDialog（按实际 confirmation semantics）
Select / Command / Popover
Input / Slider（仅 owner-defined safe control）
Badge
Table
Tooltip
Separator
Sheet（只用于辅助 inspection，不用于完整 execution detail）
```

不要给所有信息套 Card。

---

# 64. High-risk Action Visual Contract

高风险动作：

- 与普通 navigation 明显分离；
- 使用明确动作文案；
- 显示后果和 scope；
- 不依赖颜色判断危险；
- 不把所有 command 都涂红；
- destructive style 只用于 destructive intent；
- start/stop 不使用容易误触的 inline Switch。

---

# 65. Target Selector Contract

Target Selector 默认基于：

```text
Current System
Current Device
Authorized Target Groups
Recent Context
Search
```

不是全站 raw point browser。

搜索结果必须显示：

```text
业务名称
系统 / 设备上下文
控制类型
当前状态
```

避免仅显示 point code。

---

# 66. Active Overrides Ledger Contract

默认列：

```text
对象
覆盖内容
当前有效值
控制来源 / Priority
开始时间
到期时间
剩余时间
原因
执行人
状态
```

状态可包括：

```text
Active
Expiring Soon
Expired / Awaiting Reconciliation
Superseded
Readback Mismatch
Persistent
Unknown
```

---

# 67. Recent Executions Contract

默认仅展示最近 relevant executions：

```text
对象
动作
请求值
状态
当前反馈
执行人
时间
```

不显示完整 raw protocol log。

点击进入 28 Execution Record。

---

# 68. Control Center Default Prioritization

首屏优先：

```text
Active Override needing review
Execution State Unknown
Readback Mismatch
Verification Failed
Expiring Override
Blocked critical control
Current selected target context
```

而不是：

```text
今天成功执行 128 次
```

作为最大 KPI。

---

# 69. Mobile / Narrow-screen Contract

约 768px：

- 可以查看 current state / authority / override / execution；
- 可以完成 owner 允许的 control intent；
- confirmation 仍必须完整展示 target/current/proposed/impact/interlock/expiry；
- 不能因为窄屏而减少安全信息；
- 不把高风险动作压成 icon-only；
- viewport 不是 permission / authority 条件。

如果组织政策禁止移动端控制，应由 Control Policy owner 明确返回 capability，而不是前端通过 `window.innerWidth` 自己禁止。

---

# 70. Accessibility Contract

- 键盘可完整完成 target selection / proposed state / confirmation；
- Dialog 打开后焦点进入 Dialog，关闭后回到触发控件；
- 状态不只靠颜色；
- Requested / ACK / Readback / Verified 有文字；
- countdown / expiry 不依赖快速动画；
- realtime update 不抢 focus；
- critical live status 可使用适当 `aria-live`，但不能持续 flood；
- 数值输入有明确 label / unit / constraints；
- confirmation 可以在不使用鼠标的情况下完成；
- error 与 blocked reason 可被辅助技术读取。

---

# 71. Security Boundary Contract

前端隐藏按钮不是 security control。

每次 command mutation 必须由 backend owner 重新检查：

```text
Principal
Site Scope
Target Scope
Capability
Permission
Control Authority
Approval
Current Preconditions / Interlocks
Command Contract Revision
```

不能：

```text
UI 已经 disabled/hidden
→ backend 不检查
```

---

# 72. Data Authority Contract

| Fact | Owner |
|---|---|
| Target Identity | Registry / Semantic Model |
| Control Capability | Control Domain |
| Permission | IAM / Authorization |
| Control Authority | Control Domain / BAS Integration |
| Current State | Operations / Telemetry owner |
| Local/Remote | Device / Control owner |
| Interlock | Control / Safety owner |
| Precondition | Control owner |
| Guardrail | Strategy / Control / domain owner |
| BACnet Priority / Value Source | Protocol adapter / Control owner |
| Approval | Change / Control governance owner |
| Command Execution | Execution owner |
| ACK | Gateway / protocol execution owner |
| Readback | Device / telemetry / control owner |
| Verified Behavior | 13 Functional Verification / approved verification owner |
| Override | Control owner |
| Audit | Execution / Audit owner |

Frontend 只组合和呈现。

---

# 73. AI Assistance Boundary

AI 可以：

```text
解释 command lifecycle
解释为什么控制被阻塞
总结 current authority / override context
解释 readback mismatch
草拟 operator note / reason
帮助定位 execution record
```

AI 不能：

```text
自行发送控制
自行选择更高 priority
绕过 interlock
自动批准 control
把 ACK 解释成成功
把 stale value 当 current
自动重试 timeout command
自动 release override
自动修改 setpoint
```

任何 AI-suggested control 仍必须经过标准 Control Intent + Confirmation + Authorization + Preflight + Audit。

---

# 74. No Defensive Programming / No Compatibility Design

明确禁止：

```text
control API error → command unavailable = false/true 猜测

permission unavailable → allow
permission unavailable → deny with fabricated reason

authority unavailable → assume automatic
authority unavailable → assume manual

current value missing → 0
current state missing → stopped

stale current value → treat as current

interlock unavailable → clear
precondition unavailable → passed
guardrail unavailable → normal

writable point → command available
online → safe to control

approved plan → execute without preflight
admin role → bypass interlock

command timeout → failed
command timeout → automatic retry

HTTP 200 → control successful
ACK → readback
ACK → verified

readback mismatch → silently overwrite UI

last command source → current control source

priority missing → default 16
priority missing → default 8

release override → write default value
expired override → assume normal restored

same target/value → dedupe in frontend

stream disconnected → device offline
stream disconnected → command failed

optimistic update → stopped/running/new setpoint

multiple control APIs → first success wins

raw BACnet writer fallback
legacy Commands page adapter
old point-write compatibility layer
```

正式原则：

> **一个控制意图对应一个权威 Execution。Permission、Control Authority、Precondition、Interlock 和 Approval 是不同事实。Requested 不是 Attempted，ACK 不是 Readback，Readback 不是 Verified。Writable 不是安全可控，Online 不是安全可控，Unknown 保持 Unknown。**

---

# 75. Browser Acceptance Criteria

## Control Truth

- 当前 target / mode / setpoint / schedule / authority source 可见；
- writable 不直接生成 control；
- permission / authority / interlock / precondition 分开；
- active override 可见；
- source/priority unknown 不猜。

## Confirmation

- 高风险 control 使用完整 confirmation；
- Target / Current / Proposed / Impact / Interlock / Expiry 可见；
- confirmation 不是只有“确定吗？”；
- stale preflight 重新检查；
- approval 不隐藏 interlock。

## Execution

- Requested / Attempted / ACK / Readback / Verified 分开；
- HTTP/ACK 不显示“控制成功”；
- readback mismatch 明确；
- unknown execution 不自动 retry；
- page refresh 可以恢复 execution state。

## Override

- override 有 actor/reason/start/expiry/source；
- release 使用 owner-defined relinquish semantics；
- expiry 不假定正常值恢复；
- persistent override 明确标识；
- superseded override 保留历史。

## BACnet / Priority

- 不向普通 operator 暴露任意 1–16 raw priority selection；
- active source / priority 由 owner 提供；
- higher-priority conflict 可见；
- accepted-but-not-controlling 不显示成功。

## Safety / Authority

- client hidden button 不是安全边界；
- server authoritative check 存在；
- interlock 无通用 bypass；
- life-safety 不由普通 control capability 继承。

## Accessibility / Responsive

- 键盘可完成操作；
- 状态不只靠颜色；
- confirmation 在 768px 仍包含全部安全上下文；
- realtime update 不抢 focus；
- 无 page-level 横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 raw `fetch` / MQTT / BACnet write from page component；
- 无 optimistic command success；
- 无 automatic retry high-risk mutation；
- 无 N+1 target/authority/interlock queries；
- 无 universal point writer；
- 无 legacy Commands compatibility adapter；
- review scenario 无 runtime/network error。

---

# 76. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 控制中心 · 中央冷站                                  当前：自动运行       │
│ 站点：中央园区   系统：冷冻水系统   控制来源：策略 STR-18 v6            │
├──────────────────────────────────────────────────────────────────────────────┤
│ 当前模式       冷却模式     当前 CHWS SP     6.5°C                       │
│ 当前计划       工作日       Active Override  1                            │
│ 控制可用性     可用         当前来源          自动策略 · Priority 10      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 控制对象：冷冻水供水设定值                                                 │
│                                                                              │
│ 当前值                         拟议值                                        │
│ 6.5°C                          7.2°C                                         │
│ 14:22:18 · GOOD                变化 +0.7°C                                  │
│                                                                              │
│ 执行前检查                                                                 │
│ ✓ Remote Mode                                                              │
│ ✓ Cooling Mode                                                             │
│ ✓ 关键传感器有效                                                           │
│ ✓ 无维护锁定                                                               │
│                                                                              │
│ 联锁                                                                       │
│ ✓ Low CHW Flow Protection clear                                            │
│ ✓ Freeze Protection clear                                                  │
│                                                                              │
│ 影响范围                                                                   │
│ 冷冻水系统 · 3 台冷机 · 14 个 AHU                                          │
│                                                                              │
│ 覆盖方式：临时调整 · 60 分钟                                               │
│ 到期后：释放当前 operator override，由下一有效控制源接管                   │
│                                                                              │
│ 原因：[低负荷运行验证____________________________]                          │
│                                             [取消] [检查并确认执行]         │
├──────────────────────────────────────────────────────────────────────────────┤
│ 当前执行                                                                   │
│ Requested → Attempted → ACK → Readback 7.2°C → 待功能验证                  │
│ [打开执行记录] [开始功能验证]                                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 临时覆盖                                                                   │
│ CHWS SP 7.2°C · 操作员覆盖 · 49 分钟后到期 · 当前有效                     │
│ [查看详情] [释放覆盖]                                                       │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任与信息层级，不是像素规范。

---

# 77. Explicit Non-goals

25 不是：

- BACnet Explorer；
- Modbus register writer；
- MQTT console；
- life-safety control panel；
- raw priority-array editor；
- universal point editor；
- Strategy authoring page；
- Schedule authoring page；
- Alarm Rule editor；
- optimization approval page；
- Functional Verification page；
- Emergency bypass console；
- 自动 retry engine。

---

# 78. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Control Target / Control Intent 明确；
- Permission / Authority / Precondition / Interlock 分离；
- Current / Proposed State 明确；
- Command Availability 由 owner 提供；
- BACnet priority/value-source 边界明确；
- Confirmation 契约明确；
- Requested / Attempted / ACK / Readback / Verified 分离；
- Execution State Unknown 与 reconciliation 明确；
- No Automatic Retry 明确；
- Override / Expiry / Release / Relinquish 语义明确；
- Bulk Control 边界明确；
- 25 / 27 / 28 / 13 职责明确；
- Server authoritative security boundary 明确；
- No Defensive Programming 规则明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
