# 选择事件骨干与交付语义

Type: research
Status: resolved
Blocked by: 01, 04
Part of: ../map.md

## Question

目标负载和服务边界需要怎样的事件骨干？需要比较 Kafka、Redpanda 及其他可行方案，确定分区键、顺序范围、至少一次交付、幂等、重放、Schema 演进、消费者组、背压、死信、跨服务事务和运维成本，并给出平台统一事件信封与版本策略。

## Comments

- 平台唯一事实事件骨干采用 Kafka API 兼容的分区式持久化提交日志；候选实现限定为 Apache Kafka、Redpanda 或 Kafka 兼容托管服务。
- NATS JetStream、RabbitMQ、Redis Streams 等不承担平台事实日志，可仅用于明确的局部工作队列、低价值通知或开发场景。
- 事件骨干负责可靠接收、短中期保留、消费者解耦、Offset 恢复和受控重放；90 天原始遥测、五年聚合、永久审计和报表文件仍分别进入对象存储、分析存储、审计账本和文件存储。
- 生产采用统一 Kafka API、统一事件信封和治理规范，但物理分为 Control Backbone 与 Data Backbone。Control 承载命令、IAM/Core、自动化、AI 状态和高优先级安全审计；Data 承载原始/标准化遥测、最新状态 Changelog、Registry/PointMapping 投影、分析任务与隔离数据。
- Topic 按事件家族、保留期、权限和 SLO 划分，不按每个事件类型、Organization、Site 或 Device 建 Topic。遥测与命令使用 device_id 分区，Core/IAM 使用 aggregate_type+aggregate_id，Investigation 使用 investigation_id；只承诺同一分区键内有序，不承诺全局顺序。
- Control/Data 之间通过可积压、可重放、保留原 event_id 和源 Offset 的 Event Bridge 传播必要投影或派生事实；禁止业务事务直接双写两个 Backbone，也不把全部遥测复制到 Control。
- 平台统一采用端到端至少一次交付，不宣称跨 PostgreSQL、Kafka、ThingsBoard、对象存储或外部通知的全局 exactly-once。业务结果通过 event_id 幂等、aggregate_version/source_version、防旧覆盖和执行围栏实现 effectively-once。
- 数据库到 Kafka 使用 Transactional Outbox，Kafka 到数据库使用 Transactional Inbox；业务状态、版本/Transition、审计意图和 Outbox 必须同一事务提交，消费者处理结果、Inbox 和本服务 Outbox 也必须同一事务提交，成功后再提交 Offset。
- 命令物理副作用必须先持久化 attempt、execution_fence、payload_hash 和租约，再调用 ThingsBoard。无法判断是否已执行时进入 OUTCOME_UNKNOWN，禁止盲目重发非幂等命令；超时不自动等价于失败。
- 跨语言消息采用统一 Protobuf 信封与 Schema Registry，明确区分 EVENT、COMMAND、TASK、CHANGELOG、SNAPSHOT；信封携带 message_id、message_type、schema_version、租户/Site、分区键、聚合版本、occurred_at/published_at、correlation/causation/trace、actor/授权快照和 payload 元数据。
- 原始遥测保留 ThingsBoard 原始字节及 content_type，并附 IntegrationInstance、外部设备、源时间/序列、payload_hash 和接收时间；标准化遥测使用版本化 Protobuf 批次。默认向后兼容，新增可选字段演进 Schema；分区键、顺序、权限、压缩或保留语义发生破坏性变化时才升级 Topic 大版本。
- Topic 按控制、原始遥测、标准化、压缩投影、分析任务、隔离与死信分级保留；原始遥测在 Kafka 中最低保留 24 小时、目标 72 小时，并由高优先级归档消费者持续写入对象存储，Kafka 不承担 90 天原始遥测权威存储。
- 生产者必须显式背压，不能仅写进程内存后确认成功；错误按瞬时基础设施错误、数据治理错误、程序缺陷和命令不确定副作用分别进入 Retry、Quarantine、DLQ 或 OUTCOME_UNKNOWN。顺序敏感聚合按键暂停，不能让后续版本越过缺口。
- 所有重放必须创建带范围、原因、审批、目标、side_effect_policy 和结果统计的 Replay Job，使用独立 Consumer Group；默认禁止重放命令、通知、Webhook、收费模型调用和工单等外部副作用。
- 产品选型优先级为：允许托管时优先经过 POC 的 Kafka API 兼容托管服务；必须私有化且无现成 Kafka 平台时默认 Redpanda Self-Managed；已有成熟 Kafka SRE/统一平台或明确生态硬约束时才选择 Apache Kafka Self-Managed。Control/Data Backbone 使用同一产品和主版本，应用只依赖 Kafka API，不依赖厂商专属业务接口。
- 每个独立副作用或投影使用独立 Consumer Group；Topic、Schema、分区键和兼容策略由唯一领域 Owner 负责，基础设施团队负责集群、ACL、配额、保留、监控和灾备。生产服务身份按环境、服务和 Backbone 独立，禁止共享超级账号。
- 租户默认共享 Topic，通过 organization_id/site_id、服务端租户注入、消费者校验和下游租户键实现隔离；高合规租户可升级独立 Topic、命名空间或集群而不改变契约。顺序敏感 Topic 不得原地增加分区，需通过新 Topic 大版本、影子消费、围栏切换和尾部清空迁移。

