# Energy optimization

The `/energy-analysis/optimization` route opens on `当前建议` with two sections: `减台与辅机停机` (stop a chiller the others can cover, or a pump or tower still running for a stopped chiller, each confirmed by a person) and `供水温度优化` (chilled-water supply temperature reset recommendations, dispatched as setpoint commands or manual adjustment work orders). `模型与负荷分析` inspects chiller COP shadow evaluations.

## Sub-features

- `optimization-staging` reads `GET /api/v1/sites/{siteId}/optimization/staging-recommendations/latest` (`404` → `这个站点还没有做过减台评估`). The `减台与辅机停机` region shows the totals strip, a row per recommendation (`停 …`, `−N kW`, `本班约 N kWh`, capacity bar for stage-down, idle minutes for auxiliaries) and `暂停 N 条` with plain reasons from the diagnostic codes; the service's own sentences and enum names never render.
- `optimization-staging-confirm` sends `确认停机…` through the shared high-risk confirm (`动态口令`, site check, `提交停机`) with the recommendation's `suggestedCommandPointId`; once submitted the row shows `已提交 · …` and links `在运行控制查看 ›`. Under `人工接管` / `安全退出` the region shows `…中，停机建议已暂停` and the buttons are disabled.
- `optimization-manual-run-and-guard` explains missing readiness inputs (`502` declined run `alert`) when prerequisites are unready, and publishes `当前建议` (`7.1 → 8.1 °C`, `预计日节电 60 kWh`, `预计日节省 ¥51`) when ready.
- `optimization-progressive-disclosure` collapses `前置与安全条件` behind a summary toggle and hides engineering sensitivity details until expanded.
- `optimization-dispatch-vs-manual-fallback` sends a confirmed setpoint command when a write point exists, or creates a manual work order when `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` is absent from `assetModel`.

## How to get to it (user POV)

- Click `节能` → `优化建议` in the sidebar, or open `/energy-analysis/optimization?site=<SITE_ID>`.

## Driving it with Playwright

Preconditions:

- Tier 1: run `CI=1 PLAYWRIGHT_PORT=5194 npm run web:e2e -- --workers=1 e2e/optimization.spec.ts`. Set `gateway.staging` to an evaluation to exercise the staging section; `gateway.controlState.controlMode` pauses it.

- **Staging.** With an idle-tower recommendation for `CT-01` and a `ASSET_CONTROL_LOCKED` diagnostic for `CWP-01`, assert the row, the paused line and that no `MANUAL` text renders; confirm with a code and assert the submitted body carries `verificationCode`.
- **Supply temperature.** Trigger `生成建议` without `nextRecommendation` to assert the prerequisite `alert`; then set `gateway.nextRecommendation = fixture('optimization-latest.json')`, assert `当前建议`, `预计效果` and collapsed `前置与安全条件：6/6 就绪`, expand and check the shadow tab.
- **Proof.** Capture pass output and `1440×900` light/dark screenshots under `.scratch/verify/<slug>/`.

## Gotchas

- Songyaxuan has no writable `SET_CHILLED_WATER_TEMPERATURE_SETPOINT` and supply-temperature optimization is not enabled there, so its supply section falls back to the empty state and manual work orders while stage-down and idle-auxiliary stops work independently.
