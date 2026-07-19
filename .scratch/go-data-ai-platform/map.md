# Wayfinder Map: Go 数据与 AI 平台架构

## Destination

形成一份可直接进入开发的 Go 数据与 AI 平台规格：服务边界、数据所有权、南北向接口、事件契约、SLO、高可用、安全、迁移路径、验收标准和首批实施切片均已定案，不再需要新的架构讨论即可执行 `/to-spec` 与 `/to-tickets`。

## Notes

- 领域：HVAC 数据与 AI 平台。
- ThingsBoard 保持 IoT 设备平台层；`apps/hvac-web` 是业务应用层；Go 平台是数据、业务、安全和 AI 治理入口；Python `EnergyAgent` 保持 AI 推理与工作流执行层。
- ClickHouse、具体 API Gateway 产品和云厂商仍是待验证假设；事件骨干已确定采用 Kafka API 兼容的 Control/Data 双 Backbone，但具体使用托管 Kafka、Redpanda 或 Apache Kafka 需按部署条件和 POC 结果选择。
- 每个 ticket 只解决一个架构问题；默认使用 grilling 和 domain-modeling，涉及第三方能力与容量依据时使用 research，接口形态需要具体反馈时使用 prototype。
- 本地图只做规划，不实施 Go 服务、不迁移数据、不改生产链路。
- 仓库的正式 tracker 是 GitHub Issues，但当前 `gh` 认证 token 失效；本地 Markdown 按 Wayfinder fallback 规则建立。恢复认证后，应按相同名称、类型和阻塞边发布为 GitHub map/sub-issues。

## Decisions so far

- [确定运行规模、SLO 与容量边界](issues/01-operating-envelope-and-slos.md) — 按 5 万–60 万设备、最高 12 万设备包/秒稳态和 5 倍突发设计，明确分级 SLO、数据保留、并发、RPO/RTO、降级顺序及三年 3 倍扩展边界。
- [确定租户模型、领域边界与数据权威](issues/02-tenancy-domain-and-data-authority.md) — 以 Organization 为租户边界，确定 Site 单一所有者、多组织授权、五层资产模型、平台 ID 与 ThingsBoard 解耦、单字段数据权威、范围化权限及可升级的混合租户隔离。
- [盘点现有契约与迁移约束](issues/03-current-contract-and-migration-inventory.md) — 现有系统形成的是行为基线而非统一协议；保留 Site 隔离、遥测/命令/调度和 Investigation 语义，通过统一 Gateway 与反腐层演进旧 ID、响应、实时协议和数据库，并清退演示及临时实现。
- [确定粗粒度服务边界与故障域](issues/04-service-boundaries-and-failure-domains.md) — 采用五个平面、16 个粗粒度逻辑服务/Worker 和独立 Deployment；按命令、遥测、在线读取、批处理/AI 隔离资源，以唯一数据所有者、事件化状态传播、版本化本地投影和独立发布契约阻断级联故障。
- [选择事件骨干与交付语义](issues/05-event-backbone-and-delivery-semantics.md) — 采用 Kafka API 兼容的 Control/Data 双 Backbone、按键局部有序和至少一次交付；以 Outbox/Inbox、Protobuf/Schema Registry、分级保留、显式背压、Quarantine/DLQ、受控重放及三可用区多数确认实现可恢复且不虚假承诺全局 exactly-once 的事件体系。
- [确定 ThingsBoard 参考契约与生产验证门禁](issues/14-capture-real-thingsboard-contracts.md) — 架构阶段采用常见 HVAC Canonical Point/Alarm/Command 和版本化 Profile Mapping，不读取现有绑定设备；真实 Key、单位、RPC 与 ACK 验证下移为生产接入硬门禁，未验证 Profile 不得执行设备控制。
- [确定遥测摄取、处理、存储与查询架构](issues/06-telemetry-pipeline-storage-and-query.md) — 采用 Push-first 原始批次摄取、不可变绑定/映射与强类型部分标准化，以 Kafka Changelog 和可重建 Redis 提供最新态；ClickHouse、对象存储和版本化聚合承载历史分析，并通过统一 Query Service、派生血缘和 Investigation Dataset 隔离查询与 AI 取证。
- [确定命令控制的一致性与高可用模型](issues/07-command-control-consistency-and-ha.md) — 采用 Intent/Attempt 双层状态机、Canonical Command 治理、每设备串行 Control Lane、执行 Fence 与 Connector 证据日志；发送后不确定统一进入 OUTCOME_UNKNOWN，以原子 Outbox/Audit Intent、防篡改审计链、99.99% 接收 SLO 和多可用区 Fail-Closed 降级实现可验证控制。
- [确定北向 API、BFF 与实时订阅契约](issues/08-northbound-api-bff-and-realtime-contract.md) — 以 `/api/v1` 与 `hvac.realtime.v1` 形成唯一北向契约：OIDC/BFF Session、显式 Organization/Site Scope、平台 UUIDv7、类型化 REST/Problem Details、Cursor 分页、Site 时区、有限 BFF、Snapshot+Delta、可恢复 WebSocket、有界背压、命令状态流和契约驱动 Client 生成均已定案；`X-Site-Id`、ThingsBoard 公共 ID、Socket.IO 和通用成功包络直接退出新契约。
- [确定 AI 平台边界与工具治理](issues/09-ai-platform-boundary-and-tool-governance.md) — Go AI Platform 拥有长期 AI 业务状态、预算、Provenance、质量门禁与生命周期；Agent/Copilot 仅拥有短期执行或交互状态。Agent 经双主体授权、固定 Tool Manifest 与受治理 Model Policy异步执行，使用 Lease/Fencing、幂等计量、Evidence链和不可变结果版本；Recommendation 与 Command Governance强隔离，并补齐协作授权、保留删除、熔断隔离、SLO及迟到结果提升规则。
- [确定安全、租户隔离与审计基线](issues/10-security-tenancy-and-audit-baseline.md) — 采用Gateway/BFF、短期Workload Identity、mTLS和Audience受限委托形成零信任身份链；Organization/Site租户上下文贯穿数据库RLS、缓存、消息、对象、搜索、Analytics、Realtime与AI。内部API和Egress默认关闭，ThingsBoard仅由隔离Connector访问，Secret由KMS/Secret Manager管理；Webhook执行签名、防重放与幂等，Audit Ledger采用事务Audit Intent、Hash Chain、签名Checkpoint和WORM归档。最小权限、职责分离、Break-glass、分层限流、签名供应链、安全撤销/隔离及零容忍安全验收均已定案。
- [确定部署拓扑、可观测性与灾难恢复](issues/11-deployment-observability-and-disaster-recovery.md) — 生产采用Kubernetes兼容的单主地域三可用区架构和异步Warm Standby，按Command、Telemetry、在线查询、Core/IAM、Connector及Batch/AI隔离资源池。PostgreSQL单Writer跨AZ同步复制并执行PITR，Kafka三AZ多数确认，Redis仅作可重建缓存，ClickHouse分片复制并由对象存储重建；发布使用签名镜像、GitOps、金丝雀和Error Budget门禁，OpenTelemetry统一观测。区域切换先隔离旧地域并建立Fence，按实际复制水位报告RPO，4小时内恢复核心能力，并以季度故障注入、半年Restore和年度区域演练验证。
- [确定 NestJS 到 Go 的迁移与共存路径](issues/12-nestjs-to-go-migration-and-coexistence.md) — 采用Gateway先行的Strangler与版本化Route/Data Ownership Registry，Legacy只在私网作为有退役期限的Anti-Corruption Layer。Go使用独立Schema和平台ID，数据通过快照回填、单向Outbox/CDC、尾差追平与Owner Fence迁移；只读可Shadow双读，写链路禁止双写。Command与Scheduler按唯一副作用Owner和Generation灰度，已接收动作由原Owner收敛，回滚只影响未来请求。NestJS功能冻结，退役前必须实现流量归零、无未决副作用/CDC尾差、历史审计可查询及灾备不再依赖Legacy。
- [确定架构验收标准与首批纵向切片](issues/13-acceptance-and-first-vertical-slices.md) — 采用Architecture Exit Gate、Slice Release Gate和可复现Release Evidence Bundle，将契约、唯一Owner、租户零泄漏、容量/SLO、故障注入、备份Restore、迁移回滚及Command/AI安全不变量转为硬验收。首批按S0契约交付骨架、S1组织/站点/设备读取、S2遥测Snapshot+Delta、S3安全Command闭环推进，再扩展Schedule、AI、Recommendation交接和Legacy Cohort切换；每个Slice必须端到端交付前后端、数据、测试、观测、Runbook与回滚。

