# 优化建议页接入影子求解器与可写设定点门控源码比对（2026-11-11）

调研问题：`/energy-analysis/optimization`（优化建议页）准备做渐进披露升级——L1 给值班人员一眼看清当前优化建议、前置与安全条件是否通过、该走可写设定点下发还是建人工调温工单；L3 给能源工程师查看影子求解器（冷机 COP 模型）的拟合系数、CV(RMSE)、三档供水温度方案的耗电变化、P90 不确定度与未来 48 小时负荷预测。在定稿规格前，核对两件事：

1. 本仓库 `services/optimization-service`、`services/forecast-service`、`cmd/energy-api`、`modules/command`、`libs/workordermodel` 和 `apps/hvac-web` 里，哪些数据已经落库或已通过网关接口暴露，哪些还缺只读接口或前端联动。
2. OpenEMS 与 ThingsBoard 固定版本源码里，怎么把「优化模拟/派生模型评估」和「可执行设定点下发」隔离开，避免值班人员把模拟数字当成已生效的控制动作，也避免在没有可写点位的站点摆一个永远点不了的下发按钮。

本记录支撑规格 `docs/specs/chiller-cop-model.md`、`docs/specs/scheduled-optimization.md` 及优化建议页升级规格。

| 项目 | 固定版本 / 提交 | 仓库 | 许可证 |
|---|---|---|---|
| OpenEMS | `2026.9.0` (`14f0ded`) | https://github.com/OpenEMS/openems | EPL-2.0 |
| ThingsBoard | `v4.4` (`6d46786579c8b29caf5102f95ddb133674bed68b`) | https://github.com/thingsboard/thingsboard | Apache-2.0 |

## 读到的做法

### OpenEMS `2026.9.0`（`14f0ded`）

1. **优化模拟评估与控制器设定点执行分成两条独立链路。**
   - `EnergyScheduleHandler.WithDifferentModes`（`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/EnergyScheduleHandler.java:74-157`）把单个控制器的可调度模式 `modes()`（`:97`）、单周期能量流模拟 `simulate(...)`（`:131-132`）、适应度打分 `evaluate(...)`（`:145-146`）与排程写入 `applySchedule(...)`（`:156`）拆成独立方法。
   - `Optimizer`（`io.openems.edge.energy/src/io/openems/edge/energy/optimizer/Optimizer.java:247-270,311-347`）在独立单线程执行器里跑模拟，只产出不可变的 `SimulationResult`（`io.openems.edge.energy/src/io/openems/edge/energy/optimizer/SimulationResult.java:37-46`），内含按优先序排列的 `Fitness`（硬约束违规次数 `hardConstraintViolations` 最优先，其次购电成本 `gridBuyCostScore`、购电量 `gridBuyEnergyWh`、软约束违规次数 `softConstraintViolations`；`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/Fitness.java:8-18,44-53`）以及模拟轮数 `simulationsCounter` / 代数 `generationsCounter`。模拟过程本身绝不写任何硬件通道。
   - `DifferentModes.Modes`（`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/DifferentModes.java:181-203,268-272`）在每个模式上显式带 `addToOptimizer` 布尔位，`streamForOptimizer()` 只把 `addToOptimizer == true` 的模式交给优化器搜索，其余模式只留给人工或兜底逻辑。

2. **前端时间轴走只读 `getSchedule` 查询，把过去实测和未来预测拼成同一张图，不触发重算或控制。**
   - `EnergySchedulerImpl.buildJsonApiRoutes`（`io.openems.edge.energy/src/io/openems/edge/energy/EnergySchedulerImpl.java:246-258`）注册只读 JSON-RPC `getSchedule`，直接读取内存里的 `optimizer.getLatestSimulationResult()`（没有结果时返回 `EMPTY_SIMULATION_RESULT`，不报错也不阻塞控制回路）。
   - `GetSchedule.Response.from(...)`（`io.openems.edge.energy/src/io/openems/edge/energy/GetSchedule.java:160-189,206-257,347-437`）以当前时刻为界，前半段用 `Entry.Type.HISTORY`（5 分钟粒度历史读数），后半段用 `Entry.Type.PREDICTION`（15 分钟粒度，把 `TariffManager` 电价、`PredictorManager` 负荷预测与 `SimulationResult` 算出的各时段模式拼在一起）；前端图表（如 `ui/src/app/edge/live/Controller/Ess/GridOptimizedCharge/shared/prediction-chart.ts:154-268`）把实测曲线和预期曲线画成两条独立序列。

