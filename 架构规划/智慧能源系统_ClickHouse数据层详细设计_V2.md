# 智慧能源系统数据层研发契约 V2 — PostgreSQL + ClickHouse

# 智慧能源系统研发契约 V2

> 数据层正式基线：PostgreSQL + ClickHouse + Redis + MQTT

## 1. 架构变化

V1：

```text
PostgreSQL + 旧时序数据层 + Redis + MQTT
```

V2：

```text
PostgreSQL + ClickHouse + Redis + MQTT
```

职责：

```text
PostgreSQL
= Business / Metadata / Transaction

ClickHouse
= Telemetry / Rollup / Metric Series / Historical Analytics

Redis
= Latest / Runtime State

MQTT
= Edge / Cloud Messaging
```

## 2. 目录

```text
docs/
  ClickHouse时序数据模型与集群详细设计.md

postgres/
  schema.sql
  migrations/
    000001...
    000004...

clickhouse/
  001_single_node_dev.sql
  010_cluster_database.sql
  020_cluster_raw_tables.sql
  030_cluster_rollups.sql
  040_cluster_metric_series.sql
  050_cluster_distributed_tables.sql
  060_postgres_dictionaries.example.sql
  070_backup_restore.example.sql
  config/
    cluster.example.xml
    keeper.example.xml
    storage_policy.example.xml

schemas/
examples/
openapi.yaml
ADR/
VALIDATION.md
```

## 3. 开发环境

先执行：

```text
postgres/migrations
```

再对单节点 ClickHouse：

```text
clickhouse/001_single_node_dev.sql
```

第一条 Vertical Slice：

```text
Meter
→ Edge
→ MQTT
→ Go Worker
→ Redis Latest
→ ClickHouse Raw
→ History API
→ React Trend
```

## 4. 生产环境

生产 DDL 执行顺序：

```text
010_cluster_database.sql
020_cluster_raw_tables.sql
030_cluster_rollups.sql
040_cluster_metric_series.sql
050_cluster_distributed_tables.sql
```

前提：

- energy_cluster 已配置
- Keeper 已可用
- 所有 ClickHouse 数据节点已配置相同 hot_warm_cold Storage Policy
- `{shard}` / `{replica}` macros 已按节点设置

## 5. 重要约束

1. 不允许普通用户直连 ClickHouse。
2. Latest 默认走 Redis。
3. Raw History 才走 ClickHouse。
4. Metric Definition 在 PostgreSQL，Metric Series 在 ClickHouse。
5. Command/Alarm Current State 不迁入 ClickHouse。
6. Telemetry Worker 必须批量写入。
7. MQTT 重复消息在 ingestion boundary 做幂等。
8. Raw 不依赖 ReplacingMergeTree 做 Exactly Once。
9. Partition 主要用于生命周期，不按 Device/Site 创建海量 Partition。
10. Shard 默认按 `cityHash64(site_id)`。

## 6. Production Baseline

初始 HA：

```text
1 shard × 2 replicas
3 Keeper
```

规模扩大：

```text
2+ shards × 2 replicas
3 Keeper
```

Shard 数量必须由容量测试决定，而不是提前堆节点。

## 7. Retention 初始值

Numeric Raw：

```text
7d hot
90d warm
365d cold-queryable max
```

Battery Cell：

```text
3d hot
30d warm
180d raw retention
```

1min Rollup：

```text
2y
```

Metric Series：

```text
10y initial
```

所有值均需按合同/法规/容量重新校准。


---

# 智慧能源系统 ClickHouse 时序数据模型与集群详细设计

> 版本：V1.0  
> 设计日期：2026-08  
> 技术基线：PostgreSQL + ClickHouse + Redis + MQTT  
> ClickHouse 建议生产版本基线：26.3 LTS 或后续经验证的 LTS  
> 本文目的：替代原时序层设计，并作为研发契约 V2 的 ClickHouse 数据基线。

---

# 1. 技术定位

最终数据职责固定为：

