# Wayfinder #304：能源模块所有权与接口 seams

状态：DECIDED / SOURCE-BACKED  
审查日期：2026-08-25  
范围：Energy Processing、Energy Content、Telemetry Runtime、Metric 的逻辑 owner、模块深度、读写方向和适配器；不实现新服务、数据库迁移或 UI。

## 1. 决策原则

本决策建立在固定版本源码和 HVAC 当前实际调用路径上，不把当前物理共址、已有表或服务名直接当成领域所有权。

三方源码固定基线及逐文件证据见 [`wayfinder-energy-reference-source-review-2026-08.md`](../research/wayfinder-energy-reference-source-review-2026-08.md)：

| 项目 | 固定版本 | 完整提交 | 本票据使用的证据 |
| --- | --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` | Timeseries Node、Latest/History DAO、Calculated Field、Dashboard、EntityData/WebSocket |
| OpenEMS | 2026.7.0 | `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | Channel/Value、Cycle、Scheduler、Timedata、历史数据重发 |
| MyEMS | v6.7.0 | `be6e6ce8ddeac57afb04bddb9621501fb555cab0` | Cleaning、Normalization、Aggregation、Meter/Virtual Meter、API、Admin/Web |

## 2. HVAC 实际证据

本票据实际复核了以下生产源码、合同、表定义和代表性测试：

| 证据 | 观察 |
| --- | --- |
| [`telemetry-runtime-service/main.go`](../../services/telemetry-runtime-service/cmd/telemetry-runtime-service/main.go) | Runtime 启动历史投影、Latest 投影和 analytics projector；analytics 通过 `loadAnalyticsProjection` 挂载，日志明确记录 `deployment=telemetry-runtime-in-process`。这证明的是物理共址，不是 Energy ownership。 |
| [`history_test.go`](../../services/telemetry-runtime-service/internal/telemetry/history_test.go)、[`ingest_test.go`](../../services/telemetry-runtime-service/internal/telemetry/ingest_test.go) | History relay 从 Runtime 发布已接受观察；Counter 语义在历史中被快照。Runtime 测试边界覆盖 ingest、quality、ordering、history 和 current snapshot。 |
| [`001-telemetry-history.sql`](../../infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql)、[`004-counter-semantics.sql`](../../infra/s2-telemetry/clickhouse/init/004-counter-semantics.sql) | History 保存观察、质量和 Counter 元数据；`counter_deltas` 按历史 event-time 顺序处理 revision、unit、reset、rollover 和 invalid decrease，且明确不读取 Latest。 |
| [`analytics-read-model-projector/main.go`](../../services/analytics-read-model-projector/cmd/analytics-read-model-projector/main.go)、[`projector.go`](../../services/analytics-read-model-projector/internal/energy/projector.go)、[`clickhouse/client.go`](../../services/analytics-read-model-projector/internal/clickhouse/client.go) | Projector 只从 `telemetry_history.observations` 读，写 `analytics.energy_interval_facts`；当前按 `hvac_meter.energy` 建 candidate，直接按 Point/Device/Telemetry Key 分区并重新处理增量。它没有 MeterBinding 输入。 |
| [`projector_test.go`](../../services/analytics-read-model-projector/internal/energy/projector_test.go)、[`golden_test.go`](../../services/analytics-read-model-projector/internal/energy/golden_test.go)、[`client_test.go`](../../services/analytics-read-model-projector/internal/clickhouse/client_test.go) | 测试保护累计读数转区间、质量传播、回退/负值、批处理、未投影 anti-join 和 ClickHouse 去重；没有测试 Registry MeterBinding 或 Counter 视图的接入。 |
| [`telemetry-query-service/README.md`](../../services/telemetry-query-service/README.md)、[`cube/client.go`](../../services/telemetry-query-service/internal/cube/client.go)、[`cube/client_test.go`](../../services/telemetry-query-service/internal/cube/client_test.go) | Query Service 只接受固定 Energy Series 合同，映射固定 Cube members，计算 quality/partial/watermark/revision；不构造区间、不写 ClickHouse、不执行命令。 |
| [`platform-core-service/README.md`](../../services/platform-core-service/README.md)、[`server_test.go`](../../services/platform-core-service/internal/core/server_test.go)、[`postgres_integration_test.go`](../../services/platform-core-service/internal/core/postgres_integration_test.go) | Platform Core 是 `core_registry` 的 Registry 读边界，当前只暴露 Site、Asset、Device 等 Registry routes；没有 Meter/MeterBinding 能源路由。其测试保护租户/Site RLS、Grant 和 Registry read boundary。 |
| [`009a-energy-topology-metering-v2.sql`](../../infra/s1-registry/postgres/init/009a-energy-topology-metering-v2.sql) | `energy_meters`、`meter_bindings`、Virtual Meter 已在 Registry Schema 定义。Schema 存在不等于 Energy Content 已有完整运行时 owner。 |
| [`metric/engine.go`](../../services/metric-engine-service/internal/metric/engine.go)、[`metric/clickhouse.go`](../../services/metric-engine-service/internal/metric/clickhouse.go)、[`engine_test.go`](../../services/metric-engine-service/internal/metric/engine_test.go) | Metric Engine 按 Metric Binding 解析 Point/Metric/External 输入，执行版本化计算、发布和重算；Point 输入当前直接读 `telemetry_history`，Metric Result 写入 `analytics.metric_result_facts`，并通过 Registry publication state 做恢复。 |
| [`009c-metric-model-v2.sql`](../../infra/s1-registry/postgres/init/009c-metric-model-v2.sql) | Metric、Metric Version、Dependency、Binding、Calculation Run、Result Revision 的版本和不可变约束已定义；Metric 不是 Telemetry Point。 |
| [`data-ownership.v1.json`](../../contracts/ownership/data-ownership.v1.json) | 当前声明 `telemetry-history-projector` 写 History、`analytics-read-model-projector` 写 Energy facts、`telemetry-query-service` 写查询合同、`platform-core-service` 写 Registry Schema。它是当前数据合同，不能掩盖实际服务调用和缺失的 Energy Content API。 |

