# ThingsBoard 对标结论两个月后的对照（2026-10）

## 1. 基线、对照对象和方法

| 项 | 值 |
| --- | --- |
| 日期 | 2026-10-22 |
| 对照的本仓库代码 | `946b5bcb9f11f7b036f8968056d3f62a0bdfd3fe`（origin/main，2026-10-22 11:27，#685 合并） |
| 当时的基线 | ThingsBoard CE `v4.3.1.1`，commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`（Apache-2.0） |
| ThingsBoard 新版本 | `v4.4`，commit `6d46786579c8b29caf5102f95ddb133674bed68b`（BUSL-1.1） |
| OpenEMS | 基线 `2026.7.0` / `2e2792d`（ADR 0012）；参考 `2026.9.0` / `14f0ded`（`docs/architecture/references/openems-2026.9.0.md`） |
| MyEMS | 基线 `v6.7.0` / `be6e6ce8`（旧 `myems-source-review.md`）；参考 `v6.9.0` / `b360f5b`（`docs/architecture/references/myems-v6.9.0.md`） |

**本仓库 commit 的说明。** 开始时工作区在 `feat/redesign-platform-pages`（`7d860338`，比 origin/main 落后 499 个提交，缺 2026-10-14 之后的真实站点、用户管理、微信小程序等）。审查进行中，工作区被别的会话切到了 `feat/promo-video`，它和 origin/main 同为 `946b5bcb`。所以本文对照的是 `946b5bcb`，下文所有本仓库的 `路径:行号` 都指这个 commit。

**ThingsBoard 源码重新核对过。** 在 scratchpad 里浅克隆了 `v4.4`，`git rev-parse HEAD` 为 `6d46786579c8b29caf5102f95ddb133674bed68b`，和要求一致；只读，没有构建或运行。下文 `thingsboard@v4.4 路径:行号` 都出自这份克隆；为了表格不太宽，`schema-entities.sql`、`schema-functions.sql` 指 `dao/src/main/resources/sql/` 下的文件，`common/data/...` 指 `common/data/src/main/java/org/thingsboard/server/common/data/`，`controller/` 指 `application/src/main/java/org/thingsboard/server/controller/`，其余 `...` 是省略的 Java 包路径。v4.3.1.1 没有重新克隆，它的数字沿用旧清单。OpenEMS、MyEMS 没有重新克隆，沿用上表的固定版本和引用。

**基线文档已从 main 删除。** 2026-10-19 的 `8e67541c`（#605）删掉了全部 ThingsBoard/OpenEMS 裁决文档、反向审查、路线图和 `contracts/architecture/thingsboard-*.v1.json`，只留下 `docs/architecture/references/` 下三份参考和 ADR。下文引用旧文档时用 `7d860338` 里的版本，简称如下（都在 `docs/architecture/` 下）：

| 简称 | 文件 |
| --- | --- |
| 清单 | `thingsboard-ce-capability-inventory.md` |
| 路线图 | `thingsboard-implementation-roadmap.md`（S00–S26） |
| 反审 | `thingsboard-hvac-module-reverse-audit.md` |
| D01–D10 裁决 | `thingsboard-security-tenancy-`、`-entity-relation-device-asset-profile-`、`-connectivity-rpc-ota-`、`-telemetry-storage-calculated-fields-`、`-rule-engine-queue-scheduling-debug-`、`-alarm-notification-disposition-`、`-dashboard-widget-mobile-`、`-edge-sync-offline-remote-configuration-`、`-ai-analytics-integrations-`、`-operations-platform-deployment-ha-observability-upgrade-adjudication.md` |
| 三方对标 | `thingsboard-openems-benchmark.md` |
| v4.4 参考 | `references/thingsboard-v4.4.md`（仍在 main） |

**方法。**

1. 用清单的十个领域（D01–D10）做骨架，把路线图的 S00–S26 和各裁决里的 ADOPT/ADAPT/REJECT/DEFER 落到具体能力上，相近的合并成一行，共 91 行。
2. 每一行到 `946b5bcb` 的代码里找：表结构（`infra/`、各模块 `migrations/`）、对外接口（`contracts/http/platform-gateway.openapi.yaml`）、进程接线（`cmd/`、`deploy/platform/phase1/compose.yaml`）、页面（`apps/hvac-web/src/routes/`）。只看到文档或 CHANGELOG 说做了、代码里找不到的，不算已做。代码在、但没有进程运行它的，算"还差"或"部分"。
3. "明确不做"要有裁决、ADR 或删除提交写明理由，并核对代码里没有被悄悄做出来。
4. ThingsBoard 侧只核对影响结论的地方：v4.4 新增的表、控制器和枚举。
5. GitHub Issue 用 WSL 里的 `gh issue list --search "<关键词> in:title"` 查。

## 2. 结论

91 项能力里：**已做 46 项**（其中 7 项只做了一部分）、**明确不做 29 项**、**还差 16 项**。

路线图 S00–S25 在 2026-08-19 到 08-20 两天里都以 PR 关掉了，之后两个月又改掉或删掉了其中一部分：Edge 车队和 OTA（ADR 0015）、集成实例和传输档案（ADR 0015）、站点汇总（ADR 0019）、按业务分开的授权表（ADR 0022）、API Key 和租户切换（`8e67541c` 之后的两次删除）。新做的东西大多不在基线里：第一个真实站点松雅轩、微信小程序、AI 助手、节能验证、月报。总的方向是照着裁决走的，但有几处做法和裁决不一致，见第 5 节。

对第一阶段冷站节能产品最要紧的差距：

1. **平台的指令到不了真实站点。** 松雅轩的网关是第三方 Node-RED，只上报不收指令；ET1010 控制模块的下发还没做（#542）。优化建议在真实站点只能看，执行不了。
2. **真实站点的数据只有同盘备份。** 夜间备份放在同一块盘的运行目录里，没有拷到服务器外，也没有在服务器上做过恢复演练；这块盘 2026-10-19 满过一次。ThingsBoard 已停，导入的历史只剩这一份。
3. **告警出不了平台。** Alertmanager 的接收方是 `none`；通知没有配接收人、策略和模板；统一外发投递的 worker 没有任何进程在跑。现在只有微信订阅提醒能把告警送到人。
4. **当前值读取依赖 Redis。** 对外的快照读取只读 Redis，Redis 缓存缺失时回 404，Redis 不可用时回 503，和 ADR 0013 §5"Redis 只影响延迟，不影响当前值是否存在"不一致。
5. **告警规则只能用文件加命令行发布，改名或删掉的旧规则撤不掉。** 真实站点的规则会越积越多。

## 3. 按领域的对照表

状态："已做"后面括号里写"照做 / 改做法 / 部分"。

### D01 租户、身份、权限与审计

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 租户隔离 | 已做（改做法） | `infra/registry/postgres/init/001a-tenant-foundation.sql:58`（行级安全） | 反审 §9；`thingsboard@v4.4 dao/src/main/resources/sql/schema-entities.sql:521`（relation 无 tenant_id） | TB 靠 DAO 按 tenant_id 过滤；这里用 PostgreSQL 行级安全，更严 |
| 授权模型 | 已做（改做法） | `infra/registry/postgres/init/026-iam-role-assignments.sql:1`；ADR 0022 | D01 裁决 :280（拒绝固定三种 Authority）；`thingsboard@v4.4 common/data/.../security/Authority.java:7-9` | 四个内置角色指派到租户或站点；按业务分开的六张授权表已删。比 S04 设想的粒度粗：不能再按设备或读数项授权 |
| 用户管理（建号、停用、改密、改资料） | 已做 | `contracts/http/platform-gateway.openapi.yaml:663-952`（`/api/v1/iam/members*`）；`services/identity-service/README.md` | 路线图 §S04 | 2026-10-14 起在「系统管理 → 用户与角色」 |
| 浏览器会话放在网关（BFF），不给前端 JWT | 已做（照做） | `cmd/energy-api/internal/gateway/identity.go:27`；ADR 0024 | D01 裁决 :284 | 小程序用 `Authorization` 头，和网页 cookie 互不通用 |
| 两步验证和高风险操作再验证 | 已做（部分） | `services/identity-service/internal/identity/mfa.go:105`；`cmd/energy-api/internal/gateway/command.go:474,504` | 路线图 §S03 | 有 TOTP，指令审批要求再验证；开通 TOTP 只能在服务器上用 `identity-admin totp-start` |
| 用成熟的 OIDC 身份服务代替自建 | 还差 | `services/identity-service/README.md:19` | 路线图 §S03；ADR 0013 §11 | 自建身份服务在继续扩展（微信登录、用户管理），没有新的 ADR 改掉 S03 的要求 |
| 第三方登录、Passkey、SAML、自助找回密码 | 明确不做 | `services/identity-service/README.md:19`（"故意不做"） | D01 裁决 :289 | 代码里没有 OAuth2 客户端或 SAML 的表 |
| API Key 和服务账号 | 明确不做 | `infra/registry/postgres/init/030-drop-api-credentials.sql` | 路线图 §S04 要求做 | 2026-10-21 删除：网页和小程序从没用过。后台服务改用工作负载身份（ADR 0017） |
| 一个人切换多个租户 | 明确不做 | 删除提交 `4bfbac63` 的说明 | 路线图 §S04 要求做 | 接口一直被路由登记挡成 404，没人用过，2026-10-21 删除 |
| Customer 层级、委派、公开 Customer | 明确不做 | 代码里没有 customer 表 | D01 裁决 :279（DEFER）；`thingsboard@v4.4 schema-entities.sql:190`（`parent_customer_id`） | v4.4 已有多级 Customer，见第 4 节 |
| 审计记录和搜索 | 已做（照做） | `modules/audit/`；`platform-gateway.openapi.yaml`（`POST /api/v1/audit/search`）；`infra/identity/postgres/init/004-identity-auth-assurance.sql:62` | D01 裁决 :293 | 登录、锁定等安全事件和业务改动同事务写审计意图 |
| 限流 | 已做（部分） | `deploy/platform/phase1/limit-policy.v1.json`；`cmd/energy-api/internal/gateway/server.go`（`libs/limitpolicy`） | D10 裁决 :361；`thingsboard@v4.4 .../DefaultTenantProfileConfiguration.java:49` | 网关和遥测按策略限流；单机计数在进程内 |
| 租户配额和用量视图 | 还差 | 无 | D01 裁决 :294；`DefaultTenantProfileConfiguration.java:30,117` | 没有设备数、数据点、存储天数这类租户上限，也没有用量页 |
| 管理员冒充登录 | 明确不做 | 无 | D01 裁决 :291 | 没有 |
| 租户退役 | 已做 | `infra/registry/postgres/init/013-s23-maintenance-retirement.sql:99-138`；`modules/maintenance/pkg/maintenance/` | 路线图 §S23 | 只核对了代码，没有跑过 |
| 系统和租户级邮件、短信、UI 设置 | 明确不做 | 无 | D01 裁决 :296（DEFER） | 没有 |

### D02 实体、关系、设备与资产档案

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 层级模型 Tenant→Site→Space→Asset→Device→Point | 已做（改做法） | `infra/registry/postgres/init/001-s1-registry-baseline.sql`、`007-spatial-sensor-point-model.sql`；ADR 0013 §1 | `thingsboard@v4.4 schema-entities.sql:318,387`（asset、device） | 用有类型的表，不用 TB 的通用 Device/Asset |
| 自由关系图当业务拓扑 | 明确不做 | `infra/registry/postgres/init/001b-device-binding-invariants.sql:16`（类型化绑定） | ADR 0013 禁止路径 2；`thingsboard@v4.4 schema-entities.sql:521-531` | 代码里没有通用 relation 表 |
| 登记写入：新建、修改、换绑、退役，带期望版本 | 已做（照做） | `infra/registry/postgres/init/010-s05-registry-writer.sql:19-71`；`platform-gateway.openapi.yaml`（`POST /api/v1/sites/{siteId}/devices` 等） | 路线图 §S05 | 页面在「系统管理 → 资产与点位」。真实站点登记走脚本直写表，见第 5 节 |
| 模板版本（相当于 Device/Asset Profile，发布后不能改） | 已做（部分） | `POST /api/v1/templates/revisions`；`modules/registry/cmd/atv630-template-release/` | 路线图 §S05；`thingsboard@v4.4 schema-entities.sql:335`（device_profile） | 只有 ATV630 一个发布过的模板；真实站点的设备没有用模板 |
| 导入（先试算再提交）和导出 | 已做（照做） | `POST /api/v1/sites/{siteId}/imports/dry-run`、`/imports/commit`；`apps/hvac-web/src/features/system/registry-admin/RegistryImportExportWorkbench.tsx:103,174` | D02 裁决 :378 | 导出不含凭据 |
| Entity View（按读数项和时间窗口裁剪） | 明确不做 | 无；ADR 0022 | D02 裁决 :255,:375（ADAPT 为读取时裁剪） | ADR 0022 取消了按读数项授权，这个需求随之没有了 |
| 网关上报时自动建设备或 Profile | 明确不做 | `modules/connectivity/pkg/adapter/processor.go:60-70`（未登记的 source key 进隔离） | D03 裁决 :282；`thingsboard@v4.4 .../DefaultTransportApiService.java:443` | TB 拼错的 deviceType 会静默建新 Profile；这里拒收并留证据 |
| 实体版本控制和自动提交到 Git | 明确不做 | 无 | D10 裁决 :362（拒绝运行时 Git） | 用不可变版本代替 |

### D03 接入协议、网关、RPC 与 OTA

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| MQTT 接入，网关身份就是它的证书，主题只带网关 ID | 已做（改做法） | ADR 0015；`infra/telemetry/mqtt/acl:4-5`；`contracts/mqtt/gateway-uplink.v2.schema.json` | `thingsboard@v4.4 .../MqttTopics.java:52`（`v1/devices/me/*`） | 和 TB"主题不带设备 ID"同一个思路；用 X.509 双向 TLS，不用 Access Token |
| 一条网关连接带多台设备 | 已做（改做法） | `infra/registry/postgres/init/017-registry-read-port.sql:72-86` | `thingsboard@v4.4 .../DefaultTransportApiService.java:139,141,334` | 子设备身份是"网关 + source key"，不是租户内设备名 |
| 预先登记 + 一次性码换证书 + 续期 + 吊销 | 已做 | `infra/connectivity/postgres/init/004-gateway-enrollment.sql:7,21`；`POST /api/v1/gateways/{gatewayId}/enrollment-code`、`/revoke` | 路线图 §S09 | 松雅轩网关已用这套换到证书（`docs/specs/site-gateway-onboarding.md`） |
| 设备自注册、Claim | 明确不做 | 无 | D03 裁决 :287 | 没有 |
| CoAP、LwM2M、SNMP、Sparkplug、BACnet、OPC UA | 明确不做 | 代码里搜不到这些协议的实现 | D03 裁决 :270,:296-299（DEFER） | 现场 Modbus 由网关处理 |
| 集成实例、传输档案（S09 的做法） | 明确不做 | ADR 0015"Removed with no replacement" | 路线图 §S09 | 一个 Connectivity 进程服务所有网关 |
| RPC 改成受控指令（意图、审批、租约、围栏、结果未知、独立回读） | 已做（改做法） | `modules/command/migrations/005_s11_edge_execution_evidence.sql`；`POST /api/v1/commands/{commandId}/reconcile`；ADR 0018 | `thingsboard@v4.4 common/data/.../rpc/RpcStatus.java:16-23` | 比 TB 严：结果未知时停住这台设备，由人确认后放开 |
| 指令下发到真实站点网关 | 还差 | 无（松雅轩网关不订阅 `down/command`） | 路线图 §S11 | 见 §6 |
| 任意 method 的轻量 RPC | 明确不做 | 无 | D03 裁决 :290,:292 | 只能按能力档案下发 |
| OTA 包和升级 | 明确不做 | 删除提交 `91077fda`；ADR 0015 | 路线图 §S12 | 没有授权入口、broker 也没放行，整体删除，等边缘运行时重新设计 |
| 在线状态和数据新鲜度 | 已做（照做） | ADR 0002；告警"网关上报中断"（`deploy/sites/songyaxuan.alarm-rules.json`） | `thingsboard@v4.4 .../DefaultDeviceStateService.java` | |
| 真实网关断网时先存、恢复后补发 | 还差 | `edge/node-red/songyaxuan-platform-envelope.js`（只做信封） | D08 裁决；#542 | 平台能接收按序号补发的消息；松雅轩 Node-RED 的耐久发件箱还没改发平台 |

### D04 遥测、属性、存储与计算字段

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 摄取：去重、乱序、质量、隔离 | 已做（照做） | `modules/telemetry/pkg/telemetry/ingest_store.go:499,515` | `thingsboard@v4.4 .../SqlLatestInsertTsRepository.java:58-59` | 当前值只接受更新的时间，和 TB 的 `ts_kv_latest.ts <= ?` 一样 |
| 当前值读取不依赖 Redis | 还差 | `modules/telemetry/pkg/telemetry/server.go:211-219,290-296`；`cmd/telemetry-worker/main.go:79,256` | 路线图 §S06；D04 裁决 :371；ADR 0013 §5 | PostgreSQL 是真值，启动时重建 Redis；但快照读取只读 Redis |
| 历史：带类型的值和质量 | 已做（照做） | `infra/telemetry/clickhouse/init/001-telemetry-history.sql:3,20-30` | 路线图 §S07 | ClickHouse，数字、字符串、布尔、JSON 分列 |
| 分钟、15 分钟、小时汇总 | 已做（照做） | `infra/telemetry/clickhouse/init/003-telemetry-rollups.sql:4,66`；`001-telemetry-history.sql:55` | v4.4 参考"不应照搬"第 8 条 | TB 普通 PG 下按区间逐条查，这里写入时汇总 |
| 日、月汇总表 | 还差 | 无（日、月由原始能耗记录现算） | 同上 | 见 §6 |
| 计算字段改成 Metric（DELTA、AVG、INTEGRAL、RATIO、EXPRESSION，单调版本） | 已做（改做法） | `modules/metric/pkg/metric/engine.go:310-363`；`infra/registry/postgres/init/011-s08-metric-lifecycle.sql:14,48` | 路线图 §S08；D05 裁决 :448 | |
| 实时推送 | 已做（改做法） | `infra/telemetry/postgres/init/005-s2-realtime-backend.sql`；compose 的 `centrifugo` | 清单 D04 | 用 Centrifugo，不自己写 WebSocket |
| 运行时表清理 | 已做 | `modules/telemetry/pkg/telemetry/retention.go:35`；`cmd/telemetry-worker/main.go:584` | 路线图 §S23 | 每小时删已送达的历史事件和 7 天前的原始读数（#553） |
| 原始历史和指标结果的保留策略 | 还差 | `cmd/metric-worker/main.go:338`（执行器在）；ClickHouse 建表没有 TTL | 路线图 §S08、§S23；D04 裁决 :369 | 执行器只认 Metric 结果，种子里没有排这类任务 |
| 通用属性（client/shared/server） | 明确不做 | 无 | D04 裁决 :365 | 配置、期望值、运行状态各归其主，不建万能 KV |
| 可换存储后端（Cassandra、Timescale、EDQS） | 明确不做 | 只有 PostgreSQL + ClickHouse + Redis | D04 裁决 :368 | |
| 历史回放导入（含 ThingsBoard 导出的 CSV） | 已做 | `infra/telemetry/postgres/init/007-historical-replay.sql`；`scripts/thingsboard-history-import.mjs:1` | 路线图 §14.1（允许一次性迁移） | 松雅轩历史已导入；按设备核对条数还没记录结果 |

### D05 规则引擎、队列、调度与调试

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 通用规则的线上执行 | 还差 | `services/rule-runtime-service/pkg/rulemanagement/manager.go:272`（只在模拟时以 `ModeReplay` 运行） | 路线图 §S20 | 没有进程消费事件执行规则 |
| 规则管理界面（目录、校验、发布、指派、模拟） | 已做（部分） | `cmd/energy-api/rule_management.go:14`；`apps/hvac-web/src/features/system/SystemManagement.tsx:276` | 路线图 §S21 | 发布的规则不会跑，见第 5 节 |
| 告警、FDD 自带规则版本 | 已做（照做） | `modules/alarm/migrations/006_s14_alarm_evaluator.sql:4,19`；`infra/registry/postgres/init/025-fdd-rule-revisions.sql:8` | ADR 0013 §6 | |
| 任意 JS/TBEL 脚本 | 明确不做 | 各 `go.mod` 里没有 goja、otto、v8go、expr | D05 裁决 :361,:406 | |
| 队列用 PostgreSQL 发件箱加租约，不用 Kafka | 已做（改做法） | `infra/durability/postgres/init/004-audit-postgres-outbox.sql`；compose 没有 Kafka | D10 裁决；v4.4 参考"不应照搬"第 1、2 条 | Kafka 代码还留在两个非默认入口，见第 5 节 |
| 调度（cron、时区、补跑、租约） | 已做（照做） | `modules/scheduler/`；`libs/schedulerjob/`；`infra/registry/postgres/init/009l-scheduler-job-v1.sql:12` | 反审 §4.1 | FDD、M&V、报告、预测、优化都用它 |
| 调试和判断记录 | 已做（部分） | `modules/alarm/migrations/006_s14_alarm_evaluator.sql:72`（判断记录，保留 30 天） | D05 裁决 | 规则执行证据只有模拟产生 |
| Kafka、MQTT、REST、AWS、GCP、Azure 外发节点 | 明确不做 | 无 | D05 裁决 :386,:420,:438（DEFER） | |

### D06 告警、通知与处置状态

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 告警事实分开：发生/恢复、确认、抑制、指派、时间线；一个指纹只有一条正在发生的告警 | 已做（照做） | `modules/alarm/migrations/005_s13_alarm_orthogonal.sql:57-60,173` | 路线图 §S13；`thingsboard@v4.4 schema-functions.sql:7`（`create_or_update_active_alarm`） | |
| 有状态判断：持续、迟滞、无数据、过期、AND/OR、时段、不变；每分钟到期复查 | 已做（照做，有扩展） | `modules/alarm/pkg/alarmservice/evaluator.go:56,65-74`；`due_evaluations.go:40,70` | 路线图 §S14 | "一动不动"（UNCHANGED）和站点时钟是基线里没有的 |
| 告警规则的编写和撤销入口 | 还差 | `modules/alarm/cmd/alarm-rules/main.go:43,71`（只读文件发布） | 路线图 §S14 | 见 §6 |
| 告警抑制、评论的接口 | 还差 | `contracts/http/s4-alarm-public.openapi.json` 只有列表、详情、确认、指派 | 路线图 §S13；`thingsboard@v4.4 schema-entities.sql:68`（alarm_comment） | 表里有抑制字段，没有接口 |
| 站内通知：受众、模板、策略版本、升级、收件箱 | 已做（照做） | `services/notification-service/migrations/001_s16_notification.sql:7-166`；`cmd/energy-api/embedded_notification.go` | 路线图 §S16 | 服务器上没配接收人、策略和模板，收件箱是空的 |
| 统一外发投递（意图→尝试→结果→死信） | 还差 | `services/outbound-delivery-service/pkg/outbounddelivery/worker.go:28`（没有进程调用 `NewWorker`） | 路线图 §S15；ADR 0013 §8 | 表和代码都在，邮件、REST 发不出去 |
| 短信、Slack、Teams、Firebase 推送 | 明确不做 | 无 | D06 裁决 :413-416（DEFER）；`thingsboard@v4.4 .../NotificationDeliveryMethod.java:12-16` | 微信订阅消息替代了手机推送，见第 5 节 |

### D07 Dashboard、Widget、移动端

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 租户自定义 Dashboard、Widget 库 | 明确不做 | `apps/hvac-web/src/routes/`（固定产品页面） | D07 裁决 :534,:557 | |
| 站点汇总投影（S17 的 SiteDashboardSummary） | 明确不做 | ADR 0019 | 路线图 §S17 | 总览改为各数据负责方直读，各自标数据时间 |
| 控制类 Widget、自定义 JS/HTML/CSS、外部资源、公开 Dashboard | 明确不做 | 无 | D07 裁决 :539,:550,:555,:558,:565 | 控制只走「控制指令」页 |
| 只读流程图（SCADA 类） | 已做 | `apps/hvac-web/src/routes/_app._site.operations.digital-twin.tsx`；`docs/specs/digital-twin.md` | D07 裁决 :543（只读拓扑 ADAPT） | 数字孪生，只看不控 |
| 移动端 | 已做（超出基线） | `apps/hvac-miniprogram/`；ADR 0024 | D07 裁决 :567-570（DEFER） | 微信小程序：看、问助手、确认告警；没有调用指令接口 |
| 白标、自定义菜单、多语言 | 明确不做 | 无 | D07 裁决 :571 | v4.4 已并入 CE，见第 4 节 |

### D08 Edge、同步、离线与远程配置

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 自研边缘控制运行时上线（周期、过程映像、仲裁、本地租约、Modbus 桥、本地时序） | 还差 | `libs/edgecontrol/host.go:13`；只被 `tools/eg8200-simulator/cmd/eg8200-atv630-edge/main.go:86,101` 用 | 路线图 §S10；ADR 0012 | 库在，没有生产发行物；真实站点用第三方 Node-RED |
| 车队、同步、期望配置、签名 OTA | 明确不做 | 删除提交 `91077fda`；ADR 0015 | 路线图 §S12；`thingsboard@v4.4 common/edge-api/src/main/proto/edge.proto:17` | 等边缘运行时重新设计，暂无 Issue |
| 边缘本地规则、通知、界面 | 明确不做 | 无 | D08 裁决 | |

### D09 AI、分析、集成

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 模型登记、部署版本、调用记录 | 已做（部分） | `infra/registry/postgres/init/013-s22-intelligence-products.sql:7,45,120`；`services/optimization-service/internal/optimization/preparation_postgres.go:146` | 路线图 §S22；`thingsboard@v4.4 schema-entities.sql:1371`（ai_model） | 优化服务读部署绑定；AI 助手用环境变量直连 DeepSeek（`compose.yaml:764`），不写调用记录表 |
| AI 助手：只读工具、证据卡片、语义查询 | 已做（超出基线） | `services/operations-agent-service/`；ADR 0014、0023 | D09 裁决；`thingsboard@v4.4 .../controller/AiChatController.java:41` | 不能操作设备 |
| 负荷预测、优化建议、故障诊断、节能验证、月报 | 已做（超出基线） | compose 的 `forecast-service`、`optimization-service`、`fdd-service`、`mv-service`、`report-service`；ADR 0020、0021 | 三方对标 P3（MyEMS 内容） | TB 没有对应物 |
| 优化建议只经指令审批下发 | 已做（照做） | `apps/hvac-web/src/features/optimization/components/OptimizationWorkspace.tsx:17,122` | ADR 0013 禁止路径 11 | |
| 平台集成和数据转换器、外部 BI | 明确不做 | 无 | D09 裁决 :386,:401（DEFER） | v4.4 已并入 CE，见第 4 节 |
| Trendz、MCP、AI 计算字段 | 明确不做 | 无；ADR 0023 也拒绝 Cube 的 MCP | D09 裁决 :385,:386,:402 | |
| MyEMS 的碳排、能源计划/目标、工作日历 | 还差 | `infra/registry/postgres/init/009a-energy-topology-metering-v2.sql:284`（虚拟表计有表、没用） | 三方对标 ADOPT / From MyEMS | 能耗拓扑、表计、分表、电价、结算、基线已经有 |

### D10 部署、可观测、备份与升级

| 能力 | 状态 | 当前代码出处 | 基线 / TB 出处 | 说明 |
| --- | --- | --- | --- | --- |
| 单机 Compose，档位 `SINGLE_NODE_RECOVERABLE` | 已做（照做） | `deploy/platform/phase1/availability-tier.v1.json:5`；`runtime-inventory.v1.json` | 路线图 §1 | 服务器 139 跑的是 main |
| 多实例、HA、Kafka、ZooKeeper、Redis 集群、Cassandra、EDQS | 明确不做 | compose 里都没有 | 路线图 §S26；v4.4 参考"不应照搬"第 1 条 | |
| 迁移器按 sha256 校验，启动前检查 schema | 已做（照做） | compose 的 `phase1-migrator`、`phase1-schema-preflight`；`deploy/platform/phase1/migrations/manifest.v1.json` | 路线图 §S02 | |
| 按依赖判断就绪 | 已做（照做） | `cmd/telemetry-worker/main.go:79`（`observability.Dependency`） | 路线图 §S02 | |
| OTel、Prometheus、Loki、Tempo、Grafana、Alertmanager | 已做（照做） | `compose.yaml:1022-1120` | D10 裁决 | |
| 平台告警送到人 | 还差 | `deploy/platform/phase1/observability/alertmanager.yaml:11-12`（接收方 `none`） | D10 裁决 | 见 §6 |
| 夜间备份 | 已做（部分） | `deploy/platform/phase1/backup/hvac-backup.timer:6`；`run-scheduled-backups.sh` | 反审 §7（Backup/Restore UNVERIFIED） | 每天 19:30 UTC，PostgreSQL 和 ClickHouse 都备 |
| 异地备份和恢复演练 | 还差 | `scripts/run-phase1-postgres-restore-test.mjs`（有脚本） | 路线图 §S25 恢复门 | 见 §6 |
| 产品发布清单 | 已做 | `deploy/platform/phase1/product-release.v1.json` | 路线图 §S02、§S25 | |
| 维护作业：证书到期扫描、死任务处置、租户退役 | 已做（照做） | `infra/registry/postgres/init/013-s23-maintenance-retirement.sql:25` | 路线图 §S23 | |
| 冻结已执行的迁移 | 还差 | 无检查 | `CLAUDE.md`"数据库迁移" | 见 §6 |
| 删掉旧系统、影子对比和模拟器的生产接线 | 已做（照做） | 删除提交 `2a15dfa3`（ThingsBoard、Node-RED 旧集成）；`tools/` 只剩模拟器和测试工具 | 路线图 §S24 | |

## 4. 基线已经过时的地方

v4.3.1.1 时，清单把 entity group、RBAC、集成、报表、白标等标为"PE 才有、源码看不到"（清单 §6）。v4.4 把它们并进了公开仓库，规模也变了：`*Controller.java` 从 60 个到 98 个，带 `@RuleNode` 的文件约 91 个（原来 77 个），`schema-entities.sql` 里有 91 条 `CREATE TABLE`，UI 页面目录 48 个（原来 32 个）。下面只列会影响当时结论的。

| v4.4 的变化 | TB 出处 | 对当时结论的影响 |
| --- | --- | --- |
| 主体改为 BUSL-1.1：商用免费只到 100 台设备、1 台服务器，要保留品牌和 license key | `thingsboard@v4.4 README.md:149-151`；`LICENSE:13-43`；`application/src/main/resources/thingsboard.yml:2059` | 不能再"需要代码级参考时去看 CE"；v4.4 只能参考设计。要看代码，固定到 v4.3.1.1 或更早的 Apache 版本 |
| entity group、role、group_permission 进了 CE | `schema-entities.sql:639,720,735`；`controller/EntityGroupController.java:112`、`RoleController.java:62` | 清单说"看不到 RBAC 源码"已不成立。ADR 0022 的调研（`docs/architecture/iam-rbac-source-review-2026-10-14.md`）对照的是 Kubernetes 和 Grafana，没有对照 TB 的 role + group_permission |
| Customer 可以多级 | `schema-entities.sql:190`（`parent_customer_id`） | D01 对 Customer 是 DEFER。以后有集团、物业、承包商分层的需求，TB 有现成形态可参考 |
| 集成和数据转换器进了 CE（HTTP、MQTT、OPC UA、Kafka、CoAP、Tuya、AWS、Azure 等 14 个模块） | `schema-entities.sql:90,107`；`common/integration/`；`controller/IntegrationController.java:86` | D09 把平台集成标为 DEFER 的理由之一是"看不到源码"，这一条不成立了。接第三方网关时，TB 的做法是"上行转换器 + 下行转换器"；本仓库是让网关直接按平台信封发（松雅轩）。两条路的取舍值得在接第二个真实站点前重新比一次 |
| 计划任务（scheduler_event）进了 CE | `schema-entities.sql:653`；`controller/SchedulerEventController.java:82`；`application/.../service/scheduler/DefaultSchedulerService.java` | 本仓库的调度只跑后台作业。按时间下发设定值（例如夜间提高冷冻水温度）这类面向用户的计划，TB 用 scheduler_event 做；这里还没有 |
| 报表模板和报表进了 CE | `schema-entities.sql:1341,1356`；`controller/ReportController.java:84`（`/api/v2/report`） | 本仓库的月报（report-service）是自己做的，走在 TB CE 基线前面；TB 是把 Dashboard 渲染成文件 |
| 密钥存储（secret 表） | `schema-entities.sql:671`；`controller/SecretController.java:53` | 路线图里 S15 的 CredentialRef 还没做；TB 已有按租户存密钥的表，可参考形态 |
| 主机 Agent：用一条 gRPC 双向流管理边缘机器上的 Docker Compose 应用（EDGE、GATEWAY、GENERIC），有期望模板版本、容器状态、日志、批量操作 | `common/agent-api/src/main/proto/agent.proto:13`；`schema-entities.sql:966-1083`；`common/data/.../agent/AgentApplicationType.java:16-21` | ADR 0015 删掉车队时说"跟边缘运行时一起重新设计"。TB 给出的新做法是"管机器上的容器"，不是 v4.3 的 Edge 实体同步，重新设计时应对照这一版 |
| AI 对话、工具调用要人批准、AI 生成方案和设备看板 | `controller/AiChatController.java:41`；`AiToolController.java:22,28`（`/api/ai/tools/resolve-approval`）；`AiSolutionController.java:32` | D09 只比了 AI Model 和 AI 规则节点，现在 TB 也有对话式助手。本仓库的助手只读、不能操作设备，比 TB"工具调用经人批准"更窄 |
| 计算字段新增 ALARM、PROPAGATION、关联实体汇总、时序汇总；设备档案里的告警节点标为过时 | `common/data/.../cf/CalculatedFieldType.java:13-19`；`rule-engine/.../profile/TbDeviceProfileNode.java:41,52` | D06 当时对照的是"设备档案里的告警规则"，TB 已把告警搬到计算字段里。D04 说 TB 没有持久化汇总，现在有 `ENTITY_AGGREGATION`；和本仓库 Metric 的差距变小 |
| RPC 状态改为受保护的状态转换 | `common/data/.../rpc/RpcStatus.java:63-74` | 不影响 ADR 0018 的结论（TB 仍然超时即终态、发下一条） |
| 白标、自定义菜单、自定义翻译、IoT Hub 市场、license 集群表 | `schema-entities.sql:1235,1246,1274,1383`；`schema-entities.sql:14`（tb_cluster） | 清单 §6 把白标标为 PE；现在可读，但对冷站节能产品没有需求 |
| 网关自动建设备时还会自动建 entity group | `application/.../transport/DefaultTransportApiService.java:454` | 加强了 D03"不让网关自动建身份"的理由 |

## 5. 代码偏离或超出基线的地方

### 做法和裁决不一致

| 偏离 | 代码出处 | 裁决出处 | 说明 |
| --- | --- | --- | --- |
| 当前值快照只从 Redis 读 | `modules/telemetry/pkg/telemetry/server.go:211-219,290-296` | ADR 0013 §5；路线图 §S06 测试"Redis 不可用不改变当前值正确性" | 真值在 PostgreSQL，启动时重建 Redis（`cmd/telemetry-worker/main.go:256`）。但读取没有回到 PostgreSQL，就绪检查又把 Redis 标为非必需（`main.go:79`），所以 Redis 挂了服务显示就绪、读取却全是 503 |
| 微信提醒自己管投递 | `services/notification-service/migrations/002_wechat_reminders.sql:45-52`；`cmd/energy-api/embedded_wechat_reminders.go:240-256` | ADR 0013 §8：外发由统一投递负责，通知不管重试 | 微信提醒在通知库里有自己的状态（SENDING、SENT、REFUSED…），经身份服务发出。统一投递的 worker 本来就没跑，等它跑起来要决定微信走不走它 |
| 规则管理页能发布规则，但规则不执行 | `services/rule-runtime-service/pkg/rulemanagement/manager.go:272`；`SystemManagement.tsx:276` | 路线图 §S20/§S21；`CLAUDE.md`"还没接入的就明确显示未接入" | 「自动化规则」页没有说明"发布后不会运行"。要么补执行进程，要么在页面上标未接入 |
| Kafka 代码还在 | `modules/audit/cmd/audit-owner/main.go:36-41`；`modules/audit/internal/audit/consumer.go:44`；`services/outbox-relay/internal/relay/kafka.go` | ADR 0013 §13；`CLAUDE.md`"直接删除废弃路径" | 默认部署里审计走 PostgreSQL 发件箱（`cmd/energy-api/embedded_energy.go:238`），这两处只在拆分验证入口和未部署的 outbox-relay 里 |
| 自建身份服务继续扩展 | `services/identity-service/internal/identity/wechat_login.go`、`app_login.go` | ADR 0013 §11；路线图 §S03 | 没有 ADR 说明为什么不换成熟的 IdP；README 列了"故意不做"的清单 |
| API Key、租户切换删掉了 | `030-drop-api-credentials.sql`；提交 `4bfbac63` | 路线图 §S04 要求做 | 理由写在提交说明里（没人用、一直 404），没有 ADR。以后接第三方系统或集团多租户时要重新做 |
| 授权变粗：只到站点 | ADR 0022 | 路线图 §S04"细粒度" | 有 ADR，取舍写清楚了 |
| 边缘没有走自研控制面 | `edge/node-red/songyaxuan-platform-envelope.js`；`libs/edgecontrol/` 只在模拟器里用 | ADR 0012（边缘控制面是一等组件） | 真实站点是第三方网关直接发平台信封。控制路径要在 Node-RED 和自研运行时之间选一个 |
| 真实站点登记直接写表 | `scripts/site-onboarding.mjs:61-65` | ADR 0013 §2（只有负责方能写） | 和模拟器种子同一套做法，绕过了登记写入接口的期望版本和审计 |
| 基线文档整体删除 | 提交 `8e67541c` | — | 没有机器可读的路线图了；ADR 0013 还在，但它引用的裁决文档只能从 git 历史里找 |

### 走在基线前面

| 新增 | 代码出处 | 说明 |
| --- | --- | --- |
| 第一个真实站点松雅轩，接入后停用 ThingsBoard，导入其历史 | `deploy/sites/songyaxuan.site.json`；`scripts/thingsboard-history-import.mjs`；`docs/specs/site-gateway-onboarding.md` | 基线只有模拟器 |
| 微信小程序、账号密码和微信一键登录 | `apps/hvac-miniprogram/`；ADR 0024 | 基线把移动端定为 DEFER |
| 微信订阅提醒（新告警、每日运行结论） | `services/notification-service/migrations/002_wechat_reminders.sql`、`003_wechat_daily.sql` | 基线没有这个通道 |
| AI 助手和 Cube 语义层 | `services/operations-agent-service/`；`semantic/cube/`；ADR 0014、0023 | 能耗指标只在 Cube 定义一次，估算电费也在 Cube 里算 |
| 节能验证（IPMVP 方法 B、C，固定基线） | `infra/registry/postgres/init/020-savings-verification.sql:9,50`、`023-savings-device-boundary.sql`；ADR 0021 | TB、OpenEMS 都没有 |
| 月报快照、站点对标、站点档案（面积、气候区） | `022-report-snapshots.sql:8`；`024-site-profile.sql:8-10` | |
| FDD 低温差诊断，规则按不可变版本发布 | `025-fdd-rule-revisions.sql:8`；ADR 0020 | |
| 告警"一动不动"条件、按站点时钟判断积压读数 | `modules/alarm/pkg/alarmservice/evaluator.go:68-71` | TB 没有对应条件 |

## 6. 还差的清单（按第一阶段影响排序）

| 优先级 | 缺什么 | 当时排在哪 | Issue | 为什么是这个优先级 |
| --- | --- | --- | --- | --- |
| 高 | 平台指令下发到松雅轩网关（ET1010 经 Node-RED），带回读 | 路线图 §S11（W3），当时假设边缘是自研的 | #542（还开着，HANDOFF 列为网关上要做的事） | 节能产品要能执行建议；现在真实站点只能看 |
| 高 | 备份拷到服务器外；在服务器上做一次恢复演练并记录 | 路线图 §S25 恢复门；反审 §7 | 无 | 真实站点的历史只剩这一份，磁盘满过一次 |
| 中 | 平台告警的接收方（Alertmanager 至少配邮件或微信） | D10 裁决 | 无 | 写入停了 15 个小时（#649）这种事没人会第一时间知道 |
| 中 | 统一外发投递的 worker 跑起来；服务器上配通知接收人、策略、模板 | 路线图 §S15、§S16（W4） | 无（#256、#277 已关） | 告警只在告警页和微信提醒里，邮件发不出去 |
| 中 | 快照读取在 Redis 缺失或不可用时回到 PostgreSQL | 路线图 §S06（W2） | 无 | 单机上 Redis 一挂，实时页和总览的实时卡片就空了 |
| 中 | 告警规则的撤销（改名、删除、设备停用后）和至少一个查看已生效规则的页面 | 路线图 §S14 | 无（#609 已关） | 真实站点规则只增不减，旧规则会误报 |
| 中 | 松雅轩网关的耐久发件箱改发平台 | D08 裁决；路线图 §S12（已删） | #542 | 断网期间的数据会丢 |
| 中 | 能耗按日、月的汇总表 | 路线图 §S07 | 无（HANDOFF"小的后续"） | 一个站点一年的实时数据现算会超过 ClickHouse 1.5 GiB 内存上限 |
| 中 | 冻结已执行的迁移，加检查 | `CLAUDE.md`"正式上线时冻结迁移" | 无 | 服务器上已有真实数据，改过的迁移会让迁移器中止 |
| 中 | 选定控制路径：自研边缘运行时上线，或明确 Node-RED 作为执行端并补它的安全约束 | 路线图 §S10；ADR 0012 | 无 | 决定了高优先级第 1 项怎么做 |
| 低 | 通用规则的线上执行 | 路线图 §S20（W6） | 无（#280 已关） | 告警和 FDD 各有自己的规则，冷站当前用不上通用规则；先把页面标成未接入 |
| 低 | 换成熟的 OIDC 身份服务，或写 ADR 说明保留自建的理由 | 路线图 §S03（W1） | 无 | 现在只有自己公司的账号，风险可控 |
| 低 | 租户配额和用量视图 | 路线图 §S04、§S23 | 无 | 只有一个真实租户 |
| 低 | 原始历史和指标结果的保留策略（含排期） | 路线图 §S08、§S23 | #651（告警判断记录和原始读数保留 30 天） | 磁盘压力目前来自 PostgreSQL 运行时表，已有清理 |
| 低 | 告警抑制、评论的接口 | 路线图 §S13 | 无 | 值班人员现在只用确认和指派 |
| 低 | 碳排、能源计划/目标、工作日历 | 三方对标 P2（MyEMS） | 无 | 节能验证和对标已覆盖第一阶段的核心诉求 |

## 7. 没能核实的地方

- **没有运行任何东西。** 没跑测试、没起本地栈、没登录服务器。"已做"只表示代码在、而且有进程接线，不保证跑得对；租户退役、恢复脚本这类只核对了代码。
- **服务器和本地的实际环境变量看不到。** 按仓库规定没有读 `deploy/platform/phase1/runtime/**`。`RULE_RUNTIME_DATABASE_URL`、`OUTBOUND_DELIVERY_DATABASE_URL_FILE` 是否在服务器上设置，是按 compose 和 `environments/*.example` 判断的。不过即使设置了，外发投递的 worker 也没有调用方，这一条不受影响。
- **v4.3.1.1 和 OpenEMS、MyEMS 没有重新克隆。** v4.3.1.1 的数字沿用清单；OpenEMS/MyEMS 的结论只用到第 1 节表里的固定版本和三方对标里的条目，没有逐条回到它们的源码。
- **ThingsBoard v4.4 只核对了入口。** 新增的 Agent、AI 对话、scheduler、报表只看了表结构、控制器路由和枚举，没有读完它们的运行语义。带 `@RuleNode` 的文件数是按行首注解数的，是近似值。
- **旧裁决文档的行号来自 `7d860338`。** 这些文件在 main 上已删除，行号只在那个 commit 里有效。
- **Issue 只按标题关键词搜。** 用了"规则、outbound、OTA、Edge、redis、retention、保留、ET1010、Node-RED、指令、通知、备份、restore、Kafka、抑制"等词，正文里提到的可能漏掉。标"无"的行表示按这些词没搜到。
- **小程序只核对了目录和有没有调用指令接口**（没有），没有逐页看。
- **松雅轩历史导入的条数核对**，HANDOFF 写着"记录里还没有核对结果"，本文也没有核对。