## Answer

平台唯一事实事件骨干采用 Kafka API 兼容的分区式持久化日志。生产统一使用 Kafka API、Protobuf 消息信封、Schema Registry 和治理规则，但按故障域拆成独立的 Control Backbone 与 Data Backbone：Control 承载命令、IAM/Core、自动化、AI 状态和高优先级安全审计；Data 承载原始/标准化遥测、最新状态 Changelog、Registry/PointMapping 投影、分析任务、Quarantine 与 DLQ。两者使用相同产品和主版本，通过可积压、可重放的 Event Bridge 传播必要投影或派生事实，禁止业务事务直接双写两个集群。

Topic 按事件家族、SLO、权限、保留期和压缩语义划分，不按每个事件类型、Organization、Site 或 Device 建 Topic。遥测和命令按 device_id 分区，Core/IAM 按 aggregate_type+aggregate_id，Investigation 按 investigation_id；平台只承诺同一分区键内有序，不承诺全局顺序。顺序敏感 Topic 不得直接在线增加分区，扩容必须创建新 Topic 大版本，通过影子消费、写入围栏、切换点和旧 Topic 尾部清空完成迁移。

端到端交付语义统一为至少一次，不宣称跨 PostgreSQL、Kafka、ThingsBoard、对象存储或外部通知的全局 exactly-once。数据库到 Kafka 使用 Transactional Outbox，Kafka 到数据库使用 Transactional Inbox；业务状态、版本/Transition、审计意图与 Outbox 同事务提交，消费者处理结果、Inbox 与本服务 Outbox 同事务提交，成功后再提交 Offset。业务结果通过 message_id 幂等、aggregate_version/source_version、防旧覆盖、确定性处理和执行围栏实现 effectively-once。

命令物理副作用必须在调用 ThingsBoard 前持久化 command_id、attempt_id、execution_fence、payload_hash、租约和 PREPARED 状态。明确未发送时可按策略安全重试；明确 ACK 时幂等更新结果；无法判断设备是否已执行时进入 OUTCOME_UNKNOWN，禁止盲目重发非幂等命令，超时不得自动解释为失败。

跨语言事件使用统一 Protobuf 信封，明确区分 EVENT、COMMAND、TASK、CHANGELOG 和 SNAPSHOT。信封至少包含 message_id、message_type、schema_version、producer、organization_id/site_id、partition_key、聚合标识与版本、occurred_at/published_at、correlation_id、causation_id、trace_id、actor/授权快照以及内容类型和 payload。原始遥测保留 ThingsBoard 原始字节及 content_type，并附 IntegrationInstance、外部设备、源时间/序列、payload_hash 和接收时间；标准化遥测使用版本化 Protobuf 批次。默认采用向后兼容演进，只有分区、顺序、权限、保留或压缩语义发生破坏性变化时才升级 Topic 大版本。

Topic 分级保留：原始遥测在 Kafka 中最低 24 小时、目标 72 小时，并由高优先级归档消费者持续写入不可变对象存储；Kafka 不承担 90 天原始遥测、五年聚合、永久审计或报表文件的长期权威存储。生产者必须显式背压，不能仅写进程内存后确认成功。错误按瞬时基础设施错误、数据治理错误、程序缺陷和命令不确定副作用分别进入 Retry、Quarantine、DLQ 或 OUTCOME_UNKNOWN；顺序敏感聚合按键暂停，不能让后续版本越过缺口。

所有重放必须创建 Replay Job，记录范围、原因、审批、目标消费者、目标命名空间、side_effect_policy、开始/完成时间和结果统计，并使用独立 Consumer Group。默认禁止重放产生设备命令、通知、Webhook、收费模型调用、工单等外部副作用。每个独立副作用或投影使用独立 Consumer Group；Topic、Schema、分区键和兼容策略由唯一领域 Owner 负责，基础设施团队负责集群、ACL、配额、保留、监控和灾备。生产身份按环境、服务与 Backbone 隔离，禁止共享超级账号。

产品选择策略为：允许托管时优先经过 POC 的 Kafka API 兼容托管服务；必须私有化且无现成 Kafka 平台时默认 Redpanda Self-Managed；已有成熟 Kafka SRE、统一平台或明确生态硬约束时才选择 Apache Kafka Self-Managed。应用层只依赖 Kafka API，不依赖厂商专属业务接口。

Control/Data Backbone 均应跨三个可用区，关键 Topic 使用至少三副本、多数副本确认、acks=all、幂等 Producer、禁用不干净 Leader 选举和可用区感知。区域内对已成功确认的事件承诺 RPO=0；区域级灾难采用异步 Warm Standby、对象存储归档和可重建投影，并持续暴露复制与归档水位，不虚假宣称跨区域 RPO=0。上线前必须通过稳态与 5 倍突发、Broker/AZ 故障、滚动升级、一小时积压两小时清空、Outbox/Inbox 重复、Schema 阻断、Quarantine/DLQ、受控重放、ACL/租户隔离、对象归档、Bridge 恢复和命令 OUTCOME_UNKNOWN 等验收，且数据丢失计数必须为零。
