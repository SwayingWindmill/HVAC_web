# 27 策略详情 / 仿真 / 审批 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `27 策略详情 / 仿真 / 审批`  
> **Route intent：** `/sites/:siteId/strategies/:strategyId`、`/sites/:siteId/strategies/:strategyId/versions/:version`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → `DESIGN.md` → 本文件  
> **产品语言：** 中文优先；`Strategy`、`Simulation`、`Historical Replay`、`Guardrail`、`Fail-safe`、`Rollback`、`Functional Test` 等标准术语只作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有策略详情、旧规则编辑器、旧 BAS Program 页面、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 Strategy Definition / Version / Simulation / Approval / Publish / Deployment / Verification contract 的候选证据来源。

---

# 1. Primary Job

本 Surface 的核心任务是：

> **把一条自动化控制策略变成一个可理解、可评审、可仿真、可批准、可发布、可交付验证的版本化工程对象，并确保每一个关键设计决策都能追溯到输入、约束、假设、风险和验证要求。**

本 Surface 是 **strategy engineering + simulation + approval workspace**，不是：

- 25「控制中心」的一次即时控制页；
- 26「策略中心」的 Portfolio Ledger；
- 通用 `if-this-then-that` Rule Builder；
- raw BACnet / Modbus / PLC program editor；
- 任意 point expression editor；
- BOPTEST/Modelica 的完整 IDE；
- 一个只展示“仿真节能 15%”的大屏；
- 一个“一键发布到现场”的危险捷径；
- 一个把 Historical Replay 当作现场验证的页面；
- 一个允许审批绕过 Interlock / Safety / Control Authority 的页面。

用户完成评审后，应该能回答：

1. 这条 Strategy 的 Objective 是什么；
2. 当前正在评审哪个 Version；
3. 这个 Version 与上一版本到底改了什么；
4. Strategy 的 Scope / Inputs / Outputs 是什么；
5. Trigger / Schedule / Preconditions 如何决定参与运行；
6. Priority / Arbitration / Conflict Policy 是什么；
7. Guardrails 与 Fail-safe 分别是什么；
8. Strategy 在什么条件下停止、降级、释放或回退；
9. Simulation 使用了什么 Model / Version / Inputs / Assumptions；
10. Simulation 结果证明了什么、没有证明什么；
11. Historical Replay 使用了哪段数据，数据覆盖和质量如何；
12. Expected Impact 是什么，是否带不确定性；
13. 风险、限制和 Missing Evidence 是什么；
14. Functional Test Definition 是否已经定义；
15. Rollout / Rollback 的目标版本与前提是什么；
16. 当前 Version 是否可以审批；
17. Approval 到底批准了哪个 Version / Scope；
18. Publish 是否完成；
19. Publish 后应该交给哪个 Deployment / Verification workflow；
20. 哪些事实仍然 Unknown / Inconclusive。

---

# 2. 主要用户

## Primary

### 控制工程师

负责 Strategy definition、版本、inputs/outputs、trigger、guardrail、fail-safe、simulation、test definition 与 rollout/rollback 设计。

### HVAC / 系统工程师

评审 Strategy 是否符合设备、系统、Sequence、舒适、可靠性和维护要求。

### Commissioning / Verification Engineer

评审 Functional Test Definition、Expected Behavior、Test Conditions 和现场验证入口。

## Secondary

- Energy / Optimization Engineer：提供 21/22 的改善机会、预期收益和约束；
- Facility Manager：评审影响范围、可操作性、维护窗口和风险；
- OT Security / Platform Owner：评审 deployment/runtime 边界；
- Safety / Control Owner：提供 Interlock / Safety boundary；
- Approver：针对明确 Version 审批；
- Auditor：检查 Definition / Simulation / Approval / Publish / Rollback lineage。

---

# 3. 外部最佳实践依据

## 3.1 DOE OpenBuildingControl — Control Strategy 必须可规范、可模拟、可实现、可验证

DOE/LBNL OpenBuildingControl 的目标是把 control sequence 的设计、performance evaluation、implementation 和 commissioning / verification 数字化并连接起来，减少人工翻译造成的错误。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control
- https://www.energy.gov/cmei/buildings/control-platforms

**本页采用：**

```text
Strategy Definition
→ Simulation / Evaluation
→ Approved Version
→ Publish
→ Deployment
→ Functional Verification
```

- Strategy Definition 必须 versioned；
- simulation 结果必须绑定 model/version/config；
- 工程逻辑与运行 implementation 必须有明确 handoff；
- field verification 独立存在。

## 3.2 ASHRAE Guideline 36-2024 — 高性能 Sequence 需要稳定性与 Functional Test

ASHRAE Guideline 36-2024 的目的包括最大化 HVAC energy efficiency / performance、提供 control stability、支持 real-time FDD，并描述用于确认 sequence implementation 的 functional tests。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- Strategy 的 Objective 不能只写“省能”；
- Stability、equipment protection、comfort、IAQ、operability 都可以是策略设计目标/约束；
- Functional Test Definition 是 Strategy Version 的正式工程输出；
- 版本发生控制逻辑变化后，旧版 verification 不继承。

## 3.3 DOE BOPTEST — Simulation / Benchmarking 是低风险评估，不是现场证明

DOE BOPTEST 提供模拟建筑、标准 API 和 KPI，用于比较、benchmarking、debugging control algorithms。

