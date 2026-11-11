# 优化建议工作台：精简决策屏、人工调温工单与冷机 COP 模型对照

问题单 #773（子单 #774、#775、#776；路线图 #757，决策单 #760、#761）。2026-11-11 已实现。

参照项目一手源码比对（OpenEMS `2026.9.0`、ThingsBoard `v4.4`）见 `docs/architecture/optimization-shadow-ui-source-review-2026-11-11.md`。后台定时生成规则见 `docs/specs/scheduled-optimization.md`，影子求解器（冷机 COP 模型）算法见 `docs/specs/chiller-cop-model.md`。

以前优化建议页把温度范围条、回退方案、30 分钟核验说明和计算依据全部平铺在一屏里，文字密；像松雅轩这样还没有可写设定点点位的站点，主按钮「按建议下发」一直灰着点不了，值班人员没法直接把建议转成现场调温工单；后台每次运行算出来的冷机 COP 模型影子评估只写进了 ClickHouse，页面上看不到策略人工系数和站点实测拟合差多少。

## 定下来的做法

| 问题 | 做法 |
| --- | --- |
| 页面怎么分层 | `/energy-analysis/optimization` 用 URL 查询参数 `?view=proposal\|analysis`（默认 `proposal`）分成两个页签：「当前建议」给值班人员做调温决定，「模型与负荷分析」给排查模型的人对照求解器与未来 24 小时负荷预测 |
| 首屏「当前建议」留什么 | 顶部结论区直接给出「将冷冻水供水温度调至 X.X °C」（不改温度时写「保持 X.X °C」）、预计日省电量和预计日省估算电费（P90 不确定度收进 `ⓘ` 提示）、供水与室内温度范围条，下面配一条单行「前置与安全条件」摘要（例如「6/6 项条件就绪 · 可远程下发设定点」或「5/6 项条件就绪 · 仅人工调温工单」）；点开才展开 6 项明细、回退与 30 分钟核验规则 |
| 前置与安全条件有哪些 | 只展示后端真实检查的 6 项，不叫硬件「联锁」，也不编造未测指标的绿勾：① 供水温度范围（策略上下限与单步幅度）、② 室内舒适度范围（预计室温加 P90 不越界）、③ 检修与手动锁定、④ 实时读数核验（供水与室温读数在线、新鲜、质量为好，下发瞬间由控制模块复核）、⑤ 负荷预测与分时电价（覆盖未来完整计价日）、⑥ 执行通道状态（是否登记启用的可写供水温度设定点点位） |
| 怎么执行或交办 | 建议不改供水温度时，两个执行按钮都不显示。需要调温时：① 站点有启用的 `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` 可写设定点点位，显示「按建议下发」，走共用的 `<ConfirmCommand>` 提交控制指令并链向 `/operations/control` 追踪审批或回读；② 站点没有可写设定点点位（如松雅轩），不显示灰掉的下发按钮，改显示「创建人工调温工单」（需 `work-order.create` 权限），打开 `<CreateWorkOrderDialog>` 自动预填目标供水温度、基准值、预计节省与回退温度，并以 `EXTERNAL` 来源 `optimization-recommendation:<id>` 去重——已有未结束工单（`DRAFT`、`OPEN`、`IN_PROGRESS`、`BLOCKED`）时直接显示工单状态和去 `/operations/work-center` 的链接 |
| 影子求解器怎么读 | 新增独立只读接口 `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest`（负责方 `optimization-service`，不改数据库表，直接用 `optimization_service_reader` 按最新已发布建议的 `runId` 查 ClickHouse `analytics.optimization_evaluations` 的 `SHADOW` 记录）。服务端剥离原始影子建议的可执行字段，只返回只读 `summary`、拟合诊断 `fit`、能耗斜率 `energyResponse` 与三档候选 `candidates`。只有打开「模型与负荷分析」页签时才请求该接口和未来 24 小时负荷预测 |
| 「模型与负荷分析」看什么 | 顶部固定提示「影子对照 · 只记录不发布，不作为控制指令下发」，页签内不放任何下发或建工单按钮。依次展示：① 策略求解器（人工设定斜率）与冷机 COP 模型（站点历史拟合斜率）结论并排对照及 `-1 步 / 不变 / +1 步` 三档候选供水温度表（标明越界原因与外推工况，注明站点未登记冷却水设定点暂不开放冷却水温度重置）；② 近 14 天 15 分钟开机桶与筛除原因统计、每台冷机资产的拟合状态（已拟合 / 稳态样本不足 / 拟合未通过）、样本数、功率 CV(RMSE)、平均 COP、观测供水温度范围与斜率，区分「尚无影子评估记录」（404）和「暂无可用冷机拟合模型」（`NO_USABLE_MODEL`）；③ 复用站点负荷预测接口画未来 24 小时逐时预测曲线与上下界，并链向 `/energy-analysis/load` |
| 不做 | 不编造冷机开机台数组合或倒机顺控核对单，不编造防喘振、水流开关等未测绿勾，不新增数据库迁移，不发布或自动下发影子求解器结果 |

## 验收与测试接缝

1. **网关契约与优化服务只读接缝（契约检查 + Go 测试）**：
   - `GET /api/v1/sites/{siteId}/optimization/shadow-evaluations/latest` 登记进 OpenAPI 与 `route-ownership.v1.json`，校验站点分析读权限；按 `(tenant_id, site_id, optimization_run_id, quality = 'SHADOW')` 查询 ClickHouse，返回体不含原始可执行 `recommendation` 字段；无记录回 `404`，ClickHouse 故障回 `502` 且不影响 `recommendations/latest`。
2. **前端优化模型纯函数接缝（`npm run web:model:test`）**：
   - 验证 6 项前置与安全条件状态推导、人工调温工单预填草稿与未结束工单去重匹配、影子评估各 `outcome`（`FEASIBLE`、`NO_USABLE_MODEL` 等）及单台冷机状态原因与设备名映射。
3. **浏览器 1440px 端到端验收（`npm run web:e2e`）**：
   - 验证默认首屏不请求影子接口、6 项条件按需展开、有可写点位走控制指令确认、无可写点位走人工调温工单创建与去重追踪、切到「模型与负荷分析」按需加载三档候选表与冷机拟合明细且无任何执行按钮。
