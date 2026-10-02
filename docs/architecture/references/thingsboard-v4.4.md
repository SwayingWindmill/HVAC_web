# ThingsBoard v4.4 源码参考（架构评审用）

- 版本：ThingsBoard tag `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（2026-09-29），浅克隆自 `https://github.com/thingsboard/thingsboard.git`
- 方法：只读源码与仓库内配置。下文路径均相对仓库根目录，`:N` 为行号。没有引用二手资料。
- 用途：作为商用 HVAC 节能 AIoT 平台（首个场景冷站；一期单机 Docker Compose，后续扩展到多站点、多租户、多边缘网关）的一手参照。

## 0. 先说许可证和版本形态（影响"能不能抄代码"）

- **从 4.4 起主体代码改为 BUSL-1.1**，不再是 Apache-2.0。`README.md:147-151`；`LICENSE` 的 Additional Use Grant 规定：商业用途免费生产使用的上限是 **≤100 台设备且只用一台服务器**，并且必须保留 "Powered by ThingsBoard" 并使用官方 license key（`LICENSE:13-43`）。设备的定义包含"经网关接入"的子设备（`LICENSE:55-60`）。四年后转为 Apache-2.0（`LICENSE:66-68`）。
- 仓库里 BUSL 与 Apache 头混用：约 3375 个 Java 文件标注 `BUSL-1.1`，约 4770 个标注 `Apache-2.0`（`license-header-busl.txt`、`license-header.txt`）。
- v4.4 的"CE"仓库已经并入原 PE 功能，包括 entity group、role/group_permission RBAC、customer 层级、integrations、converters、scheduler、reports、white-labeling、license 检查（`application/src/main/resources/thingsboard.yml:2059-2075` 的 `license:` 段；`dao/src/main/resources/sql/schema-entities.sql:639,720,735`；`common/integration/`）。
- **结论**：本仓库只能作为设计参考，不能复制代码。参考 4.3 及以前的 Apache 版本时，也要固定 tag 并在文档里写明。

## 1. 进程与部署拓扑

### 1.1 一个可执行包，多种角色
- `application/` 是一个 Spring Boot 进程，用 `service.type` 决定角色：`monolith | tb-core | tb-rule-engine`（`application/src/main/resources/thingsboard.yml:2644-2645`）。
- 独立 workload：`transport/{mqtt,http,coap,lwm2m,snmp}`（各自是独立 Spring Boot 应用，例如 `transport/mqtt/src/main/java/org/thingsboard/server/mqtt/ThingsboardMqttTransportApplication.java`，`service.type` 默认 `tb-transport`，见 `transport/mqtt/src/main/resources/tb-mqtt-transport.yml:412-413`），`msa/js-executor`（Node.js，`msa/js-executor/server.ts`），`edqs/`（实体查询服务，`edqs/src/main/java/org/thingsboard/server/edqs/ThingsboardEdqsApplication.java`），`msa/vc-executor`（版本控制），以及 integration executor 和 web-report。
- 单机 monolith 内嵌所有 transport，用开关控制：`HTTP_ENABLED/MQTT_ENABLED/COAP_ENABLED/LWM2M_ENABLED/SNMP_ENABLED`（`thingsboard.yml:1483,1493,1558,1570,1708`）。
- 仓库内不再附带生产 compose 文件。black-box 测试从外部仓库拉取 compose：`msa/black-box-tests/src/test/java/org/thingsboard/server/msa/ComposeRepository.java:44`（`thingsboard-pe-docker-compose`）。一套集群要组合的文件可以从 `ContainerTestSuite.java:112-146` 看出：`docker-compose.yml + edqs + kafka + postgres|hybrid(cassandra) + citus + valkey + trendz ...`。

### 1.2 队列实现：in-memory 与 Kafka
- `queue.type` 默认 `in-memory`，可选 `kafka`（`thingsboard.yml:2133-2134`）。
- in-memory 只能配合 monolith：`InMemoryMonolithQueueFactory` 的条件是 `queue.type=='in-memory' && service.type=='monolith'`（`common/queue/src/main/java/org/thingsboard/server/queue/provider/InMemoryMonolithQueueFactory.java:53`）。拆成微服务（`KafkaTbCoreQueueFactory.java:76`、`KafkaTbRuleEngineQueueFactory`）就必须用 Kafka；独立 transport 默认也是 Kafka（`tb-mqtt-transport.yml:247`）。
- in-memory 的实现就是 `ConcurrentHashMap<String, LinkedBlockingQueue>`（`common/queue/src/main/java/org/thingsboard/server/queue/memory/DefaultInMemoryStorage.java:21,46`），**没有持久化**。进程崩溃时，已经 PUBACK 但还没处理的消息会丢失。
- 服务发现：`zk.enabled` 默认 false，此时用 `DummyDiscoveryService`；集群模式用 ZooKeeper 临时顺序节点（`common/queue/.../discovery/ZkDiscoveryService.java:46-47,158`；`DummyDiscoveryService.java:16-17`；`thingsboard.yml:127-145`）。
- 缓存：`cache.type` 为 `caffeine`（默认，进程内）或 `redis`（`thingsboard.yml:856-858`）。集群部署必须用 Redis/Valkey，否则多节点之间缓存不一致。

