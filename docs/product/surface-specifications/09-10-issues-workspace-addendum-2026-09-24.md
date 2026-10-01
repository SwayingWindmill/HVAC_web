# Surface 09 + 10 Addendum — 告警与诊断 Workspace

日期：2026-09-24  
状态：SELECTED / FRONTEND TARGET ADDENDUM  
适用 Surface：09 Alarm Center + 10 Diagnosis Center  
Workspace：issues

## 1. 合并裁决

09 继续拥有 Alarm-led operational queue 的主骨架。

10 不再作为与 Alarm 平级的 Workspace Tab。Diagnosis 是选中 Issue 后的 Investigation Context，并在需要更深分析时进入 durable detail route。

正式关系：

~~~text
Alarm Queue
↓ select issue
Investigation Sheet
  Alarm facts
  + Published Finding
  + Impact
  + Hypotheses
  + Evidence
  + Root Cause Decision
  + Verification
  + Related Issues
  + Timeline
↓
Durable Investigation Route / Trends / Work Order / Device / Data Quality
~~~

## 2. Workspace Task Modes

合法的 peer task modes 只有：

- 问题处置；
- 告警分析。

它们处理不同任务与不同 read model，因此可以使用 Tabs。

以下状态不是 task modes：

- 当前活动；
- 全部记录；
- 已搁置。

它们属于 Alarm Ledger 的范围 Filter，必须进入唯一的 table filter model。

## 3. Alarm ≠ Finding ≠ Hypothesis ≠ Root Cause

必须维持以下语义：

- Alarm：异常条件与生命周期事实；
- Finding：规则或模型发布的诊断结果；
- Hypothesis：调查中的候选解释，可以被证据支持、削弱或排除；
- Root Cause：经过明确决策与证据闭环确认的结论。

前端不得：

- 将 Finding score 改写为 root-cause probability；
- 自动把最高分 Hypothesis 标记为 Root Cause；
- 将 ACK、Assignment 或 Clear 当作 diagnosis state；
- 将 Root Cause confirmation 隐含在 Work Order completion 中。

## 4. Issue Priority

Issue priority 由至少两个维度共同表达：

~~~text
Operational urgency
= Alarm Severity / physical state / duration / handling state

Business impact
= avoidable energy
+ avoidable cost
+ comfort impact
+ reliability risk
~~~

两个维度不得压成一个不透明 health / priority score。

## 5. Investigation Sheet

桌面端：

- right-side OperationalDetailSheet；
- non-modal；
- no overlay；
- 不改变 Ledger width；
- sticky footer 只放直接处置动作；
- professional exits 放在正文。

窄屏：

- modal Sheet；
- overlay；
- 内容可滚动；
- 无 page-level horizontal overflow。

Sheet 顺序：

1. Issue identity / severity / condition / handling / diagnosis；
2. Current problem；
3. Core facts；
4. Published Finding；
5. Impact；
6. Investigation hypotheses；
7. Root Cause / Verification；
8. Related Issues；
9. Evidence；
10. Lifecycle / handling Timeline；
11. Trends / Operations / Device / Data Quality / Work Order exits。

## 6. Alarm Performance

Alarm Performance 是正式 analysis surface，不是空状态和 KPI card wall。

第一层：

- total triggered；
- MTTA；
- MTTR；
- diagnosis coverage。

第二层：

- repeat issue rate；
- Alarm Flood；
- standing / stale；
- chattering / nuisance；
- severity distribution。

第三层：

- avoidable energy；
- avoidable cost；
- comfort / reliability impact。

第四层：

- weekday × time Heatmap；
- Top contributors；
- recurring issue classes；
- future rule bad actors / Pareto。

所有 metrics 的定义与聚合必须由 authoritative backend contract 提供；Frontend Review fixture 只用于目标态验收。

## 7. Component / Registry Decision

### ADOPT

- tablecn + DataTableBlock：Issue Ledger；
- OperationalDetailSheet：quick detail；
- dashboardcn Timeline：audit chronology。

### ADAPT

- dashboardcn Heatmap Chart：weekday × time Alarm density；
- dashboardcn Bar List：contributors / recurring issues。

### REVIEW

- Dice UI Action Bar：bulk acknowledge / assign / suppress；
- tablecn advanced filter grammar：在 server filter ownership 不变的条件下替换旧式 Select 排列。

## 8. Backend Cooperation Boundary

本 Addendum 不授权后端改造。前端只定义目标合同和 unavailable behavior。

目标读取：

- issue investigation projection；
- issue performance aggregation。

目标命令：

- bulk acknowledge；
- bulk assign；
- bulk suppress。

指标、影响、Root Cause、Hypothesis 与 relation 的事实 ownership 必须留在后端；前端不推导生产事实。

## 9. Acceptance Gate

页面不得通过以下方式“看起来更丰富”：

- 再加一个 Diagnosis Tab；
- 增加无来源的 AI score；
- 用 Card wall 代替分析层级；
- 用 Severity 猜 energy cost；
- 用 occurrence count 猜 Alarm Flood；
- 用客户端已加载页计算全站 MTTA / MTTR；
- 为热力图或排行生成生产假数据。

正确的丰富度来自：

- 更好的状态分层；
- 可追溯的 evidence；
- 可证伪的 hypothesis；
- 可执行的 verification；
- 业务 impact；
- 系统性 performance analytics；
- 清楚的跨 Workspace exits。