3. **控制器真正写设定点前，按实时读数二次核对并支持手动/自动/关闭模式切换。**
   - `TimeOfUseTariffControllerImpl.run()`（`io.openems.edge.controller.ess.timeofusetariff/src/io/openems/edge/controller/ess/timeofusetariff/TimeOfUseTariffControllerImpl.java:205-276`）每个控制周期调 `Utils.calculateApplyMode(...)`（`io.openems.edge.controller.ess.timeofusetariff/src/io/openems/edge/controller/ess/timeofusetariff/Utils.java:68-100,102-134,194-211`）：只要电网功率、储能功率、光伏功率任一实时读数为 `null` 或当前没有排程周期（`:75-77`），立刻退回 `ApplyMode(BALANCING, null)`，不发有效设定点；即便排程建议 `CHARGE_GRID`，若实时 SoC 已达上限（`soc >= coc.maxSocInChargeGrid()`，`:127-131`）或触发电网软上限削峰（`:109-113,198-203`），也会当场改写成削峰或平衡设定点，最后才调 `ess.setActivePowerEqualsWithFilter(am.setPoint())`（`TimeOfUseTariffControllerImpl.java:275`）。

### ThingsBoard `v4.4`（`6d46786579c8b29caf5102f95ddb133674bed68b`）

1. **计算字段（派生模型）与设备 RPC 从输出类型上硬隔离，缺输入时给结构化未就绪原因。**
   - `CalculatedFieldType`（`common/data/src/main/java/org/thingsboard/server/common/data/cf/CalculatedFieldType.java:11-20`）和 `OutputType`（`common/data/src/main/java/org/thingsboard/server/common/data/cf/configuration/OutputType.java:5-8`）把计算字段的输出严格限定在 `TIME_SERIES` 或 `ATTRIBUTES`，不提供直接发设备 RPC 的输出类型。
   - 输入点位未齐备时，`CalculatedFieldState.ReadinessStatus`（`application/src/main/java/org/thingsboard/server/service/cf/ctx/state/CalculatedFieldState.java:94-121`）返回 `ready = false` 和具体缺失的参数列表（`Required arguments are missing: ...`）；`CalculatedFieldEntityMessageProcessor.processStateIfReady(...)`（`application/src/main/java/org/thingsboard/server/actors/calculatedField/CalculatedFieldEntityMessageProcessor.java:506-527`）在 `!state.isReady()` 时只写调试事件，绝不拼凑默认读数产出假结果。

2. **前端控制部件按 `widgetType.rpc` 能力门控，设定点写操作与后端 RPC 状态机闭环绑定。**
   - `action-widget-settings.models.ts`（`ui-ngx/src/app/shared/models/action-widget-settings.models.ts:20-26,89-103`）在 `type !== widgetType.rpc` 时直接从动作枚举里过滤掉 `EXECUTE_RPC`，非控制类组件不暴露下发入口。
   - 数值步进下发组件 `ValueStepperWidgetComponent`（`ui-ngx/src/app/modules/home/components/widget/lib/rpc/value-stepper-widget.component.ts:159-193,222-259`）同时订阅 `loading$`、远端布尔禁用点位 `disabledState$` 和目标值越界（`value ± valueStep` 超出 `[minValueRange, maxValueRange]`），任一命中即禁用步进按钮；RPC 返回错误时立刻回退到 `prevValue`（`:254-257`）。后端 `RpcStatus`（`common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java:14-24,61-76`）独立维护 `QUEUED -> SENT -> DELIVERED -> SUCCESSFUL / FAILED / TIMEOUT / EXPIRED` 的受保护状态跃迁，终态不可被乱序消息覆盖。

## 本仓库现状与缺口

### 1. L1（当前建议、前置与安全条件、可写设定点 vs 人工工单兜底）数据盘点

