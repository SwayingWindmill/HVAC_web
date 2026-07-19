# 确定部署拓扑、可观测性与灾难恢复

Type: grilling
Status: resolved
Blocked by: 01, 04, 05, 06, 07, 10
Part of: ../map.md

## Question

各服务和数据组件应如何部署才能满足既定 SLO 与故障隔离？需要确定环境划分、编排平台、实例与副本、自动扩缩容、健康与就绪、滚动和灰度发布、OpenTelemetry trace/metric/log、容量告警、备份恢复、Kafka/PostgreSQL/Redis/ClickHouse 高可用、多可用区或多地域策略、RPO/RTO 演练和降级模式。

## Comments

- 生产参考部署采用 Kubernetes 兼容编排平台，但不在架构阶段绑定具体云厂商、托管 Kubernetes 发行版或基础设施供应商。应用只依赖标准容器、Service、Ingress/Gateway API、NetworkPolicy、PodDisruptionBudget、StatefulSet/Operator、CSI、Secret/KMS接口和OpenTelemetry协议；托管PostgreSQL、Kafka兼容服务、Redis、ClickHouse或对象存储只有在满足既定一致性、备份、可观测性、网络和退出能力时才优先采用。基础设施通过声明式IaC与Git审查管理，禁止手工长期漂移成为生产事实。
- 环境严格分为`local/dev/test/staging/prod`，生产与非生产使用独立账号或项目、Kubernetes集群、网络、IAM Trust Domain、证书CA、KMS Key、Secret、数据库、Kafka Namespace、对象存储Bucket和模型/ThingsBoard凭证。`staging`应在拓扑、协议和发布流程上接近生产，但不复制未经治理的生产数据；生产数据进入非生产必须经过明确授权、最小化、脱敏或合成替代，并拥有独立保留和删除记录。测试环境不得访问生产ThingsBoard、生产命令Endpoint或生产Webhook目标。
- 生产首版采用单主地域、三个可用区的区域内高可用拓扑，并建设异步Warm Standby灾备地域；不采用多地域业务双活、双主数据库、跨地域同时执行命令或跨地域共享单一设备Control Lane。区域内已确认写入的Command、关键状态、Outbox/Audit Intent及遥测Kafka事实按既定边界实现RPO=0；跨地域复制为异步并持续暴露复制水位，因此不宣称区域灾难RPO=0。灾备切换必须先隔离旧地域并建立明确写入Fence，避免脑裂。
- Kubernetes按故障域和数据分类划分Namespace与Node Pool，至少分离`edge/gateway`、`command-control`、`telemetry-ingest`、`online-query/realtime`、`batch-analytics-ai`、`platform-core/iam`、`connectors`、`observability`和`security-operations`。Command、Telemetry Ingest与在线查询拥有保留CPU/Memory、独立连接池、优先级和配额，批处理/AI优先被限流或驱逐；生产节点跨三个可用区分布，关键Pod使用Topology Spread、反亲和与PDB，不能将同一故障域的全部副本调度到一个AZ、节点或电源域。
- 服务部署遵循票据04的独立二进制、镜像、数据库身份、连接池和扩缩容边界。99.99%关键级的`command-service`、`command-dispatcher`、`thingsboard-connector-control`、`telemetry-ingest`及关键事件骨干在正常状态下至少跨三个AZ保有可用副本，关键无状态服务建议最低3副本；99.95%在线级Gateway、IAM、Core、Telemetry Query、Realtime、Connector Data与Automation通常最低3副本；99.9%异步Analytics、AI Platform、Agent Worker与Copilot Runtime可按队列弹性伸缩，但生产不得依赖单副本长期运行，且不能影响关键池。具体副本和节点规模通过容量POC确定而非写死在应用代码。
- 所有无状态服务执行无会话或外部化会话设计；Gateway/BFF Session使用共享、可恢复且受租户隔离的权威会话存储或加密服务端会话，不依赖单Pod内存。Realtime连接可分散到多个实例，但订阅权威Cursor来自Kafka/服务资源而非本地连接状态；实例退出时先停止接收新连接、发送重连提示并在宽限期内排空。Copilot Runtime断线不取消AnalysisRun，Dispatcher或Worker重启不丢失Intent、Lease、Fence或Checkpoint。
- 自动扩缩容同时使用资源指标与业务指标。无状态API关注CPU、Memory、请求并发、延迟和连接数；消费者关注Kafka Lag、最老消息年龄、处理吞吐、失败率和每分区并发；Worker关注队列深度、最老任务年龄、Lease利用率、模型/Tool容量及Organization公平性。关键命令与摄取服务设置保守的最小副本、扩容预热和最大并发，不允许Scale to Zero；批处理和AI可缩到低水位但不突破Queue Deadline。Stateful组件不得仅根据CPU自动在线扩分片，任何分片、分区、排序键或拓扑变更必须经过容量计划、影子验证和可回滚迁移。
- 平台长期维持至少30%生产容量余量，并以票据01的稳态、5倍突发、1小时积压和2小时清空作为容量验收边界。容量模型覆盖Gateway连接、JWT验证、数据库连接、Kafka分区与Broker吞吐、对象归档、Redis Key与内存、ClickHouse写入/查询、Realtime订阅、模型Token/调用、Tool扫描量和备份窗口。扩容告警应在资源耗尽前基于趋势、队列年龄和预测触发，不能只在CPU达到100%后告警；每季度和大客户上线前重新校准三年3倍增长计划。
- 健康检查分为`startup`、`readiness`和`liveness`。Startup Probe覆盖迁移兼容、配置、证书和本地初始化；Readiness只在实例可以安全接收该类流量时通过，并检查必要的本地状态、权威存储连接和消费者所有权，但不将所有下游瞬时依赖串成全局级联不就绪；Liveness仅检测进程死锁、事件循环停滞或无法自恢复状态，禁止因普通数据库超时反复重启制造故障风暴。管理和健康Endpoint独立、最小化且不泄露Secret、连接串、租户或内部拓扑。
- 所有服务支持优雅终止。接收SIGTERM后立即停止新流量和新任务，HTTP/gRPC完成有界Drain；Kafka消费者停止拉取、完成或安全中断当前Inbox事务后提交Offset；Dispatcher和Agent Worker停止领取新任务、续写安全Checkpoint并释放或让Lease自然到期；Realtime通知客户端恢复；Connector在确认请求尚未写出前才可安全中止，已进入`REQUEST_COMMITTED`的命令按`OUTCOME_UNKNOWN`规则处理。Termination Grace Period按工作负载设置，不能以强制Kill替代状态收敛。
- PodDisruptionBudget、滚动升级与集群维护必须匹配服务等级。关键级服务配置跨AZ最小可用数，计划维护时`maxUnavailable=0`或等价保护并配合Surge；在线级服务保持足够副本承受单Pod与单AZ失效；异步服务允许积压但不得丢任务。节点升级、证书轮换、Operator升级和存储维护均需逐AZ执行，禁止同时驱逐同一关键聚合、同一Kafka分区多数副本或同一数据库同步副本集。
- 发布采用不可变、签名镜像和声明式GitOps/等价受审计流程。标准顺序为契约与数据库扩展、向后兼容服务发布、生产者启用新字段、消费者验证、最后删除旧字段；数据库Migration由数据Owner专用短期身份执行，禁止应用Pod启动时自动执行破坏性Migration。HTTP、gRPC、Protobuf、OpenAPI、Topic和Checkpoint至少兼容当前与前一主版本；发布必须能独立回滚，不能要求Go、Python、Node与前端锁步上线。
- 高风险服务采用金丝雀或分阶段发布。Gateway、Command、Connector、Telemetry Processor、Query、AI Workflow和模型策略按固定测试Organization、Site、IntegrationInstance、流量比例或Worker池逐步扩大；影子流量只读且禁止产生Command、Webhook、通知、收费模型调用和其他副作用。发布门禁检查错误率、p95/p99、队列年龄、租户隔离、旧Fence拒绝、Outbox/Inbox、数据质量、成本和SLO Error Budget；异常自动暂停或回滚。Feature Flag与Kill Switch均有Owner、Scope、到期、审计和旧版本兼容策略，禁止永久未清理的隐式分支。
- OpenTelemetry是统一的Trace、Metric和Log关联标准。所有入口生成或验证W3C Trace Context，跨HTTP/gRPC、Kafka、Outbox/Inbox、Replay、Webhook、Tool、Model和Connector传播`trace_id/correlation_id/causation_id`；业务状态仍以Command、Run、Attempt、ToolCall、ModelCall、Dataset等稳定ID为权威。每个服务通过本地SDK向区域OpenTelemetry Collector发送数据，Collector采用Agent/Daemon与Gateway分层、批处理、限流、脱敏和重试，观测后端故障不得阻塞核心业务路径。
- Trace采用基于风险和结果的采样：错误、高延迟、跨租户拒绝、Break-glass、Command、Outbox异常、AI隔离和安全事件优先保留，普通高频遥测使用低比例或聚合观测；采样规则版本化且不能丢失必要Audit。Metric禁止使用Command ID、Run ID、Principal ID、Device ID等高基数标签，使用服务、Environment、状态、错误码、模板类、Provider、Tool和受控租户等级等维度；具体业务ID进入Trace或受限结构化日志。日志使用结构化字段、统一时间与稳定错误码，禁止记录Token、Cookie、Grant、Secret、完整Prompt、大Payload或跨租户资源列表。
- 可观测性至少覆盖四个Golden Signals以及业务状态不变量。API关注流量、错误、延迟、饱和；Kafka关注分区、ISR、Lag、最老消息、Controller、磁盘和再平衡；PostgreSQL关注事务、锁、复制延迟、WAL、连接、慢查询和备份；Redis关注内存、淘汰、复制和重建水位；ClickHouse关注写入、Merge、查询队列、分片副本、Keeper和存储；对象存储关注归档水位、Checksum与生命周期。业务监控必须覆盖Command双Lease/旧Fence、OUTCOME_UNKNOWN、Telemetry归档缺口、RLS/租户拒绝、Outbox/Inbox积压、AI预算与质量、Audit链和删除状态。
- 告警以用户影响、SLO与数据正确性为中心，并设置明确Owner、Severity和Runbook。SEV-1包括跨租户泄漏、未授权控制、旧Fence被接受、核心命令或摄取被AI拖垮、审计不可验证；SEV-2包括关键服务区域内大面积不可用、已确认数据可能丢失、授权撤销失效、数据库或Kafka多数不可用；SEV-3包括单AZ故障已自动容错、单Provider/Tool降级、队列或容量趋势异常；单实例重启通常不直接寻呼。告警执行去重、抑制和依赖关联，禁止每个Pod或每次429分别制造通知风暴。
- PostgreSQL首版可共享一个多可用区物理集群，但每个数据Owner使用独立Database或Schema、运行账号、Migration账号、连接池、备份清单和恢复步骤，禁止跨服务外键与SQL访问。关键写入采用单Writer和至少两个跨AZ副本，Command、Audit Intent及要求区域内RPO=0的事务使用同步复制/同步提交到策略要求的远端副本后才确认；普通业务可按既定RPO采用较弱同步策略，但必须明确。数据库代理或连接池不能隐藏事务语义，Failover采用一致的Leader选举与Fencing，旧Primary隔离后才允许新Writer接受写入。
- PostgreSQL启用持续WAL归档、加密全量/增量备份、PITR、跨地域备份复制和不可变备份保留；备份Catalog记录Database、LSN、时间、Key版本、Checksum和Retention。恢复不是“备份任务成功”而是定期在隔离环境完成实际Restore、Schema校验、数据Owner一致性检查、Outbox/Inbox与Audit Intent对账。Command与核心服务区域内单Pod/单AZ故障按既定5分钟内恢复能力；区域灾难按平台核心能力4小时RTO执行，最终数据损失以实际复制水位报告，不能伪造RPO=0。
- Kafka API兼容Control/Data Backbone均按票据05跨三个可用区部署，关键Topic至少三副本、`acks=all`、`min.insync.replicas>=2`、幂等Producer、禁用unclean leader election并实施机架/AZ感知。Broker磁盘、网络和分区分布保留余量，Schema Registry和管理平面多副本部署并独立备份Schema与ACL配置。Kafka不以传统文件备份作为唯一恢复手段：跨地域异步复制必要Topic，原始遥测持续归档对象存储，业务数据库保留Outbox/Inbox权威；灾备地域恢复后依据复制水位、对象Manifest和业务状态受控重放，默认禁止重放外部副作用。
- Redis只保存可重建的最新态、缓存、会话或短期协调数据，不作为命令、遥测历史、授权或审计权威。生产采用跨AZ主从或Cluster与自动Failover，启用认证、TLS、租户Key规范、内存上限和明确淘汰策略；需要BFF Session等不可随意丢失的用途必须采用满足会话RPO的独立实例或权威会话方案，不能与大规模遥测缓存混池。最新遥测Redis故障时从Kafka Changelog重建，恢复期间Query标记degraded且高风险命令不使用降级最新值；不为Redis宣称权威RPO=0。
- ClickHouse生产采用按容量设计的分片与跨AZ复制，每个分片至少有跨故障域副本，使用ClickHouse Keeper或等价三节点仲裁；写入以批次和稳定幂等标识处理，不依赖MergeTree自动去重作为业务唯一性。Distributed Query设置租户、并发、扫描、Memory和超时限制，AI/报表与在线查询使用独立用户、配额和资源组。ClickHouse热数据故障可从Kafka保留、对象存储Parquet及版本化重建任务恢复；元数据、DDL、字典和配置必须备份，恢复后校验Dataset Revision、Watermark、行数和Checksum。
- 对象存储承担原始遥测归档、Parquet、报表、Dataset/Artifact、备份和WORM审计，但按用途使用独立Bucket/前缀、IAM、KMS Key、生命周期和复制策略。启用Versioning、Checksum、Multipart完成校验、删除保护和清单；Command/Audit证据使用保留锁，普通临时Artifact按Retention Policy清理。关键备份、审计Checkpoint和满足灾备需求的对象执行跨地域复制并监控Replication Lag；对象写成功只有在Manifest、Hash、大小、范围和Key版本持久化后才可被上层视为完成。
- ThingsBoard Connector跨AZ部署并按IntegrationInstance实施租约、Fence、限流和熔断；同一控制Attempt只有一个有效执行者，Connector Pod或AZ切换后依据持久化connector_execution、Attempt、Fence和RPC证据恢复，不能重发`REQUEST_COMMITTED`的不确定命令。数据Connector故障时上游通过持久化缓冲和Reconcile补数恢复；控制Connector或ThingsBoard不可用时命令停留在可解释状态，不创建可能重复发送的Attempt。不同IntegrationInstance故障相互隔离。
- 备份与恢复以数据Owner为单位编制矩阵，至少覆盖PostgreSQL、Kafka/Schema、ClickHouse、对象存储、KMS/Secret元数据、Git/IaC配置、ExternalBinding/Mapping、Audit Ledger、Model/Tool Registry和删除账本。每项必须定义权威源、备份方式、频率、加密、跨地域复制、RPO、RTO、恢复顺序、依赖、验证查询和Owner；Redis缓存、搜索索引、Realtime状态和本地Checkpoint等可重建组件明确记录重建来源与最大时间。Secret值不通过普通备份导出，使用KMS/Secret平台的受控灾备和Break-glass流程。
- 区域灾备采用人工或受控自动化编排的Warm Standby。正常时灾备地域接收数据库备份/WAL、Kafka必要Topic异步复制、对象跨地域复制、镜像与IaC、配置和审计Checkpoint，但不同时执行生产Command。宣告灾难后依次冻结或网络隔离旧地域、核对复制水位、提升数据库Writer与Kafka/服务端点、轮换或重新签发Workload Identity、恢复IAM/Core/Gateway/Command查询与受理、Telemetry Ingest、Realtime/Query，最后恢复报表和AI。Command派发只有在旧地域Control Lane、Connector Lease和Fence已证明失效后才能启用；所有`SENDING/REQUEST_COMMITTED`记录先进入对账，禁止灾备切换即盲目重发。
- RPO/RTO沿用票据01并按组件细化：区域内已确认Command、Command Transition、Outbox/Audit Intent和Kafka遥测事实目标RPO=0；核心业务事务RPO不超过1分钟，报表与派生数据不超过1小时，AI临时状态不超过15分钟。单Pod与消费者重平衡通常在30至60秒恢复处理能力，Connector实例2分钟、单AZ关键能力5分钟、核心API和遥测15分钟、历史查询1小时、报表/AI 4小时；整个地域不可用时4小时内恢复核心能力。任何恢复报告都同时给出服务可用时间、数据水位和功能降级，不能将“API可响应”当作数据与副作用完全恢复。
- 灾备与恢复模式默认Fail Closed并遵循优先级：Command与安全审计、Telemetry可靠摄取、Site/设备/权限查询、Realtime、历史查询、报表、AI。Command数据库不可用时拒绝新控制；IAM/Core授权投影超过最大陈旧度时高风险动作拒绝；Kafka Control不可用时只可靠写入本地Outbox并暴露派发延迟；ThingsBoard/Connector不可用时不发送；Redis不可用时在线查询可标记降级但控制验证拒绝；ClickHouse不可用时保留摄取并暂停历史/AI；Audit本地Intent不可写时高风险事务拒绝。禁止以静默缓存、旧授权、任意模型、直接数据库或直接ThingsBoard路径绕过故障。
- 灾难恢复和高可用能力必须通过固定演练而不是文档承诺。至少每季度执行单Pod、节点、单AZ、数据库Failover、Kafka Broker/Controller、Redis主节点、ClickHouse副本、对象存储错误、证书轮换和Provider故障演练；至少每半年执行跨地域备份Restore与部分服务Warm Standby提升；至少每年执行完整区域灾难演练。演练使用生产等价但受控流量，记录开始/检测/切换/恢复/对账时间、RPO水位、数据丢失、重复副作用、告警与Runbook差距，形成整改Owner和截止日期。
- 上线前强制完成故障注入与容量验收：稳态及5倍突发、1小时积压2小时清空、单AZ故障、关键Pod滚动、PostgreSQL主库切换、Kafka多数/少数故障、Redis全量重建、ClickHouse分片或Keeper故障、对象归档延迟、Outbox/Inbox重复、网络分区、DNS与证书故障、KMS/Secret不可用、ThingsBoard断连、旧Fence恢复、Realtime Resync、Audit积压、备份Restore和区域切换。已确认Command/遥测零丢失、跨租户访问零成功、未审批或旧Fence发送零成功、未知副作用零盲重试、恢复后重复正式结果零、审计链未检测篡改零是硬门禁。
- 运维责任明确：服务团队拥有应用SLO、Runbook、容量和数据恢复验证；数据Owner拥有Schema、Migration、Backup/Restore和一致性检查；SRE/平台团队拥有Kubernetes、网络、集群基础设施、Collector、全局容量和灾备编排；安全团队拥有身份、KMS、证书、供应链和事件响应；Command、Telemetry、AI等领域团队分别负责其状态机与重放边界。每个关键组件必须有唯一Primary Owner、Secondary Owner、值班升级路径和恢复决策权，第三方托管服务故障仍由内部Owner承担用户影响协调。

