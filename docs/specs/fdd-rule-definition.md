# 故障诊断规则由故障诊断服务（FDD）保存

来自当时 HANDOFF 里的一件小事：把低温差诊断的门槛从定时任务的参数里挪到 FDD 自己保存的规则里。2026-10-13 和用户确认（全部按建议），已上线。

以前「冷冻水低温差」规则的版本号（`chilled-water-low-delta-t/v1`）、阈值（3.5 K）和窗口（15 分钟）在前端诊断页和本地种子的调度参数里各写一份；手动诊断时由页面把阈值传给 FDD。FDD 自己不持有规则。

## 定下来的做法

| 问题 | 做法 |
| --- | --- |
| 存在哪 | FDD 的规则版本表，按租户保存「冷冻水低温差」规则：门槛、时间窗口、版本号；同一租户同一时间只有一个生效的版本；按站点单独设置暂不做 |
| 怎么改 | 版本发布后不能改，调整参数就发布一个新版本；已有的诊断结论引用当时的版本；没有编辑界面，用种子数据或 `scripts/publish-fdd-rule-revision.mjs` 发布 |
| 诊断请求 | 定时诊断与手动诊断都只说明诊断哪台设备（资产、设备），门槛和时间窗口由 FDD 按当前生效的版本决定；前端和种子里的规则参数已删除 |
| 诊断页 | FDD 新增读取当前规则的接口，诊断页据此显示门槛、时间窗口和版本；读取失败显示「规则暂不可用」 |

## 怎么运作

1. `core_registry.fdd_rule_revisions`：`id`、`tenant_id`、`rule_code`（`CHILLED_WATER_LOW_DELTA_T`）、`revision`、`minimum_delta_tc`（> 0）、`window_minutes`（1–1440）、`effective_from`、`created_at`；`(tenant_id, rule_code, revision)` 唯一。`fdd_runtime` 只能读，不能写或改。
2. 当前规则 = 该租户该规则 `effective_from` 不晚于诊断时刻的最高版本。没有时诊断失败（定时诊断不重试），读取接口返回 404。
3. 诊断窗口 = 诊断时刻之前的 `window_minutes`；定时诊断的诊断时刻是任务的计划时间，手动诊断是 FDD 收到请求的时间。
4. 诊断结论记下所用版本的 `id` 和门槛；「同一问题 24 小时只记一次」按版本判断。
5. 网关：`POST /api/v1/sites/{siteId}/fdd/evaluate/low-delta-t` 的请求体只有 `assetId`、`deviceId`；新增 `GET /api/v1/sites/{siteId}/fdd/rules/low-delta-t`，返回 `revisionId`、`revision`、`minimumDeltaTC`、`windowMinutes`、`operatingGates`（见下面「工况门控」）、`effectiveFrom`，需要站点读权限。

## 工况门控（2026-10-30）

以前 FDD 只看窗口里最后一对供回水温度：冷站停着时供回水温度几乎一样，温差接近 0，夜里也被当成「温差低」。松雅轩 2026-10-16 至 10-30 的 1344 个 15 分钟窗口里，旧做法 1323 个判成低温差，其中一大半是冷机停着。2026-10-30 和用户确认的默认值：冷机功率高于额定的 10%、负荷率不低于 15%、满足条件的时间不少于窗口的 70%。

**门控和比例是规则版本的一部分**：`fdd_rule_revisions.operating_gates`（jsonb，迁移 `033-fdd-operating-gates.sql`）。迁移给已有版本填上默认值，阈值不变（松雅轩仍是 3.5 K / 15 分钟）；以后改门控也要发布新版本（`--operating-gates '<json>'`，不写就沿用上一版）。

```json
{"minimumOperatingShare":0.7,"minimumLowShare":0.7,"temperatureHoldMinutes":2,"gateHoldMinutes":10,
 "chillerRunning":{"minimumRatedPowerFraction":0.1},"loadRatio":{"minimum":0.15},"chilledWaterPumpRunning":{}}
```

**按分钟统计整个窗口**：FDD 从遥测历史读窗口里每台相关设备每个点位每分钟最后一条读数（冷量表的供水温度、回水温度、瞬时冷量；站点冷机和冷冻水泵绑定设备的 `power_total`、`run_state`）。某一分钟没有读数时沿用之前最近的一条：温度最多 2 分钟（`temperatureHoldMinutes`），功率和运行状态最多 10 分钟（`gateHoldMinutes`，冷机电表 5 分钟一条）。

| 门控 | 这一分钟算通过 | 什么时候不用（结果里写明原因） |
| --- | --- | --- |
| 冷机开机（`chillerRunning`） | 至少一台冷机在运行：登记了额定功率的冷机看功率是否高于额定的 10%；没登记额定功率的看运行状态 `RUNNING` | 规则里没有（`NOT_IN_RULE`）；站点没有冷机绑定到设备（`NO_CHILLER_MAPPED`） |
| 负荷率（`loadRatio`） | 冷量表瞬时冷量 ÷ 运行中冷机额定冷量之和 ≥ 15% | 冷机开机门控没用上；有冷机没登记额定冷量（`NO_RATED_CAPACITY`）；窗口里没有瞬时冷量读数（`NO_COOLING_LOAD_READING`） |
| 冷冻水泵运行（`chilledWaterPumpRunning`，可选） | 至少一台冷冻水泵运行状态 `RUNNING` | 站点没有冷冻水泵绑定到设备（`NO_PUMP_MAPPED`）——只有登记了水泵并绑定了它的运行状态点位才会用上 |

一分钟「在运行」= 供回水温度都有，并且用上的门控都通过。判断：

1. 窗口里一分钟供回水温度都没有：和以前一样诊断失败（`ErrNoLoopEvidence`）。
2. 运行分钟不到窗口的 70%（`minimumOperatingShare`）：结果 `NOT_OPERATING`，不记诊断结论。
3. 运行分钟里温差低于阈值的分钟达到 70%（`minimumLowShare`）：低温差，按原来的 24 小时规则记 `FINDING` 或报 `ONGOING`；否则 `CLEAR`。
4. 诊断结论的测量值是运行分钟温差的平均值，依据是这些低温差分钟的供回水观测 ID；结论的窗口就是规则窗口。
5. 诊断结果多了 `window`：窗口分钟数、有温度的分钟、运行分钟、低温差分钟、两个比例、平均温差，以及每个门控用没用上、为什么没用、按什么判断（`RATED_POWER` / `RUN_STATE`）、通过了几分钟。诊断页按它显示「最近 15 分钟里冷站运行 N 分钟」或「未运行」。

**FDD 读的登记**：迁移 033 让 `fdd_runtime` 能按租户和站点读 `assets`（`id`、`asset_type`、`status`、`rated_power`、`rated_capacity`）和 `device_bindings`（设备、资产、状态、有效期）。只看在用的冷机（`CHILLER`）和冷冻水泵（`CHILLED_WATER_PUMP`）资产和它们在诊断时刻有效的绑定，不分绑定角色；一个站点最多 200 条。

**松雅轩现在**：四台冷机都绑定了各自的电表，但都没登记额定功率和额定冷量，所以冷机开机按运行状态判断，负荷率门控用不上；冷冻水泵的运行状态点位（`chwp-main`）没有绑定到任何水泵资产，水泵门控也用不上。登记冷机的额定功率和额定冷量后两个门控自动按功率和负荷率判断，不用改规则。

