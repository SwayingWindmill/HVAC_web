# HVAC Web 全模块 ThingsBoard 反向审查

状态：`CROSS_DOMAIN_MODULE_REVERSE_AUDIT_COMPLETE`

审查票：[反向审查 HVAC Web 全部对应模块](https://github.com/SwayingWindmill/HVAC_web/issues/242)

执行日期：2026-08-18

参考基线：ThingsBoard CE `v4.3.1.1`，commit `c2a52e46c44e308ddee430e7266b8e10eddde9c4`

## 1. 结论

当前 HVAC Web 已经不依赖 ThingsBoard 运行时，但这不等于当前自建实现已经完整或正确。

本轮以 `go.work`、`services/`、`libs/`、Real Build 入口、Ownership/Architecture Contract、部署资产和十份源码裁决为边界，再用 CodeGraph 复核调用关系，并在当前工作树执行直接门禁。结果如下：

- 仓库有 **23 个 service 目录**：20 个生产候选、2 个迁移期工具、1 个测试 IdP；另有 **21 个 Go library module**、Real 前端产品图和跨域部署资产。
- 没有任何证据支持“全部模块已验证”或“完整 ThingsBoard 等价能力已实现”。可保留的是一组经过直接行为测试的安全/耐久内核，不是全部产品能力。
- 生产候选服务中，4 个取得 `VERIFIED_CORE`，9 个需要 `ADAPT`，5 个核心产品语义需要 `REPLACE`，2 个属于 ThingsBoard 无直接对应物的 HVAC 本地域且仍需 `ADAPT`。
- 完整 Rule Engine、Notification Runtime、Provisioning/Credential Lifecycle、OTA/Fleet、Edge Sync/Remote Config、Registry Writer/Template、Tenant Administration、Audit Search、Lifecycle Worker、统一 Rate/Quota、Configuration Release、FDD、生产 Forecast/Optimization Read Model 等仍是 `MISSING`。
- 当前工作树存在真实回归红灯：Tenant/Organization 迁移残留、Registry `Equipment` 旧词汇、Ownership Registry Scope Contract、Real Shell、Real Assets、Operations Workspace、Edge Cycle、Settlement、Forecast 和 Optimization。它们不能被历史通过记录覆盖。
- Redis/Paho 相关测试在沙箱外重试后仍因 `proxy.golang.org` 超时而未运行完整。这些模块是 `ENV_BLOCKED / UNVERIFIED`，不是通过，也不能仅据此判定代码错误。
- ThingsBoard 继续作为固定源码参考，不进入生产依赖。需要吸收的是行为 Pattern；需要删除的是本地已证明错误、过时或迁移期的路径。
- 对 `apps/`、`services/`、`libs/` 的生产源码检索没有发现 ThingsBoard Client、URL、Token、SDK 或数据库调用；唯一生产字符串是 `libs/observability` 的敏感信息脱敏标记。`tools/eg8200-simulator/internal/simulator/thingsboard.go` 也只剩“transport retired”空壳，但其 README 仍描述旧 ThingsBoard HTTP/RPC 流程，属于必须删除的文档残留。

最重要的客观判断是：**当前系统有可取的控制安全和耐久执行基础，但仍处于“多个强内核 + 大量未闭环模块”的状态，不是完整平台。**

## 2. 审查口径

### 2.1 裁决词汇

| 裁决 | 含义 |
| --- | --- |
| `VERIFIED_CORE` | 当前工作树有直接绿色行为测试，且该内核没有被十域裁决推翻；只证明列出的内核，不代表模块完整 |
| `ADAPT` | 有值得保留的结构或行为，但权威、生命周期、恢复、测试或产品面必须补齐 |
| `REPLACE` | 核心语义与目标冲突；删除旧路径后按目标模型重做，不做双写、fallback 或兼容层 |
| `LOCAL_ONLY` | ThingsBoard CE 没有直接对应 Domain；必须由 HVAC 场景、ADR、测试和测量独立证明 |
| `MISSING` | 只有文档、DDL、占位页面、Fixture 或相邻基础，没有可运行产品闭环 |
| `REMOVE` | 迁移、Shadow、Test 或旧词汇路径不得留在最终生产图 |
| `DEFER` | 有潜在价值，但当前没有设备、产品、容量或运维证据支持实施 |

### 2.2 证据等级

| 等级 | 证据 | 可支持的声明 |
| --- | --- | --- |
| E3 | 当前工作树直接行为测试绿色 | `VERIFIED_CORE`，仅限被测试行为 |
| E2 | CodeGraph/源码 + 固定 Contract/DDL + 针对性测试曾通过或当前被环境阻断 | `ADAPT` 或 `UNVERIFIED`，不能宣称完成 |
| E1 | 文档、DDL、OpenAPI、部署模板、占位 UI | 只证明意图或形状，不证明运行时 |
| E0 | 目录名、README、注释、演示数据 | 不构成实现证据 |

模块级裁决按权威路径的最弱关键行为给出。某模块可以同时拥有 `VERIFIED_CORE` 和 `ADAPT`：前者说明哪些内核保留，后者说明模块整体仍不能验收。

### 2.3 完整性边界

本轮纳入：

- `services/` 下全部 23 个目录；
- `go.work` 中全部 21 个 `libs/` module；
- `apps/hvac-web/src/real` 的实际 Real Build 图；
- `contracts/ownership`、`contracts/architecture`、`deploy/`、`infra/` 中决定权威、迁移、运行和发布行为的资产；
- D01-D10 全部 ThingsBoard 能力域和明确缺口。

POC、Demo、Fixture、模拟器和测试 IdP 只用于证据，不算生产能力。`hvac-backend` 已由 ADR 0005 定义为非生产参考，不重新进入生产映射。

## 3. 当前门禁事实

### 3.1 绿色门禁

| 门禁 | 当前结果 | 能证明什么 |
| --- | --- | --- |
| `npm run lint` | PASS | Real/Demo TypeScript 当前可类型检查 |
| `npm run contracts:check` | PASS | 生成式 Platform Contract 未漂移 |
| `npm run ownership:check` | PASS | 当前静态 Ownership JSON 一致；不覆盖运行库测试失败 |
| `npm run operations-agent-service:check` | PASS，110/110 | Operations Agent 模块边界、预算、Evidence、Checkpoint、Owner Reader、Audit 和 HTTP 内核 |
| `npm run web:energy:test` | PASS，14/14 | Real Energy 投影、质量/时区/空值/导出行为 |
| `npm run real-commands:test` | PASS，3/3 | Real Command Scope 和状态投影 |
| `npm run real-alarms:test` | PASS，7/7 | 当前 Alarm Contract 内部一致；不证明该 Contract 是目标模型 |
| `command-service` | PASS | Command Authority 当前包行为 |
| `alarm-service` | PASS | 当前 Alarm Service 行为；不推翻 D06 的模型替换裁决 |
| `scheduler-service` | PASS | Durable Scheduler 当前测试覆盖 |
| `analytics-read-model-projector` | PASS | Energy Read Model Projector 当前单元行为 |
| `telemetry-shadow-comparator` | PASS | 迁移期 Shadow 工具可运行，不是长期产品能力 |
| `legacy-migration-service` | PASS | 迁移器当前测试可运行，不授权永久保留 |

当前直接通过的 Library 包包括 `alarmauth`、`analyticsmodel`、`workordermodel`、`workorderauth`、`commandauth`、`commandmodel`、`identitycontext`、`observability`、`oidctest`、`operationsauditevent`、`registryauth`、`sessionevent`、`sessionstore`、`telemetryauth`、`telemetryhistorymodel` 和 `workloadtls`。测试绿色只证明现有 Contract 行为，若 Contract 已被跨域裁决替换，不能据此保留旧语义。

### 3.2 真实失败与未验证

| 范围 | 当前结果 | 裁决影响 |
| --- | --- | --- |
| `iam-service` | FAIL：旧 `OrganizationID` / `ActingOrganizationID`，`iam-reconciler` 测试还引用已删除入口 | Tenant 迁移未闭环，`ADAPT` |
| `audit-ledger-service` | FAIL：旧 Organization 字段 | 耐久 Ledger 内核可保留，当前 Tenant 化不可验收 |
| `telemetry-query-service` | FAIL：旧 `ActingOrganizationID` | 查询产品和测试 Contract 都需替换 |
| `platform-core-service` | FAIL：旧 `Equipment` / `ListEquipment` 与接口漂移 | Registry 词汇迁移和读模型未闭环 |
| `platform-gateway` | FAIL/ENV_BLOCKED：Redis 下载超时；本地 S3 Gateway 测试仍用 Organization | Gateway 不得标为回归绿色 |
| `work-order-service` | FAIL：旧 Organization 字段 | 本地域模型库通过，服务集成未闭环 |
| `settlement-service` | FAIL：`MeterBinding`、`Fact`、`FactStore` 测试 API 漂移 | 本地域运行时未验证 |
| `forecast-service` | FAIL：测试仍调用旧 `NewService` 构造器 | Publication 方向可保留，模块 Gate 已断 |
| `optimization-service` | FAIL：测试调用已不存在的 `Optimize` | 当前 ESS Fallback 不能冒充产品优化 |
| `libs/ownershipregistry` | FAIL：24 个 Scope Dimension / Migration Phase 测试失败 | 静态 Ownership Check 不能代替运行级 Registry 验证 |
| `libs/edgecontrol` | FAIL：Cycle 有 7 个 phase，但 `duration=0s` | Edge Control 继续 `UNVERIFIED` |
| `rms:trusted-shell:test` | FAIL，28/56 | Real Shell 的 Session/Site/Tenant 迁移和测试 Harness 不一致 |
| `real-dashboard:test` | FAIL | Dashboard 受 Site Routing Contract 漂移影响 |
| `web:real-assets:test` | FAIL | Real Assets 模型/词汇迁移未闭环 |
| `operations-workspace:test` | FAIL | Operations 页面仍期待 `actingOrganizationId` |
| Telemetry Runtime / Metric Engine | ENV_BLOCKED：`go-redis/v9` 下载超时 | 当前工作树不能给出绿色回归结论 |
| MQTT Adapter / MQTT Connector | ENV_BLOCKED：Paho 下载超时 | Connector 与 Adapter 仍为 `UNVERIFIED` |

这些失败发生在用户已有的脏工作树上，本轮未改运行时代码。它们可能包含正在进行的迁移，但在修复并重跑前，必须按失败事实处理。

## 4. Production Service 逐模块裁决

### 4.1 20 个生产候选服务

| Module | ThingsBoard 对应域 | 当前事实 | 裁决 | 必须保留 / 必须改变 |
| --- | --- | --- | --- | --- |
| `identity-service` | D01 Authentication/User | 最小自建 OIDC/密码服务，内部包测试通过；缺 MFA、Passkey、恢复、Step-up、安全审计和完整 Store/HTTP 证据 | `REPLACE` | 保留标准 OIDC 边界；生产优先选维护良好的 IdP，不把当前模块扩张成自建通用 IdP |
| `iam-service` | D01 Authorization/Tenancy | Capability、Site Scope、Explicit Deny、短时 Grant 基础强；当前测试因 Tenant 迁移和 Reconciler 漂移失败 | `ADAPT` | 保留细粒度授权；删除 Role 硬编码、旧 Organization、补管理面、Tenant Context、Credential、Usage/Policy |
| `audit-ledger-service` | D01 Audit | 幂等消费、Hash Chain、Append-only 方向正确；测试漂移，查询面过窄 | `ADAPT` | 保留耐久 Ledger；补身份安全事件、跨域 Search、Retention/Redaction，修复 Tenant Contract |
| `platform-core-service` | D02 Entity Registry | 强类型 Site/Space/Asset/Device/Point 读取；无生产 Writer/Template/Retirement，测试仍有 Equipment | `ADAPT` | 保留 HVAC Aggregate/RLS/Binding；实现 Writer 生命周期并删除 Area/Equipment 旧路径 |
| `platform-gateway` | D01/D02/D07/D10 Gateway | BFF、Owner 路由、Scope、失败关闭结构正确；Redis 依赖未跑、S3 本地入口仍有 Organization 漂移 | `ADAPT` | 保留单公开入口和 Owner 校验；修复 Tenant 迁移、真实 Readiness、统一 Capability 和无 fallback 路由 |
| `telemetry-runtime-service` | D03/D04 Ingress/Current/Realtime | Observation、Dedup、Quarantine、Quality、Outbox、Realtime 基础；Current 的 PG→Redis→Redis 路径与权威声明冲突 | `ADAPT` | 保留摄取和耐久订阅；`REPLACE` Redis Current Authority/read path，补限额、恢复和环境可执行门禁 |
| `telemetry-query-service` | D04 History/Aggregation | 当前只提供有限 Device/numeric/latest-N 查询；测试 Contract 仍有 Organization | `REPLACE` | 重建 typed raw + cursor + quality + watermark + counter/gauge/state aggregation；删除旧查询契约 |
| `metric-engine-service` | D04 Calculated Fields | Released Version/DAG/Run/Publication/Reconcile 方向正确；Result `revision=1`、质量计算和枚举漂移未解决 | `ADAPT` | 保留 Metric Domain；`REPLACE` Result Revision/Current Projection/Gauge Rollup，补预算、Debug 和绿色测试 |
| `analytics-read-model-projector` | D04/D07/D09 Analytics | 当前单元测试绿色，Owner Projection 边界清楚 | `VERIFIED_CORE + ADAPT` | 保留 Energy 投影；补跨存储 Watermark、完整聚合 Gate、容量和发布证据 |
| `mqtt-telemetry-adapter` | D03 Transport | MQTT 上行和 Command Worker 同进程装配，静态 Binding，Paho 测试被网络阻断 | `ADAPT` | 拆分模块故障域，建立 transport-neutral Port、有限 Retry/Parking、Credential/Session 生命周期 |
| `command-service` | D03 Command/RPC | Intent、Approval、Idempotency、Fence、Audit 和状态机当前测试绿色 | `VERIFIED_CORE + ADAPT` | 保留 Command Authority；补不可变 Capability/Profile Release、Edge Binding 和完整产品管理面 |
| `command-dispatcher` | D03 RPC Dispatch/Verify | 核心 dispatcher 包绿色；Connector 测试环境阻断，重启/多实例状态和 Retry 队头仍有缺口 | `ADAPT` | 保留 Pre-send Fence、Outcome Unknown、独立 Readback；补 Connector Ownership/恢复、有限 Retry/Dead |
| `alarm-service` | D06 Alarm | 当前包绿色，但单 `status`、任意 Close/Reopen、调用方 Alarm ID、单阈值 Rule 与目标冲突 | `REPLACE` | 保留 RLS、Evidence、Idempotency、Expected Version；替换为 Active/Cleared × Ack、Fingerprint、Rule State |
| `scheduler-service` | D05/D10 Scheduling | Lease、Misfire、Retry、Dead、Timezone、Concurrency 当前测试绿色 | `VERIFIED_CORE + ADAPT` | 保留 Scheduler；补主 Cycle/容量/公平性、Rule Continuation、Maintenance Work Type 和 Owner Reconcile Contract |
| `outbox-relay` | D05/D09/D10 Queue/Delivery | 耐久 Relay Pattern 有价值；测试仍用 Organization，Retry 无统一终态 | `ADAPT` | 保留 Lease/Inbox/稳定事件；补 Max Attempt、Failure Class、Dead/Quarantine 和 Operator Disposition |
| `forecast-service` | D09 Forecast | Last-value、全结果 `FALLBACK`、Publication/Reconcile；当前测试编译失败，Real/Gateway 未接 | `REPLACE_PRODUCT + ADAPT_CORE` | 保留版本化输入与发布；真实 Forecast Model/Worker/Public Read Model 重新实现，禁止把 Fallback 当模型结果 |
| `optimization-service` | D09 Optimization | ESS-only、SHADOW、`NO_DISPATCH` Fallback；测试编译失败，无 Durable Run/Public Read | `REPLACE` | 保留 Recommendation-only 和发布边界；按 HVAC Resource/Constraint/Run 模型重建 |
| `operations-agent-service` | D09 AI/Automation | 11 模块、39 源文件，当前 110/110 通过；Evidence-first、read-only Owner Tool、预算和 Audit 清楚 | `LOCAL_ONLY / VERIFIED_CORE + ADAPT` | 保留独立调查运行时；补 Provider Registry、Secret/Cost/Data Egress 管理和当前 Real Workspace Tenant 漂移 |
| `settlement-service` | 无直接 TB 对应 | HVAC 结算与对账；测试 API 漂移，Real 只是占位 | `LOCAL_ONLY / ADAPT` | 按 Meter/Metric Lineage、Tariff Revision、Lock/Correction/Reconcile 独立证明，不因 TB 缺失而删除 |
| `work-order-service` | 无完整 TB CE 对应 | 本地 Work Order Aggregate 强，Library 通过；Service 测试仍用 Organization，正式 Alarm Link 未闭环 | `LOCAL_ONLY / ADAPT` | 保留 Timeline/Version/Evidence；修复 Tenant 化、Owner Link、Notification/Assignment 集成和服务回归 |

### 4.2 迁移期与测试服务

| Module | 当前用途 | 裁决 |
| --- | --- | --- |
| `legacy-migration-service` | Registry 一次性迁移，当前测试绿色 | `REMOVE_AFTER_ACCEPTANCE`；完成迁移、对账和签收后从工作区/部署图删除，不保留运行时 fallback |
| `telemetry-shadow-comparator` | S2 切换 Shadow 对比，当前测试绿色 | `REMOVE_AFTER_CUTOVER`；证据归档后删除生产接线，可保留独立离线诊断工具时需重新立项 |
| `oidc-test-provider` | 测试认证 Fixture | `TEST_ONLY`；不得出现在生产镜像、Compose 或安全能力声明 |
| `tools/eg8200-simulator` | 现场模型、MQTT 和 Edge 验收工具；`thingsboard.go` 已退役为空壳，README 仍是旧说明 | `TEST_ONLY + CLEANUP`；保留物理模型/验收价值，删除空壳和旧 ThingsBoard 文档，不接入 Real 生产 Truth |

## 5. Go Domain/Platform Library 逐模块裁决

| Library | 裁决 | 证据与边界 |
| --- | --- | --- |
| `alarmauth` | `VERIFIED_CORE + ADAPT` | 当前测试绿色；保留 Site-scoped Action，随新 Alarm 正交模型更新 Action/Projection |
| `alarmmodel` | `REPLACE` | 当前测试绿色只证明旧模型自洽；D06 已证明 `ACKNOWLEDGED/SUPPRESSED/CLOSED` 单状态模型错误 |
| `analyticsmodel` | `VERIFIED_CORE + ADAPT` | 当前测试绿色；补 Dataset/Watermark/Quality 和目标查询语义 |
| `commandauth` | `VERIFIED_CORE` | 当前测试绿色；精确 Scope/Action/Grant 是 Command 安全证据 |
| `commandmodel` | `VERIFIED_CORE` | 当前测试绿色；Intent/Attempt/Outcome Unknown/Fence 模型保留 |
| `domainoutbox` | `ADAPT` | Lease/Inbox/Aggregate Fence 应保留；当前无直接测试文件，Retry/Dead/Quarantine 不完整 |
| `edgecontrol` | `ADAPT / UNVERIFIED` | Process Image/Cycle/Arbiter/Lease 方向保留；直接测试 `duration=0s` 失败 |
| `identitycontext` | `VERIFIED_CORE` | 当前测试绿色；统一使用 Tenant，删除所有调用方旧 Organization Fixture |
| `observability` | `VERIFIED_CORE + ADAPT` | 当前测试绿色；Readiness 仍只是人工状态，生产 Owner 覆盖和 Export Loss SLO 不完整 |
| `oidctest` | `TEST_ONLY` | 当前测试绿色；只作为协议 Fixture，不作为生产 IdP |
| `operationsauditevent` | `VERIFIED_CORE` | 当前测试绿色；调用方 Tenant 迁移仍需闭环 |
| `ownershipregistry` | `ADAPT / FAILING` | 静态 JSON Check 通过，但 Go 测试有 24 个 Scope/Phase 失败；运行级证据优先于静态声明 |
| `registryauth` | `VERIFIED_CORE` | 当前测试绿色；保留 BOLA-safe 和 Site Scope，补管理 Action Catalog |
| `sessionevent` | `VERIFIED_CORE` | 当前测试绿色；保留版本化安全事件信封 |
| `sessionstore` | `VERIFIED_CORE` | 当前测试绿色；Session 与 Audit Intent 同事务方向保留 |
| `telemetryauth` | `VERIFIED_CORE` | 当前测试绿色；精确 Device/Key/Scope 和短时能力保留 |
| `telemetryhistorymodel` | `ADAPT` | 当前测试绿色，但现有 Device/numeric 查询不是目标 Point/typed/cursor/quality 查询产品 |
| `testpki` | `TEST_ONLY` | 仅测试 PKI；不得替代生产证书生命周期 |
| `workloadtls` | `VERIFIED_CORE` | 当前测试绿色；mTLS/SPIFFE 工作负载边界保留 |
| `workorderauth` | `LOCAL_ONLY / VERIFIED_CORE` | 当前测试绿色；由 HVAC 业务和 Site Scope 独立证明 |
| `workordermodel` | `LOCAL_ONLY / VERIFIED_CORE` | 当前测试绿色；Timeline/Version/Transition 强约束保留，服务层仍未通过 |

## 6. Real Frontend 逐产品面裁决

| Real Surface | 当前事实 | 裁决 |
| --- | --- | --- |
| Real Runtime Config / Build Graph | 固定 `/api/v1`、`centrifugo-v1`、Real/Demo 图隔离；TypeScript 通过 | `VERIFIED_CORE` |
| Authenticated Shell / Protected Scope | 设计上 BFF、Fail-closed、Site Purge、无 Demo fallback；当前 28/56 Shell 测试失败 | `ADAPT / FAILING` |
| Site Routing / Navigation | UUIDv7 Site、不可见性、显式多 Site 选择方向正确；仍混有 Organization 测试/文本 | `ADAPT` |
| Real Dashboard | 已接部分权威投影；当前测试受 Site Contract 漂移失败，缺统一 Snapshot/Watermark | `ADAPT` |
| Real Assets | Registry + Current + History + Realtime 调用链存在；全量浏览器过滤/无分页，当前模型测试失败 | `REPLACE_QUERY_PATH + ADAPT_UI` |
| Real Energy | 当前 14/14，通过 Owner Analytics 数据，不补造缺失值 | `VERIFIED_CORE + ADAPT` |
| Real Commands / Control | Command 投影 3/3 通过；完整列表/批量/运行态管理未接 | `VERIFIED_CORE + ADAPT` |
| Real Alarms | 当前旧 Contract 测试 7/7；D06 已裁决目标模型不同 | `REPLACE_CONTRACT` |
| Real Work Orders | 有真实 Owner 页面；服务测试 Tenant 漂移，Alarm Link/协作未闭环 | `LOCAL_ONLY / ADAPT` |
| Operations Workspace | Agent Service 绿色；页面测试因 `actingOrganizationId` 漂移失败 | `LOCAL_ONLY / ADAPT` |
| System Management | 只覆盖窄 Registry/System 读取，不是 Tenant/User/Profile/Policy/Usage 完整管理面 | `ADAPT / MISSING_PRODUCT_AREAS` |
| Forecast | 明确显示 `NOT_INTEGRATED`，没有伪数据 | `MISSING`；诚实占位应保留到真实 Read Model 接入 |
| Optimization | 明确显示 `NOT_INTEGRATED`，没有伪建议 | `MISSING` |
| FDD | 只有空产品框架，无权威诊断服务 | `MISSING` |
| Cost | 只有空产品框架，无 Tariff/Cost/Benefit Read Model | `MISSING` |
| Settlement | 只有空产品框架，无权威结算 Read Model | `MISSING` |
| Big Screen | Real 导航仍标记“演示大屏”；不能作为生产大屏 | `REPLACE_OR_REMOVE`，先由权威 Summary Read Model 驱动再进入 Real |
| Rule Management / Debug / Replay | 无页面 | `MISSING` |
| Notification Inbox / Policy / Template | 无页面 | `MISSING` |
| Edge Fleet / Release / OTA | 无页面 | `MISSING` |
| Native Mobile | 无独立实现 | `DEFER`，当前先保证响应式 Web 和现场终端证据 |

Real Build 的“缺功能时显示未接入、绝不加载 Demo/Mock”是正确安全行为，应保留；但占位页面不能计入能力完成度。

## 7. 跨域平台与部署资产裁决

| 平台能力 | 当前事实 | 裁决 |
| --- | --- | --- |
| Phase 1 Physical Topology | `energy-api + iot-service + telemetry-worker + metric-worker` 方向已冻结 | `KEEP` 作为最小可运行拓扑；不宣称 HA |
| Single-node Deployment | Compose、备份脚本和恢复模板存在，关键依赖均有单点 | `ADAPT`；状态只能是 `SINGLE_NODE_RECOVERABLE`，需真实 Restore Drill |
| Ownership Contracts | 静态检查通过，运行库测试失败 | `ADAPT`；Contract 与 Resolver 必须同一门禁绿色 |
| Migration Runner | Manifest/Hash 有价值；执行 SQL 与登记分离，运行时改写 SQL | `REPLACE` |
| Redis Cache | 版本 CAS/重建思路可用；Current Read 让 Redis 成功与否影响权威读 | `REPLACE_CURRENT_PATH` |
| Rate/Quota | 主要只有 Operations Agent 进程内限流 | `REPLACE / MISSING_PLATFORM_POLICY` |
| Readiness | 人工 `MarkReady`，未持续验证必要依赖/Worker | `REPLACE_HEALTH_SEMANTICS` |
| Observability | OTel/Prometheus/Loki/Tempo 和低基数校验存在；覆盖与 Export Loss SLO 不完整 | `VERIFIED_CORE + ADAPT` |
| Backup/Restore | 目标、脚本、模板存在；没有当前生产时间戳化演练证据 | `UNVERIFIED` |
| Configuration Release | 有 Digest/Cohort/Contract Revision，缺统一 Draft/Publish/Revert/Dependency Validation | `MISSING` |
| Maintenance/Lifecycle | Scheduler 内核存在，Retention/Archive/Certificate/Dead/Deletion Workers 不完整 | `ADAPT / MISSING_WORK_TYPES` |
| Multi-instance/HA | 无统一 Ownership、共享限流、重平衡和数据层 HA 证据 | `DEFER` 到容量/可用性门满足；禁止现在宣称 HA |

## 8. ThingsBoard 能力到本地模块/缺口的最终反向映射

| 域 | 已有可保留内核 | 需要替换 | 缺失能力 |
| --- | --- | --- | --- |
| D01 Security/Tenancy | BFF Session、Tenant/Site Capability、Explicit Deny、RLS、SPIFFE、事务 Audit | 自建通用 IdP 方向、Role 硬编码、Organization 残留 | MFA/Step-up、多 Tenant Context、管理面、Scoped Credential、Usage/Policy、Retirement、Audit Search |
| D02 Entity/Profile | 强类型 HVAC Aggregate、Binding、RLS、Repeatable Snapshot | Area/Equipment、过宽 Binding 基数、浏览器全量列表 | Writer CRUD、Template Revision、Import/Export、Retirement、Scoped Data View、受限 Entity Query |
| D03 Connectivity/RPC/OTA | Telemetry 摄取、Command Intent/Fence/Readback、MQTT 基础 | 无限 Retry、Connector 内存状态、静态 Binding、Simulator 生产耦合 | Transport Port、Session Registry、Credential/Provisioning、Desired/Reported Config、Signed OTA Campaign |
| D04 Telemetry/Metric | Observation/Quality/Outbox/Raw History/Counter Boundary/Metric DAG/Realtime | Redis Current、History 丢乱序、numeric-only、伪 Watermark、Metric `revision=1`、Gauge Rollup | Typed Query/Aggregation、Lifecycle Worker、完整 Debug/Backfill 证据 |
| D05 Rule/Queue/Schedule | Outbox、Scheduler、Command/Agent/Edge 的边界 Pattern | 无界 Retry、把 Alarm/Metric/Agent 误当 Rule Engine | 完整不可变 Rule Runtime、Catalog、Execution、State、Trace、Replay、UI |
| D06 Alarm/Notification | Scope、RLS、Evidence、Idempotency、ACK 自然幂等 | 单 Status、Close/Reopen、调用方 ID、Simple Threshold | Stateful Alarm Rule、正交 Alarm、Notification Runtime/Inbox/Escalation |
| D07 Dashboard/Mobile | Real Scope、真实 Owner 数据、状态诚实、Demo 隔离 | 全量 Assets、旧 Alarm 投影、Real 演示大屏 | Summary Read Model、一致时间窗、版本化布局/展示资产、移动证据 |
| D08 Edge/Offline/Config | Cycle/Controller/Safety/Lease、MQTT Spool 基础 | Simulator Driver 生产绑定、弱 Manifest/Packet Queue | Fleet Identity、双向 Cursor/Ack、Snapshot/Delta/Tombstone、Signed Release、Offline Capacity |
| D09 AI/Analytics/Integration | Operations Agent、Energy、Cross-store Publication/Reconcile | Forecast/Optimization 完成假象、直接外部副作用 Pattern | Model Registry、真实 Forecast/Optimization、Delivery Ledger、Outbound Adapter |
| D10 Platform/Ops | Outbox/Inbox、Lease/Fence、Scheduler、OTel、恢复资产 | Migration、Readiness、Redis Authority、进程内配额、无界维护重试 | 统一 Rate/Quota、Configuration Release、System Info/Usage、升级 Gate；HA 按证据延后 |

该表覆盖 `thingsboard-ce-capability-inventory.v1.json` 的十个域；每个能力要么落到已列模块，要么成为显式 `MISSING/DEFER`，没有以“以后再看”隐藏。

## 9. 允许保留的 ThingsBoard 差异及证据

只有以下冲突拿得出明确 HVAC、安全或正确性证据，可以保留本地方向：

| 保留差异 | 证据 | 结论 |
| --- | --- | --- |
| BFF Session，不让浏览器持有 JWT Pair | `docs/security/s0-durable-session-audit.md`、`libs/sessionstore` 当前绿色测试 | `KEEP` |
| Tenant/Site/Action/Explicit Deny，不采用固定三 Authority | `libs/identitycontext`、`registryauth`、`telemetryauth`、`commandauth` 当前绿色测试 | `KEEP` |
| Tenant→Site→Space→Asset→Device→Point 和类型化 Binding，不用自由 Relation 作权威 | ADR 0001、ADR 0011、Registry DDL/Contract；但 Writer/测试漂移仍需修 | `KEEP_MODEL / ADAPT_RUNTIME` |
| 同时间戳保留多个 Observation，Current 防回退 | ADR 0003、Telemetry DDL/Outbox、D04 源码裁决 | `KEEP`；当前 Redis 读路径另行替换 |
| Command Intent/Attempt/Fence/Outcome Unknown/独立读回，不用通用 RPC 成功即完成 | ADR 0006，`command-service` 与 Command Projection 当前绿色 | `KEEP` |
| 耐久 Scheduler/Outbox，不采用内存 Delay/Dedup | `scheduler-service` 当前绿色；`domainoutbox` 源码 | `KEEP_CORE / ADAPT_RETRY` |
| Rule 只产生 Owner Intent，不直接改 Telemetry/Alarm/Command/外部真值 | D05 Owner Matrix、ADR 0006/0010/0012 | `KEEP_TARGET_BOUNDARY`；Rule Runtime 仍缺失 |
| Operations Agent 独立于高频 Rule/Control Runtime | ADR 0009、ADR 0010，当前 110/110 | `KEEP` |
| Phase 1 单机可恢复，不为架构外观提前上 Kafka/Actor/HA | D10 Capacity/Recovery Gate，当前无多实例证据 | `KEEP_SIMPLE_TOPOLOGY` |
| Work Order、Settlement 等 HVAC 本地域不因 ThingsBoard 无对应物而删除 | `workordermodel`/`workorderauth` 当前绿色；结算设计与业务文档 | `LOCAL_ONLY`，服务级回归仍必须修复 |

除上述有证据差异外，若本地行为与固定 ThingsBoard 参考模式实质冲突，且没有额外 HVAC 安全、复杂度或性能证据，后续默认按十域裁决重构。

## 10. 必须删除或替换的路径

后续实现不能在这些路径外再加兼容层：

1. 删除 `OrganizationID`、`ActingOrganizationID`、`OrganizationMembership` 及相关 Fixture/断言，Tenant 是唯一 Domain 词汇。
2. 删除 Registry 的 `Area/Equipment/ListEquipment/EquipmentID` 路径，Space/Asset 是唯一机器 Contract。
3. 删除旧 Alarm `OPEN/ACKNOWLEDGED/SUPPRESSED/CLOSED` 单枚举及前端扫描 Transition 的补偿逻辑。
4. 删除 PostgreSQL 读后写 Redis 再读 Redis 的 Current 路径；Redis 只作可重建 Projection。
5. 删除 numeric-only/no-cursor/伪 Revision/伪 Watermark 的 Telemetry Query Contract。
6. 删除 Metric `revision=1` 和依赖 ReplacingMergeTree 偶然选 Current 的行为。
7. 删除无界 Transient Retry、无 Dead/Quarantine 的队列路径。
8. 删除 Real 产品图中的演示 Big Screen 身份；未接入功能保持明确 `NOT_INTEGRATED`。
9. 删除生产 Runtime 对 Simulator Driver/Simulator Truth 的绑定；模拟器只用于测试和验收。
10. Registry/Telemetry 切换证据完成后删除 `legacy-migration-service` 与 `telemetry-shadow-comparator` 的生产接线。
11. 删除声称 HA、真实 Readiness、缓存可重建或能力已 `ALIGNED` 但源码/运行门禁不支持的文档结论。
12. 删除 EG8200 Simulator 的空 `thingsboard.go` 和 README 中旧 ThingsBoard HTTP/RPC 准备说明；不新增 ThingsBoard URL、Token、SDK、数据库或运行时 fallback，ThingsBoard 只保留固定源码证据引用。

## 11. 交给跨域 Domain 模型裁决的冲突

本票确认以下所有权冲突真实存在，但不抢先替 #240 作最终 Domain 决定：

1. `DeviceTemplateRevision` 只拥有稳定引用，Transport Credential/Session 归 D03，Rule Release 归 D05，Alarm Policy 归 D06，Display Asset 归 D07，Edge Release 归 D08。
2. Telemetry Current Authority 必须在 PostgreSQL Durable Snapshot 与 Redis Projection 之间唯一；History/Analytics/Metric 各自消费明确 Revision/Watermark。
3. Rule Engine 只拥有 Execution/State/Effect Intent；Alarm、Command、Notification、AI、External Delivery 各自拥有最终提交和 Receipt。
4. Alarm 的 Active/Cleared、Ack、Assignment、Suppression、Work Order Link 和 Notification Disposition 必须拆成正交事实，不能共享一个 Status。
5. Cloud Command Intent、Edge Lease/Arbiter、Device/PLC Interlock 与 Readback Verification 必须形成单向授权链，任何上层都不能越过 Edge 安全权威。
6. Configuration Revision、Template Revision、Rule Release、Edge Release、Model Deployment 和 Product Release 的发布/回滚边界需要统一上层语言，但不能合并成可变 God Profile。
7. External Delivery Ledger 应成为 Notification、Integration 和必要 AI Provider 调用的共享 Pattern；业务 Owner 不直接拥有 Provider Retry。
8. Phase 1 物理合并与逻辑 Owner 边界必须同时成立；进程合并不能允许跨 Schema 写入或绕过 Owner Port。

## 12. 最终分类清单

### VERIFIED_CORE

- `command-service`；
- `scheduler-service`；
- `analytics-read-model-projector`；
- `operations-agent-service`（`LOCAL_ONLY`）；
- Real Runtime Build Boundary、Real Energy、Real Command Projection；
- `commandauth`、`commandmodel`、`identitycontext`、`operationsauditevent`、`registryauth`、`sessionevent`、`sessionstore`、`telemetryauth`、`workloadtls`；
- `workorderauth`、`workordermodel`（`LOCAL_ONLY`）。

这些条目只获得内核保留资格，不代表整个产品域完成。

### ADAPT

- `iam-service`、`audit-ledger-service`、`platform-core-service`、`platform-gateway`；
- `telemetry-runtime-service`、`metric-engine-service`、`mqtt-telemetry-adapter`、`command-dispatcher`、`outbox-relay`；
- `settlement-service`、`work-order-service`；
- Real Shell、Dashboard、Assets、Work Orders、Operations、System；
- `alarmauth`、`analyticsmodel`、`domainoutbox`、`edgecontrol`、`observability`、`ownershipregistry`、`telemetryhistorymodel`。

### REPLACE

- `identity-service` 作为通用生产 IdP 的方向；
- `alarm-service` / `alarmmodel` 的核心聚合与规则状态；
- `telemetry-query-service` 的当前查询产品；
- `forecast-service` 的产品能力声明与 Real/Gateway 缺链；
- `optimization-service` 的 ESS Fallback 产品模型；
- Redis Current Read、Migration Runner、平台 Rate/Quota、Readiness、旧 Alarm/Organization/Equipment Contract；
- Real Big Screen 的演示身份和 Real Assets 全量浏览器查询路径。

### LOCAL_ONLY

- Operations Agent；
- Work Order；
- Settlement/Cost/Reconciliation；
- HVAC Edge Cycle/Controller/Arbiter；
- FDD 和能源优化的 HVAC Domain 部分。

它们不因 ThingsBoard 没有直接对应物而自动获得保留权，仍必须通过本项目 ADR、行为测试和生产测量。

### MISSING

- 完整 Rule Engine 和 Rule UI；
- Notification Runtime/Inbox/Policy/Template/Escalation；
- Registry Writer/Template/Import/Export/Retirement；
- MFA/Step-up/Tenant Admin/ApiCredential/Usage/Retirement/Audit Search；
- Provisioning/Credential Rotation/Gateway Child Session/Desired Config/Signed OTA；
- Edge Fleet/Sync/Remote Config/Release/Offline Capacity；
- Typed Telemetry Query/Aggregation、Lifecycle Worker；
- 外部 Delivery Ledger 和生产 Outbound Integration；
- 真实 Forecast/Optimization/FDD/Cost/Settlement Read Model；
- 统一 Rate/Quota、Configuration Release、完整 Maintenance 和升级 Compatibility Gate。

### REMOVE / DEFER

- `legacy-migration-service`、`telemetry-shadow-comparator` 在对应切换验收后删除生产接线；
- `oidc-test-provider`、`oidctest`、`testpki`、EG8200 Simulator 只作测试/验收；
- CoAP/LwM2M/SNMP/Sparkplug、Native Mobile、通用 Widget/Connector 市场、多实例 Actor/Kafka/HA 在真实设备、容量和可用性证据前 `DEFER`。

## 13. 本票完成条件

- 23/23 service 目录已分类；
- 21/21 Go library module 已分类；
- Real Build 的已接、占位、缺失和演示产品面已分类；
- D01-D10 全部能力已映射到本地模块或显式缺口；
- 所有保留的重大 ThingsBoard 差异都给出 ADR、行为测试或安全/正确性证据；
- 当前工作树的绿色、失败和环境阻断已分开记录；
- 已形成 #240 所需的跨域所有权冲突输入；
- 本票未修改运行时行为，也未给任何已有模块默认优先权。

本轮结论用于目标 Domain 模型和实施路线的下一步裁决，不是生产认证报告。
