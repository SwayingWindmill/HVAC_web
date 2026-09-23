# 26 策略中心 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `26 策略中心`  
> **Route intent：** `/sites/:siteId/strategies`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Strategy`、`Trigger`、`Fail-safe`、`Rollback`、`Priority`、`Execution` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有策略页、规则列表、旧 Automation 页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Strategy / Version / Deployment / Trigger / Schedule / Conflict / Execution / Permission / Audit contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **让操作员和控制工程师在站点级别理解“现在有哪些自动化策略、哪些版本已经发布/部署、哪些策略被启用、哪些此刻真正处于活动状态、它们控制什么、为什么运行、是否互相冲突、是否受护栏/失效保护约束、最后一次执行发生了什么”，并安全地完成策略生命周期管理。**

本 Surface 是 **automation strategy portfolio + deployment governance workspace**，不是：

- 25「控制中心」的一次性命令页；
- 27「策略详情 / 仿真 / 审批」的完整工程开发页；
- BAS raw program browser；
- 任意 Schedule 编辑器；
- 通用 `if-this-then-that` rule builder；
- Alarm Rule editor；
- 任意 point priority editor；
- 一个“Enabled 开关列表”；
- 一个“最新执行成功率 Dashboard”；
- 允许未经审批直接修改现场 automation logic 的入口。

用户离开本页前应该知道：

1. 当前站点有哪些 Strategy；
2. 每个 Strategy 当前发布的是哪个 Version；
3. 当前部署到哪些 Site / System / Controller / Runtime；
4. 当前策略是否 Published、Deployed、Enabled、Eligible、Active；
5. 当前 Schedule / Trigger 是否满足；
6. 是否存在 Priority / Arbitration / Conflict；
7. 当前 Control Authority 是谁；
8. 当前 Guardrails / Interlocks / Fail-safe 是否正常；
9. 最近一次 Execution 的 intent、outcome、readback / verification 是什么；
10. 当前策略是否被 Override、Superseded、Suspended 或 Blocked；
11. 当前是否存在待发布的新 Version；
12. 是否需要进入 27 做修改、仿真、审批；
13. 是否需要进入 28 检查详细执行记录；
14. 是否需要进入 13 做 Functional Verification；
15. 策略 Rollback 是否真正恢复到目标 Version / State。

---

# 2. 主要用户

## Primary

### 控制工程师

管理 strategy portfolio、版本、deployment、priority / arbitration、guardrails、fail-safe 和变更入口。

### HVAC / 运行工程师

判断当前自动控制为什么生效 / 不生效、哪些策略互相影响、当前由谁取得控制权。

### 值班操作员

快速知道哪些策略 Enabled、哪些 Active、哪些被 Blocked / Suspended / Overridden，以及最近发生了什么。

## Secondary

- Energy / Optimization Engineer：从 22 Approved Plan 进入对应 Strategy；
- Commissioning Engineer：验证发布后的 strategy behavior；
- Facility Manager：查看站点自动化状态和风险；
- OT Security / Platform Owner：控制 deployment / runtime / permission 边界；
- Audit / Compliance：检查 strategy version、approval、deployment、rollback 和 execution lineage。

---

# 3. 外部最佳实践依据

## 3.1 DOE OpenBuildingControl — Strategy 必须是可规范、可部署、可验证的工程对象

DOE/LBNL OpenBuildingControl 把 HVAC control sequence 的 specification、performance evaluation、deployment 与 verification 连成同一工程链，并通过 CDL 等数字化方式减少人工翻译错误。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control
- https://www.energy.gov/sites/default/files/2023-05/bto-peer-2023-obc.pdf

**本页采用：**

```text
Strategy Definition
→ Version
→ Approval
→ Publish
→ Deployment
→ Runtime Eligibility
→ Execution
→ Verification
```

- Strategy 是 versioned engineering object；
- strategy 逻辑不直接在 portfolio 页编辑；
- 发布、部署、启用和实际活动状态必须分离；
- strategy behavior 必须能回到 27 的 approved definition 和 13 的 functional verification。

## 3.2 ASHRAE Guideline 36-2024 — Sequence 必须追求效率、稳定和可验证实现

ASHRAE Guideline 36-2024 的目的包括最大化 HVAC system energy efficiency / performance、提供 control stability，并支持 real-time FDD；Guideline 还定义 functional tests 来确认 sequence implementation。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes
- https://www.ashrae.org/professional-development/all-instructor-led-training/catalog-of-instructor-led-training/guideline-36-best-in-class-hvac-control-sequences

**本页采用：**

- 自动化策略不是“能跑就行”，需要 stability、guardrail 和 functional verification；
- strategy version / sequence implementation 必须可追溯；
- strategy portfolio 应突出异常、冲突和 verification state，而不是只显示 enable 开关。

## 3.3 NIST SP 800-82 Rev.3 — Strategy deployment 属于 OT Change

NIST SP 800-82 Rev.3 明确把 Building Automation Systems 纳入 OT，并要求同时考虑 performance、reliability 和 safety。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

**本页采用：**

- Publish / Deploy / Enable 不是普通 SaaS 配置；
- UI 权限不是安全边界；
- strategy deployment / rollback 需要服务端 authority、revision、approval 和 audit；
- failure / partial deployment / unknown runtime state 必须是正式状态；
- 禁止网络失败后前端自动重复部署。

## 3.4 ISA-101 — HMI 应突出当前自动化状态与异常，而不是配置细节

ISA-101 面向 HMI 的 design / implementation / operation / lifecycle management，强调 usability、operator performance、situational awareness 和 change management。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101

**本页采用：**

- Strategy Center 首先告诉用户“现在谁在控制、为什么、是否正常”；
- 状态不只靠颜色；
- priority/conflict/override 必须可读；
- configuration detail 不挤占 operator primary view。

## 3.5 BACnet Schedule / Priority — Schedule 和 Priority 是独立控制事实

BACnet / 成熟 BMS 中 Schedule、Calendar、Exception Event、Command Priority 分别有明确语义。Schneider EcoStruxure 的公开文档也说明 schedule exception 可覆盖 weekly event，priority 用来解决 overlapping events。

来源：

- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=6852&locale=en-US&productversion=7.1
- https://ecostruxure-building-help.se.com/bms/topics/show.castle?id=6589&locale=en-US&productversion=7.1

**本页采用：**

```text
Schedule
≠ Strategy
Priority
≠ Strategy Quality
```

- Schedule 只是 activation input 的一种；
- Priority 只说明 arbitration / precedence，不说明策略“更好”；
- exception event、manual override 和 strategy arbitration 必须分别表达。

---

# 4. 产品语言契约

主界面中文优先：

```text
策略中心
策略
版本
作用范围
生命周期
已发布版本
已部署版本
启用状态
活动状态
触发条件
计划时段
优先级 / 仲裁
冲突
控制来源
运行护栏
失效保护
最后执行
最后验证
部署状态
回滚状态
```

可以保留：

```text
Strategy
Trigger
Schedule
Priority
Fail-safe
Rollback
Execution
ACK
Readback
```

禁止默认主界面满屏英文：

```text
Published
Deployed
Enabled
Active
Eligible
Conflict
Runtime
```

这些可作为辅助标签或工程详情。

---

# 5. Strategy Domain Vocabulary

## Strategy Definition

描述自动化目标、输入、输出、trigger、schedule、preconditions、guardrails、priority/arbitration、fail-safe、control actions 和 verification intent 的正式工程对象。

## Strategy Version

策略逻辑的不可变版本，例如：

```text
STR-18 v6
```

修改 trigger、scope、setpoint logic、guardrail、priority、fail-safe、outputs 等必须形成新版本。

## Published

Version 已通过策略治理并成为可部署版本。

## Deployed

特定 Version 已部署到指定 Runtime / Controller / Site Scope。

## Enabled

部署实例允许参与运行。

## Eligible

当前 preconditions / schedule / trigger / policy 允许策略参与 arbitration。

## Active

当前策略实际取得了至少一个定义内 control authority / output effect。

## Effective

策略当前实际输出与 intended behavior 一致；是否称 `Effective` 必须有 owner-defined observation / verification contract，不由 UI 猜。

## Conflict

两个或多个策略在相同/重叠 scope、time、resource、command target 上存在不兼容控制意图或治理规则定义的冲突。

## Arbitration

决定多个可参与策略之间 precedence / composition / winner / rejection 的权威机制。

## Fail-safe

策略异常、数据异常、runtime failure 或约束触发时的 owner-defined 安全行为。

---

# 6. Mandatory Semantic Separation

```text
Strategy Definition ≠ Strategy Version