### 1.3 分区与工作分配（`HashPartitionService`）
- **实体到分区**：用 `murmur3_128`（可配 `murmur3_32`/`sha256`）对 entityId 取哈希再取模，即 `Math.abs(hash % partitionSize)`（`common/queue/src/main/java/org/thingsboard/server/queue/discovery/HashPartitionService.java:388-397`；`thingsboard.yml:2327-2328`）。这是**普通取模，不是一致性哈希**。`ConsistentHashCircle.java` 留在仓库里，但没有任何引用。
- **分区到节点**：rule-engine 按 `(hash(tenantId)+partition) % servers.size()` 分配；core 按 `partition % servers.size()`；EDQS 按 label 分组（`HashPartitionService.java:732-778`）。节点数变化会触发 `recalculatePartitions` 全量重算（`:436`），对应的 actor 也要重建，所以有 `zk.recalculate_delay` 用来缓冲（`thingsboard.yml:140-143`）。
- 分区数是固定配置：core 默认 10（`thingsboard.yml:2354`），EDQS 默认 12（`:2418`），rule-engine 的每个 queue 默认 10（见 §5）。
- **租户隔离**：tenant profile 可以开启 isolated，此时该租户使用独立 queue/topic。`service.rule_engine.assigned_tenant_profiles` 可以让某些 rule-engine 节点只服务指定 profile 的租户（`thingsboard.yml:2653-2657`；`HashPartitionService.java:321-327,739-765`）。
- **actor 归属**：`TenantActor` 根据本节点是 core 还是 rule-engine，决定创建 DeviceActor 还是 RuleChain actor（`application/src/main/java/org/thingsboard/server/actors/tenant/TenantActor.java:81-83,257,362`）。DeviceActor 只存在于持有该设备 core 分区的节点上（`:218`）。actor 框架是自研的（`common/actor/src/main/java/org/thingsboard/server/actors/DefaultTbActorSystem.java`、`TbActorMailbox.java`），**不是 Akka**。

### 1.4 EDQS
- EDQS（Entity Data Query Service）在内存里维护一份实体和最新值的副本，用来回答仪表盘的 entity data query；在 local 模式下用 RocksDB 做备份（`thingsboard.yml:2397-2420`）。默认关闭：`sync.enabled=false`、`api.supported=false`，不开时查询走 PostgreSQL。它是为大规模租户的复杂过滤查询准备的，再增加一份数据副本，也就多一处一致性问题。

## 2. 设备接入

### 2.1 transport 与 core 之间：Transport API
- 每个 transport 进程**不直连数据库**，凭据校验、子设备创建、provision 都通过 `transportApiRequestTemplate` 走队列上的请求/响应（`common/transport/transport-api/src/main/java/org/thingsboard/server/common/transport/service/DefaultTransportService.java:172,194-198,341-395,421-481`；topic 配置在 `thingsboard.yml:2329-2345`）。
- 服务端由 `DefaultTransportApiService` 处理：`validateCredentials` 按 `credentialsId` 查找（`application/src/main/java/org/thingsboard/server/service/transport/DefaultTransportApiService.java:217-300`）。凭据查询有缓存：`DeviceCredentialsServiceImpl extends AbstractCachedEntityService`，更新时通过事件驱逐缓存（`dao/src/main/java/org/thingsboard/server/dao/device/DeviceCredentialsServiceImpl.java:47-72`）。
- 凭据类型：`ACCESS_TOKEN / X509_CERTIFICATE / MQTT_BASIC / LWM2M_CREDENTIALS`（`common/data/.../security/DeviceCredentialsType.java`）。MQTT 的 CONNECT 处理：有客户端证书时按证书 SHA3 哈希查找（`common/transport/mqtt/.../MqttTransportHandler.java:1093,1129-1136`），否则用 username/password/clientId 组成 `ValidateBasicMqttCredRequestMsg`（`:1100-1103`）。
- 会话：每个 transport 进程在本地维护 `sessions` map，`registerAsyncSession` 注册的会话用于接收下行消息（`DefaultTransportService.java:248-249`）。会话不活跃超时默认 10 分钟，需要和 core 的设备不活跃超时配合（`thingsboard.yml:1436-1447`）。设备在线状态记录为 `active / lastActivityTime` 等，可以存为属性或时序（`application/src/main/java/org/thingsboard/server/service/state/DefaultDeviceStateService.java:109-133`）。

### 2.2 MQTT topic 布局
- 设备身份由认证后的会话决定，**topic 里不带设备 ID**：`v1/devices/me/telemetry`、`.../attributes`、`.../rpc/request/+`、`.../rpc/response/{id}`、`/provision/request`；v2 是短 topic，带 json/proto 后缀，并支持 OTA 分块（`common/data/src/main/java/org/thingsboard/server/common/data/device/profile/MqttTopics.java:44-108`）。
- 网关 topic：`v1/gateway/{connect,disconnect,telemetry,attributes,attributes/request,rpc,claim}`（`MqttTopics.java:58-67`），payload 以子设备名作为 key。