来源：

- https://www.energy.gov/cmei/buildings/boptest-building-operations-testing-framework
- https://ibpsa.github.io/project1-boptest/

**本页采用：**

```text
Simulation Result
≠ Field Verification
```

- 仿真必须记录 Test Case / Model / Climate / Boundary / KPI / Version；
- 标准 KPI 支持公平比较，但不自动代表目标站点现场结果；
- simulation 可以支持 approval，但不能替代 13 Functional Verification。

## 3.4 DOE / CDL — Control Logic 应具有明确、可移植、可验证的表达

DOE Controls Platform 资料说明 CDL 用于标准化 control logic 表达，使其可用于 simulation 并编译到商业控制平台。

来源：

- https://www.energy.gov/cmei/buildings/control-platforms

**本页采用：**

- Strategy Definition 不使用前端自创文本 DSL 作为唯一权威；
- UI 负责解释 owner-defined logic，而不成为编译器；
- logic source / artifact / schema/version 必须可追溯。

## 3.5 NIST SP 800-82 Rev.3 — Strategy Publish/Deployment 是 OT Change

NIST SP 800-82 Rev.3 将 Building Automation Systems 纳入 OT，并要求兼顾 performance、reliability 和 safety。

来源：

- https://csrc.nist.gov/pubs/sp/800/82/r3/final

**本页采用：**

- Strategy mutation 不使用普通 optimistic UI；
- Approval 不能绕过当前 OT safety / authority checks；
- Publish / Rollback 必须有 revision、audit 和后续 deployment verification；
- 网络超时不自动 retry 高风险 lifecycle mutation。

## 3.6 ISA-101 — 工程页仍然必须是可操作 HMI，而不是配置字段仓库

ISA-101 强调 HMI lifecycle、usability、operator performance、screen hierarchy、dynamic elements、security 和 configuration interfaces。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- https://www.isa.org/standards-and-publications/isa-standards/isa-standards-committees/isa101

**本页采用：**

- 首屏先解释 Objective / Change / Risk / Evidence / Approval Readiness；
- 不让 reviewer 在 80 个字段之间寻找最关键差异；
- Version Diff 和 unresolved issues 优先于装饰性图表；
- status 不只靠颜色。

---

# 4. 产品语言契约

主界面中文优先：

```text
策略详情
策略目标
当前版本
版本差异
作用范围
输入
输出
触发条件
计划时段
执行前提
运行护栏
优先级 / 仲裁
冲突策略
失效保护
仿真
历史回放
预期影响
风险与限制
功能测试定义
审批
发布
部署交接
回滚目标
```

可以保留：

```text
Strategy
Simulation
Historical Replay
Guardrail
Fail-safe
Rollback
Functional Test
BOPTEST
CDL
```

不默认使用：

```text
Authoring
Runtime Binding
Artifact Digest
Policy Evaluation
Deployment Manifest
```

这些进入工程详情或审计。

---

# 5. Strategy Engineering Vocabulary

## Strategy Definition

控制策略完整工程定义，包含 Objective、Scope、Inputs、Outputs、Activation Logic、Constraints、Arbitration、Fail-safe、Expected Impact 和 Verification Intent。

## Strategy Version

不可变 Strategy Definition revision，例如：

```text
STR-18 v7
```

## Logic Artifact

由 Strategy owner 管理的 machine-readable control logic / configuration artifact，例如 owner-defined CDL / control package / strategy schema。

## Simulation Run

某一 Strategy Version 在明确 Model / Test Case / Inputs / Assumptions 下的一次 simulation execution。

## Historical Replay

使用历史数据作为输入，对某 Strategy Version 进行离线 replay/evaluation 的一次分析运行。

## Version Diff

两个 immutable Strategy Version 之间的结构化差异。

## Approval

针对明确 Strategy Version、Scope、条件和有效期的治理决策。

## Publish

将 Approved Strategy Version 标记为可进入 Deployment workflow 的正式生命周期动作。

## Functional Test Definition

验证该 Strategy Version 现场行为的 requirement-driven test definition。

---

# 6. Mandatory Semantic Separation

```text
Strategy Definition ≠ Strategy Version

Draft ≠ Approved
Approved ≠ Published
Published ≠ Deployed
Deployed ≠ Enabled

Simulation ≠ Historical Replay
Simulation Passed ≠ Field Verified
Historical Replay Passed ≠ Field Verified
Historical Replay ≠ Counterfactual Truth

Expected Impact ≠ Observed Impact
Expected Savings ≠ Verified Savings

Model Fit ≠ Strategy Validity
Model Quality ≠ Site Applicability

Version Diff ≠ Runtime Diff

Trigger ≠ Precondition
Precondition ≠ Guardrail
Guardrail ≠ Interlock
Fail-safe ≠ Rollback

Priority ≠ Strategy Quality
Priority ≠ Approval Importance

Approval ≠ Deployment
Approval ≠ Interlock Bypass
Approval ≠ Control Authority

Publish ≠ Runtime Loaded

Rollback Target Selected ≠ Rollback Completed
Rollback ACK ≠ Restored Runtime State

Functional Test Definition ≠ Functional Test Result
Functional Test PASS ≠ Verified Savings
```

---

# 7. Primary Questions

页面必须让 reviewer 回答：