Published ≠ Deployed
Deployed ≠ Enabled
Enabled ≠ Eligible
Eligible ≠ Active
Active ≠ Effective

Higher Priority ≠ Better Strategy
Priority ≠ Business Importance

Conflict Detected ≠ Conflict Resolved
Conflict Resolved ≠ Both Strategies Active

Schedule Active ≠ Strategy Active
Trigger True ≠ Strategy Active

Strategy Enabled ≠ Outputs Currently Controlling
Strategy Disabled ≠ Existing Override Released
Strategy Disabled ≠ Previous Output Reverted

Deployment Requested ≠ Deployment Completed
Deployment Completed ≠ Runtime Loaded
Runtime Loaded ≠ Strategy Enabled

Rollback Requested ≠ Previous Version Restored
Rollback ACK ≠ Restored Runtime State

Last Execution Success ≠ Current Strategy Healthy
No Recent Execution ≠ Strategy Failure

Approval ≠ Deployment
Deployment ≠ Functional Verification
Functional Verification ≠ Savings Verification

Online Runtime ≠ Strategy Safe
Data Available ≠ Input Valid
```

---

# 7. Primary Questions

默认页面必须回答：

1. 当前有哪些策略；
2. 哪个版本是 Published；
3. 哪个版本已部署；
4. 当前 Enabled / Eligible / Active 分别是什么；
5. 当前 Scope 是什么；
6. Trigger / Schedule 是否满足；
7. 当前 Control Authority / Active Outputs 是什么；
8. 是否存在 Active Override / Higher Priority source；
9. 是否有策略冲突；
10. 冲突由哪个 arbitration owner 解决；
11. Guardrail / Fail-safe / Interlock 当前状态如何；
12. 最近一次 Execution 是什么；
13. 最近一次 Deployment 是否完整；
14. 最近一次 Functional Verification 是否通过；
15. 是否有 Draft / Review / Published 新版本；
16. 是否存在 rollback / supersede；
17. 需要进入 27、28、25 或 13 做什么。

---

# 8. Information Architecture

```text
Context Header
↓
Strategy Portfolio Summary
↓
Strategy Ledger
                           → Strategy Inspector