```text
PostgreSQL
├── Tenant / IAM
├── Site / Space / Asset
├── Device / Point / Template
├── Energy Topology
├── Metric Definition / Binding
├── Alarm / Rule
├── Command / Safety Policy
├── Tariff / Carbon Factor
└── Audit / Configuration

ClickHouse
├── Raw Telemetry
├── State History
├── Battery Cell High-density Telemetry
├── Telemetry Rollup
├── Metric Series
├── Data Quality Statistics
└── Large-scale Historical Analytics

Redis
├── Latest Point
├── Gateway State
├── Device State
├── Rule Runtime State
└── Realtime Cache

Object Storage
├── ClickHouse Backup
├── Cold Storage
├── Parquet Archive
└── AI Dataset
```

ClickHouse 不承担：

- 用户事务
- 权限主数据
- Device 主数据
- Alarm 当前状态事务
- Command 状态机事务
- Safety Policy 主数据
- Tariff 主数据

这些仍由 PostgreSQL 负责。

---

# 2. 为什么采用 ClickHouse

智慧能源数据具有以下特点：

```text
高写入
Append-heavy
时间范围查询
大量聚合
历史跨度长
跨Site分析
数据量持续增长
```

ClickHouse MergeTree 家族针对高摄入率和巨大数据量设计，数据物理排序主要由 `ORDER BY` 决定；官方当前文档也明确建议时序工作负载把时间和常用过滤维度纳入排序键。

因此 ClickHouse 负责：

> “大量历史事实数据 + 快速分析”。

PostgreSQL 负责：

> “强事务业务状态 + 主数据”。

---

# 3. 数据模型总原则

不采用完全 Wide，也不采用完全 Long。

采用：

# 通用窄表 + 高密度专表

```text
普通设备
Meter / PV / PCS / HVAC / Sensor
        ↓
telemetry_numeric
telemetry_state

超高密度设备
Battery Cell
        ↓
battery_cell_telemetry
```

原因：

1. Standard Point 模型仍然是全平台统一语义。
2. 普通设备的点位类型变化较多，窄表更容易治理。
3. ClickHouse 列式存储对重复 ID / LowCardinality 字段压缩能力强。
4. 电芯等高密度通道若每个 Value 独立一行，行数会极度放大，因此使用专表。
5. 不在第一版为每一种设备建立独立 Wide Table，避免 Schema 爆炸。

---

# 4. ID 策略

PostgreSQL 内部主键继续：

```text
BIGINT
```

ClickHouse 使用：

```text
UInt64
```

保存：

```text
tenant_id
site_id
gateway_id
device_id
point_id
asset_id
```

不在高频 Raw Telemetry 中反复存：

```text
UUID String
设备名称
站点名称
资产名称
```

Public UUID 仍用于 API。

查询需要名称时：

- Go Service 从 PostgreSQL 获取；
- 或 ClickHouse Dictionary 缓存低频元数据。

---

# 5. Numeric Telemetry

核心表：

```text
telemetry_numeric
```

用于：

- TELEMETRY numeric
- COUNTER
- numeric SETTING readback

核心列：

```text
event_time
ingest_time
tenant_id
site_id
gateway_id
device_id
point_id
device_type
point_code
point_kind
data_class
value
quality
source_seq
replay
```

其中：

```text
point_kind:
1 = TELEMETRY
2 = COUNTER
3 = SETTING
```

```text
data_class:
0 = STANDARD
1 = IMPORTANT
2 = CRITICAL
3 = LOW_VALUE
```

Quality 与平台标准保持一致：

```text
0  GOOD
10 BAD
20 TIMEOUT
30 OFFLINE
40 MISSING
50 STALE
60 OUT_OF_RANGE
70 PARSE_ERROR
80 ESTIMATED
90 MANUAL
```

---

# 6. 为什么 Raw 表仍然保存 site/device/point

理论上只保存：

```text
event_time + point_id + value
```

最省字段。

但 ClickHouse 是分析数据库，我们希望常见查询：

```text
WHERE tenant_id = ?
AND site_id = ?
AND device_id = ?
AND event_time BETWEEN ...
```

能直接利用物理排序，而不是每次先去 PostgreSQL JOIN。

因此采用适度反规范化：