### 2.3 网关：一条连接承载多个子设备
- 网关本身也是一个 Device，靠 `additionalInfo.gateway=true` 识别。CONNECT 成功后，transport 为它创建 `GatewaySessionHandler`（`MqttTransportHandler.java:1268-1278`）。Sparkplug B 走单独的 `SparkplugNodeSessionHandler`（`:1291`）。
- `AbstractGatewaySessionHandler` 维护两个表：`devices: deviceName → 子设备会话` 和 `deviceFutures`（正在创建中的子设备）（`common/transport/mqtt/.../session/AbstractGatewaySessionHandler.java:106-132`）。第一次见到某个子设备时，调用 `GetOrCreateDeviceFromGateway`，成功后**为每个子设备单独 `registerAsyncSession`**（`:345-396`）。之后每个子设备在平台侧都是一个普通会话，所以 RPC 和属性推送可以复用设备通道。
- 自动建子设备在 core 侧完成（`DefaultTransportApiService.java:331-344,430-464`）：
  - 按 **子设备名 + 租户** 查找；按设备名加一个本地 `ReentrantLock` 防止并发重复创建（`:141,334`，这只是单节点锁）；
  - 用网关上报的 `deviceType` 字符串 `findOrCreateDeviceProfile`，**拼错的 type 会静默生成一个新 profile**（`:442-443`）；
  - 子设备继承网关的 customer；用 `additionalInfo.lastConnectedGateway` 记录最近经过的网关，每次换网关都会写一次 device 表（`:445-447,466-479`）；
  - 创建 `gateway → device` 关系，类型为 `GATEWAY_CREATED_RELATION`（`:459-462`）。Sparkplug 改名前会检查这条关系，防止网关接管别的设备（`:392-406`）。
- 网关批量遥测：每个子设备的消息逐个进入队列，全部进队后才回 PUBACK（`AbstractGatewaySessionHandler.java:454-514`，`remaining/ackSent`）。
- 子设备 RPC 下行：子设备会话收到 RPC 后，由父网关连接转换成 `v1/gateway/rpc` 发布（`AbstractGatewayDeviceSessionContext.java:104-107`）。

### 2.4 Provision
- Device Profile 的 provision 类型有 `DISABLED / ALLOW_CREATE_NEW_DEVICES / CHECK_PRE_PROVISIONED_DEVICES / X509_CERTIFICATE_CHAIN`（`common/data/.../DeviceProfileProvisionType.java`）。设备拿 profile 上的 provisionKey/Secret 申请；预置设备模式用服务端属性 `provisionState` 防止重复领取（`application/src/main/java/org/thingsboard/server/service/device/DeviceProvisionServiceImpl.java:66,145-189`）。

### 2.5 限流
- 租户和设备级限流写在 Tenant Profile 里，分三层，每层又分消息数、遥测消息数、数据点数：`transportTenant*`、`transportDevice*`、`transportGateway*`，另有 `transportGatewayDevice*`（`common/data/.../tenant/profile/DefaultTenantProfileConfiguration.java:49-82`）。
- 网关会话的独立限流在 `common/transport/mqtt/.../limits/GatewaySessionLimits.java`。另有可选的 IP 封禁，用于抵御错误凭据爆破（`thingsboard.yml:1473-1479`）。
- 除限流外还有月度配额：`maxTransportMessages/maxTransportDataPoints/maxREExecutions/maxDPStorageDays` 等（`DefaultTenantProfileConfiguration.java:107-121`）。

## 3. 数据管线

### 3.1 从 transport 到存储
1. transport 把 JSON 转成 `TbMsg`，类型 `POST_TELEMETRY_REQUEST`，`metaData.ts` 取自设备 payload（`DefaultTransportService.java:537-549`）。目标 rule chain 和 queue 名**取自 Device Profile 的 `defaultRuleChainId/defaultQueueName`**（`:1117-1142`）。`TbMsg` 定义在 `common/message/src/main/java/org/thingsboard/server/common/msg/TbMsg.java`。
2. 消息发到 rule-engine queue，分区按 originator 的 hash 计算（§1.3），同一设备的消息落在同一分区。
3. 根 rule chain 里的 `TbMsgTimeseriesNode` 解析 JSON，计算 ts（可选 `useServerTs`），确定 TTL：消息 metadata 的 `TTL` > 节点配置 > tenant profile 的 `defaultStorageTtlDays`，然后调用 `TelemetryService.saveTimeseries`（`rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/telemetry/TbMsgTimeseriesNode.java:120-160`）。
4. 保存策略可配：`OnEveryMessage / Deduplicate / WebSocketsOnly / Advanced`，分别控制写历史、写 latest、推 WS、触发 calculated field 这四个动作（`TbMsgTimeseriesNode.java:34-37,122-128`）。
5. 写入是批量异步的：`sql.ts.batch_size=10000 / batch_max_delay=100ms / batch_threads=3`，latest 和 attributes 各自也有批量参数（`thingsboard.yml:492-512`）。

