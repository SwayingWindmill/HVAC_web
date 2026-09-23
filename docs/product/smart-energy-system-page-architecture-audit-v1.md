# 智慧能源系统页面体系外部基准审核 v1

> **状态：COMPLETED / EXTERNAL BENCHMARK AUDIT**  
> **日期：2026-09-14**  
> **审核对象：** `docs/product/smart-energy-system-page-architecture-v1.md`  
> **原则：** 不参考当前项目已有页面、旧菜单、旧路由、旧设计稿或现有组件布局；仅审核产品任务、信息架构、跨页工作流、专业语义与用户易用性。

---

## 1. 审核目标

本轮审核不是确认“页面够不够多”，而是回答四个问题：

1. 页面体系是否覆盖成熟智慧能源 / EMIS / HVAC 运营的核心能力？
2. 页面之间是否形成可执行、可验证的业务闭环，而不是孤立模块？
3. 页面是否足够专业，能够支撑能源经理、HVAC 工程师、运维人员、控制工程师和管理层的真实工作？
4. 页面是否足够易用，让日常运营用户不需要先理解 FDD、EnPI、MBCx、IPMVP 等术语就能完成任务？

最终判断：**v1 的主方向正确，但不能作为最终最佳实践版。需要从“固定 32 页”升级为“完整 Surface Catalog + capability gating”，并补足 Portfolio Benchmarking、Comfort/IEQ、Functional Verification/MBCx、Utility Bill Management 与 DER/Flexibility 等能力。**

---

# 2. 外部资料基线

本轮优先使用政府、行业组织、标准机构和成熟智慧建筑平台的公开资料。

## 2.1 U.S. DOE / FEMP — EMIS 能力

DOE 将 Energy Management Information Systems 的典型能力明确划分为：

- Utility bill management
- Interval meter analytics
- Measurement and verification
- Automated fault detection and diagnostics
- Supervisory control
- Operations and maintenance optimization
- Centralize / normalize / visualize data

来源：
- https://www.energy.gov/cmei/femp/energy-management-information-system-capabilities
- https://www.energy.gov/cmei/femp/what-are-energy-management-information-systems

**对本产品的影响：**

- 能源页面不能只做图表，必须有 Utility Bill / Interval Analytics / M&V。
- AFDD/FDD 是一种分析能力，不应天然成为一级产品域。
- 控制与 O&M 必须进入同一个闭环，而不是分析完成后结束。

## 2.2 DOE — Monitoring-Based Commissioning / Performance Assurance

DOE 明确把持续监测、纠偏和性能保持作为 EMIS 的重要应用。MBCx 通过持续接入 BAS、meter、weather 等数据，支持 ongoing commissioning、发现能源措施并防止性能回退。

来源：
- https://www.energy.gov/cmei/femp/articles/enhancing-performance-contracts-monitoring-based-commissioning
- https://www.energy.gov/cmei/femp/energy-management-information-system-benefits-federal-agencies
- https://www.energy.gov/cmei/femp/performance-assurance-planning-utility-energy-service-contracts

**对本产品的影响：**

运营闭环必须从：

`发现 → 诊断 → 工单完成`

升级为：

`发现 → 诊断 → 修正 → Functional Verification → 持续监测`

因此需要独立的 **功能验证 / 持续调试** Surface。

## 2.3 ASHRAE Guideline 36

ASHRAE Guideline 36-2024 的目的包括：

- 最大化 HVAC 能源效率和性能
- 提供控制稳定性
- 支持实时 fault detection and diagnostics
- 通过 functional tests 确认 sequence of operation 的实现

来源：
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**对本产品的影响：**

专业 HVAC 产品必须同时覆盖：

- 当前运行序列
- Setpoint / Mode / Stage
- FDD / Diagnosis
- Functional Verification
- Control execution / readback

而不能只提供“趋势 + 告警”。

## 2.4 ENERGY STAR Portfolio Manager — Benchmarking

ENERGY STAR 将 benchmarking 作为能源管理的第一步，用于：

- 与历史表现比较
- 与类似建筑比较
- 与目标比较
- 识别低效建筑
- 确定投资优先级
- 验证改进并防止 savings snapback

