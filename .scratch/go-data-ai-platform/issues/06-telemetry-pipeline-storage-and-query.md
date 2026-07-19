# 确定遥测摄取、处理、存储与查询架构

Type: grilling
Status: resolved
Blocked by: 01, 02, 04, 05
Part of: ../map.md

## Question

ThingsBoard 遥测应如何进入 Go 数据平台并支持实时态势、历史分析和 AI 取证？需要确定摄取接口、批量与背压、raw/normalized 流、字段标准化和单位转换、乱序与迟到数据、去重和重放、Redis 最新状态、ClickHouse 或其他历史存储、对象归档、聚合层、保留策略、查询服务和实时订阅的数据来源。

## Comments

- 遥测主链路采用 Push-first、append-first、reconcile-second：ThingsBoard 通过 Rule Chain、Integration 或 Export Adapter 主动批量推送至 thingsboard-connector-data，再由 telemetry-ingest 写入 `telemetry.accepted.v1`；只有 Kafka 多数副本确认成功后才向上游确认。
- 摄取层保留 ThingsBoard/设备原始批次和原始字节，不拆成每点一条消息，也不要求接收时已存在 TelemetryPoint Mapping。未知设备、未知 Key 或类型异常仍先保存事实，随后进入标准化或 Quarantine。
- `organization_id`、`site_id` 和平台 `device_id` 必须由服务端根据 IntegrationInstance 与外部设备绑定解析，不能直接信任上游声明。认证使用独立集成身份、TLS 和可轮换凭据，不使用浏览器 JWT 或全租户共享永久 Token。
- REST 历史查询仅用于按设备与时间水位核对缺口和生成 BACKFILL 批次，不作为生产主摄取链路。补数保留原 source timestamp，并不得触发仅面向实时到达的通知或外部副作用。
- 若 ThingsBoard 推送方式不支持可靠重投，则 Adapter 必须具备 ThingsBoard 侧持久化重试、Connector WAL、Kafka 原生输出或基于历史水位可靠补数中的至少一种；进程内存队列不能作为 RPO=0 依据。
- 标准化采用不可变版本的 DeviceBinding 与 PointMapping。历史事件按 source_timestamp 解析当时有效绑定；设备时间不可信时才回退 received_at 并标记 `BINDING_TIME_INFERRED`。Processor 通过 `registry.projection.v1` 维护本地版本投影，不在热路径同步调用 Core 或 ThingsBoard。
- 映射匹配顺序为 Device Override → Device Profile Mapping → IntegrationInstance Profile Mapping；无明确映射则进入 Quarantine，禁止根据 Key 名称或数值范围静默猜测。已发布映射不得原地修改，修正需创建新 mapping_version，并区分 ORIGINAL_MAPPING 原样重放与 REMAP 修正重算。
- 原始批次按点部分成功：合法点进入 `telemetry.normalized.v1`，失败点单独进入 Quarantine，不能因一个未知 Key 拒绝整包。标准化值使用 Typed Union，保留 raw_value/raw_unit 与 canonical_value/canonical_unit，并记录 mapping、binding 和 processor 版本。
- 类型、枚举、缩放和单位转换必须在 Mapping 中显式声明；转换失败不能用零值替代。质量采用可组合 Flags 与 VALID/SUSPECT/INVALID 总级别，显式表达迟到、乱序、时钟偏差、补数、重复候选、越界、未验证映射和时间推断；迟到与乱序仍进入历史存储。
- 同 Key、同时间戳、不同值必须全部保留并标记 `TIMESTAMP_CONFLICT`；完全相同的重复值才可按稳定来源身份幂等忽略，不能依赖最后写入覆盖历史证据。
- 最新态由 `latest-state-projector` 对标准化点值执行确定性版本比较后发布至按 telemetry_point_id 分区、日志压缩的 `telemetry.latest-changelog.v1`。比较优先使用可靠 source_sequence，其次 event_timestamp、received_at 和稳定消息 ID；INVALID 不更新正式最新态，SUSPECT 按 Point Policy 决定。
- Redis 仅物化 UI、控制验证、告警与自动化需要的最新点位，是可删除并由 Changelog 重建的在线视图，不承担权威数据 RPO。写入使用 latest_version CAS；stale/offline 由独立评估器产生状态事件，不能依靠 Redis TTL 删除旧值。
- 最新快照由 telemetry-query-service 从 Redis 返回 snapshot_version、数据水位与 projection_staleness；Redis 故障时可限流降级至历史存储最新值查询，但不得用于高风险命令前置验证。
- Realtime Gateway 直接消费 `telemetry.latest-changelog.v1`。客户端先获取带 cursor 的 REST 快照，再订阅游标后的增量；慢客户端按 point_id 合并待发送状态，缓冲超限时返回 `RESYNC_REQUIRED`，不能阻塞 Kafka Consumer。
- 历史分析默认使用 ClickHouse 保存规范化热明细和多级聚合，对象存储保存 90 天原始批次与规范化 Parquet；热明细参考保留 30 天并按 Point Class 调整，1 分钟聚合保留 2 年，15 分钟和小时聚合保留 5 年，日/月/KPI 长期保留。
- ClickHouse 明细采用一行一个 Canonical Point Value，围绕 organization/site/point/time 排序并批量写入；不能依赖 ClickHouse 排序键提供关系数据库式唯一约束。对象归档按时间和 Integration/Bucket 组织大文件，Manifest、Offset 范围和 Checksum 校验成功后才视为完成。
- 聚合采用 PROVISIONAL/FINAL 两阶段，保存 sum/count/min/max/first/last、覆盖时长、质量计数以及状态时长或计数器增量。迟到、补数和 Mapping 修正通过 AggregateRebuildJob 生成更高 revision，禁止依赖增量物化视图自动修复历史。
- telemetry-query-service 是唯一历史查询入口，按最新态、热明细、多级聚合和 Cold Query Job 路由 Redis、ClickHouse 与对象存储；所有结果返回 data/aggregate watermark、dataset_revision、partial、projection_staleness 和质量摘要。
- 查询调用方只能按 Organization/Site/System/Equipment/Device/TelemetryPoint 等业务范围访问数据，不能提交任意 SQL、存储表名、Kafka Offset、Redis Key、对象路径或 ThingsBoard 外部 ID。Query Service 在执行前解析授权 Point 集合，并实施 Point 数、时间跨度、返回点数、扫描字节、执行时间和并发预算；超预算时降低分辨率或转异步 Job，不得静默截断。
- 派生指标作为独立、版本化的 Derived Point 保存，记录公式版本、输入点、窗口、缺失与质量策略、输入 dataset_revision/watermark 和完整 lineage；流式派生与批量派生分离，依赖图必须无环，派生值不得覆盖或冒充原始设备点。
- EnergyAgent 与 agent-worker 不得直连 Redis、ClickHouse、Kafka、ThingsBoard 或对象存储，只能调用经审计、重新鉴权且有查询预算的 Tool API。复杂分析先创建不可变 InvestigationDataset，固化范围、分辨率、修订、水位、质量、血缘与内容哈希；AI Finding 必须引用 dataset_id 和证据点/时间范围。
- AI 结论不能直接生成 ThingsBoard RPC，必须经过 Proposed Action、Command Policy、权限与风险检查、审批/自动化规则和 Command Service。历史分析值不能替代控制链路要求的在线 VERIFIED 状态。