### 3.2 latest 与历史分表，乱序处理
- Postgres：历史表 `ts_kv(entity_id uuid, key int, ts bigint, bool_v, str_v, long_v, dbl_v, json_v)`，主键 `(entity_id,key,ts)`，`PARTITION BY RANGE(ts)`；key 名映射到 `key_dictionary` 的整数 ID（`dao/src/main/resources/sql/schema-ts-psql.sql:6-24`）。默认按月分区（`thingsboard.yml:559-561`）。
- latest 单独存在 `ts_kv_latest(entity_id,key)`，写法是 upsert，并带一个 `version` 序列（`dao/src/main/java/org/thingsboard/server/dao/sqlts/insert/latest/sql/SqlLatestInsertTsRepository.java:30-61`）。
- **乱序**：默认 `update_by_latest_ts=true`，即 upsert 加条件 `WHERE ts_kv_latest.ts <= ?`，旧数据不会覆盖 latest，但历史表照常插入（`SqlLatestInsertTsRepository.java:25-26,58-61`；`thingsboard.yml:507-512` 的注释）。历史表遇到同一 `(entity,key,ts)` 时覆盖。
- 一种 "多值宽表 + 类型列" 的 EAV 存储，所有设备共用。
- 后端选择：`database.ts.type` 和 `database.ts_latest.type` 可选 `sql | timescale | cassandra`（`thingsboard.yml:305-310`）。可选 Citus 分布式 PG（`:311`；`dao/.../sql/citus/package-info.java`）。Timescale 版本把同样的表做成 hypertable，chunk 默认 7 天（`dao/src/main/resources/sql/schema-timescale.sql:8-38`；`thingsboard.yml:562-565`）。Cassandra 分区键是 `(entity_type, entity_id, key, partition)`，partition 默认按月（`dao/src/main/resources/cassandra/schema-ts.cql:6-26`；`thingsboard.yml:456-461`）。
- latest 前有缓存（caffeine 或 Redis，`CachedRedisSqlTimeseriesLatestDao.java`；`thingsboard.yml:866-868`）。

### 3.3 TTL / 保留
- **Cassandra** 使用原生的每行 TTL（`dao/.../timeseries/CassandraBaseTimeseriesDao.java:184-186`）。
- **Postgres / Timescale** 的保存接口**会忽略每条消息的 TTL**：`JpaSqlTimeseriesDao.save` 只用 ttl 计算计费用的 `dataPointDays`（`dao/.../sqlts/sql/JpaSqlTimeseriesDao.java:72-87`；`AbstractSqlTimeseriesDao.java:94-108`）。真正的清理由每天一次的存储过程完成：
  - `drop_partitions_by_system_ttl`：按系统 TTL 删除整个分区（`schema-ts-psql.sql:26`；`JpaSqlTimeseriesDao.java:101`）；
  - `cleanup_timeseries_by_ttl`：对每个租户/客户读取名为 `TTL` 的属性，然后执行 `DELETE ... WHERE entity_id IN (SELECT id FROM device WHERE tenant_id=...)`（`schema-ts-psql.sql:214-275`）。这是对大表的逐行删除，代价高。
- 其他表各有 TTL 任务：events、edge_events、alarms、rpc、audit_logs（`thingsboard.yml:566-622`）。

### 3.4 聚合查询
- 聚合函数只有 `MIN, MAX, AVG, SUM, COUNT, NONE`（`common/data/.../kv/Aggregation.java:10`）。
- **普通 PG 每个时间区间发一次 SQL**（`while (startPeriod < endPeriod)` 循环，`dao/.../sqlts/AbstractChunkedAggregationTimeseriesDao.java:116-151`），所以要用 `database.ts_max_intervals=700` 限制单次 API 最多生成的查询数（`thingsboard.yml:306`）。Timescale 版本用 `time_bucket` 一次查完（`dao/.../sqlts/timescale/AggregationRepository.java:32-48`）。
- 平台**没有持久化的降采样或连续聚合**。小时、日这类统计要靠 calculated field 的 `ENTITY_AGGREGATION`（§5.4），或者在查询时现算。

## 4. 实体与领域模型

- 层级：`Tenant → Customer（v4.4 有 parent_customer_id 多级）→ Device / Asset / Dashboard / Edge / User`（`schema-entities.sql` 中 customer 表有 `parent_customer_id`）。
- Device 必须属于一个 Device Profile；Asset 有 Asset Profile。Profile 承载了很多职责（`common/data/.../device/profile/DeviceProfileData.java:19-27`）：transport 配置（payload 类型、topic 过滤、LwM2M/SNMP 映射）、provision 配置、旧版 alarm 规则、默认 rule chain、默认 queue 和 OTA 包。
- 属性：`attribute_kv(entity_id, attribute_type, attribute_key)` 主键，scope 是 `CLIENT_SCOPE(1) / SERVER_SCOPE(2) / SHARED_SCOPE(3)`（`common/data/.../AttributeScope.java:13-15`）。client 属性由设备上报；shared 属性由平台下发，设备可以订阅；server 属性只在平台内可见。
- 关系表：`relation(from_id, from_type, to_id, to_type, relation_type_group, relation_type, additional_info)`，**没有 tenant_id 也没有外键**，`relation_type` 是自由字符串（`schema-entities.sql:521-531`）。内置约定只有 `Contains/Manages/Uses/ManagedByEdge...` 等常量（`common/data/.../relation/EntityRelation.java:36-41`）。同一张表还承担 rule chain 拓扑、entity group 成员、edge 分配等用途（`RelationTypeGroup.java:7-13` 中的 `RULE_CHAIN, RULE_NODE, FROM_ENTITY_GROUP, EDGE...`）。

**好的地方**
- 三种属性 scope 清楚地区分了"设备说的"、"平台定的"和"要下发给设备的"，配合订阅即可同步配置。
- latest 与历史分开存，查询"当前值"不用扫历史。
- Profile 把一类设备的接入方式和处理路由集中在一个地方。