## Architecture status

- 票据01–14均已解决，Wayfinder Destination已达到；NestJS保持Legacy Frozen并按Strangler计划迁移。
- 核心边界与安全不变量不得在实施中隐式重开；任何偏离必须通过显式ADR或架构变更票据治理。

## Implementation handoff

- [S0 — Platform Contract & Delivery Foundation](../go-data-ai-platform-s0/spec.md) 已形成正式实施规格。
- [S0 implementation tracker](../go-data-ai-platform-s0/README.md) 已拆分8张`ready-for-agent`纵向票据；初始frontier为票据01“Contract-first Gateway bootstrap”。
- S0完成前不进入S1正式开发；每张票据必须遵守其阻塞边、外部黑盒验收和NestJS Legacy Frozen边界。

## Not yet specified

- 各服务的精确OpenAPI、gRPC、Protobuf、Realtime和事件字段，以及数据库DDL、索引、分区/排序键与Topic数量，需要在实施规格中展开并通过兼容性与容量测试。
- 具体云厂商、Kubernetes发行版、托管Kafka/PostgreSQL/Redis/ClickHouse/Object Storage产品、节点规格和成本预算，需要在单主地域三可用区加Warm Standby的既定拓扑内完成POC与采购决策。
- 具体AI模型供应商、模型部署与成本参数，需要在Model Policy、数据分类、区域、保留、评估、回退和Kill Switch的既定边界内选择。
- 每个生产ThingsBoard IntegrationInstance和设备Profile仍需执行真实Key、单位、Attribute、Alarm、RPC和ACK验证；未达到`VERIFIED`的Profile不能进入生产控制。

## Out of scope

- 用 Go 重写 Python EnergyAgent。
- 替换 ThingsBoard 的设备接入、协议适配和基础 IoT 能力。
- 在本地图过程中实现、部署或切换任何生产服务。
- 与 Go 平台架构无关的 HVAC Web 视觉重设计。
- 在缺乏容量依据前采购或绑定具体云产品。
