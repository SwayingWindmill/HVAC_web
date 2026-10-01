# Issues Operations Surface Contract

**Status: SELECTED**  
**Workspace:** 告警与诊断  
**Primary task:** 把大量 Alarm 信号整理成可处理的问题，帮助值班人员判断先处理什么、为什么、下一步做什么，并保留原始告警事实可追溯。  
**Composition:** problem-first queue + raw Alarm ledger + OperationalDetailSheet + analysis view.

## 1. Operator-facing model

页面不向用户展示内部架构术语。用户需要看到的是：

~~~text
问题处置
├─ 待处理问题
│  ├─ 问题
│  ├─ 关联告警
│  ├─ 处理进度
│  ├─ 影响
│  ├─ 负责人
│  ├─ 下一步
│  └─ 最近变化
└─ 原始告警
   └─ authoritative Alarm ledger

告警分析
├─ 触发与响应效率
├─ 重复问题
├─ 告警集中爆发
├─ 高发时段
├─ 高频设备
└─ 能耗 / 成本影响
~~~

内部可以使用 Issue、Finding、Hypothesis、Projection 等领域词，但页面标题和说明应使用自然中文，例如“问题”“诊断结果”“可能原因”“验证情况”。

## 2. Information architecture

Workspace 只有两个 peer task modes：问题处置、告警分析。

当前活动、全部记录、已搁置不是 Tabs，而是范围筛选。

问题处置内部允许一个轻量视图切换：待处理问题、原始告警。默认进入“待处理问题”。原始告警是事实层和审计入口，不是主工作流。

## 3. Problem queue

一个问题可以包含一条或多条相关 Alarm。归并允许使用规则、拓扑、时间重叠、相关性、人工确认或模型结果，但归并关系本身不等于因果关系。

默认列：问题、关联告警、处理进度、影响、负责人、下一步、最近变化。

影响优先表达可避免能耗、可避免成本、舒适影响、可靠性风险。不得把这些维度压缩成不透明的单一“健康分”。

## 4. Raw Alarm ledger

原始告警保持 authoritative Alarm semantics：condition = ACTIVE / CLEARED 是物理事实；ACK、assignment、suppression 是独立处置事实；工单完成不制造 Alarm recovery；页面不能提供不存在的人工恢复或关闭动作。

Raw Alarm ledger 使用 tablecn interaction grammar + project DataTable。

批量选择只作用于 Alarm 事实层。批量确认、指派、搁置必须使用真正 bulk command；不得在浏览器里循环 N 次单条 mutation 冒充批量操作。

## 5. Detail Sheet

选择问题后使用 OperationalDetailSheet，而不是旧 Inspector。

桌面：right-side、non-modal、no overlay、不压缩主列表、width 480–640px。窄屏：modal、overlay、内部滚动、页面级无横向溢出。

内容顺序：
1. 该问题包含几条相关告警，以及为什么一起处理；
2. 下一步；
3. 当前选中的告警事实；
4. 诊断结果；
5. 影响；
6. 可能原因；
7. 原因结论；
8. 验证情况；
9. 其他关联问题；
10. 证据；
11. 状态与处置时间线；
12. 趋势 / 系统运行 / 设备 / 数据质量 / 工单出口。

页面不得出现 Published Finding、Hypothesis、Root Cause Decision、Investigation Projection、AI 根因概率。这些属于实现和领域 contract，不是值班人员文案。

## 6. Diagnosis truth

必须保持：Alarm ≠ diagnosis result ≠ possible cause ≠ confirmed cause。

诊断评分只描述该诊断输出，不表示“原因概率”。可能原因必须支持依据、反证、下一验证。只有权威后端明确确认后，页面才显示“原因已确认”。

## 7. Alarm analysis

告警分析回答实际管理问题：最近触发多少、多久确认、多久恢复、有多少已经得到诊断、哪些问题反复出现、有没有短时间告警集中爆发、哪些设备贡献最多、哪些时段最集中、这些问题造成多少可避免能耗和费用。

Heatmap、ranking、MTTA、MTTR、repeat/flood/standing/chattering 等必须来自后端定义的聚合语义，前端不得用已加载行自行推断全站指标。

## 8. Source selection

Current decisions:
- shadcn/ui / Radix primitives → base UI authority；
- tablecn + project DataTable → operational ledgers；
- OperationalDetailSheet → quick detail；
- dashboardcn Timeline → status / handling chronology；
- dashboardcn Heatmap + Bar List → ADAPT for alarm analysis；
- shadcn ToggleGroup → problem/raw-alarm local view switch；
- Dice UI Action Bar → ADAPT for future raw-Alarm bulk handling。

Existing project implementations have no automatic preference. Use ADOPT / ADAPT / REPLACE / REJECT based on the task.

## 9. Backend cooperation boundary

This frontend phase does not implement backend changes.

Target read models:
~~~text
GET /api/v1/sites/{siteId}/issues?scope=active|all|suppressed
GET /api/v1/sites/{siteId}/issues/{alarmId}/investigation
GET /api/v1/sites/{siteId}/issues/performance?period=7d|30d|90d
~~~

Target bulk commands should support bulk acknowledge, bulk assign, bulk suppress, partial failure, idempotency, authorization and audit.

When a target read model is unavailable, production UI must state that clearly and retain access to the raw Alarm facts. Frontend-review fixtures may show the target product state for design certification only.

## 10. Acceptance

- default task is problem-first；
- raw Alarm facts remain reachable；
- active/all/suppressed are not peer Tabs；
- problem list is not Card-wrapped；
- grouped alarms are visibly traceable；
- grouping never claims causality without evidence；
- Sheet never reflows desktop queue；
- user-visible copy avoids internal architecture jargon；
- diagnosis never becomes an unsupported confirmed cause；
- analysis does not derive production metrics client-side；
- no Ant Table / Drawer / Modal / Tabs；
- narrow layouts do not create page-level horizontal overflow。