1. 为什么需要这条 Strategy；
2. 当前 Version 相比上一版改了什么；
3. 改动是否改变控制风险；
4. 哪些对象会被读、哪些会被写；
5. 数据来源是否权威且质量足够；
6. Trigger 是否完整；
7. Preconditions 是否合理；
8. Guardrails 是否覆盖关键舒适、IAQ、设备和 grid constraint；
9. Interlock boundary 是否保持；
10. Priority / Arbitration 是否可能产生 conflict；
11. Fail-safe 是否明确；
12. Simulation 是否覆盖 relevant scenarios；
13. Replay 是否有足够历史数据；
14. Simulation / Replay 有哪些 limitation；
15. Expected Impact 是什么；
16. 是否存在 contradictory evidence；
17. Functional Test Definition 是否足够验证现场实现；
18. Rollout / Rollback 是否完整；
19. 当前 Version 是否满足 approval readiness；
20. Approval 后能否 Publish；
21. Publish 后进入哪个 Deployment / Verification workflow。

---

# 8. Information Architecture

```text
Strategy Header
  Identity / Objective / Current Version / Governance State
↓
Version Diff / Change Summary
↓
Engineering Definition
  Scope
  Inputs
  Outputs
  Trigger / Schedule
  Preconditions
  Guardrails
  Priority / Conflict Policy
  Fail-safe
↓
Simulation Workspace
↓
Historical Replay Workspace
↓
Expected Impact / Risk / Applicability
↓
Functional Test Definition
↓
Rollout / Rollback
↓
Approval Readiness
↓
Approval / Publish
↓
Deployment Handoff
↓
Audit / Version History
```

采用 **single-version engineering workspace + section navigation**，不做二十个同权重 Tabs。

---

# 9. Route / URL State Contract

Canonical routes：

```text
/sites/:siteId/strategies/:strategyId
/sites/:siteId/strategies/:strategyId/versions/:version
```

Search Params 可以包括：

```text
section
diffFrom
simulationRun
replayRun
scenario
approval
```

不进入 URL：

```text
unsaved form state
confirmation dialog
local validation errors
hover
local disclosure state
```

当前 Version 属于 URL / durable context，不使用组件本地 state 隐式切换。

---

# 10. Entry / Source Contract

可以从：

```text
22 优化方案
26 策略中心
25 控制中心
28 执行记录
13 功能验证
10 诊断中心
```

进入。

入口上下文可以携带：

```text
strategyId
version
approvedPlanRevision
executionId
verificationId
sourceFinding
selectedScope
```

来源 URL 不能成为 approval / publish 权限来源。

---

# 11. Strategy Identity Contract

至少包含：

```text
Strategy ID
中文名称
Strategy Type
Owner
Domain
Site / Scope
Current Draft Version
Published Version
Runtime Version（只读引用）
Successor / Predecessor（若存在）
Source Opportunity / Optimization Plan
```

名称变更不能改变 Strategy ID。

---

# 12. Strategy Version Contract

每个 Version 是 immutable engineering object。

至少绑定：

```text
Version
Definition Revision / Hash
Created By
Created At
Parent Version
Change Reason
Change Summary
Logic Artifact Version
Simulation References
Replay References
Functional Test Definition Revision
Approval State
Publish State
```

修改以下任一关键内容必须新 Version：

```text
Objective
Scope
Input meaning / source
Output meaning / mapping
Trigger logic
Schedule binding
Precondition
Guardrail
Priority / Arbitration policy
Conflict policy
Fail-safe
Control limit
Functional Test Definition
```

不能原地编辑 Approved/Published Version。

---

# 13. Version Diff Contract

Version Diff 应优先显示语义差异，而不是文本 diff。

建议分类：

```text
Objective
Scope
Input
Output
Trigger
Schedule
Precondition
Guardrail
Priority / Arbitration
Conflict Policy
Fail-safe
Expected Impact
Functional Test
Rollout / Rollback
```

示例：

```text
v6 → v7

Trigger
负荷 < 35%
→ 负荷 < 30% 且持续 10 min

Guardrail
CHWS lower bound 5.5°C
→ 6.0°C

Fail-safe
无变化
```

raw artifact diff 可以进入 Advanced / Engineering detail。

---

# 14. Version Diff Risk Contract

Diff owner 可以把变更分类为：

```text
Editorial / Documentation
Non-behavioral Metadata
Behavioral Logic Change
Scope Change
Safety / Guardrail Change
Priority / Arbitration Change
Fail-safe Change
Verification Definition Change
```

Risk classification 属于 Strategy Governance owner。

前端不能根据 changed-lines 数量判断风险。

---

# 15. Objective Contract

Strategy Objective 必须是可理解的工程目标，例如：

```text
在低负荷工况降低冷站综合 kW/RT，
同时维持 CHWS、关键区域舒适与设备最小运行约束。
```

避免：

```text
Optimize plant
```

Objective 可以关联：

```text
Energy
Demand
Comfort
IAQ
Reliability
Resilience
Maintenance
Grid Flexibility
```

但必须明确 primary objective 和 constraints。

---

# 16. Scope Contract

Scope 必须来自 authoritative semantic/control model：

```text
Site
System
Equipment group
Zones
Control targets
Runtime targets
```

不能从：

```text
设备名
tag string contains
前端目录层级
```

猜 Scope。

Scope change 是 versioned behavioral change。

---

# 17. Inputs Contract

每个关键 Input 至少定义：