| L1 页面要素 | 现状（已暴露 / 缺口） | 代码位置 |
|---|---|---|
| 当前发布的优化建议（基准供水温度/室温/日耗电/日电费、`candidate` 供水温度与日费用、预期日省电/省钱、P90 不确定度、舒适度余量、风险等级、回退方案、30 分钟核验计划、人工审批状态 `DRAFT`） | **已在 `energy-api` 暴露**。`GET /api/v1/sites/{siteId}/optimization/recommendations/latest` 返回 `PublishedRecommendation`（含 `runId`、`trigger`、`runStatus`、`quality`、`recommendation`、`responseModel`）。无已发布建议时返回 `404`。 | `cmd/energy-api/internal/gateway/intelligence.go:140-142,198-200,448-468`<br>`services/optimization-service/internal/optimization/http.go:49,108-127`<br>`services/optimization-service/internal/optimization/postgres.go:17-27,139-174`<br>`services/optimization-service/internal/optimization/model.go:63-135` |
| 最近一次定时准备结果与阻断原因（14 个 `PreparationReason` + `PREPARATION_FAILED`） | **已在 `energy-api` 暴露**。`GET /api/v1/sites/{siteId}/optimization/preparations/latest` 返回 `{ trigger: "SCHEDULE", status: "PREPARED" \| "FAILED" \| "PENDING", reason, firedAt, completedAt }`；站点从未触发过定时任务时返回 `404`。 | `cmd/energy-api/internal/gateway/intelligence.go:143-145,202-204`<br>`services/optimization-service/internal/optimization/http.go:50,129-148`<br>`services/optimization-service/internal/optimization/preparation_reason.go:13-28`<br>`apps/hvac-web/src/features/optimization/optimization-model.ts:104-125` |
| 手动触发一次优化求解 | **已在 `energy-api` 暴露**。`POST /api/v1/optimization/runs`（网关调 `Prepare` 组装快照后转发 `POST /v1/optimize`），前端 `OptimizationWorkspace.tsx` 已接线并轮询新 `runId`。 | `cmd/energy-api/internal/gateway/intelligence.go:146-148,470-509`<br>`apps/hvac-web/src/features/optimization/components/OptimizationWorkspace.tsx:136-156` |
| 可写冷冻水供水温度设定点门控（`COMMAND` 点位 + `writable: true` + `CONTROLS` 关系 + `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` 能力 + `ACTIVE` 反馈点位） | **已在 `energy-api` 暴露且前后端规则一致**。前端已通过 `GET /api/v1/sites/{siteId}/asset-model`（`useRealtimePlant()`）调用 `controlTargets(...)` + `supplySetpoint(...)` 查找可写设定点；后端 `POST /api/v1/commands` 校验同一套注册表条件、温度范围 `5..12 ℃`、步长 `0.5 ℃`、单次最大调整幅度 `3.0 ℃`（`≤ 1.0 ℃` 免审直发，`≤ 2.0 ℃` 单人审批，`≤ 3.0 ℃` 双人审批）。 | `apps/hvac-web/src/features/optimization/components/OptimizationWorkspace.tsx:95-101,157-176`<br>`apps/hvac-web/src/features/control/control-model.ts:60-101`<br>`cmd/energy-api/internal/gateway/command.go:263-324,648-708`<br>`libs/commandmodel/model.go:85`<br>`modules/command/pkg/commandservice/governance.go:19-55,84-95` |
| 已登记可写设定点时的控制指令预览与审批跳转 | **已有页内确认弹窗与下发后跳转，`/operations/control` 尚无草稿预填参数**。当前点击「按建议下发」会打开与设备控制页共用的 `<ConfirmCommand>` 弹窗，提交 `POST /api/v1/commands` 后在页内展示状态并给出 `/operations/control?site=<siteId>&inspect=<commandId>` 链接；`/operations/control` 路由 search schema 目前只收 `{ status?, inspect? }`。 | `apps/hvac-web/src/features/optimization/components/OptimizationWorkspace.tsx:170-175,247-252,307-330`<br>`apps/hvac-web/src/features/control/components/CommandConfirmation.tsx:27-97`<br>`apps/hvac-web/src/routes/_app._site.operations.control.tsx:6-9` |
| 未登记可写设定点时的人工工单兜底 | **后端与通用弹窗已就绪，优化建议页还没接线（前端缺口）**。当前 `setting === null` 时只渲染灰掉的 `<Button disabled>按建议下发</Button>` 和小字提示（`OptimizationWorkspace.tsx:270-271,323-330`），没有像诊断页那样接 `<CreateWorkOrderDialog>` 调 `POST /api/v1/sites/{siteId}/work-orders`，也没有查询是否已为该条建议建过工单。 | `apps/hvac-web/src/features/optimization/components/OptimizationWorkspace.tsx:270-271,323-330`<br>`apps/hvac-web/src/features/work-orders/CreateWorkOrderDialog.tsx:27-96`<br>`cmd/energy-api/internal/gateway/work_order_mutation.go:20-29,179-201`<br>`libs/workordermodel/model.go:58-80,468-494`<br>`apps/hvac-web/e2e/gateway.ts:464-484` |

