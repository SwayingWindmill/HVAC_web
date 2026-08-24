# ThingsBoard CE 运行平台、部署、HA、可观测性与升级裁决

Status: `D10_ADJUDICATION_COMPLETE`

Date: 2026-08-18

Issue: #235

Reference: ThingsBoard CE `v4.3.1.1`, commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`

## 1. 执行结论

本轮不假设 HVAC Web 当前实现正确，也不把 ThingsBoard 当成目标架构模板。结论以固定版本源码、上游配置、官方文档、本地 CodeGraph 反向审查和 HVAC 控制安全边界共同决定。

核心裁决如下：

- **当前 Phase 1 是可恢复的单服务器部署，不是 HA。** 单机 Docker Compose 可以继续作为第一阶段产品基线，但文档、验收和运维界面不得使用“高可用”表述。数据库、MQTT、Redis、ClickHouse 与观测后端均存在单点故障。
- **保留 PostgreSQL Outbox、租约、Fence、幂等 Inbox、Scheduler Job 状态机和 ClickHouse/PostgreSQL 可证明发布。** 对控制链而言，这些本地语义比 ThingsBoard 的内存队列、默认 Kafka 参数和 Actor 内存状态更适合当前阶段。
- **吸收 ThingsBoard 的按实体串行执行、分区所有权变化、队列类型化策略、版本化缓存写入、配额分类、资源用量视图、升级版本预检和类型化 Housekeeping Pattern。** 这些是行为模式，不意味着复制其 Java Actor、Kafka、ZooKeeper、Redis 或 Git 运行时。
- **替换本地平台级配额、Readiness、迁移原子性、缓存权威、维护任务治理和配置发布的薄弱实现。** 这些差距足够大，不能因为已有代码而保留。
- **拒绝把 ThingsBoard 默认值当成生产 HA。** 固定版本的 Kafka 默认副本数/最小同步副本、进程内限流、内存队列、VC 超时后提交消费进度、Housekeeper 固定睡眠重试及升级中吞掉部分数据更新失败，都不是 HVAC Web 应接受的可靠性语义。
- **拒绝在控制或业务真值路径执行任意租户 JavaScript。** ThingsBoard JS Executor 的进程分离、超时和结果大小限制可参考，但其 `node:vm` 不是不可信代码安全边界；Node.js 官方文档也明确禁止将其用于不可信代码。
- **不保留旧路径兼容层。** 数据库升级仍必须受控且可验证，但不以双写、旧路由 fallback、运行时版本猜测或永久兼容层换取“平滑”。每次发布采用明确的一次性迁移、版本门禁和整体回退策略。

这份文档完成“参考源码到目标设计”的裁决，不表示上述缺口已经实现或通过生产认证。

## 2. 范围和跨域边界

本轮覆盖 12 项能力：

1. 单体与微服务部署拓扑；
2. TB Node、Web UI、Transport、JS/VC Executor、EDQS 等进程边界；
3. Actor、分区、集群发现与节点间消息；
4. 队列实现、分区、统计、背压和恢复；
5. 本地缓存、Redis 与缓存一致性；
6. DAO、数据库变体、迁移和升级；
7. 脚本隔离、Job、Housekeeper 与 TTL；
8. Stats、Lifecycle、Error、Health、System Info 与 Usage；
9. Rate、Resource、Entity 与 API Limits；
10. Entity Version Control、Import/Export 与 Auto Commit；
11. 升级、版本兼容、打包、Docker、HAProxy 与 REST Client；
12. TLS、配置、Secret 与运行安全。

边界归属：

- D05 负责业务 Rule Engine 与业务队列执行语义；D10 负责平台队列、分区、HA 和运行治理。
- D02 负责实体 Import/Export 语义；D10 负责版本执行器、发布包、升级兼容和仓库基础设施。
- D04 负责 Telemetry/TTL 业务语义；D10 负责维护 Worker、清理任务、容量和运行治理。
- D01 负责身份与 Secret Policy；D10 负责部署 TLS、配置装载、Secret 交付和轮换运行机制。
- D08 负责 Edge 行为；D10 不用云端集群机制替代 Edge Store-and-Forward 和本地安全控制。

## 3. 固定证据基线

### 3.1 ThingsBoard 源码

上游证据固定在 Apache-2.0 的 `thingsboard/thingsboard` tag `v4.3.1.1`、commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`。本轮直接审查的关键入口包括：

- `application/src/main/resources/thingsboard.yml`；
- `common/queue/.../HashPartitionService.java`、`ZkDiscoveryService.java`、`ConsistentHashCircle.java`；
- `application/.../DefaultActorService.java`、`AppActor.java`、`TenantActor.java`、`StatsActor.java`；
- `common/queue/.../InMemoryTbQueueConsumer.java`、`TbKafkaConsumerTemplate.java`、`AbstractTbQueueConsumerTemplate.java`；
- `common/cache/.../RedisTbTransactionalCache.java`、`VersionedRedisTbCache.java` 及 Caffeine 实现；
- `common/cache/.../DefaultRateLimitService.java`、`RateLimitProcessingFilter.java`、`DefaultTransportRateLimitService.java`；
- `application/.../DefaultSystemInfoService.java` 与 System Info Controller；
- `application/.../ThingsboardInstallService.java`、`DefaultDatabaseSchemaSettingsService.java`、`DefaultDataUpdateService.java`；
- `common/version-control/.../DefaultClusterVersionControlService.java` 与 `DefaultGitVersionControlQueueService.java`；
- `application/.../HousekeeperService.java`、`HousekeeperReprocessingService.java`；
- `msa/js-executor/api/jsExecutor.ts`。

