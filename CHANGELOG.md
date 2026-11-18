# 更新记录

记录对使用者可见的变化：功能、页面与运行行为。格式参照 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
项目尚未正式发布，所有变化都在「未发布」下按日期记录；每条注明对应的 PR。

## 未发布

### 2026-11-18

#### 修复与加固

- 完成实时遥测管道与冷热分级存储 P0 安全边界与稳定性加固（`modules/connectivity`、`modules/telemetry`、`modules/maintenance`、`cmd/connectivity`、`cmd/telemetry-worker`、`cmd/maintenance-worker`、`deploy/platform/phase1/compose.yaml` 与迁移 `telemetry-history-dead-letter-and-retention.sql`、`017-retention-cleanup-grants.sql`）：
  - **断网补传隔离与快照防回滚**：接入层识别 `replay: true` 或超过滞后阈值（默认 `10m`）的历史报文并固化入队接收时刻，为补传报文分配基于消息的确定性 `mqtt-replay:` 分区（避免重启重置或重试串分），不覆写最新状态快照；`EvaluateHistoricalObservation` 兼顾边缘续传路径仅归档不触发实时推送、在线态更新或阈值告警；Redis 热缓存 Lua 脚本在版本校验与点位配置集合比对下通过 `sampled_ms` 阻止陈旧样本回滚最新工况，并设置 7 天 TTL。
  - **背压保护、毒丸隔离与停机排空**：`SharedSession` 透传 `ReceiveMaximum` 接收上限且单网关限流不误断共享连接；入库 `400` 失败按已提交分片跳过重发并逐条隔离，`HistoryRelay` 使用独立超时上下文结算租约、遇到 ClickHouse 永久错误执行二分拆分并把毒丸与失效/超大记录转入 `DEAD` 状态；调整默认攒批窗口为 `100ms` 并在停机时保护未耗尽溢写目录，优先停收再多轮排空中继并延长 `stop_grace_period` 至 `45s`。
  - **双库过期清理与生命周期审计**：PostgreSQL 补齐死信发布/历史发件箱、MQTT 运行证据、解析隔离、恢复游标、订阅与超过 7 天的 `mqtt-replay:%` 源位点清理策略及部分索引；ClickHouse 支持按环境变量开启分表滚动清理并跳过依法保全租户/站点，且在 `maintenance-worker` 新增生命周期策略与保全冲突定期审计任务。

#### 新增与架构演进

- 完成 Go 端 AI 调控影子试运行、多副本防重入与端到端追踪告警闭环 P1（`libs/intelligencemodel`、`modules/registry`、`modules/iam`、`modules/alarm`、`services/optimization-service`、`cmd/energy-api`、`contracts/`、`apps/hvac-web` 与迁移 `optimization-shadow-and-trace.sql`）：
  - **影子跟跑（`SHADOW`）与 IAM 双重防线**：租约与执行监控新增 `executionMode`（`CLOSED_LOOP` / `SHADOW`）；开启 `SHADOW` 时，优化控制周期照常执行实时点位新鲜度检查与物理护栏评估，在最后一跳拦截仅写入 `SHADOW_RECORDED` 对照记录而绝不下发网关指令，且 IAM 授权侧同样对非回退 `SHADOW` 租约无条件拦截兜底。
  - **多副本站点防重入与全链路决策追踪**：为优化执行作业接入 PostgreSQL 站点级非阻塞咨询锁防止多副本同站点周期踩踏；在推理、护栏、执行监控与下行命令之间贯通 UUIDv7 `decision_id` 与 W3C `trace_id`（`traceparent`）传播链。
  - **设定值收敛偏差监测与安全告警联动**：超过 10 分钟收敛窗口后若反馈设定值仍明显偏离下发目标即判定 `setpoint_tracking_deviation` 并触发自动回退，同时向 `alarm-service` 推送 `SETPOINT_TRACKING_DEVIATION` 与 `AUTO_CONTROL_ROLLBACK_FAILED` 安全事件；前端租约弹窗新增运行模式切换卡片及影子状态中文标注。

- 完成 Go 端 AI 自动调控物理安全护栏与推理韧性升级 P0（`libs/commandmodel`、`libs/intelligencemodel`、`modules/telemetry`、`modules/registry`、`modules/iam`、`modules/command`、`cmd/energy-api`、`services/optimization-service`、`contracts/`、`apps/hvac-web` 与迁移 `optimization-safety-guardrails.sql`）：
  - **纯 Go 确定性物理安全护栏与推理降级**：在 `libs/commandmodel/guardrails.go` 与 `diagnostics.go` 实现八段确定性护栏链（点位 90 秒新鲜度与防未来时间戳校验、故障报警与就地/手自动极性一票否决、人工退避拦截、水路与启停间隔互锁、`>= 5.0 °C` 冷冻水防冻硬下限钳位、单步爬坡限幅以及调节死区过滤）；`libs/intelligencemodel/resilience.go` 新增三态熔断器、协程池舱壁并发限制与 3 秒默认超时无扰降级。
  - **全链路实时点位取数与控制权仲裁闭环**：扩展 `modules/telemetry` 与 `optimization-service` 从 `telemetry_runtime` 直接读取实时传感器数值、质量与版本修订替代 15 分钟滞后快照，并彻底清除下游伪造 `GOOD`/`1`；操作员下发手动指令及现场手调水泵/主机设定值或切入就地手动时自动施加 2 小时人工退避锁；通过站点事务锁 + `PENDING_SUBMIT` 原子预占与崩溃超时回收消除并发超频与进程中断裂脑；监控窗口新增供水防冻下限及设备故障保护回退，且 `auto-rollback:` 合规通道可在断采/大幅漂移工况下执行 `EMERGENCY_BYPASS` 回退同时拦截非回退越权提权。
  - **OpenAPI 契约与自动控制界面同步**：收紧供水温度设定下限至 `5.0 °C`、新增租约「调节死区」编辑边界、可空指令标识（`PENDING_SUBMIT`/`SHADOW_RECORDED`/`SUBMIT_FAILED`）与护栏限幅/退避提示文案并更新生成契约。

- 完成设备接入与边缘自治 P2 改造（`libs/mqttclient`、`libs/edgecontrol`、`modules/connectivity`、`modules/registry`、`modules/command`、`cmd/connectivity`、`tools/eg8200-simulator`、`apps/hvac-web` 与迁移 `gateway-shadow-drivers-and-autonomy.sql`）：
  - **南向传输统一、多版本编解码与会话防呆**：抽出共享包 `libs/mqttclient`（TLS 1.3 双向认证、代数隔离连接跟踪、SUBACK 重试、`$share` 过滤与单连接多路由 `SharedSession`），使接入主服务与集中器通道复用同一长连接；`adapter` 新增可插拔编解码注册表（兼容标准 `2.0` 与紧凑批量 `1.0`）、基于会话 ID / 序号 / 时间戳的网关在线状态防抖器（拦截延迟遗嘱覆写新连接）以及解包前置的每网关令牌桶限流（超限写入 `RATE_LIMITED` 隔离并带 5 秒冷却抑噪，上报指标 `hvac_mqtt_gateway_rate_limited_total`）。
  - **声明式 Modbus 驱动与影子热加载**：扩展 `connectivity.gateway_desired_configs` / `gateway_reported_configs` 与 OpenAPI / Zod 契约支持 `driverConfigs`（FC3/FC4 相邻合并读取、优先级分频轮询、故障线性退避、FC6/FC16 编码写与字字节序）和 `autonomyPolicy`（局部保留 / 置空清除的三态局部更新与乱序漂移对比），同时支持 `DEVICE` 模板附加可选 `modbusDriver` 描述；边缘运行时与模拟器在收到配置影子变更后无需重启即可热替换驱动与自治参数。
  - **本地水力互锁、断网自治与分级续传**：`libs/edgecontrol` 新增水力互锁控制器（泵运行/流量联锁与最小启停保护、频率钳位）与断网兜底控制器（失联超过宽限期切入本地安全设定，重连后平滑交还云端）；模拟器支持数值变化触发与周期全量快照混合上报，并基于 `FileTimedata` 与持久化游标在重连后按 `VERY_HIGH -> HIGH -> MEDIUM -> LOW` 优先级断点续传离线积压数据（标记 `replay: true` 并累计 `hvac_edge_mqtt_replay_batches_total`）。
  - **网页端运维配置与诊断联动**：站点网关抽屉新增「协议驱动点表」与「断网自治策略」结构化展示、校验和局部提交能力，并补齐 `RATE_LIMITED`（「上报速率超限」）隔离展示与 `DEVICE` 模板 Modbus 驱动概览标签。

### 2026-11-17

#### 修复与加固

- 修复规则触发、通知投递、优化监护、定时调度与控制链路中的生产级阻塞隐患：
  - 遥测评估中继（`modules/telemetry`、`cmd/telemetry-worker` 与迁移 `alarm-evaluation-dead-letter.sql`）增加单次投递超时上限、指数退避与永久错误哨兵，连续失败超限或不可重试错误转入死信队列并清空租约，消除毒丸事件无限重放；
  - 定时调度（`cmd/scheduler`、`modules/scheduler`）消除环境变量配置解析时的直接 panic，并对周期作业写入补全按调度定义串行互斥的幂等去重；
  - 告警通知与微信提醒（`cmd/energy-api`、`services/notification-service`）为后台消费协程补全 panic 恢复与退避、有界批次排空与外部请求超时隔离，回收中断在 `SENDING` 状态的过期或空租约、钳制重复目标与边界配置，并保护重放事件不覆盖原调度位点；
  - 运行优化自动控制监护（`services/optimization-service` 与迁移 `optimization-monitor-lease.sql`）引入分布式原子租约认领与过期接管校验防止多副本重复回滚，并为优化服务主进程补全优雅信号关闭与协程恢复保护；
  - 设备控制失败告警（`modules/command` 与迁移 `control-alarm-outbox.sql`）改为与状态变更同事务落库发件箱并由持久化中继器投递，消除队列打满或进程重启丢失安全告警的隐患；边缘命令执行器（`edge/node-red/songyaxuan-command-executor.ts`）修复序列表延迟中止时的定时器泄漏，并串行化受理准入、持久化回滚与序列中止清理。

#### 新增

- 完成告警规则高阶编排、因果图校验与多步模拟试算能力（对应 P2-1 规则表达与 DAG 编排）：
  - 表达式引擎、趋势斜率窗口与多级模板（`modules/alarm` 与迁移 `alarm-evaluation-slope-window.sql`）：新增受控递归下降 AST 三值表达式条件 `EXPRESSION`（上限 64 节点 / 16 深度 / 16 个输入别名，支持 `abs`/`min`/`max` 与除零不确定态隔离）、滑动时间窗趋势斜率条件 `SLOPE` 与持久化字段 `alarm_runtime.alarm_evaluation_state.sample_window`，以及模板多层继承覆写（`overriddenFields`）与规则级前置依赖解析。
  - 因果有向无环图校验与防环阻断（`modules/alarm/pkg/rulegraph`、`alarmrules`、`alarmservice`）：引入 `ValidateDAG` 结合显式依赖与设备读写足迹检测环路与最大级联跳数（`MaxLinkageHops = 3`），在规则编译加载与站点规则同步阶段阻断环路与越界级联。
  - 纯内存规则试算接口与网页工作台（`modules/alarm`、`cmd/energy-api`、`contracts/`、`apps/hvac-web`；路由注册表 `route-ownership.v1.json` 升级至第 `56` 版）：新增受 CSRF 保护的 `POST /api/v1/alarm-rules/simulate` 接口，在不写入告警状态或发布真实告警的前提下逐步推演触发与恢复过程；网页端「正在生效的规则」新增规则来源/模板派生与因果联动跳数展示，并提供「模拟试算」右侧抽屉进行多步采样推演。

- 完善告警全生命周期治理、规则热更新、控制指令优先级仲裁与告警通知根因降噪能力（对应 P1 规则告警域演进）：
  - 告警全生命周期与评估追踪（`modules/alarm`、`libs/alarmmodel`、`libs/alarmauth`、`cmd/energy-api`、`apps/hvac-web`）：新增取消认领（`UNASSIGN`）、检修静默与到期自动解静默（`SUPPRESS` / `UNSUPPRESS` / `SweepExpiredSuppressions`），补全规则评估历史持久化（`alarm_runtime.alarm_evaluation_event`）与预触发评估追溯接口 `GET /api/v1/alarms/{alarmId}/evaluations`，支持按站点热更新告警规则并保留未修改规则的在途评估状态与自动消警已下线规则；网页端「告警管理」补全「已静默」筛选视图、取消认领、检修静默弹窗及评估历程明细区。
  - 控制指令优先级分级仲裁与设备优先级锁（`libs/commandmodel`、`modules/command`、`cmd/energy-api`、`apps/hvac-web`）：定义从安全联锁（`SAFETY_INTERLOCK`）、手动干预（`MANUAL_OVERRIDE`）、本地保护（`LOCAL_PROTECTION`）、AI 优化（`AI_OPTIMIZATION`）到定时场景（`SCHEDULED_SCENE`）的五档优先级与指令来源分类，支持高优先级自动抢占或拒绝低优先级指令与 `device_priority_locks` 独占锁，并将新不变量 `P7` 纳入 `command-safety` 形式化检验；控制日志与设备指令抽屉同步展示优先级与来源标签并提示抢占锁定报错。
  - 告警通知根因抑制、静默期降噪与频次控制（`services/notification-service`）：发件箱投递联动同步告警静默窗与退避抑制，引入按路由级冷却间隔及 `DEVICE_ROOT_CAUSE` 父子设备根因抑制关系过滤网关断网等次生离线告警风暴。

- 新增网关通信抓包、上行接收隔离排查接口与网关详情抽屉诊断视图（`infra/connectivity/postgres/init/uplink-quarantine-diagnostics.sql`、`modules/connectivity/pkg/packettap`、`cmd/connectivity`、`cmd/energy-api` 与 `apps/hvac-web`；网关路由注册表 `route-ownership.v1.json` 升级至第 `54` 版）：
  - 新增平台网关与连接服务诊断接口 `GET /api/v1/gateways/{gatewayId}/quarantine`、`GET /api/v1/gateways/{gatewayId}/packet-capture`、`POST /api/v1/gateways/{gatewayId}/packet-capture/start`（支持 `30` 至 `900` 秒抓包时长与最多 `32` 路活跃抓包上限）与 `POST /api/v1/gateways/{gatewayId}/packet-capture/stop`。
  - 迁移 `uplink-quarantine-diagnostics.sql` 为 `connectivity.uplink_quarantine` 补充 `occurrence_count`、`last_seen_at`、`payload_preview`（`<= 4096` 字节）、`payload_encoding`（`utf8`/`hex`/`empty`）与 `payload_truncated` 字段及清理函数 `connectivity.prune_expired_uplink_quarantine`；高频突发异常报文按冷却窗口合并累计次数，连接服务启动时及每小时自动清理超过 `7` 天的旧记录，并在优雅停机阶段刷写未落盘计数。
  - 站点网关详情抽屉（`GatewaySheet`，宽度由 `480px` 调整为 `640px`）新增「通信抓包」与「接收隔离」折叠面板，运维人员可在页面按网关启停抓包、按通道或方向筛选报文、复制原始载荷摘要并查看上行隔离记录及累计次数。
- 网页端补齐网关配置影子、模板目录激活、多维过滤与标签运维界面（对应 P1-2 与 P1-3，#883）：
  - 「集成管理」网关详情抽屉（`GatewaySheet`）新增「配置影子」分区，支持查看云边同步状态（未配置 / 待同步下发 / 云边已一致 / 配置存在漂移）、期望与边缘已上报修订号、漂移明细与集中器暂存单元汇总，并可通过结构化弹窗编辑点位映射与采集周期（保留集中器离线配置并处理并发修订冲突刷新）；「无线集中器」参数及 200 槽 EID 提交确认框新增「若网关离线，暂存至配置影子（重连后自动同步）」勾选框与 `STAGED_SHADOW` 状态联动。
  - 「系统管理 → 设备与点位登记」的「模板版本」新增持久化租户模板目录、`DEVICE` 模板结构化配置器（适用设备类型、标准点位与质量检查规则、控制能力档位及键值化发布引用）、版本内容预览、载入重编以及带筛选分页的「站点已应用模板」记录表；「资源与绑定」新增资产/设备键值对标签（`tags`）管理、可点击递归「空间层级」筛选，以及状态、模板、标签与「全部授权站点」跨站点组合检索。
  - 运维「设备」页面新增基于设备与绑定资产标签的 URL `?tag=` 筛选下拉框，设备详情抽屉新增「标签」徽章展示。
- 新增网关与无线集中器配置影子同步链路（`infra/connectivity/postgres/init/gateway-config-shadows.sql`、`modules/connectivity`、`cmd/energy-api` 与 `tools/eg8200-simulator`；网关路由注册表 `route-ownership.v1.json` 升级至第 `53` 版）：
  - 新增租户行级隔离表 `connectivity.gateway_desired_configs` 与 `connectivity.gateway_reported_configs`，提供运维读写接口 `GET/PUT /api/v1/gateways/{gatewayId}/config-shadow`（支持 `expectedDesiredRevision` 乐观并发锁、部分字段 `PUT` 局部更新、集中器 RS485 保护地址与重复 EID 校验、归一化顺序无关漂移对比 `UNCONFIGURED` / `PENDING_SYNC` / `SYNCED` / `DRIFTED`）及网关 mTLS 增量拉取与回执接口 `GET /gateway/v1/config-shadow`、`POST /gateway/v1/config-shadow/report`。
  - 无线集中器参数与 EID 槽位写入新增 `stageIfOffline` 离线暂存能力与 `STAGED_SHADOW` 审计结果；写影子成功（针对在线网关）或收到网关 `GATEWAY_CONNECTED` 上线事件时按单网关串行异步对账并将集中器差异重放至边缘设备，离线换址保留当前总线地址待上电写入后再重键，未建基线的局部离线参数写入拒绝兜底默认值，失败时在影子中保留部分成功状态与漂移明细。
  - `eg8200-simulator` 增加配置影子持久化状态与重连/定期增量同步（先成功上报回执再提交持久化修订号），并按生效影子中的 `pointMappings`（单源主点位与 `copyTargets` 复制映射）和 `samplingPolicies`（逐点与通配发布节流）生成并上送遥测信封。
- 为资产与设备补充标签（`tags` jsonb + GIN 索引）、多维度/跨站点检索、模板只读接口与模板版本业务激活闭环（P1-3）：
  - 新增迁移 `registry-tags-and-template-read.sql`（`hvac_s1`）与 `command-template-capability-sync.sql`（`hvac_s3`）：为 `core_registry.assets` 与 `core_registry.devices` 增加最多 64 个键值对、序列化不超过 8192 字节的 `tags` 列与 GIN 索引；向运行时只读角色 `s1_core_runtime` 授予模板表（`registry_templates`、`registry_template_revisions`、`registry_template_assignments`）租户及站点行级隔离的只读权限；向 `s3_command_runtime` 授予 `command_runtime.capability_profiles` 写入权限。
  - 平台网关与契约新增跨站点检索接口 `GET /api/v1/assets`、`GET /api/v1/devices` 与站点内/跨站点多维过滤参数（`siteId`、`status`、`assetType`/`deviceType`、基于空间树递归查询的 `spaceId`、`templateKey`、`templateRevisionId` 与可重复的 `tag=key:value`/`tag=key`），并将过滤条件规范编码绑定到分页游标 `cursor` 防跨条件串页；同时新增模板只读接口 `GET /api/v1/templates`、`GET /api/v1/templates/revisions/{revisionId}` 与 `GET /api/v1/sites/{siteId}/template-assignments`，网关路由注册表 `route-ownership.v1.json` 升级至第 51 版。
  - `TemplateRevision` 发布新增对可选 `deviceType`、`standardPoints`、`qualityChecks` 与 `capabilityProfiles` 段的严格校验（保持原有参数模板兼容）；`AssignTemplate` 校验目标资源存在且未退役、设备型号匹配，并在同一可序列化事务内把 `standardPoints` 与质量检查规则物化写入 `core_registry.telemetry_points`；`OutboxRelay` 中继 `registry.template-revision.released` 与 `registry.template-assignment.changed`，向遥测服务 `ApplyRegistrySync` 传入模板上下限与 `qualityChecks`（非模板同步留空时保留运维手工配置，不递增 `policy_revision`），并通过新增的 `POST /internal/v1/capability-profiles:sync` 把模板控制能力档位同步到 `command_runtime.capability_profiles`。
