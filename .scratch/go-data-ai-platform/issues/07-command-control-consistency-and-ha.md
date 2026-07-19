# 确定命令控制的一致性与高可用模型

Type: grilling
Status: resolved
Blocked by: 01, 02, 04, 05
Part of: ../map.md

## Question

设备控制命令如何在权限、安全、低延迟和故障恢复之间取得可验证的一致性？需要确定命令状态机、授权与审批、幂等键、同设备顺序、人工/调度/策略/AI 冲突优先级、Outbox/Inbox、ThingsBoard RPC Adapter、ACK、超时、重试、故障转移、断网补偿、审计不可抵赖性，以及命令链路的 SLO、RPO 和 RTO。

## Comments

- 命令采用 Command Intent 与 Command Attempt 双层状态机。Intent 表达请求、治理、审批、排队与最终业务结果；Attempt 表达某次物理发送、ACK 和验证过程。一个 Intent 可有多个 Attempt，但每次 Attempt 必须有独立 attempt_id、execution_fence、payload_hash、租约和证据。
- 命令创建成功只表示意图已可靠持久化，API 返回 202/SUBMITTED，不得在设备尚未执行时返回“控制成功”。客户端必须提供 organization_id+device_id 范围内的 idempotency_key；同 Key 同 Payload 返回原命令，同 Key 不同 Payload 返回幂等冲突。
- Intent 状态覆盖 DRAFT、SUBMITTED、VALIDATING、AWAITING_APPROVAL、APPROVED、QUEUED、DISPATCHING、IN_PROGRESS 以及 SUCCEEDED、FAILED、REJECTED、CANCELLED、EXPIRED、SUPERSEDED、OUTCOME_UNKNOWN 等终态。Attempt 状态覆盖 PREPARED、SENDING、ACCEPTED、VERIFYING、COMPLETED 及 NOT_SENT、SEND_REJECTED、EXECUTION_FAILED、TIMED_OUT、OUTCOME_UNKNOWN、ABORTED。
- Dispatcher 调用 ThingsBoard 前必须在同一事务持久化 PREPARED Attempt、执行围栏、Payload Hash 与租约；故障转移后无法证明旧执行者未发送时，新实例不得盲目重发，必须进入 OUTCOME_UNKNOWN。只有明确证明 NOT_SENT 时才允许安全重试。
- SUCCEEDED 仅能由强设备 ACK、明确 Persistent RPC 成功状态、验证点达到目标或 Adapter 已验证的等价证据触发。HTTP 2xx 只证明接口响应，调用后超时、连接中断、ACK 丢失、执行后未验证等情况统一进入 OUTCOME_UNKNOWN，不得自动标记 FAILED。
- 超时必须区分排队、审批、调度、传输、设备 ACK、结果验证和命令过期。进入 DISPATCHING 后的取消只停止后续重试/验证，不能保证撤回已发送命令；API 区分 CANCELLED_BEFORE_SEND 与 CANCEL_REQUESTED_AFTER_SEND。
- 状态迁移使用 command_version/attempt_version 单调版本并保存 from/to、reason、actor、causation 和 evidence。迟到 ACK 可将 OUTCOME_UNKNOWN 解析为 SUCCEEDED 或 FAILED，但必须保留原不确定历史和 Resolution 记录。
- 北向接口只接受 Canonical Command，禁止提交任意 ThingsBoard method+params。每个设备使用版本化 CapabilityProfile 将 Canonical Command 映射为 external_method、请求/响应 Schema、超时和验证策略；生产控制只允许 VERIFIED Mapping。
- 风险按命令类型、参数变化、设备类别、现场模式、时间段、Alarm、数据质量、命令来源、影响范围和联锁动态计算为 LOW/MEDIUM/HIGH/CRITICAL，并记录规则版本。权限同时校验 Organization/Site/Device 范围、Capability 和风险级别，不能以只读或普通控制权限推导启停权限。
- 命令提交和批准时生成不可变授权快照；执行阶段不实时依赖 IAM，但必须确认快照未过期、身份未紧急吊销、设备仍在授权范围、Capability/审批版本未变化。IAM 不可用时新建高风险命令 Fail Closed，已批准低风险命令只能在有效快照内继续。
- 参数必须依次通过 Schema、Canonical Unit、设备绝对边界、Site 策略、最大变化量、在线状态、联锁和前置条件校验，禁止静默截断。高风险前置条件只允许使用 VERIFIED、未 stale 且水位满足策略的在线投影，不能使用 ClickHouse 降级结果。
- 审批支持 NONE、SINGLE_APPROVER、TWO_PERSON、ROLE_SEPARATION 和 CHANGE_WINDOW，并绑定 command_id、payload_hash、Capability Version、风险评估和有效期；任何目标、参数、映射或风险变化都使审批失效。批量控制拆为每设备独立 Intent，Break-glass 仍受强认证、设备硬边界、围栏和不可删除审计约束。
- 所有状态变更命令按 device_id 进入串行 Control Lane，并在 PostgreSQL 中分配单调 device_command_sequence；Kafka 只承载交付顺序，持久化序列、active fence 与设备控制状态才是故障切换后的顺序依据。无副作用 QUERY 可走独立 Read Lane。
- 冲突以 device_id+control_group+controlled_property 形成 conflict_key。Capability 声明 QUERY、DESIRED_STATE、EDGE_TRIGGER、PROCEDURE 或 SAFETY_ACTION 语义及 conflicts_with；未声明并发安全时按整台设备冲突处理。
- 引入有期限且可审计的 ControlAuthorityLease，支持 LOCAL_LOCKOUT、MAINTENANCE、MANUAL_HOLD 和 AUTOMATIC。现场安全联锁始终优先；人工 Hold 可暂时压制调度、优化和普通自动化，但 Lease 过期后必须重新计算目标并创建新命令，不能恢复执行陈旧队列。
- 只有仍未进入 DISPATCHING、conflict_key 相同、均为 supersedable DESIRED_STATE 且新命令已独立完成治理时，旧命令才可标记 SUPERSEDED。已发送、一次性动作、Procedure 或审批上下文不同的命令不得自动合并。
- OUTCOME_UNKNOWN 默认冻结相关 control_group，后续冲突命令只能等待迟到 ACK、状态读取、Persistent RPC 查询或人工解析。多设备操作使用每设备独立 Intent 的受审计 Saga，支持停止后续派发或经验证的补偿，但不承诺跨设备原子执行。
- 执行链路由 Command Dispatcher 与 thingsboard-connector-control 分离：Dispatcher 负责设备顺序、租约、Attempt 和 Fence；Connector 负责 ThingsBoard Token、RPC/Persistent RPC 协议、错误分类、限流、原始请求响应证据和持久化 connector_execution 日志，不能自行决定授权或业务成功。
- Connector 对相同 attempt_id+execution_fence+payload_hash 幂等返回已有记录；同 Attempt 不同 Payload 立即拒绝。只有能够证明请求尚未写出的 DNS/建连/本地拒绝等 PRE_SEND 失败可自动重试；REQUEST_COMMITTED 后的超时、断线、崩溃或失联默认 OUTCOME_UNKNOWN，禁止盲目重发。
- 重试策略属于版本化 Capability，支持 NEVER、PRE_SEND_ONLY、IDEMPOTENT_REISSUE、STATUS_QUERY_THEN_REISSUE 和 MANUAL_ONLY，默认 PRE_SEND_ONLY。发送后重发必须由真实设备契约证明命令 ID 幂等或先查询外部状态，不能因命令看似 Desired-State 就假设安全。
- 离线命令默认保留在平台队列并在设备恢复时重新校验授权、审批、时效、在线状态、联锁和前置条件；Persistent RPC 仅在 Capability 明确允许、可查询状态且延迟执行安全时启用。创建 Persistent RPC 只表示 PERSISTENT_PENDING，不代表执行成功。
- Dispatcher 跨可用区多实例部署，但同设备只允许一个有效执行者。Consumer Rebalance 或实例重启后必须通过 Attempt、Fence、Connector 日志与 Persistent RPC 状态恢复；不能把 SENDING 统一改回 QUEUED。IntegrationInstance 级限流和熔断用于隔离 ThingsBoard 故障，设备级错误不得触发全局熔断。
- Command Service 的本地 PostgreSQL 事务必须原子写入 Command/Attempt、Transition、Idempotency、Device Control State、授权/审批快照、Audit Intent 和 Outbox；事务提交后返回 202。Audit Ledger 不可用时可依赖本地 Audit Intent 积压，但本地审计意图无法持久化时必须拒绝控制。
- Audit Ledger 采用 append-only 事件、按 Organization/时间分片的 Hash Chain、周期签名 Checkpoint 及启用保留锁/WORM 的对象归档，至少保留 7 年。审计保存 Canonical Command、脱敏参数、Payload/响应 Hash、Capability/Policy/授权/审批版本、Fence 与证据引用，不保存 Token、密码或设备凭据。
- 命令接收 SLO 为 99.99%/月，合法命令 POST→SUBMITTED 目标 p95≤300ms、p99≤1s；无人工审批的治理 p95≤500ms、p99≤2s；条件满足时 QUEUED_READY→SEND_STARTED p95≤1s、p99≤3s；状态传播 p95≤1s、p99≤3s。设备 ACK 与物理执行延迟按 Capability 单独统计，不混入平台内部 SLO。
- 单区域多可用区内，已返回 202 的 Command、Transition、Outbox 和 Audit Intent 目标 RPO=0；区域灾难不宣称 RPO=0。单 Pod 恢复目标≤30秒、Consumer Rebalance≤60秒、Connector 实例≤2分钟、单可用区≤5分钟；恢复服务能力不等价于自动重发未决 Attempt。
- 降级必须 Fail Closed：Command PostgreSQL 不可用时拒绝新命令；IAM/Core 不可用时新建高风险拒绝；在线状态投影不可用时拒绝依赖前置条件的命令；Control Backbone 不可用时仅可靠接收至 Outbox 并暴露派发延迟；ThingsBoard/Connector 不可用时不进入发送阶段；审计积压超过阈值后高风险控制拒绝。