```text
tenant_id
site_id
device_id
point_id
```

全部写入 Raw。

由于这些整数列高度重复，列式压缩成本通常比行存数据库低得多。

---

# 7. 不在 Raw 每行保存 message_id

MQTT 一个 Batch 可能包含数百个 Point。

如果每一个 Value 都复制：

```text
message_id
```

会造成额外空间。

因此：

```text
message_id
```

主要用于：

- IoT ingestion log
- Redis dedup state
- observability/log

Raw Telemetry 只保存：

```text
source_seq
```

和设备/点位/时间。

如果未来确实需要 Value-Level Message Lineage，再增加独立 ingestion_batch 表，而不是直接扩大所有 Raw Row。

---

# 8. ORDER BY

Numeric Raw：

```text
ORDER BY
(
 tenant_id,
 site_id,
 device_id,
 point_id,
 event_time
)
```

这是整个 ClickHouse Telemetry 设计最关键的决策之一。

它优化：

```text
某Tenant某Site
某Device
某Point
某时间范围
```

这种最常见查询。

ClickHouse 的稀疏主索引依赖物理排序，ORDER BY 设计对查询性能影响远大于传统意义上的“多建几个索引”。

---

# 9. PARTITION BY

采用：

```text
PARTITION BY toYYYYMM(event_time)
```

而不是：

```text
按Device分区
按Site分区
按Day疯狂分区
```

Partition 主要用于：

- Lifecycle
- TTL
- 管理
- 大范围数据删除/移动

避免产生海量 Partition。

---

# 10. Numeric Raw TTL

第一版生产基线：

```text
0 ~ 7 day
HOT NVMe

7 ~ 90 day
WARM SSD

90 ~ 365 day
COLD Object Storage

> 365 day
DELETE from Raw
```

即：

```text
7d  → warm
90d → cold
365d → delete
```

这是工程初始值，不是商业合同最终值。

不同项目可按：

```text
Data Class
法规
合同
存储成本
AI需求
```

调整。

---

# 11. State Telemetry

离散状态独立：

```text
telemetry_state
```

原因：

State 常见值：

```text
RUNNING
STOPPED
FAULT
REMOTE
LOCAL
```

不应该强行编码为 Float64 再让业务解释。

使用：

```text
LowCardinality(String)
```

保存标准状态。

状态变化率通常远低于 Numeric。

---

# 12. Battery Cell 专表

电芯是特殊容量场景。

例如：

```text
1000 ESS
× 5000 Cell
= 5,000,000 Cells
```

如果：

```text
每个Cell:
Voltage
Temperature
```

都转换成普通 Point Row，行数会非常大。

因此使用：

```text
battery_cell_telemetry
```

一行：

```text
event_time
asset_id
rack_no
pack_no
cell_no
voltage_v
temperature_c
quality
```

形成：

```text
一个Cell Sample = 一行
```

而不是：

```text
Voltage 一行
Temperature 一行
```

这样在高密度 BMS 场景可显著减少行数量。

---

# 13. 是否建立 PCS Wide Table

第一版：

```text
不建立。
```

PCS 一般只有几十到数百 Point，5 秒采样。

Generic Numeric 足够。

只有性能测试证明：

```text
PCS相关查询
```

已经成为真实瓶颈时，再增加：

```text
pcs_snapshot
```

专表或 Projection。

原则：

> 基于真实瓶颈优化，而不是提前为每种设备创建 Wide Table。

---

# 14. Latest 不查询 ClickHouse

Latest：

```text
Redis
```

历史：

```text
ClickHouse
```

因此：

```text
GET /telemetry/latest
```

优先 Redis。

不要让实时 Dashboard 每秒：

```text
SELECT latest FROM ClickHouse
```

这样可以隔离：

```text
Realtime workload
```

和：

```text
Historical analytics workload
```

---

# 15. 写入链路

```text
Edge
 ↓
MQTT
 ↓
IoT Service
 ↓
Telemetry Worker
 ├── Schema Validation
 ├── Standard Point Mapping
 ├── Unit Conversion
 ├── Quality
 ├── Batch Dedup
 ├── Redis Latest
 └── ClickHouse Batch Insert
```

