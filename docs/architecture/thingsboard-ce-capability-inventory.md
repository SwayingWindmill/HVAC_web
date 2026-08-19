# ThingsBoard CE v4.3.1.1 全功能覆盖目录

状态：`CURRENT_AUDIT_INPUT`

用途：为 ThingsBoard 与 HVAC Web 的源码级反向审查定义完整、可复核的能力边界。

非用途：本文不判定 ThingsBoard 或 HVAC Web 哪一方更优，也不把任何现有 HVAC Web 模块视为正确实现。

## 1. 固定参考基线

| 项目 | 固定值 |
| --- | --- |
| 官方仓库 | `thingsboard/thingsboard` |
| 版本 | `v4.3.1.1` |
| 提交 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` |
| 许可证 | Apache-2.0 |
| 本地只读源码 | `C:\Users\HaoZhang\AppData\Local\Temp\thingsboard-v4.3.1.1-src` |
| 机器可读目录 | `contracts/architecture/thingsboard-ce-capability-inventory.v1.json` |

官方功能文档用于发现遗漏和解释入口，源码、测试、迁移与运行配置才用于确认 CE 行为。主要文档入口包括 [User Guide](https://thingsboard.io/docs/user-guide/)、[Connectivity](https://thingsboard.io/docs/user-guide/connectivity-guide/)、[Device Profiles](https://thingsboard.io/docs/user-guide/device-profiles/)、[Calculated Fields](https://thingsboard.io/docs/user-guide/calculated-fields/)、[Command and Control](https://thingsboard.io/docs/user-guide/command-and-control/)、[Dashboards](https://thingsboard.io/docs/user-guide/dashboards/)、[Widgets](https://thingsboard.io/docs/user-guide/widgets/)、[Security](https://thingsboard.io/docs/user-guide/security/overview/)、[Audit Log](https://thingsboard.io/docs/user-guide/security/audit-log/) 和 [ThingsBoard Edge](https://thingsboard.io/docs/edge/)。

## 2. 覆盖规则

以下规则共同构成“没有遗漏功能域”的判定门槛：

1. 如果能力出现在控制器、公共 Domain 数据类型、活动数据库 DDL、UI 页面域、传输模块、Rule Node 注册、应用服务包、部署模块、上游测试或官方文档中的任一处，就必须进入目录。
2. 每个源码信号必须分配一个主审查域；跨域行为通过交叉引用处理，不重复计算主归属。
3. 一个领域只有在生产源码、上游测试、持久化模型、公共接口/UI 入口和失败/边界行为都被检查后，才能完成审查。某一证据面不存在时，必须记录“缺少证据”，不能当作“不需要审查”。
4. 文档中存在、但固定 CE 源码中不存在的能力，只能标记为 `PE_OR_EXTERNAL_SOURCE_UNAVAILABLE` 或 `POST_BASELINE_OR_EXTERNAL`，不能据文档推断其代码语义。
5. 动态注册能力通过注册表、组件描述器、Rule Node 注解、模块装配和运行配置纳入，不要求它必须有独立控制器或表。
6. 所有 HVAC Web 对应实现初始状态均为 `UNVERIFIED`。只有后续裁决票可以给出 `ADOPT`、`ADAPT`、`REPLACE`、`KEEP`、`REJECT` 或 `DEFER`。

## 3. 枚举证据面

| 证据面 | 固定结果 | 覆盖意义 |
| --- | ---: | --- |
| `*Controller.java` | 60 个命名类；去除 `BaseController` 与 `AbstractRpcController` 后为 58 个具体控制器，其中 57 个直接带控制器注解，`AutoCommitController` 为额外具体入口 | 公共管理/API 能力 |
| UI `home/pages` | 32 个页面域 | 操作员和管理员可见能力 |
| `@RuleNode` | 77 个具体节点，分布于 29 个包类别 | 自动化运行能力与扩展面 |
| 活动 `CREATE TABLE` | 66 条声明、64 个唯一表名；另有 1 条注释掉的未来表声明，不计入活动模型 | 持久化 Domain 模型 |
| `application/.../service` | 43 个服务包 | 应用编排和后台运行能力 |
| 核心传输模块 | HTTP、MQTT、CoAP、LwM2M、SNMP 共 5 类 | 南向连接能力 |
| 控制器测试 | 53 个测试类 | 公共 API 行为证据 |
| Rule Engine 组件测试 | 88 个测试类 | 节点语义和异常证据 |
| DAO 测试 | 116 个测试类 | 持久化和查询证据 |
| 应用传输集成测试 | 165 个测试类 | 协议、认证、会话与上下行证据 |
| UI 单元测试 | 1 个 `*.spec.ts` | 明确记录为薄弱证据面，后续不得据此推断 UI 行为充分 |

注意：简单搜索会得到 67 行 `CREATE TABLE`，其中 1 行是注释掉的 `device_profile_firmware` 草案；活动模型的正确数字是 66 条声明、64 个唯一表名。本文采用后者，避免用文本命中数夸大持久化能力。

## 4. 十个完整审查域

### D01 租户、身份、权限与审计

审查票：[审查租户、身份、权限与审计](https://github.com/SwayingWindmill/HVAC_web/issues/238)

- 系统管理员、租户、客户和用户生命周期，以及客户分配和公开访问。
- Tenant Profile 的配额、功能开关、实体/API/资源限制。
- 固定 Authority/Permission 模型和跨租户数据隔离。
- 登录、JWT、激活、密码重置、会话和凭据状态。
- 双因素认证配置与用户 2FA 流程。
- OAuth2/SSO、Domain、注册模板和客户端映射。
- API Key 生命周期与授权边界。
- Audit Log、API Usage、限流和安全事件可追溯性。
- Mail/SMS/Auth/UI/QR 等系统和租户级设置。

### D02 实体、关系、设备与资产档案

审查票：[审查实体模型、关系、设备与资产档案](https://github.com/SwayingWindmill/HVAC_web/issues/237)

- Asset、Asset Profile、Device、Device Profile 的生命周期和默认绑定。
- Device Credentials、注册元数据和 Profile 驱动配置。
- Entity Relation 图、Entity Query/Search 和关系方向语义。
- Entity View 的键裁剪、时间窗口和租户边界。
- 客户分配、共享和公开暴露的实体语义。
- Resource、Image、Component Descriptor 与可重用组件元数据。
- 实体导入/导出、版本控制和自动提交入口。
- Profile 中的 Transport、Rule Chain、Queue、OTA、Alarm 配置交叉边界。

### D03 接入协议、网关、RPC 与 OTA

审查票：[审查接入协议、网关、RPC 与 OTA](https://github.com/SwayingWindmill/HVAC_web/issues/234)

- HTTP、MQTT v3/v5、Sparkplug B、CoAP、LwM2M 和 SNMP 的连接、认证与会话。
- Access Token、Basic、X.509、PSK 等协议适配凭据。
- Telemetry/Attributes 上行入口及其转入应用层的边界。
- 设备活动状态、会话生命周期、断线和重连处理。
- Device Provisioning、Claim 和 Connectivity Check。
- MQTT Gateway API 和核心仓库内的 gateway-device 服务边界。
- Server-side/Client-side RPC、v1/v2 API、持久 RPC、超时与过期。
- OTA 包、设备/Profile 分配、下载和更新状态。
- 外部 ThingsBoard IoT Gateway 与 CE 核心仓库的边界。

### D04 遥测、属性、时序、存储与计算字段

审查票：[审查遥测、属性、时序、存储与计算字段](https://github.com/SwayingWindmill/HVAC_web/issues/245)

- Client/Shared/Server Attributes 的写入、读取、订阅和删除。
- Latest/Time-series 遥测摄取、查询、聚合和删除。
- Key Dictionary、值类型、批量操作和时间戳语义。
- WebSocket 实时订阅、重连与订阅状态。
- Entity Data Query、聚合、分页和过滤。
- Calculated Field 类型、实体/Profile 绑定、链式计算和循环防护。
- Calculated Field 调试、作业/重处理入口和版本授权边界。
- SQL、TimescaleDB、Cassandra、EDQS 等存储/查询路径。
- TTL、清理、保留策略、乱序/重复数据和 Rule Engine 投递。

### D05 Rule Engine、队列、调度与调试

审查票：[审查 Rule Engine、队列、调度与调试](https://github.com/SwayingWindmill/HVAC_web/issues/243)

- Rule Chain/Rule Node CRUD、导入导出、根链和 Profile 绑定。
- Component Descriptor 和动态节点目录。
- Message envelope、消息类型、Metadata 和不可变/可变边界。
- Actor 路由、Relation Type、分支、扇出和嵌套 Rule Chain。
- Node State、处理栈、回调、确认和失败传播。
- Queue、分区、处理策略、顺序、重试、超时和并发控制。
- 执行配额、限流和租户隔离。
- Debug Event、时限调试、统计和可追溯性。
- Delay、Deduplication、Checkpoint/Ack、Transaction 等运行语义。
- Filter/Switch、Metadata、Transform、Math、Geo、Data 和 Action 节点。
- JavaScript/TBEL 脚本执行与沙箱边界。
- Alarm、Notification、RPC、Edge 和外部系统节点。

### D06 Alarm、Notification 与处置状态

审查票：[审查 Alarm、Notification 与处置状态](https://github.com/SwayingWindmill/HVAC_web/issues/239)

- Alarm 聚合、类型、Severity、Active/Cleared、Ack/Unack 和 Assignment。
- Alarm Comment、Entity Alarm 传播、查询、过滤和计数。
- Create/Update/Clear Alarm 节点与幂等、重复、恢复语义。
- Device Profile Alarm Condition 的持续时间、重复、计划和状态。
- Notification Target、Template、Rule、Request、用户 Inbox 和已读状态。
- Email、SMS、Slack、Microsoft Teams、Mobile/Firebase 通道。
- Alarm、API Usage、Device Activity、Edge、Entity、Rate Limit、Resource、Rule Component 和 Task 触发器。
- 调度、去重、失败、重试、升级和 Edge 同步边界。

### D07 Dashboard、Widget、移动端与展示扩展

审查票：[审查 Dashboard、Widget、移动端与展示扩展](https://github.com/SwayingWindmill/HVAC_web/issues/246)

- Dashboard CRUD、导入导出、客户/公开分配和 Home Dashboard。
- Dashboard State、Layout、Entity Alias、Time Window 和 Action。
- Telemetry、Alarm、RPC、Static、Map、SCADA 等 Widget 能力。
- Widget Type、Widget Bundle、自定义 JavaScript/HTML 和资源依赖。
- Image/Resource 与 SCADA Symbol 管理。
- Mobile App、Mobile Bundle、OAuth2/QR 和移动导航配置。
- UI Settings、Home Links 和实时 WebSocket 数据绑定。
- 展示资产的版本控制边界。

### D08 Edge、同步、离线与远程配置

审查票：[审查 Edge、同步、离线与远程配置](https://github.com/SwayingWindmill/HVAC_web/issues/241)

- Edge 实体、凭据、注册、连接和升级。
- Device、Asset、Profile、Rule Chain、Dashboard、Entity View、OTA 等分配。
- `edge_event` 队列、游标和配置/数据变更投递。
- gRPC 会话、全量/增量同步和断点恢复。
- Cloud↔Edge 的数据、配置、Alarm 和 RPC 流。
- 离线本地运行、恢复同步、优先级和背压。
- Edge 本地 Rule Engine、Notification 和 UI 边界。
- 冲突、删除、重复、顺序和 CE Edge 兼容性。

### D09 AI、分析、集成与自动化扩展

审查票：[审查 AI、分析、集成与自动化扩展](https://github.com/SwayingWindmill/HVAC_web/issues/236)

- AI Model CRUD、Provider 配置、凭据和请求生命周期。
- AI Rule Node 与模型输出进入规则消息的边界。
- 固定源码内可验证的 AI 辅助 UI/Calculated Field 钩子。
- Trendz Controller 作为外部分析产品集成钩子，而非 Trendz 实现。
- MQTT、Kafka、RabbitMQ、REST 外部动作节点。
- AWS SQS/SNS/Lambda、GCP Pub/Sub、Azure IoT Hub 节点。
- Email、SMS、Slack、Teams 等外部通知集成。
- Geo、Math 和聚合类分析节点。
- ThingsBoard IoT Gateway、Platform Integrations、MCP 等外部或版本后能力的边界。

### D10 部署、HA、缓存、限流、可观测性与升级

审查票：[审查部署、HA、缓存、限流、可观测性与升级](https://github.com/SwayingWindmill/HVAC_web/issues/235)

- 单体和微服务部署拓扑，以及 tb-node、web-ui、transport、JS/VC executor、EDQS。
- Actor 分区、Cluster、Discovery 和节点间消息。
- Queue 实现、分区、配置、统计、背压和失败恢复。
- Cache/Redis 和缓存一致性。
- DAO 抽象、数据库迁移、更新程序、TimescaleDB/Cassandra 变体。
- Script Executor 隔离、作业、Housekeeper 和 TTL。
- Stats、Lifecycle/Error Event、Health、System Info 和 Usage。
- 速率、资源、实体和 API 限制。
- Entity Version Control、Import/Export 和 Auto Commit。
- 升级、版本兼容、Packaging、Docker、HAProxy 和 REST Client。
- TLS、配置、密钥和运维安全，与 D01 交叉审查。

## 5. 源码信号的主域分配

### 5.1 58 个具体控制器

| 主域 | 控制器 |
| --- | --- |
| D01 | `AdminController`, `ApiKeyController`, `AuditLogController`, `AuthController`, `CustomerController`, `DomainController`, `MailConfigTemplateController`, `OAuth2ConfigTemplateController`, `OAuth2Controller`, `QrCodeSettingsController`, `TenantController`, `TenantProfileController`, `TwoFactorAuthConfigController`, `TwoFactorAuthController`, `UiSettingsController`, `UsageInfoController`, `UserController` |
| D02 | `AssetController`, `AssetProfileController`, `ComponentDescriptorController`, `DeviceController`, `DeviceProfileController`, `EntityQueryController`, `EntityRelationController`, `EntityViewController`, `ImageController`, `TbResourceController` |
| D03 | `DeviceConnectivityController`, `Lwm2mController`, `OtaPackageController`, `RpcV1Controller`, `RpcV2Controller` |
| D04 | `CalculatedFieldController`, `JobController`, `TelemetryController` |
| D05 | `AutoCommitController`, `QueueController`, `QueueStatsController`, `RuleChainController`, `RuleEngineController` |
| D06 | `AlarmCommentController`, `AlarmController`, `NotificationController`, `NotificationRuleController`, `NotificationTargetController`, `NotificationTemplateController` |
| D07 | `DashboardController`, `MobileAppBundleController`, `MobileAppController`, `WidgetsBundleController`, `WidgetTypeController` |
| D08 | `EdgeController`, `EdgeEventController` |
| D09 | `AiModelController`, `TrendzController` |
| D10 | `EntitiesVersionControlController`, `EventController`, `SystemInfoController` |

### 5.2 32 个 UI 页面域

| 主域 | 页面目录 |
| --- | --- |
| D01 | `account`, `admin`, `api-usage`, `audit-log`, `customer`, `profile`, `profiles`, `security`, `tenant`, `tenant-profile`, `user` |
| D02 | `asset`, `asset-profile`, `device`, `device-profile`, `entities`, `entity-view` |
| D03 | `gateways`, `ota-update` |
| D04 | `calculated-fields` |
| D05 | `rulechain` |
| D06 | `alarm`, `notification` |
| D07 | `dashboard`, `home-links`, `mobile`, `scada-symbol`, `widget` |
| D08 | `edge` |
| D09 | `ai-model` |
| D10 | `features`, `vc` |

### 5.3 64 个唯一活动表

| 主域 | 表 |
| --- | --- |
| D01 | `admin_settings`, `audit_log`, `customer`, `tenant_profile`, `tenant`, `tb_user`, `user_credentials`, `oauth2_client`, `domain`, `domain_oauth2_client`, `mobile_app_bundle_oauth2_client`, `oauth2_client_registration_template`, `api_usage_state`, `api_key`, `user_auth_settings`, `user_settings`, `qr_code_settings` |
| D02 | `asset_profile`, `asset`, `device_profile`, `device`, `device_credentials`, `relation`, `entity_view`, `component_descriptor`, `resource` |
| D03 | `ota_package`, `rpc` |
| D04 | `attribute_kv`, `ts_kv`, `ts_kv_latest`, `key_dictionary`, `calculated_field`, `cf_debug_event`, `job` |
| D05 | `rule_chain`, `rule_node`, `rule_node_state`, `rule_node_debug_event`, `rule_chain_debug_event`, `queue`, `queue_stats` |
| D06 | `alarm`, `alarm_comment`, `entity_alarm`, `alarm_types`, `notification_target`, `notification_template`, `notification_rule`, `notification_request`, `notification` |
| D07 | `dashboard`, `widget_type`, `widgets_bundle`, `widgets_bundle_widget`, `mobile_app`, `mobile_app_bundle` |
| D08 | `edge`, `edge_event` |
| D09 | `ai_model` |
| D10 | `tb_schema_settings`, `stats_event`, `lc_event`, `error_event` |

`ts_kv_latest` 和 `key_dictionary` 各在通用实体 schema 与 PostgreSQL 时序 schema 中声明一次，因此 66 条活动声明对应 64 个唯一表名。

### 5.4 动态与后台能力

- 77 个 `@RuleNode` 归入 29 类：`action`, `ai`, `aws`, `credentials`, `data`, `debug`, `deduplication`, `delay`, `edge`, `external`, `filter`, `flow`, `gcp`, `geo`, `kafka`, `mail`, `math`, `metadata`, `mqtt`, `notification`, `profile`, `rabbitmq`, `rest`, `rpc`, `sms`, `telemetry`, `transaction`, `transform`, `util`。
- 43 个应用服务包全部归档：D01=`apiusage/mail/security/session/sms/user`；D02=`asset/component/device/entitiy/gateway_device/profile/query/resource`；D03=`lwm2m/ota/rpc/transport`；D04=`cf/job/subscription/telemetry/ttl`；D05=`action/executors/queue/rule/ruleengine/script/state`；D06=`notification`；D07=`mobile`；D08=`edge/sync`；D09=`ai`；D10=`edqs/housekeeper/install/partition/stats/system/update/ws`。
- Actor 包 `app`, `calculatedField`, `device`, `ruleChain`, `service`, `shared`, `stats`, `tenant` 主要由 D04、D05、D10 联合审查。
- 微服务/部署模块 `tb`, `tb-node`, `web-ui`, transports, `js-executor`, `vc-executor`, `edqs`, monitoring 与 black-box tests 由 D10 主审查，各业务域验证其对应运行路径。

## 6. 版本与产品边界

| 能力 | 目录状态 | 原因 |
| --- | --- | --- |
| ThingsBoard IoT Gateway 的 Modbus、BACnet、OPC UA 等连接器 | `EXTERNAL_OPEN_SOURCE_COMPANION` | 独立开源仓库；CE 核心仅包含 gateway-device 服务和 MQTT Gateway API 等衔接点 |
| Platform Integrations | `PE_OR_EXTERNAL_SOURCE_UNAVAILABLE` | 官方文档存在，但固定 CE 核心源码不含完整实现 |
| Advanced RBAC / Entity Groups | `PE_OR_EXTERNAL_SOURCE_UNAVAILABLE` | 不以文档描述推断专有实现语义 |
| White Labeling / Reporting | `PE_OR_EXTERNAL_SOURCE_UNAVAILABLE` | 固定 CE 源码没有可完整审查的实现 |
| Trendz | `EXTERNAL_COMMERCIAL_PRODUCT` | CE 有 `TrendzController` 集成钩子，不包含 Trendz 本体 |
| Calculated Field 历史重处理 | `ENTITLEMENT_REQUIRES_DOMAIN_REVIEW` | CE 有 Calculated Field 与 Job 基础代码，但版本/授权行为需 D04 以源码和测试确认 |
| 文档中的 MCP Server、预测性维护等较新能力 | `POST_BASELINE_OR_EXTERNAL` | 固定 `v4.3.1.1` 源码未找到相应核心实现，不能反向投射到该版本 |

## 7. 完成条件与后续顺序

本目录只证明“审查对象已覆盖”，不证明任何能力应被采用。接下来的十个领域票必须逐项产出：它解决的问题、Domain 模型、核心流程、关键代码结构、异常/边界、值得吸收的 Pattern、不适合 HVAC 的部分、与 HVAC Web 的映射，以及带证据的裁决。

领域审查完成后，才进入 [反向审查 HVAC Web 全部对应模块](https://github.com/SwayingWindmill/HVAC_web/issues/242)、[裁决跨功能冲突并确定目标 Domain 模型](https://github.com/SwayingWindmill/HVAC_web/issues/240) 和 [生成分阶段替换与实施路线](https://github.com/SwayingWindmill/HVAC_web/issues/244)。在这些票完成前，已有实现仍是 `UNVERIFIED`，不得因为“已经写完”获得优先权。
