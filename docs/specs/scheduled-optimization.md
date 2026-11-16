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
3. 准备失败时（最常见的是站点输入不全：最近一天的用电与电费、最新负荷预测、实时供水与室内温度），这次调度触发失败且不重试，下一个小时重新读取。失败原因写在调度触发任务的 `error_code` 里，说明缺的是哪一项（`services/optimization-service/internal/optimization/preparation_reason.go`）：`SITE_NOT_VISIBLE`、`POLICY_MISSING`（没有或不止一份已发布策略）、`POLICY_UNSUPPORTED`、`MAINTENANCE_ACTIVE`（站点控制退出模式为 `MANUAL_OVERRIDE` 或 `SAFE_SHUTDOWN_EXIT`、存在未解锁的资产控制锁、策略处于检修或锁定状态，或站点本地日期处于已生效的 `MAINTENANCE_WINDOW` 或 `HOLIDAY_SHUTDOWN` 运行例外区间内）、`TOPOLOGY_MISSING`、`LOAD_FORECAST_MISSING`、`TARIFF_MISSING`、`MODEL_DEPLOYMENT_MISSING`、`PRICED_DAY_MISSING`、`SUPPLY_TEMPERATURE_MISSING`、`ZONE_TEMPERATURE_MISSING`、`BASELINE_OUTSIDE_LIMITS`（当前供水或室内温度已在策略范围之外）、`INPUTS_INVALID`、`INPUTS_UNREADABLE`；不属于这些的写 `PREPARATION_FAILED`。
4. 站点最近一次调度触发怎么样了，经网关 `GET /api/v1/sites/{siteId}/optimization/preparations/latest` 读（优化服务 `GET /v1/sites/{siteId}/optimization/preparations/latest`）：`status` 是 `PREPARED`、`FAILED` 或 `PENDING`，失败时 `reason` 是上面的原因；站点没有触发过时返回 404。优化建议页没有新建议时用它说明原因：没有建议时写「最近一次定时计算没有生成建议：读不到可用的实时室内温度」之类，站点没有触发过时写「站点还没有配置定时优化」；已有建议但之后一次定时计算失败时，在建议上方提示下面是上一条建议。改这项之前记的旧代码（`OPTIMIZATION_INPUT_UNAVAILABLE` 等）读出来是 `PREPARATION_FAILED`。
5. 已发布的建议带 `trigger`：`MANUAL` 或 `SCHEDULE`，取自该运行任务的触发方式。
6. 已发布的建议还带 `responseModel`：这次运行固定下来的策略响应系数（供水温度每升高 1 °C 冷站日用电和室内温度怎么变，以及两者的不确定性）。这些系数由人在优化策略里填写，不是从站点数据拟合的，页面「计算依据」里标「人工设定」。
7. 「最新建议」是最后准备的那次已发布运行（按运行创建时间），排在队里的旧运行晚完成也不会盖掉它。
8. 当站点控制模式退出自动控制（`MANUAL_OVERRIDE` 或 `SAFE_SHUTDOWN_EXIT`）、站点下任一资产存在未解锁的控制锁，或站点本地日历日命中已生效（`APPROVED`）的 `MAINTENANCE_WINDOW`、`HOLIDAY_SHUTDOWN` 运行例外（日期首尾都算，例如 10 月 13 日到 16 日就是四整天）时，无论是每小时定时触发还是页面手动发起的设定点优化都会在准备阶段暂停并返回 `MAINTENANCE_ACTIVE`，同时撤回该站点下尚未确认的草稿建议。

## 站点自动控制租约与 30 分钟回滚监护

默认情况下，所有站点保持人工确认模式（`MANUAL_CONFIRM`）：定时与手动运行只产生并保存优化建议，不自动下发控制指令。只有由有权限的操作员通过两步验证显式开通自动控制租约后，站点才允许在严格边界内自动执行优化建议：

1. **租约开通、查询与随时收回**：
   - 租约记录保存在 `core_registry.site_auto_control_leases`，同一站点同一时间最多只允许一条 `ACTIVE` 租约，最长有效期不超过 24 小时。
   - 查询当前租约调用 `GET /api/v1/sites/{siteId}/optimization/auto-control-lease`（没有有效租约时返回 `effectiveMode: MANUAL_CONFIRM`）；开通调用 `PUT /api/v1/sites/{siteId}/optimization/auto-control-lease`（需要 `schedule.manage` 权限即运维主管或管理员角色及 6 位认证器验证码，必须设定允许的能力白名单、`maxSetpointStepC` 上限 2 °C、`supplyTempMinC` 到 `supplyTempMaxC`、`zoneTempMaxC`、`minRunningChillers`、`maxCommandsPerHour` 1 到 6 次以及不超 24 小时的过期时间）；收回调用 `POST /api/v1/sites/{siteId}/optimization/auto-control-lease/revoke`（同样需要 `schedule.manage` 权限），立即把状态置为 `REVOKED` 恢复人工确认。
2. **建议 65 分钟时效与下发前实时复核**：
   - 新生成的优化建议默认附带 65 分钟有效期（`validUntil`），过期后既不能手动也不能自动下发指令。
   - 优化运行发布建议后，若站点存在 `ACTIVE` 且未过期租约，优化服务在下发前会执行实时复核：确认站点仍处于 `REMOTE_AUTO` 且无资产控制锁、近 1 小时自动指令数未达上限、供水温度与全部室内温度及冷机功率测点健康且未超时、实测供水温度与建议基线 `baseline.supplyTempC` 相差不超过 `0.5 °C`、各冷机实测功率不超过额定功率 `0.90` 倍、目标设定点与当前实测温差均在租约边界之内。
   - 复核通过后，先记录 `APPROVED` 与实时复核凭证 `currentStateRevalidation`，再向 IAM `POST /internal/v1/command/workload-decision` 申请携带 `WorkloadSPIFFE`、`LeaseID`、`ArmedBy` 溯源且有效期不超过 30 秒的一次性授权（禁止转授），通过 `POST /api/v1/sites/{siteId}/optimization/auto-commands`（仅限经过验证的内部优化工作负载直接调用，拒绝浏览器会话与反代入口）以幂等键 `auto-opt:<recommendationId>` 提交指令。
3. **30 分钟执行监护、自动回滚与断路跳闸**：
   - 每次自动下发指令后立即在 `core_registry.optimization_execution_monitors` 写入一条 `OBSERVING` 监护记录（观察窗口 30 分钟），可通过 `GET /api/v1/sites/{siteId}/optimization/execution-monitors` 查看全部监护结果。
   - 优化服务每分钟巡检处于 `OBSERVING` 的监护记录：一旦发现实测室内温度高于 `zoneTempMaxC`、实测供水温度高于 `supplyTempMaxC`、任一运行冷机功率超过额定功率 `0.90` 倍，或关键遥测持续不可用超过 5 分钟，立即下发 `auto-rollback:<monitorId>` 指令恢复原供水设定点 `restoreSupplyTempC`（支持在断采场景下执行回滚），并把租约原子置为 `TRIPPED` 跳闸锁定（防止继续自动下发）、写入 `AUTO_CONTROL_LEASE_TRIPPED` 事件通知值班人员；若 30 分钟窗口内始终安全达标，监护状态收敛为 `VERIFIED`。