## Answer

生产平台采用Kubernetes兼容的单主地域三可用区架构，并建设异步Warm Standby灾备地域；首版不做多地域业务双活。服务按Command、Telemetry Ingest、在线查询、Core/IAM、Connector和Batch/AI划分独立Namespace、Node Pool、资源配额、连接池和优先级，关键与在线服务跨AZ至少维持足够副本，AI和批处理优先降级。自动扩缩容同时依据CPU/Memory、请求并发、Kafka Lag、最老任务年龄和业务容量，关键路径不Scale to Zero；长期保留30%容量余量，并以稳态、5倍突发和积压恢复作为硬验收。

无状态服务使用Startup/Readiness/Liveness、PDB、Topology Spread和优雅Drain；状态机Worker在退出时收敛Lease、Fence、Inbox、Checkpoint与不确定副作用。发布采用签名不可变镜像、声明式IaC/GitOps、扩展优先Migration、当前/前一契约兼容、金丝雀和Error Budget门禁，Go、Python、Node与前端可以独立上线和回滚。OpenTelemetry统一Trace、Metric和Log，跨同步与异步链路传播Trace Context并以业务ID保持长期关联；观测后端故障不阻塞业务，日志、指标和采样执行敏感信息及高基数治理。

PostgreSQL使用单Writer、跨AZ副本、明确同步提交、连续WAL/PITR和定期真实Restore；Kafka Control/Data Backbone使用三AZ、三副本、多数确认、跨地域异步复制与对象归档；Redis只做可重建缓存并独立隔离会话用途；ClickHouse采用分片复制、Keeper仲裁、资源组和对象存储重建；对象存储按用途隔离、启用Versioning、Checksum、KMS、生命周期、WORM及关键数据跨地域复制。所有组件拥有数据Owner级备份矩阵、RPO/RTO、恢复顺序和一致性验证。

区域灾难切换必须先隔离旧地域并建立写入Fence，再按IAM/Core/Gateway/Command、Telemetry、Realtime/Query、报表与AI的优先级恢复；Command只有在旧Control Lane、Connector Lease和Fence失效并完成不确定Attempt对账后才恢复派发。区域内已确认Command、Audit Intent和Kafka遥测目标RPO=0，跨地域按实际复制水位报告；整个地域故障4小时内恢复核心能力。高可用与灾备通过季度故障注入、半年跨地域Restore和年度完整区域演练验证，已确认数据零丢失、旧Fence零发送、跨租户零成功、未知副作用零盲重试和审计完整性是上线硬门禁。
