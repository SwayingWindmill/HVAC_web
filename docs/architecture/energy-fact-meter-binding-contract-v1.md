# Wayfinder #306：Energy Fact、MeterBinding、质量与溯源契约

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：首个 electricity Meter 纵向切片的 MeterBinding 读取范围、Energy Interval Fact 最小逻辑字段、质量/transition/provenance、事实唯一性和重放身份。不实现 Schema 迁移、Registry API、Projector 重构或 UI。

## 1. 冻结结论

首个切片的 Energy Interval Fact 不是“Point + telemetry key 的增量结果”，而是一个在固定 Registry 内容快照下、由两个历史 Counter observations 形成的、可追溯的能源区间事实。

冻结以下边界：

1. MeterBinding 是 Energy Content 的身份来源。Energy Processing 只能读取已发布的 Binding/Topology 内容，不能用 `hvac_meter.energy` 推断 Meter。
2. Binding 按 observation 的 `sampled_at` 做半开区间匹配：`effective_from <= sampled_at < effective_to`；缺失或歧义 Binding 时不生成 Energy Fact，不静默选择“最新”配置。
3. Fact 的历史输入仍是 `telemetry_history.counter_deltas` 的规范语义；事实必须保留 MeterBinding 快照、前后 observation ID、当前 source position、Point/Counter 语义快照和 transition type。
4. `FactID` 首个切片继续使用 current observation ID。相同 Raw + 相同 Binding 版本 + 相同算法的重放必须得到相同事实身份；改变 Binding 或处理算法的重算代际不伪装成普通重放，generation/supersession 仍由后续票据裁决。
5. `datasetRevision` 首个切片继续使用当前事实的 source offset 派生值；它不是 Registry version，也不是完整 rebuild generation。
6. `quality` 是 Energy Processing 的归一化质量，`quality_reasons` 是稳定代码集合；Raw 的 acceptance、quality 和 reasons 仍由 History 保存，不被 Fact 覆盖。
7. `INITIAL`、revision/unit boundary、invalid decrease 没有可计量 delta；它们由 `counter_deltas` 保留为 transition evidence，不伪造成零能耗。`RESET`、`ROLLOVER` 和 `RECOVERY` 按 canonical delta 生成可追溯事实。

## 2. 源码与参考基线

三方固定提交和逐文件审查记录见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)：

| 项目 | 固定提交 | 本票据吸收的机制 |
| --- | --- | --- |
| ThingsBoard CE v4.3.1.1 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` | Latest 与 History 分离；实体、键和时间是时序事实的来源上下文。HVAC 只吸收边界，不复制其通用 Telemetry runtime。 |
| OpenEMS 2026.7.0 | `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | Channel/Doc 对类型、单位、类别和自描述的约束。HVAC 将 Point/Counter 的语义快照保留在历史溯源中，不引入 Edge Cycle/Controller。 |
| MyEMS v6.7.0 | `be6e6ce8ddeac57afb04bddb9621501fb555cab0` | Meter/Virtual Meter、Cleaning/Normalization、Data Repair 和处理记录的职责分离。HVAC 按逻辑边界吸收，不复制 MyEMS 的物理数据库数量。 |

本票据实际复核的 HVAC 生产源码、DDL、合同和测试：

