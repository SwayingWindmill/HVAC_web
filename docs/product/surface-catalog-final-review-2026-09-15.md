# 智慧能源 Surface Catalog 01–36 一次性终审

> **状态：SUPERSEDED / HISTORICAL 36-SURFACE REVIEW**  
> **Superseded by:** `smart-energy-system-page-architecture-v3-research-backed.md` + `global-navigation-context-interaction-contract-v2.md`  
> **日期：2026-09-15**  
> **范围：** `01–36 Surface Catalog`、全局导航、Context、Route Intent、跨页责任边界、用户可理解性、无障碍、治理检查。  
> **终审目标：** 不再继续新增页面；确认现有 Surface 是否完整、不重复、可理解、可导航、可实现，并确保“专业性”不会以牺牲用户易用性为代价。

---

# 1. 最终结论

本轮终审后，正式结论是：

> **01–36 Surface Catalog 已形成完整、职责清晰、可通过角色/能力投影为简洁产品导航的页面体系，可以整体进入 Wireframe 阶段。**

但“READY FOR WIREFRAME”不是指 36 个页面都要平铺在 Sidebar，也不是指每个页面都必须展示全部专业字段。

最终产品结构应理解为：

```text
36 个 Surface 能力目录
+
角色 / Capability / Permission 导航投影
+
稳定 Global Shell
+
共享 Site / Object / Time / Source Context
+
Operational View
→ Professional Detail
→ Raw Evidence / Audit
```

用户看到的是**当前任务所需要的简洁入口和业务语言**；专业深度通过下钻逐层展开。

终审后不建议继续设计第 37、38 个页面。下一阶段应进入：

```text
全局 Wireframe
→ Navigation / Shell Wireframe
→ 关键工作流 Wireframe
→ Surface Wireframe
→ 视觉系统应用
→ Browser / WSL 用户体验验收
```

---

# 2. 终审依据

## 2.1 项目内部权威

按以下顺序裁决：

1. 用户最新明确要求；
2. `PRODUCT.md`；
3. `docs/product/smart-energy-system-page-architecture-v2.md`；
4. `docs/product/global-navigation-context-interaction-contract-v1.md`；
5. 对应 `docs/product/surface-specifications/01–36`；
6. `DESIGN.md`；
7. shadcn/ui / shadcn-admin 稳定模式；
8. 当前实现只作为数据 owner / API / capability 证据，不作为页面设计权威。

## 2.2 外部最佳实践

本次终审重点使用：

- ISA-101 HMI：信息层级、导航、颜色、动态元素、告警、人机交互、减少 operator error、提升 situational awareness；
  - https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards
- WCAG 2.2：keyboard、focus visible / not obscured、headings / labels、consistent navigation、status messages；
  - https://www.w3.org/TR/WCAG22/
- WAI-ARIA / W3C APG：Breadcrumb、Dialog、Table/Grid、Combobox 等语义；
  - https://www.w3.org/WAI/ARIA/apg/
- ENERGY STAR Portfolio Manager：benchmarking、peer comparison、normalization、screening 与 root-cause 边界；
  - https://www.energystar.gov/buildings/benchmark
  - https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results
- DOE/FEMP EMIS、M&V、Commissioning、Metering、50001 Ready；
- ASHRAE Guideline 36 / Standard 223；
- ISA-18.2 / IEC 62682；
- NIST OT / RBAC / Zero Trust；
- BACnet、OPC UA、MQTT 等协议语义。

这些资料共同支持一个核心方向：

> **高专业度操作产品的易用性来自稳定层级、正确语义、明确状态、渐进披露和可解释下一步，而不是把所有工程细节堆在首屏。**

---

# 3. 终审发现并已修复的问题

## 3.1 Critical — Surface Catalog 有 36 个，但正式详细规格原先只有 03–36

终审前：

```text
01 企业总览
02 Portfolio Benchmarking
```

只存在于架构摘要，没有和 03–36 同等级的 Surface Specification。

这意味着“完整 36 Surface 都 READY FOR WIREFRAME”在治理上并不成立。

**已修复：**

新增：

```text
docs/product/surface-specifications/01-enterprise-overview.md
docs/product/surface-specifications/02-portfolio-benchmarking.md
```

01 已定义为：

```text
Portfolio Attention Router
```

02 已定义为：

```text
可解释的站点对标 / Portfolio Comparison Workspace
```