**有问题的地方**
- 关系是自由图：没有类型约束，没有租户列，删除时没有外键级联，需要单独的 housekeeper 任务去清理（`application/src/main/java/org/thingsboard/server/service/housekeeper/`）。"冷机属于哪个冷站"这种业务事实只是一条字符串类型的边，数据库不保证它成立。
- Device Profile 可变，并且被网关的 `deviceType` 字符串隐式创建（§2.3）。修改 profile 会立刻影响所有设备的解析和路由，profile 本身没有版本概念。
- 子设备身份靠"租户内设备名"。网关里改名就等于新建一台设备，`lastConnectedGateway` 也只是 JSON 字段。
- 设备 ID、`additionalInfo`、`version` 等字段都是自由 JSON，业务语义散落在 JSON 里。

## 5. Rule Engine

### 5.1 结构
- Rule chain 由 rule node 组成，节点之间按关系名（Success/Failure/True/自定义）连接。节点 SPI 是 `TbNode.init/onMsg/destroy/onPartitionChangeMsg/upgrade`（`rule-engine/rule-engine-api/src/main/java/org/thingsboard/rule/engine/api/TbNode.java:17-37`）。用注解 `@RuleNode(type, configClazz, relationTypes, clusteringMode, version)` 声明（`RuleNode.java:21-59`）；启动时做 classpath 扫描（`application/.../component/AnnotationComponentDiscoveryService.java:51-90`）。内置约 100 个 `Tb*Node`。
- 脚本：TBEL（JVM 内）或 JS。JS 可以在本地执行，也可以通过队列交给远程 `msa/js-executor`（`thingsboard.yml:1388-1397`）。

### 5.2 Queue、提交策略与 ack
- Queue 是数据库里的实体，安装时预置三个（`application/.../install/DefaultSystemDataLoaderService.java:257-322`）：
  - `Main`：10 分区，`BURST`，`SKIP_ALL_FAILURES`，pack 超时 2s；
  - `HighPriority`：`BURST`，`RETRY_FAILED_AND_TIMED_OUT`；
  - `SequentialByOriginator`：`SEQUENTIAL_BY_ORIGINATOR`，`RETRY_FAILED_AND_TIMED_OUT`，重试 3 次。
- 提交策略：`BURST, BATCH, SEQUENTIAL_BY_ORIGINATOR, SEQUENTIAL_BY_TENANT, SEQUENTIAL`。处理策略：`SKIP_ALL_FAILURES, SKIP_ALL_FAILURES_AND_TIMED_OUT, RETRY_ALL, RETRY_FAILED, RETRY_TIMED_OUT, RETRY_FAILED_AND_TIMED_OUT`（`common/data/.../queue/SubmitStrategyType.java:6`、`ProcessingStrategyType.java:6`）。
- ack 语义：consumer 拉取一批消息（pack），提交给 actor，等待 `packProcessingTimeout`；处理策略根据成功、失败、超时的结果决定 commit offset 还是重投（`application/.../queue/ruleengine/TbRuleEngineQueueConsumerManager.java:120-156`）。**这是至少一次的批级语义。默认 Main 队列失败即跳过，`BURST` 下同一设备的消息也不保证顺序。**
- 每条消息在 rule chain 中能执行的节点数有上限：`maxRuleNodeExecutionsPerMessage`（`DefaultTenantProfileConfiguration.java:119`），用来防止环路。
- Debug：节点和 CF 的 debug 事件写入 event 表，按租户限流并有时长上限（`maxDebugModeDurationMinutes`，`DefaultTenantProfileConfiguration.java:121`；`thingsboard.yml:745-751`）。

### 5.3 告警
- 旧方式：Device Profile 里的 `alarms` 列表加上 `TbDeviceProfileNode`。**v4.4 已将该节点标为 `@Deprecated`，名称改为 "device profile (deprecated)"**（`rule-engine/.../profile/TbDeviceProfileNode.java:41-53`）。
- 新方式：类型为 `ALARM` 的 calculated field，定义见 `common/data/.../cf/AlarmRuleDefinition.java`、`cf/configuration/AlarmCalculatedFieldConfiguration.java`。
- 仍可用 `TbCreateAlarmNode / TbClearAlarmNode` 在规则链里显式建告警和清告警（`rule-engine/.../action/`）。
- 存储上，同一 originator 同一 type 只能有一条活动告警，由 DB 函数 `create_or_update_active_alarm` 保证（`dao/src/main/resources/sql/schema-functions.sql:7`；索引 `schema-entities-idx.sql:7-20`）。