## Answer

命令平台采用 Command Intent 与 Command Attempt 双层模型。Intent 负责命令请求、治理、审批、排队、冲突和最终业务结果；Attempt 负责一次物理执行、围栏、发送、ACK 和状态验证。命令创建接口只在 PostgreSQL 原子事务成功后返回 202/SUBMITTED；客户端幂等键在 organization_id+device_id 范围内生效，同 Key 同 Payload 返回原命令，同 Key 不同 Payload 返回冲突。状态迁移使用单调版本和不可删除 Transition；只有强设备 ACK、明确 Persistent RPC 成功状态或经过真实契约验证的 reported-state 证据才能进入 SUCCEEDED，发送后无法判断统一进入 OUTCOME_UNKNOWN，并允许迟到证据后续解析但不删除不确定历史。

北向接口只接受 Canonical Command，禁止透传任意 ThingsBoard method+params。每个设备的版本化 CapabilityProfile 定义 RPC Method、请求/响应 Schema、超时、重试、Persistent RPC 和验证策略，生产控制只允许 VERIFIED Mapping。风险根据命令、参数变化、设备类别、现场模式、Alarm、数据质量、来源、影响范围和联锁动态计算；权限同时约束 Organization、Site、Device、Capability 和风险级别。参数依次经过 Schema、Canonical Unit、设备硬边界、Site 策略、最大变化量、在线状态、联锁和乐观状态版本校验，禁止静默截断。审批绑定 Payload Hash、Capability Version、风险与有效期，任何目标、参数或版本变化都使审批失效；Break-glass 仍受强认证、设备硬边界、执行围栏和不可删除审计约束。

