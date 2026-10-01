# 02 站点对标 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-15**  
> **Surface Catalog：** `02 站点对标`  
> **Route intent：** `/portfolio/benchmarking`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **产品语言：** 中文优先；EUI、EnPI、ENERGY STAR 等标准名称保留。  
> **设计输入声明：** 本文件不参考当前项目已有 Benchmark 页、排名榜、大屏、旧 Ant/ProComponents 页面或历史设计稿。现有代码只能在实施阶段作为真实 benchmark / peer / normalization / coverage / permission contract 的候选证据来源。

---

# 1. Primary Job

站点对标的唯一核心任务是：

> **让企业能源负责人快速判断“哪些站点在一个可解释、可比较的口径下表现相对较差或较好”，从而确定进一步调查和投资优先级。**

它是 **portfolio comparison workspace**，不是：

- Root Cause 诊断页；
- 站点实时运行页；
- “排行榜羞辱墙”；
- 自动投资决策系统；
- 用一个不透明分数决定站点好坏的页面；
- 对不同用途、不同数据质量、不同时间口径的站点强行排名。

用户离开本页时应知道：

1. 当前比较的是哪些站点；
2. 使用什么 Metric / Period / Peer Group / Normalization；
3. 哪些站点明显偏离；
4. 哪些结果因数据覆盖或可比性不足不能下结论；
5. 下一步应该进入哪个站点或能源 / 效率 / Energy Review 工作流。

---

# 2. 主要用户

## Primary

- 企业能源负责人；
- 多站点资产 / 设施管理负责人；
- 能源分析工程师。

## Secondary

- 管理层：查看高层比较结果，但不在本页做正式 Management Review；
- 投资 / 项目负责人：消费优先级信号，但不能把排名直接当 business case。

---

# 3. 外部最佳实践依据

## 3.1 ENERGY STAR Portfolio Manager — Benchmarking 是比较与筛选工具

ENERGY STAR 将 benchmarking 定义为把建筑绩效与相似建筑、历史表现或参考水平比较，并明确指出 1–100 Score 是 screening tool，不能解释为什么建筑表现好或差。

来源：

- https://www.energystar.gov/buildings/benchmark
- https://www.energystar.gov/buildings/benchmark/analyze-benchmarking-results
- https://www.energystar.gov/buildings/benchmark/understand-metrics

本页采用：

```text
Benchmark Result
≠ Root Cause
```

以及：

```text
Peer Group / Normalization / Coverage
```

必须与排名一起可见。

## 3.2 ENERGY STAR — Comparable Peer 需要 Property Type / Use / Operating Context

不同建筑用途、规模、气候和运行条件会影响能源绩效，因此对标不能只比较总 kWh。

本页采用：

- Peer Group 必须显式；
- EUI / EnPI denominator 必须明确；
- weather / operating normalization 只在正式模型存在时使用；
- 不具备可比性时允许“不纳入排名”。

## 3.3 DOE / FEMP — 优先排序后仍需进入调查与验证

Benchmark 负责发现对象；Energy / Efficiency / Diagnosis / Opportunity / M&V 负责解释与闭环。

---

# 4. Primary Questions

## Q1 — 当前比较口径是什么？

页面第一眼必须能看到：

```text
Metric
Period
Comparison / Benchmark Reference
Peer Group
Normalization
Coverage
```

例如：

```text
指标：天气归一化 EUI
周期：2025-10 → 2026-09
同类组：办公建筑 · 20,000–60,000 m² · 亚太区
覆盖：12 / 14 站点
```

## Q2 — 哪些站点表现明显偏离？

默认用 Ledger + Distribution / Scatter 支持快速比较。

核心列建议：

```text
站点
核心指标
相对中位数 / 目标
趋势
数据覆盖
可比性
需要关注原因
```

不默认显示十几个工程指标。

## Q3 — 这个排名值得信吗？

必须能看到：

```text
Peer Definition
Normalization Method
Coverage
Estimated / Missing
Excluded Sites
Benchmark Source / Version
```

## Q4 — 为什么这个站点差？

本页不回答 Root Cause。

应该提供明确下钻：

```text
查看站点总览
查看能源分析
查看效率分析
查看能源评审
```

并保留 benchmark context。

## Q5 — 哪些优秀站点值得复制经验？

对标不仅显示 bottom performers，也允许识别：

```text
Consistently High Performer
Recent Improvement
Best Comparable Site
```

但：

```text
Top Rank ≠ Best Practice Proven
```

需要进一步查看证据和方法。

---

# 5. Mandatory Semantic Separation

```text
Benchmarking ≠ Diagnosis
Ranking ≠ Root Cause
Ranking ≠ Investment Approval
Score ≠ Explanation
Peer Group ≠ Entire Portfolio
Total Energy ≠ Energy Intensity
Site EUI ≠ Source EUI automatically
Actual ≠ Weather-normalized
Normalized ≠ Measured
Target ≠ Peer Median
Peer Median ≠ Baseline
Benchmark Reference ≠ EnB automatically
Incomplete ≠ Zero
Excluded ≠ Best/Worst
Comparable ≠ Identical
```

