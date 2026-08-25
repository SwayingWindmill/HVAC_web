# Wayfinder #305：能源数据链与事实生命周期

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：Raw、Current、Normalized、Aggregate、Report 的真实边界，以及累计电量事实的质量、乱序、重复、回退、水位、版本和重算语义。不实现 Edge、部署变更或本票据之外的数据库迁移。

## 1. 决策结论

能源数据不是一条强制的 `Raw → Current → Normalized → Aggregate → Report` 单线流水线。

- `Raw` 是可追溯的历史观察事实；它是能源处理的输入。
- `Current/Latest` 是 Telemetry Runtime 的实时投影，服务当前状态、表计健康度和实时界面，不作为历史 Counter 增量的输入。
- `Normalized` 在本项目不是一张已经独立存在的表。其规范计算语义由 `telemetry_history.counter_deltas` 提供，产品化的能源区间事实由 `analytics.energy_interval_facts` 承载。
- `Aggregate` 是从能源区间事实或规范 Counter delta 得出的可重建读模型。当前 hour/day/month 查询由 Cube 对 `energy_interval_facts` 做固定聚合；不是 Report 的事实源。
- `Report` 当前没有能源事实 owner 或实现。未来只能消费稳定的 Aggregate/Query 合同，不能反向定义 Raw 或 Interval Fact。

因此，当前应采用两条并行关系：

```text
accepted/out-of-order history observations
        ├──> Current/Latest（实时状态支线）
        └──> Counter normalization → Energy Interval Fact → Aggregate/Query → Report
```

Metric 也是并行消费者，不是 Energy Interval Fact 的替代品；它继续由 Metric Engine 按自己的 Binding、Run 和 Result Revision 生命周期管理。

## 2. 证据基线

三方固定版本和完整源码清单见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)：

| 项目 | 固定版本 | 完整提交 | 本票据吸收的源码结论 |
| --- | --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` | Latest 与 History 是不同读写关注点；不把实时最新值当作历史时间序列事实。 |
| OpenEMS | 2026.7.0 | `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | Timedata 是历史数据面，但其 Edge Channel/Cycle/Controller 语义不进入本阶段 Backend。 |
| MyEMS | v6.7.0 | `be6e6ce8ddeac57afb04bddb9621501fb555cab0` | Cleaning、Normalization、Aggregation 分层，能源内容与处理结果分开；按职责吸收，不复制其数据库数量。 |

本票据实际复核的 HVAC 生产源码、DDL、README 和测试包括：

| 文件 | 事实证据 |
| --- | --- |
| [`001-telemetry-history.sql`](../../infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql) | `telemetry_history.observations` 保存 observation identity、source position、sample/receive time、acceptance、quality、Point revision、Counter 语义和 payload digest；accepted scope 必须具备 Tenant/Site/Device/Point。 |
| [`004-counter-semantics.sql`](../../infra/s2-telemetry/clickhouse/init/004-counter-semantics.sql) | Counter delta 按历史 event-time 计算，不读取 Latest；`ACCEPTED` 与 `OUT_OF_ORDER` 都参与；revision/unit boundary 不产生 delta，并显式区分 invalid decrease、recovery、reset、rollover。 |
| [`003-telemetry-rollups.sql`](../../infra/s2-telemetry/clickhouse/init/003-telemetry-rollups.sql) | Raw 是 rollup 的 authority，rollup 可重建；非 Counter 有 1 分钟/15 分钟 rollup；没有不带 Site timezone 的 canonical daily rollup。 |
| [`history_clickhouse.go`](../../services/telemetry-runtime-service/internal/telemetry/history_clickhouse.go) | History 以 observation ID 作为 ClickHouse insert deduplication token；已解析的 `OUT_OF_ORDER` 观察也可落库。 |
| [`ingest.go`](../../services/telemetry-runtime-service/internal/telemetry/ingest.go) | Acceptance status 与 observation quality 分离；Point binding 快照包含 Point revision、unit、decrease mode 和 rollover modulus。 |
| [`analytics-read-model-projector/README.md`](../../services/analytics-read-model-projector/README.md) | 当前 Projector 只做 `hvac_meter.energy` 的累计电量区间事实；当前 observation ID 是 fact identity，source offset 被当作 numeric dataset revision，current sampled time 是 data watermark；没有历史修正和显式 rebuild。 |
| [`analytics-read-model-projector/internal/clickhouse/client.go`](../../services/analytics-read-model-projector/internal/clickhouse/client.go) | 当前候选扫描自己按 Tenant/Site/Point/Sensor/Device/Telemetry Key 分区，筛选 `ACCEPTED`，再 anti-join 已有 fact；没有读取 `counter_deltas`、MeterBinding 或 durable projector checkpoint。 |
| [`002-analytics-energy-interval.sql`](../../infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql) | 当前 Interval Fact 有源 observation、offset、dataset revision、watermark 和 projected time，但没有 Meter、MeterBinding、binding version、transition type 或 source Point revision/unit snapshot。 |
| [`telemetry-query-service/README.md`](../../services/telemetry-query-service/README.md) | Query 返回 data/aggregate watermark、dataset revision、partial 和 quality counts；aggregate watermark 当前等于直接 Fact 聚合的 data watermark；缓存刷新时间不是 data watermark。 |
| [`metric/engine.go`](../../services/metric-engine-service/internal/metric/engine.go) | Metric 是独立的 Binding/Input/Run/Publication/Result 生命周期；结果重算会推进 revision，Redis Latest 只是可重建投影。 |