所有状态变更命令按 device_id 进入串行 Control Lane，并由 PostgreSQL 分配单调 device_command_sequence；Kafka 分区顺序只是交付机制，持久化序列、active Attempt 和 execution_fence 才是故障切换后的顺序权威。冲突以 device_id+control_group+controlled_property 形成 conflict_key，并通过有期限的 ControlAuthorityLease 仲裁 LOCAL_LOCKOUT、MAINTENANCE、MANUAL_HOLD 和 AUTOMATIC。只有未进入 DISPATCHING、同 conflict_key、均为可合并 DESIRED_STATE 且新命令已独立完成治理时，旧命令才能 SUPERSEDED。已发送、Procedure、一次性动作或 OUTCOME_UNKNOWN 默认冻结冲突通道。多设备操作使用每设备独立 Intent 的受审计 Saga，不承诺跨设备原子执行；安全联动和快速保护优先留在 PLC、DDC 或 Edge。

执行链路由 Command Dispatcher 与 thingsboard-connector-control 分离。Dispatcher 负责持久化设备顺序、Attempt、租约和 Fence；Connector 负责 ThingsBoard Token、RPC/Persistent RPC 协议、错误分类、限流、原始证据和持久化 connector_execution 日志。Connector 对 attempt_id+execution_fence+payload_hash 幂等，旧 Fence 或同 Attempt 不同 Payload 必须拒绝。只有能证明请求尚未写出的 PRE_SEND 错误允许自动重试；REQUEST_COMMITTED 后的超时、断线、崩溃或失联进入 OUTCOME_UNKNOWN。重试策略属于 Capability，默认 PRE_SEND_ONLY；发送后重发必须由真实设备契约证明设备端幂等，或先通过外部状态查询确认。离线命令默认保留在平台队列，恢复在线后重新校验授权、审批、时效、联锁和当前状态；Persistent RPC 仅在延迟执行安全、状态可查询且 Capability 显式允许时使用。