并明确：

```text
Benchmark Signal ≠ Root Cause
Ranking ≠ Investment Approval
Expected Savings ≠ Verified Savings
Comparable ≠ Identical
```

## 3.2 High — 用户可见主导航仍残留不必要英文

终审前有：

```text
Energy Review
Benchmarking
```

这与既定“中文优先，缩写辅助”规则冲突。

**已修复为：**

```text
能源评审
站点对标
```

`SEU / EnPI / EnB / M&V / IAQ / COP` 等真正有行业价值的标准缩写继续保留。

## 3.3 High — Route Intent 跨文档不一致

终审发现：

```text
设备中心：/assets vs /devices
功能验证：/verification vs /verifications
报告：全局 /reports vs site-scoped reports
管理评审：/management-review vs site-scoped reviews
Semantic Model：/settings/semantic-model vs /sites/:siteId/model
Control Center 与 Execution Detail 重复拥有 execution route
Strategy Center 与 Strategy Detail 重复拥有 detail route
```

**已修复：**

最终 Route Intent 统一由 `global-navigation-context-interaction-contract-v1.md` 汇总，并和各 Surface 保持一致。

核心决策：

```text
/sites/:siteId/devices
/sites/:siteId/devices/:deviceId

/sites/:siteId/verifications
/sites/:siteId/verifications/:verificationId

/sites/:siteId/control
/sites/:siteId/strategies
/sites/:siteId/strategies/:strategyId
/sites/:siteId/strategies/:strategyId/versions/:version
/sites/:siteId/executions
/sites/:siteId/executions/:executionId

/sites/:siteId/reports
/sites/:siteId/management-reviews
/sites/:siteId/data-quality
/sites/:siteId/model
```

Control Center 不再拥有 Execution Detail route；Strategy Center 不再和 Strategy Detail 争夺 detail ownership。

## 3.4 High — “用户友好”原本有原则，但还不够成为全站 Content Design Gate

终审前已经有：

- 中文优先；
- 10 秒理解；
- Progressive Disclosure；
- Loading / Partial / Stale / Error 区分；
- 不显示内部 ID。

但还缺少一组明确的“页面内容怎么写才算用户友好”的统一规则。

**已修复：**

`DESIGN.md` 新增：

```text
6.5 用户可理解性 / Content Design
```

`global-navigation-context-interaction-contract-v1.md` 的用户易用性章节扩展为正式 Content Design Contract。

---

# 4. 最终 01–36 Surface Catalog

## Portfolio / Management

```text
01 企业总览
02 站点对标
03 站点总览
```

## Operations

```text
04 系统运行
05 趋势分析
06 设备中心
07 设备详情
08 舒适与室内环境
```

## Events / Maintenance / Commissioning

```text
09 告警中心
10 诊断中心
11 工单中心
12 工单详情
13 功能验证 / 持续调试
```

## Energy Performance

```text
14 能源分析
15 需求、负荷与柔性分析
16 效率分析
17 能源评审
18 账单、成本与电价
19 碳排放
20 分布式能源与柔性
```

## Continuous Improvement

```text
21 节能机会
22 优化方案
23 目标与行动计划
24 节能量验证（M&V）
```

## Control & Automation

```text
25 控制中心
26 策略中心
27 策略详情 / 仿真 / 审批
28 执行记录
```

## Reporting / Management

```text
29 报告中心
30 管理评审
```

## Data / Platform Governance

```text
31 数据质量
32 计量与语义模型
33 规则与通知
34 集成管理
35 站点与系统配置
36 用户、权限与审计
```

全部 36 个 Surface 都必须有独立 Primary Job；没有一个 Surface 的存在理由只是“后端有这个服务”。

---

# 5. 重复 Surface / 职责冲突终审

终审结论：**保留 36 个能力 Surface，但很多不是 Sidebar 一级页面；不存在必须合并的重复 Surface。**

以下容易被误认为重复的页面，正式边界如下。

## 5.1 01 企业总览 vs 03 站点总览 vs 30 管理评审

```text
01 企业总览
= 多站点 Attention / Portfolio Performance

03 站点总览
= 单站点“现在最值得处理什么”

30 管理评审
= 周期性正式 Management Decision Record
```

禁止把三者做成三个风格相同的 KPI Dashboard。

## 5.2 01 企业总览 vs 02 站点对标