ClickHouse 不直接消费 Modbus / OPC / Vendor Data。

---

# 16. Go ClickHouse Client

使用官方：

```text
clickhouse-go
```

优先：

```text
Native Protocol
+
Batch Insert
```

第一阶段参数应通过压测确定，不写死为产品标准。

初始测试范围可从：

```text
5k ~ 20k rows / batch
50 ~ 200ms flush interval
```

开始。

如果某些消费者无法做好 Client Batch，可使用 ClickHouse Async Insert。

截至 ClickHouse 26.3 LTS，异步 Insert 已默认启用，但我们仍优先做好 Worker 侧 Batch，以降低小 Part 压力并便于背压控制。

---

# 17. At-least-once 与 Dedup

MQTT QoS、网络重试、Edge Replay 都可能产生重复数据。

原则：

```text
At-least-once
+
Application Idempotency
```

Batch 级：

```text
gateway_id + message_id / sequence
```

在 IoT/Telemetry Worker 做 Dedup。

不把 ClickHouse ReplacingMergeTree 当 Raw Exactly-Once 机制。

原因：

ReplacingMergeTree 的去重是后台 Merge 行为，不应作为控制业务一致性的唯一保障。

---

# 18. Late / Out-of-order Data

Raw 以：

```text
event_time
```

为业务时间。

```text
ingest_time
```

用于观测数据链路。

Late Data 可以继续写入旧 Partition。

Telemetry Worker 需要计算：

```text
ingest_time - event_time
```

并统计：

```text
telemetry_lag
late_data
replay_backlog
```

超过允许迟到窗口的数据，需要触发：

```text
Metric Recalculation
```

---

# 19. 1分钟 Rollup

第一层预聚合：

```text
telemetry_numeric_1m
```

保存：

```text
MIN
MAX
AVG
FIRST
LAST
SAMPLE_COUNT
GOOD_COUNT
```

为什么必须保留：

```text
FIRST / LAST
```

因为 COUNTER：

```text
energy_import
```

的业务计算依赖区间首末累计值，而不是 AVG。

---

# 20. Materialized View

使用：

```text
Incremental Materialized View
```

在 Raw INSERT 时将 1分钟 Aggregate State 写入：

```text
AggregatingMergeTree
```

这样将部分计算成本从查询阶段移动到写入阶段。

1分钟 Aggregate 建议：

```text
2年
```

保留。

超过 2 年的业务趋势主要使用：

```text
Metric Series
Hourly / Daily KPI
```

---

# 21. 为什么第一版不同时建立 5m / 15m / 1h 四套 MV

过多 Materialized View 会增加写入路径负担。

第一版：

```text
Raw
→ 1min
```

即可。

查询层根据范围：

```text
24h
→ Raw / 1min

7d
→ 1min

30d
→ 1min再聚合

长期业务
→ Metric Series
```

等真实负载出现后再增加：

```text
15m / 1h
```

专用 Rollup。

---

# 22. Metric Series

Metric Definition 仍在 PostgreSQL。

ClickHouse 保存：

```text
metric_series
```

例如：

```text
daily_energy
monthly_energy
max_demand
total_energy_cost
pv_generation
ess_round_trip_efficiency
```

结构：

```text
period
subject
metric_code
value
quality
metric_version
binding_version
revision
```

使用：

```text
ReplacingMergeTree(revision)
```

支持 Late Data 重算产生新 Revision。

查询结算类数据时不得盲目依赖后台 Merge 后才去重。

Service 层应使用：

```text
argMax(value, revision)
```

或已确认 Snapshot。

---

# 23. Metric Definition 与 Metric Result 分离

PostgreSQL：

```text
metric_definition
metric_dependency
metric_binding
```

ClickHouse：

```text
metric_series
```

这样：

```text
定义
```

是强事务配置，

```text
结果
```

是海量历史分析数据。

---

# 24. Data Quality Aggregate

ClickHouse 建立：

```text
data_quality_5m
```

按：

```text
tenant
site
data_class
5min
```

统计：

```text
total
good
bad
```

