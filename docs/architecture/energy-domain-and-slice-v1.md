# Wayfinder #302：能源领域词汇与首个纵向切片

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：Backend 与 UI 的能源领域目标、领域边界、首个可交付切片；不修改部署架构，不实现 Edge。

## 1. 这份决策解决什么问题

三方源码审查已经证明，HVAC 目前拥有一条可以运行的 electricity interval fact 投影链，但这条链不能代表完整的 Energy Backend：

- ThingsBoard 的强项是通用 Telemetry、Latest/History、规则编排、关系聚合和可组合 Dashboard；它没有替代能源计量、费率、碳因子和报表内容模型。
- OpenEMS 的强项是 Edge Channel、Process Image、Cycle、Controller/Scheduler 和 Timedata；这些是未来现场控制面，不是本阶段 Backend 的能源内容。
- MyEMS 明确分离 Cleaning、Normalization、Aggregation、Billing/Carbon/Reporting，并把 Meter、Virtual Meter、Energy Category、Tariff 等作为能源内容；它的 13 个逻辑数据库不应原样变成 HVAC 的 13 个物理数据库。

本文件将这些源码证据转成 HVAC 的当前领域决策。现有代码不享有默认正确性；“已经存在”不等于“已经被领域证明”。

## 2. 证据基线

三方固定提交及完整文件清单见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)：

| 项目 | 固定版本 | 完整提交 |
| --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| OpenEMS | 2026.7.0 | `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` |
| MyEMS | v6.7.0 | `be6e6ce8ddeac57afb04bddb9621501fb555cab0` |

本次为了确定 #302，实际复核了 HVAC 以下源码和合同：

| 本地文件 | 事实 |
| --- | --- |
| [`CONTEXT.md`](../../CONTEXT.md) | Telemetry Runtime 拥有当前运行时真相；Telemetry Quality 与 ingest acceptance 分离；Metric 是有定义、单位、质量策略和溯源的版本化派生事实。 |
| [`009a-energy-topology-metering-v2.sql`](../../infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql) | `energy_meters` 定义物理计量身份；`meter_bindings` 将 Meter、Accounting Edge、Device 和 `COUNTER` Point 通过版本、角色、方向和有效期绑定。还存在 Virtual Meter，但本切片不使用。 |
| [`002-analytics-energy-interval.sql`](../../infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql) | 当前事实表记录 Site、Device、Point、Telemetry Key、Energy Type、区间、质量、源观察 ID、offset、watermark 和 dataset revision；没有 `meter_id`、`meter_binding_id` 或绑定版本。 |
| [`projector.go`](../../services/analytics-read-model-projector/internal/energy/projector.go) | 当前只接受 `hvac_meter.energy`，把两条累计电量观察转换为 electricity interval fact；负值为 INVALID，回退为 SUSPECT。 |
| [`analytics-read-model-projector/README.md`](../../services/analytics-read-model-projector/README.md) | 当前链路是 `telemetry_history.observations -> analytics.energy_interval_facts`，并以 current observation ID 和 ClickHouse 去重令牌避免重复投影。 |
| [`telemetry-query-service/README.md`](../../services/telemetry-query-service/README.md) | 当前查询边界是固定的 electricity energy series，返回 hour/day/month、质量摘要、watermark、partial 和 dataset revision；明确不负责构造区间、费率、成本、基线或比较模型。 |
| [`energy.go`](../../libs/analyticsmodel/energy.go) | 当前产品合同只允许 `electricity`、`hour/day/month` 和两种质量策略。 |
| [`energy-analytics.ts`](../../apps/hvac-web/src/api/energy-analytics.ts) | UI 通过固定 `/api/v1/analytics/energy-series` 合同查询，不直接访问 Cube 或 Telemetry Query Service。 |
| [`EnergyAnalytics.tsx`](../../apps/hvac-web/src/real/EnergyAnalytics.tsx) | `RealEnergyWorkspace` 使用真实查询结果，并展示 quality、partial、watermark、dataset revision 和当前/比较周期状态。 |
| [`pages/Energy/analytics.ts`](../../apps/hvac-web/src/pages/Energy/analytics.ts) | 旧 Energy 页面仍生成模拟的日、周、月、设备拆分和费用数据；它不是本次首个切片的权威能源 UI。 |
| [`mock.ts`](../../apps/hvac-web/src/api/mock.ts) | `MOCK_KPI` 明确注明 energyToday 等聚合没有遥测来源；这些值不得作为能源领域事实。 |
| [`data-ownership.v1.json`](../../contracts/ownership/data-ownership.v1.json) | 当前声明 energy interval projection、energy series query contract 和 Cube semantic model 的写入/运行时边界；这是现状合同，不等于完整 Energy Content 已完成。 |