- 引入共享语义设备群孪生库 `libs/planttwin`（`SiteTwin`、`HydraulicGraph`、`EvaluateOperatingSchedule` 及单事务只读视图适配器 `PostgresLoader` / 测试桩 `StaticLoader`），并新建增量迁移 `plant-twin-relationships-and-roles.sql` 与 `ADR 0033`：
  - 新增 `core_registry.asset_relationships`（`SERVES` 水力服务有向无环连接）与 `core_registry.point_subject_bindings.semantic_role`（11 类物理语义角色闭合枚举），配套 10 个按租户与授权站点行级隔离的只读视图 `core_registry.plant_twin_*_v1`。
  - `deploy/sites/songyaxuan.site.json` 补全 16 条水力连接与点位语义角色，`scripts/site-onboarding.mjs` 与 `scripts/phase1-central-plant-simulator.mjs` 同步校验、幂等下发并检测漂移。
- 增强 `modules/connectivity`、`cmd/connectivity` 与 `cmd/energy-api` 的 MQTT 上行队头隔离、单证吊销与无损密钥轮换能力（新增迁移 `gateway-certificate-revocation.sql` 与 `route-ownership.v1.json` 修订 `52`）：
  - 上行订阅支持 MQTT v5 `$share/<group>/...` 共享订阅（`CONNECTIVITY_MQTT_SHARE_GROUP`，默认 `connectivity`），并用非阻塞每网关内存环形队列加磁盘溢出缓存（`gatewayMailbox`、`CONNECTIVITY_OVERFLOW_DIR`、`CONNECTIVITY_OVERFLOW_MAX_MESSAGES`）替代阻塞通道，消除慢网关对共享 MQTT 读循环的跨网关队头阻塞，溢出装满时留由 Broker 重传并输出 `hvac_mqtt_gateway_overflow_depth` 与 `hvac_mqtt_gateway_overflow_shed_total`。
  - 新增网关证书列表、单证按 ID 吊销、按证书指纹精准踢下线反向运维隧道会话（`CloseGatewayByFingerprint`）、无损重放轮换（`POST /api/v1/gateways/{gatewayId}/rotate` 及续期参数 `revokePeerCertificate`）与已吊销序列号签名 CRL 导出（`/internal/v1/gateways/ca.crl`）。
- 完善网关详情抽屉（`GatewaySheet`）证书生命周期管理界面：新增「已签发证书」列表与有效、已过期、已吊销状态展示，支持按单张证书弹窗确认吊销并填写可选原因，支持保留当前有效证书的「轮换密钥」，并在网关停用后支持通过「重新启用并生成接入码」恢复接入。

#### 变更

- 完成南向 MQTT 设备接入、指令调度与可观测性 P1 扩展改造：
  - 连接池、单飞缓存与凭证短缓存：`modules/connectivity` 支持通过 `CONNECTIVITY_DB_MAX_CONNS` 配置数据库连接池上限（默认 `32`，范围 `1` 至 `256`）；`ResolveGateway`、`ResolveDevice` 与 `ResolveBindings` 引入基于 `singleflight` 的并发请求合并，凭证校验引入 `15` 秒活跃凭证短缓存并在吊销、签发或退役时实时清除对应缓存。
  - 多副本快速感知、自适应退避与分阶段优雅停机：`mqttconnector` 在启用共享订阅组（`$share/command-dispatcher/...`）时通过 `LoadCommandCorrelation` 每 `500` 毫秒感知同组其他副本落盘的控制结果；`cmd/connectivity` 将空闲租户轮询退避从固定 `100` 毫秒调整为 `100` 至 `1500` 毫秒指数退避（租户列表 `5` 秒刷新），并在停机信号到来时先将 `/health/ready` 与 `/health/telemetry/ready` 置为 `503`、停止拉取新任务，再保留最长 `30` 秒窗口完成在途指令与上行队列排空。
  - 全链路追踪、指标治理与边缘断点日志：`adapter`、`cmd/telemetry-worker`、`concentrator` 与 `mqttconnector` 完整接入 `Observability` OTLP Tracer、自动 W3C `traceId` 生成与 `packettap` 抓包回调，`deploy/observability/phase1-metric-catalog.v1.json` 补齐对应 MQTT 与集中器指标，并在网关或通道空闲移除时调用 `DeleteGauge` 回收过时指标序列；`eg8200-simulator` 控制执行记录改为追加式 JSONL 预写日志（`command-execution-records.wal`，达 `1024` 条自动压缩并保留各设备最大 `executionFence`）。
- 完成内部架构边界收敛与 Go Workspace 模块整合，消除跨库表直读并收紧构建与静态检查门槛：
  - `optimization-service` 与 `telemetry-worker` 新增内部 mTLS 工作负载点位健康核验接口 `POST /internal/v1/telemetry/optimization/point-health:check`，移除优化服务对 `telemetry_runtime` 表的跨模块直读；`libs/intelligencemodel` 与 `services/optimization-service` 将运行优化建议、影子评估与作业输出全部收敛为强类型结构体。
  - 新增 `hvac_s1` 迁移 `iam-registry-read-views.sql`，补齐 `core_registry` 只读合约视图（`sites_v1`、`iam_requested_devices_v1`、`site_auto_control_leases_v1`、`optimization_execution_monitors_v1`）并回收 `s1_iam_runtime` 对 `core_registry` 基表的直读权限，`modules/iam` 全面切换至 `*_v1` 合约视图。
  - `npm run ownership:check` 新增静态边界检查（`scripts/check-ownership-boundaries.ts`），拦截跨模块非 `*_v1` SQL 读写、平级模块互引与优化核心包非测试代码的 `map[string]any`。
  - 引入仓库根级 `go.mod` 并将 `go.work` 由 61 个子模块收敛为 9 个（主仓根模块与独立契约/边缘模块），同步更新构建脚本、PR 门禁分类与容器镜像构建定义。
- 为后台常驻协程补齐统一的 panic 恢复与全链路南向分布式追踪透传：
  - `libs/observability` 新增 `Recover`、`GoSafe` 与 `(*Runtime).Go`，捕获后台协程 panic、输出脱敏错误日志并累加 `hvac_goroutine_panics_total`；`cmd/*`、`modules/{alarm,energy,iam,maintenance,registry,telemetry,workorder}` 后台循环与连接隧道协程统一接入恢复保护。
  - `modules/connectivity` 将 MQTT 上行接收改为按网关 UUIDv7 分桶的定长协程池（默认 `32` 路、`4096` 个追踪队列上限、空闲队列回收与非阻塞 `time.AfterFunc` 重试），并将上行报文 `traceId` 与 HTTP `traceparent` 透传至 `telemetry-worker` 实时接收及运行事件证据链路。
  - `modules/command` 新增迁移 `command_intent_trace_context.sql` 保存命令意图 `trace_id`，打通平台网关与 `command-service` HTTP `traceparent` 到 `DispatchEnvelope` 及下行 MQTT 命令报文 `traceId`（幂等哈希保持与 `traceId` 无关），并用带 `256` 容量上限、panic 恢复与停机排空的 `controlFailureQueue` 替换裸异步控制失败通知协程。
- 收口 AI 运维助手（`services/operations-agent-service`）与平台网关（`cmd/energy-api/internal/gateway`）的运行期站点快照与工具授权边界，并完成 `#641` `pi-codemode` 实测取舍：
  - 引入按单次问答运行期（`RunSiteSnapshot`）单飞复用的站点上下文与懒加载设备点位模型：每次问答至多查询 1 次 `registry.getSite`，仅在读取设备、时序、告警、故障诊断或工单时按需查询至多 1 次 `registry.getSiteAssetModel` 并附上 `SERVES` 上下游关联，全站能耗和拒答类提问不再多余查询资产模型。
  - 网关 `/internal/v1/operations/tool-authorization` 改用声明式工具授权分发表 `operationsToolAuthorizers` 并与 `contracts/http/operations-tool-authorization-internal.openapi.yaml` 对齐全部 10 个只读工具；将单次分析的 `operations:investigate` 服务代理授权有效期延长至 180 秒（经 `ValidateDelegationAnyScopeWithMaxTTL` 限制不超过 5 分钟，每次工具授权仍实时核验会话未吊销，下游只读服务短期授权维持 30 秒不变），并用 `OWNER_GRANT_WIRE_POLICY` 统一 4 种下游认证头写法。
  - 基于 12 道松雅轩评测题 × 3 轮 × 2 模式的 72 轮真实 `deepseek-flash` A/B 对照评测，因 `pi-codemode` 未提高整题通过率（同为 `9/12`）且平均耗时（`8.18s` vs `7.83s`）、读取次数（`2.86` vs `2.56`）和令牌（`2939` vs `2768`）均增加，决定保持直调模式不引入 `@earendil-works/pi-codemode`（#641），结果记入 `docs/architecture/pi-agent-source-review.md`。
- 将运行优化（`services/optimization-service`）、故障诊断（`services/fdd-service`）与控制门禁（`modules/command`）收口到 `libs/planttwin`：
  - 删除 `SongyaxuanChillerMeterBindings()` 硬编码、多键 `run_state` 猜测、按 `deviceId` 强绑定的四信号假设，以及优化与控制侧从 `interlockChecks` 逆推 `SERVES` 的兜底逻辑；改为按 `SemanticRole` 跨拆分设备（电表、温度集中器、热量表）与 `HydraulicGraph` 解析设备与点位。
  - 将优化备选、诊断工况门控与控制时段检查统一收口至 `EvaluateOperatingSchedule`（统一 IANA 时区、预冷/延后缓冲与已批准运行例外）。

#### 修复

- 修复南向 MQTT 设备接入、控制指令回执与遥测覆盖度的P0可靠性与安全问题：
  - 命令连接器（`modules/command/pkg/mqttconnector` 与 `cmd/connectivity`）：回执订阅默认启用 MQTT v5 共享订阅（`$share/command-dispatcher/...`）与按实例生成的唯一 `ClientID`，避免多副本重复消费；改为手动 `PUBACK` 配合按网关哈希分片的 16 路工作池（先将 `REPLIED` 持久化进数据库再向 Broker 确认，持久化瞬时失败主动断连触发重投，畸形、超限或无匹配载荷直接确认避免阻塞），`SUBACK` 拒绝时有限重试并断连重订阅；为可序列化事务补充 `40001`/`40P01` 指数退避重试，`GatewayTenant` 优先命中正缓存。
  - 上行适配与运维通道（`modules/connectivity` 与 `cmd/connectivity`）：`adapter`、`concentrator` 统一经由带 `SUBACK >= 0x80` 校验的重试与失败断连路径完成订阅恢复，`cmd/connectivity` 主连接默认按主机名派生唯一 `ClientID`；遥测上行 `429` 限流退避改为按单台网关隔离处理，不再把整实例就绪状态标为失败；集中器 RPC 请求增加 MQTT v5 `MessageExpiry` 防止断网陈旧参数指令延迟重发，并在操作网关前校验证书确处于未过期、未吊销状态；隔离入库详情按 UTF-8 字符边界截断。
  - 遥测覆盖度（`modules/telemetry` 与 `contracts/mqtt`）：60 秒覆盖度心跳携带 `gatewayScopes` 按网关过滤已断连且无更新实时读数的下属子设备，防止已断电网关的子设备被误标为在线；在 `gateway-uplink.v2.schema.json` 补充 `sessionId` 与 `reason` 字段定义，并在 Broker ACL 集成测试改用直连 MQTT v5 报文校验 `$share/` 权限隔离。
- 修复 AI 助手回复里的 Markdown 表格偶发缺少表头分隔线、页面显示成带竖线原文的问题（#669）：提示词（`system-policy.ts`）补全标准 `| --- |` 分隔线示例与约束，网页端与小程序共享解析器（`answer-markdown.ts`）对代码块外的无分隔线管道表格自动补全分隔行，并在 `evals/grade.ts` 加入 `markdown-table` 自动检查。

### 2026-11-16

#### 变更

- 将实时遥测写入热路径重构为端到端微批处理，消除逐点同步 HTTP、逐点档案库查询与逐点 PostgreSQL 事务锁竞争：
  - `modules/connectivity`（`pkg/connectivity.Store.ResolveBindings`、`pkg/adapter` 与 `cmd/connectivity`）在单个只读事务中批量解析网关下属设备与点位绑定，队列支持空队列零延迟直刷或窗口聚合（`CONNECTIVITY_BATCH_MAX_MESSAGES` / `CONNECTIVITY_BATCH_WINDOW`，受 `512` 条与 `3 MiB` 编码上限约束），通过 `POST /internal/v1/telemetry/sources/observations:acceptBatch` 转发给 `telemetry-worker` 并严格按到达顺序在整批落盘后回执 MQTT ACK；同步移除旧的逐点接口 `/internal/v1/telemetry/sources/observations:accept` 与 `ResolvePoint`。
  - `modules/telemetry`（`AcceptObservationBatch` / `AcceptHistoricalObservationBatch`、`source_server.go` 与 `probe_offset.go`）在单次 `ReadCommitted` 事务内按统一 `hashtextextended` 锁序获取分区、事件与设备咨询锁并预取事实，内存顺序判定后经 `pgx.Batch` 批量落盘，每台设备每批最多重算一次设备快照与实时推送出站消息，保持与历史重放、点位健康检查及 ClickHouse 历史中继链路完全兼容。
- 为遥测批量入库与历史重放补齐限流前查重防活锁，并为连接服务增加短生命周期绑定缓存：
  - `modules/telemetry` 在 `:acceptBatch` 与历史重放入口限流前先通过 `FilterUnseenObservations` 过滤 `telemetry_runtime.source_observations` 已落盘 `source_event_id`，对全部命中的重试请求跳过限流计数以免 `429` Retry 活锁。
  - `modules/connectivity`（`Store.ResolveGateway`、`Store.ResolveDevice` 与 `Store.ResolveBindings`）引入 `4096` 条上限、按网关 `epoch` 防护的短时缓存（正缓存 `30` 秒并受凭证 `valid_until` 上限约束，负缓存 `5` 秒），在凭证签发/吊销与已退役网关吊销时即时清退缓存并触发覆盖率摘除。

- 将遥测在离线状态收敛为单一事实源，并降低能耗计算对档案服务的逐条查询开销：
  - MQTT 上线、断线与遗嘱事件（`LWT`）在 `modules/connectivity` 解析出目标设备后，统一写入 `telemetry_runtime.presence_signals` 并仅重算该设备自身快照，网关离线不再级联把下属子设备标为离线（ADR 0002、ADR 0015）。
  - 遥测在读与定时巡检共同收口冻结的「最新」快照：`TelemetryPresentState` 新增可选的 `unchangedSince` 与 `freshWithinSeconds`，`SweepDuePresence` 定时重算已过鲜活窗的设备，`readLatestSnapshot` 在读侧通过 `RefreshSnapshotAt` 实时投影过期状态且不改写库内缓存与 `businessRevision`（ADR 0003）。
  - 告警计算优先读取遥测快照提供的 `unchangedSince`（仅在缺失时回退快照对比），并把探头偏移（`probeOffsets`）折叠从实时接收路径移至 `telemetry-worker` 后台周期执行（`SweepProbeOffsets`）。
  - 注册表内部接口将逐点 `/meter-bindings/resolve` 替换为按站点批量读取的 `GET /internal/v1/registry/sites/{siteId}/meter-bindings`（保留站点行级隔离），`modules/energy` 提取纯匹配函数 `MatchMeterBindings` 与带 30 秒缓存及未命中单次刷新保护的 `coreclient.Resolver`，一次站点级拉取即可服务整批读数。
- 将控制指令校验、三档控制退出、资产控制锁、检修窗口与水路联锁规则统一收拢至控制服务 `ControlAuthority`（ADR 0032），新增迁移 `008_command_precondition_mode.sql` 以显式 `EMERGENCY_BYPASS` + `bypass_reason` 取代急停断采时的虚假遥测拼接，并由 `ClaimDispatch` 在下发 MQTT 前复用同一套 `EvaluateControlGate` 安全闸门。
- 平台网关 `cmd/energy-api/internal/gateway/{command.go, control_exit.go}` 收薄为会话、CSRF、限流、边缘两步验证与 `X-Command-Control-Context` 签名转发层，`route-ownership.v1.json` 升级至第 50 版并将 `/api/v1/sites/{siteId}/control-{state,mode,shutdown}` 与 `/api/v1/assets/{assetId}/control-lock` 划归 `command-service`；同步移除派发器 `dynamic_safety.go` 与连接层 `command_route.go` 的重复安全策略分支。

#### 新增

- 打通网关 MQTT 上下线与下游设备观测覆盖率闭环（ADR 0002、ADR 0015）：新增迁移 `mqtt-runtime-events.sql`，边缘模拟器 `MQTTPublisher` 注册与建连同 `sessionId` 配对的 `LWT` 遗嘱消息及 `GATEWAY_CONNECTED` / `GATEWAY_DISCONNECTED` 事件；连接服务附带目标设备与下游子设备列表上报，遥测服务按同会话与事件时间定序（含 `60` 秒未来时钟偏斜防护），仅向网关自身写入 `presence_signals` 而将子设备通过 `observation_coverage` 置为 `UNAVAILABLE`（`SOURCE_UNAVAILABLE`）；连接运行期在订阅就绪、每 `60` 秒心跳、断连、进程优雅退出与网关凭证吊销时同步调用 `POST /internal/v1/telemetry/sources/coverage:report` 上报受管设备覆盖状态。
- 注册表资源退役检查现同步校验仍处于启用状态的设备源键映射（`core_registry.gateway_device_source_keys`），新增 `GATEWAY_SOURCE_KEY` 绑定写入与 `closeOnly` 解绑关停（产出 `registry.binding.closed`），确保绑定先解绑后方可退役。
- 网关反向运维隧道（`modules/connectivity/pkg/tunnel`）新增持久审计表 `connectivity.gateway_tunnel_audit` 与租户 RLS 策略，记录会话建立（`SESSION_OPENED`）、断开及收发字节数（`SESSION_CLOSED`）、流打开（`STREAM_OPENED`）与端口/建流拒绝（`STREAM_REJECTED`）。
- 核心注册表新增域事件投递中继（`OutboxRelay`）与投递授权迁移 `registry-outbox-delivery-grants.sql`，在设备/点位变更、导入提交与设备/站点退役时自动向遥测运行时（`/internal/v1/telemetry/registry-sync:apply`）、连接凭证（`/internal/v1/gateways/{gatewayId}:retire`）与指令服务（`/internal/v1/commands:cancelRetiredTarget`）推送权威状态、吊销已退役网关凭证/隧道并取消挂起指令。
- 现场接入脚本 `scripts/site-onboarding.mjs` 收敛设备在线判定信号白名单（包含 `EXPLICIT_CONNECT` 与 `EXPLICIT_DISCONNECT`），为 `apply` 增加站点 `revision` 乐观并发检查，并仅针对实际新增或修订递增的实体写入 `registry_audit_facts` 与 `domain_outbox_events`（未变化重跑零写入）。
- `/operations/control` 机组列头显示绑定的 `HVAC_POWER_METER` 实时功率（无有效读数时显示「暂无读数」），控制联锁统一读取点位 `sourceMetadata.interlockChecks`（#862）
- 站点控制模式与设备锁定接口返回操作人姓名 `updatedByName`（依托 `iam.resolve_tenant_principal_names` 查询同租户在职成员），控制页与实时运行页直接显示人员姓名，不再回退为「其他人员」（#859）
- 控制下发与整站安全停机动态口令校验按 `ProblemDetails.reason` 区分口令错误（`CODE_INVALID`）、口令已使用（`CODE_REPLAYED`）、验证已锁定（`MFA_LOCKED`）与未开通两步验证（`MFA_NOT_CONFIGURED`），并在未开通时引导跳转 `/system?tab=users`（#860）
- `/energy-analysis/optimization` 供水温度优化在未就绪时按接口 `responseCoefficient` 与 `tenantStrategy` 展示具体缺口说明（「响应系数还没标定」「共享租户策略还没确认」），并保持上方减台与辅机停机独立可用（#861）
- `/energy-analysis/optimization` 新增限时自动控制租约管理（开启租约含边界校验与两步动态口令确认、手动撤回、`AUTO_WITH_LEASE` 确认模式联动）与「30 分钟执行监护」观察窗口及退出原因展示，并在 `/operations/control` 控制方式栏显示生效中的自动控制租约状态与跳转入口

