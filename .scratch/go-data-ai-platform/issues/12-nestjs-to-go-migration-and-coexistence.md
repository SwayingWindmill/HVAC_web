# 确定 NestJS 到 Go 的迁移与共存路径

Type: grilling
Status: resolved
Blocked by: 03, 04, 06, 07, 08, 09, 10, 11
Part of: ../map.md

## Question

现有 NestJS 后端如何在不中断 HVAC Web、ThingsBoard 和设备控制的情况下被 Go 平台替换？需要确定 Strangler 路由、Legacy 端口与服务定位、数据库 Schema 所有权、数据迁移、双读/双写或 CDC、契约测试、影子流量、命令链路灰度、切换和回滚标准、历史数据处理、旧服务冻结规则与最终退役条件。

## Comments

- 迁移采用按业务能力和资源 Scope 渐进替换的 Strangler 模式，不做逐文件翻译、全量重写后一次切换，也不长期保留两个对等业务平台。`platform-gateway` 首先成为浏览器、移动端、第三方系统和 Realtime 的唯一公开入口，并通过版本化 Route Ownership Registry 将每个 Method+Path、WebSocket协议、Webhook和内部入口路由到 Go Owner、Legacy Anti-Corruption Layer或暂时的 NestJS Owner。HVAC Web始终访问统一 `/api/v1` 与 `hvac.realtime.v1`，不感知内部服务、端口、语言或切换批次。
- 当前 NestJS 默认端口3001、Vite代理3000和Copilot Runtime默认3001均不构成业务契约。生产只暴露Gateway；NestJS以内部服务名例如`legacy-nestjs-backend`运行在私有网络，Copilot Runtime使用独立内部服务，实际端口由部署配置管理。Local/dev同样只让Vite代理Gateway，禁止前端通过切换端口直接选择Legacy或Go，以免绕过认证、兼容层、审计和灰度规则。
- 建立中心化 Migration Registry，至少记录`migrationUnitId`、能力域、Route、资源Scope、Legacy Owner、Target Owner、数据Owner、依赖、契约版本、迁移状态、影子比例、切流比例、回滚策略、Owner、开始时间和观察期限。状态采用`DISCOVERED → BASELINED → SHADOWING → READY → CANARY → CUTOVER → OBSERVING → RETIRED`，失败可进入`PAUSED`或按允许边界`ROLLED_BACK_ROUTE`；数据库写入所有权一旦完成移交，不通过普通路由回滚重新授予Legacy写权限。
- 迁移单元按领域和副作用边界划分，不按Controller文件划分。建议顺序为：统一Gateway与身份兼容层；Core/IAM与平台ID映射；Registry只读；Telemetry latest/history只读；Realtime；报表与聚合；AI业务状态与Tool边界；Scheduler/Automation；最后迁移Command写链路与ThingsBoard控制Connector。Command可在前置治理和真实Capability验证完成后独立灰度，不要求等待所有低风险查询，但不能在身份、设备绑定、审计、Fence和回滚演练未就绪时提前切换。
- Legacy Anti-Corruption Layer是有明确退役日期的边界，不是第二套领域平台。它负责将旧`customerId/tbCustomerId/buildingId`、ThingsBoard外部ID、旧Role/Scope、`X-Site-Id`、旧响应包、旧Telemetry参数、旧Command DTO和Socket.IO消息映射为平台Organization/Site/Equipment/Device/TelemetryPoint、UUIDv7、Problem Details、Canonical Command和新的Realtime语义；旧字段不能进入Go领域模型、数据库主键、事件Schema或新的服务间API。任何无法无损映射的请求必须明确拒绝或返回迁移错误，禁止猜测Site、单位、Capability或用户权限。
- 旧兼容契约只存在于Gateway/ACL的显式版本和路由中，并具备调用量、调用方、弃用Header、目标替代接口、关闭条件和最终日期。`X-Site-Id`、多种成功包络、`range`时序参数、Socket.IO逐Key订阅、旧角色名及ThingsBoard ID可在受控兼容期内翻译，但新生成Client和新前端代码不得继续依赖。默认演示ID`b1`、Building A/B/C、浏览器localStorage Token、前端角色授权、读取时`sync=true`、未签名摄取和`default` Token Namespace不设长期兼容路径，迁移准备完成后直接清退。
- 迁移前先冻结并版本化当前行为基线。修复NestJS Registry Controller测试依赖，给EnergyAgent补充可重复的测试依赖与统一命令；从HVAC Web实际DTO、Site越权、Registry、Telemetry latest/timeseries、Realtime恢复、Command幂等与状态、Scheduler运行历史以及Investigation语义提取语言无关黑盒契约测试。测试通过Gateway执行，同一Fixture可分别命中Legacy和Go实现；不能使用内部Class、ORM实体或语言特有异常作为验收依据。
- 契约测试至少覆盖认证与Session、Organization/Site授权、资源不可见语义、UUID与ExternalBinding、分页与时间范围、数据质量和Watermark、错误码、幂等键、乐观并发、Realtime Snapshot+Delta、Command状态与`OUTCOME_UNKNOWN`、Scheduler时区、AI取消/迟到结果以及审计。Golden Fixture保存输入、Canonical响应、排序、单位、时间、质量、错误和允许差异；任何故意契约变化必须通过新版本和前端迁移，不能把Legacy偶然Bug永久固化为新平台要求。
- Route Ownership Registry由Gateway权威执行，切换按Method、Path、Organization、Site、IntegrationInstance、设备Cohort或稳定百分比进行，但安全语义不能因灰度组不同而变化。路由决策记录`routePolicyRevision`、目标Owner、Scope、原因和Trace，并进入审计。Sticky Cohort使用稳定业务键而非客户端Cookie，防止同一设备或命令在请求间落到不同Owner；未知或冲突的路由状态Fail Closed，不进行随机Fallback。
- Gateway到NestJS只传递经过验证的内部Principal Context或短期Audience受限Legacy Delegation Token，不转发浏览器Refresh Token、Session Cookie或未经验证的`X-Principal/X-Site` Header。Legacy仍必须执行其自身必要授权，但不能覆盖Gateway解析出的owning Organization/Site。迁移期间发现Legacy授权能力不足时，通过ACL和前置权威检查收紧访问；不允许以“旧系统兼容”为由恢复公开Legacy端口或跳过资源授权。
- 数据所有权通过版本化 Data Ownership Registry管理。每个表、事件家族、对象前缀、缓存投影和搜索索引有唯一写Owner、读者、权威级别、迁移方式和退役计划。NestJS和Go不得同时写同一业务表、同一聚合、同一Command、同一Schedule或同一ThingsBoard配置字段；禁止数据库Trigger或通用双写把两个模型互相覆盖。Go服务使用独立Database或Schema、运行账号和Migration账号，Legacy数据库账号在所有权切换后立即降为只读或撤销。
- 新Go领域模型从独立Schema开始，使用平台UUIDv7和`organization_id/site_id`，不直接重用旧表作为新领域持久层。旧`customer_id/tb_customer_id`、用户、Asset、Device、Command和Schedule通过明确映射资源迁移，例如`LegacyIdentityMap`、`ExternalBinding`、`LegacyResourceMap`与`MigrationProvenance`；映射记录Legacy系统、表、主键、平台资源ID、owning Organization、转换版本、有效时间、Hash和迁移批次。旧ID在兼容层中可查询，但不成为新公共主键或跨服务关联键。
- 数据迁移采用“快照回填 + 单向增量捕获 + 验证 + 写入冻结 + 尾差追平 + 所有权围栏切换”的通用流程。快照作业按Organization/Site和稳定主键分页，幂等Upsert到目标Schema并保存Source Watermark、行数、Hash、错误和Checkpoint；增量优先使用Legacy Outbox或应用事件，无法改造时可使用受控CDC读取数据库日志。CDC只向目标Read Model或待接管Schema单向传播，不反向写Legacy，也不能代替业务语义转换。
- 每次数据所有权切换创建`DataCutoverPlan`，固定Source Watermark、Target Revision、写冻结窗口、验证查询、允许尾差、回滚边界和审批。切换时先阻止Legacy新写，等待在途事务和Outbox完成，追平CDC，执行数量、Hash、引用、租户、版本和业务不变量校验，再原子更新Owner Fence与Gateway路由。新Owner开始写后，Legacy只能通过Go API、事件投影或只读兼容视图读取，不允许直接恢复旧表写入；严重问题优先前向修复，只有尚未产生新Owner写入时才允许完整撤销数据切换。
- 禁止业务双写。对普通写请求，Gateway只调用当前Owner；对需要迁移可观察性的写链路，可执行无副作用的Shadow Validation，例如只运行Schema、授权、风险、Capability或计算逻辑并丢弃结果，但不得创建第二条业务记录、发送第二个事件、调用ThingsBoard、触发Webhook、发送通知、扣费模型调用或改变状态。所有Shadow调用标记`side_effect_policy=NONE`，使用隔离凭证、配额和审计。
- 只读链路允许受控双读影子比对，但用户只收到一个权威响应。Gateway或专用Compare Worker异步调用Shadow Owner，Canonicalize后比较资源集合、排序、数值、单位、时间、水位、质量、授权拒绝和错误码；大响应使用Hash、统计和抽样，不在请求线程内做无界Diff。Shadow超时、失败或差异不能拖慢权威请求，差异进入受限Migration Diff Store并按Organization、Route、版本和原因聚合。
- 只读切流门槛按接口固定，至少包括契约测试全通过、跨租户差异为零、资源集合和授权一致、关键数值/单位/时间无不可解释差异、错误率与p95/p99不劣化、容量满足5倍突发、观测和回滚可用。数值聚合可定义业务容差，但必须解释来源，例如不同最终Watermark或浮点舍入；资源缺失、错误Site、单位错误、静默截断和授权差异不允许以统计容差掩盖。
- Registry/Core迁移先建立平台Organization、Site、Equipment、Device、TelemetryPoint和ExternalBinding，再迁移旧Asset/Device镜像。旧Asset不能自动等同Equipment，旧Device也不能同时代表物理设备和业务设备；转换规则必须由Profile、关系和人工校验支持。无法唯一解析的记录进入Migration Quarantine，不生成猜测绑定。`GET /devices?sync=true`在Gateway兼容层可临时返回明确的同步状态，但不再于读请求内触发ThingsBoard全量同步。
- 身份迁移以外部IAM为认证权威。旧用户与`logto_subject/auth_provider`映射到平台Principal，OrganizationMembership、RoleBinding和SiteBinding通过迁移规则生成并逐批验证；本地密码、微信密码流程、`active_tb_customer_id`和用户直接输入`tbCustomerId`绑定Site逐步关闭。Legacy页面尚未迁移时仍通过Gateway/BFF Session访问，由Gateway提供受限内部身份；不能同时维护两套独立密码和权限事实。权限迁移必须包含拒绝优先、职责分离和跨组织SiteBinding测试。
- Telemetry迁移优先替换读取契约而非复制全部历史进Legacy业务库。新摄取从ThingsBoard Connector进入Kafka、对象存储、ClickHouse和Redis；旧历史若只存在ThingsBoard或Legacy存储，通过版本化Backfill Job按真实Key、单位、Mapping和时间范围导入，并记录`source=LEGACY/TB_BACKFILL`、Binding/Mapping版本、Watermark和质量。未确认的Key、单位、Alarm、Attribute和RPC不得由迁移脚本猜测；票据14的VERIFIED Profile是生产迁移门禁。
- Telemetry latest/history先运行Shadow Compare，再按Site或IntegrationInstance逐批切流。新Query结果必须包含实际分辨率、Watermark、Dataset Revision、Partial和Quality Summary；ACL可将Legacy结果转换为旧Map DTO供未升级页面使用，但不能丢失或伪造质量。旧Telemetry API停止写新缓存后进入只读观察期，最终由Go Query和Realtime统一提供数据。
- Realtime迁移采用REST Snapshot先行、双订阅Shadow和客户端协议显式升级。Gateway可以在兼容期提供旧Socket.IO端点并从新Changelog转换`{deviceId,key,value,ts}`，但新`hvac.realtime.v1`使用WebSocket、Cursor、Sequence、Revision和Resync。不能将同一用户可见更新同时从Legacy和Go推送造成重复；每个连接只选择一个权威Realtime Source，Shadow只在服务端比较。客户端迁移完成、旧连接量归零并通过恢复/背压测试后关闭Socket.IO。
- Command迁移绝对禁止双写和Shadow发送。建立`CommandRoutingAuthority`，按IntegrationInstance、Site或Device Cohort固定唯一Command Owner与生效Generation；同一Device在任一时刻只有NestJS或Go能够接受新Command并调用控制Connector。Gateway在创建前读取权威路由，记录Owner Revision；所有状态查询可由兼容Read Model聚合，但原Command ID、Owner和状态机保持不可变。
- Command灰度只允许VERIFIED Device Binding、Capability Profile、RPC/ACK契约、在线状态投影和审计链路完整的Cohort。顺序为只影子校验Canonical Command与风险、测试设备、内部Site、低风险Capability、小比例生产设备，再逐步扩大。每阶段必须验证幂等、审批、Control Lane、Fence、Connector证据、`OUTCOME_UNKNOWN`、迟到ACK、离线命令和单AZ故障；任何未审批、跨租户、旧Fence或重复RPC成功数必须为零。
- Command切换时先停止目标Cohort在Legacy接受新命令，并让已接受的Legacy Command继续由Legacy收敛到终态；随后分配更高Routing Generation，允许Go接受新Command。不得把Legacy的`PENDING/SENT/TIMEOUT`原地改写为Go状态或由Go重发。历史Legacy Command迁移为不可变History Projection并保留Legacy ID、请求Hash、状态、时间、用户、设备映射和原始证据；需要统一展示时由Gateway转换为Canonical只读DTO，不能伪装成Go原生Command。
- Command回滚只改变未来新请求的Routing Generation。Go已经接受的Command、Attempt、Lease、Fence和可能已发送副作用继续由Go收敛；Legacy已接受的命令继续由Legacy收敛。回滚前必须冻结目标Cohort新请求、对账所有未决Command和Connector证据，再将未来请求路由回仍具备安全能力的Owner。任何系统都不能因回滚盲目重发`REQUEST_COMMITTED`或`OUTCOME_UNKNOWN`命令。
- Scheduler/Automation迁移采用相同单一副作用Owner原则。每个Schedule、Rule或Strategy持有`executionOwner`和Generation；先迁移定义、Target Binding、时区、Next Run和历史记录，影子计算下一执行时间但不触发动作。切换时暂停Legacy Cron注册，确认无未决触发，更新Owner Fence后再启用Go Scheduler。已由Legacy产生的Run由Legacy记录到终态；Automation创建Command只能经Go Command Service，不允许Legacy和Go在同一触发窗口各执行一次。
- AI迁移遵循票据09边界。Investigation、AnalysisRun、Finding、Recommendation和Review的长期权威移至Go AI Platform；EnergyAgent旧内存状态先导出为带来源和版本的历史资源，不能把A/B/C、`b1`、固定Asia/Shanghai或Mock Dataset迁移为生产事实。Copilot Runtime始终经Gateway和Interaction API；新Run通过Tool API读取真实数据。旧Agent可在隔离Shadow环境重放固定Fixture，但不得写正式结果或调用控制。
- 审计历史不可丢失。旧`audit_logs`、Command、Schedule Run、用户、Customer、Device和ThingsBoard外部ID按不可变批次迁移或归档，并创建内容Hash、记录数、Source Watermark、映射版本和访问策略。旧审计若无法达到新Hash Chain保证，必须标记`LEGACY_AUDIT_IMPORTED`及其可验证范围，不能伪称原生防篡改；迁移后所有新操作进入新Audit Ledger。旧Command和审计不得因Device或用户删除级联删除。
- 数据删除和保留政策在迁移中继续生效。回填、CDC、Diff Store、Shadow Payload、导出和临时映射均有Retention Class、Encryption和Owner；不得为了迁移永久复制生产数据。已删除或受Legal Hold资源在Source和Target中保持一致状态，备份恢复后重放Deletion Ledger。Migration Quarantine中的敏感数据限制访问并按政策清理。
- 迁移期间NestJS进入功能冻结：只接受安全修复、生产缺陷修复、迁移适配、观测、Outbox/CDC和契约测试相关变更；禁止新增业务领域、扩展旧DTO、建立新的跨表耦合、引入新的直接ThingsBoard调用或在Legacy数据库创建长期权威。任何Legacy Schema变更需Data Owner和迁移Owner联合审批，并同时更新Data Ownership Registry、转换规则、测试和退役计划。
- Legacy与Go独立部署、扩缩容和回滚，使用不同ServiceAccount、数据库账号、Kafka ACL、Secret、连接池和资源配额。Legacy资源池不得挤占Command、Telemetry Ingest或Go在线查询；Gateway按目标Owner设置独立超时、熔断和限流。Legacy不可用时，已迁移路由继续由Go服务；未迁移路由返回明确不可用，不得临时绕过Gateway直连NestJS或访问其数据库。
- 全链路使用统一Trace Context、Migration Unit、Route Policy Revision、Data Owner Revision、Legacy ID和平台ID关联。Dashboard至少展示每Route流量、错误、延迟、Owner、Shadow差异、CDC Lag、Backfill Watermark、Quarantine、未决副作用、兼容字段使用量和退役倒计时。迁移告警有明确Owner和Runbook；跨租户差异、双写、重复Command、Owner冲突、CDC倒退和无法解释的数据缺失属于高优先级事件。
- 每次Canary和Cutover均需版本化Runbook与审批，包含影响Scope、依赖、预检查、数据库/队列水位、备份、路由Revision、Owner Fence、监控、成功门槛、暂停门槛、回滚步骤、用户沟通和对账查询。切换不与大规模Schema迁移、证书轮换、Kafka分区变更或区域演练同时进行。普通只读路由可以自动回滚；Command、Schedule和数据Owner切换必须人工确认未决副作用与围栏状态。
- 切换成功标准至少包括：契约与安全测试通过；Shadow差异在预先批准阈值内；生产容量和SLO满足；跨租户成功数为零；无双写或Owner冲突；CDC/Outbox尾差清零；备份Restore和路由回滚演练通过；审计和Trace完整；前端无Legacy特有字段新增；值班与Runbook就绪。观察期内按Cohort比较错误、延迟、数据质量、成本、Command结果和用户工单，异常时暂停扩面。
- Legacy Endpoint、字段或表只有同时满足以下条件才可退役：所有已知调用方迁移；Gateway调用量持续归零并覆盖至少一个发布周期；不存在未决Legacy Command、Schedule Run、Outbox、CDC尾差或Legal Hold迁移任务；历史查询已有受支持路径；备份与审计归档验证完成；生产回滚不再依赖Legacy写能力；安全扫描、Secret、DNS、Ingress、数据库账号和Kafka ACL具备撤销计划。普通只读能力建议至少观察30天，Command/控制、身份和审计能力建议至少观察90天并完成一次故障或恢复演练。
- 退役顺序为：阻止新调用和新写；保持只读诊断窗口；清空任务、连接、Outbox和CDC；导出最终Manifest和审计Checkpoint；撤销Gateway Route、Realtime端点、ServiceAccount、证书、Secret、ThingsBoard Token、Kafka ACL和数据库写权限；删除生产Deployment；按Retention Policy归档或删除Legacy数据库与对象；最后移除代码和兼容Schema。不能先删除代码或数据库再验证无人使用。
- 迁移完成定义不是“Go接口能返回200”，而是统一Gateway下所有正式路由由Go平台或明确保留的非Legacy组件拥有，所有业务数据有唯一Owner，Legacy无生产写权限和南向凭证，HVAC Web不含Legacy DTO/Header/Socket.IO依赖，未决副作用已收敛，历史与审计可查询，备份恢复和区域灾备不依赖NestJS，兼容层调用归零并已按计划退役。

