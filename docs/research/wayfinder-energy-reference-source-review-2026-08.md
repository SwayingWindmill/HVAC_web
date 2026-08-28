# Wayfinder #303：三方能源模块与 HVAC 现状源码证据

状态：RESEARCH COMPLETE / SOURCE-PINNED  
审查日期：2026-08-24  
范围：Backend、能源数据处理、能源内容模块、UI 内容入口；不修改部署架构，不实现 Edge。

## 1. 审查规则与固定基线

本记录遵循 `AGENTS.md` 的 source-first 规则：先固定官方 release/tag 和完整 commit，再读取生产源码、官方测试、Schema/迁移和官方文档。HVAC 当前实现只作为待核对证据，不享有默认正确性。

| 项目 | 固定版本 | 完整 commit | 本次实际读取的重点 |
| --- | --- | --- | --- |
| ThingsBoard CE | v4.3.1.1 | `c2a52e46c44e308ddee430e7266b8e10eddde9c4` | Telemetry 保存、Latest、Rule Engine、Calculated Field、Dashboard 服务/UI、WebSocket 数据订阅 |
| OpenEMS | 2026.7.0 | `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` | Channel/Process Image、Cycle、Scheduler、Timedata、断线历史数据重发 |
| MyEMS | v6.7.0 | `be6e6ce8ddeac57afb04bddb9621501fb555cab0` | database、cleaning、normalization、aggregation、API、admin/web UI |

官方入口：