| 文件 | 源码事实 |
| --- | --- |
| [`009a-energy-topology-metering-v2.sql`](../../infra/registry/postgres/init/009a-energy-topology-metering-v2.sql) | `energy_topology_versions`、`energy_meters`、`meter_bindings`、`meter_ratio_versions` 有 Tenant/Site scope、version、status、effective time、released/active 生命周期和约束；Binding 关联 topology、edge、energy type、meter、device、counter point、role、direction。 |
| [`007-spatial-sensor-point-model.sql`](../../infra/registry/postgres/init/007-spatial-sensor-point-model.sql) | Point 是 canonical measurement identity，包含 reporting device、point type、value type、unit、status、revision；Point 不等于 Meter。 |
| [`001-telemetry-history.sql`](../../infra/telemetry/clickhouse/init/001-telemetry-history.sql) | History observation 保存 observation ID、Tenant/Site/Device/Point/Sensor、source event/partition/offset、sample/receive time、acceptance、quality、quality reasons、unit、point revision 和 Counter semantics。 |
| [`004-counter-semantics.sql`](../../infra/telemetry/clickhouse/init/004-counter-semantics.sql) | Counter 以 Tenant/Site/Point 的 event-time 顺序产生 transition/delta；Point revision、unit、reset、rollover、invalid decrease、recovery 都有明确结果；Latest 不参与。 |
| [`002-analytics-energy-interval.sql`](../../infra/telemetry/clickhouse/init/002-analytics-energy-interval.sql) | 当前 Fact 缺少 Meter/Binding/Topology/version/transition/source Point semantics；`energy_kwh` 是非 Nullable Float64，`source_offset` 和 `dataset_revision` 是 UInt64。 |
| [`analytics-read-model-projector/internal/energy/projector.go`](../../modules/energy/internal/energy/projector.go) | 当前 FactID 使用 current observation ID；质量为 VALID/SUSPECT/INVALID；当前将负值和回退置零，回退标记为 SUSPECT；source offset 复制为 dataset revision。 |
| [`analytics-read-model-projector/internal/clickhouse/client.go`](../../modules/energy/internal/clickhouse/client.go) | 当前 candidate 只携带 previous/current observation、数值、quality、sampled time 和 source offset；查询只筛 `ACCEPTED`，窗口按 Device/Point/Sensor/Telemetry Key 重算。 |
| [`projector_test.go`](../../modules/energy/internal/energy/projector_test.go)、[`golden_test.go`](../../modules/energy/internal/energy/golden_test.go) | 现有测试保护当前 electricity interval 行为，包括 FactID、quality、source offset、负值和 rollback；没有 MeterBinding、Topology Version、canonical Counter view 或 Source Position 完整快照测试。 |
| [`telemetry-query-service/internal/history/aggregate.go`](../../modules/telemetry/internal/history/aggregate.go)、[`aggregate_test.go`](../../modules/telemetry/internal/history/aggregate_test.go) | 现有 History Counter aggregate 已按 `ACCEPTED/OUT_OF_ORDER`、Point revision、unit、quality、reset、rollover 生成查询语义；这是当前 Projector 应复用而不是绕过的本地证据。 |
| [`platform-core-service/README.md`](../../modules/registry/README.md) | 当前 Registry read API 只有 Site、Asset、Device；Energy Content 表虽有 RLS/read grant，但 Meter/MeterBinding API 尚未实现。 |

## 3. MeterBinding Contract

### 3.1 Binding 读取范围

Energy Processing 通过只读 Energy Content adapter 获取 released snapshot，不直接查询或写 `core_registry`。首个切片的逻辑输入是：

| 字段 | 必须 | 用途 |
| --- | --- | --- |
| `tenant_id`, `site_id` | 是 | 数据隔离和产品查询 scope |
| `meter_binding_id` | 是 | Binding 的稳定身份 |
| `meter_id` | 是 | 物理 Meter 的稳定身份 |
| `topology_version_id` | 是 | 说明该 Binding 属于哪个已发布能流图版本 |
| `binding_version` | 是 | 说明事件时间使用的 MeterBinding 业务版本 |
| `energy_type` | 是 | 首个切片固定为 `electricity`，不能由 telemetry key 推断 |
| `meter_role` | 是 | 首个切片固定选择 `PRIMARY`；CHECK/MONITORING/BACKUP 不进入本切片 |
| `direction` | 是 | 保留 IMPORT/EXPORT/CHARGE/DISCHARGE 等 Registry 语义，不在 Fact 中重新推断 |
| `device_id`, `point_id`, `point_type` | 是 | 将 Binding 与历史 observation 的源点对齐；`point_type` 必须是 `COUNTER` |
| `effective_from`, `effective_to` | 是 | 按 observation event-time 做 Binding 选择 |

`energy_edge_id` 不作为首个 Fact 的独立查询维度；它已由 `meter_binding_id + topology_version_id` 定位，待 Energy Aggregate/Settlement 需要 accounting-edge 维度时再单独扩展。`meter_ratio_versions` 也不进入本切片的 Fact 字段：当前 Projector 没有平台倍率计算，首个切片只接受 `DEVICE_APPLIED`/`EDGE_APPLIED` 或有效 cloud multiplier 为 1 的 Meter 输入；`PLATFORM_APPLIED` 需要单独的倍率计算合同，不能被静默忽略。

