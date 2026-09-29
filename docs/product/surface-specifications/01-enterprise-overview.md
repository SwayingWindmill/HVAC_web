# 01 企业总览 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-15**  
> **Surface Catalog：** `01 企业总览`  
> **Route intent：** `/portfolio/overview`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v3-research-backed.md` → `global-navigation-context-interaction-contract-v2.md` → 本文件 → `DESIGN.md`  
> **产品语言：** 中文优先；EUI、EnPI、M&V 等标准缩写作为专业辅助。  
> **设计输入声明：** 本文件不参考当前项目已有 Dashboard、旧大屏、旧 Ant/ProComponents 页面或历史参考图。现有代码只能在实施阶段作为真实 portfolio read model / capability / permission 的候选证据来源。

---

# 1. Primary Job

企业总览的唯一核心任务是：

> **让企业能源负责人或管理者在 30–60 秒内判断“整个站点组合里，哪里最需要关注、哪里已经产生经过验证的改善、下一步应该进入哪个站点或管理工作流”。**

它是 **portfolio attention router**，不是：

- 36 个模块的入口墙；
- 所有站点 KPI 的拼盘；
- 实时 BMS；
- 站点运行控制台；
- Benchmarking 详情页；
- 管理评审会议记录；
- 通过一个黑盒“企业健康分”总结所有风险。

用户离开本页时应知道：

1. 哪些站点最值得优先处理；
2. 为什么值得处理；
3. 当前 portfolio 能源 / 成本 / 碳 / 目标 / Verified Savings 的核心事实是什么；
4. 哪些结论的数据完整性不足；
5. 下一步应该进入 02 站点对标、03 站点总览、24 M&V、23 目标与行动计划或 30 管理评审中的哪一个。

---

# 2. 主要用户

## Primary

- 企业能源负责人；
- 多站点设施 / 运营负责人；
- 需要周期性查看能源绩效的管理者。

## Secondary

- 能源分析工程师：从 portfolio 发现异常站点后进入 02 / 14 / 16；
- 可持续发展负责人：在碳能力存在时消费 portfolio carbon summary；
- 财务 / 采购负责人：在 billing capability 存在时消费成本事实。

## 不作为主要目标用户

- HVAC 值班工程师；
- 告警值班员；
- 维修技师；
- 控制工程师。

这些角色不应被迫经过企业总览完成日常任务。

---

# 3. 外部最佳实践依据

## 3.1 ENERGY STAR Portfolio Manager — Benchmark 用于发现优先对象，不用于解释根因

ENERGY STAR 将 benchmarking 定义为把建筑能源绩效与类似建筑、历史表现或参考水平进行比较，并用于识别低效建筑、确定投资优先级和持续追踪改善。

来源：

- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results

本页采用：

- 企业总览只展示足够做 portfolio prioritization 的事实；
- “低于同类”是筛选信号，不是 Root Cause；
- 具体比较口径与 peer definition 进入 02；
- 具体原因调查进入单站点页面。

## 3.2 DOE / FEMP — EMIS 需要 Identify & Prioritize → Diagnose → Correct → Verify

企业总览负责第一步：Identify / Prioritize，并把用户送入后续 owner 页面。

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

本页采用：

- 需要行动的异常比正常背景信息更突出；
- 页面不直接完成 diagnosis / correction；
- Verified Result 与 Expected Result 分离。

## 3.3 ISA-101 — 高性能 HMI 优先 situational awareness

ISA-101 强调信息层级、导航、颜色、动态元素和告警呈现，以减少用户错误并提升判断效率。

来源：

- https://www.isa.org/standards-and-publications/isa-standards/isa-101-standards

本页采用：

- 正常站点视觉安静；
- 需要关注的站点具有明确但克制的优先级；
- 状态不只靠颜色；
- 页面不因实时刷新重新排序到让用户失去空间记忆，除非用户主动选择按最新风险排序。

---

# 4. Primary Questions

页面按以下顺序帮助用户回答问题。

## Q1 — 整个组合里，哪里最值得先看？

默认显示 3–7 个最需要关注的站点或事项，每项必须有：

```text
站点
为什么值得关注
影响维度
持续时间 / 截止时间
数据可信度
下一步
```

示例：

```text
新加坡中央园区
冷站效率连续 4 天偏离同类运行区间
影响：能耗 / 成本
数据：完整
[查看站点]
```

禁止：

```text
Site Health 68
```

但没有解释原因。

## Q2 — Portfolio 的能源绩效大方向是什么？

只展示 2–4 个真正可比较、可解释的核心事实，例如：

```text
总用能
归一化 EUI / EnPI（适用时）
成本（有 billing capability 时）
碳排放（有 carbon capability 时）
```

必须显示：

```text
Period
Scope
Comparison Basis
Coverage / Completeness
```

## Q3 — 改善是否已经被验证？

优先显示：

```text
Verified Savings
已完成 M&V 的项目数
Persistence / Snapback issue
```

必须保持：

```text
Expected Savings
≠ Verified Savings
```

## Q4 — 目标是否需要管理干预？

显示：

```text
At Risk Targets
Blocked Actions
Resource Decision Required
```

不能只显示“目标完成率”。

## Q5 — 数据是否足够支持当前判断？

至少说明：

```text
覆盖多少站点
哪些站点缺关键能源/成本/碳数据
是否存在高业务影响数据质量问题
```

---

# 5. Mandatory Semantic Separation

```text
Portfolio Summary ≠ Site Detail
Benchmark Signal ≠ Root Cause
Ranking ≠ Priority automatically
Performance ≠ Health Score
Expected Savings ≠ Verified Savings
Target Forecast ≠ Target Achievement
Total Energy ≠ Energy Intensity
Cost ≠ Energy
Carbon ≠ Energy
Complete ≠ Accurate
Coverage ≠ Data Quality
Missing Site Data ≠ 0
Not Integrated ≠ No Consumption
```

---

# 6. Context Contract

企业总览 Context 至少包括：

```text
Organization
Portfolio / Business Unit / Region
Reporting Period
Comparison
Metric Set
Currency（成本存在时）
```

Portfolio context 必须进入 URL 或 durable route state，允许分享与恢复。

用户从企业总览进入某个 Site 后必须保留：

```text
Source = Portfolio Overview
Portfolio Filter
Period
Comparison
Selected Metric（如适用）
```

但单站点页面不继续显示无意义的 portfolio 排名卡片。

---

# 7. Information Architecture

```text
Portfolio Context Header
↓
需要关注
↓
核心绩效趋势
↓
站点分布 / 绩效分布
↓
已验证改善
↓
目标与阻塞
↓
数据覆盖与可信度
↓
最近重要变化
```

默认页面不超过 2–4 个 Summary Cards。

“需要关注”是首屏主区域，不是 Summary Cards 之后的第六屏。

---

# 8. Attention Item Contract

Attention Item 只能来自明确 owner：

```text
Alarm / Operations
Work Order
Energy / Efficiency Analytics
Data Quality
M&V
Target / Action Plan
Management Review
```

每项至少包含：

```text
Site
Business Reason
Impact
Source Domain
Occurred / Started At
Current Owner / Responsibility（适用时）
Data Quality
Next Action
```

前端不能自己通过多个 KPI 拼一个 attention score。

---

# 9. Portfolio Metric Contract

Portfolio 聚合指标必须由权威 owner 提供：

```text
Metric Definition
Scope
Numerator / Denominator（如适用）
Normalization Method
Period
Coverage
Included Sites
Excluded Sites
Revision / Method
```

禁止浏览器：

```text
fetch all sites
→ sum / average
→ authoritative portfolio KPI
```

尤其：

```text
Average of site EUI
≠ Portfolio EUI automatically
```

---

# 10. Site Attention / Ranking Contract

企业总览可以显示“需要关注的站点”，但不能把它设计成完整 ranking page。

排序依据必须可解释，例如：

```text
Verified major deviation
Target at risk
Critical operational issue
High-value opportunity
Data quality blocking formal result
Overdue management action
```

如果用户需要完整横向比较，进入 02 站点对标。

---

# 11. Verified Improvement Contract

默认只消费 24 M&V 的正式结果：

```text
Verified Savings
Reporting Period
Result Revision
Persistence State
```

可以同时展示 Expected Pipeline，但必须分组：

```text
预计改善
已验证改善
```

不能相加成“总节能”。

---

# 12. Goal / Action Contract

目标相关摘要来自 23：

```text
At Risk
Off Track
Pending Verification
Blocked Actions
Management Decision Required
```

```text
Action Complete ≠ Target Achieved
Forecast On Track ≠ Achieved
```

---

# 13. Data Confidence Contract

企业总览不显示黑盒 Data Health Score。

至少明确：

```text
Site Coverage
Metric Coverage
Estimated / Missing
High-impact Data Quality Issues
```

例如：

```text
12 / 14 站点具备完整电力数据
2 个站点账单数据未接入
1 个站点 M&V 结果受流量计质量影响
```

比：

```text
Data Health 86
```

更适合管理决策。

---

# 14. User-friendly Content Contract

首页每个区块都必须回答一个用户问题，而不是以数据模型名命名。

推荐：

```text
需要关注
能源绩效
已验证改善
目标与阻塞
数据覆盖
```

避免：

```text
Portfolio KPI
Analytics
M&V Projection
Issue Projection
```

规则：

- 首屏只显示需要用户理解/决定的信息；
- 每个异常必须解释“为什么值得关注”；
- 每个区块有明确下一步；
- 默认隐藏内部 ID、revision、method hash；
- 专业口径通过“查看方法 / 查看证据”渐进展开；
- 空状态说明“没有问题”还是“没有数据”；
- 禁止只写“暂无数据”。

---

# 15. Loading / Empty / Partial / Error

必须区分：

```text
Loading
Ready with data
Ready empty
Partial
Stale
Suspect
Owner unavailable
Not integrated
Not authorized
Request failed
```

示例：

```text
成本数据未接入
能源与 M&V 结果仍可正常查看。
```

而不是整个页面 Error。

---

# 16. Capability Gating

以下能力只有真实接入时显示：

```text
Billing / Cost
Carbon
Comfort / IEQ
DER
Portfolio Benchmark Standard
Formal EnMS / Management Review
```

能力不存在时不显示空卡片占位。

---

# 17. Primary Actions

默认 Primary Actions 不超过 1–2 个，例如：

```text
查看站点对标
查看需要关注的站点
```

管理层不应在此页直接：

- ACK Alarm；
- 完成 Work Order；
- 调整 Setpoint；
- 发布 Strategy；
- 修改 EnPI / Baseline。

---

# 18. Accessibility / Responsive

- 颜色不是唯一状态编码；
- Site list / attention list 使用 semantic table/list；
- 所有 KPI 有文本 label 与单位；
- 键盘可进入站点详情；
- 768px 下仍能看到：需要关注、核心绩效、Verified Savings、目标风险；
- 图表必须有 table/text alternative；
- 实时刷新不得抢 focus。

---

# 19. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 企业总览 · 亚太区                              2026 年度 · 截至 9 月         │
│ 14 个站点 · 12 个能源数据完整 · 2 个部分接入                               │
├──────────────────────────────────────────────────────────────────────────────┤
│ 需要关注                                                                    │
│ 1. 新加坡中央园区  冷站效率连续偏离 4 天      [查看站点]                    │
│ 2. 上海研发中心    2026 节能目标存在风险       [查看目标]                    │
│ 3. 东京办公园区    M&V 结果受流量计质量影响    [查看数据问题]                │
├──────────────────────────────────────────────────────────────────────────────┤
│ 能源绩效                         │ 已验证改善                                 │
│ 总用能  38.2 GWh                │ Verified Savings  3.1 GWh                 │
│ 同比   -4.8% · 同口径           │ 8 个项目已完成 M&V                        │
│ [查看站点对标]                   │ 1 个项目出现持续性回退                     │
├──────────────────────────────────────────────────────────────────────────────┤
│ 目标与阻塞                       │ 数据覆盖                                   │
│ 2 项目标存在风险                 │ 12/14 站点完整                             │
│ 1 项资源决策待管理层             │ 2 个站点账单数据未接入                     │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

# 20. Browser Acceptance Criteria

## 10-second comprehension

进入页面 10 秒内能回答：

- 最值得关注的是哪几个站点；
- portfolio 能源绩效大方向；
- 已验证改善是否存在；
- 数据是否足够可信。

## User-friendly content

- 主界面中文；
- 不显示 UUID / trace / schema / raw enum；
- “需要关注”项有业务原因和下一步；
- capability 不存在时不显示假入口；
- Partial state 不阻塞其他可用事实；
- 普通管理者无需打开 Advanced 才能完成主要判断。

## Professional integrity

- Benchmark 不冒充 Root Cause；
- Expected / Verified 分离；
- Total / Intensity 分离；
- Coverage / Quality 分离；
- Portfolio KPI 有明确 scope / period / method；
- 前端不自行聚合 authoritative portfolio result。

---

# 21. Explicit Non-goals

01 不是：

- 站点实时运行页；
- 设备大屏；
- 完整 Benchmarking；
- 能源分析页；
- 报告中心；
- 管理评审；
- Portfolio CRUD；
- 黑盒企业健康分。

---

# 22. No Defensive Programming / No Compatibility Design

明确禁止：

```text
portfolio API error → []
missing site metric → 0
missing cost → $0
missing carbon → 0 tCO₂e
not integrated → zero
expected savings → verified savings
site rank → root cause
missing coverage → assume complete
portfolio EUI → average site EUI
site data failed → hide site
capability unavailable → empty placeholder card
old dashboard fallback
legacy big-screen compatibility adapter
```

正式原则：

> **企业总览只展示足够支持 portfolio prioritization 的权威业务事实。异常必须可解释，改善必须区分 Expected / Verified，缺失必须保持缺失；用户先看需要做什么，再按需下钻方法和证据。**

---

# 23. READY FOR WIREFRAME Decision

达到 READY FOR WIREFRAME 必须满足：

- Attention Router 而不是 KPI Dashboard；
- Portfolio / Site 职责分离；
- Benchmarking / Root Cause 分离；
- Expected / Verified 分离；
- Coverage / Data Quality 分离；
- Capability gating 明确；
- 主界面中文和业务化；
- 下一步动作明确；
- 10 秒理解原则可验收；
- No Defensive Programming 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