命令权威事务同时写入 Command/Attempt、Transition、Idempotency、Device Control State、授权/审批快照、Audit Intent 和 Outbox。Audit Ledger 从 Outbox 消费 append-only 事件，以 Hash Chain、签名 Checkpoint 和受保留锁保护的对象归档提供可验证防篡改证据，至少保留 7 年。审计记录覆盖请求、授权、风险、审批、Lease、参数/联锁、Attempt、Fence、RPC 证据、ACK、验证、OUTCOME_UNKNOWN、人工解析、Break-glass 和批量 Saga，并对敏感 Payload 采用脱敏 Hash、加密证据引用与严格读取审计。

命令接收 SLO 为 99.99%/月；合法命令接收 p95≤300ms、p99≤1s，无人工审批治理 p95≤500ms、p99≤2s，条件满足时就绪到发送 p95≤1s、p99≤3s，状态传播 p95≤1s、p99≤3s。设备 ACK 和最终执行延迟按 Capability 单独衡量。单区域多可用区内，已确认接收的 Command、Transition、Outbox 和 Audit Intent 目标 RPO=0；区域灾难不宣称 RPO=0。单 Pod、Consumer Rebalance、Connector 和单可用区故障目标分别在 30 秒、60 秒、2 分钟和 5 分钟内恢复处理能力，但恢复后仍必须执行 Attempt/Fence/审批/状态扫描，不能自动重发不确定命令。

所有依赖故障均有明确降级边界：Command PostgreSQL 不可用时拒绝新命令；IAM/Core 或在线控制投影不可用时高风险命令 Fail Closed；Control Backbone 不可用时命令只在数据库与 Outbox 中可靠排队；ThingsBoard/Connector 不可用时不创建可能发送的 Attempt；Audit Ledger 短时不可用可依赖本地 Audit Intent 积压，超过阈值后高风险控制拒绝。正确性目标包括已确认接收命令零丢失、无有效 Fence 发送为零、未审批高风险发送为零、未验证 Capability 生产发送为零、跨租户发送为零，以及审计篡改未检测为零。投产前必须通过崩溃点、重复消息、Consumer Rebalance、请求写出后断线、迟到 ACK、ACK/状态冲突、单可用区故障、旧 Fence 恢复、控制权冲突和审计链验证等故障注入测试。