## 3. 最终 owner 裁决

### 3.1 Owner 表

| 逻辑模块 | Domain Owner | Phase 1 物理承载 | 权威写入 | 允许读取 | 明确不拥有 |
| --- | --- | --- | --- | --- | --- |
| Telemetry Runtime | `telemetry-runtime-service` | 独立服务 | 接受/拒绝观察、Runtime current、Latest/Presence、Runtime quality 和运行时事件 | 源适配器、IAM 授权、Runtime Schema | Energy Interval Fact、MeterBinding、Energy Aggregate、Metric Result |
| Telemetry History | `telemetry-history-projector`（支撑 owner） | 当前由 Runtime 启动 relay | `telemetry_history.observations` | Runtime 已发布的历史观察 | Energy 内容定义和能源聚合语义 |
| Energy Content | `Energy Content` 逻辑模块，Phase 1 归属 S1 Registry owner `platform-core-service` | 不新增独立 deployable service；先作为 Registry 边界内的明确模块 | Meter、MeterBinding、Topology、Virtual Meter 及后续 Category/Item、Tariff、Carbon 等 released content | Registry 自有数据和内容引用 | Raw/Current、Energy Fact 计算、Cube 聚合执行、Edge 控制 |
| Energy Processing | `Energy Processing` 逻辑模块，运行 owner 为 `analytics-read-model-projector` | 独立 projector 包/worker；当前允许在 `telemetry-runtime-service` 内物理共址运行 | `analytics.energy_interval_facts` 以及未来 Energy Processing read models | History read contract、released Energy Content read contract | Telemetry Runtime current、Energy Content mutation、Metric 内部表、控制执行 |
| Energy Series Query | 不是独立 Domain Owner；是产品查询适配边界 | `telemetry-query-service` + Cube | 固定 query contract/semantic model，不写领域事实 | Energy Processing read model | 区间生成、质量判定、MeterBinding 变更、任意 SQL/Cube member |
| Metric | `metric-engine-service` | 独立 metric worker/service | Metric calculation run、Metric Result publication、`analytics.metric_result_facts` | Metric definitions/bindings、History Point input、已发布 Metric input；未来可读 Energy contract | Energy Interval Fact 生成、Telemetry current、MeterBinding ownership |

这里的 owner 是逻辑责任。`platform-core-service` 当前没有 Meter/MeterBinding API，因此 Energy Content 的 owner 决策是目标模块归属，不是宣称该能力已经完成。

### 3.2 Energy Content 不拥有 Energy Aggregate 查询

Energy Content 拥有“计量口径和内容定义”，不拥有“由历史事实计算出来的聚合结果”：

- Meter/MeterBinding/Topology 决定哪些源点、方向、有效期和内容口径可参与计算；
- Energy Processing 根据这些 released inputs 生成 Interval Fact 和 Aggregate read model；
- `telemetry-query-service` 只负责固定产品查询、授权委托、Cube 适配和结果元数据；
- Reporting（后续独立模块）可以读取 Energy Processing 的稳定查询合同，不把报告读取权倒置给 Energy Content。

这与 MyEMS 源码中的 cleaning/normalization/aggregation 分阶段职责一致；也避免把 ThingsBoard Dashboard 或 Rule Chain 当作能源事实 owner。

## 4. 模块深度裁决

### 4.1 Energy Content：逻辑模块，不新增服务