官方架构文档仅用于解释意图，最终行为以固定源码为准：[Architecture](https://thingsboard.io/docs/reference/architecture/)、[Microservices](https://thingsboard.io/docs/reference/architecture/microservices/)、[Caching](https://thingsboard.io/docs/reference/architecture/caching/)、[Docker Compose](https://thingsboard.io/docs/installation/docker-compose-setup/)、[Upgrade](https://thingsboard.io/docs/installation/upgrade-instructions/)。

特别说明：当前官方文档可能使用 consistent hashing 的概括，但固定源码中的活动分配路径是哈希后取模和按服务列表取模；`ConsistentHashCircle` 在该版本没有发现生产调用。本文不采用比源码更强的宣传性表述。

### 3.2 HVAC Web 本地源码

本地证据通过 CodeGraph 调用路径和源文件反向审查获得，主要包括：

- `deploy/platform/phase1/architecture-baseline.v1.json`、`alignment-matrix.v1.json` 与 canonical Compose；
- `deploy/platform/phase1/migrations/{manifest.v1.json,run-phase1-migrations.sh}`；
- `deploy/platform/phase1/backup`、`recovery` 与 `docs/operations/go-platform-production-rollout.md`；
- `libs/observability/{runtime,metrics,trace,exporter}.go` 与 Phase 1 Prometheus/OTel/Loki/Tempo/Grafana/Alert 资产；
- `services/platform-gateway/internal/gateway/operations_agent.go`；
- `services/telemetry-runtime-service/internal/telemetry/latest_cache.go`；
- `services/outbox-relay/internal/relay/relay.go`、`libs/domainoutbox/store.go`；
- `services/command-dispatcher/pkg/commanddispatcher/{durable,verification}.go`；
- `services/scheduler-service/internal/scheduler/{store,stats}.go` 及 Metric Worker 作业存储。

所有本地实现仍按 `UNVERIFIED` 对待；这里的 KEEP 只针对经过本轮源码审查的具体语义，不是对整个模块的背书。

## 4. ThingsBoard 解决的问题与实现行为

### 4.1 部署拓扑和运行分区

ThingsBoard 同时支持 monolith、TB Core、TB Rule Engine、Transport、JS Executor、VC Executor、EDQS 等服务类型。其价值是把协议接入、规则计算、脚本和版本仓库按故障域及伸缩维度拆开。

`HashPartitionService` 按实体哈希与分区数量确定分区，再根据服务列表和队列类型分配所有者。拓扑变化会重新计算分配；Tenant Actor 在失去 TB Core 或 Rule Engine 分区时停止相应子 Actor，在获得所有权时重建运行状态。Actor 是进程内串行执行单元，其状态可从数据库和服务重建，不是耐久真值。

值得吸收的是：

- 按聚合/实体键保证局部顺序；
- 所有权获得、失去和重建是显式生命周期；
- 接入、计算、脚本等按失败和资源边界分离；
- 租户隔离队列允许重负载租户不拖垮共享队列。

不应直接复制的是：

- 为单服务器 Phase 1 引入自定义 Actor Runtime、ZooKeeper 和 Kafka；
- 把取模重分配描述为最小迁移的 consistent hashing；
- 让进程内 Actor 状态或分区所有权承担控制命令真值；
- 在没有吞吐、热点或故障证据时提前拆成完整微服务矩阵。

### 4.2 队列、背压和恢复

ThingsBoard 提供 In-memory 和 Kafka 队列、主题级参数、消费者统计及手动提交。Kafka 分区可在正确选键时提供实体内顺序。

固定源码同时暴露了重要边界：

- In-memory consumer 使用无界 `LinkedBlockingQueue`，poll 后即移除，commit 是 no-op；进程崩溃会丢失已 poll 消息，也没有可靠背压。
- Kafka producer 没有在固定配置中显式启用幂等生产；默认副本数和最小同步副本通常为 1，不能据此声称 HA。
- 通用 consumer template 的 poll/commit 锁等待和坏消息处理可能阻塞进度。
- 队列存在并不自动提供端到端 exactly-once；业务侧仍需要幂等、Fence、可重放和结果核验。

因此只吸收“队列类型化、按实体选键、独立策略和可观测统计”，不复制默认耐久性结论。

### 4.3 缓存一致性

ThingsBoard 的 Redis Transactional Cache 使用 WATCH/MULTI、TTL、Negative Cache 与 Tombstone，避免失效后旧值重新填充；Versioned Redis Cache 用 Lua 只接受更高版本写入。Caffeine 实现提供按 Cache 的容量、TTL 和统计。

这些 Pattern 适合本地可重建 Projection，但不能证明数据库与 Redis 跨存储原子，也不能把缓存提升为业务权威。

### 4.4 配额和限流

ThingsBoard 把限制归入 Tenant Profile、REST、Transport、Device、Gateway、Gateway Device、Telemetry Data Point、WebSocket Subscription 等不同维度。拒绝时返回明确限制结果，并可产生运维通知。这比一个全局 requests-per-minute 数字更接近平台治理。

但固定源码中的核心 Bucket/Cache 主要是进程内状态；多实例下不能天然提供严格一致的全局配额。System Tenant/Administrator 还存在绕过路径。可吸收的是 Policy Taxonomy、Scope、错误码和通知，不是进程内实现。

### 4.5 可观测性和系统信息

ThingsBoard 有 Actuator/Micrometer、Stats Actor、System Info Service、资源不足通知以及 Prometheus/Grafana 部署资产。System Info 会采样 CPU、内存、磁盘和服务信息，并保存为带 TTL 的 Telemetry。

其固定默认配置只暴露有限 Actuator Endpoint，Prometheus 需要显式开启；源码中没有发现足以证明所有依赖健康的自定义 readiness/liveness 体系。这里本地 OpenTelemetry Trace、集中日志和低基数指标设计更强，不应被 ThingsBoard 替换。

### 4.6 安装、升级与版本控制

ThingsBoard 将安装/升级作为单独进程，升级前验证产品和数据库版本，随后执行 Schema、数据更新、Rule Node 升级、Widget/Resource 更新，最后写入目标 Schema Version。这个“一次性 Migrator + 产品/版本预检 + 顺序升级”模式值得吸收。

不应复制的行为：

- `SKIP_SCHEMA_VERSION_CHECK` 可绕过版本门禁；
- 部分 Rule Node 数据更新在内部捕获异常并继续，而外层仍可能推进最终版本；
- 没有自动得到应用排空、只读维护、失败恢复和整体回退协议；
- 版本号更新不能替代逐步骤成功证据。

VC Executor 将 Tenant Repository 分区到执行器，按 Tenant 单线程串行任务，支持 init/test/list/commit、分块实体 JSON 和 Auto Commit。其 Pattern 对版本化配置有启发，但固定源码在 pack 超时后仅记录日志并提交消费进度，可能丢失未完成工作；运行时每租户 Git 仓库和 CRUD 自动提交也会把产品数据库、Secret 与发布流程混在一起。

### 4.7 JS Executor、Job 和 Housekeeper

ThingsBoard JS Executor 的进程隔离、脚本缓存、执行超时和结果大小上限值得参考。但其 sandbox 路径基于 Node `vm.Script/runInNewContext`，非 sandbox 路径甚至没有同等级执行超时。Node.js 官方文档明确说明 [`node:vm` 不是安全机制，不能用于运行不可信代码](https://nodejs.org/api/vm.html)。因此它不能成为多租户 HVAC 控制脚本边界。

Housekeeper 提供类型化任务、单线程处理、超时、最大重试和失败通知；但 Reprocessing 使用全局单 Kafka 分区、固定 sleep，并忽略部分 producer 发送结果。可吸收的是任务状态和运维通知，不是其具体重试管线。

## 5. Domain 模型对照

### 5.1 ThingsBoard 参考模型

```text
ServiceInfo -> ServiceType -> Partitions -> QueueConsumer
TenantProfile -> LimitedApi/TransportLimit -> RateLimitState
ActorId -> ActorMailbox -> RuntimeState(rebuildable)
CacheKey -> Version/TTL/Tombstone -> CachedValue
HousekeeperTask -> Attempt -> Reprocessing
DatabaseSchemaVersion -> UpgradeStep -> DataUpdate
VersionControlRequest -> TenantRepository -> EntityChunk/Commit
SystemInfo/ApiUsageState -> Telemetry/Notification
```

优点是平台治理维度比较完整；弱点是部分状态只在进程内、可靠性依赖部署参数，而且产品实体、运行时仓库和凭据边界可能耦合。

### 5.2 HVAC Web 当前模型

```text
DomainEvent -> PostgreSQL Outbox -> Relay -> Consumer Inbox
JobInstance -> JobAttempt -> Lease -> RetryWait/Dead
CommandIntent -> DispatchLease/Fence -> Connector -> Verification
DurableFact(PostgreSQL/ClickHouse) -> Versioned Redis Projection
Runtime -> Metric/Trace/Log -> Prometheus/Tempo/Loki
MigrationManifest -> Sanitized SQL -> SchemaMigration Hash
Backup/RecoveryTarget -> RestoreDrill Evidence
```

优点是控制链具备耐久租约、Fence、核验和明确不确定结果，跨存储发布有可证明恢复路径。缺点是平台配额、连续依赖健康、维护任务、版本发布和多节点所有权尚不完整。

### 5.3 目标模型

```text
AvailabilityTier
  -> DeploymentRevision
  -> DependencySet
  -> RecoveryObjective + Evidence

PlatformPolicy
  -> LimitDefinition(scope, resource, window, action)
  -> UsageCounter
  -> LimitDecision + Notification/Audit

WorkQueue
  -> WorkItem(entityKey, priority, idempotencyKey)
  -> Lease + Attempt + Backoff
  -> Completed | DeadLetter

ReleaseArtifact
  -> Product/Schema Version
  -> Immutable Digest
  -> Preflight -> Drain/Migrate/Deploy/Verify
  -> Rollback Decision

ProjectionCache
  -> SourceRevision
  -> Value | Tombstone
  -> Rebuild/Repair Status
```

`AvailabilityTier`、`PlatformPolicy`、`WorkQueue`、`ReleaseArtifact` 和 `ProjectionCache` 是平台概念；它们不拥有 Alarm、Command、Telemetry、Rule 或 Registry 的业务真值。

## 6. 核心目标流程

### 6.1 Phase 1 发布与升级

```text
immutable image/artifact digest
  -> product + schema version preflight
  -> backup readiness and restore evidence check
  -> stop admission / drain owned work
  -> run one-shot migration transaction(s)
  -> record step outcome atomically
  -> start services
  -> dependency-aware readiness + smoke + safety gates
  -> promote or roll back the complete release
```

禁止通过旧路由 fallback、双写或忽略版本不匹配继续运行。若 Schema 已完成不可逆迁移，回退必须使用预先验证的数据库恢复/前滚方案，不能假装仅回退镜像即可安全恢复。

### 6.2 平台耐久任务

```text
typed WorkItem
  -> durable enqueue with idempotency key
  -> SKIP LOCKED claim + lease
  -> execute with timeout and cancellation
  -> complete atomically
  -> retry with bounded exponential backoff + jitter
  -> DEAD with queryable reason and operator action
```

所有业务关键消费者都必须声明：是否可重复执行、顺序键、最大尝试、超时、结果核验、死信处置和积压 SLO。

### 6.3 限流决策

```text
authenticated request/device message
  -> exact Tenant/Site/Actor/Resource classification
  -> load active versioned LimitPolicy
  -> atomically reserve usage
  -> ALLOW | REJECT | DEGRADED
  -> stable error + retry metadata + metric
  -> notification/audit for policy events
```

控制写、身份写和 Secret 操作在限流状态不可判定时 fail closed；低风险只读是否 fail open 必须由资源类型显式声明，不能由异常路径默认决定。

### 6.4 多实例所有权（仅达到触发门槛后）

```text
orchestrator membership
  -> partition assignment revision
  -> old owner stops claiming
  -> lease/fence expires or transfers
  -> new owner rebuilds projection
  -> readiness only after reconciliation
```

本流程仍以数据库 Lease/Fence 和幂等记录为安全边界，不以节点内 Actor 状态为控制真值。

## 7. 本地源码级反向审查

### 7.1 已验证可保留的实现

- `domainoutbox` 使用每 Consumer Delivery、`FOR UPDATE SKIP LOCKED`、Aggregate Version Fence，并在完成时原子写 Inbox 与 Delivery。
- Command Dispatcher 的 Dispatch/Verification 使用耐久 Claim/Lease、Pre-send Fence、取消和读回；在 Connector 前 fail closed。
- Scheduler 的一次 Cycle 分事务完成取消、Retry Promotion、Expired Lease Recovery、Pending Promotion 和 Due Schedule Scan；Schedule Scan 在同一事务中创建带 Dedup Key 的 Job 并推进 `next_fire_at`。
- Scheduler 对 Metric Job 在租约过期且未达到 `max_attempts` 时进入 `RETRY_WAIT`，达到上限后进入 `DEAD`；非 Metric Job 默认失败并要求 reconciliation，避免盲目重放未知外部副作用。
- Queue Stats 提供 READY、RETRY_WAIT、RUNNING 和最老 READY Age；这是可扩展的平台队列指标基础。
- Metric/Forecast Cross-store Publication 在 ClickHouse 成功、PostgreSQL Complete 失败时保留 `PERSISTING`，由 Reconcile 证明结果存在后补完，优于把缓存或消费提交当作事实。
- 观测库已有低基数标签校验、Prometheus、W3C Trace Context、异步 Exporter 和集中 Trace/Log/Metric 后端。
- Phase 1 已定义不可变 Digest、分 cohort 发布、安全门禁、分层 RPO/RTO、外部备份和季度 Restore Drill 证据机制。

这些 KEEP 都有边界：固定重试、未覆盖所有 Work Type、证据模板存在但不等于现场演练已完成，均不能被表述成“平台能力已完整”。

### 7.2 必须替换或补齐的实现

#### 平台配额几乎缺失

当前唯一明确的通用请求限流只出现在 Operations Agent Gateway：默认每 Session 每分钟 30 次，进程内固定窗口、Mutex 保护。它没有空闲 Key 清理，多 Session 会使 Map 持续增长；多实例不共享状态；也没有 Tenant、Site、User、Device、Endpoint、Telemetry Data Point、并发或资源预算的统一策略。

结论：`REPLACE`。保留稳定 429 和局部 Budget 思路，建立版本化 Policy、共享/可切换 Counter Backend、分类 fail mode 和通知。

#### Readiness 是人工布尔值

`libs/observability/runtime.go` 的 `/health/live` 与 `/health/startup` 基本表示进程存在，`/health/ready` 只读取 `MarkReady/MarkNotReady` 的人工状态，没有持续验证 PostgreSQL、Redis、MQTT、ClickHouse、上游 Owner 或必要队列消费能力。Phase 1 Alignment Matrix 声称“保持 readiness 与真实依赖一致”，但源码不支持这一强结论。

结论：基础框架 `KEEP`，健康语义 `REPLACE`。需要按服务定义必要依赖、可选依赖、启动门禁和持续状态；只有必要依赖影响 readiness，避免把所有外部系统绑成级联故障。

#### 观测覆盖和 Export 可靠性不足

Prometheus 当前只抓取部分服务；仓库中的生产服务数多于 scrape 列表。异步 Trace Export 队列满时只计数并丢弃，Delegate 失败无重试。对 Trace 可接受“测量后丢弃”，但必须有 dropped/failed SLO 与告警，且不能把 Trace 当业务审计。

结论：本地体系 `KEEP`，覆盖、SLO 和 Export Loss 治理 `ADAPT`。

#### Redis 权威边界冲突

Telemetry Latest Cache 有 Business Revision CAS、重建和 Materialization Outbox，方向正确；但当前 Public Current Read 仍有 PostgreSQL 读取后写 Redis 再读 Redis 的路径，使缓存成为请求成功的额外依赖。Phase 1 文档声称 Redis 可重建，与该行为冲突。

结论：Projection Pattern `KEEP`，Current Read 路径 `REPLACE`。缓存失败只能降低性能或实时推送能力，不能改变权威 Current Fact。

#### Migration Runner 存在崩溃窗口

当前 Runner 对 SQL 做运行时 awk Sanitization，再计算和记录 Hash；执行 SQL 与插入 `schema_migrations` 是两个独立 psql 调用。若执行后、记录前崩溃，会出现已变更但未登记状态；生产执行内容也不是原始受审查文件的精确字节。服务启动时没有统一的 Product/Schema Compatibility Gate。

结论：Manifest/Hash/Allowlist 思路 `KEEP`，执行模型 `REPLACE`。生产 Migration 必须使用专门且已经净化的源文件，步骤执行和成功记录在可行时同事务；不可事务步骤必须有 `APPLYING/APPLIED/FAILED`、验证探针和人工恢复协议。服务发现版本不匹配必须拒绝启动。

#### 平台维护任务不完整

Scheduler 已有强状态机，但 Retention、Archive、Certificate Expiry、Outbox/Inbox 清理、Audit 保留、Dead Work Requeue 等尚未全部接成统一 Worker。Outbox Relay 和部分 Store 只有固定 Retry/Next Retry，没有统一最大尝试和 queryable Dead Letter。

结论：Scheduler 内核 `KEEP`，平台 Maintenance Contract `ADAPT`，无界重试 `REPLACE`。

#### 配置版本与发布治理不完整

本地有 Contract Revision、Digest、Migration Hash 和 Cohort Rollout，但没有统一 Product Configuration Revision、Diff、Publish/Revert、依赖校验和受审计发布包。D02 的 Import/Export 仍缺生产 Writer。

结论：`ADAPT` ThingsBoard Version Control 的版本、Diff、串行提交和分块思想；`REJECT` 运行时每 Tenant Git 仓库、CRUD 自动提交、Secret-in-repository 和超时后提交 Queue Offset。

#### 文档治理冲突

`architecture-baseline.v1.json` 的 `backwardCompatibleMigrationPreferred: true` 与当前仓库“删除旧路径、不增加兼容层、fallback 或兼容迁移”的规则冲突。`alignment-matrix.v1.json` 对 Health 和 Migration 的 KEEP 描述也强于源码证据。

结论：本裁决优先。后续实现票必须修改旧基线和对应静态检查；在修改完成前，这两处不能作为 D10 已通过证据。

## 8. 全能力裁决矩阵

| # | 能力 | ThingsBoard 证据判断 | HVAC Web 当前判断 | 裁决 | 目标行为 |
|---|---|---|---|---|---|
| 1 | Monolith / Microservices Topology | 两种拓扑都真实存在；微服务按伸缩维度拆分 | Canonical Phase 1 为单 Linux Server + Compose | `KEEP + ADAPT` | 保留单机 Tier 1，但明确非 HA；只有容量/故障证据触发 Tier 2 |
| 2 | Node/UI/Transport/Executor/EDQS | 进程边界清楚，但数量和运维复杂度高 | 多个 Go Owner，部分聚合进 `energy-api` | `ADAPT` | 按安全、资源、故障和独立伸缩证据拆分，不照搬服务清单 |
| 3 | Actor/Partition/Discovery/Inter-node | 实体串行和所有权生命周期有价值；固定版本是取模分配 | 控制路径使用 DB Lease/Fence，无通用集群发现 | `ADOPT Pattern + DEFER Runtime` | 保留耐久所有权；达到多实例门槛后再实现编排器成员关系和分区 |
| 4 | Queue/Partition/Stats/Backpressure/Recovery | 类型化队列和选键有价值；In-memory 与默认 Kafka 不够可靠 | Outbox/Inbox/Scheduler 较强，但策略不统一 | `KEEP + ADAPT + REJECT defaults` | 统一 Work Contract、顺序键、背压、Dead Letter、积压 SLO；不引入 Kafka 作为 Phase 1 前置 |
| 5 | Caffeine/Redis/Consistency | Version CAS、Tombstone、Negative Cache 值得吸收 | 有 Revision CAS 和重建，但 Current Read 依赖 Redis | `KEEP Pattern + REPLACE reads` | Redis 永远是 Projection；采用 Version/Tombstone，失败不改变业务正确性 |
| 6 | DAO/DB variants/Migration | 多数据库适配广；升级预检有价值，也有绕过/吞错 | Postgres+ClickHouse 分工合理；Runner 有原子性和源文件问题 | `KEEP specialization + REPLACE migrator + REJECT variants` | 不引入 Cassandra/Timescale 抽象；建立 Product/Schema Gate 和一次性受控迁移 |
| 7 | JS/Jobs/Housekeeper/TTL | Executor 隔离、超时和类型任务有价值；`node:vm` 与重试边界不足 | Scheduler 状态机较强，平台维护覆盖不足 | `KEEP jobs + ADAPT housekeeping + REJECT arbitrary JS` | 使用类型化 Job/受限表达式；控制路径禁止任意脚本；所有维护任务可查询、可终止 |
| 8 | Stats/Lifecycle/Error/Health/System Info/Usage | System Info/Usage 有价值；默认 Health 较浅 | OTel/Prometheus/Loki/Tempo 更强，但 readiness 和覆盖不足 | `KEEP local + ADOPT system info + REPLACE health` | 依赖感知 Readiness、SLO、Backlog/Dead Work、Export Loss 和资源告警 |
| 9 | Rate/Resource/Entity/API Limits | 分类完整；核心 Counter 多为进程内 | 仅 Operations Agent 局部固定窗口 | `ADOPT taxonomy + REPLACE implementation` | 版本化 Tenant/Site/Actor/Resource Policy；单机可本地，扩容前切共享 Backend |
| 10 | VC/Import/Export/Auto Commit | 版本、Diff、串行仓库有价值；超时提交和运行时 Git 有风险 | 只有零散 Revision/Digest，产品配置发布不完整 | `ADAPT + REJECT runtime Git` | 不可变 Release Artifact、Diff、Validate、Publish/Revert、SecretRef；D02 拥有实体语义 |
| 11 | Upgrade/Compatibility/Packaging/HAProxy/REST Client | One-shot Upgrade 和版本预检值得吸收；Skip/吞错不可接受 | Digest/Cohort/Restore Gate 较强，启动版本门禁缺失 | `ADOPT preflight + KEEP rollout + REPLACE gaps` | 顺序升级、错误即失败、启动拒绝错版；不保留旧路径兼容层 |
| 12 | TLS/Config/Secret/Operational Security | 支持 TLS/Redis TLS/部署参数，但配置可混入 Secret | 有 mTLS、私网、非版本 Secret File 和 Digest 规则 | `KEEP + ADAPT` | SecretRef/文件装载、轮换、最小权限、证书到期 Job；生产禁用 dev tag/default Secret |

## 9. 值得吸收的 Pattern

1. **Entity-key serialization**：同一聚合的命令、规则或配置变更按稳定 Key 串行，跨聚合并行。
2. **Ownership lifecycle**：获得/失去分区时显式停止 Claim、重建 Projection、Reconcile 后才 Ready。
3. **Per-queue policy**：每类 Work 独立声明优先级、并发、超时、重试、顺序、Dead Letter 和指标。
4. **Versioned cache with tombstone**：只接受更高 Source Revision，删除也携带版本，防止旧值回填。
5. **Limit taxonomy**：Tenant、Actor、Resource、Transport、Telemetry Point、Subscription 分开治理。
6. **Resource shortage notification**：CPU、内存、磁盘、队列和配额越界形成运维事件，不只暴露原始指标。
7. **One-shot migrator and version preflight**：应用不自行猜测或跳过数据库版本。
8. **Typed housekeeping**：清理、归档、证书扫描、重算都有持久 Job、Attempt、超时、上限和通知。
9. **Versioned product assets**：配置先 Validate/Diff，再 Publish；运行时只读取已发布不可变 Revision。
10. **Executor isolation as a failure boundary**：高 CPU/不稳定扩展与核心服务隔离，但安全隔离必须使用真正的进程/容器和能力限制。

## 10. 不适合本项目的部分

- 在没有规模证据时复制完整 ThingsBoard 微服务拓扑、Kafka、ZooKeeper 和自定义 Actor Runtime。
- 把固定版本的取模分配误认为最小迁移 consistent hashing。
- 使用 In-memory Queue 处理耐久业务工作，或以 Kafka 默认副本配置声称 HA。
- 把进程内 Rate Limit Cache 用作多副本严格 Tenant 配额。
- 把 Redis Cache、Actor Mailbox、Queue Offset 或 Git Repository 作为业务事实。
- 允许 HVAC 控制、身份或 Secret 路径执行任意 Tenant JavaScript。
- 使用 `SKIP_SCHEMA_VERSION_CHECK` 或吞掉数据升级失败后仍推进版本。
- 每个实体 CRUD 自动提交 Git，或把 Repository Credential 放入产品 JSON。
- Housekeeper 全局单分区、固定 sleep、忽略 producer failure 和无可查询 Dead Letter。
- 为不存在的旧生产系统增加双写、fallback、永久 Compatibility Layer 或迁移桥。
- 在单服务器、单数据库实例和单观测栈上使用“高可用”标签。

## 11. 映射到本项目设计

### 11.1 P0：先纠正会造成错误可靠性判断的差距

1. 建立 `AvailabilityTier` 文档和机器契约：Phase 1 = `SINGLE_NODE_RECOVERABLE`，明确每个组件的 SPOF、RPO/RTO 前提和不可用影响。
2. 重构 Phase 1 Migrator：去掉运行时 SQL Sanitization；增加 Product/Schema Preflight、步骤状态、失败阻断、服务启动版本门禁和恢复 Runbook。
3. 用版本化 `LimitPolicy` 替换 Operations Agent 的无界 Session Map；至少覆盖登录/Token、控制写、Operations Agent、Telemetry Ingest 和高成本查询。
4. 把 Readiness 改为依赖感知状态机，补齐所有 Production Owner 的 scrape/日志/Trace 覆盖，增加 dropped spans、dead work、lease recovery、oldest ready age 告警。
5. 修正 Telemetry Current Read：Redis 不可成为请求正确性依赖。
6. 建立 Restore Drill 的实际证据门禁；只有模板、脚本或目标值不得被报告为“RPO/RTO 已实现”。

### 11.2 P1：统一耐久工作和运行治理

1. 将 Retention、Archive、Certificate Expiry、Outbox/Inbox 清理、Projection Repair 接入统一 `WorkItem/Attempt/DeadLetter` Contract。
2. 为所有 Consumer 记录顺序键、幂等键、最大尝试、退避、Timeout、Side-effect Classification 和 Reconciliation 方法。
3. 增加 System Info、Tenant Usage、容量趋势和 Resource Shortage 运维视图，但不把它们建模为 Telemetry 业务真值。
4. 建立不可变 Product Configuration Release：Draft、Validate、Diff、Published Revision、Dependency Lock、SecretRef、Audit 和 Revert Decision。
5. 将 TLS/证书到期、Secret 装载失败、镜像 Digest、Schema Version 纳入发布 Preflight。

### 11.3 P2：只有触发门槛成立才进入多实例/HA

以下任一证据成立后，才能提交多实例设计 ADR：

- 单实例在目标峰值的 CPU、内存、延迟或队列积压上不能满足 SLO；
- 业务可接受停机时间小于当前实际恢复时间；
- 单一故障域的风险成本超过 HA 的运维复杂度；
- 独立服务存在明确不同的伸缩、安全或发布节奏。

届时再引入：

- 编排器原生 Service Discovery 与 Replica Management；
- Shared Rate Limiter/Counter Backend；
- Redis Sentinel/Cluster 或移除关键缓存依赖；
- PostgreSQL、MQTT、ClickHouse 的目标化 HA；
- 必要时的 Kafka/Pulsar 类 Backbone，但必须通过故障、顺序、Backpressure 和恢复基准；
- Partition Revision、Lease/Fence Transfer 和 Reconciliation Readiness。

不以“ThingsBoard 使用了”作为引入理由。

## 12. 异常和边界处理

| 场景 | 必须行为 |
|---|---|
| Queue 重复投递 | Idempotency Key/Inbox 去重；控制副作用还需 Fence 与 Verification |
| Queue 消息永久失败 | 有界尝试后进入 queryable Dead Letter，保留 Error Code、Attempt、Payload Digest 和处置记录 |
| Consumer 在副作用后、完成记录前崩溃 | 进入 Reconcile，不根据 Queue Offset 猜测结果 |
| Cache 写失败 | Durable Write 保持成功；缓存标记 stale/repair，不回滚事实 |
| Cache 收到旧版本 | CAS 拒绝；删除 Tombstone 也必须参与版本比较 |
| Limit Backend 不可用 | 按资源分类 fail closed/open；控制、身份、Secret 写必须 fail closed |
| Migration 中断 | 不启动错版服务；根据步骤状态验证、重试、前滚或恢复，不静默补版本号 |
| Upgrade Data Step 部分失败 | 整体发布失败；不得吞错后更新最终 Schema Version |
| 节点失去所有权 | 停止新 Claim，Fence 旧 Owner，等待/恢复 Lease，新 Owner Reconcile 后才 Ready |
| Trace Export Queue 满 | 业务不中断，但计数、告警和 SLO 可见；Audit 不依赖 Trace |
| Housekeeping 长期失败 | 不无限静默重试；进入 Dead Letter 并触发运维事件 |
| 任意脚本超时或逃逸 | 控制链无任意脚本；扩展运行器使用独立进程/容器、CPU/内存/时间/网络/文件能力限制 |
| Backup 不可读或 Restore 超时 | 发布/扩容认证失败；目标值不得替代演练结果 |

## 13. 实施门槛

### Single-node Production Gate

- 所有 SPOF 和停机影响已枚举，页面/文档无 HA 误导；
- Postgres/ClickHouse/MQTT/对象数据有离机备份；
- 最近一次真实 Restore Drill 达到声明 RPO/RTO；
- Outbox、Job、Command、Verification 在进程/主机重启后可恢复；
- 磁盘耗尽、数据库不可用、MQTT 不可用有明确降级和告警。

### Migration Gate

- Migrator Artifact Digest 固定；执行的是受审查源文件，不在运行时改写；
- Product、Source Schema 和 Target Schema 版本严格匹配；
- 每一步状态可查询，失败不会写入最终版本；
- 所有服务在 Schema 不兼容时拒绝 Ready；
- 前滚/恢复方案经过隔离环境演练。

### Rate-limit Gate

- 每个 Policy 有 Scope、Resource、Window、Burst、Action、Error Code、Retry Hint 和版本；
- 多实例前证明 Counter Backend 的一致性和故障行为；
- 高危写 fail closed，低风险读的 fail open 必须显式批准；
- 限流自身不会因无界 Key 导致内存泄漏。

### Observability Gate

- 所有 Production Owner 有 Metrics、Structured Logs、Trace 或明确豁免；
- Readiness 反映必要依赖和 Worker 能力，Liveness 不因外部依赖失败触发重启风暴；
- Queue Age、Dead Letter、Lease Recovery、Cache Repair、Migration、Backup、Rate Reject 和 Export Loss 均有告警；
- 定义可用性、延迟、数据新鲜度和控制完成 SLO，而不只监控 CPU。

### Multi-instance Admission Gate

- 有真实负载/可用性证据证明单实例不足；
- 所有进程内 Rate Limit、Cache 和 Ownership 状态已迁移为共享或明确分区状态；
- 逐实体顺序、Lease/Fence、重平衡和滚动升级故障测试通过；
- 数据层和消息层的可用性不低于应用副本层，否则不得声称端到端 HA。

### Configuration Release Gate

- 发布包不可变、可 Diff、可验证依赖、无 Secret；
- Draft 不能被运行时读取，Published Revision 不能原地修改；
- Publish/Revert 有 Audit 和权限；
- Import 逐项错误，不使用强制覆盖掩盖冲突。

## 14. 差距过大项

以下差距不能以“Phase 1 简化”直接豁免，必须进入实现计划：

1. 平台级 Rate Limit/Quota 几乎没有，现有实现只覆盖 Operations Agent 且不可扩展。
2. Readiness 只是人工布尔值，和文档声称的真实依赖健康不一致。
3. Migration 执行/记录存在崩溃窗口，且没有服务启动 Schema Gate。
4. Redis Current Read 权威边界错误，与“缓存可重建”原则冲突。
5. 平台 Maintenance、Retention 和 Dead Letter 没有统一执行闭环。
6. 配置 Version/Diff/Publish/Revert 还不是生产能力。
7. 当前不存在通用多实例所有权、集群发现和跨节点严格配额；因此不能把已有 K8s 资产视为 HA 已实现。
8. RPO/RTO 已有目标和工具，但是否达标必须由真实、时间戳化 Restore Drill 证明。
9. 观测服务覆盖不完整，Trace 丢弃只有计数而无统一 SLO。
10. Canonical Compose 仍允许开发 Tag fallback；生产必须由部署门禁强制 Digest，而不是依赖操作者记忆。

## 15. 最终裁决

ThingsBoard 在“通用 IoT 平台运行面”的覆盖广度明显强于 HVAC Web，尤其是服务分区、配额分类、缓存版本、系统资源视图、版本仓库和安装升级入口；这些差距不能被本地现有模块掩盖。

HVAC Web 在“安全控制与耐久业务执行”上有更适合自身场景的基础：PostgreSQL Outbox/Inbox、Lease/Fence、Command Verification、Cross-store Reconciliation 和低基数 OTel 体系不应被 ThingsBoard 默认队列、Actor 或 Actuator 模型替换。

最终目标不是缩小成 ThingsBoard 的复制品，而是：

- 以本地耐久控制内核为真值；
- 吸收 ThingsBoard 的平台治理分类和成熟 Pattern；
- 修复本地缺失的配额、升级、健康、维护和配置发布闭环；
- 用证据决定何时进入多实例与 HA，而不是为了架构外观提前引入 Kafka、ZooKeeper、自定义 Actor 或数据库变体。

## 16. 本轮验证结果

- 已覆盖 D10 Inventory 的全部 12 项能力，没有把 Import/Export、JS Executor、Housekeeper、VC、REST/Packaging 或 TLS/Secret 隐藏到“以后再看”。
- 已区分官方文档、固定 CE 源码与 PE/商业能力；未把 PE 宣传内容当作源码证据。
- 已对本地 Rate Limit、Observability、Scheduler、Outbox、Command、Cache、Migration、Recovery 和 Deployment 做源码级反向审查。
- 已记录本地优于 ThingsBoard、ThingsBoard 优于本地及双方都不适合直接保留的部分。
- 本轮只完成架构裁决和实施门槛，没有宣称缺口已实现或生产已认证。

## 17. S23 Housekeeping / Retention / Tenant Retirement 实施源码复核

Date: 2026-08-19

Issue: #281

S23 实施前再次固定审查 ThingsBoard CE `v4.3.1.1` / commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4` 的以下源码与测试，而不是仅依据本裁决文档实现：

- `application/src/main/java/org/thingsboard/server/service/housekeeper/HousekeeperService.java`；
- `application/src/main/java/org/thingsboard/server/service/housekeeper/HousekeeperReprocessingService.java`；
- `common/data/src/main/java/org/thingsboard/server/common/data/housekeeper/HousekeeperTaskType.java`；
- `application/src/test/java/org/thingsboard/server/service/housekeeper/HousekeeperServiceTest.java`。

S23 的逐项裁决如下：

- **ADOPT**：类型化维护任务、单任务超时、最大尝试次数、失败后的显式可观测结果，以及测试中“验证真实清理结果而非只验证调用次数”的原则。
- **ADAPT**：ThingsBoard 的 Kafka Housekeeper/Reprocessing 改为 HVAC Web 已有 PostgreSQL `job_instances` / `job_attempts`、`SKIP LOCKED` Claim、Lease Renewal、`RETRY_WAIT` 与 `DEAD`；重试使用有界退避与抖动，不复制固定 `sleep`。
- **ADAPT**：ThingsBoard 的 Tenant 级实体删除改为跨 Owner 的 Retirement Saga。IAM、Registry、Telemetry、Metric、Alarm、Outbound Delivery 必须分别留下成功 Proof；任一 Owner 失败时状态只能是 `INCOMPLETE`，不能宣告 Tenant 已退休。
- **ADAPT**：Housekeeper 的失败通知改为持久 `maintenance_events`。证书/Secret 到期、`DEAD` 工作和 Retirement 不完整都必须可查询并带明确 `action_code`，而不是只存在日志中。
- **REJECT**：固定睡眠重试、单一重处理分区作为可靠性前提、达到重试上限后没有持久处置记录，以及未证明 Producer 交付结果就推进任务状态。
- **KEEP**：HVAC Web S08 已有 Legal Hold、Archive Manifest、Deletion Tombstone 与 Restore Tombstone 语义。S23 复用这些真值，不另建平行 Retention/Archive 删除模型。

本轮没有复制 ThingsBoard Java 源码；只采用经裁决的行为模式。ThingsBoard 上述源码许可证为 Apache-2.0。

## 18. Phase 1 deployment tiers / owner split 实施源码复核

Date: 2026-08-24

本轮在官方仓库重新检出 ThingsBoard CE `v4.3.1.1`，并用 `git rev-parse HEAD` 确认精确提交为 `c2a52e46c44e308ddee430e7266b8e10eddde9c4`。实施前直接阅读了以下官方源码，而不是从架构图推断行为：

- `application/src/main/resources/thingsboard.yml`：`TB_SERVICE_TYPE` 默认 `monolith`，`TB_QUEUE_TYPE` 默认 `in-memory`，两个选择相互独立；
- `common/queue/src/main/java/org/thingsboard/server/queue/discovery/DefaultTbServiceInfoProvider.java`：monolith 注册全部 `ServiceType`，非 monolith 只注册配置角色；
- `common/queue/src/main/java/org/thingsboard/server/queue/provider/InMemoryMonolithQueueFactory.java`；
- `common/queue/src/main/java/org/thingsboard/server/queue/provider/KafkaMonolithQueueFactory.java`；
- `common/queue/src/main/java/org/thingsboard/server/queue/provider/KafkaTbCoreQueueFactory.java`：三者分别由 `queue.type + service.type` 的精确组合装配；
- `common/queue/src/main/java/org/thingsboard/server/queue/memory/InMemoryTbQueueConsumer.java`：`poll()` 从内存存储取走消息，`commit()` 无行为，不能作为耐久工作基础；
- `docker/.env`、`docker/docker-compose.yml`、`docker/docker-compose.prometheus-grafana.yml` 与 `docker/monitoring/prometheus/prometheus.yml`：官方 Docker 监控由 `MONITORING_ENABLED` 显式启用，仅增加 Prometheus/Grafana；微服务 Compose 明确部署多个 Core、Rule Engine、Transport、JS Executor 与 ZooKeeper，不适合小型单机照搬；
- `docker/README.md`：确认监控开关、数据库/缓存选择和升级入口的官方运行说明。

同时阅读了最接近本次机制的上游测试：

- `common/queue/src/test/java/org/thingsboard/server/queue/memory/DefaultInMemoryStorageTest.java`：验证 poll 会降低 lag 并取走批次，支持“不采用内存队列作为耐久基础”的判断；
- `common/queue/src/test/java/org/thingsboard/server/queue/discovery/QueueKeyTest.java`：验证服务类型与租户共同构成稳定队列身份。

固定版本没有为三个 Queue Factory 的条件装配或 `DefaultTbServiceInfoProvider` 的 monolith 角色展开提供直接单元测试，因此本地没有虚构“上游测试已证明”的结论；这些行为以固定源码为证据，并由本地部署档位与 Owner 选择测试保护。

实施裁决：

- **ADOPT**：部署拓扑必须由一个显式、可验证的机器选择决定；轻量监控必须可选，不能把完整观测栈强制进最小档。
- **ADAPT**：ThingsBoard 的 `service.type` 改为 `PHASE1_DEPLOYMENT_TIER`、Compose profile 与 `ENERGY_API_EMBEDDED_OWNERS`。Stage 0 使用同一 `energy-api` 制品聚合 Owner；Stage 1 使用相同产品版本的现有 Go Owner binary 和 `owner-split` overlay。Notification 在形成独立、源码复核过的制品前保持唯一内嵌角色。
- **KEEP local**：PostgreSQL Outbox/Inbox、Scheduler Job、Lease/Fence、Command Verification 继续作为耐久骨干。拓扑切换不改变业务真值和幂等边界。
- **REJECT**：不增加 `queue.type` 运行时切换，不采用 In-memory Queue、Kafka、ZooKeeper、自定义 Actor Runtime、十副本 JS Executor 或 ThingsBoard 微服务清单作为 Phase 1 前提。
- **REJECT claim**：Owner-split Compose 配置通过不等于 Stage 1 已运行认证；必须完成同版本 live contract drill 后才能更新认证状态。

本轮对应的本地实现与证据入口：

- `deploy/platform/phase1/deployment-tiers.v1.json`；
- `deploy/platform/phase1/owner-split.compose.yaml`；
- `scripts/phase1-deployment-tier.mjs` 与 `scripts/phase1-wsl-compose.mjs`；
- `services/platform-gateway/cmd/platform-gateway/embedded_energy.go`；
- `scripts/test-phase1-deployment-tier.mjs` 与 `scripts/test-phase1-owner-split.mjs`。
