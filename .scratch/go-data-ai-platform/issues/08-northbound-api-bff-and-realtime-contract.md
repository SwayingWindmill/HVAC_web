# 确定北向 API、BFF 与实时订阅契约

Type: prototype
Status: resolved
Blocked by: 02, 04, 06, 07
Part of: ../map.md

## Question

HVAC Web 应通过怎样的稳定北向契约使用平台能力，而不感知内部微服务拓扑？需要以粗粒度 OpenAPI、WebSocket/SSE 订阅和必要的 BFF 聚合原型来确定认证与 Site Context、资源 ID、时间范围、分页、错误模型、命令状态推送、遥测快照与增量、断线恢复、背压、API 版本和前端 client 生成方式。

## Comments

- 北向认证统一采用外部 IAM 签发的 OIDC/OAuth 2.0 Access Token。所有外部请求只进入 `platform-gateway`；Gateway 统一验证 JWT 签名、issuer、audience、有效期和 Token 类型，并建立可信 Principal Context。内部领域服务不直接暴露给浏览器，仍由各数据所有者执行资源级和动作级授权，不能把 Gateway 认证等同于业务授权。
- HVAC Web 浏览器采用 BFF Session：通过 OIDC Authorization Code Flow + PKCE 登录，由 Gateway/BFF 在服务端加密保存 Access Token 与 Refresh Token，浏览器仅持有 `HttpOnly`、`Secure`、合适 `SameSite` 策略的不透明 Session Cookie，禁止把 Token 写入 localStorage、URL 或 WebSocket 查询参数。Cookie 写操作必须配套 Origin/CSRF 防护，会话撤销必须同步终止实时连接；移动端、CLI、第三方系统和服务账户仍使用适合其客户端类型的 Bearer Access Token 访问 Gateway。
- Organization 是租户安全边界。组织级接口通过 `/organizations/{organizationId}` 显式表达目标；Site、Device、Command 等资源级接口由服务端根据资源权威归属解析 owning Organization。客户端可以选择 acting Organization，但 Gateway/IAM 必须依据有效 Membership、RoleBinding、SiteBinding 和策略重新验证，不能把 Header、Session 偏好或 Token Claim 当作授权真相。新核心契约不使用 `X-Organization-Id`；active Organization 仅用于 UI 导航和恢复选择。跨组织 SiteBinding 场景必须分别记录 acting organization 与 resource owning organization，禁止合并用户在多个组织中的权限链。
- Site Context 采用路径优先、资源归属优先。Site 级集合、创建和聚合接口使用 `/sites/{siteId}/...`；单个 Device、Equipment、Command 等资源接口只提交平台资源 ID，由服务端按权威数据解析所属 Site；跨 Site 操作必须使用显式 Scope。`X-Site-Id` 不设置兼容期，直接从新北向契约和迁移目标中废弃；现有客户端必须迁移到显式 Site 路径或资源 ID。active Site 仅作为 UI 导航偏好，不得参与授权或隐式决定 API 范围。
- 所有新北向 API 仅使用平台生成的不可变 UUIDv7 作为公共资源 ID。ThingsBoard UUID、数据库内部主键、旧业务 ID 和演示别名不得作为公共主键；外部系统标识通过 `ExternalBinding` 或受控迁移映射关联到平台资源。业务 `code` 只用于展示、搜索、导入和租户范围内的可管理标识，不能替代平台 ID。普通业务接口默认不暴露 ThingsBoard ID，仅集成管理或诊断接口在专门权限下返回受控外部引用。
- 成功响应不使用全局 `{success, data, message}` 包络。单资源接口直接返回类型化资源对象；集合返回带 `items` 和明确分页元数据的集合 DTO；创建返回 `201 Created` 与资源表示，异步受理返回 `202 Accepted` 与可查询的 Operation/Command 资源，无响应体操作使用 `204 No Content`。请求追踪信息主要放在响应 Header；BFF 聚合必须定义稳定、可生成客户端的页面 DTO，禁止以任意 `data` 容器掩盖无边界聚合。
- 所有北向 API 错误统一使用 `application/problem+json`，包含稳定的 `type`、`title`、`status`、机器可读业务 `code`、安全 `detail`、`instance`、`traceId`、`retryable`，以及可选字段级 `errors`。HTTP 状态码严格区分认证失败、资源不可见、协议错误、业务语义错误、状态冲突、并发前置条件、限流和基础设施故障；客户端只能依赖 `code` 和状态码，不能依赖错误文案。Gateway 必须保留领域错误语义并移除内部敏感细节，不得把所有失败统一改写为模糊 `500`。
- 新北向集合接口统一采用不透明 Cursor 与 Keyset Pagination，请求使用 `limit`、`cursor`，响应返回 `items`、`nextCursor`、`hasMore`。Cursor 必须绑定 Scope、过滤条件、排序、查询版本和必要快照上界，但不能替代当前认证授权；排序必须包含不可变唯一 Tie-breaker。新核心接口不提供通用 `page/pageSize/offset`，也不默认执行精确总数统计。需要严格重现的数据集通过独立 Snapshot、Export 或 Investigation Dataset 资源固定范围、水位和版本。
- 所有北向 Instant 使用带时区的 RFC 3339 字符串，响应规范化为 UTC 毫秒精度；Site 保存 IANA 时区，日、周、月等业务日历按 Site 时区解释。所有时间范围统一采用左闭右开 `[startAt, endAt)`，领域 API 只接受明确绝对区间，不直接解释“今天”“本月”等模糊预设。遥测明确区分 `observedAt`、`ingestedAt`、`asOf`、watermark 与 revision；实时顺序和去重必须依赖 Cursor/sequence/revision，不能只依赖时间戳。
- `platform-gateway` 作为唯一北向入口，仅提供少量页面导向、类型化且调用预算受限的 BFF 读取接口。BFF 只负责可信上下文传播、有限查询编排、字段裁剪、页面 DTO 转换和契约化降级，不拥有业务权威数据、不复制领域规则、不执行跨服务写事务。高频或高成本的重复聚合应沉淀为事件驱动 Read Model；部分依赖失败只能按 DTO 预先定义的必需区块和可降级区块处理，并明确各区块的 `asOf`、watermark 与新鲜度。
- 遥测页面采用 REST Snapshot + Realtime Delta 两阶段模型。Snapshot 返回完整 Point 状态、`asOf`、watermark、revision 和不透明 `snapshotCursor`；实时订阅必须以该 Cursor 为起点继续，保证 Snapshot 与订阅建立之间不产生静默缺口。Snapshot 与 Delta 复用同一 Point Schema，客户端按 Point ID、sequence、revision 和 Cursor 幂等合并，允许重复但不得状态回退；Cursor 不能替代授权，过期、范围不匹配或无法证明连续性时必须返回 `RESYNC_REQUIRED` 并重新获取 Snapshot。
- 第一版北向实时协议统一采用原生 WebSocket over TLS，并通过 `Sec-WebSocket-Protocol: hvac.realtime.v1` 协商协议版本。浏览器使用同源 BFF Session Cookie 完成握手认证，一条连接通过 `subscriptionId` 复用多个逻辑订阅；连接建立不等于订阅成功，必须收到 `SUBSCRIBED`。WebSocket 只承载订阅控制和服务端事件推送，设备命令及其他业务写操作继续通过 REST；第一版不并行提供等价 SSE 协议，也不继续以 Socket.IO 作为新公共契约。
- WebSocket 客户端采用显式连接状态机，只有连接、认证、订阅确认和 Cursor 连续性均成立后才进入 `LIVE`。连接健康由协议级 Ping/Pong 与应用层 Heartbeat 共同判断，第一版默认 20 秒心跳、45 秒失效阈值；断线采用带 Full Jitter 的指数退避，默认从 1 秒增长到最多 30 秒并尊重服务端 `retryAfter`。每个逻辑订阅只在完整应用 Frame 后推进并保存 Cursor，重连时重新授权并独立 Resume；Cursor 过期、Sequence 缺口或本地状态不可信时必须 `RESYNC_REQUIRED` 并重新获取 Snapshot。认证失效或永久协议错误停止自动重连。
- 实时 Gateway 对每个逻辑订阅和连接设置有界队列、应用层 ACK 与公平调度。遥测当前状态流显式使用 `LATEST_STATE`，客户端积压时允许按 Point 合并到最高有效 revision，但合并结果必须是完整权威状态并保留失效语义与可证明 Cursor；命令、告警、审批和审计等 `ORDERED_EVENTS` 只能批量，禁止丢弃或合并中间事件。客户端仅在完整应用 Frame 后 ACK；达到硬上限、长期无 ACK 或无法证明连续性时必须停止对应订阅并返回 `RESYNC_REQUIRED`，不得静默丢数据或使用无限发送队列。
- Command REST 资源始终是命令当前状态的权威来源。创建命令返回 `202 Accepted`、Command revision 和可衔接实时流的 `statusCursor`；WebSocket 通过 `COMMAND_STATUS`、`ORDERED_EVENTS` 推送不可合并的有序状态事件，客户端按 `eventId`、sequence 和 revision 幂等应用。断线优先从最后 ACK Cursor Resume；无法恢复时重新 GET 原 Command，并使用返回的新 Cursor 订阅，绝不重新提交命令。`OUTCOME_UNKNOWN` 是可审计的真实状态，不能自动映射为成功或失败；迟到证据通过显式 Resolution 补充，不静默改写历史状态。
- REST 主版本固定在显式路径 `/api/v1`，WebSocket 主版本通过 `Sec-WebSocket-Protocol: hvac.realtime.v1` 协商。同一主版本只允许明确的兼容扩展；字段删除、类型/单位/时间/状态语义变化、认证边界变化以及 Cursor、Sequence、ACK 或交付语义变化必须升级主版本。仓库中的 OpenAPI、实时协议 Schema 与公共 Schema 是北向契约权威来源；TypeScript REST Client 和 WebSocket Frame 类型/运行时校验器由锁定版本工具生成并提交仓库，CI 通过 Schema 校验、破坏性变更检测、再生成 Diff 和实现契约测试阻止漂移。HVAC Web 只手写薄 Adapter、业务 Hook 与实时连接状态机，禁止重新引入 `X-Site-Id`、ThingsBoard ID、重复 DTO 或 `{success,data}` 包络。