## 3. 五层边界与 owner

| 层 | 当前权威对象 | 逻辑 owner | 允许的事实含义 | 当前状态 |
| --- | --- | --- | --- | --- |
| Raw | `telemetry_history.observations` | Telemetry Runtime / History Projector | 已持久化观察、来源位置、event-time、接受状态、质量和 Point/Counter 语义快照 | 已有；作为能源处理的输入事实 |
| Current | Runtime current / Latest / Presence 投影 | Telemetry Runtime | 最新可用状态、实时值、表计新鲜度和健康度 | 已有；与历史事实并行，不能作为 Counter delta 输入 |
| Normalized | `counter_deltas` 规范视图 + `analytics.energy_interval_facts` 产品事实 | Energy Processing | 按 event-time 和计数器语义得出的可解释区间电量 | 语义已有，Projector 接入方式不合规 |
| Aggregate | Counter rollup views、Cube Energy semantic model、固定 Energy Query | Energy Processing；Query Service 负责适配 | 按 Site timezone 和产品粒度汇总的可查询读模型 | 部分已有；不是独立 Report 数据库 |
| Report | 尚无权威事实表或服务 | 尚未实现的 Reporting 能力 | 报表、导出、账单/碳排等消费结果 | Not yet specified，不提前设计存储 |

### 3.1 Raw 不是“未经校验的任意输入”

`Raw` 在当前实现中指 History 中保存的观察记录，而不是绕过 Runtime 的外部 payload。Rejected、Quarantined、Duplicate 等状态可以作为审计记录存在，但能源 Normalization 只消费具备完整租户/site/device/point scope 且状态属于 `ACCEPTED` 或 `OUT_OF_ORDER` 的历史观察。

这保留了两种不同事实：

1. acceptance 决定观察能否进入历史处理；
2. quality 决定这条已经进入处理范围的观察能否、以何种质量参与能源事实。

不能用“把无效值写成 0”替代质量和排除原因。

### 3.2 Current 与历史计算解耦

`004-counter-semantics.sql` 明确写明 Latest 不参与 Counter delta 计算。历史增量必须从 Raw observations 按 event-time 重新排序，不能拿当前值和上一条当前缓存值相减。

Current 仍然有明确用途：实时运营页面、表计在线/新鲜度判断、当前读数展示和 Runtime 的 Latest 查询。它不是 Raw 的替代存储，也不是能源重算的 checkpoint。

## 4. Counter Normalization 规范

### 4.1 输入顺序与分区

规范输入是 `telemetry_history.counter_deltas`，其源码规则为：

- 只处理 `ACCEPTED`、`OUT_OF_ORDER`、`COUNTER`、数值有效且具有 Tenant/Site/Device/Point 的观察；
- 按 `tenant_id + site_id + point_id` 分区，按 `sampled_at + observation_id` 排序；
- 不按当前 Projector 的 `device_id + telemetry_key` 窗口重新定义 Point 的历史边界；
- Point revision 变化和 unit 变化都是边界，不跨边界计算 delta；
- `Latest` 不参与，`source_offset` 只能作为来源溯源，不能替代 event-time 排序。