### 5.4 Calculated Fields（v4.4 的重点）
- 类型：`SIMPLE, SCRIPT, GEOFENCING, ALARM, PROPAGATION, RELATED_ENTITIES_AGGREGATION, ENTITY_AGGREGATION`（`common/data/src/main/java/org/thingsboard/server/common/data/cf/CalculatedFieldType.java`）。参数可以来自本实体、按关系路径动态解析的实体，或当前 owner（`cf/configuration/RelationPathQueryDynamicSourceConfiguration.java`、`CurrentOwnerDynamicSourceConfiguration.java`）。输出可以直接写入，也可以先经过 rule chain（`TimeSeriesImmediateOutputStrategy` / `TimeSeriesRuleChainOutputStrategy`）。
- 运行时：每个实体一个 `CalculatedFieldEntityActor`，由 `CalculatedFieldManagerActor` 管理（`application/.../actors/calculatedField/`）。CF 状态持久化有两种：in-memory 模式用 RocksDB（`RocksDBCalculatedFieldStateService.java:26`），Kafka 模式用 compacted topic（`KafkaCalculatedFieldStateService.java:45`；`thingsboard.yml:2497-2519`）。
- 意义：派生指标（例如冷站 COP = 制冷量 / 功率）和告警从"写在规则链脚本里"变成了"挂在实体上、有类型的声明式配置"。这是 v4.4 最值得参考的方向。

## 6. RPC

- 单向 RPC：发出即视为成功（`DeviceActorMessageProcessor.java:227-229`）。双向 RPC：注册到 `toDeviceRpcPendingMap`，按 `expirationTime` 设定定时器，到期回调超时（`application/src/main/java/org/thingsboard/server/actors/device/DeviceActorMessageProcessor.java:191-240,494-519`）。
- 持久化 RPC：先写入 `rpc` 表（状态为 `QUEUED`），落库成功后再发送；设备离线时保持排队，重连后补发（`:203-209,420-448`）。状态有 `QUEUED, SENT, DELIVERED, SUCCESSFUL, TIMEOUT, EXPIRED, FAILED, DELETED`（`common/data/.../rpc/RpcStatus.java:16-23`）。RPC 记录有 TTL（`rpcTtlDays`）。
- 提交策略：`BURST / SEQUENTIAL_ON_ACK_FROM_DEVICE / SEQUENTIAL_ON_RESPONSE_FROM_DEVICE`；持久化 RPC 最多重试 5 次；可选在投递超时后关闭会话（`thingsboard.yml:715-738`；`DeviceActorMessageProcessor.java:141,329-330`）。
- "已送达"的判定依赖协议：MQTT QoS1 以 PUBACK 为准，QoS0 和 HTTP 视为立即送达（`thingsboard.yml:723-737` 注释）。
- 网关后的设备：RPC 先到达子设备的 DeviceActor，再通过子设备的 async session 交给网关连接，转换成 `v1/gateway/rpc`（§2.3）。子设备会话因超时被注销后，RPC 回到 QUEUED 状态，等网关下次为该子设备发消息时再补发（`thingsboard.yml:727-733`）。
- 设备属于某个 Edge 时，RPC 改写为 edge event，交给边缘处理（`DeviceActorMessageProcessor.java:221,278-316`）。

## 7. Edge（云侧实现）

- 本仓库只有**云端那一侧**。边缘端产品 ThingsBoard Edge 不在这里，仓库中也找不到 cloud_event 之类的边缘侧代码。
- 协议：一个双向流 gRPC `EdgeRpcService.handleMsgs(stream RequestMsg) returns (stream ResponseMsg)`，端口 7070，edge 用 `edgeRoutingKey + edgeSecret` 认证，消息带版本号（`common/edge-api/src/main/proto/edge.proto:15-19,21-24,60-107`）。上行和下行消息都带 `uplinkMsgId/downlinkMsgId`，对端逐条回复 success/error 作为 ack（`edge.proto:549-600`）。
- 下行同步：云端实体变更由 `EdgeEventSourcingListener` 通过 `@TransactionalEventListener` 捕获（`application/.../service/edge/EdgeEventSourcingListener.java:77-168`），写入 `edge_event` 表；在 Kafka 模式下改为写入 Kafka（`KafkaEdgeEventService.java:28`）。`edge_event` 按 created_time 分区，`seq_id` 是 IDENTITY 并允许 CYCLE（`schema-entities.sql:1085-1099`）。
- 游标：每个 edge 的读取位置记在服务端属性 `queueStartTs/queueStartSeqId` 里（`application/.../edge/rpc/processor/PostgresGeneralEdgeEventsDispatcher.java:37-38,147-171`）。读取时把起点回退 60s（`misordering_compensation_millis`），用来补偿时间戳乱序（`GeneralEdgeEventFetcher.java:29-38`；`thingsboard.yml:2034`）。这意味着**投递语义是至少一次，接收方必须能处理重复**。
- 全量同步：edge 连接时可以请求 `SyncRequestMsg{fullSync}`（`edge.proto:105-107`），云端按 `EdgeSyncCursor` 依次推送各类实体。
- 离线行为（云侧能看到的部分）：edge 离线期间事件在 `edge_event` 里累积，TTL 默认 1 个月（`thingsboard.yml:583-585`）。超过 TTL 的事件会丢失，只能靠全量同步恢复。edge 断线超过 60s 才发通知（`thingsboard.yml:2046-2050`）。edge 的上行限流在 tenant profile 中（`DefaultTenantProfileConfiguration.java:163-169`）。

## 8. 多租户与授权