↓
Portfolio Issues
  Conflicts
  Deployment drift
  Verification required
  Guardrail / Fail-safe issues
  Unknown runtime state
↓
Recent Deployments / Rollbacks
↓
Recent Executions
↓
27 Strategy Detail / 28 Execution / 25 Control / 13 Verification
```

默认 **Ledger-first**，不是 Strategy Card Wall。

---

# 9. Route / URL State Contract

```text
/sites/:siteId/strategies
/sites/:siteId/strategies/:strategyId
```

Search Params 可以包括：

```text
system
scope
lifecycle
published
runtimeState
enabled
active
conflict
verification
owner
sort
selectedStrategy
```

不进入 URL：

```text
hover
本地展开状态
confirmation dialog
未提交筛选输入
```

---

# 10. Strategy Lifecycle Contract

治理生命周期建议：

```text
Draft
In Review
Approved / Publishable
Published
Superseded
Retired
Rejected
```

Runtime / Deployment lifecycle 与之分离：

```text
Not Deployed
Deployment Pending
Deploying
Deployed
Partially Deployed
Deployment Failed
Runtime Unknown
Rollback Pending
Rolled Back
```

Enable / Activity 再单独表达：

```text
Enabled / Disabled
Eligible / Ineligible / Unknown
Active / Inactive / Unknown
```

不能压成一个：

```text
Status = Running
```

---

# 11. Strategy Version Contract

每个 Version 至少记录：

```text
Strategy ID
Version
Definition Hash / Revision
Objective
Scope
Inputs
Outputs
Triggers
Schedule references
Preconditions
Guardrails
Priority / Arbitration contract
Fail-safe
Verification contract
Owner
Created At
Approved / Published At
```

Version 必须不可变。

如果修改：

```text
Trigger
Scope
Output behavior
Priority
Guardrail
Fail-safe
Schedule binding
```

必须产生新 Version。

---

# 12. Publish Contract

Publish 表示：

> 当前 Version 已通过策略治理，可以进入 deployment workflow。

Publish 不等于：

```text
已部署
已启用
已生效
已执行
已验证
```

必须保留：

```text
Published Version
Approved By
Approval Reference
Published At
Effective Eligibility Rules
```

---

# 13. Deployment Contract

Deployment 至少包含：

```text
Strategy Version
Target Runtime / Controller / Gateway
Target Site / Scope
Deployment Artifact / Package Revision
Requested By
Requested At
Approval Reference
Deployment State
Runtime Load State
Runtime Reported Version
Completed At
Failure / Drift reason
```

Portfolio 页面不直接展示 raw deployment console。

---

# 14. Deployment Drift Contract

如果：

```text
Published Version = v8
Expected Deployed = v8
Runtime Reported = v7
```

必须显示：

```text
部署漂移 / Version Drift
```

不能：

```text
Published = v8
→ 假定现场也在跑 v8
```

同样允许：

```text
部分 Controller v8
部分 Controller v7
```

此时状态是 `Partially Deployed`，不能显示整体 `Deployed`。

---

# 15. Enable Contract

Enabled 表示：

> Runtime / strategy owner 允许该 deployment instance 参与运行。

Enabled 不表示当前 trigger 成立，也不表示 strategy 已取得 control authority。

Disable strategy 只能改变 owner-defined enable state，不得前端推断：

```text
Disable
→ 释放所有 override
→ 恢复旧 setpoint
→ 停止设备
```

这些恢复行为必须由 strategy definition / fail-safe / control owner 明确规定。

---

# 16. Eligibility Contract

Eligible 由 authoritative strategy runtime / policy owner 计算，可以考虑：

```text
Enabled
Schedule
Trigger
Preconditions
Data Quality
Mode
Occupancy
Control Authority
Maintenance State
Program / Tariff / DR state
Approval window
```

前端不自行：

```text
enabled && trigger
→ eligible
```

---

# 17. Active Contract

Active 表示 strategy 当前实际参与控制并对其声明的 output scope 产生权威作用。

可能出现：

```text
Enabled = Yes
Eligible = Yes
Active = No
```

原因可能是：

```text
higher priority strategy
manual override
local mode
arbitration lost
shared resource unavailable
```

也可能：

```text
Active on 3 outputs
Blocked on 1 output
```

Portfolio 应允许部分 activity，而不是只有 boolean。

---

# 18. Schedule Contract

Schedule 是 strategy activation input 的一种。

可以引用：

```text
Weekly Schedule
Calendar
Holiday / Exception
Occupancy Schedule
Tariff Window
DR Event Window
```

Schedule owner 提供：

```text
Current State
Next Transition
Exception State
Timezone
Revision
```

Strategy Center 不复制一个通用 Calendar editor。

Durable schedule editing 属于 27 或专门 Schedule owner。

---

# 19. Trigger Contract

Trigger 可以是：

```text
Schedule
Operating Mode
Load Threshold
Weather Condition
Occupancy
Price / DR Event
Equipment State
Energy / Demand Condition
Composite owner-defined logic
```

但 Portfolio 只展示 human-readable trigger summary。

完整逻辑进入 27。

禁止：

```text
frontend 从 telemetry 自己判断 trigger true
```

---

# 20. Trigger State Contract

Trigger State 建议：

```text
True
False
Unknown
Not Applicable
Suppressed by Policy
```

如果输入 stale / missing：

```text
Unknown
```

而不是前端：

```text
missing → false
```

或：

```text
missing → last known
```

---

# 21. Priority / Arbitration Contract

Priority 是 arbitration input，不是策略价值评分。

```text
Higher Priority
≠ Better Strategy
≠ Higher Energy Savings
≠ More Important Business Objective
```

Priority / arbitration 由 Strategy / Control owner 定义。

Portfolio 可以显示：

```text
Priority Class
Arbitration Group
Current Winner
Losing / Suppressed strategies
Reason
```

前端不默认使用 BACnet 1–16 做 Strategy Priority。

---

# 22. Conflict Contract

Conflict 可能来自：

```text
Same target, incompatible values
Overlapping schedules
Competing operating modes
Resource capacity conflict
Guardrail conflict
Shared equipment conflict
Mutually exclusive strategies
Priority ambiguity
Circular dependency
```

冲突必须由 authoritative conflict evaluator / governance owner 给出。

不能：

```text
两个策略 target name 相同
→ frontend 标 Conflict
```

---

# 23. Conflict State Contract

建议状态：

```text
No Conflict
Potential Conflict
Active Conflict
Resolved by Arbitration
Blocked Pending Review
Unknown
```

```text
Conflict Detected
≠ Conflict Resolved
```

`Resolved by Arbitration` 还应显示：

```text
Winner / Result
Rule / Priority
Owner
Evaluated At
```

---

# 24. Control Authority Contract

每个 Strategy 应显示：

```text
Declared Output Scope
Current Active Output Scope
Control Authority Source
Higher Priority Sources
Manual Overrides
Local/Remote constraints
```

如果 Strategy Enabled 但没有 authority，应显示：

```text
已启用 · 当前未取得控制权
```

不能显示 `Running`。

---

# 25. Inputs Contract

Portfolio 只显示关键输入摘要，例如：

```text
Load
Outdoor Air Temperature
Zone Demand
Schedule
Occupancy
Price Signal
Equipment Availability
```

每个 input 应有：

```text
Current State
Freshness
Quality
Source
```

关键 input invalid 时，策略行为由 owner-defined fail-safe / eligibility contract 决定。

前端不自行决定 fail-safe。

---

# 26. Outputs Contract

Outputs 必须用业务语义表达：

```text
CHWS Setpoint Reset
AHU SAT Reset
Chiller Staging Request
Static Pressure Reset
Battery Dispatch Target
EV Charging Limit
```

而不是 raw point names。

Portfolio 显示 output summary；完整 output mapping 进入 27。

---

# 27. Preconditions Contract

Preconditions 是策略参与运行前的 readiness 条件，例如：

```text
System in Cooling Mode
Remote Control Available
Required Sensor Quality Good
No Maintenance Lock
Required Equipment Available
```

状态：

```text
Passed
Blocked
Unknown
Not Applicable
```

Unknown 不等于 Passed。

---

# 28. Guardrail Contract

Guardrail 可以来自：

```text
Comfort / IEQ
Equipment Limit
Minimum Ventilation
Freeze / Condensation Protection
Demand Limit
Battery Reserve
Grid Export Limit
Critical Zone Constraint
```

Portfolio 应显示：

```text
Normal
Approaching Limit
Violated
Unknown
```

Guardrail violation 后是 suspend、rollback、degrade、alarm 还是人工 review，由 strategy definition 决定。

---

# 29. Interlock Boundary

Interlock 属于 25 / Control / Safety owner 的保护事实。

Strategy 可以依赖 Interlock state，但 26 不能：

```text
Disable Interlock
Bypass Interlock
Treat Unknown as Clear
```

Strategy approval / priority 不能绕过 Interlock。

---

# 30. Fail-safe Contract

每个高影响 Strategy 应明确：

```text
Fail-safe Trigger
Fail-safe Action
Safe / fallback target
Authority
Timeout
Recovery rule
Verification requirement
```

例如：

```text
关键 flow sensor invalid
→ suspend optimization output
→ relinquish to baseline sequence
```

这是 owner-defined contract，不是前端硬编码。

---

# 31. Fail-safe ≠ Rollback

```text
Fail-safe
= runtime abnormal-condition behavior