```text
01
= 哪些站点值得关注

02
= 在什么可比口径下，哪些站点相对好/差
```

01 不承载完整 ranking；02 不承载企业级全业务风险汇总。

## 5.3 04 系统运行 vs 25 控制中心

```text
04
= 看系统怎样运行

25
= 在明确 Authority / Precondition / Interlock 下执行即时控制
```

运行页可以展示 Control Authority / Current Setpoint，但默认不提供高风险写操作。

## 5.4 05 趋势 vs 14 能源分析

```text
05
= 通用时序证据调查

14
= Energy Performance Analysis
```

05 不承担 authoritative energy aggregation；14 不变成任意点位趋势工具。

## 5.5 06/07 设备 vs 32 计量与语义模型

```text
06/07
= 运营人员扫描 / 调查设备

32
= 管理 canonical identity / relation / meter / point / model
```

Registry / Semantic CRUD 不能重新污染 Device Center。

## 5.6 09 告警 vs 33 规则与通知

```text
09
= 处理当前 Alarm Occurrence

33
= 管 Rule Definition / Evaluation / Notification Policy
```

Alarm ACK / Assign 与 Rule 修改彻底分开。

## 5.7 10 诊断 vs 31 数据质量

```text
10
= Finding / Hypothesis / Root Cause Investigation

31
= Data Quality Issue / Business Impact / Correction / Recomputation
```

Bad Data ≠ Equipment Fault。

## 5.8 13 功能验证 vs 24 M&V

```text
13
= 系统/sequence 是否按预期工作

24
= 实施以后到底节省多少
```

```text
Functional Verification PASS
≠ Verified Savings
```

## 5.9 14 / 15 / 16

```text
14 Energy
= 用了多少 / 何时 / 用在哪里 / 为什么变化

15 Demand
= Peak / Load Shape / Flexibility

16 Efficiency
= 在当前负荷和工况下完成输出的效率
```

```text
High Energy ≠ Low Efficiency
Energy ≠ Demand
```

因此不建议合并成“能源大屏”。

## 5.10 21 / 22 / 23 / 24

```text
21 Opportunity
→ 22 Engineering Plan
→ 23 Objective / Action Governance
→ 24 Verified Result
```

不同生命周期必须保留，否则会重新出现：

```text
Expected ≠ Approved ≠ Executed ≠ Verified
```

被一个状态字段压平的问题。

## 5.11 25 / 26 / 27 / 28

```text
25 即时控制
26 策略 Portfolio / Runtime
27 Strategy Engineering / Version / Simulation / Approval
28 Execution Truth Ledger
```

不建议为了“页面少”合并。它们是高风险 OT 领域必要的责任拆分。

## 5.12 29 报告 vs 30 管理评审

```text
29
= 生成 / 审批 / 发布 / 分发固定证据报告

30
= 基于冻结证据做 Management Decision
```

```text
Review Pack ≠ Management Review
```

## 5.13 31 / 32 / 34

```text
31 Data Quality
= 哪里坏 / 影响什么 / 修复与重算

32 Semantic / Metering Model
= 对象和关系真相

34 Integration
= 外部连接、认证、同步、Mapping Lifecycle
```

三者不能重新合成万能“系统管理”。

## 5.14 35 vs 36

```text
35
= Site / Calendar / Timezone / Weather / Commissioning / Capability Config

36
= Principal / Role / Permission / Scope / Audit
```

站点配置不能变成用户权限管理页。

---

# 6. 最终 Sidebar 裁决

36 Surface **不是 36 个菜单项**。

最终默认导航分组保持：

```text
总览
  站点总览
  企业总览                    [portfolio]

运行
  系统运行
  设备
  趋势
  舒适与室内环境              [capability]

事件与工作
  告警
  诊断
  工单
  功能验证

能源与绩效
  能源
  需求与负荷
  效率
  能源评审                    [role]
  账单与成本                  [capability]
  碳排放                      [capability]
  分布式能源                  [capability]

优化与控制
  节能机会
  优化方案
  控制中心                    [capability + permission]
  策略中心                    [capability + permission]

管理
  站点对标                    [portfolio]
  目标与行动计划
  节能量验证
  报告
  管理评审                    [role]

系统
  数据质量
  配置
```

`配置` 下再进入：

```text
计量与语义模型
规则与通知
集成管理
站点与系统配置
用户、权限与审计
```