## Answer

遥测主链路采用 Push-first、append-first、reconcile-second。ThingsBoard 通过 Rule Chain、Integration 或 Export Adapter 将原始设备批次主动推送至 thingsboard-connector-data，随后由 telemetry-ingest 写入 Kafka `telemetry.accepted.v1`；只有三副本多数确认成功后才向上游确认。摄取层保留原始字节、包边界、IntegrationInstance、外部设备、来源时间/序列、接收时间和 payload hash，不在入口拆成单点消息，也不因未知设备、未知 Key 或映射缺失而丢弃事实。REST 历史接口只承担水位核对、缺口检测与 BACKFILL，不能作为生产主摄取链路。

所有租户与资产上下文均由平台依据 IntegrationInstance、ExternalBinding 和事件发生时有效的 DeviceBinding 解析，不能信任上游自行声明的 organization_id/site_id。DeviceBinding 与 PointMapping 均为不可变版本，Processor 消费 `registry.projection.v1` 维护本地投影，不在热路径同步调用 Core 或 ThingsBoard。映射按 Device Override、Device Profile、Integration Profile 的顺序匹配；无明确映射进入 Quarantine，禁止通过字段名、数值范围或模糊规则静默猜测。

标准化按原始批次逐点部分成功。合法点进入 `telemetry.normalized.v1`，失败点单独进入 Quarantine；Canonical Value 使用 BOOLEAN、INT64、FLOAT64、STRING、ENUM、JSON 等 Typed Union，同时保存 raw_value/raw_unit 与 canonical_value/canonical_unit，并记录 binding_version、mapping_version、processor_version、source_message_id 和 occurrence_index。类型转换、枚举、缩放、偏移和单位换算必须由 Mapping 显式声明。质量使用 VALID/SUSPECT/INVALID 总级别及可组合 Flags，覆盖迟到、乱序、时钟偏差、补数、重复候选、越界、来源质量、未验证映射和时间推断。迟到、乱序和冲突值仍进入历史存储；同 Key、同时间戳、不同值必须全部保留并标记 TIMESTAMP_CONFLICT。