```text
Semantic Name
Source Object
Unit / Enum
Freshness Requirement
Quality Requirement
Sampling / Aggregation
Valid Range / Applicability
Fallback / Missing behavior（owner-defined）
```

例如：

```text
Plant Cooling Load
Source: validated plant load analytics
Unit: RT
Freshness: <= 2 min
Quality: GOOD
```

不能只写 raw point ID。

---

# 18. Missing / Bad Input Contract

每个 Strategy 必须明确关键 Input 缺失或质量异常时的行为：

```text
Block Eligibility
Hold Last Safe Output（仅 owner 明确批准）
Relinquish
Enter Degraded Mode
Fail-safe
Continue with Reduced Input Set（必须明确定义）
```

不能由 UI / runtime adapter 做：

```text
value || 0
last good forever
missing → false
```

---

# 19. Outputs Contract

Output 用业务语义定义：

```text
CHWS Setpoint Request
Chiller Staging Request
SAT Reset Target
Static Pressure Reset Target
Battery Dispatch Limit
EV Charging Power Limit
```

每个 Output 至少说明：

```text
Target
Intent Type
Unit / Enum
Allowed Bounds
Rate Limit / Ramp（若适用）
Priority / Arbitration Group
Verification requirement
Control owner
```

raw point mapping 作为 engineering reference，不作为用户主要语义。

---

# 20. Trigger Logic Contract

Trigger Definition 至少包括：

```text
Inputs
Logic
Threshold / State
Persistence / Delay
Hysteresis / Deadband（若适用）
Reset / Exit Condition
Owner
Version
```

如果是 composite trigger，页面需要 human-readable logic summary。

完整 machine-readable logic 由 Strategy owner 管理。

前端不自行执行 trigger calculation 作为权威结果。

---

# 21. Schedule Contract

Schedule 可以是 Activation input，但必须引用 versioned Schedule owner 对象：

```text
Schedule ID
Revision
Timezone
Current Rules
Exception Policy
```

Strategy Version 记录 binding revision。

后续 Schedule revision 是否自动应用，必须由 contract 明确：

```text
Pinned Revision
或
Follow Current Approved Schedule
```

不能模糊。

---

# 22. Trigger vs Precondition Contract

```text
Trigger
= 何时应该尝试参与策略

Precondition
= 参与前必须满足什么
```

例如：

```text
Trigger:
Plant load < 30%

Precondition:
At least one eligible chiller available
Remote mode available
Required flow sensors valid
```

Trigger True 但 Precondition Blocked 时，Strategy 不能进入 Eligible。

---

# 23. Preconditions Contract

每个 Precondition 定义：

```text
Name
Source
Pass Criteria
Unknown behavior
Evaluation owner
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

# 24. Guardrail Contract

Guardrail 是 Strategy 运行时持续保持的边界。

示例：

```text
Critical Zone Temp
Minimum Ventilation
CHWS Bounds
Chiller Loading
DP Bounds
Battery Reserve
Grid Import Limit
```

每个 Guardrail 至少说明：

```text
Metric / Source
Allowed Range
Warning / Limit（若 owner 定义）
Persistence
Response
Owner
```

Response 可以是：

```text
Alert
Degrade
Suspend
Relinquish
Rollback Request
```

但必须由 Strategy definition 明确。

---

# 25. Interlock Boundary

Interlock 属于 Control / Safety owner。

Strategy Definition 可以声明：

```text
Required Interlock Clear
```

但不能定义：

```text
Bypass Interlock
```

除非存在独立受治理 workflow；默认不支持。

Approval / Publish 不能绕过实时 Interlock。

---

# 26. Priority / Arbitration Contract

必须明确：

```text
Arbitration Group
Priority Class / Rule
Tie-break rule
Manual Override relation
Higher-level Strategy relation
Shared Resource policy
```

Priority 不是 `1 = 最好`。

如果 owner 使用不同机制，例如 optimization / voting / supervisory controller，应按真实机制表达，不强制降维成整数。

---

# 27. Conflict Policy Contract

Strategy Version 可以声明：

```text
Mutually Exclusive With
Can Compose With
Requires Arbitration With
Shared Resource Constraint
Output-specific Conflict Rule
```

冲突检测仍由 authoritative evaluator 负责。

UI 可以在 engineering stage 显示 potential conflict analysis，但不能把 heuristic 当 runtime fact。

---

# 28. Fail-safe Contract

高影响 Strategy 必须明确 Fail-safe：

```text
Trigger Condition
Action
Target State / Control Source
Authority
Timeout
Recovery Condition
Verification Requirement
```

示例：

```text
关键 load sensor quality bad > 2 min
→ Suspend optimization
→ Relinquish CHWS reset
→ baseline sequence 接管
```

Fail-safe Action 必须能在 25 / 28 观察和审计。

---

# 29. Fail-safe vs Degraded Mode Contract

```text
Degraded Mode
= 策略继续运行，但能力 / 输入 /输出受限

