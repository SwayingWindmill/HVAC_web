# 页面收敛与交互层级审计 2026-09-22

> 状态：SUPERSEDED / INITIAL HYPOTHESIS
>
> 已被 `docs/product/smart-energy-system-page-architecture-v3-research-backed.md` 取代。初版仅基于内部结构分析，后续成熟 BMS / EMIS / FDD / FM 产品研究修正了多个结论，尤其是 Trend、Alarm/Diagnosis/Work、Control 以及 desktop Inspector 的层级。
>
> 历史目标：减少表面页面数量，保留专业能力；把 Surface、Route、Sidebar Entry、Tab、Inspector、Dialog 分离建模。

## 1. 核心结论

当前 36 个 Surface Catalog 适合作为“产品能力目录”，不适合直接等价为 36 个页面，更不适合直接投影成 Sidebar。

当前实现的主要结构性问题：

1. Surface = Page = Route = Navigation Entry 被过度绑定。
2. 多个分析页共享同一站点、时间范围、对象、比较维度和工具栏，却被拆成独立页面。
3. 多个 Ledger 的“查看一条记录”被建模成详情页面，而快速查看更适合 Context Inspector。
4. 系统治理类能力直接进入全局导航，挤占日常运营导航。
5. Detail Route 与 Quick Inspect 没有明确分工，导致用户频繁离开当前工作上下文。
6. 02“站点对标”的业务语义是 portfolio benchmarking，但当前 Surface scope / route 是 site，存在信息架构语义错位。

新的原则：

```text
Capability Catalog
      ↓
Primary Workspace
      ↓
Tab / View
      ↓
Context Inspector (Sheet)
      ↓ only when durable depth is required
Durable Detail Route
```

Sidebar 只列 Primary Workspace，不列所有 Capability。

## 2. 建议的一级工作台

| 新工作台 | 吸收当前 Surface | 默认结构 | 说明 |
| --- | --- | --- | --- |
| 企业 | 01 + 02 | Overview + Benchmarking tabs | 02 应回到 portfolio 语义，不再作为单站点一级页 |
| 站点总览 | 03 | Attention Router | 保持独立，是站点任务入口 |
| 运行 | 04 + 08 | 系统运行 / 空间环境 tabs | Comfort/IEQ 是运行与优化 guardrail，不必长期占一个一级页面 |
| 趋势 | 05 | Analysis Workspace | 保持独立，跨对象、跨事件的时序调查需要稳定 URL |
| 设备 | 06 + 07 quick inspect | Ledger + Sheet Inspector | 07 的完整详情 Route 仅用于持续调查 |
| 事件与工作 | 09 + 10 + 11 + 13 | 告警 / 诊断 / 工单 / 验证 tabs | 共享同一异常处置闭环；保留对象间语义边界，不再占四个 Sidebar 项 |
| 能源与绩效 | 14 + 15 + 16 + 17 + 18 + 19 + 20 | 用能 / 需量 / 能效 / 绩效方法 / 成本 / 碳 / 灵活性 capability tabs | 共享 period、comparison、baseline、meter lineage；按 capability 显示 tab |
| 改进 | 21 + 22 + 23 + 24 + 30 | 机会 / 方案 / 行动计划 / M&V / 管理评审 | 对应持续改进闭环；复杂方案和 M&V 项目可进入 durable detail |
| 控制与策略 | 25 + 26 + 28 | 即时控制 / 策略 / 执行记录 tabs | 27 策略详情保留 durable route，用于仿真、版本、审批、发布 |
| 报告 | 29 | Report Workspace | 报告生成、计划和分发有独立持续任务，可保留 |
| 系统设置 | 31 + 32 + 33 + 34 + 35 + 36 | 数据 / 模型 / 规则 / 集成 / 站点 / 权限 local navigation | 不再把六个治理能力全部放进日常业务 Sidebar |

一级导航目标约为 10–11 个工作台，而不是 30+ 个业务页面入口。

## 3. Detail Route 保留标准

只有满足以下条件之一，才值得拥有独立 detail route：

- 用户需要长时间持续调查或编辑同一对象；
- 页面需要多区块、多 tab、大量证据或工程上下文；
- URL 需要被分享、收藏、审计引用或从通知直接打开；
- 浏览器 Back/Forward 对任务有明确意义；
- 工作会跨越多个步骤或会话；
- 离开 Ledger 上下文是合理的任务切换。

建议继续保留 durable route 的对象：

- 设备详情：持续设备调查；
- 工单详情：checklist、timeline、notes、attachments、completion evidence；
- 优化方案详情：风险、约束、审批、测试、回滚；
- 策略详情：仿真、版本 diff、审批、rollout；
- M&V 项目详情：baseline/model/adjustment/evidence；
- 必要时报告定义编辑器、复杂集成映射编辑器。

不应仅因为“对象有详情”就自动创建 route。

## 4. Ledger / Table 的默认交互