用于：

```text
Data Good Rate
Data Completeness
Site Health
SLO
```

Point 级完整率仍由 Data Quality Service 根据 Expected Sampling 计算。

---

# 25. PostgreSQL Metadata 与 ClickHouse

原则：

```text
Hot Query:
不实时JOIN PostgreSQL
```

Raw Telemetry 自己携带：

```text
tenant/site/device/point
```

名称等维度可以：

### 方法A

Go Service 查询 PostgreSQL，再查询 ClickHouse。

### 方法B

ClickHouse Dictionary 从 PostgreSQL周期加载：

```text
Device ID → Name / Type / Site
```

Dictionary 适用于：

- 小到中型维度表
- 变化频率低
- 大量重复 Lookup

---

# 26. 集群阶段

## Phase A：开发

```text
1 ClickHouse Node
```

使用：

```text
MergeTree
```

不部署 Keeper。

---

## Phase B：正式生产 HA

建议：

```text
1 Shard
×
2 Replicas
+
3 Keeper
```

数据节点：

```text
CH01
CH02
```

Keeper：

```text
K1 K2 K3
```

解决：

- 单节点故障
- 副本
- 高可用

但不增加总 Shard 写入容量。

---

## Phase C：规模扩展

```text
2 Shards
×
2 Replicas
+
3 Keeper
```

即：

```text
4 Data Nodes
3 Keeper
```

后续：

```text
N Shards × 2 Replicas
```

---

# 27. 为什么 Replica = 2

智慧能源 Telemetry：

```text
大量
可补传
```

但生产系统仍需要节点故障继续服务。

默认：

```text
2 Replica
```

通常是性能、容量和可靠性的平衡点。

对于极端关键环境可单独评估 3 Replica。

---

# 28. Keeper

ReplicatedMergeTree 的复制协调使用：

```text
ClickHouse Keeper
```

生产建议：

```text
3 Keeper
```

保持奇数节点。

Keeper 不承担 Telemetry 数据存储，它负责：

- Replication metadata
- Coordination
- Leader/consensus information

应独立监控：

```text
Keeper quorum
latency
disk
snapshot
```

---

# 29. Sharding Key

默认：

```text
cityHash64(site_id)
```

原因：

智慧能源大部分查询边界是：

```text
Site
```

把同一 Site 数据放同一 Shard，有利于：

- Site Overview
- Energy Balance
- Site Trend
- Metric Calculation

减少跨 Shard 聚合。

---

# 30. Mega-Site Hotspot

如果未来出现：

```text
某单一Site
占平台 30%+
Telemetry
```

按 site_id 分片会形成 Hot Shard。

这时可切换大站点策略：

```text
cityHash64(site_id, device_id)
```

或者：

```text
Mega Site 独立 Cluster
```

因此需要持续监控：

```text
Shard Write Rate
Shard Disk
Shard CPU
Shard Parts
```

---

# 31. 新增 Shard 不等于自动均衡旧数据

Horizontal Scale 要有明确的数据重分布方案。

新增 Shard 后：

```text
新数据
```

可以按新 Sharding Key 路由。

历史数据如果需要重新平衡：

```text
需要迁移 / 重写 / 分区复制
```

因此不要过早开很多 Shard，也不要假设加节点后旧数据自动完美均衡。

---

# 32. Distributed Table

生产使用：

```text
*_local
```

作为实际 ReplicatedMergeTree 数据表。

对应用暴露：

```text
telemetry_numeric
```

Distributed Table。

```text
Go Worker
→ Distributed
→ Shard
→ Local Replicated Table
```

Cluster Config 开启：

```text
internal_replication = true
```

由 ReplicatedMergeTree 负责副本同步。

---

# 33. Hot / Warm / Cold

建议：

```text
HOT
NVMe
最新7天

WARM
SSD
7~90天

COLD
S3 / MinIO
90~365天
```

具体区间按数据类型不同。

Battery Cell 数据增长更快：

```text
HOT 3d
WARM 30d
COLD 180d
```

第一版 DDL 已针对 Cell 采用更短周期。

---

# 34. Storage Policy