Rollback
= deployment / version lifecycle operation
```

二者可以关联，但不能混为一个按钮。

---

# 32. Degraded Mode Contract

某些 strategy 在部分输入或资源不可用时可以进入 owner-defined `Degraded Mode`。

必须明确：

```text
What is degraded
Which outputs remain active
Which constraints changed
Expected impact
Exit criteria
```

不能前端看到缺数据后自行决定：

```text
fallback to old logic
```

---

# 33. Execution Contract

一次 Strategy Execution 至少关联：

```text
Strategy ID / Version
Runtime Instance
Trigger / Schedule reason
Eligibility snapshot
Arbitration result
Control outputs / intents
Execution State
Readback / observed result references
Started At / Ended At
Supersede / interrupt reason
```

Portfolio 只显示 summary，完整证据进入 28。

---

# 34. Execution State Contract

建议：

```text
Not Triggered
Eligible
Started
Active
Completed
Superseded
Suspended
Blocked
Failed
State Unknown
```

如果 strategy 是长期持续策略，`Active` 可以持续存在；不能强制所有 Strategy 都有传统 Start/Complete。

---

# 35. Last Execution Contract

`Last Execution` 必须带：

```text
Version
Trigger
Start / End
Outcome
Outputs affected
Execution ID
```

```text
Last Execution Success
≠ Current Strategy Healthy
```

一个上周成功执行过的策略，今天可能 deployment drift、input invalid 或 conflict。

---

# 36. Observability / Strategy Health Contract

不默认制造黑盒 `Strategy Health 92`。

使用可解释事实：

```text
Deployment State
Runtime State
Input Quality
Conflict State
Guardrail State
Fail-safe State
Execution State
Verification State
```

必要时可形成 owner-defined summarized status，但必须可展开解释。

---

# 37. Strategy Enabled ≠ Healthy

合法组合：

```text
Enabled
Runtime Unknown
```

或：

```text
Enabled
Conflict Active
```

或：

```text
Enabled
Deployment Drift
```

UI 不能因为 Enabled=true 就显示绿色 Healthy。

---

# 38. Verification Contract

每个 Strategy Version 可以关联：

```text
Verification Required
Verification Definition
Last Functional Verification
Result
Evidence reference
Verified Version
Verified At
```

如果：

```text
Deployed Version = v8
Last Verified Version = v7
```

必须显示：

```text
当前版本尚未完成验证
```

不能沿用 v7 的 PASS。

---

# 39. Publish / Deploy / Verify Chain

正式链条：

```text
27 Strategy v8
↓ Approved
Published v8
↓ Deploy
Runtime v8
↓ Enable / Execute
13 Functional Verification
↓
Verified v8
```

其中任何一步都不能跳过语义。

---

# 40. Rollback Contract

Rollback 必须指向明确 target Version：

```text
Current runtime v8
Rollback target v7
```

记录：

```text
Reason
Requested By
Approval
Target Scope
Runtime targets
Requested At
Deployment State
Runtime Reported Version
Verification requirement
```

```text
Rollback Requested
≠ Restored
```

必须确认 runtime 实际回到目标 version。

---

# 41. Rollback Verification Contract

Rollback 完成后至少检查：

```text
Runtime Version
Enable State
Control Authority
Active Outputs
Guardrails
Functional Verification requirement
```

如果 runtime version 恢复但现场行为异常，不得显示“Rollback Success”作为最终事实。

---

# 42. Disable Strategy Contract

Disable 是改变 strategy participation state。

确认应显示：

```text
Strategy
Current Version
Current Active Outputs
Current Control Authority
Expected release / fail-safe behavior
Affected systems
Existing overrides
```

Disable 不自动等于：

```text
relinquish manual override
restore baseline values
stop equipment
```

除非 owner contract 明确说明。

---

# 43. Retire Strategy Contract

Retire 是 lifecycle governance，不是即时 runtime action。

Retire 前必须确认：

```text
No required active deployment
Replacement / successor strategy
Historical references
Dependent strategies
Verification / audit preservation
```

历史 execution、version、approval 不删除。

---

# 44. Manual Override Relationship

25 的 manual / operator override 可能 supersede strategy output。

26 应显示：

```text
Strategy Active: partial
Output CHWS SP: superseded by operator override
Override expires in 42 min
```

但 26 不直接释放 override；进入 25。

---

# 45. Schedule Exception Relationship

Schedule Exception 可以使 Strategy Eligible / Ineligible，但不能：

```text
Schedule exception active
→ assume strategy active
```

如果 arbitration / authority 不允许，仍可能 inactive。

---

# 46. Dependencies Contract

Strategy 可以依赖：

```text
Another Strategy
Shared Resource
Meter / Sensor
Schedule
DER capability
Control authority
External signal
Tariff / DR event
```

依赖关系来自 Strategy definition / semantic owner。

前端不从 imports / code references 猜业务 dependency。

---

# 47. Strategy Graph Boundary

默认不画策略依赖关系图。

只有真实 authoritative dependency/conflict model 存在，并且图形明显提升理解时，才提供 Graph view。

不能通过：

```text
同名 target
相同 schedule
同一设备
```

拼“策略拓扑”。

---

# 48. Portfolio Summary Contract

Compact summary 可显示：

```text
已发布策略
已部署
活动中
存在冲突
部署漂移
待验证版本
Fail-safe / Guardrail 异常
Runtime Unknown
```

这些是 portfolio facts，不是 KPI vanity cards。

---

# 49. Strategy Ledger Contract

默认列：

```text
策略
作用范围
已发布版本
运行版本
启用 / 活动状态
触发 / 计划摘要
优先级 / 仲裁
冲突
护栏 / Fail-safe
最后执行
验证状态
```

可选：

```text
Owner
Last Deployment
Next Schedule Transition
```

---

# 50. Strategy Inspector Contract

Inspector 负责快速判断：

```text
Strategy identity
Published / Runtime Version
Scope
Enabled / Eligible / Active
Trigger / Schedule
Control Authority
Priority / Conflict
Guardrail / Fail-safe
Last Execution
Verification
Next action
```

不复制完整 27 Strategy Detail。

---

# 51. Strategy Detail Boundary

26 Detail 可以展示 durable operational summary，但真正的：

```text
Objective
Inputs / Outputs
Trigger logic
Schedule detail
Preconditions
Guardrails
Fail-safe
Priority / Conflict policy
Simulation
Historical Replay
Approval
Publish
```

进入 27。

26 是 Portfolio / Operations，27 是 Engineering / Change Development。

---

# 52. Strategy vs Control Center Boundary

25：

```text
一次即时 control intent
manual override
release override
command lifecycle
```

26：

```text
自动化 Strategy portfolio
runtime / deployment / conflict status
```

27：

```text
Strategy logic authoring / simulation / approval
```

用户不应通过 26 手工写 raw output 值。

---

# 53. Strategy vs Execution Record Boundary

28 负责：

```text
Execution timeline
Trigger snapshot
Arbitration
Command attempts
ACK / Readback
Error / retry owner events
Verification references
Audit
```

26 只显示 recent execution summary。

---

# 54. Strategy vs Optimization Plan Boundary

22 可以产生：

```text
Proposed control change
Approved Plan Revision
```

但真正变成自动控制策略，需要：

```text
27 Strategy Definition / Version
Approval
Publish
Deploy
Verify
```

```text
Optimization Plan Approved
≠ Strategy Published
```

---

# 55. Strategy vs Functional Verification Boundary

26 只显示当前 version 的 verification status。

13 负责：

```text
Requirement
Expected Behavior
Observed Behavior
Evidence
Pass / Fail / Inconclusive
Retest
```

Strategy Center 不前端自判 `PASS`。

---

# 56. Snapshot + Stream Contract

策略中心使用：

```text
Snapshot
+
Runtime / Execution Stream
```

Snapshot：

```text
Version
Deployment
Enable state
Eligibility
Active state
Conflict
Guardrail
Fail-safe
Last Execution
Verification
```

Stream 可以更新：

```text
trigger
activity
runtime state
conflict
output ownership
execution state
```

Stream 不能：

```text
自动切换 selected row
重置 filter
关闭 inspector
重排大量列表
自动改变 published version
```

---

# 57. Stream Disconnect Contract

```text
Stream disconnected
≠ Strategy disabled
≠ Strategy inactive
≠ Strategy failed
```

显示：

```text
策略实时状态暂不可用
最后确认：14:22:18
```

当前 control safety 由 strategy runtime / control owner 负责，前端不猜。

---

# 58. Deployment Mutation Contract

策略 Publish / Deploy / Enable / Disable / Rollback 都通过明确 domain mutation：

```text
UI
→ Strategy feature mutation
→ Strategy Governance / Deployment API
→ Runtime / Control Platform
```

禁止页面直接：

```text
upload script to controller
PUT runtime config
write BACnet points
```

---

# 59. No Automatic Retry Contract

高风险策略 mutation 默认不做前端自动 retry，包括：

```text
Deploy
Enable
Disable
Rollback
Retire
```

如果响应丢失：

```text
Mutation State Unknown
↓
按 Deployment / Change ID 对账
```

不能：

```text
network timeout
→ retry deploy 3 times
```

---

# 60. Deployment Idempotency / Reconciliation Contract

一次 Deployment / Rollback 产生唯一 identity。

UI：

- 提交后恢复该 operation state；
- 页面刷新后可继续追踪；
- runtime reported version 是 reconciliation 事实；
- 如果状态 unknown，不允许盲目重新部署。

前端不通过相同 strategy/version 猜 duplicate。

---

# 61. Error / Unknown State Contract

必须区分：

```text
Strategy Not Found
Unauthorized
Definition Unavailable
Published Version Unknown
Runtime Version Unknown
Deployment Pending
Deployment Partial
Deployment Failed
Enable State Unknown
Trigger State Unknown
Conflict Unknown
Guardrail Unknown
Fail-safe Unknown
Execution State Unknown
Verification Pending
Strategy Service Unavailable
```

禁止一个：

```text
策略异常
```

覆盖所有情况。

---

# 62. Security Boundary Contract

Client hide/disable 不是安全控制。

任何 lifecycle mutation 都由 backend 重新检查：

```text
Principal
Site Scope
Strategy Scope
Permission
Published / Approved Revision
Runtime target
Deployment authority
Current state
Conflict / dependency policy
Change window
```

不能因为用户有 Admin UI role 就默认能 publish/deploy 所有策略。

---

# 63. Audit Contract

至少审计：

```text
Strategy creation
Version creation
Review / approval
Publish
Deployment request
Deployment result
Enable / Disable
Priority / Arbitration change
Schedule binding change
Guardrail / Fail-safe change
Rollback
Retire
Runtime drift
Conflict decision
Verification result reference
```

每条至少有：

```text
Who
When
Before / After
Reason
Revision
Approval reference
Affected scope
```

---

# 64. Query / Read Model Contract

Strategy Ledger 使用服务端 portfolio read model。

禁止：

```text
100 strategies
→ 100 deployment queries
→ 100 runtime queries
→ 100 conflict queries
→ 100 verification queries
```

Summary read model 至少包含：

```text
identity
scope
published/runtime version
enabled/eligible/active
trigger/schedule summary
conflict
control authority
guardrail/fail-safe
last execution
verification
```

---

# 65. No Universal Rule Engine UI

禁止建设：

```text
GenericRuleBuilder
UniversalConditionEditor
BaseStrategy<T>
AnyPointAutomationEditor
```

Strategy semantics 由 domain / strategy type 决定。

可以共享：

```text
Ledger
Status strip
Version diff shell
Approval shell
Schedule summary
Execution summary
```

但共享组件不决定业务逻辑。

---

# 66. AI Assistance Boundary

AI 可以：

```text
总结 strategy current state
解释为什么 Enabled 但不 Active
解释 conflict
解释 version drift
整理 deployment / execution history
草拟 review note
指出缺失 verification
```

AI 不能：

```text
自动启用策略
自动禁用策略
自动提高 priority
自动修改 trigger
自动解决 conflict
自动发布新版本
自动部署
自动 rollback
把 AI 解释变成 authoritative fail-safe
```

AI 建议必须进入 27 /治理流程。

---

# 67. No Defensive Programming / No Compatibility Design

明确禁止：

```text
strategy API error → []