### 4.2 转换结果

| transition | delta | 处理结论 |
| --- | --- | --- |
| `INITIAL` | 空 | 不生成区间能耗 |
| `REVISION_BOUNDARY` | 空 | 不跨 Point revision 计算 |
| `UNIT_BOUNDARY` | 空 | 不跨 unit 计算 |
| `INCREASE` / `UNCHANGED` | 当前值 - 前值 | 生成正常 delta；unchanged 的 delta 为 0 |
| `RECOVERY` | 当前值 - 当前 revision/unit 下历史最大值 | 生成恢复 delta |
| `RESET` | 当前值 | 只有 Point 的 `RESET_TO_ZERO` 语义才成立 |
| `ROLLOVER` | modulus - 前值 + 当前值 | 只有有效 modulus 和 `ROLLOVER` 语义才成立 |
| `INVALID_DECREASE` | 空 | 排除该过渡并保留 exclusion evidence |

能源区间事实必须能追溯到前后 observation、transition、quality 和原因。把所有回退都写成 `0 + SUSPECT` 是当前 Projector 的局部实现，不是规范语义；这项行为需要后续重构，不能通过兼容分支继续保留两套规则。

### 4.3 MeterBinding 必须进入事实身份和溯源

`009a-energy-topology-metering-v2.sql` 已经定义 Meter、MeterBinding、角色、方向和有效期，但当前 Interval Fact 仍只有 Point/Device/Telemetry Key。

目标 Normalized Fact 至少要能够回答：

- 这段电量属于哪个 Meter；
- 使用了哪个 MeterBinding 及其 released version；
- 事件时间落在哪个 binding 有效期；
- 使用了哪个 Point revision、unit 和 Counter decrease/rollover 语义；
- 事实由哪两个 observation 产生，或因哪个 transition 被排除。

因此不能继续用 `hvac_meter.energy` 这个 telemetry key 代替 MeterBinding。现有 `energy_interval_facts` 的字段不足是 LOCAL-CHANGE，后续实现应删除这种隐式绑定，而不是增加兼容字段让两种身份长期并存。

## 5. 重复、乱序、晚到和修正

### 5.1 重复

当前代码只证明了有限范围内的幂等：

- History insert 使用 observation ID deduplication token；
- Energy Projector 以 current observation ID anti-join，并使用批次 SHA-256 token；
- ClickHouse dedup 依赖配置的 deduplication window。

这不是 durable projector checkpoint，也不是无限期的重放保证。目标合同应继续以 observation/fact identity 保证单事实不重复，但不能把当前 anti-join 宣称成完整历史修正机制。

### 5.2 乱序和晚到

Runtime/History 已有 `OUT_OF_ORDER` 状态，canonical Counter view 也明确纳入 `OUT_OF_ORDER`。因此乱序不是把新值直接接到 Current 后面，而是让历史 Normalization 按 event-time 重新得到受影响的过渡。

当前 Projector 只筛 `ACCEPTED`，且没有 durable checkpoint 或 rebuild，因此它无法完整处理已落库的晚到观察对既有 Interval Fact 的影响。这是 LOCAL-CHANGE：后续 Projector 必须以 canonical Counter read seam 为准，不能继续维护第二套窗口算法。

### 5.3 历史修正与重算

现有源码和测试证明了：

- History 是 Raw authority，rollup 可重建；
- Metric 有明确的 Run/Publication/Result Revision 重算生命周期；
- Energy Projector 当前没有历史修正、rebuild job、checkpoint 或 fact supersession 机制。

本票据只锁定边界，不虚构尚未被源码证明的替换策略：

- 晚到数据、MeterBinding 有效期变化、Counter 语义变化或质量规则变化，必须走显式 Energy rebuild/revision 流程；
- 不允许通过 Current、静默补写或再次运行普通增量 projector 隐式“修正”历史；
- rebuild 的 generation identity、旧事实的 supersede/tombstone 语义、并发执行和失败恢复仍是 Not yet specified，需在能明确提出问题后单独立票据。

## 6. Watermark、Dataset Revision 和 Partial

