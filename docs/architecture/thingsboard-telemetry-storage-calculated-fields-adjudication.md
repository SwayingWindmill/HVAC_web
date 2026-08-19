# ThingsBoard CE 遥测、属性、时序、存储与计算字段裁决

状态：`D04_ADJUDICATION_COMPLETE`

审查票：[审查遥测、属性、时序、存储与计算字段](https://github.com/SwayingWindmill/HVAC_web/issues/245)

本文只裁决 ThingsBoard CE v4.3.1.1 的 Attributes、Telemetry、Latest、Time Series、Aggregation、WebSocket Subscription、Calculated Fields、存储抽象、TTL 与查询行为，并将其与 HVAC Web 的 S2、PostgreSQL、Redis、ClickHouse 和 Metric Engine 做源码级反向审查。本文不假设任一方已经正确，也不在本票内改变运行时产品行为。

裁决词汇：

- `ADOPT`：行为和边界可直接成为目标设计；
- `ADAPT`：吸收模式，但按 HVAC Domain、安全、质量和数据权威重做；
- `KEEP`：本地实现有明确场景或正确性证据，应保留；
- `REPLACE`：本地或上游行为存在实质冲突，应由目标行为替换；
- `REJECT`：明确不进入本项目；
- `DEFER`：有潜在价值，但当前没有产品或运行证据支持实施。

## 1. 执行结论

HVAC Web 不应恢复 ThingsBoard 运行时，也不应照搬它的通用 Entity KV 和可插拔时序存储层。固定源码与本地源码对照后的客观结论是：

1. **保留本地摄取证据链。** Source Event/Partition/Offset、Binding、Quarantine、类型/单位/范围/时钟校验、Acceptance 与 Quality 分离、有效乱序保留、Latest 不回退，以及 PostgreSQL 事务加 Durable Outbox，比 ThingsBoard 的通用写入链更适合 HVAC 可追溯遥测。
2. **拒绝 ThingsBoard 同时间戳覆盖和 History/Latest 分离提交。** ThingsBoard SQL 的历史主键为 `(entity_id,key,ts)`，冲突更新会覆盖同时间戳旧值；History 与 Latest 由两个异步 Future 分开保存。本地应继续以 Observation 身份保存事实，并由一个提交决定驱动历史和当前投影。
3. **纠正本地 Redis 权威定义和读路径。** 公开 Current Snapshot 每次都先执行 PostgreSQL `EvaluateAndRead`，再写 Redis、再读 Redis；Redis 没有卸载读取，却被文档标为 Current Authority。目标应是：PostgreSQL Business Snapshot 是耐久当前状态权威，Redis 只是可重建低延迟投影。若没有可证明的 read-first 策略和 freshness deadline，应从公开读路径移除 Redis，而不是保留无收益的一致性仪式。
4. **修复本地 History API 丢弃有效乱序事实。** ClickHouse 原始表和 Rollup 都接受 `ACCEPTED` 与 `OUT_OF_ORDER`，但公开历史查询只返回 `ACCEPTED`。这直接违反 S2 自己的“有效乱序进入历史、不得回退 Latest”契约，必须替换。
5. **修复本地 History Query 的语义缺口。** 当前只支持数值、无游标、无聚合接口，把分区内 `source_offset` 暴露为跨 Key 的 `revision`，并用查询范围之外的最大事件时间推断 `partial`。这些都不能作为稳定公开契约。
6. **保留 ClickHouse 固定历史权威，但重做 Rollup 质量边界。** 现有 1 分钟、15 分钟、小时 Rollup 没有把 `point_revision` 纳入分组，也把非 GOOD 值直接算入 AVG/MIN/MAX。HVAC 聚合必须显式携带版本、质量、完整度和 Counter 语义，不能只复制 ThingsBoard 的通用聚合函数。
7. **本地 Metric Domain 方向优于 ThingsBoard Calculated Field，但当前实现不能判定为完成。** Metric/Version/Binding/Dependency、数据库拒绝依赖环、受限表达式、质量/完整度、Run、Cross-store Publication、Job Lease 与 Backfill 是正确的 Domain 边界；但 `metric_series` 每次写入都把 `revision` 固定为 `1`，与“重算追加更高 Revision”文档直接冲突，且 ReplacingMergeTree 会在相同逻辑窗口发生同版竞争。
8. **吸收 ThingsBoard Calculated Field 的运行时模式，不吸收其业务边界。** Actor/Queue、RocksDB State、动态参数、Scheduled Reevaluation、Debug Event、租户限额、运行时链路去环值得参考；任意 Script、直接写 Attributes/Telemetry、跨实体 Propagation、Alarm 输出和 Rule Chain Side Effect 不得进入核心能源 Metric。
9. **保留本地受治理生命周期模型，但承认它尚未执行。** Lifecycle Policy、Legal Hold、Archive Manifest、Deletion Request、Tombstone 的模型强于 ThingsBoard 的简单 TTL；当前仓库没有消费这些表并实际清理 PostgreSQL/ClickHouse/Object Storage 的执行器，因此只能标为 Schema/Foundation，不能宣称 Retention 已完成。
10. **本地实时订阅边界总体保留。** 耐久 Subscription、短时 Capability、精确 Tenant/Site/Device/Keys、可撤销 Recovery Cursor、Business Revision 和 Outbox 比 ThingsBoard 的进程内 Subscription Map 更强；但部分订阅发布成功后失败会整事件重试，客户端必须按 Event/Revision 去重，且需要清理和负载测试证据。

因此 D04 的结论不是“本地胜出”，而是：**保留本地摄取、隔离和 Metric Domain 形状；立即替换 Current Authority、History Query、Metric Revision 和 Rollup Quality 的错误实现；选择性吸收 ThingsBoard 的查询产品能力、订阅限额、计算运行时和实际 TTL Worker。**

## 2. 固定证据基线

| 证据 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 全功能目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

上游行为以该固定提交的源码、测试、DDL 和配置为准；产品文档只用于解释公开入口，不覆盖源码事实。

主要 ThingsBoard 源码入口：

- [TelemetryController](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/controller/TelemetryController.java)；
- [BaseTimeseriesService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/timeseries/BaseTimeseriesService.java)；
- [SqlLatestInsertTsRepository](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/latest/sql/SqlLatestInsertTsRepository.java) 与 [SqlInsertTsRepository](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/sql/SqlInsertTsRepository.java)；
- [AttributesService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/attributes/AttributesService.java) 与 `BaseAttributesService`；
- `schema-entities.sql`、`schema-ts-psql.sql`、`schema-ts-latest-psql.sql`、`schema-timescale.sql`；
- [TimeseriesCleanUpService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/ttl/TimeseriesCleanUpService.java)；
- [CalculatedFieldType](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/common/data/src/main/java/org/thingsboard/server/common/data/cf/CalculatedFieldType.java)、[DefaultCalculatedFieldProcessingService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/cf/DefaultCalculatedFieldProcessingService.java) 与 `CalculatedFieldEntityMessageProcessor`；
- [DefaultTbLocalSubscriptionService](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/java/org/thingsboard/server/service/subscription/DefaultTbLocalSubscriptionService.java)。

主要本地证据：

- `services/mqtt-telemetry-adapter/internal/adapter/processor.go`；
- `services/telemetry-runtime-service/internal/telemetry/ingest_store.go`、`ingest.go`、`latest_cache.go`、`server.go`、`history.go`、`history_clickhouse.go`、`realtime.go`、`realtime_postgres.go`；
- `services/telemetry-query-service/internal/history/client.go`；
- `services/metric-engine-service/internal/metric/engine.go`、`postgres.go`、`clickhouse.go`、`jobs.go`、`scheduler.go`；
- `infra/s2-telemetry/clickhouse/init/001-telemetry-history.sql`、`003-telemetry-rollups.sql`、`004-counter-semantics.sql`、`005-metric-series.sql`；
- `infra/s1-registry/postgres/init/009c-metric-model-v2.sql`、`009d-data-governance-v2.sql`、`009g-object-storage-governance-v2.sql`、`009l-scheduler-job-v1.sql`；
- `contracts/ownership/s2-telemetry-ownership.v1.json` 与 `docs/architecture/data-architecture-v2-conformance.md`。

## 3. 参考项目功能与它解决的问题

### 3.1 Attributes

ThingsBoard 提供 Client/Shared/Server 三类属性作用域、按 Key 查询、保存、删除、版本变化和订阅通知。它解决“设备上报状态、服务端配置、共享配置和实体元数据如何统一读取与订阅”的问题。

优点是 API 一致、实体类型覆盖广、订阅链成熟。代价是 Attribute KV 同时承载配置、TTL、状态和扩展元数据，业务语义靠字符串 Key 约定，容易绕过明确的 Aggregate、Revision 和安全动作。

### 3.2 Latest 与 Time Series

ThingsBoard 支持 typed value、Latest、History、Key Dictionary、范围查询、排序、Limit、删除、删除后从历史重建 Latest，以及 MIN/MAX/AVG/SUM/COUNT/NONE 聚合。它解决通用 IoT 平台中高频 KV 时序的写入与多后端查询。

SQL Latest 默认只在新时间戳不早于现值时更新，能避免普通乱序回退。但 SQL History 以 `(entity,key,ts)` 唯一，同时间戳会更新旧行；History 和 Latest 分别异步保存，不具备本地摄取事务与 Outbox 的统一提交语义。

### 3.3 Storage Abstraction 与 TTL

ThingsBoard 对 SQL、TimescaleDB、Cassandra 和 EDQS 提供 DAO 抽象，并按配置运行时序清理。它解决多部署规模和多数据库选择问题。

该能力的代价是后端行为不完全一致：公开“单次写入 TTL”在固定版本文档中只对 Cassandra 生效；SQL 清理又分系统 TTL 分区处理和 Tenant/Customer 行删除，Tenant TTL 通过通用属性 Key `TTL` 读取。可插拔并不等于语义一致。

### 3.4 WebSocket Subscription

ThingsBoard 支持 Latest、Timeseries、Attributes、Entity Data 和 Alarm 的 WebSocket Subscription，并提供 Tenant/User 订阅限额、Session 清理、集群拓扑变化后重新推送订阅。

固定源码的主要索引是进程内 `ConcurrentMap<sessionId, subscriptions>` 与 `ConcurrentMap<entityId, subscriptions>`。重启后靠活动连接重新建立或拓扑重推，不是耐久、可撤销、带恢复游标的业务订阅。

### 3.5 Calculated Fields

固定版本提供七类 Calculated Field：

- `SIMPLE`；
- `SCRIPT`；
- `GEOFENCING`；
- `ALARM`；
- `PROPAGATION`；
- `RELATED_ENTITIES_AGGREGATION`；
- `ENTITY_AGGREGATION`。

它们解决实时参数依赖、滚动时序、跨实体关系计算、地理围栏、告警规则和派生值写回问题。运行时包含 Queue、Entity Actor、State、RocksDB 持久化、Scheduled Reevaluation、Debug Event、Script Test 和租户限额。派生链在消息中携带 Previous Calculated Field IDs，处理器遇到已出现的 ID 时跳过，从而阻止运行时递归环。

但输出可以直接保存为 Attributes/Telemetry、传播到其他实体，或送入 Rule Chain；Alarm 也被放入 Calculated Field 类型。这个通用边界不适合直接成为 HVAC 的原始遥测、能源 Metric、Alarm 和控制边界。

固定 CE 源码中的通用 `JobType` 只有 `DUMMY`。虽然 Job Manager 有失败任务 reprocess 框架，但没有 Calculated Field 历史重算 Job，因此不能把“历史计算/回填”列为该 CE 版本已实现能力。

## 4. Domain 模型对照

### 4.1 ThingsBoard

```text
Entity
  ├─ AttributeKv(scope, key, typed value, version, lastUpdateTs)
  ├─ TsKvEntry(key, ts, typed value)
  ├─ TsKvLatest(key, ts, typed value, version)
  └─ CalculatedField
       ├─ Configuration(type, arguments, output, schedule)
       ├─ Entity Context / Runtime State
       ├─ Links to referenced entities
       ├─ Debug Event
       └─ Output -> Attribute | Time Series | Alarm | Rule Chain
```

它是通用 IoT Entity 模型，优点是统一；缺点是 Point、单位、传感器、计数器、质量、校准、版本与业务归属不在核心时序行中。

### 4.2 HVAC Web

```text
Source Observation
  ├─ Source Event / Partition / Offset / Path
  ├─ Integration + External Entity + Telemetry Key
  ├─ Device Binding + Point Binding
  ├─ sampledAt + receivedAt
  ├─ Acceptance + Quality + Reasons
  └─ typed value + unit + payload digest

Current State
  ├─ PostgreSQL durable Device Business Snapshot
  ├─ Latest accepted working projection
  └─ Redis rebuildable low-latency projection

Metric
  ├─ Metric Version
  ├─ Binding(subject, granularity, source definition)
  ├─ Dependency(POINT | METRIC | EXTERNAL)
  ├─ Calculation Run
  ├─ Metric Result + provenance + quality + completeness
  └─ Cross-store Publication / Job / Backfill

Data Governance
  ├─ Lifecycle Policy
  ├─ Legal Hold
  ├─ Archive/Backup Manifest
  ├─ Deletion Request
  └─ Tombstone
```

该模型更复杂，但复杂度来自 HVAC 所需的可追溯性、单位/计数器语义、跨存储一致性和删除治理，不是为了复制通用平台。

## 5. 核心流程对照

### 5.1 ThingsBoard 时序写入

```text
REST / Transport / Rule Engine
  -> TimeseriesService
     -> History DAO future
     -> Latest DAO future
  -> subscription/rule notifications
```

Latest SQL 按时间戳防回退，History 支持多后端。但 History 与 Latest 没有单一业务提交点，同时间戳冲突会覆盖历史值。

### 5.2 本地 S2 摄取

```text
MQTT Adapter
  -> canonical Observation
  -> SERIALIZABLE PostgreSQL transaction
     -> advisory lock source partition/event
     -> source position + duplicate/out-of-order decision
     -> Device/Point binding and policy validation
     -> source observation/evidence
     -> historical outbox
     -> accepted latest working projection
     -> presence + device snapshot
  -> commit
  -> ClickHouse history relay
  -> realtime/current projections
```

该流程应 `KEEP`。它把“是否接受事实”“事实质量”“是否推进 Latest”拆开，能保存有效乱序和同时间戳不同 Source Event。

### 5.3 本地 Current 读取现状

```text
Public read
  -> PostgreSQL EvaluateAndRead
  -> Redis PutIfNewer(full snapshot)
  -> Redis Get
  -> revision check
  -> response
```

这不是缓存命中路径。Redis 故障反而会让已从 PostgreSQL 得到的有效读取失败。目标流程必须二选一：

- PostgreSQL 直接服务，Redis 暂时退出公开读取；或
- Redis read-first，并以 Business Revision、Freshness Evaluation Deadline、短时 Capability 和明确的 PostgreSQL 修复路径保证正确性。

无论选择哪一种，Redis 都是 Projection，不是耐久业务权威。

### 5.4 Calculated Field / Metric

ThingsBoard：

```text
Telemetry/Attribute/Timer
  -> CF Queue
  -> Entity Actor + persisted state
  -> calculate
  -> immediate write | Rule Chain | Alarm | propagation
  -> linked CFs with previous-ID loop guard
```

HVAC Web：

```text
Scheduled/Manual/Dependency/Backfill Job
  -> load released Metric Version + Binding
  -> read Point/Metric dependencies
  -> calculate with quality/completeness
  -> PostgreSQL Run + Cross-store Publication
  -> ClickHouse Metric Result
  -> reconcile publication
  -> Redis Latest projection + domain outbox
```

本地流程的 Domain 边界更合适，但 Metric Result Revision 和存储键目前不满足自己的历史重算承诺。

## 6. 关键代码结构裁决

### 6.1 ThingsBoard 值得吸收

| 结构 | 价值 | 裁决 |
| --- | --- | --- |
| Controller -> Service -> backend-specific DAO | API 与存储实现分离 | `ADAPT`，但只保留当前实际使用的 PostgreSQL/ClickHouse，不做无需求的多后端框架 |
| Latest 按时间戳条件更新 | 防止普通乱序回退 | `ADOPT` 为测试行为；本地继续使用更强的 Business Revision/Source Position |
| 范围、排序、Limit、Aggregation、Calendar Interval | 成熟的时序查询产品能力 | `ADAPT`，加入 Point Type、Unit、Quality、Completeness、Timezone 和 Counter Policy |
| 删除后重建 Latest | 修正历史后恢复当前值 | `ADAPT` 为耐久 Correction/Deletion Workflow，不开放任意直接删除 |
| Subscription Rate Limit/Session Cleanup | 防止单 Tenant/User 消耗失控 | `ADOPT` 到本地耐久 Subscription API |
| CF Queue/Actor/State/Debug | 隔离实体状态、可调试、可限额 | `ADAPT` 到 Metric/Rule 执行器 |
| Previous CF ID 链路去环 | 阻止派生写回递归 | `ADOPT` 为 Execution Causation Chain，同时保留发布时 DAG 静态拒环 |
| 实际运行的 TTL Scheduler | 策略最终必须被执行 | `ADAPT` 到受 Legal Hold/Archive/Tombstone 约束的 Lifecycle Worker |

### 6.2 本地应保留

| 结构 | 证据 | 裁决 |
| --- | --- | --- |
| Observation Identity + Source Position | 重复/重放/乱序有独立证据 | `KEEP` |
| Acceptance 与 Quality 分离 | 可接受但 PARTIAL/STALE 的事实不被误删 | `KEEP` |
| Binding Quarantine | 未知/冲突身份不自动创建或误绑定 | `KEEP` |
| Tenant/Site/Device/Point 直接进入历史行 | 分析隔离和 Lineage 不依赖二次 Entity Join | `KEEP` |
| PostgreSQL Outbox -> ClickHouse | 失败可重试并保留耐久意图 | `KEEP` |
| Counter Revision/Unit Boundary | 不跨更换、校准或单位边界算差值 | `KEEP` |
| Metric Version/Binding/Dependency DAG | 语义和有效期显式、数据库拒绝依赖环 | `KEEP` |
| 受限 Go AST Expression | 无任意网络、文件或脚本副作用 | `KEEP` |
| Durable Realtime Subscription/Recovery Cursor | 权限、Scope、Revision 和恢复边界显式 | `KEEP` |
| Lifecycle Policy + Legal Hold + Tombstone | 满足受治理删除和可证明清理 | `KEEP` 模型，执行器仍需实现 |

## 7. 本地源码级反向审查

### 7.1 摄取与历史投影

`AcceptObservation` 在 Serializable Transaction 内锁定 Source Partition/Event，解析 Binding、执行质量策略、写 Source Observation、History Outbox、Latest Working Projection、Presence 与 Device Snapshot。Seen Event、同 Offset 和低 Offset 不推进 Source Position；有效但时间落后的 Observation 进入历史且不替换 Latest。

这比 ThingsBoard `(entity,key,ts)` Upsert 更能保存证据，应保留。

`ClickHouseHistorySink` 名义上接收 Batch，但每条 Observation 发一个 HTTP Insert，最多 16 并发。正确性由 Outbox 与单条 `insert_deduplication_token` 支撑，吞吐和连接开销没有负载证据。不得凭直觉立即换掉幂等语义；P1 应先建立基准，再采用真正批量且可重试的 Batch Identity 或可证明的 Observation 去重表设计。

### 7.2 Current Snapshot 与 Redis

Redis Hash 以 `business_revision` CAS 拒绝同版/旧版，并能从 PostgreSQL 全量重建，这是合格 Projection 的基础。

问题在于公开单读、批读都先调用 PostgreSQL `EvaluateAndRead`，随后 `PutIfNewer` 再 `Get`；Command Verifier 更直接只读 PostgreSQL。当前实现没有 Redis 命中路径，却让 Redis 成为额外故障点。`latestTelemetry.implementationStatus=PASS` 和 `v2Authority=redis-rebuildable-latest-cache` 没有源码支持。

此外 Presence/Freshness 会随墙钟变化，即使没有新遥测，快照也可能过期。任何 read-first Cache 必须保存下一次 Evaluation Deadline 或由 Scheduler 主动推进状态；只比较 Business Revision 不能防止时间驱动状态陈旧。

### 7.3 ClickHouse Raw 与 Rollup

`telemetry_history.observations` 使用 MergeTree，按月分区，排序键含 Tenant/Site/Point/Sensor/Device/Key/SampledAt/ObservationID。它保留 typed values、Point Revision、Counter Policy、Acceptance、Quality 和 Payload Digest，适合作为历史权威。

现有 `numeric_1min`、`numeric_15min` 和 `numeric_hourly` 存在两项实质问题：

1. 分组键没有 `point_revision`，同一 Point 语义或校准版本变化后仍可能聚合在同一 Bucket；
2. `ACCEPTED`/`OUT_OF_ORDER` 中所有数值直接进入 AVG/MIN/MAX，没有按 Quality Policy 分层，也没有输出 GOOD/PARTIAL/STALE 计数和 Completeness。

Counter 视图已经显式处理 Revision Boundary、Unit Boundary、Reset、Rollover 和 Invalid Decrease，方向正确。但 Metric Engine 使用 `RESET`/`METER_REPLACEMENT`，而 Canonical Counter DDL 使用 `RESET_TO_ZERO`/`INVALID`，存在枚举漂移，必须统一为一个 Domain Vocabulary。

### 7.4 公开历史查询

当前查询有以下源码事实：

- 只返回 `value_number`，STRING/BOOLEAN/JSON 虽被存储却不可查询；
- 只过滤 `acceptance_status='ACCEPTED'`，丢弃有效 `OUT_OF_ORDER`；
- 每 Key 只取最新 N 条，没有 Cursor，旧数据无法继续翻页；
- `source_offset` 被命名为 `revision`，但 Offset 只在 Source Partition 内有序；
- `DatasetRevision` 是静态配置加最大 Source Offset，不是耐久数据集版本；
- Watermark 查询不受请求时间范围约束，并用 `watermark < query.to` 判定 Partial，未来/范围外数据会掩盖范围内缺口，稀疏或历史窗口也可能被误判；
- 没有公开聚合、质量选择、Point Revision 或 Counter-aware Query。

该模块不能按“历史查询已完成”验收。

### 7.5 Realtime

本地订阅把 Principal、Issuer、Session、Tenant、Device、Keys、Scope Digest、Policy Revision、Channel、Status 与 Expiry 持久化；Token 最长五分钟，Recovery Cursor 绑定身份、Scope、Business Revision、Transport Epoch/Offset，并可过期和撤销。

Relay 对一个 Event 的所有 Active Subscription 逐个发布，全部成功后才 Mark Published。中途失败会重试整个 Event，因此先成功的订阅可能重复收到消息。该行为可以接受为 At-least-once，但 Publication 必须携带稳定 Event ID/Business Revision，客户端和 Transport 必须按其去重；还需要 Subscription/Cursor 过期清理与部分发布负载测试。

### 7.6 Metric Engine

本地已实现：

- Released Metric Version/Binding 与有效期读取；
- `POINT`、`METRIC`、`EXTERNAL` Dependency；
- 数据库级 Metric Dependency Cycle 拒绝；
- `IDENTITY/SUM/AVG/MIN/MAX/FIRST/LAST/DELTA/COUNT/DURATION/INTEGRAL/DIFFERENCE/RATIO/EXPRESSION/MODEL`；
- 受限表达式和未注册 MODEL fail-closed；
- Quality/Completeness 汇总；
- Calculation Run、Cross-store Publication、ClickHouse 存在性 Reconciliation；
- Scheduler Job Claim/Lease/Renew/Retry/Dead、单 Backfill 并发和取消；
- Result Domain Event 与下游 Delivery Intent。

但以下冲突使它仍是“待验证实现”：

1. `InsertMetric` 每次把 `revision` 写死为 `1`；
2. `ReplacingMergeTree(revision)` 的排序键不含 Result ID、Metric Version 或 Binding Version，而同一 Metric Code/Subject/Window 的重算仍为同版；Merge 后保留哪条无法表达业务上的“更高 Revision”；
3. 文档声称历史重算追加更高 Revision，源码没有分配该 Revision；
4. `ReadPoint` 对所有可接受质量的值先计算 Aggregate，再用 GOOD Count 标记 PARTIAL；宽松策略下计算值本身仍可能混入 STALE/PARTIAL 数据；
5. Counter Decrease Mode 与 Canonical Point/ClickHouse 枚举不一致；
6. 当前测试无法形成绿色闭环：依赖下载被网络阻塞，Telemetry Query 测试还使用已从类型中移除的 `ActingOrganizationID`，说明工作树存在契约漂移。

目标应把 Metric Result 设计为 Append-only Result Fact，以 `result_id` 保留所有重算；另建由 PostgreSQL 原子分配单调 Business Revision 的 Current Metric Projection，或使用明确的 `argMax(revision, calculated_at)` 视图。不能继续依赖硬编码 `revision=1` 的 ReplacingMergeTree 偶然选行。

### 7.7 生命周期

PostgreSQL 已有版本化 Lifecycle Policy、Legal Hold、Deletion Request、Archive/Backup Manifest 和 Tombstone，并通过约束防止重叠策略、无归档删除和 Legal Hold 下批准。

源码搜索只发现 DDL 与数据库测试，没有服务读取 `data_lifecycle_policies` 并执行 ClickHouse Partition/Row 删除、对象归档、状态推进和失败重试。模型强于 ThingsBoard，但能力尚未落地。

## 8. 客观裁决矩阵

| 功能/行为 | ThingsBoard | HVAC Web 当前 | 裁决 |
| --- | --- | --- | --- |
| Typed telemetry storage | Bool/String/Long/Double/JSON | Raw 全类型，公开查询仅 Number | `ADAPT` TB 查询完整性；`REPLACE` 本地 numeric-only API |
| Same timestamp | SQL 冲突覆盖 | Observation ID 保留多事实 | `KEEP` 本地；`REJECT` 覆盖 |
| History/Latest consistency | 两条异步保存链 | 单摄取事务 + Outbox | `KEEP` 本地 |
| Latest 防回退 | 按 timestamp 条件 Upsert | Source Position + sampledAt + Business Revision | `KEEP` 本地更强语义 |
| Generic direct entity telemetry write | REST 可直接写多类 Entity | Adapter/Binding/Policy 入口 | `REJECT` 用于原始现场事实；手工修正另建 Correction Fact |
| Attribute scopes | Client/Shared/Server | 分散在 Registry/Config/State | `ADAPT` 订阅体验；`REJECT` 通用 KV 成为业务模型 |
| Range/aggregate query | 成熟，支持 interval/timezone/agg/order/limit | 有限 raw numeric latest-N | `ADAPT`，按 Point/Quality/Counter 语义重做 |
| Delete + rewrite latest | 直接 API 支持 | 治理模型，无执行链 | `ADAPT` 为批准、审计、Hold-aware Correction/Delete Saga |
| Storage pluggability | SQL/Timescale/Cassandra/EDQS | PostgreSQL + ClickHouse + Redis 固定职责 | `KEEP` 固定职责；`REJECT` 无需求的通用多后端抽象 |
| Per-write TTL | 后端行为不一致 | 不接受写入者自定 TTL | `REJECT`；Retention 只来自版本化 Lifecycle Policy |
| Actual TTL worker | 已有 Scheduler/Cleanup | 只有治理表和约束 | `ADAPT` Worker Pattern；本地必须补执行器 |
| Redis current authority | 上游 Latest 有独立 DAO/Cache | PG-first 后写再读 Redis | `REPLACE` 本地权威声明与路径 |
| WebSocket subscriptions | 成熟类型、限额，主要进程内 | 耐久、短时能力、Recovery Cursor | `KEEP` 本地；`ADOPT` 上游限额/清理经验 |
| CF types | 7 类通用计算/传播/告警 | 专门 Metric Engine | `KEEP` Metric Domain；选择性 `ADAPT` Actor/State/Debug |
| Arbitrary script | TBEL Script | 受限算术 AST | `KEEP` 受限表达式；`REJECT` 核心 Metric 任意脚本 |
| CF output to telemetry/attribute/rule chain | 支持 | Metric Result/Domain Event | `REJECT` 作为核心能源计算边界 |
| Dependency cycle handling | 消息链 Previous IDs 运行时去环 | DB 静态 DAG 拒环 | `ADOPT` 双层防护：发布时 DAG + 运行时 Causation Chain |
| Historical CF reprocessing | 固定 CE 未实现 CF Job | Backfill Job 骨架已存在 | `KEEP` 本地方向，但需通过运行/幂等测试 |
| Metric revision | 不等同本地 Metric Result | 固定写 `1`，与文档冲突 | `REPLACE`，原子分配单调 Revision并保留全部 Result Facts |
| Rollup semantics | 通用 AVG/MIN/MAX 等 | 有 Counter 专用语义，Gauge Rollup 忽略质量/版本 | `KEEP` Counter 边界；`REPLACE` Gauge Rollup |
| Tenant provenance in time-series row | 通过 Entity 所属关系解析 | Tenant/Site/Device/Point 直接存储 | `KEEP` 本地 |

## 9. 异常与边界处理

### 9.1 必须保留的失败关闭

- 未知、冲突、过期或隔离 Binding 不得自动创建设备或写入 Latest；
- 类型、单位、范围和未来时钟非法时拒绝或隔离；
- 重复/重放不推进 Source Position；
- 乱序不回退 Latest，但有效事实进入 History；
- Metric 必需依赖缺失、Strict Quality 非 GOOD、未注册 Model、依赖环时失败；
- Legal Hold 或必需 Archive Evidence 缺失时不得删除；
- Redis/Realtime/ClickHouse 投影失败不得篡改已提交的 PostgreSQL 业务决定。

### 9.2 必须新增或证明的边界

- 同 `sampled_at`、不同 Source Event 的查询与聚合顺序；
- Source Partition 切换、Offset 重置和 Gateway Replacement；
- Gauge/Counter 在 Point Revision、Unit、Calibration 边界上的聚合；
- 稀疏数据、无样本窗口、DST、Site Timezone Business Day；
- History Cursor 在并发新写入时的稳定性；
- Metric Backfill 与 Online Job 同窗竞争、取消、重试和 Result Revision 分配；
- Realtime 部分 Fan-out 失败后的重复消息；
- Retention 在 Legal Hold、Archive 失败、Partition 混合 Tenant 和恢复后重新删除时的行为。

## 10. 值得吸收的 Pattern

1. **分层查询 API。** Raw、Latest、Aggregate、Entity Data Query 分开，统一类型、范围、Limit 和错误模型。
2. **Calendar-aware Interval。** Week/ISO Week/Month/Quarter 和 Timezone 不是固定毫秒除法。
3. **Tenant/User Subscription Quota。** 耐久订阅也需要创建频率、总数、Keys、Fan-out 和带宽限制。
4. **Stateful Calculation Runtime。** 每 Subject 隔离状态、Queue 分区、状态持久化、定时重评和 Debug Evidence。
5. **静态与运行时双重去环。** 配置发布时拒绝 DAG 环，消息传播时携带 Causation IDs。
6. **删除后 Current 修复。** 删除/纠正历史必须明确重算 Current，而不是留下已不存在事实的 Latest。
7. **实际 Housekeeping Loop。** Retention Policy 必须有 Claim、Lease、Batch、Retry、Metrics 和 Audit，不能停留在 DDL。
8. **API Limit 是 Domain Guardrail。** Query Points、Intervals、CF Arguments、Schedules、Subscriptions 和 Debug Event 都需要 Tenant Policy。

## 11. 不适合 HVAC Web 的部分

- Generic Entity Telemetry Write 会绕过 Integration、Binding、Point、Unit、Quality 和 Source Evidence。
- Generic Attribute KV 无法替代 Desired/Reported Configuration、Registry Metadata、Presence、Metric、Alarm 或 Lifecycle Policy。
- `(entity,key,ts)` Upsert 会丢失同时间戳多源事实和原始证据。
- History/Latest 分离异步提交不能证明当前值来自已落地历史事实。
- 默认把 typed value 字符串化会弱化前端和规则的类型契约。
- 任意 Script 和 Rule Chain Side Effect 不应参与核心能源结算、优化或控制输入。
- Calculated Field 直接产生 Alarm 混淆了计算、规则判断、Alarm Lifecycle 和审计责任。
- 跨实体 Propagation 不应偷偷复制配置或业务状态；配置分发必须有 Version、Target、Ack、Expiry 和冲突语义。
- 通用属性 Key `TTL` 和后端特有单次 TTL 不满足受治理保留策略。
- 为未来可能使用 Cassandra/TimescaleDB 建通用存储框架没有当前部署证据，只会增加语义漂移。
- 进程内 WebSocket Subscription 不能替代本地要求的可撤销、可恢复、精确 Scope 的耐久订阅。

## 12. 映射到目标设计

| 目标能力 | 权威/Owner | 目标行为 |
| --- | --- | --- |
| Raw Observation | ClickHouse `telemetry_history.observations`，摄取决定在 PostgreSQL | Append-only、Observation Identity、typed value、Point Revision、Acceptance/Quality |
| Current Device State | PostgreSQL durable Business Snapshot | 时间驱动重新评估；Redis 仅为可重建 Projection |
| Redis Latest | S2 Projection | read-first 只有在 Deadline/Revision/Failover 契约和负载收益通过后启用 |
| Public Raw History | Telemetry Query Service | 全类型、稳定 Cursor、有效乱序、明确排序、Point/Quality/Revision 信息 |
| Aggregate History | Telemetry Query/Analytics | Point-type-aware、Quality Policy、Completeness、Site Timezone、Revision Boundary |
| Attribute-like Data | 各 Bounded Context | Registry Metadata、Desired/Reported Config、Runtime State 分开，不建万能 KV |
| Metric Definition | PostgreSQL Registry | Versioned、Released、有效期、Dependency DAG、Unit/Type/Quality Policy |
| Metric Result Fact | ClickHouse Append-only | 每次 Run/Result/Provenance 保留，不由 ReplacingMergeTree 擅自覆盖 |
| Current Metric Result | 显式 Projection | PostgreSQL 分配单调 Revision，View/Cache 选择最高已提交 Revision |
| Metric Execution | Metric Engine | Job Lease、Backfill、Strict/Estimated Policy、Debug Evidence、Causation Chain |
| Alarm/Rule | Rule Engine/Alarm Domain | 只消费事实和 Metric Result，不由 Calculated Field 偷渡 Alarm Lifecycle |
| Retention/Delete | Data Governance Worker | Policy + Hold + Archive + Tombstone + Audit + Partition-aware Execution |
| Realtime | Durable Subscription + Centrifugo Transport | At-least-once、Event/Revision 去重、短时 Capability、可撤销 Recovery |

## 13. 文档与源码冲突

1. `data-architecture-v2-conformance.md` 声称历史重算写入更高 Metric Revision；`InsertMetric` 实际始终写 `revision=1`。这是错误声明，必须以源码事实纠正。
2. 同一文档把 Redis 描述为对外 Current Snapshot 权威并称读路径自修复；源码是 PostgreSQL-first、Redis write-then-read。可重建 Cache 不能被定义为耐久 Authority。
3. `s2-telemetry-ownership.v1.json` 同时把 Redis 标为 `implementationStatus=PASS`，又把 `currentState` 标为 `PARTIAL` 并称写/读路径未完成，内部自相矛盾。
4. Ownership 契约要求保留有效乱序历史；公开 History Query 只查 `ACCEPTED`，与契约直接冲突。
5. Raw/Rollup 接受 `OUT_OF_ORDER`，公开查询拒绝它，三个读取层语义不一致。
6. 文档称 Metric Calculation Service “尚未完成”；源码已有 Engine、Scheduler、Job、Publication 和 Reconciliation。更准确状态应为 `PARTIAL/NOT_PRODUCTION_PROVEN`，并列出 Revision、Quality、测试和运行证据缺口。
7. Lifecycle 文档描述了 Policy/Hold/Archive/Tombstone，但仓库没有实际 Executor；不得将 Schema Foundation 写成运行能力。
8. Telemetry Query 测试仍构造已删除的 `ActingOrganizationID`，当前测试契约与生产类型漂移，不能用其他通过测试掩盖。

## 14. 实施优先级与验收门槛

本文只冻结后续实施顺序；运行时修改应拆为独立实施票。

### P0：纠正数据权威和会丢事实的行为

1. 将 PostgreSQL Durable Business Snapshot 定义为 Current Authority；Redis 改为 Projection。删除矛盾文档和 Contract 字段，不增加兼容层。
2. 在有真实负载测试前，选择 PostgreSQL 直接读或实现严格 read-first Redis；禁止保留现有 PG-read -> Redis-write -> Redis-read 路径。
3. History Query 返回有效 `OUT_OF_ORDER`，并把 Acceptance、Quality、Reasons、Observation ID、Source Position 明确建模。
4. 删除 `source_offset == revision` 契约；建立稳定 Cursor 和真正 Dataset/Projection Watermark。
5. 重做 Metric Result Revision：Append-only Fact + 单调 Current Revision；保留每次 Backfill/Recalc 的 Result/Run/Provenance。
6. 修复 Telemetry Query 的 `ActingOrganizationID` 测试漂移，使目标包重新编译。

验收门槛：同时间戳多 Observation 不丢失；乱序能查询且不回退 Current；Redis 不可用时行为符合明确契约；相同 Metric Window 连续三次重算可稳定查询全部 Result 和唯一 Current Revision；目标服务测试绿色。

### P1：完成可用查询、聚合和生命周期

1. 增加 STRING/BOOLEAN/JSON Raw Query 与稳定 Cursor Pagination。
2. 建立 Counter/Gauge/State 分开的聚合 API；Rollup Key 纳入 Point Revision，并输出 Quality Count/Completeness。
3. Watermark 改为 Projector/Ingest Commit Progress，不再用范围外最大 Event Time 猜测完整性。
4. 统一 Counter Decrease Vocabulary，移除 `RESET`/`METER_REPLACEMENT` 与 `RESET_TO_ZERO`/`INVALID` 漂移。
5. 实现 Lifecycle Worker：Claim/Lease、Legal Hold、Archive Proof、ClickHouse Partition/Mutation、Object Delete、Tombstone、Retry、Audit 和 Metrics。
6. 为 Realtime 部分 Fan-out、Cursor 清理、重连和配额增加端到端测试。
7. 对 History Relay 做负载基准；只有证明单条 HTTP Insert 成为瓶颈后，实施可重试真 Batch。

验收门槛：聚合不会跨 Point Revision/Unit；质量策略有 Golden Dataset；DST/Timezone/空窗口有测试；Hold 下删除必然失败；归档失败不删除；Worker 重启可恢复；Realtime 重复可由客户端稳定去重。

### P2：选择性扩展计算体验

1. 吸收 Calculated Field 的 Debug Event、参数预览、Scheduled Reevaluation、执行配额和 Causation Chain。
2. 根据产品证据增加 Geofencing 或 Related Entity Aggregation，但仍输出明确 Domain Fact，不直接写原始遥测或 Alarm。
3. Entity Data Query、通用 Dashboard Aggregation 和 EDQS 在 Dashboard/Visualization 审查域继续裁决，不提前建设通用查询平台。

验收门槛：任何新增计算类型都有版本、单位、质量、依赖、资源上限、回填、调试和失败关闭证据；核心能源/结算/控制计算不允许任意脚本副作用。

## 15. 本轮最终裁决

- 本地 Observation/Source Position、Binding Quarantine、Acceptance/Quality、有效乱序、PostgreSQL Outbox、ClickHouse Raw History、Counter Boundary、Metric Version/DAG 和 Durable Realtime 获得 `KEEP`。
- ThingsBoard 的范围/聚合查询、Calendar Interval、Subscription Limit、CF Queue/Actor/State/Debug、Causation 去环、Delete 后 Latest 修复和实际 TTL Worker 获得 `ADOPT/ADAPT`。
- ThingsBoard 的 Generic Direct Telemetry Write、同时间戳覆盖、History/Latest 分离提交、Generic Attribute 业务建模、后端特有 TTL、任意 Script Side Effect、CF 直接 Alarm/Propagation 获得 `REJECT/REPLACE`。
- 本地 Redis Current Authority 与 write-then-read 路径、History 丢弃乱序、numeric-only/no-cursor Query、伪 Revision/Watermark、Gauge Rollup 忽略版本/质量、Metric `revision=1` 和无 Lifecycle Executor 获得 `REPLACE` 或“未实现”。
- 固定 PostgreSQL + ClickHouse + Redis 职责获得 `KEEP`；无真实部署需求的通用多存储抽象获得 `REJECT`。
- ThingsBoard CE 固定版本没有 Calculated Field 历史 Job；本地 Backfill 方向可保留，但只有运行、幂等、Revision 和测试证据完成后才能宣称实现。

该裁决完成 D04 对比，不完成整个 ThingsBoard 全功能审查；其余审查域和最终跨域反向审查继续按总路线图推进。

## 16. 本轮验证结果

执行日期：2026-08-17。

| 验证 | 结果 |
| --- | --- |
| CodeGraph 对 Telemetry Adapter、Runtime、Latest、Realtime、Metric Engine 的调用链审查 | 完成 |
| ThingsBoard 固定提交 Telemetry/Attributes/Latest/Aggregation/TTL/CF/WS 源码与 DDL 审查 | 完成 |
| 本地 ClickHouse Raw/Rollup/Metric DDL 审查 | 完成 |
| 生命周期执行器搜索 | 仅发现 DDL 与测试；未发现运行时 Executor |
| `go test` 首次执行 | Sandbox 无权访问 Windows Go Build Cache |
| 提权后 `go test ./services/telemetry-runtime-service/... ./services/telemetry-query-service/... ./services/metric-engine-service/...` | 未通过：`go-redis/v9` 下载网络失败；Telemetry Query 测试还因 `ActingOrganizationID` 已删除而编译失败；`internal/cube` 通过 |
| 运行时产品代码修改 | 无；本票只新增裁决文档 |
