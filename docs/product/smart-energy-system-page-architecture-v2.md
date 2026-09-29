# 智慧能源系统页面体系与交互蓝图 v2

> **状态：SUPERSEDED / HISTORICAL GREENFIELD BLUEPRINT**  
> **Superseded by:** `docs/product/smart-energy-system-page-architecture-v3-research-backed.md`  
> **日期：2026-09-14**  
> **上游审核：** `docs/product/smart-energy-system-page-architecture-audit-v1.md`  
> **设计输入声明：** 本文件不参考当前项目已有页面、旧路由、旧菜单、旧设计稿或旧组件布局。当前代码只可在后续实施阶段作为业务能力、数据契约和安全语义的候选来源。

---

# 1. 产品目标

这是一个企业级智慧能源与 HVAC 运营系统，不是“能源 Dashboard + 告警 + 设备表”。产品需要同时支撑三个可验证闭环：

## 1.1 运行闭环

```text
Observe
→ Detect
→ Validate
→ Diagnose
→ Prioritize
→ Correct
→ Functional Verification
→ Monitor
```

## 1.2 能源持续改进闭环

```text
Benchmark
→ Energy Review
→ Analyze
→ Opportunity
→ Engineering Proposal
→ Implement
→ Operational Verification
→ M&V
→ Persistence Monitoring
→ Management Review
```

## 1.3 控制闭环

```text
Strategy
→ Simulation
→ Approval
→ Publish
→ Intent
→ Attempt
→ ACK
→ Readback
→ Functional Verification
→ Energy + Comfort Result
```

---

# 2. 易用性与专业性的统一原则

## 2.1 Professional Depth without Professional Friction

系统必须做到：

- 普通值班人员能在 10–30 秒内理解当前最重要的问题。
- 工程师可以继续下钻到点位、模型、规则、基线、计算和审计证据。
- 两类用户使用同一个对象与上下文，不建立两套互相割裂的产品。

## 2.2 两层信息结构

每个复杂 Surface 默认分为：

### Operational View

回答：

1. 发生了什么？
2. 哪个最重要？
3. 影响什么？
4. 下一步做什么？

### Engineering Detail

进一步展开：

- raw/point evidence
- engineering units
- baseline/model method
- calculation lineage
- rule/model metadata
- control guardrail
- readback/audit

## 2.3 业务语言优先

导航和主要标题优先使用业务语言：

- 诊断中心，而不是 FDD
- 功能验证，而不是 MBCx
- 节能量验证，而不是 IPMVP Option C
- 能源绩效指标，而不是只写 EnPI

专业缩写保留在详情和专业模式中。

## 2.4 不以卡片数量表现“智慧”

首页和工作区不做 KPI Card Wall。最多使用紧凑 Facts Strip 展示少量关键事实，视觉主导必须是问题、工作、分析和下一动作。

## 2.5 Capability Gating

本文件定义完整 **Surface Catalog**，不是固定菜单。

页面是否出现取决于：

- 用户角色
- 权限
- 站点数据能力
- 计量能力
- 控制能力
- tariff / carbon / DER / IAQ 等真实集成

不存在的能力直接不显示，不长期放 disabled 菜单。

---

# 3. 全局导航模型

36 个 Surface 不能平铺。Sidebar 建议固定为少量业务组：

```text
总览
  企业总览 / 站点总览（角色决定默认入口）

运行
  系统运行
  设备
  趋势

事件与工作
  告警
  诊断
  工单
  功能验证

能源与绩效
  能源
  效率
  能源评审
  节能机会

优化与控制
  优化方案
  控制中心
  策略中心

管理
  站点对标
  目标与行动计划
  M&V
  报告
  管理评审

系统
  数据质量
  配置
```

以下通常不进入 Sidebar：

- 设备详情
- 工单详情
- 策略详情
- 执行记录
- 账单/碳/DER（能力存在时才加入对应业务组）
- 计量与语义模型
- 规则与通知
- 集成管理
- 用户权限审计