---

# 6. Benchmark Context Contract

必须具有：

```text
Portfolio Scope
Metric
Unit
Numerator / Denominator
Period
Peer Group
Normalization Method
Benchmark Reference
Benchmark Version
Coverage
Timezone / Period Alignment
```

所有这些 Context 必须可分享、可恢复。

---

# 7. Peer Group Contract

Peer Group 只能来自明确 owner / approved benchmark definition。

可以依据：

```text
Property / Building Type
Use
Size Band
Climate / Region
Operating Hours
Occupancy / Production Context
Technology / System Type（如业务允许）
```

但前端不能：

```text
同一个城市
→ same peer group
```

或：

```text
建筑名字相似
→ comparable
```

Peer Group 必须显示：

```text
Name
Definition
Included Sites
Exclusions
Owner
Version
Effective Period
```

---

# 8. Metric Contract

Benchmark Metric 至少说明：

```text
Name
Definition
Unit
Direction（higher/lower is better）
Numerator
Denominator
Normalization
Method / Version
Validity Requirements
```

例如：

```text
天气归一化 EUI
kWh/m²·年
Lower is better
```

不能只显示：

```text
72
```

---

# 9. Ranking Contract

Ranking 只有在：

```text
Metric Comparable
Period Aligned
Peer Group Valid
Coverage Sufficient
Metric Valid
```

时才可以出现。

否则显示：

```text
不参与排名
原因：面积数据缺失
```

而不是把站点排在最后。

排名必须允许用户切换：

```text
按表现
按偏差
按改善幅度
按目标风险
```

但每个排序都必须解释口径。

---

# 10. Distribution / Scatter Contract

主图优先回答：

> **这个站点相对同类处于什么位置？**

推荐：

```text
Distribution / Percentile
或
Scatter: Size / Load Context vs Performance
```

图上至少提供：

```text
Peer median / range
Selected Site
Coverage
Metric / Unit
```

禁止：

- 彩虹色 3D 排名图；
- 大量不可点击小站点标签；
- 用面积大小表现与用户任务无关的第三维；
- 用颜色作为唯一好坏编码。

---

# 11. Comparison Types

至少明确区分：

```text
Peer Comparison
Past Performance
Target Comparison
Reference / Standard Comparison
```

不能把这些全部叫：

```text
对标值
```

其中：

```text
Peer Median ≠ Target
Past Period ≠ Baseline
Benchmark Reference ≠ Energy Baseline
```

---

# 12. Normalization Contract

Normalization 只消费正式 owner 的方法。

必须显示：

```text
Raw Actual
Normalized Metric（适用时）
Method / Variables
Version
Applicability
```

不能：

```text
normalize = true
→ 隐藏 actual
```

也不能前端临时拟合天气模型。

---

# 13. Coverage / Data Quality Contract

每个站点至少需要：

```text
Coverage
Data Nature
Quality State
Comparable State
```

例如：

```text
东京办公园区
EUI  142 kWh/m²·年
Coverage  98.7%
Estimated  1.1%
Comparable  Yes
```

另一个：

```text
上海研发中心
—
不参与排名
原因：建筑面积未确认
```

不能显示 0。

---

# 14. Information Architecture

```text
Benchmark Context Header
↓
Comparison Controls
↓
Distribution / Scatter
↓
Site Benchmark Ledger
↓
Selected Site Inspector
↓
Method / Peer Definition / Coverage
↓
Drill-down to Site / Energy / Efficiency
```

默认页面不使用 KPI 卡片墙。

---

# 15. Site Benchmark Ledger

默认列控制在能够完成核心扫描的范围：

```text
站点
核心指标
相对同类
趋势
覆盖 / 可比性
需要关注原因
```

Advanced columns 才进入：

```text
normalized variables
benchmark version
property type
floor area
operating hours
raw / normalized split
method diagnostics
```

---

# 16. Inspector Contract

选择站点后，Inspector 只显示：

```text
站点身份
当前指标
Peer Position
趋势
Coverage / Comparable State
为什么值得关注
下一步
```

不塞完整能源分析或效率分析。

---

# 17. Cross-surface Handoff

## 02 → 03 站点总览

携带：

```text
Site
Benchmark Metric
Period
Peer / Reference
Source Context
```

## 02 → 14 能源分析

携带：

```text
Site
Period
Metric / Energy Type
Comparison
```

## 02 → 16 效率分析

仅当偏差与系统效率调查相关时进入，并携带 comparable window。

## 02 → 17 能源评审

用于正式 EnPI / EnB / SEU 治理时进入。

---

# 18. User-friendly Content Contract

默认文案使用用户能理解的业务问题：

