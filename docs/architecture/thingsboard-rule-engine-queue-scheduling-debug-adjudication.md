# ThingsBoard CE Rule Engine、队列、调度与调试裁决

状态：`D05_ADJUDICATION_COMPLETE`

审查票：[审查 Rule Engine、队列、调度与调试](https://github.com/SwayingWindmill/HVAC_web/issues/243)

本文只裁决 ThingsBoard CE v4.3.1.1 的 Rule Chain、Rule Node、消息信封、Actor 路由、嵌套链、队列、顺序、重试、执行限制、状态、调度、调试、脚本与 77 个内置节点，并将其与 HVAC Web 当前 Alarm Rule、Metric Engine、Domain Outbox、Scheduler、Command Safety、Operations Agent 和 Edge Runtime 做源码级反向审查。

本文不把任一方预设为正确，也不在本票内实现运行时。现有本地模块仍按 `UNVERIFIED` 审查；裁决词汇为 `ADOPT / ADAPT / REPLACE / KEEP / REJECT / DEFER`。

## 1. 执行结论

HVAC Web 需要实现完整 Rule Engine，但“完整”不等于复制 ThingsBoard 的通用 IoT 自动化平台。固定源码与本地源码对照后的结论是：

1. **当前本地没有完整 Rule Engine。** 仓库只有单阈值 Alarm Rule、Metric 受限表达式、耐久 Scheduler、Domain Outbox、Command 动态安全检查和 Operations Agent 状态机；没有通用 Rule Definition/Revision/Binding、图发布、Node Catalog、执行记录、跨节点状态、Rule UI、Debug Trace 或 Rule Replay。任何“现有模块已经组成完整规则引擎”的说法都不成立。
2. **吸收 ThingsBoard 的执行骨架。** Rule Chain/Node 生命周期、组件描述、消息信封、分支扇出、嵌套链调用栈、节点生命周期、按 Originator 串行、批次提交、Checkpoint、节点执行上限、时限 Debug 和统计都解决了真实问题，应进入目标设计。
3. **不复制 ThingsBoard 的可变发布模型。** 固定版本在同一 Rule Chain/Node 记录上原位更新，运行中的消息没有固定不可变 Rule Revision；目标必须使用 Draft、不可变 Released Revision、显式 Binding Activation 和整版回滚。执行一旦开始就固定 Package Digest、Node Catalog Version 和 Binding Revision。
4. **不复制 Giant Context 和任意类加载。** ThingsBoard 节点通过类名反射实例化，`TbContext` 可访问 Telemetry、Alarm、RPC、Credential、Cassandra、Mail、SMS、Notification、AI 等大量服务。目标节点只获得声明过的窄端口、预算和 Scope，不允许 Rule Engine 成为跨域 God Service。
5. **采用至少一次投递与稳定幂等，不宣称 exactly-once。** ThingsBoard 支持整批或部分重试，但 `RETRY_ALL` 会重放已成功消息，派生消息使用随机 ID，重试耗尽或 Skip 策略最终提交偏移。目标必须使用确定性 Execution/Work Item/Effect ID、Owner Inbox/Receipt、有限重试、Dead/Quarantine 和可证明的 ACK 边界。
6. **保留本地耐久 Scheduler 方向，替换 ThingsBoard 内存 Delay/Dedup。** ThingsBoard `tellSelf` 明确留有持久化待办，Delay 和 Dedup 都先 ACK 再把数据留在内存；Delay 已被上游自身标记为可能丢失。目标 Timer、Debounce、Window、Retry Wakeup 和 Continuation 全部复用耐久 Scheduler/Job 模式。
7. **保留本地领域 Owner，不允许规则节点直接改业务真值。** Telemetry/Metric 归 D04，Alarm/Notification 归 D06，Command/RPC/OTA 归 D03，Edge Release 归 D08，外部投递和 AI 归 D09。Rule Engine 只产生带稳定身份的 Domain Effect Intent，由 Owner 校验并提交。
8. **拒绝任意 JavaScript/TBEL 进入生产控制和安全路径。** 目标只提供版本化、确定性、无 I/O、受类型和资源预算限制的表达式；脚本不能读取 Secret、访问网络、写数据库、发 Command、改变 Alarm Lifecycle 或绕过 Owner API。
9. **Debug 不能代替 Replay。** ThingsBoard 有输入/输出/失败 Debug Event、15 分钟全量调试、失败常开、租户速率限制和字段截断，但固定源码没有一等 Rule Replay。目标要同时实现脱敏 Trace 与无副作用 Deterministic Replay，真实重投必须是另一项受授权操作。
10. **本地较强部分也存在真实缺口。** Scheduler 的 Lease/Misfire/Retry/Dead 方向优于 ThingsBoard Delay，但主 `Cycle` 路径缺覆盖证据；Domain Outbox 有 Lease/Inbox，但 Retry 没有最大次数和 Dead；Metric Expression 仅支持 Go AST 的数字、标识符和 `+ - * /`，没有显式 AST 深度/节点预算；Alarm Rule 只有 `SIMPLE_THRESHOLD`。这些都不能按“已完成”处理。

因此 D05 的最终方向是：**以 ThingsBoard 的 Rule Chain 执行模型作为参考骨架，以本地 Domain Owner、Outbox/Inbox、Scheduler、Evidence、Command Safety 和 Edge Safety 作为硬边界，重建一个不可变发布、类型化、耐久、可预算、可回放但不拥有跨域业务真值的 Rule Runtime。**

## 2. 范围与跨域边界

### 2.1 D05 拥有

- Rule Package、Rule Revision、Rule Binding 与发布/回滚生命周期；
- Node Definition Catalog、节点配置 Schema、端口类型、权限和 Placement；
- Rule Event Envelope、Execution、Work Item、Node Attempt、Continuation、Rule State；
- 图编译、静态校验、路由、扇出、子图、执行预算和终止语义；
- Rule Queue 的提交、顺序、Lease、Retry、Timeout、Dead 与 Quarantine；
- Rule Trace、Debug Capture、Simulation/Replay 与统计；
- Rule 管理 API 和受治理编辑/发布/观察 UI 的契约。

### 2.2 D05 不拥有

| 能力 | 权威 Owner | Rule Engine 允许做什么 |
| --- | --- | --- |
| Telemetry / Latest / History | D04 Telemetry | 读取授权 Snapshot，产生派生意图；不得直接写事实表 |
| Metric Definition / Result | D04 Metric | 触发或消费 Metric 事实；不得重建 Metric SoT |
| Alarm Lifecycle | D06 Alarm | 产生 `AlarmPublicationIntent` / `AlarmConditionObservation` |
| Notification | D06 + D09 | 产生 `NotificationRequestIntent`；通道投递由 Delivery Owner 完成 |
| Command / RPC / OTA | D03 Command | 产生 `CommandIntentRequest`；审批、Lease、Fence、Dispatch、Readback 不可绕过 |
| Registry / Profile | D02 Registry | 按精确 Revision 读取 Binding/Profile；不得任意建关系或改凭据 |
| Edge 本地控制 | D08 Edge | 执行签名 Rule Package 的受限子集；Safety/Arbiter/Controller 保持权威 |
| AI / 外部系统 | D09 Integration | 产生受策略约束的异步 Intent；不得节点内直连 Provider |
| 基础队列、配额、HA | D10 Platform | 复用基础设施；D05 定义业务执行语义和预算 |

### 2.3 与用户文档对齐

`智慧能源系统_前端交互与能源控制UX规范_V1.1.md` 要求 Rule Version、触发条件、当前值、Timeline、Acknowledgement、Recovery 可解释，并严格区分“平台接受”“设备执行”和“状态未知”。因此 Rule UI 只能显示 Rule Evaluation 与 Intent 状态，不能把 Intent Accepted 显示为 Command Executed，也不能把 Shadow/Replay 显示为真实动作。

`智慧能源系统_前端工程架构与实现设计_V1.md` 要求真实授权在 Go API 执行、失败不无限自动重试、UNKNOWN 保持明确。目标 Rule UI 不在浏览器执行规则，不持有节点 Secret，不用前端 Permission 代替服务端授权，也不对 Dead/Quarantine 自动无限重放。

## 3. 固定证据基线

### 3.1 ThingsBoard 固定源码

- 仓库：`thingsboard/thingsboard`
- Tag：`v4.3.1.1`
- Commit：`c2a52e46c44e308ddee430e7266b8e10eddde9c4`
- License：Apache-2.0

主要源码入口：

- `dao/.../BaseRuleChainService.java`
- `dao/.../RuleChainDataValidator.java`
- `application/.../RuleChainController.java`
- `application/.../DefaultTbRuleChainService.java`
- `application/.../actors/ruleChain/RuleChainActorMessageProcessor.java`
- `application/.../actors/ruleChain/RuleNodeActorMessageProcessor.java`
- `application/.../actors/ruleChain/DefaultTbContext.java`
- `common/message/.../TbMsg.java`
- `rule-engine/rule-engine-api/.../TbNode.java`
- `rule-engine/rule-engine-api/.../TbContext.java`
- `application/.../service/queue/ruleengine/TbRuleEngineQueueConsumerManager.java`
- `application/.../service/queue/processing/*`
- `application/.../service/script/RuleNode*ScriptEngine.java`
- `rule-engine/rule-engine-components/...`
- `dao/.../BaseEventService.java`
- `application/.../controller/EventController.java`

固定源码中 D05 有 5 个控制器、1 个 UI 页面域、7 个唯一业务表、7 个应用服务包和 77 个 `@RuleNode`。官方当前 [Rule Engine](https://thingsboard.io/docs/user-guide/rule-engine/)、[Rule Engine Queues](https://thingsboard.io/docs/user-guide/rule-engine/queues/) 与 [Delay](https://thingsboard.io/docs/reference/rule-engine/nodes/action/delay/) 文档只用于解释产品入口；当文档与固定源码不一致时以固定源码为准。

### 3.2 HVAC Web 本地源码

本地通过 CodeGraph 先行审查，主要证据为：

- `libs/alarmmodel/rule.go`
- `modules/alarm/pkg/alarmservice/store.go`
- `modules/alarm/pkg/alarmservice/postgres.go`
- `modules/metric/internal/metric/engine.go`
- `modules/metric/internal/metric/postgres.go`
- `modules/metric/internal/metric/scheduler.go`
- `modules/scheduler/internal/scheduler/store.go`
- `libs/domainoutbox/store.go`
- `modules/command/pkg/commanddispatcher/dynamic_safety.go`
- `services/operations-agent-service/src/runtime-langgraph/internal/langgraph-agent-execution-runtime.ts`
- `libs/edgecontrol/*`
- `docs/architecture/thingsboard-pattern-adoption.md`

## 4. ThingsBoard 功能与它解决的问题

### 4.1 Rule Chain 与 Component Catalog

Rule Chain 把消息处理表达为可视图：一个 First Node、多个 Rule Node、按 Relation Type 命名的有向连接，以及可调用的其他 Rule Chain。Component Descriptor 从注解发现 Node 名称、类型、配置类、版本、关系类型、队列和 UI 元数据。

它解决的是通用 IoT 系统中“不同租户如何组合过滤、补充、转换和动作，而不为每条自动化重新部署服务”。

### 4.2 Message 与 Actor 路由

`TbMsg` 携带 ID、时间、类型、Originator、Customer、Metadata、Data、Rule Chain/Node、Correlation、Partition、处理栈和 Callback。Rule Chain Actor 按 Relation Type 找目标；单目标直发，多目标创建 Callback Wrapper 并扇出；目标可以是 Rule Node 或 Rule Chain。

它解决的是异步图执行、节点隔离、扇出完成汇聚和跨分区路由。

### 4.3 Queue 与执行策略

Rule Engine Queue 将基础 Kafka/In-memory Queue 之上再加两组策略：

- Submit：`BURST / BATCH / SEQUENTIAL_BY_ORIGINATOR / SEQUENTIAL_BY_TENANT / SEQUENTIAL`；
- Processing：`SKIP_ALL_FAILURES / SKIP_ALL_FAILURES_AND_TIMED_OUT / RETRY_ALL / RETRY_FAILED / RETRY_TIMED_OUT / RETRY_FAILED_AND_TIMED_OUT`。

它解决的是负载尖峰、顺序、批次等待、失败/超时重处理和队列偏移提交。

### 4.4 State、Delay、Dedup、Checkpoint 与子链

Node State 允许按 Rule Node + Entity 保存字符串状态。Processing Stack 支持调用子链并由 Output Node 返回调用者。Checkpoint 把消息成功放入另一队列后 ACK 当前消息。Delay、Dedup 和 Generator 使用 Actor Scheduler/self-message。

它解决的是状态化处理、子流程复用、跨队列故障域、延迟和聚合窗口。

### 4.5 Debug、统计和脚本

Node/Chain Debug Event 保存输入、输出、Relation、错误和消息字段；全量 Debug 有时限，失败 Debug 可持续，另有 Tenant Rate Limit、字段截断和分页/清理 API。JS/TBEL Script 支持 Filter、Switch、Transform 和 JSON 结果。

它解决的是租户自助排错和低代码表达扩展。

## 5. ThingsBoard Domain 模型

```text
Tenant
  ├─ RuleChain(root?, type, firstRuleNodeId, version, debugSettings)
  │    ├─ CONTAINS -> RuleNode(type class, configuration, queueName, debugSettings)
  │    ├─ RULE_NODE Relation(type string) -> RuleNode
  │    └─ USES -> RuleChain
  ├─ Queue(submitStrategy, processingStrategy, poll/timeout settings)
  └─ DebugEvent / QueueStats / Usage

TbMsg
  ├─ originator + type + data + metadata
  ├─ queue + partition + callback
  ├─ ruleChainId + ruleNodeId
  └─ mutable processing counter + nested stack

RuleNodeState
  └─ ruleNodeId + entityId + stateData
```

该模型覆盖面广，但有四个目标架构不能继承的结构问题：

1. Graph Edge 复用通用 `EntityRelation`，Relation Type 是字符串，不是 Node Port Schema；
2. Rule Node 类型是 Java 类名，配置验证依赖反射；
3. `TbContext` 是跨域 Service Locator；
4. Rule Chain 的 Version 是可变实体乐观锁，不是不可变执行发布物。

## 6. 核心流程与源码行为

### 6.1 保存、导入、发布与根链

`RuleChainDataValidator` 保证名称、Tenant、唯一 Root，并对同一链内连接执行无环检查。`BaseRuleChainService.saveRuleChainMetaData` 在事务内检查可选 Version、删除旧 Relation、保存/删除 Node、重建 Relation、更新 First Node 并发布 Entity Save Event。Import 会映射 ID 后调用同一保存流程。

值得吸收：

- 图保存的整体事务；
- 乐观冲突；
- First Node、Root/Profile Binding、Import/Export；
- Node 配置版本升级入口。

不能照搬：

- Version 可为空，不能作为强制 Expected Revision；
- 保存直接改运行实体，没有 Draft/Release/Activation 分离；
- 同链无环校验不覆盖跨 Rule Chain 静态依赖；跨链环主要到运行时才由 Stack 阻止；
- `validateRuleNode` 返回 `Throwable`，底层保存调用未使用该返回值作为硬失败，不能把配置反射验证当成可靠发布门；
- Import 可覆盖现有链，不提供不可变 Digest、签名、依赖锁定或 Dry-run Activation。

### 6.2 Node 初始化与执行

`RuleNodeActorMessageProcessor` 用 `Class.forName` 和反射构造 Node，调用 `init`，配置变化时 Destroy/Restart。每次消息进入 Node 都增加处理计数，并按 Tenant Profile 的 `maxRuleNodeExecutionsPerMessage` 终止；默认示例为 50，但 0 表示无限。Node 异常转到 Failure Relation。

值得吸收的是明确 Node Lifecycle、统一 Failure 路由和每消息执行预算。需要替换的是反射、无限值、无每节点 CPU/内存/I/O 预算以及 Giant Context。

### 6.3 路由、扇出与嵌套链

Relation Type 使用不区分大小写的字符串匹配。一个分支直发；多个分支为每个消息复制随机 ID，并由多 Callback Wrapper 汇聚。跨分区路由先序列化到 Rule Engine Queue。嵌套链把调用方 Rule Chain/Node 压入消息 Stack，Output 弹栈返回；同一 Input Node 再次出现在 Stack 时运行时失败。

目标需要保留分支汇聚和子图返回语义，但改为：

- 类型化输入/输出端口；
- 发布时检查可达性、端口兼容、跨子图依赖环和 Fan-out 上限；
- 派生 Work Item ID 由 Execution + Path + Node + Input Digest 确定；
- 子图引用固定 Released Revision，不在运行时解析“当前版本”；
- Stale/Missing Target 是失败或 Quarantine，不得静默成功。

### 6.4 Queue、ACK、Retry 与顺序

Consumer Poll 一个 Pack，Submit Strategy 推给 Actor，Callback 把消息分类为 Success、Failed、Pending/Timeout；Processing Strategy 决定提交偏移或重处理 Map。Retry Pause 采用线程睡眠并倍增到上限，没有 Jitter；`maxRetries=0` 可无限。重试耗尽、失败比例超过阈值或 Skip 策略会提交 Pack。`RETRY_ALL` 会包含已成功消息；超时取消不能中断已经开始的 Node Side Effect。

顺序策略只约束提交：Burst/Batch 不能保证同 Originator 的异步完成顺序；Sequential-by-Originator 在上一条 Callback 成功后才提交该 Entity 的下一条，同时允许不同 Entity 并行。

目标裁决：

- `ADOPT` 按业务 Ordering Key 串行、跨 Key 并行；
- `ADAPT` Pack/Callback 统计；
- `REPLACE` 无限重试、线程 Sleep、整批成功重放、失败比例丢弃和“超时等于已停止”；
- ACK 只发生在 Execution 已耐久终止，或 Continuation/Effect Intent 已原子落库之后；
- Side Effect 必须由稳定 Effect ID 和 Owner Receipt 防重，不能依赖 Callback 恰好一次。

### 6.5 State、调度和特殊 Flow Node

`RuleNodeState` 只有 RuleNode、Entity 和字符串数据，Context 提供查询、保存、清除，没有 Expected Revision、CAS、TTL、状态 Schema 或 Rule Revision 隔离。

`DefaultTbContext.tellSelf` 源码明确标有“add persistence layer”待办。Delay 先 ACK 再把原消息留在进程内 Map，重启即丢；Dedup 同样先 ACK，在进程内按 Originator 聚合并用 Scheduler 唤醒，输出入队失败达到次数后丢弃。旧 Synchronization Begin/End 已弃用，官方推荐 Queue 的 Sequential-by-Originator。Checkpoint 则是正确的“目标 Queue 入队成功后才 ACK 当前消息”。

目标裁决：

- `ADOPT` Checkpoint 的可靠交接原则；
- `ADOPT` 顺序策略替代同步 Begin/End；
- `REPLACE` Delay、Dedup、Window、Debounce、Retry Wakeup 为耐久 Job/Continuation；
- `REPLACE` Node State 为 `(tenant, site, packageRevision, nodeInstance, scopeKey, stateSchemaVersion, revision, expiresAt, value)` + CAS；
- 状态迁移和 Execution/Continuation 写入必须有同一事务或 Outbox 证明。

### 6.6 Debug 与 Replay

固定源码行为：

- Full Debug 默认 15 分钟，可由 Tenant Profile/System 设置；
- Full Debug 到期后可继续只保存 Failure；
- 默认 Tenant Rule Debug Rate Limit 配置为 50,000/小时；
- Debug 字段默认截断到 4,096 字符；
- Event API 支持分页过滤和 Clear；
- 固定生产源码搜索不到一等 Rule Replay 流程。

这些能力适合排错，不足以重放。Debug Event 会截断，未固定完整输入 Snapshot、Owner Projection Revision、Rule Package Digest、外部读取结果和副作用 Receipt。

目标必须区分：

- `Trace`：生产执行证据，默认记录结构化 Node Attempt、耗时、结果码和引用；敏感数据脱敏；
- `Debug Capture`：短时、授权、限速的额外输入/输出采样；
- `Simulation Replay`：固定事件、Rule Revision 和读取 Snapshot，所有 Effect 进入虚拟 Sink；
- `Operational Redelivery`：对 Dead/Quarantine 的受授权真实重投，生成新 Execution，保留 Parent/Reason，不冒充 Replay。

### 6.7 JavaScript 与 TBEL

ThingsBoard Script Engine 把 Data、Metadata 和 Message Type 交给异步 JS/TBEL 执行器，可返回布尔、关系集合或变换后的消息。它提供租户用量计数和独立执行服务，但表达式可改变消息和路由，且与任意外部/动作节点组合后可形成难以静态证明的业务路径。

HVAC 目标只允许：

- 数字、布尔、字符串、时间和受控集合运算；
- 显式输入变量和单位；
- 无网络、文件、数据库、Clock、Random、Secret 和反射；
- 编译时类型/单位检查；
- AST Node/Depth、输入大小、步骤和墙钟时间预算；
- 版本化表达式语法和确定性错误码。

## 7. 本地源码级反向审查

### 7.1 通用 Rule Runtime：`MISSING`

CodeGraph 和源码搜索没有发现生产级 `RuleChain / RuleRevision / NodeCatalog / RuleExecution / RuleTrace / RuleReplay` 模型、迁移、API 或 UI。唯一名为 `RuleNode` 的生产模型是 Alarm 的单阈值节点。Operations Stream 中的 Replay、HTTP Idempotency Replay 和 MQTT Store-and-forward Replay 都不是 Rule Replay。

### 7.2 Alarm Rule：`FOUNDATION_ONLY`

`libs/alarmmodel/rule.go` 的优点：

- Schema Version、Rule ID/Revision；
- `MATCHED / NOT_MATCHED / INDETERMINATE`；
- 非 GOOD Quality 不产生可执行 Alarm Publication；
- Evidence 引用 Point Business Revision；
- Alarm Publication 由内部 Store 幂等合并。

缺口：

- 只有 `SIMPLE_THRESHOLD`；
- 没有持久 Rule Definition/Binding/Release；
- 没有消费 `alarm-rule` Delivery 的生产 Worker；
- Alarm/Event ID 仍由调用方传入；
- 没有 Duration、Hysteresis、Debounce、Schedule、Clear、Repeat、Suppression、Escalation、Node State 和 Trace。

该实现保留为 Alarm Domain Adapter/Node 语义参考，不升级为通用 Graph Runtime。

### 7.3 Metric Engine：`KEEP_DOMAIN_OWNER, SHARE_PURE_RUNTIME`

Metric Engine 已有 Released Binding/Version、依赖、质量/完整度、Run、Cross-store Publication 和 Reconciliation。表达式用 Go Parser，只接受数值 Literal、Identifier、括号、一元正负和 `+ - * /`，拒绝函数、选择器和其他 AST，除零失败。

方向正确但不能宣称通用 Rule Language：

- 只有数值计算；
- 没有显式 AST Depth/Node/Step Budget；
- 单次 Run 按 Binding 顺序读取依赖，Metric DAG 发布/调度仍由 Metric Domain 负责；
- D04 已确认 Result Revision 固定为 1 等独立缺口。

目标可提取同一受限 Expression Compiler/VM 供 Metric 与 Rule 使用，但 Metric Definition、Dependency、Result 和 Reconciliation 继续由 D04 拥有。

### 7.4 Domain Outbox：`KEEP + COMPLETE`

`libs/domainoutbox` 已实现按 Consumer 的 Delivery Row、`FOR UPDATE SKIP LOCKED` Lease、Aggregate Version Inbox 和 Owner-checked Complete。这比随机派生消息和 Callback-only 防重更适合作为 Rule Ingress。

但当前 `Retry` 只把状态改回 `FAILED` 并设置下次时间；`Claim` 会继续选择 `FAILED`，没有 Max Attempts、Failure Class、Dead、Quarantine 或 Operator Disposition。目标必须补齐有限重试和终态，不能把现状视为完整 Queue。

### 7.5 Scheduler：`KEEP + HARDEN`

本地 Scheduler 已有：

- PostgreSQL Schedule Definition 和 Job Instance；
- Misfire Policy、Catch-up Limit、Concurrency `FORBID`；
- `PENDING / READY / CLAIMED / RUNNING / RETRY_WAIT / FAILED / DEAD / CANCELLED`；
- Lease 过期恢复、Attempt、Retry Delay、Max Attempts；
- `FOR UPDATE SKIP LOCKED`；
- Catch-up 安全上限和 IANA Timezone。

该方向明显优于 ThingsBoard 内存 Delay。仍需补齐：

- CodeGraph 未找到主 `Store.Cycle` 的直接覆盖测试；
- 非 Metric Job 的 Lease 过期默认进入需要 Reconciliation 的失败，Owner 合同要逐类明确；
- Rule Continuation 的幂等键、State/Execution 原子边界和 Fencing Token 尚未定义；
- 大量积压、公平性、时钟跳变和最长停机恢复需要容量证据。

### 7.6 Command Safety 与 Edge Runtime：`KEEP OUTSIDE RULE AUTHORITY`

Command Dispatcher 在下发前读取权威 Reported State，拒绝 Unavailable、Offline、Not Current、Stale、非 GOOD Quality 和缺失时间；Command/Edge 还有 Capability、Lease、Fence、Expiry、Arbiter 和 Readback。Rule Engine 不得用一个 Generic RPC Node 绕过这些检查。

Edge 可执行签名 Rule Package 的纯节点、状态节点和 Domain Intent 节点子集；最终控制仍由 Edge Arbiter/Controller 和设备/PLC 安全层决定。

### 7.7 Operations Agent：`KEEP SEPARATE`

Operations Agent 有 LangGraph Checkpoint、Revision、Lease、预算、确定性 Tool 和副作用 Receipt，提供了可恢复工作流的好证据。但它是调查/辅助决策运行时，允许模型参与受限文字合成，延迟和成本模型也不同。不得为了复用而把它变成高频 Rule Engine 或控制运行时。

### 7.8 Frontend：`MISSING`

本地没有 Rule Package 列表、Graph Editor、Node Catalog、Server Validation、Simulation Fixture、Release/Binding、Execution Trace、Dead/Quarantine 或 Replay 页面。未来 UI 必须建立在服务端编译/授权之上，不能在浏览器执行生产规则。

## 8. 十二项能力裁决矩阵

| # | 能力 | ThingsBoard 固定行为 | HVAC Web 当前 | 裁决 | 目标行为 |
| --- | --- | --- | --- | --- | --- |
| 1 | Rule Chain/Node 生命周期、导入导出 | 原位 CRUD、事务保存、可选 Version、Import/Export | 无通用模型 | `ADAPT` | Draft + immutable Released Revision + Digest + Dry-run Import + 显式 Activation/Rollback |
| 2 | Root/Profile Binding/Descriptor | Root Chain、Profile 引用、反射 Descriptor | 无；D02 有不可变 Template 方向 | `REPLACE` | 事件选择器 + 有效期 Rule Binding + 闭集 Node Catalog；Profile 只引用 Released ID |
| 3 | Message Envelope | 丰富可序列化 `TbMsg`，但 Metadata/Context 可变 | Domain Event 有 Scope/Version/Trace，形状分散 | `ADAPT` | immutable `RuleEventEnvelope`，固定 Scope/Event/Schema/Source Position/Trace/Revision；无共享可变 Map |
| 4 | Routing/Fan-out/Nested Chain | 字符串 Relation、多分支 Callback、Stack 防环 | 无通用图 | `ADAPT` | typed ports、稳定分支 ID、静态跨子图去环、Fan-out/Depth 上限、Pinned Subgraph Revision |
| 5 | State/Stack/Callback/Failure | Entity State 字符串、Stack、Callback Success/Failure | Scheduler/Agent 有较强状态；Rule 无 | `REPLACE` | Versioned CAS State、结构化 Outcome、Lease Fence、耐久 Continuation、显式 Failure Class |
| 6 | Queue/Order/Retry/Timeout/Concurrency | 5 Submit + 6 Processing 策略、Pack Commit | Outbox Lease + Scheduler；重试终态不一致 | `ADAPT` | Per Ordering Key 串行、跨 Key 并行、有限指数退避+Jitter、Dead/Quarantine、Owner Receipt |
| 7 | Tenant 执行限制与隔离 | Tenant Profile Usage、Node Count；0 可无限 | RLS/Scope 强，Rule Budget 缺失 | `ADOPT_AND_STRENGTHEN` | 所有预算必须正且有限：节点数、深度、扇出、Payload、状态、CPU、Wall Clock、队列年龄、并发 |
| 8 | Debug/Stats/Replay | 时限 Debug、失败 Debug、Rate Limit、截断、Stats；无一等 Replay | OTel/Audit/Agent Replay 分散；Rule 无 | `ADAPT` | Trace + Debug Capture + no-effect Simulation Replay + 独立受权 Redelivery |
| 9 | Delay/Dedup/Checkpoint/Ack/Transaction | Delay/Dedup 内存先 ACK；Checkpoint 可靠交接；同步节点弃用 | Durable Scheduler 更强 | `REPLACE_EXCEPT_CHECKPOINT` | Durable Timer/Window/Dedup/Continuation；Checkpoint 原则保留；无静默 Drop |
| 10 | Filter/Switch/Metadata/Transform/Math/Geo/Data/Action | 77 个节点，组合灵活 | Alarm/Metric/Command 各自实现 | `SELECTIVE_ADAPT` | 纯节点进 Core；读取走窄 Owner Port；动作只产 Domain Intent |
| 11 | JavaScript/TBEL | 异步脚本 Filter/Switch/Transform + Tenant Usage | Metric 有受限算术表达式 | `REJECT_ARBITRARY_SCRIPT` | 共享 typed deterministic expression VM，禁止 I/O/Secret/Clock/Random/副作用 |
| 12 | Alarm/Notification/RPC/Edge/External | Rule Node 直接调用各域和 Provider | 本地 Owner 边界更强但接线不全 | `REPLACE` | Intent -> Owner Validation/Commit -> Receipt；外部 Adapter 由 D09，控制由 D03/D08 |

十二项均已单独裁决，没有以“已有模块”为默认优先项。

## 9. 77 个 ThingsBoard Node 的完整映射

### 9.1 Action（11）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbAssignToCustomerNode`, `TbUnassignFromCustomerNode` | `REJECT` | 本项目没有 Customer 作为授权代理；Assignment 归 D01/D02 |
| `TbCreateAlarmNode`, `TbClearAlarmNode` | `ADAPT` | 产生 Alarm Condition/Publication Intent；D06 决定生命周期 |
| `TbCopyAttributesToEntityViewNode` | `REJECT` | 不复制业务真值，使用授权 Read Model |
| `TbCreateRelationNode`, `TbDeleteRelationNode` | `REJECT` | 类型化 Binding/Topology 由 D02/D04 Owner 维护 |
| `TbDeviceStateNode` | `ADAPT` | Presence/Readiness 作为只读规则输入，不由节点改写 |
| `TbLogNode` | `REJECT_PRODUCTION` | Trace 是结构化平台能力；测试环境可提供 Fixture Sink |
| `TbMsgCountNode` | `ADAPT` | 运行统计进入 D10 Metrics，不作为业务动作 |
| `TbSaveToCustomCassandraTableNode` | `REJECT` | 禁止规则内任意数据库写入 |

### 9.2 AI 与外部云（4）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbAiNode` | `REJECT_DIRECT / DEFER_INTENT` | D09 可提供 Evidence-bound AI Enrichment Intent；输出不能控制 Outcome |
| `TbAwsLambdaNode`, `TbSnsNode`, `TbSqsNode` | `DEFER` | 只有真实产品需求成立后通过 D09 Delivery Adapter；不进入 Core Node |

### 9.3 Debug、Dedup、Delay 与 Edge（5）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbMsgGeneratorNode` | `ADAPT_TEST_ONLY` | 只用于 Simulation Fixture，不允许生产无限生成 |
| `TbMsgDeduplicationNode` | `REPLACE` | Durable Window + stable key + persisted result + bounded continuation |
| `TbMsgDelayNode` | `REPLACE` | Durable Timer/Job；拒绝 ACK 后内存保留 |
| `TbMsgPushToCloudNode`, `TbMsgPushToEdgeNode` | `ADAPT` | D08 Durable Delivery/Assignment/Release Intent；沿用“先存 Event 再传输”模式 |

### 9.4 Filter/Switch（11）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbMsgTypeFilterNode`, `TbMsgTypeSwitchNode`, `TbOriginatorTypeFilterNode`, `TbOriginatorTypeSwitchNode` | `ADOPT` | 改为 Schema/Subject Type 的 typed port |
| `TbAssetTypeSwitchNode`, `TbDeviceTypeSwitchNode` | `ADAPT` | 使用 D02 Template/Capability Revision，不使用自由字符串 Type |
| `TbCheckMessageNode` | `ADAPT` | 受限 Predicate/Schema Validate Node |
| `TbCheckAlarmStatusNode` | `ADAPT` | 只读 D06 Alarm Snapshot，不写 Lifecycle |
| `TbCheckRelationNode` | `ADAPT` | 查询类型化 Binding/Topology Projection，禁止无限图遍历 |
| `TbJsFilterNode`, `TbJsSwitchNode` | `REJECT` | 替换为 typed deterministic expression/predicate |

### 9.5 Flow（4）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbAckNode` | `ADAPT` | 改为显式 Terminal `Complete/Discard`；Discard 必须有原因和策略 |
| `TbCheckpointNode` | `ADOPT` | 目标 Continuation/Queue 耐久提交后 ACK 来源 |
| `TbRuleChainInputNode`, `TbRuleChainOutputNode` | `ADAPT` | Pinned Subgraph Call/Return；发布时检查跨子图环和端口 |

### 9.6 GCP、Geo、Kafka 与 Mail（6）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbPubSubNode`, `TbKafkaNode` | `DEFER` | D09 Adapter + Delivery Ledger；不直连 Broker |
| `TbGpsGeofencingActionNode`, `TbGpsGeofencingFilterNode` | `DEFER / ADAPT_PURE` | 有 Site 地理围栏需求时只引入纯 Geometry Predicate；动作仍为 Intent |
| `TbMsgToEmailNode` | `ADAPT` | 变为 Notification Template Input，不生成可含 Secret 的任意邮件对象 |
| `TbSendEmailNode` | `REPLACE` | D06 Request + D09 Delivery Attempt/Receipt |

### 9.7 Math 与 Metadata（12）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbMathNode` | `ADOPT_WITH_LIMITS` | 共享 typed/unit-aware expression VM |
| `CalculateDeltaNode` | `ADAPT` | 服从 Point Revision、Unit 和 Counter Reset/Rollover Policy |
| `TbFetchDeviceCredentialsNode` | `REJECT` | Rule 永远不能读取 Device Credential Material |
| `TbGetAttributesNode`, `TbGetCustomerAttributeNode`, `TbGetCustomerDetailsNode`, `TbGetDeviceAttrNode`, `TbGetOriginatorFieldsNode`, `TbGetRelatedAttributeNode`, `TbGetTelemetryNode`, `TbGetTenantAttributeNode`, `TbGetTenantDetailsNode` | `ADAPT` | 收敛为少量 typed Snapshot Read Node；精确 Scope/Key/Revision/大小限制，无 Customer 泛化 |

### 9.8 MQTT、Notification、Profile、RabbitMQ、REST、RPC、SMS（12）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbAzureIotHubNode`, `TbMqttNode`, `TbRabbitMqNode` | `DEFER` | D09/IoT Adapter，必须有 Destination Policy、CredentialRef、Receipt 和 SSRF/网络策略 |
| `TbNotificationNode`, `TbSlackNode`, `TbSendSmsNode` | `REPLACE` | D06 Notification Request -> D09 Delivery Ledger/Channel Adapter |
| `TbDeviceProfileNode` | `ADAPT` | Template/Capability Revision Predicate 或 Binding Selector |
| `TbRestApiCallNode`, `TbSendRestApiCallReplyNode` | `REPLACE` | Outbound HTTP Intent；无 Rule Node 内直接 URL/Secret/网络调用 |
| `TbSendRPCRequestNode`, `TbSendRPCReplyNode` | `REPLACE` | D03 Command/RPC API，保留 Approval/Lease/Fence/Expiry/Readback |

### 9.9 Telemetry 与 Transaction（6）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbCalculatedFieldsNode` | `REJECT_DUPLICATE_OWNER` | Metric 由 D04 Metric Engine 拥有；Rule 可触发 Job/消费 Result |
| `TbMsgAttributesNode`, `TbMsgDeleteAttributesNode`, `TbMsgTimeseriesNode` | `REPLACE` | 产生受 Schema 约束的 D04 Intent；通常规则不应回写原始 Telemetry |
| `TbSynchronizationBeginNode`, `TbSynchronizationEndNode` | `REJECT` | 上游已弃用；使用 Ordering Key/Queue Policy |

### 9.10 Transform（7）

| 节点 | 裁决 | 映射 |
| --- | --- | --- |
| `TbCopyKeysNode`, `TbDeleteKeysNode`, `TbRenameKeysNode`, `TbJsonPathNode`, `TbSplitArrayMsgNode` | `ADOPT_WITH_SCHEMA` | 纯变换、输出 Schema、Payload/Fan-out 上限 |
| `TbChangeOriginatorNode` | `ADAPT` | 不可改写安全 Scope；只能产生显式 Subject Projection，保留原 Subject/Causation |
| `TbTransformMsgNode` | `REJECT_SCRIPT / ADAPT_TYPED` | 用 declarative mapping/typed expression 替换 JS/TBEL |

上述分组共 77 个节点：11 + 4 + 5 + 11 + 4 + 6 + 12 + 12 + 6 + 7 = 77。没有因节点不适合 HVAC 就从覆盖范围中删除。

## 10. 目标 Rule Runtime 设计

### 10.1 Domain 模型

```text
NodeDefinition
  id, version, inputPorts, outputPorts, configSchema,
  permissions, determinism, stateSchema, placement, resourceClass

RuleDefinition
  identity, tenant, purpose, ownerDomain
  └─ RuleRevision(DRAFT -> VALIDATED -> RELEASED -> RETIRED)
       graph, catalogVersion, dependencyRevisions, digest, createdBy, releasedBy

RuleBinding
  tenant, site?, subjectSelector, eventSelector,
  ruleRevisionId, effectiveFrom/To, priority, placement, revision

RuleEventEnvelope
  eventId, schema, tenant/site, subject, aggregateVersion,
  occurredAt/receivedAt, sourcePosition, trace/causation,
  payloadDigest, immutable payload/metadata

RuleExecution
  executionId, ruleRevisionId, bindingRevision,
  eventId, orderingKey, status, budget, leaseFence, started/completedAt
  ├─ NodeAttempt
  ├─ WorkItem
  ├─ StateTransition
  ├─ Continuation/Timer
  ├─ EffectIntentReference
  └─ TraceReference

RuleState
  packageRevision + nodeInstance + scopeKey + schemaVersion + revision + expiresAt + value
```

### 10.2 发布编译器

Release 前服务端必须完成：

1. Node Definition 和配置 Schema 精确匹配；
2. 唯一 Entry、所有 Node 可达、Terminal 明确；
3. Input/Output Port 类型兼容；
4. 同图与跨 Subgraph 全局去环；只有显式、有限的 Window/Iteration Node 可形成受控循环；
5. Depth、Fan-out、Node Count、Payload Growth 和状态预算静态上限；
6. Side Effect Permission 与 Owner Domain 匹配；
7. Cloud/Edge Placement 合法；
8. Secret 只能是 CredentialRef，Export 不包含 Secret；
9. Test Fixture 和 Simulation Gate 通过；
10. 生成 Canonical Digest 和不可变 Execution Plan。

不支持旧 Schema 时直接拒绝，不增加兼容层、运行时迁移或双读。升级由显式离线转换产生新 Draft Revision。

### 10.3 执行与 ACK

```text
Domain Outbox Delivery
  -> resolve exact RuleBinding revision
  -> create/idempotently load RuleExecution
  -> lease ordering key
  -> execute ready pure nodes
  -> atomically persist Node Attempts + State CAS + Continuation/Effect Intents
  -> terminal success / typed failure / dead / quarantine
  -> complete source Delivery only after durable boundary
```

稳定身份建议：

- `executionId = H(ruleRevisionId, bindingRevision, sourceEventId)`；
- `workItemId = H(executionId, graphPath, nodeInstanceId, inputDigest)`；
- `effectId = H(workItemId, outputPort, effectOrdinal, payloadDigest)`；
- Retry 保持同一 Work/Effect ID；人工 Redelivery 创建新 Execution，并引用 Parent。

### 10.4 Queue 与失败分类

默认按 `tenant/site/aggregateType/aggregateId` 排序；没有 Aggregate 的事件必须由 Source Contract 给出稳定 Ordering Key。不同 Key 并行，同 Key 一个有效 Lease/Fence。

| Failure Class | 处理 |
| --- | --- |
| `VALIDATION / POLICY / SAFETY_DENIED` | 不重试，记录明确 Outcome；必要时 Quarantine |
| `TRANSIENT_INFRASTRUCTURE` | 有限指数退避 + Jitter + Queue Age 上限 |
| `AMBIGUOUS_EFFECT` | 不重复发动作；查询 Owner Receipt/Reconcile |
| `TIMEOUT` | Fence 旧 Worker；只有无副作用纯节点可安全重跑 |
| `POISON_EVENT / SCHEMA_UNKNOWN` | Quarantine，不能推进为成功 |
| `BUDGET_EXHAUSTED` | Dead，保留最后 Node/Path/Budget Evidence |

所有队列必须配置正数 Max Attempts；禁止 `0 = unlimited`。Dead/Quarantine 必须可查询、可授权处置、可导出证据，但不自动无限重放。

### 10.5 Node Contract

```text
Node.evaluate(NodeContext, TypedInput) -> NodeOutcome

NodeContext:
  exact tenant/site/subject scope
  immutable execution/rule/binding identity
  declared read ports only
  state CAS handle when stateful
  deterministic clock from envelope/evaluation context
  resource budget and cancellation token

NodeOutcome:
  typed outputs[]
  stateTransition?
  effectIntents[]
  continuation?
  diagnostics[]
```

Node 不能直接 ACK Queue、提交数据库、发网络请求或调用跨域 Service。Runtime 根据持久化 Outcome 决定 ACK。

### 10.6 Trace、Debug 与 Replay

每次 Execution 至少保存：Rule/Binding/Catalog Revision、输入 Event 引用和 Digest、每个 Node 的 Attempt/Port/Outcome/耗时/预算、State Revision、Effect ID/Owner Receipt、终止原因。Payload 默认只保存 HMAC/Blob Reference 和允许字段；PII、Credential、Command Secret 永不进入 Trace。

Simulation Replay 读取已冻结 Event/Projection Snapshot，在虚拟 Clock 和 Effect Sink 中执行相同 Plan，输出 Trace Diff。它不得写 Telemetry、Alarm、Command、Notification、Registry 或外部系统。

### 10.7 Cloud 与 Edge

Cloud Package 可以包含纯节点、Owner Snapshot Reads、Intent Nodes、Durable Timer 和受限表达式。Edge Package 进一步限制为：

- 签名、完整 Digest、精确 Catalog Version；
- 纯 Filter/Transform/Math、受控状态、Local Observation、Local Intent；
- 无任意脚本、AI、外部 HTTP/Broker、Credential Read 或 Registry Mutation；
- 所有控制 Intent 仍经过 Edge Capability、Interlock、Priority、Lease、Arbiter 和 Readback；
- 激活发生在安全 Cycle Boundary，失败回滚上一签名 Revision。

## 11. 值得吸收的 Pattern

1. Rule Chain 把路由与节点职责分开；
2. Node init/onMsg/destroy 生命周期；
3. Component Descriptor + 配置版本；
4. First Node、显式 Relation/Port 和子链 Call/Return；
5. 多分支完成汇聚；
6. 每消息最大节点执行次数；
7. Sequential-by-Originator 的并行/顺序平衡；
8. Checkpoint 的先入队后 ACK；
9. 全量 Debug 自动到期、失败 Debug 常开；
10. Debug Rate Limit、字段长度和 Queue Stats；
11. Import/Export 和可视编辑器的产品体验；
12. Push-to-Edge 先持久化 Event 再交给传输的边界。

## 12. 不适合本项目的部分

- 可变 Rule Chain 直接作为运行版本；
- 通用 Entity Relation 充当执行图；
- Java 类名 + 反射作为插件信任边界；
- `TbContext` 暴露整个平台服务；
- 可共享修改的 Metadata/Processing Context；
- 任意 JS/TBEL 与运行时任意 Message Type；
- Node 直接写 Telemetry、Alarm、Credential、Cassandra、RPC、Email、SMS、Slack、MQTT、Kafka、RabbitMQ、REST、云服务或 AI；
- 随机派生消息 ID；
- 0 表示无限 Node Execution/Retry；
- `RETRY_ALL` 重放已成功副作用；
- 失败比例或 Skip 策略静默提交丢弃；
- Delay/Dedup 先 ACK 后留内存；
- State 无 Revision/CAS/TTL/Schema；
- Timeout 后假设已开始动作被取消；
- Debug Event 被称作 Replay；
- In-memory Queue 用于生产；
- 把 Rule Engine Root/Profile 设置当作授权或业务真值。

## 13. 实施路线图

### P0：最小完整纵切

1. 定义 `RuleDefinition / RuleRevision / RuleBinding / NodeDefinition / RuleExecution` 机器契约和 PostgreSQL 迁移；
2. 建立闭集 Node Catalog：Event Type Filter、Quality/Freshness Filter、Compare、Switch、Copy/Rename/Delete/JSONPath、Math Expression、Subgraph、Terminal、Alarm Intent；
3. 实现发布编译器和不可变 Revision；
4. 用现有 Domain Outbox 接入 `alarm-rule`，补齐 Max Attempts/Dead/Quarantine；
5. 用稳定 ID 执行一个 Telemetry/Metric Event -> Alarm Intent -> D06 Publication 的纵切；
6. 实现 Execution/Node Attempt/Trace API；
7. 实现无副作用 Simulation Replay；
8. Rule UI 先做 List、Draft Form/Graph、Server Validation、Simulation、Release、Binding、Trace，不做任意插件市场。

### P1：状态、调度与领域动作

1. Versioned Rule State + CAS + TTL；
2. Durable Timer、Duration、Debounce、Hysteresis、Window、Dedup；
3. Pinned Subgraph、跨图静态去环；
4. Notification、Metric Trigger、Work Order 和 Command Intent Adapter；
5. Owner Receipt/Reconciliation；
6. Dead/Quarantine 管理、授权 Redelivery 和审计；
7. Tenant/Site Budget、Queue Fairness 和容量门。

### P2：Edge 与选择性扩展

1. 签名 Edge Rule Package 和受限 Node Catalog；
2. 安全 Cycle Boundary 激活/回滚；
3. Geo、更多 typed enrichment 和领域 FDD Node；
4. 只有现场需求与 Delivery Gate 成立后，接入 D09 外部 Adapter；
5. AI 只允许 Evidence-bound、非控制、非权威 Enrichment。

不以“兼容 ThingsBoard Rule Chain JSON”为目标。若需要导入，只做一次性、可审计、Dry-run 的转换工具，生成新的本地 Draft；不保留运行时兼容层。

## 14. 验收门槛

### Graph Release Gate

- 所有端口、Schema、Node Version、Subgraph Revision、Scope 和 Permission 校验；
- 同图/跨图环、不可达 Node、无 Terminal、Fan-out/Depth/Payload 超限均拒绝；
- Released Revision 不可变，Rollback 只切 Binding。

### Delivery Correctness Gate

- Source Delivery 仅在 Execution/Continuation/Effect Intent 耐久后完成；
- Crash-before/after 每个提交点均无丢失和重复业务效果；
- Retry/Redelivery 使用稳定身份；
- Dead/Quarantine 可见，不存在无限重试。

### State and Scheduling Gate

- State CAS 冲突、Lease 过期、双 Worker、时钟跳变、Misfire、长停机、Timer 重复均有测试；
- Delay/Dedup/Window 在进程重启后继续；
- Running Execution 固定 Rule Revision。

### Safety and Ownership Gate

- Rule Node 不能直接写 Owner 表或访问 Secret/网络；
- Command Intent 必经 D03/D08 的 Approval/Capability/Lease/Fence/Readback；
- 非 GOOD、Stale、Unavailable 和未知状态保持 Fail-closed/Indeterminate；
- Replay/Shadow 永不产生真实副作用。

### Observability and Replay Gate

- Trace 关联 Source Event、Rule/Binding Revision、Node Attempt、State Revision 和 Effect Receipt；
- Debug 有时限、限速、脱敏、大小和 Retention；
- 同输入/修订的 Simulation 结果确定；
- UI 不把 Rule Evaluation、Intent Accepted、Command Executed 和 Verified 混为一体。

### Capacity Gate

- 按 Ordering Key 的吞吐、P95/P99、积压年龄、State/Trace 增长、最大 Fan-out 和长停机恢复有测量；
- 在容量证据证明 PostgreSQL Queue/Lease 不足前，不因 ThingsBoard 使用 Kafka 就引入 Kafka；
- 若引入 Kafka，业务 ACK、稳定 ID、Inbox/Receipt 和 Dead/Quarantine 语义不得改变。

## 15. 差距过大项

下列差距不能靠小修补齐，实施前必须按新目标模型设计：

1. 通用 Rule Runtime、持久模型、API 和 UI 全部缺失；
2. Alarm Rule 只有一个节点且尚无生产 Consumer；
3. Domain Outbox 没有有限重试终态；
4. Rule Execution/Node Attempt/Trace/Replay 不存在；
5. 跨图发布、不可变 Revision、Binding Activation 和回滚不存在；
6. Rule State/Timer/Continuation 不存在；
7. Node Catalog/权限/Placement/预算不存在；
8. 当前 Scheduler 虽方向正确，但缺 Rule Continuation 接线和完整恢复/容量证据；
9. Edge Rule Package、签名、激活和回滚不存在。

这些差距意味着后续实现应先完成 P0 纵切，而不是继续给现有单阈值 Rule 增加分支并称其为完整引擎。

## 16. 最终裁决

- ThingsBoard 的 Rule Chain、Node Lifecycle、分支/子链、按 Originator 串行、Checkpoint、执行上限、Debug/Stats 获得 `ADOPT/ADAPT`。
- ThingsBoard 的可变 Chain、字符串 Relation、反射插件、Giant Context、随机派生 ID、无限 Retry/Execution、整批成功重放、内存 Delay/Dedup、无 CAS State 和 Debug-as-Replay 获得 `REPLACE/REJECT`。
- 77 个 Node 全部映射；纯 Filter/Transform/Math 选择性吸收，领域 Action 改为 Intent，任意脚本、Credential/DB 读写和直接外部副作用拒绝，缺乏产品证据的 Provider 延后。
- 本地 Alarm/Metric/Outbox/Scheduler/Command/Agent/Edge 的正确边界获得 `KEEP/ADAPT`，但没有任何一个被误判为完整 Rule Engine。
- 完整 Rule Engine 的目标已固定为不可变发布、类型化端口、耐久执行、稳定幂等、Owner Intent、有限预算、可追踪、可模拟回放和 Cloud/Edge 分级 Catalog。

本裁决完成 D05 的架构问题，不代表 P0/P1/P2 已实现，也不构成生产 Rule Engine 认证。

## 17. 本轮验证结果

| 验证项 | 结果 |
| --- | --- |
| 固定 ThingsBoard Tag/Commit | 完成 |
| Rule Chain/Node/Actor/Queue/Script/Debug 源码审查 | 完成 |
| 77 个 `@RuleNode` 全量分组映射 | 完成，77/77 |
| D05 十二项能力逐项裁决 | 完成，12/12 |
| 官方文档与固定源码冲突分离 | 完成 |
| CodeGraph 本地反向审查 | 完成 |
| Attached UX/Frontend 文档对齐 | 完成 |
| 运行时代码修改 | 未执行，本票不授权实施 |
| 完整 Rule Engine 生产能力 | `MISSING / ROADMAP_DEFINED` |