---

# 4. Global Shell

必须包含：

- Organization / Portfolio / Site Context
- Global Search / Command Palette
- 当前站点与时间上下文提示
- Notification / Task indicator
- 用户与权限入口
- Help / terminology help

Global Search 至少支持：

- Site
- Device
- Alarm
- Diagnosis
- Work Order
- Strategy
- Opportunity

AI 是 Shell 级跨页面 Copilot，但不是一级业务域。

---

# 5. 跨页 Context Contract

## 5.1 必须可持续传递

- `site`
- `timeRange`
- `selectedObject`
- `comparison`
- `baselineVersion`
- `sourceContext`

从能源分析选中 14:00–16:00 后进入效率、趋势、诊断，不能重新回到“今天”。

## 5.2 Quick Inspect → Durable Detail

统一交互：

```text
Ledger / Topology / Chart
        ↓ select
Context Inspector
        ↓ deeper investigation
Durable Detail Route
```

## 5.3 Return to Source

跨页调查链要显式显示来源：

`Alarm → Diagnosis → Work Order → Verification`

用户不应依赖浏览器 Back 才知道自己从哪里来。

---

# 6. 36 个 Surface Catalog

## Portfolio / Management

### 01 企业总览

**Primary job：** 找出整个组合中最值得管理层关注的站点、风险和节能结果。

**默认运营视图：**

- Portfolio 能源/成本/碳核心趋势（只显示已接入维度）
- 需要关注的站点
- 重大运行风险
- 高价值节能机会
- Verified Savings
- 目标进度

**专业深化：**

- normalized metrics
- region/business unit breakdown
- data completeness
- target/baseline definitions

**主要出口：** 02 站点对标、03 站点总览、16 效率、21 节能机会、24 M&V、30 管理评审。

**不负责：** 单站点实时控制、单设备详情。

---

### 02 站点对标

**Primary job：** 识别相对低效站点并确定投资/改进优先级。

**默认运营视图：**

- Site ranking
- EUI / EnPI
- current vs past / target / peers
- top / bottom performers
- weather-normalized comparison（适用时）

**专业深化：**

- peer group definition
- normalization variables
- benchmark standard/version
- confidence/data coverage

**主要出口：** 03 站点总览、14 能源分析、16 效率、17 能源评审。

---

### 03 站点总览

**Primary job：** 在一分钟内知道“这个站点现在最值得处理什么”。

**默认运营视图：**

- 当前运行模式
- 紧急/重要告警
- 未指派/超时工单
- 当前能源偏差
- 当前效率异常
- 数据可信度
- 高价值节能机会
- 最近重要变化

**专业深化：** 不在首页展开工程细节，只提供来源与下钻。

**主要出口：** 04 系统运行、09 告警、11 工单、14 能源、16 效率、21 节能机会、31 数据质量。

**设计原则：** attention router，不做模块入口墙。

---

## Operations

### 04 系统运行

**Primary job：** 理解 HVAC 当前怎样运行。

**默认运营视图：**

- 冷源 / 冷冻水 / 冷却水 / AHU/末端等系统切换
- 当前 operating mode
- stage / schedule / setpoint
- 设备群运行状态
- 关键温度、压力、流量、频率、功率、COP
- 当前异常
- 运行拓扑（仅真实关系）
- Context Inspector

**专业深化：**

- sequence state
- interlock
- control authority
- point-level evidence

**出口：** 05 趋势、07 设备详情、09 告警、10 诊断、25 控制、27 策略详情。

---

### 05 趋势分析

**Primary job：** 用时序证据调查运行问题。

**默认运营视图：**

- 2–6 个核心点位
- 时间范围
- event markers
- zoom / crosshair
- 简要统计

**专业深化：**

- multi-axis
- engineering units
- min/max/avg/P95/runtime
- baseline overlay
- sampling/data-quality semantics
- raw export