- [ThingsBoard v4.3.1.1 release](https://github.com/thingsboard/thingsboard/releases/tag/v4.3.1.1)
- [OpenEMS 2026.7.0 release](https://github.com/OpenEMS/openems/releases/tag/2026.7.0)
- [MyEMS v6.7.0 release](https://github.com/MyEMS/myems/releases/tag/v6.7.0)

## 2. ThingsBoard：平台运行时和可组合视图，不是能源模型

### 2.1 实际源码证据

| 固定提交中的文件 | 源码/测试观察 |
| --- | --- |
| [`TbMsgTimeseriesNode.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/telemetry/TbMsgTimeseriesNode.java) | 一个保存时间序列的 Rule Node 同时协调历史时间序列、Latest、WebSocket 通知和 Calculated Field 通知；每种动作可使用 On Every Message、Deduplicate、Skip；时间戳默认来自消息 metadata，也可使用服务器时间；TTL 按消息、节点配置、Tenant Profile 依次取值。 |
| [`TbMsgTimeseriesNodeTest.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/rule-engine/rule-engine-components/src/test/java/org/thingsboard/rule/engine/telemetry/TbMsgTimeseriesNodeTest.java) | 官方测试覆盖消息类型、空消息、TTL、server timestamp、重复处理、WebSocket-only、按动作独立去重和全部 Skip；这证明处理策略是运行时契约，而不是 UI 行为。 |
| [`root_rule_chain.json`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/main/data/json/tenant/rule_chains/root_rule_chain.json) | 默认租户 Rule Chain 把消息类型分发到 Save Timeseries、Save Client Attributes、RPC 和日志节点；数据进入通用消息/规则管线，而不是直接进入能源专用模型。 |
| [`TbCalculatedFieldsNode.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/rule-engine/rule-engine-components/src/main/java/org/thingsboard/rule/engine/telemetry/TbCalculatedFieldsNode.java) | Calculated Field 可以被单独触发，使用 `CF_ONLY` 策略而不持久化原始消息；它属于实时派生能力，不等于能源清洗、计量结算或报告事实。 |
| [`RelatedEntitiesAggregationCalculatedFieldTest.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/application/src/test/java/org/thingsboard/server/cf/RelatedEntitiesAggregationCalculatedFieldTest.java) | 官方集成测试覆盖关系创建/删除、关系路径变化、参数变化、指标变化、去重窗口、默认值和聚合结果；关系图会影响派生结果。 |
| [`TimeseriesLatestDao.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/timeseries/TimeseriesLatestDao.java)、[`AbstractSqlTimeseriesDao.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/sqlts/AbstractSqlTimeseriesDao.java) | Latest 与历史查询分别有 DAO 边界；历史查询支持明确的 TTL、分区和聚合查询参数。Latest 不是浏览器内存缓存的别名。 |
| [`DashboardServiceImpl.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/main/java/org/thingsboard/server/dao/dashboard/DashboardServiceImpl.java)、[`DashboardServiceTest.java`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/dao/src/test/java/org/thingsboard/server/dao/service/DashboardServiceTest.java) | Dashboard 由 Tenant 持有，可分配给 Customer/Edge；服务层校验实体存在和跨 Tenant 边界，保存/删除会发布事件并处理缓存失效。 |
| [`dashboard-page.component.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/modules/home/components/dashboard-page/dashboard-page.component.ts) | UI Dashboard 是布局、Breakpoint、Widget、Alias、State、编辑态和只读态的组合容器；它不是一个固定的能源页面。 |
| [`entity-data-subscription.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/api/entity-data-subscription.ts)、[`telemetry-websocket.service.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/ws/telemetry-websocket.service.ts) | UI 通过统一 EntityData/WebSocket 订阅得到历史、Latest、聚合和比较数据，前端再按 Widget 数据键和聚合器更新视图。 |
| [`dashboard.service.ts`](https://github.com/thingsboard/thingsboard/blob/c2a52e46c44e308ddee430e7266b8e10eddde9c4/ui-ngx/src/app/core/http/dashboard.service.ts) | UI 通过独立 Dashboard API 获取、保存、删除和分配 Dashboard；Customer、Public Dashboard、Edge Dashboard 都是显式服务操作。 |

### 2.2 对 HVAC 的有效启示

- `ADOPT`：把 Latest、历史查询、实时订阅、聚合查询分别定义为明确的服务/查询边界；不要让浏览器直接拼接底层时序查询。
- `ADOPT`：实时处理需要显式声明时间戳来源、TTL、去重和处理结果；这些规则属于运行时契约。
- `ADAPT`：采用 Dashboard/Widget/Alias/State 的可组合思想，但 Energy Management 页面必须使用固定的能源领域查询契约，不能直接暴露任意 telemetry key、Cube member 或 SQL。
- `ADAPT`：关系图可作为能源派生的输入，但 MeterBinding、Space 归属和 Energy Flow 必须是有类型、有约束的能源关系，不能复制自由关系图。
- `REJECT`：把 ThingsBoard Rule Engine 当作 HVAC 的能源处理核心；它能触发和编排消息，却没有 MyEMS 那样的能源质量、计量、费率、碳因子和报告语义。
- `REJECT`：把通用 Dashboard 模型当作能源内容模型；Energy Content 需要自己的查询、质量和版本边界。

## 3. OpenEMS：Edge 控制语义，不是本阶段 Backend 能源内容

### 3.1 实际源码证据

| 固定提交中的文件 | 源码/测试观察 |
| --- | --- |
| [`Channel.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.common/src/io/openems/edge/common/channel/Channel.java) | Channel 有静态 ChannelDoc、ChannelAddress、类型、当前 Value 和回调；采用 Process Image，后台填充 next value，在切换点把 next value 变为 active/current value。 |
| [`Value.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.common/src/io/openems/edge/common/channel/value/Value.java) | Value 明确允许 UNDEFINED，提供 `getOrError`、Optional、时间戳和类型/单位格式化；设备通信丢失时的未定义状态是模型的一部分。 |
| [`CycleWorker.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.core/src/io/openems/edge/core/cycle/CycleWorker.java) | 一个 Edge Cycle 依次执行 watchdog、切换 Channel Process Image、更新 Sum、事件、Schedulers、Controllers 和写入阶段；控制执行与数据采集切换有明确时序。 |
| [`SchedulerFixedOrderImpl.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.scheduler.fixedorder/src/io/openems/edge/scheduler/fixedorder/SchedulerFixedOrderImpl.java)、[`SchedulerFixedOrderImplTest.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.scheduler.fixedorder/test/io/openems/edge/scheduler/fixedorder/SchedulerFixedOrderImplTest.java) | Scheduler 保存去重后的 Controller ID 有序集合；官方测试验证配置顺序就是执行顺序。 |
| [`Timedata.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.timedata.api/src/io/openems/edge/timedata/api/Timedata.java)、[`TimedataManagerImpl.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.backend.core/src/io/openems/backend/core/timedatamanager/TimedataManagerImpl.java) | Timedata 是 Edge/Backend 的可插拔历史数据接口；Backend TimedataManager 可组合多个 Timedata provider，对历史数据、能量周期数据和写入进行路由/归一化。 |
| [`ResendHistoricDataWorker.java`](https://github.com/OpenEMS/openems/blob/2e2792d59fc5ba3b99ce3cf98d15081c0a74895e/io.openems.edge.controller.api.backend/src/io/openems/edge/controller/api/backend/ResendHistoricDataWorker.java) | 历史数据重发需要显式触发，使用成功时间点、缓冲区、最多 5 分钟的数据窗口和可重试的发送结果；它不是普通查询重试。 |

### 3.2 对 HVAC 的有效启示

- `ADOPT LATER`：未来 Edge seam 保留 Channel identity、当前值/下一值、UNDEFINED、周期、控制器、调度器、Timedata 和 resend 的语义。
- `ADAPT LATER`：把 OpenEMS 的 Channel/Process Image 语义改写成 HVAC Edge Control Plane 的 intent、manifest、readback、evidence；现阶段只冻结接口，不实现 Edge。
- `REJECT NOW`：把 Java/OSGi 组件模型、Edge Cycle 或 Controller Scheduler 引入当前 Backend；用户已明确本阶段重点是 Backend 和 UI。
- `REJECT`：把 OpenEMS Timedata 当作 MyEMS 的能源结算模型。Timedata 解决历史数据 provider 和边缘数据回传，不解决 MeterBinding、Tariff、Carbon 或 Report ownership。

## 4. MyEMS：能源处理链和能源内容的直接参考

### 4.1 实际源码证据

| 固定提交中的文件 | 源码/Schema 观察 |
| --- | --- |
| [`database/README.md`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/database/README.md) | 官方数据库说明按 system、historical、energy、billing、carbon、energy baseline、energy model、energy plan、energy prediction、FDD、user、reporting、production 组织 13 个逻辑数据库；historical 有原始值/latest 值以及 `is_bad`、`is_published`，energy 以 hourly 为主要粒度并由其派生 daily/month/year，billing 和 carbon 按 tariff/factor 计算。 |
| [`myems-cleaning/main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-cleaning/main.py)、[`clean_energy_value.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-cleaning/clean_energy_value.py) | Cleaning 进程把 analog、digital、energy 分开处理；energy 清洗在 `tbl_energy_value` 上按时间窗口运行，先按 high/low limit 和异常形状识别坏值，再把剩余未检查值标为 good，原始值没有被转换成另一个语义。 |
| [`myems-normalization/main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/main.py)、[`meter.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/meter.py) | Normalization 以独立进程处理 physical meter、offline meter、virtual meter、virtual point 和 data repair；physical meter 从累计读数生成 hourly consumption，并从 system 配置取得 meter/point 和上下限。 |
| [`virtualmeter.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/virtualmeter.py)、[`offlinemeter.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-normalization/offlinemeter.py) | Virtual Meter 使用表达式引用 physical/virtual/offline meter；offline meter 从 Excel 进入 hourly energy 数据，并与 system meter 配置校验。 |
| [`myems-aggregation/main.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/main.py) | Aggregation 按 equipment、combined equipment、meter、offline meter、virtual meter、space、store、tenant、shopfloor 启动大量独立处理进程，同时计算 energy、billing、carbon。 |
| [`meter_billing.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/meter_billing.py)、[`meter_carbon.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/meter_carbon.py)、[`tariff.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-aggregation/tariff.py) | Billing/Carbon 都从 system 配置和 energy hourly 数据读取，按上次处理时间增量计算并写入独立数据库；billing 使用 tariff/time-of-use，carbon 使用 energy category 的 emission factor。 |
| [`myems-api/app.py`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-api/app.py) | Falcon API 直接注册 meters、virtualmeters、spaces、equipments、energycategories、energyitems、tariffs、energyflowdiagrams、reports、billing、carbon、baseline、prediction 等资源/报表路由；API 是中心查询/管理入口。 |
| [`myems-web/src/routes.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-web/src/routes.js) | Web UI 按 Space、Equipment、Meter、Tenant、Store、Shopfloor、Combined Equipment 和 Auxiliary System 组织；每个对象下有 energy、carbon、cost、saving、plan、prediction、comparison 等任务页面。 |
| [`myems-admin/app/services/settings/meter/meter.service.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/services/settings/meter/meter.service.js)、[`meter.controller.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/controllers/settings/meter/meter.controller.js) | Admin UI 对 meter 做 CRUD、导入导出、复制、主表/子表树、category/cost center/energy item 绑定；它是配置和绑定工作区，不是趋势分析页面。 |
| [`category.service.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/services/settings/category/category.service.js)、[`energycategory.controller.js`](https://github.com/MyEMS/myems/blob/be6e6ce8ddeac57afb04bddb9621501fb555cab0/myems-admin/app/controllers/settings/category/energycategory.controller.js) | Energy Category 是独立的管理资源，有独立 CRUD 和变更事件；它不是 Meter 或 Point 的一个展示字段。 |

### 4.2 对 HVAC 的有效启示

- `ADOPT`：把 Raw/Current、Cleaning、Normalization、Aggregation、Reporting 视为不同语义阶段；每阶段应有独立 owner、质量状态、重算边界和查询契约。
- `ADOPT`：Energy Category/Item、Meter/MeterBinding、Virtual Meter/Point、Tariff、Carbon Factor、Baseline、Plan、Billing、FDD、Reporting 作为 Backend 一级内容模块。
- `ADOPT`：Admin 配置/绑定和 Web 能源分析分成不同产品任务；第一阶段可以仍在同一前端仓库或部署单元内，但模块边界不能混淆。
- `ADAPT`：使用 Go module 和现有 ClickHouse/PostgreSQL 逻辑数据集实现，而不是照搬 Python 多进程和 MySQL 表名。
- `ADAPT`：把 MyEMS 的按对象复制处理改成受控的 Energy Processing pipeline；先支持一个纵向 slice，再扩展 meter/space/equipment 等维度。
- `REJECT`：直接复制 13 个物理数据库；这只是 MyEMS 的部署/维护选择，不是目标领域模型。
- `REJECT`：把定时 normalization/aggregation worker 当作秒级控制系统；能源处理和控制闭环必须分开。

## 5. HVAC 当前源码证据

| 本地文件 | 当前观察 |
| --- | --- |
| [`modules/energy/internal/energy/projector.go`](../../modules/energy/internal/energy/projector.go) | 当前只接受 `hvac_meter.energy` 和 `electricity`；从累计电量的 previous/current observations 构造 `energy_interval_facts`，记录 quality/reasons、source offset、observation IDs、watermark 和 numeric dataset revision；负累计值标为 invalid，回退标为 suspect。 |
| [`modules/energy/README.md`](../../modules/energy/README.md) | 已有 `telemetry_history.observations -> analytics.energy_interval_facts` 的投影链，有 current observation anti-join 和 ClickHouse 去重 token；这是一个可复用的事实投影基础。 |
| [`cmd/telemetry-worker/main.go`](../../cmd/telemetry-worker/main.go) | 当前 analytics projection 以 telemetry-runtime-in-process 方式挂载，默认 poll interval 为 500ms；这说明部署形态和逻辑 owner 目前仍有耦合。 |
| [`modules/telemetry/README.md`](../../modules/telemetry/README.md)、[`libs/analyticsmodel/energy.go`](../../libs/analyticsmodel/energy.go) | 当前公开能源查询只覆盖 electricity，支持 hour/day/month、quality policy、timezone、watermark、partial、dataset revision 和 quality summary；明确不覆盖 tariff、cost、baseline、comparison 等能源内容。 |
| [`contracts/ownership/data-ownership.v1.json`](../../contracts/ownership/data-ownership.v1.json) | 当前声明 `analytics-read-model-projector` 写入 energy interval fact，`telemetry-query-service` 持有查询契约；这是本地现状声明，不能替代本次参考源码裁决。 |
| [`contracts/architecture/backend-architecture.v2.json`](../../contracts/architecture/backend-architecture.v2.json) | 当前把 Energy 标记为已对齐，但这次源码核对表明“已有 electricity fact projection”不能证明 MyEMS 级 Energy Content 已完成，应把该状态降为局部实现/待领域裁决。 |

### 5.1 本地与三方的差距

当前 HVAC 已经有一段比通用 telemetry 更明确的能源事实投影，但它仍然是“电量区间查询 slice”，不是完整 Energy Backend：

```text
当前 HVAC：Telemetry observations -> electricity interval facts -> fixed query

MyEMS 能源域：Historical raw/latest -> cleaning -> meter normalization
               -> virtual/offline meter -> energy aggregation
               -> billing/carbon/baseline/plan/prediction/reporting

ThingsBoard：Telemetry/Latest -> Rule Engine/Calculated Field -> Dashboard/Widget

OpenEMS：Edge Channel/Process Image -> Cycle/Controller/Scheduler -> Timedata/Backend
```

结论不是“照搬 MyEMS”，而是：HVAC 必须补足能源内容的 ownership 和语义，才能让现有 electricity fact 成为 Energy Processing 的第一个输入/输出，而不是把它包装成已完成的能源平台。

## 6. 本轮架构裁决

### 6.1 Backend

1. 将 Backend 能源能力拆成两个相互协作但职责不同的层：
   - **Energy Processing**：raw/current 读取、清洗、累计值转区间、质量判断、归一化、汇总、重算和处理运行记录。
   - **Energy Content**：Energy Category/Item、Meter/MeterBinding、Virtual Meter、Space/Asset 归属、Tariff、Carbon Factor、Baseline、Plan、Billing、FDD、Reporting。
2. 现有 `analytics.energy_interval_facts` 保留为第一个可验证的 Energy Processing 输出，但不能继续代表全部 Energy Content。
3. 处理链需要固定为逻辑语义：`Raw -> Current -> Normalized -> Aggregate -> Report`。物理表、schema、进程和数据库数量后续按 ownership 和访问模式裁决。
4. MyEMS 的多数据库结构作为“逻辑数据集分区”参考；当前不接受 13 个物理数据库作为目标约束。
5. ThingsBoard 的规则/事件能力只作为通用编排或实时派生 seam；能源结算、质量和报告不得依赖浏览器 Dashboard 或任意 Rule Chain。
6. OpenEMS 的控制时序和 Edge 数据回传只冻结为未来 seam，不进入当前 Backend 能源处理实现。

### 6.2 UI

1. **Operations**：实时状态、告警、设备/站点运行、历史 telemetry 和实时订阅。
2. **Energy Management**：能耗、计量、成本、碳排、基线、计划、对比、能流和报表；所有页面使用固定的 Energy Content/Processing 查询契约。
3. **Administration**：站点/空间/资产、MeterBinding、Energy Category/Item、Tariff、Carbon Factor、报表配置和权限。
4. 吸收 ThingsBoard 的 Dashboard/Widget/Alias/State 组合能力，但不把自由 Dashboard 变成能源领域的事实来源。
5. 吸收 MyEMS 的 Admin/Web 任务分离，但不复制它的页面数量和对象重复菜单；先围绕第一个 Energy Slice 形成最小闭环。

## 7. ADOPT / ADAPT / REJECT / VERIFY 清单

| 来源机制 | 裁决 | HVAC 处理 |
| --- | --- | --- |
| ThingsBoard Latest 与历史 DAO 分离 | ADOPT | 保持 authoritative Current 与可重建 Latest projection 的边界；后续以能源事实查询补足 domain semantics |
| ThingsBoard Rule Node 的 TTL/时间戳/去重策略 | ADAPT | 仅把明确的处理策略思想用于能源处理任务，不引入完整 Rule Engine |
| ThingsBoard Calculated Field/关系聚合 | ADAPT | 可用于未来派生指标或关系聚合，但 Meter/Space/Flow 关系必须由能源域定义 |
| ThingsBoard Dashboard/Widget/WebSocket | ADAPT | 用于 Operations 和可组合 UI；Energy Management 只消费固定领域查询 |
| OpenEMS Channel/Process Image/UNDEFINED | DEFER/ADAPT | 作为未来 Edge Control Plane 的接口语义；当前 Backend/UI 不实现 |
| OpenEMS Cycle/Controller/Scheduler | DEFER | 未来控制执行时序；与本轮 Backend 能源处理隔离 |
| OpenEMS Timedata/resend | ADAPT LATER | 作为未来 Edge 回传与断线补发 seam；不作为 billing/carbon 数据模型 |
| MyEMS cleaning/normalization/aggregation | ADOPT/ADAPT | 采用职责分层和处理顺序，按 Go/现有存储改写，增加 HVAC 需要的 owner/revision/quality contract |
| MyEMS meter/virtual meter/category/tariff/carbon/baseline/reporting | ADOPT | 进入 Energy Content 规划，按首个纵向 slice 逐步实现 |
| MyEMS 13 个物理数据库 | REJECT | 采用逻辑数据集分区，不复制物理数据库数量 |
| 当前 HVAC 仅 electricity interval fact | KEEP AS SLICE | 作为第一个产品切片，不再把它描述为完整 Energy 模块 |
| 当前 HVAC 的 Energy 已标记 ALIGNED | LOCAL-CHANGE | 需要把“局部 electricity projection”与“完整 Energy Content”分开，状态降级并在后续票据中重新裁决 |

## 8. 证据边界与 VERIFY 项

以下内容本轮没有足够证据，不能写入目标架构作为已证实事实：

- MyEMS v6.7.0 全部生产故障恢复、跨数据库事务和严格幂等行为；本轮只核对了关键处理路径和少量官方测试，不能据此推导生产级一致性。
- MyEMS 全量 admin/web 页面之间的稳定 API 契约；本轮核对了路由、Meter/Category 管理入口和代表性页面，不代表全量 UI 已审查。
- ThingsBoard 全量 Widget 类型、所有队列部署模式和所有多租户授权细节；本轮只取与 Telemetry/Energy/UI 规划直接相关的实现与测试。
- OpenEMS 全量 Edge/Backend JSON-RPC 和 UI 绑定细节；本轮只冻结控制与 Timedata 相关 seam。
- HVAC Energy Content 的 MeterBinding、Tariff、Carbon、Baseline、Billing、FDD、Reporting 领域对象和 owner；必须由后续 #304/#305/#306 继续裁决。

## 9. 本轮变更

- 新增本研究记录。
- 未修改 Backend、UI、数据库、部署配置或测试代码。
- 未运行完整项目测试；本轮是源码取证和架构裁决，不以测试数量替代证据。

## 10. Wayfinder #310 实施规格取证补充

本次为 Energy Slice v1 实施规格再次核对了以下固定提交和本地源码：

| 来源 | 追加核对内容 | 决策 |
| --- | --- | --- |
| HVAC `004-counter-semantics.sql`、`002-analytics-energy-interval.sql`、`analytics-read-model-projector/internal/energy/projector.go`、`internal/clickhouse/client.go`、`telemetry-query-service/internal/history/aggregate.go`、`009a-energy-topology-metering-v2.sql` | 当前 canonical `counter_deltas` 已有 transition/delta 规则，但 Projector 仍自行按 raw observations 配对、只筛 `ACCEPTED`、缺少 MeterBinding 和 Source Position；Fact schema 也缺少 Binding/Point/Counter 快照。 | **LOCAL-CHANGE**：实现时以 canonical view 为唯一 Counter 入口，补齐 Fact provenance 和 event-time Binding resolver；删除旧的负差值写 0 行为。 |
| ThingsBoard 固定提交 `c2a52e46c44e308ddee430e7266b8e10eddde9c4` 的 `dao/.../TimeseriesDao.java`，以及已记录的 `TbMsgTimeseriesNode` 生产源码和官方测试 | Timeseries DAO 提供按 Entity/Key/时间范围的时序存取，Rule Node 负责运行时保存/Latest/WebSocket/去重策略；没有 MeterBinding、Counter transition 或能源质量事实。 | **ADAPT/REJECT**：只吸收时序与运行时边界，不把通用 Telemetry/Rule Engine 当作能源处理核心。 |
| OpenEMS 固定提交 `2e2792d59fc5ba3b99ce3cf98d15081c0a74895e` 的 `Channel.java`、`Value.java`、`CycleWorker.java`、`Timedata.java`、`TimedataManagerImpl.java`、`ResendHistoricDataWorker.java` 及对应 UI/测试 | typed Channel/Value 保留类型、单位和 `UNDEFINED`；Timedata 是历史 provider seam；Cycle/Scheduler/Resend 是 Edge runtime 时序。 | **ADAPT/DEFER**：保留来源 typed/provenance 和未来 Edge seam；当前 Backend 不复制 Edge runtime。 |
| MyEMS 固定提交 `be6e6ce8ddeac57afb04bddb9621501fb555cab0` 的 `clean_energy_value.py`、`meter.py`、`myems-aggregation/main.py`、`meter_billing.py`、database schema/README | Cleaning 先标记 bad/good；Normalization 从累计值按窗口生成 hourly consumption；Aggregation 按对象并行处理 energy/billing/carbon；Billing 读取 energy hourly 与 tariff 增量计算。 | **ADOPT/ADAPT/REJECT**：吸收职责顺序和处理边界，改写成 HVAC 的 canonical Counter/Fact/Query；不复制 Python worker、13 个物理数据库或隐式补 0。 |

本补充已落实到 [`energy-slice-implementation-spec-v1.md`](../architecture/energy-slice-implementation-spec-v1.md)；没有因为参考项目存在实现就扩大首个 Slice 的范围。
