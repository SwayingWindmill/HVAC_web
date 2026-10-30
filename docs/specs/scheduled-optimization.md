# 优化建议定时运行

问题单 #462 剩下的部分。2026-10-13 和用户确认（全部按建议），已上线。

以前预测和故障诊断已经按站点定时运行，优化建议却只能在页面上手动生成，没人点时，总览和优化建议页上的建议会一直停在旧的状态。

## 定下来的做法

| 问题 | 做法 |
| --- | --- |
| 范围 | 补上优化建议的定时运行，完成后关闭 #462 |
| 怎么做 | 调度器发来的 `OPTIMIZATION_RUN` 不指定是哪次运行；优化服务领到后，按站点当前的状态准备好输入并固定下来、创建一次运行，再照常执行，结果和手动生成一样。手动生成的入口保留 |
| 多久一次 | 每个站点每小时一次，第 20 分钟（错开第 7 分钟的负荷预测） |
| 用途 | 只更新「最新建议」并记入历史，不自动下发控制指令，不发通知；页面标明「定时生成」或「手动生成」 |

## 怎么运作

1. 每个站点一条调度定义：`OPTIMIZATION_RUN`，cron `20 * * * *`（UTC），参数 `{}`；种子数据写入时把下次触发时间设为当下，所以本地重建后会马上运行一次。
2. 优化服务领取到带调度定义的任务时，把它当作一次「调度触发」：按站点准备运行（触发方式 `SCHEDULE`），排入该运行自己的任务后，调度触发任务即完成。
3. 准备失败时（最常见的是站点输入不全：最近一天的用电与电费、最新负荷预测、实时供水与室内温度），这次调度触发失败且不重试，下一个小时重新读取。失败原因写在调度触发任务的 `error_code` 里，说明缺的是哪一项（`services/optimization-service/internal/optimization/preparation_reason.go`）：`SITE_NOT_VISIBLE`、`POLICY_MISSING`（没有或不止一份已发布策略）、`POLICY_UNSUPPORTED`、`MAINTENANCE_ACTIVE`、`TOPOLOGY_MISSING`、`LOAD_FORECAST_MISSING`、`TARIFF_MISSING`、`MODEL_DEPLOYMENT_MISSING`、`PRICED_DAY_MISSING`、`SUPPLY_TEMPERATURE_MISSING`、`ZONE_TEMPERATURE_MISSING`、`BASELINE_OUTSIDE_LIMITS`（当前供水或室内温度已在策略范围之外）、`INPUTS_INVALID`、`INPUTS_UNREADABLE`；不属于这些的写 `PREPARATION_FAILED`。
4. 站点最近一次调度触发怎么样了，经网关 `GET /api/v1/sites/{siteId}/optimization/preparations/latest` 读（优化服务 `GET /v1/sites/{siteId}/optimization/preparations/latest`）：`status` 是 `PREPARED`、`FAILED` 或 `PENDING`，失败时 `reason` 是上面的原因；站点没有触发过时返回 404。优化建议页没有新建议时用它说明原因：没有建议时写「最近一次定时计算没有生成建议：读不到可用的实时室内温度」之类，站点没有触发过时写「站点还没有配置定时优化」；已有建议但之后一次定时计算失败时，在建议上方提示下面是上一条建议。改这项之前记的旧代码（`OPTIMIZATION_INPUT_UNAVAILABLE` 等）读出来是 `PREPARATION_FAILED`。
5. 已发布的建议带 `trigger`：`MANUAL` 或 `SCHEDULE`，取自该运行任务的触发方式。
6. 已发布的建议还带 `responseModel`：这次运行固定下来的策略响应系数（供水温度每升高 1 °C 冷站日用电和室内温度怎么变，以及两者的不确定性）。这些系数由人在优化策略里填写，不是从站点数据拟合的，页面「计算依据」里标「人工设定」。
7. 「最新建议」是最后准备的那次已发布运行（按运行创建时间），排在队里的旧运行晚完成也不会盖掉它。