来源：
- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results

**对本产品的影响：**

Portfolio Benchmarking 不应只是企业首页的一张排名卡，而应该是独立分析 Workspace。

## 2.5 DOE — Metadata / Semantic Modeling

DOE 强调 BAS/AMI metadata 的准确性直接影响分析、可视化和控制；Semantic Model 用结构化关系描述 building system、components、properties、relationships，使 analytics 和 controls 更易部署。

来源：
- https://www.energy.gov/cmei/femp/articles/best-practices-energy-management-information-systems-metadata-schemas
- https://www.energy.gov/cmei/buildings/semantic-modeling-and-interoperability

**对本产品的影响：**

- “数据质量”必须是产品级能力，不是后台运维页。
- “计量与语义模型”必须支撑 analytics lineage、FDD、commissioning、optimization。
- 数据错误应能显示 downstream impact：影响哪个指标、规则、机会、M&V 结果。

## 2.6 Siemens Building X

Siemens 将 Operations Manager、Energy Manager、Sustainability Manager 等能力分开，同时共享统一数据平台。

Energy Manager 提供：

- multi-site / single-site monitoring
- benchmarking
- EnPI
- near-real-time + historical analytics
- anomaly detection
- forecasting
- reporting

Operations Manager 提供：

- real-time equipment monitoring
- historical data
- fault investigation
- remote commands
- comfort status

来源：
- https://www.siemens.com/en-us/products/building-x/applications/operations-manager/
- https://www.siemens.com/en-gb/products/building-x/applications/energy-manager/
- https://resources.sw.siemens.com/en-US/fact-sheet-energy-manager/

**对本产品的影响：**

- Operations 与 Energy 应分开但可互相 drill-down。
- Portfolio → Site → System / Room 的 drilldown 是成熟产品常见模式。
- AI 应嵌入 fault triage / analytics，而不是独立成为整个 IA。

## 2.7 Schneider EcoStruxure

Schneider 将 building operations、energy、asset、workplace/comfort 分成相互关联的能力域。公开能力强调：

- integrated operations
- energy and benchmarking
- predictive / condition-based maintenance
- asset life
- comfort and IAQ

来源：
- https://www.se.com/ww/en/work/software/ecostruxure-building/large-buildings-operations-management/
- https://www.se.com/us/en/work/software/ecostruxure-building/large-buildings-energy-management/
- https://www.se.com/us/en/work/software/ecostruxure-building/large-buildings-workplace-management/
- https://www.se.com/us/en/work/software/ecostruxure-building/large-buildings-asset-management/

**对本产品的影响：**

HVAC 优化不能只有 energy guardrail，必须把 comfort / IAQ / reliability 纳入决策上下文。

## 2.8 Honeywell Forge

Honeywell 的公开能力包括：

- portfolio benchmarking
- utility bill ingestion
- demand charges / consumption patterns / costs
- carbon / sustainability
- power and demand optimization
- occupancy / pricing / weather context

来源：
- https://buildings.honeywell.com/us/en/solutions/buildings/honeywell-forge-sustainability-plus-for-buildings-carbon-and-energy-management
- https://buildings.honeywell.com/us/en/solutions/buildings/honeywell-forge-sustainability-plus-for-buildings-power-and-demand-management-healthcare

**对本产品的影响：**

“成本与电价”过窄，应升级为 **账单、成本与电价**；需求管理未来需要兼容 load flexibility / DR。

---

# 3. v1 审核结果

## 3.1 保留且确认正确的方向

以下方向经过外部资料验证，应继续保留：

### A. Operations 与 Energy 分开

正确。

运行工程师关心：

- 系统现在如何运行
- 哪个设备异常
- 为什么异常
- 是否需要处理

能源经理关心：

- 哪里用能高
- 为什么偏离
- 哪些站点/系统效率差
- 哪个机会值得投资
- 最终省了多少

两个领域共享数据，但不应该被做成同一个“超级 Dashboard”。

### B. Alarm → Diagnosis → Work Order

正确，但闭环不完整。

应升级为：

`Alarm / Anomaly → Diagnosis → Work → Functional Verification → Continue Monitoring`