推荐：

```text
站点表现
相对同类
近 12 个月变化
是否可比
为什么值得关注
```

避免默认使用：

```text
Percentile Engine
Normalization Projection
Benchmark Model Output
```

规则：

- 所有排名旁边都能看到比较口径；
- “差”必须说明相对谁差；
- “改善”必须说明相对哪个期间；
- “不可比”必须说明原因；
- 默认不显示内部 metric ID / model hash / raw enum；
- 下钻按钮使用具体动作，如“查看站点能源分析”，不写“详情”。

---

# 19. Empty / Partial / Error

示例：

```text
当前只有 3 个站点满足该同类组条件，暂不生成排名。
你仍可以查看各站点的原始 EUI。
```

优于：

```text
暂无数据
```

如果 benchmark service 不可用：

```text
站点原始能源指标仍可查看；同类排名暂不可用。
```

Partial state 不阻塞整个页面。

---

# 20. Capability / Permission

Portfolio Benchmarking 只对：

```text
Portfolio capability exists
AND principal may discover portfolio comparison
```

时显示。

用户可看 ranking 不等于可编辑：

```text
Peer Definition
Benchmark Method
Target
```

编辑进入有权限的治理流程。

---

# 21. Accessibility / Responsive

- Table 使用 native table semantics；
- 图表提供 table/text alternative；
- 颜色不是唯一好坏编码；
- selected site 有非颜色选中状态；
- keyboard 能完成 filter / sort / select / drill-down；
- 768px 下保留：站点、指标、相对同类、覆盖、下一步；
- hover tooltip 不是唯一方法说明入口。

---

# 22. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 站点对标 · 亚太区                                              [方法说明]   │
│ 指标：天气归一化 EUI · 2025-10→2026-09 · 办公建筑 20k–60k m²              │
│ 12 / 14 站点可比                                                           │
├──────────────────────────────────────────────────────────────────────────────┤
│                    同类分布 / Selected Site                                 │
│  P10 ───── P25 ───── Median ───── P75 ───── P90                            │
│                         ● 新加坡中央园区                                    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 站点              EUI          相对中位数    12 月趋势   可比性   下一步   │
│ 新加坡中央园区     168          +22%           ↑ 8%       可比     查看站点 │
│ 东京办公园区       121           -8%           ↓ 4%       可比     查看站点 │
│ 上海研发中心        —             —             —        不可比   查看原因 │
├──────────────────────────────────────────────────────────────────────────────┤
│ 选中：新加坡中央园区                                                        │
│ 为什么值得关注：连续 4 个月高于同类中位数，且近 12 个月继续上升            │
│ [查看站点总览] [查看能源分析] [查看效率分析]                               │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

# 23. Browser Acceptance Criteria

## 10-second comprehension

10 秒内用户知道：

- 当前比什么；
- 跟谁比；
- 哪些站点偏离；
- 哪些结果不可比。

## User-friendly

- 主界面中文；
- 排名带比较口径；
- 不可比有原因；
- 主表无需横向滚动完成核心判断；
- Advanced 才展示模型 / 变量 / 版本；
- 下钻动作具体可理解。

## Professional integrity

- Ranking 不等于 Root Cause；
- Peer Median 不等于 Target / Baseline；
- Raw / Normalized 分开；
- Comparable / Excluded 分开；
- Coverage / Quality 分开；
- 不对不可比站点强制排名。

---

# 24. Explicit Non-goals

02 不是：

- 企业总览；
- 站点运行页；
- Root Cause 诊断；
- EnPI / EnB editor；
- 自动投资审批；
- 全国 ENERGY STAR Score 替代品；
- 黑盒排行榜。

---

# 25. No Defensive Programming / No Compatibility Design

明确禁止：

```text
benchmark API error → []
missing metric → 0
missing floor area → divide by 1
missing weather → use global default
missing peer group → all sites
missing benchmark → previous period
not comparable → rank last
normalized unavailable → actual labeled normalized
low rank → root cause
rank 1 → best practice proven
score missing → 50
coverage unavailable → assume 100%
old ranking dashboard fallback
legacy portfolio adapter
```

正式原则：

> **站点对标只在可解释、可比较的口径下给出筛选信号。排名必须带 Peer / Period / Metric / Coverage，Benchmark 不是 Root Cause，不可比就是不可比，Unknown 保持 Unknown。**

---

# 26. READY FOR WIREFRAME Decision

达到 READY FOR WIREFRAME 必须满足：

- Metric / Peer / Period / Coverage 可见；
- Ranking / Root Cause 分离；
- Peer Median / Target / Baseline 分离；
- Raw / Normalized 分离；
- Comparable / Excluded 分离；
- Ledger + Distribution 而不是排名卡片墙；
- 中文业务语言；
- 明确下钻路径；
- No Defensive Programming 明确。

**当前决定：SELECTED / READY FOR WIREFRAME。**
