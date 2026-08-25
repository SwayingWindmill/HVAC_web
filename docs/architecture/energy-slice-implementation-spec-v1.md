# Wayfinder #310：Energy Slice v1 实施规格与验收门禁

状态：IMPLEMENTATION-READY / SOURCE-BACKED  
审查日期：2026-08-25  
范围：首个 electricity Energy Slice 的 Backend 实施顺序、物理数据调整、失败语义、权限边界和最小验收门禁。不实现代码、数据库迁移、部署拓扑或 Edge。

## 1. 本票据要交给实现会话什么

首个可交付切片固定为：

- 一个 Tenant 下的一个 Site；
- 一个有效的 `PRIMARY` electricity Meter；
- 一个 `COUNTER` Point；
- 从 History observation 计算 additive interval facts；
- 通过现有 Energy Series 查询合同返回 hour/day/month 结果；
- UI 能显示真实的质量、水位、partial 和 dataset revision。

本票据不把当前已经存在的 `analytics.energy_interval_facts` 当作正确性证明。实现会话必须先删除当前 Projector 的第二套 Counter 算法，再接入 canonical Counter read contract 和已发布 MeterBinding。

首个切片不包含：

- rebuild generation、旧事实 supersede/tombstone、历史修正任务和多写入者并发；
- virtual/offline Meter、Space/Asset 分摊、Tariff、Billing、Carbon、Baseline、Forecast、Optimization、Report；
- Edge runtime、现场协议、离线回传和闭环控制；
- 为了兼容现有错误行为而保留的 telemetry-key 推断、`SUSPECT` Raw quality 或“回退写 0”分支。

## 2. 已核对的源码证据

### 2.1 HVAC 当前实现

| 文件 | 实际观察 | 对实现的约束 |
| --- | --- | --- |
| [`001-telemetry-history.sql`](../../infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql) | History 保存 observation identity、Tenant/Site/Device/Point、source event/partition/offset、sampled/received time、acceptance、quality、Point revision、Counter semantics 和 payload digest。 | Energy Processing 只能从 History 读取，不能从 Current/Latest 猜测前值。 |
| [`004-counter-semantics.sql`](../../infra/s2-telemetry/clickhouse/init/004-counter-semantics.sql) | `counter_deltas` 按 Tenant/Site/Point 的 event-time 顺序计算；包含 `ACCEPTED` 与 `OUT_OF_ORDER`；明确区分 `INITIAL`、`INCREASE`、`RECOVERY`、`RESET`、`ROLLOVER`、revision/unit boundary 和 `INVALID_DECREASE`。 | 这是唯一的 Counter 增量语义入口；Projector 不得再次在 observations 上实现 `lag` 和回退规则。 |
| [`002-analytics-energy-interval.sql`](../../infra/s2-telemetry/clickhouse/init/002-analytics-energy-interval.sql) | Fact 目前只有 Point/Device/Telemetry Key、区间、质量、两个 observation ID、单一 source offset、dataset revision 和 watermark；没有 Meter/Binding/Topology/version、transition、source partition/event 或 Point/Counter snapshot。 | 必须做一次明确的 Fact provenance schema 变更；不能通过 telemetry key 继续隐式绑定 Meter。 |
| [`projector.go`](../../services/analytics-read-model-projector/internal/energy/projector.go) | `BuildFact` 自己计算当前值减前值，把负累计值/回退写成 `0`，并把 `source_offset` 复制成 dataset revision；Raw quality 使用未定义的 `GOOD/SUSPECT`。 | 删除该计算路径；Fact builder 只接受 canonical delta 和 Resolver snapshot。 |
| [`client.go`](../../services/analytics-read-model-projector/internal/clickhouse/client.go) | Reader 自己按 `Tenant/Site/Point/Sensor/Device/Telemetry Key` 窗口，只筛 `ACCEPTED`，再 anti-join Fact。 | 改为读取 `counter_deltas`；纳入 `OUT_OF_ORDER`，并以 `(tenant, site, binding, current observation)` 作为业务幂等键。 |
| [`projector_test.go`](../../services/analytics-read-model-projector/internal/energy/projector_test.go) | 现有测试保护的是旧的 rollback/negative-to-zero 行为，并没有 Binding、canonical transition 或完整 Source Position。 | 更新测试契约；删除旧行为断言，不添加兼容测试。 |
| [`aggregate.go`](../../services/telemetry-query-service/internal/history/aggregate.go) | History aggregate 已有 Counter 的 reset、rollover、revision/unit boundary、quality policy 和 event-time 聚合处理。 | Energy Fact Query 与 History aggregate 不得各自发明第三套 Counter 语义。 |
| [`009a-energy-topology-metering-v2.sql`](../../infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql) | `energy_meters` 是物理 Meter；`meter_bindings` 保存 topology、edge、energy type、meter、device、point、role、direction、priority、effective interval、version、status、revision，并有 released/active PRIMARY overlap 约束。 | Processing 按 observation `sampled_at` 解析已发布 Binding，使用半开区间，保存解析快照。 |
| [`history_clickhouse.go`](../../services/telemetry-runtime-service/internal/telemetry/history_clickhouse.go) | History insert 以 observation ID 做 ClickHouse deduplication token，解析的 `OUT_OF_ORDER` observation 也可落库。 | Projector 不能把 `OUT_OF_ORDER` 当作不可处理的输入。 |