### C. FDD 作为能力，而不是一级产品域

正确。

用户需要的是“调查问题”，不是理解分析引擎缩写。

专业详情可以显示：

- rule finding
- model finding
- deterministic check
- AI hypothesis

但一级页面名称应使用“诊断中心 / 调查中心”这样的业务语言。

### D. AI 不作为一级业务 IA

正确。

AI 应嵌入：

- Diagnostics
- Trend Analysis
- Work Order Draft
- Opportunity Explanation
- Optimization Proposal
- Report Summary

AI 不能替代数据 owner、审批 owner、控制 owner。

### E. Data Quality 与 Semantic Model 是产品能力

正确，而且重要性应上调。

### F. M&V 独立于 Opportunity / Proposal

正确。

Expected Savings 与 Verified Savings 必须分开。

---

# 4. v1 的主要缺口

## 4.1 缺少 Portfolio Benchmarking

### 问题

把站点排名藏在“企业总览”中，会把一个长期分析任务压缩成首页摘要。

### 修正

增加独立 **Portfolio Benchmarking**。

必须支持：

- portfolio / region / business unit
- peer grouping
- absolute / normalized KPI
- weather normalization（适用时）
- EUI / EnPI
- target / baseline / peer comparison
- top / bottom performers
- trend over time
- drill-down to site

首页只负责提示“哪些站点值得关注”，Benchmarking 负责解释“相对表现如何”。

---

## 4.2 缺少 Comfort / IEQ 约束视角

### 问题

单纯追求 energy / COP 容易形成错误产品激励。

HVAC 优化实际目标应是：

**在满足安全、舒适、IAQ、业务运行和设备可靠性约束的前提下优化能源。**

### 修正

增加 capability-gated **舒适与室内环境** Surface。

必须覆盖：

- thermal comfort
- temperature deviation
- humidity
- CO₂ / IAQ（数据存在时）
- occupancy context
- hours outside target
- affected zones
- system contribution
- complaint / impact context（若接入）

它同时是 Optimization / Control 的 guardrail evidence。

---

## 4.3 缺少 Functional Verification / Continuous Commissioning

### 问题

工单“完成”只能说明任务流程结束，不能说明系统 sequence 已恢复正确。

### 修正

增加独立 **功能验证 / 持续调试** Surface。

主要对象不是 Alarm，也不是 Work Order，而是：

- sequence requirement
- test condition
- test run
- expected behavior
- observed behavior
- evidence
- pass / fail / inconclusive
- corrective action
- retest
- verification history

典型任务：

- chiller staging verification
- chilled water reset verification
- static pressure reset verification
- economizer sequence verification
- pump staging verification
- safety/interlock verification
- strategy rollout verification

---

## 4.4 Utility Bill Management 不完整

### 问题

“成本与电价”不能覆盖成熟 EMIS 的账单管理。

### 修正

改成 **账单、成本与电价**。

至少包括：

- bill period
- bill actual / estimated
- tariff version
- demand charge
- TOU
- interval-to-bill reconciliation
- missing / duplicate / suspect bills
- cost allocation
- tenant/business allocation（适用时）
- cost trend

---

## 4.5 Demand Analysis 需要兼容 Flexibility

### 修正

“需求与负荷分析”升级为：

**需求、负荷与柔性分析**

当前没有 DER/DR 时，只展示 demand / peak / load shape。

未来有能力时扩展：

- peak shaving
- load shifting
- demand response
- flexible load
- grid event
- tariff response

---

## 4.6 DER 不应该硬塞进 HVAC，但必须预留

增加 capability-gated：

**分布式能源与柔性**

只有真实接入以下对象时才显示：

- PV
- BESS
- EVSE
- generator
- grid import/export
- thermal storage
- demand response

---

# 5. 易用性与专业性：必须同时成立

本轮最重要的产品规则不是“更多功能”，而是建立 **Professional Depth without Professional Friction**。

## 5.1 两层专业体验

每个复杂页面默认分为两层：

### Layer A — Operational View

让用户 10–30 秒内回答：