Detail Surface 不进入一级 Sidebar：

```text
07 设备详情
12 工单详情
27 策略详情
28 单条执行详情
以及其他 durable detail routes
```

最终原则：

```text
Surface Visibility
= Product Capability
AND Site / Deployment Capability
AND Principal Discoverability
```

Action Availability 再独立判断权限、对象能力和安全条件。

---

# 7. 用户友好内容终审

这次终审不只检查“页面有哪些模块”，还把**内容如何被用户理解**升级为正式设计门禁。

## 7.1 每页进入 10 秒内必须回答

```text
我现在看的是谁 / 什么范围？
这里最重要的事实是什么？
为什么值得注意？
这个结论值得信吗？
下一步应该做什么？
```

如果用户需要先理解系统内部架构才能回答，页面不合格。

## 7.2 首屏优先级

首屏顺序默认：

```text
Context
↓
需要关注 / Primary Fact
↓
Primary Workspace
↓
下一步
↓
Supporting Evidence
```

避免：

```text
8–12 个 KPI Cards
↓
更多 KPI
↓
真正的任务在第二屏以后
```

## 7.3 文案必须使用业务语言

推荐：

```text
需要关注
当前运行
数据可信度
已验证节能量
待处理工单
为什么无法控制
下一步
```

避免主界面默认暴露：

```text
Projection
Read Model
Payload
resourceId
traceId
schema
Revision Hash
raw enum
BACnet object address
```

## 7.4 异常必须给解释

不合格：

```text
不可用
Failed
Disabled
```

合格：

```text
当前无法远程控制
原因：设备处于 Local 模式
下一步：确认现场模式后重试
```

或者：

```text
能源基线服务暂不可用
实际用电仍可查看；相对基线偏差暂不计算。
```

## 7.5 空状态必须区分无事发生与不可见

```text
当前没有活动告警
```

与：

```text
告警服务暂不可用，当前状态未知
```

必须是不同 UI。

全平台继续坚持：

```text
Unknown ≠ 0
Missing ≠ 0
Not Integrated ≠ 0
Not Authorized ≠ Empty
```

## 7.6 动作必须具体

推荐：

```text
查看设备趋势
创建工单
提交优化方案审批
释放临时覆盖
确认将 CHWS 设定值调整到 7.2°C
```

避免：

```text
详情
处理
操作
确定
执行
```

## 7.7 专业深度用渐进披露，不用首屏堆叠

全站统一三层：

```text
Operational View
→ Professional Detail
→ Raw Evidence / Audit
```

普通用户先看到正确业务事实；专业人员仍然可以继续追溯：

```text
Method
Version
Boundary
Lineage
Raw Point
Audit
```

## 7.8 表格默认列数量受任务约束

默认 Ledger 的目标不是“字段越多越专业”，而是完成 scan / compare / prioritize。

目标桌面宽度下：

> **核心判断不应依赖横向滚动。**

低频字段进入：

```text
Column Settings
Inspector
Durable Detail
Advanced
```

## 7.9 实时更新必须尊重用户注意力

禁止实时数据：

- 抢 focus；
- 关闭用户正在使用的 Sheet / Popover / Dialog；
- 重置分页 / 筛选；
- 每个 sample 都重排行；
- 把用户正在查看的证据自动滚走；
- 用跳动 / 闪烁表达“实时”。

状态变化应原位、稳定、可被辅助技术感知。

## 7.10 状态不只靠颜色

Alarm、Quality、Risk、Verification、Execution 等：

```text
文字
+ 图标 / 形状 / 标签
+ 必要时颜色
```

颜色只是冗余编码。

## 7.11 中文优先

正式用户界面：

```text
能源绩效指标（EnPI）
能源基线（EnB）
重大用能（SEU）
节能量验证（M&V）
室内空气质量（IAQ）
```

不再出现“页面专业 = 满屏英文”。

## 7.12 User-friendly ≠ Semantic Simplification

易用性不能通过合并专业事实实现。

以下语义仍必须始终独立：

```text
Offline ≠ Fault
Stale ≠ Offline
Bad Quality ≠ Alarm
ACK ≠ Cleared
Opportunity ≠ Approved Change
Expected ≠ Verified
Approved ≠ Executed
ACK ≠ Readback
Delivered ≠ Read
Action Complete ≠ Performance Improved
```

用户友好的目标是：