推荐统一模式：

```text
Table row
  ↓
select
  ↓
Context Inspector
  implemented by shadcn Sheet
  ↓
quick facts / evidence / safe actions
  ↓
“打开完整详情”
  ↓ only when necessary
Durable Detail Route
```

产品层名称建议使用 **Inspector / 详情侧栏 / 快速查看**。
实现层使用 shadcn `Sheet`。

Sheet 的作用是补充当前主内容，而不是替代所有详情页。

### 适合 Sheet

- 查看设备关键状态；
- 查看告警证据与处置状态；
- 查看诊断摘要；
- 查看工单摘要；
- 查看节能机会证据；
- 查看执行记录；
- 查看数据质量问题；
- 查看用户、角色、审计事件；
- 中等复杂度、需要保留列表上下文的编辑。

### 适合 Dialog / AlertDialog

Dialog 是阻塞式聚焦交互，底层内容进入 inert 状态。

适合：

- ACK / Assign 等短表单；
- 删除、取消、提交、发布等确认；
- 小型创建/编辑；
- 需要用户明确完成或取消后才能继续的动作。

高风险不可逆动作优先 AlertDialog / confirmation pattern，而不是 Sheet。

### 适合完整 Route

- 多 tab、多证据、长时间调查；
- 多步骤工程编辑；
- 仿真 / 审批 / 发布；
- checklist + timeline + attachment；
- 需要 deep link / bookmark / audit reference 的对象；
- 独立分析工作区，例如趋势。

## 5. Row Action 规则

整行点击默认执行“查看 / inspect”，不直接执行业务写操作。

行内行为建议固定为：

```text
Primary row click → Sheet Inspector
Object name/link → optional durable detail route
⋯ menu → secondary row actions
Checkbox → selection only
Explicit button → named business action
```

不要让同一行不同空白区域随机进入不同子页面。

## 6. 第一批建议合并

### A. 设备 06 / 07

06 继续作为 Ledger。
Row → Sheet。
Sheet 中提供“打开设备详情”。
07 保留 hidden durable route。

### B. 告警 09 / 诊断 10 / 工单 11 / 验证 13

合并为“事件与工作”工作台。

建议 URL：

```text
/sites/:siteId/work?view=alarms
/sites/:siteId/work?view=diagnostics
/sites/:siteId/work?view=orders
/sites/:siteId/work?view=verification
```

对象之间通过 sourceContext 串联，不通过四个一级导航来表达闭环。

12 工单详情继续保留 durable route。

### C. 能源 14–20

合并为“能源与绩效”。

建议共享：

- period
- energy source
- comparison
- baseline
- selected scope
- data-quality context

视图通过 query/tab 切换；Billing / Carbon / DER 只在 capability 存在时出现。

### D. 节能机会 21 / 优化 22 / 行动计划 23 / M&V 24 / 管理评审 30

合并为“改进”工作台，但不混淆业务语义。

Opportunity、Proposal、Approved Action、Verified Savings 必须继续作为不同对象。

列表对象先 Sheet inspect；复杂方案、M&V 项目进入 durable route。

### E. 控制 25 / 策略 26 / 执行 28

合并为“控制与策略”。

25 = 当前授权下的即时控制。
26 = 自动化策略资产。
28 = 不可含糊的执行事实。

27 策略详情继续为 durable route。

### F. 31–36

统一进入“系统设置”Shell 的 local navigation。

Data Quality 可以从所有业务页的数据状态 deep-link 进入，但不需要和日常运行页面平级占据 Sidebar。

## 7. 不建议的极端收敛

不要把所有东西都变成 Sheet。

以下做法同样会损害产品：

- 把设备完整工程详情塞进 384–600px Sheet；
- 把工单执行全过程做成 Drawer；
- 把策略仿真/审批做成弹窗；
- 把趋势分析放进 Sheet；
- 仅为了减少 route 数量而让 URL 无法表达用户的持久工作上下文。

目标不是“页面越少越好”，而是：

> 一个 Route 对应一个值得持续存在的用户任务；一个 Sheet 对应一次不应打断当前上下文的快速调查。

## 8. 实施顺序

Phase 1：先只改 IA 与交互 contract，不删除业务能力。

- 新建 Primary Workspace Catalog；
- 从 Surface Catalog 分离 capability / route / navigation；
- 定义统一 Interaction Level：inline / popover / dialog / sheet / route；
- 给现有 36 Surface 标记 KEEP_ROUTE / MERGE_TAB / SHEET_ONLY / SETTINGS_CHILD。

Phase 2：优先迁移高收益重复区。

1. 设备 Ledger / Detail quick inspect。
2. 事件与工作 09–13。
3. 能源与绩效 14–20。
4. 控制与策略 25–28。
5. 系统设置 31–36。
6. 改进闭环 21–24 + 30。

Phase 3：删除旧路由、旧导航与兼容入口，不做 alias/compatibility 页面。