- 发生了什么？
- 哪个最重要？
- 影响什么？
- 我下一步做什么？

只展示：

- 最重要事实
- 优先队列
- 人类可理解状态
- 一个清晰的下一动作

### Layer B — Engineering Detail

通过 Inspector / Detail / Advanced Analysis 展开：

- point-level evidence
- baseline model
- algorithm source
- confidence semantics
- sequence conditions
- measurement boundary
- readback / audit
- engineering units

**专业内容不能删除，但不能把所有专业内容同时压在首屏。**

---

## 5.2 业务语言优先，专业术语保留

推荐：

- `诊断中心`，详情显示 `AFDD Finding`
- `能源绩效指标`，详情显示 `EnPI`
- `能源基线`，详情显示 `EnB`
- `功能验证`，详情说明 `Functional Verification / MBCx`
- `节能量验证`，详情说明 `M&V / IPMVP Method`

不推荐 Sidebar 直接出现：

- FDD
- EnPI
- EnB
- MBCx
- IPMVP Option C

这些术语属于专业详情，不应成为导航理解门槛。

---

## 5.3 首页不应该要求用户“理解仪表盘”

首页的任务不是展示所有能力，而是：

1. 告诉用户最重要的变化。
2. 给出为什么值得关注的简短证据。
3. 引导到负责处理的 Workspace。

因此企业总览 / 站点总览必须是 **attention router**，而不是 KPI wall。

---

## 5.4 专业用户需要密度，不需要噪声

Ledger 型页面应该：

- 高密度
- 固定列语义
- 支持 saved views
- 支持 keyboard / command search
- 支持列筛选与排序
- 默认排序按业务优先级，而不是创建时间

但不能：

- 默认显示内部 UUID
- 默认展示 20+ 列
- 每行堆 5 个彩色 Badge

---

## 5.5 Progressive Disclosure

复杂信息采用统一层级：

`Summary → Evidence → Engineering Detail → Raw Evidence`

例如 Diagnostic：

- Summary：冷冻水温差异常
- Evidence：同负荷基线偏离、关联设备、时间窗口
- Engineering Detail：rule/model finding、supporting/contradicting evidence
- Raw Evidence：point history / rule revision / calculation trace

普通用户可以停在前两层，工程师可以继续下钻。

---

## 5.6 一页只负责一个 durable job

页面不应该同时承担：

- 监控
- 配置
- 审批
- 报表
- 批量维护

如果一个用户任务不能在一句话中说清楚，它通常不应该是一个单独 Surface。

---

## 5.7 Default View 必须面向 80% 日常任务

例如：

- Alarm 默认 Active，而不是历史统计。
- Work Order 默认显示 urgent / overdue / unowned。
- Energy 默认进入最近适用周期的 Actual + named comparison。
- Asset 默认 Table/Ledger，不默认图形卡片。
- Control 默认 Current Authority + Setpoints + Overrides，不默认策略编辑器。

---

## 5.8 Advanced Mode 不等于另一套产品

不要为“专业工程师”做第二套完全不同 IA。

应该在同一页面逐步提供：

- More columns
- Engineering chart controls
- raw point evidence
- calculation lineage
- model metadata
- audit trace

这样运营团队和工程团队可以讨论同一个对象，而不是使用两套彼此断裂的页面。

---

# 6. 页面目录应从固定页数升级为 Surface Catalog

成熟产品不应该承诺“所有客户永远看到 36 页”。

正确模型：

- **Surface Catalog**：定义完整业务能力。
- **Navigation Projection**：根据用户角色、站点能力、数据接入、权限投影导航。

例如：

- 没有 tariff → 不显示“账单、成本与电价”。
- 没有 IAQ sensor → “舒适与室内环境”只显示 thermal comfort，或整个 capability 不启用。
- 没有 command capability → 不显示 Control / Strategy actions。
- 没有 DER → 不显示“分布式能源与柔性”。

不应在 Sidebar 长期显示一堆 disabled 菜单。

---

# 7. 审核后的 Surface Catalog

建议定义 **36 个完整 Surface**，但不代表每个客户都看到 36 个菜单。

## Portfolio / Management

