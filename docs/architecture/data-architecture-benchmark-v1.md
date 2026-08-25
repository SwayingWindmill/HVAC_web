# 数据架构规划：从采集事实到能源事实

状态：PROPOSED / SOURCE-ALIGNED  
范围：Backend 数据与 UI 查询数据  
原则：先固定数据语义和 ownership，再决定物理表/库数量

## 1. 目标数据链

~~~text
Source Definition
    ↓
Raw Ingestion Fact
    ↓
Quality / Cleaning Decision
    ↓
Correction Fact（可选）
    ↓
Normalized Energy Fact
    ↓
Hourly / Daily / Monthly / Yearly Aggregate
    ↓
Billing / Carbon / Baseline / FDD / Report Read Model
~~~

每一层有不同的事实含义，不允许用一个 status 字段把所有层混为“telemetry 已处理”。

## 2. 逻辑数据集

| 数据集 | 内容 | 权威归属 | 主要消费者 |
| --- | --- | --- | --- |
| Registry | Tenant、Site、Space、Asset、Device、Point、Meter、Binding、Category、Tariff 等定义 | PostgreSQL | 所有映射和查询 |
| Raw Telemetry | 原始 event time、ingest time、value、source、quality、mapping revision | ClickHouse + 接收账本 | 清洗、重算、审计 |
| Current Projection | 当前接受值、current revision、quality、watermark | PostgreSQL projection head；Redis 可重建 | Operations UI、告警、实时 |
| Cleaning Decision | good/bad/stale/estimated 的判定、规则、操作人/运行 | PostgreSQL | normalization、质量 UI、审计 |
| Correction Fact | 手工或规则修正值、原因、审批和有效窗口 | PostgreSQL | normalization、settlement |
| Normalized Energy Fact | 统一单位、方向、倍率、能源分类后的能源值 | ClickHouse | aggregation、billing、carbon |
| Aggregate Fact | 固定粒度的能耗、峰谷、成本、碳排、质量摘要 | ClickHouse | 趋势、对比、报表 |
| Processing Run | 输入 watermark、规则版本、依赖 revision、状态、输出 revision | PostgreSQL | 重试、审计、运维 |
| Report Snapshot | 面向用户/导出的稳定结果和生成版本 | PostgreSQL/Object Storage | UI、下载、审计 |
| Control Evidence | command intent、approval、lease、effective value、readback、constraint reason | PostgreSQL | 控制 UI、审计、FDD |

## 3. 时间和质量

原始采集值至少保留：

- event time：设备或来源产生数据的时间；
- ingest time：平台接受数据的时间；
- source key：来源字段/寄存器/通道标识；
- point revision：当时生效的点映射版本；
- quality：GOOD、PARTIAL、ESTIMATED、MANUAL、STALE、INVALID；
- acceptance status：平台是否接受该事件进入事实链。

event-time 乱序数据可以保留为历史事实，但不能让旧 event-time 回退 current projection。COUNTER、单位变更、point revision 变化和数据修复不能跨 revision 盲目聚合。

## 4. 质量与修复不变量

1. Raw Telemetry 永不被覆盖；
2. INVALID 数据默认不能进入正式能源汇总；
3. ESTIMATED/MANUAL 数据必须在下游结果中保留质量摘要；
4. Correction Fact 追加写入，并包含原因、操作者、审批和有效窗口；
5. Cleaning、Normalization、Aggregation 以 run 为单位幂等；
6. 任何重算都记录输入 watermark、规则版本、源事实 revision 和输出 revision；
7. Virtual Meter 依赖图发布前必须无环；
8. Meter Binding 在同一有效期、同一对象和同一用途下必须满足唯一性；
9. Tariff 使用 Site timezone 和有效期版本，不能把服务器时区当业务时区；
10. report snapshot 不能悄悄引用未来版本的 topology、tariff 或 carbon factor。

## 5. 物理存储建议

当前 Phase 1 的 PostgreSQL、ClickHouse、Redis、Object Storage 分工可以继续使用，但需要按下表重新约束：

| 存储 | 允许成为 authority 的内容 | 不允许承担 |
| --- | --- | --- |
| PostgreSQL | registry、业务状态、processing run、quality/correction、control evidence、projection head | 高频历史事实的唯一查询仓库 |
| ClickHouse | raw telemetry、normalized energy fact、aggregate fact、analytics fact | IAM、审批状态、不可变控制生命周期 |
| Redis | current/realtime/cache projection | 唯一业务事实、唯一审计事实 |
| Object Storage | report export、archive、backup、模型/数据集 artifact | 未登记的事实或绕过 manifest 的归档 |
| MQTT | 事件与命令传输 | 事实权威、计算状态、长期查询 |

MyEMS 的多数据库经验被吸收为“逻辑数据集分离”，不是照搬数据库数量。数据库拆分只有在写入模式、查询隔离、保留周期或故障隔离被证明后才实施。

## 6. 对现有数据架构的修改

现有项目已经有 PostgreSQL/ClickHouse/Redis 的方向，但仍需补上以下语义，不应只继续加表：

- 将 Raw、Current、Normalized、Aggregate、Report 明确命名并分开；
- 将 cleaning、normalization、aggregation 变成可观测的 processing run；
- 将 meter/virtual meter/energy category/tariff/carbon/baseline/billing/FDD 纳入数据 ownership；
- 将 quality、provenance、revision、watermark 作为跨层契约；
- 将 UI 所需的趋势、对比、报表结果从基础事实查询中抽出；
- 继续删除已退出的旧数据路径，不增加兼容写入。

## 7. Backend/UI 交付优先级

P1 只实现：

1. 一个站点的 Point → Meter → Energy Category 映射；
2. raw/current/history；
3. quality 和最小 cleaning；
4. 单位/方向/倍率明确的 normalization；
5. 小时/日基础 aggregation；
6. 趋势、对比、质量状态和导出 read model。

Billing、Carbon、Baseline、Forecast、FDD 先完成数据模型和接口 seam，再按真实业务链逐项实现。不要提交只返回空数组的“完成模块”。