**出口：** 10 诊断、07 设备详情、28 执行记录。

---

### 06 设备中心

**Primary job：** 快速扫描和筛选真实设备群体。

**默认运营视图：**

- Ledger/Table first
- 名称、类型、位置
- Operating state
- Connectivity
- Freshness
- Quality
- 关键值
- 最后更新时间

**专业深化：** 可选更多工程列、点位覆盖、关系。

**出口：** Inspector → 07 设备详情、09 告警、10 诊断、31 数据质量。

**禁止：** 模糊 health score、UUID 主显示、默认卡片墙。

---

### 07 设备详情

**Primary job：** 持续调查单一设备。

**默认运营视图：**

- 身份与位置
- 当前运行/连接
- 关键实时事实
- 关键趋势
- 当前告警
- 当前诊断
- 相关工单

**专业深化：**

- full point list
- point quality/freshness
- sequence/control state
- related assets
- audit trace

**出口：** 05 趋势、09 告警、10 诊断、12 工单详情、25 控制、31 数据质量。

---

### 08 舒适与室内环境（Capability-gated）

**Primary job：** 确认节能优化没有牺牲使用者舒适和 IAQ。

**默认运营视图：**

- zones outside target
- temperature deviation
- humidity
- CO₂ / IAQ（有数据时）
- occupancy context
- violation hours

**专业深化：**

- zone trend
- comfort/IAQ target definition
- HVAC contribution
- complaint correlation（有集成时）

**出口：** 04 系统运行、05 趋势、10 诊断、22 优化方案。

---

## Events / Maintenance / Commissioning

### 09 告警中心

**Primary job：** 处理当前发生的异常并建立处置责任。

**默认运营视图：** Active-first Triage Ledger。

核心字段：

- severity
- physical condition
- handling status
- owner
- duration
- repeat
- source
- suppression

**专业深化：** occurrence history、rule source、correlation evidence。

**动作：** ACK、Assign、Suppress（按真实能力）。

**出口：** 07 设备详情、10 诊断、11/12 工单、05 趋势。

**强制语义：** ACK/Assign/Work Complete ≠ CLEARED。

---

### 10 诊断中心

**Primary job：** 区分事实、Finding、Hypothesis 和 Root Cause。

**默认运营视图：**

- symptom
- verified facts
- evidence window
- published finding
- impact scope
- next verification

**专业深化：**

- supporting/contradicting evidence
- rule/model metadata
- confidence semantics
- investigation timeline
- raw evidence

**出口：** 05 趋势、07 设备、11 工单、13 功能验证、21 节能机会。

**禁止：** Rule Hit = Root Cause；Correlation = Causality。

---

### 11 工单中心

**Primary job：** 让异常变成有人负责、有 SLA、有下一动作的工作。

**默认运营视图：** Ledger，默认优先显示 urgent / overdue / unowned / blocked。

字段：

- work
- source
- priority
- state
- owner
- SLA
- next action
- due
- verification state

**专业深化：** saved views、更多执行列。

**出口：** 12 工单详情。

---

### 12 工单详情

**Primary job：** 完成维护执行并保存证据。

包含：

- problem statement
- source evidence
- owner/team/SLA
- checklist
- timeline
- field notes
- attachments
- execution evidence
- completion evidence
- verification requirement

**出口：** 07/09/10 来源对象，13 功能验证，24 M&V（节能措施时）。

---

### 13 功能验证 / 持续调试

**Primary job：** 确认设备/sequence/策略在修正或发布后真的按预期工作。

**默认运营视图：**

- verification queue
- requirement / sequence
- current status
- latest test result
- failed / inconclusive items
- next action

**专业深化：**

- test conditions
- expected behavior
- observed behavior
- point evidence
- pass/fail criteria
- retest history
- commissioning evidence

**典型对象：** staging、reset、economizer、interlock、strategy rollout。

**出口：** 04 系统运行、05 趋势、12 工单、27 策略详情、28 执行记录、24 M&V。