## Answer

HVAC Web 只通过 `platform-gateway` 使用平台能力。浏览器采用 OIDC Authorization Code Flow + PKCE 与服务端 BFF Session，浏览器只持有安全的不透明 Cookie；非浏览器客户端使用 Bearer Access Token。Gateway 负责统一认证、会话、路由、Trace 和协议治理，领域服务继续拥有资源级授权与业务规则。

REST 北向契约固定为 `/api/v1`。Organization 是租户边界，组织级目标通过路径表达；Site 级集合、创建和聚合使用 `/sites/{siteId}/...`，单资源请求只使用平台 UUIDv7 并由服务端解析权威归属。`X-Site-Id` 与 `X-Organization-Id` 不进入新契约，ThingsBoard UUID 和旧业务 ID 只存在于 `ExternalBinding` 或迁移映射中。成功响应直接返回类型化资源或集合 DTO，错误统一采用 `application/problem+json`；集合使用不透明 Cursor 和 Keyset Pagination，时间使用 RFC 3339 UTC 毫秒 Instant、Site IANA 时区及左闭右开区间 `[startAt, endAt)`。

BFF 只提供少量页面导向、类型化、调用预算受限的读取聚合，不保存业务权威数据、不复制领域规则、不执行跨服务写事务。重复且高成本的页面摘要应沉淀为事件驱动 Read Model，并在响应中暴露可解释的 `asOf`、watermark 和降级状态。