**前置与安全条件核对（避免前端编造未测条件）：**

读 `services/optimization-service/internal/optimization/preparation.go:75-190,227-307` 与 `model.go:81-95` 可以确认，后端实际检查并留痕的条件只有以下六类，**并没有**在优化准备阶段单独查「冷机故障告警计数」或「喘振/最低负荷率裕度」：

1. **供水温度策略边界（`SUPPLY_TEMP`）**：基准供水温度与建议供水温度都在 `[minC, maxC]` 内，且单步变化不超过 `maxStepC`（越界时报 `BASELINE_OUTSIDE_LIMITS`）。
2. **室内温度舒适度边界（`ZONE_COMFORT`）**：基准室温在 `[minC, maxC]` 内，且叠加 `zoneTempP90C` 后的预计室温仍不越界（余量记在 `risk.comfortMarginC`）。
3. **检修与手动锁定门控（`MAINTENANCE_ACTIVE`）**：`maintenanceConstraints.outOfService` 与 `manualLocks.resources` 均为空时才允许生成建议（`preparation.go:227-229`）。
4. **关键读数在线与新鲜度（`CURRENT_STATE_REVALIDATION` / `SUPPLY_TEMPERATURE_MISSING` / `ZONE_TEMPERATURE_MISSING`）**：冷冻水供水温度与室内温度点位均满足 `AVAILABLE`、`ONLINE`、`CURRENT`、`FRESH`、`GOOD`（`preparation.go:299-307`），且真正下发控制指令时由 `command-service` 再次核对实时状态（`modules/command/pkg/commandservice/service.go:436-447`）。
5. **负荷预测与分时电价齐备（`LOAD_FORECAST` & `TARIFF`）**：存在覆盖未来一个完整计价自然日（96 个 15 分钟点）的有效负荷预测和生效中的分时电价（否则报 `LOAD_FORECAST_MISSING`、`MODEL_DEPLOYMENT_MISSING`、`PRICED_DAY_MISSING` 或 `TARIFF_MISSING`）。
6. **执行通道状态（`SET_CHILLED_WATER_TEMPERATURE_SETPOINT`）**：站点资产模型是否已登记 `ACTIVE` 可写供水温度设定点点位——决定走「控制指令审批下发」还是「人工调温工单」。

**松雅轩真实站点现状（`deploy/sites/songyaxuan.site.json`）：**

- A–D 四台冷机的有功功率、蒸发器进出水温度、冷凝器进出水温度，总管流量/冷量计与室外干球温度点位均已登记为 `ACTIVE`（`songyaxuan.site.json:215-520,681-760,829-848`），满足 `CHILLER_COP_MODEL` 拟合所需的采集点位。
- `ET1010 控制模块（UID 31）` 的 `u11`–`u44` 全部登记为只读 `TELEMETRY`（`songyaxuan.site.json:522-642`），全站没有任何 `pointType: "COMMAND"` 或 `writable: true` 点位。
- `房间温度`（`sourceKey: "zone.temperature"`）因现场未装室内探头处于 `INACTIVE`（`songyaxuan.site.json:876-894`），`intelligence` 段也暂未登记 `optimization` 策略与定时任务（`songyaxuan.site.json:1078-1094`）。因此在松雅轩当前线上环境中，`recommendations/latest` 与 `preparations/latest` 均返回 `404`；即便将来补齐室温探头并跑通优化求解，因没有可写设定点，L1 也必然走「人工调温工单」路径。

### 2. L3（冷机 COP 模型评估、三档方案对比、未来 48 小时负荷预测）数据盘点