## 3. 目标边界

### 3.1 Energy Processing

Energy Processing 是对已接受的遥测历史和已发布的能源内容进行处理的领域能力。它负责：

- 清洗、质量判断和异常原因；
- 从累计读数计算区间电量；
- 按固定时间口径生成可查询的 Energy Aggregate；
- 记录 processing watermark、dataset revision、重算所需的源观察和处理证据。

它不负责定义 Site、Meter、MeterBinding、Tariff 或 Carbon Factor，也不负责设备当前 Presence、Command 执行或 Edge 控制。

### 3.2 Energy Content

Energy Content 是能源业务定义和配置的领域能力。它负责定义和管理：

- Meter 与 MeterBinding；
- Energy Topology、Energy Category/Item；
- Virtual Meter 及其来源；
- 后续的 Tariff、Carbon Factor、Baseline、Plan、Billing、FDD 和 Reporting 内容。

现有 `009a` 已经声明部分 Registry 表，但“表存在”不等于 Energy Content 已有完整的服务 owner、变更 API、查询契约和处理引用。#304 需要继续裁决这些所有权，不在本票据中凭空指定服务名。

### 3.3 与其他领域的边界

| 领域 | 负责什么 | 不负责什么 |
| --- | --- | --- |
| Telemetry Runtime | 接收源观察、当前接受状态、Presence、最新运行时值和运行时质量 | 不把当前值直接当成 Energy Aggregate，不拥有 Meter 业务配置 |
| Telemetry History | 保存可重建的历史观察和其 ingest 证据 | 不定义能源归属、费率或报告 |
| Energy Processing | 生成 Energy Interval Fact、固定聚合、质量和溯源 | 不拥有物理计量身份和控制闭环 |
| Energy Content | 定义 Meter、Binding、Topology、Category、Tariff 等业务内容 | 不承担秒级设备控制或遥测接入 |
| Metric | 计算通用的、带定义和版本的派生事实 | 不因为名称相似就替代 Energy Interval Fact |
| Future Edge | Channel、Process Image、Cycle、Controller/Scheduler、Intent、Readback、Evidence 和 Timedata seam | 不进入当前 Backend/UI 首个能源切片 |

## 4. 统一词汇

以下定义是本项目后续文档和代码评审的唯一语言。它们描述领域语义，不要求每个词马上对应一个物理表。