- 用户的 `Authority` 只有 `SYS_ADMIN / TENANT_ADMIN / CUSTOMER_USER` 三种，另有几种 token 类型（`common/data/.../security/Authority.java:7-13`）。v4.4 叠加了 PE 的细粒度 RBAC：`role(permissions JSON)`，以及把角色授予"用户组到实体组"的 `group_permission(role_id, user_group_id, entity_group_id)`（`schema-entities.sql:720-744`）。权限检查入口是 `application/.../security/permission/DefaultAccessControlService.java`、`TenantAdminPermissions.java`、`CustomerUserPermissions.java`。
- 隔离方式：**共享库共享表，在行上带 `tenant_id`**，由 DAO 和 controller 负责按租户过滤。`relation`、`ts_kv`、`attribute_kv` 都没有 tenant_id 列，只靠 entity_id 间接归属租户，所以任何直接的 SQL 或报表都必须先 join 实体表。
- 资源隔离：tenant profile 中的配额和限流（§2.5），外加可选的 isolated rule-engine queue 和专属节点（§1.3）。Cassandra 的读写还有按租户的限流（`DefaultTenantProfileConfiguration.java:153-160`）。
- 实体数量上限：`maxDevices/maxAssets/maxCustomers/...`，以及各类 TTL：`defaultStorageTtlDays/alarmsTtlDays/rpcTtlDays`（`DefaultTenantProfileConfiguration.java:30-45,180-185`）。

## 9. 扩展点

- 新协议有两种做法：
  - 一是写一个 transport 模块，实现 Netty/Californium 等服务端，复用 `TransportService`（凭据校验、`process(PostTelemetryMsg...)`、会话注册）。参照 `common/transport/{mqtt,coap,http,lwm2m,snmp}`。
  - 二是写 Integration：`AbstractIntegration`，配合 uplink/downlink converter（脚本型 `ScriptUplinkDataConverter`）把第三方报文转换成标准消息（`common/integration/integration-api/src/main/java/org/thingsboard/integration/api/`）。仓库已经带了 OPC-UA、MQTT、HTTP、Kafka、AWS、Azure 等实现（`common/integration/*`）。
- 新 rule node：实现 `TbNode`，加 `@RuleNode` 注解，放到 `plugins.scan_packages` 能扫描到的包里（§5.1）。配置版本通过 `version()+upgrade()` 迁移。
- 新的派生逻辑应优先写成 calculated field，而不是 rule node（§5.4）。

## 10. 源码里看得到的弱点和成本

1. **两套运行时**：in-memory 只适用于 monolith，而且不持久化；一旦要横向扩展，就需要 Kafka、ZooKeeper、Redis/Valkey 全套（§1.2）。再加上 Cassandra（hybrid 模式）、Citus、EDQS、js-executor，一个完整集群有 10 种以上组件。
2. **配置面巨大**：`thingsboard.yml` 有 2766 行，Kafka 的 topic 和 consumer 参数还要按 topic 单独调（`thingsboard.yml:2140-2326`）。
3. **分区靠取模，不是一致性哈希**：节点数变化时所有分区重算，actor 冷启动成本高，所以需要 `recalculate_delay` 这类补丁（§1.3）。
4. **默认至少一次、允许跳过失败**：Main 队列默认 `SKIP_ALL_FAILURES + BURST`，失败的消息会静默丢弃，同一设备的消息也可能乱序（§5.2）。in-memory 模式下宕机会丢失已 ack 的消息。
5. **TTL 语义因后端而异**：同一个 TTL 配置在 Cassandra 上按行生效，在 PG/Timescale 上被忽略，只剩系统和租户级 DELETE（§3.3）。
6. **聚合在普通 PG 上按区间逐条查询**，也没有持久化的 rollup（§3.4）。
7. **数据模型约束弱**：relation 没有 tenant 列和外键；profile 可以被字符串隐式创建；子设备身份只是设备名；一张 relation 表承担业务拓扑、规则链结构和分组成员等多种用途（§4）。
8. **单节点锁和最终一致**：网关建设备的去重只用 JVM 内的锁（`DefaultTransportApiService.java:141,334`），多 core 节点时依赖 DB 唯一约束兜底；edge 游标回退 60s 后重放（§7）。
9. **职责不断叠加**：设备 profile 里的告警、rule chain 里的告警、CF 里的告警三套机制并存，处在迁移期（§5.3）；EDQS 又多出一份查询副本（§1.4）。
10. **许可证**：BUSL，商用免费额度只有 100 台设备、1 台服务器，而且要求保留品牌（§0）。

---

## 值得吸收（Worth absorbing）