## Answer

迁移采用Gateway先行的Strangler模式。`platform-gateway`成为唯一公开入口，通过版本化Route Ownership Registry按路由和Organization/Site/IntegrationInstance/设备Cohort将流量交给Go或内部NestJS；Legacy端口、数据库和ThingsBoard访问不对浏览器暴露。Legacy Anti-Corruption Layer只负责将旧ID、Header、DTO、响应和Socket.IO转换为平台Canonical契约，并以调用量和明确退役条件管理，旧字段不进入Go领域模型。

数据迁移坚持每张表和每个聚合单一写Owner，禁止业务双写。Go使用独立Schema和平台UUID；旧Customer、Asset、Device、User、Command和Schedule通过版本化映射、快照回填、单向Outbox/CDC、尾差追平、验证和Owner Fence完成迁移。只读接口可以异步双读Shadow比较，但用户只收到一个权威响应；写接口只调用当前Owner，Shadow只能执行无副作用校验。数据Owner移交后Legacy降为只读，普通路由回滚不能重新授予其写权限。

Command和Scheduler按副作用Owner与Generation灰度。每个设备Cohort任一时刻只有一个Command Owner；切换时Legacy已接受命令继续由Legacy收敛，Go只接受切换后新命令，回滚也只影响未来请求，禁止重复RPC和对`OUTCOME_UNKNOWN`盲重试。Scheduler先影子计算、停止Legacy Cron并围栏后再启用Go执行。Telemetry先迁移真实数据摄取和只读查询，Realtime由Snapshot+Delta新协议替换Socket.IO；AI长期状态移至Go，EnergyAgent仅执行受控Attempt。

每个迁移单元必须通过语言无关契约、安全与租户测试、Shadow差异、CDC水位、容量SLO、备份Restore、路由回滚和故障注入门禁。NestJS进入功能冻结，只接受安全、缺陷和迁移相关修改。Legacy退役前必须实现调用量持续归零、无未决副作用与CDC尾差、历史和审计可查询、生产灾备不再依赖Legacy，并撤销Ingress、身份、Secret、ThingsBoard Token、Kafka ACL和数据库权限；迁移最终状态是Go平台拥有全部正式业务路由和唯一数据权威，而非简单替换运行语言。