### 3.2 Event-time 选择规则

对每个 current observation：

1. 先按 `tenant_id + site_id + device_id + point_id + energy_type` 定位候选 Binding；
2. 只使用已发布生命周期中的 Binding snapshot，并满足 `effective_from <= current.sampled_at` 且 `effective_to IS NULL OR current.sampled_at < effective_to`；
3. Binding 的 Point、Device、PointType 必须与 Raw observation 对齐；Point revision、unit 和 Counter decrease semantics 以 Raw observation 内的历史快照为准；
4. 结果必须唯一。没有 Binding、Binding 已失效、Binding 关联不匹配或出现多个匹配 Binding 时，Energy Processing 记录不可归属/歧义证据，不生成 Fact；不能用当前 ACTIVE 绑定覆盖历史，也不能按 `updated_at` 选择一条。

现有 PostgreSQL trigger 已阻止同一 topology/edge/direction 下重叠的 PRIMARY Binding，但它不能自动证明跨 topology version 的全局 event-time 唯一性。因此“一个观察时间只能得到一个有效 PRIMARY Binding”必须成为 Energy Content release/read contract 的不变量；现有表约束不足是 LOCAL-CHANGE。

## 4. Energy Interval Fact 最小逻辑模型

物理表字段不要求一比一照抄下面的 JSON，但任何实现必须能表达这些逻辑信息：

```json
{
  "fact_id": "current_observation_id",
  "scope": {
    "tenant_id": "...",
    "site_id": "..."
  },
  "binding": {
    "meter_id": "...",
    "meter_binding_id": "...",
    "topology_version_id": "...",
    "binding_version": 1,
    "energy_type": "electricity",
    "meter_role": "PRIMARY",
    "direction": "IMPORT"
  },
  "source": {
    "device_id": "...",
    "point_id": "...",
    "sensor_id": "...",
    "point_revision": 3,
    "unit": "kWh",
    "counter_decrease_mode": "RESET_TO_ZERO",
    "counter_rollover_modulus": null,
    "previous_observation_id": "...",
    "current_observation_id": "...",
    "source_position": {
      "partition": "...",
      "offset": 123,
      "event_id": "..."
    }
  },
  "measurement": {
    "period_start": "...",
    "period_end": "...",
    "energy_kwh": 2.75,
    "transition_type": "INCREASE"
  },
  "quality": {
    "quality": "VALID",
    "quality_reasons": []
  },
  "lifecycle": {
    "dataset_revision": 123,
    "data_watermark": "...",
    "projected_at": "..."
  }
}
```

### 4.1 字段裁决