1. **transport 无状态，通过 API 向 core 校验凭据**：transport 进程不连数据库，凭据校验走请求/响应，并在 core 侧缓存。这样可以单独扩展接入层。证据：`DefaultTransportService.java:341-395`，`DefaultTransportApiService.java:217-300`，`DeviceCredentialsServiceImpl.java:47-72`。
2. **topic 不带设备 ID，身份来自认证会话**（`v1/devices/me/*`），消除了"冒用别的设备 topic"这一整类问题。证据：`MqttTopics.java:44-56`。
3. **一条网关连接映射为多个子设备会话**：子设备在平台侧和直连设备没有区别，RPC 和属性下行也复用设备通道。PUBACK 在所有子消息入队后才发。证据：`AbstractGatewaySessionHandler.java:106-132,345-396,454-514`。
4. **网关与子设备之间显式建立"创建者"关系，并在越权操作前校验**，防止一台网关冒领其他设备。证据：`DefaultTransportApiService.java:392-406,459-462`。
5. **latest 与历史分开存，latest 只接受更新的 ts**（`WHERE latest.ts <= new.ts`），历史表照常插入。乱序和补传都不会把"当前值"写回旧值。证据：`SqlLatestInsertTsRepository.java:25-61`。
6. **按时间范围分区的窄表，key 用整数字典**，配合 Timescale `time_bucket` 做聚合。PG 单机就能支撑起步阶段。证据：`schema-ts-psql.sql:6-24`，`timescale/AggregationRepository.java:32-48`。
7. **三种属性 scope（client/server/shared）**：区分设备上报、平台私有、平台下发三类配置，shared 属性用作设备期望配置的同步通道。证据：`AttributeScope.java:13-15`。
8. **持久化 RPC 状态机**：先落库为 QUEUED，然后经过 SENT、DELIVERED，最终到 SUCCESSFUL、TIMEOUT 或 EXPIRED；设定过期时间，离线时排队，重连后补发，并把"送达"和"有响应"区分开。适合冷站的控制指令审计。证据：`RpcStatus.java:16-23`，`DeviceActorMessageProcessor.java:191-240,420-519`，`thingsboard.yml:715-738`。
9. **派生量和告警做成挂在实体上的声明式计算字段**：参数可以按关系路径解析，状态持久化，输出直写或进入后续流程。这正对应冷站 COP、负荷率、效率告警这类需求。证据：`CalculatedFieldType.java`，`cf/configuration/*`，`RocksDBCalculatedFieldStateService.java:26`。
10. **按 tenant profile 声明分层限流和配额**（租户/设备/网关/网关子设备 × 消息数/数据点数），用一份配置对象表达资源边界。证据：`DefaultTenantProfileConfiguration.java:30-185`。
11. **边缘同步用的协议形态**：一条 gRPC 双向流，每条消息带 ID 并逐条 ack，云端事件表加上持久游标，可请求全量同步作为兜底。证据：`edge.proto:15-19,85-107,549-600`，`PostgresGeneralEdgeEventsDispatcher.java:37-38,147-171`。
12. **预置设备 provision 用状态位防止重复领取**：`provisionState` 保证一次性领取，适合现场批量安装网关。证据：`DeviceProvisionServiceImpl.java:66,156-189`。

## 不应照搬（Should NOT be copied）

1. **一期不要引入 Kafka、ZooKeeper、Redis、Cassandra、EDQS 这一整套**：ThingsBoard 自己的单机形态就是 monolith 加 PG，分布式组件只在多节点时才需要。证据：`thingsboard.yml:2133-2134,127-129,856-858`，`InMemoryMonolithQueueFactory.java:53`。
2. **不要用不持久的内存队列，再在入队后就回 ack**：宕机会丢掉已确认的数据。单机方案应直接写 PG（或者用 PG 做 outbox），再 ack。证据：`DefaultInMemoryStorage.java:21,46`，`DefaultTransportService.java:1142`。
3. **不要默认"失败即跳过"并且 BURST 乱序**：失败要显式暴露（死信或错误计数），同一设备内的处理要保序。证据：`DefaultSystemDataLoaderService.java:257-275`。
4. **不要做通用的可视化 rule chain 引擎加脚本节点**：约 100 种节点、JS/TBEL 双引擎、远程 js-executor，加上 debug 事件存储，对一个小团队维护成本太高。冷站的业务规则应当写成有类型的领域代码或计算字段。证据：`rule-engine/rule-engine-components`（约 100 个 `Tb*Node`），`thingsboard.yml:1388-1430`，`msa/js-executor/`。
5. **不要用无类型、无租户列、无外键的自由关系表承载业务拓扑**：冷站、冷机、水泵、表计之间的归属应该是有约束的领域表。证据：`schema-entities.sql:521-531`，`RelationTypeGroup.java:7-13`。
6. **不要让网关上报的字符串隐式创建设备类型或模型，也不要只用设备名作为身份**：模型应当预先登记并有版本，子设备身份用网关作用域内的稳定点位 ID。证据：`DefaultTransportApiService.java:430-447`。
7. **不要让 TTL 语义随存储后端变化，也不要用大表逐行 DELETE 做保留期清理**：保留策略应按分区或 chunk 整块删除（例如 Timescale 的 retention policy），并在契约里写明。证据：`JpaSqlTimeseriesDao.java:72-87`，`schema-ts-psql.sql:214-275`。
8. **不要在普通 PG 上逐区间循环做聚合**，也不要把"日、月能耗"留到查询时现算。能耗统计应由 owner 持久化 rollup。证据：`AbstractChunkedAggregationTimeseriesDao.java:116-151`，`thingsboard.yml:306`。
9. **不要让三套告警机制并存**（profile 告警、rule node 告警、CF 告警）。一个不变量只保留一个 owner。证据：`TbDeviceProfileNode.java:41-53`，`TbCreateAlarmNode.java`，`AlarmCalculatedFieldConfiguration.java`。
10. **不要复制代码或 UI**：4.4 起采用 BUSL-1.1，商业免费只覆盖 ≤100 设备、单服务器，并要求保留品牌和 license key。只能吸收设计思想，需要代码级参考时用 4.3 及以前的 Apache 版本，并固定 tag。证据：`LICENSE:13-43`，`README.md:147-151`。
