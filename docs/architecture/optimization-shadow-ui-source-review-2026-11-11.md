# 优化建议工作台接入影子求解器、安全条件与人工调温工单的参照核对（2026-11-11）

值班人员打开 `/energy-analysis/optimization`（优化建议页）时，需要先看清当前发布的优化建议该怎么调冷冻水供水温度、前置与安全条件有没有通过、现场是走远程控制指令审批还是建人工调温工单；排查能效模型的人则需要把策略人工设定的响应系数、影子求解器（冷机 COP 模型）从历史读数拟合出来的系数、三档候选供水温度和未来 24 小时负荷预测放在一起对照。本文核对两个外部开源项目固定版本的一手源码与本仓库实现（对应 `#773`–`#776`，见 `docs/specs/optimization-workspace.md`），记录各项规则的采纳、改造与拒绝决定。

| 项目 | 固定版本 | 提交 | 许可证与借鉴边界 |
| --- | --- | --- | --- |
| OpenEMS | `2026.9.0` | `14f0dedf5fe279845281bafaf0e48ea0ab51333a` | EPL-2.0，只核对模拟评估与设定点执行分离、只读时间轴查询及下发前二次核对，不搬代码 |
| ThingsBoard | `v4.4` | `6d46786579c8b29caf5102f95ddb133674bed68b` | 服务端主干 Apache-2.0；前端部件只核对 RPC 能力门控与缺输入就绪状态，不搬代码 |

---

## 读到的做法

### 1. OpenEMS `2026.9.0`（`14f0ded`）

1. **优化模拟评估与控制器设定点执行分成两条独立链路。**
   - `EnergyScheduleHandler.WithDifferentModes`（`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/EnergyScheduleHandler.java:74-157`）把可调度模式 `modes()`（`:97`）、单周期模拟 `simulate(...)`（`:131-132`）、打分 `evaluate(...)`（`:145-146`）与写入 `applySchedule(...)`（`:156`）拆成独立方法。
   - `Optimizer`（`io.openems.edge.energy/src/io/openems/edge/energy/optimizer/Optimizer.java:247-270,311-347`）在独立线程里跑模拟，只产出不可变的 `SimulationResult`（`io.openems.edge.energy/src/io/openems/edge/energy/optimizer/SimulationResult.java:37-46`），内含硬约束违规次数排在最前的 `Fitness`（`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/Fitness.java:8-18,44-53`）。模拟过程本身绝不写任何设备通道。
   - `DifferentModes.Modes`（`io.openems.edge.energy.api/src/io/openems/edge/energy/api/handler/DifferentModes.java:181-203,268-272`）在每个模式上显式带 `addToOptimizer` 布尔位，只有 `addToOptimizer == true` 的模式才参与优化搜索，其余模式留给人工或兜底逻辑。
2. **前端时间轴走只读查询，不触发重算或控制。**
   - `EnergySchedulerImpl.buildJsonApiRoutes`（`io.openems.edge.energy/src/io/openems/edge/energy/EnergySchedulerImpl.java:246-258`）注册只读 `getSchedule`，直接读取 `optimizer.getLatestSimulationResult()`，没有结果时返回 `EMPTY_SIMULATION_RESULT`，不报错也不阻塞控制回路。
   - `GetSchedule.Response.from(...)`（`io.openems.edge.energy/src/io/openems/edge/energy/GetSchedule.java:160-189,206-257,347-437`）把分时电价、负荷预测和模拟算出的各时段方案拼成只读序列供前端画图（`ui/src/app/edge/live/Controller/Ess/GridOptimizedCharge/shared/prediction-chart.ts:154-268`）。
3. **真正写设定点前，按实时读数二次核对。**
   - `TimeOfUseTariffControllerImpl.run()`（`io.openems.edge.controller.ess.timeofusetariff/src/io/openems/edge/controller/ess/timeofusetariff/TimeOfUseTariffControllerImpl.java:205-276`）每次下发前调 `Utils.calculateApplyMode(...)`（`Utils.java:68-100,102-134,194-211`）：只要关键实时读数缺失或越过安全上限，立刻退回平衡或安全档位，最后才向设备写入设定点（`TimeOfUseTariffControllerImpl.java:275`）。

### 2. ThingsBoard `v4.4`（`6d46786579c8b29caf5102f95ddb133674bed68b`）

1. **派生计算模型与设备下发从输出类型上硬隔离，缺输入时给出具体未就绪原因。**
   - `CalculatedFieldType`（`common/data/src/main/java/org/thingsboard/server/common/data/cf/CalculatedFieldType.java:11-20`）和 `OutputType`（`common/data/src/main/java/org/thingsboard/server/common/data/cf/configuration/OutputType.java:5-8`）把计算输出严格限定在时间序列或属性，不提供直接下发设备指令的输出类型。
   - 输入点位未齐备时，`CalculatedFieldState.ReadinessStatus`（`application/src/main/java/org/thingsboard/server/service/cf/ctx/state/CalculatedFieldState.java:94-121`）返回 `ready = false` 和具体缺失项；`CalculatedFieldEntityMessageProcessor.processStateIfReady(...)`（`application/src/main/java/org/thingsboard/server/actors/calculatedField/CalculatedFieldEntityMessageProcessor.java:506-527`）在未就绪时绝不拼凑默认值产出假结果。