---

## Energy Performance

### 14 能源分析

**Primary job：** 解释能源用了多少、何时用、用在哪里、为什么变化。

**默认运营视图：**

- period
- energy type
- actual consumption
- demand
- named comparison
- primary load profile
- ranked contributors
- variance windows

**专业深化：**

- grouping dimension
- normalization
- baseline version
- meter lineage
- data quality

**出口：** 15 需求负荷、16 效率、07 设备、21 节能机会、31 数据质量。

---

### 15 需求、负荷与柔性分析

**Primary job：** 理解 peak、load shape 和可移峰能力。

默认能力：

- demand trend
- peak interval
- load duration curve
- peak contributors
- scheduled vs unexpected peak

Capability 扩展：

- demand response
- load shifting
- peak shaving
- tariff response
- flexible load

**出口：** 16 效率、18 账单、20 DER、21 节能机会。

---

### 16 效率分析

**Primary job：** 判断系统是否以合理能效完成当前负荷。

**默认运营视图：**

- system COP
- kW/RT
- ΔT
- tower approach
- subsystem comparison
- equipment outliers

**专业深化：**

- denominator definition
- load vs efficiency scatter
- weather/load context
- transport efficiency
- validity prerequisites

**出口：** 07 设备、05 趋势、10 诊断、21 节能机会。

---

### 17 能源评审

**Primary job：** 管理正式能源评审和绩效方法。

默认业务语言：

- 重大用能
- 能源绩效指标
- 能源基线
- 影响变量

专业详情保留：

- SEU
- EnPI
- EnB
- baseline model
- normalization method
- version/effective period

**出口：** 14/16 分析、21 节能机会、23 目标计划、24 M&V、30 管理评审。

---

### 18 账单、成本与电价（Capability-gated）

**Primary job：** 对账并解释实际能源成本。

包含：

- utility bills
- bill status
- actual vs estimated
- tariff version
- TOU
- demand charge
- interval reconciliation
- missing/duplicate/suspect bills
- cost allocation

**出口：** 15 需求负荷、14 能源、21 节能机会、31 数据质量。

---

### 19 碳排放（Capability-gated）

**Primary job：** 基于可审计边界和排放因子管理排放绩效。

包含：

- source / scope
- factor source/version/date
- location-based / market-based（适用时）
- actual emissions
- intensity
- target
- verified project contribution

---

### 20 分布式能源与柔性（Capability-gated）

**Primary job：** 管理站点的能源供需灵活性。

对象可包括：

- PV
- BESS
- EVSE
- generator
- thermal storage
- grid import/export
- DR event

默认视图必须仍然以运行目标、约束和经济影响为中心，不做纯电气单线图替代全部交互。

---

## Continuous Improvement

### 21 节能机会

**Primary job：** 对有证据的节能机会进行优先级排序。

默认运营视图： Ledger。

核心字段：

- opportunity
- evidence scope
- expected benefit
- risk
- status
- next action

**专业深化：** calculation basis、confidence、constraints、affected objects。

**出口：** 22 优化方案。

**强制语义：** Opportunity ≠ Approved Change；Expected ≠ Verified Savings。

---

### 22 优化方案

**Primary job：** 把机会转成可评审、可审批、可回滚的工程变更。

包含：

- objective
- current state
- proposed change
- affected scope
- preconditions
- guardrails
- simulation / what-if
- expected energy/cost effect
- comfort/reliability impact
- risk
- test plan
- rollback plan
- approval
- execution window

**出口：** 12 工单、27 策略详情、24 M&V。

---

### 23 目标与行动计划

**Primary job：** 把能源目标变成持续追踪的正式改进计划。

包含：

- objective/target
- baseline/EnPI reference
- owner
- due date
- action plans
- opportunities / optimization plans
- progress
- blockers
- verified results

**出口：** 17 能源评审、22 优化、24 M&V、30 管理评审。