1. 企业总览
2. Portfolio Benchmarking
3. 站点总览

## Operations

4. 系统运行
5. 趋势分析
6. 设备中心
7. 设备详情
8. 舒适与室内环境（capability-gated）

## Events / Maintenance / Commissioning

9. 告警中心
10. 诊断中心
11. 工单中心
12. 工单详情
13. 功能验证 / 持续调试

## Energy Performance

14. 能源分析
15. 需求、负荷与柔性分析
16. 效率分析
17. Energy Review / SEU / EnPI / Baseline
18. 账单、成本与电价（capability-gated）
19. 碳排放（capability-gated）
20. 分布式能源与柔性（capability-gated）

## Continuous Improvement

21. 节能机会
22. 优化方案
23. 目标与行动计划
24. 节能量验证 M&V

## Control & Automation

25. 控制中心
26. 策略中心
27. 策略详情 / 仿真 / 审批
28. 执行记录

## Reporting / Review

29. 报告中心
30. 管理评审

## Data / Platform Governance

31. 数据质量
32. 计量与语义模型
33. 规则与通知
34. 集成管理
35. 站点与系统配置
36. 用户、权限与审计

---

# 8. 三个最终闭环

## 8.1 Operations Loop

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

## 8.2 Energy Improvement Loop

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

## 8.3 Control Loop

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

# 9. 导航易用性建议

36 个 Surface 绝不能平铺成 36 个 Sidebar 菜单。

默认 Sidebar 建议控制在 **6 个业务组 + 1 个系统组**：

```text
总览
  企业 / 站点（按角色选择默认入口）

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
  Energy Review
  节能机会

优化与控制
  优化方案
  控制中心
  策略中心

管理
  Benchmarking
  目标与行动计划
  M&V
  报告
  管理评审

系统
  数据质量
  配置
```

其他页面通过上下文进入，或按 capability 展开。

### 关键易用性规则

- 不把“设备详情”“工单详情”“策略详情”放 Sidebar。
- 账单/碳/DER 只有 capability 存在才出现。
- 管理员配置与日常运营菜单视觉上分区。
- 首页和 Sidebar 不重复罗列所有能力。
- Global Search / Command Palette 支持直接跳设备、告警、工单、策略、站点。

---

# 10. 页面设计统一审核清单

任何 Surface 进入 wireframe 前必须同时通过两组问题。

## 10.1 专业性

- authoritative fact 是什么？
- metric denominator 是否明确？
- baseline / forecast / actual 是否分开？
- evidence 是否可追溯？
- time boundary 是否明确？
- control safety 是否完整？
- completion 与 verification 是否分开？
- expected saving 与 verified saving 是否分开？

## 10.2 易用性

- 用户进入后 10 秒能否知道最重要的事？
- 页面是否有一个明确 primary job？
- 是否知道下一步动作？
- 专业术语是否提供业务语言解释？
- 是否避免无意义大卡片和内部 ID？
- 是否默认隐藏不常用高级选项？
- 跨页是否保留 Site / Time / Object / Compare Context？
- 是否区分 loading / empty / unavailable / stale / unauthorized？

如果专业性通过但易用性失败，不进入 UI 实现；反之亦然。

---

# 11. 结论

v1 的基础结构是可用的，但经过 DOE、ENERGY STAR、ASHRAE，以及 Siemens / Schneider / Honeywell 等成熟产品公开资料的反向验证后，必须做以下升级：

1. 固定 32 页 → Surface Catalog + capability gating。
2. 新增 Portfolio Benchmarking。
3. 新增 Comfort / IEQ。
4. 新增 Functional Verification / MBCx。
5. Cost 页面升级为 Utility Bill + Cost + Tariff。
6. Demand 页面兼容 Flexibility / DR。
7. M&V 加 Operational Verification / persistence / snapback 语义。
8. Data Quality 增加 downstream lineage / impact。
9. 管理评审增加 corrective actions / previous actions / governance。
10. 所有专业页面采用 progressive disclosure，保证运营人员易用、工程师可深挖。

**审核结论：v1 不再应作为最终页面蓝图。应生成 v2，并以本审核结论作为 v2 的设计依据。**