- 新增免交互接口同步脚本 `scripts/sync-songyaxuan-nodered.ts`（配合平台主机 `/opt/hvac-platform/runtime/secrets/songyaxuan-nodered-admin` 只读密钥与 `--ssh` 服务端回环通道），先校对 `ET1010·机组启停` 和 `平台·常驻隧道` 页签启用，再仅热更新 `平台·形成信封` 并回读 `/flows` 核验。
- 「运行控制」可以直接从平台停机：控制方式条右侧「停机…」执行安全停机（先停所有没锁定的冷机，主机停稳后再停对应的水泵和冷却塔，站点切到安全退出并撤回没下发的指令）；站点登记了紧急全断点位时还可以选「紧急全断」，立刻断开全部输出通道。机组的一键启停还没结束时，机组列上出现「中止…」，只中止并按顺序停这一组。三种都要填原因、输入动态口令并确认已和现场沟通。松雅轩登记在冷冻水总管上的「紧急全断全部 DO 通道」不再显示成普通的「停止」按钮。
- 第一性原理重构遥测可信边界、冷机防冻保护与整机根因抑制（#749）：
  - `deploy/sites/songyaxuan.alarm-rules.json` 与 `modules/alarm/pkg/alarmrules` 新增设备级根因告警规则 `deviceUntrusted`（`device-untrusted/<device>`，`MAJOR`，连续 `180 秒`）：非派生设备的全部 `>= 2` 个监测点位同时出现超量程、通讯异常值 `38.4` 或 `SOURCE_UNTRUSTED` 时，仅发出 1 条整台根因告警，并在条件树内部同步抑制同设备逐点 `suspect`（支持 `durationSeconds: 300`）与 `readings-not-updated` 症状告警，避免 23 路探头同时掉线刷屏；同时排除 `device-untrusted/` 触发无意义的 `rules-unevaluable`。
  - `deploy/sites/songyaxuan.alarm-rules.json` 新增 `chiller-leaving-low`（`CRITICAL`，`180 秒`）：任意运行中的冷机（A `t1`、B `t2`、C `t3`、D `t4`）冷冻水出水低于 `4.0 °C` 时立即触发防冻保护告警（冷机停机或水温回升过 `4.5 °C` 恢复）。
  - 资产铭牌（`libs/nameplate` 与 `scripts/site-onboarding.mjs`）新增 `feederAuxiliaryPowerKw` 字段，用于表达合并计量冷机与定频辅机（水泵、冷却塔）的总馈线电表辅机功耗基数（并新增迁移 `042-grant-asset-nameplate-read.sql` 按最小权限授予 `fdd_runtime` 与 `optimization_runtime` 只读 `core_registry.assets.nameplate`）；通知路由新增多条分作用域根因抑制规则 `rootCauseRules`（`scopeSameDevice: true` 让 `device-untrusted/<device>` 只静默同设备的下游告警通知，不误伤站内其它正常设备）。
- 系统管理「用户与角色」页面（`/system?tab=users`）新增「两步验证」列，并在管理员与运维主管提交高风险指令前提前预警（#831）：
  - `services/identity-service`（`DirectoryUser`）与网关成员接口（`GET /api/v1/iam/members`）仅返回每个成员是否已开通认证器两步验证的布尔状态 `mfaEnabled`（不返回密钥、密文或恢复码等敏感信息），同步更新 OpenAPI 与生成客户端 `UserAdminMember`。
  - 用户列表展示「已开通 / 未开通」，对启用中且拥有管理员或运维主管角色但未开通两步验证的成员标出 `▲ 未开通` 与「提交不了高风险指令」，停用账号时自动撤去提醒。
- 网页端「集成管理」新增「无线集中器」管理页签（`/settings/integrations?view=concentrators`）：
  - 在 `IntegrationsWorkspace`、`Gateways` 客户端与纯模型（`integrations-model.ts`）接入 `getGatewayConcentratorStatus` / `updateGatewayConcentratorParams` / `getGatewayConcentratorEids` / `updateGatewayConcentratorEids`，提供网关与 RS485 从站选择（拦截受保护从站 `{2, 3, 4, 5, 10, 31}`）、实时探头通道状态与低电压分级告警（`< 2600 mV` / `< 2450 mV`）、IME 安全的通道搜索与侧边详情抽屉。
  - 提供 14 项串口与 LoRa 射频参数编辑器、每页不超过 `50` 行的 200 槽 EID 分页映射表（支持区间批量填充、非零重复 EID 冲突拦截）、带变更前后对比与必填原因的 Flash 写入确认弹窗（`0x9C41 = 6 / 8`）、近期下发变更审计记录以及 `device.write` 只读保护。
- 无线集中器远程配置与多路状态读取/写入后端（MQTT mTLS RPC、租户行级审计与平台网关接口）：
  - `modules/connectivity` 新增数据库迁移 `006-gateway-concentrator-audit.sql`（启用并强制按 `app.tenant_id` 执行 `connectivity.gateway_concentrator_audit` 行级隔离，表约束封禁 RS485 保护地址 `{2, 3, 4, 5, 10, 31}`），以及 `pkg/concentrator` 服务层（按 `(gatewayId, unitId)` 串行加锁、`autopaho` QoS 1 MQTT RPC 收发与 `gatewayId` 强校验、内部 mTLS HTTP 接口）。
  - 更新 Mosquitto 访问控制 `infra/telemetry/mqtt/acl`（仅允许平台适配器发布 `hvac/v1/+/down/concentrator` 并订阅 `hvac/v1/+/up/#`，禁止网关伪造下行或跨网关写入），并在 `cmd/connectivity` 与 `cmd/energy-api` 接入 `GET/PUT /api/v1/gateways/{gatewayId}/concentrators/{unitId}/{status,params,eids}`（`device.read` / `device.write` 鉴权、CSRF 校验、RFC 9457 错误映射与 `25s` 上游超时）及生成 OpenAPI 前后端客户端。
  - Edge 侧新增 `edge/node-red/concentrator-mqtt-rpc.ts`，在网关本地二次校验并拒绝 `{2, 3, 4, 5, 10, 31}` 写操作、`WRITE_PARAMS` 末位写 `modbusId` 后执行 `0x9C41 = 6` 提交与回读核对、`WRITE_EIDS` 拒绝非零重复 EID 并将 200 槽位脏区合并为最多每批 `20` 槽的 `FC16` 写入及 `0x9C41 = 8` 提交核对。
- 三层站点控制退出与安全停机编排（`POST /api/v1/sites/{siteId}/control-shutdown`）：
  - 在网关（`cmd/energy-api/internal/gateway/control_exit.go`）、OpenAPI 契约与生成客户端中新增 `executeSiteControlShutdown`，支持一键顺控中止（`ABORT_UNIT_SEQUENCE`，撤回排队与在途顺控指令并通知边缘停止计时器）、全站安全停机（`PLANT_SAFE_SHUTDOWN`，切换 `SAFE_SHUTDOWN_EXIT` 后先发未锁定冷机停机脉冲，仅对确认已停冷机放行水泵/冷却塔保持线圈并把仍在运行的泵/塔列入 `deferredHoldingAssetIds`）以及二次确认全站紧急断电（`EMERGENCY_DO_RELEASE`，自动核对阻断急停的历史未知指令后向 `et1010/emergency_stop_all` 下发全输出清零，即使断采也强制放行）。
  - `MANUAL_OVERRIDE` 与 `SAFE_SHUTDOWN_EXIT` 退出模式下，平台网关与接入路由双层放行 `STOP` 与 `STOP_UNIT_SEQUENCE`，允许冷机停妥后释放仍在保持的水泵与冷却塔继电器；非停机与设定类指令继续严格拦截。
- 站点自动控制租约、下发前实时安全复核与 30 分钟执行监护回滚机制：
  - 新增迁移 `043-auto-control-leases-and-monitors.sql` 与核心注册表（`modules/registry`）租约接口，在站点下新增 `site_auto_control_leases` 与 `optimization_execution_monitors` 两张表；默认保持 `MANUAL_CONFIRM`（不自动下发），仅支持持 `schedule.manage` 权限（运维主管或管理员）并完成 6 位认证器动态验证码两步验证的操作员通过 `PUT /api/v1/sites/{siteId}/optimization/auto-control-lease` 开通最长 24 小时、限定能力白名单与温升/功率/频次边界的自动控制租约，或通过 `revoke` 随时收回，并通过 `GET .../execution-monitors` 查看监护状态。
  - 在 `libs/commandauth` 与 `modules/iam` 新增内部工作负载指令授权端点 `POST /internal/v1/command/workload-decision` 与审计字段（`WorkloadSPIFFE`、`LeaseID`、`ArmedBy`），二次核验租约未过期、站点处于 `REMOTE_AUTO` 且无资产锁定、能力与设定值边界、每小时频次上限及回滚预置匹配后，签发不超过 30 秒且不可转授的控制授权；网关新增仅接受已认证 `optimization-service` 内部呼叫的 `POST /api/v1/sites/{siteId}/optimization/auto-commands`。
  - 在 `libs/intelligencemodel` 与 `services/optimization-service` 为优化建议加上 65 分钟有效期 `validUntil`，发布后仅在持有有效租约时执行供水偏差不超过 `0.5 °C`、冷机功率不超过额定 `0.90` 倍与实时温控健康复核后以 `auto-opt:<recommendationId>` 自动下发，并在随后 30 分钟每分钟巡检实时温度、冷机负荷与遥测健康状态；一旦越界或断采超过 5 分钟，立即以 `auto-rollback:<monitorId>` 自动回滚设定点、将租约断路置为 `TRIPPED` 并投递 `AUTO_CONTROL_LEASE_TRIPPED` 告警通知。
- 冷站联锁防呆、主机启停保护与控制失败主动告警闭环：
  - 控制服务（`ControlAuthority`）在常规启停下发前新增冷水机组与辅机防误操保护（当指令历史查询不可用时 fail-closed 返回 `503`）：对应主机开机指令在途或成功启动后的 `6 分钟` 启动盲窗内拦截关联冷冻泵、冷却泵与冷却塔停机（`COMMAND_INTERLOCK_BLOCKED`）；限制单台冷机满足 `20 分钟` 最小运行时间、`15 分钟` 最小停机时间及 `60 分钟` 内最多启动 `2 次`（`COMMAND_COOLDOWN_ACTIVE`）；在运营排班时段内且不存在有效 `MAINTENANCE_WINDOW` / `HOLIDAY_SHUTDOWN` 例外时阻止直接停掉最后一台在运冷机（`COMMAND_LAST_ACTIVE_CHILLER_PROTECTED`，引导改走全站安全停机 `PLANT_SAFE_SHUTDOWN`）。
  - 完善三层退出与停机异常韧性（`ControlAuthority`）：`PLANT_SAFE_SHUTDOWN` 在遥测不可用或过期时安全降级返回 `200 OK`（继续停未锁定且无联锁阻断的冷机，并将无法确认主机已停或联锁阻断的水泵/塔记入 `deferredHoldingAssetIds`），仅在核心注册表操作或下游非 `409` 指令派发失败时返回 `503`；处于 `SAFE_SHUTDOWN_EXIT` 时在确认对应主机停稳并越过 `60 秒` 惯性窗后最佳努力释放延迟保持的辅机；`PLANT_SAFE_SHUTDOWN` 通过 `cancelPending`（`abortActiveSequences: true`）撤回并终止在途顺控，而 `ABORT_UNIT_SEQUENCE` 停机模式与撤回派发中（`DISPATCHING`）的顺控指令时还会向边缘显式下发 `ABORT_UNIT_SEQUENCE` 点位指令。
  - 补齐控制失败与紧急操作即时告警及监控指标汇聚（`modules/command`、`modules/alarm`、`libs/observability` 与 `infra/observability/alerts/domain-phase1.yaml`）：当指令落入 `OUTCOME_UNKNOWN`、`PULSE_DELIVERED_POWER_UNCONFIRMED`（含数据库超时对账、租约过期及超期后才到达的 `VerificationSucceeded` 回执）或触发 `EMERGENCY_DO_RELEASE` 时，非阻塞异步向 `alarm_runtime` 内部控制事件接口（`POST /internal/v1/alarms/control-events`）写入并去重关联告警；在 `cmd/energy-api` 中汇集内嵌领域模块指标并去重 Prometheus 同名指标族头，并将指令核验失败率与超时率告警阈值从高流量门槛调整为发生即触发（`>= 1`）。

#### 修复

- 修复 `deploy/platform/phase1/cd/observability-config.sh` 的 `wait_loaded` 在启用 `pipefail` 时用 `grep -Eq` 过滤大体积 `/metrics` 输出提前退出、导致 `wget` 触发 `SIGPIPE`（退出码 `141`）并让监控配置重载误判超时的故障。
- 同步松雅轩冷却塔出水温度通道（`t55`）并停用无线集中器空槽位 `51`：
  - 将松雅轩 `cooling-tower/cooling_tower_leaving_temperature`（`cooling_tower.leaving_water_temperature`）主来源切至现场在线的 55 号通道探头（`t55`），并在边缘包络函数 `edge/node-red/songyaxuan-platform-envelope.js` 对同点位共存通道（`t55` 优先、`t12` 回退）按 `(target, code)` 去重，防止双通道同时在线时重复写入或离线通道覆盖有效读数。
  - 鉴于物理探头 `EID 51` 实测绑定在 11 号槽位、51 号槽位未绑探头，将 `t51`、`v51`、`rssi51`、`status51` 在站点清单标记为 `"status": "INACTIVE"`（保留数组顺序与 `pointIndex`），并在边缘侧将 `SPARE` 扩展为 `{20, 51, 52}`，使空槽位不再计入 `offline_count` 或触发误报警。
  - 修复 `modules/telemetry` 的 `SweepDuePresence` 巡检逻辑：对无新读数且已标记 `NOT_APPLICABLE` 的设备（如新增 `banquet_room_temperature` 但探头暂离线的 `room`），补齐快照缺失的新增启用点位键，避免批量快照读取因 `ErrSnapshotIncomplete` 返回 `503`。
  - 修复 `cmd/energy-api` 的 `validateTelemetrySnapshot`：对在离线判断为 `NOT_APPLICABLE` 的纯复制设备放行 `displayState: null` 且同时拒绝无效 `currentState`/非空 `displayState` 组合，解除实时批量观测接口 `503` 阻塞。
- 系统消除松雅轩机组辅机误判、开机热平衡瞬态误报与异常值连带刷屏：
  - **C/D 总馈线与压缩机专用表解耦**：松雅轩 C（`meter-uid5` / `ammeter-unit1`）、D（`meter-uid2` / `ammeter-unit2`）铭牌配置 `feederAuxiliaryPowerKw: 30`；边缘包络脚本 `edge/node-red/songyaxuan-platform-envelope.js` 在 ET1010 已知辅机线圈（`u31..u33` / `u41..u43`）未吸合时改回退 C/D 总馈线迟滞 `57.75 / 45 kW`，同时将 `ET1010 UID31` 原始线圈键 `u0..u19` 归一为 `u11..u54`（`A/B` 仍用 `12.75 / 5 kW`）；FDD 开机门控、优化减台评估与网页排班偏差检测统一计入 `feederAuxiliaryPowerKw` 并在排班模型中优先采用 `run_state` 判定，消除 C/D 单开水泵和冷却塔（`31–44 kW`）及本地 MCC 手动模式下误报冷机运行与温差倒挂的问题。
  - **开机热惯性稳定窗与同伴新鲜度校验**：`modules/telemetry` 把 `unchanged_since` 连续状态追踪扩充至非数值点位（含 `run_state`），跨点检查与运行状态门控在入库评估时读取 `freshness_policies.fresh_within_seconds` 过滤过期同伴读数；物理规则新增 `stabilizeSeconds`（热力方向、总管方向与能效比设为 `600 秒`，`PHYSICS_POWER` 保持 `0 秒` 瞬时拦截），平线规则新增 `flatlineStabilizeSeconds`（`600 秒`）且统一走 `VALID_ONLY`，A–D 蒸发/冷凝出水通道按点位绑定各自冷机电表 `run_state`。
  - **放宽物理真实量程与补全异常/冷启动链路**：松雅轩 `qualityProfiles` 放宽至物理极限（冷冻水 `[-2, 40] °C`、冷却水 `[0, 50] °C` 等），`2.8 °C` 过冷低温正常入库并进入 `chiller-leaving-low` 保护而非被拒收；`SENTINEL` 与 `SOURCE_UNTRUSTED` 不再叠加 `FLATLINE`；修复冷启动仅收到拒收读数（`ONLY_REJECTED_CANDIDATES`）时的 `OUT_OF_RANGE` 评估、拒收行 `SOURCE_UNTRUSTED` 健康记录、`site_rules_http.go` 的 `REASONS` 谓词校验缺口、OpenAPI/前后端 `SOURCE_UNTRUSTED` 契约枚举，以及去掉派生设备 `btu-calc` / `meter-total` 冗余的 `suspect` 与 `btu-chw-direction` 告警。
- 修复 `deploy/platform/phase1/cd/build-release.sh` 的 `collect_migrator_tree` 在读取 `migrations/Dockerfile` 时遇到第二个 `FROM scratch`（`sources-runtime` 阶段）即提前中断、导致发布包遗漏运行时迁移文件的问题，并新增基于 `migration-list.tsv` 的全量校验。
- 对齐生产域名反向代理与松雅轩现场无线集中器默认值：
  - 为内部工作负载身份清单补充 `host-edge-proxy`，容器 `nginx` 使用 `trusted-client-ca.pem` 与身份主题映射，使宿主机 `nginx` 校验网关客户端证书后通过内部双向 TLS 安全透传 `X-Gateway-Client-Cert` 至 `connectivity`，并更新 `hvac-gateway-nodered-loopback@.service` 容器名为 `hvac-platform-connectivity-1`；`deploy.sh` 迁库前对齐 `roles.sql`、补建 `$R/alarm-delivery` 目录，并核对 `mqtt-broker` 容器内单文件挂载（`mosquitto.conf`、`acl`、`gateway-ca.pem`）并在内容漂移时自动重建容器绑定新 inode。
  - 修复无线集中器 `WRITE_PARAMS` 在修改 `modbusId` 后改向新从站地址提交 `0x9C41 = 6`；为边缘隧道与证书自动续签补齐系统根证书（`tls.rootCertificates`）与私有网关 CA 的合并校验，确保直连生产 HTTPS 域名时同时验证公网证书与网关 CA 签名；网页端「无线集中器」默认从站点 `TEMPERATURE_MODULE` 推断初始从站地址（松雅轩默认为 `UID 20`）。
- 修正松雅轩电表启停迟滞、脉冲核验不误冻结与一键顺控硬件时序：
  - 边缘信封脚本（`edge/node-red/songyaxuan-platform-envelope.js`）改用 `12.75 kW` 启动、`5 kW` 停机迟滞判定各机组电表与主机控制器运行状态，按设备持久化避免单泵空转误判为主机启动，同步上报 `et1010/run_state`，并将 `ctrl-chiller-a..d` 的采样周期统一对齐电表的 `300` 秒。
  - 控制执行器（`edge/node-red/songyaxuan-command-executor.ts`）改为 `< 1` 秒先行返回已受理证据并在后台异步按硬件延迟（开机 `0s/30s/30s/60s`、停机 `0s/30s/10s/10s`）执行，`START_UNIT_SEQUENCE` 发冷机启动脉冲前二次读回水泵与冷却塔线圈，单设备 `STOP` 自动中止对应机组异步顺控；紧急停止指令将线圈 `0..19` 全部归零。
  - 指令状态机（`libs/commandmodel`、`modules/command` 与 `commandformal`）在 10 分钟核验期满且电表功率未跨阈值时，对携带脉冲双读回、顺控受理或急停归零有效回执的指令统一收敛为 `FAILED`（`PULSE_DELIVERED_POWER_UNCONFIRMED`）且不再误冻结设备控制组，保持所有 `REQUEST_COMMITTED` 及普通保持/设定类指令结果不明必冻结不变量不变。