ClickHouse Server Config：

```text
hot_nvme
warm_ssd
cold_s3
```

组成：

```text
hot_warm_cold
```

表通过：

```text
SETTINGS storage_policy='hot_warm_cold'
```

使用。

TTL：

```text
TO VOLUME 'warm'
TO VOLUME 'cold'
DELETE
```

完成自动生命周期。

---

# 35. Object Storage 的两个不同用途

不要混淆：

## Cold Queryable Storage

ClickHouse MergeTree Part 位于：

```text
S3 / MinIO Disk
```

仍能 SQL 查询。

## Backup / Archive

独立：

```text
BACKUP DATABASE
```

保存备份。

两者必须使用不同：

```text
Bucket / Prefix
```

避免管理混乱。

---

# 36. Backup

推荐初始策略：

```text
Weekly Full Backup

Daily Incremental Backup

Object Storage
```

至少保留：

```text
2个完整Backup Chain
```

重要项目进一步结合：

- 备份不可变策略
- 跨账号/跨区域复制
- Object Lock

---

# 37. Replication 不是 Backup

Replica 会复制：

```text
数据
错误删除
错误TTL
错误操作
```

因此：

```text
2 Replicas
```

不能代替 Backup。

---

# 38. Restore Drill

至少定期：

```text
Restore 到隔离集群
```

验证：

```text
DDL
Row Count
Sample Checksum
Metric Query
Application Read
```

RPO/RTO 必须通过实际演练获得，而不是根据 Backup 配置推测。

---

# 39. INSERT Strategy

禁止：

```text
1 value
→ 1 INSERT
```

Telemetry Worker 必须 Batch。

初始 POC 测试矩阵：

```text
Batch:
1k
5k
10k
20k

Flush:
50ms
100ms
200ms
500ms
```

比较：

```text
values/s
CPU
parts/s
merge pressure
P95 ingest lag
```

---

# 40. Async Insert

如果某一服务确实会产生大量小 INSERT，可以启用/使用：

```text
async_insert
```

当前 26.3 LTS 已把异步 INSERT 默认启用。

但平台仍需要观察：

```text
async insert queue
flush
error
parts
```

不能以“数据库会自动Batch”为理由，让 Worker 完全失去背压和批次控制。

---

# 41. Query Routing

建议：

```text
Latest
→ Redis

0~24h Point Trend
→ ClickHouse Raw / 1m

1~30d Trend
→ 1m

Long-term Energy KPI
→ Metric Series

Multi-site Historical Analytics
→ Metric Series / ClickHouse
```

前端不知道存储路由。

React 永远：

```text
→ Go API
```

---

# 42. Raw Query Guardrail

普通用户 API 禁止：

```text
100k Points
×
365 days
×
Raw
```

后端限制：

```text
time range
point count
result points
granularity
```

API 自动选择 Rollup。

---

# 43. 查询模式

重点支持：

### Point Trend

```text
point_id + time
```

### Device Trend

```text
device_id + point list + time
```

### Site Data Quality

```text
site + time
```

### Site Energy

主要使用：

```text
Metric Series
```

而不是扫描所有 Raw Meter Value 再实时计算。

---

# 44. Projection

第一版不强制。

如果后期出现第二种非常高频的访问模式，例如：

```text
point_code + site + time
```

与主 ORDER BY 不匹配，可以先评估：

```text
Projection
```

再考虑创建新表。

原则：

```text
ORDER BY
→ Data Type
→ Materialized View
→ Projection
→ Skip Index
```

按真实查询数据逐步优化。

---

# 45. Skip Index

不要像 PostgreSQL 一样：

```text
每个WHERE字段建Index
```

ClickHouse 重点依赖：

```text
ORDER BY + Sparse Primary Index
```

Skip Index 只在数据相关性和查询选择性经过验证后引入。

---

# 46. ReplacingMergeTree 的边界

Raw Telemetry：

```text
不用 ReplacingMergeTree
```

因为 Raw 必须高吞吐，并且 Duplicate 应在 Ingestion Boundary 处理。

Metric Series：

```text
使用 ReplacingMergeTree(revision)
```