| 词汇 | 定义 | 当前证据/状态 |
| --- | --- | --- |
| Raw Observation | Telemetry History 保存的、带 sampled/received/quality/ingest 证据的源观察；它是能源处理的输入，不是清洗后的电量事实。 | 当前已有 `telemetry_history.observations` 链路；KEEP |
| Current | Telemetry Runtime 在某个 key 上最新接受的当前运行时真相。它用于运行态读取，不能代替完整历史区间。 | `CONTEXT.md` 已定义；KEEP |
| Energy Processing | 对 Raw/Current 和 Energy Content 输入进行清洗、区间化、归一化、聚合和重算的处理边界。 | 现有 projector 是其第一个局部实现；ADOPT/LOCAL-CHANGE |
| Energy Content | Meter、MeterBinding、Topology、Category/Item、Tariff、Carbon 等能源业务定义和配置。 | Registry 有部分表，完整 owner 尚未裁决；ADOPT/DEFER |
| Meter | 物理计量身份。当前 Registry 的 `energy_meters` 以 Site、Device、Energy Type、code 和生命周期状态定义它。 | 已有 SQL 证据；KEEP |
| MeterBinding | Meter 到一个 accounting edge 和一个 `COUNTER` Point 的有版本、有效期、方向、角色绑定。 | 已有 SQL 证据；KEEP，必须被处理链真正使用 |
| Energy Interval Fact | 两个有序累计观察之间的增量电量事实，带区间、质量、质量原因、源观察 ID、源位置和处理元数据。 | 当前 projector/ClickHouse 已实现 electricity 版本；KEEP AS FIRST SLICE |
| Energy Aggregate | 按 Site、Meter/Binding 语义、Energy Type、时区和固定粒度汇总的可查询结果。它是事实的派生读取结果，不是用户随意编辑的 Meter 内容。 | 当前查询服务直接从 interval facts 做固定聚合；KEEP AS READ MODEL，后续补 Meter 维度 |
| Dataset Revision | 让消费者知道本次结果基于哪一版查询数据集和事实修订的标识。当前实现形如 `<QUERY_DATASET_REVISION>:<maximum fact revision>`。 | 当前查询 README 已明确；KEEP，但不与 Meter 配置 revision 混用 |
| Quality | 对 Observation 或 Energy Fact 的可用性/可信度分类。Telemetry 的 `GOOD/PARTIAL/ESTIMATED/MANUAL/STALE/INVALID` 与 Energy Fact 的 `VALID/SUSPECT/INVALID` 是不同层级，不能合并成一个枚举。 | `CONTEXT.md`、projector 和查询合同均有证据；KEEP |
| Provenance | 说明结果由什么产生的证据，包括源观察 ID、source offset、sampled interval、watermark、projectedAt 和 dataset revision。 | 当前 fact 与 response 已部分实现；KEEP/EXPAND |
| Metric | 按显式定义、单位、质量策略和溯源计算的通用版本化派生事实。Energy Interval Fact 不因“派生”二字自动变成 Metric。 | `CONTEXT.md`、`009c` 有证据；KEEP AS SEPARATE BOUNDARY |

## 5. 首个纵向切片

### 5.1 产品目标

首个切片锁定为：

> 在一个 Tenant 下的一个 Site，针对一个有效的 PRIMARY 累计电量 Meter、一个与其绑定的 `COUNTER` Point，生成可解释的 electricity interval facts，并向 UI 提供按站点时区查询的 hour/day/month 能耗序列。

这里的“一块电表”不是把 `hvac_meter.energy` 这个字符串重新命名成 Meter。它必须最终能够落到 Registry 的 Meter 和 MeterBinding 语义上。当前投影尚未携带 `meter_id`、`meter_binding_id` 或绑定版本，所以现有结果只能称为“Site 范围的 electricity point-key slice”，不能宣称已经完成计量绑定正确性。

### 5.2 切片链路

```text
Telemetry History observations
    -> Energy Processing: cumulative reading -> interval fact
    -> Energy Processing: quality / reasons / provenance
    -> fixed hour/day/month Energy Aggregate query
    -> Platform Gateway
    -> RealEnergyWorkspace UI
```

逻辑阶段继续采用：

```text
Raw -> Current -> Normalized -> Aggregate -> Report
```

这五个词是语义阶段，不要求创建五张表：

- `Raw`：历史源观察；
- `Current`：运行时当前接受值；
- `Normalized`：累计读数转换后的 Energy Interval Fact；
- `Aggregate`：固定时区/粒度的能耗查询结果；
- `Report`：本切片暂不实现，等待 Reporting 内容和导出作业的独立裁决。

### 5.3 首个切片明确包含

- Tenant/Site 授权边界；
- 一个有效 PRIMARY cumulative electricity Meter 及其 `MeterBinding` 语义；
- `hvac_meter.energy` 当前实现对应的累计读数转换；
- 区间电量、`VALID/SUSPECT/INVALID`、质量原因和源观察溯源；
- hour/day/month 固定查询合同；
- `dataWatermark`、`aggregateWatermark`、`datasetRevision`、`partial` 和质量摘要；
- `RealEnergyWorkspace` 的当前周期、比较周期、质量口径和权威边界展示。

### 5.4 首个切片明确不包含

- 多块电表合并、Virtual Meter、Energy Topology 复杂流量平衡；
- Space/Asset/Equipment 分摊和设备能耗拆分；
- Water、Gas、Cooling 或其他 Energy Type；
- Tariff、Cost、Carbon、Baseline、Plan、Billing、FDD、Prediction 和正式 Report；
- 任意 Cube member、任意 SQL、自由 Rule Chain 或自由 Dashboard 作为能源事实来源；
- OpenEMS Edge、控制 Intent、Cycle/Scheduler、Readback 或现场证据闭环。