| 当前字段/能力 | 裁决 | 说明 |
| --- | --- | --- |
| `fact_id = current_observation_id` | KEEP | 继续作为首个切片的稳定事实身份和重放幂等键。 |
| Tenant/Site/Device/Point/Sensor | KEEP | 保留查询分区和源点溯源；Tenant/Site 是强制 scope。 |
| `telemetry_key` | ADJUST | 只能是 source provenance，不能作为 Meter 身份或唯一性依据；Energy 归属来自 MeterBinding。 |
| `energy_type` | KEEP / SOURCE FROM BINDING | 保留 `electricity` 产品代码，但不再由固定 telemetry key 硬编码决定。 |
| `period_start`, `period_end` | KEEP | 分别来自 previous/current observation 的 sampled time；采用 UTC 存储，业务日历由 Query 使用 Site timezone。 |
| `energy_kwh` | ADJUST | 仅对 canonical delta 非空的 transition 生成非负值；invalid decrease、revision/unit boundary 不写成 0。它们由 `counter_deltas` 保留 evidence。 |
| `quality`, `quality_reasons` | KEEP / CLARIFY | Fact 使用 Energy 归一化质量；理由是去重、排序后的稳定 code 集合，保留 source reasons 和 processing reasons。 |
| `observation_count` | REMOVE FROM DOMAIN CONTRACT | 当前永远为 2，且 previous/current observation ID 已经表达该事实；物理 read model 可在迁移前暂存，但不作为独立业务语义。 |
| `source_previous_observation_id`, `source_current_observation_id` | KEEP | 事实必须能回到两个 Raw observation；current ID 同时是首个切片 FactID。 |
| `source_offset` | ADJUST TO SOURCE POSITION | 不能单独代表来源位置；至少与 source partition、source event ID 一起形成 current observation 的 Source Position。 |
| `dataset_revision` | KEEP WITH NARROW MEANING | 首个切片继续由 current source offset 派生，用于现有 Query metadata；不解释为 Binding version 或 rebuild generation。 |
| `data_watermark` | KEEP | 等于 current observation 的 sampled time，是数据覆盖水位，不是 projected time。 |
| `projected_at` | KEEP | 只记录处理时间，不能用于判断 event-time coverage。 |
| Meter/Binding/Topology/version | ADD | 这是现有 Fact 缺失、但首个 Meter slice 必需的业务身份和可重现 provenance。 |
| Point revision/unit/Counter semantics | ADD | 这些已在 Raw 中快照；Fact 需要保留使用过的语义，避免只看当前 Registry Point。 |
| `transition_type` | ADD | 使 `INCREASE/RECOVERY/RESET/ROLLOVER` 的计算证据可解释；excluded transition 仍以 `counter_deltas` 为规范证据。 |

## 5. Quality 与 Transition 契约

### 5.1 两种质量不能混用

| 层 | 允许的事实 | owner |
| --- | --- | --- |
| Raw acceptance | `ACCEPTED`、`OUT_OF_ORDER` 等 observation 状态 | Telemetry Runtime/History |
| Raw quality | `GOOD`、`PARTIAL`、`ESTIMATED`、`MANUAL`、`STALE`、`INVALID` 及 source reasons | Telemetry Runtime/History |
| Energy quality | `VALID`、`SUSPECT`、`INVALID` | Energy Processing |

首个 slice 的质量归一化规则：

- previous/current 都是 `GOOD` → `VALID`；
- previous/current 属于可保留但不完全可信的 `PARTIAL`、`ESTIMATED`、`MANUAL` 集合 → `SUSPECT`；
- `STALE`、`INVALID` 或未定义的 source quality → `INVALID`，追加 `SOURCE_QUALITY_INVALID`；
- source reasons 与 transition reasons 合并，去空、去重、按稳定顺序输出；
- Energy quality 不改变 Raw observation，也不把 source acceptance 改写成 Energy quality。

当前 Go Projector 使用 `GOOD/SUSPECT` 作为 source quality token，但 `ingest.go` 的 canonical Raw enum 是 `GOOD/PARTIAL/ESTIMATED/MANUAL/STALE/INVALID`。这是 LOCAL-CHANGE：实现时要让 source adapter 使用 Raw quality contract，不能长期维护一套未定义的 `SUSPECT` Raw 值。

### 5.2 Transition 的事实规则

| Transition | Energy Fact | 说明 |
| --- | --- | --- |
| `INITIAL` | 不生成 | 没有 previous observation，不能形成区间。 |
| `INCREASE` / `UNCHANGED` | 生成 | delta 为当前值减前值；unchanged 为 0。 |
| `RECOVERY` | 生成 | 使用同一 Point revision/unit 下的历史最大值作为 recovery base。 |
| `RESET` | 生成 | 只有 Raw snapshot 声明 `RESET_TO_ZERO` 时，delta 为当前值。 |
| `ROLLOVER` | 生成 | 只有 modulus 和 `ROLLOVER` 语义均有效时，按 `modulus - previous + current` 计算。 |
| `REVISION_BOUNDARY` / `UNIT_BOUNDARY` | 不生成 | 不跨 Point revision 或 unit 计算。 |
| `INVALID_DECREASE` | 不生成 | 不能把无效回退变成零能耗；Raw 和 `counter_deltas` 保留排除证据。 |

Source quality 为 `INVALID` 但 transition 本身具有 delta 时，Fact 可以保留计算出的 `energy_kwh` 与 `INVALID` quality；Query 的 quality policy 决定是否纳入统计。这与当前 History aggregate 对 current/previous quality 的独立过滤相符，也避免用零覆盖来源事实。