> **更快理解正确事实，而不是更快理解一个错误的简化事实。**

---

# 8. Accessibility 终审

Wireframe 和实现统一以 **WCAG 2.2 AA 方向**作为 baseline。

至少验收：

- 核心任务 keyboard 可完成；
- Focus visible；
- sticky Header / Footer 不完全遮挡 focus；
- 状态更新可由 screen reader 感知，不需要抢 focus；
- headings / labels 描述真实目的；
- 相同能力跨页名称一致；
- navigation 相对位置稳定；
- 图表存在文本/表格等价信息；
- hover 不是获取关键证据的唯一方式；
- Dialog focus lifecycle 正确；
- 状态不依赖颜色；
- topology / graph 提供 list / facts alternative。

---

# 9. 页面类型终审

为了让用户学习成本可控，36 Surface 不应产生 36 种布局。

最终收敛为少数稳定模式。

## Attention Router

```text
01 企业总览
03 站点总览
```

## Ledger First

```text
06 设备中心
09 告警
11 工单
21 节能机会
26 策略中心
28 执行记录
29 报告中心
```

## Analytical Workspace

```text
02 站点对标
05 趋势
14 能源
15 需求负荷
16 效率
24 M&V
```

## Investigation Workspace

```text
10 诊断
13 功能验证
31 数据质量
```

## Engineering / Control Workspace

```text
04 系统运行
20 DER
25 控制中心
27 策略详情
32 语义模型
33 规则与通知
34 集成管理
```

## Durable Detail / Governance Workspace

```text
07 设备详情
12 工单详情
17 能源评审
18 账单
19 碳
22 优化方案
23 目标与行动计划
30 管理评审
35 站点配置
36 权限与审计
```

这些模式共享 Page Intro、Context、State、Inspector、Detail、Dialog 规则，使产品保持一致而不是模板化。

---

# 10. 关键跨页闭环终审

## 10.1 每日运行闭环

```text
03 站点总览
→ 04 系统运行
→ 05 趋势 / 06–07 设备
→ 09 告警
→ 10 诊断
→ 11–12 工单
→ 13 功能验证
→ 03 / 04 返回运行
```

通过。

## 10.2 数据可信度闭环

```text
31 数据质量
→ 32 计量与语义模型 / 34 集成管理
→ Source / Mapping Correction
→ Downstream Impact
→ Recomputation
→ New Result Revision
```

通过。

## 10.3 能源改善闭环

```text
14 / 15 / 16 / 17
→ 21 节能机会
→ 22 优化方案
→ 23 行动计划
→ 实施
→ 13 功能验证
→ 24 M&V
→ 29 报告
→ 30 管理评审
```

通过。

## 10.4 自动控制闭环

```text
21 Opportunity
→ 22 Plan
→ 27 Strategy Engineering
→ 26 Deployment / Runtime
→ 28 Execution
→ 13 Functional Verification
→ 24 M&V（如涉及节能）
```

通过。

## 10.5 Portfolio 闭环

```text
01 企业总览
→ 02 站点对标
→ 03 站点总览
→ 14 / 16 / 17 / 21
→ 24 Verified Result
→ 01 / 30
```

补齐 01/02 后通过。

---

# 11. 一事实一 Owner 终审

以下事实保持单一 owner：

| 事实 | Owner Surface / Domain |
|---|---|
| 当前系统运行 | 04 |
| 通用历史趋势 | 05 / historian owner |
| 设备身份与运营调查 | 06/07 |
| Alarm Occurrence | 09 |
| Finding / Diagnosis | 10 |
| Work lifecycle | 11/12 |
| Functional Verification | 13 |
| Energy Performance | 14 |
| Demand / Peak / Flexibility analytics | 15 |
| Efficiency metric | 16 |
| EnPI / EnB / SEU governance | 17 |
| Bill / Tariff / Cost | 18 |
| Carbon inventory | 19 |
| DER resource state | 20 |
| Opportunity | 21 |
| Engineering Change Plan | 22 |
| Objective / Action Plan | 23 |
| Verified Savings | 24 |
| Immediate Control Intent | 25 |
| Strategy Portfolio / Runtime | 26 |
| Strategy Definition / Version | 27 |
| Execution Truth | 28 |
| Fixed Report Revision | 29 |
| Management Decision | 30 |
| Data Quality Issue | 31 |
| Canonical Identity / Relationship / Meter Model | 32 |
| Detection Rule / Notification Policy | 33 |
| Connector / Integration Lifecycle | 34 |
| Site Master Configuration | 35 |
| Principal / Authorization / Audit | 36 |