### 2.2 三个固定参考项目

固定版本和完整源码清单见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)。本票据额外复核了以下生产源码：

| 项目和固定提交 | 实际核对源码 | 结论 |
| --- | --- | --- |
| ThingsBoard CE v4.3.1.1，`c2a52e46c44e308ddee430e7266b8e10eddde9c4` | [`TimeseriesDao.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/timeseries/TimeseriesDao.java) 及报告中固定版本的 telemetry/history、Rule Engine、Dashboard subscription 源码和测试 | **ADAPT**：时序存取、Latest/History 分离和实时编排是平台能力；**REJECT**：用通用 Telemetry/Rule Engine 代替能源 Counter、MeterBinding 和质量事实。 |
| OpenEMS 2026.7.0，`2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | `Channel.java`、`Value.java`、`CycleWorker.java`、`Timedata.java`、`TimedataManagerImpl.java`、`ResendHistoricDataWorker.java` 及对应 UI/测试；本地 `E:\Code\openems` checkout 与官方固定提交对应源码已实际查看 | **ADAPT**：typed value、unit、UNDEFINED 和历史数据 provider 边界；**DEFER**：Edge Cycle、Controller、Scheduler、resend runtime。 |
| MyEMS v6.7.0，`be6e6ce8ddeac57afb04bddb9621501fb555cab0` | [`clean_energy_value.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-cleaning/clean_energy_value.py)、[`meter.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/meter.py)、[`main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/main.py)、[`meter_billing.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/meter_billing.py)、database schema/README 和已有处理测试 | **ADOPT/ADAPT**：Cleaning → Normalization → Aggregation 的职责顺序、Meter 配置和增量处理；**REJECT**：13 个物理数据库、每个对象复制一套 worker、用 0 填充缺失/坏数据的隐式语义。 |

三方证据说明了“职责分层”，没有证明 HVAC 应复制任何一个项目的具体实现。实现以 HVAC 的固定 Counter/Registry/Query 合同为准。

## 3. 目标数据流和 owner

```text
telemetry_history.observations
        │
        ▼
telemetry_history.counter_deltas
        │  canonical transition + delta + current source position
        │
        ├──> Core private Binding Resolver
        │       (released/active MeterBinding, event-time match)
        │
        ▼
Energy Fact Builder
        │  quality + binding snapshot + source provenance
        ▼
analytics.energy_interval_facts
        │
        ▼
Telemetry Query Service / Energy Series contract
        │
        ▼
Platform Gateway → UI Energy Management
```

各段 owner 固定如下：

| 能力 | owner | 不允许做什么 |
| --- | --- | --- |
| History observation | Telemetry Runtime / History Projector | 不生成 Meter 归属或 Energy Fact。 |
| Counter delta | Energy Processing 使用 S2 canonical view | 不读取 Current/Latest；不在 Go 中复制窗口算法。 |
| Meter/MeterBinding/Topology | S1 Registry / Platform Core | 不计算电量、不读 ClickHouse。 |
| Fact | Energy Processing | 不由 Query Service 或 UI 现场生成。 |
| Energy Series | Telemetry Query Service 固定查询适配 | 不读取 Registry 表补全 MeterBinding，不暴露任意 SQL/Cube。 |
| Public access | Platform Gateway | 不把浏览器 session、Tenant/Site header 直接转发给 Core/ClickHouse。 |

## 4. 首个 Slice 的精确实现规格

### 4.1 Canonical Counter read contract

实现前先为 `telemetry_history.counter_deltas` 增加一个明确的内部 read contract，不能只依赖当前 view 的列碰巧够用。它必须返回：

| 组 | 字段 |
| --- | --- |
| Scope/source identity | `tenant_id`、`site_id`、当前 `device_id`、`point_id`、`sensor_id`、当前 `telemetry_key` |
| Counter semantics | `point_revision`、`unit`、`counter_decrease_mode`、`counter_rollover_modulus` |
| Current observation | `observation_id`、`sampled_at`、`received_at`、`quality`、`quality_reasons`、`source_event_id`、`source_partition`、`source_offset` |
| Previous observation | `previous_observation_id`、`previous_value`、`previous_sampled_at`、`previous_quality`、`previous_quality_reasons` |
| Calculated result | `transition_type`、`delta_value` |

实现方式可以是扩展 `counter_deltas` 的输出，或在其上建立只补充 provenance 的 wrapper view；不得重新从 `observations` 写另一套 `lag`、reset、rollover、recovery 公式。扩展时必须保持现有 canonical 规则：

- 分区是 `tenant_id + site_id + point_id`；排序是 `sampled_at + observation_id`；
- `ACCEPTED` 和 `OUT_OF_ORDER` 都参与；
- revision/unit boundary 不跨越计算；
- `INVALID_DECREASE`、`INITIAL` 等 `delta_value IS NULL` 的行不生成 Fact；
- `INCREASE`、`UNCHANGED`、`RECOVERY`、`RESET`、`ROLLOVER` 才进入 Fact Builder；
- `RESET`、`ROLLOVER` 的合法性只能由 Raw 中快照的 Counter semantics 决定。

当前 view 没有 previous observation ID 和完整 current Source Position；这不是由 Go adapter 猜测，而是本次 S2 read-contract/schema 变更必须补齐的内容。

### 4.2 MeterBinding event-time resolution

每一个有非空 canonical delta 的 current observation 都要经过 `meter-binding.resolve` 私有能力。输入固定为：

```text
tenant_id, site_id, device_id, point_id, sampled_at
```

Core 只返回已发布内容的快照。命中条件固定为：

```text
effective_from <= sampled_at
AND (effective_to IS NULL OR sampled_at < effective_to)
AND status IN ('RELEASED', 'ACTIVE')
AND point_type = 'COUNTER'
AND meter_role = 'PRIMARY'
AND energy_type = 'electricity'
AND binding.device_id = observation.device_id
AND binding.point_id = observation.point_id
```

结果只有三种：

| 结果 | Projector 行为 |
| --- | --- |
| `MATCH`，恰好一条 | 使用返回的 Meter/Binding/Topology/version/role/direction 快照继续生成 Fact。 |
| `NO_MATCH` | 不生成 Fact、不写 0；作为配置前置条件失败返回本批错误，下一次仍可在 Binding 修复后重新评估。 |
| `AMBIGUOUS` | 不生成 Fact、不选择“最新”或按 `updated_at` 猜一条；作为不可接受的 Registry 发布冲突返回本批错误。 |

首个 Slice 暂不引入独立的 processing checkpoint/exclusion table。因为当前目标只有一个站点和一个 PRIMARY Binding，遇到无归属/歧义时 fail-fast 比静默吞掉更安全；后续扩大到多站点或引入历史修正前，必须单独裁决 durable outcome、重试和 rebuild generation，不能把当前轮询重复当作完整恢复机制。

Resolver 返回的 `revision`、`version` 和 topology identity 必须与 Fact 一起写入。Fact 生成后不再读取当前 Registry 行来补字段。

### 4.3 Fact logical contract

Fact 的逻辑身份和字段采用 #306，不因为当前物理表缺字段而删减：

```json
{
  "fact_id": "source_current_observation_id",
  "tenant_id": "...",
  "site_id": "...",
  "meter_id": "...",
  "meter_binding_id": "...",
  "topology_version_id": "...",
  "binding_version": 1,
  "energy_type_id": "...",
  "energy_type": "electricity",
  "meter_role": "PRIMARY",
  "direction": "IMPORT",
  "device_id": "...",
  "point_id": "...",
  "sensor_id": "...",
  "telemetry_key": "source-only-label",
  "point_revision": 3,
  "unit": "kWh",
  "counter_decrease_mode": "RESET_TO_ZERO",
  "counter_rollover_modulus": null,
  "source_previous_observation_id": "...",
  "source_current_observation_id": "...",
  "source_event_id": "...",
  "source_partition": "...",
  "source_offset": 123,
  "period_start": "...",
  "period_end": "...",
  "energy_kwh": 2.75,
  "transition_type": "INCREASE",
  "quality": "VALID",
  "quality_reasons": [],
  "dataset_revision": 123,
  "data_watermark": "...",
  "projected_at": "..."
}
```

字段语义：

- `fact_id` 仍等于 current observation ID；首个 Slice 的业务唯一性是 `(tenant_id, site_id, meter_binding_id, source_current_observation_id)`。
- `energy_type` 来自 Binding，不能从 `hvac_meter.energy` 硬编码推导；`telemetry_key` 只保留 source provenance。
- `period_start`/`period_end` 是 previous/current observation 的 `sampled_at`，存 UTC，按 `period_end` 所在 Site timezone bucket 归档；首个 Slice 不跨 bucket prorate。
- `energy_kwh` 只能来自 `delta_value`，可以是 0（`UNCHANGED`），不能把 NULL delta 或 invalid decrease 写成 0。
- Raw quality 和 Energy quality 分开。首个 Slice 使用：`GOOD → VALID`；`PARTIAL/ESTIMATED/MANUAL → SUSPECT`；`STALE/INVALID/未定义 → INVALID`。当前 Go 中的 Raw `SUSPECT` 不再接受。
- `transition_type` 必须来自 canonical view。`INITIAL`、`REVISION_BOUNDARY`、`UNIT_BOUNDARY`、`INVALID_DECREASE` 不生成 Fact。
- `dataset_revision` 暂时保持现有查询兼容含义，由 current source offset 派生；它不是 Registry revision、Binding version 或 rebuild generation。
- `data_watermark` 等于 current observation 的 `sampled_at`；`projected_at` 只记录处理时间。
- 当前 `observation_count` 恒为 2，已被两个 observation ID 完整表达，移出领域契约；物理迁移时删除，不保留兼容字段。

### 4.4 ClickHouse physical schema change

实现会话应新增一个有序的 S2 migration（当前初始化序列之后），对 `analytics.energy_interval_facts` 做一次明确变更：

1. 新增 `meter_id`、`meter_binding_id`、`topology_version_id`、`binding_version`、`energy_type_id`、`meter_role`、`direction`。
2. 新增 `point_revision`、`unit`、`counter_decrease_mode`、`counter_rollover_modulus`、`transition_type`。
3. 新增当前 observation 的 `source_event_id`、`source_partition`；保留 `source_offset`，但不再把它单独称为完整 Source Position。
4. 保留 `energy_type` 作为 Binding 的稳定产品代码，保留 `telemetry_key` 作为 source provenance；二者都不承担 Meter identity。
5. 删除 `observation_count` 的领域语义和物理列；同步更新 Go model、JSONEachRow writer、Cube/Query SQL 和测试。
6. `ORDER BY` 至少把 `meter_id`、`meter_binding_id` 放在 Site 之后，并以 `period_end`、`source_current_observation_id` 保持稳定读取顺序。

ClickHouse MergeTree 不提供业务唯一约束。v1 的持久化不变量由三层共同形成：

- Reader anti-join 使用完整逻辑键，而不是只按 current observation ID；
- Writer 使用稳定排序和由逻辑键组成的 deduplication token；
- v1 只运行一个 active Energy Projector writer。多 writer/HA 不是本票据批准的部署形态，不能宣称当前实现已经具备集群并发安全。

### 4.5 Projector service seams

实现只需要三个清晰 seam：

```go
type CounterSource interface {
    ListDeltas(context.Context, int) ([]CounterDelta, error)
}

type BindingResolver interface {
    Resolve(context.Context, BindingResolveInput) (BindingResolution, error)
}

type FactSink interface {
    InsertFacts(context.Context, []EnergyIntervalFact) error
}
```

不新增通用 Pipeline、Rule Engine、插件注册或跨能源类型泛型。首个 Slice 只允许 electricity/PRIMARY/COUNTER，第二个能源类型到来时再根据真实差异扩展模型。

单批处理顺序：

1. 从 canonical read contract 读取最多 `BatchSize` 条 `delta_value IS NOT NULL` 的候选，并 anti-join 已有 Fact。
2. 对候选解析 Binding；`NO_MATCH`/`AMBIGUOUS` 直接使本批失败且不写 Fact。
3. 校验 Resolver snapshot 与当前 observation 的 Tenant/Site/Device/Point/Counter type 一致；不一致是内部契约错误，不做静默修正。
4. 由 canonical delta、Raw quality 和 Binding snapshot 构造 Fact。
5. 先在内存中检查批内逻辑键唯一，再一次性写入 Fact；写入失败不推进任何应用层 checkpoint。
6. Writer 错误返回给现有 runner；runner 保持 not-ready/失败指标，下一轮重读。不要 catch-and-ignore，也不要把失败候选标为已处理。

当前 Projector 的 `BuildFact` 负值/回退分支、raw observation window query 和 `ACCEPTED`-only filter 在这一步删除；不保留两个算法版本。

## 5. 权限和接口影响

### 5.1 Registry/Core

沿用 #307 的公共 Content API 和内部 resolver 契约：

- 浏览器仍然只访问 Platform Gateway；
- Gateway 新增/使用 `energy-meter.list/read`、`meter-binding.list/read`；
- Processing 使用独立的服务间 `meter-binding.resolve` 能力，不借用浏览器 action；
- Core 使用 mTLS、delegation grant、Tenant/Site scope 和 RLS；resolver 不允许任意表查询或列表分页猜测；
- Resolver 响应必须包含 `revision`、`version`、status、effective interval、Meter/Topology/Point identity 和 direction。

### 5.2 ClickHouse identities

按 owner 分离最小权限：

| 身份 | 权限 |
| --- | --- |
| `analytics_projector_reader` | 读取 `telemetry_history.counter_deltas` 及完成 provenance 所需的 History observation；读取 Fact 做 anti-join。 |
| `analytics_projector_writer` | 仅插入 `analytics.energy_interval_facts`。 |
| `cube_analytics_reader` | 只读 Fact，继续服务 Energy Series。 |

不向 Gateway、UI 或 Query Service 授予 Registry 写权限、Fact 写权限或 Raw 写权限。

## 6. 需要删除、保留和改变的当前行为

| 当前行为 | 裁决 | 实施动作 |
| --- | --- | --- |
| `hvac_meter.energy` 决定 electricity Meter | 删除 | 只把 key 留作来源标签，Meter 归属改由 Binding Resolver。 |
| Projector 按 raw observations 自己 `lag` | 删除 | 改读 `counter_deltas` canonical contract。 |
| 只筛 `ACCEPTED` | 删除 | 使用 canonical 的 `ACCEPTED` + `OUT_OF_ORDER`。 |
| 所有回退/负差值写 `0` | 删除 | 由 transition 决定：RESET/ROLLOVER/RECOVERY 可形成事实；INVALID_DECREASE 不形成事实。 |
| Raw `GOOD/SUSPECT` | 删除 | 使用 History 已定义的 Raw quality enum，再映射到 Energy quality。 |
| current observation ID 作为 FactID | 保留 | 继续作为首个 Slice 的稳定身份；同时保存完整 Binding/Source provenance。 |
| anti-join + JSONEachRow + async insert | 保留并收紧 | anti-join 使用完整逻辑键，dedup token 包含逻辑键，批写失败可重试。 |
| `source_offset = dataset_revision` | 暂时保留 | 只保留当前查询兼容含义，并在 UI/文档中禁止解释为 rebuild generation。 |
| 现有 Energy Series URL、粒度和质量查询 | 保留 | Query 适配新 Fact 字段；不借此扩张 Meter/Space/Asset subject。 |

## 7. 最小实现顺序

### Step 1：先落 canonical read contract 和 Fact schema

- 为 `counter_deltas` 补齐 previous ID、current Source Position 和质量读取字段；
- 新增 Fact provenance migration，删除 `observation_count`；
- 更新 ClickHouse grants 和初始化/迁移顺序；
- 先让 schema 与逻辑模型可以表达事实，再改 Go。

### Step 2：完成 Core Binding Resolver

- 按 #307 追加 private resolver route、Registry query、RLS、delegation 和响应校验；
- 用 event-time half-open match；
- 让 NO_MATCH/AMBIGUOUS 成为明确 domain result；
- 不在 Processing 中复制 SQL 或读取 Registry 数据库。

### Step 3：重写 Projector 的 source/builder/sink

- `Candidate` 改成 canonical `CounterDelta`；
- `BuildFact` 不再做累计值相减，只做 Binding snapshot 合并、quality mapping、Fact identity 和字段验证；
- Reader 删除 raw window query，改读 canonical view；
- Writer 增加 provenance 字段和完整逻辑 key；
- runner 增加 resolver client、失败指标和单 writer 运行约束。

### Step 4：接通 Query 和 UI 的真实结果

- 保持现有 Energy Series public contract；
- 验证 hour/day/month 按 Site timezone、quality policy、watermark、partial、dataset revision 工作；
- Energy Management 页面显示 Fact 数据状态；
- Administration 页面只读 Meter/MeterBinding 内容和 Registry revision，不把它伪装成 Energy freshness。

### Step 5：再开扩展票据

只有首个 slice 的事实、查询、权限和 UI 状态都通过门禁后，才建立下一张票：rebuild generation、旧事实 supersede/tombstone、并发执行、失败恢复和多站点处理。Cost/Carbon/Baseline/Report 另立领域事实和查询契约，不在本实现中预埋字段。

## 8. 真实产品验收门禁

门禁只保护可观察产品契约和数据不变量；不以覆盖率为目标。

### 8.1 Counter semantics gate

使用真实 ClickHouse fixture 或等价的 canonical view integration fixture，至少验证：

1. `INITIAL` 不产生 Fact。
2. `INCREASE` 产生正确 delta 和 `VALID` quality。
3. `UNCHANGED` 产生 0 kWh，而不是被当作缺失。
4. `OUT_OF_ORDER` observation 参与 event-time 配对。
5. `REVISION_BOUNDARY`、`UNIT_BOUNDARY`、`INVALID_DECREASE` 不产生 Fact，不写 0。
6. `RESET_TO_ZERO`、有效 `ROLLOVER`、`RECOVERY` 使用 canonical delta 并记录对应 `transition_type`。
7. Raw `PARTIAL/ESTIMATED/MANUAL/STALE/INVALID` 映射为正确 Energy quality，且 Raw 记录未被修改。

### 8.2 Binding and provenance gate

1. `effective_from` 命中，`effective_to` 相等不命中；
2. 不存在 Binding 时无 Fact；
3. 多个 released/active PRIMARY 命中时无 Fact，不能自动挑一条；
4. Device/Point/Counter type/energy type 不一致时，批次失败且无 Fact；
5. Fact 能读出 Meter、Binding、Topology、Binding version、Point revision、unit、Counter semantics、previous/current observation ID 和 current Source Position；
6. Fact 的 Energy type 来自 Binding，换 telemetry key 不会绕过 Binding。

### 8.3 Idempotency and persistence gate

1. 同一 Raw + 同一 Binding snapshot + 同一算法重复运行不增加 Fact；
2. `projected_at` 改变不改变 Fact identity 或 measurement；
3. Fact writer 在 ClickHouse 暂时失败时返回错误，不吞错；下一轮可重读并得到同一结果；
4. 批内重复 current observation 被拒绝；
5. 两个 Projector 同时运行不被测试包装成已支持能力；v1 发布门禁必须确认只有一个 active writer。

### 8.4 Query/UI gate

1. 现有 Energy Series public contract 无需客户端知道 Fact 表结构即可返回结果；
2. Site timezone 下 hour/day/month bucket 与 `period_end` 规则一致；
3. `VALID_ONLY` 与 `VALID_AND_SUSPECT` 的结果、quality summary、watermark、partial、dataset revision 与 Fact 质量一致；
4. UI 不把 NO_MATCH、AMBIGUOUS、STALE、PARTIAL 或无数据渲染成 0 kWh；
5. Administration 显示 Registry revision，Energy Management 显示 Energy dataset revision，两者不混淆。

## 9. 明确不做的测试和防御

- 不为每个 DTO getter、配置默认值或 SQL 字符串重复做无意义测试；
- 不为未来的 water/gas/cooling、Virtual Meter、Tariff、Carbon、Report 建空测试矩阵；
- 不在 Gateway、Core、Projector、Query 每层重复校验同一 trusted contract；
- 不通过 catch-and-ignore、silent fallback、回退置零或“选择最新 Binding”掩盖数据错误；
- 不把 ClickHouse deduplication window 描述成无限期 exactly-once；
- 不在本票据实现 rebuild、multi-writer lease 或 HA。它们应在首个 Slice 稳定后作为新的源码核对和决策票据。

## 10. Source-review record 更新点

实现会话开始前和完成后都要更新 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)：

- 记录 HVAC `counter_deltas` read-contract 扩展、Fact migration、Resolver response 和 Projector 行为的实际文件；
- 对 MyEMS 的 Cleaning/Normalization/Aggregation 记录为 ADOPT/ADAPT，不记录成“照搬”；
- 对 ThingsBoard 时序存取记录为平台边界 ADAPT，不记录成能源模型来源；
- 对 OpenEMS typed Channel/Timedata 记录为 provenance/history seam ADAPT/DEFER，不记录成当前 Backend runtime；
- 每一项都链接实际生产源码、实际测试、schema/migration 或官方文档；没有核对的行为写 VERIFY。

本票据只给出可以执行的路线；它不代表代码已经通过验收。
