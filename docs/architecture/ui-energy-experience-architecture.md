# UI 能源体验目标架构

状态：PROPOSED / SOURCE-ALIGNED  
范围：当前 React UI 与 Backend Query/Realtime module  
原则：围绕能源管理任务组织 UI，不把设备 telemetry 看板当作完整产品

## 1. UI 的三个工作空间

~~~text
Operations
├─ Site / Space / Asset overview
├─ Live telemetry / status
├─ Alarm / notification / work order
└─ Command status / evidence

Energy Management
├─ Energy category / meter hierarchy
├─ Trend / comparison / ranking
├─ Energy flow / balance
├─ Billing / carbon
├─ Baseline / plan / forecast
└─ Data quality / repair / report export

Administration
├─ Tenant / user / permission
├─ Site / space / asset / device / point
├─ Meter binding / virtual meter
├─ Tariff / calendar / carbon factor
├─ Dashboard / widget / alias
└─ Manifest / capability / integration configuration
~~~

ThingsBoard 的 Dashboard、Widget、Entity Alias 和 time window 提供可配置视图机制；MyEMS 的 admin/web 源码显示配置维护和能源分析是两类任务；OpenEMS 的 Channel/EdgeConfig 提供了运行时能力自描述方向。三者结合后，目标不是复制任一界面，而是“Asset-centered 上下文 + 可配置视图 + 能源分析任务 + 能力驱动控件”。

## 2. UI module 与 Backend seam

| UI module | 只依赖的 Backend seam | 不应直接依赖 |
| --- | --- | --- |
| Context Navigator | hierarchy/ownership query | 数据库表、MQTT topic |
| Live View | current snapshot、realtime subscription | adapter 内部对象 |
| Energy Analysis | normalized/aggregate query、comparison query | raw telemetry 拼装逻辑 |
| Quality & Repair | quality query、correction workflow | 直接修改 raw fact |
| Alarm/FDD/Work Order | lifecycle query、action command | 自己判断告警最终状态 |
| Command View | command lifecycle、evidence query | 设备协议响应 |
| Dashboard | dashboard state、alias、widget datasource | 任意领域表名 |
| Report | report snapshot、export job | 在浏览器中计算账单/碳排 |
| Administration | registry/binding/tariff/manifest command | 绕过权限的配置写入 |

## 3. 数据呈现规则

所有时间序列和指标返回以下上下文，而不是只有 number：

- subject：Site、Space、Asset、Device、Point 或 Meter；
- time range 和 timezone；
- unit 与方向语义；
- quality summary；
- data freshness；
- source/processing revision；
- whether value is raw、normalized、estimated、manual 或 aggregate。

对于控制操作，至少显示 requested value、effective value、constraint reason、winning controller、command state 和 readback evidence。当前没有生产 Edge 时，UI 必须显示 simulator/contract 状态，不把 accepted 当成 applied。

## 4. 当前 UI 的直接修改

- 将首页和主导航从“设备监控优先”改为 Operations 与 Energy Management 并列；
- 新增 Meter/Energy Category/Space 的上下文导航；
- 增加趋势对比、能流/平衡、质量、数据修复和报告入口；
- 将 dashboard/widget/alias 作为可配置视图能力，但先实现有限 widget 类型，不先做通用低代码编辑器；
- 管理页面覆盖绑定、tariff/calendar/carbon factor，而不只维护设备字段；
- 把 current/history/normalized/aggregate 的显示标签明确写出；
- 删除 UI 直接访问内部数据库或 MQTT 的旧路径，不增加兼容分支。

## 5. P1 UI 最小交付

P1 只要求一条真实链路：

1. 选择 Tenant/Site/Space/Asset；
2. 查看设备/计量点 current 与 quality；
3. 查看历史趋势；
4. 查看小时/日能耗；
5. 按空间、设备、能源分类进行基础对比；
6. 显示数据 freshness、processing revision 和异常质量；
7. 导出当前 query 对应的 report snapshot。

P1 不要求：

- 全量 Dashboard 低代码设计器；
- 全量协议驱动控制；
- 自动化优化下发；
- 未接通 Backend 的静态报表页面。

## 6. 验收条件

- UI 不需要理解 PostgreSQL/ClickHouse 的物理分布；
- 同一数据在 Operations 和 Energy Management 中不会因查询路径不同而产生矛盾；
- quality、timezone、unit、revision 显式可见；
- 报表结果可以追溯到 processing run；
- Admin 的绑定修改能触发必要的 normalization/aggregation revision；
- 断开 realtime 时，UI 回退到明确标记 freshness 的 current/history 查询，不伪造实时状态。