| L3 页面要素 | 存储位置与计算现状 | `energy-api` 暴露现状 | 代码位置 |
|---|---|---|---|
| 未来 48 小时站点负荷预测（15 分钟点序列、上下界 `lower_bound` / `upper_bound`、质量 `VALID` / `FALLBACK`、按站点本地整点聚合） | **已落库并每小时发布**（PostgreSQL 预测快照表）。 | **已在 `energy-api` 暴露**。`GET /api/v1/sites/{siteId}/forecast/load`，前端 `apps/hvac-web/src/features/load/query.ts` 已有现成的 `siteLoadForecastQuery(siteId)` 和 `hourlyForecast(...)` 可直接复用。 | `cmd/energy-api/internal/gateway/intelligence.go:122-124,247-267`<br>`services/forecast-service/internal/forecast/service.go:204-227`<br>`apps/hvac-web/src/features/load/query.ts:52-68`<br>`apps/hvac-web/src/features/load/forecast-model.ts:34-92` |
| 策略线性响应模型（当前正式发布求解器使用的固定斜率与固定 P90） | 随每次发布的建议落入 PostgreSQL `core_registry.optimization_recommendations.recommendation_json`。 | **已在 `energy-api` 暴露**（`recommendations/latest` 的 `responseModel` 字段）。 | `services/optimization-service/internal/optimization/postgres.go:17-27,163-168` |
| 影子求解器总体结论（`solver: "CHILLER_COP_MODEL"`、`mode: "SHADOW"`、`outcome`: `FEASIBLE` \| `INFEASIBLE` \| `NO_USABLE_MODEL` \| `HISTORY_UNAVAILABLE` \| `INVALID_REQUEST`、`reason`、`publishedRecommendationId`、`generatedAt`） | **已实现并写入 ClickHouse** `analytics.optimization_evaluations`（`quality = 'SHADOW'`，`optimization_run_id = published.runId`，`recommendation_id = published.ID`）。 | **还没暴露（后端只写不读）**。`ClickHouseInputReader` 目前只有 `InsertEvaluation` 和测试用的 `HasEvaluation`，`optimization-service` 与 `energy-api` 均无读取路由。 | `services/optimization-service/internal/optimization/service.go:149-184,245`<br>`services/optimization-service/internal/optimization/cop_model.go:399-466`<br>`services/optimization-service/internal/optimization/clickhouse.go:17-20,72-101`<br>`infra/telemetry/clickhouse/init/007-optimization-evaluations.sql:1-31` |
| 每台冷机 COP 模型拟合详情（`deviceId`、`status`: `FITTED` \| `INSUFFICIENT_DATA` \| `REJECTED`、`reason`、15 分钟样本数 `samples`、P99 参考制冷量 `capacityReference`、回归系数 `intercept` / `loadRatio` / `loadRatioSquared` / `chilledWaterSupplyTempC` / 可选 `condenserReturnTempC`、`cvRmsePower`、`cvRmseCOP`、`meanCOP`、历史出水温度覆盖区间 `chilledWaterSupplyRangeC: [low, high]`）与全站 `fit.cvRmsePower`、14 天样本筛除统计 `fit.history` | **已实现并写入同一条 ClickHouse `SHADOW` 记录的 `evaluation_json.fit`**。 | **还没暴露**。注：`fit.chillers[].deviceId` 可在前端直接通过已加载的 `SiteAssetModel`（`devices` / `telemetryPoints` / `relationships`）映射回冷机资产名称（如 `A 机组`），无需后端跨库拼表。 | `services/optimization-service/internal/optimization/cop_model.go:72-117,121-244`<br>`apps/hvac-web/src/api/generated/platformGateway.gen.ts:318-329` |
| 拟合能耗斜率与参考日冷机耗电（`energyResponse`: `dailyEnergyDeltaKWhPerSupplyTempC`、`referenceChillerEnergyKWh`、`referenceSamples`、`replacesPolicyValue`） | **已实现并写入 `evaluation_json.energyResponse`**。 | **还没暴露**。 | `services/optimization-service/internal/optimization/cop_model.go:63-70,355-397,480-484` |
| 三档供水温度方案逐档对比（`candidates[]`：`-maxStepC` / `0` / `+maxStepC` 三档的 `supplyTempC`、`projectedZoneTempC`、`chillerEnergyDeltaKWh`、`dailyEnergyKWh`、`dailyCost`、是否超出历史训练区间 `extrapolated`、`feasible`、淘汰原因 `SUPPLY_TEMP` \| `ZONE_COMFORT`）与影子建议 P90 不确定度（`1.28155 × cvRmsePower × referenceChillerEnergyKWh`） | **已实现并写入 `evaluation_json.candidates` 与 `evaluation_json.recommendation`**。 | **还没暴露**。 | `services/optimization-service/internal/optimization/cop_model.go:89-99,467-550` |