published version unavailable
→ use latest draft

runtime version unavailable
→ assume published version

runtime state unavailable
→ disabled
runtime state unavailable
→ active

enabled → active
trigger true → active
schedule active → active

missing trigger input
→ false
missing trigger input
→ last known

priority unavailable
→ default priority

conflict service unavailable
→ no conflict

guardrail unavailable
→ normal
fail-safe unavailable
→ normal

input stale
→ still valid automatically

strategy disabled
→ release all overrides
strategy disabled
→ restore defaults

publish
→ deploy

deploy completed
→ enabled

enabled
→ verified

last execution success
→ strategy healthy

verification v7 pass
→ v8 pass

network timeout
→ auto retry deploy

rollback requested
→ restored

same strategy name
→ merge

same target
→ conflict

multiple strategy APIs
→ first success wins

old Automation Dashboard fallback
legacy rule-center compatibility adapter
raw BAS program fallback
```

正式原则：

> **一个 Strategy 对应一个权威 Versioned Lifecycle。Published、Deployed、Enabled、Eligible、Active 和 Verified 是不同事实。Priority 只服务 arbitration，不代表质量。Conflict、Guardrail、Fail-safe 和 Runtime State 必须来自权威 owner。Disable 不等于恢复，Rollback 不等于已恢复，Unknown 保持 Unknown。**

---

# 68. Accessibility Contract

- Strategy Ledger 使用 semantic table；
- lifecycle / runtime / conflict 不只靠颜色；
- keyboard 可完成筛选、选择、打开详情；
- enable/disable/publish/deploy/rollback 等高风险操作有清晰 confirmation；
- 状态更新不抢 focus；
- runtime updates 不频繁 announce；
- priority/conflict 有文字解释；
- 768px 下仍能查看 strategy、version、scope、enabled/active、conflict、last execution、verification；
- 无 page-level 横向溢出。

---

# 69. Browser Acceptance Criteria

## Strategy Truth

- Published / Deployed / Enabled / Eligible / Active 分开；
- Published Version 和 Runtime Version 都可见；
- runtime version unknown 不假定 published；
- Enabled 不显示成 Active；
- Active 不显示成 Verified。

## Schedule / Trigger

- Schedule 与 Strategy 分开；
- Trigger True 不自动显示 Active；
- missing/stale input 不前端猜 false；
- exception / next transition 来自 schedule owner。

## Priority / Conflict

- Higher Priority 不显示为“更优策略”；
- conflict 有明确 owner/reason/state；
- conflict evaluator unavailable 不显示 No Conflict；
- arbitration result 可解释。

## Guardrail / Fail-safe

- Guardrail Unknown 不显示 Normal；
- Fail-safe definition / state 可追溯；
- Fail-safe 与 Rollback 分离；
- Interlock 无通用 bypass。

## Deployment

- Publish 不自动 Deploy；
- Deploy 不自动 Enable；
- partial deployment 可见；
- runtime version drift 可见；
- timeout 不自动 retry；
- rollback 需要确认 restored runtime version。

## Verification

- 当前 version verification 独立；
- v7 PASS 不继承到 v8；
- execution success 不等于 functional verification；
- 13 为 verification owner。

## Accessibility / Responsive

- semantic table；
- 状态不只靠颜色；
- keyboard 可完成主要流程；
- 768px 保留关键治理事实；
- 无横向 page overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无页面直接 raw BAS program / point write；
- 无前端自动 retry lifecycle mutation；
- 无前端根据 trigger/schedule 推导 authoritative activity；
- 无 N+1 strategy/deployment/runtime/conflict queries；
- 无 generic rule builder；
- 无 legacy Automation compatibility adapter；
- review scenario 无 runtime/network error。

---

# 70. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 策略中心 · 中央园区                                      自动化运行正常   │
│ Scope：全站 / HVAC / DER                                                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ 已发布 12   已部署 11   活动中 6   冲突 1   部署漂移 1   待验证版本 2      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 策略              Scope        发布/运行版本   启用/活动   冲突    验证      │
│ 冷站低负荷优化     冷站         v8 / v8         启用·活动   无      v8 已通过 │
│ AHU SAT Reset      AHU-01..14   v6 / v5         启用·活动   无      v5 已通过 │
│ 需量响应 Shed      全站         v4 / v4         启用·未活动 1 个     v4 待验证 │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 选中：AHU SAT Reset                            │ 快速判断                     │
│ 已发布版本：v6                                 │ 运行版本：v5                 │
│ 部署状态：存在版本漂移                         │ 当前：启用 · 活动            │
│ Trigger：Occupied + cooling demand             │ Control source：STR-22 v5    │
│ Schedule：工作日 · 当前有效                    │ Conflict：无                 │
│ Guardrail：正常                                │ Verification：仅 v5 已通过   │
│ Fail-safe：已配置 · 当前未触发                 │                              │
│                                               │ [打开策略详情]               │
│ Next action                                    │ [查看部署记录]               │
│ 将 v6 部署到 Runtime 并完成 Functional Test    │ [查看执行记录]               │
├──────────────────────────────────────────────────────────────────────────────┤
│ Portfolio Issues                                                             │
│ · AHU SAT Reset：Published v6 / Runtime v5                                    │
│ · 需量响应 Shed：与 EV Charging Strategy 存在 Active Conflict                │
│ · 冷站夜间策略：v9 已部署但尚未完成 Functional Verification                  │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任与信息层级，不是像素规范。

---

# 71. Explicit Non-goals

26 不是：

- 通用 Rule Builder；
- BAS program editor；
- raw Schedule editor；
- raw BACnet priority editor；
- 单次即时控制页；
- Strategy simulation authoring 页；
- Strategy approval detail 页；
- Execution protocol log；
- Alarm Rule editor；
- interlock bypass console；
- 自动修复 conflict 的 AI console。

---

# 72. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Strategy / Version / Deployment 语义明确；
- Published / Deployed / Enabled / Eligible / Active 分离；
- Schedule / Trigger 分离；
- Priority / Arbitration 语义明确；
- Conflict Contract 明确；
- Control Authority 明确；
- Guardrail / Interlock / Fail-safe 边界明确；
- Deployment Drift / Partial Deployment 明确；
- Execution summary / 28 boundary 明确；
- Functional Verification / 13 boundary 明确；
- Rollback / Fail-safe 分离；
- Disable 不等于恢复；
- No Automatic Retry 明确；
- Security / Audit boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