实时北向协议固定为原生 WebSocket over TLS，并通过 `hvac.realtime.v1` 协商。页面先获取 REST Snapshot，再以 `snapshotCursor` 建立 Delta 订阅；客户端按资源 ID、sequence、revision 与 Cursor 幂等合并。连接使用显式状态机、Heartbeat、Full Jitter 重连、独立订阅 Resume/Resync、有界队列和应用层 ACK。遥测当前状态使用可按 Point 合并的 `LATEST_STATE`；命令、告警、审批和审计使用不可合并的 `ORDERED_EVENTS`。无法证明连续性时必须 `RESYNC_REQUIRED`，不得静默丢失。

Command REST 资源是命令状态权威来源。创建返回 `202 Accepted`、revision 与 `statusCursor`，WebSocket 只推送后续有序状态事件；恢复失败时重新读取原 Command，绝不重新提交命令。`OUTCOME_UNKNOWN` 保留为真实可审计状态，迟到证据通过 Resolution 补充。

OpenAPI、实时协议 Schema 与公共 Schema 是契约权威来源。TypeScript REST Client、Frame 类型和运行时校验器由锁定工具生成并提交仓库，CI 必须执行 Schema 校验、兼容性检测、再生成 Diff 和实现契约测试。前端只手写薄 Adapter、业务 Hook 与实时连接状态机。