### 2026-11-15

#### 变更

- 清理 `hvac_s4.alarm_runtime` 中 4 张 S13 一次性迁移遗留表（`alarm_current_pre_s13_backup`、`alarm_idempotency_pre_s13_backup`、`s13_alarm_migration_report`、`s13_alarm_identity_map`）并移除对应过时回滚脚本；为 8 个 PostgreSQL 数据库下全部 12 个 schema 与 221 张在用数据表补齐中文 `COMMENT ON` 注释，并新增静态门禁 `npm run migrations:conventions:check` 拦截阶段性命名、`CREATE TABLE ... AS` 副本表与缺失注释的新迁移。

#### 改进

- 「控制指令」页改为「运行控制」，按值班习惯重排：
  - 顶部一条「控制方式」显示远程自动控制 / 人工接管 / 安全退出、谁在几点设的、原因、锁定设备数和待处理指令数，点另外两种方式即可切换（必须填原因）；切离远程自动时列出会被撤回的待审批、排队指令，还有指令在下发或等现场确认时不能安全退出。
  - 设备按机组排成列：每台冷机和它的冷冻泵、冷却泵、冷却塔（取自登记的开机联锁）一列，带「一键启动」「一键停机」；每台设备只给当前能做的那个按钮，水路联锁不满足时直接写「先开 1 号冷冻泵」「先停 A 机组主机」并置灰。
  - 运行计划里今天有检修窗口时，控制方式条写出窗口名称和日期，启动和调整设定值置灰，只留停机和复位。
  - 每台设备可以「手动锁定」「检修锁定」或解除锁定（必须填原因），锁定后撤回它还没下发的指令，平台不再给它下发指令或停机建议。
  - 启停和一键启停属于高风险指令：确认时要输入认证器上的 6 位动态口令并勾选「已和现场确认」；批准高风险指令时如果两步验证超过 10 分钟，会提示「批准前要再验证一次身份」，验证后回到这条指令。
  - 指令记录新增「撤回」，网关拒绝时显示中文原因（例如「被水路联锁拦下」「还有指令正在下发或等现场确认，先处理完再安全退出」），不再显示英文错误。
- 站点处于人工接管或安全退出时，所有站点页面顶栏都显示「▲ 人工接管」「◆ 安全退出」并链到运行控制；侧栏「运行控制」角标显示待审批和待现场确认的指令数。
- 实时运行页头部在有设备锁定时显示「N 台锁定」，「最近事件」加入今天的控制方式切换和设备锁定；设备详情侧栏对锁定的设备显示锁定方式、谁锁的和原因。

#### 新增

- 优化建议页「当前建议」新增「减台与辅机停机」：列出可以停的冷机（停机后剩余制冷能力、余量、能效变化）、运行计划停机时段里还在转的冷机，以及对应主机已停但仍在转的水泵、冷却塔，各自显示预计省多少千瓦、本班约省多少电；逐条「确认停机…」走高风险确认后下发，已提交的显示审批状态并链到运行控制；被锁定、因控制方式或检修窗口暂停的项目单列「暂停 N 条」并写明原因。供水温度优化放在下方，两者互不影响。

#### 修复

- 修正控制指令回执与模拟器下发时序：设备启停及一键启停序列（`START`、`STOP`、`START_UNIT_SEQUENCE`、`STOP_UNIT_SEQUENCE`）的遥测状态核验窗口从 2 分钟延长至 10 分钟，匹配现场电表采样周期与冷机加卸载延迟；数值设定与故障复位仍保持 2 分钟。同时将 `connectivity` 下行应答超时从 15 秒放宽至 25 秒（派发步 40 秒、租约 45 秒），并为 `eg8200-simulator` 补齐 `pointCode`、`actuationMode`、`channel`、`coilAddress`、`pulseDurationMs` 报文字段与一键启停序列仿真，避免携带路由元数据的控制报文在模拟网关被静默丢弃。
- 修正松雅轩 A、B 机组电表映射：确认 Modbus `UID 4`（`ammeter-unit4`）为 A 机组电表、`UID 3`（`ammeter-unit3`）为 B 机组电表（此前 2026-11-12 现场核对将两者对调）；同步迁移 A 机组支管进水温度 `chw_entering_temperature_branch`（`t10`）到 `ammeter-unit4`、`ctrl-chiller-a/b` 运行状态绑定与冷站阶段加减机映射，保持原 76938 kWh 基线留在物理表 `ammeter-unit4` 上，并补充 C、D 机组电表各自合并计量对应机组、水泵与冷却塔的说明。

#### 新增

- 现场网关 Node-RED 24/7 常驻双向 TLS 反向隧道与 14 天证书自动续签：新增 `nginx` `:443/gateway/v1/tunnel` HTTP Upgrade mTLS 代理、`modules/connectivity/pkg/tunnel` 多路复用隧道中枢（强制只转发网关 `127.0.0.1:1880` 与宿主机 `127.0.0.1:18800` 回环监听、25 秒心跳保活、吊销即断线）、`cmd/connectivity` `-tunnel-stdio` 桥接与 `systemd` 套接字激活模板、网关侧 `edge/node-red/gateway-nodered-tunnel.ts`（`平台·常驻隧道` 与关键页签防误删保护）和 `edge/node-red/gateway-cert-autorenew.ts`（`平台·证书` 剩余有效期 `<= 14` 天走 `/gateway/v1/renew` 原子续签）。
- 打通周运行计划检修窗口自动拦截与停机时段冷机停机建议（`OFF_HOURS_STOP`）：`GET /api/v1/sites/{siteId}/control-state` 新增 `activeMaintenanceWindow` 字段，站点处于已批准检修窗口期间自动拦截非停机、非复位的远程控制指令并暂停设定温度优化草案；同时在停机时段对仍在运行的冷机自动生成人工确认停机建议，不自动强制停机。
- 松雅轩站点清单新增温度采集模块（UID 20）无线探头电池电压（`temperature-voltage-uid20`，`v1`–`v20` 与 `v51`/`v53`–`v55`，`mV`）、信号强度（`temperature-rssi-uid20`，`rssi1`–`rssi20` 与 `rssi51`/`rssi53`–`rssi55`，`dBm`）与通道通信状态及汇总诊断（`temperature-status-uid20`，`status1`–`status20`、`status51`/`status53`–`status55` 及 `online_count` / `offline_count` / `low_battery_count` / `min_voltage_mv` / `min_rssi_dbm`）；同步启用 14 号冷却水塔水探头、11/51 与 12/55 共存冷却塔通道及房间大厅（`t53` -> `room_temperature` / `zone.temperature`）与宴会厅（`t54` -> `banquet_room_temperature` / `zone.banquet_temperature`）室内温度，仅保留 20 号备用通道为 `INACTIVE`（网关 `SPARE` 同时屏蔽已停用的 52 号通道）；网页与微信小程序遥测词典和实时监测视图同步补齐 `mV`、`dBm` 整数精度及 `zone.banquet_temperature`，网关信封脚本 `edge/node-red/songyaxuan-platform-envelope.js` 同步最新通道映射、诊断合并上报与离线通道质量降级（`quality: 1`）。
- 松雅轩冷站 ET1010 20 通道控制能力与双闸门水路联锁保护（Wave 2）：
  - 在站点清单 `deploy/sites/songyaxuan.site.json` 与 `scripts/site-onboarding.mjs` 中补齐主机停机脉冲点位 `u51`–`u54`（UID 31 地址 `16`–`19`）与 16 台设备控制器（`ctrl-chiller-a..d`、`ctrl-chwp-1..4`、`ctrl-cwp-1..4`、`ctrl-tower-1..4`，共 40 个 `COMMAND` 点位，`CONTROLS` 绑定），仅对观测点位应用存在性与新鲜度监控。
  - 资产登记模块新增读取视图 `core_registry.command_point_routes_v1`（迁移 `038-command-route-point-metadata.sql`），使网关下行链路自动下发 `actuationMode`（`LEVEL` / `PULSE` / `UNIT_SEQUENCE`）、`channel`、`coilAddress` 与 `pulseDurationMs`。
  - 在平台网关提交前（`cmd/energy-api/internal/gateway/command.go`）与 MQTT 下发前（`cmd/connectivity` + `dynamic_safety.go`）双闸门实时核验同伴设备 `run_state` 联锁条件，水泵或冷却塔未运行禁止脉冲开启主机、主机未停机禁止断开对应水泵或冷却塔。
  - 更新 Edge 网关报文函数 `edge/node-red/songyaxuan-platform-envelope.js` 上报控制器运行状态，并实现 `edge/node-red/songyaxuan-command-executor.ts` 下行脉冲复位与 15 秒内一键冷机启停序列执行。
- 站点与单台设备控制退出边界与交互原型（Wave 1 #824、#825）：后端落地站点控制模式（`REMOTE_AUTO` / `MANUAL_OVERRIDE` / `SAFE_SHUTDOWN_EXIT`）、单台设备锁（`UNLOCKED` / `MANUAL_LOCK` / `MAINTENANCE_LOCK`）及未开始派发指令取消，并完成网页端「运行控制与节能联动」Figma 原型设计待审。
- 补齐节能减台与闲置辅机联动评估后端接口与联锁控制感知（#542）：
  - 网关新增 `GET /api/v1/sites/{siteId}/optimization/staging-recommendations/latest` 只读接口、OpenAPI 契约与前端生成客户端（`apps/hvac-web/src/api/generated/platformGateway.gen.ts`），并在 `POST /api/v1/commands` 放行 `CRITICAL` 风险等级控制指令。
  - `services/optimization-service` 新增数据库迁移 `039-optimization-staging-catalog-read.sql`，按站点与租户行级权限读取站点控制模式、单台设备锁定状态、全部绑定设备、可控 `COMMAND` 点位以及点位 `interlockChecks` 液压关联：全站处于手动接管（`MANUAL_OVERRIDE`）或安全停机退出（`SAFE_SHUTDOWN_EXIT`）时暂停产出停机建议，被锁定的冷机或水泵/冷却塔会被跳过并给出原因诊断，允许停机的建议会直接带回 `suggestedCommandPointId`。

#### 修复

- 将告警评估快照的站点输入点数上限（`MaxEvaluationSnapshotInputs`）从 `128` 提升至 `512`，支持站点扩充无线探头诊断点位后的全量规则评估。

### 2026-11-14

#### 新增

- 设备告警按站点路由发到值班人员手上（#753）：每个站点在 `deploy/sites/<站点>.alarm-routing.json` 里写清楚告警等级、收件人编号、发送渠道（QQ 邮箱 SMTP、企业微信应用消息或企业微信群）、等待时间与无人确认逐级升级间隔、站点时区免打扰时段（重要及以上照发）和每人每小时上限。告警确认后停止升级并通知已收到的人；恢复时通知他们。「网关上报中断」亮着时压住连带的读数缺口与数据质量症状告警；规则 30 分钟以上无法判断时自动报「告警规则长时间无法判断」（警告）。默认 `ALARM_DELIVERY_MODE=off`，须先在服务器放好收件人与密钥并完成 `dry-run` 演练再切换 `live`（见 `docs/operations/alarm-delivery.md`）。

#### 改进

- 网页各页面的文案进一步改为值班人员和能源经理日常用的说法：去掉模板拼接出来的词（「时均」「每时用电」「分日明细」「退役空间」「空间登记资源」等），把「本期间」改成「所选时段」，「通信端点」改成具体电表和传感器名，「诊断发现」统一成「诊断结论」，「告警分诊」改成「告警处理」，「解释」按钮改成「分析原因」，五档告警级别统一成「紧急、重要、次要、预警、提示」，点位单位显示成 `°C`、`m³/h`，并去掉报错提示里的内部英文模块名和自我辩解说明；`npm run design:check` 扩展禁用词与单字模板拼接检查，防止这些表达再写回代码。（#756）

### 2026-11-13

#### 变更

- 松雅轩四块机组电表在平台资产登记、数据缺口提示与告警文案中的显示名称统一为「A 机组电表」「B 机组电表」「C 机组电表」「D 机组电表」，去掉括号里的旧电表编号与从站地址后缀。（#808）

#### 修复

- 修复控制指令与 MQTT 回执恢复中的三处边界问题：超过核验时限才收到的状态读数现在统一转为人工核对（`结果不明` / `OUTCOME_UNKNOWN`）而不会误判成功；对已结束或已失效的控制尝试再次提交派发结果会返回围栏过期错误；MQTT 重连回执恢复遇到已失效或已注销的控制尝试时会将关联记录标记为已处理（`RESOLVED`），避免积压 50 条后阻断后续有效控制回执。同时新增 `npm run command:formal` 穷举检验门禁（`command-safety`）。
- 修正深色主题破坏性操作按钮文字对比度（`.dark` `--destructive` 从 `#e5484d` 调整为 `#d9383a`，白字对比度提升至 `4.59:1` 达 WCAG AA），并按术语规范统一网页剩余旧称（「分表」改为「设备电表」、「计收」改为「计费」、「认领 / 认领处理」改为「分配 / 由我负责」）。
- 系统管理的「用户与角色」权限清单改为直接按 IAM 网关契约约束，补齐「吊销会话」（`session.revoke`）与「模板版本」（`template.manage`）展示名称并移除已废弃键，不再错误计入「另有 N 项未命名的权限」。

### 2026-11-12

#### 新增

- 读数中断 24 小时未确认升级与每日汇总：正在发生的点位「读数中断」和设备「读数长时间没有更新」超过 24 小时仍无人确认时，告警模块自动升一级（「警告」升「重要」、「重要」升「紧急」，同一条告警只升一次）并按升级后的级别再推一次小程序微信告警提醒；已被确认的告警不再自动升级。每天站点时间 8 点的「每日运行结论」微信提醒现在同时附上当前仍未恢复的读数中断汇总（哪台设备或电表、从什么时候起、中断了多久），恢复后自动从次日汇总中移除。（#800）
- 数据缺口接口与页面提示条：平台网关新增 `GET /api/v1/sites/{siteId}/data-gaps`，汇总站点当前正在发生的 `reading-gap/` 关键点位读数中断告警，关联资产登记里的设备与点位名称、合成总表分表登记以及遥测最新读数时间，返回从哪一分钟起没有读数、影响哪些站点数字，以及是否使合成总表缺少这台设备。总览、能耗分析、实时运行和能源报告页顶共用同一条数据缺口提示条（例如「D 机组电表 11/8 16:13 起没有读数，今日空调用电不含这台」），正常无中断时不占空间，点开直达「集成管理」的电表数据核对页签。（#799）
- 合成总表与跨缺口电量：站点文件里把能耗总表配成 `"totalMeter": {"kind": "sum-of-sub-meters"}` 时，平台用各分表电量相加作为站点空调用电总量，不再因为网关算的总表停报就让整站总览、能耗分析和能源报告变成「—」；某个小时有分表缺读数时照样显示已有分表之和，并标明「不完整，缺 <表名>」，网关算的总表保留为普通电表用来核对。累计电量或累计供冷量前后两条读数相隔超过 1 小时时，恢复后的差值记为「跨缺口电量」：计入周期总用电、分时电费、电费账单核对和月度/年度能源报告总量，但不摊入逐时柱状图、小时 COP 和每日用电曲线，能耗分析与能源报告中单列跨缺口电量。松雅轩已启用合成总表，并按现场核实更新四块电表与四台冷机的对应关系（A 机组电表为 UID 3、B 机组电表为 UID 4、C 机组电表为 UID 5、D 机组电表为 UID 2）；在服务器上执行 ClickHouse 迁移、部署服务并套用站点文件后生效。（#798）
- 关键点位读数中断告警：站点文件里标为关键（`critical: true`）且由设备自己采集的启用点位，现在每个点位各有一条「读数中断」告警（`<设备名>：<点位名>读数中断`，级别「重要」，超过 15 分钟没有新读数就发报，收到新读数自动恢复），不再被同一台设备上别的点位缺读数或从别的设备复制来的温度读数掩盖。每台设备一条的「读数长时间没有更新」（级别「警告」，30 分钟）改为只看这台设备剩下的自有启用点位；设备自有启用点位都是关键点位或只有复制点位时，不再重复生成设备级规则。松雅轩的空调总电表、四台机组电表的有功功率与累计电量、冷量计的瞬时制冷量与累计供冷量、冷冻水总供回水温度 1 和总冷冻水流量共 15 个点位已标为关键点位；在服务器上套用站点文件并重新运行 `alarm-rules` 发布规则后生效。（#796）
- 网页「平台」分组的「集成管理」（`/settings/integrations`）扩展为「现场网关」「电表数据核对」「传感器校准」三个页签：保留原有五段式数据链路与网关接入码及停用管理；「电表数据核对」对比冷站近 24 小时有效电量与含可疑样本电量（配图表与表格切换，缺数据或未结束时段画斜线占位），在表计列表中展示 24 小时逐时质量条、近 24 小时有效与可疑差值电量及回零翻转规则，点开右侧抽屉可看通信通道、采样周期、逐时增量图/表、一键跳转「历史趋势」或创建自动去重的现场核查工单（明确标明平台保留原始采集记录、不在网页端手工覆盖历史电量）；「传感器校准」集中核查数值点位的仅水泵稳态探头零点偏移建议（标明「未采用」，不自动改写原始读数）、小温差测量不确定度区间、物理一致性提醒与传感器定检到期状态（未录入到期日时如实显示「未登记」），点开右侧抽屉可看物理传感器厂家型号、关联设备铭牌基准、逐时均值图/表并一键创建自动去重的校准工单。（#790）
- 网页「能源」分组新增「电费账单」（`/energy-analysis/billing`）：支持站点管理员按自然月设置冷站「月度电费预算」，首屏对比当月累计估算电费、预计全月估算电费、预算匀速参考线与尖峰/峰/平/谷电费占比（月内若有缺少电价覆盖的时段会如实标出未定价电量，不会把部分电费冒充整月总额）。收到供电局发票或通知单后可按「冷站专变」或「整栋总表」录入两部制「结算电费单」草稿（拆分电度电费、按容量或最大需量的基本电费、支持负数奖励减免的力调电费及其他代征附加），在右侧抽屉按供电局实际跨月抄表起止日期自动对齐平台冷站电表：冷站专变直接核对抄表电量与电度电费偏差，整栋总表按冷站电量实测占比（或财务指定比例）与整栋综合单价折算冷站应分摊电费；核对无误确认归档后永久锁定，并同步显示在对应月份「能源报告」（`/reports`）的「电费构成」一节中。（#785）
- 网页「运行」分组新增「运行计划」（`/operations/schedules`）：由登记模块保存站点每周 7×24h 预期供冷窗口、预冷提前窗与停机余冷滞后窗（支持跨午夜时段自动拆分为当日与次日凌晨两段，目标冷冻水供水温度明确标明仅作值班交接参考、不会自动下发设备指令）。首屏 `7×24h` 矩阵叠加分时电价底色，按 15 分钟历史遥测自动标出停机窗口内的「计划外开机」，点开右侧抽屉可一键跳转「历史趋势」查看前后 30 分钟曲线，或创建自动去重的现场核查工单。「例外日程」页签支持值班人员申请节假日停机、维保停机或加班供冷，由运维主管或管理员审批生效（按站点日历日全天覆盖常规周计划，同一站点已生效例外的日期区间互不重叠；审批通过加班供冷后，对应时段的计划外开机标记自动解除）。（#780）

#### 修复