**需要特别留意的两个事实：**

1. **无需新增数据库迁移。** 冻结迁移 `infra/telemetry/clickhouse/init/007-optimization-evaluations.sql:24,31` 已经把表排序键建成 `ORDER BY (tenant_id, site_id, optimization_run_id, evaluation_id)`，并且已经执行过 `GRANT SELECT ON analytics.optimization_evaluations TO optimization_service_reader`。`optimization-service` 用现有的只读 ClickHouse 连接就能按 `(tenant_id, site_id, optimization_run_id)` 前缀直接命中索引。
2. **影子求解只在 `Optimize(...)` 运行时触发（`service.go:245`）。** 如果一个站点连 `Prepare(...)` 都没跑通（例如松雅轩当前缺室内温度探头且未登记优化策略），ClickHouse 里不会有该站点的 `SHADOW` 记录。L3 必须把「尚无影子评估记录（`404`）」和「已有影子评估但样本不足/拟合被拒（`NO_USABLE_MODEL` / `INSUFFICIENT_DATA` / `REJECTED`）」区分开展示。

## 建议采纳与取舍

### 1. 影子求解器只读网关契约：新增独立 `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`，不往 `recommendations/latest` 里塞

- **采纳**：在 `optimization-service` 新增只读接口 `GET /v1/sites/{siteId}/optimization/shadow-evaluations/latest`，由 `energy-api` 暴露为 `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`（登记进 `contracts/http/platform-gateway.openapi.yaml` 与 `contracts/ownership/route-ownership.v1.json`，owner 为 `optimization-service`，权限与 `recommendations/latest` 一致，走站点分析读权限）。
  - 查询逻辑：先从 PostgreSQL 查该站点最新已发布建议的 `runId`（若无已发布建议直接返回 `404`），再用 `optimization_service_reader` 查 ClickHouse `analytics.optimization_evaluations WHERE tenant_id = ? AND site_id = ? AND optimization_run_id = ? AND quality = 'SHADOW' ORDER BY generated_at DESC LIMIT 1`；若该次运行未开启或未写出影子评估，返回 `404`。
  - 返回体：原样返回结构化的影子评估摘要 `{ evaluationId, runId, publishedRecommendationId, generatedAt, solver, mode: "SHADOW", outcome, reason?, cvRmse?, fit?, condenserReset?, energyResponse?, zoneResponse?, candidates?, recommendation? }`（直接对应 `cop_model.go:404-416` 的 `ShadowEvaluation` 加表头元数据），其中影子 `recommendation` 仅供 L3 与已发布建议对照，前端绝不拿它当可执行设定点。
- **拒绝**：把 `shadowEvaluation` 嵌进 `GET /api/v1/sites/{siteId}/optimization/recommendations/latest`。
  - 原因：第一，`docs/specs/chiller-cop-model.md` 明确规定影子评估只做记录、绝不影响正式发布的建议；如果把 ClickHouse 查询绑进 `recommendations/latest`，ClickHouse 抖动就会拖慢甚至拖垮 L1 的正式建议卡片与控制下发入口。第二，L1 首屏完全不需要拉取几千字节的冷机回归系数和样本筛除明细，独立接口天然契合 L1 / L3 按需加载。

### 2. L1 单行安全与室温条件 + L3 条件明细：严格映射后端真实检查的 6 项，不编造现场未测指标