## 6. 对当前 HVAC 的直接裁决

| 当前假设 | 源码证据 | 裁决 |
| --- | --- | --- |
| “Energy”已经可以标记为完整 ALIGNED | 当前只有 electricity interval projector 和固定查询；MyEMS 的 Meter/Normalization/Aggregation/Billing/Report 语义尚未闭合 | `LOCAL-CHANGE`：Energy 拆为 Energy Processing slice 与 Energy Content，不能继续用一个 ALIGNED 标签覆盖二者 |
| `hvac_meter.energy` 就是 Meter | 当前 projector 只校验 telemetry key；事实表没有 meter/binding 标识 | `REJECT` 该命名替代；后续必须把 Registry MeterBinding 作为可验证输入 |
| 旧 Energy 页面展示的设备/费用/趋势可以作为产品事实 | 本地 `pages/Energy/analytics.ts` 和 `mock.ts` 明确生成模拟数据 | `REJECT AS AUTHORITY`；首个切片 UI 以 `real/EnergyAnalytics.tsx` 为准，旧页面只能在后续重构时删除或改造成真实合同消费者 |
| Energy Aggregate 等于单独的业务实体 | 当前查询服务从 interval facts 做固定聚合，没有独立 Aggregate 内容 owner | `KEEP AS READ MODEL`；不为当前切片新增可编辑 Aggregate 实体 |
| Dataset Revision 等于 Meter/Topology revision | 当前 response 使用查询数据集前缀和事实最大 revision | `REJECT` 混用；配置 revision、事实 revision、查询数据集 revision 分开 |
| Edge 语义属于当前 Backend 能源处理 | OpenEMS 源码的 Channel/Process Image/Cycle/Scheduler/Timedata 都是 Edge/控制语义 | `DEFER`；当前仅保留未来接口 seam |

## 7. 后续依赖

本票据只冻结目标和语言，不假设后续模块已经存在。下一阶段需要按以下问题继续源码和本地合同核对：

1. #305 已确定 Raw/Current/Normalized/Aggregate 的数据架构、Counter 语义复用、重算边界和质量/溯源规则，详见 [`energy-data-lifecycle-v1.md`](energy-data-lifecycle-v1.md)。
2. #306 已冻结 canonical `counter_deltas` 到 Energy Interval Fact 的最小事实字段、MeterBinding released read、质量、transition 和 provenance 规则，详见 [`energy-fact-meter-binding-contract-v1.md`](energy-fact-meter-binding-contract-v1.md)。
3. #307 已冻结 Backend Energy Series 与 Energy Content 的查询主体、职责、路由、授权、分页、时间和错误语义，详见 [`energy-content-query-contract-v1.md`](energy-content-query-contract-v1.md)；
4. #308 已冻结 Operations、Energy Management、Administration 的 UI 信息架构、Site/Space/Asset 上下文、真实状态语言和事实/建议/待审批动作边界，详见 [`ui-workspace-information-architecture-v1.md`](ui-workspace-information-architecture-v1.md)；
5. #310 已把首个 electricity slice 的 canonical Counter read contract、MeterBinding event-time resolver、Fact provenance schema、Projector seam、单 writer 运行边界和最小验收门禁落实为实施规格；rebuild generation、supersede/tombstone、并发与失败恢复仍单独后置。

## 8. 本轮结论

- `Energy Processing` 是当前首个可落地切片的处理边界；`Energy Content` 是后续必须补齐的业务内容边界。
- 首个产品切片锁定为“单站点、单一有效 PRIMARY 累计电量 Meter、单一绑定 COUNTER Point、electricity interval series”。
- 当前 HVAC 已有的 projector/query/UI 可以作为起点，但只有 `RealEnergyWorkspace` 的真实数据路径可作为本切片 UI 依据；模拟 Energy 页面不得继续充当事实来源。
- 当前事实投影还没有完成 MeterBinding 解析，因此后续实现必须补足这条证据链；在补足前不把 Energy 宣称为完整对齐。
- 本轮只更新架构决策文档，不修改 Backend、UI、数据库、部署配置或测试代码。