因为指标确实存在：

```text
Late Data
Recalculation
Revision
```

这两类数据的语义不同。

---

# 47. Counter 处理

`energy_import` 等累计量保存在：

```text
telemetry_numeric
```

1min Rollup 保存：

```text
FIRST
LAST
```

Metric Engine 计算：

```text
Delta
```

同时必须识别：

```text
Counter Reset
Rollover
Replacement
Manual Correction
```

不能直接：

```text
last - first
```

覆盖所有情况。

---

# 48. Time

统一：

```text
UTC Storage
```

列：

```text
DateTime64(3, 'UTC')
```

Site Timezone 只用于：

```text
Reporting Window
Tariff
Daily/Monthly Boundary
Frontend Display
```

不能按照服务器本地时区解释 Energy Day。

---

# 49. Data Correctness

ClickHouse 高吞吐不意味着业务可以接受错误数据。

数据链必须保持：

```text
Point Standard
Unit
Quality
Event Time
Sequence
Counter Semantics
```

不允许：

```text
错误数据先写进去
以后再说
```

---

# 50. Security

ClickHouse 仅内网访问。

角色建议：

```text
telemetry_writer
metric_writer
analytics_reader
backup_operator
platform_admin
```

普通 Web User：

```text
不能直接连接 ClickHouse
```

业务 RBAC 继续由 Go IAM 实现。

---

# 51. TLS / Secrets

生产：

```text
Go → ClickHouse TLS
```

Object Storage Secret：

```text
Secret Manager / Environment Injection
```

禁止：

```text
Git
SQL File
Frontend
```

保存实际 Secret。

---

# 52. Observability

至少监控：

```text
Insert rows/s
Insert bytes/s
Query P95/P99
Parts count
Small parts
Merge queue
Merge duration
Disk usage
Storage tier usage
Replica delay
Readonly replica
Keeper health
Distributed queue
Backup result
TTL move
Memory
CPU
```

系统表重点：

```text
system.metrics
system.parts
system.replicas
system.merges
system.query_log
system.backups
system.backup_log
```

---

# 53. 业务级可观测性

数据库性能指标之外，仍然必须观察：

```text
Telemetry Values/s
Telemetry Lag
Data Good Rate
Data Completeness
Replay Backlog
Metric Calculation Lag
```

因为：

> ClickHouse 正常，不代表能源数据正常。

---

# 54. Capacity Test

第一阶段正式验收：

```text
10k values/s
持续24~72h
```

第二档：

```text
20k values/s
```

第三档：

```text
50k values/s
```

规模化之前：

```text
100k+
```

每一档都同时运行：

```text
Write
Latest
History Query
1m MV
Metric Query
Replay
```

---

# 55. Replay Storm Test

必须模拟：

```text
Realtime 10k values/s
+
Replay 20k~50k values/s
```

验证：

```text
Realtime priority
ClickHouse merge pressure
Worker queue
Telemetry lag
```

如果 Replay 影响 Realtime SLO：

```text
Replay Rate Limit
```

必须收紧。

---

# 56. Shard Scale Trigger

不要根据设备数量自动扩容。

主要看：

```text
Node CPU
Disk
Merge backlog
Parts
Write latency
Historical query P95
Replication lag
Storage growth
```

持续达到阈值后才考虑：

```text
New Shard
```

---

# 57. Kafka 仍然后置

第一阶段：

```text
MQTT
→ Telemetry Worker
→ ClickHouse
```

足够。

当出现：

```text
多个独立消费者
高吞吐解耦
消息长期Replay
消费端独立扩缩
```

再演进：

```text
MQTT
→ Kafka
→ Consumers
```

---

# 58. PostgreSQL + ClickHouse 一致性

不存在跨数据库分布式事务。

因此采用：

```text
Metadata first
then Telemetry
```

Device/Point 发布后才允许 Gateway 下发对应 Mapping。

Telemetry 包中未知：

```text
device_id
point_id
```

进入：

```text
Dead Letter / Invalid Pipeline
```

而不是偷偷创建 Metadata。

---

# 59. Schema Evolution

