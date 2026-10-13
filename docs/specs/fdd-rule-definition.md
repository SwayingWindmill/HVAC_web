# 故障诊断规则归 FDD 管

HANDOFF 小事项「把 FDD 低温差阈值从调度参数移到 owner 持有的规则定义」。2026-10-13 与用户确认（全部按建议）。本文是实现契约。

此前「冷冻水低温差」规则的版本号（`chilled-water-low-delta-t/v1`）、阈值（3.5 K）和窗口（15 分钟）在前端诊断页和本地种子的调度参数里各写一份；手动诊断时由页面把阈值传给 FDD。FDD 自己不持有规则。

## 决定

| 决策 | 结论 |
| --- | --- |
| 存放 | FDD 新建规则版本表，按租户保存「冷冻水低温差」规则：阈值、窗口、版本号；同一租户同一时间只有一个生效版本；按站点覆盖暂不做 |
| 修改 | 版本不可修改，调整参数即发布新版本；已有诊断结论引用当时的版本；本轮不做编辑界面，由种子或运维脚本发布 |
| 诊断请求 | 定时诊断与手动诊断都只说明诊断哪台设备（资产、设备），阈值与窗口由 FDD 按当前生效版本决定；删除前端与种子里的规则参数 |
| 诊断页 | FDD 新增读取当前规则的接口，诊断页据此显示阈值、窗口与版本；读取失败显示「规则暂不可用」 |
| 自动化规则 | FDD 诊断规则不并入系统管理的「自动化规则」，两者属于不同 owner |

## 行为

1. `core_registry.fdd_rule_revisions`：`id`、`tenant_id`、`rule_code`（`CHILLED_WATER_LOW_DELTA_T`）、`revision`、`minimum_delta_tc`（> 0）、`window_minutes`（1–1440）、`effective_from`、`created_at`；`(tenant_id, rule_code, revision)` 唯一。`fdd_runtime` 只能读，不能写或改。
2. 当前规则 = 该租户该规则 `effective_from` 不晚于诊断时刻的最高版本。没有时诊断失败（定时诊断不重试），读取接口返回 404。
3. 诊断窗口 = 诊断时刻之前的 `window_minutes`；定时诊断的诊断时刻是任务的计划时间，手动诊断是 FDD 收到请求的时间。
4. 诊断结论记录所用版本的 `id` 与阈值；「同一问题 24 小时只记一次」按版本判断。
5. 网关：`POST /api/v1/sites/{siteId}/fdd/evaluate/low-delta-t` 的请求体只有 `assetId`、`deviceId`；新增 `GET /api/v1/sites/{siteId}/fdd/rules/low-delta-t`，返回 `revisionId`、`revision`、`minimumDeltaTC`、`windowMinutes`、`effectiveFrom`，需要站点读权限。