Phase 1 不创建 `energy-content-service`。原因是实际代码显示：

- `core_registry` 已经是平台 Registry 的隔离边界；
- `platform-core-service` 已经负责 Registry 的授权读取和 RLS；
- 当前没有成熟的 Energy Content API 或独立处理运行时，贸然新增 deployable service 只会制造另一层转发。

目标深度是：在 Registry owner 内形成明确的 Energy Content 模块和专用合同，至少覆盖 Meter、MeterBinding、Topology Version、Virtual Meter 的 released read/mutation seam。Energy Content 不能继续只是几份 SQL 表，也不能让 Energy Processing 直接写这些表。

### 4.2 Energy Processing：事实处理模块/worker

Energy Processing 保持独立的处理模块，不并入 Telemetry Runtime 的领域 owner：

- `analytics-read-model-projector/internal/energy` 拥有当前 electricity interval fact 的计算逻辑；
- `analytics-read-model-projector/internal/clickhouse` 是 History read / Analytics write 适配器；
- 当前 Runtime 内的启动代码只是 Phase 1 物理共址；日志中的 `telemetry-runtime-in-process` 不得写入 ownership 合同；
- 未来是否单独部署由吞吐、重算窗口和故障隔离触发，不通过复制一套 projector 或增加通用 pipeline framework 解决。

### 4.3 Telemetry Runtime：保持独立

Telemetry Runtime 继续拥有接受、current、Presence、Latest 和历史发布路径。Energy Processing 读取 History，而不是读取 Runtime PostgreSQL 或 Redis Latest。这样历史能耗不受当前状态缓存刷新和运行时故障影响。

### 4.4 Metric：保持独立，不作为 Energy Processing 子模块

Metric 的版本化定义、依赖图、计算运行、结果 publication、reconciliation 和 retention 已有独立代码与测试。Energy Interval Fact 不应通过 Metric Engine 生成，Metric 也不能成为 Energy Processing 的内部库。

当前 Metric Engine 直接写 `core_registry` 中的 Metric publication state，而数据 ownership 合同把整个 `core_registry` 的 writer 归为 `platform-core-service`。这是实际代码与当前 ownership 粒度之间的冲突，记录为后续 Schema/ownership 修正项；本票据不通过兼容写入或新 fallback 掩盖它。

## 5. 允许的 seams 与数据读取方向

```text
Source Adapter
    -> Telemetry Runtime
        -> Telemetry History Projector
            -> telemetry_history.observations
                -> [read-only History adapter]
                    -> Energy Processing

Energy Content released definitions
    -> [read-only Content adapter]
        -> Energy Processing

Energy Processing
    -> analytics.energy_interval_facts / Energy read models
        -> Cube semantic model
            -> telemetry-query-service
                -> platform-gateway
                    -> UI

Telemetry History + Metric definitions
    -> Metric Engine
        -> analytics.metric_result_facts + Metric publication state
```

### 5.1 允许的适配器

1. **History read adapter**：Energy Processing 只能读取已发布的 `telemetry_history` 观察和其 Counter 元数据；不得查询 Runtime current/Latest 作为历史区间的替代。
2. **Energy Content read adapter**：Energy Processing 只能读取按 event-time 生效的 released Meter/MeterBinding/Topology 版本；不得直接修改 Registry 内容。
3. **Energy fact sink**：Energy Processing 只能写自己的 `analytics` Energy 数据集；不得写 Telemetry Runtime、Registry 或 Metric 内部状态。
4. **Product query adapter**：`telemetry-query-service` 只能把固定产品合同映射到 Energy read model；浏览器只能通过 Gateway 访问它。
5. **Metric input adapter**：Metric Engine 通过自身已存在的 Point/Metric 输入适配器读取来源；未来读取 Energy 时使用稳定 Energy query/read contract，不调用 Energy Processing 内部 Go package。

### 5.2 禁止的方向

- Telemetry Runtime 直接写 `analytics.energy_interval_facts`；
- Energy Processing 直接读 `telemetry_runtime` PostgreSQL 或 Redis Latest；
- Energy Processing 直接写 `core_registry` 的 Meter、Binding、Topology 或 Metric 表；
- `telemetry-query-service` 直接构造累计电量区间或执行任意 SQL；
- Metric Engine 作为 Energy Interval Fact 的隐式计算器；
- UI 直接读取 ClickHouse、Cube、Registry 或 projector 内部接口。

## 6. 当前实现的偏差与处理顺序