终审没有发现必须通过“兼容层”解决的产品事实重复。

---

# 12. 终审后明确禁止的全局退化

后续 Wireframe / 实现不允许出现：

```text
36 Surface → 36 个 Sidebar 菜单

所有页面
→ KPI Card Wall

所有列表
→ 通用 CRUD Table

所有详情
→ 巨型 Drawer

所有专业状态
→ Health Score

所有空状态
→ 暂无数据

所有异常
→ 红色 Badge + 无解释

所有 Button
→ 详情 / 操作 / 确定

所有实时更新
→ 自动重排 / 抢 focus

Capability Missing
→ 空白占位页

Permission Denied
→ 数据不存在

旧页面
→ Compatibility Adapter

旧 Ant / ProComponents
→ 目标 UI
```

---

# 13. Wireframe 阶段统一验收清单

每一个 Surface Wireframe 必须在视觉设计之前通过：

## User Job

- Primary Job 是否一句话说清？
- 首屏是否服务 Primary Job？
- 是否有明确“下一步”？

## Context

- 用户是否一直知道 Organization / Site / System / Object / Period？
- 跨页后是否保留来源和 compatible context？

## Content

- 主界面中文是否自然？
- 是否默认隐藏内部字段？
- 专业术语是否解释？
- 异常是否有原因和下一步？
- 空状态是否区分 no issue / no data / unavailable / not integrated / unauthorized？

## Density

- Summary Cards 是否控制在真正必要范围？
- Ledger 核心判断是否无需横向滚动？
- Advanced 字段是否真正渐进展开？

## Trust

- 时间范围、单位、口径、数据性质是否清楚？
- Unknown / Missing / Estimated / Stale 是否真实表达？
- 用户能否进入 Evidence / Method / Lineage？

## Action Safety

- Action 是否具体？
- High-risk action 是否显示 Current / Proposed / Impact / Preconditions？
- ACK / Readback / Verification 是否分开？

## Accessibility

- keyboard 是否能完成核心任务？
- focus 是否可见？
- state 是否不只靠颜色？
- chart / topology 是否有替代内容？
- status update 是否不抢 focus？

未通过以上任一核心项，不进入视觉精修。

---

# 14. 实施阶段建议顺序

终审后建议不再以“页面编号顺序”机械实现，而是按用户闭环价值：

## Wave 1 — Global Shell + 每日运行

```text
Global Shell
03
04
05
06/07
09
10
11/12
13
31
```

## Wave 2 — 能源绩效

```text
01/02
14
15
16
17
21
```

## Wave 3 — 持续改进

```text
22
23
24
29
30
```

## Wave 4 — 控制自动化

```text
25
26
27
28
```

## Wave 5 — Capability / Governance

```text
08
18
19
20
32
33
34
35
36
```

Capability-gated Surface 不应为了“页面完整”先做假数据版本。

---

# 15. Final Decision

经过这次一次性终审，正式裁决：

```text
Surface Catalog Completeness
PASS — 01–36 均有正式 Surface Specification

Responsibility Ownership
PASS — 无必须合并的重复 Surface

Navigation Projection
PASS — 36 Surface 不平铺菜单

Route Ownership
PASS — 已统一主要 durable route intent

Cross-page Workflows
PASS — 运行 / 数据 / 能源改善 / 控制 / Portfolio 闭环完整

Semantic Integrity
PASS — 一事实一 owner，关键状态保持独立

Chinese Product Language
PASS — 主界面中文优先，标准缩写辅助

User-friendly Content Contract
PASS — 已升级为全局治理规则

Accessibility Baseline
PASS — WCAG 2.2 AA 方向纳入 Wireframe / Implementation Gate

No Defensive Programming
PASS — 各 Surface 保持 Unknown / Missing / Owner failure 的真实语义

Compatibility Design
REJECTED — 旧 UI / 旧路由 / 旧 Ant 页面不作为目标权威
```

**最终状态：01–36 `READY FOR SYSTEM-WIDE WIREFRAME`。**

下一阶段不再重新发明页面，而是依据这些契约完成整套 Wireframe，并用真实浏览器在目标桌面视口和窄屏场景做用户任务验收。
