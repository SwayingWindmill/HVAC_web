# 数据架构 V2 一致性基线

状态：CURRENT

权威来源：`SE-DATA-001《智慧能源系统数据架构设计 V2》V2.0 CURRENT`。

机器基线：`contracts/data/data-architecture.v2.json`。

校验命令：`npm run data:architecture:check`。

## 1. Canonical 主链

当前项目数据架构必须收敛为：

`Tenant → Site → Space → Asset → Device → Point → Telemetry`

其中：

- `Organization` 已批准从当前项目删除，不得作为数据主维度、授权主维度或历史事实维度继续扩散。
- `ThingsBoard` 已批准从当前项目退出，不得作为 canonical source、兼容读写路径、控制连接器、部署目标或测试依赖继续存在。
- `Point` 始终是 canonical 数据/控制点。
- `Physical Sensor` 仅在真实探头需要独立安装、更换、校准或追溯生命周期时存在；它不是 Device 与 Point 之间的必经层。
- `Point != Metric`。平台计算指标进入 Metric；虚拟计量进入 Virtual Meter，不再以 Calculated Point 建模。

## 2. 当前已收敛的 V2 语义

- Point Code 使用 `lower_snake_case` canonical code；vendor/register/source key 与 Point Code 分离，vendor `source_key` 可保留厂商/寄存器原始命名但不得反向污染 Point Code。
- Point 类型统一为 `TELEMETRY / COUNTER / STATE / SETTING / COMMAND`。
- Unit Registry 使用 canonical ASCII 单位代码（包括 `m3 / m3/h`，不使用 `m³ / m³/h`）；Energy Edge 固化方向符号语义：Grid 向站内为 `IMPORT`、站内向 Grid 为 `EXPORT`，ESS 向外为 `DISCHARGE`、流入 ESS 为 `CHARGE`。
- Physical Sensor 不再拥有 measured-subject canonical binding；测量对象归属于 Point。
- Telemetry 同时保留 event time 与 ingest time。
- Telemetry Quality 主值统一为 `GOOD / PARTIAL / ESTIMATED / MANUAL / STALE / INVALID`，与 ingest acceptance/status 分离。
- 映射成功且内容有效的 event-time out-of-order 数据保留完整历史事实，但不回退 Latest。
- COUNTER Point 显式声明 `RESET_TO_ZERO / ROLLOVER / INVALID` 下降语义；ROLLOVER 必须声明正数 modulus。历史事实快照 Point revision 与 Counter 规则，按 event time 计算 delta，不跨 Point revision/unit 计算，且 COUNTER 不进入 generic numeric avg/min/max rollup。
- Energy Topology 具备 Site 级 Topology Version、单 ACTIVE 版本与发布后 graph 冻结；Edge 不能跨 Topology Version 引用 Node。
- Metering 基础具备 Meter、有效期 CT/PT Ratio Version、Meter Binding、同 Edge 重叠 PRIMARY 拒绝，以及 `DEVICE_APPLIED / EDGE_APPLIED / PLATFORM_APPLIED` 单次倍率语义。
- Virtual Meter 第一阶段仅允许 Meter Binding / Virtual Meter 来源，并在写入时拒绝依赖环。
- Settlement Boundary 绑定固定 Topology Version，并以单 Node 或 Edge Set 定义正式业务结算边界；Edge Set 在发布前必须非空。
- Tariff 使用 Site timezone 快照和有效期版本；released 版本不可时间重叠，Tariff Period 按 `SUPER_PEAK / PEAK / FLAT / VALLEY` 与本地时间片建模。
- Settlement Period 使用 `OPEN / CALCULATING / REVIEW / LOCKED / REVISED / CANCELLED` 状态机；LOCKED 必须已有初始 Snapshot，Snapshot 永不可 UPDATE/DELETE，锁后变化通过 Change Candidate 与追加式 Settlement Revision 产生新 Snapshot。
- Metric 使用稳定 Identity + 有效期 Metric Version；Dependency 仅允许 `POINT / METRIC / EXTERNAL` 并强制 DAG，released Version 与 Dependency 冻结；Metric Binding 固定 Subject、Version、binding version 与 granularity，Calculation Run 固定输入引用和运行状态。
- Metric Result 落 ClickHouse `analytics.metric_series`，携带 metric/binding/calculation-run lineage 和 `revision`；历史重算追加更高 Revision，不静默覆盖旧结果。
- Data Lifecycle Policy 在 PostgreSQL 按 Dataset/Data Class/有效期版本化，ClickHouse 不再以硬编码 TTL 充当治理权威；ACTIVE Legal Hold 会阻断删除批准/执行。
- Archive 与 Backup 使用独立 Object Storage Bucket Purpose 和独立 Manifest 账本：`archive_required` 删除必须绑定匹配 Dataset/Site 的 VERIFIED Archive Manifest，Restore 必须绑定 VERIFIED Backup Manifest；Backup 不能替代 Archive，Archive 也不能替代 Backup。
- 删除以 Deletion Request + 不可变 Tombstone 记账；Restore Run 在全部 Tombstone 执行 `REDELETE / EXCLUDE` 前禁止完成，防止备份恢复让已删除数据复活。
- Manual Correction 经过 DRAFT→REVIEW→APPROVED，再生成不可变 Correction Fact；Correction Fact 只作为下游 Metric/Settlement 合并输入，不覆盖 Raw Telemetry。
- Forecast 生产路径使用 Go Forecast Service；P0 仅覆盖 `SITE_LOAD / PV_GENERATION`，Feature Set Version、Dataset Snapshot、Training Run、Model Version、Deployment、Input Snapshot、Job 与 Forecast Snapshot 形成不可变追溯链。
- Forecast Result 落 ClickHouse `analytics.forecast_series`，每个点携带 `forecast_origin / forecast_for / model_version / feature_set_version / input_snapshot_id / topology_version_id / quality`；当前 Go baseline 仅提供 `LAST_VALUE` 且标记 `FALLBACK`，无有效 Last Value 时 fail-closed。
- Optimization 生产路径使用 Go Optimization Service；P0 固定为 `SITE + ESS + DAY_AHEAD 24h + 15MIN`，Input Snapshot 在 `BUILDING` 阶段绑定 Policy Version、Topology Version、Load Forecast、可选 PV Forecast、Tariff Version、当前状态、Safety/Maintenance/Manual Lock 与 ESS Resource 快照，`SEALED` 后整体不可变。
- 当前 Go Optimization baseline 仅允许 `SHADOW + NO_DISPATCH`，为每个 ESS 生成完整 96 个 15min 的 0 kW Dispatch Interval 并标记 `FALLBACK`；Plan 不包含 Command/MQTT/Execution surface，SHADOW Plan 也被数据库状态机禁止进入 `APPROVED / ACTIVE`，保持 `Plan != Execution`。
- Redis Latest 已作为对外 Current Snapshot 的可重建缓存：只物化 `subscription_id IS NULL` 的 canonical full-snapshot outbox，按 `business_revision` CAS 写入并拒绝同版/旧版回退；启动时从 PostgreSQL business state-machine Snapshot 批量重建，读路径也会按完整 Snapshot 自修复。`latest_accepted_telemetry` 仅保留为 ingest/evaluator 内部工作投影，不再承担对外 Latest authority。
- ClickHouse 保持 Historical Telemetry/Analytics SoT。
- MQTT 仅承担传输。

## 3. 当前仍未满足 V2 的主要阻塞项

以下状态必须以 `contracts/data/data-architecture.v2.json` 为准，不得由旧 Phase 1 基线覆盖：

- Organization 尚未从全仓库清除。
- Metric calculation execution service 尚未完成。
- Settlement calculation/reconciliation execution 尚未完成。
- Forecast advanced training/artifact/deployment orchestration 尚未完成。
- Optimization cost/demand/carbon solver、审批与 Control execution handoff 尚未完成。
- Object Storage 的实际归档执行、完整 Lineage 与生命周期编排尚未完成。
- Object Storage 的生产数据治理角色尚未完成。

在这些阻塞项完成前，V2 `acceptanceEligible` 必须保持 `false`。

## 4. 旧数据架构基线已退出

旧的 `phase1-data-architecture` 六模型 JSON、checker 和架构页已经删除。当前仓库只保留 Data Architecture V2 作为 CURRENT 数据架构权威。