2. **前端控制部件按设备可写能力门控，设定点写操作与状态机绑定。**
   - `action-widget-settings.models.ts`（`ui-ngx/src/app/shared/models/action-widget-settings.models.ts:20-26,89-103`）对非控制类部件直接隐去 `EXECUTE_RPC` 动作，不摆永远点不了的下发入口。
   - `ValueStepperWidgetComponent`（`ui-ngx/src/app/modules/home/components/widget/lib/rpc/value-stepper-widget.component.ts:159-193,222-259`）同时校验步长与上下限，后端 `RpcStatus`（`common/data/src/main/java/org/thingsboard/server/common/data/rpc/RpcStatus.java:14-24,61-76`）独立维护下发与终态跃迁。

---

## 本仓库现状（已实现）

### 1. 影子求解器只读接口与执行边界隔离

- **数据库零新增迁移**：ClickHouse 冻结表 `infra/telemetry/clickhouse/init/007-optimization-evaluations.sql` 排序键已为 `(tenant_id, site_id, optimization_run_id, evaluation_id)`，并已把 `SELECT` 授权给 `optimization_service_reader`。
- **独立只读网关接口**：`optimization-service` 暴露 `GET /v1/sites/{siteId}/optimization/shadow-evaluations/latest`（`services/optimization-service/internal/optimization/http.go`），经 `energy-api` 公开为 `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`（`contracts/http/platform-gateway.openapi.yaml`、`contracts/ownership/route-ownership.v1.json`，负责方 `optimization-service`，校验站点分析读权限）。
- **剥离可执行字段**：`Service.LatestShadowEvaluation`（`services/optimization-service/internal/optimization/service.go`）先读 PostgreSQL 最新已发布优化建议的 `runId`，再按 `(tenant_id, site_id, optimization_run_id, quality = 'SHADOW')` 查 ClickHouse；返回体 `PublishedShadowEvaluation` 在服务端把原始影子 `Recommendation`（含 `id`、`approval`、`rollbackPlan`、`verificationPlan`）收敛成只读的 `ShadowSummary`（仅保留候选供水温度、预计日省电量、预计日省估算电费、能量 P90 不确定度与风险等级），从接口契约上杜绝前端误把影子数字喂给控制指令接口。

### 2. 前置与安全条件、可写设定点门控与人工调温工单

- **真实 6 项前置与安全条件**：`apps/hvac-web/src/features/optimization/optimization-model.ts`（`buildReadinessSummary`）按 `recommendations/latest`、`preparations/latest` 和站点 `asset-model` 组合后端实际检查的 6 项条件——`供水温度范围`、`室内舒适度范围`、`检修与手动锁定`、`实时读数核验`、`负荷预测与分时电价`、`执行通道状态`。首屏浓缩为单行摘要，点击后在同页展开 6 项详情。
- **双路径执行闭环**：
  - 站点登记了启用的 `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` 可写设定点点位时，`OptimizationWorkspace.tsx` 提供「按建议下发」，打开共用的 `<ConfirmCommand>` 提交 `POST /api/v1/commands`，下发后追踪审批或 2 分钟回读状态并链向 `/operations/control`。
  - 站点未登记可写设定点点位（如松雅轩）时，隐去远程下发按钮，改为「创建人工调温工单」（按 `work-order.create` 权限门控），打开 `<CreateWorkOrderDialog>` 提交 `POST /api/v1/sites/{siteId}/work-orders`，自动填入目标供水温度、基准值、预计节省与回退温度，并以 `EXTERNAL` 来源 `optimization-recommendation:<recommendationId>` 去重反查未结束工单（`DRAFT` / `OPEN` / `IN_PROGRESS` / `BLOCKED`），已建工单时直接链向 `/operations/work-center`。

---

## 采纳、改造与拒绝决定

### 1. 影子求解器读取契约与首屏隔离

- **已采纳（OpenEMS 只读模拟查询 + ThingsBoard 计算与控制硬隔离）**：新增独立的 `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`，且网页只在切到「模型与负荷分析」页签（`?view=analysis`）时才发起该请求与未来 24 小时负荷预测请求。首屏「当前建议」（`?view=proposal`）零额外请求，ClickHouse 查询异常也绝不影响已发布优化建议的查看与执行。
- **已拒绝（把影子评估塞进 `recommendations/latest` 或原样透传影子 `Recommendation`）**：塞进同一接口会让 ClickHouse 抖动拖累正式建议读取，也会让首屏背上冷机回归系数与样本筛除统计；原样透传带 `id` 和审批结构的影子建议则容易被误当成可执行建议。

### 2. 前置与安全条件展示边界

- **已采纳（ThingsBoard 就绪状态精确列出真实检查项 + OpenEMS 下发前实时复核说明）**：只映射后端在准备与下发阶段真正检查的 6 项条件，文案统一称为「前置与安全条件」，明确区分优化准备时刻的边界检查与控制指令下发瞬间的实时读数复核。
- **已拒绝（编造硬件联锁或冷机开机台数建议）**：两个求解器都不建模冷机加减载台数、冷冻水泵与冷却塔功率，也不检查防喘振裕度或水流开关硬件联锁；页面绝不编造「开机组合」「倒机顺控核对单」或未测条件的绿勾。

### 3. 无可写设定点站点的人工闭环

- **已改造采纳（ThingsBoard 非控制部件隐去远程下发入口 + 本仓库工单来源引用）**：没有可写供水温度设定点时不再渲染永远禁用的「按建议下发」按钮，而是直接转入人工调温工单，并用 `EXTERNAL` 来源引用 `optimization-recommendation:<id>` 做幂等追踪，无需给 `/operations/control` 增加易失的 URL 草稿参数。