---

### 24 节能量验证 M&V

**Primary job：** 回答“实施后到底省了多少，并且能否审计”。

默认运营视图：

- project
- reporting period
- verified savings
- status
- persistence indicator

**专业深化：**

- M&V plan
- measurement boundary
- baseline period
- reporting period
- baseline model
- independent variables
- routine/non-routine adjustments
- excluded periods
- actual
- adjusted baseline
- uncertainty/model quality
- operational verification evidence
- snapback/persistence monitoring

**出口：** 22 优化方案、13 功能验证、28 执行记录、30 管理评审。

---

## Control & Automation

### 25 控制中心

**Primary job：** 理解并执行当前授权范围内的即时控制。

默认运营视图：

- current mode
- active setpoints
- schedule
- overrides
- control authority source
- command availability

**专业深化：** interlock、precondition、guardrail、readback。

**执行流程：** Confirm → Intent → Attempt → ACK → Readback。

**出口：** 07 设备、27 策略、28 执行记录、13 功能验证。

---

### 26 策略中心

**Primary job：** 管理自动化策略组合。

Ledger 包含：

- strategy
- scope
- status
- type
- schedule
- priority
- conflict
- last execution
- version

**出口：** 27 策略详情、28 执行记录。

---

### 27 策略详情 / 仿真 / 审批

**Primary job：** 把控制逻辑变成可理解、可验证、可发布的工程对象。

包含：

- objective
- inputs/outputs
- triggers
- preconditions
- guardrails
- priority/conflict
- fail-safe
- simulation
- historical replay（有可信数据时）
- expected impact
- version diff
- approval
- rollout/rollback

**出口：** 28 执行记录、13 功能验证、24 M&V。

---

### 28 执行记录

**Primary job：** 提供不可含糊的控制事实 Ledger。

必须分别记录：

- requested
- requester
- source
- target
- intended command
- authorization
- attempt
- gateway delivery
- ACK
- readback
- final result
- failure reason
- rollback/override

**禁止：** 合成一个模糊“执行成功”。

---

## Reporting / Management

### 29 报告中心

**Primary job：** 生成和分发可追溯的业务报告。

包括：

- templates
- scope
- period
- completeness
- generated reports
- scheduled delivery
- recipients
- version/time
- drill-back links

---

### 30 管理评审

**Primary job：** 支持管理层周期复盘和资源决策。

包含：

- energy performance
- EnPI / baseline
- target progress
- SEU changes
- verified savings
- major deviations
- operational/data risks
- internal audit findings（如启用正式 EnMS）
- corrective actions
- previous review actions
- resource/investment decisions

**设计原则：** 不是高管 KPI Dashboard，而是决策 Workspace。

---

## Data / Platform Governance

### 31 数据质量

**Primary job：** 告诉用户哪些数据/分析值得信，以及问题影响什么。

默认运营视图：

- coverage
- freshness
- completeness
- suspect/invalid
- synchronization
- source health
- impacted business calculations

**专业深化：**

- point/meter incident
- unit/scale issue
- lineage
- downstream impact graph

例如：

`坏点 → COP → Diagnosis Rule → Opportunity → M&V`

---

### 32 计量与语义模型

**Primary job：** 建立可信的业务对象和计量关系。

包含：

- portfolio/site/building/space
- system hierarchy
- asset/device/point
- meter hierarchy
- energy source
- allocation
- semantic tags
- units/ranges
- calculated point
- virtual meter
- relationships
- version/effective date

---

### 33 规则与通知

**Primary job：** 管理检测规则和通知策略，而不是处置告警。

包含： threshold/condition/hysteresis/delay/recovery/severity/notification/escalation/suppression/version/test/audit。

---

### 34 集成管理

**Primary job：** 管理外部数据/控制连接的生命周期。

包含： connector、health、auth、sync lag、mapping、errors、capability、test connection、audit。

---

### 35 站点与系统配置