- 修正登记模块读取站点合成总表分表清单（`GET /internal/v1/registry/sites/{siteId}/total-electricity-sources`）时误查不存在列名 `energy_types.code`（实际列名为 `energy_code`）导致返回 `503` 的问题，使遥测工作进程能将各机组电表分表登记写入 ClickHouse `analytics.site_total_submeters`；同时把 `semantic/cube` 的内容摘要传入 `cube` 容器环境变量并在服务器解包时同步清理已删文件，改过语义模型文件后执行 `local:up` 或服务器部署会自动重建 `cube` 加载新定义。
- 设备在线状态现在只认这台设备自己发来的读数，并每分钟复查一次：站点文件里带 `from` 的复制点位（例如从温度模块复制到机组电表、冷冻水泵、冷却塔和室外环境上的温度或流量）仍照常更新最新读数与历史，但不再产生这台设备的在线信号；遥测每分钟复查一次在线状态，只收到复制读数或完全不再上报的设备超过离线时限后会自动变成「离线」并推送状态变化，不再停留在最后一次收到读数时的「在线」。没有自有启用点位的设备（松雅轩的冷却塔、室外环境、房间）改为不判断在线（`NOT_APPLICABLE`）；在服务器上部署并套用站点文件后生效。（#797）

### 2026-11-11

#### 新增

- 网页「能源」分组的「优化建议」（`/energy-analysis/optimization`）拆成「当前建议」与「模型与负荷分析」两个页签：首屏只留调温结论、预计日省电与估算电费、温度范围条和单行「前置与安全条件」摘要，点开再看 6 项真实检查明细、回退和 30 分钟核验说明；站点还没有可写供水温度设定点时，不再摆一个灰掉点不了的「按建议下发」按钮，改为一键「创建人工调温工单」（自动填好目标供水温度、基准值、预计节省和回退温度，同一条建议已建过未结束工单时直接给工单状态和跳转链接）。「模型与负荷分析」页签按需读取冷机 COP 模型影子评估（`GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`，只读对照、剥离可执行字段），并排对比策略人工系数与站点近 14 天历史拟合系数、三档候选供水温度、每台冷机的拟合误差 CV(RMSE) 与样本筛除统计，以及未来 24 小时负荷预测。（PR #779）
- 网页「监测」分组新增「历史趋势」（`/operations/trends`）：把冷站总电表、冷量表、各台冷机、水泵、冷却塔和室外环境的最多 8 个遥测点位放在同一条时间轴上排查。顶部带 4 个随当前站点已接入点位自动匹配的排障预设（「冷站功率与供回水」「各冷机运行对比」「水泵流量与温差」「室外环境与供冷负荷」），不同单位的点位自动分栏画图、双向同步悬停游标，设定值画成阶梯虚线，每段读数同时画出最小到最大的浅色带，缺读数的时段留白断开。支持 1 小时（每 1 分钟）、6 小时（每 5 分钟）、今日、7 天、自定义时段以及与前一日或上周同时段的虚线对比，右上角可切换到「统计表」看最新值、均值、极值发生时刻和读数完整度，底部用状态条按桶内主导状态展示冷机等设备的运行和停止时段。可开关「叠加告警」和「叠加控制指令」（密集事件自动合并成一条气泡，点开可直接带入趋势时段与排查点位由告警创建工单；没有对应权限时开关置灰说明原因，不挡看图），告警详情和故障诊断详情新增「查看当时趋势」按钮，一键带入事发前后 30 分钟窗口与相关点位。（PR #772）

#### 修复

- 实时运行页的今日趋势和设备详情侧栏的最近 24 小时曲线，遇到设备断线缺读数的时段现在会留白断开，不再用直线把断线前后的读数连起来。（PR #772）
- 控制指令页在输入设定值时，使用中文输入法按回车确认候选词不再误触发指令下发弹窗；网页切换「系统 / 浅色 / 深色」外观时，浏览器的自动填充底色、原生滚动条兜底和系统弹窗现在会跟着页面深浅色一起变化，打印报告时保持浅色。（PR #777）

### 2026-11-10

#### 新增

- 点位数据质量。每个点位可以在站点文件里配置量程、单次跳变、通讯异常值（松雅轩的 38.4）、卡住（机组在运行时更短、停着时更长）和过期时限。超出量程的读数仍然拒收，页面留着上一条可信的数，并标明这个点位无效；跳变、异常值、卡住和「供冷量用的温度不可信」会把这条读数标成部分。设备页上能看到可疑、无效或过期。冷机 COP 的 15 分钟数据只使用质量为好的读数。松雅轩的告警从「每条温度 6 小时不动报一条提示」改成数据质量告警：供回水和供冷量不可信是严重，其余是警告。站点文件和规则文件改了，但要先做迁移、再部署、再套用站点文件、最后发布告警规则才会在现场生效。（#750）
- 设备铭牌和物理一致性。冷机、水泵、冷却塔的铭牌写在站点文件里，套用后进资产登记；还没确认的项（松雅轩水泵是 22 kW 还是 15 kW）不写成额定功率。湿球高于干球、湿球不像湿球、功率超过铭牌 15%、能效超过水温卡诺上限的 70%、冷冻水或冷却水方向反了、热平衡对不上，都会把这条读数标成部分，设备页显示可疑，并各发一条告警。冷冻水泵在跑但没有机组运行只发告警，不把运行状态标成可疑。松雅轩四台冷机按铭牌 572 kW / 127.5 kW 登记；冷却水流量还没有，热平衡先不算。要先做迁移、再部署、再套用站点文件、最后发布告警规则才会在现场生效。（#751）
- 冷量和能效的测量不确定度。瞬时制冷量和冷站 COP 显示由探头允许误差合成的不确定度。两支 ±0.1 K 的温度探头在总管温差大约 0.9 K 时，冷量和能效大约不确定 ±16%，这条冷量标成部分。实时冷量和冷站 COP 照常显示数值，标「低可信」并给出区间；COP 均值、故障诊断、小时供冷和节能验证不再用这一段。水泵在跑、冷机都停、供回水稳定时，平台估计供回水探头零点，只作为建议显示，不改读数；零点过大或比上次又漂了会报警。温度默认 ±0.1 K，流量 ±2%，功率 ±1%。要先做迁移、再部署、再套用站点文件、最后发布告警规则才会在现场生效。（#752）

#### 变更

- 松雅轩的温度读数按现场 2026-11-08 的手绘点位表对应：四台机组的冷冻水出水、冷却水进水和出水温度都有了（之前 A、B 的出水温度和四台的冷却水进水停用），A 机组多了一个冷冻水支管进水温度，冷却塔有了出塔和进塔水温；室外温度改读通道 15，室外湿球温度没有探头了，不再显示。房间还没有温度探头，房间温度不再显示（之前显示的是别的通道），告警「房间温度过高」和松雅轩的优化建议先去掉，装上探头后再加回来。告警「冷却水出水温度过高」从只看 C、D 改为看四台。温度采集模块各通道的名字跟着改了，例如通道 1 从「D 机组蒸发出水温度（数据判定）」改成「A 机组冷冻水出水温度（蒸发出）」。

### 2026-11-04

#### 新增

- 小程序总览加了两张图：「今天用能」卡里是今天每小时用电，每根柱子上的横线是昨天同一小时用了多少；「冷站现在」下面是今天每小时能效，和运行页的那张一样（#748）。

#### 变更

- 小程序总览最上面始终是「本月节省」：还没算出来时写「—」和原因，比如「还没有在统计的节能措施」，下面紧跟「还能再省」。以前没有数时这两项挪到告警下面合成一张小卡（#748）。
- 小程序先藏起 AI 助手，好过微信审核（生成式 AI 的功能要先有备案或资质）：底部导航只剩总览、运行、通知、我的四项，告警详情去掉「问助手」，助手的页面和代码不进上传包。告警详情在不能确认时不再显示底部按钮栏。网页的 AI 助手不变（#747）。

### 2026-10-30

#### 新增

- 优化服务每次发布建议后，再用「冷机 COP 模型」影子求解器算一遍，只记录、不发布：它用站点最近 14 天的 15 分钟数据给每台冷机拟合能效（负载率、冷冻水出水温度，有冷却水温度时也算进去），按拟合结果比较冷冻水出水温度升 1 ℃、不变、降 1 ℃ 哪个用电最少，结果和拟合误差 CV(RMSE) 写进优化评估表（质量 `SHADOW`）。页面上的建议仍是原来的求解器给的，不会因此下发任何指令。冷却水温度重设现在做不了：没有站点有冷却水温度设定点，评估里写明「不可用」。设 `OPTIMIZATION_SHADOW_SOLVER=OFF` 可以关掉。（#744）

#### 变更

- 松雅轩接入清单跟上租户现在生效的优化策略：供水温度范围 5–8 ℃、室内温度读数 room_temperature；`validate` 提示「低温差诊断未启用」时，门槛从数据库里现在生效的诊断规则版本读出（例如「现在生效的是第 2 版：温差低于 1.5 K 持续 15 分钟」），不再写死在清单说明里（#745）。
- 模拟冷站的室内温度传感器点位代码从 temperature 改成 room_temperature，模拟冷站初始化写的优化策略也改用 room_temperature、供水 5–8 ℃，和松雅轩一致，同一个租户策略在各站读同一个键（#745）。已经部署的环境要重新初始化模拟冷站、并且模拟器重新上报之后才有 room_temperature 的读数；改名前的历史读数仍记在 temperature 下。

### 2026-10-30

#### 新增

- 优化建议页没有建议时，会说明最近一次定时计算为什么没生成，例如「最近一次定时计算（10/30 12:20）没有生成建议：读不到可用的实时室内温度」；站点从没有定时计算过时写「站点还没有配置定时优化，建议只能手动生成」。已有建议、但之后一次定时计算失败时，建议上方提示「最近一次定时计算没有生成新建议」和原因，下面的是上一条建议。以前只写「还没有优化建议」，看不出是缺负荷预测、电价、温度读数，还是策略、模型没配。原因记在调度任务的错误代码里（共 14 种，见 `docs/specs/scheduled-optimization.md`），网关新增 `GET /api/v1/sites/{siteId}/optimization/preparations/latest` 读最近一次的结果。这个改动之前失败的定时计算读出来是「计算没有完成，原因没有记录」（#738）。
- 优化建议的「计算依据」列出策略的响应系数（供水每升高 1 °C 冷站日用电、室内温度怎么变），标「人工设定」：这些数是人在优化策略里填的，不是从站点运行数据拟合的（#738）。
- 站点接入脚本改成两个动作：`validate` 只读数据库，按「站点和设备、点位、能耗和负荷预测、告警规则、诊断、优化建议」列出松雅轩已经有的、还缺的、要改的和要人决定的；`apply` 写入缺的和要改的（部署每次运行它），重跑不改没变的行。站点文件新增低温差诊断、优化建议和告警规则文件的对应关系；告警规则读的点位、诊断读的供回水温度、优化策略里的点位代码在本站对不上时，脚本直接报错，什么都不写。优化策略和诊断规则是租户共用的，已有而和站点文件不同时只列为「要决定」，不由一个站点改；本站的优化建议只在策略一致时才登记，所以松雅轩的优化建议要等决定租户策略之后才开始出（#740）。
- 松雅轩登记资产「冷冻水总管」和每 15 分钟一次的低温差诊断，先不启用：诊断规则按温差低于 3.5 K 判定，松雅轩高负荷时总管温差只有约 1.65 ℃，启用会一直报，要先定本站的门槛（#740）。

#### 修复