Fail-safe
= 异常条件下进入安全预定义行为
```

二者不能混。

如果支持 Degraded Mode，必须说明：

```text
Allowed Inputs
Allowed Outputs
Changed Constraints
Exit Criteria
Expected Impact
```

---

# 30. Simulation Contract

每次 Simulation Run 必须绑定：

```text
Simulation ID
Strategy Version
Model / Test Case
Model Version
Configuration
Climate / Weather Input
Boundary Conditions
Initial Conditions
Input Dataset
Scenario
Duration
Warm-up
KPI Definition / Version
Assumptions
Created By
Started / Completed At
```

结果不可脱离这些上下文独立存在。

---

# 31. Simulation Scenario Contract

建议场景类型：

```text
Nominal
High Load
Low Load
Shoulder Season
Extreme Weather
Occupancy Variation
Sensor Fault / Missing Input
Equipment Unavailable
Manual Override
Grid / Tariff / DR Event
Recovery / Restart
```

哪些场景 mandatory 由 Strategy Type / Governance owner 决定。

前端不硬编码所有策略必须跑相同场景。

---

# 32. Simulation KPI Contract

BOPTEST 等框架支持 Energy、Cost、Peak、Comfort、Control performance 等 KPI。

页面可以显示：

```text
Energy
Demand
Cost
Comfort violation
IAQ constraint
Equipment cycling
Control error
Response time
Stability metric
```

但每个 KPI 必须带：

```text
Definition
Unit
Boundary
Method
Version
```

不能仅显示：

```text
Score 92
```

---

# 33. Simulation Result Contract

建议状态：

```text
Completed
Completed with Warnings
Failed
Inconclusive
Cancelled
Invalidated
```

`Simulation Passed` 只能在存在 owner-defined acceptance criteria 时使用。

```text
Simulation favorable
≠ Approval
```

---

# 34. Simulation Applicability Contract

必须展示：

```text
Model applicability
Scenario coverage
Input coverage
Known limitations
Unmodeled effects
Uncertainty / sensitivity（若 owner 有）
```

例如：

```text
未建模：实际 valve stiction
未建模：manual intervention
历史天气仅覆盖夏季
```

这些不能藏在附件里。

---

# 35. Simulation vs Field Contract

```text
Simulation
= 在模型中评估策略行为

Functional Verification
= 在真实/受控现场条件下验证实现
```

所以：

```text
Simulation PASS
→ Publish candidate / approval evidence
```

而不是：

```text
Simulation PASS
→ Verified in field
```

---

# 36. Historical Replay Contract

Historical Replay 至少绑定：

```text
Replay ID
Strategy Version
Historical Dataset ID
Dataset Window
Timezone
Coverage
Quality
Missing Data
Event / Override Context
Replay Engine / Version
Assumptions
Output Mapping
```

Replay 结果是离线 evidence。

---

# 37. Historical Replay Limitations Contract

Historical Replay 必须明确：

```text
Replay Output
≠ Counterfactual Truth
```

原因可能包括：

```text
未记录 actuator dynamics
未记录 local control state
manual intervention incomplete
historical sensor bias
missing weather / occupancy
strategy output would have changed future states
```

因此不能把：

```text
Replay saved 12%
```

直接写成：

```text
Expected Savings = 12%
```

除非独立 analytics owner 明确提供 estimate method。

---

# 38. Replay Data Quality Contract

至少显示：

```text
Coverage
Missing
Estimated / corrected data
Sensor quality
Excluded periods
Known event gaps
```

Missing 不得自动填 0 或 silently forward-fill。

Replay engine 的 missing-data policy 必须 versioned / explicit。

---

# 39. Expected Impact Contract

Expected Impact 可以包括：

```text
Energy
Demand
Cost
Carbon
Comfort
IAQ
Equipment wear / cycling
Reliability
Resilience
Operational workload
```

每个 Expected Impact 必须说明：

```text
Method
Source
Boundary
Period
Assumptions
Uncertainty / Confidence semantics
Version
```

Expected Impact 不等于现场结果。

---

# 40. Expected Savings Boundary

Expected Savings 可以来自：

```text
22 Optimization Plan
Simulation analytics
Replay analytics
Engineering estimate
```

但必须保留 source/version。

```text
Expected Savings
≠ Verified Savings
```

真正 Verified Savings 进入 24 M&V。

---

# 41. Risk / Constraint Contract

至少覆盖：

```text
Comfort / IAQ
Equipment Protection
Reliability
Control Stability
Operator Workload
Maintenance
Grid / Tariff
Cyber / OT
Data Quality
Model Applicability
Rollback Complexity
Verification Gap
```

Risk owner 提供 rating / rationale。

前端不通过 keywords 自动定 Low/Medium/High。

---

# 42. Contradicting Evidence Contract

Engineering Review 必须允许记录：

```text
Supporting Evidence
Contradicting Evidence
Missing Evidence
Unresolved Questions
```

例如：

```text
Supporting:
Simulation 低负荷节能 11–14%

Contradicting:
历史回放显示部分 AHU 舒适偏离增加