- **采纳**：借鉴 ThingsBoard `CalculatedFieldState.ReadinessStatus` 与 OpenEMS 二次校验做法，L1 底部单行状态摘要与 L3 展开明细只展示后端真实产出的 6 项门控，并按 `recommendations/latest` + `preparations/latest` + `asset-model` 实时组合：
  1. **供水温度范围**（来自 `recommendation.constraints` 中 `SUPPLY_TEMP` 的 `[minC, maxC]` 与 `candidate.supplyTempC`；准备失败时对应 `SUPPLY_TEMPERATURE_MISSING` / `BASELINE_OUTSIDE_LIMITS`）。
  2. **室内舒适度范围**（来自 `ZONE_COMFORT` 的 `[minC, maxC]`、`projectedZoneTempC ± zoneTempP90C` 与 `risk.comfortMarginC`；准备失败时对应 `ZONE_TEMPERATURE_MISSING` / `BASELINE_OUTSIDE_LIMITS`）。
  3. **检修与手动锁定**（准备通过代表捕获时刻无检修停运与手动锁定；准备失败且 `reason === 'MAINTENANCE_ACTIVE'` 时直接标红说明存在检修或手动锁）。
  4. **实时读数核验**（来自 `CURRENT_STATE_REVALIDATION`；提示下发瞬间仍由 `command-service` 复核设备在线、读数新鲜且质量 `GOOD`）。
  5. **负荷预测与分时电价**（准备通过代表未来整日 96 点预测与分时电价齐备；准备失败时精确翻译 `LOAD_FORECAST_MISSING` / `MODEL_DEPLOYMENT_MISSING` / `PRICED_DAY_MISSING` / `TARIFF_MISSING`）。
  6. **执行方式就绪度**（按 `asset-model` 是否存在 `ACTIVE` 的 `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` 可写点位，明确显示「可远程下发设定点」或「仅人工调温工单」）。
- **拒绝**：在 L1 或 L3 虚构「冷机无故障告警」「防喘振裕度正常」「冷冻水流量达标」等 `optimization-service` 并未检查的绿勾。没测过的条件标绿勾会误导值班人员放松现场核查。

### 3. 可写设定点下发 vs 人工调温工单的衔接契约

- **已登记可写供水温度设定点（`supplySetpoint !== null`）**：
  - **采纳**：保留并收口现有的 `<ConfirmCommand>` 页内确认弹窗（与 `/operations/control` 共用同一组件和同一套 `POST /api/v1/commands` 校验：`{ siteId, deviceId: setting.deviceId, pointId: setting.pointId, capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', parameters: { setpointC } }`，按 `step = 0.5 ℃` 对齐）；提交成功后立即展示控制指令状态（`|ΔT| ≤ 1.0 ℃` 自动进入派发与 2 分钟回读核验；`1.0 ℃ < |ΔT| ≤ 2.0 ℃` 进入 `AWAITING_APPROVAL` 单人审批；`2.0 ℃ < |ΔT| ≤ 3.0 ℃` 双人审批），并提供一键跳转 `/operations/control?site=<siteId>&inspect=<commandId>` 完成审批或追踪回读证据。无需给 `/operations/control` 增加易失的 URL 草稿参数。
- **未登记可写供水温度设定点（`supplySetpoint === null`，如松雅轩）**：
  - **采纳**：学习 ThingsBoard 非 RPC 组件隐藏 `EXECUTE_RPC` 动作的做法，**去掉永远禁用的「按建议下发」主按钮**，改为主操作按钮「创建人工调温工单」（按 `principal.authorization.capabilities.includes('work-order.create')` 校验权限），直接复用 `<CreateWorkOrderDialog>` 调用 `POST /api/v1/sites/{siteId}/work-orders`：
    - `sourceReferences`：传单条 `ORIGIN` 引用 `[{ domain: 'EXTERNAL', resourceId: 'optimization-recommendation:' + recommendation.recommendation.id, relationship: 'ORIGIN' }]`。之所以选 `EXTERNAL` 而不是 `ASSET`，是因为 `Recommendation` 是冷站总供水温度建议、本身不含单个 `targetAssetId` 字段，且 `libs/workordermodel/model.go:478-480` 对 `EXTERNAL` 允许任意 `1..512` 字符标识，同时天然支持通过 `GET /api/v1/sites/{siteId}/work-orders?sourceDomain=EXTERNAL&sourceRef=optimization-recommendation:<id>`（后端与 `apps/hvac-web/e2e/gateway.ts:469-470` 均已实现）反查该条建议是否已建过工单。
    - 预填内容：标题预填为 `人工调整冷冻水供水温度至 <candidate.supplyTempC> ℃`，优先级默认 `MEDIUM`，说明自动填入基准供水温度、建议供水温度、预计日省电量/省电费、预计室温与舒适度余量，以及回退供水温度（`rollbackPlan.restoreSupplyTempC`）。
    - 去重与追踪：若反查发现该 `recommendation.id` 已有未结束工单（`OPEN` / `IN_PROGRESS` / `BLOCKED`）或刚创建成功，L1 直接显示工单状态徽章与跳转链接 `/operations/work-center?site=<siteId>&inspect=<workOrderId>`，与控制指令的追踪体验完全对称。