- 优化、诊断和报告页能分清「还没有结果」和「服务器上没开这个功能」。以前智能服务没配置、或者服务器上的版本还没有这个接口时，平台也回「未找到」，页面就显示「还没有发布建议」「这个月没有存档」，看起来一切正常。现在这种情况页面显示「此功能尚未在服务器上启用」；确实还没有结果的，仍然显示原来的空状态。手动诊断失败时，「诊断服务连不上」显示「诊断服务暂时不可用」，只有诊断服务答复了但拒绝诊断（例如窗口内没有可用的供回水温度）才显示「诊断未完成」。(PR #741)
- 指标计算（例如每小时用电量）里的累计电量差值，在点位改版或换单位时断开，不再把改版前后两个读数之差算成用电或计数器归零。以前同一点位改了配置、电表从别的读数重新开始时，那一步会作为一次大的用电（或者一次没有归零策略的下降、整个指标算不出来）落进那个小时；能耗记录本来就在这里断开，现在两边一致。（PR #739）

#### 改进

- 能耗分析、能源报告和 AI 助手按天、按月查站点和机组用电量时，整小时读按小时汇总好的数据，只在查询的起止不在整点时读单条记录，结果不变。在模拟数据上，一个站点一个月按天查从约 240 毫秒降到约 55 毫秒。电费仍按单条记录算。能耗差值查询也只读相关点位的读数，不再每次扫全部累计读数（一个窗口从约 750 万行降到约 30 万行）。上线前要在服务器上手工应用并回填，见 `docs/operations/energy-hourly-rollup.md`。（PR #739）

#### 变更

- 冷冻水低温差诊断只在冷站运行时判断（#743）。以前只看窗口里最后一对供回水温度，冷站停着时温差接近 0，夜里也记成低温差；松雅轩 10-16 至 10-30 的 1344 个 15 分钟窗口里旧做法判了 1323 个。现在按分钟统计整个窗口：冷机开机（有额定功率时看功率是否高于额定的 10%，没有时看运行状态）、负荷率不低于 15%（冷机都登记了额定冷量才用）、冷冻水泵运行（登记并绑定了水泵才用）；运行时间不到窗口的 70% 时结果是「未运行」，不记诊断结论；运行时间里低于阈值的分钟达到 70% 才算低温差，测量值是运行时间的平均温差。门控是规则版本的一部分，已有版本按这些默认值，阈值不变（松雅轩仍是 3.5 K）。诊断页「立即诊断」会说明这次窗口里冷站运行了几分钟。

### 2026-10-29

#### 修复

- 页面顶部的实时推送状态（读屏软件会读出来，集成管理和系统管理页也显示）不再一直是「未连接」。以前实时页、总览这些页面打开了推送，却没人把状态报给页面顶部，所以永远显示「未连接」，看起来像推送坏了。现在这些页面打开推送后会显示「正在连接」「已连接」「重连中」「正在重新读取」「已断开」；一页里有几处同时在推送时，显示最差的那个（例如一处已连接、一处重连中，就显示「重连中」）。没有打开推送的页面（没有实时推送权限、设备点位太多改为每 30 秒刷新、或者这个页面本来就不看实时读数）显示「未订阅」，表示没在推送而不是推送断了；集成管理和系统管理页上「已断开」从灰色改成黄色提醒。
- 从仓库删掉的告警规则，发布后在服务器上也会删掉，Prometheus 不再继续加载。以前发布只把 `source.tar` 解压到 `/opt/hvac-platform`，解压只新增和覆盖、不删除，删掉的 `infra/observability/alerts/*.yaml` 会一直留着生效。现在解压后把告警规则、Grafana 看板和 Grafana provisioning 三个目录对齐成发布里的样子，删了哪个文件写在部署日志里；这三个目录以外的文件不动。
- 发布后，改过的告警规则和告警接收配置会真正生效。以前这些文件是单个挂进 Prometheus 和 Alertmanager 的，发布换了文件，容器仍读旧的，要等哪天重启才生效。现在发布前先用 promtool / amtool 检查（不合法就不发布），发布通过后重启变了的那个容器，并确认它已经加载成功。
- 主机磁盘「预计 24 小时写满」不再在部署时误报。以前按最近 2 小时的趋势算、剩余不到 50% 就判断，一次部署 20 分钟写进 1 到 2 GiB 就被当成长期趋势（2026-10-28、10-29 两次都在部署时报）。现在按 12 小时的趋势、剩余不到 25% 才判断，连续 1 小时才报；另加一条「预计 4 小时写满」（6 小时趋势、剩余不到 40%、连续 30 分钟），专抓失控写盘。
- ClickHouse 自己的查询记录（`system.query_log`）从留 7 天改成留 3 天，每条查询只记结束或出错那一行，不再多记一行「开始」。2026-10-29 它有 718 MiB，比遥测数据本身还大；改完预计降到 300 MiB 以下。上线后旧表会被 ClickHouse 改名成 `query_log_0` 留下，按 `docs/operations/disk-full.md` 手工删一次。
- Prometheus 不再把 Centrifugo 报成挂了。以前每 15 秒用明文去抓只开 TLS 的 8000 端口，抓取失败，`Phase1ProcessDown`（警告）会亮，Centrifugo 日志里也每隔 15 秒一条 TLS 握手错误。指标改从容器网络里的明文 9000 端口抓；8000 仍只给实时连接和发布接口用。
- 松雅轩温度通道 14 停用。它从 2026-10-14 03:26 起每分钟原始值都是 0、质量位非 0，是没接探头；室外环境上从它复制的第二个室外温度一起停用。设备页和「一动不动」不再盯这条。网关上的采集函数要单独重新部署，在那之前平台把送来的这条读数当成停用点位，不记成有效读数。
- 每次部署重跑站点入网脚本，不再把松雅轩所有设备、点位和绑定都改成「刚修改过」。以前重跑时每一行都重写一遍、`updated_at` 刷成当时，2026-10-29 那次部署后 67 条当前生效的点位绑定和 81 个点位都显示刚改过，而真正从启用改成停用的 14 条绑定修订号仍是 1。现在只有站点文件里值真的变了的行才写，写的时候修订号加 1（和在注册表页面上修改一致）；值没变的行原样不动。之前留下的修改时间和修订号不回填。
- 站点文件里改了设备的在线判断或点位的新鲜度设置（比如采样周期），对应策略的修订号（`policy_revision`）会加 1。以前入网脚本只改值、修订号一直是 1，遥测给每条读数和在线状态记下的「按哪一版策略算的」分不出改前改后。没改的策略修订号不动；之前改过的不回填。
- 登录时输入的用户名不存在，身份安全审计不再记下这串输入的 SHA-256。有人会把密码误填进用户名框，没加密钥的 SHA-256 可以拿候选密码离线逐个比对。现在这类记录用身份服务进程启动时随机生成、不落盘的密钥做 HMAC-SHA256，同一进程内对同一输入仍是同一个引用，重启后会变；已有账号的审计引用不变。读账号的语句也不再和密码哈希一起读，CodeQL #4（`go/weak-sensitive-data-hashing`）随之消失。不需要新密钥，服务器上不用做任何事。
- 读数合格率不再在流量低时误报。以前不到每秒 1 条时，告警把分母抬成 1，松雅轩大约每秒 0.97 条、合格率约 98% 却被算成 0.91，从 2026-10-14 起一直报 `S2DataGoodRateLow`。现在先算真正的比例，10 分钟里不到 100 条就不报。快照出错、断线补发失败、指令和告警接口的比例告警同样改了。

#### 新增

- 部署流水线手动运行时可以勾选 `rebuild_base`：先把每个服务的部署基础镜像（`<名字>:deploy-base`）按正在跑的镜像重建成一层，比对配置和每个文件都一致之后才换上，再照常部署；有任何不一致就停下，什么都不改。旧基础之后会被清掉，退回到 `before-*` 再部署也不会把它找回来。推到 `main` 的部署不重建，和以前一样。步骤见 `docs/operations/disk-full.md`。
- 平台告警可以发到企业微信群机器人，也可以发邮件。密钥放在服务器运行目录里，不进仓库；两个都没配时，告警仍只留在 Alertmanager，不会发到错误的地址。磁盘超过 85%、预计 24 小时写满、内存和交换分区吃紧、容器反复重启、备份超过 26 小时、公网证书不足 14 天、数据库和 MQTT 中断，都会出现在这套告警里。

### 2026-10-22

#### 新增

- 告警页多了一个「正在生效的规则」页签：列出这个站点现在按哪些规则判断告警，每条写明规则名、看哪些设备、什么时候报警、什么时候恢复和严重程度，例如「……供水温度高于 10 °C，持续 15 分钟」。规则随站点的规则文件发布，这里只能查看。告警因为规则被撤销而恢复时，处理记录里写「规则已撤销」。（#693）

#### 变更

- 系统管理里的「自动化规则」页签删掉了：规则能发布，但从来没有程序执行它们。告警和低温差诊断各有自己的规则，不受影响。管理员的权限清单里也不再有「自动化规则」。（#691）
- 网页的「通知」改成直接列这个站点的告警，和小程序的通知页一样：上面是正在发生的（严重的在前，写着几点开始），下面是最近恢复的 20 条（写着几点恢复，同名、同一分钟恢复的合成一行，写「同时恢复共 N 条」），可以只看没确认的；点一条在右边看告警详情，可以确认、指派或创建工单。顶栏铃铛上的小红点表示这个站点有正在发生、还没人确认的告警。原来的通知收件箱、通知规则和模板删掉了：服务器上从没配过规则，收件箱一直是空的。（#691）

#### 修复

- Redis 重启或挂掉时，看设备当前读数的页面（网页的实时运行、小程序的「冷站现在」等）不再一片空白：设备当前值改从 PostgreSQL 里保存的那一份读，Redis 恢复后照常从 Redis 读。只有 PostgreSQL 里也没有这台设备，才显示没有数据。（#692）
- 告警规则文件里删掉或改名的规则、设备不再有启用点位的规则，重新发布后会被撤销，不再报警；它正亮着的告警会自动恢复。以前这些旧规则一直留在站点上，可能误报。（#693）
- 松雅轩 C、D 机组的水温改对了：C 机组的「冷冻水出水温度」以前读到 29–33 ℃（其实是冷却水），现在是真正的冷冻水出水，冷却水出水也有了读数；四台机组的「冷冻水进水温度」改成总冷冻水回水。现场手绘的通道对照表和读数对不上，这次按一周的数据判定（哪一路的温差跟着哪台机组的功率走）。A、B 机组这几天没开过，看不出接线，它们的出水温度和四台的「冷却水进水温度」先停用，等现场核对后再接回；没有探头的温度通道 8 也停用。改之前的历史读数不变。（#614）

### 2026-10-21

#### 新增

- 微信小程序可以微信一键登录：登录页「登录」下面多了「微信一键登录」，在「我的」里绑定过微信的人点一下就登录，不用输账号密码；开了验证码的账号还要再输验证码。没绑定的微信会提示「这个微信还没有绑定账号，先用账号密码登录，再到『我的』里绑定」，不会新建账号；停用或锁定的账号提示「这个账号现在不能登录，请联系管理员」。下控制命令仍然要密码加验证码。（#664）
- 微信小程序可以每天早上收昨天的运行结论：总览最下面的「每日运行结论」写着还能收几天、发的是哪个站点，点「多收一天」、在微信里允许就多一天（在哪个站点点的，以后就发哪个站点）。每天站点时间 8 点以后，微信里会收到一条「昨日能效数据查询提醒」：站点、昨天的用冷量和用电量（比如 7402kWh、1691kWh）、系统 EER（用冷量 ÷ 用电量，比如 4.38）；平台不单独算主机 COP，那一格是「-」，读不到的数也写「-」。点开进入总览。每人每天一条，发不出去的过 1、4、9 分钟再试，站点时间 12 点以后不再发。（#664）
- 微信小程序可以收告警的微信提醒：通知页顶部的「微信提醒」写着还能收到几条，点「多收一条」、在微信弹窗里点「允许」就多一条（个人主体的小程序只能用一次性订阅，允许一次能收一条，勾了「总是保持以上选择」以后再点就不弹窗）。某个站点出现紧急或重要的新告警时，每个有次数、现在还能看这个站点告警的人会在微信里收到一条「监控告警通知」：告警标题、紧急或重要、站点、开始时间（站点时间），点开就是这条告警的详情。同一条告警只提醒一次；发不出去的过 1、4、9 分钟再试，告警开始 30 分钟后就不再发。（#664）
- 微信小程序「我的」里可以绑定微信：点「微信」一行，微信确认后，这个账号就和你的微信绑在一起；已绑定的可以点它解除。一个账号只绑一个微信，一个微信也只能绑一个账号：已经绑了别人账号的微信会被拒绝，并提示先在那个账号里解除。这是后面「微信一键登录」和「告警微信提醒」的前提。服务器要先由管理员填好小程序的 AppSecret，没填时绑定会提示「服务器还没有开通微信功能」。（#664）
- 微信小程序有了「通知」：底部导航第四个，上面的红色数字是这个站点正在发生、还没人确认的告警条数。通知页直接列告警：上面是正在发生的（严重的在前，形状也表示严重程度：◆ 紧急 ▲ 重要 ● 次要 ■ 预警 ○ 提示），下面是最近恢复的，可以只看没确认的。告警详情写着它是什么、哪台设备、什么时候开始、持续多久、报过几次，可以写一句说明后确认（确认不会让告警消失，读数恢复后它自己恢复），也可以「问助手」让助手解释这条告警。总览上「正在发生的告警」点一下就到通知页。（#664）
- 微信小程序有了「助手」：底部导航里凸起的圆形按钮。新对话页有四个可以直接点的问题（比如「今天哪几个小时能效最低？」）和最近的三个对话。问了以后先看到助手正在查哪些数据，回答一个字一个字出来，第一行是加粗的结论；下面是结论卡片：有没有数据依据、关键数字、图、数据说明不了的地方，再下面是「接着问」。助手反问时点选项或在输入框里回答，回答中可以停止，「历史对话」能打开以前问过的对话接着问。对话和网页的 AI 助手是同一份，在网页上问过的手机上也能看到。（#664）
- 微信小程序有了「运行」页：「实时运行」先说冷站在运行还是已停机、主要设备几台在转，再给冷站总功率、制冷量和能效（停机时不算能效），下面是今天每小时的能效柱子（没读数的小时画斜线并写明是几点，比如「12–14 点没有读数」），以及冷冻水、冷却水、室内、室外的读数，读数过期的会标出来。「设备」按冷站主设备、计量和环境分两组，可以只看运行、停机或需关注（离线、故障、读数过期）的设备；点一台设备看它的全部读数和今天每小时用了多少电、占全站几成。总览上多了一张「冷站现在」，点一下进入运行页。设备的分类、状态和读数和网页「实时运行」用同一套规则。（#664）
- 微信小程序第一步：值班人员用平台账号（开了验证码的再输验证码）登录小程序后，总览最先看到本月省了多少钱、少用了多少电、在统计的节能措施各省了多少，然后是今天的用电、电费、供冷和能效（和昨天同一时段比，变差标红、变好标绿）以及正在发生的告警条数；「我的」可以退出登录。账号、权限、停用和退出与网页相同。（#664）

#### 变更

- 小程序换上和网页一样的 lucide 图标：底部导航的总览、运行、我的（总览和网页一样用仪表盘图标），卡片标题前的小图标（今天用能、冷站现在、每日运行结论、今天每小时能效、水系统和环境、今天用电、全部读数、微信提醒），水系统每一行（冷冻水、冷却水、室内、室外），「我的」里的当前站点、微信和退出登录；主要数字的名称前（用电、电费、供冷、能效、功率、温度），设备详情的每个读数前按测的是什么配图标（电、冷量、温度、流量、频率、百分比、湿度、开关机、故障），告警详情的设备、开始、恢复、持续、报过几次前也有图标。助手的图标换成网页的泉来禾风车标志，放在一颗没有白边的靛蓝圆球里：球里的光朝和风车相反的方向缓缓流动，像风吹过叶片；风车平时慢慢转（12 秒一圈），助手查数据、回答时，回答前的风车和光都变快（风车 1.4 秒一圈），答完停下。底部导航中间的圆球只比导航栏高出一点，不挡页面；球有明暗和高光，看起来更立体。登录页的品牌方块里也是这个风车。（#677）
- 小程序登录页：品牌方块里有了标志；「登录」和「微信一键登录」之间多了一条写着「绑定过微信的」的分隔线，微信按钮前有微信图标，一眼能看出是另一种登录方式；底部说明分成两行短句，不再剩一个字折到第二行。（#677）
- 小程序助手页：在助手页上，底部导航中间的「助手」不再凸起，变成普通的蓝色选中项，输入框不再被它压住。从告警「问助手」开始的对话，在「最近的对话」和「历史对话」里不再以日期开头，比如显示成「告警「室外环境读数长时间没有更新」是怎么回事？」，什么时候问的照旧写在下面一行。（#677）
- 小程序「运行」和「设备」的数字写法统一：温度一律一位小数（供水 5.0 °C、室内 27.7 °C），上千的数加千分位（累计电量 169,333 kWh），读数后面不再带句号；总览「冷站现在」也一样。「设备」里不再列网关上的温度采集模块和控制模块，也不算进「几台在线」（松雅轩从 13 台变成 11 台）。一直没有读数的设备只写「没有读数」，不再同时写「在线」和「—」。实时运行最下面重复的「设备」入口去掉了，用顶部的页签进入。（#677）
- 小程序通知页更好扫：每条告警前面是带形状的等级小标签（比如浅橙底「▲ 重要」），最底下那行「形状代表严重程度」的图例去掉了。「最近恢复」的告警文字变灰，等级标签也变灰（形状还在）；有人看过的不再挂标签，没人看过的是灰色「未确认」，橙色「未确认」只留给正在发生、没人确认的；同名告警在同一分钟恢复的合成一行，写「共 8 条」，点开是没人看过的那条（都看过就是最新的）。「微信提醒」挪到告警列表下面，「多收一条」改成描边按钮，说明缩短（比如「还能收 3 条（紧急和重要的新告警）」「允许后，紧急和重要的新告警发到微信」）。告警详情的说明太长时先显示三行，点「展开」看全部；「已确认」从绿色改成灰色，确认不等于问题解决。（#677）
- 小程序总览更紧凑：还没有节能措施、优化建议时，不再在最上面放两张说明卡，改成告警下面的一张小卡，每项一行（比如「本月节省 · 还没有在统计的节能措施」），一打开先看到今天的用电、电费、供冷和能效。「正在发生的告警」一行下面写着其中几条没人确认，和底部「通知」上的红点是同一个数。「每日运行结论」的「多收一天」改成描边按钮，没次数时的说明缩短成「每天 8 点把昨天的用电、用冷和能效发到微信」。所有页面的卡片加了很浅的阴影，内边距统一；总览的主要数字更大更粗；每页最底下的内容不再被中间的助手按钮挡住。（#677）
- 平台接口说明里删掉了 9 个从来调不通的接口：切换租户（`/api/v1/auth/tenant-contexts`、`/api/v1/auth/tenant-context`）、通用的权限修改和 API 凭据（`/api/v1/iam/admin/mutations`、`/api/v1/iam/api-credentials/*`）、按点位读最新值和历史（`/api/v1/devices`、`/api/v1/telemetry/latest`、`/api/v1/telemetry/history`）。前 6 个在服务器上一直回 404，后 3 个一直回 503；网页和小程序都没用过它们，页面没有变化。以后接口说明里的每个网关接口都必须登记负责方，漏了检查会报错。（#672）
- 删掉了 API 凭据：IAM 里发、换、收回 API 凭据的内部接口，存凭据和服务账号的两张表，管理员的 `api-credential.manage` 权限，部署时生成凭据密钥的一次性程序，以及每小时到期检查里查 API 凭据的那一半（网关证书的到期检查照旧）。网关从没转发过这些接口，网页和小程序也没用过，页面没有变化；管理员的权限清单里少了这一项。（#673）
- 小程序助手回答里的表格可以左右滑动：第一列（机组、日期这类）不再被挤成一个字一行，只有一列全是数字时才靠右，很长的说明在一定宽度内折行。（#664）
- 小程序「运行 → 设备」里没有功率读数的水泵（比如松雅轩的冷冻水泵总管），右边显示流量，下面那行不再重复写流量。（#664）
- 小程序五个标签页的标题改用微信自带的导航栏（和告警详情、设备页一样），不再在页面里自己画大标题；通知页「只看没确认的」换成微信的开关，助手结论卡片里各台机组的横条换成微信的进度条。（#664）

#### 修复

- 小程序助手结论卡片里的折线图不再浮在页面上：以前回答往上滚时，折线留在屏幕原处，盖住文字、输入框和底部导航。现在折线图是一张跟着页面滚动的图片，单独一个小时的读数也画成一个点。（#664）

### 2026-10-20

#### 新增

- 松雅轩有了「一动不动」告警：某个温度读数连续 6 小时完全没变（比如 10/16 室外温度一整天停在 21.5 ℃），告警页出现一条「提示」，标题就是这个读数，比如「室外温度（通道 13）一动不动」；读数一变就自动恢复。温度采集模块的 18 个通道和冷量计的 2 个温度各一条；机组电表上的温度是从温度采集模块复制来的，不重复报。（#616）
- 松雅轩有了「网关上报中断」告警：整个站点 15 分钟没有收到任何读数时（现场网关断电、断网或程序停了），告警页出现一条「重要」告警，重新收到读数后自动恢复。以前告警只在收到新读数时才计算，网关整个断线时什么都不会报；现在告警服务每分钟会把到时间的判断再算一遍，各台设备的「读数长时间没有更新」也不再要等别的设备有读数才能报出来。（#615）
- AI 助手的结论卡片上可以有一张小图，放在关键数字上面：问每小时的用电是折线，问几天或几段时间对比是柱状，问各台机组是从多到少的横条，问设备最近的读数是折线（比如冷冻水泵的流量）。图里的数都是助手这次读到的数据，助手自己不写图里的数；只是一两个数的回答不画图。（#640）
- AI 助手面板顶部多了「历史对话」按钮：列出你在当前站点最近问过的 50 个对话，最新的在上面，每条显示第一个问题、开始时间和答完没有（答完、没答完、已停止、正在回答、等你回答）。点一条就打开那个对话，原来的问答和结论卡片都在，可以接着问。每个人只看得到、打得开自己问的对话，别人的对话不再出现在你的面板里。（#639）

#### 变更

- AI 助手的回答第一行就是加粗的关键结论，比如「**10 月 15 日比 10 月 8 日多用电 262 kWh，主要是负荷变大了，多出的电集中在 14–18 点。**」，后面才是支撑它的数字和小表，正文里别的地方不再加粗。以前有的回答先写一个加粗的标题，或者先写数据说明，要读到后面才知道答案（#663）。
- 网关断线恢复后，告警追上积压读数快了约 4 倍：以前每收到一台设备的一批读数，都要把站点的 34 条告警规则全算一遍，现在只算用到这台设备读数的规则（比如冷冻水泵的一批读数只算 2 条）。2026-10-20 晚上松雅轩剩下的约 5,000 批积压，以前每分钟只能处理约 250 批，现在约 950 批，5 分钟就追完了；「网关上报中断」随即恢复，3 个卡住的温度读数的「一动不动」也报了出来。什么时候发报、什么时候恢复，规则都没变。（#659）
- 报告服务也只接受平台网关签发、写明租户和站点的请求，并且要求双向证书：以前网络里任何程序只要在请求里写上租户编号，就能读到那个租户任意站点的月报和年报。报告页面的使用方式不变。（#613）
- AI 助手的「接着问」挪到对话里：几个建议问题显示在最后一个回答的卡片下面，跟着对话一起滚动，不再固定在输入框上方、压住卡片。只显示最后一个回答的建议，发出新问题后旧的建议就不见了。点一下建议仍然直接发问。（#638）

#### 修复

- 网关断线恢复、告警补收积压读数时，不再出现报了又马上恢复的告警：以前告警拿收到读数的时间去判断旧读数，补到落后 30 分钟左右时，「读数长时间没有更新」会来回报和恢复（2026-10-20 12:47 有 5 条，发出 8 次报警、16 次恢复的通知），落后 15 分钟左右时「网关上报中断」也一样。现在补收时旧读数按它自己的时间判断；补收完 2 分钟内没有新读数，就按当前时间判断，读数真的停了照样会报。告警上显示的发生和恢复时间不变，仍是平台做出判断的时间。（#661）
- MQTT 服务器反复重启之后，接入服务不再停止收数据：2026-10-19 服务器磁盘满时，MQTT 服务器重启了几百次，接入服务一小时后就不再重连，腾出磁盘后读数仍然进不来，直到手动重启接入服务；那段时间 `/health/ready` 还一直显示正常。现在每次断线都会重连，数据库恢复后积压的读数自己补进来；断线时日志记 `mqtt_uplink_connection_lost`，`/health/ready` 变成 503。（#652）
- 服务器磁盘不会再被告警判断记录写满：以前每收到一批读数，站点的每条告警规则都记一行带整个站点读数的判断记录，结果没变也记，松雅轩一天就是 100 万行、约 6 GB，2026-10-19 晚上和 10-20 上午两次把磁盘写满，读数因此断了十几个小时。现在只在判断结果变了时记一行（比如从正常变成发报、从发报变成恢复、从能判断变成读数太旧判断不了），一天几百行；超过 30 天的记录自动删掉。告警本身、告警的处理记录和告警页显示的内容都不变。（#651）
- 服务器磁盘满或 ClickHouse 出错之后，设备的历史曲线和用电量会自己接上：以前写历史读数的程序一分钟内失败 12 次就永远停下，之后所有站点的新读数都进不了历史，要人手工改库才能恢复（2026-10-19 晚上磁盘满了一分钟就是这样）；现在一直重试同一批，ClickHouse 能写了就按顺序补上。（#649）
- 节能项目里登记措施时，填到一半的表单不会再被清空：以前填写中途时间一跨过整分钟，已填的名称就没了，计量电表也回到冷站总电表，点「登记」只会提示「请填写名称」。现在默认的计划实施时间是打开对话框的时间，填多久都不会丢；点「取消」后再打开是一张新的空白表单。（#644）

### 2026-10-19

#### 变更

- AI 助手写结论时不再反复被退回：以前大约每 4 次就有 1 次被退回重写，偶尔连续被退回到次数用完、回答失败；现在评测 72 次一次都没有。能效这类没有单位的数，结论卡片上不再出现「倍」「比值」「无单位」这样编出来的单位。系统管理的「AI 助手」标签里，每个步骤多了「没成功的原因」一列，比如「关键数字太长 3」「数据暂时读不到 1」，只说原因，不显示助手写的内容。（#624）
- AI 助手回答得更快、步骤更少：面板里不再先出现「读取站点信息」这一步，站点名和站点当前的时间已经写在给助手的说明里；问「9 月份用电量和电费各多少」这类同时要用电和电费的问题，一次就能查出来，不会先被拒几次；问各台机组的用电时，不再先读一遍设备清单。评测里每次回答平均读数据的次数从 3.1 次降到 1.7 次，平均用时从 5.2 秒降到 4.6 秒，答对的比例没有下降。助手回答里的温度单位写成「°C」，和设备页一样，不再出现「Cel」。（#593）

- 页面上的说法改成日常用语，意思不变：比如数值变灰时的提示从「数据过期或质量降级」改成「数据过期或不可靠」；报告说明里的「计量区间」「快照」改成「每段用电」「存档的结果」；负荷预测的「预测区间」改成「预测范围」；规则管理的「运行证据」改成「运行记录」；节能项目详情里的「逐日核算」改成「每日节电」；设备登记的导入导出不再出现「拓扑」「规范化登记快照」；集成页的实时推送状态从「未订阅」改成「未连接」。启动失败、页面出错时的英文标题（STARTUP BLOCKED、RUNTIME ERROR 等）换成中文；实时连接状态的说明不再显示站点的内部编号。（#605）
- 系统管理的「资产与点位」和「用户与角色」里，「设备资产」「设备端点」「测点」改成和其他页面一样的「资产」「设备」「点位」，「测点 → 业务对象」改成「点位 → 测量对象」；点位类型里的「命令」「控制命令」统一叫「控制点」。（#636）

### 2026-10-18

#### 新增

- 系统管理里多了「AI 助手」标签：选一个站点，能看到最近 14 天每天 AI 助手答完了几次、没答完几次（出错或被停下）、平均用时和模型用量，旁边一张表列出它读各类数据的次数和失败次数（「读取告警」「读取当前读数」这样的说法，和助手面板一致）。只显示数字，不显示问了什么、答了什么。有「查看操作记录」权限的人（现在是管理员）才能看。（#597）
- 设备页的设备详情里多了「解释」按钮：点一下会打开 AI 助手，填好「『这台设备』最近 24 小时运行得怎么样？现在的读数有没有异常？」，可以改了再发。助手会读这台设备现在的读数、最近 24 小时的读数和它的告警来回答。助手回答里的运行状态也和设备页一样说「运行、停机、待机、故障」，不再出现英文。（#596）
- 松雅轩有了告警规则，告警页开始出现真实告警：①有机组在运行、冷冻水总管供水温度高于 10 ℃ 持续 15 分钟，报「冷冻水供水温度过高」（重要），降到 9 ℃ 以下或机组全部停机后自动恢复；②每台设备只要有一个读数超过 30 分钟没有更新或一直没有读数，报「某某读数长时间没有更新」（预警），所有读数恢复后自动恢复。发布当天就报出了 C 机组电表的冷却水出水温度和室外环境的第二个室外温度一直没有读数。告警只出现在告警页，目前没有配置任何通知对象，不会给人发消息。规则写在 `deploy/sites/songyaxuan.alarm-rules.json`，改完重新发布即可，重复发布不会产生重复规则。（#609）
- AI 助手能回答告警问题：问「现在有哪些告警」「最近恢复了哪些告警」，助手会读当前站点的告警，告诉你每条告警的标题、说明、严重程度（紧急、重要、次要、预警、提示，和告警页一致）、哪台设备、什么时候开始、发生了几次、有没有人确认；没有告警时直接说「现在没有正在发生的告警」。助手只能看告警，不能确认、认领或改动告警。告警页的告警详情里多了「解释」按钮，点一下会打开助手，并填好「某月某日几点开始的告警『……』是怎么回事？」。（#594）
- AI 助手能回答故障诊断和工单问题：问「最近有哪些故障诊断结果」，助手会说出每条结果是哪条诊断规则（比如冷冻水低温差）、哪台设备、哪个时间段、测到多少、阈值多少、偏离多严重，以及是否已转成告警或工单；问「现在有哪些还没处理完的工单」，助手会说出每张工单的标题、说明、优先级、状态（待处理、处理中、受阻）、有没有指派、截止时间和来源（来自告警、来自哪台设备或人工创建）。没有结果时直接说「最近没有故障诊断结果」「现在没有还没处理完的工单」。助手只能看，不能建工单、改工单或重新诊断。助手面板里这两步显示为「读取故障诊断结果」「读取工单」，可以点到故障诊断页和工单页。（#595）

#### 修复

- AI 助手的回答更干净：查数据前那句过程话（有时是英文，比如「I'll start by reading…」）不再显示，也不保存；回答下面卡片里的提醒，如果和正文说的是同一件事，就不再重复显示（比如正文已经说了「14–16 点没有读数」，卡片不再写一遍）。（#607）

- AI 助手面板里，一个问题回答完以后再问（自己输入、点「接着问」或在告警页点「解释」）都会失败，显示「助手暂时没有回应」，只能点「新对话」。现在可以在同一个对话里接着问，助手能看到前面问过什么、答过什么，比如先问 C 机组的告警，再问「那室外环境那条呢？」也能答上。回答出错或被你按停后，也能直接接着问。（#617）

- AI 助手面板里，有时回答显示在问题上面：服务端给这次回答记的开始时间比问题早了 1 毫秒，面板按时间排序就排反了。现在问题和它引出的回答用同一个时间，新问的问题都按先问后答显示。（#591）

#### 变更

- 预测、优化建议和节能验证三个服务也只接受平台网关签发、写明租户和站点的请求，并且要求双向证书：以前网络里任何程序只要在请求里写上租户编号，就能读别的租户的预测和优化建议，还能以别人的名义登记、实施或结束节能措施。现在节能措施记录的操作人就是登录的人。三个页面的使用方式不变。没人用的「按编号查一次优化运行」接口已删除。（#612）
- 故障诊断服务只接受平台网关签发、写明租户和站点的请求，并且要求双向证书：以前网络里任何程序只要在请求里写上租户编号就能读写诊断结果。故障诊断页的使用方式不变。（#595）
- AI 助手不会说自己操作过设备：助手只能读数据。如果它在回答里说了「已为你关闭 B 机组」「我已把设定值调到 8 度」这类话（中文英文都算），这次回答不能就此结束，助手必须紧接着写一句「更正：我没有也不能操作设备」才能收尾，值班人员会在原话下面看到这句更正。描述设备本身状态的话不受影响，比如「C 机组已停机」「群控系统已停止 C 机组」「运行人员已把 B 机组关闭」。（#592）
- AI 助手的回答不再说两遍：结论只写在回答正文里（一两句话，需要时加一张小表），下面的卡片只放「有数据依据 / 数据不足以下结论」、几个关键数字、提醒（例如电费是估算的）、可以点开核对的页面和可以接着问的问题，不再把正文的结论再写一遍，卡片上也不再有「结论」两个字。以前的对话打开后也是这个样子。回答平均约 5 秒（之前约 8 秒）。（#591）
- AI 助手遇到数据有问题时直说：某天有几个小时没有读数、读数可疑，或者温度一整天一动不动时，助手会说「这天的数据不完整，没法判断」，不再替它猜一个原因。问它做不了的事（关机组、改设定值、看别的站点、预测以后的用电）时，会先说明做不了，不会把当前站点的数据说成别的站点的。拒绝这类问题时也会正常结束，不再显示为回答失败。（#591）
- AI 助手说大白话、日期算对：助手查数据时拿到的结果改成运维人员的说法（「数据完整/不完整」「数据截至 10-15 18:30」「有读数可疑，不能比较」、用电/供冷、尖峰/峰/平/谷/未计价、设备名称），不再出现计量区间个数、数据版本、英文状态和设备编号，回答里也就不再冒出「区间」「口径」这类词。日期一律按站点本地日期、首尾都算：问「10月13日到16日」就是这四整天，不再少算一天；站点有夏令时切换的那天也按完整的本地一天算。助手参数写错时，会收到一句中文提示告诉它该怎么改，平台内部的错误信息不会传给它。（#590）

### 2026-10-17

#### 修复

- AI 助手读不到设备的当前读数：问「各台机组现在的冷冻水出水温度」时，助手每次读当前读数都失败，只能退回去看历史数据。原因是平台核对助手的读取授权时只认网关，不认助手本身。现在按实际出示授权的服务核对，助手能直接读到当前读数，这类问题这次实测约 6 秒答完（之前基线平均约 16 秒）。（#601）

- 松雅轩的 AI 助手面板打不开（显示出错）：助手的一个回答里有一项关键数字的名称是「10/13 用电量（七日最高）」，网关按字节而不是按字数检查长度，把这 14 个字当成超长，导致这个站点的所有对话都读不出来。现在按字数检查，面板已恢复。（#599）

#### 新增

- AI 助手可以一次查几个能耗指标：问「这几天每天的用电、供冷和能效」「上周各电价时段的电费」这类要同时看几个数的问题，助手会在站点能耗、设备分项能耗或电费估算里选好指标一次查出来，按小时、天或月列出，空着的时段就是没有数据。助手只能选平台公开的指标，只能查当前站点，一次最多 31 天。（#573）

#### 变更

- 估算电费改由平台统一计算：能耗分析、总览、报告和 AI 助手看到的是同一个电费，按站点电价逐条计量区间计价（半点换时段也按实际时段算）。没有电价覆盖的电量不再按电价表算进去，而是单独提示「有多少 kWh 落在电价未覆盖的时段，未计入电费」；月度报告存档时也用这个电费。AI 助手回答用电问题时会带上估算电费，并说明以结算为准、不含需量电费。能耗分析和报告不再为算电费额外按小时读一遍用电量。（#575）
- 优化建议和节能核验也改用这个统一电费：优化建议按最近一个完整、全部有电价的日子折算每度电的价格；节能核验的节省电费按每小时站点用电的平均电价计算，没有电价的小时仍记为未计价。原来的「冷站日电度电费」指标不再计算。（#575）

### 2026-10-16

#### 新增

- AI 助手第一版：站点页面的页头多了「AI 助手」按钮，右侧打开 420px 的面板（把页面推开，不遮挡；面板打开时左侧导航自动收成图标，关掉后恢复，表格不用左右拖动）。可以问用电为什么偏高、哪台机组用得多；助手只读平台数据，先给结论再给依据：回答过程中显示它查了哪些数据（默认折叠），回答逐字出现，最后是结论卡（一两句结论、几个关键数字、数据不足的地方、可以点回能耗分析等页面的依据），下面是可以接着问的问题；回答中途可以停止。每个站点继续最近一次对话，「新对话」重新开始。能耗分析的图上点一根柱子（默认用电最高的一根）后点标题栏的「解释」，或在明细表某一行点「解释」，会带着那一天（或那一小时）的日期和用电量来问；进行中的时段不提供解释。助手服务需要在服务器上配置模型密钥后启用，未启用时面板显示「这个环境还没有接入 AI 助手」。（#577）
- 「分项能耗」并入「能耗分析」：选一次统计期间，同一页先看用了多少电、供了多少冷、能效和电费，再看「用电去向」（左边能流图，右边各设备的用电、占比和上期对比）；下面三个页签是分项趋势、分时电费和明细。「本年」也能按月看分项。导出合成一份，明细后面接各设备用电。页面副标题写明各自回答的问题：能耗分析是「用了多少电、用在哪台设备、花了多少钱」，用电负荷是「现在的负荷、最高负荷和接下来的预测」。（#567）
- 「需量分析」和「负荷预测」合成一页「用电负荷」（能源 › 用电负荷）。顶部是当前负荷、今日最高（含负荷率）、本月最高、明天最高（预测，高于本月最高时标黄）；一张曲线把今天和明天放在同一条时间轴上：实测负荷到现在，昨日同时段作对照，之后是预测和区间，电价高峰时段着色，本月最高画成参考线。下面三个页签：逐时预测（今天剩余和明天，各小时的电价、前一天同时段，电价按需量收费时还有距本月最高）、本月每日最高（明天的预测最高也画在旁边）、需量电费（只在电价按需量收费时出现）。逐时预测还给出明天预计用电和其中电价高峰时段的占比。「需量」只在提示里解释，页面上统一说「负荷」。（#566）

#### 修复

- 松雅轩遥测合格率从 10-15 起只有约 33%，一直触发数据质量告警：网关每次采样拆成多条消息，耐久发件箱每存一条新消息就把还没确认的消息重发一遍，同一条温度消息每分钟发了八次。现在网关一次采样只发一条消息，里面带上所有设备，重复为 0。（#564）
- 松雅轩的「负荷预测」一直显示「还没有负荷预测」：站点接入时没有建预测需要的 15 分钟负荷指标、预测模型和每小时的预测任务（只有演示站点有）。现在接入时一并建好，按空调总电表的功率预测；服务器上已补上并补算了一周的 15 分钟负荷。（#565）

### 2026-10-15

#### 新增

- 数字孪生改为铺满全屏的场景加四角浮动面板（参照仓储孪生演示的布局）：左上冷站功率、制冷量、COP 三张指标卡；右上是冷站卡（今日用电和供冷、设备运行台数、需要处理的事项），选中设备后换成该设备的读数和负载，末端视角下是楼层切换；旁边是放大、旋转、复位、视角和图层工具条；左下是冷冻水环路；右下是机组、水泵、冷却塔（或末端设备）的列表，点一行相机飞过去。场景里不再有悬浮文字，选中的设备只显示四角框。场景换成冷调配色：站点周围加了街区、行道树、围栏和斑马线，机房改成带蓝色压顶和角柱的彩钢板墙，透视镜头，远处有雾。系统开了「减少动态效果」时，水流和风机不动、镜头直接切换。（#563）
- 新增「运行 › 数字孪生」页面：站点以等距建筑沙盘呈现。冷站机房里的冷水机组、冷冻泵、冷却泵按 Registry 登记的台数生成，冷却塔、总管和阀门也在其中；冷冻水（蓝）和冷却水（青绿）管路在机组和水泵运行时流动，设备按运行状态亮灯、转动。松雅轩的三层楼宇可以整栋看，也可以剖切到某一层，能看到大厅、包间、空调箱、变风量末端、风机盘管和风管；末端还没接入，统一标「未接入」。可切换全景、冷站、末端三个视角，能关掉外立面、突出管路；点选设备会聚焦并打开详情。3D 代码只在这个页面加载，弱显卡上会自动关闭环境光遮蔽。（#559）
- 松雅轩温度读数按现场对照表挂到设备上：A–D 机组各自显示冷冻水、冷却水的进出水温度（进水两路四台共用），D 机组也有了冷却水温度；新增「房间」温度；室外环境显示室外温度和湿球温度；冷却塔没有探头，不再显示暂定读数。（#558）
- 平台告警可以送达：新增 Alertmanager（唯一能访问外网的监控组件），Prometheus 的告警交给它，配置邮件收件人后即可收到；新增两条告警：把电表读数算成用电量的程序连续失败 15 分钟、15 分钟没有收到任何设备读数（网关、MQTT 或接入服务中断）。（#554）
- 服务器每晚 03:30 自动备份：PostgreSQL 全量备份和 ClickHouse（历史读数与能耗结果），各保留最近 7 份；备份在同一块磁盘上，防误操作和数据损坏，暂不防整机故障。（#552）

#### 修复

- 数字孪生页面改用设计系统的结构：三维视图放进面板，视角、楼层、外立面、突出管路在面板工具栏里，图例在右侧；冷站功率、制冷量、COP 和室内外温度排成面板底部的指标条，并链接到「实时运行」；场景标签用统一的状态徽章和字号；点选未接入的设备也在右侧详情栏打开（地址里带 `inspect`，可以分享）。（#562）
- 松雅轩总览的「今日供冷量」「今日综合能效」和能耗分析的供冷量一直显示「—」：网关算出的累计冷量只绑了逐时供冷量指标，能源拓扑里没有冷量计，算能耗时把它跳过了，冷量一直为空。现在能源拓扑里有「冷站 → 建筑冷负荷」的冷量计，站点接入时一并生成；服务器上已补上，8 月 22 日以来的冷量能耗已重新计算。（#561）
- 松雅轩温度模块的备用通道 11、20 没接探头，每分钟上报一次无效读数，把遥测合格率拉到 95% 以下、会一直触发数据质量告警；现在网关不再上报这两个通道，平台上标为停用，设备页不再显示。站点文件的点位可以写 `status`。（#555）
- 遥测运行库不再无限增长：写往 ClickHouse 的事件送达 1 小时后清理，原始接收记录保留 7 天（足够网关补发时去重）后清理；服务器上这两张表已占 8 GB 且只增不减。（#553）
- 能耗数据（总览今日用能、能耗分析、分项、电价）从 10-14 21:46 起停止更新：松雅轩的冷量计上报了累计冷量，它不属于电力计量，没有计量绑定，算能耗的程序遇到它就整批失败、反复重试，所有电表的用电量都停在那一刻。现在没有计量绑定的累计量会被跳过，只有绑定有歧义时才停下。（#551）

### 2026-10-14

#### 新增

- 登录页改版：左侧是节能主题的动态插画（冷却塔风扇转动、能耗柱状图起伏等，系统开了「减少动态效果」时静止），右侧是登录栏，标题「欢迎回来」，输入框更大，可以「记住用户名」；泉来禾标志换成新版（单色风车，没有底色），口号「让冷站省下每一度电」。（#550）
- 松雅轩设备页更完整：A–D 机组卡片显示功率、运行状态和冷冻水出水温度，详情里有冷冻水、冷却水进出水温度；新增冷却塔（出水温度）、冷冻水泵（总管流量与运行状态）和室外环境（室外温度）。温度通道与设备的对应是暂定的，等现场给出对照表后修改。D 机组的累计电量改读正确的寄存器，从现在起开始计量。（#548）
- 松雅轩显示瞬时制冷量和 COP：现场没有冷量表，按总冷冻水流量与供回水温差计算（供回水通道由两个月的历史数据判定），机组全部停机时制冷量记为 0；历史曲线一并补齐。（#547）
- 松雅轩接入能耗与电价：「空调总电表（四台机组合计）」由四块机组电表相加，能源拓扑以它为总表、四块机组电表为分项；总览、能耗分析、分项、电价页面有了数据，逐时、逐日用电和日电费按计划计算，每月生成报告；电价暂按深圳工商业分时电价（峰 1.03、平 0.62、谷 0.24 元/kWh）。服务器启用报告、节能、预测、诊断、优化服务。（#546）
- 接入第一个真实站点「松雅轩」：现场 Node-RED 网关的 20 路温度、流量计、4 块电表和 ET1010 的实时数据直接进入平台，设备页可以看到在线状态和读数；A–D 机组各自显示电表读数，温度通道按冷冻水、冷却水、冷却塔和室外命名（暂定）；没接探头的温度通道显示为无效读数，不再记为 0 ℃。ThingsBoard 上自 2026-08-22 起的历史数据正在导入平台，ThingsBoard 已停用。（#542）
- 系统管理 →「用户与角色」可以管理用户：管理员新建用户并直接设定登录密码，给用户指派角色（整个组织或某一个站点），撤销角色，重置密码，修改姓名和邮箱，停用或重新启用。停用后对方立即退出登录、不能再登录；不能停用自己，也不能去掉自己的管理员角色。非管理员在这里看到自己的账号和权限。（#537）
- 右上角用户菜单增加「修改密码」，需要输入当前密码；连续输错 5 次锁定 15 分钟，与登录相同。（#537）

#### 变更

- 设备页卡片：没有标准读数的设备（网关接入的电表、温度采集模块、流量计）显示自己的测量值和近 24 小时曲线，不再显示「近 24 小时没有记录」。（#547）
- 设备页里没有绑定资产的流量计、温度采集模块和控制模块按设备类型分组显示，不再归为「其他设备」。（#542）
- 登录页换成和应用其他页面一致的样式（跟随浅色、深色），口令错误时保留已输入的用户名；退出登录、登录失效和登录超时的提示页也用同一样式。（#540）
- 权限改为 4 个内置角色：只读、运维人员、运维主管、管理员，每个角色可以授予整个组织（包括以后新增的站点）或某一个站点；角色说明显示在「用户与角色」页。新增设备和点位后不再需要另外授权。管理员角色只能授予整个组织。（#537）
- 下发和审批指令的权限按风险上限授予：运维人员可下发中风险以下的指令，运维主管可下发和审批高风险以下的指令，管理员不限。（#537）
- 工单可以指派给在该站点能处理工单的任何人，不再需要事先登记指派对象。（#537）
- 密码规则改为：至少 8 位，不要求字母数字组合，不能是常见密码，也不能和用户名相同（原来至少 12 位）。（#537）
- 用户管理不再要求先完成两步验证。（#537）

### 2026-10-13

#### 变更

- 界面改用新设计系统（设计系统 1.0，第一阶段：颜色字号等基础样式和应用框架）。
  - 主色由青绿改为靛蓝；页面底色为浅灰、面板为白色，两层分开；图表、状态、热力色使用经过校验的新色板，深色模式同步调整。
  - 顶栏横跨整个窗口：左边是产品标志和当前站点，右边是搜索（Ctrl K）、通知和用户菜单；用户菜单从侧栏底部移到顶栏右侧。
  - 侧栏按“总览、运行、能源、节能、报告、运维”分组，当前所在的组自动展开；控制指令单独放在分隔线下；集成管理和系统管理固定在侧栏底部（集成管理此前没有入口）。所有页面地址不变。
  - 外观切换改为侧栏底部的“系统 / 浅色 / 深色”，侧栏可收起为图标。
  - 页面标题、按钮、徽章、卡片按新规范调整字号、圆角和阴影。各页面的布局改造分批进行，设计系统见 `DESIGN.md`。
- 总览改为两栏：左边是今日用能、冷站实时（实时 COP 居首，功率和制冷量画在同一刻度上，每台设备一个状态点）和近 7 天逐时用电；右边是「需要处理」（告警、诊断发现和工单合成一个列表，按严重程度排序，用形状区分级别）和「节能」。
- 告警改为分诊台：上方是恢复时长、近 14 天每天新触发的告警和告警最多的设备（点设备即可筛选）；下方左边是按“未确认 / 已确认”分组的列表，右边直接显示选中告警的详情和操作，不再弹出侧栏。键盘 J / K 上下切换，A 确认，C 创建工单；确认说明改在确认时的小窗里填写。
- 工单改为按状态分列的看板（待处理、处理中、受阻；已结束和全部视图另有已完成、已取消列），列头写明未指派和已超期的数量；工单详情增加进度条，取消工单改为次要的红字操作。
- 设备改为设备卡片墙：每台设备显示主读数、两项关键读数、近 24 小时走势和范围、点位是否正常、数据时间和未完成工单；原来的表格保留为「表格」视图（`?layout=table`）。设备详情把每个点位显示为读数卡片。
- 侧栏、详情侧栏从顶栏下方展开，不再盖住顶栏。
- 实时运行改为冷站运行台：冷站总功率、制冷量和 COP 的近 6 / 24 小时走势，今天的告警、指令和断线设备合成「最近事件」，水系统与室内外环境读数带近期走势；去掉了冷站系统图（设备多时显示不下，之后由三维视图代替）。
- 故障诊断改为规则条加发现列表：规则条一行显示当前温差、阈值和设计值、最近一次结果和「立即诊断」；发现按天分组（近 7 天 / 全部），右边是选中发现的结论、诊断前后几小时的温差曲线（平均和最低）、原因和处理建议，以及该设备的工单。
- 控制指令改为设定值表格加指令记录：每台设备一行状态和启停按钮，每个设定值一行当前读数、目标值和进行中的指令；指令记录按「待审批、执行中、待现场确认」筛选，标题处显示近 50 条的成功率。确认下发时显示变化量、设备状态、可设范围和读数时间；指令详情里可直接批准或记录现场确认结果。「待核对」改称「待现场确认」。
- 通知中心改为收件箱：左边按未读 / 已读分组，可多选后一起标记已读；右边显示选中的通知和来源告警现在的状态。标记已读后顶栏的通知提示立即消失。
- 能耗分析：指标页签增加「估算电费」（与空调用电、供冷量、综合能效并列，每个指标下写明对比期的数值），图表改为本期与对比期并排的柱子，进行中的时段画成斜纹；分时电费改成表格，列出每个时段的时间、电价、用电、占比和电费；明细表把每个时段 / 每天的用电按峰、平、谷拆开，最新的排在最前。原来的「期间概况」卡片去掉，用电最高、平均、未计入的数据段和数据时间移到图表标题和页面标题旁。
- 分项能耗：桑基图下面直接是设备用电表（计量方式、用电、占比、对比期和变化），总电表减各设备电表的差值单列一行，叫「未单独计量」；堆叠柱图改为每个分项一张小图，各按自己的刻度，水泵和冷却塔不再被冷机压扁。可导出 CSV。
- 需量分析：当前需量、今日 / 昨日最大需量和负荷率放在需量曲线上方的一条里，当前需量带本 15 分钟的进度，今日最大需量写明距本月最大还差多少；需量曲线可切换为表格（每 15 分钟今日、昨日对比）；本月需量电费单独成块，写明计费方式、计费时段和最大需量出现的时间。
- 负荷预测：逐时预测表增加电价时段、昨日同时段负荷和「距本月最大」（需量计费时段里，本月最大需量减去预测上限，不足 20 kW 标黄，超出标红），分两栏显示一整天；预测更新时间移到页面标题旁。
- 站点对标：三种排名（综合能效、单位面积用电、度日修正用电）和所有列都保留，无法参与当前排名的站点单独列在「未参与此排名」下；每日综合能效改为每个站点一张小图，虚线为整体；站点数和无权限站点数移到页面标题；可导出 CSV。
- 字号与设计稿对齐：正文、表格、菜单、按钮统一为 13 px，说明文字 12 px，面板标题 14 px；表头改为 12 px 灰字、浅灰底、36 px 高。
- 优化建议：建议值、风险、生成方式和「按建议下发」与预计效果放在同一块；按建议下发后，这里直接显示那条控制指令的状态并可跳转。安全与舒适约束改为两条带刻度的范围条；计算依据和「验证与回退」并排，验证条件逐条列出。
- 节能项目：本月节电量、节电率、节省电费和累计节电放在一块，下面是正在统计的措施的累计节电曲线；措施列表改为 5 列（措施、状态、累计节电、节电率、节省电费），可按「统计中 / 计划中 / 已结束」筛选。措施详情按「成效 / 对比基线是否可靠 / 每日节电 / 依据」排列，操作按钮放在底部。用语改为日常说法：「核算」改称「统计」，「测量边界」改称「计量电表」，「分表」改称「电表」，「基线」改称「对比基线」。
- 报告：页面标题改为「报告」，报告抬头直接标出「已存档 / 实时」；「概要」扩为 8 个数字（新增日均用电、用电最多的一天、最大需量、计量覆盖或数据完整性），用电和综合能效分成两张图并标出用电最多的一天；电费构成表增加占比条；月份可用左右箭头切换。
- 集成管理：顶部是「数据链路」，从现场设备、现场网关证书、平台服务、站点目录到实时推送逐段显示状态（原系统管理里的「数据接入」并入这里）；网关列表显示证书状态和到期日（还剩多少天），点开是网关详情侧栏：证书、一次性接入码和停用网关。「吊销」改称「停用」。
- 系统管理：标签页改为「系统概览、用户与角色、站点与组织、资产与点位、自动化规则、操作记录」。概览显示平台状态、授权站点、现场网关、我的权限、平台服务和当前登录；用户与角色说明用户目录未接入，并把当前用户的权限按「查看 / 运维 / 管理」列出；站点与组织列出每个站点的时区、建筑面积、气候区和设备数；「审计日志」改称「操作记录」。「数据接入」和「设计系统」两个标签页去掉。
- 无权限、服务不可用、站点不可见等页面状态和 404 改为统一的状态卡片，并保留在应用框架内；404 页可回到总览、搜索页面或进入常用页面。页面加载时显示和页面同形的骨架。

- 删除切换站点时的「切换站点会丢弃未保存内容」确认框，以及站点范围里登记未保存内容的机制（#526）。#524 之后已经没有页面用它；站点页面的表单都在对话框里，切换站点时照旧先停掉当前站点正在进行的请求和实时推送。系统管理页的未保存确认不受影响。
- 故障诊断规则改由诊断服务（FDD）统一管理。
  - 「冷冻水低温差」的阈值和诊断窗口按租户保存为不可修改的版本；调整参数就发布新版本（本地用 `scripts/publish-fdd-rule-revision.mjs`），已有的诊断结论仍引用当时的版本。
  - 诊断页从诊断服务读取当前规则，显示阈值、窗口和版本号；读取失败时显示「诊断规则暂不可用」。
  - 「立即诊断」和定时诊断都只说明诊断哪台设备，不再由页面或调度参数决定阈值。

#### 新增

- 站点对标：按冷却度日做气候修正（#485）。
  - 每个站点用自己气象站的室外干球温度，按天算冷却度日（日平均温度高于 26 °C 的度数，CDD26）。
  - 站点排名新增「单位度日用电」（kWh/m²·℃·d）一列，并可切换为「按度日修正用电」排名，越低越好。
  - 没有气象站、没有登记建筑面积或期间没有供冷度日的站点不参与该排名；「更多对标维度」里气候修正改为「已接入」，行业基准仍为「未接入」。

- 优化建议每小时自动生成一次（#462）。
  - 每个站点在每小时第 20 分钟按当前运行数据重新计算冷冻水供水温度建议，总览和优化建议页的建议保持最新；手动「生成建议」照常可用。
  - 页面标明建议是「定时生成」还是「手动生成」。定时生成的建议不会自动下发，仍需人工确认。
  - 这一次算不出来（例如站点输入不全）就跳过，下一个小时再算。

- 站点对标：按单位面积用电对比站点（#485）。
  - 站点新增档案：建筑面积和气候区（GB 50176 热工分区），在系统管理 → 注册中心的站点表单里维护，都可不填。
  - 站点排名新增气候区、建筑面积和单位面积用电（期间空调用电 ÷ 建筑面积），可切换为按单位面积用电排名；没有登记建筑面积的站点不参与该排名。
  - 气候修正和行业基准仍显示「未接入」。

- 节能项目：可以按单台设备的分表统计节能量（IPMVP 选项 B，#484）。
  - 登记措施时选择测量边界：冷站总表，或某台有分表的设备。设备边界以设备逐时用电对冷站供冷量拟合基线，检验标准不变。
  - 不同设备上的措施可以同时统计；同一设备，或冷站总表与任何设备，同一时间只能有一项措施在统计。
  - 总览、节能项目页和能源报告的站点节电率改为「节电量 ÷（冷站同期用电 + 节电量）」，多项设备措施可以相加；单项措施仍按自己的调整后基线计算。
  - 每台有分表的设备新增「设备逐时用电量」指标，每小时计算。

#### 修复

- 总览等能耗页面时不时显示「分析引擎暂不可用」（energy-series 返回 503）。原因是不管查哪一段时间，每次能耗查询都要把所有站点的全部用电记录读出来排序，总览同时发出的 7 个查询会撞上 ClickHouse 1.5 GiB 的内存上限。现在查询只读这个站点、这段时间的记录，「数据截至」也只从查询起点往后找；查一天的逐时用电从约 100 MiB 内存降到约 20 MiB，本地连续加载总览 15 次不再出现 503（#531）。查询起点之后没有任何数据的站点，「数据截至」现在显示为本期间没有计量数据，不再显示更早的时间。
- 系统管理的「资产与点位」「自动化规则」打不开，页面报错 "Protected Site scope is not active."（#524）。两个页签把未保存草稿登记到站点范围，而系统管理是平台页面，没有激活的站点。现在草稿由系统管理页自己登记：有未保存内容时，切换页签或离开页面（包括侧栏、站点切换、通知和搜索跳转）会先弹出确认，列出尚未保存的内容；刷新或关闭浏览器由浏览器询问。
- 逐时用电、逐时供冷少算一段：算某个小时（或某一天）用了多少电时，只看这段时间里的电表读数，上一段最后一个读数到这一段第一个读数之间走的字不算在任何一段里。按分钟上报的电表，逐时用电因此偏低约 1.7%，每天少算一分钟，冷站日电度电费、节能统计的逐时数据和优化的基准都跟着偏低。现在从上一段最后一个读数算起，和能耗分析的用电量一致，相邻的时段加起来等于电表的全部走字。已经算好的结果要重算才会更新。
- 回放历史数据后，用电量停在回放开始的时刻，能耗分析、总览、报告等页面不再有新的用电量；同时 ClickHouse 内存长期接近上限，能耗曲线、指标计算和历史写入的查询也会被中途终止。原因是一批新到的读数里同时有几周前的回放数据和实时数据时，把电表读数算成用电量的程序会把「最早那条读数到现在」这几周的全部读数一次排序，超出内存后被终止并不断重试。现在它按读数时间把每批拆成不超过一小时的几段，每段只读这一段前后的读数，内存只取决于这一批数据本身；算出的用电量与以前相同。部署时需要重新执行 `infra/telemetry/clickhouse/init/004-counter-semantics.sql` 里的视图定义，再更新 telemetry-worker（#533）。
- 本地历史回放（`scripts/local-history-replay.mjs`）会把 `--to` 那一刻也写进去，而回放前检查“窗口里已有数据”时不含这一刻。两段相邻的回放因此在交界时刻各写一条电表读数，算用电量的程序遇到同一时刻的两条读数会停下。现在回放只写 `[--from, --to)`，`--to` 那一刻留给下一段回放（#534）。

#### 新增

- 能源报告：已结束月份存档，新增年度报告（#483 第二版）。
  - 每月 1 日 03:00（站点时区）由新服务 `report-service` 为上月生成快照：用电、供冷、综合能效、分时电度电费及峰平谷构成、逐日数据、节能统计与诊断发现。快照生成后不再改变；重算会生成新版本，页面显示最新版本。
  - 已存档的月份按快照显示，标注「已存档 · 生成于 · 覆盖 X / N 天」；需量、告警与工单不在快照里，标注「未存档，按当前记录现算」。本月和还没有存档的月份照旧现算，标注「实时」。
  - 报告页改名「能源报告」，新增「年度」视图：逐月用电、供冷、能效、电度电费与数据来源，以及全年合计；已存档月份用快照，其余现算。
  - 网关接口：`GET /api/v1/sites/{siteId}/reports/monthly/{month}`（某月最新快照，未存档时 404）与 `GET /api/v1/sites/{siteId}/reports/monthly?year=`。

#### 变更

- 冷站日电费按分时电价逐时计价，并改名「冷站日电度电费」。
  - 以前按时间加权的日均电价乘以全天用电，现在每小时用电乘以该小时生效的电度单价再求和：高峰用电多的日子电费更高，优化建议折算的节省金额也随之按当天实际的峰谷构成计算。
  - 当天任何一小时缺少逐时用电、数据质量不合格或没有电价覆盖，就不出当天电费，不会给出偏低的数。
  - 只含电度电费，不含需量电费；电价调整后已发布的日电费不自动重算。

### 2026-10-12

#### 修复

- 用电量少算：把电表读数算成用电量的程序分批处理时会漏掉一部分时段，实时数据约少 8%，历史回放约少 27%，能耗分析、总览、报告等页面的用电量因此偏低。现已修复，每一段都会算进去。已有数据要补算：停掉 telemetry-worker，清空它记录处理进度的表（`analytics.energy_projection_checkpoints`），再启动，漏掉的时段会补上。

#### 新增

- 分项能耗改读设备分表（#481 第二版）。
  - 冷水机组、冷冻水泵、冷却水泵、冷却塔各有自己的累计电量表（分表），挂在空调总表之下；分项能耗页读这些实测电量，不再按设备功率估算。
  - 没有分表的设备标「未计量」，其用电计入「未分项」。未分项 = 空调总表 − 各分表之和，如实显示；为负说明分表合计超过总表。
  - 站点总用电只统计总表，加分表后能耗分析、报告等页面的总量不会重复计算。
  - 能耗查询 `POST /api/v1/analytics/energy-series` 新增 `breakdown: "device"`，同时返回各设备分表的电量序列。
  - 本地环境需 `npm run local:reset` 后重建；测点编号因新增分表测点而变化。
- 系统管理新增「设计系统」页：按当前主题读出每种颜色并计算 WCAG 对比度，列出字号层级、组件状态与圆角间距。
- 节能项目与节能验证（#484 第一版，IPMVP 选项 C）。
  - 能源组新增「节能项目」页：登记节能措施，确认实施后系统用实施前 14 天的逐时用电与供冷量拟合基线并冻结，检验按 ASHRAE Guideline 14（CV(RMSE) ≤ 30%、|NMBE| ≤ 10%、至少 72 个完整小时且覆盖 ≥ 90%）。
  - 基线不可信时不出节能数字，页面说明原因；发布第一天的统计结果前可换时段重新拟合。
  - 实施后 24 小时为稳定期；之后每天 02:00 统计前一天：节电量 = 调整后基线 − 实际用电，节省电费按逐时分时电度电价，不含需量电费。逐日结果发布后不再改动。
  - 同一站点同一时间只统计一项措施。
  - 总览「节能机会」改为「节能成效」：本月已统计的节电量与电费，旁附优化建议的预计值；月度报告新增「节能成效」一节。
  - 登记与推进措施需要新权限「节能项目管理」（`savings.manage`），查看只需站点读权限；租户管理员角色自动获得。
- 站点对标页面（#485 第一版）。
  - 各授权站点按冷站综合能效排名，对比整体能效（按电量加权）、空调用电、日均用电与供冷量，统计期间可选近 7 天或本月。
  - 附各站点每日综合能效曲线。
  - 没有能耗分析权限的站点会列出，但不参与对比。
  - 单位面积能耗、气候修正与行业基准暂未接入，原因是站点档案和基准数据源还没有。
- 优化建议的节省金额改为「按日均电价折算」，不再写单一制电价（#509）。
- 月度报告页面（#483 第一版）。
  - 可选择最近 12 个月；内容包括用电、供冷量与综合能效（与上月对比）、每日用能与能效、分时电费与需量电费构成，以及告警、工单、诊断发现的汇总。
  - 「导出 PDF」按 A4 输出报告本身，不含应用外壳。
  - 报告按需从各数据源现算，不另行保存。
- 分项能耗页面（#481 第一版）。
  - 冷水机组、冷冻水泵、冷却水泵、冷却塔各自的用电量与占比，统计期间可选今日、近 7 天或本月。
  - 能流桑基图按「空调总用电 → 系统 → 设备」展示。
  - 附设备用电排行，以及逐时 / 逐日的分项用电。
  - 设备用电由各设备功率逐时累计得出，是估算值；与总电表的差额单列为「未分项」。
- 分时电价与需量电费（#504）。
  - 站点当前生效的电价改由设备登记服务（注册中心）提供，接口为 `GET /api/v1/sites/{siteId}/tariff`。
  - 本地种子改为两部制分时电价：峰 1.20、平 0.72、谷 0.32 元/kWh，峰段需量 38 元/kW·月。
  - 能耗分析新增「分时电费」：峰平谷各段的用电与电费、均价，以及今日电价时段。
  - 需量分析新增「本月需量电费（估算）」，曲线上标出计收需量的时段。
  - 总览新增「今日电费（估算）」页签。
  - 以上金额均为按电价估算，正式电费以结算为准。
- 总览「节能机会」显示最新优化建议预计每天节省的电量与电费（#502）。
- 故障诊断按计划运行：冷冻水低温差每 15 分钟诊断一次，同一问题 24 小时内只记一次，之后的诊断标为「持续中」（#501）。
- 优化建议页面：冷冻水供水温度建议，附安全与舒适约束、计算依据、确认下发和回退条件（#500）。

#### 改进

- 节能项目详情改为直接展示每日节电量（负值标红），节电只占一两个百分点时也看得清；总览「节能成效」的统计情况改为一句话说明（#484）。
- 「信息」级告警的标记色加深，对白底的对比度由 2.56:1 提到 4.76:1，达到图形 3:1 的要求。
- 节能措施可以关联一条本站已执行的控制指令作为依据，详情里按控制指令页的说法显示并可跳转；另可填写依据说明（#484）。
- 没有配置电价的站点也能统计节电量：按站点时区划分统计日，电费处注明有多少小时没有电价、未计入（#484）。
- 本地管理员在两个本地站点都有能耗分析等权限，站点对标可以比较两个站点（#511）。
- 全站统一视觉体系（#505）。
  - 站点切换移到侧栏顶部；顶栏吸顶并半透明。
  - 搜索可以直接找到设备。
  - 页头格式统一，标题旁增加状态标签。
  - 状态与优先级标签统一为浅色底加静态圆点。
  - 表格直接放在卡片里，没有数据时的提示统一。
  - 设备、工单、系统管理、集成管理、通知、故障诊断各页重整了结构。
- 实时运行与详情侧栏打磨（#507）。
  - 「水侧与室内外工况」三栏统一为「主读数 + 明细」，分别是冷冻水看温差、冷却水看逼近温度、末端看室内温度。
  - 设备、告警、工单详情侧栏加宽到 480px，去掉内嵌方框，趋势读数改为下拉选择。
- 每天的用电量改为一段一段加起来，电表读数清零或走满回到 0 时，日用电量不再偏低（#497、#499）。

#### 修复

- 模拟器在时钟回拨后不再输出空快照，而是跳过该周期（#498）。

### 2026-10-11

#### 新增

- 需量分析：按 15 分钟平均功率计算需量，显示今日、昨日和本月的最大需量，以及负荷率（#494）。
- 故障诊断页面与诊断发现（测量值与阈值），总览中可看到诊断发现（#493、#495）。
- 负荷预测页面，以及每小时一次的预测计划（#490、#491）。
- 控制指令：设定值与启停指令，中高风险指令需审批，结果不明时由人工核对（#488、#489）。
- 冷站模拟器新增办公区室内温度测点（#492）。

#### 改进

- 导航重新分为「监测、能源、运维」三组（#487）。
- 数字条、表格和应用框架的细节打磨（#474、#477、#496）。

### 2026-10-10

#### 新增

- 实时运行页面：冷站两环路系统图和设备运行快照（#464）。
- 告警页面：按严重度排序，可确认、指派，并给出趋势统计（#466）。
- 总览改版，接入真实的能耗、运行与待办数据（#459）。
- 能耗分析接入真实的电量与冷量数据（#455）。