Raw Table 新增字段：

```text
向后兼容
```

优先使用：

```text
ADD COLUMN with DEFAULT
```

不能频繁：

```text
Mutation update all historical rows
```

Device 新语义优先增加 Standard Point，而不是改变旧 Point 的含义。

---

# 60. ADR 冻结

V2 正式冻结：

```text
PostgreSQL
= Business / Metadata / Transaction

ClickHouse
= Telemetry / Historical Analytics

Redis
= Latest / Runtime State

MQTT
= Edge Messaging
```

ClickHouse 是平台唯一默认时序事实数据库与历史分析数据库。

正式架构不再保留其他时序数据库作为主实现或候选实现；容量、可靠性与成本评估均以 ClickHouse 为基线。

---

# 61. 推荐最终架构

```text
                           PostgreSQL
                           ├─ IAM
                           ├─ Asset
                           ├─ Device
                           ├─ Point
                           ├─ Topology
                           ├─ Alarm
                           ├─ Command
                           └─ Metric Definition

Device
  ↓
Edge Gateway
  ↓
MQTT
  ↓
Go Telemetry Worker
  ├──────────→ Redis
  │            Latest / State
  │
  └──────────→ ClickHouse
               ├─ Numeric Raw
               ├─ State Raw
               ├─ Battery Cell
               ├─ 1min Rollup
               ├─ Metric Series
               └─ Data Quality
                        │
                        ↓
                  S3 / MinIO
                  Cold + Backup
```

---

# 62. 第一阶段实施顺序

```text
1. PostgreSQL Migration V2
2. ClickHouse single-node Dev
3. Meter Vertical Slice
4. Numeric Raw
5. Redis Latest
6. History API
7. 1min MV
8. Data Quality
9. ESS / Battery Cell
10. Production Replica + Keeper
11. Storage Tier
12. Backup / Restore Drill
```

---

# 63. 生产 Gate

正式生产前至少满足：

- ClickHouse 24~72h endurance PASS
- 10k/20k Values/s PASS
- Replay Storm PASS
- Replica failover PASS
- Keeper node failure PASS
- S3 cold move PASS
- Full Backup PASS
- Incremental Backup PASS
- Restore Drill PASS
- Query Guardrail PASS
- Telemetry Lag SLO PASS
- No uncontrolled small-parts growth
- No direct end-user ClickHouse access

---

# 64. 结论

V2 不把 ClickHouse 当成“另一个 PostgreSQL”。

它只承担最适合的事情：

```text
大量
追加型
时序
历史
分析
聚合
```

业务世界仍由 PostgreSQL 管理。

最终核心边界：

```text
PostgreSQL 负责“是什么、允许什么、当前业务状态是什么”。

ClickHouse 负责“过去发生了多少、什么时候发生、趋势和统计是什么”。

Redis 负责“现在最新是什么”。

MQTT 负责“现场消息如何流动”。
```

这四个边界稳定后，即使未来扩展到百万级 Point、数十万 Values/s、数百 TB 甚至 PB 级历史数据，Device → Point → Telemetry → Metric 的领域模型都不需要推倒重来。


---

# V1 → V2 数据层迁移说明

当前尚处于正式编码前阶段，因此建议直接采用 V2，不执行生产在线迁移。

## 删除

V1：

```text
000005_timeseries.up.sql
旧时序数据库扩展
telemetry_raw hypertable
metric_series hypertable
```

不再作为默认契约。

## 保留

PostgreSQL：

```text
Tenant
Site
Space
Asset
Gateway
Device
Point
Topology
Metric Definition
Alarm
Command
Tariff
Safety Policy
Audit
```

全部继续保留。

## 新增

ClickHouse：

```text
telemetry_numeric
telemetry_state
battery_cell_telemetry
telemetry_numeric_1m
data_quality_5m
metric_series
```

## 应用代码边界

V1 代码设计中：

```text
Telemetry Repository
```

不得暴露具体数据库 SQL 给 Domain。

V2 实现：

```text
ClickHouseTelemetryRepository
```

替代旧时序数据 Repository。

React/API 契约不因数据库变化而改变。