## 6. 重放、唯一性与版本含义

### 6.1 首个切片的事实身份

在首个切片约束“一条 observation 在指定 event-time 只能绑定一个 PRIMARY electricity Meter”下：

- `FactID = source_current_observation_id`；
- 逻辑唯一性为 `(tenant_id, site_id, meter_binding_id, source_current_observation_id)`；由于首个 slice 的 Binding 选择必须唯一，current observation ID 在产品路径上仍是稳定幂等键；
- 相同 Raw、相同 released Binding snapshot、相同 Counter algorithm 的重放必须产生相同 FactID 和相同测量值；`projected_at` 可以不同，但不改变事实身份；
- Fact writer 的 dedup 和 anti-join 必须保护这个逻辑键，不能只依赖短期 ClickHouse dedup window。

### 6.2 Dataset Revision 与 Registry Version 分离

首个切片保留三种不同的 revision 语义：

| 名称 | 语义 |
| --- | --- |
| `binding_version` / `topology_version_id` | Energy Content 的 released 配置版本，决定事件时间使用哪个 Meter/Topology 口径。 |
| `dataset_revision` | 当前 Query 兼容字段；首个 slice 从 current source offset 派生，表示来源事实 revision，不表示整站重算完成。 |
| rebuild generation | 未来历史修正/重算的处理代际；本票据不定义，不能借用 `dataset_revision` 代替。 |

因此，Binding version 改变不能只更新 Fact 的 `dataset_revision`；它会改变事实的 provenance，并需要后续 rebuild generation/supersession 规则。这个问题保持为后续独立票据，不在本票据内发明 ClickHouse 替换策略。

## 7. 与三方参考项目的裁决

| 参考机制 | 裁决 | HVAC 目标行为 |
| --- | --- | --- |
| ThingsBoard Latest/History 与 entity-key-time 时序上下文 | ADAPT | 保持 Current 与 History 分离；Fact 的稳定身份使用 observation lineage，不让 Latest 参与 Counter 计算。 |
| OpenEMS Channel/Doc 的 typed value、unit、self-description | ADAPT | 把 Point revision、unit 和 Counter semantics 作为历史来源快照；不引入 Java/OSGi 或 Edge runtime。 |
| MyEMS Meter/Virtual Meter、Normalization、Data Repair | ADOPT/ADAPT | MeterBinding 是 Energy Content，Normalization 是 Energy Processing，修正必须留下处理/质量证据；不复制 MyEMS 多数据库和对象命名。 |
| MyEMS 未来 cost/carbon/billing 字段 | DEFER | 本切片只承载 electricity interval；Tariff、Carbon、Billing 通过后续事实/查询合同加入，不预埋无使用者列。 |

## 8. 已确认的本地修改项

以下问题必须在实施票据中直接改，不以兼容路径长期保留：

1. Projector 需要接入 `counter_deltas` 或等价的 canonical Counter read contract，删除自己的第二套窗口增量算法。
2. Projector 输入必须与 canonical view 一致，纳入 `OUT_OF_ORDER` 并按 Point revision/unit/Counter semantics 处理。
3. `energy_interval_facts` 需要补充 MeterBinding/Topology/version 和 Point/Counter provenance；固定 telemetry key 不能继续代表 Meter。
4. `source_offset` 需要和 source partition/event ID 一起表达 Source Position；不能继续把单一 offset 同时当作完整 provenance。
5. 当前 rollback/negative path 的“写 0”行为需要按 canonical transition contract 调整；不增加兼容分支。
6. Energy Content 需要新增只读 released MeterBinding contract；当前 Platform Core 只有 Site/Asset/Device routes，不能让 UI 直接读 Registry 表补洞。

本票据不实现具体 SQL migration、Registry API、ClickHouse engine 或 rebuild job。#307 已另行冻结 Energy Content 的查询 API、授权、分页和 resolver 输入，详见 [`energy-content-query-contract-v1.md`](energy-content-query-contract-v1.md)；#310 已将 Projector、Fact schema 和首个 Slice 的验收门禁落实为实施规格，rebuild generation/supersession 仍后置。