最新状态由 latest-state-projector 对标准化点执行确定性版本比较，优先使用可靠 source_sequence，其次 event_timestamp、received_at 和稳定消息 ID。胜出的更新发布到按 telemetry_point_id 分区并日志压缩的 `telemetry.latest-changelog.v1`。Redis 只物化 UI、控制验证、告警和自动化需要的最新点位，通过 latest_version CAS 幂等更新，可被完全删除并由 Changelog 重建，不承担权威数据 RPO。stale/offline 由独立评估器根据 Point Policy 产生状态变化事件，不能使用 Redis TTL 删除旧值来表达失效。

实时访问采用 REST 快照加增量订阅。telemetry-query-service 从 Redis 返回 snapshot_version、cursor、数据水位和 projection_staleness；Realtime Gateway 直接消费 latest Changelog，补发 cursor 后的更新。慢客户端按 point_id 合并尚未发送的状态，缓冲超限时返回 RESYNC_REQUIRED 并要求重新获取快照，不能阻塞 Kafka Consumer。Redis 故障时允许限流降级到 ClickHouse 最新值查询，但必须标记 degraded 和实际水位，且不得用于高风险命令前置验证。

历史分析默认采用 ClickHouse 保存规范化热明细与多级聚合，对象存储保存 90 天原始批次和规范化 Parquet。热明细参考保留 30 天并按 Point Class 调整；1 分钟聚合保留 2 年，15 分钟与小时聚合保留 5 年，日/月/KPI 长期保留。ClickHouse 明细逻辑上一行一个 Canonical Point Value，围绕 organization/site/point/time 排序并使用批量 INSERT；不能依赖排序键或 MergeTree 自动提供唯一约束。对象归档按时间和 Integration/Bucket 组织大 Parquet 文件，记录 Kafka Offset 范围、时间范围、行数与 Checksum，Manifest 校验成功后才视为归档完成。

聚合采用 PROVISIONAL/FINAL 两阶段。近实时聚合快速服务仪表盘，超过允许迟到水位后重新计算为 FINAL；聚合保存可继续组合的 sum/count/min/max/first/last、覆盖时长和质量计数，状态点保存持续时间与切换次数，累计量保存有效正向增量、复位和回绕信息。迟到、BACKFILL、Mapping 修正或数据修订必须创建 AggregateRebuildJob，从规范化明细或 Parquet 生成更高 dataset revision，不能依靠增量物化视图自动修复历史，也不能对大规模明细执行无边界 UPDATE。

telemetry-query-service 是最新、历史、聚合、冷数据和导出的唯一入口。调用方按 Organization、Site、System、Equipment、Device 或 TelemetryPoint 业务范围查询，服务端解析授权 Point 集合并路由 Redis、ClickHouse 或 ColdQueryJob。每次查询执行成本预算和分辨率协商；返回 requested/actual resolution、data_watermark、aggregate_watermark、dataset_revision、partial、projection_staleness 与 quality_summary。质量策略限定为 VALID_ONLY、VALID_AND_SUSPECT 或 ALL_WITH_FLAGS，禁止静默过滤后仍声称结果完整。

派生指标作为独立版本化 Derived Point，不覆盖原始点。定义记录公式版本、输入语义选择器、窗口、对齐、缺失和质量策略、输出类型/单位及有效期，结果记录 input_dataset_revision、input_watermark、calculation_time 和 lineage。低延迟简单指标由流式处理器生成，复杂 KPI、能耗、基线和历史修订由 analytics-worker 批量计算；派生依赖必须构成有向无环图。

AI 数据访问严格通过受审计 Tool API。EnergyAgent 与 agent-worker 不得直连 Redis、ClickHouse、Kafka、ThingsBoard 或对象存储。复杂分析先创建稳定 InvestigationDataset，固化授权范围、时间范围、Point、实际分辨率、dataset revision、水位、质量摘要、血缘、内容哈希与有效期；AI Finding 必须引用 dataset_id、证据点、时间范围和定义版本。AI 结论只能形成 Proposed Action，后续必须经过 Command Governance、风险和权限检查以及审批或自动化策略，不能绕过 Command Service 直接控制设备。
