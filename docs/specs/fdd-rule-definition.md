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
5. 网关：`POST /api/v1/sites/{siteId}/fdd/evaluate/low-delta-t` 的请求体只有 `assetId`、`deviceId`；新增 `GET /api/v1/sites/{siteId}/fdd/rules/low-delta-t`，返回 `revisionId`、`revision`、`minimumDeltaTC`、`windowMinutes`、`effectiveFrom`，需要站点读权限。