Missing:
冬季场景尚未仿真
```

Approval readiness 必须能看到这些事实。

---

# 43. Functional Test Definition Contract

每个需要现场验证的 Strategy Version 应关联一个明确 Test Definition：

```text
Requirement
Preconditions
Test Conditions
Expected Behavior
Observed Points
Criteria
Tolerance
Timing
Pass / Fail / Inconclusive logic owner
Evidence requirements
Safety / Control constraints
```

定义产生于 27，执行/结果属于 13。

---

# 44. Functional Test Version Binding

```text
Strategy v7
→ Functional Test Definition FT-18 v4
```

如果 Strategy logic 发生影响 test criteria 的变化，应产生新的 Test Definition revision。

旧 Strategy / Test Definition pairing 保持历史完整。

---

# 45. Approval Readiness Contract

Approval Readiness 不做黑盒 score。

显示可解释 checklist，例如：

```text
Definition complete
Version diff reviewed
Scope approved
Inputs / outputs resolved
Guardrails complete
Fail-safe complete
Conflict review complete
Simulation required scenarios complete
Replay complete / Not Required
Risk review complete
Functional Test Definition complete
Rollback target available
Deployment targets known
Open critical issues = 0
```

每项来源由 owner 定义。

---

# 46. Approval State Contract

建议：

```text
Draft
Ready for Review
In Review
Changes Requested
Approved
Approved with Conditions
Rejected
Expired
Superseded
```

`Approved with Conditions` 必须有 structured conditions。

条件必须成为 Publish / Deployment gate 的输入，不能只显示备注。

---

# 47. Approval Version Binding Contract

Approval 必须绑定：

```text
Strategy ID
Version
Definition Hash
Scope
Approval Conditions
Approver
Approved At
Valid Until（若适用）
```

关键内容一旦变化：

```text
v7 Approved
↓ edit trigger / guardrail / fail-safe
v8 Draft
```

不能继承 v7 approval。

---

# 48. Approval Independence Contract

```text
Approval
≠ Publish
≠ Deploy
≠ Enable
≠ Execute
```

Approval 只表示 governance owner 对明确 Version 的工程评审结论。

实时 Interlock、Control Authority、Runtime Safety 仍由后续 owner 检查。

---

# 49. Publish Contract

Publish 只能针对：

```text
Approved Version
满足 Approval Conditions
满足 Strategy Governance policy
```

Publish 产生：

```text
Published Version
Published At
Published By
Approval Reference
Artifact Reference
Publish ID
```

Publish 不直接调用 controller/runtime deployment。

---

# 50. Publish State Unknown Contract

如果 Publish mutation 响应丢失：

```text
Publish State Unknown
→ query by Publish ID
→ reconcile authoritative state
```

不能自动 retry。

---

# 51. Deployment Handoff Contract

Publish 后交给 26 / Deployment owner：

```text
Strategy Version
Artifact
Scope
Runtime Targets
Deployment Preconditions
Approval Reference
Change Window
Expected Runtime Version
Functional Verification Requirement
Rollback Target
```

27 不伪造 `Deployment Successful`。

---

# 52. Rollout Plan Contract

Rollout Plan 可以定义：

```text
Scope sequence
Pilot group
Canary / staged rollout
Change window
Monitoring period
Hold point
Go / No-go criteria
Verification checkpoint
Rollback trigger
```

哪些策略需要 staged rollout 由 Governance owner 决定。

前端不默认所有策略一次全站发布。

---

# 53. Rollback Target Contract

Rollback Target 必须是明确、可部署的已知 Version / Baseline Strategy：

```text
Rollback target = STR-18 v6
```

至少展示：

```text
Target Version
Last Known Deployment State
Last Verification State
Compatibility / Applicability
Known Limitations
```

不能：

```text
Rollback = previous
```

而不说明 previous 到底是谁。

---

# 54. Rollback Boundary

27 负责设计 Rollback Target / Conditions。

26/Deployment owner 负责运行时 Rollback lifecycle。

25 负责必要的即时控制恢复 intent。

13 负责 rollback 后必要的 Functional Verification。

```text
Rollback Plan exists
≠ Rollback configured
≠ Rollback completed
```

---

# 55. Version History Contract

历史至少显示：

```text
Version
Change Summary
Governance State
Simulation State
Replay State
Approval State
Publish State
Deployment References
Verification References
Created By / At
```

历史 Approved/Published Version 不删除、不覆盖。

---

# 56. Audit Contract

审计至少覆盖：

```text
Version created
Definition changed
Simulation run
Replay run
Risk review
Approval request
Approval decision
Approval conditions
Publish request/result
Rollback target change
Functional Test binding change
```

每条有：

```text
Who
When
Before / After
Reason
Version
Reference
```

---

# 57. Concurrent Editing Contract

Draft Version 可以被多人评审，但保存必须基于 authoritative revision。

如果用户基于旧 revision 修改：

```text
Conflict
→ reload authoritative draft
→ review diff
→ explicitly reapply
```

不使用 silent last-write-wins。

不在前端开发复杂自动 merge strategy logic。

---

# 58. Draft State / Unsaved Change Contract

Draft editor 明确区分：

```text
Saved Draft Revision
Unsaved Local Changes
```

切换 Version / Route 前如果存在 unsaved changes，应明确 confirmation。

但已批准/已发布版本始终只读。

---

# 59. Form / Editing Boundary

27 可以编辑 owner-defined Strategy Definition fields，但不提供：

```text
AnyPointExpressionEditor
Raw PLC Code Editor
Universal Script Editor
Raw BACnet Program Editor
```

如果 Strategy owner 提供 machine-readable logic artifact，可：

- 展示可读摘要；
- 下载/引用 artifact；
- 在受控专业 editor 中编辑；

但不能让通用前端表单决定编译语义。

---

# 60. Simulation Execution Boundary

Simulation mutation：

```text
UI
→ Strategy Simulation API
→ Simulation / BOPTEST / Model owner
```

页面不能自己在浏览器里跑简化 physics model，然后把结果当正式 simulation。

---

# 61. Historical Replay Execution Boundary

Replay mutation：

```text
UI
→ Replay API
→ Historian / Replay Engine
```

Replay owner 负责：

```text
alignment
missing data
quality
clock/timezone
event context
calculation
```

前端只配置 scenario/request 和展示结果。

---

# 62. No Automatic Retry Contract

以下 mutation 默认不前端自动 retry：

```text
Approval Submit
Approval Decision
Publish
Rollback-target change requiring governance
Simulation run with side-effectful reservation（若 owner 定义）
```

普通纯读查询可按 Query policy retry。

高风险 mutation timeout 后通过 operation ID reconciliation。

---

# 63. Security Boundary Contract

Frontend permission 只用于 affordance。

Backend 在每次关键 mutation 重新验证：

```text
Principal
Site Scope
Strategy Scope
Draft / Version State
Permission
Approval Authority
Publish Authority
Artifact Revision
Current Governance State
Change Window / Policy
```

Admin UI role 不等于 OT strategy publish authority。

---

# 64. Query / Read Model Contract

27 使用 version/detail read model。

一次读取应能得到：

```text
Identity
Version
Definition
Diff summary
Simulation summaries
Replay summaries
Expected Impact
Risks
Functional Test binding
Approval
Publish state
Deployment references
Verification references
```

不要：

```text
20 sections
→ 20 independent N+1 API calls
```

重型 Simulation / Replay evidence 可以按 section lazy-load。

---

# 65. Realtime Contract

Engineering Detail 默认不需要高频 telemetry stream。

只在：

```text
Current runtime reference
Deployment reference
Active conflict context
Live validation preview（owner-defined）
```

需要时订阅有限事实。

Realtime update 不能修改用户 Draft、关闭 Diff、覆盖 unsaved changes。

---

# 66. AI Assistance Boundary

AI 可以：

```text
总结 Version Diff
解释 Strategy Logic
整理 Simulation 结果
发现未覆盖 scenario
总结 Contradicting Evidence
草拟 Review Note
检查 Functional Test Definition 缺项
解释 Replay limitation
```

AI 不能：

```text
自动审批
自动 Publish
自动 Deploy
自动提高 Priority
自动修改 Guardrail / Fail-safe
把 Simulation 结论升级成现场事实
把 Replay savings 升级成 Verified Savings
把 AI 生成 logic 直接当 Approved Artifact
```

AI 草拟的 engineering change 必须进入 Draft + Review。

---

# 67. No Defensive Programming / No Compatibility Design

明确禁止：

```text
strategy detail API error → empty strategy