当前 Query 合同的含义固定如下：

| 字段 | 当前含义 | 不能解释成 |
| --- | --- | --- |
| `dataWatermark` | 返回事实中最大的 event-time `data_watermark`，当前由 current observation sampled time 产生 | projector 完成时间、缓存刷新时间或 source checkpoint |
| `aggregateWatermark` | 当前直接从 Interval Fact 聚合时与 data watermark 相同 | 证明请求范围没有内部缺口 |
| `datasetRevision` | `<QUERY_DATASET_REVISION>:<maximum fact revision>`；当前 fact revision 来自 source offset | 完整 rebuild generation 或版本化快照 |
| `partial` | watermark 未覆盖请求结束时间，或请求的本地 hour/day/month bucket 缺少事实行 | 仅由“有一条最新数据”推断完整覆盖 |
| `projected_at` | 处理时间审计字段 | 数据覆盖水位 |

Site 的 day/month 必须使用 Registry 的 Site timezone；`003-telemetry-rollups.sql` 明确拒绝用 UTC `toStartOfDay` 充当 canonical business day。当前 README 中的 `period_end` 归桶、不跨桶 prorate 也继续保留为第一版 Energy Interval Fact 合同。

当前 `dataset_revision = source_offset` 只能作为来源顺序/事实 revision。它不能承载“整站已按某个版本重算完成”的语义；在真正的 rebuild generation 定义前，UI 只能展示当前合同含义，不能把它包装成数据集快照版本。

## 7. 对三方参考项目的吸收裁决

| 参考机制 | 裁决 | HVAC 取舍 |
| --- | --- | --- |
| ThingsBoard Latest 与 History 分离 | ADOPT | Current/Latest 和历史事实分开；不复制其通用 Telemetry runtime。 |
| MyEMS Cleaning → Normalization → Aggregation | ADAPT | 采用职责分层，但以 History、Counter canonical view、Energy Fact 和固定 Query 合同落地；不复制 MyEMS 多数据库布局。 |
| OpenEMS Timedata 历史数据面 | ADAPT LATER | 认可历史数据与实时控制面分离；当前不引入 Edge Channel、Cycle、Controller 或 Timedata runtime。 |
| 三方的通用规则/调度/控制运行时 | REJECT for this slice | 当前 Backend/UI 只锁定数据事实和查询边界，Edge 与控制编排不进入本票据。 |

## 8. 当前必须修改的地方

这些不是“以后可以优化”的偏好，而是源码证据已经证明的架构偏差：

1. Energy Projector 必须停止自己按 `device + telemetry_key` 重新实现 Counter delta，改为消费 `counter_deltas` 或等价的已发布 canonical read contract。
2. Projector 的输入范围必须与 canonical Counter view 一致，不能只处理 `ACCEPTED` 而遗漏 `OUT_OF_ORDER`。
3. 回退、reset、rollover、revision/unit boundary 和 invalid decrease 必须使用同一套 transition 语义；不能同时维护“全部回退写 0 SUSPECT”和 canonical Counter 规则。
4. Interval Fact 必须补足 MeterBinding released version 及 Point/Counter provenance；不能继续由固定 telemetry key 隐式代表能源计量身份。
5. 任何历史 correction/rebuild 在合同明确前不得伪装成普通增量投影；当前实现应把“尚未支持”保持为明确限制。

## 9. 下一张票的边界

本票据已经回答数据链和生命周期，但没有批准实现迁移。#306 已进一步冻结了首个 Fact、MeterBinding、质量和溯源契约。下一阶段可从以下问题中选择一个单独立票据：

- 按 [`energy-fact-meter-binding-contract-v1.md`](energy-fact-meter-binding-contract-v1.md) 编写 canonical `counter_deltas` 到 Energy Interval Fact 的实现规格和验收门禁；
- Energy Content released read API 已由 #307 冻结，见 [`energy-content-query-contract-v1.md`](energy-content-query-contract-v1.md)；
- rebuild generation、fact supersession 和并发恢复语义；
- Energy Query 的 Aggregate/Report 读模型以及 UI 对 partial、quality、watermark 的呈现。

这些问题不能在未补齐对应源码/合同证据前提前合并成一个“大而全”的 Energy Processing 服务。
