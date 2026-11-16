# Energy optimization

The `/energy-analysis/optimization` route opens on `当前建议` with two sections: `减台与辅机停机` (stop a chiller the others can cover, every chiller during the schedule's off hours, or a pump or tower still running for a stopped chiller, each confirmed by a person) and `供水温度优化` (chilled-water supply temperature reset recommendations, dispatched as setpoint commands or manual adjustment work orders). `模型与负荷分析` inspects chiller COP shadow evaluations.

## Sub-features

- `optimization-staging` reads `GET /api/v1/sites/{siteId}/optimization/staging-recommendations/latest` (`404` → `这个站点还没有做过减台评估`). The `减台与辅机停机` region shows the totals strip, a row per recommendation (`停 …`, `−N kW`, `本班约 N kWh`, capacity bar for stage-down, idle minutes for auxiliaries) and `暂停 N 条` with plain reasons from the diagnostic codes; the service's own sentences and enum names never render.
- `optimization-staging-confirm` sends `确认停机…` through the shared high-risk confirm (`动态口令`, site check, `提交停机`) with the recommendation's `suggestedCommandPointId`; once submitted the row shows `已提交 · …` and links `在运行控制查看 ›`. Under `人工接管` / `安全退出` the region shows `…中，停机建议已暂停` and the buttons are disabled.
- `optimization-manual-run-and-guard` explains missing readiness inputs (`502` declined run `alert`) when prerequisites are unready, and publishes `当前建议` (`7.1 → 8.1 °C`, `预计日节电 60 kWh`, `预计日节省 ¥51`) when ready.
- `optimization-progressive-disclosure` collapses `前置与安全条件` behind a summary toggle and hides engineering sensitivity details until expanded.
- `optimization-readiness-reasons` reads `GET /api/v1/sites/{siteId}/optimization/preparations/latest` (`OptimizationPreparation`), showing `响应系数还没标定` and `共享租户策略还没确认` when `responseCoefficient.status === 'NOT_CALIBRATED'` or `tenantStrategy.status === 'PENDING'`, while showing `站点还没有配置定时优化，建议只能手动生成。` when `NOT_SCHEDULED` with `CALIBRATED` and `CONFIRMED`.
- `optimization-auto-control-lease` reads `GET /api/v1/sites/{siteId}/optimization/auto-control-lease` and gates `开启自动控制租约…` (`PUT` with bounds, `reason`, `expectedRevision`, `verificationCode`, plus `ProblemDetails.reason` guidance) and `撤回租约` (`POST .../revoke`) behind `schedule.manage`. Active `AUTO_WITH_LEASE` updates the recommendation dispatch note from manual confirmation to auto-control execution within the leased window.
- `optimization-execution-monitors` reads `GET /api/v1/sites/{siteId}/optimization/execution-monitors` and renders `30 分钟执行监护` (`DataTableBlock`), translating trip reasons (`telemetry_unhealthy_over_5m`, `supply_temperature_exceeded_max`, `zone_temperature_exceeded_max`, `chiller_power_exceeded_90pct`) into Chinese and linking each `commandIntentId` to `/operations/control`.
- `optimization-dispatch-vs-manual-fallback` sends a confirmed setpoint command when a write point exists, or creates a manual work order when `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` is absent from `assetModel`.

## How to get to it (user POV)

- Click `节能` → `优化建议` in the sidebar, or open `/energy-analysis/optimization?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/optimization.spec.ts`. Set `gateway.staging` to an evaluation to exercise the staging section; `gateway.controlState.controlMode` pauses it.

- **Staging.** With an idle-tower recommendation for `CT-01` and a `ASSET_CONTROL_LOCKED` diagnostic for `CWP-01`, assert the row, the paused line and that no `MANUAL` text renders; confirm with a code and assert the submitted body carries `verificationCode`.
- **Supply temperature.** Trigger `生成建议` without `nextRecommendation` to assert the prerequisite `alert`; then set `gateway.nextRecommendation = fixture('optimization-latest.json')`, assert `当前建议`, `预计效果` and collapsed `前置与安全条件：6/6 就绪`, expand and check the shadow tab.
- **Readiness gaps, auto-control lease, and monitors.** Set `gateway.optimizationPreparation` to `NOT_CALIBRATED` and `PENDING` to verify `响应系数还没标定` and `共享租户策略还没确认` appear while `减台与辅机停机` stays active; verify lease arming (validation bounds, `MFA_NOT_CONFIGURED` link to `/system?tab=users`, TOTP `246810` to `AUTO_WITH_LEASE`), `30 分钟执行监护` table (`DataTableBlock`) with translated Chinese trip reasons, and lease revocation back to `MANUAL_CONFIRM`.
- **Proof.** Capture pass output and `1440×900` light/dark screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- Songyaxuan has no writable `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` and supply-temperature optimization is not enabled there, so its supply section falls back to the empty state and manual work orders while stage-down and idle-auxiliary stops work independently.