published version missing
→ latest draft

parent version missing
→ infer previous numeric version

input source unavailable
→ use raw point with similar name

input missing behavior undefined
→ use 0
→ use last known

trigger missing
→ false

precondition unavailable
→ passed

guardrail unavailable
→ normal

interlock unavailable
→ clear

fail-safe undefined
→ baseline strategy fallback guessed by frontend

priority undefined
→ default priority

conflict analysis unavailable
→ no conflict

simulation unavailable
→ passed

simulation failed
→ use previous successful run from another version

simulation favorable
→ approved

BOPTEST passed
→ field verified

historical replay favorable
→ expected savings authoritative

historical replay missing data
→ silently forward-fill

replay result
→ counterfactual truth

expected savings
→ verified savings

v6 approval
→ inherit to v7

v6 functional test PASS
→ v7 verified

approved
→ published

published
→ deployed

publish timeout
→ auto retry

rollback target = previous string
→ choose latest lower version

rollback plan exists
→ rollback completed

same strategy name
→ merge version histories

multiple strategy APIs
→ first success wins

old Rule Editor fallback
legacy Automation Strategy compatibility adapter
raw BAS program fallback
```

正式原则：

> **一个 Strategy Version 对应一个不可变工程定义。Simulation、Replay、Approval、Publish、Deployment 和 Verification 是不同证据与生命周期阶段。模型结果不是现场事实，历史回放不是反事实真相，Approval 不等于 Publish，Publish 不等于 Deploy，Unknown 保持 Unknown。**

---

# 68. Accessibility Contract

- Section navigation 可键盘操作；
- Version selector 有明确 label；
- Version Diff 不只靠红/绿颜色，必须显示 added/changed/removed 文本；
- Simulation / Replay chart 有表格或可读 summary；
- Approval condition 可被辅助技术读取；
- status 不只靠颜色；
- Dialog 焦点正确管理；
- realtime reference 不抢 focus；
- 768px 下仍能查看 Objective、Version Diff、Risk、Approval、Simulation summary；
- page 无横向全局 overflow。

---

# 69. Browser Acceptance Criteria

## Version Truth

- Draft / Approved / Published 分开；
- Approved Version 不可编辑；
- behavioral change 产生新 Version；
- Version Diff 是 semantic diff；
- runtime version 不冒充 engineering version。

## Engineering Definition

- Objective / Scope / Inputs / Outputs 清楚；
- Trigger / Preconditions / Guardrail 分开；
- Interlock 无 bypass；
- Priority 不等于质量；
- Fail-safe 明确。

## Simulation

- Simulation 有 Model/Test Case/Version/Scenario；
- 有 applicability / limitations；
- Simulation PASS 不显示 Field Verified；
- KPI definition/version 可追溯；
- 不能借用其他 Strategy Version 的 run 充当前 evidence。

## Historical Replay

- Replay 有 Dataset / Window / Coverage / Quality；
- Missing 不静默补零；
- Replay 不称 counterfactual truth；
- Replay savings 不称 Verified Savings。

## Approval

- Approval 绑定 Strategy Version / Definition Hash；
- change 后 approval 不继承；
- Approved with Conditions 条件结构化；
- Approval 不绕过 interlock；
- Approval 不自动 Publish。

## Publish / Handoff

- Publish 独立 operation；
- publish timeout 不自动 retry；
- Publish 后进入 Deployment workflow；
- 27 不显示虚假的 runtime success；
- Rollback Target 明确 Version。

## Verification

- Functional Test Definition 可见；
- Strategy Version 与 Test Definition revision 绑定；
- field result 进入 13；
- Expected Savings 不升级为 Verified Savings。

## Accessibility / Responsive

- keyboard 可完成 review；
- Diff 不只靠颜色；
- 768px 保留关键工程与审批事实；
- 无 page-level 横向溢出。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无通用 Rule Builder；
- 无浏览器端 physics/simulation 作为权威结果；
- 无前端 replay engine；
- 无自动 retry Publish；
- 无旧策略页兼容 adapter；
- review scenario 无 runtime/network error。

---

# 70. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 策略详情 · 冷站低负荷优化                              STR-18 · v7 Draft   │
│ Objective：低负荷下降低冷站综合 kW/RT，同时维持 CHWS / 舒适 / 最小运行约束 │
│ [版本 v7] [对比 v6]                                       [提交工程评审]    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 版本差异                                                                     │
│ Trigger      负荷 <35% → <30% 且持续 10 min                                 │
│ Guardrail    CHWS lower bound 5.5°C → 6.0°C                                 │
│ Fail-safe    无变化                                                           │
├───────────────────────────────────────────────┬──────────────────────────────┤
│ 工程定义                                      │ 评审状态                     │
│ Scope：中央冷站                              │ Definition      完整         │
│ Inputs：Load / CHWS / CHWR / Flow / OAT      │ Simulation      有警告       │
│ Outputs：CHWS reset / staging request        │ Replay          完成         │
│ Trigger：低负荷持续条件                      │ Risk Review     1 项未解决    │
│ Preconditions：Remote / sensors good         │ Functional Test 已定义       │
│ Guardrails：CHWS / comfort / min runtime     │ Approval        未提交       │
│ Priority：Plant optimization group           │                              │
│ Fail-safe：relinquish to baseline sequence   │ [查看未解决问题]             │
├──────────────────────────────────────────────────────────────────────────────┤
│ 仿真                                                                         │
│ BOPTEST / Plant Model v5 · Summer low-load · 14 days                         │
│ Energy -11.8%   Peak -4.2%   Comfort violation +0.3 h                       │
│ 结果：Completed with Warnings                                                 │
│ Limitation：未覆盖冬季；未模拟真实 valve stiction                            │
│ [打开仿真详情]                                                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 历史回放                                                                     │
│ Dataset：2026-06-01 → 2026-08-31 · Coverage 98.6%                            │
│ 结果：支持低负荷节能方向，但存在 2 个 AHU 舒适限制时段                       │
│ 注意：Historical Replay ≠ 现场反事实证明                                     │
├──────────────────────────────────────────────────────────────────────────────┤
│ 功能测试定义                                                                 │
│ Requirement：低负荷单机运行 + CHWS reset                                    │
│ Expected：稳定进入目标 stage，无持续振荡，Guardrail 不突破                   │
│ [打开 Test Definition]                                                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Rollout / Rollback                                                           │
│ Pilot：CH-02 · Change Window 22:00–01:00                                     │
│ Rollback target：STR-18 v6                                                   │
│ Go/No-go：关键传感器有效；无 critical alarm；v6 可部署                      │
├──────────────────────────────────────────────────────────────────────────────┤
│ 审批                                                                         │
│ 当前：Draft                                                                  │
│ Open issue：冬季场景未覆盖                                                    │
│ [提交工程评审]                                                               │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达责任与信息层级，不是像素规范。

---

# 71. Explicit Non-goals

27 不是：

- raw PLC / BAS programming IDE；
- universal rule builder；
- raw point expression console；
- BOPTEST / Modelica 完整客户端；
- Deployment runtime console；
- 一次即时控制页；
- Functional Verification result page；
- M&V page；
- interlock bypass console；
- AI 自动控制逻辑生成并直接发布的入口。

---

# 72. READY FOR WIREFRAME Decision

本 Surface 达到 READY FOR WIREFRAME 的条件：

- Strategy Definition / Version 明确；
- Version Diff 明确；
- Objective / Scope / Inputs / Outputs 明确；
- Trigger / Schedule / Preconditions / Guardrails 明确；
- Priority / Conflict / Fail-safe 明确；
- Simulation contract 明确；
- BOPTEST / model applicability 明确；
- Historical Replay contract 与 limitation 明确；
- Expected Impact / Risk / Contradicting Evidence 明确；
- Functional Test Definition 明确；
- Approval 与 Version 强绑定；
- Approved ≠ Published ≠ Deployed 明确；
- Rollout / Rollback target 明确；
- No Automatic Retry 明确；
- Security / Audit boundary 明确；
- No Defensive Programming 明确；
- 中文产品语言明确；
- Browser Acceptance Criteria 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