| 偏差 | 源码事实 | 决策 |
| --- | --- | --- |
| Analytics projector 在 Runtime 内运行 | `main.go` 启动 projector，日志标注 in-process | `KEEP AS DEPLOYMENT CO-LOCATION`；不改变逻辑 owner，后续按容量/故障边界决定拆分 |
| Projector 重新计算 Counter delta | `client.go` 按 Point/Device/Telemetry Key 做窗口；`004-counter-semantics.sql` 已有更完整的 revision/unit/reset/rollover 语义 | `LOCAL-CHANGE`；后续应让 Energy Processing 消费 canonical Counter 语义，不再让两个模块各自定义回退规则 |
| Projector 没有 MeterBinding | Fact schema 只有 Device/Point/Telemetry Key，没有 Meter/Binding/version | `LOCAL-CHANGE`；Energy Content read seam 和 fact provenance 必须在首个 Meter 切片实现时补齐 |
| Platform Core 没有 Energy Content routes | README 只列 Site/Asset/Device registry routes | `KEEP OWNER BOUNDARY / ADD DOMAIN CONTRACT LATER`；不通过让 UI 直接查 Registry 表解决 |
| Query Service 被列在 Energy Processing 实现中 | 它只负责 Cube 查询映射和 query metadata | `ADAPT`；保留为 Energy Processing 的查询适配边界，不把它当事实 owner |
| Metric Engine 与 core_registry writer 粒度不一致 | Metric PostgreSQL store 更新 Metric run/publication 表，ownership 只声明 schema writer | `VERIFY/LOCAL-CHANGE`；后续单独收敛 Schema owner，不在 Energy slice 引入跨域直写 |

## 7. 三方源码带来的采用边界

| 参考机制 | 裁决 | HVAC 处理 |
| --- | --- | --- |
| ThingsBoard Latest 与 History DAO 分离 | ADOPT | 保持 Runtime current/Latest 与 Energy History/Fact 的边界；不复制通用 Rule Runtime |
| ThingsBoard Rule Node 的消息编排 | ADAPT | 未来可作为通用编排 seam；不让 Rule Chain 拥有能源事实或 MeterBinding |
| MyEMS Cleaning → Normalization → Aggregation 分段 | ADOPT | Energy Processing 按阶段承担事实生成、质量和重算；按现有 Go/ClickHouse/PostgreSQL 边界改写 |
| MyEMS Meter/Virtual Meter/Category/Tariff 作为内容 | ADOPT | 归入 Energy Content；不把 13 个 MyEMS 物理数据库复制到 HVAC |
| OpenEMS Timedata provider/Backend manager | ADAPT LATER | 作为未来 Edge History/resend seam；不改变当前 Backend 的 Energy owner |
| OpenEMS Channel/Cycle/Scheduler | DEFER | 属于 Edge Control Plane；不进入 Energy Processing/Telemetry Runtime 的当前边界 |

## 8. 后续票据边界

- #305 已将上述读取方向落实为 Raw/Current/Normalized/Aggregate 的数据架构、Counter 语义复用、MeterBinding 版本快照、重算边界和 dataset revision 规则，详见 [`energy-data-lifecycle-v1.md`](energy-data-lifecycle-v1.md)。
- #306 已冻结首个切片的 Energy Fact、MeterBinding released read、质量、transition 和 provenance 契约，详见 [`energy-fact-meter-binding-contract-v1.md`](energy-fact-meter-binding-contract-v1.md)。
- #307 已把 ownership seam 映射为 Backend/UI 固定 API、Administration 的 Meter/Binding 管理入口和 Energy Management 查询入口，详见 [`energy-content-query-contract-v1.md`](energy-content-query-contract-v1.md)。
- #308 已把这些 seams 映射为 Operations、Energy Management、Administration 三个 UI 工作空间，并保留 Big Screen 为展示 surface，详见 [`ui-workspace-information-architecture-v1.md`](ui-workspace-information-architecture-v1.md)。
- 当前不新增 `energy-content-service`、通用 pipeline framework、跨域 SQL fallback 或兼容双写。

## 9. 结论

1. Telemetry Runtime 只拥有 Runtime current、Latest、Presence、质量和历史发布；它不拥有 Energy facts。
2. Energy Processing 拥有规范化 Energy facts、质量判定、重算和 Energy Aggregate read models；当前 projector 的 Runtime 共址只是部署选择。
3. Energy Content 拥有 Meter、MeterBinding、Topology、Virtual Meter 以及后续能源内容配置；Phase 1 归入 Registry owner 边界，不新增独立服务。
4. `telemetry-query-service` 是固定查询适配器，不是 Energy domain owner；Reporting 后续读取稳定的 Energy 查询合同。
5. Metric Engine 保持独立；Energy Interval Fact 不是 Metric 的别名，二者不能通过内部调用互相耦合。
6. 当前最大实现偏差是 Projector 没有使用 canonical Counter 语义和 MeterBinding；这两项进入后续实现门禁。

本轮只更新架构决策文档和与之冲突的架构合同，不修改业务实现、数据库、部署配置或测试代码。
