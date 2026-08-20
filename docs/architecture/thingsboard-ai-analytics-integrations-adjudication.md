# ThingsBoard CE AI、分析、集成与自动化扩展裁决

> 状态：`D09_ADJUDICATION_COMPLETE`
>
> 审查票：[审查 AI、分析、集成与自动化扩展 #236](https://github.com/SwayingWindmill/HVAC_web/issues/236)
>
> 固定上游：`thingsboard/thingsboard` tag `v4.3.1.1`，commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`，Apache-2.0
>
> 本文只形成源码级裁决和目标设计，不把缺失能力描述成已经实现，也不改变 Real 运行流量。

## 1. 执行结论

本轮结论不是“ThingsBoard 更完整，所以全部照搬”，也不是“本项目安全设计更多，所以现状全部保留”。两边各有明显强项和缺陷。

1. **保留本地 Operations Agent 的安全内核。** 当前实现把模型限制为 Finding 的呈现层：权威 Owner 先产生 Evidence 和确定性分析，模型只能在固定分类、既有 Evidence ID 和严格 JSON Schema 内组织结论；超时、Provider 错误或非法输出回退到确定性结果。它还保留模型、配置摘要、提示词策略、Schema 版本、Token、延迟和 Provider Trace。这个边界明显强于 ThingsBoard AI Rule Node 把模型输出直接变成普通规则消息的做法。
2. **吸收 ThingsBoard 的 AI Model 管理面，但拒绝它的 Secret 行为。** Model CRUD、Provider 抽象、版本、连通性检查和按 ID 引用值得采用；把 API Key、云凭据和自定义 Endpoint 原样放进 `JSONB`、API 响应、导入导出或审计对象不适合生产。目标必须使用 `CredentialRef`、密钥管理、脱敏 DTO、受限 Endpoint Policy 和可撤销 Model Deployment。
3. **保留本地 Energy Analytics 的领域设计，但先修复回归门。** Real UI、Gateway、IAM、Query、Projection、Timezone、Quality、Watermark 和 Dataset Revision 已形成真实读取链；本轮 `analyticsmodel` 和 Analytics Projector 测试通过，但聚合测试命令被 Telemetry History 中已删除的 `ActingOrganizationID` 测试字段阻断，Gateway 套件也有相同的 Organization→Tenant 漂移和依赖下载问题。因此设计可 `KEEP`，当前整链认证不能写成 Green。Trendz 只是 ThingsBoard CE 中的外部商业产品钩子，不提供可替代的分析引擎；其 Customer User 可读取完整 API Key 的行为必须拒绝。
4. **不接受“Rule Node 直接产生外部副作用”作为目标架构。** ThingsBoard MQTT、Kafka、RabbitMQ、REST 和云节点提供了丰富连接器与明确 Success/Failure 分支，但缺少统一的业务级 Outbox、Delivery Ledger、Idempotency、Destination Policy、Secret Reference 和重放治理。Kafka 同步发送异常只记录日志、RabbitMQ 未启用 Publisher Confirm、REST 的 SSRF 防护默认关闭，说明它也不是可直接复制的可靠交付基线。
5. **建立独立的 Outbound Integration Domain。** 规则或业务域只能提交持久化 `DeliveryIntent`；隔离的 Egress Worker 依据版本化 `IntegrationDefinition` 和 `CredentialRef` 执行，记录 `DeliveryAttempt` 与 `DeliveryReceipt`，再发布成功、失败、未知、重试耗尽或 Dead Letter 结果。Inbound 现场接入、Outbound 业务集成和 Notification Channel 必须是三个不同边界。
6. **Forecast/Optimization 不能被当前文件数量掩盖。** 两个服务都有较好的输入快照、版本、PostgreSQL→ClickHouse 发布状态和 Reconcile Pattern，但没有进入 Phase 1 默认部署，Gateway 路由仍返回 `CONTRACT_NOT_ACTIVE`，Real 页面明确 `NOT_INTEGRATED`。Forecast 只实现 Site Load/PV Last-Value 回退；Optimization 默认是 ESS 的 24 小时 `NO_DISPATCH` Shadow 基线，不是 HVAC 求解器。保留发布模式，替换产品能力声明和领域实现。
7. **Scheduler 只完成了 Job Coordination。** 它能创建、去重、恢复 Lease、处理 Misfire/Retry/Concurrency，但 Forecast/Optimization 没有已接通的 Job Worker/Dispatcher。不能把“支持 `FORECAST_RUN`/`OPTIMIZATION_RUN` 字符串”当成自动化已经完成。
8. **按用例引入连接器，不建设连接器市场。** REST/Webhook 可作为第一个最小交付适配器；MQTT/Kafka/RabbitMQ、AWS、GCP、Azure、Slack/Teams/Email/SMS 都必须由明确业务用例、数据分类、目标允许列表、运行成本和运维责任触发。Platform Integrations 属 PE/外部实现，Trendz 属外部商业产品，MCP 属固定版本之后或外部能力，均不得从文档反推源码语义。

## 2. 固定证据基线

### 2.1 ThingsBoard 生产源码与测试

本轮直接审查以下主要源码面；官方文档只用于补充产品入口和当前版本边界，不替代源码证据。

- AI Model：`application/.../controller/AiModelController.java`、`application/.../service/ai/`、`common/data/.../ai/`、`dao/.../AiModelEntity.java`、`AiModelDataValidator.java`、`schema-entities.sql`、导入导出服务、`AiModelControllerTest.java`。
- AI Rule Node：`rule-engine-components/.../ai/TbAiNode.java`、`TbAiNodeConfiguration.java`、`TbAiNodeTest.java`。
- 外部节点公共边界：`TbAbstractExternalNode.java`。
- REST：`TbRestApiCallNode.java`、`TbHttpClient.java`、`SsrfSafeAddressResolverGroup.java`、对应测试和 `thingsboard.yml`。
- Broker：`TbMqttNode.java`、`TbKafkaNode.java`、`TbRabbitMqNode.java` 及对应测试。
- Cloud：`TbSqsNode.java`、`TbSnsNode.java`、`TbAwsLambdaNode.java`、`TbPubSubNode.java`、`TbAzureIotHubNode.java` 及对应测试。
- Notification Action：`TbSendEmailNode.java`、`TbSendSmsNode.java`、`TbSlackNode.java` 及配置类。
- Analytics/Geo/Math：`TrendzController.java`、`DefaultTrendzSettingsService.java`、`TrendzControllerTest.java`、`TbMathNode.java`、`CalculateDeltaNode.java`、`TbGetTelemetryNode.java`、`TbGpsGeofencingFilterNode.java`、`TbGpsGeofencingActionNode.java`。
- UI：`ui-ngx/.../ai-model/`、`core/http/ai-model.service.ts`、AI Rule Node 配置组件。

官方补充入口：[AI Models](https://thingsboard.io/docs/user-guide/ai-models/)、[AI Request Rule Node](https://thingsboard.io/docs/reference/rule-engine/nodes/external/ai-request/)、[Integrations](https://thingsboard.io/docs/user-guide/integrations/)、[Remote Integrations](https://thingsboard.io/docs/user-guide/integrations/remote/)、[Connectivity Guide](https://thingsboard.io/docs/user-guide/connectivity-guide/)、[Kafka Integration](https://thingsboard.io/docs/user-guide/integrations/kafka/)。

### 2.2 本地反向审查面

本地先通过 CodeGraph 追踪调用路径和影响面，再检查配置、契约、部署与文档：

- Real 路由：`apps/hvac-web/src/real/RealApp.tsx`、`SiteScopedShell.tsx`、`RealProductPages.tsx`、`EnergyAnalytics.tsx`。
- AI：`services/operations-agent-service/`、Operations Agent HTTP/AG-UI/Gateway 投影、OpenAI Finding Synthesizer、PostgreSQL Persistence、测试和契约。
- Analytics：`apps/hvac-web/src/api/energy-analytics.ts`、`services/platform-gateway/internal/gateway/analytics.go`、`services/analytics-read-model-projector/`、`services/telemetry-query-service/`、`libs/analyticsmodel/`。
- Forecast/Optimization：对应 Go Service、HTTP、PostgreSQL/ClickHouse Store、Migrations、Real Feature Boundary、Gateway Contract-only Route 和部署清单。
- Scheduler：`services/scheduler-service/`、Phase 1 Compose、Schedule SQL Example、Metric Worker 的 Claim 入口。
- Integration：MQTT Telemetry Adapter、Command Connector、Domain Outbox/Delivery 表、Ownership/Registry 契约和全仓 Outbound Connector 搜索结果。

## 3. 参考项目功能与它解决的问题

### 3.1 AI Model 生命周期与请求

ThingsBoard 用 Tenant-scoped `AiModel` 统一保存 Provider、Model ID 和 Provider-specific 配置，支持 OpenAI、Azure OpenAI、Gemini、Vertex、Mistral、Anthropic、Bedrock、GitHub Models 和 Ollama。Tenant Admin 可以增删改查、分页搜索和执行 Chat 连通性检查。

它解决的问题是：Rule Node 不应各自硬编码 Provider SDK；管理员需要一个模型目录、统一 Provider 配置和在规则配置前验证模型可用性的入口。

客观缺陷：

- `ai_model.configuration` 是普通 `JSONB`，Provider Config 中包含 API Key、Access Key、Secret Key 或 Service Account Key；未找到独立 Secret Reference、字段级加密或脱敏投影。
- `/api/ai/model/chat` 接收完整 `chatModelConfig`，而不是引用已保存的 Model ID；连通性 UI 因此把凭据再次发给后端。
- Controller 把任意 `Throwable.getMessage()` 放入失败响应，可能泄漏 Provider、网络或内部异常细节。
- Executor 使用固定线程池，但固定线程池的工作队列默认无界；没有看到 Tenant 级队列、成本配额或 Circuit Breaker。
- 导入导出沿用完整 Model 对象，未找到 Secret 清除或重新绑定流程。

结论：`ADAPT` 管理模型，`REJECT` Secret 和错误暴露行为。

### 3.2 AI Rule Node

`TbAiNode` 通过 Tenant+Model ID 读取 Chat Model，展开 Prompt Template，读取受 Tenant 校验的文本/PDF/图片 Resource，支持 Text、JSON 和 JSON Schema 输出，配置 1–600 秒超时，并把成功结果转换为新的 `TbMsg`。失败走 Failure Connection；嵌套 Provider Retry 被强制为 0，避免 Rule Engine Retry 与 SDK Retry 叠加。

它解决的问题是：让模型调用成为可编排节点，并给规则链提供结构化输出、显式失败和 Resource 输入。

客观缺陷：

- 模型输出进入与普通业务事实相同的消息通道，没有 Trust Level、Model Revision、Input Snapshot、Cost、Evidence 或人工确认语义。
- 500k 级 Prompt/Resource 配置没有对应的 Tenant Token/Cost Budget 证据。
- 没有看到面向 Telemetry、Document 和 Operator Data 的数据分类、DLP、外发同意或 Provider Region Policy。
- `forceAck` 可在 Provider 调用完成前 ACK 输入；进程崩溃会产生已经 ACK、但外部调用或后续消息未完成的窗口。

结论：吸收 Model Reference、Schema Output、Failure Branch、Timeout 和“单层重试”原则；拒绝把 AI 输出直接变成 Command、Alarm、FDD 事实或无来源普通消息。

### 3.3 AI UI 与 Calculated Field 钩子

固定源码中可验证的 UI 是 AI Model 管理、Provider 表单、连通性 Dialog 和 AI Rule Node 配置。没有找到可证明的 Calculated Field AI 执行链，因此不能把较新文档或产品叙述反投到 `v4.3.1.1`。

它解决的问题是管理员配置和规则作者选模，而不是完整 AI 运维产品。结论为：Model 管理 UI `ADAPT`；通用 AI Calculated Field `NO_SOURCE_EVIDENCE`；本地 Real AI Workspace `KEEP`。

### 3.4 Trendz 外部分析钩子

CE 只保存 `enabled/baseUrl/apiKey` 并提供 `/api/trendz/settings`，不包含 Trendz 分析引擎。Tenant Admin 可写，Tenant Admin 和 Customer User 可读；上游测试明确验证 Customer User 能得到完整 `apiKey`。

它解决的是外部产品跳转/集成配置，不解决 HVAC Analytics Domain。这个 Secret 返回行为直接 `REJECT`；是否采购或对接 Trendz 为 `DEFER`，且不得影响本地 Energy Analytics 权威。

### 3.5 MQTT、Kafka、RabbitMQ 与 REST 外部动作

ThingsBoard 为规则消息提供 Topic/URL/Headers/Body 模板和异步 Success/Failure 分支。MQTT 支持 TLS、QoS、Retained；Kafka 返回 Topic/Partition/Offset；RabbitMQ 支持 Exchange/Routing Key/Properties；REST 支持认证、代理、超时、并发限制、响应大小和响应映射。

它解决的问题是规则结果怎样触达外部系统，以及外部系统的回执如何回到规则链。

关键客观问题：

- 公共 `forceAck` 优化了吞吐，却把业务效果交付从“输入处理完成”中提前拆开，没有持久 Delivery Intent 作为恢复依据。
- Kafka `producer.send(...)` 的同步异常只写 Debug Log，不调用 Failure，消息可能悬空；异步 Callback 才正确进入 Success/Failure。
- RabbitMQ 在 `basicPublish` 返回时报告成功，没有 `confirmSelect/waitForConfirms` 或异步 Publisher Confirm，不能证明 Broker 已持久接收。
- REST 已实现 DNS-rebinding-safe Resolver、禁 Redirect 和响应上限，这是好 Pattern；但生产默认 `SSRF_PROTECTION_ENABLED=false`，默认超时/并发可为无限，`InterruptedException` 只记录日志而不完成 Failure。
- 各节点可以携带静态 Credential、任意目标地址和任意 Metadata Header，没有统一 Destination Allowlist、Credential Rotation、字段分类和 Egress Audit。

结论：连接器能力 `ADAPT`，直接副作用执行模型 `REPLACE`。

### 3.6 AWS、GCP 与 Azure 外部动作

SQS/SNS/Lambda、Pub/Sub 和 Azure IoT Hub 节点解决云服务发布和函数调用。SQS FIFO 用 `TbMsg.id` 作为 Deduplication ID、Originator 作为 Group ID；Lambda 可读取 Function Error 和 Request ID；Pub/Sub 有有界 Client Retry 设置；Azure 复用 MQTT/TLS。

值得保留的是稳定业务消息 ID→Provider Idempotency Key、Provider Request ID/Receipt 和有界 Timeout。需要拒绝的是每节点静态云密钥、任意 Region/Function/Topic、无统一交付账本和无 Least-Privilege Credential Reference。`TbAwsLambdaNode.init` 没有调用父类 `init(ctx)`，因此它与其他 External Node 的全局 `forceAck` 行为不一致；这是上游实现缺陷，不应复制。

### 3.7 Email、SMS、Slack 与 Teams

ThingsBoard 同时存在系统级 Provider Settings 和节点级 Provider/Credential 配置。系统级配置减少重复，节点级配置提高灵活性；但直接节点没有形成 Notification Request、Recipient Policy、Cooldown、Deduplication、Escalation、Consent 和 Delivery History 的完整业务生命周期。

本能力的完整生命周期归 D06 Notification 审查。D09 只裁决适配器边界：渠道适配器可 `ADAPT`，节点内 Credential 和直接发送 `REJECT`，目标必须由 Notification Domain 或 Outbound Integration Domain 创建 Delivery Intent。

### 3.8 Geo、Math、Delta 与聚合

`TbMathNode` 支持常量、Message、Metadata、Attribute、Latest Telemetry 作为参数，按 Originator 串行计算，并可写回 Message、Attribute 或 Timeseries。`CalculateDeltaNode` 读取 Latest 或进程软缓存计算差值和间隔。`TbGetTelemetryNode` 能按窗口读取和聚合。Geo 节点支持圆/多边形 Geofence，并保存 Entered/Left/Inside/Outside 状态。

它们解决的是无需 JavaScript 的常用轻量计算、Counter 差分、窗口聚合和移动对象空间事件。

客观裁决：

- Math 的轻量纯函数和 Originator 串行化值得吸收，但直接写 Generic Attribute/Timeseries 缺少 Metric Version、Input Lineage、Quality 和 Publication 语义。
- Delta 用进程缓存或 Latest 作为前值，对乱序、重启、Reset/Wrap、Binding Revision 和 Counter Provenance 的表达弱于本地 Counter/Metric 目标；本地不应退回该模型。
- Aggregation 应继续由 Analytics/Metric Owner 提供，而不是在 Rule Node 内创建第二套读写权威。
- Geo 对固定建筑 HVAC 不是当前需求；若未来有移动能源资产，应采用持久状态和 Event Time，而不是把节点内内存状态作为权威。

### 3.9 IoT Gateway、Platform Integrations 与 MCP

ThingsBoard IoT Gateway 的 Modbus、BACnet、OPC UA 等 Connector 在独立开源仓库；核心 CE 只有 Gateway API/Device 边界。Platform Integrations 官方文档明确属于 PE 路径，固定 CE 源码无法审查完整实现。MCP 和较新的预测性维护资料在固定版本中没有对应核心实现。

结论：IoT Gateway Pattern 已由 D03 交叉审查；Platform Integrations、Trendz 和 MCP 只能做边界识别，分别标为 `PE_OR_EXTERNAL_SOURCE_UNAVAILABLE`、`EXTERNAL_COMMERCIAL_PRODUCT`、`POST_BASELINE_OR_EXTERNAL`，不能获得源码级采用结论。

## 4. Domain 模型对照

### 4.1 ThingsBoard 参考模型

```text
AiModel
  -> tenantId + name + version + configuration(JSONB)
  -> ProviderConfig(credentials + endpoint + model parameters)
  -> TbAiNode(modelId + prompt + resources + response format)
  -> TbMsg(success/failure)

RuleNodeConfiguration
  -> destination template + credential/config
  -> direct external SDK/client
  -> provider receipt copied into TbMsg metadata
  -> Success / Failure

TrendzSettings
  -> enabled + baseUrl + apiKey
```

这个模型的优势是统一、可配置、可在 Rule Chain 中快速组装；弱点是 Credential、Destination、Delivery 和业务信任混在 Node Configuration/TbMsg 中。

### 4.2 HVAC Web 当前模型

```text
Operations Investigation
  -> trusted Tenant/Site Scope
  -> Plan / Run / Step / Tool Receipt
  -> authoritative Evidence + deterministic Analysis
  -> bounded Finding Synthesis
  -> Finding + Synthesis Provenance + Audit + Outbox

Energy Analytics
  -> Site/Timezone/Range/Granularity/QualityPolicy
  -> IAM Delegation + Query Owner
  -> Energy Interval Read Model
  -> Watermark + DatasetRevision + QualitySummary

Forecast / Optimization
  -> versioned input references
  -> PostgreSQL Run/Publication state
  -> ClickHouse result/evaluation
  -> Reconcile stale PERSISTING

MQTT IntegrationInstance
  -> static adapter config + inbound source/binding identity
  -> Telemetry ingest / Command connector
```

当前强项是 Scope、Evidence、Version、Quality、Audit 和跨存储发布；缺口是 Model 管理面、Credential Reference、Outbound Integration、通用 Delivery Ledger、Forecast/Optimization 产品接线和真实 HVAC 模型。

### 4.3 目标模型

```text
AiModelDefinition
  id, tenantId, provider, modelId, capabilities, status, revision
  endpointPolicyId, credentialRef, createdBy, updatedBy

ModelDeployment
  id, modelDefinitionId, environment, promptPolicyVersion,
  outputSchemaVersion, dataEgressPolicyId, enabled, revision

AiInvocation
  id, tenantId, siteId, useCase, deploymentId,
  inputSnapshotId/inputDigest, evidenceIds, promptPolicyVersion,
  outputSchemaVersion, status, providerRequestId,
  tokenUsage, cost, latency, fallbackReason, createdAt

IntegrationDefinition
  id, tenantId, direction, adapterType, destinationPolicyId,
  credentialRef, configRevision, enabled

DeliveryIntent
  id, tenantId, siteId, integrationId, purpose,
  payloadSchema, payloadDigest, idempotencyKey,
  sourceAggregate, classification, state, createdAt

DeliveryAttempt
  deliveryIntentId, attemptNo, leaseOwner, startedAt,
  destinationRevision, providerRequestId, outcome,
  retryAt, boundedErrorCode

DeliveryReceipt
  deliveryIntentId, providerMessageId, acceptedAt,
  durabilityEvidence, responseDigest, finalOutcome

OptimizationRecommendation
  inputSnapshot, baseline, objective, constraints, candidate,
  expectedImpact, uncertainty, risk, rollback, verificationPlan,
  approvalState, commandIntentId?
```

约束：`AiInvocation` 不是业务事实；`DeliveryReceipt` 不是设备执行证明；`OptimizationRecommendation` 不是 `Command`。三者只能通过各自领域的显式治理流程升级语义。

## 5. 核心流程

### 5.1 AI 调查目标流

```text
Principal + Tenant/Site Capability
  -> Investigation 创建/恢复
  -> READ Tool 逐次授权
  -> Owner 返回有来源 Evidence
  -> 确定性 Analysis
  -> Run Budget 消耗 modelInvocations
  -> ModelDeployment / DataEgressPolicy 校验
  -> 严格 Schema 的 Finding Synthesis
  -> 输出校验与 Evidence ID 收缩
  -> MODEL 或 DETERMINISTIC_FALLBACK
  -> Finding + Invocation Provenance + Audit + Outbox 原子提交
```

模型不得请求新权限、扩大 Site Scope、增加不存在的 Evidence、执行 Command 或改变 Alarm/Work Order 状态。

### 5.2 Outbound Integration 目标流

```text
Rule/Domain Decision
  -> 创建 DeliveryIntent（同业务事务或可靠 Outbox）
  -> Egress Worker Claim + Lease
  -> 读取 IntegrationDefinition Revision + CredentialRef
  -> Destination/SSRF/Data Classification Policy
  -> 发送（Provider Idempotency Key）
  -> 记录 Attempt + Provider Receipt
  -> DELIVERED / RETRY_WAIT / OUTCOME_UNKNOWN / DEAD
  -> 发布有界结果事件
  -> 人工重放只复用原 Intent，不重造业务事实
```

REST/Webhook 必须默认阻断私网、Metadata Address、Redirect 和未授权 DNS 解析结果；只有受审目标允许列表可以例外。

### 5.3 Forecast/Optimization 自动化目标流

```text
ScheduleDefinition
  -> durable Job
  -> Domain Worker Claim + Lease
  -> 冻结 InputSnapshot / ModelDeployment / Policy / Topology
  -> Forecast 或 Optimization 运行
  -> PostgreSQL BeginPublication
  -> ClickHouse 写入（稳定 Result Identity）
  -> PostgreSQL CompletePublication + Domain Event
  -> Gateway Read Model
  -> Real UI 展示 Quality/Uncertainty/Revision
```

Optimization 只产生 Recommendation。只有审批后才能创建 Command Intent，且 Command Domain 重新校验当前状态、安全约束和过期时间。

### 5.4 Analytics 目标流

现有 Real Energy 流继续作为权威：浏览器只提交受限查询参数；Tenant/Site 由已认证 Site Context 校验；Gateway/IAM 生成精确 Delegation；Query Owner 返回 Calendar-aligned Series、Quality、Watermark 和 Dataset Revision。外部 BI 只能消费该 Read Model 或其版本化导出，不能成为 Energy Truth Owner。

## 6. 关键代码结构裁决

### 6.1 ThingsBoard 值得吸收

| 结构 | 价值 | 采用方式 |
| --- | --- | --- |
| `AiModel` + Provider polymorphism | Rule/Use Case 与 SDK 解耦 | `ADAPT` 为无 Secret 的 ModelDefinition + Deployment |
| AI JSON Schema output | 把自由文本约束为可验证对象 | `ADOPT`，并叠加 Evidence/Trust/Invocation Provenance |
| External Node Success/Failure | 外部结果有显式分支 | `ADOPT` 到 Delivery Outcome Event，不直接 ACK 原业务事实 |
| REST DNS-rebinding-safe Resolver | 校验连接时真实解析地址 | `ADOPT` 且默认强制开启 |
| Provider Receipt Metadata | 便于关联外部请求 | `ADOPT` 到 DeliveryReceipt，不复制任意响应头 |
| SQS FIFO Dedup/Group | 稳定消息身份和局部顺序 | `ADAPT` 为业务 Idempotency Key + Ordering Scope |
| Node Timeout / concurrency knobs | 防止一个目标拖垮执行器 | `ADOPT` 为策略上限，不允许 0 表示无限 |
| Remote Integration isolation | Connector 故障与平台隔离、可本地缓冲 | `ADAPT` 为独立 Egress/Ingress Worker |
| Math 不依赖任意脚本 | 常用计算更易验证 | `ADAPT` 为纯函数 Rule/Metric Operator |

### 6.2 本地必须保留

| 本地结构 | 保留理由 |
| --- | --- |
| Investigation + Evidence + Finding | AI 结论有业务上下文和可审计来源 |
| Strict Tool Catalog 与 Owner Reader | 模型/运行时不能自造权限或直连数据库 |
| Deterministic Analysis + Model Fallback | Provider 不可用不改变业务结论基础 |
| Run Resource Budget | Provider 调用先消费耐久 Budget，避免无限调用 |
| Synthesis Provenance | 保存 Model/Policy/Schema/Digest/Token/Latency/Trace |
| Energy Read Model | Calendar/Timezone/Quality/Watermark/Revision 比 Trendz Hook 更接近本项目权威需求 |
| Cross-store Publication + Reconcile | 防止 PostgreSQL 状态与 ClickHouse 结果永久分裂 |
| Real `NOT_INTEGRATED` Boundary | 缺能力时诚实为空，不用 Demo 或浏览器推断补真值 |

## 7. 异常与边界处理

### 7.1 AI

- Provider 未配置时默认 `DISABLED`，不自动选择模型。
- Model 必须在精确 Allowlist；后续管理面仍需 Deployment State、Credential Rotation 和 Endpoint Policy。
- Provider Retry 保持单层；业务重试由 Invocation/Job Policy 管理。
- Timeout、非法 JSON、Schema Drift、未知 Evidence ID、分类漂移和超预算全部回退或失败关闭。
- Provider 原始异常不得返回浏览器或写入低基数 Telemetry；只保留有界 Error Code 和受限 Trace Reference。
- 外发前必须检查 Data Egress Policy；现有 Prompt Injection 防护不能替代数据分类/DLP。

### 7.2 External Delivery

- `forceAck` 不得用于有耐久业务后果的交付；只有 Delivery Intent 提交成功后，业务处理才可确认。
- REST 默认 SSRF 防护、无 Redirect、有限 Body、有限并发、有限总时长和 DNS Rebinding 防护。
- Broker “API 返回”不等于耐久接收：Kafka 需 Broker Ack，RabbitMQ 需 Publisher Confirm，MQTT 需明确 QoS/Session/Receipt 语义。
- Timeout 分为 `NOT_SENT`、`MAYBE_SENT`、`ACCEPTED_NOT_CONFIRMED`，不能统一自动重发。
- Secret 只由 Worker 通过 `CredentialRef` 获取，不进入 Rule Config、Intent Payload、Audit、日志或 UI。
- 任意 Metadata/Header 转发默认拒绝；使用字段级 Allowlist 和 Data Classification。
- Dead Letter 必须保留原 Intent、Attempt 历史和重放审批，不允许编辑后伪装成同一交付。

### 7.3 Analytics、Forecast 与 Optimization

- 空、零、部分、Suspect、Fallback、Unavailable 必须保持不同状态。
- Forecast Fallback 必须明确 `Quality=FALLBACK`，不得展示为模型预测。
- Optimization `NO_DISPATCH` 计划不是节能建议；没有目标函数求解、HVAC 约束、收益与不确定性就不能进入 Recommendation Pool。
- Scheduler Lease 过期对有外部效果的 Job 不得盲目重试，必须先 Reconcile。
- PostgreSQL Completion 失败但 ClickHouse 已有结果时保持 `PERSISTING` 并 Reconcile；不得再次生成不同 Result ID。

## 8. 本地源码级反向审查

| 本地模块 | 源码事实 | 当前判断 | 裁决 |
| --- | --- | --- | --- |
| Real AI Landing / Operations Workspace | Real Route 指向真实 Operations Gateway Projection，不读取 Demo 会话 | 产品入口真实；不是通用聊天机器人 | `KEEP` |
| Operations Agent Investigation | Tenant/Site Scope、READ Tool、Evidence、Finding、Audit、Outbox、PostgreSQL Checkpoint、资源预算均有源码和测试 | 安全内核强于 TB AI Rule Node | `KEEP` |
| OpenAI Finding Synthesizer | 单 OpenAI Provider；Env Secret；Model 精确 Allowlist；Strict Schema；`store=false`；`maxRetries=0`；有 Idempotency/Token/Latency/Trace | 执行边界可用，管理/轮换/数据外发治理不足 | `ADAPT` |
| Energy Analytics Real Flow | Real UI→Gateway→IAM/Query→Read Model；Timezone/Quality/Watermark/Revision 有契约；核心 Model/Projector 测试通过，聚合 Gate 被 History 测试字段漂移阻断 | 源码链存在，整链回归非 Green | `KEEP` 设计；先修测试漂移 |
| Forecast Service | Last-Value Engine；Site Load/PV；全结果 `FALLBACK`；Publication/Reconcile 完整；测试仍调用旧 `NewService(sink, clock)`，当前无法编译 | 发布代码有价值，但模块 Gate 已坏且不是生产预测模型 | `KEEP` 发布内核，`REPLACE` 产品能力声明并修复测试 |
| Forecast Gateway/Real | Public Route 属 Contract-only，Real Boundary=`NOT_INTEGRATED`，默认 Phase 1 不部署 | 未形成端到端产品链 | `REPLACE`/补实现 |
| Optimization Service | ESS-only、DAY_AHEAD 24h/15min、SHADOW、零功率 `NO_DISPATCH`、`Quality=FALLBACK`；测试仍调用已不存在的包级 `Optimize`，当前无法编译 | 安全占位，模块 Gate 已坏，且不是 HVAC 优化 | `KEEP` 发布内核，`REPLACE` Solver/Domain 并修复测试 |
| Optimization Gateway/Real | Public Route Contract-only；Real Boundary=`NOT_INTEGRATED` | 未接入 | `REPLACE`/补实现 |
| Scheduler | 创建 Durable Job、Misfire、Lease、Retry、Concurrency、Recovery | Coordination 已有；只有 Metric Worker 有明确 Claim/执行链 | `KEEP` Coordinator；为 Forecast/Optimization 新增 Worker Adapter |
| MQTT Telemetry Adapter | Inbound 现场数据和 Command Connector；IntegrationInstance 主要来自静态配置 | 属 Connectivity，不是 Outbound Business Integration | `KEEP` 边界，D03 继续治理 |
| Domain Outbox/Delivery 表 | 有内部 Domain Event Delivery/Inbox Pattern | 可复用模式；未形成外部交付产品 | `ADAPT`，不冒充已实现 |
| REST/Webhook/Kafka/Rabbit/Cloud/Slack/Teams Outbound | 未找到 Real 生产模块、管理面、Worker、Delivery Ledger 或 Gateway API | 功能缺失 | `ADOPT/DEFER` 按矩阵实施 |
| Legacy `/ai`/Demo 页面 | 不在 Real AI 调查入口；Real Build Gate 负责阻止 Demo 依赖 | 不作为 Real 实现依据 | `REJECT` 迁回 Real |

## 9. 全能力裁决矩阵

| D09 能力 | ThingsBoard 证据 | 本地现状 | 最终裁决 | 目标行为 |
| --- | --- | --- | --- | --- |
| AI Model CRUD | Controller/DAO/UI/Version | 无管理面，仅 Env | `ADAPT` | ModelDefinition/Deployment CRUD、Capability、Revision、禁用/撤销 |
| Provider Credentials | Provider Config 存 JSON | Server Env Secret | `REPLACE` | CredentialRef + Secret Manager + Rotation；API 永不返回 Secret |
| AI Connectivity Check | Chat API + UI Dialog | 启动时配置验证，无管理 Dry Run | `ADAPT` | 受限 Dry Run，引用 Deployment，固定 Prompt，不回显原始错误 |
| AI Provider Abstraction | 多 Provider Configurer | 单 OpenAI Synthesizer Port | `ADAPT` | 保留 Port；只按业务需求增加 Provider，不追求全覆盖 |
| AI Request Lifecycle | Async Future + Timeout | Invocation Provenance、Budget、Fallback | `KEEP` 本地并扩展 | Durable Invocation/Quota/Cost/Deployment Revision |
| AI Rule Node | Prompt/Resource/Schema/Failure | Investigation Finding Synthesis | `REPLACE` TB 信任模型 | AI 仅 advisory；Evidence-bound；不得直接 Command/Alarm |
| AI UI | Model Admin/Rule Config | Real AI Workspace | `KEEP` Workspace、`ADAPT` Admin | 管理面与业务工作台分离 |
| AI Calculated Field Hook | 固定版本无完整证据 | 无 | `DEFER` | 只有明确用例和成本/真值策略后再建 |
| Trendz Hook | `enabled/baseUrl/apiKey` | 无 Trendz | `REJECT` Secret 行为，`DEFER` 产品 | 外部 BI 只读消费版本化 Analytics Export |
| Energy Analytics | Trendz 不含 CE 引擎 | Real 权威 Read Model | `KEEP` | 继续本地 Calendar/Quality/Revision 模型 |
| REST/Webhook Action | 模板、认证、SSRF 可选、响应映射 | 无 Outbound Domain | `ADAPT` P0 | DeliveryIntent + 强制 SSRF/Allowlist + Receipt |
| MQTT Action | QoS/TLS/Retained | MQTT 仅现场接入/Command | `DEFER` Outbound | 明确业务目标后作为 Egress Adapter，不复用 Field Connector 权限 |
| Kafka Action | Async Callback/Receipt；同步异常漏洞 | Kafka 非 Phase 1 必需 | `DEFER` | 需要事件外发时加入，必须有 Outbox/Ack/Ledger |
| RabbitMQ Action | Basic Publish，无 Confirm | 无 | `DEFER` | 只有客户系统要求时加入，强制 Publisher Confirm |
| AWS SQS/SNS/Lambda | SDK 节点、SQS FIFO Dedup | 无 | `DEFER` | 采用 Workload Identity/Secret Ref、Destination Policy、Receipt |
| GCP Pub/Sub | SDK Retry/Callback | 无 | `DEFER` | 同上 |
| Azure IoT Hub | MQTT/TLS/SAS/PEM | 无通用 Azure Egress | `DEFER` | IoT Connectivity 用例归 D03，业务 Egress 归 Integration |
| Email/SMS/Slack/Teams | System/per-node Provider | Notification 尚未完整 | `ADAPT` Adapter，D06 主审 | Notification Request→Channel Delivery；无节点内 Secret |
| Math | 轻量函数、Originator 串行 | Metric Engine/DAG | `ADAPT` | 纯函数 Operator；结果由 Metric Owner 版本化发布 |
| Calculate Delta | Latest/进程缓存 | Counter/Metric 目标更强 | `REJECT` 退化 | 保留 Reset/Wrap/乱序/Binding Revision 语义 |
| Telemetry Aggregation | Rule Node 窗口读取 | Analytics/Metric Owner | `KEEP` 本地 | 不在 Rule Engine 再造 Analytics Truth |
| GPS Geofence | Filter/Action/状态 | 固定建筑无需求 | `DEFER` | 移动资产需求出现后使用 Event Time + Durable State |
| IoT Gateway Connectors | 外部开源项目 | MQTT/Edge 方向已有部分 | `CROSS_REF D03` | 连接能力不混入 Outbound Integration |
| Platform Integrations | PE/源码不可用 | 无 | `DEFER` | 不推断专有实现 |
| MCP | Post-baseline/外部 | 无 | `DEFER` | 有受控 Tool Protocol 用例后单独审查 |
| Forecast Extension | TB 固定 CE 无对应产品内核 | Standalone Fallback Service | `KEEP/REPLACE` | 保留 Publication；补真实 HVAC Model/Worker/API/UI |
| Optimization Extension | TB 不是参考实现 | ESS Safe Baseline | `KEEP/REPLACE` | 建立 HVAC Recommendation Domain，绝不直接 Dispatch |

## 10. 值得吸收的 Pattern

1. Model Registry 与 Provider Adapter 分离。
2. JSON Schema Structured Output 和初始化时能力校验。
3. 外部结果显式 Success/Failure，不把异常吞成成功。
4. 连接时 DNS Rebinding 防护、禁 Redirect、限制 Response 和并发。
5. Provider Receipt、Request ID、Broker Offset/Partition 作为交付证据。
6. 稳定业务 Message ID 映射 Provider Idempotency Key。
7. Remote Connector 独立故障域和本地缓冲。
8. 常用 Math Operator 替代任意脚本。
9. 配置版本升级函数和可测试的默认配置。

## 11. 不适合本项目的部分

1. Secret 与 Model/Node 配置共存并随 API、导入导出或审计对象流动。
2. Customer User 读取完整 Trendz API Key。
3. Tenant 可配置任意 URL、Broker、Function、Topic 或 Cloud Endpoint。
4. SSRF 防护默认关闭，Timeout/Concurrency 允许无限。
5. Rule Node 直接拥有不可恢复的外部副作用。
6. `forceAck` 用于需要耐久交付证明的动作。
7. AI 输出作为普通消息直接驱动控制、Alarm、FDD 或业务真值。
8. 把任意 Message Metadata 透传到外部 Header。
9. 每节点静态云凭据和每节点 SDK Client 生命周期。
10. 用进程软缓存承担 Counter 或 Geofence 的业务状态权威。
11. 为了“功能齐全”一次性支持全部 Provider/Connector。
12. 把 PE、Trendz 或 Post-baseline 文档当作 CE 源码行为。

## 12. 映射到本项目设计

### 12.1 P0：先完成可信边界

- 新增 AI ModelDefinition/Deployment/CredentialRef/DataEgressPolicy 设计；迁移 OpenAI Env 只能作为部署 Secret 输入，不能成为长期管理模型。
- 保持 Operations Agent 的 Evidence-first、Deterministic-first 和 Read-only Tool Catalog；禁止引入通用写工具。
- 定义 Outbound Integration OpenAPI/Domain：IntegrationDefinition、DeliveryIntent、Attempt、Receipt、Dead Letter、Replay Approval。
- 首个 Adapter 只做 REST/Webhook；默认 SSRF、Destination Allowlist、Timeout、Body Limit、Field Allowlist 和 Idempotency。
- 明确 Forecast/Optimization 当前为 `NOT_INTEGRATED`；不得修改 Real 文案宣称可用。

### 12.2 P1：接通真实 Intelligence 产品链

- Forecast：建立 HVAC 目标（负荷、供回水温度、COP/效率、故障概率等）的 Model/Feature/Input Snapshot；加入 Scheduler Worker、Gateway Read API、Real Series 和 Uncertainty/Quality。
- FDD：Finding 必须记录 Rule/Model Version、Evaluation Window、Evidence、Confidence、Quality Blocker，并可显式链接 Alarm/Work Order。
- Optimization：把 ESS Safe Baseline 与 HVAC Recommendation 分开；建立 Baseline、Objective、Comfort/Safety Constraint、Candidate、Expected Impact、Uncertainty、Rollback、Verification Plan 和 Approval。
- 任何 Recommendation→Command 都进入独立审批和 Command 当前状态复核，不复用优化计算时快照直接下发。

### 12.3 P1/P2：按需求扩展 Adapter

- Email/SMS/Slack/Teams 跟随 Notification Domain，不先做直接 Rule Action。
- Kafka/RabbitMQ/MQTT Egress、AWS/GCP/Azure 只在客户系统和 SLO 明确后增加。
- 每个 Adapter 必须实现相同 Delivery Port、错误分类、Idempotency、Receipt、CredentialRef 和故障注入测试；不能修改核心 Delivery 状态机。

### 12.4 P2/DEFER

- Generic AI Calculated Field、MCP、Geo、Trendz/外部 BI 深度集成、连接器市场。
- 多 Provider 只在数据驻留、成本、离线或客户合同要求出现时增加；不以 Provider 数量作为产品完成度。

## 13. 文档与源码冲突

1. `docs/research/ai-llm-integration.md` 仍描述 NestJS、Mock-first `/api/v1/ai/chat` 和前端拼接 Telemetry；当前 Real 路径已经是 Operations Agent + Gateway + Owner Evidence。该文档只能视为历史调研，不能指导 Real 实现。
2. `docs/operations-agent/framework-architecture.md` 的早期状态段仍称 Live External Model Provider 在当前 Slice 之外，但源码已经包含 OpenAI Responses Adapter、Env Bootstrap 和测试。实现状态以源码和测试为准，文档需后续同步。
3. Backend Architecture 把 Forecast/Optimization 称为 Selective Intelligence Services；这只说明物理部署定位，不证明它们已部署或已形成产品能力。部署清单、Gateway Contract-only Route 和 Real Boundary 共同证明当前未接通。
4. Scheduler 支持 Forecast/Optimization Job Type，只证明优先级和持久 Job Schema 接受这些类型；没有 Worker Claim/Handler 就不能声称自动执行。
5. ThingsBoard 官方 Integrations 文档包含 PE Platform Integrations；固定 CE 源码没有完整实现，本文不对其内部语义作推断。

## 14. 实施门槛

### AI Production Gate

- Secret 不出 Secret Manager/Workload Identity 边界。
- Deployment、Prompt Policy、Schema、Data Egress Policy 和 Model Revision 全部可追溯。
- Tenant/Site/Use Case 有调用预算；Token/Cost/Latency 可汇总但不泄漏 Prompt/Completion。
- Prompt Injection、Scope Widening、Unknown Evidence、Invalid Schema、Timeout、Provider Outage 和 Budget Exhaustion 均有测试。
- AI 不拥有 Command、Alarm、Work Order、FDD 或 Energy Truth 写权限。

### External Delivery Gate

- Intent 在外发前耐久提交；Worker 重启、Lease 过期和重复投递可收敛。
- REST SSRF/DNS Rebinding/Redirect/Metadata IP/Body Limit/Timeout 测试通过。
- Broker/Cloud Adapter 必须证明 Provider Ack/Confirm，不把本地 API Return 当作耐久接收。
- Secret Rotation、Destination Revision、Idempotency Conflict、Outcome Unknown、Dead Letter 和 Replay 有测试和审计。
- 交付指标按固定 Adapter/Outcome 分类，禁止目标 URL、Topic、Tenant 或任意错误成为高基数 Label。

### Intelligence Product Gate

- 默认部署或选择性部署有明确启用证据、健康检查、Worker、Gateway API 和 Real UI。
- 结果包含 Model/Feature/Input/Policy/Topology Revision、Quality、Uncertainty 和 Watermark。
- Fallback 不与 Model Output 混淆；没有输入时不制造预测。
- Optimization 只产生 Recommendation；审批、Command、执行和效果验证是独立状态。

## 15. 本轮最终裁决

- **KEEP**：Operations Agent Evidence/Tool/Investigation/Finding/Audit/Outbox 安全内核；Real Energy Analytics；Forecast/Optimization 的版本化输入和 Cross-store Publication/Reconcile；Real 诚实的 `NOT_INTEGRATED` 状态；Scheduler Coordination。
- **ADOPT/ADAPT**：AI Model Registry、Provider Adapter、JSON Schema Output、REST DNS Rebinding 防护、Provider Receipt、稳定 Idempotency、Remote Worker Isolation、轻量 Math Operator、统一 Delivery Outcome。
- **REPLACE**：Secret-in-JSON、完整配置 Chat Check、原始异常回显、AI 普通消息信任、Direct External Side Effect、Force Ack Durable Effect、任意 Metadata Egress、Rabbit 无 Confirm、Kafka 吞同步异常、Forecast/Optimization 已完成假象、ESS Baseline 充当 HVAC Optimization。
- **REJECT**：Customer User 读取 Trendz API Key、默认关闭 SSRF、无限外部请求、每节点静态云 Secret、AI 直驱物理控制或业务真值。
- **DEFER**：未有场景证据的 Provider/Connector、Trendz、Platform Integrations、MCP、Geo、Generic AI Calculated Field 和连接器市场。

本轮没有发现理由把 ThingsBoard 外部节点的实现行为整体作为默认答案。它的配置体验和适配器广度值得参考；本地的 Scope、Evidence、Quality、Audit 和 Publication 语义更适合 HVAC 安全边界。反过来，本地也不能因为已有服务目录就宣称 Forecast/Optimization/Outbound Integration 完成：端到端接线和真实 HVAC Domain 仍是明确缺口。

## 16. 验证结果

本轮实际结果：

| 命令 | 结果 | 解释 |
| --- | --- | --- |
| `npm run operations-agent-service:check` | `PASS` | Contract、Typecheck、Boundary、Build 和 110 项测试通过 |
| `npm run rms:real:graph` | `PASS` | 88 个 Real 可达模块通过，未发现 Demo 依赖回流 |
| `npm run lint` | `PASS` | HVAC Web TypeScript `--noEmit` 通过 |
| `go test ./...`（Forecast） | `FAIL` | 测试仍按旧签名调用 `NewService`，测试包无法编译 |
| `go test ./...`（Optimization） | `FAIL` | 测试仍调用已删除的包级 `Optimize`，测试包无法编译 |
| `npm run test:analytics` | `FAIL` | `analyticsmodel`、Analytics Projector 通过；Telemetry Query History/Server 测试仍引用已删除的 `ActingOrganizationID` |
| `npm run test:gateway` | `FAIL` | 一部分失败来自 `go-redis` 下载网络不可用；S3 Local Gateway 测试还引用已删除的 `OrganizationID/organizationID`，属于真实测试漂移 |

所以本轮可以关闭“裁决”票，但不能把 Forecast、Optimization、Analytics/Gateway 全量回归或外部交付能力标为完成。未运行的真实 OpenAI Provider、PostgreSQL、ClickHouse、Docker、Broker 和云服务集成也不得写成已验证。

## 17. S22 实施证据（Issue #282）

S22 按本文固定裁决继续执行，并再次对照固定 ThingsBoard CE v4.3.1.1 / `c2a52e46c44e308ddee430e7266b8e10eddde9c4` 的 `AiModelController.java`、`AiModel.java`、`AiModelControllerTest.java` 与官方 AI Model / AI Request 文档。实现继续遵循以下 ADOPT / ADAPT / REJECT：

- **ADOPT**：Model Definition 目录、明确 Provider/Model ID、结构化输出 Schema、Provider Connectivity/Failure 显式化、不可变 Revision 思路。
- **ADAPT**：凭据从 Model JSON 中拆成 `CredentialRef`；加入 Tenant/Site/Use Case、Data Egress Policy、预算、Input Digest、Evidence IDs、Token/Cost/Latency 与 Failure Code；Deployment Revision 作为不可变事实，启用/回滚通过独立 Binding 指向别的 Revision。
- **REJECT**：Secret-in-model、AI 普通消息直接成为领域事实、AI/FDD/Optimization 直接持有 Command/Alarm/Work Order 写权限，以及把 Rule Chain 消息流直接等价为 HVAC 控制链。

S22 已把本文 12.2 / Intelligence Product Gate 的核心缺口落到代码：

1. `libs/intelligencemodel` 定义 ModelDefinition、DeploymentRevision、DataEgressPolicy、Invocation Provenance、FDD Finding、Optimization Recommendation 与独立 Current-State Revalidation；公开结构没有 API Key/Password/Secret 字段。
2. Invocation Adapter 在 Provider 调用前执行 Data Egress 与预算判定，严格 JSON Schema 解码；`PROVIDER_UNAVAILABLE`、`OUTPUT_SCHEMA_INVALID`、`BUDGET_EXCEEDED`、`DATA_EGRESS_DENIED` 为独立失败码。
3. Forecast 只接受带时间戳 Observation；四个及以上输入进入拟合模型并输出 `VALID + uncertainty bounds`，短历史显式 `FALLBACK`，零输入直接失败；PostgreSQL Snapshot 与 ClickHouse 点集必须 provenance 一致才允许读取。
4. Forecast/Optimization 均接入 Scheduler 的 Claim/Lease/Attempt/Retry/Lease-expiry recovery，不再只有 Job Type 和表结构。
5. FDD 首条真实规则为 Chilled-Water Low Delta-T：冻结评估窗口、供回水 Evidence、Rule/Model Revision、Confidence；Finding 不自动生成 Alarm/Work Order，只保存显式链接。
6. Optimization 删除运行时 ESS `NO_DISPATCH` 产品链，改为 HVAC Recommendation：Baseline、Objective、Comfort/Safety Constraints、Candidate、Expected Impact、Uncertainty、Risk、Rollback、Verification 全部齐备；Recommendation 保持非执行对象。
7. Recommendation 即使 APPROVED，也只有在 recommendation 创建之后完成独立 Current-State Revalidation 且仍未过期时，数据库和领域模型才允许挂接 Command Intent。
8. Gateway 通过 Wayfinder route owner 将 Forecast/FDD/Optimization 路由分别交给对应领域服务；Gateway 只做 Session、CSRF（写请求）、精确 Site 授权和代理，不拼造 Intelligence 事实。
9. Real UI 直接读取已发布 Forecast/FDD/Optimization 事实；Forecast 的 FALLBACK/uncertainty 明示，Optimization 页面不提供直接 Command 下发入口。

S22 同时保留既有 `mlops_*` Metadata Domain：它继续记录 Artifact/Evaluation/Approval/Deployment Metadata/Drift/Rollback；新增 AI Model Registry 负责 Provider Model 与 Invocation Policy，不用两套表同时充当同一个 owner。