**Primary job：** 定义组织、站点、建筑、空间、HVAC 系统、日历、时区、天气源和投产状态。

---

### 36 用户、权限与审计

**Primary job：** 管理授权和高风险业务操作追溯。

必须区分：

- read vs control
- edit vs approve
- site scope
- sensitive command authority
- audit event

---

# 7. 页面默认状态与错误语义

所有主要区块必须区分：

1. Loading
2. Ready with data
3. Ready but empty
4. Partial
5. Stale
6. Suspect
7. Owner unavailable
8. Not integrated
9. Not authorized
10. Request failed

不能统一显示“暂无数据”。

---

# 8. 数据语义不可混淆

必须分别呈现：

- identity
- connectivity
- operating state
- freshness
- quality
- alarm condition
- alarm handling
- diagnostic finding
- root-cause hypothesis
- work state
- functional verification
- control intent
- attempt
- ACK
- readback
- expected savings
- verified savings

`Unknown ≠ 0`，`Stale ≠ Current`，`Forecast ≠ Actual`。

---

# 9. 页面布局类型

## 9.1 Attention Router

企业总览、站点总览。

## 9.2 Ledger First

设备、告警、工单、节能机会、策略、执行记录。

## 9.3 Analytical Workspace

趋势、能源、需求负荷、效率、站点对标、M&V。

## 9.4 Investigation Workspace

诊断、功能验证、数据质量。

## 9.5 Durable Detail

设备详情、工单详情、策略详情、优化方案。

统一页面类型可以提高用户学习效率，同时允许每个领域保持专业深度。

---

# 10. 易用性验收标准

任何页面在视觉设计前必须满足：

- 10 秒内用户知道页面当前最重要的内容。
- Primary Job 能用一句话表达。
- Primary Action 不超过 1–2 个。
- 默认不暴露内部 ID。
- 默认不展示超过实际日常需要的列/过滤器。
- 专业术语有业务语言解释。
- Advanced controls 可渐进展开。
- 页面跳转不丢 Site / Time / Object / Compare Context。
- 返回来源清晰。
- capability 不存在时不显示假入口。

---

# 11. 专业性验收标准

- authoritative source 清晰。
- units / denominator 清晰。
- actual / baseline / forecast 分离。
- evidence 可追溯。
- time boundary 清晰。
- data quality 清晰。
- control intent/ACK/readback 分离。
- completion/verification 分离。
- opportunity/proposal/approved action 分离。
- expected/verified savings 分离。
- comfort/reliability guardrail 可见。

---

# 12. 实施顺序

不按现有代码难度排序，按产品闭环价值排序。

## Phase A — 每日运行闭环

03 站点总览
→ 04 系统运行
→ 05 趋势
→ 06/07 设备
→ 09 告警
→ 10 诊断
→ 11/12 工单
→ 13 功能验证
→ 31 数据质量

## Phase B — 能源绩效

02 站点对标
→ 14 能源
→ 15 需求负荷
→ 16 效率
→ 17 能源评审
→ 21 节能机会

## Phase C — 持续改进

22 优化方案
→ 23 目标与行动计划
→ 24 M&V
→ 29 报告
→ 30 管理评审

## Phase D — 控制自动化

25 控制
→ 26 策略中心
→ 27 策略详情
→ 28 执行记录
→ 13 功能验证

## Phase E — 条件能力

01 企业总览
08 Comfort/IEQ
18 账单成本电价
19 碳
20 DER/Flexibility
32–36 平台治理

---

# 13. 最终结论

v2 不再追求“固定 36 个菜单”，而是定义一个专业完整的 Surface Catalog，并通过角色、能力和权限投影成易用导航。

产品体验的目标不是“让每个人看到所有专业信息”，而是：

> **让每个角色先看到足够做出正确下一步判断的信息，同时保证所有工程结论都能继续下钻到可验证证据。**

这就是本系统对“易用性 × 专业性”的统一定义。
